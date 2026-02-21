package cipher

import (
	"crypto/aes"
	std_cipher "crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
)

// StreamCipherBlock represents a block cipher in stream mode that supports
// seeking and bitwise encryption and decryption.
type StreamCipherBlock interface {
	Seek(offset int64, whence int) (int64, error)
	EncryptBit(bit uint8) (uint8, error)
	DecryptBit(bit uint8) (uint8, error)
	EncryptByte(b uint8) (uint8, error)
	DecryptByte(b uint8) (uint8, error)
}

var _ StreamCipherBlock = (*streamCipherImpl)(nil)

type streamCipherImpl struct {
	// Cipher attributes
	nonce   uint32
	counter uint32

	currentBlock []byte
	index        int64
	mixIndex     int64
	maxIndex     int64

	block     std_cipher.Block
	blockSize uint32
}

type Block interface {
	std_cipher.Block
}

type Options struct {
	block     std_cipher.Block
	blockSize int
}

type Option func(*Options)

func WithBlock(b std_cipher.Block) Option {
	return func(o *Options) {
		o.block = b
	}
}

// NewCipher creates a new StreamCipherBlock with the given nonce and key.
//
// nonce: A unique nonce for the cipher.
// key: A 16-byte AES-128 key.
//
// Returns a StreamCipherBlock instance and an error.
func NewCipher(nonce uint32, key []byte, options ...Option) (*streamCipherImpl, error) {
	opts := Options{blockSize: 16}
	cb, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewCipher: %w", err)
	}
	opts.block = cb

	for _, opt := range options {
		opt(&opts)
	}

	s := &streamCipherImpl{nonce: nonce, blockSize: uint32(opts.blockSize), block: opts.block}
	s.refreshCipherBlock()
	return s, nil
}

// refreshCipherBlock generates a new cipher block using the current nonce and counter values.
func (s *streamCipherImpl) refreshCipherBlock() {
	counterBytes := make([]byte, 8)
	binary.LittleEndian.PutUint32(counterBytes, s.counter)
	nonceBytes := make([]byte, 8)
	binary.LittleEndian.PutUint32(nonceBytes, s.nonce)

	payload := append(nonceBytes, counterBytes...)
	s.currentBlock = make([]byte, s.blockSize)
	s.block.Encrypt(s.currentBlock, payload)
	s.mixIndex = int64(s.blockSize * s.counter * 8)
	s.maxIndex = int64((s.counter + 1) * s.blockSize * 8)
}

// Seek sets the current position for the next encryption/decryption operation.
//
// n: The position to seek to.
//
// Returns an error if the position is out of range.
func (s *streamCipherImpl) Seek(n int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		if n < 0 {
			return s.index, fmt.Errorf("illegal argument")
		}
	case io.SeekCurrent:
		if n < 0 && (n*-1) > s.index {
			return s.index, fmt.Errorf("illegal argument")
		}
		n = n + s.index
	case io.SeekEnd:
		return 0, fmt.Errorf("not implemented")
	}

	if n > s.maxIndex || n < s.mixIndex {
		s.counter = uint32(n / int64(s.blockSize*8))
		s.refreshCipherBlock()
	}
	s.index = n

	return s.index, nil
}

// EncryptBit encrypts a single bit using the current position of the cipher.
//
// bichi: The bit to encrypt.
//
// Returns the encrypted bit and an error if any.
func (s *streamCipherImpl) EncryptBit(bichi uint8) (uint8, error) {
	return s.processBit(bichi)
}

// DecryptBit decrypts a single bit using the current position of the cipher.
//
// bichi: The bit to decrypt.
//
// Returns the decrypted bit and an error if any.
func (s *streamCipherImpl) DecryptBit(bichi uint8) (uint8, error) {
	return s.processBit(bichi)
}

// EncryptByte encrypts all 8 bits of b in one call, MSB first.
func (s *streamCipherImpl) EncryptByte(b uint8) (uint8, error) {
	var out uint8
	for i := 7; i >= 0; i-- {
		enc, err := s.processBit((b >> i) & 1)
		if err != nil {
			return 0, err
		}
		out |= enc << i
	}
	return out, nil
}

// DecryptByte decrypts all 8 bits of b in one call. CTR mode: identical to EncryptByte.
func (s *streamCipherImpl) DecryptByte(b uint8) (uint8, error) {
	return s.EncryptByte(b)
}

// processBit processes a single bit for encryption or decryption.
func (s *streamCipherImpl) processBit(bichi uint8) (uint8, error) {
	if s.index >= s.maxIndex || s.index < s.mixIndex {
		s.counter = uint32(s.index / int64(s.blockSize*8))
		s.refreshCipherBlock()
	}

	idx := (s.index / 8) % int64(s.blockSize)
	b := s.currentBlock[idx]
	bit := (b >> (s.index % 8)) & 1
	res := bichi ^ bit
	s.index++
	return res, nil
}
