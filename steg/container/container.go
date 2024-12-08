package container

import (
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

func WritePayload(w io.WriteSeeker, payload io.Reader, hashFn hash.Hash) error {
	// Reserve space for length (4 bytes)
	_, err := w.Seek(4, io.SeekStart)
	if err != nil {
		return err
	}

	var length uint32
	buf := make([]byte, 1024)
	for {
		n, readErr := payload.Read(buf)
		if n > 0 {
			hashFn.Write(buf[:n])
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
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

	// Write the hash
	checksum := hashFn.Sum(nil)
	_, err = w.Write(checksum)
	if err != nil {
		return err
	}

	// Go back and write the length
	_, err = w.Seek(0, io.SeekStart)
	if err != nil {
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
