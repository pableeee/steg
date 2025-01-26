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

func byteToBits(b byte) []int {
	var bits []int
	for i := 7; i >= 0; i-- { // Extract bits from most significant to least significant
		bit := (b >> i) & 1
		bits = append(bits, int(bit))
	}

	return bits
}

func splitSlice(slice []int, chunkSize int) [][]int {
	if chunkSize <= 0 {
		panic("chunkSize must be greater than 0")
	}

	var chunks [][]int
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func (r *readWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	return r.cur.Seek(offset*8, whence)
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
