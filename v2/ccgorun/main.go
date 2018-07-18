// Copyright 2018 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ccgorun executes binary programs produced by the ccgo compiler.
//
// Usage
//
// To execute a ccgo binary named a.out
//
//	ccgorun a.out [arguments]
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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

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

	fn := os.Args[1]
	ext := filepath.Ext(fn)
	if ext != ".go" {
		in, err := os.Open(fn)
		if err != nil {
			exit(1, "%s\n", err)
		}

		fn = filepath.Base(fn)
		fn = fn[:len(fn)-len(ext)]
		f, err := fileutil.TempFile("", fn, ".go")
		if err != nil {
			exit(1, "%s\n", err)
		}

		fn = f.Name()

		defer func() {
			os.Remove(fn)
		}()

		if _, err := io.CopyBuffer(f, in, make([]byte, 4096)); err != nil {
			exit(1, "%s\n", err)
		}

		if err := f.Close(); err != nil {
			exit(1, "%s\n", err)
		}
	}

	cmd := exec.Command("go", append([]string{"run", fn}, os.Args[2:]...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		exit(1, "%s\n", err)
	}
}
