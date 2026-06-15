// Letterbox resizing for SigLIP2 512×512 input.
//
// SigLIP2 FixRes models require square 512×512 input with the content
// centered and surrounded by padding. The padding color MUST be gray
// (RGB 128, 128, 128), NOT black. Gray corresponds to the normalization
// neutral value (0 in [-1, 1] space), while black normalizes to -1
// which can interfere with neuron activation in padding patches.
//
// This function is for inference preprocessing ONLY, not for cache hashing
// (L1/L2 use their own 480p normalization).

package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"image/jpeg"
	"math"

	xdraw "golang.org/x/image/draw" // CatmullRom scaler
)

// Letterbox resizes an image to targetSize×targetSize while preserving
// aspect ratio. The scaled image is centered; the surrounding area is
// filled with gray (RGB 128,128,128), which is SigLIP2's neutral value.
//
// CatmullRom scaling is used for high-quality downscaling, equivalent to
// Lanczos3 but available in Go's extended standard library.
func Letterbox(src image.Image, targetSize int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if srcW <= 0 || srcH <= 0 {
		return grayCanvas(targetSize)
	}

	// Uniform scale: larger dimension fits into targetSize
	scaleW := float64(targetSize) / float64(srcW)
	scaleH := float64(targetSize) / float64(srcH)
	scale := math.Min(scaleW, scaleH)

	scaledW := int(math.Round(float64(srcW) * scale))
	scaledH := int(math.Round(float64(srcH) * scale))

	// Center the scaled image on a square canvas
	offsetX := (targetSize - scaledW) / 2
	offsetY := (targetSize - scaledH) / 2

	// Canvas: gray. NOT black.
	dst := image.NewRGBA(image.Rect(0, 0, targetSize, targetSize))
	imagedraw.Draw(dst, dst.Bounds(),
		&image.Uniform{color.RGBA{R: 128, G: 128, B: 128, A: 255}},
		image.Point{}, imagedraw.Src)

	// High-quality scaling with CatmullRom (better than Bilinear for downscale)
	scaledRect := image.Rect(offsetX, offsetY, offsetX+scaledW, offsetY+scaledH)
	xdraw.CatmullRom.Scale(dst, scaledRect, src, src.Bounds(), xdraw.Over, nil)

	return dst
}

// LetterboxJPEG applies Letterbox then encodes as JPEG at the given quality.
// Q85 is the standard for SigLIP2: good quality with no visible artifacts
// that could confuse the model.
func LetterboxJPEG(src image.Image, targetSize int, quality int) ([]byte, error) {
	lb := Letterbox(src, targetSize)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, lb, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("letterbox jpeg encode: %w", err)
	}
	return buf.Bytes(), nil
}

// grayCanvas returns a targetSize×targetSize RGBA image filled with gray.
func grayCanvas(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	imagedraw.Draw(img, img.Bounds(),
		&image.Uniform{color.RGBA{R: 128, G: 128, B: 128, A: 255}},
		image.Point{}, imagedraw.Src)
	return img
}
