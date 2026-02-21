package cursors

import (
	"io"
)

func CursorAdapter(c Cursor) io.ReadWriteSeeker {
	return &readWriteSeekerAdapter{cur: c}
}

type readWriteSeekerAdapter struct {
	io.Reader
	io.Seeker
	io.Writer
	cur Cursor
}

func (r *readWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	n, err := r.cur.Seek(offset*8, whence)
	return n / 8, err
}

func (r *readWriteSeekerAdapter) Read(payload []byte) (n int, err error) {
	p := 0
	for ; p < len(payload); p++ {
		var nBits = 8
		var res uint8
		for i := 0; i < nBits; i++ {
			bit, err := r.cur.ReadBit()
			if err != nil {
				return p, err
			}

			res |= uint8(bit << (nBits - i - 1))
		}
		payload[p] = res
	}

	return p, nil
}
func (r *readWriteSeekerAdapter) Write(payload []byte) (n int, err error) {
	for i, bite := range payload {
		for j := 7; j >= 0; j-- {
			_, err = r.cur.WriteBit((bite >> j) & 1)
			if err != nil {
				return i, err
			}
		}
	}

	return len(payload), nil
}
