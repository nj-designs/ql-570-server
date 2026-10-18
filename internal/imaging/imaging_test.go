// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/nj-designs/ql-570-server/internal/ql570"
)

// pngData encodes a w×h image, black on the left half and white on the right.
func pngData(t *testing.T, w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{255, 255, 255, 255}
			if x < w/2 {
				c = color.NRGBA{0, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLoadTransparentIsWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{0, 0, 0, 0})
	img.Set(1, 0, color.NRGBA{0, 0, 0, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	g, err := Load(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if g.pix[0] < 254 || g.pix[1] > 1 {
		t.Errorf("pix = %v, want [255 0]", g.pix)
	}
}

func TestLoadRejectsGarbage(t *testing.T) {
	if _, err := Load([]byte("not an image")); err == nil {
		t.Error("accepted garbage")
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		media   string
		w, h    int
		rotate  int
		wantRot int
		wantLen int // 0: media length
	}{
		{"62", 100, 200, Auto, 0, 1392},           // portrait: scaled to 696 wide
		{"62", 400, 100, Auto, 90, 2784},          // landscape: turned to run along the tape
		{"62", 1000, 10, 0, 0, ql570.MinLength},   // short strip padded to minimum length
		{"29x90", 900, 300, Auto, 90, 0},          // landscape image on a long label
		{"62x29", 900, 300, Auto, 0, 0},           // landscape image on a wide label
		{"d24", 50, 50, Auto, 0, 0},               // square
		{"29", 100, 20000, 0, 0, ql570.MaxLength}, // capped at maximum length
		{"62x100", 300, 300, 180, 180, 0},         // explicit rotation
	}
	for _, tt := range tests {
		m := ql570.LookupMedia(tt.media)
		g, err := Load(pngData(t, tt.w, tt.h))
		if err != nil {
			t.Fatal(err)
		}
		bm, rot := Render(g, m, Options{Rotate: tt.rotate, Threshold: 128})
		if rot != tt.wantRot {
			t.Errorf("%s %dx%d: rotation %d, want %d", tt.media, tt.w, tt.h, rot, tt.wantRot)
		}
		wantLen := tt.wantLen
		if wantLen == 0 {
			wantLen = m.Length
		}
		if bm.Width != m.Width || bm.Height != wantLen {
			t.Errorf("%s %dx%d: bitmap %dx%d, want %dx%d", tt.media, tt.w, tt.h, bm.Width, bm.Height, m.Width, wantLen)
			continue
		}
		if _, err := ql570.Encode(m, bm, ql570.JobOptions{}); err != nil {
			t.Errorf("%s: %v", tt.media, err)
		}
		// The preview undoes the rotation, so the image's black left half
		// stays on the left.
		p := Preview(bm, rot)
		b := p.Bounds()
		if p.GrayAt(b.Dx()/2-3, b.Dy()/2).Y != 0 || p.GrayAt(b.Dx()/2+3, b.Dy()/2).Y != 255 {
			t.Errorf("%s %dx%d: preview is not black on the left, white on the right", tt.media, tt.w, tt.h)
		}
	}
}

func TestDither(t *testing.T) {
	g := newGray(100, 100)
	for i := range g.pix {
		g.pix[i] = 128
	}
	bm := mono(g, Options{Dither: true, Threshold: 128})
	black := 0
	for _, v := range bm.Pix {
		black += int(v)
	}
	if black < 4000 || black > 6000 {
		t.Errorf("50%% grey dithered to %d/10000 black", black)
	}
}

func TestResizePreservesFlat(t *testing.T) {
	g := newGray(37, 23)
	for i := range g.pix {
		g.pix[i] = 200
	}
	for _, sz := range [][2]int{{10, 7}, {100, 90}, {37, 5}} {
		r := resize(g, sz[0], sz[1])
		for _, v := range r.pix {
			if v < 199.9 || v > 200.1 {
				t.Fatalf("resize to %v gave %v", sz, v)
			}
		}
	}
}

func TestOrient(t *testing.T) {
	// 2×1 image [a b]; check the top-left pixel after each orientation.
	g := &Gray{w: 2, h: 1, pix: []float32{1, 2}}
	want := map[int]struct {
		w, h int
		tl   float32
	}{
		1: {2, 1, 1}, 2: {2, 1, 2}, 3: {2, 1, 2}, 4: {2, 1, 1},
		5: {1, 2, 1}, 6: {1, 2, 1}, 7: {1, 2, 2}, 8: {1, 2, 2},
	}
	for o, w := range want {
		r := g.orient(o)
		if r.w != w.w || r.h != w.h || r.pix[0] != w.tl {
			t.Errorf("orientation %d: %dx%d tl=%v, want %dx%d tl=%v", o, r.w, r.h, r.pix[0], w.w, w.h, w.tl)
		}
	}
}

func TestExifOrientation(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		tiff := make([]byte, 8+2+12)
		if bo == binary.LittleEndian {
			copy(tiff, "II")
		} else {
			copy(tiff, "MM")
		}
		bo.PutUint16(tiff[2:], 42)
		bo.PutUint32(tiff[4:], 8)
		bo.PutUint16(tiff[8:], 1)
		bo.PutUint16(tiff[10:], 0x0112)
		bo.PutUint16(tiff[12:], 3)
		bo.PutUint32(tiff[14:], 1)
		bo.PutUint16(tiff[18:], 6)

		seg := append([]byte("Exif\x00\x00"), tiff...)
		jpg := []byte{0xff, 0xd8, 0xff, 0xe1}
		jpg = binary.BigEndian.AppendUint16(jpg, uint16(len(seg)+2))
		jpg = append(jpg, seg...)
		jpg = append(jpg, 0xff, 0xda, 0, 2)
		if got := exifOrientation(jpg); got != 6 {
			t.Errorf("%v: orientation %d, want 6", bo, got)
		}
	}
	if got := exifOrientation([]byte{0xff, 0xd8, 0xff, 0xda}); got != 1 {
		t.Errorf("no EXIF: orientation %d, want 1", got)
	}
}
