// Copyright 2018 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"go/scanner"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cznic/sortutil"
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
