package cursors

import (
	"io"
)

type readWriteSeekerAdapter struct {
	io.Reader
	io.Seeker
	io.Writer
	cur Cursor
}

func CursorAdapter(c Cursor) io.ReadWriteSeeker {
	return &readWriteSeekerAdapter{cur: c}
}

func byteToBits(b byte) []int {
	var bits []int
	for i := 7; i >= 0; i-- { // Extract bits from most significant to least significant
		bit := (b >> i) & 1
		bits = append(bits, int(bit))
	}

	return bits
}

func (r *readWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	return r.cur.Seek(offset, whence)
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
		bits := byteToBits(bite)
		for _, b := range bits {
			_, err = r.cur.WriteBit(uint8(b))
			if err != nil {
				return i, err
			}
		}
	}

	return len(payload), nil
}
