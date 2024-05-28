package cipher

import (
	"bytes"
	"crypto/aes"
	std_cipher "crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
)

// thanks to: https://github.com/go-web/tokenizer/blob/master/pkcs7.go
// pkcs7Pad pads the input byte slice to a multiple of the block size using PKCS#7 padding.
//
// It returns an error if the block size is invalid or the input byte slice is nil or empty.
//
// blocksize: Size of the blocks to pad to.
// b: The byte slice to pad.
//
// Returns the padded byte slice or an error.
func pkcs7Pad(b []byte, blocksize int) ([]byte, error) {
	if blocksize <= 0 {
		return nil, fmt.Errorf("invalid blocksize")
	}

	if len(b) == 0 {
		return nil, fmt.Errorf("invalid byte array")
	}

	if len(b)%blocksize == 0 {
		return b, nil
	}

	n := blocksize - (len(b) % blocksize)
	pb := make([]byte, len(b)+n)

	copy(pb, b)
	copy(pb[len(b):], bytes.Repeat([]byte{byte(n)}, n))

	return pb, nil
}

// StreamCipherBlock represents a block cipher in stream mode that supports
// seeking and bitwise encryption and decryption.
type StreamCipherBlock interface {
	Seek(offset int64, whence int) (int64, error)
	EncryptBit(bit uint8) (uint8, error)
	DecryptBit(bit uint8) (uint8, error)
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

// NewCipher creates a new StreamCipherBlock with the given nonce and passphrase.
//
// nonce: A unique nonce for the cipher.
// pass: The passphrase used to generate the AES key.
//
// Returns a StreamCipherBlock instance.
func NewCipher(nonce uint32, pass []byte, options ...Option) *streamCipherImpl {
	opts := Options{blockSize: 16}
	pass, _ = pkcs7Pad(pass, opts.blockSize)
	cb, _ := aes.NewCipher(pass)
	opts.block = cb

	for _, opt := range options {
		opt(&opts)
	}

	s := &streamCipherImpl{nonce: nonce, blockSize: uint32(opts.blockSize), block: opts.block}

	s.refreshCipherBlock()

	return s
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
		n = n
	case io.SeekCurrent:
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
