// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// stdin is a seam, so a test can say what a password pipe holds.
var stdin io.Reader = os.Stdin

// maxPasswordFile bounds what is read as a password: the file is the user's,
// but "-" may be a pipe from anywhere.
const maxPasswordFile = 4096

// passwordFlag declares -password-file on fs and returns what to call, after
// Parse, to get the password.
//
// ⛔ There is no -password: an argument is visible to every process on the
// machine (ps, /proc) and stays in the shell's history. The password comes from
// a file, or from standard input with "-".
func passwordFlag(fs *flag.FlagSet) func() (string, error) {
	file := fs.String("password-file", "", "read the password of an encrypted file from this file, or from standard input if \"-\"")
	return func() (string, error) { return readPassword(*file) }
}

// readPassword reads the password in path ("-" is standard input), without one
// trailing newline. An empty path means no password.
func readPassword(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	var r io.Reader = stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("password file: %w", err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxPasswordFile+1))
	if err != nil {
		return "", fmt.Errorf("password file %s: %w", path, err)
	}
	if len(b) > maxPasswordFile {
		return "", fmt.Errorf("password file %s: longer than %d bytes", path, maxPasswordFile)
	}
	s := string(b)
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2], nil
	}
	return strings.TrimSuffix(s, "\n"), nil
}
