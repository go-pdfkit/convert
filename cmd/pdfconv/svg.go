// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-pdfkit/convert"
)

// Seams: the failures they stand for (a file system without hard links, a disk
// that fills mid-write) cannot be provoked on the machine a test runs on.
var (
	linkFile = os.Link
	wrapOut  = func(w io.Writer) io.Writer { return w }
	// notifySignals asks for SIGINT and SIGTERM on c; a test hands the channel
	// to itself instead of signalling the test binary.
	notifySignals = func(c chan<- os.Signal) { signal.Notify(c, os.Interrupt, syscall.SIGTERM) }
)

// tmpName is the temporary file the run is writing, shared with the goroutine
// that cleans up on a signal. Empty until the file exists: removing "" is a
// harmless error, so there is no "not yet" branch to get wrong.
type tmpName struct {
	mu sync.Mutex
	s  string
}

func (t *tmpName) set(s string) { t.mu.Lock(); t.s = s; t.mu.Unlock() }
func (t *tmpName) get() string  { t.mu.Lock(); defer t.mu.Unlock(); return t.s }

// exitOnSignal removes the temporary file and exits with the shell's code for
// the signal (128+2 for SIGINT, 128+15 for SIGTERM) when one arrives, and
// stops when the returned function is called.
//
// ⛔ Without it an interrupted run leaves .pdfconv-*.tmp in the output
// directory: the deferred removal in toSVG never runs when the process dies.
func exitOnSignal(name *tmpName) (stop func()) {
	sigc := make(chan os.Signal, 1)
	done := make(chan struct{})
	notifySignals(sigc)
	go func() {
		select {
		case s := <-sigc:
			os.Remove(name.get())
			code := 130
			if s == syscall.SIGTERM {
				code = 143
			}
			osExit(code)
		case <-done:
		}
	}()
	return func() { signal.Stop(sigc); close(done) }
}

// toSVG writes the pages of a PDF as SVG, named the way BentoPDF names them: a
// single page is name.svg, several are name_svg.zip holding page_N.svg.
//
// ⛔ All or nothing, like from-pdf and to-archive: the first page that cannot
// be written stops the run, the error names the page, and no file is left
// behind. The pages are STREAMED into a temporary file in the output directory,
// so the peak memory is one page, not the run; the file takes its name only
// when every page is in it.
func toSVG(args []string, out, errw io.Writer) error {
	fl := flag.NewFlagSet("to-svg", flag.ContinueOnError)
	fl.SetOutput(errw)
	pages := fl.String("pages", "", "which pages, e.g. 1,3,5-9; empty means all")
	getPassword := passwordFlag(fl)
	outDir := fl.String("o", ".", "the directory to write into")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 1 {
		return fmt.Errorf("to-svg takes exactly one PDF: pdfconv to-svg [-pages 1-3] [-o dir] <in.pdf>")
	}
	want, err := parseRange(*pages)
	if err != nil {
		return err
	}
	password, err := getPassword()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(fl.Arg(0))
	if err != nil {
		return err
	}

	base := strings.TrimSuffix(filepath.Base(fl.Arg(0)), filepath.Ext(fl.Arg(0)))
	var (
		tmp  *os.File
		bw   *bufio.Writer
		zw   *convert.SVGArchiveWriter
		name string
		n    int
		size counter
	)
	var cur tmpName
	defer exitOnSignal(&cur)()
	defer func() {
		// The temporary name always goes: on success the published name is a
		// second link to the same file, and on failure a temporary file that
		// outlives it is a half-written result with a date on it.
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	err = convert.StreamSVG(src, convert.SVGOptions{Pages: want, Password: password},
		func(page, of int, svg []byte) error {
			if tmp == nil {
				if err := os.MkdirAll(*outDir, 0o755); err != nil {
					return err
				}
				f, err := createTemp(*outDir, &cur)
				if err != nil {
					return err
				}
				tmp = f
				bw = bufio.NewWriter(wrapOut(&size))
				size.w = f
				name = base + ".svg"
				if of > 1 {
					name = base + "_svg.zip"
					zw = convert.NewSVGArchive(bw)
				}
			}
			n++
			if zw != nil {
				return zw.Add(page, svg)
			}
			_, err := bw.Write(svg)
			return err
		})
	if err != nil {
		return fmt.Errorf("%s: %w", fl.Arg(0), err)
	}
	var zerr error
	if zw != nil {
		zerr = zw.Close()
	}
	if err := errors.Join(zerr, bw.Flush(), tmp.Close()); err != nil {
		return fmt.Errorf("%s: %w", fl.Arg(0), err)
	}
	path := filepath.Join(*outDir, name)
	if err := publish(tmp.Name(), path); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d page(s), %d bytes\n", path, n, size.n)
	return nil
}

// counter counts what reaches the file, so the size is known without asking
// the file system for it.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	k, err := c.w.Write(p)
	c.n += int64(k)
	return k, err
}

var tmpSeq atomic.Uint64

// createTemp makes a file in dir that did not exist, with the mode the result
// will keep (so no chmod can fail after the work is done), under a name no other
// run shares: the process, the clock and a sequence.
//
// The name is handed to cur BEFORE the file is created, so a signal that lands
// between the create and the return still finds it; if the create failed, the
// name is one that does not exist and removing it is a harmless error.
func createTemp(dir string, cur *tmpName) (*os.File, error) {
	name := filepath.Join(dir, fmt.Sprintf(".pdfconv-%d-%d-%d.tmp", os.Getpid(), time.Now().UnixNano(), tmpSeq.Add(1)))
	cur.set(name)
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}

// publish gives the finished temporary file its name WITHOUT replacing anything
// and without following a link at that name: a hard link, which fails if the
// name exists in any form (a file, a directory, a symlink even dangling); and
// where the file system has no hard links, a create that is exclusive for the
// same reason. The temporary file is left for the caller to remove.
func publish(tmp, target string) error {
	err := linkFile(tmp, target)
	if err == nil {
		return nil
	}
	// Whatever the link said (the name exists, or hard links are not allowed
	// here), the exclusive create below decides and says so.
	src, serr := os.Open(tmp)
	if serr != nil {
		return serr
	}
	defer src.Close()
	dst, derr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if derr != nil {
		if errors.Is(derr, fs.ErrExist) {
			return fmt.Errorf("%s exists: it is not overwritten, remove it or choose another -o", target)
		}
		return derr
	}
	_, cerr := io.Copy(dst, src)
	if clerr := dst.Close(); cerr == nil {
		cerr = clerr
	}
	if cerr != nil {
		// This call made the file (the create was exclusive), so it may remove it.
		os.Remove(target)
		return cerr
	}
	return nil
}
