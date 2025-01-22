package steg

import (
	"crypto/md5"
)

// deriveSeedFromPassword takes a password and returns a reproducible int64 seed.
func deriveSeedFromPassword(pass []byte) (int64, error) {
	hashFn := md5.New()
	if _, err := hashFn.Write(pass); err != nil {
		return 0, err
	}
	seedBytes := hashFn.Sum(nil)

	var seedVal int64
	// Use the first 8 bytes (or fewer, if shorter) of the hash to form a seed.
	for i := 0; i < 8 && i < len(seedBytes); i++ {
		seedVal = (seedVal << 8) | int64(seedBytes[i])
	}

	return seedVal, nil
}
