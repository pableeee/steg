package cursors

import "github.com/pableeee/steg/cipher"

type cipherMiddleware struct {
	block cipher.StreamCipherBlock
	next  Cursor
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

func (c *cipherMiddleware) WriteByte(b uint8) error {
	encrypted, err := c.block.EncryptByte(b)
	if err != nil {
		return err
	}
	return c.next.WriteByte(encrypted)
}

func (c *cipherMiddleware) ReadByte() (uint8, error) {
	b, err := c.next.ReadByte()
	if err != nil {
		return 0, err
	}
	return c.block.DecryptByte(b)
}
