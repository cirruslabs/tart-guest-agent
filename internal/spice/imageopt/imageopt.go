package imageopt

import (
	"bytes"
	"errors"
	"fmt"
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

var (
	ErrEmptyData          = errors.New("empty image data")
	ErrOversizedPayload   = errors.New("image payload exceeds maximum recommended size")
	ErrInvalidDimensions  = errors.New("image dimensions exceed safety limits")
	ErrTranscodedTooLarge = errors.New("transcoded PNG output exceeds maximum recommended size")
)

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (lb *limitedBuffer) Write(p []byte) (n int, err error) {
	if lb.buf.Len()+len(p) > lb.limit {
		return 0, ErrTranscodedTooLarge
	}
	return lb.buf.Write(p)
}

// OptimizeImage inspects image dimensions and safely transcodes incoming image formats
// (TIFF, BMP, PNG, JPEG, GIF) into verified PNG bytes. Returns an error if the image
// is invalid or exceeds safety bounds, ensuring unsafe rasters are rejected.
func OptimizeImage(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmptyData
	}
	if len(data) > MaxRecommendedSize {
		return nil, fmt.Errorf("%w: %d > %d", ErrOversizedPayload, len(data), MaxRecommendedSize)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image config: %w", err)
	}

	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxDimension || cfg.Height > MaxDimension || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, fmt.Errorf("%w: width=%d height=%d", ErrInvalidDimensions, cfg.Width, cfg.Height)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image raster: %w", err)
	}

	var lb limitedBuffer
	lb.limit = MaxRecommendedSize
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&lb, img); err != nil {
		if errors.Is(err, ErrTranscodedTooLarge) {
			return nil, fmt.Errorf("%w: transcoded size exceeds %d bytes", ErrOversizedPayload, MaxRecommendedSize)
		}
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}

	return lb.buf.Bytes(), nil
}
