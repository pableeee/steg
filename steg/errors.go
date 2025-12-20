package steg

import "fmt"

// ErrInsufficientCapacity is returned when the image doesn't have enough space
// to encode the payload.
type ErrInsufficientCapacity struct {
	RequiredBytes int64
	AvailableBytes int64
}

func (e *ErrInsufficientCapacity) Error() string {
	return fmt.Sprintf("insufficient capacity: need %d bytes, have %d bytes (required: %.2f%% more capacity)", 
		e.RequiredBytes, e.AvailableBytes, 
		float64(e.RequiredBytes-e.AvailableBytes)*100.0/float64(e.AvailableBytes))
}

// ErrInvalidFormat is returned when the image format is not supported or invalid.
type ErrInvalidFormat struct {
	Format string
	Reason string
}

func (e *ErrInvalidFormat) Error() string {
	return fmt.Sprintf("invalid image format '%s': %s", e.Format, e.Reason)
}
