package cursors_test

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
)

func fullOrder(t *testing.T, w, h, publicLen int, key [32]byte) []image.Point {
	t.Helper()
	points, err := cursors.NewPixelOrder(w, h, publicLen)
	require.NoError(t, err)
	cursors.FinishPixelOrder(points, publicLen, key)
	return points
}

// TestPixelOrderGolden pins the traversal order, which is part of the on-image
// format. If this fails, the change breaks every existing encoded image: either
// revert it or treat it as a deliberate format bump and update the vectors.
func TestPixelOrderGolden(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	// 43 = ceil(128/3): the salt prefix length at the default 3 bits/pixel.
	points := fullOrder(t, 64, 48, 43, key)

	pts := func(xy ...int) []image.Point {
		out := make([]image.Point, 0, len(xy)/2)
		for i := 0; i < len(xy); i += 2 {
			out = append(out, image.Pt(xy[i], xy[i+1]))
		}
		return out
	}
	// Public prefix (key-independent).
	require.Equal(t, pts(40, 26, 54, 8, 59, 8, 44, 32, 45, 23, 33, 35, 11, 23, 49, 20), points[:8])
	// Last public position and first secret one.
	require.Equal(t, pts(19, 12, 62, 35), points[42:44])
	// Tail of the secret remainder.
	require.Equal(t, pts(27, 26, 16, 3, 44, 12, 50, 32, 31, 45, 11, 32, 0, 40, 32, 41), points[len(points)-8:])
}

// TestPixelOrderIsPermutation checks every pixel appears exactly once, for
// several shapes and prefix lengths including the degenerate ones.
func TestPixelOrderIsPermutation(t *testing.T) {
	for _, tc := range []struct{ w, h, k int }{
		{1, 1, 0}, {1, 1, 1}, {1, 2, 1}, {2, 1, 2}, {1, 43, 43}, {13, 1, 5},
		{7, 3, 0}, {3, 7, 21}, {64, 48, 43}, {100, 37, 128}, {37, 100, 16},
	} {
		points := fullOrder(t, tc.w, tc.h, tc.k, [32]byte{1, 2, 3})
		require.Len(t, points, tc.w*tc.h)
		seen := make(map[image.Point]bool, len(points))
		for _, p := range points {
			require.True(t, p.In(image.Rect(0, 0, tc.w, tc.h)), "%dx%d: %v out of range", tc.w, tc.h, p)
			require.False(t, seen[p], "%dx%d k=%d: %v appears twice", tc.w, tc.h, tc.k, p)
			seen[p] = true
		}
	}
}

// TestPixelOrderPublicPrefix checks the first publicLen positions depend only
// on the dimensions (so a decoder can find the salt without a key) while the
// remainder depends on the key.
func TestPixelOrderPublicPrefix(t *testing.T) {
	for _, tc := range []struct{ w, h, k int }{
		{64, 48, 43}, {200, 3, 128}, {9, 9, 16},
	} {
		noKey, err := cursors.NewPixelOrder(tc.w, tc.h, tc.k)
		require.NoError(t, err)
		a := fullOrder(t, tc.w, tc.h, tc.k, [32]byte{0xaa})
		b := fullOrder(t, tc.w, tc.h, tc.k, [32]byte{0xbb})

		require.Equal(t, noKey[:tc.k], a[:tc.k])
		require.Equal(t, noKey[:tc.k], b[:tc.k])
		require.NotEqual(t, a[tc.k:], b[tc.k:])
	}
}

func TestNewPixelOrderRejectsTooFewPixels(t *testing.T) {
	_, err := cursors.NewPixelOrder(6, 7, 43)
	require.Error(t, err)
	_, err = cursors.NewPixelOrder(0, 7, 0)
	require.Error(t, err)

	_, err = cursors.NewPixelOrder(1, 43, 43)
	require.NoError(t, err)
}
