// Package mcpserver exposes the code graph to AI agents over the Model
// Context Protocol: tools to list routes, follow flows down to SQL, find
// callers and paths, inspect tables and read source, plus resources for
// the route list and the schema.
//
// Tool results are compact text that names graph IDs, so an agent can
// pass them to the next call; diagrams are available as Mermaid.
package mcpserver

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
	"github.com/tewecske/interactivecodebase/internal/views"
)

// Version is reported to clients.
var Version = "dev"

// Limits keep tool results small enough for an agent's context.
const (
	maxListLines   = 200
	maxEdgesByKind = 40
	maxSourceLines = 300
	maxPaths       = 5
)

type server struct {
	cur *live.Current

	mu        sync.Mutex
	tablesFor *analysis.Result
	tables    map[string][]views.TableRoute
}

// New returns an MCP server over the current analysis in cur.
func New(cur *live.Current) *mcp.Server {
	s := &server{cur: cur}
	srv := mcp.NewServer(&mcp.Implementation{Name: "icb", Title: "interactivecodebase", Version: Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	tool := func(name, desc string) *mcp.Tool { return &mcp.Tool{Name: name, Description: desc} }
	mcp.AddTool(srv, tool("list_routes", "List the HTTP routes: method, pattern, access level (public, authenticated, admin, guest; * = public but reads the session), handler and registration position. Optional filters."), s.listRoutes)
	mcp.AddTool(srv, tool("get_route", "Describe a route: access and its evidence, handler, middleware, templates, the requests and static assets its page uses, and where you can navigate to and from."), s.getRoute)
	mcp.AddTool(srv, tool("get_flow", "The call flow of a route from its handler down to SQL (with tables), files, outbound HTTP, mail, processes and env reads, following interface dispatch. format=text (tree) or mermaid (sequence diagram)."), s.getFlow)
	mcp.AddTool(srv, tool("get_node", "Describe any graph node by ID or name: kind, package, source position, detail (signature, SQL, URL) and its incoming and outgoing edges."), s.getNode)
	mcp.AddTool(srv, tool("get_source", "Read lines of a source file of the module (path relative to the module root)."), s.getSource)
	mcp.AddTool(srv, tool("find_callers", "Who calls a function or method (or dispatches to it through an interface, or routes to it)."), s.findCallers)
	mcp.AddTool(srv, tool("find_callees", "What a function or method calls, in call-site order, including interface dispatch and sinks."), s.findCallees)
	mcp.AddTool(srv, tool("find_paths", "Shortest call paths between two nodes, e.g. from a route or handler to a table (sql_table:name) or a function."), s.findPaths)
	mcp.AddTool(srv, tool("routes_touching_table", "Which routes read or write a table, with the operation (select, insert, update, delete)."), s.routesTouchingTable)
	mcp.AddTool(srv, tool("list_tables", "List the database tables from the migrations (and tables only seen in queries) with their columns."), s.listTables)
	mcp.AddTool(srv, tool("get_table", "Describe a table: columns, primary key, foreign keys both ways with ON DELETE, the queries and routes that use it. format=mermaid for an ER diagram of it and its neighbours."), s.getTable)
	mcp.AddTool(srv, tool("search", "Full-text search over nodes (routes, functions, types, tables, SQL text, templates); substrings and qualified names match."), s.search)
	mcp.AddTool(srv, tool("reanalyze", "Analyze the module again after code changes and report the new counts."), s.reanalyze)
	srv.AddResource(&mcp.Resource{URI: "icb://routes", Name: "routes", Description: "All HTTP routes with access level and handler", MIMEType: "text/plain"}, s.routesResource)
	srv.AddResource(&mcp.Resource{URI: "icb://schema", Name: "schema", Description: "Database tables with columns, and the schema as a Mermaid ER diagram", MIMEType: "text/plain"}, s.schemaResource)
	return srv
}

const instructions = `icb is a static analysis of one Go web application. Start with list_routes or search.
Node IDs look like "route:GET /{lang}/notes", "method:(*example.com/app/web.Handler).List",
"sql_table:notes"; tools also accept a unique name or the end of an ID. To see what an
endpoint does, call get_flow with the route; to see who changes a table, call
routes_touching_table.`

// text returns a plain-text tool result.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// with runs fn against the current analysis.
func (s *server) with(fn func(r *analysis.Result) (string, error)) (*mcp.CallToolResult, any, error) {
	var out string
	err := s.cur.With(func(r *analysis.Result) error {
		var err error
		out, err = fn(r)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return text(out), nil, nil
}

// ListRoutesIn filters list_routes.
type ListRoutesIn struct {
	Access string `json:"access,omitempty" jsonschema:"only this access level: public, authenticated, admin or guest"`
	Method string `json:"method,omitempty" jsonschema:"only this HTTP method, e.g. POST"`
	Match  string `json:"match,omitempty" jsonschema:"only patterns containing this text"`
}

func (s *server) listRoutes(ctx context.Context, _ *mcp.CallToolRequest, in ListRoutesIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) { return routesText(ctx, r, in) })
}

func routesText(ctx context.Context, r *analysis.Result, in ListRoutesIn) (string, error) {
	nodes, err := r.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
	if err != nil {
		return "", err
	}
	slices.SortStableFunc(nodes, byPos)
	var b strings.Builder
	n := 0
	for _, rt := range nodes {
		a := rt.Attrs
		if (in.Access != "" && a["access"] != in.Access) || (in.Method != "" && !strings.EqualFold(a["method"], in.Method)) ||
			(in.Match != "" && !strings.Contains(strings.ToLower(rt.Name), strings.ToLower(in.Match))) {
			continue
		}
		if n++; n > maxListLines {
			continue
		}
		access := a["access"]
		if a["optionalAuth"] == "true" {
			access += "*"
		}
		handler := short(a["handler"])
		if mw := a["middleware"]; mw != "" {
			handler = short(mw) + " > " + handler
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", rt.Name, access, handler, pos(rt.Pos))
	}
	header := fmt.Sprintf("%d routes (access * = public, reads the session)\n", n)
	if n > maxListLines {
		header += fmt.Sprintf("showing the first %d; narrow with access, method or match\n", maxListLines)
	}
	return header + b.String(), nil
}

// RouteIn names a route.
type RouteIn struct {
	Route string `json:"route" jsonschema:"the route, e.g. \"POST /{lang}/groups\" or its ID"`
}

func (s *server) getRoute(ctx context.Context, _ *mcp.CallToolRequest, in RouteIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		rt, err := resolveKind(ctx, r.Graph, in.Route, graph.KindRoute)
		if err != nil {
			return "", err
		}
		a := rt.Attrs
		var b strings.Builder
		fmt.Fprintf(&b, "%s  (id %s)\nregistered at %s\n", rt.Name, rt.ID, pos(rt.Pos))
		access := a["access"]
		if a["optionalAuth"] == "true" {
			access = "public, reads the session"
		}
		fmt.Fprintf(&b, "access: %s", access)
		if ev := a["accessEvidence"]; ev != "" {
			fmt.Fprintf(&b, " (%s)", short(ev))
		}
		fmt.Fprintf(&b, "\nhandler: %s\n", funcRef(a["handler"]))
		if mw := a["middleware"]; mw != "" {
			fmt.Fprintf(&b, "middleware: %s\n", short(mw))
		}
		if v := a["variants"]; v != "" {
			fmt.Fprintf(&b, "variants: %s\n", v)
		}
		sections := []struct {
			title string
			kind  graph.EdgeKind
			dir   graph.Direction
		}{
			{"templates", graph.EdgeRenders, graph.Out},
			{"requests from its page", graph.EdgeRequests, graph.Out},
			{"static assets", graph.EdgeLoads, graph.Out},
			{"navigates to", graph.EdgeNavigatesTo, graph.Out},
			{"reached from", graph.EdgeNavigatesTo, graph.In},
		}
		for _, sec := range sections {
			nbs, err := r.Graph.Neighbors(ctx, rt.ID, sec.dir, sec.kind)
			if err != nil {
				return "", err
			}
			if len(nbs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "%s:\n", sec.title)
			for _, nb := range nbs {
				switch sec.kind {
				case graph.EdgeRequests:
					target := nb.Node.Attrs["target"]
					if target == "" {
						target = nb.Node.Attrs["method"] + " " + nb.Node.Attrs["url"] + " (unresolved)"
					}
					fmt.Fprintf(&b, "  %s\t%s\t%s\n", target, nb.Node.Attrs["trigger"], pos(nb.Node.Pos))
				case graph.EdgeNavigatesTo:
					via := nb.Edge.Attrs["template"]
					if via == "" {
						via = short(nb.Edge.Attrs["via"])
					}
					fmt.Fprintf(&b, "  %s\t%s via %s\t%s\n", nb.Node.Name, nb.Edge.Attrs["trigger"], via, pos(nb.Edge.Pos))
				default:
					fmt.Fprintf(&b, "  %s\t%s\n", nb.Node.Name, cmp.Or(nb.Node.Attrs["file"], pos(nb.Node.Pos)))
				}
			}
		}
		return b.String(), nil
	})
}

// FlowIn configures get_flow.
type FlowIn struct {
	Route  string `json:"route" jsonschema:"the route, e.g. \"POST /{lang}/groups\""`
	Method string `json:"method,omitempty" jsonschema:"follow branches for this request method instead of the route's"`
	Prune  string `json:"prune,omitempty" jsonschema:"sinks (default: only paths reaching SQL/files/HTTP/...), module (all module code) or none"`
	Depth  int    `json:"depth,omitempty" jsonschema:"maximum call depth (default 16)"`
	Format string `json:"format,omitempty" jsonschema:"text (default, a call tree) or mermaid (a sequence diagram)"`
}

func (s *server) getFlow(ctx context.Context, _ *mcp.CallToolRequest, in FlowIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		rt, err := resolveKind(ctx, r.Graph, in.Route, graph.KindRoute)
		if err != nil {
			return "", err
		}
		opts := flow.Options{Method: in.Method, MaxDepth: in.Depth}
		switch in.Prune {
		case "", "sinks":
		case "module":
			opts.Prune = flow.PruneModule
		case "none":
			opts.Prune = flow.PruneNone
		default:
			return "", fmt.Errorf("prune must be sinks, module or none")
		}
		tree, err := flow.Build(ctx, r.Graph, rt.ID, opts)
		if err != nil {
			return "", err
		}
		if in.Format == "mermaid" {
			return mermaid.Sequence(tree).Mermaid, nil
		}
		var buf bytes.Buffer
		if err := flow.WriteText(&buf, tree); err != nil {
			return "", err
		}
		return buf.String(), nil
	})
}

// NodeIn names a node.
type NodeIn struct {
	ID string `json:"id" jsonschema:"a node ID, or a name / end of an ID that matches one node"`
}

func (s *server) getNode(ctx context.Context, _ *mcp.CallToolRequest, in NodeIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		n, err := r.Graph.Resolve(ctx, in.ID)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s\nid: %s\n", n.Kind, n.Name, n.ID)
		if n.Package != "" {
			fmt.Fprintf(&b, "package: %s\n", n.Package)
		}
		if p := pos(n.Pos); p != "" {
			if n.Pos.EndLine > n.Pos.StartLine {
				p += fmt.Sprintf("-%d", n.Pos.EndLine)
			}
			fmt.Fprintf(&b, "source: %s\n", p)
		}
		if n.Detail != "" {
			fmt.Fprintf(&b, "detail: %s\n", n.Detail)
		}
		keys := make([]string, 0, len(n.Attrs))
		for k := range n.Attrs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, n.Attrs[k])
		}
		for _, dir := range []graph.Direction{graph.Out, graph.In} {
			nbs, err := r.Graph.Neighbors(ctx, n.ID, dir)
			if err != nil {
				return "", err
			}
			writeEdges(&b, nbs, dir)
		}
		return b.String(), nil
	})
}

// writeEdges lists neighbors grouped by edge kind, capped per kind.
func writeEdges(b *strings.Builder, nbs []graph.Neighbor, dir graph.Direction) {
	var kinds []graph.EdgeKind
	byKind := map[graph.EdgeKind][]graph.Neighbor{}
	for _, nb := range nbs {
		if _, ok := byKind[nb.Edge.Kind]; !ok {
			kinds = append(kinds, nb.Edge.Kind)
		}
		byKind[nb.Edge.Kind] = append(byKind[nb.Edge.Kind], nb)
	}
	for _, k := range kinds {
		list := byKind[k]
		arrow := string(k) + " →"
		if dir == graph.In {
			arrow = "← " + string(k)
		}
		fmt.Fprintf(b, "%s (%d):\n", arrow, len(list))
		for i, nb := range list {
			if i == maxEdgesByKind {
				fmt.Fprintf(b, "  … %d more\n", len(list)-i)
				break
			}
			fmt.Fprintf(b, "  %s\t%s\n", nb.Node.ID, pos(nb.Edge.Pos))
		}
	}
}

// SourceIn selects source lines.
type SourceIn struct {
	File  string `json:"file" jsonschema:"path relative to the module root, as in positions"`
	Start int    `json:"start,omitempty" jsonschema:"first line (default 1)"`
	End   int    `json:"end,omitempty" jsonschema:"last line (default start+100)"`
}

func (s *server) getSource(_ context.Context, _ *mcp.CallToolRequest, in SourceIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		root, err := os.OpenRoot(r.Dir) // confines reads to the module, symlinks included
		if err != nil {
			return "", err
		}
		defer func() { _ = root.Close() }()
		data, err := root.ReadFile(filepath.Clean(filepath.FromSlash(in.File)))
		if err != nil {
			return "", fmt.Errorf("cannot read %s in the module: %w", in.File, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		start := max(in.Start, 1)
		end := in.End
		if end == 0 {
			end = start + 100
		}
		end = min(end, len(lines), start+maxSourceLines-1)
		if start > end {
			return "", fmt.Errorf("%s has %d lines", in.File, len(lines))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s lines %d-%d of %d\n", in.File, start, end, len(lines))
		for i := start; i <= end; i++ {
			fmt.Fprintf(&b, "%5d  %s\n", i, lines[i-1])
		}
		return b.String(), nil
	})
}

// SymbolIn names a function, method or other node.
type SymbolIn struct {
	Symbol string `json:"symbol" jsonschema:"a node ID or a name / end of an ID matching one node, e.g. \"GroupService).Create\""`
}

func (s *server) findCallers(ctx context.Context, _ *mcp.CallToolRequest, in SymbolIn) (*mcp.CallToolResult, any, error) {
	return s.neighbors(ctx, in.Symbol, graph.In, graph.EdgeCalls, graph.EdgeDispatchesTo, graph.EdgeHandledBy)
}

func (s *server) findCallees(ctx context.Context, _ *mcp.CallToolRequest, in SymbolIn) (*mcp.CallToolResult, any, error) {
	return s.neighbors(ctx, in.Symbol, graph.Out, graph.EdgeCalls, graph.EdgeDispatchesTo, graph.EdgeQueries)
}

func (s *server) neighbors(ctx context.Context, symbol string, dir graph.Direction, kinds ...graph.EdgeKind) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		n, err := r.Graph.Resolve(ctx, symbol)
		if err != nil {
			return "", err
		}
		nbs, err := r.Graph.Neighbors(ctx, n.ID, dir, kinds...)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", n.ID)
		if len(nbs) == 0 {
			b.WriteString("(none)\n")
		}
		for i, nb := range nbs {
			if i == maxListLines {
				fmt.Fprintf(&b, "… %d more\n", len(nbs)-i)
				break
			}
			fmt.Fprintf(&b, "  %s\t%s\t%s\n", nb.Edge.Kind, nb.Node.ID, pos(nb.Edge.Pos))
		}
		return b.String(), nil
	})
}

// PathsIn names the ends of a path search.
type PathsIn struct {
	From     string `json:"from" jsonschema:"start node: a route, function or ID"`
	To       string `json:"to" jsonschema:"end node, e.g. sql_table:groups or a function"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"maximum edges per path (default 10)"`
}

func (s *server) findPaths(ctx context.Context, _ *mcp.CallToolRequest, in PathsIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		from, err := r.Graph.Resolve(ctx, in.From)
		if err != nil {
			return "", err
		}
		to, err := r.Graph.Resolve(ctx, in.To)
		if err != nil {
			return "", err
		}
		paths, err := r.Graph.Paths(ctx, from.ID, to.ID, graph.PathOptions{MaxDepth: in.MaxDepth, Limit: maxPaths})
		if err != nil {
			return "", err
		}
		if len(paths) == 0 {
			return fmt.Sprintf("no path from %s to %s within the depth limit\n", from.ID, to.ID), nil
		}
		var b strings.Builder
		for i, p := range paths {
			fmt.Fprintf(&b, "path %d:\n  %s\n", i+1, p[0].From)
			for _, e := range p {
				fmt.Fprintf(&b, "  -%s-> %s\t%s\n", e.Kind, e.To, pos(e.Pos))
			}
		}
		return b.String(), nil
	})
}

// TableRoutesIn selects a table and operation.
type TableRoutesIn struct {
	Table string `json:"table" jsonschema:"table name"`
	Op    string `json:"op,omitempty" jsonschema:"only this operation: select, insert, update, delete"`
}

func (s *server) routesTouchingTable(ctx context.Context, _ *mcp.CallToolRequest, in TableRoutesIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		if _, err := r.Graph.Node(ctx, graph.NodeID(graph.KindSQLTable, in.Table)); err != nil {
			return "", fmt.Errorf("no table %q", in.Table)
		}
		byTable, err := s.tableRoutes(ctx, r)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		n := 0
		for _, tr := range byTable[in.Table] {
			if in.Op != "" && tr.Op != in.Op {
				continue
			}
			n++
			fmt.Fprintf(&b, "%s\t%s\n", tr.Route, tr.Op)
		}
		return fmt.Sprintf("%d routes touch %s\n", n, in.Table) + b.String(), nil
	})
}

// tableRoutes computes the table → routes index once per analysis.
func (s *server) tableRoutes(ctx context.Context, r *analysis.Result) (map[string][]views.TableRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tablesFor == r {
		return s.tables, nil
	}
	m, err := views.TableRoutes(ctx, r.Graph)
	if err != nil {
		return nil, err
	}
	s.tablesFor, s.tables = r, m
	return m, nil
}

func (s *server) listTables(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) { return tablesText(ctx, r) })
}

func tablesText(ctx context.Context, r *analysis.Result) (string, error) {
	tables, err := r.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindSQLTable}})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d tables\n", len(tables))
	for _, t := range tables {
		note := ""
		if t.Attrs["inferred"] == "true" {
			note = " [not in migrations]"
		}
		fmt.Fprintf(&b, "%s%s: %s\n", t.Name, note, t.Detail)
	}
	return b.String(), nil
}

// TableIn selects a table and output format.
type TableIn struct {
	Name   string `json:"name" jsonschema:"table name"`
	Format string `json:"format,omitempty" jsonschema:"text (default) or mermaid (ER diagram of the table and its foreign-key neighbours)"`
}

func (s *server) getTable(ctx context.Context, _ *mcp.CallToolRequest, in TableIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		id := graph.NodeID(graph.KindSQLTable, in.Name)
		t, err := r.Graph.Node(ctx, id)
		if err != nil {
			return "", fmt.Errorf("no table %q", in.Name)
		}
		if in.Format == "mermaid" {
			d, err := views.ER(ctx, r.Graph, in.Name, 1)
			return d.Mermaid, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "table %s (defined at %s)\ncolumns:\n", t.Name, cmp.Or(pos(t.Pos), "not in migrations"))
		cols, err := r.Graph.Neighbors(ctx, id, graph.Out, graph.EdgeHasColumn)
		if err != nil {
			return "", err
		}
		for _, c := range cols {
			var flags []string
			if c.Node.Attrs["primaryKey"] == "true" {
				flags = append(flags, "PK")
			}
			if c.Node.Attrs["notNull"] == "true" {
				flags = append(flags, "NOT NULL")
			}
			fmt.Fprintf(&b, "  %s %s %s\n", c.Node.Name, c.Node.Detail, strings.Join(flags, " "))
		}
		for _, dir := range []graph.Direction{graph.Out, graph.In} {
			fks, err := r.Graph.Neighbors(ctx, id, dir, graph.EdgeFK)
			if err != nil {
				return "", err
			}
			for _, fk := range fks {
				od := fk.Edge.Attrs["onDelete"]
				if od != "" {
					od = " on delete " + od
				}
				if dir == graph.Out {
					fmt.Fprintf(&b, "references: %s → %s%s\n", fk.Edge.Attrs["columns"], fk.Edge.Attrs["references"], od)
				} else {
					fmt.Fprintf(&b, "referenced by: %s.%s%s\n", fk.Node.Name, fk.Edge.Attrs["columns"], od)
				}
			}
		}
		byTable, err := s.tableRoutes(ctx, r)
		if err != nil {
			return "", err
		}
		if routes := byTable[in.Name]; len(routes) > 0 {
			b.WriteString("routes:\n")
			for _, tr := range routes {
				fmt.Fprintf(&b, "  %s\t%s\n", tr.Route, tr.Op)
			}
		}
		queries, err := r.Graph.Neighbors(ctx, id, graph.In, graph.EdgeQueries)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "queries (%d):\n", len(queries))
		for i, q := range queries {
			if i == maxEdgesByKind {
				fmt.Fprintf(&b, "  … %d more\n", len(queries)-i)
				break
			}
			fmt.Fprintf(&b, "  %s in %s (%s): %s\n", q.Edge.Attrs["op"], short(q.Node.Attrs["caller"]), q.Node.ID, oneLine(q.Node.Detail))
		}
		return b.String(), nil
	})
}

// SearchIn is a search query.
type SearchIn struct {
	Query string `json:"query" jsonschema:"text to find in names, packages, files, SQL and IDs"`
	Kind  string `json:"kind,omitempty" jsonschema:"only this node kind, e.g. route, method, func, type, sql_table, sink.sql"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results (default 30)"`
}

func (s *server) search(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		opts := graph.SearchOptions{Limit: min(cmp.Or(in.Limit, 30), maxListLines)}
		if in.Kind != "" {
			opts.Kinds = []graph.NodeKind{graph.NodeKind(in.Kind)}
		}
		nodes, err := r.Graph.Search(ctx, in.Query, opts)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d results\n", len(nodes))
		for _, n := range nodes {
			fmt.Fprintf(&b, "%s\t%s\n", n.ID, pos(n.Pos))
		}
		return b.String(), nil
	})
}

func (s *server) reanalyze(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	if err := s.cur.Reanalyze(ctx); err != nil {
		return nil, nil, fmt.Errorf("reanalysis failed, keeping the previous analysis: %w", err)
	}
	return s.with(func(r *analysis.Result) (string, error) {
		c, err := r.Graph.Counts(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("analyzed %s in %v: %d routes, %d functions, %d SQL queries, %d tables\n",
			r.Module, r.Stats.Total().Round(1e6), c.Nodes[graph.KindRoute], r.Stats.ModuleFunctions,
			c.Nodes[graph.KindSinkSQL], c.Nodes[graph.KindSQLTable]), nil
	})
}

func (s *server) routesResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	var out string
	err := s.cur.With(func(r *analysis.Result) error {
		var err error
		out, err = routesText(ctx, r, ListRoutesIn{})
		return err
	})
	return resource(req, out), err
}

func (s *server) schemaResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	var out string
	err := s.cur.With(func(r *analysis.Result) error {
		tables, err := tablesText(ctx, r)
		if err != nil {
			return err
		}
		er, err := views.ER(ctx, r.Graph, "", 0)
		out = tables + "\n" + er.Mermaid
		return err
	})
	return resource(req, out), err
}

func resource(req *mcp.ReadResourceRequest, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/plain", Text: text}}}
}

// resolveKind resolves ref, requiring a node of kind.
func resolveKind(ctx context.Context, g *graph.Graph, ref string, kind graph.NodeKind) (graph.Node, error) {
	n, err := g.Resolve(ctx, ref)
	if err != nil {
		return graph.Node{}, err
	}
	if n.Kind != kind {
		return graph.Node{}, fmt.Errorf("%s is a %s, not a %s", n.ID, n.Kind, kind)
	}
	return n, nil
}

func pos(p graph.Pos) string {
	if p.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", p.File, p.StartLine)
}

// pkgPathRE matches a package path followed by the dot before a name.
var pkgPathRE = regexp.MustCompile(`[\w.-]+(?:/[\w.-]+)+\.`)

// short drops package path directories from go/ssa names wherever they
// appear: "(*example.com/app/web.H).List" -> "(*web.H).List".
func short(s string) string {
	s = strings.ReplaceAll(s, ",", ", ")
	return pkgPathRE.ReplaceAllStringFunc(s, func(m string) string { return m[strings.LastIndex(m, "/")+1:] })
}

// funcRef names a handler with its graph ID so agents can follow it.
func funcRef(name string) string {
	kind := graph.KindFunc
	if strings.HasPrefix(name, "(") {
		kind = graph.KindMethod
	}
	return fmt.Sprintf("%s (id %s)", short(name), graph.NodeID(kind, name))
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func byPos(a, b graph.Node) int {
	return cmp.Or(cmp.Compare(a.Pos.File, b.Pos.File), cmp.Compare(a.Pos.StartLine, b.Pos.StartLine), cmp.Compare(a.ID, b.ID))
}
