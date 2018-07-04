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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cznic/cc/v2"
)

/*

var <mangled name> = Lb(n)		allocate BSS(n)
const Lp<mangled name> = "type"		function prototype
const Lh<helper><num> = "args"		helper function

*/

const (
	objVersion = 1
)

var (
	objMagic = []byte{0xc6, 0x1f, 0xd0, 0xb5, 0xc4, 0x39, 0xad, 0x56}
)

func objWrite(out io.Writer, goos, goarch string, binaryVersion uint64, magic []byte, in io.Reader) error {
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

func objRead(out io.Writer, goos, goarch string, binaryVersion uint64, magic []byte, in io.Reader) error {
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
func Object(out io.Writer, goos, goarch string, in *cc.TranslationUnit) error {
	var buf bytes.Buffer
	g := newNGen(&buf, in)
	if err := g.gen(); err != nil {
		return err
	}

	return objWrite(out, goos, goarch, objVersion, objMagic, &buf)
}

// Linker produces Go files from object files.
type Linker struct {
	errs   scanner.ErrorList
	fn     string
	goarch string
	goos   string
	out    io.Writer
}

// NewLinker returns a newly created Linker writing to out.
func NewLinker(out io.Writer, goos, goarch string) *Linker {
	return &Linker{
		goarch: goarch,
		goos:   goos,
		out:    out,
	}
}

func (l *Linker) err(msg string, args ...interface{}) {
	l.errs.Add(token.Position{}, fmt.Sprintf(msg, args...))
}

// Link incerementaly links objects files.
func (l *Linker) Link(fn string, obj io.Reader) error {
	l.fn = fn
	l.link(obj)
	if len(l.errs) != 0 {
		return l.errs
	}

	return nil
}

// Close finihes the linking.
func (l *Linker) Close() (err error) {
	defer func() {
		if x, ok := l.out.(*bufio.Writer); ok {
			if e := x.Flush(); e != nil && err == nil {
				err = e
			}
		}

	}()

	panic("TODO")
}

func (l *Linker) link(obj io.Reader) {
	var b bytes.Buffer
	if err := objRead(&b, l.goos, l.goarch, objVersion, objMagic, obj); err != nil {
		l.err("%v", err)
		return
	}

	fset := token.NewFileSet()
	ast, err := parser.ParseFile(fset, "", io.MultiReader(bytes.NewBufferString("package p\n"), &b), parser.ParseComments)
	if err != nil {
		l.err("%v", err)
		return
	}

	l.file(ast)

	dbg("====")
	format.Node(os.Stdout, fset, ast)
}

func (l *Linker) file(n *ast.File) {
	w := 0
	for i := range n.Decls {
		p := &n.Decls[i]
		l.decl(p)
		if *p != nil {
			n.Decls[w] = *p
			w++
		}
	}
	n.Decls = n.Decls[:w]
}

func (l *Linker) decl(n *ast.Decl) {
	switch x := (*n).(type) {
	case *ast.GenDecl:
		w := 0
		for i := range x.Specs {
			p := &x.Specs[i]
			l.spec(p, x.Tok)
			if *p != nil {
				x.Specs[w] = *p
				w++
			}
		}
		x.Specs = x.Specs[:w]
		if w == 0 {
			*n = nil
		}
	default:
		//TODO panic(fmt.Errorf("%T", x))
	}
}

func (l *Linker) spec(n *ast.Spec, tok token.Token) {
	switch x := (*n).(type) {
	case *ast.ValueSpec:
		if tok == token.CONST && len(x.Names) == 1 {
			nm := x.Names[0].Name
			if strings.HasPrefix(nm, "L") {
				*n = nil
			}
		}
	default:
		panic(fmt.Errorf("%T", x))
	}
}
