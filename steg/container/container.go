package container

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

const (
	// Format version constants
	FormatVersion0 = 0 // Old format: [4-byte length][payload][checksum], nonce=0
	FormatVersion1 = 1 // New format: [1-byte version][4-byte nonce][4-byte length][payload][checksum]
)

// CalculateRequiredCapacity calculates the total capacity needed in bytes
// for encoding a payload of the given size.
// Format: [4-byte length][payload][hash-size checksum]
func CalculateRequiredCapacity(payloadSize int64, hashSize int) int64 {
	return 4 + payloadSize + int64(hashSize)
}

// GenerateNonce generates a cryptographically secure random 32-bit nonce
func GenerateNonce() (uint32, error) {
	nonceBytes := make([]byte, 4)
	if _, err := rand.Read(nonceBytes); err != nil {
		return 0, err
	}
	return uint32(nonceBytes[0]) | uint32(nonceBytes[1])<<8 | uint32(nonceBytes[2])<<16 | uint32(nonceBytes[3])<<24, nil
}

// WritePayload writes a payload in the old format (version 0) for backward compatibility
func WritePayload(w io.WriteSeeker, payload io.Reader, hashFn hash.Hash) error {
	// Old format: [4-byte length][payload][checksum]
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

// WritePayloadWithNonce writes a payload with format version 1 (nonce is already written separately)
func WritePayloadWithNonce(w io.WriteSeeker, payload io.Reader, hashFn hash.Hash, nonce uint32) error {
	// Format version 1: [1-byte version][4-byte length][payload][checksum]
	// Note: nonce is written separately before this function is called
	
	// Write format version (1 byte)
	versionByte := []byte{FormatVersion1}
	if _, err := w.Write(versionByte); err != nil {
		return fmt.Errorf("failed to write format version: %w", err)
	}

	// Reserve space for length (4 bytes)
	if _, err := w.Seek(4, io.SeekCurrent); err != nil {
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
	_, err := w.Write(checksum)
	if err != nil {
		return err
	}

	// Go back and write the length (after version byte)
	_, err = w.Seek(1, io.SeekStart) // After version byte
	if err != nil {
		return err
	}

	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, length)
	_, err = w.Write(sizeBytes)
	return err
}

// ReadPayloadOldFormat reads a payload in the old format (version 0, no nonce)
func ReadPayloadOldFormat(r io.ReadWriteSeeker, hashFn hash.Hash) ([]byte, error) {
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

// ReadPayload reads a payload in the new format (version 1, with nonce).
// The nonce should already be read separately (unencrypted).
// Returns payload, nonce (for verification), and error.
func ReadPayload(r io.ReadWriteSeeker, hashFn hash.Hash) ([]byte, uint32, error) {
	// Read format version (1 byte, encrypted)
	versionByte := make([]byte, 1)
	_, err := io.ReadFull(r, versionByte)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read format version: %w", err)
	}

	version := versionByte[0]
	if version != FormatVersion1 {
		return nil, 0, fmt.Errorf("unexpected format version: %d (expected %d)", version, FormatVersion1)
	}

	// Read length (4 bytes, encrypted)
	sizeBytes := make([]byte, 4)
	_, err = io.ReadFull(r, sizeBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read payload size: %w", err)
	}

	length := binary.LittleEndian.Uint32(sizeBytes)
	payload := make([]byte, length)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read payload: %w", err)
	}
	hashFn.Write(payload)

	checksum := make([]byte, hashFn.Size())
	_, err = io.ReadFull(r, checksum)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read checksum: %w", err)
	}

	if !bytesEqual(checksum, hashFn.Sum(nil)) {
		return nil, 0, fmt.Errorf("checksum validation failed")
	}

	// Return 0 as nonce - it's passed separately for verification
	return payload, 0, nil
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
