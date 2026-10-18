// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package imaging

import (
	"bytes"
	"encoding/binary"
	"math"
)

func quarterTurns(deg int) int { return ((deg%360 + 360) % 360) / 90 }

// rot90 turns a w×h grid a quarter turn clockwise, giving an h×w grid.
func rot90[T any](w, h int, pix []T) []T {
	out := make([]T, len(pix))
	for y := range h {
		for x := range w {
			out[x*h+(h-1-y)] = pix[y*w+x]
		}
	}
	return out
}

func flipH[T any](w, h int, pix []T) []T {
	out := make([]T, len(pix))
	for y := range h {
		for x := range w {
			out[y*w+(w-1-x)] = pix[y*w+x]
		}
	}
	return out
}

// rotate turns g clockwise by a multiple of 90 degrees.
func (g *Gray) rotate(deg int) *Gray {
	out := g
	for range quarterTurns(deg) {
		out = &Gray{w: out.h, h: out.w, pix: rot90(out.w, out.h, out.pix)}
	}
	return out
}

func (g *Gray) flip() *Gray { return &Gray{w: g.w, h: g.h, pix: flipH(g.w, g.h, g.pix)} }

// orient applies an EXIF orientation (1–8) so the image displays upright.
func (g *Gray) orient(o int) *Gray {
	switch o {
	case 2:
		return g.flip()
	case 3:
		return g.rotate(180)
	case 4:
		return g.rotate(180).flip()
	case 5:
		return g.rotate(90).flip()
	case 6:
		return g.rotate(90)
	case 7:
		return g.flip().rotate(90)
	case 8:
		return g.rotate(270)
	}
	return g
}

// exifOrientation returns the orientation tag from a JPEG's EXIF data, or 1.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		switch {
		case marker == 0xff: // fill byte
			i++
			continue
		case marker == 0x01 || marker >= 0xd0 && marker <= 0xd8: // no payload
			i += 2
			continue
		case marker == 0xda || marker == 0xd9: // image data starts; no EXIF
			return 1
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	for k := range int(bo.Uint16(t[off:])) {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

type contrib struct {
	start   int
	weights []float32
}

// contributions computes a triangle (bilinear) filter from in to out samples,
// widened when shrinking so every source sample is averaged in.
func contributions(in, out int) []contrib {
	scale := float64(in) / float64(out)
	support := math.Max(1, scale)
	cs := make([]contrib, out)
	for i := range cs {
		center := (float64(i)+0.5)*scale - 0.5
		lo := max(0, int(math.Ceil(center-support)))
		hi := min(in-1, int(math.Floor(center+support)))
		var ws []float32
		var sum float64
		for j := lo; j <= hi; j++ {
			w := max(0, 1-math.Abs(float64(j)-center)/support)
			ws = append(ws, float32(w))
			sum += w
		}
		if sum == 0 {
			j := clamp(int(math.Round(center)), 0, in-1)
			cs[i] = contrib{start: j, weights: []float32{1}}
			continue
		}
		for k := range ws {
			ws[k] /= float32(sum)
		}
		cs[i] = contrib{start: lo, weights: ws}
	}
	return cs
}

// resize resamples g to w×h, horizontally then vertically.
func resize(g *Gray, w, h int) *Gray {
	if w == g.w && h == g.h {
		return g
	}
	tmp := newGray(w, g.h)
	cw := contributions(g.w, w)
	for y := range g.h {
		row := g.pix[y*g.w : (y+1)*g.w]
		out := tmp.pix[y*w : (y+1)*w]
		for x, c := range cw {
			var s float32
			for k, wt := range c.weights {
				s += row[c.start+k] * wt
			}
			out[x] = s
		}
	}
	dst := newGray(w, h)
	for y, c := range contributions(g.h, h) {
		out := dst.pix[y*w : (y+1)*w]
		for k, wt := range c.weights {
			row := tmp.pix[(c.start+k)*w : (c.start+k+1)*w]
			for x := range out {
				out[x] += row[x] * wt
			}
		}
	}
	return dst
}
