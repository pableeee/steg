package steg

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/pableeee/steg/cursors"
	mock_cursors "github.com/pableeee/steg/mocks/cursors"
	"github.com/stretchr/testify/assert"
)

func TestInputStreemTooShort(t *testing.T) {
	t.Run("should fail when skiping payload len", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)

		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any(), gomock.Any()).
			Return(int64(0), fmt.Errorf("out of range"))
		w := writer{cursor: cursors.CursorAdapter(cur), hashFunc: md5.New()}
		err := w.Write(bytes.NewReader([]byte("some payload")))
		assert.Error(t, err)
	})

	t.Run("should fail writing payload", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)

		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any(), gomock.Any()).Return(int64(0), nil)
		// writes 1 bit
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		// stream ends
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), fmt.Errorf("out of range"))

		w := writer{cursor: cursors.CursorAdapter(cur), hashFunc: md5.New()}
		err := w.Write(bytes.NewReader([]byte("some payload")))
		assert.Error(t, err)
	})

	t.Run("should fail writing hash", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		payload := []byte("yellow submarine")
		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any(), gomock.Any()).Return(int64(0), nil)

		for i := 0; i < len(payload)*8; i++ {
			// writes 1 bit
			cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		}

		// writes 1 bit
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		// stream ends
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), fmt.Errorf("out of range"))

		w := writer{cursor: cursors.CursorAdapter(cur), hashFunc: md5.New()}
		err := w.Write(bytes.NewReader(payload))
		assert.Error(t, err)
	})

}

func TestWriteSuccess(t *testing.T) {
	t.Run("should write payload ok, len(medium) > len(payload)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		payload := []byte("yellow submarine")
		size := make([]byte, 4)
		binary.LittleEndian.PutUint32(size, uint32(len(payload)))

		// the skips writing the payload size for last.
		cur.EXPECT().Seek(int64(4*8), io.SeekStart).Return(int64(0), nil)
		// mocks writing the payload
		for _, bite := range payload {
			for _, b := range byteToBits(bite) {
				cur.EXPECT().WriteBit(uint8(b)).Return(uint(b), nil)
			}
		}
		// mocks writing the hash
		for i := 0; i < md5.New().Size()*8; i++ {
			cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		}

		// seeks to the beggining to write the payload size.
		cur.EXPECT().Seek(int64(0), io.SeekStart).Return(int64(0), nil)

		// mocks writing the payload size
		for _, bite := range size {
			for _, b := range byteToBits(bite) {
				cur.EXPECT().WriteBit(uint8(b)).Return(uint(b), nil)
			}
		}

		w := writer{cursor: cursors.CursorAdapter(cur), hashFunc: md5.New()}
		err := w.Write(bytes.NewReader(payload))
		assert.NoError(t, err)
	})
}
