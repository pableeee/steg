package cursors

import "github.com/pableeee/steg/cipher"

type cipherMiddleware struct {
	block cipher.StreamCipherBlock
	next  Cursor
}

// GetUnderlyingCursor returns the underlying cursor, unwrapping cipherMiddleware if present
func GetUnderlyingCursor(c Cursor) (Cursor, cipher.StreamCipherBlock) {
	if cm, ok := c.(*cipherMiddleware); ok {
		return cm.next, cm.block
	}
	return c, nil
}

var _ Cursor = (*cipherMiddleware)(nil)

func CipherMiddleware(c Cursor, block cipher.StreamCipherBlock) Cursor {
	return &cipherMiddleware{
		next:  c,
		block: block,
	}
}

func (c *cipherMiddleware) Seek(n int64, whence int) (int64, error) {
	_, err := c.block.Seek(n, whence)
	if err != nil {
		return 0, err
	}

	n, err = c.next.Seek(n, whence)
	if err != nil {
		return 0, err
	}

	return n, nil
}

func (c *cipherMiddleware) WriteBit(bit uint8) (uint, error) {
	b, err := c.block.EncryptBit(bit)
	if err != nil {
		return 0, err
	}

	return c.next.WriteBit(b)
}
func (c *cipherMiddleware) ReadBit() (uint8, error) {

	b, err := c.next.ReadBit()
	if err != nil {
		return 0, err
	}

	b, err = c.block.DecryptBit(b)
	if err != nil {
		return 0, err
	}

	return b, err
}

func (c *cipherMiddleware) Capacity() int64 {
	return c.next.Capacity()
}
