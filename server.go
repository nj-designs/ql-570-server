// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/nj-designs/ql-570-server/internal/imaging"
	"github.com/nj-designs/ql-570-server/internal/ql570"
)

//go:embed web/index.html
var indexHTML []byte

const maxCopies = 100

type server struct {
	printer   *ql570.Printer
	maxUpload int64
}

func newServer(p *ql570.Printer, maxUpload int64) http.Handler {
	s := &server{printer: p, maxUpload: maxUpload}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/media", s.handleMedia)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/preview", s.handlePreview)
	mux.HandleFunc("POST /api/print", s.handlePrint)
	return mux
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ql570.AllMedia())
}

type statusResponse struct {
	Connected bool     `json:"connected"`
	Error     string   `json:"error,omitempty"`
	Media     string   `json:"media,omitempty"`     // ID of the detected media
	MediaName string   `json:"mediaName,omitempty"` // description, even if unsupported
	Problems  []string `json:"problems,omitempty"`
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var resp statusResponse
	st, err := s.printer.Status()
	switch {
	case err == nil:
		resp.Connected = true
		resp.MediaName = st.MediaDescription()
		if m := st.Media(); m != nil {
			resp.Media = m.ID
		}
		resp.Problems = st.Errors()
	case errors.Is(err, ql570.ErrNoStatus), errors.Is(err, ql570.ErrStatusTimeout):
		resp.Connected = true
		resp.Error = "media can't be detected; choose it manually"
	default:
		resp.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handlePreview(w http.ResponseWriter, r *http.Request) {
	req, err := s.parseRequest(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	bm, rot := imaging.Render(req.img, req.media, req.opts)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Print-Width", strconv.Itoa(bm.Width))
	w.Header().Set("X-Print-Length", strconv.Itoa(bm.Height))
	w.Header().Set("X-Rotation", strconv.Itoa(rot))
	png.Encode(w, imaging.Preview(bm, rot))
}

func (s *server) handlePrint(w http.ResponseWriter, r *http.Request) {
	req, err := s.parseRequest(w, r)
	if err != nil {
		writeError(w, err)
		return
	}

	// Check the loaded media first: a mismatch makes the printer stop with
	// a blinking red light.
	st, err := s.printer.Status()
	switch {
	case err == nil:
		if problems := st.Errors(); len(problems) > 0 {
			writeError(w, httpError{http.StatusConflict, "printer reports: " + strings.Join(problems, ", ")})
			return
		}
		if st.MediaType != 0 && (st.Media() == nil || st.Media().ID != req.media.ID) {
			writeError(w, httpError{http.StatusConflict, fmt.Sprintf("the printer has %s loaded, but %s is selected", st.MediaDescription(), req.media.Name)})
			return
		}
	case errors.Is(err, ql570.ErrNoStatus), errors.Is(err, ql570.ErrStatusTimeout):
		// Can't verify the media; print anyway.
	default:
		writeError(w, httpError{http.StatusServiceUnavailable, "printer unavailable: " + err.Error()})
		return
	}

	bm, _ := imaging.Render(req.img, req.media, req.opts)
	job, err := ql570.Encode(req.media, bm, ql570.JobOptions{Copies: req.copies, AutoCut: req.autoCut})
	if err != nil {
		writeError(w, err)
		return
	}
	labels := "label"
	if req.copies > 1 {
		labels = "labels"
	}
	log.Printf("printing %d × %s (%d × %d dots)", req.copies, req.media.Name, bm.Width, bm.Height)
	switch err := s.printer.Print(job, req.copies); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Printed %d %s", req.copies, labels)})
	case errors.Is(err, ql570.ErrNoStatus):
		writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Sent %d %s to the printer", req.copies, labels)})
	default:
		log.Printf("print failed: %v", err)
		writeError(w, httpError{http.StatusBadGateway, err.Error()})
	}
}

type printRequest struct {
	img     *imaging.Gray
	media   *ql570.Media
	opts    imaging.Options
	copies  int
	autoCut bool
}

// parseRequest reads the multipart form shared by preview and print.
func (s *server) parseRequest(w http.ResponseWriter, r *http.Request) (*printRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, httpError{http.StatusRequestEntityTooLarge, fmt.Sprintf("upload is larger than %d MB", s.maxUpload>>20)}
		}
		return nil, badRequest("invalid upload: %v", err)
	}
	defer r.MultipartForm.RemoveAll()

	req := &printRequest{
		opts:    imaging.Options{Rotate: imaging.Auto, Threshold: 128},
		copies:  1,
		autoCut: r.FormValue("autocut") != "false",
	}
	if req.media = ql570.LookupMedia(r.FormValue("media")); req.media == nil {
		return nil, badRequest("unknown media %q", r.FormValue("media"))
	}
	switch v := r.FormValue("rotate"); v {
	case "", "auto":
	case "0", "90", "180", "270":
		req.opts.Rotate, _ = strconv.Atoi(v)
	default:
		return nil, badRequest("rotate must be auto, 0, 90, 180 or 270")
	}
	switch r.FormValue("mode") {
	case "", "threshold":
	case "dither":
		req.opts.Dither = true
	default:
		return nil, badRequest("mode must be threshold or dither")
	}
	if v := r.FormValue("threshold"); v != "" {
		t, err := strconv.Atoi(v)
		if err != nil || t < 1 || t > 254 {
			return nil, badRequest("threshold must be between 1 and 254")
		}
		req.opts.Threshold = t
	}
	if v := r.FormValue("copies"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCopies {
			return nil, badRequest("copies must be between 1 and %d", maxCopies)
		}
		req.copies = n
	}

	f, _, err := r.FormFile("image")
	if err != nil {
		return nil, badRequest("no image uploaded")
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if req.img, err = imaging.Load(data); err != nil {
		return nil, badRequest("%v", err)
	}
	return req, nil
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if he, ok := errors.AsType[httpError](err); ok {
		code = he.code
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
