// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/nj-designs/ql-570-server/internal/ql570"
	"github.com/nj-designs/ql-570-server/internal/textlabel"
)

func upload(t *testing.T, url string, fields map[string]string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("image", "test.png")
	png.Encode(fw, image.NewGray(image.Rect(0, 0, 40, 20)))
	mw.Close()
	res, err := http.Post(url, mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func testFonts(t *testing.T) *textlabel.Fonts {
	t.Helper()
	f, err := textlabel.LoadFonts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestServerText(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "lp0")
	os.WriteFile(dev, nil, 0o600)
	ts := httptest.NewServer(newServer(ql570.NewPrinter(dev), testFonts(t), 1<<20))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/fonts")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("fonts: %v %v", err, res.Status)
	}
	var fonts struct {
		Default  string
		Families []string
	}
	json.NewDecoder(res.Body).Decode(&fonts)
	res.Body.Close()
	if fonts.Default == "" || len(fonts.Families) == 0 {
		t.Fatalf("fonts = %+v", fonts)
	}

	// Text works URL-encoded as well as multipart.
	res, err = http.PostForm(ts.URL+"/api/preview", url.Values{"media": {"29x90"}, "text": {"Hello"}, "font": {"Go"}})
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("preview: %v %v", err, res.Status)
	}
	img, err := png.Decode(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Shown with the label's long side horizontal.
	if b := img.Bounds(); b.Dx() != 991 || b.Dy() != 306 {
		t.Errorf("text preview is %v", b)
	}

	res, _ = http.PostForm(ts.URL+"/api/preview", url.Values{"media": {"62x29"}, "text": {"Hi"}, "font": {"Go"}, "size": {"200"}})
	if res.Header.Get("X-Text-Clipped") != "1" {
		t.Error("oversized text not flagged as clipped")
	}
	res.Body.Close()

	for name, form := range map[string]url.Values{
		"blank":    {"media": {"62"}, "text": {"  "}},
		"font":     {"media": {"62"}, "text": {"x"}, "font": {"No Such Font"}},
		"size":     {"media": {"62"}, "text": {"x"}, "size": {"1"}},
		"align":    {"media": {"62"}, "text": {"x"}, "align": {"justify"}},
		"no input": {"media": {"62"}},
	} {
		res, _ := http.PostForm(ts.URL+"/api/preview", form)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %s", name, res.Status)
		}
		res.Body.Close()
	}

	res, _ = http.PostForm(ts.URL+"/api/print", url.Values{"media": {"62"}, "text": {"Hello\nworld"}, "copies": {"2"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("print: %s", res.Status)
	}
	res.Body.Close()
	if job, _ := os.ReadFile(dev); job[len(job)-1] != 0x1a {
		t.Error("device received a malformed job")
	}
}

func TestServer(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "lp0")
	os.WriteFile(dev, nil, 0o600)
	ts := httptest.NewServer(newServer(ql570.NewPrinter(dev), testFonts(t), 1<<20))
	defer ts.Close()

	res := upload(t, ts.URL+"/api/preview", map[string]string{"media": "29x90", "mode": "dither"})
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("preview: %s", res.Status)
	}
	img, err := png.Decode(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	// Landscape image on a long label: auto-rotated, then shown upright.
	m := ql570.LookupMedia("29x90")
	if b := img.Bounds(); b.Dx() != m.Length || b.Dy() != m.Width {
		t.Errorf("preview is %v, want %dx%d", b, m.Length, m.Width)
	}

	if res := upload(t, ts.URL+"/api/preview", map[string]string{"media": "nope"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown media: %s", res.Status)
	}
	if res := upload(t, ts.URL+"/api/print", map[string]string{"media": "62", "copies": "0"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("zero copies: %s", res.Status)
	}

	res = upload(t, ts.URL+"/api/print", map[string]string{"media": "62", "copies": "3", "autocut": "false"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("print: %s", res.Status)
	}
	job, _ := os.ReadFile(dev)
	if !bytes.HasPrefix(job, make([]byte, 200)) || job[len(job)-1] != 0x1a {
		t.Errorf("device received a malformed job of %d bytes", len(job))
	}
	if got := bytes.Count(job, []byte{0x1b, 'i', 'M', 0x00}); got != 3 {
		t.Errorf("%d pages without auto cut, want 3", got)
	}

	res, _ = http.Get(ts.URL + "/")
	if res.StatusCode != http.StatusOK {
		t.Errorf("index: %s", res.Status)
	}
}
