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
	n, err := r.cur.Seek(offset*8, whence)
	return n / 8, err
}

func (r *readWriteSeekerAdapter) Read(payload []byte) (n int, err error) {
	for p := range payload {
		b, err := r.cur.ReadByte()
		if err != nil {
			return p, err
		}
		payload[p] = b
	}
	return len(payload), nil
}

func (r *readWriteSeekerAdapter) Write(payload []byte) (n int, err error) {
	for i, b := range payload {
		if err := r.cur.WriteByte(b); err != nil {
			return i, err
		}
	}
	return len(payload), nil
}
