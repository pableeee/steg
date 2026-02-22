package steg_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/require"
)

// newTestImage creates a 1000x1000 RGBA image with ~375 KB capacity (3 bits/pixel).
func newTestImage() *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, 1000, 1000))
}

// testPayload returns a 100 KB payload for benchmarks.
func testPayload() []byte {
	p := make([]byte, 100*1024)
	for i := range p {
		p[i] = byte(i & 0xff)
	}
	return p
}

// TestParallelRoundTrip verifies EncodeParallel → DecodeParallel round-trip.
func TestParallelRoundTrip(t *testing.T) {
	pass := []byte("testpass")
	payload := []byte("parallel round-trip test payload")
	m := image.NewRGBA(image.Rect(0, 0, 500, 500))

	err := steg.EncodeParallel(m, pass, bytes.NewReader(payload), 1)
	require.NoError(t, err)

	got, err := steg.DecodeParallel(m, pass, 1)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestParallelSequentialInterop verifies EncodeParallel → Decode (sequential) works.
func TestParallelSequentialInterop(t *testing.T) {
	pass := []byte("interoppass")
	payload := []byte("cross-mode interop test payload")
	m := image.NewRGBA(image.Rect(0, 0, 500, 500))

	err := steg.EncodeParallel(m, pass, bytes.NewReader(payload), 1)
	require.NoError(t, err)

	got, err := steg.Decode(m, pass, 1)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestSequentialParallelInterop verifies Encode (sequential) → DecodeParallel works.
func TestSequentialParallelInterop(t *testing.T) {
	pass := []byte("seqparpass")
	payload := []byte("sequential encode, parallel decode")
	m := image.NewRGBA(image.Rect(0, 0, 500, 500))

	err := steg.Encode(m, pass, bytes.NewReader(payload), 1)
	require.NoError(t, err)

	got, err := steg.DecodeParallel(m, pass, 1)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// BenchmarkEncodeSequential benchmarks the sequential Encode function.
// Run with: go test ./steg/ -bench=BenchmarkEncode -benchtime=5s
func BenchmarkEncodeSequential(b *testing.B) {
	pass := []byte("benchpass")
	payload := testPayload()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := newTestImage()
		if err := steg.Encode(m, pass, bytes.NewReader(payload), 1); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodeParallel benchmarks the parallel EncodeParallel function.
func BenchmarkEncodeParallel(b *testing.B) {
	pass := []byte("benchpass")
	payload := testPayload()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := newTestImage()
		if err := steg.EncodeParallel(m, pass, bytes.NewReader(payload), 1); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeSequential benchmarks the sequential Decode function.
func BenchmarkDecodeSequential(b *testing.B) {
	pass := []byte("benchpass")
	payload := testPayload()
	m := newTestImage()
	if err := steg.Encode(m, pass, bytes.NewReader(payload), 1); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := steg.Decode(m, pass, 1); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeParallel benchmarks the parallel DecodeParallel function.
func BenchmarkDecodeParallel(b *testing.B) {
	pass := []byte("benchpass")
	payload := testPayload()
	m := newTestImage()
	if err := steg.EncodeParallel(m, pass, bytes.NewReader(payload), 1); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := steg.DecodeParallel(m, pass, 1); err != nil {
			b.Fatal(err)
		}
	}
}

// sizeCases drives the cross-size benchmarks below.
// Capacity = w*h*3 bits / 8 bytes; payloadBytes must fit with 40 bytes of framing overhead.
//
//	100×100  → 3,750 B capacity  → 1 KB payload
//	500×500  → 93,750 B capacity → 50 KB payload
//	2000×2000 → 1,500,000 B capacity → 500 KB payload
var sizeCases = []struct {
	name         string
	w, h         int
	payloadBytes int
}{
	{"small_100x100_1KB", 100, 100, 1 * 1024},
	{"medium_500x500_50KB", 500, 500, 50 * 1024},
	{"large_2000x2000_500KB", 2000, 2000, 500 * 1024},
	{"4k_3840x2160_2MB", 3840, 2160, 2 * 1024 * 1024},
}

// BenchmarkEncodeBySize compares sequential vs parallel encode across image sizes.
// Run with: go test ./steg/ -bench=BenchmarkEncodeBySize -benchtime=3s
func BenchmarkEncodeBySize(b *testing.B) {
	pass := []byte("benchpass")
	for _, tc := range sizeCases {
		payload := make([]byte, tc.payloadBytes)
		for i := range payload {
			payload[i] = byte(i & 0xff)
		}
		b.Run(tc.name+"/seq", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
				if err := steg.Encode(m, pass, bytes.NewReader(payload), 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/par", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
				if err := steg.EncodeParallel(m, pass, bytes.NewReader(payload), 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDecodeBySize compares sequential vs parallel decode across image sizes.
// Run with: go test ./steg/ -bench=BenchmarkDecodeBySize -benchtime=3s
func BenchmarkDecodeBySize(b *testing.B) {
	pass := []byte("benchpass")
	for _, tc := range sizeCases {
		payload := make([]byte, tc.payloadBytes)
		for i := range payload {
			payload[i] = byte(i & 0xff)
		}

		// Pre-encode so decode benchmarks measure only the decode path.
		mSeq := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		if err := steg.Encode(mSeq, pass, bytes.NewReader(payload), 1); err != nil {
			b.Fatal(err)
		}
		mPar := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		if err := steg.EncodeParallel(mPar, pass, bytes.NewReader(payload), 1); err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/seq", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := steg.Decode(mSeq, pass, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/par", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := steg.DecodeParallel(mPar, pass, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
