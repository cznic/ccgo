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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cznic/ccgo/v2/internal/object"
)

var (
	log     = func(string, ...interface{}) {}
	logging bool
)

func main() {
	r, err := main1(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
	}
	os.Exit(r)
}

func main1(args []string) (r int, err error) {
	if fn := os.Getenv("CCGOLOG"); fn != "" {
		logging = true
		var f *os.File
		if f, err = os.OpenFile(fn, os.O_APPEND|os.O_CREATE|os.O_WRONLY|os.O_SYNC, 0644); err != nil {
			return 1, err
		}

		pid := fmt.Sprintf("[pid %v] ", os.Getpid())

		log = func(s string, args ...interface{}) {
			s = fmt.Sprintf(pid+s, args...)
			switch {
			case len(s) != 0 && s[len(s)-1] == '\n':
				fmt.Fprint(f, s)
			default:
				fmt.Fprintln(f, s)
			}
		}

		defer func() {
			log("---- exit status %v, err %v", r, err)
			f.Close()
		}()

		log("==== %v", args)
	}

	if len(args) < 2 {
		return 2, fmt.Errorf("invalid arguments %v", args)
	}

	fn, err := filepath.Abs(args[1])
	if err != nil {
		return 1, err
	}

	tempDir, err := ioutil.TempDir("", "ccgorun-")
	if err != nil {
		return 1, err
	}

	return buildAndRun(tempDir, fn, args[2:])
}

func buildAndRun(tempDir, fn string, args []string) (r int, err error) {
	defer func() {
		if e := os.RemoveAll(tempDir); e != nil && err == nil {
			r = 1
			err = e
		}
	}()

	var bin string
	if bin, r, err = build(tempDir, fn); err != nil {
		return r, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1, err
	}

	return 0, nil
}

func build(tempDir, fn string) (bin string, rc int, err error) {
	fin, err := os.Open(fn)
	if err != nil {
		return "", 1, err
	}

	in := bufio.NewReader(fin)
	if _, err := in.ReadBytes('\n'); err != nil {
		return "", 1, err
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", 1, err
	}

	if err := os.Chdir(tempDir); err != nil {
		return "", 1, err
	}

	defer func() {
		if e := os.Chdir(wd); e != nil && err == nil {
			rc = 1
			err = e
		}
	}()

	fout, err := os.Create("main.go")
	if err != nil {
		return "", 1, err
	}

	out := bufio.NewWriter(fout)
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
		return "", 1, err
	}

	fin.Close()
	if err := out.Flush(); err != nil {
		return "", 1, err
	}

	if err := fout.Close(); err != nil {
		return "", 1, err
	}

	cmd := exec.Command("go", "build", "main.go")
	for _, v := range os.Environ() {
		if v != "CC=ccgo" {
			cmd.Env = append(cmd.Env, v)
		}
	}
	if co, err := cmd.CombinedOutput(); err != nil {
		return "", 1, fmt.Errorf("%s\n%v", co, err)
	}

	m, err := filepath.Glob("*")
	if err != nil {
		return "", 1, err
	}

	for _, v := range m {
		if v == "main.go" {
			continue
		}

		bin = filepath.Join(tempDir, v)
		break
	}
	return bin, 0, nil
}
