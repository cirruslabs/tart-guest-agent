package imageopt

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptimizeImage(t *testing.T) {
	// Create a test RGBA image
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}

	var rawBuf bytes.Buffer
	err := png.Encode(&rawBuf, img)
	require.NoError(t, err)

	optimized, err := OptimizeImage(rawBuf.Bytes())
	require.NoError(t, err)
	require.NotEmpty(t, optimized)
	require.LessOrEqual(t, len(optimized), MaxRecommendedSize)

	// Verify decoded result matches dimensions
	decoded, format, err := image.Decode(bytes.NewReader(optimized))
	require.NoError(t, err)
	require.Contains(t, []string{"png", "jpeg"}, format)
	require.Equal(t, 200, decoded.Bounds().Dx())
	require.Equal(t, 200, decoded.Bounds().Dy())
}

func TestOptimizeImage_OversizedPayload(t *testing.T) {
	// Exceeds MaxRecommendedSize
	oversized := make([]byte, MaxRecommendedSize+1)
	result, err := OptimizeImage(oversized)
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrOversizedPayload)
}

func TestOptimizeImage_ExcessiveDimensions(t *testing.T) {
	// 8-byte PNG header + IHDR chunk with dimensions 10000x10000 (> MaxDimension)
	var hdr bytes.Buffer
	hdr.Write([]byte("\x89PNG\r\n\x1a\n"))

	ihdrBody := []byte{
		'I', 'H', 'D', 'R',
		0x00, 0x00, 0x27, 0x10, // width: 10000
		0x00, 0x00, 0x27, 0x10, // height: 10000
		0x08, 0x06, 0x00, 0x00, 0x00, // 8-bit truecolor with alpha
	}
	crc := crc32.ChecksumIEEE(ihdrBody)

	// Length (13)
	hdr.Write([]byte{0x00, 0x00, 0x00, 0x0d})
	hdr.Write(ihdrBody)
	// CRC (4 bytes big-endian)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	hdr.Write(crcBytes)

	result, err := OptimizeImage(hdr.Bytes())
	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrInvalidDimensions)
}
