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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cznic/cc/v2"
)

/*

Lb + n					bss + m
const Lp<mangled name> = "type"		function prototype
const Lh<helper><num> = "args"		helper function


*/

const (
	objVersion = 1
)

var (
	objMagic = []byte{0xc6, 0x1f, 0xd0, 0xb5, 0xc4, 0x39, 0xad, 0x56}
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
	if _, err := io.Copy(w, in); err != nil {
		return err
	}

	return w.Close()
}

func objRead(out io.Writer, goos, goarch string, binaryVersion uint64, magic []byte, in io.Reader) (err error) {
	r, err := gzip.NewReader(in)
	if err != nil {
		return err
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

// Object writes a linker object file produced from in to out.
func Object(out io.Writer, goos, goarch string, in *cc.TranslationUnit) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack())
		}
	}()

	defer func() {
		if x, ok := out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	var buf bytes.Buffer
	g := newNGen(&buf, in)
	if err := g.gen(); err != nil {
		returned = true
		return err
	}

	err = objWrite(out, goos, goarch, objVersion, objMagic, &buf)
	returned = true
	return err
}

type prototype struct {
	pos string
	typ string
}

// Linker produces Go files from object files.
type Linker struct {
	bss        int64
	ds         []byte
	errMu      sync.Mutex
	errs       scanner.ErrorList
	goarch     string
	goos       string
	helpers    map[string]int
	num        int
	out        io.Writer
	prototypes map[string]prototype
	renamed    map[string]int
	strings    map[int]int64
	text       []int
	tld        []string
	ts         int64
	visitor

	bool2int bool
}

// NewLinker returns a newly created Linker writing to out. The header argument
// is written prior to any other linker's output, which does not include the
// package clause.
func NewLinker(out io.Writer, header, goos, goarch string) (*Linker, error) {
	header = strings.TrimSpace(header)
	if header != "" {
		if _, err := fmt.Fprintln(out, header); err != nil {
			return nil, err
		}
	}

	r := &Linker{
		goarch:     goarch,
		goos:       goos,
		helpers:    map[string]int{},
		out:        out,
		prototypes: map[string]prototype{},
		renamed:    map[string]int{},
		strings:    map[int]int64{},
	}
	r.visitor.Linker = r
	return r, nil
}

func (l *Linker) w(s string, args ...interface{}) {
	if _, err := fmt.Fprintf(l.out, s, args...); err != nil {
		panic(err)
	}
}

func (l *Linker) err(msg string, args ...interface{}) {
	l.errMu.Lock()
	l.errs.Add(token.Position{}, fmt.Sprintf(msg, args...))
	l.errMu.Unlock()
}

// Link incerementaly links objects files.
func (l *Linker) Link(fn string, obj io.Reader) (err error) {
	returned := false

	defer func() {
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack())
		}

		for k := range l.renamed {
			delete(l.renamed, k)
		}
	}()

	l.w("\n// linking %s\n", fn)
	l.link(obj)
	if len(l.errs) != 0 {
		err = l.errs
	}

	returned = true
	return err
}

// Close finihes the linking.
func (l *Linker) Close() (err error) {
	returned := false

	defer func() {
		l.out = nil
		e := recover()
		if !returned && err == nil {
			err = fmt.Errorf("PANIC: %v\n%s", e, debugStack())
		}
	}()

	defer func() {
		if x, ok := l.out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	l.genHelpers()

	l.w(`
var (
	bss     = crt.BSS(&bssInit[0])
	bssInit [%d]byte
`,
		l.bss)
	if n := len(l.ds); n != 0 {
		if n < 16 {
			l.ds = append(l.ds, make([]byte, 16-n)...)
		}
		l.w("\tds = %sDS(dsInit)\n", crt)
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
		l.w("}\n")
	}
	if l.ts != 0 {
		l.w("\tts      = %sTS(\"", crt)
		for _, v := range l.text {
			s := fmt.Sprintf("%q", dict.S(v))
			l.w("%s\\x00", s[1:len(s)-1])
		}
		l.w("\")")
	}
	l.w("\n)\n")
	returned = true
	return err
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
		case "set%db":
			// eg.: [0: "set%db" 1: ignored 2: operand type "uint32" 3: pack type "uint8" 4: op size 5: bits "3" 6: bitoff "2"]
			l.w("(p *%[3]s, v %[2]s) %[2]s { *p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(v) << %[6]s & ((1<<%[5]s - 1) << %[6]s)); return v<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)}",
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

func (l *Linker) link(obj io.Reader) {
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

	sc := bufio.NewScanner(r)

	const (
		skipBlank = iota
		collectComments
		copy
		copyFunc
		copyParen
	)

	state := skipBlank
	for sc.Scan() {
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
			l.tld = append(l.tld, s)
			if strings.HasPrefix(s, "//") {
				break
			}

			switch {
			case strings.HasPrefix(s, "const ("):
				state = copyParen
			case strings.HasPrefix(s, "const L"):
				l.lConst(s)
				state = skipBlank
			case strings.HasPrefix(s, "var"):
				state = copy
			case strings.HasPrefix(s, "func"):
				if strings.HasSuffix(s, "}") {
					l.emit(w)
					state = skipBlank
					break
				}

				state = copyFunc
			case strings.HasPrefix(s, "type"):
				if !strings.HasSuffix(s, "{") {
					l.emit(w)
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
				l.emit(w)
				state = skipBlank
				break
			}
		case copyFunc:
			l.tld = append(l.tld, s)
			if s == "}" {
				l.emit(w)
				state = skipBlank
			}
		case copyParen:
			l.tld = append(l.tld, s)
			if s == ")" {
				l.emit(w)
				state = skipBlank
			}
		default:
			panic(state)
		}
	}
	if err := sc.Err(); err != nil {
		l.err(err.Error())
	}
	if len(l.tld) != 0 {
		l.emit(w)
	}
}

func (l *Linker) emit(w *io.PipeWriter) (err error) {
	s := strings.Join(l.tld, "\n")
	fset := token.NewFileSet()
	in := io.MultiReader(bytes.NewBufferString("package p\n"), bytes.NewBufferString(s))
	file, err := parser.ParseFile(fset, "", in, parser.ParseComments)
	if err != nil {
		return err
	}

	ast.Walk(&l.visitor, file)
	e := emitor{out: l.out}
	format.Node(&e, fset, file)

	l.tld = l.tld[:0]
	return nil
}

func (l *Linker) lConst(s string) {
	// ex `const LpX__builtin_exit = "func(crt.TLS, int32)"`
	a := strings.SplitN(s, " ", 4)
	nm := a[1][1:]
	arg, err := strconv.Unquote(a[3])
	if err != nil {
		panic(err)
	}

	switch {
	case strings.HasPrefix(nm, "h"): // helper
		_, id := l.parseID(nm[1:])
		if x, ok := l.helpers[arg]; ok {
			_ = x
			_ = id
			panic("TODO")
			return
		}

		l.helpers[arg] = id
	case strings.HasPrefix(nm, "p"): // prototype
		nm = nm[1:]
		if x, ok := l.prototypes[nm]; ok {
			_ = x
			panic("TODO") // check consistency
			return
		}

		l.prototypes[nm] = prototype{pos: l.pos(), typ: arg}
	default:
		panic(fmt.Sprintf("%s\n%q %q", strings.Join(l.tld, "\n"), nm, arg))
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

func (l *Linker) pos() string {
	if len(l.tld) != 1 {
		return ""
	}

	s := l.tld[0]
	if strings.HasPrefix(s, "// ") {
		return s[3:]
	}

	return ""
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
