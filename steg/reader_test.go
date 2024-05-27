package steg

import (
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

func mockReaderCursor(
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
	// mock the payload size
	for _, bite := range bs {
		for _, b := range byteToBits(bite) {
			cur.EXPECT().ReadBit().
				Return(uint8(b), nil)
		}
	}

	// mock the payload
	for i := 0; i < int(size); i++ {
		randNum := byte(rnd.Intn(256))
		_, err := hashFn.Write([]byte{randNum})
		require.NoError(t, err)

		for _, b := range byteToBits(randNum) {
			cur.EXPECT().ReadBit().
				Return(uint8(b), nil)
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

	return cur
}

func TestRead(t *testing.T) {
	t.Run("read bytes until eof with correct chechsum", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		hashFn := md5.New()
		rnd := rand.New(rand.NewSource(0))
		cur := mockReaderCursor(t, ctrl, uint32(4), true, hashFn, rnd)

		r := reader{cursor: cur, hashFunc: md5.New()}
		_, err := r.Read()
		require.NoError(t, err)
	})

	t.Run("read bytes until eof with incorrect checksum", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		hashFn := md5.New()
		rnd := rand.New(rand.NewSource(0))
		cur := mockReaderCursor(t, ctrl, uint32(4), false, hashFn, rnd)

		r := reader{cursor: cur, hashFunc: md5.New()}
		_, err := r.Read()
		assert.NotNil(t, err)
	})

	t.Run("read bytes with incorrect size", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		hashFn := md5.New()
		rnd := rand.New(rand.NewSource(0))
		size := 4

		bs := make([]byte, 4)
		binary.LittleEndian.PutUint32(bs, uint32(size+20))
		cur := mock_cursors.NewMockCursor(ctrl)
		// mock the payload size
		for _, bite := range bs {
			for _, b := range byteToBits(bite) {
				cur.EXPECT().ReadBit().
					Return(uint8(b), nil)
			}
		}

		// mock the payload
		for i := 0; i < int(size); i++ {
			randNum := byte(rnd.Intn(256))
			_, err := hashFn.Write([]byte{randNum})
			require.NoError(t, err)

			for _, b := range byteToBits(randNum) {
				cur.EXPECT().ReadBit().
					Return(uint8(b), nil)
			}
		}

		cks := hashFn.Sum(nil)

		// mock the checksum
		for i := 0; i < len(cks); i++ {
			for _, b := range byteToBits(cks[i]) {
				cur.EXPECT().ReadBit().
					Return(uint8(b), nil)
			}
		}

		cur.EXPECT().ReadBit().
			Return(uint8(0), fmt.Errorf("eof"))

		r := reader{cursor: cur, hashFunc: md5.New()}
		b, err := r.Read()
		assert.NotNil(t, err)
		assert.Nil(t, b)
	})
}
