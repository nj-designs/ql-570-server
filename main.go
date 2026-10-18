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
	"syscall"
	"time"

	"github.com/nj-designs/ql-570-server/internal/ql570"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	device := flag.String("device", "/dev/usb/lp0", "printer device file")
	maxUpload := flag.Int64("max-upload", 32<<20, "maximum upload size in bytes")
	flag.Parse()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newServer(ql570.NewPrinter(*device), *maxUpload),
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
