// Copyright 2018 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"go/scanner"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/cznic/sortutil"
)

var (
	bNL    = []byte{'\n'}
	bPanic = []byte("panic")
)

func env(key, value string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}

	return value
}

func expandError(err error) error {
	switch x := err.(type) {
	case scanner.ErrorList:
		var a []string
		for _, v := range x {
			a = append(a, v.Error())
		}
		return fmt.Errorf("%s", strings.Join(a[:sortutil.Dedupe(sort.StringSlice(a))], "\n"))

	default:
		return err
	}
}

func toExt(nm, new string) string { return nm[:len(nm)-len(filepath.Ext(nm))] + new }

func debugStack2() []byte {
	b := debug.Stack()
	b = b[bytes.Index(b, bPanic)+1:]
	b = b[bytes.Index(b, bPanic):]
	b = b[bytes.Index(b, bNL)+1:]
	a := bytes.SplitN(b, bNL, 3)
	if len(a) > 2 {
		a = a[:2]
	}
	if len(a) > 1 {
		a = a[1:]
	}
	return bytes.Join(a, bNL)
}

func errs(out, in error) error {
	if out == nil {
		out = in
	}
	return out
}
