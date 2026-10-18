// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

package ql570

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNoStatus means the device file cannot be read back, so the
	// printer's status is unknown.
	ErrNoStatus = errors.New("printer status is not available from this device")
	// ErrStatusTimeout means the printer did not answer a status request.
	ErrStatusTimeout = errors.New("printer did not reply with its status")
)

const (
	statusTimeout = 2 * time.Second
	pageTimeout   = 20 * time.Second
	pollInterval  = 50 * time.Millisecond
)

// Printer talks to a QL-570 through its device file. The file is opened for
// each operation so the printer can be switched off or replugged at will.
type Printer struct {
	Path string
	mu   sync.Mutex
}

func NewPrinter(path string) *Printer { return &Printer{Path: path} }

// Status asks the printer for its status.
func (p *Printer) Status() (*Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, err := os.OpenFile(p.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := drain(f); err != nil {
		return nil, err
	}
	req := make([]byte, invalidateLen, invalidateLen+5)
	req = append(req, esc, '@', esc, 'i', 'S')
	if _, err := f.Write(req); err != nil {
		return nil, err
	}
	return readStatus(f, statusTimeout)
}

// PrintError is returned when the printer reports an error mid-job.
type PrintError struct{ Status *Status }

func (e *PrintError) Error() string {
	if errs := e.Status.Errors(); len(errs) > 0 {
		return "printer error: " + strings.Join(errs, ", ")
	}
	return "printer reported an error"
}

// Print sends a job of the given number of pages and waits until the printer
// reports each one printed. If the device cannot be read back, Print returns
// ErrNoStatus once the job has been written.
func (p *Printer) Print(job []byte, pages int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, err := os.OpenFile(p.Path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	canRead := drain(f) == nil
	deadline := time.Now().Add(statusTimeout + time.Duration(pages)*pageTimeout)
	_ = f.SetWriteDeadline(deadline) // best effort: unsupported on plain files
	if _, err := f.Write(job); err != nil {
		return fmt.Errorf("sending job: %w", err)
	}
	if !canRead {
		return ErrNoStatus
	}

	for done := 0; done < pages; {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("printer confirmed %d of %d labels before timing out", done, pages)
		}
		s, err := readStatus(f, remaining)
		if err != nil {
			return err
		}
		switch s.Type {
		case StatusError:
			return &PrintError{Status: s}
		case StatusCompleted:
			done++
		}
	}
	return nil
}

// drain discards any unread status replies. It returns ErrNoStatus if the
// file does not support read deadlines (it is not a pollable device).
func drain(f *os.File) error {
	buf := make([]byte, 64)
	for range 64 {
		if err := f.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			return ErrNoStatus
		}
		n, err := f.Read(buf)
		if err != nil || n == 0 {
			return nil
		}
	}
	return nil
}

type deadlineReader interface {
	io.Reader
	SetReadDeadline(time.Time) error
}

func readStatus(f deadlineReader, timeout time.Duration) (*Status, error) {
	deadline := time.Now().Add(timeout)
	if err := f.SetReadDeadline(deadline); err != nil {
		return nil, ErrNoStatus
	}
	buf := make([]byte, statusLen)
	for n := 0; n < statusLen; {
		m, err := f.Read(buf[n:])
		n += m
		switch {
		case errors.Is(err, os.ErrDeadlineExceeded):
			return nil, ErrStatusTimeout
		case err == io.EOF || err == nil && m == 0:
			// usblp completes an empty read when the printer has nothing
			// to report yet, which os.File reports as EOF. Poll again.
			if time.Now().After(deadline) {
				return nil, ErrStatusTimeout
			}
			time.Sleep(pollInterval)
		case err != nil:
			return nil, fmt.Errorf("reading status: %w", err)
		}
	}
	return ParseStatus(buf)
}
