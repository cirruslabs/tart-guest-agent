package imageopt

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const (
	// MaxRecommendedSize is 20 MB (well below Electron / Claude Desktop 30MB limit).
	MaxRecommendedSize = 20 * 1024 * 1024
)

// OptimizeImage decodes any incoming image format (TIFF, BMP, PNG, JPEG, GIF)
// and returns a high-fidelity compressed PNG (or high-quality JPEG if oversized).
func OptimizeImage(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return data
	}

	if buf.Len() <= MaxRecommendedSize {
		return buf.Bytes()
	}

	// Fallback to high-quality JPEG if PNG is larger than 20MB
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 88}); err == nil && jpegBuf.Len() < buf.Len() {
		return jpegBuf.Bytes()
	}

	return buf.Bytes()
}
