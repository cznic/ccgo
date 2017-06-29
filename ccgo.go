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

	s := dict.S(int(nm))
	t := tc.MustType(id)
	for {
		done := true
		switch t.Kind() {
		case ir.Array:
			t = t.(*ir.ArrayType).Item
			for s[0] != ']' {
				s = s[1:]
			}
			s = s[1:]
			done = false
		case ir.Pointer:
			if t.ID() == idVoidPtr {
				return
			}

			t = t.(*ir.PointerType).Element
			s = s[1:]
			done = false
		}
		if done {
			break
		}
	}
	id = t.ID()
	switch t.Kind() {
	case ir.Struct, ir.Union:
		if _, ok := tm[id]; !ok {
			tm[id] = string(s)
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

	for _, v := range obj {
		if err := v.Verify(); err != nil {
			return err
		}
	}

	for k, v := range typeMap {
		tm[k] = v
	}

	return irgo.New(out, obj, tm)
}
