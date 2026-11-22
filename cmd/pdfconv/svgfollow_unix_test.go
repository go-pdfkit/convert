// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The Stat works on a file the open then refuses: the open's own error is
// reported.
func TestPasswordFileThatCannotBeOpenedIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens anything")
	}
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte("pw"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := readPassword(p); !errors.Is(err, os.ErrPermission) {
		t.Errorf("got %v", err)
	}
}

// A FIFO with no writer blocks open(2) forever: the refusal must come from the
// Stat, before any open.
func TestPasswordFileOnAFIFOIsRefusedNotBlockedOn(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pw")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := readPassword(fifo)
		errc <- err
	}()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readPassword blocked on a FIFO with no writer")
	}
}
