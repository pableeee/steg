package cursors

import (
	"errors"
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// TestLSBMatchingSeedFailure checks that a seeding failure is returned by the
// constructor (never swallowed, never replaced by a weak source), and that
// cursors which do not need a generator do not consume the seed source.
func TestLSBMatchingSeedFailure(t *testing.T) {
	boom := errors.New("entropy unavailable")
	saved := seedSource
	seedSource = failingReader{boom}
	defer func() { seedSource = saved }()

	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	_, err := NewRNGCursor(img, WithLSBMatching())
	require.ErrorIs(t, err, boom)

	// No option, or more than 1 bit per channel: no generator, no seeding.
	c, err := NewRNGCursor(img)
	require.NoError(t, err)
	require.Nil(t, c.matchRNG)
	c, err = NewRNGCursor(img, WithLSBMatching(), WithBitsPerChannel(2))
	require.NoError(t, err)
	require.Nil(t, c.matchRNG)
}

// TestLSBMatchingPrivateGenerators checks each cursor gets its own generator.
func TestLSBMatchingPrivateGenerators(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	a, err := NewRNGCursor(img, WithLSBMatching())
	require.NoError(t, err)
	b, err := NewRNGCursor(img, WithLSBMatching())
	require.NoError(t, err)
	require.NotNil(t, a.matchRNG)
	require.NotSame(t, a.matchRNG, b.matchRNG)
	require.NotEqual(t, a.matchRNG.Uint64(), b.matchRNG.Uint64(), "independently seeded")
}
