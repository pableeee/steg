package steg

import (
	"crypto/hmac"
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

// newWorkerStack creates a per-worker cipher+cursor stack seeked to the correct
// byte offset. Each worker has its own independent cipher and cursor state.
// imgMu, when non-nil, is shared across all concurrent workers to serialise
// img.At() / img.Set() calls and eliminate data races on the shared draw.Image.
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
	seed, encKey, macKey, nonce, err := deriveKeys(pass)
	if err != nil {
		return err
	}

	// Buffer the full payload to build the padded block. Padding must be known
	// before dispatch so every encode writes the full image capacity.
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
			adapter, werr := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels, imgMu)
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
			// Flush write-back cache via a no-op seek.
			if _, ferr := adapter.Seek(0, io.SeekStart); ferr != nil {
				errChan <- ferr
			}
		}()
	}

	// Dispatch padded data in aligned chunks. streamOffset skips the 4-byte
	// container length field, which is written in the sequential post-pass.
	totalLen := int64(len(padded))
	var offset int64
	for offset < totalLen {
		end := offset + int64(chunkSize)
		if end > totalLen {
			end = totalLen
		}
		chunk := make([]byte, end-offset)
		copy(chunk, padded[offset:end])
		jobChan <- encJob{streamOffset: 4 + offset, data: chunk}
		offset = end
	}
	close(jobChan)
	wg.Wait()

	select {
	case werr := <-errChan:
		return werr
	default:
	}

	// Post-parallel sequential writes: length field (byte 0) and HMAC tag.
	seqAdapter, err := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(0, io.SeekStart); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(totalLen))
	if _, err = seqAdapter.Write(lenBuf); err != nil {
		return err
	}

	if _, err = seqAdapter.Seek(4+totalLen, io.SeekStart); err != nil {
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
	seed, encKey, macKey, nonce, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Read the 4-byte container length field at byte offset 0.
	seqAdapter, err := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels, nil)
	if err != nil {
		return nil, err
	}
	if _, err = seqAdapter.Seek(0, io.SeekStart); err != nil {
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
			// Decode workers only read the image; concurrent reads are race-free.
			adapter, werr := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels, nil)
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

	// Dispatch aligned chunks; streamOffset skips the 4-byte length field.
	var offset int64
	for offset < totalRemaining {
		size := chunkSize
		if offset+size > totalRemaining {
			size = totalRemaining - offset
		}
		dest := decryptedBuf[offset : offset+size]
		jobChan <- decJob{streamOffset: 4 + offset, dest: dest}
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
