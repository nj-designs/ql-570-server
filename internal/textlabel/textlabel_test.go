// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package textlabel

import (
	"image"
	"slices"
	"sync"
	"testing"

	"github.com/nj-designs/ql-570-server/internal/ql570"
)

var (
	fontsOnce sync.Once
	fonts     *Fonts
)

func loadFonts(t *testing.T) *Fonts {
	t.Helper()
	fontsOnce.Do(func() {
		var err error
		if fonts, err = LoadFonts(t.TempDir()); err != nil {
			t.Fatal(err)
		}
	})
	return fonts
}

// ink returns the bounding box of the dark pixels.
func ink(img *image.Gray) image.Rectangle {
	var r image.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.GrayAt(x, y).Y < 128 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

func TestBuiltinFonts(t *testing.T) {
	f := loadFonts(t)
	for _, fam := range []string{"Go", "Go Mono"} {
		if !slices.Contains(f.Families(), fam) {
			t.Errorf("families lack %q", fam)
		}
	}
	if f.Default() == "" {
		t.Error("no default font")
	}
}

func TestFrames(t *testing.T) {
	f := loadFonts(t)
	tests := []struct {
		media      string
		w, h, rot  int
		continuous bool
	}{
		{"62", 0, 696, 90, true},
		{"29x90", 991, 306, 90, false},
		{"62x29", 696, 271, 0, false},
		{"d24", 236, 236, 0, false},
	}
	for _, tt := range tests {
		m := ql570.LookupMedia(tt.media)
		res, err := f.Render("Hello", m, Options{Font: "Go"})
		if err != nil {
			t.Fatal(err)
		}
		b := res.Image.Bounds()
		if res.Rotate != tt.rot || b.Dy() != tt.h || (!tt.continuous && b.Dx() != tt.w) {
			t.Errorf("%s: %v rotated %d, want %dx%d rotated %d", tt.media, b, res.Rotate, tt.w, tt.h, tt.rot)
		}
		if res.Clipped {
			t.Errorf("%s: auto-sized text reported clipped", tt.media)
		}
		// Auto-fit should fill most of the limiting dimension.
		in := ink(res.Image)
		if in.Empty() {
			t.Fatalf("%s: nothing drawn", tt.media)
		}
		if float64(max(in.Dx()*b.Dy(), in.Dy()*b.Dx())) < 0.5*float64(b.Dx()*b.Dy()) {
			t.Errorf("%s: ink %v is small for frame %v", tt.media, in, b)
		}
	}
}

func TestContinuousLengthFollowsText(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("29")
	short, _ := f.Render("Hi", m, Options{Font: "Go"})
	long, _ := f.Render("Hello there, world", m, Options{Font: "Go"})
	if long.Image.Bounds().Dx() <= short.Image.Bounds().Dx() {
		t.Errorf("long text %v not longer than short %v", long.Image.Bounds(), short.Image.Bounds())
	}
	if short.Image.Bounds().Dx() < ql570.MinLength {
		t.Errorf("label shorter than the printer minimum")
	}
}

func TestManualSize(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("62x29")
	small, _ := f.Render("Hi", m, Options{Font: "Go", Size: 10})
	big, _ := f.Render("Hi", m, Options{Font: "Go", Size: 20})
	if small.Clipped || big.Clipped {
		t.Error("small text reported clipped")
	}
	if a, b := ink(small.Image).Dy(), ink(big.Image).Dy(); b < a*3/2 {
		t.Errorf("20pt ink height %d not about twice 10pt %d", b, a)
	}
	huge, _ := f.Render("Hi", m, Options{Font: "Go", Size: 200})
	if !huge.Clipped {
		t.Error("200pt text on a 29 mm label not reported clipped")
	}
	wrapped, _ := f.Render("one two three four five six seven", m, Options{Font: "Go", Size: 14})
	if wrapped.Clipped {
		t.Error("wrapped text reported clipped")
	}
}

func TestAlign(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("62x100")
	text := "a much longer first line\nshort"
	lefts := map[Align]int{}
	for _, a := range []Align{Left, Center, Right} {
		res, _ := f.Render(text, m, Options{Font: "Go", Align: a})
		// Find where ink starts on the bottom (short) line.
		in := ink(res.Image)
		y := in.Max.Y - in.Dy()/6
		for x := range res.Image.Bounds().Dx() {
			if res.Image.GrayAt(x, y).Y < 128 {
				lefts[a] = x
				break
			}
		}
	}
	if !(lefts[Left] < lefts[Center] && lefts[Center] < lefts[Right]) {
		t.Errorf("short line starts at %v; want left < center < right", lefts)
	}
}

func TestInvertAndBorder(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("62x29")
	res, _ := f.Render("x", m, Options{Font: "Go", Invert: true})
	if res.Image.GrayAt(1, 1).Y != 0 {
		t.Error("inverted corner is not black")
	}
	res, _ = f.Render("x", m, Options{Font: "Go", Border: true})
	if c := res.Image.GrayAt(res.Image.Bounds().Dx()/2, 7); c.Y != 0 {
		t.Error("no border at the top edge")
	}
	round, _ := f.Render("x", ql570.LookupMedia("d24"), Options{Font: "Go", Invert: true})
	if round.Image.GrayAt(0, 0).Y != 255 || round.Image.GrayAt(5, 118).Y != 0 {
		t.Error("inverted round label should be black inside the circle only")
	}
}

func TestRejects(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("62")
	if _, err := f.Render(" \n ", m, Options{}); err != ErrEmpty {
		t.Errorf("blank text: %v", err)
	}
	long := make([]rune, MaxRunes+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := f.Render(string(long), m, Options{}); err == nil {
		t.Error("accepted over-long text")
	}
}

// TestScripts needs system fonts with wider coverage than the built-in Go
// font, so it is skipped when they are missing.
func TestScripts(t *testing.T) {
	f := loadFonts(t)
	m := ql570.LookupMedia("62")
	for _, s := range []string{"中文", "مرحبا", "שלום", "नमस्ते"} {
		r := []rune(s)[0]
		face := fontmap{f.fm, f}.ResolveFace(r)
		if _, ok := face.NominalGlyph(r); !ok {
			t.Logf("no font for %q; skipping", s)
			continue
		}
		res, err := f.Render(s, m, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if ink(res.Image).Empty() {
			t.Errorf("%q drew nothing", s)
		}
	}
}
