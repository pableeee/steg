package container

import (
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

func WritePayload(w io.WriteSeeker, payload io.Reader, hashFn hash.Hash) error {
	// Capture current position. When called from encode, basePos=4 (after nonce).
	// When called directly (container tests), basePos=0. Behavior identical in both cases.
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

func ReadPayload(r io.ReadWriteSeeker, hashFn hash.Hash) ([]byte, error) {
	sizeBytes := make([]byte, 4)
	_, err := io.ReadFull(r, sizeBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload size: %w", err)
	}

	length := binary.LittleEndian.Uint32(sizeBytes)
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

	if !bytesEqual(checksum, hashFn.Sum(nil)) {
		return nil, fmt.Errorf("checksum validation failed")
	}

	return payload, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
