package server

import (
	"context"
	"go/types"
	"net/http"
	"slices"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
)

// sitemapDiagram draws routes grouped by access, with navigation edges.
// Query parameters narrow it: get=1 (GET routes only), access (comma list
// of groups: public, optional, guest, authenticated, admin), q (substring).
func (s *Server) sitemapDiagram(r *http.Request) (any, error) {
	ctx := r.Context()
	routes, err := s.r.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
	if err != nil {
		return nil, err
	}
	var nav []graph.Edge
	for _, n := range routes {
		nbs, err := s.r.Graph.Neighbors(ctx, n.ID, graph.Out, graph.EdgeNavigatesTo)
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
	tree, err := flow.Build(r.Context(), s.r.Graph, route.ID, opts)
	if err != nil {
		return nil, err
	}
	return mermaid.Sequence(tree), nil
}

// erDiagram draws a table and the tables within depth foreign keys of it
// (default 1), or the whole schema when no table is given.
func (s *Server) erDiagram(r *http.Request) (any, error) {
	ctx := r.Context()
	depth, err := intParam(r, "depth", 1)
	if err != nil {
		return nil, err
	}
	all, err := s.r.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindSQLTable}})
	if err != nil {
		return nil, err
	}
	include := map[string]bool{}
	if name := r.URL.Query().Get("table"); name != "" {
		start := analysis.TableID(name)
		if _, err := s.r.Graph.Node(ctx, start); err != nil {
			return nil, notFound("no table " + name)
		}
		include[start] = true
		frontier := []string{start}
		for range depth {
			var next []string
			for _, id := range frontier {
				for _, dir := range []graph.Direction{graph.Out, graph.In} {
					nbs, err := s.r.Graph.Neighbors(ctx, id, dir, graph.EdgeFK)
					if err != nil {
						return nil, err
					}
					for _, nb := range nbs {
						if !include[nb.Node.ID] {
							include[nb.Node.ID] = true
							next = append(next, nb.Node.ID)
						}
					}
				}
			}
			frontier = next
		}
	} else {
		for _, t := range all {
			include[t.ID] = true
		}
	}
	var tables []mermaid.Table
	var fks []graph.Edge
	for _, t := range all {
		if !include[t.ID] {
			continue
		}
		cols, err := s.r.Graph.Neighbors(ctx, t.ID, graph.Out, graph.EdgeHasColumn)
		if err != nil {
			return nil, err
		}
		mt := mermaid.Table{Node: t}
		for _, c := range cols {
			mt.Columns = append(mt.Columns, c.Node)
		}
		tables = append(tables, mt)
		out, err := s.r.Graph.Neighbors(ctx, t.ID, graph.Out, graph.EdgeFK)
		if err != nil {
			return nil, err
		}
		for _, nb := range out {
			if include[nb.Node.ID] {
				fks = append(fks, nb.Edge)
			}
		}
	}
	return mermaid.ER(tables, fks), nil
}

// typesDiagram draws the types around a node: for a function, its
// receiver, parameter and result types; for a type, itself; plus the
// module interfaces they implement and implementations of interfaces.
func (s *Server) typesDiagram(r *http.Request) (any, error) {
	id, err := required(r, "id")
	if err != nil {
		return nil, err
	}
	infos, err := s.typeInfos(r.Context(), id)
	if err != nil {
		return nil, err
	}
	return mermaid.Classes(infos), nil
}

// maxDiagramTypes bounds a class diagram.
const maxDiagramTypes = 12

func (s *Server) typeInfos(ctx context.Context, id string) ([]mermaid.TypeInfo, error) {
	var roots []*types.TypeName
	switch {
	case strings.HasPrefix(id, string(graph.KindType)+":"):
		tn := s.r.TypeName(id)
		if tn == nil {
			return nil, notFound("no type " + id)
		}
		roots = append(roots, tn)
	default:
		fn := s.r.Func(id)
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
			nbs, err := s.r.Graph.Neighbors(ctx, analysis.TypeID(tn), dir, graph.EdgeImplements)
			if err != nil {
				return nil, err
			}
			for _, nb := range nbs {
				if other := s.r.TypeName(nb.Node.ID); other != nil && !slices.Contains(all, other) && len(all) < maxDiagramTypes {
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
		if obj.Pkg() != nil && s.r.InModule(obj.Pkg().Path()) && !slices.Contains(into, obj) && len(into) < maxDiagramTypes {
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
		nbs, err := s.r.Graph.Neighbors(ctx, info.ID, graph.Out, graph.EdgeImplements)
		if err != nil {
			return nil, err
		}
		for _, nb := range nbs {
			if tn := s.r.TypeName(nb.Node.ID); tn != nil && inDiagram[name(tn)] {
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
