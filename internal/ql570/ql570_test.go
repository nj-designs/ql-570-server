// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package ql570

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaTable(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range AllMedia() {
		if seen[m.ID] {
			t.Errorf("duplicate media ID %q", m.ID)
		}
		seen[m.ID] = true
		if m.offsetR+m.Width > HeadPins {
			t.Errorf("%s: printable area %d+%d exceeds %d pins", m.ID, m.offsetR, m.Width, HeadPins)
		}
		if (m.Kind == Continuous) != (m.Length == 0) {
			t.Errorf("%s: kind %v with length %d", m.ID, m.Kind, m.Length)
		}
		if LookupMedia(m.ID) == nil {
			t.Errorf("LookupMedia(%q) = nil", m.ID)
		}
	}
}

func TestEncode(t *testing.T) {
	m := LookupMedia("62x29")
	bm := NewBitmap(m.Width, m.Length)
	bm.Pix[0] = 1 // top-left pixel

	job, err := Encode(m, bm, JobOptions{Copies: 2, AutoCut: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(job[:invalidateLen], make([]byte, invalidateLen)) {
		t.Fatal("job does not start with invalidate bytes")
	}
	rest := job[invalidateLen:]
	if !bytes.HasPrefix(rest, []byte{esc, '@', esc, 'i', 'z'}) {
		t.Fatalf("unexpected header % x", rest[:8])
	}
	info := rest[2:15]
	if info[4] != 0x0b || info[5] != 62 || info[6] != 29 {
		t.Errorf("print info media = % x", info[4:7])
	}
	if n := binary.LittleEndian.Uint32(info[7:11]); n != uint32(m.Length) {
		t.Errorf("raster count = %d, want %d", n, m.Length)
	}
	if info[11] != 0 {
		t.Errorf("first page flag = %d, want 0", info[11])
	}

	first := bytes.IndexByte(rest, 'g')
	line := rest[first : first+3+lineBytes]
	if line[1] != 0 || line[2] != lineBytes {
		t.Fatalf("raster command header % x", line[:3])
	}
	pin := m.offsetR + m.Width - 1
	for i, b := range line[3:] {
		want := byte(0)
		if i == pin/8 {
			want = 0x80 >> (pin % 8)
		}
		if b != want {
			t.Errorf("byte %d = %#x, want %#x", i, b, want)
		}
	}

	if job[len(job)-1] != 0x1a {
		t.Errorf("job ends with %#x, want 0x1a", job[len(job)-1])
	}
	if got := bytes.Count(job, []byte{esc, 'i', 'z'}); got != 2 {
		t.Errorf("%d print info commands, want 2", got)
	}
}

func TestEncodeRejectsWrongSize(t *testing.T) {
	m := LookupMedia("29x90")
	if _, err := Encode(m, NewBitmap(m.Width, m.Length-1), JobOptions{}); err == nil {
		t.Error("accepted short die-cut bitmap")
	}
	c := LookupMedia("62")
	if _, err := Encode(c, NewBitmap(c.Width, MinLength-1), JobOptions{}); err == nil {
		t.Error("accepted too-short continuous bitmap")
	}
}

func status(err1, err2, kind, width, length byte) []byte {
	b := make([]byte, statusLen)
	b[0], b[1], b[2], b[3] = 0x80, statusLen, 'B', '0'
	b[8], b[9], b[10], b[11], b[17] = err1, err2, width, kind, length
	return b
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		raw    []byte
		media  string
		errors int
	}{
		{status(0, 0, 0x0a, 62, 0), "62", 0},
		{status(0, 0, 0x0b, 29, 90), "29x90", 0},
		{status(0, 0, 0x0b, 58, 58), "d58", 0},
		{status(0, 0, 0x0b, 23, 23), "23x23", 0},
		{status(0x01, 0x10, 0, 0, 0), "", 2},
	}
	for _, tt := range tests {
		s, err := ParseStatus(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		id := ""
		if m := s.Media(); m != nil {
			id = m.ID
		}
		if id != tt.media {
			t.Errorf("media = %q, want %q", id, tt.media)
		}
		if len(s.Errors()) != tt.errors {
			t.Errorf("errors = %v, want %d", s.Errors(), tt.errors)
		}
	}
	if _, err := ParseStatus([]byte{1, 2, 3}); err == nil {
		t.Error("accepted short status")
	}
}

// usblpReader mimics usblp: empty reads (EOF) until the printer replies.
type usblpReader struct {
	empty int
	reply []byte
}

func (r *usblpReader) SetReadDeadline(time.Time) error { return nil }

func (r *usblpReader) Read(p []byte) (int, error) {
	if r.empty > 0 {
		r.empty--
		return 0, io.EOF
	}
	if len(r.reply) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.reply)
	r.reply = r.reply[n:]
	return n, nil
}

func TestReadStatusRetriesEmptyReads(t *testing.T) {
	r := &usblpReader{empty: 3, reply: status(0, 0, 0x0b, 29, 90)}
	s, err := readStatus(r, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if m := s.Media(); m == nil || m.ID != "29x90" {
		t.Errorf("media = %v", m)
	}

	if _, err := readStatus(&usblpReader{}, 200*time.Millisecond); err != ErrStatusTimeout {
		t.Errorf("no reply: error = %v, want ErrStatusTimeout", err)
	}
}

func TestPrintToPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lp0")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewPrinter(path)
	if _, err := p.Status(); err != ErrNoStatus {
		t.Errorf("Status() error = %v, want ErrNoStatus", err)
	}
	if err := p.Print([]byte("job"), 1); err != ErrNoStatus {
		t.Errorf("Print() error = %v, want ErrNoStatus", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "job" {
		t.Errorf("file holds %q", got)
	}
}
