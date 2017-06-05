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
	"github.com/cznic/crt"
	"github.com/cznic/ir"
	"github.com/cznic/irgo"
	"github.com/cznic/xc"
)

var (
	// Testing amends things for tests.
	Testing bool

	dict      = xc.Dict
	idVoidPtr = ir.TypeID(dict.SID("*struct{}"))
)

//TODO remove me.
func TODO(msg string, more ...interface{}) string { //TODOOK
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "%s:%d: %v\n", path.Base(fn), fl, fmt.Sprintf(msg, more...))
	os.Stderr.Sync()
	panic(fmt.Errorf("%s:%d: %v", path.Base(fn), fl, fmt.Sprintf(msg, more...)))
}

func typ(tc ir.TypeCache, tm map[ir.TypeID]string, id ir.TypeID, nm ir.NameID) {
	if nm == 0 {
		return
	}

	t := tc.MustType(id)
	n := 0
	for t.Kind() == ir.Pointer {
		if id == idVoidPtr {
			return
		}

		t = t.(*ir.PointerType).Element
		id = t.ID()
		n++
	}
	switch t.Kind() {
	case ir.Struct, ir.Union:
		if _, ok := tm[id]; !ok {
			tm[id] = string(dict.S(int(nm)))[n:]
		}
	}
}

// New writes Go code generated from ast to out. No package or import clause is
// generated.
func New(ast []*cc.TranslationUnit, out io.Writer) (err error) {
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

	tc := ir.TypeCache{}
	tm := map[ir.TypeID]string{}
	var build [][]ir.Object
	for _, v := range ast {
		obj, err := ccir.New(v)
		if err != nil {
			return err
		}

		for _, v := range obj {
			if err := v.Verify(); err != nil {
				return err
			}

			switch x := v.(type) {
			case *ir.DataDefinition:
				typ(tc, tm, x.TypeID, x.TypeName)
			case *ir.FunctionDefinition:
				for _, v := range x.Body {
					switch y := v.(type) {
					case *ir.VariableDeclaration:
						typ(tc, tm, y.TypeID, y.TypeName)
					}
				}
			}
		}

		build = append(build, obj)
	}

	obj, err := ir.LinkMain(build...)
	if err != nil {
		return err
	}

	for k, v := range map[ir.TypeID]string{
		ir.TypeID(dict.SID(crt.TFILE)):                "crt.XFILE",
		ir.TypeID(dict.SID(crt.Tpthread_attr_t)):      "crt.Xpthread_attr_t",
		ir.TypeID(dict.SID(crt.Tpthread_mutex_t)):     "crt.Xpthread_mutex_t",
		ir.TypeID(dict.SID(crt.Tpthread_mutexattr_t)): "crt.Xpthread_mutexattr_t",
		ir.TypeID(dict.SID(crt.Tstruct_stat64)):       "crt.Xstruct_stat64",
		ir.TypeID(dict.SID(crt.Tstruct_timeval)):      "crt.Xstruct_timeval",
		ir.TypeID(dict.SID(crt.Ttm)):                  "crt.Xtm",
	} {
		tm[k] = v
	}
	return irgo.New(out, obj, tm)
}
