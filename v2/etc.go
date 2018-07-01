// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccgo

import (
	"bytes"
	"fmt"
	"go/scanner"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/cznic/ir"
	"github.com/cznic/sqlite2go/internal/c99"
	"github.com/cznic/strutil"
	"github.com/cznic/xc"
)

const (
	ap   = "ap"
	crt  = "crt."
	null = "null"
)

var (
	allocaDeclarator = &c99.Declarator{}
	bNL              = []byte{'\n'}
	bPanic           = []byte("panic")
	dict             = xc.Dict

	idBacktrace              = dict.SID("backtrace")
	idBuiltinAlloca          = dict.SID("__builtin_alloca")
	idBuiltinTypesCompatible = dict.SID("__builtin_types_compatible__") // Implements __builtin_types_compatible_p
	idBuiltinVaList          = dict.SID("__builtin_va_list")
	idFuncName               = dict.SID("__func__")
	idMain                   = dict.SID("main")
	idStart                  = dict.SID("_start")
	idStderr                 = dict.SID("stderr")
	idStdin                  = dict.SID("stdin")
	idStdout                 = dict.SID("stdout")
	idVaEnd                  = dict.SID("__va_end")
	idVaList                 = dict.SID("va_list")
	idVaStart                = dict.SID("__va_start")

	testFn      string
	traceOpt    bool
	traceTODO   bool
	traceWrites bool
)

func pretty(v interface{}) string { return strutil.PrettyString(v, "", "", nil) }

func compact(s string, maxLines int) string {
	a := strings.Split(s, "\n")
	w := 0
	for _, v := range a {
		v = strings.TrimSpace(v)
		if v != "" {
			a[w] = v
			w++
		}
	}
	a = a[:w]
	if len(a) > maxLines {
		a = a[:maxLines]
	}
	return strings.Join(a, "\n")
}

func debugStack() []byte {
	b := debug.Stack()
	b = b[bytes.Index(b, bPanic)+1:]
	b = b[bytes.Index(b, bPanic):]
	b = b[bytes.Index(b, bNL)+1:]
	return b
}

func errString(err error) string {
	var b bytes.Buffer
	printError(&b, "", err)
	return b.String()
}

func isSingleExpression(n *c99.ExprList) bool { return n.ExprList == nil }

func mangleIdent(nm int, exported bool) string {
	switch {
	case exported:
		return fmt.Sprintf("X%s", dict.S(nm))
	default:
		return fmt.Sprintf("_%s", dict.S(nm))
	}
}

func printError(w io.Writer, pref string, err error) {
	switch x := err.(type) {
	case scanner.ErrorList:
		x.RemoveMultiples()
		for i, v := range x {
			fmt.Fprintf(w, "%s%v\n", pref, v)
			if i == 50 {
				fmt.Fprintln(w, "too many errors")
				break
			}
		}
	default:
		fmt.Fprintf(w, "%s%v\n", pref, err)
	}
}

func roundup(n, to int64) int64 {
	if r := n % to; r != 0 {
		return n + to - r
	}

	return n
}

func strComment(sv *ir.StringValue) string {
	s := dict.S(int(sv.StringID))
	if len(s) > 32 {
		s = append(append([]byte(nil), s[:32]...), []byte("...")...)
	}
	s = bytes.Replace(s, []byte("*/"), []byte(`*\x2f`), -1)
	return fmt.Sprintf("/* %q */", s)
}

func todo(msg string, args ...interface{}) {
	_, f, l, _ := runtime.Caller(1)
	if msg == "" {
		msg = strings.Repeat("%v ", len(args))
	}
	if traceTODO {
		fmt.Fprintf(os.Stderr, "\n\n%v:%d: TODO\n\n%s\n", f, l, fmt.Sprintf(msg, args...)) //TODOOK
	}
	panic(fmt.Errorf("\n\n%v:%d: TODO\n\n%s", f, l, fmt.Sprintf(msg, args...))) //TODOOK
}

func isFnPtr(t c99.Type, out *c99.Type) bool {
	switch x := c99.UnderlyingType(t).(type) {
	case *c99.PointerType:
		if x.Item.Kind() == c99.Function {
			if out != nil {
				*out = x.Item
			}
			return true
		}
	}
	return false
}

func (g *gen) typeComment(t c99.Type) (r string) {
	const max = 64
	defer func() {
		r = strings.Replace(r, "\n", "", -1)
		if len(r) > max+3 {
			r = r[:max/2] + "..." + r[len(r)-max/2:]
		}
	}()

	switch x := t.(type) {
	case *c99.NamedType:
		return fmt.Sprintf("T%s = %s", dict.S(x.Name), g.typeComment(x.Type))
	case *c99.PointerType:
		n := 1
		for {
			t, ok := underlyingType(x.Item, true).(*c99.PointerType)
			if !ok {
				switch {
				case x.Item == c99.Void:
					return fmt.Sprintf("%svoid", strings.Repeat("*", n))
				default:
					return fmt.Sprintf("%s%s", strings.Repeat("*", n), g.typeComment(x.Item))
				}
			}

			x = t
			n++
		}
	default:
		return g.ptyp(t, false, 1)
	}
}

func env(key, val string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}

	return val
}

func mkdir(p string) error {
	if _, err := os.Stat(p); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		return os.MkdirAll(p, 0775)
	}
	return nil
}

func cpDir(dst, src string, buf []byte) error {
	if len(buf) == 0 {
		buf = make([]byte, 1<<16)
	}
	if err := mkdir(dst); err != nil {
		return err
	}

	a, err := ioutil.ReadDir(src)
	if err != nil {
		return err
	}

	for _, v := range a {
		switch {
		case v.IsDir():
			if err = cpDir(filepath.Join(dst, v.Name()), filepath.Join(src, v.Name()), buf); err != nil {
				return err
			}
		default:
			if err = cpFile(filepath.Join(dst, v.Name()), filepath.Join(src, v.Name()), buf); err != nil {
				return err
			}
		}
	}
	return nil
}

func cpFile(dst, src string, buf []byte) (err error) {
	var d, s *os.File
	if s, err = os.Open(src); err != nil {
		return err
	}

	defer func() {
		if e := s.Close(); e != nil && err == nil {
			err = e
		}
	}()

	if d, err = os.Create(dst); err != nil {
		return err
	}

	defer func() {
		if e := d.Close(); e != nil && err == nil {
			err = e
		}
	}()

	_, err = io.CopyBuffer(d, s, buf)
	return err
}
