package imageopt

import (
	"bytes"
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

	optimized := OptimizeImage(rawBuf.Bytes())
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
	result := OptimizeImage(oversized)
	require.Equal(t, len(oversized), len(result))
}

func TestOptimizeImage_ExcessiveDimensions(t *testing.T) {
	// 8-byte PNG header + IHDR chunk with dimensions 10000x10000 (> MaxDimension)
	var hdr bytes.Buffer
	hdr.Write([]byte("\x89PNG\r\n\x1a\n"))
	// IHDR chunk: length (13), chunk type (IHDR), width (10000), height (10000), bit depth (8), color type (6), comp (0), filter (0), interlace (0)
	ihdrData := []byte{
		0x00, 0x00, 0x00, 0x0d, // length
		'I', 'H', 'D', 'R',
		0x00, 0x00, 0x27, 0x10, // width: 10000
		0x00, 0x00, 0x27, 0x10, // height: 10000
		0x08, 0x06, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, // dummy crc
	}
	hdr.Write(ihdrData)

	result := OptimizeImage(hdr.Bytes())
	// Should return input without trying to allocate/decode a 10000x10000 raster
	require.Equal(t, hdr.Bytes(), result)
}
