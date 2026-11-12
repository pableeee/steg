package steg

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image/draw"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func TestPaddedSize(t *testing.T) {
	const big = 1 << 30 // capacity far above every bucket below
	for _, tc := range []struct {
		name              string
		realLen, capacity int
		want              int
	}{
		{"empty", 0, big, 64},
		{"one_byte", 1, big, 64},
		{"last_in_min_bucket", 60, big, 64}, // 4 + 60 = 64
		{"first_over_min_bucket", 61, big, 128},
		{"tiny", 100, big, 128},
		{"exact_4096", 4092, big, 4096}, // 4 + 4092 = 4096
		{"just_over_4096", 4093, big, 8192},
		{"exact_8192", 8188, big, 8192}, // 4 + 8188 = 8192
		{"just_over_8192", 8189, big, 16384},
		{"exact_2^20", 1<<20 - 4, big, 1 << 20},
		{"just_over_2^20", 1<<20 - 3, big, 1 << 21},
		{"exact_2^30", 1<<30 - 4, big, 1 << 30},
		{"exact_2^30_at_capacity", 1<<30 - 4, 1<<30 - 4, 1 << 30}, // bucket == capacity + 4

		// Bucket larger than capacity + 4: full fill.
		{"bucket_exceeds_capacity", 4093, 6000, 6004},
		{"bucket_exceeds_capacity_big", 600_000, 1_000_000, 1_000_004}, // bucket 2^20 > 1_000_004
		{"bucket_fits_capacity", 500_000, 1_000_000, 524288},

		// capacity + 4 below the 64-byte floor: always full fill.
		{"tiny_carrier_empty", 0, 7, 11},
		{"tiny_carrier_payload", 5, 7, 11},
		{"tiny_carrier_at_capacity", 7, 7, 11},
		{"capacity_59", 0, 59, 63},
		{"capacity_60", 0, 60, 64}, // capacity + 4 == 64: same either way

		// Small carrier above the floor: buckets until one no longer fits.
		{"small_carrier_empty", 0, 881, 64},
		{"small_carrier_payload", 100, 881, 128},
		{"small_carrier_last_bucket", 508, 881, 512},
		{"small_carrier_over_last_bucket", 509, 881, 885}, // bucket 1024 > 885
		{"small_carrier_at_capacity", 881, 881, 885},

		// Payload equal to capacity: full fill.
		{"at_capacity", 374944, 374944, 374948},
		{"at_capacity_power_of_two_block", 8188, 8188, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := paddedSize(tc.realLen, tc.capacity)
			require.Equal(t, tc.want, got)
			require.GreaterOrEqual(t, got, tc.realLen+4, "block must hold the prefix and the payload")
			require.LessOrEqual(t, got, tc.capacity+4, "block must fit the carrier")
		})
	}
}

// TestPaddedSizeProperties sweeps the invariants: the block holds the payload,
// fits the carrier, and is either a power of two >= MinPaddedBlock or exactly
// full fill.
func TestPaddedSizeProperties(t *testing.T) {
	for _, capacity := range []int{1, 7, 59, 60, 61, 100, 881, 4091, 4092, 4093, 8188, 8189, 10_000, 65_532, 100_000} {
		for realLen := 0; realLen <= capacity; realLen += 1 + realLen/7 {
			got := paddedSize(realLen, capacity)
			require.GreaterOrEqual(t, got, realLen+4)
			require.LessOrEqual(t, got, capacity+4)
			if got != capacity+4 {
				require.GreaterOrEqual(t, got, MinPaddedBlock)
				require.Zero(t, got&(got-1), "capacity %d realLen %d: %d is not a power of two", capacity, realLen, got)
				if got > MinPaddedBlock {
					require.Greater(t, realLen+4, got/2, "bucket is not the smallest that fits")
				}
			}
		}
		require.Equal(t, capacity+4, paddedSize(capacity, capacity), "payload at capacity must fill the carrier")
	}
}

type encodeFn func(draw.Image, []byte, io.Reader, int, int) error
type decodeFn func(draw.Image, []byte, int, int) ([]byte, error)

var (
	encoders = []struct {
		name string
		fn   encodeFn
	}{{"Encode", Encode}, {"EncodeParallel", EncodeParallel}}
	decoders = []struct {
		name string
		fn   decodeFn
	}{{"Decode", Decode}, {"DecodeParallel", DecodeParallel}}
)

func patternPayload(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*131 + 17)
	}
	return p
}

// TestBoundedPaddingRoundTrip covers every encode/decode combination for
// payloads that get the minimum bucket, a larger bucket, an exact bucket and
// full fill. Some configurations make the padded block smaller than a single
// parallel worker chunk, with and without a first-chunk shift.
func TestBoundedPaddingRoundTrip(t *testing.T) {
	pass := []byte("bounded-pass")
	for _, cfg := range []struct {
		name                     string
		w, h                     int
		bitsPerChannel, channels int
	}{
		// capacity 14944; chunk 3072 B, shift 1.
		{"200x200_3ch_1bpc", 200, 200, 1, 3},
		// capacity 25256; chunk 9 × 1024 = 9216 B, larger than the small buckets, shift 7.
		{"150x150_3ch_3bpc", 150, 150, 3, 3},
		// capacity 7850; chunk 5120 B, shift 0.
		{"110x115_1ch_5bpc", 110, 115, 5, 1},
		// capacity 881: every block fits inside one 3072-byte chunk.
		{"50x50_3ch_1bpc", 50, 50, 1, 3},
		// capacity 7: capacity + 4 < MinPaddedBlock, always full fill.
		{"13x13_3ch_1bpc", 13, 13, 1, 3},
	} {
		base := noisyRGBA(cfg.w, cfg.h, 99)
		capacity := CapacityBytes(base, cfg.bitsPerChannel, cfg.channels)
		require.Positive(t, capacity)

		sizes := []int{0, 7, 10, 60, 61, 508, 509, 4092, 4093, 8188, 8189, capacity}
		for _, n := range sizes {
			if n > capacity {
				continue
			}
			payload := patternPayload(n)
			for _, enc := range encoders {
				for _, dec := range decoders {
					name := fmt.Sprintf("%s/len%d/%s_%s", cfg.name, n, enc.name, dec.name)
					t.Run(name, func(t *testing.T) {
						m := cloneRGBA(base)
						require.NoError(t, enc.fn(m, pass, bytes.NewReader(payload), cfg.bitsPerChannel, cfg.channels))
						got, err := dec.fn(m, pass, cfg.bitsPerChannel, cfg.channels)
						require.NoError(t, err)
						require.Equal(t, payload, got)
					})
				}
			}
		}
	}
}

// containerLength decodes the (encrypted) container length field of an
// encoded image, i.e. the padded block length the encoder chose.
func containerLength(t *testing.T, m draw.Image, pass []byte, bpc, ch int) int {
	t.Helper()
	keys, order, err := prepareDecode(m, pass, bpc, ch)
	require.NoError(t, err)
	rw, err := newWorkerStack(m, keys, order, bpc, ch, false)
	require.NoError(t, err)
	_, err = rw.Seek(saltBytes, io.SeekStart)
	require.NoError(t, err)
	var buf [4]byte
	_, err = io.ReadFull(rw, buf[:])
	require.NoError(t, err)
	return int(binary.LittleEndian.Uint32(buf[:]))
}

// TestBoundedPaddingFootprint encodes a small payload into a large carrier and
// checks that only the pixels holding salt, length, padded block and tag can
// change: every pixel outside order[:touched] is byte-identical, and most of
// the image is untouched.
func TestBoundedPaddingFootprint(t *testing.T) {
	pass := []byte("footprint-pass")
	for _, cfg := range []struct {
		bitsPerChannel, channels int
	}{{1, 3}, {2, 3}, {1, 1}, {3, 3}} {
		for _, enc := range encoders {
			t.Run(fmt.Sprintf("%dch_%dbpc_%s", cfg.channels, cfg.bitsPerChannel, enc.name), func(t *testing.T) {
				const w, h = 1000, 1000
				orig := noisyRGBA(w, h, 5)
				m := cloneRGBA(orig)
				payload := patternPayload(100)
				require.NoError(t, enc.fn(m, pass, bytes.NewReader(payload), cfg.bitsPerChannel, cfg.channels))

				bucket := containerLength(t, m, pass, cfg.bitsPerChannel, cfg.channels)
				require.Equal(t, 128, bucket) // 4 + 100 rounds up to 128

				bpp := cfg.bitsPerChannel * cfg.channels
				streamBits := (payloadStreamOffset + bucket + sha256.Size) * 8
				touched := (streamBits + bpp - 1) / bpp

				_, order, err := prepareDecode(m, pass, cfg.bitsPerChannel, cfg.channels)
				require.NoError(t, err)
				inPrefix := make([]bool, w*h)
				for _, idx := range order[:touched] {
					inPrefix[idx] = true
				}

				changed := 0
				for i := 0; i < w*h; i++ {
					same := bytes.Equal(orig.Pix[4*i:4*i+3], m.Pix[4*i:4*i+3])
					if !same {
						changed++
						require.True(t, inPrefix[i], "pixel %d outside the stream prefix was modified", i)
					}
					require.Equal(t, orig.Pix[4*i+3], m.Pix[4*i+3], "alpha of pixel %d changed", i)
				}
				require.LessOrEqual(t, changed, touched)
				// Random data flips each touched pixel with probability >= 1/2.
				require.Greater(t, changed, touched/3, "suspiciously few pixels changed")
				require.Less(t, changed, w*h/20, "most of the image must be untouched")

				got, err := Decode(m, pass, cfg.bitsPerChannel, cfg.channels)
				require.NoError(t, err)
				require.Equal(t, payload, got)
			})
		}
	}
}

// TestBoundedPaddingWrongPassword checks that a bounded image still rejects a
// wrong password cleanly in both decoders.
func TestBoundedPaddingWrongPassword(t *testing.T) {
	for _, enc := range encoders {
		m := noisyRGBA(400, 400, 8)
		require.NoError(t, enc.fn(m, []byte("right"), bytes.NewReader([]byte("short secret")), 1, 3))
		require.Equal(t, MinPaddedBlock, containerLength(t, m, []byte("right"), 1, 3))
		for _, dec := range decoders {
			got, err := dec.fn(m, []byte("wrong"), 1, 3)
			require.Error(t, err, "%s → %s", enc.name, dec.name)
			require.Nil(t, got)
		}
	}
}

// encodeFullFill writes payload the way the encoder did before bounded
// padding: the padded block always covers the whole capacity, whatever the
// payload size. It shares every step with Encode except the block length.
func encodeFullFill(t *testing.T, m draw.Image, pass, payload []byte, bpc, ch int) {
	t.Helper()
	capacity := CapacityBytes(m, bpc, ch)
	padded := make([]byte, capacity+4)
	binary.LittleEndian.PutUint32(padded, uint32(len(payload)))
	copy(padded[4:], payload)
	_, err := rand.Read(padded[4+len(payload):])
	require.NoError(t, err)

	keys, order, err := prepareEncode(m, pass, bpc, ch)
	require.NoError(t, err)
	cur, err := cursors.NewRNGCursor(m, cursorOptions(order, bpc, ch, false)...)
	require.NoError(t, err)
	c, err := cipher.NewCipher(keys.nonce, keys.encKey)
	require.NoError(t, err)
	cm := cursors.CipherMiddleware(cur, c)
	_, err = cm.Seek(saltBytes*8, io.SeekStart)
	require.NoError(t, err)
	require.NoError(t, container.WritePayload(cursors.CursorAdapter(cm), bytes.NewReader(padded), hmac.New(sha256.New, keys.macKey)))
}

// TestFullFillImagesStillDecode checks that the decoders accept images in
// which a small payload was padded to the full capacity, as every image
// written before bounded padding was.
func TestFullFillImagesStillDecode(t *testing.T) {
	pass := []byte("legacy-pass")
	payload := []byte("written with full-capacity padding")
	for _, cfg := range []struct{ bpc, ch int }{{1, 3}, {2, 2}, {3, 3}} {
		base := noisyRGBA(300, 300, 4)
		m := cloneRGBA(base)
		encodeFullFill(t, m, pass, payload, cfg.bpc, cfg.ch)
		require.Equal(t, CapacityBytes(m, cfg.bpc, cfg.ch)+4, containerLength(t, m, pass, cfg.bpc, cfg.ch))
		for _, dec := range decoders {
			got, err := dec.fn(m, pass, cfg.bpc, cfg.ch)
			require.NoError(t, err, "%s %d bpc %d ch", dec.name, cfg.bpc, cfg.ch)
			require.Equal(t, payload, got)
		}
	}
}
