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
	seed, encKey, macKey, kdfNonce, err := deriveKeys(pass)
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

	// Write encrypted nonce to image bytes 0–3 via the bootstrap cipher.
	// This must happen before workers start so they begin at the correct offset.
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	var rawNonce [4]byte
	if _, err = rand.Read(rawNonce[:]); err != nil {
		return err
	}
	bootstrapCipher, err := cipher.NewCipher(kdfNonce, encKey)
	if err != nil {
		return err
	}
	bootstrapAdapter := cursors.CursorAdapter(cursors.CipherMiddleware(rawCur, bootstrapCipher))
	if _, err = bootstrapAdapter.Write(rawNonce[:]); err != nil {
		return err
	}
	rawCur.Flush()

	randomNonce := binary.BigEndian.Uint32(rawNonce[:])

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

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter, werr := newWorkerStack(m, randomNonce, encKey, points, bitsPerChannel, channels, imgMu)
			if werr != nil {
				errChan <- werr
				return
			}
			for job := range jobChan {
				if _, serr := adapter.Seek(job.streamOffset, io.SeekStart); serr != nil {
					errChan <- serr
					return
				}
				if _, werr2 := adapter.Write(job.data); werr2 != nil {
					errChan <- werr2
					return
				}
			}
			if _, ferr := adapter.Seek(0, io.SeekStart); ferr != nil {
				errChan <- ferr
			}
		}()
	}

	// Dispatch padded data in aligned chunks. streamOffset skips 4 bytes of
	// encrypted nonce + 4 bytes of container length field = byte 8.
	totalLen := int64(len(padded))
	var offset int64
	for offset < totalLen {
		end := offset + int64(chunkSize)
		if end > totalLen {
			end = totalLen
		}
		chunk := make([]byte, end-offset)
		copy(chunk, padded[offset:end])
		jobChan <- encJob{streamOffset: 8 + offset, data: chunk}
		offset = end
	}
	close(jobChan)
	wg.Wait()

	select {
	case werr := <-errChan:
		return werr
	default:
	}

	// Post-parallel sequential writes: container length field (byte 4) and HMAC.
	// Workers use randomNonce; the nonce region (bytes 0–3) is already written.
	seqAdapter, err := newWorkerStack(m, randomNonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(4, io.SeekStart); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(totalLen))
	if _, err = seqAdapter.Write(lenBuf); err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(8+totalLen, io.SeekStart); err != nil {
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
	seed, encKey, macKey, kdfNonce, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Decrypt the encrypted nonce from image bytes 0–3.
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	bootstrapCipher, err := cipher.NewCipher(kdfNonce, encKey)
	if err != nil {
		return nil, err
	}
	bootstrapAdapter := cursors.CursorAdapter(cursors.CipherMiddleware(rawCur, bootstrapCipher))
	var rawNonce [4]byte
	if _, err = io.ReadFull(bootstrapAdapter, rawNonce[:]); err != nil {
		return nil, err
	}
	randomNonce := binary.BigEndian.Uint32(rawNonce[:])

	// Read the 4-byte container length field at byte 4 (after the encrypted nonce).
	seqAdapter, err := newWorkerStack(m, randomNonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return nil, err
	}
	if _, err = seqAdapter.Seek(4, io.SeekStart); err != nil {
		return nil, err
	}
	lenBuf := make([]byte, 4)
	if _, err = io.ReadFull(seqAdapter, lenBuf); err != nil {
		return nil, fmt.Errorf("failed to read payload length: %w", err)
	}
	payloadLen := int64(binary.LittleEndian.Uint32(lenBuf))

	// Allocate buffer for padded data + HMAC tag.
	totalRemaining := payloadLen + 32
	decryptedBuf := make([]byte, totalRemaining)

	alignment := lcmBytes(8, channels*bitsPerChannel)
	chunkSize := int64(alignment * 1024)

	numWorkers := runtime.GOMAXPROCS(0)
	jobChan := make(chan decJob, numWorkers*2)
	errChan := make(chan error, numWorkers)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter, werr := newWorkerStack(m, randomNonce, encKey, points, bitsPerChannel, channels, nil)
			if werr != nil {
				errChan <- werr
				return
			}
			for job := range jobChan {
				if _, serr := adapter.Seek(job.streamOffset, io.SeekStart); serr != nil {
					errChan <- serr
					return
				}
				if _, rerr := io.ReadFull(adapter, job.dest); rerr != nil {
					errChan <- rerr
					return
				}
			}
		}()
	}

	// Dispatch aligned chunks; streamOffset skips 4 (enc nonce) + 4 (length) = byte 8.
	var offset int64
	for offset < totalRemaining {
		size := chunkSize
		if offset+size > totalRemaining {
			size = totalRemaining - offset
		}
		dest := decryptedBuf[offset : offset+size]
		jobChan <- decJob{streamOffset: 8 + offset, dest: dest}
		offset += size
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
