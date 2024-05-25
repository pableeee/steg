package cipher

import (
	"crypto/aes"
	"testing"

	"github.com/golang/mock/gomock"

	mock_cipher "github.com/pableeee/steg/mocks/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_StreamCipherPrimitive(t *testing.T) {
	pass := []byte("YELLOW SUBMARINE")
	dummyBlock := func(ctrl *gomock.Controller) Block {
		block := mock_cipher.NewMockBlock(ctrl)
		block.EXPECT().Decrypt(gomock.Any(), gomock.Any()).
			DoAndReturn(func(dst, src []byte) {
				copy(dst, src)
			}).AnyTimes()
		block.EXPECT().Encrypt(gomock.Any(), gomock.Any()).
			DoAndReturn(func(dst, src []byte) {
				copy(dst, src)
			}).AnyTimes()
		block.EXPECT().BlockSize().
			Return(16).AnyTimes()
		return block
	}

	t.Run("underliying cipher block should change after block size number encriptions/decriptions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		b := dummyBlock(ctrl)
		c := NewCipher(0, pass, WithBlock(b))

		funcs := []func(uint8) (uint8, error){
			c.DecryptBit,
			c.EncryptBit,
		}

		for _, fn := range funcs {
			for i := 0; i < 2; i++ {
				// seek at the begining of a block
				err := c.Seek(uint(i * b.BlockSize() * 8))
				require.NoError(t, err)

				currentBlock := make([]byte, len(c.currentBlock))
				copy(currentBlock, c.currentBlock)
				// move across the same block, until reaching end.
				for e := 0; e < b.BlockSize()*8; e++ {
					_, err = fn(1)
					require.NoError(t, err)
					assert.Equal(t, currentBlock, c.currentBlock)
				}

				// this next call should trigger a new block generation.
				_, err = fn(1)
				require.NoError(t, err)
				assert.NotEqual(t, currentBlock, c.currentBlock)
			}
		}
	})
	t.Run("seeking within a block, should not create a new cipher block", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		_, _ = aes.NewCipher(pass)
		b := dummyBlock(ctrl)
		c := NewCipher(0, pass, WithBlock(b))

		for i := 0; i < 2; i++ {
			// seek at the begining of a block
			offset := uint(i * b.BlockSize() * 8)
			err := c.Seek(offset)
			require.NoError(t, err)

			currentBlock := make([]byte, len(c.currentBlock))
			copy(currentBlock, c.currentBlock)
			// move across the same block, until reaching end.
			for e := 0; e < b.BlockSize()*8; e++ {
				err = c.Seek(offset + uint(e))
				require.NoError(t, err)
				assert.Equal(t, currentBlock, c.currentBlock)
			}

			// this next call should trigger a new block generation.
			err = c.Seek(offset + uint(b.BlockSize()*8) + uint(1))
			require.NoError(t, err)
			assert.NotEqual(t, currentBlock, c.currentBlock)
		}
	})

	t.Run("visiting previuos blocks should generate equal blocks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		_, _ = aes.NewCipher(pass)
		b := dummyBlock(ctrl)
		c := NewCipher(0, pass, WithBlock(b))
		times := 2
		blocksSeen := make(map[uint][]byte)

		for i := 0; i < times; i++ {
			for e := 0; e < 4; e++ {
				// seek at the begining of a block
				offset := uint(e * b.BlockSize() * 8)
				err := c.Seek(offset)
				require.NoError(t, err)

				currentBlock := make([]byte, len(c.currentBlock))
				copy(currentBlock, c.currentBlock)
				b, found := blocksSeen[offset]
				if found {
					assert.Equal(t, b, c.currentBlock)
				} else {
					blocksSeen[offset] = c.currentBlock
				}
			}
		}
	})
}
