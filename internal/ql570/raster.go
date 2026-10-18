// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package ql570

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	esc           = 0x1b
	invalidateLen = 200
)

// Bitmap is a 1-bit image. Width runs across the print head and Height along
// the feed. Pix holds one byte per pixel: 1 for black, 0 for white.
type Bitmap struct {
	Width, Height int
	Pix           []uint8
}

func NewBitmap(w, h int) *Bitmap {
	return &Bitmap{Width: w, Height: h, Pix: make([]uint8, w*h)}
}

// JobOptions controls how a job is printed.
type JobOptions struct {
	Copies  int
	AutoCut bool // cut after every label; otherwise only at the end of the job
}

// Encode builds the raster byte stream that prints bm on m. The bitmap must
// be exactly m.Width dots wide and, for die-cut and round labels, exactly
// m.Length dots long.
func Encode(m *Media, bm *Bitmap, o JobOptions) ([]byte, error) {
	if bm.Width != m.Width {
		return nil, fmt.Errorf("bitmap is %d dots wide, %s needs %d", bm.Width, m.Name, m.Width)
	}
	if m.Kind == Continuous {
		if bm.Height < MinLength || bm.Height > MaxLength {
			return nil, fmt.Errorf("bitmap is %d dots long, continuous labels must be %d–%d", bm.Height, MinLength, MaxLength)
		}
	} else if bm.Height != m.Length {
		return nil, fmt.Errorf("bitmap is %d dots long, %s needs %d", bm.Height, m.Name, m.Length)
	}
	if o.Copies < 1 {
		o.Copies = 1
	}

	raster := rasterLines(m, bm)
	var b bytes.Buffer
	b.Grow(invalidateLen + o.Copies*(len(raster)+64))
	b.Write(make([]byte, invalidateLen)) // invalidate: flush any partial command
	b.Write([]byte{esc, '@'})            // initialise

	for page := range o.Copies {
		b.Write(printInfo(m, bm.Height, page == 0))

		var mode byte
		if o.AutoCut {
			mode = 0x40
		}
		b.Write([]byte{esc, 'i', 'M', mode}) // various mode: auto cut
		if o.AutoCut {
			b.Write([]byte{esc, 'i', 'A', defaultCut}) // cut every n labels
		}
		b.Write([]byte{esc, 'i', 'K', 0x08}) // expanded mode: cut at end
		b.Write([]byte{esc, 'i', 'd', byte(m.feedMargin), byte(m.feedMargin >> 8)})

		b.Write(raster)
		if page == o.Copies-1 {
			b.WriteByte(0x1a) // print, end of job
		} else {
			b.WriteByte(0x0c) // print, more pages follow
		}
	}
	return b.Bytes(), nil
}

// printInfo builds the ESC i z command describing the media and page.
func printInfo(m *Media, lines int, first bool) []byte {
	const (
		validKind    = 0x02
		validWidth   = 0x04
		validLength  = 0x08
		validQuality = 0x40
		recovery     = 0x80
	)
	kind := byte(0x0a) // continuous
	if m.Kind != Continuous {
		kind = 0x0b // die-cut, including round labels
	}
	var page byte
	if !first {
		page = 1
	}
	cmd := []byte{esc, 'i', 'z',
		recovery | validQuality | validKind | validWidth | validLength,
		kind, byte(m.WidthMM), byte(m.LengthMM),
		0, 0, 0, 0, // raster line count, filled below
		page, 0}
	binary.LittleEndian.PutUint32(cmd[7:11], uint32(lines))
	return cmd
}

// rasterLines encodes every row of bm as a 'g' raster command. The head's pin
// order is mirrored relative to the label, and the printable area starts
// m.offsetR pins in from the edge.
func rasterLines(m *Media, bm *Bitmap) []byte {
	const cmdLen = 3 + lineBytes
	out := make([]byte, bm.Height*cmdLen)
	for y := range bm.Height {
		line := out[y*cmdLen : (y+1)*cmdLen]
		line[0], line[1], line[2] = 'g', 0x00, lineBytes
		data := line[3:]
		row := bm.Pix[y*bm.Width : (y+1)*bm.Width]
		for x, v := range row {
			if v != 0 {
				pin := m.offsetR + bm.Width - 1 - x
				data[pin/8] |= 0x80 >> (pin % 8)
			}
		}
	}
	return out
}
