package imageopt

import (
	"bytes"
	"image"
	"image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const (
	// MaxRecommendedSize is 20 MB (raw encoded byte limit).
	MaxRecommendedSize = 20 * 1024 * 1024
	// MaxDimension is maximum width or height permitted (8192px).
	MaxDimension = 8192
	// MaxPixels is maximum total pixels permitted (8192 * 8192 = 67,108,864 pixels).
	MaxPixels = 8192 * 8192
)

// OptimizeImage inspects image dimensions and decodes incoming image formats
// (TIFF, BMP, PNG, JPEG, GIF) into a compressed PNG if within safe bounds.
func OptimizeImage(data []byte) []byte {
	if len(data) == 0 || len(data) > MaxRecommendedSize {
		return data
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return data
	}

	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxDimension || cfg.Height > MaxDimension || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
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

	return buf.Bytes()
}
