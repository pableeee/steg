package steg

import "testing"

// TestChunkBoundariesArePixelAligned is the deterministic guard for the
// lost-update bug that firstChunkShift exists to prevent. A boundary that falls
// mid-pixel hands the same pixel to two workers, each of which loads it,
// updates its own bits, and stores it back; the later store discards the other
// worker's bits and the image no longer verifies on decode.
//
// The race window is narrow enough that a round-trip test passes by luck most
// of the time, so this checks the arithmetic directly instead.
func TestChunkBoundariesArePixelAligned(t *testing.T) {
	for _, channels := range []int{1, 2, 3} {
		for _, bitsPerChannel := range []int{1, 2, 4, 8} {
			bitsPerPixel := channels * bitsPerChannel
			alignment := lcmBytes(8, bitsPerPixel)
			chunkSize := int64(alignment * 1024)
			shift := firstChunkShift(bitsPerPixel)

			if shift < 0 || shift >= int64(bitsPerPixel) {
				t.Fatalf("channels=%d bpc=%d: shift %d out of range",
					channels, bitsPerChannel, shift)
			}

			// Boundaries fall at shift, shift+chunkSize, shift+2*chunkSize, ...
			for k := int64(0); k < 16; k++ {
				offset := shift + k*chunkSize
				absBit := int64(payloadStreamOffset)*8 + offset*8
				if absBit%int64(bitsPerPixel) != 0 {
					t.Errorf("channels=%d bpc=%d: boundary %d at bit %d is mid-pixel (bitsPerPixel=%d)",
						channels, bitsPerChannel, k, absBit, bitsPerPixel)
				}
			}
		}
	}
}

// TestFirstChunkShiftKnownValues pins the shift for the configurations that
// actually needed one. 160 mod 3 = 1, so every bitsPerPixel divisible by 3
// requires a one-byte shift; the power-of-two cases divide 160 evenly already.
func TestFirstChunkShiftKnownValues(t *testing.T) {
	for _, tc := range []struct {
		bitsPerPixel int
		want         int64
	}{
		{1, 0}, {2, 0}, {4, 0}, {8, 0}, {16, 0},
		{3, 1}, {6, 1}, {12, 1}, {24, 1},
	} {
		if got := firstChunkShift(tc.bitsPerPixel); got != tc.want {
			t.Errorf("firstChunkShift(%d) = %d, want %d", tc.bitsPerPixel, got, tc.want)
		}
	}
}
