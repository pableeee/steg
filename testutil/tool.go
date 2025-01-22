package testutil

import (
	"errors"
	"io"
)

// MemReadWriteSeeker is a simple in-memory implementation of io.ReadWriteSeeker.
// It stores data in a []byte slice and keeps track of the current offset.
type MemReadWriteSeeker struct {
	data   []byte
	offset int64
}

var _ io.ReadWriteSeeker = (*MemReadWriteSeeker)(nil)

// NewMemReadWriteSeeker creates a new MemReadWriteSeeker with the given initial data.
func NewMemReadWriteSeeker(initial []byte) *MemReadWriteSeeker {
	// Make a copy of the data to avoid external modification
	d := make([]byte, len(initial))
	copy(d, initial)
	return &MemReadWriteSeeker{
		data:   d,
		offset: 0,
	}
}

func (m *MemReadWriteSeeker) Read(p []byte) (int, error) {
	if m.offset >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.offset:])
	m.offset += int64(n)
	return n, nil
}

func (m *MemReadWriteSeeker) Write(p []byte) (int, error) {
	// If offset is beyond the end, we need to extend data
	end := m.offset + int64(len(p))
	if end > int64(len(m.data)) {
		// Extend the underlying slice
		newData := make([]byte, end)
		copy(newData, m.data)
		m.data = newData
	}
	n := copy(m.data[m.offset:], p)
	m.offset += int64(n)
	return n, nil
}

func (m *MemReadWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = m.offset + offset
	case io.SeekEnd:
		newPos = int64(len(m.data)) + offset
	default:
		return 0, errors.New("invalid whence")
	}

	if newPos < 0 {
		return 0, errors.New("negative position")
	}

	m.offset = newPos
	return m.offset, nil
}

// Bytes returns a copy of the current data stored.
func (m *MemReadWriteSeeker) Bytes() []byte {
	cpy := make([]byte, len(m.data))
	copy(cpy, m.data)
	return cpy
}

// Bytes returns a copy of the current data stored.
func (m *MemReadWriteSeeker) Truncate(size int64) error {
	if size < 0 {
		return errors.New("negative size")
	}

	if size > int64(len(m.data)) {
		return errors.New("exceeds current size")
	}

	m.data = m.data[:size]

	return nil
}
