package cursors

import (
	"fmt"
	"image/color"
	"image/draw"
	"runtime"
	"sync"
)

// WriteOperation represents a single bit write operation
type WriteOperation struct {
	PixelX         int
	PixelY         int
	Channel        BitColor
	BitPosition    int // Position within channel (0 to bitsPerChannel-1)
	BitValue       uint8
	CursorPosition int64 // For ordering/verification
}

// PixelWrite represents all write operations for a single pixel
type PixelWrite struct {
	X          int
	Y          int
	Operations []WriteOperation // All bit changes for this pixel
}

// ParallelWriteConfig configures parallel writing behavior
type ParallelWriteConfig struct {
	Enabled     bool
	WorkerCount int // 0 = auto (runtime.NumCPU())
}

// ParallelWriter manages parallel pixel writing using a worker pool
type ParallelWriter struct {
	workerCount int
	taskQueue   chan *PixelWriteTask
	errorChan   chan error
	wg          sync.WaitGroup
}

// PixelWriteTask represents a pixel write task for a worker
type PixelWriteTask struct {
	Pixel          *PixelWrite
	Image          draw.Image
	BitsPerChannel int
}

// NewParallelWriter creates a new ParallelWriter with the specified number of workers
func NewParallelWriter(workerCount int) *ParallelWriter {
	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
	}
	return &ParallelWriter{
		workerCount: workerCount,
		errorChan:   make(chan error, workerCount),
	}
}

// WritePixels writes all pixels in parallel using worker goroutines
func (pw *ParallelWriter) WritePixels(image draw.Image, pixels []*PixelWrite, bitsPerChannel int) error {
	if len(pixels) == 0 {
		return nil
	}

	// Create task queue with buffer
	pw.taskQueue = make(chan *PixelWriteTask, len(pixels))

	// Start workers
	pw.wg.Add(pw.workerCount)
	for i := 0; i < pw.workerCount; i++ {
		go pw.worker(image, bitsPerChannel)
	}

	// Send all tasks to queue
	go func() {
		defer close(pw.taskQueue)
		for _, pixel := range pixels {
			pw.taskQueue <- &PixelWriteTask{
				Pixel:          pixel,
				Image:          image,
				BitsPerChannel: bitsPerChannel,
			}
		}
	}()

	// Wait for all workers to complete
	pw.wg.Wait()
	close(pw.errorChan)

	// Collect any errors
	var errors []error
	for err := range pw.errorChan {
		if err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("parallel write errors: %v", errors)
	}

	return nil
}

// worker processes pixel write tasks from the queue
func (pw *ParallelWriter) worker(image draw.Image, bitsPerChannel int) {
	defer pw.wg.Done()
	for task := range pw.taskQueue {
		if err := writePixelAtomically(task.Image, task.Pixel, task.BitsPerChannel); err != nil {
			// Send error (non-blocking)
			select {
			case pw.errorChan <- err:
			default:
			}
		}
	}
}

// writePixelAtomically reads a pixel, applies all bit modifications, and writes it back atomically
func writePixelAtomically(img draw.Image, pixel *PixelWrite, bitsPerChannel int) error {
	// Read current pixel values
	r, g, b, a := img.At(pixel.X, pixel.Y).RGBA()
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)
	a8 := uint8(a >> 8)

	// Apply all bit modifications for this pixel
	for _, op := range pixel.Operations {
		switch op.Channel {
		case R_Bit:
			r8 = applyBitChange(r8, op.BitPosition, op.BitValue, bitsPerChannel)
		case G_Bit:
			g8 = applyBitChange(g8, op.BitPosition, op.BitValue, bitsPerChannel)
		case B_Bit:
			b8 = applyBitChange(b8, op.BitPosition, op.BitValue, bitsPerChannel)
		default:
			return fmt.Errorf("invalid channel %v for pixel (%d, %d)", op.Channel, pixel.X, pixel.Y)
		}
	}

	// Write pixel once (atomic operation)
	img.Set(pixel.X, pixel.Y, color.RGBA{r8, g8, b8, a8})
	return nil
}

// applyBitChange applies a single bit change to a channel value
func applyBitChange(value uint8, bitPos int, bitValue uint8, bitsPerChannel int) uint8 {
	if bitPos < 0 || bitPos >= bitsPerChannel {
		// Invalid bit position, return unchanged
		return value
	}

	// Clear the bit at the specific position
	clearMask := ^(uint8(1) << bitPos)
	value = value & clearMask

	// Set the bit if needed
	if bitValue == 1 {
		setMask := uint8(1) << bitPos
		value = value | setMask
	}

	return value
}

// groupByPixel groups write operations by pixel coordinates
func groupByPixel(ops []WriteOperation) map[string]*PixelWrite {
	pixelMap := make(map[string]*PixelWrite)

	for _, op := range ops {
		key := fmt.Sprintf("%d,%d", op.PixelX, op.PixelY)
		pixel, exists := pixelMap[key]
		if !exists {
			pixel = &PixelWrite{
				X:          op.PixelX,
				Y:          op.PixelY,
				Operations: make([]WriteOperation, 0),
			}
			pixelMap[key] = pixel
		}
		pixel.Operations = append(pixel.Operations, op)
	}

	return pixelMap
}
