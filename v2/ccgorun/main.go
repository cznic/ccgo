// Copyright 2018 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ccgorun executes binary programs produced by the ccgo compiler.
//
// Usage
//
// To execute a ccgo binary named a.out
//
//	$ ccgorun a.out [arguments]
//
// Installation
//
// To install or update
//
//      $ go get [-u] github.com/cznic/ccgo/v2/ccgorun
//
// Online documentation: [godoc.org/github.com/cznic/ccgo/v2/ccgorun](http://godoc.org/github.com/cznic/ccgo/v2/ccgorun)
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cznic/ccgo/v2/internal/object"
	"github.com/cznic/fileutil"
)

func exit(code int, msg string, arg ...interface{}) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, os.Args[0]+": "+msg, arg...)
	}
	os.Exit(code)
}

func main() {
	if len(os.Args) < 2 {
		exit(2, "invalid arguments %v\n", os.Args)
	}

	ifn, err := filepath.Abs(os.Args[1])
	if err != nil {
		exit(1, "%s\n", err)
	}

	fin, err := os.Open(ifn)
	if err != nil {
		exit(1, "%s\n", err)
	}

	in := bufio.NewReader(fin)
	b, err := in.ReadBytes('\n') // Skip shebang
	if err != nil {
		exit(1, "%s\n", err)
	}

	if !bytes.HasPrefix(b, []byte("///")) {
		exit(1, "invalid file format: %s\n", ifn)
	}

	ofn := filepath.Base(ifn)
	ofn = ofn[:len(ofn)-len(filepath.Ext(ifn))]
	f, err := fileutil.TempFile("", ofn, ".go")
	if err != nil {
		exit(1, "%s\n", err)
	}

	defer os.Remove(ofn)

	ofn = f.Name()
	fout, err := os.Create(ofn)
	if err != nil {
		exit(1, "%s\n", err)
	}

	out := bufio.NewWriter(fout)
	if _, err := fmt.Fprintf(out, "//line %s:1\n", ofn); err != nil {
		exit(1, "%s\n", err)
	}

	r, w := io.Pipe()
	var e2 error

	go func() {
		defer func() {
			if e := w.Close(); e != nil && e2 == nil {
				e2 = e
			}
		}()

		if _, e := io.Copy(w, in); e != nil && e2 == nil {
			e2 = e
		}
	}()

	if err := object.Decode(out, runtime.GOOS, runtime.GOARCH, object.BinVersion, object.BinMagic, r); err != nil {
		exit(1, "%s\n", err)
	}

	fin.Close()
	if err := out.Flush(); err != nil {
		exit(1, "%s\n", err)
	}

	if err := fout.Close(); err != nil {
		exit(1, "%s\n", err)
	}

	cmd := exec.Command("go", append([]string{"run", ofn}, os.Args[2:]...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(ofn)
		exit(1, "%s\n", err)
	}
}
