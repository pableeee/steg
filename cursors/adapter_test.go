package cursors

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/golang/mock/gomock"
	mock_cursors "github.com/pableeee/steg/mocks/cursors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedBytes = []byte{0xA, 0xB, 0xC, 0xD, 0xE, 0xF}
var expectedBits = []uint8{
	// 0x0A
	0, 0, 0, 0, 1, 0, 1, 0,
	// 0x0B
	0, 0, 0, 0, 1, 0, 1, 1,
	// 0x0C
	0, 0, 0, 0, 1, 1, 0, 0,
	//0x0D
	0, 0, 0, 0, 1, 1, 0, 1,
	// 0x0E
	0, 0, 0, 0, 1, 1, 1, 0,
	// 0x0F
	0, 0, 0, 0, 1, 1, 1, 1,
}

func TestAdapterRead(t *testing.T) {
	t.Run("should read the whole payload read buffer", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		_ = image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := mock_cursors.NewMockCursor(ctrl)
		reader := CursorAdapter(cur)

		for _, b := range expectedBits {
			cur.EXPECT().ReadBit().Return(b, nil)
		}

		payload := make([]byte, len(expectedBytes))
		n, err := reader.Read(payload)
		require.NoError(t, err)

		assert.True(t, bytes.Equal(expectedBytes, payload))
		assert.Equal(t, n, len(payload))
	})

	t.Run("should read a payload smaller than the read buffer", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		_ = image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := mock_cursors.NewMockCursor(ctrl)
		reader := CursorAdapter(cur)

		for _, b := range expectedBits {
			cur.EXPECT().ReadBit().Return(b, nil)
		}

		cur.EXPECT().ReadBit().Return(uint8(0), fmt.Errorf("out of range"))

		payload := make([]byte, len(expectedBytes)+1)
		n, err := reader.Read(payload)
		assert.Error(t, err)

		assert.True(t, bytes.Equal(expectedBytes, payload[:n]))
		assert.Equal(t, n, len(payload)-1)
	})

	t.Run("should read a payload smaller than all available bytes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		reader := CursorAdapter(cur)

		for _, b := range expectedBits {
			cur.EXPECT().ReadBit().Return(b, nil)
		}

		// mock infinite succesive reads.
		cur.EXPECT().ReadBit().Return(uint8(1), nil).AnyTimes()

		payload := make([]byte, len(expectedBytes))
		n, err := reader.Read(payload)
		require.NoError(t, err)

		assert.True(t, bytes.Equal(expectedBytes, payload))
		assert.Equal(t, n, len(payload))
	})
}
func TestAdapterWrite(t *testing.T) {
	t.Run("should successfully write the complete payload", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		writer := CursorAdapter(cur)

		for _, b := range expectedBits {
			cur.EXPECT().WriteBit(b).Return(uint(0), nil)
		}

		_, err := writer.Write(expectedBytes)
		assert.NoError(t, err)

	})
	t.Run("should fail while writing, incomplete write", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cur := mock_cursors.NewMockCursor(ctrl)
		writer := CursorAdapter(cur)

		for _, b := range expectedBits[:len(expectedBits)-(8*1)] {
			cur.EXPECT().WriteBit(b).Return(uint(0), nil)
		}

		cur.EXPECT().WriteBit(gomock.Any()).Return(uint(0), fmt.Errorf("out of range")).AnyTimes()

		n, err := writer.Write(expectedBytes)
		assert.Error(t, err)
		assert.Equal(t, n, len(expectedBytes)-1)

	})
}
