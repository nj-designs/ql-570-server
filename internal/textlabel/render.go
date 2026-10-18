// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package textlabel

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/fontscan"
	"github.com/go-text/typesetting/shaping"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/nj-designs/ql-570-server/internal/ql570"
)

// MaxRunes limits the length of label text.
const MaxRunes = 2000

var ErrEmpty = errors.New("enter some text to print")

type Align int

const (
	Left Align = iota
	Center
	Right
)

// Options control how text is set.
type Options struct {
	Font         string // family name; empty for the default
	Bold, Italic bool
	Align        Align
	Size         float64 // points; 0 makes the text as large as fits
	Invert       bool    // white text on black
	Border       bool
}

// Result is text rendered in reading orientation.
type Result struct {
	Image   *image.Gray
	Rotate  int  // clockwise rotation that maps Image onto the label raster
	Clipped bool // the text does not fit at the requested size
}

// Render sets text for media m. On continuous tape the lines run along the
// tape and the label is as long as the text; on die-cut labels the text is
// set with the label's long side horizontal.
func (f *Fonts) Render(text string, m *ql570.Media, o Options) (*Result, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmpty
	}
	if utf8.RuneCountInString(text) > MaxRunes {
		return nil, fmt.Errorf("text is longer than %d characters", MaxRunes)
	}
	paras := strings.Split(strings.TrimRight(text, "\n"), "\n")

	f.mu.Lock()
	defer f.mu.Unlock()

	family := o.Font
	if family == "" {
		family = f.def
	}
	aspect := font.Aspect{Style: font.StyleNormal, Weight: font.WeightNormal, Stretch: font.StretchNormal}
	if o.Bold {
		aspect.Weight = font.WeightBold
	}
	if o.Italic {
		aspect.Style = font.StyleItalic
	}
	f.fm.SetQuery(fontscan.Query{Families: []string{family, f.def}, Aspect: aspect})
	l := &layouter{fm: fontmap{f.fm, f}, paras: paras}

	fr := newFrame(m, o.Border)
	var b block
	clipped := false
	if o.Size <= 0 {
		// Lay out at a reference size, scale to fit, then nudge down if
		// rounding left it fractionally too big.
		ref := fixed.I(100)
		b = l.layout(ref, noWrap)
		size := fixed.Int26_6(float64(ref) * fr.fitScale(b))
		for range 10 {
			b = l.layout(max(size, fixed.I(1)), noWrap)
			if fr.fits(b) {
				break
			}
			size = size * 97 / 100
		}
	} else {
		size := fixed.Int26_6(o.Size * ql570.DPI / 72 * 64)
		b = l.layout(size, fr.wrapWidth())
		clipped = !fr.fits(b)
	}

	if fr.w == 0 {
		fr.w = max(ql570.MinLength, b.width.Ceil()+2*fr.pad)
	}
	img := fr.draw(b, o)
	return &Result{Image: img, Rotate: fr.rotate, Clipped: clipped}, nil
}

const noWrap = math.MaxInt32

type line struct {
	runs                 []shaping.Output // in visual order
	width                fixed.Int26_6
	ascent, descent, gap fixed.Int26_6 // descent is positive, below the baseline
}

type block struct {
	lines         []line
	width, height fixed.Int26_6
}

type layouter struct {
	fm      fontmap
	paras   []string
	seg     shaping.Segmenter
	shaper  shaping.HarfbuzzShaper
	wrapper shaping.LineWrapper
}

// layout shapes every paragraph at size (in pixels), wrapping lines wider
// than maxWidth.
func (l *layouter) layout(size fixed.Int26_6, maxWidth int) block {
	var b block
	for _, p := range l.paras {
		runes := []rune(p)
		if len(runes) == 0 {
			runes = []rune{' '} // keep blank lines' height
		}
		dir := direction(runes)
		in := shaping.Input{Text: runes, RunEnd: len(runes), Direction: dir, Size: size}
		var outs []shaping.Output
		for _, run := range l.seg.Split(in, l.fm) {
			outs = append(outs, l.shaper.Shape(run))
		}
		cfg := shaping.WrapConfig{Direction: dir, BreakPolicy: shaping.WhenNecessary}
		lines, _ := l.wrapper.WrapParagraph(cfg, maxWidth, runes, shaping.NewSliceIterator(outs))
		for _, ln := range lines {
			b.lines = append(b.lines, newLine(ln, size))
		}
	}
	for i, ln := range b.lines {
		b.width = max(b.width, ln.width)
		b.height += ln.ascent + ln.descent
		if i > 0 {
			b.height += b.lines[i-1].gap
		}
	}
	return b
}

// newLine copies a wrapped line (the wrapper reuses its buffers) and
// measures it.
func newLine(ln shaping.Line, size fixed.Int26_6) line {
	out := line{runs: make([]shaping.Output, len(ln))}
	for i, run := range ln {
		run.Glyphs = slices.Clone(run.Glyphs)
		out.runs[i] = run
		for _, g := range run.Glyphs {
			out.width += g.Advance
		}
		out.ascent = max(out.ascent, run.LineBounds.Ascent)
		out.descent = max(out.descent, -run.LineBounds.Descent)
		out.gap = max(out.gap, run.LineBounds.Gap)
	}
	if len(ln) == 0 {
		out.ascent, out.descent = size*4/5, size/5
	}
	slices.SortFunc(out.runs, func(a, b shaping.Output) int { return int(a.VisualIndex - b.VisualIndex) })
	return out
}

// direction picks right-to-left for a paragraph whose first strong character
// is from a right-to-left script.
func direction(runes []rune) di.Direction {
	for _, r := range runes {
		if unicode.In(r, unicode.Arabic, unicode.Hebrew, unicode.Syriac, unicode.Thaana, unicode.Nko, unicode.Adlam) {
			return di.DirectionRTL
		}
		if unicode.IsLetter(r) {
			return di.DirectionLTR
		}
	}
	return di.DirectionLTR
}

// frame is the label area in reading orientation, in dots.
type frame struct {
	w, h        int // w is 0 for continuous tape until the text is laid out
	rotate      int
	round       bool
	pad         int // text margin, including any border
	borderInset int
	borderWidth int
}

func newFrame(m *ql570.Media, border bool) frame {
	var fr frame
	switch {
	case m.Kind == ql570.Continuous:
		fr.h, fr.rotate = m.Width, 90
	case m.Length > m.Width:
		fr.w, fr.h, fr.rotate = m.Length, m.Width, 90
	default:
		fr.w, fr.h = m.Width, m.Length
	}
	fr.round = m.Kind == ql570.Round
	short := fr.h
	if fr.w > 0 {
		short = min(fr.w, fr.h)
	}
	fr.pad = max(6, short*6/100)
	if border {
		fr.borderInset = max(2, short*2/100)
		fr.borderWidth = max(3, short*25/1000)
		fr.pad += fr.borderInset + fr.borderWidth
	}
	return fr
}

func (fr frame) innerW() float64 { return float64(fr.w - 2*fr.pad) }
func (fr frame) innerH() float64 { return float64(fr.h - 2*fr.pad) }
func (fr frame) radius() float64 { return float64(fr.h)/2 - float64(fr.pad) }

func fl(v fixed.Int26_6) float64 { return float64(v) / 64 }

// fitScale is the factor that makes b exactly fit the frame.
func (fr frame) fitScale(b block) float64 {
	bw, bh := max(fl(b.width), 1), max(fl(b.height), 1)
	switch {
	case fr.w == 0:
		return fr.innerH() / bh
	case fr.round:
		return fr.radius() / math.Hypot(bw/2, bh/2)
	}
	return min(fr.innerW()/bw, fr.innerH()/bh)
}

func (fr frame) fits(b block) bool { return fr.fitScale(b) >= 1 }

func (fr frame) wrapWidth() int {
	switch {
	case fr.w == 0:
		return noWrap
	case fr.round:
		return int(fr.radius() * math.Sqrt2)
	}
	return int(fr.innerW())
}

type bitmapGlyph struct {
	data   font.GlyphBitmap
	bounds image.Rectangle
}

// draw rasterises b centred in the frame, then applies inversion and border.
func (fr frame) draw(b block, o Options) *image.Gray {
	w, h := fr.w, fr.h
	rast := vector.NewRasterizer(w, h)
	var bitmaps []bitmapGlyph

	x0 := (float64(w) - fl(b.width)) / 2
	y := (float64(h) - fl(b.height)) / 2
	for _, ln := range b.lines {
		baseline := y + fl(ln.ascent)
		x := x0 + (fl(b.width)-fl(ln.width))*float64(o.Align)/2
		for _, run := range ln.runs {
			scale := fl(run.Size) / float64(run.Face.Upem())
			for _, g := range run.Glyphs {
				gx, gy := x+fl(g.XOffset), baseline-fl(g.YOffset)
				switch d := run.Face.GlyphData(g.GlyphID).(type) {
				case font.GlyphOutline:
					addOutline(rast, d.Segments, gx, gy, scale)
				case font.GlyphBitmap:
					if d.Outline != nil {
						addOutline(rast, d.Outline.Segments, gx, gy, scale)
					} else if ext, ok := run.Face.GlyphExtents(g.GlyphID); ok {
						r := image.Rect(
							int(gx+float64(ext.XBearing)*scale), int(gy-float64(ext.YBearing)*scale),
							int(gx+float64(ext.XBearing+ext.Width)*scale), int(gy-float64(ext.YBearing+ext.Height)*scale))
						bitmaps = append(bitmaps, bitmapGlyph{d, r})
					}
				}
				x += fl(g.Advance)
			}
		}
		y += fl(ln.ascent + ln.descent + ln.gap)
	}

	cover := image.NewAlpha(image.Rect(0, 0, w, h))
	rast.Draw(cover, cover.Bounds(), image.Opaque, image.Point{})
	img := image.NewGray(cover.Rect)
	for i, a := range cover.Pix {
		img.Pix[i] = 255 - a
	}
	for _, bg := range bitmaps {
		drawBitmap(img, bg)
	}

	ink := uint8(0)
	if o.Invert {
		ink = 255
		for i, v := range img.Pix {
			img.Pix[i] = 255 - v
		}
		if fr.round {
			fr.eachPixel(func(x, y int, d float64) {
				if d >= float64(w)/2 {
					img.Pix[y*w+x] = 255
				}
			})
		}
	}
	if o.Border {
		fr.drawBorder(img, ink)
	}
	return img
}

func addOutline(z *vector.Rasterizer, segs []ot.Segment, x, y, scale float64) {
	pt := func(p ot.SegmentPoint) (float32, float32) {
		return float32(x + float64(p.X)*scale), float32(y - float64(p.Y)*scale)
	}
	for _, s := range segs {
		switch s.Op {
		case ot.SegmentOpMoveTo:
			z.MoveTo(pt(s.Args[0]))
		case ot.SegmentOpLineTo:
			z.LineTo(pt(s.Args[0]))
		case ot.SegmentOpQuadTo:
			bx, by := pt(s.Args[0])
			cx, cy := pt(s.Args[1])
			z.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(s.Args[0])
			cx, cy := pt(s.Args[1])
			dx, dy := pt(s.Args[2])
			z.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	z.ClosePath()
}

// drawBitmap darkens img with a colour bitmap glyph (such as an emoji)
// converted to grey.
func drawBitmap(img *image.Gray, bg bitmapGlyph) {
	if bg.data.Format != font.PNG && bg.data.Format != font.JPG || bg.bounds.Empty() {
		return
	}
	src, _, err := image.Decode(bytes.NewReader(bg.data.Data))
	if err != nil {
		return
	}
	scaled := image.NewRGBA(bg.bounds)
	xdraw.ApproxBiLinear.Scale(scaled, bg.bounds, src, src.Bounds(), xdraw.Src, nil)
	r := bg.bounds.Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := scaled.RGBAAt(x, y)
			// Premultiplied colour over white.
			bgnd := 255 - uint32(c.A)
			lum := uint8((299*(uint32(c.R)+bgnd) + 587*(uint32(c.G)+bgnd) + 114*(uint32(c.B)+bgnd)) / 1000)
			if lum < img.GrayAt(x, y).Y {
				img.SetGray(x, y, color.Gray{lum})
			}
		}
	}
}

// eachPixel calls fn with each pixel's distance from the frame centre.
func (fr frame) eachPixel(fn func(x, y int, d float64)) {
	c := float64(fr.w) / 2
	for y := range fr.h {
		for x := range fr.w {
			fn(x, y, math.Hypot(float64(x)+0.5-c, float64(y)+0.5-float64(fr.h)/2))
		}
	}
}

func (fr frame) drawBorder(img *image.Gray, ink uint8) {
	in, bw := fr.borderInset, fr.borderWidth
	if fr.round {
		outer := float64(fr.w)/2 - float64(in)
		fr.eachPixel(func(x, y int, d float64) {
			if d < outer && d >= outer-float64(bw) {
				img.Pix[y*fr.w+x] = ink
			}
		})
		return
	}
	for y := in; y < fr.h-in; y++ {
		for x := in; x < fr.w-in; x++ {
			if x < in+bw || x >= fr.w-in-bw || y < in+bw || y >= fr.h-in-bw {
				img.Pix[y*fr.w+x] = ink
			}
		}
	}
}
