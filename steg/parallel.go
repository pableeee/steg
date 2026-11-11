package steg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
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
// Encode workers write the image's Pix bytes without any lock. That is safe
// only because no two workers touch the same pixel: setting a bit is a
// load-modify-store of a whole byte, so if a boundary fell mid-pixel the workers
// on either side could both rewrite the same channel byte (always, when
// bitsPerChannel > 1), and whichever stored last would silently discard the
// other's bits — the image would then fail MAC verification on decode, and the
// race detector would flag it. A mutex around each access would not help: it is
// the load-modify-store sequence that must not interleave.
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
// own independent cipher and cursor state over the shared order slice and the
// shared image. Workers need no lock: the dispatch loops hand them
// pixel-aligned chunks (see firstChunkShift), so they write disjoint bytes.
func newWorkerStack(m draw.Image, keys mainKeys, order []uint32,
	bitsPerChannel, channels int) (io.ReadWriteSeeker, error) {
	cur, err := cursors.NewRNGCursor(m, cursorOptions(order, bitsPerChannel, channels)...)
	if err != nil {
		return nil, err
	}
	c, err := cipher.NewCipher(keys.nonce, keys.encKey)
	if err != nil {
		return nil, err
	}
	return cursors.CursorAdapter(cursors.CipherMiddleware(cur, c)), nil
}

// EncodeParallel encodes r into m using a parallel worker pool.
// The on-image layout is identical to Encode, so DecodeParallel and Decode
// can both decode images written by EncodeParallel (and vice-versa). Carrier
// types and settings are validated as in Encode.
func EncodeParallel(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
	if err := validateCarrier(m, bitsPerChannel, channels); err != nil {
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

	// Write the plaintext salt (stream bytes 0–15) and derive the keys and the
	// full pixel order before workers start.
	keys, points, err := prepareEncode(m, pass, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	alignment := lcmBytes(8, channels*bitsPerChannel)
	chunkSize := alignment * 1024

	// Pre-compute HMAC over the full padded block before dispatching workers.
	hashFn := hmac.New(sha256.New, keys.macKey)
	hashFn.Write(padded)
	tag := hashFn.Sum(nil)

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
			adapter, werr := newWorkerStack(m, keys, points, bitsPerChannel, channels)
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
	// The salt region (bytes 0–15) was written by prepareEncode.
	seqAdapter, err := newWorkerStack(m, keys, points, bitsPerChannel, channels)
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
	_, err = seqAdapter.Write(tag)
	return err
}

// DecodeParallel decodes a message from m using a parallel worker pool.
// Images encoded by Encode (sequential) are fully compatible. Carrier types
// and settings are validated as in Decode.
func DecodeParallel(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	if err := validateCarrier(m, bitsPerChannel, channels); err != nil {
		return nil, err
	}
	// Read the plaintext salt (stream bytes 0–15) and derive the keys and the
	// full pixel order.
	keys, points, err := prepareDecode(m, pass, bitsPerChannel, channels)
	if err != nil {
		return nil, err
	}

	// Read the 4-byte container length field at byte 16 (after the plaintext salt).
	seqAdapter, err := newWorkerStack(m, keys, points, bitsPerChannel, channels)
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
			adapter, werr := newWorkerStack(m, keys, points, bitsPerChannel, channels)
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
	mac := hmac.New(sha256.New, keys.macKey)
	mac.Write(decryptedBuf[:payloadLen])
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, decryptedBuf[payloadLen:]) {
		return nil, fmt.Errorf("checksum validation failed")
	}

	return extractRealPayload(decryptedBuf[:payloadLen])
}
