package cipher

import (
	"crypto/aes"
	std_cipher "crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
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
		c, err := NewCipher(0, pass, WithBlock(b))
		require.NoError(t, err)

		funcs := []func(uint8) (uint8, error){
			c.DecryptByte,
			c.EncryptByte,
		}

		for _, fn := range funcs {
			for i := 0; i < 2; i++ {
				// seek at the beginning of a block (in bits)
				_, err := c.Seek(int64(i*b.BlockSize()*8), io.SeekStart)
				require.NoError(t, err)

				currentBlock := make([]byte, len(c.currentBlock))
				copy(currentBlock, c.currentBlock)
				// move across the same block one byte at a time, until reaching end.
				for e := 0; e < b.BlockSize(); e++ {
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
		b := dummyBlock(ctrl)
		c, err := NewCipher(0, pass, WithBlock(b))
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			// seek at the begining of a block
			offset := int64(i * b.BlockSize() * 8)
			_, err := c.Seek(offset, io.SeekStart)
			require.NoError(t, err)

			currentBlock := make([]byte, len(c.currentBlock))
			copy(currentBlock, c.currentBlock)
			// move across the same block, until reaching end.
			for e := int64(0); e < int64(b.BlockSize()*8); e++ {
				_, err = c.Seek(offset+e, io.SeekStart)
				require.NoError(t, err)
				assert.Equal(t, currentBlock, c.currentBlock)
			}

			// this next call should trigger a new block generation.
			_, err = c.Seek(offset+int64(b.BlockSize()*8)+int64(1), io.SeekStart)
			require.NoError(t, err)
			assert.NotEqual(t, currentBlock, c.currentBlock)
		}
	})

	t.Run("visiting previuos blocks should generate equal blocks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		b := dummyBlock(ctrl)
		c, err := NewCipher(0, pass, WithBlock(b))
		require.NoError(t, err)
		times := 2
		blocksSeen := make(map[int64][]byte)

		for i := 0; i < times; i++ {
			for e := 0; e < 4; e++ {
				// seek at the begining of a block
				offset := int64(e * b.BlockSize() * 8)
				_, err := c.Seek(offset, io.SeekStart)
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

// countingBlock wraps a cipher.Block and counts Encrypt calls, so tests can
// check how often the keystream block is regenerated without timing anything.
type countingBlock struct {
	std_cipher.Block
	encrypts int
}

func (c *countingBlock) Encrypt(dst, src []byte) {
	c.encrypts++
	c.Block.Encrypt(dst, src)
}

// expectedKeystreamByte computes keystream byte byteOff directly with AES.
// The counter block is LE32(nonce) || 4 zero bytes || LE32(counter) || 4 zero
// bytes, and the counter is the 16-byte block index.
func expectedKeystreamByte(t *testing.T, key []byte, nonce uint32, byteOff int64) byte {
	t.Helper()
	blk, err := aes.NewCipher(key)
	require.NoError(t, err)
	var in, out [16]byte
	binary.LittleEndian.PutUint32(in[0:4], nonce)
	binary.LittleEndian.PutUint32(in[8:12], uint32(byteOff/16))
	blk.Encrypt(out[:], in[:])
	return out[byteOff%16]
}

// TestKeystreamPast512MiB covers the uint32 overflow in refreshCipherBlock:
// past stream bit 2^32 (byte 512 MiB) the cached block range wrapped, so every
// bit missed the cache and re-ran AES. The keystream must match AES computed
// directly, and consecutive bytes must reuse the cached block.
func TestKeystreamPast512MiB(t *testing.T) {
	key := []byte("YELLOW SUBMARINE")
	const nonce = 0xdeadbeef
	const n = 64

	for _, start := range []int64{
		0,
		(512 << 20) - 8, // straddles bit 2^32
		(600 << 20) + 5, // well past it, not block aligned
		(4 << 30) + 3,   // 4 GiB
	} {
		t.Run(fmt.Sprintf("byte=%d", start), func(t *testing.T) {
			aesBlock, err := aes.NewCipher(key)
			require.NoError(t, err)
			cb := &countingBlock{Block: aesBlock}
			c, err := NewCipher(nonce, key, WithBlock(cb))
			require.NoError(t, err)

			_, err = c.Seek(start*8, io.SeekStart)
			require.NoError(t, err)
			cb.encrypts = 0

			for i := int64(0); i < n; i++ {
				got, err := c.EncryptByte(0)
				require.NoError(t, err)
				// EncryptByte consumes keystream bits LSB first but emits them
				// into MSB-first positions, so the output is bit-reversed.
				want := bits.Reverse8(expectedKeystreamByte(t, key, nonce, start+i))
				require.Equalf(t, want, got, "keystream byte at offset %d", start+i)
			}

			// n bytes touch at most n/16 + 1 blocks; the Seek may already have
			// loaded the first. Anything near n means one AES call per byte.
			assert.LessOrEqual(t, cb.encrypts, n/16+1,
				"keystream block regenerated %d times for %d consecutive bytes", cb.encrypts, n)
		})
	}
}

// TestSeekBeyondCounterRange checks that positions past 2^32 blocks (64 GiB),
// where the 32-bit counter would wrap and reuse keystream, are rejected.
func TestSeekBeyondCounterRange(t *testing.T) {
	c, err := NewCipher(0, []byte("YELLOW SUBMARINE"))
	require.NoError(t, err)

	last := int64(1<<32)*128 - 1 // last bit of block 2^32-1
	_, err = c.Seek(last, io.SeekStart)
	require.NoError(t, err)
	_, err = c.Seek(last+1, io.SeekStart)
	assert.Error(t, err)
}

func benchmarkEncryptAt(b *testing.B, byteOff int64) {
	c, err := NewCipher(1, []byte("YELLOW SUBMARINE"))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := c.Seek(byteOff*8, io.SeekStart); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(1)
	for i := 0; i < b.N; i++ {
		if _, err := c.EncryptByte(0); err != nil {
			b.Fatal(err)
		}
	}
}

// The two benchmarks should report about the same ns/op; before the overflow
// fix the second was ~15x slower.
func BenchmarkEncryptByteStart(b *testing.B)  { benchmarkEncryptAt(b, 0) }
func BenchmarkEncryptByte600MiB(b *testing.B) { benchmarkEncryptAt(b, 600<<20) }
