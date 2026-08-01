package container

import (
	"crypto/hmac"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

func WritePayload(w io.WriteSeeker, payload io.Reader, hashFn hash.Hash) error {
	// Capture current position. When called from encode, basePos=16 (after the
	// plaintext salt). When called directly (container tests), basePos=0.
	// Behavior is identical in both cases.
	basePos, err := w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	_, err = w.Seek(basePos+4, io.SeekStart) // skip past length field
	if err != nil {
		return err
	}

	var length uint32
	buf := make([]byte, 1024)
	for {
		n, readErr := payload.Read(buf)
		if n > 0 {
			hashFn.Write(buf[:n])
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			length += uint32(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	checksum := hashFn.Sum(nil)
	if _, err = w.Write(checksum); err != nil {
		return err
	}

	if _, err = w.Seek(basePos, io.SeekStart); err != nil { // seek back to write length
		return err
	}

	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, length)
	_, err = w.Write(sizeBytes)
	return err
}

// ReadPayload reads a framed payload written by WritePayload and verifies its
// MAC.
//
// maxPayload bounds the length field. That field is decrypted but not yet
// authenticated when it is read, so a wrong password yields an essentially
// random uint32 — up to 4 GiB. Rejecting anything larger than the carrier could
// possibly hold turns a huge speculative allocation into an immediate error.
// A non-positive maxPayload disables the check.
func ReadPayload(r io.ReadWriteSeeker, hashFn hash.Hash, maxPayload int) ([]byte, error) {
	sizeBytes := make([]byte, 4)
	_, err := io.ReadFull(r, sizeBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload size: %w", err)
	}

	length := binary.LittleEndian.Uint32(sizeBytes)
	if maxPayload > 0 && int64(length) > int64(maxPayload) {
		return nil, fmt.Errorf(
			"payload length %d exceeds maximum %d: wrong password or corrupt image",
			length, maxPayload)
	}
	payload := make([]byte, length)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload: %w", err)
	}
	hashFn.Write(payload)

	checksum := make([]byte, hashFn.Size())
	_, err = io.ReadFull(r, checksum)
	if err != nil {
		return nil, fmt.Errorf("failed to read checksum: %w", err)
	}

	if !hmac.Equal(checksum, hashFn.Sum(nil)) {
		return nil, fmt.Errorf("checksum validation failed")
	}

	return payload, nil
}
