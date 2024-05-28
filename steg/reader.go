package steg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

type reader struct {
	cursor   io.ReadWriteSeeker
	hashFunc hash.Hash
}

func (t *reader) Read() ([]byte, error) {
	payloadSize := make([]byte, 4)
	n, err := t.cursor.Read(payloadSize)
	if err != nil || len(payloadSize) != n {
		return nil, fmt.Errorf("unable to read payload size %w", err)
	}

	payload := make([]byte, binary.LittleEndian.Uint32(payloadSize))
	n, err = t.cursor.Read(payload)
	if err != nil || len(payload) != n {
		return nil, fmt.Errorf("unable to read payload size %w", err)
	}
	t.hashFunc.Write(payload)

	expectedChecksum := t.hashFunc.Sum(nil)
	checksum := make([]byte, t.hashFunc.Size())
	n, err = t.cursor.Read(checksum)
	if err != nil || len(checksum) != n {
		return nil, fmt.Errorf("unable to read payload %w", err)
	}

	if !bytes.Equal(expectedChecksum, checksum) {
		return nil, fmt.Errorf("checksum failed")
	}

	return payload, nil
}
