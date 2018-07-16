// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccgo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cznic/cc/v2"
	"github.com/cznic/sortutil"
)

/*


-------------------------------------------------------------------------------
Linker constants (const Lx = "value")
-------------------------------------------------------------------------------

Ld<mangled name>	Defintion with external linkage. Value: type.
Lf			Translation unit boundary. Value: file name.

-------------------------------------------------------------------------------
Linker magic names
-------------------------------------------------------------------------------

Lb + n			-> bss + off
Ld + "foo"		-> dss + off

*/

const (
	lConstPrefix = "const L"
	objVersion   = 1
)

var (
	objMagic     = []byte{0xc6, 0x1f, 0xd0, 0xb5, 0xc4, 0x39, 0xad, 0x56}
	traceLConsts bool
)

func objWrite(out io.Writer, goos, goarch string, binaryVersion uint64, magic []byte, in io.Reader) (err error) {
	w := gzip.NewWriter(out)
	w.Header.Comment = "ccgo object file"
	var buf bytes.Buffer
	buf.Write(magic)
	fmt.Fprintf(&buf, "%s|%s|%v", goos, goarch, binaryVersion)
	w.Header.Extra = buf.Bytes()
	w.Header.ModTime = time.Now()
	w.Header.OS = 255 // Unknown OS.
	if _, err = io.Copy(w, in); err != nil {
		return err
	}

	return w.Close()
}

func objRead(out io.Writer, goos, goarch string, binaryVersion uint64, magic []byte, in io.Reader) (err error) {
	r, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("error reading object file: %v", err)
	}

	if len(r.Header.Extra) < len(magic) || !bytes.Equal(r.Header.Extra[:len(magic)], magic) {
		return fmt.Errorf("unrecognized file format")
	}

	buf := r.Header.Extra[len(magic):]
	a := bytes.Split(buf, []byte{'|'})
	if len(a) != 3 {
		return fmt.Errorf("corrupted file")
	}

	if s := string(a[0]); s != goos {
		return fmt.Errorf("invalid platform %q", s)
	}

	if s := string(a[1]); s != goarch {
		return fmt.Errorf("invalid architecture %q", s)
	}

	v, err := strconv.ParseUint(string(a[2]), 10, 64)
	if err != nil {
		return err
	}

	if v != binaryVersion {
		return fmt.Errorf("invalid version number %v", v)
	}

	if _, err := io.Copy(out, r); err != nil {
		return err
	}

	return r.Close()
}

// ReadObject reads an object file from in and writes it to out.
func ReadObject(out io.Writer, goos, goarch string, in io.Reader) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack2())
		}
		if e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	defer func() {
		if x, ok := out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	r, w := io.Pipe()
	var e2 error

	go func() {
		defer func() {
			if err := w.Close(); err != nil && e2 == nil {
				e2 = err
			}
		}()

		_, e2 = io.Copy(w, in)
	}()

	err = objRead(out, goos, goarch, objVersion, objMagic, r)
	if e2 != nil && err == nil {
		err = e2
	}
	returned = true
	return err
}

// NewSharedObject writes shared object files from in to out.
func NewSharedObject(out io.Writer, goos, goarch string, in io.Reader) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack2())
		}
		if e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	defer func() {
		if x, ok := out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	r, w := io.Pipe()
	var e2 error

	go func() {
		defer func() {
			if err := w.Close(); err != nil && e2 == nil {
				e2 = err
			}
		}()

		_, e2 = io.Copy(w, in)
	}()

	err = objWrite(out, goos, goarch, objVersion, objMagic, r)
	if e2 != nil && err == nil {
		err = e2
	}
	returned = true
	return err
}

// NewObject writes a linker object file produced from in that comes from file to
// out.
func NewObject(out io.Writer, goos, goarch, file string, in *cc.TranslationUnit) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack2())
		}
		if e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	defer func() {
		if x, ok := out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	r, w := io.Pipe()
	g := newNGen(w, in, file)

	go func() {
		defer func() {
			if err := recover(); err != nil && g.err == nil {
				g.err = fmt.Errorf("%v", err)
			}
			if err := w.Close(); err != nil && g.err == nil {
				g.err = err
			}
		}()

		g.gen()
	}()

	err = objWrite(out, goos, goarch, objVersion, objMagic, r)
	if e := g.err; e != nil && err == nil {
		err = e
	}
	returned = true
	return err
}

// Linker produces Go files from object files.
type Linker struct {
	bss            int64
	definedExterns map[string]string // name: type
	ds             []byte
	errs           scanner.ErrorList
	errsMu         sync.Mutex
	goarch         string
	goos           string
	header         string
	helpers        map[string]int
	num            int
	out            *bufio.Writer
	renamed        map[string]int
	strings        map[int]int64
	tempFile       *os.File
	text           []int
	tld            []string
	ts             int64
	visitor        visitor
	wout           *bufio.Writer

	bool2int bool
}

// NewLinker returns a newly created Linker writing to out. The header argument
// is written prior to any other linker's output, which does not include the
// package clause.
//
// The Linker must be eventually closed to prevent resource leaks.
func NewLinker(out io.Writer, header, goos, goarch string) (*Linker, error) {
	bin, ok := out.(*bufio.Writer)
	if !ok {
		bin = bufio.NewWriter(out)
	}

	tempFile, err := ioutil.TempFile("", "ccgo-linker-")
	if err != nil {
		return nil, err
	}

	r := &Linker{
		definedExterns: map[string]string{},
		goarch:         goarch,
		goos:           goos,
		header:         header,
		helpers:        map[string]int{},
		out:            bin,
		renamed:        map[string]int{},
		strings:        map[int]int64{},
		tempFile:       tempFile,
		wout:           bufio.NewWriter(tempFile),
	}
	r.visitor = visitor{r}
	return r, nil
}

func (l *Linker) w(s string, args ...interface{}) {
	if _, err := fmt.Fprintf(l.wout, s, args...); err != nil {
		panic(err)
	}
}

func (l *Linker) err(msg string, args ...interface{}) {
	l.errsMu.Lock()
	l.errs.Add(token.Position{}, fmt.Sprintf(msg, args...))
	l.errsMu.Unlock()
}

func (l *Linker) error() error {
	l.errsMu.Lock()

	defer l.errsMu.Unlock()

	if len(l.errs) == 0 {
		return nil
	}

	var a []string
	for _, v := range l.errs {
		a = append(a, v.Error())
	}
	return fmt.Errorf("%s", strings.Join(a[:sortutil.Dedupe(sort.StringSlice(a))], "\n"))
}

// Link incerementaly links objects files.
func (l *Linker) Link(fn string, obj io.Reader) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack2())
		}
		if e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	if err := l.link(fn, obj); err != nil {
		l.err("%v", err)
	}
	err = l.error()
	returned = true
	return err
}

func (l *Linker) link(fn string, obj io.Reader) error {
	r, w := io.Pipe()

	go func() {
		defer func() {
			if err := w.Close(); err != nil {
				l.err(err.Error())
			}
		}()

		if err := objRead(w, l.goos, l.goarch, objVersion, objMagic, obj); err != nil {
			l.err("%v", err)
		}
	}()

	l.w("\nconst Lf = %q\n", fn)

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s := sc.Text()
		switch {
		case strings.HasPrefix(s, lConstPrefix):
			l.lConst(s[len(lConstPrefix):])
		default:
			l.w("%s\n", s)
		}
	}
	return sc.Err()
}

func (l *Linker) lConst(s string) { // x<name> = "value"
	if traceLConsts {
		l.w("\n// const L%s\n", s)
	}
	a := strings.SplitN(s, " ", 3)
	nm := a[0]
	arg, err := strconv.Unquote(a[2])
	if err != nil {
		panic(err)
	}

	switch {
	case strings.HasPrefix(nm, "d"):
		nm = nm[1:]
		if _, ok := l.definedExterns[nm]; ok {
			l.err("external symbol redefined: %s", nm)
			break
		}

		l.definedExterns[nm] = arg
		nm = nm[1:]
		if _, ok := l.definedExterns[nm]; ok {
			l.err("external symbol redefined: %s", nm)
			break
		}

		l.definedExterns[nm] = arg
	case strings.HasPrefix(nm, "h"): // helper
		_, id := l.parseID(nm[1:])
		if x, ok := l.helpers[arg]; ok {
			_ = x
			_ = id
			panic("TODO")
			return
		}

		l.helpers[arg] = id
	default:
		todo("%s", s)
		panic("unreachable")
	}
}

func (l *Linker) parseID(s string) (string, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if c := s[i]; c < '0' || c > '9' {
			i++
			n, err := strconv.ParseInt(s[i:], 10, 31)
			if err != nil {
				panic(err)
			}

			return s[:i], int(n)
		}
	}
	panic("TODO") // missing helper local ID
}

// Close finihes the linking.
func (l *Linker) Close() (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack2())
		}
		if e != nil && err == nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	if err := l.close(); err != nil {
		l.err("%v", err)
	}
	err = l.error()
	returned = true
	return err
}

func (l *Linker) close() (err error) {
	if err = l.wout.Flush(); err != nil {
		return err
	}

	if _, err = l.tempFile.Seek(0, os.SEEK_SET); err != nil {
		return err
	}

	l.wout = l.out
	l.w("%s\n", strings.TrimSpace(l.header))

	defer func() {
		if e := l.wout.Flush(); e != nil && err == nil {
			err = e
		}
	}()

	const (
		skipBlank = iota
		collectComments
		copy
		copyFunc
		copyParen
	)

	sc := bufio.NewScanner(l.tempFile)
	state := skipBlank
	for l.scan(sc) {
		s := sc.Text()
		switch state {
		case skipBlank:
			if len(s) == 0 {
				break
			}

			l.tld = l.tld[:0]
			state = collectComments
			fallthrough
		case collectComments:
			if s == "" {
				l.emit()
				state = skipBlank
				break
			}

			l.tld = append(l.tld, s)
			if strings.HasPrefix(s, "//") {
				break
			}

			switch {
			case strings.HasPrefix(s, "const ("):
				state = copyParen
			case strings.HasPrefix(s, "var"):
				state = copy
			case strings.HasPrefix(s, "func"):
				if strings.HasSuffix(s, "}") {
					l.emit()
					state = skipBlank
					break
				}

				state = copyFunc
			case strings.HasPrefix(s, "type"):
				if !strings.HasSuffix(s, "{") {
					l.emit()
					state = skipBlank
					break
				}

				state = copyFunc
			default:
				panic(fmt.Sprintf("%q", s))
			}
		case copy:
			l.tld = append(l.tld, s)
			if len(s) == 0 {
				l.emit()
				state = skipBlank
				break
			}
		case copyFunc:
			l.tld = append(l.tld, s)
			if s == "}" {
				l.emit()
				state = skipBlank
			}
		case copyParen:
			l.tld = append(l.tld, s)
			if s == ")" {
				l.emit()
				state = skipBlank
			}
		default:
			panic(state)
		}
	}

	if err = sc.Err(); err != nil {
		return err
	}
	if len(l.tld) != 0 {
		l.emit()
	}

	l.genHelpers()

	l.w(`
var (
	bss     = crt.BSS(&bssInit[0])
	bssInit [%d]byte`,
		l.bss)
	if n := len(l.ds); n != 0 {
		if n < 16 {
			l.ds = append(l.ds, make([]byte, 16-n)...)
		}
		l.w("\n\tds = %sDS(dsInit)\n", crt)
		l.w("\tdsInit = []byte{")
		if isTesting {
			l.w("\n\t\t")
		}
		for i, v := range l.ds {
			l.w("%#02x, ", v)
			if isTesting && i&15 == 15 {
				l.w("// %#x\n\t\t", i&^15)
			}
		}
		if isTesting && len(l.ds)&15 != 0 {
			l.w("// %#x\n\t", len(l.ds)&^15)
		}
		l.w("}")
	}
	if l.ts != 0 {
		l.w("\n\tts      = %sTS(\"", crt)
		for _, v := range l.text {
			s := fmt.Sprintf("%q", dict.S(v))
			l.w("%s\\x00", s[1:len(s)-1])
		}
		l.w("\")")
	}
	l.w("\n)\n")
	return nil
}

func (l *Linker) genHelpers() {
	a := make([]string, 0, len(l.helpers))
	for k := range l.helpers {
		a = append(a, k)
	}
	sort.Strings(a)
	for _, k := range a {
		a := strings.Split(k, "$")
		l.w("\n\nfunc "+a[0], l.helpers[k])
		switch a[0] {
		case "add%d", "and%d", "div%d", "mod%d", "mul%d", "or%d", "sub%d", "xor%d":
			// eg.: [0: "add%d" 1: op "+" 2: operand type "uint32"]
			l.w("(p *%[2]s, v %[2]s) %[2]s { *p %[1]s= v; return *p }", a[1], a[2])
		case "and%db", "or%db", "xor%db":
			// eg.: [0: "or%db" 1: op "|" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			l.w(`(p *%[3]s, v %[2]s) %[2]s {
r := (%[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)) %[1]s v
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "set%d": // eg.: [0: "set%d" 1: op "" 2: operand type "uint32"]
			l.w("(p *%[2]s, v %[2]s) %[2]s { *p = v; return v }", a[1], a[2])
		case "setb%d":
			// eg.: [0: "setb%d" 1: ignored 2: operand type "uint32" 3: pack type "uint8" 4: op size 5: bits "3" 6: bitoff "2"]
			l.w("(p *%[3]s, v %[2]s) %[2]s { *p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(v) << %[6]s & ((1<<%[5]s - 1) << %[6]s)); return v<<(%[4]s-%[5]s)>>(%[4]s-%[5]s) }",
				"", a[2], a[3], a[4], a[5], a[6])
		case "rsh%d":
			// eg.: [0: "rsh%d" 1: op ">>" 2: operand type "uint32" 3: mod "32"]
			l.w("(p *%[2]s, v %[2]s) %[2]s { *p %[1]s= (v %% %[3]s); return *p }", a[1], a[2], a[3])
		case "fn%d":
			// eg.: [0: "fn%d" 1: type "unc()"]
			l.w("(p uintptr) %[1]s { return *(*%[1]s)(unsafe.Pointer(&p)) }", a[1])
		case "fp%d":
			l.w("(f %[1]s) uintptr { return *(*uintptr)(unsafe.Pointer(&f)) }", a[1])
		case "postinc%d":
			// eg.: [0: "postinc%d" 1: operand type "int32" 2: delta "1"]
			l.w("(p *%[1]s) %[1]s { r := *p; *p += %[2]s; return r }", a[1], a[2])
		case "preinc%d":
			// eg.: [0: "preinc%d" 1: operand type "int32" 2: delta "1"]
			l.w("(p *%[1]s) %[1]s { *p += %[2]s; return *p }", a[1], a[2])
		case "postinc%db":
			// eg.: [0: "postinc%db" 1: delta "1" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			l.w(`(p *%[3]s) %[2]s {
r := %[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r+%[1]s) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "preinc%db":
			// eg.: [0: "preinc%db" 1: delta "1" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			l.w(`(p *%[3]s) %[2]s {
r := (%[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)) + %[1]s
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "float2int%d":
			// eg.: [0: "float2int%d" 1: type "uint64" 2: max "18446744073709551615"]
			l.w("(f float32) %[1]s { if f > %[2]s { return 0 }; return %[1]s(f) }", a[1], a[2])
		default:
			todo("%q", a)
		}
	}
	l.w("\n")
	if l.bool2int {
		l.w(`
func bool2int(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
`)
	}
}

func (l *Linker) emit() (err error) {
	s := strings.Join(l.tld, "\n")
	fset := token.NewFileSet()
	in := io.MultiReader(bytes.NewBufferString("package p\n"), bytes.NewBufferString(s))
	file, err := parser.ParseFile(fset, "", in, parser.ParseComments)
	if err != nil {
		return err
	}

	ast.Walk(&l.visitor, file)
	e := emitor{out: l.wout}
	format.Node(&e, fset, file)

	l.tld = l.tld[:0]
	return nil
}

type visitor struct {
	*Linker
}

func (v *visitor) Visit(node ast.Node) ast.Visitor {
	switch x := node.(type) {
	case *ast.SelectorExpr:
		ast.Walk(v, x.X)
		return nil
	case *ast.BinaryExpr:
		switch x2 := x.X.(type) {
		case *ast.Ident:
			switch {
			case x2.Name == "Lb":
				rhs := x.Y.(*ast.BasicLit)
				n, err := strconv.ParseInt(rhs.Value, 10, 63)
				if err != nil {
					panic(err)
				}

				x2.Name = "bss"
				rhs.Value = fmt.Sprint(v.bss)
				v.bss += roundup(n, 8) // keep alignment
				return nil
			case x2.Name == "Ld":
				rhs := x.Y.(*ast.BasicLit)
				s, err := strconv.Unquote(rhs.Value)
				if err != nil {
					panic(err)
				}

				x2.Name = "ds"
				rhs.Value = fmt.Sprint(v.allocDS(s))
				return nil
			}
		}
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				panic(err)
			}

			x.Value = fmt.Sprintf("ts+%d %s", v.allocString(dict.SID(s)), strComment2([]byte(s)))
		}
	case *ast.Ident:
		switch {
		case strings.HasPrefix(x.Name, "X"):
			if _, ok := v.definedExterns[x.Name]; !ok {
				x.Name = fmt.Sprintf("%s%s", crt, x.Name)
			}
		case strings.HasPrefix(x.Name, "C"): // Enum constant
			x.Name = v.rename("c", x.Name[1:])
		case strings.HasPrefix(x.Name, "E"): // Tagged enum type
			x.Name = v.rename("e", x.Name[1:])
		case strings.HasPrefix(x.Name, "S"): // Tagged struct type
			x.Name = v.rename("s", x.Name[1:])
		case strings.HasPrefix(x.Name, "v"): // static
			x.Name = v.rename("v", x.Name[1:])
		case x.Name == "bool2int":
			v.bool2int = true
		}
	}
	return v
}

func (l *Linker) allocDS(s string) int64 {
	up := roundup(int64(len(l.ds)), 8) // keep alignment
	if n := up - int64(len(l.ds)); n != 0 {
		l.ds = append(l.ds, make([]byte, n)...)
	}
	r := len(l.ds)
	l.ds = append(l.ds, s...)
	return int64(r)
}

func (l *Linker) rename(prefix, nm string) string {
	n := l.renamed[nm]
	if n == 0 {
		l.num++
		n = l.num
		l.renamed[nm] = n
	}
	for {
		if c := nm[0]; c < '0' || c > '9' {
			break
		}

		nm = nm[1:]
	}
	return fmt.Sprintf("%s%d%s", prefix, n, nm)
}

func (l *Linker) allocString(s int) int64 {
	if n, ok := l.strings[s]; ok {
		return n
	}

	r := l.ts
	l.strings[s] = r
	l.ts += int64(len(dict.S(s))) + 1
	l.text = append(l.text, s)
	return r
}

type emitor struct {
	out  io.Writer
	gate bool
}

func (e *emitor) Write(b []byte) (int, error) {
	if e.gate {
		return e.out.Write(b)
	}

	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		e.gate = true
		n, err := e.out.Write(b[i+1:])
		return n + i, err
	}

	return len(b), nil
}

func (l *Linker) scan(sc *bufio.Scanner) bool {
	for {
		if !sc.Scan() {
			return false
		}

		if s := sc.Text(); strings.HasPrefix(s, lConstPrefix) {
			l.lConst2(s[len(lConstPrefix):])
			continue
		}

		return true
	}
}

func (l *Linker) lConst2(s string) { // x<name> = "value"
	if traceLConsts {
		l.w("\n// const L%s\n", s)
	}
	switch {
	case strings.HasPrefix(s, "f"):
		for k := range l.renamed {
			delete(l.renamed, k)
		}
	default:
		todo("%s", s)
		panic("unreachable")
	}
}
