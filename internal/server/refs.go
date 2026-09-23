package server

import (
	"go/ast"
	"go/token"
	"go/types"
	"net/http"
	"path/filepath"
	"sort"

	"golang.org/x/tools/go/packages"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// Ref is an identifier in a file that refers to a definition.
type Ref struct {
	Line   int    `json:"line"`
	Col    int    `json:"col"` // 1-based, in bytes
	EndCol int    `json:"endCol"`
	Name   string `json:"name"`
	// Target is the definition, when it is in the module.
	Target *graph.Pos `json:"target,omitempty"`
	// Node is the graph node of the definition (function, method, type).
	Node string `json:"node,omitempty"`
	// Kind is the object kind: func, method, type, var, const, field, ...
	Kind string `json:"kind"`
}

// refs returns the identifiers of a module Go file that refer to
// definitions in the module, for "go to definition" in the code view.
func (s *Server) refs(r *http.Request) (any, error) {
	file, err := required(r, "file")
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(s.r.Dir, filepath.Clean(filepath.FromSlash(file)))
	var out []Ref
	found := false
	packages.Visit(s.r.Packages, nil, func(p *packages.Package) {
		if found || p.TypesInfo == nil || !s.r.InModule(p.PkgPath) {
			return
		}
		for i, f := range p.CompiledGoFiles {
			if f != abs || i >= len(p.Syntax) {
				continue
			}
			found = true
			out = s.fileRefs(p, p.Syntax[i])
			return
		}
	})
	if !found {
		return nil, notFound("no analyzed Go file " + file)
	}
	return nonNil(out), nil
}

func (s *Server) fileRefs(p *packages.Package, f *ast.File) []Ref {
	fset := p.Fset
	var out []Ref
	add := func(id *ast.Ident, obj types.Object) {
		if obj == nil || obj.Pkg() == nil || !obj.Pos().IsValid() {
			return
		}
		start := fset.Position(id.Pos())
		ref := Ref{Line: start.Line, Col: start.Column, EndCol: start.Column + len(id.Name), Name: id.Name, Kind: objKind(obj)}
		if s.r.InModule(obj.Pkg().Path()) {
			def := fset.Position(obj.Pos())
			if rel, err := filepath.Rel(s.r.Dir, def.Filename); err == nil {
				ref.Target = &graph.Pos{File: filepath.ToSlash(rel), StartLine: def.Line, StartCol: def.Column}
			}
		}
		ref.Node = nodeFor(obj)
		if ref.Target == nil && ref.Node == "" {
			return
		}
		out = append(out, ref)
	}
	for id, obj := range p.TypesInfo.Uses {
		if tokenFile(fset, id.Pos()) == tokenFile(fset, f.Pos()) {
			add(id, obj)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out
}

func tokenFile(fset *token.FileSet, pos token.Pos) string { return fset.Position(pos).Filename }

// nodeFor returns the graph node ID of a function, method or named type.
func nodeFor(obj types.Object) string {
	switch obj := obj.(type) {
	case *types.Func:
		sig := obj.Type().(*types.Signature)
		if recv := sig.Recv(); recv != nil {
			if _, isIface := recv.Type().Underlying().(*types.Interface); isIface {
				return graph.NodeID(graph.KindInterfaceCall, obj.FullName())
			}
			return graph.NodeID(graph.KindMethod, obj.FullName())
		}
		return graph.NodeID(graph.KindFunc, obj.FullName())
	case *types.TypeName:
		if !obj.IsAlias() && obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
			return analysis.TypeID(obj)
		}
	}
	return ""
}

func objKind(obj types.Object) string {
	switch obj := obj.(type) {
	case *types.Func:
		if obj.Type().(*types.Signature).Recv() != nil {
			return "method"
		}
		return "func"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.Var:
		if obj.IsField() {
			return "field"
		}
		return "var"
	case *types.PkgName:
		return "package"
	}
	return "other"
}
