// Copyright 2017 The CCGO Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccgo

import (
	"bytes"
	"fmt"

	"github.com/cznic/ir"
	"github.com/cznic/sqlite2go/internal/c99"
)

type tCacheKey struct {
	c99.Type
	bool
}

func isVaList(t c99.Type) bool {
	x, ok := t.(*c99.NamedType)
	return ok && (x.Name == idVaList || x.Name == idBuiltinVaList)
}

func (g *gen) typ(t c99.Type) string { return g.ptyp(t, true, 0) }

func (g *gen) ptyp(t c99.Type, ptr2uintptr bool, lvl int) (r string) {
	k := tCacheKey{t, ptr2uintptr}
	if s, ok := g.tCache[k]; ok {
		return s
	}

	defer func() { g.tCache[k] = r }()

	if ptr2uintptr {
		if t.Kind() == c99.Ptr && !isVaList(t) {
			if _, ok := t.(*c99.NamedType); !ok {
				g.enqueue(t)
				return "uintptr"
			}
		}

		if x, ok := t.(*c99.ArrayType); ok && x.Size.Value == nil {
			return "uintptr"
		}
	}

	switch x := t.(type) {
	case *c99.ArrayType:
		if x.Size.Value == nil {
			return fmt.Sprintf("*%s", g.ptyp(x.Item, ptr2uintptr, lvl))
		}

		return fmt.Sprintf("[%d]%s", x.Size.Value.(*ir.Int64Value).Value, g.ptyp(x.Item, ptr2uintptr, lvl))
	case *c99.FunctionType:
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "func(%sTLS", crt)
		switch {
		case len(x.Params) == 1 && x.Params[0].Kind() == c99.Void:
			// nop
		default:
			for _, v := range x.Params {
				switch underlyingType(v, true).(type) {
				case *c99.ArrayType:
					fmt.Fprintf(&buf, ", uintptr")
				default:
					fmt.Fprintf(&buf, ", %s", g.typ(v))
				}
			}
		}
		if x.Variadic {
			fmt.Fprintf(&buf, ", ...interface{}")
		}
		buf.WriteString(")")
		if x.Result != nil && x.Result.Kind() != c99.Void {
			buf.WriteString(" " + g.typ(x.Result))
		}
		return buf.String()
	case *c99.NamedType:
		if isVaList(x) {
			if ptr2uintptr {
				return "*[]interface{}"
			}

			return fmt.Sprintf("%s", dict.S(x.Name))
		}

		g.enqueue(t)
		t := x.Type
		for {
			if x, ok := t.(*c99.NamedType); ok {
				t = x.Type
				continue
			}

			break
		}
		return g.ptyp(t, ptr2uintptr, lvl)
	case *c99.PointerType:
		if x.Item.Kind() == c99.Void {
			return "uintptr"
		}

		switch {
		case x.Kind() == c99.Function:
			todo("")
		default:
			return fmt.Sprintf("*%s", g.ptyp(x.Item, ptr2uintptr, lvl+1))
		}
	case *c99.StructType:
		var buf bytes.Buffer
		buf.WriteString("struct{")
		layout := g.model.Layout(x)
		for i, v := range x.Fields {
			if v.Bits < 0 {
				continue
			}

			if v.Bits != 0 {
				if layout[i].Bitoff == 0 {
					fmt.Fprintf(&buf, "F%d %s;", layout[i].Offset, g.typ(layout[i].PackedType))
					if lvl == 0 {
						fmt.Fprintf(&buf, "\n")
					}
				}
				continue
			}

			switch {
			case v.Name == 0:
				fmt.Fprintf(&buf, "_ ")
			default:
				fmt.Fprintf(&buf, "%s ", mangleIdent(v.Name, true))
			}
			fmt.Fprintf(&buf, "%s;", g.ptyp(v.Type, ptr2uintptr, lvl+1))
			if lvl == 0 && ptr2uintptr && v.Type.Kind() == c99.Ptr {
				fmt.Fprintf(&buf, "// %s\n", g.ptyp(v.Type, false, lvl+1))
			}
		}
		buf.WriteByte('}')
		return buf.String()
	case *c99.EnumType:
		if x.Tag == 0 {
			return g.typ(x.Enums[0].Operand.Type)
		}

		g.enqueue(x)
		return fmt.Sprintf("E%s", dict.S(x.Tag))
	case *c99.TaggedEnumType:
		g.enqueue(x)
		return fmt.Sprintf("E%s", dict.S(x.Tag))
	case *c99.TaggedStructType:
		g.enqueue(x)
		return fmt.Sprintf("S%s", dict.S(x.Tag))
	case *c99.TaggedUnionType:
		g.enqueue(x)
		return fmt.Sprintf("U%s", dict.S(x.Tag))
	case c99.TypeKind:
		switch x {
		case
			c99.Char,
			c99.Int,
			c99.Long,
			c99.LongLong,
			c99.SChar,
			c99.Short:

			return fmt.Sprintf("int%d", g.model[x].Size*8)
		case
			c99.UChar,
			c99.UShort,
			c99.UInt,
			c99.ULong,
			c99.ULongLong:

			return fmt.Sprintf("uint%d", g.model[x].Size*8)
		case c99.Float:
			return fmt.Sprintf("float32")
		case
			c99.Double,
			c99.LongDouble:

			return fmt.Sprintf("float64")
		default:
			todo("", x)
		}
	case *c99.UnionType:
		al := int64(g.model.Alignof(x))
		sz := g.model.Sizeof(x)
		switch {
		case al == sz:
			return fmt.Sprintf("struct{X int%d}", 8*sz)
		default:
			return fmt.Sprintf("struct{X int%d; _ [%d]byte}", 8*al, sz-al) //TODO use precomputed padding from model layout?
		}
	default:
		todo("%v %T %v\n%v", t, x, ptr2uintptr, pretty(x))
	}
	panic("unreachable")
}

func prefer(d *c99.Declarator) bool {
	if d.DeclarationSpecifier.IsExtern() {
		return false
	}

	if d.Initializer != nil || d.FunctionDefinition != nil {
		return true
	}

	t := d.Type
	for {
		switch x := underlyingType(t, true).(type) {
		case *c99.ArrayType:
			return x.Size.Type != nil
		case *c99.FunctionType:
			return false
		case
			*c99.EnumType,
			*c99.StructType:

			return true
		case *c99.PointerType:
			t = x.Item
		case *c99.TaggedStructType:
			return x.Type != nil
		case c99.TypeKind:
			if x.IsScalarType() || x == c99.Void {
				return true
			}

			panic(x)
		default:
			panic(x)
		}
	}
}

func underlyingType(t c99.Type, enums bool) c99.Type {
	for {
		switch x := t.(type) {
		case
			*c99.ArrayType,
			*c99.FunctionType,
			*c99.PointerType,
			*c99.StructType,
			*c99.UnionType:

			return x
		case *c99.EnumType:
			if enums {
				return x
			}

			return x.Enums[0].Operand.Type
		case *c99.NamedType:
			if x.Type == nil {
				return x
			}

			t = x.Type
		case *c99.TaggedEnumType:
			if x.Type == nil {
				return x
			}

			t = x.Type
		case *c99.TaggedStructType:
			if x.Type == nil {
				return x
			}

			t = x.Type
		case *c99.TaggedUnionType:
			if x.Type == nil {
				return x
			}

			t = x.Type
		case c99.TypeKind:
			switch x {
			case
				c99.Char,
				c99.Double,
				c99.Float,
				c99.Int,
				c99.Long,
				c99.LongDouble,
				c99.LongLong,
				c99.SChar,
				c99.Short,
				c99.UChar,
				c99.UInt,
				c99.ULong,
				c99.ULongLong,
				c99.UShort,
				c99.Void:

				return x
			default:
				panic(fmt.Errorf("%v", x))
			}
		default:
			panic(fmt.Errorf("%T", x))
		}
	}
}
