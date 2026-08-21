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
