package steg

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"hash"
	"math/rand"
	"testing"

	"github.com/golang/mock/gomock"
	mock_cursors "github.com/pableeee/steg/mocks/cursors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockWriterCursor(
	t *testing.T,
	ctrl *gomock.Controller,
	size uint32,
	correctChecksum bool,
	hashFn hash.Hash,
	rnd *rand.Rand,
) *mock_cursors.MockCursor {
	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, size)
	cur := mock_cursors.NewMockCursor(ctrl)

	// the skips writing the payload size for last.
	cur.EXPECT().Seek(4 * 8).Return(nil)

	// mock the payload
	for i := 0; i < int(size); i++ {
		randNum := byte(rnd.Intn(256))
		_, err := hashFn.Write([]byte{randNum})
		require.NoError(t, err)

		for _, b := range byteToBits(randNum) {
			cur.EXPECT().WriteBit(gomock.Any()).
				Return(uint(b), nil)
		}
	}

	var cks []byte
	if correctChecksum {
		cks = hashFn.Sum(nil)
	} else {
		cks = make([]byte, 1)
		for i := range bs {
			bs[i] = byte(rnd.Intn(256))
		}
	}

	// mock the checksum
	for i := 0; i < len(cks); i++ {
		for _, b := range byteToBits(cks[i]) {
			cur.EXPECT().ReadBit().
				Return(uint8(b), nil)
		}
	}

	// mock the payload size
	for _, bite := range bs {
		for _, b := range byteToBits(bite) {
			cur.EXPECT().ReadBit().
				Return(uint8(b), nil)
		}
	}

	return cur
}

func TestInputStreemTooShort(t *testing.T) {
	t.Run("should fail when skiping payload len", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)

		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any()).Return(fmt.Errorf("out of range"))
		w := writer{cursor: cur, hashFunc: md5.New()}
		err := w.Write(bytes.NewReader([]byte("some payload")))
		assert.Error(t, err)
	})

	t.Run("should fail writing payload", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)

		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any()).Return(nil)
		// writes 1 bit
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		// stream ends
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), fmt.Errorf("out of range"))

		w := writer{cursor: cur, hashFunc: md5.New()}
		err := w.Write(bytes.NewReader([]byte("some payload")))
		assert.Error(t, err)
	})

	t.Run("should fail writing hash", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		payload := []byte("yellow submarine")
		// the skips writing the payload size for last.
		cur.EXPECT().Seek(gomock.Any()).Return(nil)

		for i := 0; i < len(payload)*8; i++ {
			// writes 1 bit
			cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		}

		// writes 1 bit
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), nil)
		// stream ends
		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), fmt.Errorf("out of range"))

		w := writer{cursor: cur, hashFunc: md5.New()}
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
		cur.EXPECT().Seek(uint(4 * 8)).Return(nil)
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
		cur.EXPECT().Seek(uint(0)).Return(nil)

		// mocks writing the payload size
		for _, bite := range size {
			for _, b := range byteToBits(bite) {
				cur.EXPECT().WriteBit(uint8(b)).Return(uint(b), nil)
			}
		}

		w := writer{cursor: cur, hashFunc: md5.New()}
		err := w.Write(bytes.NewReader(payload))
		assert.NoError(t, err)
	})
}
