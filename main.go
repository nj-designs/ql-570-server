// Copyright (c) 2026 Neil Johnson
// SPDX-License-Identifier: MIT

// Command ql570-server is a web front end for printing images on a Brother
// QL-570 label printer.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nj-designs/ql-570-server/internal/ql570"
	"github.com/nj-designs/ql-570-server/internal/textlabel"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	device := flag.String("device", "/dev/usb/lp0", "printer device file")
	maxUpload := flag.Int64("max-upload", 32<<20, "maximum upload size in bytes")
	fontCache := flag.String("font-cache", defaultCacheDir(), "directory for the system font index")
	flag.Parse()

	start := time.Now()
	fonts, err := textlabel.LoadFonts(*fontCache)
	if err != nil {
		log.Fatalf("loading fonts: %v", err)
	}
	log.Printf("indexed %d font families in %v", len(fonts.Families()), time.Since(start).Round(time.Millisecond))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newServer(ql570.NewPrinter(*device), fonts, *maxUpload),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("serving on %s, printer %s", *addr, *device)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// defaultCacheDir prefers the directory systemd provides through
// CacheDirectory=, then the user's cache directory.
func defaultCacheDir() string {
	if dir := os.Getenv("CACHE_DIRECTORY"); dir != "" {
		return dir
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "ql570-server")
	}
	return filepath.Join(os.TempDir(), "ql570-server")
}
