// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestToSVGASignalRemovesTheTemporaryFileAndExitsWithTheShellCode(t *testing.T) {
	for _, c := range []struct {
		name string
		sig  os.Signal
		code int
	}{{"SIGINT", os.Interrupt, 130}, {"SIGTERM", syscall.SIGTERM, 143}} {
		t.Run(c.name, func(t *testing.T) {
			oldN, oldE, oldL := notifySignals, osExit, linkFile
			defer func() { notifySignals, osExit, linkFile = oldN, oldE, oldL }()
			var sigc chan<- os.Signal
			notifySignals = func(ch chan<- os.Signal) { sigc = ch }
			exited := make(chan int, 1)
			osExit = func(code int) { exited <- code }

			in := pdfOn(t, "in.pdf", nil, sq)
			out := t.TempDir()
			var gotCode int
			var left []string
			// The signal arrives when the whole file is written and only the
			// publish is left: the temporary file exists, and it is the signal
			// handler (not the deferred removal, which runs after this) that has
			// to take it away.
			linkFile = func(string, string) error {
				sigc <- c.sig
				gotCode = <-exited
				es, _ := os.ReadDir(out)
				for _, e := range es {
					left = append(left, e.Name())
				}
				return errors.New("interrupted")
			}
			if err := run([]string{"to-svg", "-o", out, in}); err == nil {
				t.Error("an interrupted run reported success")
			}
			if gotCode != c.code {
				t.Errorf("exit code %d, want %d", gotCode, c.code)
			}
			if len(left) != 0 {
				t.Errorf("the signal handler left %v", left)
			}
		})
	}
}

// pipeInfo says that what was opened is a FIFO.
type pipeInfo struct{ os.FileInfo }

func (pipeInfo) Mode() fs.FileMode { return fs.ModeNamedPipe }

func TestPasswordFileRechecksWhatWasOpened(t *testing.T) {
	defer func(f func(*os.File) (os.FileInfo, error)) { statOpened = f }(statOpened)
	p := pwFile(t, "pw")
	statOpened = func(*os.File) (os.FileInfo, error) { return nil, errors.New("stat failed") }
	if _, err := readPassword(p); err == nil || !strings.Contains(err.Error(), "stat failed") {
		t.Errorf("a failed fstat: %v", err)
	}
	// The name was a regular file at the Stat and a FIFO at the open.
	statOpened = func(f *os.File) (os.FileInfo, error) {
		fi, err := f.Stat()
		return pipeInfo{fi}, err
	}
	if _, err := readPassword(p); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a file swapped for a FIFO: %v", err)
	}
}
