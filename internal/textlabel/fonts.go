// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

// Package textlabel lays out and rasterises Unicode text for a label, using
// the fonts installed on the system with per-character fallback.
package textlabel

import (
	"bytes"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/fontscan"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
)

// Preferred default families, in order. The built-in Go font always exists.
var defaultFamilies = []string{"Noto Sans", "DejaVu Sans", "Liberation Sans", "Go"}

var builtin = []struct {
	family string
	data   []byte
}{
	{"Go", goregular.TTF}, {"Go", gobold.TTF}, {"Go", goitalic.TTF}, {"Go", gobolditalic.TTF},
	{"Go Mono", gomono.TTF}, {"Go Mono", gomonobold.TTF}, {"Go Mono", gomonoitalic.TTF}, {"Go Mono", gomonobolditalic.TTF},
}

// Fonts holds the system font index. Font faces are not safe for concurrent
// use, so every layout holds mu.
type Fonts struct {
	mu        sync.Mutex
	fm        *fontscan.FontMap
	prints    []fontscan.Footprint
	faces     map[fontscan.Location]*font.Face
	fallbacks map[rune]*font.Face
	families  []string
	def       string
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// LoadFonts indexes the system fonts, caching the index in cacheDir, and adds
// the built-in Go fonts. It returns an error only if the built-in fonts fail
// to load; a system without fonts still works.
func LoadFonts(cacheDir string) (*Fonts, error) {
	f := &Fonts{
		fm:        fontscan.NewFontMap(nopLogger{}),
		faces:     map[fontscan.Location]*font.Face{},
		fallbacks: map[rune]*font.Face{},
	}
	if err := f.fm.UseSystemFonts(cacheDir); err == nil {
		f.prints, _ = fontscan.SystemFonts(nopLogger{}, cacheDir)
	}
	for i, b := range builtin {
		if err := f.fm.AddFont(bytes.NewReader(b.data), "builtin:"+strconv.Itoa(i), b.family); err != nil {
			return nil, err
		}
	}
	f.families = f.listFamilies()
	for _, d := range defaultFamilies {
		if slices.Contains(f.families, d) {
			f.def = d
			break
		}
	}
	return f, nil
}

// Families lists the font families suitable for label text, sorted by name.
func (f *Fonts) Families() []string { return f.families }

// Default is the family used when none is requested.
func (f *Fonts) Default() string { return f.def }

// listFamilies reads the display names of system fonts that cover basic
// Latin, skipping emoji and symbol fonts.
func (f *Fonts) listFamilies() []string {
	seen := map[string]bool{"Go": true, "Go Mono": true}
	names := []string{"Go", "Go Mono"}
	done := map[string]bool{}
	for _, fp := range f.prints {
		if done[fp.Family] || !fp.Runes.Contains('A') || !fp.Runes.Contains('z') || !fp.Runes.Contains('0') {
			continue
		}
		done[fp.Family] = true
		name := describe(fp.Location)
		if name == "" || seen[name] || strings.Contains(strings.ToLower(name), "emoji") {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return names
}

func describe(loc fontscan.Location) string {
	file, err := os.Open(loc.File)
	if err != nil {
		return ""
	}
	defer file.Close()
	lds, err := ot.NewLoaders(file)
	if err != nil || int(loc.Index) >= len(lds) {
		return ""
	}
	d, _ := font.Describe(lds[loc.Index], nil)
	return d.Family
}

// fontmap resolves faces through fontscan, then searches every system font
// for characters the chosen face lacks. fontscan's own fallback can give up
// on characters without a script, such as emoji.
type fontmap struct {
	*fontscan.FontMap
	f *Fonts
}

func (m fontmap) ResolveFace(r rune) *font.Face {
	face := m.FontMap.ResolveFace(r)
	if _, ok := face.NominalGlyph(r); ok || unicode.IsSpace(r) || !unicode.IsGraphic(r) {
		return face
	}
	if fb := m.f.fallback(r); fb != nil {
		return fb
	}
	return face
}

// fallback finds a system face with a glyph for r, preferring outline fonts
// over colour bitmap ones, which print less cleanly.
func (f *Fonts) fallback(r rune) *font.Face {
	if face, ok := f.fallbacks[r]; ok {
		return face
	}
	var face *font.Face
	for _, colour := range []bool{false, true} {
		for _, fp := range f.prints {
			if strings.Contains(fp.Family, "color") != colour || !fp.Runes.Contains(r) {
				continue
			}
			if face = f.load(fp.Location); face != nil {
				break
			}
		}
		if face != nil {
			break
		}
	}
	f.fallbacks[r] = face
	return face
}

func (f *Fonts) load(loc fontscan.Location) *font.Face {
	if face, ok := f.faces[loc]; ok {
		return face
	}
	var face *font.Face
	if data, err := os.ReadFile(loc.File); err == nil {
		if faces, err := font.ParseTTC(bytes.NewReader(data)); err == nil && int(loc.Index) < len(faces) {
			face = faces[loc.Index]
		}
	}
	f.faces[loc] = face
	return face
}
