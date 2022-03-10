package protoc

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/compiler/protogen"

	"github.com/cloudwego/kitex"
)

func (pp *protocPlugin) GenerateFastFile(gen *protogen.Plugin, f *protogen.File) error {
	filename := pp.adjustPath(f.GeneratedFilenamePrefix + ".pb.fast.go")
	g := gen.NewGeneratedFile(filename, f.GoImportPath)
	// package
	g.P(fmt.Sprintf("// Code generated by Kitex %s. DO NOT EDIT.", kitex.Version))
	g.P()
	g.P("package ", f.GoPackageName)
	// imports
	g.QualifiedGoIdent(protogen.GoIdent{GoImportPath: "fmt"})
	g.QualifiedGoIdent(protogen.GoIdent{GoImportPath: "github.com/cloudwego/kitex/pkg/protocol/bprotoc"})
	for i, imps := 0, f.Desc.Imports(); i < imps.Len(); i++ {
		imp := imps.Get(i)
		impFile, ok := gen.FilesByPath[imp.Path()]
		if !ok || impFile.GoImportPath == f.GoImportPath || imp.IsWeak {
			continue
		}
		g.QualifiedGoIdent(protogen.GoIdent{GoImportPath: impFile.GoImportPath})
	}

	// body
	gf := newFastGen(gen, f)
	var ps []FastAPIGenerator
	for _, msg := range f.Messages {
		ps = append(ps, gf.NewMessage(msg))
		sort.Sort(sortFields(msg.Fields))
		for _, field := range msg.Fields {
			ps = append(ps, gf.NewField(field))
		}
	}
	// gen body
	for i := range ps {
		ps[i].GenFastRead(g)
	}
	for i := range ps {
		ps[i].GenFastWrite(g)
	}
	for i := range ps {
		ps[i].GenFastSize(g)
	}
	for i := range ps {
		ps[i].GenFastConst(g)
	}
	return nil

}

type sortFields []*protogen.Field

func (s sortFields) Len() int {
	return len(s)
}

func (s sortFields) Less(i, j int) bool {
	return s[i].Desc.Number() < s[j].Desc.Number()
}

func (s sortFields) Swap(i, j int) {
	tmp := s[i]
	s[i] = s[j]
	s[j] = tmp
}
