package steg

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"
	"runtime"
	"sync"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
)

// payloadStreamOffset is the stream byte offset at which the padded payload
// begins: 16 bytes of plaintext salt followed by the 4-byte container length.
const payloadStreamOffset = 20

// firstChunkShift returns how many extra bytes the first worker chunk must carry
// so that every subsequent chunk boundary lands on a pixel boundary.
//
// Workers read-modify-write whole pixels: a cursor loads a pixel with img.At(),
// updates the bits it owns, and stores it back with img.Set(). If a boundary
// falls mid-pixel, the workers on either side both load, modify, and store that
// pixel, and whichever stores last silently discards the other's bits — the
// image then fails MAC verification on decode. A shared mutex does not help,
// because each individual At and Set is already serialised; it is the
// load-modify-store sequence that must not interleave.
//
// Chunk sizes are already multiples of lcm(8, bitsPerPixel)/8 bytes, so the only
// misalignment comes from the header: the payload starts at bit
// payloadStreamOffset*8 = 160, and 160 is not a multiple of bitsPerPixel
// whenever bitsPerPixel is a multiple of 3 (the default is 3). Shifting the
// first chunk by the smallest such remainder realigns every later boundary.
func firstChunkShift(bitsPerPixel int) int64 {
	const headerBits = payloadStreamOffset * 8
	for r := int64(0); r < int64(bitsPerPixel); r++ {
		if (headerBits+r*8)%int64(bitsPerPixel) == 0 {
			return r
		}
	}
	return 0 // unreachable: gcd(8, bitsPerPixel) always divides 160
}

type encJob struct {
	streamOffset int64
	data         []byte
}

type decJob struct {
	streamOffset int64
	dest         []byte
}

// newWorkerStack creates a per-worker cipher+cursor stack. Each worker has its
// own independent cipher and cursor state. imgMu, when non-nil, is shared
// across concurrent workers to serialise img.At()/img.Set() calls.
func newWorkerStack(m draw.Image, nonce uint32, encKey []byte,
	points []image.Point, bitsPerChannel, channels int, imgMu *sync.Mutex) (io.ReadWriteSeeker, error) {
	opts := []cursors.Option{
		cursors.WithSharedPoints(points),
		cursors.WithBitsPerChannel(bitsPerChannel),
	}
	if channels >= 2 {
		opts = append(opts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		opts = append(opts, cursors.UseBlueBit())
	}
	if imgMu != nil {
		opts = append(opts, cursors.WithImageMutex(imgMu))
	}
	cur := cursors.NewRNGCursor(m, opts...)
	c, err := cipher.NewCipher(nonce, encKey)
	if err != nil {
		return nil, err
	}
	return cursors.CursorAdapter(cursors.CipherMiddleware(cur, c)), nil
}

// EncodeParallel encodes r into m using a parallel worker pool.
// The on-image layout is identical to Encode, so DecodeParallel and Decode
// can both decode images written by EncodeParallel (and vice-versa).
func EncodeParallel(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
	seed, err := deriveSeed(pass)
	if err != nil {
		return err
	}

	realPayload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	padded, err := buildPaddedPayload(m, realPayload, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Write the plaintext salt (16 bytes) to image bytes 0–15 before workers start.
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	var randomSalt [16]byte
	if _, err = rand.Read(randomSalt[:]); err != nil {
		return err
	}
	saltAdapter := cursors.CursorAdapter(rawCur)
	if _, err = saltAdapter.Write(randomSalt[:]); err != nil {
		return err
	}
	rawCur.Flush()

	// Derive main keys from the random salt.
	encKey, macKey, payloadNonce, err := deriveMainKeys(pass, randomSalt[:])
	if err != nil {
		return err
	}

	alignment := lcmBytes(8, channels*bitsPerChannel)
	chunkSize := alignment * 1024

	// Pre-compute HMAC over the full padded block before dispatching workers.
	hashFn := hmac.New(sha256.New, macKey)
	hashFn.Write(padded)
	tag := hashFn.Sum(nil)

	// Shared mutex serialises img.At()/img.Set() across concurrent workers.
	imgMu := &sync.Mutex{}

	numWorkers := runtime.GOMAXPROCS(0)
	jobChan := make(chan encJob, numWorkers*2)
	errChan := make(chan error, numWorkers)

	// abort is closed by the first worker to fail, so the dispatch loop below
	// cannot block forever writing to jobChan after every worker has exited.
	abort := make(chan struct{})
	var abortOnce sync.Once
	fail := func(err error) {
		errChan <- err
		abortOnce.Do(func() { close(abort) })
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter, werr := newWorkerStack(m, payloadNonce, encKey, points, bitsPerChannel, channels, imgMu)
			if werr != nil {
				fail(werr)
				return
			}
			for job := range jobChan {
				if _, serr := adapter.Seek(job.streamOffset, io.SeekStart); serr != nil {
					fail(serr)
					return
				}
				if _, werr2 := adapter.Write(job.data); werr2 != nil {
					fail(werr2)
					return
				}
			}
			if _, ferr := adapter.Seek(0, io.SeekStart); ferr != nil {
				fail(ferr)
			}
		}()
	}

	// Dispatch the padded block in pixel-aligned chunks, starting at the byte
	// where the payload begins. The first chunk absorbs the header's
	// misalignment so no two workers ever share a pixel.
	totalLen := int64(len(padded))
	shift := firstChunkShift(channels * bitsPerChannel)
	var offset int64
dispatch:
	for offset < totalLen {
		size := int64(chunkSize)
		if offset == 0 {
			size += shift
		}
		if offset+size > totalLen {
			size = totalLen - offset
		}
		chunk := make([]byte, size)
		copy(chunk, padded[offset:offset+size])
		select {
		case jobChan <- encJob{streamOffset: payloadStreamOffset + offset, data: chunk}:
			offset += size
		case <-abort:
			break dispatch
		}
	}
	close(jobChan)
	wg.Wait()

	select {
	case werr := <-errChan:
		return werr
	default:
	}

	// Post-parallel sequential writes: container length field (byte 16) and HMAC.
	// Both run after wg.Wait(), so although each may share a pixel with the
	// payload region, no concurrent writer can clobber it.
	// Workers use payloadNonce; the salt region (bytes 0–15) is already written.
	seqAdapter, err := newWorkerStack(m, payloadNonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(16, io.SeekStart); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(totalLen))
	if _, err = seqAdapter.Write(lenBuf); err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(payloadStreamOffset+totalLen, io.SeekStart); err != nil {
		return err
	}
	if _, err = seqAdapter.Write(tag); err != nil {
		return err
	}
	_, err = seqAdapter.Seek(0, io.SeekStart)
	return err
}

// DecodeParallel decodes a message from m using a parallel worker pool.
// Images encoded by Encode (sequential) are fully compatible.
func DecodeParallel(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	seed, err := deriveSeed(pass)
	if err != nil {
		return nil, err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Read the 16-byte random salt from image bytes 0–15 (stored in plaintext).
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	saltAdapter := cursors.CursorAdapter(rawCur)
	var randomSalt [16]byte
	if _, err = io.ReadFull(saltAdapter, randomSalt[:]); err != nil {
		return nil, err
	}

	// Derive main keys from the recovered salt.
	encKey, macKey, payloadNonce, err := deriveMainKeys(pass, randomSalt[:])
	if err != nil {
		return nil, err
	}

	// Read the 4-byte container length field at byte 16 (after the plaintext salt).
	seqAdapter, err := newWorkerStack(m, payloadNonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return nil, err
	}
	if _, err = seqAdapter.Seek(16, io.SeekStart); err != nil {
		return nil, err
	}
	lenBuf := make([]byte, 4)
	if _, err = io.ReadFull(seqAdapter, lenBuf); err != nil {
		return nil, fmt.Errorf("failed to read payload length: %w", err)
	}
	payloadLen := int64(binary.LittleEndian.Uint32(lenBuf))

	// The length field is decrypted but not yet authenticated, so a wrong
	// password yields an essentially random uint32. Reject anything the carrier
	// could not hold rather than allocating up to 4 GiB on it.
	maxPadded := int64(CapacityBytes(m, bitsPerChannel, channels)) + 4
	if payloadLen > maxPadded {
		return nil, fmt.Errorf(
			"payload length %d exceeds maximum %d: wrong password or corrupt image",
			payloadLen, maxPadded)
	}

	// Allocate buffer for padded data + HMAC tag.
	totalRemaining := payloadLen + 32
	decryptedBuf := make([]byte, totalRemaining)

	alignment := lcmBytes(8, channels*bitsPerChannel)
	chunkSize := int64(alignment * 1024)

	numWorkers := runtime.GOMAXPROCS(0)
	jobChan := make(chan decJob, numWorkers*2)
	errChan := make(chan error, numWorkers)

	abort := make(chan struct{})
	var abortOnce sync.Once
	fail := func(err error) {
		errChan <- err
		abortOnce.Do(func() { close(abort) })
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter, werr := newWorkerStack(m, payloadNonce, encKey, points, bitsPerChannel, channels, nil)
			if werr != nil {
				fail(werr)
				return
			}
			for job := range jobChan {
				if _, serr := adapter.Seek(job.streamOffset, io.SeekStart); serr != nil {
					fail(serr)
					return
				}
				if _, rerr := io.ReadFull(adapter, job.dest); rerr != nil {
					fail(rerr)
					return
				}
			}
		}()
	}

	// Dispatch chunks starting at the byte where the payload begins: 16
	// (plaintext salt) + 4 (length field). Decode workers only read, so they
	// cannot clobber each other, but the same shift as EncodeParallel keeps the
	// two dispatch loops symmetric.
	shift := firstChunkShift(channels * bitsPerChannel)
	var offset int64
dispatch:
	for offset < totalRemaining {
		size := chunkSize
		if offset == 0 {
			size += shift
		}
		if offset+size > totalRemaining {
			size = totalRemaining - offset
		}
		dest := decryptedBuf[offset : offset+size]
		select {
		case jobChan <- decJob{streamOffset: payloadStreamOffset + offset, dest: dest}:
			offset += size
		case <-abort:
			break dispatch
		}
	}
	close(jobChan)
	wg.Wait()

	select {
	case werr := <-errChan:
		return nil, werr
	default:
	}

	// Verify HMAC over the full padded block.
	mac := hmac.New(sha256.New, macKey)
	mac.Write(decryptedBuf[:payloadLen])
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, decryptedBuf[payloadLen:]) {
		return nil, fmt.Errorf("checksum validation failed")
	}

	return extractRealPayload(decryptedBuf[:payloadLen])
}
