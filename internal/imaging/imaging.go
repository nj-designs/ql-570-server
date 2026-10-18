// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

// Package imaging turns uploaded pictures into 1-bit bitmaps sized for a
// label.
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"slices"

	"github.com/nj-designs/ql-570-server/internal/ql570"
)

const maxPixels = 50_000_000

// Gray is a greyscale image with values from 0 (black) to 255 (white).
type Gray struct {
	w, h int
	pix  []float32
}

func newGray(w, h int) *Gray { return &Gray{w: w, h: h, pix: make([]float32, w*h)} }

// Load decodes a PNG, JPEG or GIF, flattens any transparency onto white and
// turns it upright according to its EXIF orientation.
func Load(data []byte) (*Gray, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("not a PNG, JPEG or GIF image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is %d × %d pixels; the limit is %d megapixels", cfg.Width, cfg.Height, maxPixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", format, err)
	}
	g := toGray(img)
	if format == "jpeg" {
		g = g.orient(exifOrientation(data))
	}
	return g, nil
}

// FromImage converts an already decoded image, such as rendered text.
func FromImage(img image.Image) *Gray { return toGray(img) }

func toGray(img image.Image) *Gray {
	b := img.Bounds()
	g := newGray(b.Dx(), b.Dy())
	if gr, ok := img.(*image.Gray); ok {
		for y := range g.h {
			row := gr.Pix[gr.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := range g.w {
				g.pix[y*g.w+x] = float32(row[x])
			}
		}
		return g
	}
	if yc, ok := img.(*image.YCbCr); ok {
		for y := range g.h {
			row := yc.Y[yc.YOffset(b.Min.X, b.Min.Y+y):]
			for x := range g.w {
				g.pix[y*g.w+x] = float32(row[x])
			}
		}
		return g
	}
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, gr, bl, a := img.At(x, y).RGBA()
			// Colours are alpha-premultiplied, so adding the missing
			// coverage composites them onto white.
			bg := 0xffff - a
			lum := 0.299*float64(r+bg) + 0.587*float64(gr+bg) + 0.114*float64(bl+bg)
			g.pix[i] = float32(lum / 257)
			i++
		}
	}
	return g
}

// Options control how an image is fitted and converted to black and white.
type Options struct {
	Rotate    int  // degrees clockwise (0, 90, 180, 270) or Auto
	Dither    bool // Floyd–Steinberg dithering instead of a hard threshold
	Threshold int  // 1–254: greys darker than this print black
}

// Auto rotates the image so its long side matches the label's.
const Auto = -1

// Render scales src to fit m's printable area and converts it to black and
// white. Continuous labels take the image's length. It returns the bitmap and
// the clockwise rotation that was applied.
func Render(src *Gray, m *ql570.Media, o Options) (*ql570.Bitmap, int) {
	rot := o.Rotate
	if rot == Auto {
		rot = autoRotation(src, m)
	}
	img := src.rotate(rot)

	scale := float64(m.Width) / float64(img.w)
	if m.Length > 0 {
		scale = min(scale, float64(m.Length)/float64(img.h))
	} else if float64(img.h)*scale > ql570.MaxLength {
		scale = ql570.MaxLength / float64(img.h)
	}
	w := clamp(int(math.Round(float64(img.w)*scale)), 1, m.Width)
	h := max(1, int(math.Round(float64(img.h)*scale)))
	if m.Length > 0 {
		h = min(h, m.Length)
	} else {
		h = min(h, ql570.MaxLength)
	}
	img = resize(img, w, h)

	length := m.Length
	if length == 0 {
		length = max(h, ql570.MinLength)
	}
	canvas := newGray(m.Width, length)
	for i := range canvas.pix {
		canvas.pix[i] = 255
	}
	x0, y0 := (m.Width-w)/2, (length-h)/2
	for y := range h {
		copy(canvas.pix[(y0+y)*canvas.w+x0:], img.pix[y*w:(y+1)*w])
	}
	return mono(canvas, o), rot
}

func autoRotation(g *Gray, m *ql570.Media) int {
	if g.w == g.h {
		return 0
	}
	imgLandscape := g.w > g.h
	if m.Length == 0 {
		// Continuous tape: run the image's long side along the tape.
		if imgLandscape {
			return 90
		}
		return 0
	}
	if m.Width != m.Length && imgLandscape != (m.Width > m.Length) {
		return 90
	}
	return 0
}

func mono(g *Gray, o Options) *ql570.Bitmap {
	bm := ql570.NewBitmap(g.w, g.h)
	t := float32(o.Threshold)
	if !o.Dither {
		for i, v := range g.pix {
			if v < t {
				bm.Pix[i] = 1
			}
		}
		return bm
	}
	// Floyd–Steinberg error diffusion, with the threshold acting as a
	// darkness control.
	pix := slices.Clone(g.pix)
	for y := range g.h {
		for x := range g.w {
			i := y*g.w + x
			old := pix[i]
			var v float32 = 255
			if old < t {
				v = 0
				bm.Pix[i] = 1
			}
			e := old - v
			if x+1 < g.w {
				pix[i+1] += e * 7 / 16
			}
			if y+1 < g.h {
				if x > 0 {
					pix[i+g.w-1] += e * 3 / 16
				}
				pix[i+g.w] += e * 5 / 16
				if x+1 < g.w {
					pix[i+g.w+1] += e * 1 / 16
				}
			}
		}
	}
	return bm
}

// Preview renders bm as an image, undoing the rotation Render applied so it
// appears the way the original was uploaded.
func Preview(bm *ql570.Bitmap, rotation int) *image.Gray {
	pix := make([]uint8, len(bm.Pix))
	for i, v := range bm.Pix {
		if v == 0 {
			pix[i] = 255
		}
	}
	w, h := bm.Width, bm.Height
	for range quarterTurns(360 - rotation) {
		pix = rot90(w, h, pix)
		w, h = h, w
	}
	return &image.Gray{Pix: pix, Stride: w, Rect: image.Rect(0, 0, w, h)}
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
