package cipher

import (
	"bytes"
	"crypto/aes"
	std_cipher "crypto/cipher"
	"encoding/binary"
	"fmt"
)

// thanks to: https://github.com/go-web/tokenizer/blob/master/pkcs7.go
// n is the block size. The size of the result is x times n, where x
// is at least 1.
func pkcs7Pad(b []byte, blocksize int) ([]byte, error) {
	if blocksize <= 0 {
		return nil, fmt.Errorf("invalid blocksize")
	}

	if b == nil || len(b) == 0 {
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

type StreamCipherBlock interface {
	Seek(n uint) error
	EncryptBit(bit uint8) (uint8, error)
	DecryptBit(bit uint8) (uint8, error)
}

type streamCipherImpl struct {
	// Cipher attributes
	nonce   uint32
	counter uint32

	currentBlock []byte
	index        uint32
	mixIndex     uint32
	maxIndex     uint32

	block     std_cipher.Block
	blockSize uint32
}

func NewCipher(nonce uint32, pass []byte) StreamCipherBlock {
	s := &streamCipherImpl{nonce: nonce, blockSize: 16}
	pass, _ = pkcs7Pad(pass, int(s.blockSize))
	cb, _ := aes.NewCipher(pass)
	s.block = cb
	s.getCipherBlock()

	return s
}

func (s *streamCipherImpl) getCipherBlock() {
	counterBytes := make([]byte, 8)
	binary.LittleEndian.PutUint32(counterBytes, s.counter)
	nonceBytes := make([]byte, 8)
	binary.LittleEndian.PutUint32(nonceBytes, s.nonce)

	payload := append(nonceBytes, counterBytes...)
	s.currentBlock = make([]byte, s.blockSize)
	s.block.Encrypt(s.currentBlock, payload)
	s.mixIndex = s.blockSize * s.counter * 8
	s.maxIndex = (s.counter + 1) * s.blockSize * 8
}

func (s *streamCipherImpl) Seek(n uint) error {
	if n > uint(s.maxIndex) || n < uint(s.mixIndex) {
		s.counter = uint32(n / uint(s.blockSize*8))

		s.getCipherBlock()

	}
	s.index = uint32(n)

	return nil
}

func (s *streamCipherImpl) EncryptBit(bichi uint8) (uint8, error) {
	if uint(s.index) > uint(s.maxIndex) || uint(s.index) < uint(s.mixIndex) {
		s.counter = uint32(uint(s.index) / uint(s.blockSize*8))

		s.getCipherBlock()
	}

	idx := (s.index / 8) % s.blockSize
	b := s.currentBlock[idx]
	bit := b >> (s.index % 8) & 1
	res := bichi ^ bit
	s.index++

	return res, nil
}
func (s *streamCipherImpl) DecryptBit(bichi uint8) (uint8, error) {
	if uint(s.index) > uint(s.maxIndex) || uint(s.index) < uint(s.mixIndex) {
		s.counter = uint32(uint(s.index) / uint(s.blockSize*8))

		s.getCipherBlock()
	}

	idx := (s.index / 8) % s.blockSize
	b := s.currentBlock[idx]
	bit := b >> (s.index % 8) & 1
	res := bichi ^ bit
	s.index++

	return res, nil
}
