// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package ql570

import "fmt"

const statusLen = 32

// Status types reported in byte 18 of a status reply.
const (
	StatusReply       = 0x00
	StatusCompleted   = 0x01
	StatusError       = 0x02
	StatusNotice      = 0x05
	StatusPhaseChange = 0x06
)

// Status is a decoded 32-byte status reply.
type Status struct {
	Err1, Err2    byte
	MediaType     byte // 0x0a continuous, 0x0b die-cut, 0 none
	MediaWidthMM  int
	MediaLengthMM int
	Type          byte
	Phase         byte
}

func ParseStatus(b []byte) (*Status, error) {
	if len(b) != statusLen || b[0] != 0x80 || b[1] != statusLen || b[2] != 'B' {
		return nil, fmt.Errorf("malformed status reply % x", b)
	}
	return &Status{
		Err1:          b[8],
		Err2:          b[9],
		MediaWidthMM:  int(b[10]),
		MediaType:     b[11] & 0x0f,
		MediaLengthMM: int(b[17]),
		Type:          b[18],
		Phase:         b[19],
	}, nil
}

// Media returns the loaded media, or nil if none is loaded or it is not
// recognised.
func (s *Status) Media() *Media {
	switch s.MediaType {
	case 0x0a:
		return mediaFor(true, s.MediaWidthMM, 0)
	case 0x0b:
		return mediaFor(false, s.MediaWidthMM, s.MediaLengthMM)
	}
	return nil
}

// MediaDescription describes the loaded media, even when it is not in the
// supported list.
func (s *Status) MediaDescription() string {
	if m := s.Media(); m != nil {
		return m.Name
	}
	switch s.MediaType {
	case 0x0a:
		return fmt.Sprintf("unsupported %d mm continuous", s.MediaWidthMM)
	case 0x0b:
		return fmt.Sprintf("unsupported %d × %d mm die-cut", s.MediaWidthMM, s.MediaLengthMM)
	}
	return "no media"
}

var (
	err1Text = [8]string{"no media", "end of media", "cutter jam", "", "printer in use", "", "", "fan error"}
	err2Text = [8]string{"replace media", "expansion buffer full", "transmission error", "transmission buffer full", "cover open", "cancel key", "media cannot be fed", "system error"}
)

// Errors lists the error conditions the printer reported.
func (s *Status) Errors() []string {
	var errs []string
	for bit := range 8 {
		if s.Err1&(1<<bit) != 0 && err1Text[bit] != "" {
			errs = append(errs, err1Text[bit])
		}
		if s.Err2&(1<<bit) != 0 {
			errs = append(errs, err2Text[bit])
		}
	}
	return errs
}
