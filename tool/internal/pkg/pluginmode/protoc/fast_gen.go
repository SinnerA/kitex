package protoc

import (
	"fmt"
	_ "unsafe"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type FastAPIGenerator interface {
	GenFastRead(g *protogen.GeneratedFile)
	GenFastWrite(g *protogen.GeneratedFile)
	GenFastSize(g *protogen.GeneratedFile)
	GenFastConst(g *protogen.GeneratedFile)
}

func newFastGen(gen *protogen.Plugin, f *protogen.File) *fastgen {
	return &fastgen{gen: gen, f: f}
}

type fastgen struct {
	gen *protogen.Plugin
	f   *protogen.File
}

func (fg *fastgen) NewMessage(m *protogen.Message) FastAPIGenerator {
	return &fgMessage{m: m}
}

func (fg *fastgen) NewField(f *protogen.Field) FastAPIGenerator {
	field := &fgField{}
	field.number = fmt.Sprintf("%d", f.Desc.Number())
	field.body = fg.newFieldBody(f.Desc, f.Desc.IsList())
	field.f = f
	return field
}

func (fg *fastgen) newFieldBody(desc protoreflect.FieldDescriptor, isList bool) fastAPIBodyGenerator {
	// map
	if desc.IsMap() {
		// map
		b := &bodyMap{}
		k, v := desc.MapKey(), desc.MapValue()
		b.Key, b.Value = fg.newFieldBody(k, false), fg.newFieldBody(v, false)
		b.TypeName = fmt.Sprintf("map[%s]%s", b.Key.typeName(), b.Value.typeName())
		return b
	}
	// []
	if isList {
		b := &bodyList{}
		b.IsPacked = desc.IsPacked()
		b.Element = fg.newFieldBody(desc, false)
		b.TypeName = "[]" + b.Element.typeName()
		return b
	}
	// Enum
	kind := desc.Kind()
	switch kind {
	case protoreflect.MessageKind:
		// *struct
		b := &bodyMessage{}
		// FIXME: Any is unsupported.
		if desc.Message().ParentFile().Package() != fg.f.Desc.Package() {
			b.TypeName = "*" + string(desc.Message().FullName())
		} else {
			b.TypeName = "*" + string(desc.Message().Name())
		}
		return b
	case protoreflect.EnumKind:
		// Enum
		b := &bodyEnum{}
		// FIXME: Any is unsupported.
		if desc.Enum().ParentFile().Package() != fg.f.Desc.Package() {
			b.TypeName = string(desc.Enum().FullName())
		} else {
			b.TypeName = string(desc.Enum().Name())
		}
		return b
	default:
		b := &bodyBase{}
		b.TypeName = kindGoType[kind]
		b.APIType = kindAPIType[kind]
		return b
	}
}

var _ FastAPIGenerator = &fgMessage{}

type fgMessage struct {
	m *protogen.Message
}

func (f *fgMessage) name() string {
	return f.m.GoIdent.GoName
}

func (f *fgMessage) GenFastRead(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) FastRead(buf []byte, _type int8, number int32) (offset int, err error) {", f.name()))
	// switch case
	g.P("switch number {")
	for i := range f.m.Fields {
		number := f.m.Fields[i].Desc.Number()
		g.P(fmt.Sprintf("case %d:", number))
		g.P(fmt.Sprintf("offset, err = x.fastReadField%d(buf, _type)", number))
		g.P("if err != nil { goto ReadFieldError }")
	}
	g.P(`default:`)
	g.P(`offset, err = bprotoc.Binary.Skip(buf, _type, number)`)
	g.P(`if err != nil { goto SkipFieldError }`)
	g.P(`}`)
	// return
	g.P(`return offset, nil`)
	g.P(`SkipFieldError:`)
	g.P(`return offset, fmt.Errorf("%T cannot parse invalid wire-format data, error: %s", x, err)`)
	g.P(`ReadFieldError:`)
	g.P(`return offset, fmt.Errorf("%T read field %d '%s' error: %s", x, number, fieldIDToName_` + f.name() + `[number], err)`)
	g.P(`}`)
	g.P()
}

func (f *fgMessage) GenFastWrite(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) FastWrite(buf []byte) (offset int) {", f.name()))
	// switch case
	g.P("if x == nil { return offset }")
	for i := range f.m.Fields {
		number := f.m.Fields[i].Desc.Number()
		g.P(fmt.Sprintf("offset += x.fastWriteField%d(buf[offset:])", number))
	}
	g.P(`return offset`)
	g.P(`}`)
	g.P()
}

func (f *fgMessage) GenFastSize(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) Size() (n int) {", f.name()))
	// switch case
	g.P("if x == nil { return n }")
	for i := range f.m.Fields {
		number := f.m.Fields[i].Desc.Number()
		g.P(fmt.Sprintf("n += x.sizeField%d()", number))
	}
	g.P(`return n`)
	g.P(`}`)
	g.P()
}

func (f *fgMessage) GenFastConst(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("var fieldIDToName_%s = map[int32]string {", f.name()))
	for _, field := range f.m.Fields {
		g.P(fmt.Sprintf(`%d: "%s",`, field.Desc.Number(), field.GoName))
	}
	g.P(`}`)
	g.P()
}

var _ FastAPIGenerator = &fgField{}

type fgField struct {
	f      *protogen.Field
	number string // field number string
	body   fastAPIBodyGenerator
}

func (f *fgField) parentName() string {
	return f.f.Parent.GoIdent.GoName
}

func (f *fgField) name() string {
	return f.f.GoName
}

func (f *fgField) GenFastRead(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) fastReadField%s(buf []byte, _type int8) (offset int, err error) {", f.parentName(), f.number))
	f.body.bodyFastRead(g, fmt.Sprintf("x.%s", f.name()), f.f.Desc.IsList())
	g.P("}")
	g.P()
}

func (f *fgField) GenFastWrite(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) fastWriteField%s(buf []byte) (offset int) {", f.parentName(), f.number))

	setter := fmt.Sprintf("x.%s", f.name())
	switch {
	case f.f.Desc.IsMap() || f.f.Desc.IsList() || f.f.Desc.Kind() == protoreflect.BytesKind:
		g.P(fmt.Sprintf("if len(%s) == 0 { return offset }", setter))
	case f.f.Desc.Kind() == protoreflect.BoolKind:
		g.P(fmt.Sprintf("if !%s { return offset }", setter))
	case f.f.Desc.Kind() == protoreflect.StringKind:
		g.P(fmt.Sprintf(`if %s == "" { return offset }`, setter))
	case f.f.Desc.Kind() == protoreflect.MessageKind:
		g.P(fmt.Sprintf("if %s == nil { return offset }", setter))
	default:
		g.P(fmt.Sprintf("if %s == 0 { return offset }", setter))
	}
	f.body.bodyFastWrite(g, setter, f.number)
	g.P("return offset")
	g.P("}")
	g.P()
}

func (f *fgField) GenFastSize(g *protogen.GeneratedFile) {
	g.P(fmt.Sprintf("func (x *%s) sizeField%s() (n int) {", f.parentName(), f.number))

	setter := fmt.Sprintf("x.%s", f.name())
	switch {
	case f.f.Desc.IsMap() || f.f.Desc.IsList() || f.f.Desc.Kind() == protoreflect.BytesKind:
		g.P(fmt.Sprintf("if len(%s) == 0 { return n }", setter))
	case f.f.Desc.Kind() == protoreflect.BoolKind:
		g.P(fmt.Sprintf("if !%s { return n }", setter))
	case f.f.Desc.Kind() == protoreflect.StringKind:
		g.P(fmt.Sprintf(`if %s == "" { return n }`, setter))
	case f.f.Desc.Kind() == protoreflect.MessageKind:
		g.P(fmt.Sprintf("if %s == nil { return n }", setter))
	default:
		g.P(fmt.Sprintf("if %s == 0 { return n }", setter))
	}
	f.body.bodyFastSize(g, setter, f.number)
	g.P("return n")
	g.P("}")
	g.P()
}

func (f *fgField) GenFastConst(g *protogen.GeneratedFile) {
	return
}

type fastAPIBodyGenerator interface {
	typeName() string
	bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool)
	bodyFastWrite(g *protogen.GeneratedFile, setter string, number string)
	bodyFastSize(g *protogen.GeneratedFile, setter string, number string)
}

// no *struct here
type bodyBase struct {
	TypeName string
	APIType  string
}

func (f *bodyBase) typeName() string {
	return f.TypeName
}

func (f *bodyBase) bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool) {
	if !appendSetter {
		g.P(fmt.Sprintf("%s, offset, err = bprotoc.Binary.Read%s(buf[offset:], _type)", setter, f.APIType))
		g.P(`return offset, err`)
		return
	}
	// appendSetter
	g.P(fmt.Sprintf("var v %s", f.TypeName))
	g.P(fmt.Sprintf("v, offset, err = bprotoc.Binary.Read%s(buf[offset:], _type)", f.APIType))
	g.P(`if err != nil { return offset, err }`)
	g.P(fmt.Sprintf("%s = append(%s, v)", setter, setter))
	g.P(`return offset, err`)
}

func (f *bodyBase) bodyFastWrite(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("offset += bprotoc.Binary.Write%s(buf[offset:], %s, %s)", f.APIType, number, setter))
}

func (f *bodyBase) bodyFastSize(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("n += bprotoc.Binary.Size%s(%s, %s)", f.APIType, number, setter))
}

// enum
type bodyEnum struct {
	TypeName string
}

func (f *bodyEnum) typeName() string {
	return f.TypeName
}

func (f *bodyEnum) bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool) {
	g.P("var v int32")
	g.P("v, offset, err = bprotoc.Binary.ReadInt32(buf[offset:], _type)")
	g.P(`if err != nil { return offset, err }`)
	if appendSetter {
		g.P(fmt.Sprintf("%s = append(%s, %s(v))", setter, setter, f.TypeName))
	} else {
		g.P(fmt.Sprintf("%s = %s(v)", setter, f.TypeName))
	}
	g.P("return offset, nil")
}

func (f *bodyEnum) bodyFastWrite(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("offset += bprotoc.Binary.WriteInt32(buf[offset:], %s, int32(%s))", number, setter))
}

func (f *bodyEnum) bodyFastSize(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("n += bprotoc.Binary.SizeInt32(%s, int32(%s))", number, setter))
}

// *struct
type bodyMessage struct {
	TypeName string
}

func (f *bodyMessage) typeName() string {
	return f.TypeName
}

func (f *bodyMessage) bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool) {
	g.P("var v ", f.TypeName[1:]) // type name is *struct, trim * here
	g.P("offset, err = bprotoc.Binary.ReadMessage(buf[offset:], _type, &v)")
	g.P(`if err != nil { return offset, err }`)
	if appendSetter {
		g.P(fmt.Sprintf("%s = append(%s, &v)", setter, setter))
	} else {
		g.P(setter, " = &v")
	}
	g.P("return offset, nil")
}

func (f *bodyMessage) bodyFastWrite(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("offset += bprotoc.Binary.WriteMessage(buf[offset:], %s, %s)", number, setter))
}

func (f *bodyMessage) bodyFastSize(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("n += bprotoc.Binary.SizeMessage(%s, %s)", number, setter))
}

// string, bytes, *struct, no packed map
type bodyList struct {
	TypeName string // []xxx
	IsPacked bool
	Element  fastAPIBodyGenerator
}

func (f *bodyList) typeName() string {
	return f.TypeName
}

func (f *bodyList) bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool) {
	// packed
	if f.IsPacked {
		g.P(`offset, err = bprotoc.Binary.ReadList(buf[offset:], _type,`)
		g.P(`func(buf []byte, _type int8) (n int, err error) {`)
		f.Element.bodyFastRead(g, setter, appendSetter)
		g.P(`})`)
		g.P(`return offset, err`)
		return
	}
	f.Element.bodyFastRead(g, setter, appendSetter)
}

func (f *bodyList) bodyFastWrite(g *protogen.GeneratedFile, setter string, number string) {
	if f.IsPacked {
		g.P(fmt.Sprintf("offset += bprotoc.Binary.WriteListPacked(buf[offset:], %s, len(%s),", number, setter))
		g.P(`func(buf []byte, numTagOrKey, numIdxOrVal int32) int {`)
		g.P(`offset := 0`)
		f.Element.bodyFastWrite(g, setter+"[numIdxOrVal]", "numTagOrKey")
		g.P(`return offset`)
		g.P(`})`)
	}
	g.P(fmt.Sprintf("for i := range %s {", setter))
	f.Element.bodyFastWrite(g, setter+"[i]", number)
	g.P(`}`)
}

func (f *bodyList) bodyFastSize(g *protogen.GeneratedFile, setter string, number string) {
	if f.IsPacked {
		g.P(fmt.Sprintf("n += bprotoc.Binary.SizeListPacked(%s, len(%s),", number, setter))
		g.P(`func(numTagOrKey, numIdxOrVal int32) int {`)
		g.P(`n := 0`)
		f.Element.bodyFastSize(g, setter+"[numIdxOrVal]", "numTagOrKey")
		g.P(`return n`)
		g.P(`})`)
	}
	g.P(fmt.Sprintf("for i := range %s {", setter))
	f.Element.bodyFastSize(g, setter+"[i]", number)
	g.P(`}`)
}

// map cannot append, no []map list
type bodyMap struct {
	TypeName   string // map[xxx]xxx
	Key, Value fastAPIBodyGenerator
}

func (f *bodyMap) typeName() string {
	return f.TypeName
}

func (f *bodyMap) bodyFastRead(g *protogen.GeneratedFile, setter string, appendSetter bool) {
	// check nil
	g.P(fmt.Sprintf(`if %s == nil { %s = make(%s) }`, setter, setter, f.typeName()))
	// set default
	g.P(fmt.Sprintf("var key %s", f.Key.typeName()))
	g.P(fmt.Sprintf("var value %s", f.Value.typeName()))
	// unmarshal
	g.P("offset, err = bprotoc.Binary.ReadMapEntry(buf[offset:], _type,")
	g.P(`func(buf []byte, _type int8) (int, error) {`)

	f.Key.bodyFastRead(g, "key", false)
	g.P(`},`)
	g.P(`func(buf []byte, _type int8) (int, error) {`)
	f.Value.bodyFastRead(g, "value", false)
	g.P(`})`)

	g.P(`if err != nil { return offset, err }`)
	g.P(setter, "[key] = value")
	g.P("return offset, nil")
}

func (f *bodyMap) bodyFastWrite(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("for k, v := range %s {", setter))
	g.P(fmt.Sprintf("offset += bprotoc.Binary.WriteMapEntry(buf[offset:], %s,", number))
	g.P(`func(buf []byte, numTagOrKey, numIdxOrVal int32) int {`)
	g.P(`offset := 0`)
	f.Key.bodyFastWrite(g, "k", "numTagOrKey")
	f.Value.bodyFastWrite(g, "v", "numIdxOrVal")
	g.P(`return offset`)
	g.P(`})`)
	g.P(`}`)
}

func (f *bodyMap) bodyFastSize(g *protogen.GeneratedFile, setter string, number string) {
	g.P(fmt.Sprintf("for k, v := range %s {", setter))
	g.P(fmt.Sprintf("n += bprotoc.Binary.SizeMapEntry(%s,", number))
	g.P(`func(numTagOrKey, numIdxOrVal int32) int {`)
	g.P(`n := 0`)
	f.Key.bodyFastSize(g, "k", "numTagOrKey")
	f.Value.bodyFastSize(g, "v", "numIdxOrVal")
	g.P(`return n`)
	g.P(`})`)
	g.P(`}`)
}

var kindAPIType = []string{
	protoreflect.BoolKind:     "Bool",
	protoreflect.Int32Kind:    "Int32",
	protoreflect.Sint32Kind:   "Sint32",
	protoreflect.Uint32Kind:   "Uint32",
	protoreflect.Int64Kind:    "Int64",
	protoreflect.Sint64Kind:   "Sint64",
	protoreflect.Uint64Kind:   "Uint64",
	protoreflect.Sfixed32Kind: "Sfixed32",
	protoreflect.Fixed32Kind:  "Fixed32",
	protoreflect.FloatKind:    "Float",
	protoreflect.Sfixed64Kind: "Sfixed64",
	protoreflect.Fixed64Kind:  "Fixed64",
	protoreflect.DoubleKind:   "Double",
	protoreflect.StringKind:   "String",
	protoreflect.BytesKind:    "Bytes",
}

var kindGoType = []string{
	protoreflect.BoolKind:     "bool",
	protoreflect.Int32Kind:    "int32",
	protoreflect.Sint32Kind:   "int32",
	protoreflect.Uint32Kind:   "uint32",
	protoreflect.Int64Kind:    "int64",
	protoreflect.Sint64Kind:   "int64",
	protoreflect.Uint64Kind:   "uint64",
	protoreflect.Sfixed32Kind: "int32",
	protoreflect.Fixed32Kind:  "uint32",
	protoreflect.FloatKind:    "float32",
	protoreflect.Sfixed64Kind: "int64",
	protoreflect.Fixed64Kind:  "uint64",
	protoreflect.DoubleKind:   "float64",
	protoreflect.StringKind:   "string",
	protoreflect.BytesKind:    "[]byte",
}
