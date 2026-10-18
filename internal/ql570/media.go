// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

// Package ql570 drives a Brother QL-570 label printer through the Linux
// usblp device (/dev/usb/lpN) using Brother's raster command set.
package ql570

// Print head geometry at 300 dpi.
const (
	DPI        = 300
	HeadPins   = 720
	lineBytes  = HeadPins / 8
	MinLength  = 150   // shortest continuous label, in dots
	MaxLength  = 11811 // longest continuous label, in dots (1 m)
	defaultCut = 1
)

// Kind is the physical form of a label roll.
type Kind int

const (
	Continuous Kind = iota
	DieCut
	Round
)

func (k Kind) String() string {
	switch k {
	case Continuous:
		return "continuous"
	case DieCut:
		return "die-cut"
	case Round:
		return "round"
	}
	return "unknown"
}

func (k Kind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// Media describes a label roll the QL-570 can print on. Width runs across the
// print head and Length along the feed; both printable sizes are in dots.
type Media struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     Kind     `json:"kind"`
	WidthMM  int      `json:"widthMm"`
	LengthMM int      `json:"lengthMm"`   // 0 for continuous rolls
	Width    int      `json:"widthDots"`  // printable dots across the head
	Length   int      `json:"lengthDots"` // printable dots along the feed, 0 for continuous rolls
	Products []string `json:"products"`

	offsetR    int // unused head pins to the right of the printable area
	feedMargin int // dots fed before and after the image
}

// Geometry from Brother's QL raster command reference (as tabulated by the
// brother_ql project). The 102 mm media needs the wider QL-1050 head.
var media = []Media{
	{ID: "12", Name: "12 mm continuous", Kind: Continuous, WidthMM: 12, Width: 106, offsetR: 29, feedMargin: 35, Products: []string{"DK-22214"}},
	{ID: "29", Name: "29 mm continuous", Kind: Continuous, WidthMM: 29, Width: 306, offsetR: 6, feedMargin: 35, Products: []string{"DK-22210", "DK-22211"}},
	{ID: "38", Name: "38 mm continuous", Kind: Continuous, WidthMM: 38, Width: 413, offsetR: 12, feedMargin: 35, Products: []string{"DK-22225"}},
	{ID: "50", Name: "50 mm continuous", Kind: Continuous, WidthMM: 50, Width: 554, offsetR: 12, feedMargin: 35, Products: []string{"DK-22223"}},
	{ID: "54", Name: "54 mm continuous", Kind: Continuous, WidthMM: 54, Width: 590, offsetR: 0, feedMargin: 35, Products: []string{"DK-N55224"}},
	{ID: "62", Name: "62 mm continuous", Kind: Continuous, WidthMM: 62, Width: 696, offsetR: 12, feedMargin: 35, Products: []string{"DK-22205", "DK-22212", "DK-22113", "DK-22606", "DK-44205", "DK-44605"}},

	{ID: "17x54", Name: "17 × 54 mm", Kind: DieCut, WidthMM: 17, LengthMM: 54, Width: 165, Length: 566, offsetR: 0, Products: []string{"DK-11204"}},
	{ID: "17x87", Name: "17 × 87 mm", Kind: DieCut, WidthMM: 17, LengthMM: 87, Width: 165, Length: 956, offsetR: 0, Products: []string{"DK-11203"}},
	{ID: "23x23", Name: "23 × 23 mm", Kind: DieCut, WidthMM: 23, LengthMM: 23, Width: 202, Length: 202, offsetR: 42, Products: []string{"DK-11221"}},
	{ID: "29x42", Name: "29 × 42 mm", Kind: DieCut, WidthMM: 29, LengthMM: 42, Width: 306, Length: 425, offsetR: 6},
	{ID: "29x90", Name: "29 × 90 mm", Kind: DieCut, WidthMM: 29, LengthMM: 90, Width: 306, Length: 991, offsetR: 6, Products: []string{"DK-11201"}},
	{ID: "38x90", Name: "38 × 90 mm", Kind: DieCut, WidthMM: 38, LengthMM: 90, Width: 413, Length: 991, offsetR: 12, Products: []string{"DK-11208"}},
	{ID: "39x48", Name: "39 × 48 mm", Kind: DieCut, WidthMM: 39, LengthMM: 48, Width: 425, Length: 495, offsetR: 6},
	{ID: "52x29", Name: "52 × 29 mm", Kind: DieCut, WidthMM: 52, LengthMM: 29, Width: 578, Length: 271, offsetR: 0},
	{ID: "62x29", Name: "62 × 29 mm", Kind: DieCut, WidthMM: 62, LengthMM: 29, Width: 696, Length: 271, offsetR: 12, Products: []string{"DK-11209"}},
	{ID: "62x100", Name: "62 × 100 mm", Kind: DieCut, WidthMM: 62, LengthMM: 100, Width: 696, Length: 1109, offsetR: 12, Products: []string{"DK-11202"}},

	{ID: "d12", Name: "Ø 12 mm round", Kind: Round, WidthMM: 12, LengthMM: 12, Width: 94, Length: 94, offsetR: 113, feedMargin: 35, Products: []string{"DK-11219"}},
	{ID: "d24", Name: "Ø 24 mm round", Kind: Round, WidthMM: 24, LengthMM: 24, Width: 236, Length: 236, offsetR: 42, Products: []string{"DK-11218"}},
	{ID: "d58", Name: "Ø 58 mm round", Kind: Round, WidthMM: 58, LengthMM: 58, Width: 618, Length: 618, offsetR: 51, Products: []string{"DK-11207"}},
}

// AllMedia returns every supported label type.
func AllMedia() []Media { return media }

// LookupMedia returns the media with the given ID, or nil.
func LookupMedia(id string) *Media {
	for i := range media {
		if media[i].ID == id {
			return &media[i]
		}
	}
	return nil
}

// mediaFor matches the media dimensions a printer reports in its status.
func mediaFor(continuous bool, widthMM, lengthMM int) *Media {
	for i := range media {
		m := &media[i]
		if (m.Kind == Continuous) != continuous || m.WidthMM != widthMM {
			continue
		}
		if continuous || m.LengthMM == lengthMM {
			return m
		}
	}
	return nil
}
