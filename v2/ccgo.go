// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ccgo translates C99 ASTs to Go source code. Work In Progress. API unstable.
package ccgo

import (
	"bytes"
	"container/list"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/cznic/cc/v2"
)

var (
	isTesting bool // Test hook
)

const (
	mainSrc = `
func main() {
	psz := unsafe.Sizeof(uintptr(0))
	argv := crt.MustCalloc((len(os.Args) + 1) * int(psz))
	p := argv
	for _, v := range os.Args {
		*(*uintptr)(unsafe.Pointer(p)) = %[1]sCString(v)
		p += psz
	}
	a := os.Environ()
	env := crt.MustCalloc((len(a) + 1) * int(psz))
	p = env
	for _, v := range a {
		*(*uintptr)(unsafe.Pointer(p)) = %[1]sCString(v)
		p += psz
	}
	*(*uintptr)(unsafe.Pointer(Xenviron)) = env
	X_start(%[1]sNewTLS(), int32(len(os.Args)), argv)
}
`
	compactStack = 30
)

// Main implements a C compiler command.
//
// Example ccgo command
//
//	package main
//
//	import (
//		"os"
//
//		"github.com/cznic/ccgo/v2"
//	)
//
//	func main() { os.Exit(ccgo.Main(os.Args...)) }
func Main(args ...string) int {
	panic("TODO")
}

type gen struct { //TODO-
	bss                    int64
	ds                     []byte
	enqueued               map[interface{}]struct{}
	errs                   scanner.ErrorList
	externs                map[int]*cc.Declarator
	filenames              map[string]struct{}
	fset                   *token.FileSet
	helpers                map[string]int
	in                     []*cc.TranslationUnit
	incompleteExternArrays map[int]*cc.Declarator
	initializedExterns     map[int]struct{}
	model                  cc.Model
	needBool2int           int
	nextLabel              int
	num                    int
	nums                   map[*cc.Declarator]int
	opaqueStructTags       map[int]struct{}
	out                    io.Writer
	out0                   bytes.Buffer
	producedDeclarators    map[*cc.Declarator]struct{}
	producedEnumTags       map[int]struct{}
	producedExterns        map[int]struct{}
	producedStructTags     map[int]struct{}
	queue                  list.List
	staticDeclarators      map[int]*cc.Declarator
	strings                map[int]int64
	tCache                 map[tCacheKey]string
	text                   []int
	ts                     int64
	units                  map[*cc.Declarator]int

	escAllTLDs bool
	mainFn     bool
	needAlloca bool
	needNZ32   bool //TODO -> crt
	needNZ64   bool //TODO -> crt
	needPreInc bool
}

type ngen struct { //TODO rename to gen
	definedExterns map[int]struct{}
	fset           *token.FileSet
	helpers        map[string]int
	in             *cc.TranslationUnit
	model          cc.Model
	needBool2int   int
	nextLabel      int
	nums           map[*cc.Declarator]int
	out            io.Writer
	out0           bytes.Buffer
	tCache         map[tCacheKey]string

	mainFn     bool
	needAlloca bool
	needNZ32   bool //TODO -> crt
	needNZ64   bool //TODO -> crt
}

func newNGen(out io.Writer, in *cc.TranslationUnit) *ngen { //TODO rename to newGen
	return &ngen{
		definedExterns: map[int]struct{}{},
		helpers:        map[string]int{},
		in:             in,
		model:          in.Model,
		nums:           map[*cc.Declarator]int{},
		out:            out,
		tCache:         map[tCacheKey]string{},
	}
}

func newGen(out io.Writer, in []*cc.TranslationUnit) *gen { //TODO-
	return &gen{
		enqueued:  map[interface{}]struct{}{},
		externs:   map[int]*cc.Declarator{},
		filenames: map[string]struct{}{},
		helpers:   map[string]int{},
		in:        in,
		incompleteExternArrays: map[int]*cc.Declarator{},
		initializedExterns:     map[int]struct{}{},
		nums:                   map[*cc.Declarator]int{},
		opaqueStructTags:       map[int]struct{}{},
		out:                    out,
		producedDeclarators:    map[*cc.Declarator]struct{}{},
		producedEnumTags:       map[int]struct{}{},
		producedExterns:        map[int]struct{}{},
		producedStructTags:     map[int]struct{}{},
		staticDeclarators:      map[int]*cc.Declarator{},
		strings:                map[int]int64{},
		tCache:                 map[tCacheKey]string{},
		units:                  map[*cc.Declarator]int{},
	}
}

func (g *gen) enqueue(n interface{}) {
	if _, ok := g.enqueued[n]; ok {
		return
	}

	g.enqueued[n] = struct{}{}
	switch x := n.(type) {
	case *cc.Declarator:
		if x.Linkage == cc.LinkageNone {
			return
		}

		if x.DeclarationSpecifier.IsStatic() {
			g.enqueueNumbered(x)
			return
		}

		if x.DeclarationSpecifier.IsExtern() {
			return
		}
	}

	g.queue.PushBack(n)
}

func (g *gen) enqueueNumbered(n *cc.Declarator) {
	if _, ok := g.nums[n]; ok {
		return
	}

	g.num++
	g.nums[n] = g.num
	g.queue.PushBack(n)
}

func (g *gen) gen(cmd bool) (err error) {
	if len(g.in) == 0 {
		return fmt.Errorf("no translation unit passed")
	}

	g.model = g.in[0].Model
	g.fset = g.in[0].FileSet
	for _, v := range g.in[1:] {
		if !g.model.Equal(v.Model) {
			return fmt.Errorf("translation units use different memory models")
		}
	}

	if err := g.collectSymbols(); err != nil {
		return err
	}

	g.w(`
var _ unsafe.Pointer

const %s = uintptr(0)
`, null)
	switch {
	case cmd:
		sym, ok := g.externs[idStart]
		if !ok {
			todo("")
			break
		}

		g.w(mainSrc, crt)
		g.define(sym)
	default:
		var a []string
		for nm := range g.externs {
			a = append(a, string(dict.S(nm)))
		}
		sort.Strings(a)
		for _, nm := range a {
			g.define(g.externs[dict.SID(nm)])
		}
		todo("")
	}
	if err := g.errs.Err(); err != nil {
		return fmt.Errorf("%s", errString(err))
	}

	if g.needNZ64 {
		g.w("\n\nfunc init() { nz64 = -nz64 }")
	}
	if g.needNZ32 {
		g.w("\n\nfunc init() { nz32 = -nz32 }")
	}

	var a []string
	for k := range g.opaqueStructTags {
		a = append(a, string(dict.S(k)))
	}
	sort.Strings(a)
	for _, k := range a {
		tag := dict.SID(k)
		if _, ok := g.producedStructTags[tag]; !ok {
			g.w("\ntype S%s struct{ uintptr }\n", k)
		}
	}

	if g.needPreInc {
		g.w("\n\nfunc preinc(p *uintptr, n uintptr) uintptr { *p += n; return *p }")
	}
	if g.needAlloca {
		g.w("\n\nfunc alloca(p *[]uintptr, n int) uintptr { r := %sMustMalloc(n); *p = append(*p, r); return r }", crt)
	}

	g.genHelpers()

	g.w("\n\nvar (\n")
	if g.bss != 0 {
		g.w("bss = %sBSS(&bssInit[0])\n", crt)
		g.w("bssInit [%d]byte\n", g.bss)
	}
	if n := len(g.ds); n != 0 {
		if n < 16 {
			g.ds = append(g.ds, make([]byte, 16-n)...)
		}
		g.w("ds = %sDS(dsInit)\n", crt)
		g.w("dsInit = []byte{")
		if isTesting {
			g.w("\n")
		}
		for i, v := range g.ds {
			g.w("%#02x, ", v)
			if isTesting && i&15 == 15 {
				g.w("// %#x\n", i&^15)
			}
		}
		g.w("}\n")
	}
	if g.needNZ64 {
		g.w("nz64 float64\n")
	}
	if g.needNZ32 {
		g.w("nz32 float32\n")
	}
	g.w("ts = %sTS(\"", crt)
	for _, v := range g.text {
		s := fmt.Sprintf("%q", dict.S(v))
		g.w("%s\\x00", s[1:len(s)-1])
	}
	g.w("\")\n)\n")
	return newOpt().do(g.out, &g.out0, testFn, g.needBool2int)
}

func (g *ngen) gen() error {
	for l := g.in.ExternalDeclarationList; l != nil; l = l.ExternalDeclarationList {
		switch n := l.ExternalDeclaration; n.Case {
		case cc.ExternalDeclarationDecl: // Declaration
			f := false
			g.declaration(n.Declaration, &f)
		case cc.ExternalDeclarationFunc: // FunctionDefinition
			g.functionDefinition(n.FunctionDefinition.Declarator)
		default:
			panic(fmt.Errorf("unexpected %v", n.Case))
		}
	}
	g.genHelpers()
	return newOpt().do(g.out, &g.out0, testFn, g.needBool2int)
}

// dbg only
func (g *gen) position0(n cc.Node) token.Position { return g.in[0].FileSet.PositionFor(n.Pos(), true) }

func (g *gen) position(n *cc.Declarator) token.Position {
	return g.in[g.units[n]].FileSet.PositionFor(n.Pos(), true)
}

func (g *ngen) position(n cc.Node) token.Position {
	return g.in.FileSet.PositionFor(n.Pos(), true)
}

func (g *gen) w(s string, args ...interface{}) {
	if _, err := fmt.Fprintf(&g.out0, s, args...); err != nil {
		panic(err)
	}

	if traceWrites {
		fmt.Fprintf(os.Stderr, s, args...)
	}
}

func (g *ngen) w(s string, args ...interface{}) {
	if _, err := fmt.Fprintf(&g.out0, s, args...); err != nil {
		panic(err)
	}

	if traceWrites {
		fmt.Fprintf(os.Stderr, s, args...)
	}
}

func (g *gen) collectSymbols() error {
	for unit, t := range g.in {
		for nm, n := range t.FileScope.Idents {
			switch x := n.(type) {
			case *cc.Declarator:
				g.units[x] = unit
				if x.Type.Kind() == cc.Function && x.FunctionDefinition == nil {
					continue
				}

				switch x.Linkage {
				case cc.LinkageExternal:
					if nm == idMain {
						x.Type = &cc.FunctionType{
							Params: []cc.Type{
								cc.Int,
								&cc.PointerType{Item: &cc.PointerType{Item: cc.Char}},
							},
							Result: cc.Int,
						}
					}
					if ex, ok := g.externs[nm]; ok {
						if g.position(ex) == g.position(x) {
							break // ok
						}

						if ex.Type.Kind() == cc.Function {
							todo("")
						}

						if !ex.Type.IsCompatible(x.Type) {
							//typeDiff(ex.Type, x.Type)
							todo("", g.position(ex), ex.Type, g.position(x), x.Type)
						}

						if ex.Initializer != nil && x.Initializer != nil {
							todo("")
						}

						if prefer(ex) || !prefer(x) {
							break // ok
						}
					}

					g.externs[nm] = x
				case cc.LinkageInternal:
					// ok
				case cc.LinkageNone:
					if x.DeclarationSpecifier.IsTypedef() {
						// nop ATM
						break
					}

					todo("")
				default:
					todo("")
				}
			case *cc.EnumerationConstant:
				// nop
			default:
				todo("%T", x)
			}
		}
	}
	return nil
}

func (g gen) escaped(n *cc.Declarator) bool {
	if isVaList(n.Type) {
		return false
	}

	if n.AddressTaken || n.IsTLD() && g.escAllTLDs {
		return true
	}

	switch cc.UnderlyingType(n.Type).(type) {
	case *cc.ArrayType:
		return !n.IsFunctionParameter
	case
		*cc.StructType,
		*cc.TaggedStructType,
		*cc.TaggedUnionType,
		*cc.UnionType:

		return n.IsTLD() || n.DeclarationSpecifier.IsStatic()
	default:
		return false
	}
}

func (g ngen) escaped(n *cc.Declarator) bool {
	if isVaList(n.Type) {
		return false
	}

	if n.IsTLD() || n.AddressTaken {
		return true
	}

	switch cc.UnderlyingType(n.Type).(type) {
	case *cc.ArrayType:
		return !n.IsFunctionParameter
	case
		*cc.StructType,
		*cc.TaggedStructType,
		*cc.TaggedUnionType,
		*cc.UnionType:

		return n.IsTLD() || n.DeclarationSpecifier.IsStatic()
	default:
		return false
	}
}

func (g *gen) allocString(s int) int64 {
	if n, ok := g.strings[s]; ok {
		return n
	}

	r := g.ts
	g.strings[s] = r
	g.ts += int64(len(dict.S(s))) + 1
	g.text = append(g.text, s)
	return r
}

func (g *gen) shiftMod(t cc.Type) int {
	if g.model.Sizeof(t) > 4 {
		return 64
	}

	return 32
}

func (g *gen) registerHelper(a ...interface{}) string {
	b := make([]string, len(a))
	for i, v := range a {
		b[i] = fmt.Sprint(v)
	}
	k := strings.Join(b, "$")
	if id := g.helpers[k]; id != 0 {
		return fmt.Sprintf(b[0], id)
	}

	id := len(g.helpers) + 1
	g.helpers[k] = id
	return fmt.Sprintf(b[0], id)
}

func (g *ngen) registerHelper(a ...interface{}) string {
	b := make([]string, len(a))
	for i, v := range a {
		b[i] = fmt.Sprint(v)
	}
	k := strings.Join(b, "$")
	if id := g.helpers[k]; id != 0 {
		return fmt.Sprintf(b[0], id)
	}

	id := len(g.helpers) + 1
	g.helpers[k] = id
	return fmt.Sprintf(b[0], id)
}

func (g *gen) genHelpers() {
	a := make([]string, 0, len(g.helpers))
	for k := range g.helpers {
		a = append(a, k)
	}
	sort.Strings(a)
	for _, k := range a {
		a := strings.Split(k, "$")
		g.w("\n\nfunc "+a[0], g.helpers[k])
		switch a[0] {
		case "add%d", "and%d", "div%d", "mod%d", "mul%d", "or%d", "sub%d", "xor%d":
			// eg.: [0: "add%d" 1: op "+" 2: operand type "uint32"]
			g.w("(p *%[2]s, v %[2]s) %[2]s { *p %[1]s= v; return *p }", a[1], a[2])
		case "and%db", "or%db", "xor%db":
			// eg.: [0: "or%db" 1: op "|" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			g.w(`(p *%[3]s, v %[2]s) %[2]s {
r := (%[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)) %[1]s v
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "set%d": // eg.: [0: "set%d" 1: op "" 2: operand type "uint32"]
			g.w("(p *%[2]s, v %[2]s) %[2]s { *p = v; return v }", a[1], a[2])
		case "set%db":
			// eg.: [0: "set%db" 1: ignored 2: operand type "uint32" 3: pack type "uint8" 4: op size 5: bits "3" 6: bitoff "2"]
			g.w("(p *%[3]s, v %[2]s) %[2]s { *p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(v) << %[6]s & ((1<<%[5]s - 1) << %[6]s)); return v<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)}",
				"", a[2], a[3], a[4], a[5], a[6])
		case "rsh%d":
			// eg.: [0: "rsh%d" 1: op ">>" 2: operand type "uint32" 3: mod "32"]
			g.w("(p *%[2]s, v %[2]s) %[2]s { *p %[1]s= (v %% %[3]s); return *p }", a[1], a[2], a[3])
		case "fn%d":
			// eg.: [0: "fn%d" 1: type "unc()"]
			g.w("(p uintptr) %[1]s { return *(*%[1]s)(unsafe.Pointer(&p)) }", a[1])
		case "fp%d":
			g.w("(f %[1]s) uintptr { return *(*uintptr)(unsafe.Pointer(&f)) }", a[1])
		case "postinc%d":
			// eg.: [0: "postinc%d" 1: operand type "int32" 2: delta "1"]
			g.w("(p *%[1]s) %[1]s { r := *p; *p += %[2]s; return r }", a[1], a[2])
		case "preinc%d":
			// eg.: [0: "preinc%d" 1: operand type "int32" 2: delta "1"]
			g.w("(p *%[1]s) %[1]s { *p += %[2]s; return *p }", a[1], a[2])
		case "postinc%db":
			// eg.: [0: "postinc%db" 1: delta "1" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			g.w(`(p *%[3]s) %[2]s {
r := %[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r+%[1]s) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "preinc%db":
			// eg.: [0: "preinc%db" 1: delta "1" 2: operand type "int32" 3: pack type "uint8" 4: op size "32" 5: bits "3" 6: bitoff "2"]
			g.w(`(p *%[3]s) %[2]s {
r := (%[2]s(*p>>%[6]s)<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)) + %[1]s
*p = (*p &^ ((1<<%[5]s - 1) << %[6]s)) | (%[3]s(r) << %[6]s & ((1<<%[5]s - 1) << %[6]s))
return r<<(%[4]s-%[5]s)>>(%[4]s-%[5]s)
}`, a[1], a[2], a[3], a[4], a[5], a[6])
		case "float2int%d":
			// eg.: [0: "float2int%d" 1: type "uint64" 2: max "18446744073709551615"]
			g.w("(f float32) %[1]s { if f > %[2]s { return 0 }; return %[1]s(f) }", a[1], a[2])
		default:
			todo("%q", a)
		}
	}
}

func (g *ngen) genHelpers() {
	a := make([]string, 0, len(g.helpers))
	for k := range g.helpers {
		a = append(a, k)
	}
	sort.Strings(a)
	for _, k := range a {
		a := strings.Split(k, "$")
		fmt.Fprintf(g.out, "\nconst Lh"+a[0]+" = %q", g.helpers[k], strings.Join(a[1:], "$"))
	}
	fmt.Fprintln(g.out)
}
