package server

import (
	"cmp"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// Summary describes the analyzed module.
type Summary struct {
	Module        string       `json:"module"`
	Dir           string       `json:"dir"`
	Packages      int          `json:"packages"`
	Functions     int          `json:"moduleFunctions"`
	Counts        graph.Counts `json:"counts"`
	MigrationDirs []string     `json:"migrationDirs"`
	TotalMS       int64        `json:"analysisMs"`
}

func (s *Server) summary(r *http.Request) (any, error) {
	counts, err := s.r.Graph.Counts(r.Context())
	if err != nil {
		return nil, err
	}
	return Summary{
		Module: s.r.Module, Dir: s.r.Dir, Packages: s.r.Stats.Packages, Functions: s.r.Stats.ModuleFunctions,
		Counts: counts, MigrationDirs: nonNil(s.r.MigrationDirs), TotalMS: s.r.Stats.Total().Milliseconds(),
	}, nil
}

// RouteInfo is one route in the route list.
type RouteInfo struct {
	ID           string    `json:"id"`
	Method       string    `json:"method"`
	Pattern      string    `json:"pattern"`
	Variants     []string  `json:"variants,omitempty"`
	Access       string    `json:"access"`
	OptionalAuth bool      `json:"optionalAuth,omitempty"`
	Handler      string    `json:"handler"`
	Middleware   []string  `json:"middleware,omitempty"`
	Page         bool      `json:"page,omitempty"`
	Static       bool      `json:"static,omitempty"`
	Conditional  bool      `json:"conditional,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
	Pos          graph.Pos `json:"pos"`
}

// listRoutes returns routes in registration order, filtered by the
// optional query parameters access, method and q (substring of pattern).
func (s *Server) listRoutes(r *http.Request) (any, error) {
	nodes, err := s.r.Graph.Nodes(r.Context(), graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	var out []RouteInfo
	for _, n := range sortByPos(nodes) {
		ri := routeInfo(n)
		if (q.Get("access") != "" && ri.Access != q.Get("access")) ||
			(q.Get("method") != "" && ri.Method != q.Get("method")) ||
			(q.Get("q") != "" && !strings.Contains(strings.ToLower(ri.Pattern), strings.ToLower(q.Get("q")))) {
			continue
		}
		if nbs, err := s.r.Graph.Neighbors(r.Context(), n.ID, graph.Out, graph.EdgeRenders); err == nil && len(nbs) > 0 {
			ri.Page = true
		}
		out = append(out, ri)
	}
	return nonNil(out), nil
}

func routeInfo(n graph.Node) RouteInfo {
	a := n.Attrs
	ri := RouteInfo{
		ID: n.ID, Method: a["method"], Pattern: a["pattern"], Access: a["access"], Handler: a["handler"],
		OptionalAuth: a["optionalAuth"] == "true", Static: a["static"] == "true", Conditional: a["conditional"] == "true",
		Evidence: a["accessEvidence"], Pos: n.Pos,
	}
	if v := a["variants"]; v != "" {
		ri.Variants = strings.Fields(v)
	}
	if m := a["middleware"]; m != "" {
		ri.Middleware = strings.Split(m, ",")
	}
	return ri
}

func sortByPos(nodes []graph.Node) []graph.Node {
	slices.SortStableFunc(nodes, func(a, b graph.Node) int {
		return cmp.Or(cmp.Compare(a.Pos.File, b.Pos.File), cmp.Compare(a.Pos.StartLine, b.Pos.StartLine), cmp.Compare(a.ID, b.ID))
	})
	return nodes
}

// NodeDetail is a node with its edges grouped by kind.
type NodeDetail struct {
	Node graph.Node                  `json:"node"`
	Out  map[string][]graph.Neighbor `json:"out"`
	In   map[string][]graph.Neighbor `json:"in"`
}

func (s *Server) node(r *http.Request) (any, error) {
	id, err := required(r, "id")
	if err != nil {
		return nil, err
	}
	n, err := s.r.Graph.Node(r.Context(), id)
	if err != nil {
		return nil, err
	}
	d := NodeDetail{Node: n, Out: map[string][]graph.Neighbor{}, In: map[string][]graph.Neighbor{}}
	for dir, into := range map[graph.Direction]map[string][]graph.Neighbor{graph.Out: d.Out, graph.In: d.In} {
		nbs, err := s.r.Graph.Neighbors(r.Context(), id, dir)
		if err != nil {
			return nil, err
		}
		for _, nb := range nbs {
			into[string(nb.Edge.Kind)] = append(into[string(nb.Edge.Kind)], nb)
		}
	}
	return d, nil
}

// PageDetail is what a route renders and what its page references.
type PageDetail struct {
	Route    RouteInfo        `json:"route"`
	Renders  []graph.Neighbor `json:"renders"`
	Requests []graph.Neighbor `json:"requests"`
	Assets   []graph.Neighbor `json:"assets"`
	Links    []graph.Neighbor `json:"navigatesTo"`
}

func (s *Server) page(r *http.Request) (any, error) {
	route, err := s.routeNode(r)
	if err != nil {
		return nil, err
	}
	p := PageDetail{Route: routeInfo(route)}
	for kind, into := range map[graph.EdgeKind]*[]graph.Neighbor{
		graph.EdgeRenders: &p.Renders, graph.EdgeRequests: &p.Requests, graph.EdgeLoads: &p.Assets, graph.EdgeNavigatesTo: &p.Links,
	} {
		nbs, err := s.r.Graph.Neighbors(r.Context(), route.ID, graph.Out, kind)
		if err != nil {
			return nil, err
		}
		*into = nonNil(nbs)
	}
	return p, nil
}

// routeNode reads the "route" parameter: a route node ID or "METHOD pattern".
func (s *Server) routeNode(r *http.Request) (graph.Node, error) {
	key, err := required(r, "route")
	if err != nil {
		return graph.Node{}, err
	}
	id := key
	if !strings.HasPrefix(key, string(graph.KindRoute)+":") {
		id = graph.NodeID(graph.KindRoute, key)
	}
	n, err := s.r.Graph.Node(r.Context(), id)
	if err != nil {
		return graph.Node{}, notFound("no route " + key)
	}
	return n, nil
}

func flowOptions(r *http.Request) (flow.Options, error) {
	opts := flow.Options{Method: r.URL.Query().Get("method")}
	switch r.URL.Query().Get("prune") {
	case "", "sinks":
		opts.Prune = flow.PruneSinks
	case "module":
		opts.Prune = flow.PruneModule
	case "none":
		opts.Prune = flow.PruneNone
	default:
		return opts, badRequest("prune must be sinks, module or none")
	}
	depth, err := intParam(r, "depth", 0)
	opts.MaxDepth = depth
	return opts, err
}

func (s *Server) flow(r *http.Request) (any, error) {
	route, err := s.routeNode(r)
	if err != nil {
		return nil, err
	}
	opts, err := flowOptions(r)
	if err != nil {
		return nil, err
	}
	return flow.Build(r.Context(), s.r.Graph, route.ID, opts)
}

func (s *Server) paths(r *http.Request) (any, error) {
	from, err := required(r, "from")
	if err != nil {
		return nil, err
	}
	to, err := required(r, "to")
	if err != nil {
		return nil, err
	}
	depth, err := intParam(r, "depth", 0)
	if err != nil {
		return nil, err
	}
	paths, err := s.r.Graph.Paths(r.Context(), from, to, graph.PathOptions{MaxDepth: depth})
	return nonNil(paths), err
}

// Source is a slice of a source file.
type Source struct {
	File  string   `json:"file"`
	Start int      `json:"start"`
	End   int      `json:"end"`
	Total int      `json:"total"`
	Lines []string `json:"lines"`
}

// maxSourceLines bounds one source response.
const maxSourceLines = 2000

// source returns lines of a file inside the module. file is relative to
// the module root; paths escaping it are rejected.
func (s *Server) source(r *http.Request) (any, error) {
	file, err := required(r, "file")
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, badRequest("file must be relative to the module")
	}
	// os.Root also stops symlinks inside the module from escaping it.
	root, err := os.OpenRoot(s.r.Dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // read-only
	b, err := root.ReadFile(clean)
	if err != nil {
		return nil, notFound("no file " + file)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	start, err := intParam(r, "start", 1)
	if err != nil {
		return nil, err
	}
	end, err := intParam(r, "end", len(lines))
	if err != nil {
		return nil, err
	}
	start = max(start, 1)
	end = min(end, len(lines), start+maxSourceLines-1)
	if start > end {
		return nil, badRequest("empty line range")
	}
	return Source{File: filepath.ToSlash(clean), Start: start, End: end, Total: len(lines), Lines: lines[start-1 : end]}, nil
}

func (s *Server) tables(r *http.Request) (any, error) {
	nodes, err := s.r.Graph.Nodes(r.Context(), graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindSQLTable}})
	return nonNil(nodes), err
}

// TableRoute is a route whose flow touches a table.
type TableRoute struct {
	Route string `json:"route"`
	Op    string `json:"op"`
}

// TableDetail is a table with its columns, relations and users.
type TableDetail struct {
	Table   graph.Node       `json:"table"`
	Columns []graph.Node     `json:"columns"`
	FKsOut  []graph.Neighbor `json:"references"`
	FKsIn   []graph.Neighbor `json:"referencedBy"`
	Queries []graph.Neighbor `json:"queries"`
	Routes  []TableRoute     `json:"routes"`
}

func (s *Server) table(r *http.Request) (any, error) {
	name, err := required(r, "name")
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	id := analysis.TableID(name)
	t, err := s.r.Graph.Node(ctx, id)
	if err != nil {
		return nil, notFound("no table " + name)
	}
	d := TableDetail{Table: t}
	cols, err := s.r.Graph.Neighbors(ctx, id, graph.Out, graph.EdgeHasColumn)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		d.Columns = append(d.Columns, c.Node)
	}
	if d.FKsOut, err = s.r.Graph.Neighbors(ctx, id, graph.Out, graph.EdgeFK); err != nil {
		return nil, err
	}
	if d.FKsIn, err = s.r.Graph.Neighbors(ctx, id, graph.In, graph.EdgeFK); err != nil {
		return nil, err
	}
	if d.Queries, err = s.r.Graph.Neighbors(ctx, id, graph.In, graph.EdgeQueries); err != nil {
		return nil, err
	}
	routes, err := s.tableRoutes(r)
	if err != nil {
		return nil, err
	}
	d.Columns, d.FKsOut, d.FKsIn, d.Queries = nonNil(d.Columns), nonNil(d.FKsOut), nonNil(d.FKsIn), nonNil(d.Queries)
	d.Routes = nonNil(routes[name])
	return d, nil
}

// tableRoutes maps each table to the routes whose flows reach it,
// computed once from every route's flow.
func (s *Server) tableRoutes(r *http.Request) (map[string][]TableRoute, error) {
	// Computed once for all requests, so not tied to this request's context.
	ctx := context.WithoutCancel(r.Context())
	s.tablesOnce.Do(func() {
		s.routesByTbl = map[string][]TableRoute{}
		nodes, err := s.r.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
		if err != nil {
			s.tablesErr = err
			return
		}
		for _, n := range sortByPos(nodes) {
			tree, err := flow.Build(ctx, s.r.Graph, n.ID, flow.Options{})
			if err != nil {
				s.tablesErr = err
				return
			}
			seen := map[[2]string]bool{} // table, op
			flow.Walk(tree, func(st *flow.Step, _ int) {
				if st.Node.Kind != graph.KindSQLTable {
					return
				}
				op := st.Edge.Attrs["op"]
				if key := [2]string{st.Node.Name, op}; !seen[key] {
					seen[key] = true
					s.routesByTbl[st.Node.Name] = append(s.routesByTbl[st.Node.Name], TableRoute{Route: n.Name, Op: op})
				}
			})
		}
	})
	return s.routesByTbl, s.tablesErr
}

// search finds nodes matching q, optionally only of the given kinds
// (comma list). Without q it lists the nodes of those kinds.
func (s *Server) search(r *http.Request) (any, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, err := intParam(r, "limit", 50)
	if err != nil {
		return nil, err
	}
	opts := graph.SearchOptions{Limit: min(limit, 500)}
	for _, k := range strings.Split(r.URL.Query().Get("kind"), ",") {
		if k != "" {
			opts.Kinds = append(opts.Kinds, graph.NodeKind(k))
		}
	}
	if q == "" {
		if len(opts.Kinds) == 0 {
			return nil, badRequest("give q, kind, or both")
		}
		nodes, err := s.r.Graph.Nodes(r.Context(), graph.NodeFilter{Kinds: opts.Kinds})
		if len(nodes) > opts.Limit {
			nodes = nodes[:opts.Limit]
		}
		return nonNil(nodes), err
	}
	nodes, err := s.r.Graph.Search(r.Context(), q, opts)
	return nonNil(nodes), err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
