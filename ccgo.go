// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ccgo translates cc ASTs to Go source code. (Work In Progress)
package ccgo

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"

	"github.com/cznic/cc"
	"github.com/cznic/ccir"
	"github.com/cznic/ir"
	"github.com/cznic/irgo"
)

var (
	// Testing amends things for tests.
	Testing bool
)

//TODO remove me.
func TODO(msg string, more ...interface{}) string { //TODOOK
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "%s:%d: %v\n", path.Base(fn), fl, fmt.Sprintf(msg, more...))
	os.Stderr.Sync()
	panic(fmt.Errorf("%s:%d: %v", path.Base(fn), fl, fmt.Sprintf(msg, more...)))
}

// New writes Go code generated from ast to out. No package clause is
// generated. The result is not formatted. The qualifier function is called for
// implementation defined functions.  It must return the package qualifier, if
// any, that should be used to call the implementation defined function.
func New(ast *cc.TranslationUnit, out io.Writer, qualifier func(*ir.FunctionDefinition) string) (err error) {
	if !Testing {
		defer func() {
			switch x := recover().(type) {
			case nil:
				// ok
			default:
				err = fmt.Errorf("ccgo.New: PANIC: %v", x)
			}
		}()
	}

	obj, err := ccir.New(ast)
	if err != nil {
		return err
	}

	if obj, err = ir.LinkMain(obj); err != nil {
		return err
	}

	return irgo.New(obj, out, qualifier)
}
