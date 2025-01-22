package container_test

import (
	"bytes"
	"crypto/md5"
	"io"
	"testing"

	"github.com/pableeee/steg/steg/container"
	"github.com/pableeee/steg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerRoundTrip(t *testing.T) {
	payload := []byte("hello world")
	// Use the in-memory RWS
	buf := testutil.NewMemReadWriteSeeker(nil)

	err := container.WritePayload(buf, bytes.NewReader(payload), md5.New())
	require.NoError(t, err)

	// Reset seek to start of stream
	buf.Seek(0, io.SeekStart)

	readData, err := container.ReadPayload(buf, md5.New())
	require.NoError(t, err)
	assert.Equal(t, payload, readData)
}

func TestEmptyPayload(t *testing.T) {
	payload := []byte{}
	buf := testutil.NewMemReadWriteSeeker(nil)

	err := container.WritePayload(buf, bytes.NewReader(payload), md5.New())
	require.NoError(t, err)

	buf.Seek(0, io.SeekStart)
	readData, err := container.ReadPayload(buf, md5.New())
	require.NoError(t, err)
	assert.Empty(t, readData)
}

func TestChecksumMismatch(t *testing.T) {
	payload := []byte("test payload")
	buf := testutil.NewMemReadWriteSeeker(nil)

	err := container.WritePayload(buf, bytes.NewReader(payload), md5.New())
	require.NoError(t, err)

	// Corrupt the last byte (checksum)
	data := buf.Bytes()
	corruptedData := data[len(data)-1] ^ 0xFF
	buf.Seek(-1, io.SeekEnd)
	buf.Write([]byte{corruptedData})

	buf.Seek(0, io.SeekStart)
	_, err = container.ReadPayload(buf, md5.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksum")
}

func TestTruncatedData(t *testing.T) {
	payload := []byte("test payload")
	buf := testutil.NewMemReadWriteSeeker(nil)

	err := container.WritePayload(buf, bytes.NewReader(payload), md5.New())
	require.NoError(t, err)

	// Truncate the buffer to remove part of the checksum
	err = buf.Truncate(int64((len(buf.Bytes()) - 2)))
	assert.NoError(t, err)

	buf.Seek(0, io.SeekStart)
	_, err = container.ReadPayload(buf, md5.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read")
}

func TestExcessiveLength(t *testing.T) {
	payload := []byte("short")
	buf := testutil.NewMemReadWriteSeeker(nil)

	err := container.WritePayload(buf, bytes.NewReader(payload), md5.New())
	require.NoError(t, err)

	// Increase the length field
	buf.Seek(0, io.SeekStart)
	// length field is in the first 4 bytes (little-endian)
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0x7F})

	buf.Seek(0, io.SeekStart)
	_, err = container.ReadPayload(buf, md5.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read payload")
}
