package cursors_test

import (
	"image"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
)

func TestParallelWriteRoundTrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	payload := []byte("parallel writing test data that is reasonably long")

	// Write using parallel mode
	cur := cursors.NewRNGCursor(img,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(42),
	)

	adapterInterface := cursors.CursorAdapter(cur)
	adapter := adapterInterface.(*cursors.ReadWriteSeekerAdapter)
	config := cursors.ParallelWriteConfig{
		Enabled:     true,
		WorkerCount: runtime.NumCPU(),
	}

	_, err := adapter.WriteParallel(payload, config)
	require.NoError(t, err)

	// Read back and verify
	adapterReadInterface := cursors.CursorAdapter(cur)
	adapterRead := adapterReadInterface.(*cursors.ReadWriteSeekerAdapter)
	_, err = adapterRead.Seek(0, 0)
	require.NoError(t, err)

	readBuf := make([]byte, len(payload))
	_, err = adapterRead.Read(readBuf)
	require.NoError(t, err)

	assert.Equal(t, payload, readBuf, "Parallel write should produce same result as sequential")
}

func TestParallelWriteVsSequential(t *testing.T) {
	// Create two identical images
	img1 := image.NewRGBA(image.Rect(0, 0, 50, 50))
	img2 := image.NewRGBA(image.Rect(0, 0, 50, 50))

	// Copy img1 to img2 to ensure they're identical
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img2.Set(x, y, img1.At(x, y))
		}
	}

	payload := []byte("test data for comparison")

	// Write to img1 using sequential
	cur1 := cursors.NewRNGCursor(img1,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(42),
	)
	adapter1 := cursors.CursorAdapter(cur1)
	_, err := adapter1.Write(payload)
	require.NoError(t, err)

	// Write to img2 using parallel
	cur2 := cursors.NewRNGCursor(img2,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(42),
	)
	adapter2Interface := cursors.CursorAdapter(cur2)
	adapter2 := adapter2Interface.(*cursors.ReadWriteSeekerAdapter)
	config := cursors.ParallelWriteConfig{
		Enabled:     true,
		WorkerCount: 2,
	}
	_, err = adapter2.WriteParallel(payload, config)
	require.NoError(t, err)

	// Images should be identical
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			assert.Equal(t, img1.At(x, y), img2.At(x, y),
				"Pixel at (%d, %d) should be identical between sequential and parallel writes",
				x, y)
		}
	}
}

func TestPreComputeWritePlan(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	cur := cursors.NewRNGCursor(img,
		cursors.UseGreenBit(),
		cursors.WithSeed(42),
	)

	bits := []uint8{1, 0, 1, 1, 0, 0, 1, 0}
	ops, err := cur.PreComputeWritePlan(bits)
	require.NoError(t, err)
	require.Len(t, ops, len(bits))

	// Verify operations are valid
	for i, op := range ops {
		assert.GreaterOrEqual(t, op.PixelX, 0)
		assert.Less(t, op.PixelX, 10)
		assert.GreaterOrEqual(t, op.PixelY, 0)
		assert.Less(t, op.PixelY, 10)
		assert.Equal(t, int64(i), op.CursorPosition, "Operation %d should have cursor position %d", i, i)
		assert.Equal(t, bits[i], op.BitValue, "Operation %d should have bit value %d", i, bits[i])
	}

	// Test that grouping works correctly via WritePixelsParallel
	bits2 := []uint8{1, 0, 1, 1}
	err = cur.WritePixelsParallel(bits2, cursors.ParallelWriteConfig{
		Enabled:     true,
		WorkerCount: 2,
	})
	require.NoError(t, err)
}

func TestParallelWriteWithDifferentWorkers(t *testing.T) {
	testCases := []struct {
		name        string
		workerCount int
	}{
		{"1 worker", 1},
		{"2 workers", 2},
		{"4 workers", 4},
		{"auto workers", 0}, // Should use runtime.NumCPU()
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 50, 50))
			payload := []byte("test payload for different worker counts")

			cur := cursors.NewRNGCursor(img,
				cursors.UseGreenBit(),
				cursors.UseBlueBit(),
				cursors.WithSeed(42),
			)

			adapterInterface := cursors.CursorAdapter(cur)
			adapter := adapterInterface.(*cursors.ReadWriteSeekerAdapter)
			config := cursors.ParallelWriteConfig{
				Enabled:     true,
				WorkerCount: tc.workerCount,
			}

			_, err := adapter.WriteParallel(payload, config)
			require.NoError(t, err, "Parallel write with %d workers should succeed", tc.workerCount)

			// Verify we can read it back
			adapterReadInterface := cursors.CursorAdapter(cur)
			adapterRead := adapterReadInterface.(*cursors.ReadWriteSeekerAdapter)
			_, err = adapterRead.Seek(0, 0)
			require.NoError(t, err)

			readBuf := make([]byte, len(payload))
			_, err = adapterRead.Read(readBuf)
			require.NoError(t, err)
			assert.Equal(t, payload, readBuf)
		})
	}
}
