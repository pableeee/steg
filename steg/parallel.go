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

// newWorkerStack creates a per-worker cipher+cursor stack seeked to the correct
// byte offset. Each worker has its own independent cipher and cursor state.
func newWorkerStack(m draw.Image, nonce uint32, encKey []byte,
	points []image.Point, bitsPerChannel, channels int) (io.ReadWriteSeeker, error) {
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
	seed, encKey, macKey, err := deriveKeys(pass)
	if err != nil {
		return err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Minimum chunk alignment: lcm(8 bits/byte, channels*bitsPerChannel bits/pixel) / 8 bytes.
	alignment := lcmBytes(8, channels*bitsPerChannel)
	chunkSize := alignment * 1024

	// Write 4-byte nonce plaintext via raw (unencrypted) cursor.
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	nonceBuf := make([]byte, 4)
	if _, err = rand.Read(nonceBuf); err != nil {
		return err
	}
	nonce := binary.BigEndian.Uint32(nonceBuf)
	rawAdapter := cursors.CursorAdapter(rawCur)
	if _, err = rawAdapter.Write(nonceBuf); err != nil {
		return err
	}
	// Flush write-back cache: the nonce write may have left a partially-modified
	// pixel in rawCur that hasn't been committed to the image yet.
	rawCur.Flush()

	numWorkers := runtime.GOMAXPROCS(0)
	jobChan := make(chan encJob, numWorkers*2)
	errChan := make(chan error, numWorkers)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			adapter, werr := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels)
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
			// Flush write-back cache: last job may have left a dirty pixel.
			// Seek(0) flushes via RNGCursor.Seek without disturbing other workers.
			if _, ferr := adapter.Seek(0, io.SeekStart); ferr != nil {
				errChan <- ferr
			}
		}()
	}

	// Stream input, feed workers, accumulate HMAC.
	hashFn := hmac.New(sha256.New, macKey)
	var totalLen int64
	buf := make([]byte, chunkSize)
	var dispatchErr error
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			hashFn.Write(buf[:n])
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			// streamOffset: skip 4-byte nonce + 4-byte length = byte 8.
			jobChan <- encJob{streamOffset: 8 + totalLen, data: chunk}
			totalLen += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			dispatchErr = readErr
			break
		}
	}
	close(jobChan)
	wg.Wait()

	if dispatchErr != nil {
		return dispatchErr
	}
	select {
	case werr := <-errChan:
		return werr
	default:
	}

	// Post-parallel sequential writes: length field (byte 4) and HMAC tag.
	seqAdapter, err := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	// Write encrypted 4-byte LE length at byte offset 4.
	if _, err = seqAdapter.Seek(4, io.SeekStart); err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(totalLen))
	if _, err = seqAdapter.Write(lenBuf); err != nil {
		return err
	}

	// Write encrypted 32-byte HMAC tag immediately after payload.
	if _, err = seqAdapter.Seek(8+totalLen, io.SeekStart); err != nil {
		return err
	}
	tag := hashFn.Sum(nil)
	if _, err = seqAdapter.Write(tag); err != nil {
		return err
	}
	// Flush write-back cache on seqAdapter via a no-op seek.
	_, err = seqAdapter.Seek(0, io.SeekStart)
	return err
}

// DecodeParallel decodes a message from m using a parallel worker pool.
// Images encoded by Encode (sequential) are fully compatible.
func DecodeParallel(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	seed, encKey, macKey, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	bounds := m.Bounds()
	points := cursors.GenerateSequence(bounds.Max.X, bounds.Max.Y, seed)

	// Read 4-byte nonce plaintext via raw cursor.
	rawOpts := []cursors.Option{cursors.WithSharedPoints(points), cursors.WithBitsPerChannel(bitsPerChannel)}
	if channels >= 2 {
		rawOpts = append(rawOpts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		rawOpts = append(rawOpts, cursors.UseBlueBit())
	}
	rawCur := cursors.NewRNGCursor(m, rawOpts...)
	rawAdapter := cursors.CursorAdapter(rawCur)
	nonceBuf := make([]byte, 4)
	if _, err = io.ReadFull(rawAdapter, nonceBuf); err != nil {
		return nil, err
	}
	nonce := binary.BigEndian.Uint32(nonceBuf)

	// Read encrypted 4-byte length sequentially (cipher at byte offset 4).
	seqAdapter, err := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels)
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

	// Allocate buffer for payload + HMAC tag.
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
			adapter, werr := newWorkerStack(m, nonce, encKey, points, bitsPerChannel, channels)
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

	// Dispatch aligned chunks (all but last must be multiple of alignment bytes).
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

	// Verify HMAC tag.
	hashFn := hmac.New(sha256.New, macKey)
	hashFn.Write(decryptedBuf[:payloadLen])
	expected := hashFn.Sum(nil)
	if !hmac.Equal(expected, decryptedBuf[payloadLen:]) {
		return nil, fmt.Errorf("checksum validation failed")
	}

	return decryptedBuf[:payloadLen], nil
}
