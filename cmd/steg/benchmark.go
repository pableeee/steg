package main

import (
	"fmt"
	"image"
	"image/draw"
	"math/rand"
	"os"
	"runtime"
	"time"

	"github.com/pableeee/steg/cursors"
	"github.com/spf13/cobra"
)

var (
	benchmarkCmd = &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark parallel vs sequential writing performance",
		Long:  "Benchmarks the performance difference between parallel and sequential writing algorithms for different image sizes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBenchmark()
		},
	}

	benchmarkFlags = struct {
		smallSize   int // Image dimensions for small test (default: 100)
		mediumSize  int // Image dimensions for medium test (default: 500)
		largeSize   int // Image dimensions for large test (default: 2000)
		workers     int // Number of workers for parallel writing (0 = auto)
		iterations  int // Number of benchmark iterations per test
		dataSize    int // Size of random data to write in bytes (0 = use 50% of capacity)
		bitsPerChan int // Bits per channel (default: 1)
	}{}
)

func init() {
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.smallSize, "small", "s", 100, "Small image size (NxN pixels, default: 100).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.mediumSize, "medium", "m", 500, "Medium image size (NxN pixels, default: 500).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.largeSize, "large", "l", 2000, "Large image size (NxN pixels, default: 2000).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.workers, "workers", "w", 0, "Number of workers for parallel writing (0 = auto, default: 0).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.iterations, "iterations", "i", 5, "Number of benchmark iterations per test (default: 5).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.dataSize, "data_size", "d", 0, "Size of random data to write in bytes (0 = use 50%% of capacity, default: 0).",
	)
	benchmarkCmd.Flags().IntVarP(
		&benchmarkFlags.bitsPerChan, "bits_per_channel", "b", 1, "Bits per channel (1-3, default: 1).",
	)

	rootCmd.AddCommand(benchmarkCmd)
}

type benchmarkResult struct {
	algorithm  string
	size       string
	imageSize  int
	dataSize   int
	duration   time.Duration
	throughput float64 // bytes per second
	iterations int
}

func runBenchmark() error {
	if benchmarkFlags.workers < 0 {
		return fmt.Errorf("workers must be >= 0")
	}
	if benchmarkFlags.iterations < 1 {
		return fmt.Errorf("iterations must be >= 1")
	}
	if benchmarkFlags.bitsPerChan < 1 || benchmarkFlags.bitsPerChan > 3 {
		return fmt.Errorf("bits_per_channel must be between 1 and 3")
	}

	// Determine worker count
	workerCount := benchmarkFlags.workers
	if workerCount == 0 {
		workerCount = runtime.NumCPU()
	}

	fmt.Fprintf(os.Stdout, "Benchmark Configuration:\n")
	fmt.Fprintf(os.Stdout, "  Workers: %d\n", workerCount)
	fmt.Fprintf(os.Stdout, "  Iterations per test: %d\n", benchmarkFlags.iterations)
	fmt.Fprintf(os.Stdout, "  Bits per channel: %d\n", benchmarkFlags.bitsPerChan)
	fmt.Fprintf(os.Stdout, "\n")

	// Test sizes
	sizes := []struct {
		name string
		size int
	}{
		{"Small", benchmarkFlags.smallSize},
		{"Medium", benchmarkFlags.mediumSize},
		{"Large", benchmarkFlags.largeSize},
	}

	var results []benchmarkResult

	for _, testSize := range sizes {
		fmt.Fprintf(os.Stdout, "Testing %s (%dx%d) image...\n", testSize.name, testSize.size, testSize.size)

		// Create image
		img := image.NewRGBA(image.Rect(0, 0, testSize.size, testSize.size))

		// Create cursor to determine capacity
		cur := cursors.NewRNGCursor(
			img,
			cursors.UseGreenBit(),
			cursors.UseBlueBit(),
			cursors.WithBitsPerChannel(benchmarkFlags.bitsPerChan),
			cursors.WithSeed(42),
		)

		// Determine data size
		dataSize := benchmarkFlags.dataSize
		if dataSize == 0 {
			// Use 50% of capacity
			capacityBytes := cur.Capacity() / 8
			dataSize = int(capacityBytes / 2)
		}

		// Ensure data size is valid
		maxCapacity := int(cur.Capacity() / 8)
		if dataSize > maxCapacity {
			fmt.Fprintf(os.Stderr, "Warning: data_size %d exceeds capacity %d, using capacity\n", dataSize, maxCapacity)
			dataSize = maxCapacity
		}

		// Generate random data
		rng := rand.New(rand.NewSource(42))
		data := make([]byte, dataSize)
		rng.Read(data)

		fmt.Fprintf(os.Stdout, "  Data size: %d bytes (%.2f KB)\n", dataSize, float64(dataSize)/1024)

		// Benchmark sequential writing
		seqResult := benchmarkSequential(img, data, testSize.name, testSize.size, benchmarkFlags.iterations)
		results = append(results, seqResult)
		fmt.Fprintf(os.Stdout, "  Sequential: %v (%.2f KB/s)\n", seqResult.duration, seqResult.throughput/1024)

		// Benchmark parallel writing
		parResult := benchmarkParallel(img, data, testSize.name, testSize.size, workerCount, benchmarkFlags.iterations, benchmarkFlags.bitsPerChan)
		results = append(results, parResult)
		fmt.Fprintf(os.Stdout, "  Parallel:   %v (%.2f KB/s)\n", parResult.duration, parResult.throughput/1024)

		// Calculate speedup
		speedup := float64(seqResult.duration) / float64(parResult.duration)
		fmt.Fprintf(os.Stdout, "  Speedup:    %.2fx\n", speedup)
		fmt.Fprintf(os.Stdout, "\n")
	}

	// Print summary table
	printSummaryTable(results)

	return nil
}

func benchmarkSequential(img draw.Image, data []byte, sizeName string, imageSize, iterations int) benchmarkResult {
	// Create fresh image for each iteration
	durations := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		// Create a fresh copy of the image
		freshImg := image.NewRGBA(img.Bounds())
		draw.Draw(freshImg, freshImg.Bounds(), img, img.Bounds().Min, draw.Src)

		// Create cursor
		cur := cursors.NewRNGCursor(
			freshImg,
			cursors.UseGreenBit(),
			cursors.UseBlueBit(),
			cursors.WithBitsPerChannel(benchmarkFlags.bitsPerChan),
			cursors.WithSeed(42),
		)

		adapter := cursors.CursorAdapter(cur)

		// Benchmark
		start := time.Now()
		_, err := adapter.Write(data)
		duration := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error in sequential benchmark iteration %d: %v\n", i+1, err)
			continue
		}

		durations = append(durations, duration)
	}

	if len(durations) == 0 {
		fmt.Fprintf(os.Stderr, "All sequential benchmark iterations failed\n")
		return benchmarkResult{}
	}

	// Calculate average
	avgDuration := averageDuration(durations)
	throughput := float64(len(data)) / avgDuration.Seconds()

	return benchmarkResult{
		algorithm:  "Sequential",
		size:       sizeName,
		imageSize:  imageSize,
		dataSize:   len(data),
		duration:   avgDuration,
		throughput: throughput,
		iterations: iterations,
	}
}

func benchmarkParallel(img draw.Image, data []byte, sizeName string, imageSize, workers, iterations, bitsPerChan int) benchmarkResult {
	// Create fresh image for each iteration
	durations := make([]time.Duration, 0, iterations)

	for i := 0; i < iterations; i++ {
		// Create a fresh copy of the image
		freshImg := image.NewRGBA(img.Bounds())
		draw.Draw(freshImg, freshImg.Bounds(), img, img.Bounds().Min, draw.Src)

		// Create cursor
		cur := cursors.NewRNGCursor(
			freshImg,
			cursors.UseGreenBit(),
			cursors.UseBlueBit(),
			cursors.WithBitsPerChannel(bitsPerChan),
			cursors.WithSeed(42),
		)

		adapterInterface := cursors.CursorAdapter(cur)
		adapter := adapterInterface.(*cursors.ReadWriteSeekerAdapter)

		config := cursors.ParallelWriteConfig{
			Enabled:     true,
			WorkerCount: workers,
		}

		// Benchmark
		start := time.Now()
		_, err := adapter.WriteParallel(data, config)
		duration := time.Since(start)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error in parallel benchmark iteration %d: %v\n", i+1, err)
			continue
		}

		durations = append(durations, duration)
	}

	if len(durations) == 0 {
		fmt.Fprintf(os.Stderr, "All parallel benchmark iterations failed\n")
		return benchmarkResult{}
	}

	// Calculate average
	avgDuration := averageDuration(durations)
	throughput := float64(len(data)) / avgDuration.Seconds()

	return benchmarkResult{
		algorithm:  "Parallel",
		size:       sizeName,
		imageSize:  imageSize,
		dataSize:   len(data),
		duration:   avgDuration,
		throughput: throughput,
		iterations: iterations,
	}
}

func averageDuration(durations []time.Duration) time.Duration {
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	return sum / time.Duration(len(durations))
}

func printSummaryTable(results []benchmarkResult) {
	fmt.Fprintf(os.Stdout, "========================================\n")
	fmt.Fprintf(os.Stdout, "Benchmark Summary\n")
	fmt.Fprintf(os.Stdout, "========================================\n")
	fmt.Fprintf(os.Stdout, "\n")

	// Group results by size
	sizeGroups := make(map[string][]benchmarkResult)
	for _, r := range results {
		sizeGroups[r.size] = append(sizeGroups[r.size], r)
	}

	for _, sizeName := range []string{"Small", "Medium", "Large"} {
		if results, ok := sizeGroups[sizeName]; ok && len(results) == 2 {
			seqResult := results[0]
			parResult := results[1]
			if seqResult.algorithm != "Sequential" {
				seqResult, parResult = parResult, seqResult
			}

			speedup := float64(seqResult.duration) / float64(parResult.duration)

			fmt.Fprintf(os.Stdout, "%s Image (%dx%d, %d bytes):\n", sizeName, seqResult.imageSize, seqResult.imageSize, seqResult.dataSize)
			fmt.Fprintf(os.Stdout, "  Sequential:  %12v  %10.2f KB/s\n", seqResult.duration, seqResult.throughput/1024)

			speedupStr := fmt.Sprintf("%.2fx speedup", speedup)
			if speedup < 1.0 {
				speedupStr = fmt.Sprintf("%.2fx slowdown", 1.0/speedup)
			}
			fmt.Fprintf(os.Stdout, "  Parallel:    %12v  %10.2f KB/s  (%s)\n", parResult.duration, parResult.throughput/1024, speedupStr)
			fmt.Fprintf(os.Stdout, "\n")
		}
	}
}
