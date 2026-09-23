package server

import (
	"context"
	"errors"
	"go/types"
	"net/http"
	"slices"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
	"github.com/tewecske/interactivecodebase/internal/views"
)

// sitemapDiagram draws routes grouped by access, with navigation edges.
// Query parameters narrow it: get=1 (GET routes only), access (comma list
// of groups: public, optional, guest, authenticated, admin), q (substring).
func (s *Server) sitemapDiagram(r *http.Request) (any, error) {
	ctx := r.Context()
	routes, err := s.p.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
	if err != nil {
		return nil, err
	}
	var nav []graph.Edge
	for _, n := range routes {
		nbs, err := s.p.Graph.Neighbors(ctx, n.ID, graph.Out, graph.EdgeNavigatesTo)
		if err != nil {
			return nil, err
		}
		for _, nb := range nbs {
			nav = append(nav, nb.Edge)
		}
	}
	q := r.URL.Query()
	opts := mermaid.SiteMapOptions{GETOnly: q.Get("get") == "1", Match: q.Get("q")}
	if a := q.Get("access"); a != "" {
		opts.Access = strings.Split(a, ",")
	}
	return mermaid.SiteMap(routes, nav, opts), nil
}

func (s *Server) flowDiagram(r *http.Request) (any, error) {
	route, err := s.routeNode(r)
	if err != nil {
		return nil, err
	}
	opts, err := flowOptions(r)
	if err != nil {
		return nil, err
	}
	tree, err := flow.Build(r.Context(), s.p.Graph, route.ID, opts)
	if err != nil {
		return nil, err
	}
	return mermaid.Sequence(tree), nil
}

// erDiagram draws a table and the tables within depth foreign keys of it
// (default 1), or the whole schema when no table is given.
func (s *Server) erDiagram(r *http.Request) (any, error) {
	depth, err := intParam(r, "depth", 1)
	if err != nil {
		return nil, err
	}
	name := r.URL.Query().Get("table")
	d, err := views.ER(r.Context(), s.p.Graph, name, depth)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, notFound("no table " + name)
	}
	return d, err
}

// typesDiagram draws the types around a node: for a function, its
// receiver, parameter and result types; for a type, itself; plus the
// types their fields refer to, the module interfaces they implement and
// implementations of interfaces. Go projects get fields and methods from
// go/types; other projects get the types and their relations from the
// graph alone.
func (s *Server) typesDiagram(r *http.Request) (any, error) {
	id, err := required(r, "id")
	if err != nil {
		return nil, err
	}
	var infos []mermaid.TypeInfo
	if s.p.Go != nil {
		infos, err = s.typeInfos(r.Context(), id)
	} else {
		infos, err = s.graphTypeInfos(r.Context(), id)
	}
	if err != nil {
		return nil, err
	}
	return mermaid.Classes(infos), nil
}

// maxDiagramTypes bounds a class diagram.
const maxDiagramTypes = 12

// graphTypeInfos finds the types around id through uses_type edges (from a
// function to its signature's types, from a type to its fields' types)
// and implements edges. The graph has no members, so none are drawn.
func (s *Server) graphTypeInfos(ctx context.Context, id string) ([]mermaid.TypeInfo, error) {
	g := s.p.Graph
	n, err := g.Node(ctx, id)
	if errors.Is(err, graph.ErrNotFound) {
		return nil, notFound("no node " + id)
	}
	if err != nil {
		return nil, err
	}
	var all []graph.Node
	add := func(n graph.Node) {
		if n.Kind == graph.KindType && len(all) < maxDiagramTypes &&
			!slices.ContainsFunc(all, func(o graph.Node) bool { return o.ID == n.ID }) {
			all = append(all, n)
		}
	}
	follow := func(id string, dir graph.Direction, kind graph.EdgeKind) error {
		nbs, err := g.Neighbors(ctx, id, dir, kind)
		for _, nb := range nbs {
			add(nb.Node)
		}
		return err
	}
	if n.Kind == graph.KindType {
		add(n)
	} else if err := follow(id, graph.Out, graph.EdgeUsesType); err != nil {
		return nil, err
	}
	// One level of field types, then implementations and interfaces, as
	// for Go.
	for _, t := range slices.Clone(all) {
		if err := follow(t.ID, graph.Out, graph.EdgeUsesType); err != nil {
			return nil, err
		}
	}
	for _, t := range slices.Clone(all) {
		for _, dir := range []graph.Direction{graph.Out, graph.In} {
			if err := follow(t.ID, dir, graph.EdgeImplements); err != nil {
				return nil, err
			}
		}
	}
	names := map[string]string{} // ID → diagram name
	for _, t := range all {
		names[t.ID] = graphTypeName(t)
	}
	var out []mermaid.TypeInfo
	for _, t := range all {
		info := mermaid.TypeInfo{ID: t.ID, Name: names[t.ID], Kind: "type"}
		switch t.Detail {
		case "interface", "trait":
			info.Kind = "interface"
		case "struct", "class":
			info.Kind = "struct"
		}
		for _, rel := range []struct {
			kind graph.EdgeKind
			into *[]string
		}{{graph.EdgeUsesType, &info.Uses}, {graph.EdgeImplements, &info.Implements}} {
			nbs, err := g.Neighbors(ctx, t.ID, graph.Out, rel.kind)
			if err != nil {
				return nil, err
			}
			for _, nb := range nbs {
				if name, ok := names[nb.Node.ID]; ok && !slices.Contains(*rel.into, name) {
					*rel.into = append(*rel.into, name)
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// graphTypeName qualifies a type by the last element of its package, as
// source code does: notes.Repository for example.com/app/notes, db.Repo
// for the Scala package com.example.db.
func graphTypeName(t graph.Node) string {
	pkg := t.Package
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:]
	} else if i := strings.LastIndex(pkg, "."); i >= 0 {
		pkg = pkg[i+1:]
	}
	if pkg == "" {
		return t.Name
	}
	return pkg + "." + t.Name
}

// typeInfos is the types diagram from Go type information.
func (s *Server) typeInfos(ctx context.Context, id string) ([]mermaid.TypeInfo, error) {
	var roots []*types.TypeName
	switch {
	case strings.HasPrefix(id, string(graph.KindType)+":"):
		tn := s.p.Go.TypeName(id)
		if tn == nil {
			return nil, notFound("no type " + id)
		}
		roots = append(roots, tn)
	default:
		fn := s.p.Go.Func(id)
		if fn == nil {
			return nil, notFound("no function " + id)
		}
		sig := fn.Signature
		if recv := sig.Recv(); recv != nil {
			roots = s.collect(roots, recv.Type())
		}
		for i := range sig.Params().Len() {
			roots = s.collect(roots, sig.Params().At(i).Type())
		}
		for i := range sig.Results().Len() {
			roots = s.collect(roots, sig.Results().At(i).Type())
		}
	}
	// One level of field types: a service's repositories and mailers.
	all := slices.Clone(roots)
	for _, tn := range roots {
		if st, ok := tn.Type().Underlying().(*types.Struct); ok {
			for i := range st.NumFields() {
				all = s.collect(all, st.Field(i).Type())
			}
		}
	}
	// Then the implementations of interfaces, and interfaces implemented.
	for _, tn := range slices.Clone(all) {
		for _, dir := range []graph.Direction{graph.Out, graph.In} {
			nbs, err := s.p.Graph.Neighbors(ctx, analysis.TypeID(tn), dir, graph.EdgeImplements)
			if err != nil {
				return nil, err
			}
			for _, nb := range nbs {
				if other := s.p.Go.TypeName(nb.Node.ID); other != nil && !slices.Contains(all, other) && len(all) < maxDiagramTypes {
					all = append(all, other)
				}
			}
		}
	}
	return s.describe(ctx, all)
}

// collect adds the module named types a type refers to (through pointers,
// slices, maps).
func (s *Server) collect(into []*types.TypeName, t types.Type) []*types.TypeName {
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() != nil && s.p.Go.InModule(obj.Pkg().Path()) && !slices.Contains(into, obj) && len(into) < maxDiagramTypes {
			into = append(into, obj)
		}
	case *types.Pointer:
		return s.collect(into, t.Elem())
	case *types.Slice:
		return s.collect(into, t.Elem())
	case *types.Array:
		return s.collect(into, t.Elem())
	case *types.Map:
		return s.collect(s.collect(into, t.Key()), t.Elem())
	}
	return into
}

func (s *Server) describe(ctx context.Context, tns []*types.TypeName) ([]mermaid.TypeInfo, error) {
	name := func(tn *types.TypeName) string { return tn.Pkg().Name() + "." + tn.Name() }
	inDiagram := map[string]bool{}
	for _, tn := range tns {
		inDiagram[name(tn)] = true
	}
	var out []mermaid.TypeInfo
	for _, tn := range tns {
		// Qualify other packages by name, as source code does: *sql.DB.
		qual := func(p *types.Package) string {
			if p == tn.Pkg() {
				return ""
			}
			return p.Name()
		}
		info := mermaid.TypeInfo{ID: analysis.TypeID(tn), Name: name(tn), Kind: "type"}
		switch u := tn.Type().Underlying().(type) {
		case *types.Struct:
			info.Kind = "struct"
			for i := range u.NumFields() {
				f := u.Field(i)
				info.Fields = append(info.Fields, mermaid.Member{Name: f.Name(), Type: types.TypeString(f.Type(), qual), Exported: f.Exported()})
				for _, used := range s.collect(nil, f.Type()) {
					if inDiagram[name(used)] {
						info.Uses = append(info.Uses, name(used))
					}
				}
			}
		case *types.Interface:
			info.Kind = "interface"
			for i := range u.NumMethods() {
				m := u.Method(i)
				info.Methods = append(info.Methods, member(m, qual))
			}
		}
		if info.Kind != "interface" {
			ms := types.NewMethodSet(types.NewPointer(tn.Type()))
			for i := range ms.Len() {
				if m, ok := ms.At(i).Obj().(*types.Func); ok {
					info.Methods = append(info.Methods, member(m, qual))
				}
			}
		}
		nbs, err := s.p.Graph.Neighbors(ctx, info.ID, graph.Out, graph.EdgeImplements)
		if err != nil {
			return nil, err
		}
		for _, nb := range nbs {
			if tn := s.p.Go.TypeName(nb.Node.ID); tn != nil && inDiagram[name(tn)] {
				info.Implements = append(info.Implements, name(tn))
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func member(m *types.Func, qual types.Qualifier) mermaid.Member {
	sig := strings.TrimPrefix(types.TypeString(m.Type(), qual), "func")
	return mermaid.Member{Name: m.Name(), Type: sig, Exported: m.Exported()}
}
