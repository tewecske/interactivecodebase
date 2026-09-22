package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Node IDs of the fixture, modelled on goweb's "POST /{lang}/groups" flow.
var (
	idRoute   = NodeID(KindRoute, "POST /{lang}/groups")
	idHandler = NodeID(KindHandler, "httpadapter.(*groupHandler).create")
	idService = NodeID(KindMethod, "service.(*GroupService).Create")
	idIface   = NodeID(KindInterfaceCall, "service.GroupRepository.CreateGroup")
	idRepo    = NodeID(KindMethod, "postgres.(*GroupRepository).CreateGroup")
	idAuth    = NodeID(KindMethod, "httpadapter.Authenticator.Authenticate")
	idSink    = NodeID(KindSinkSQL, "postgres/groups.go:80")
	idGroups  = NodeID(KindSQLTable, "groups")
	idMembers = NodeID(KindSQLTable, "group_members")
	idName    = NodeID(KindSQLColumn, "groups.name")
)

func fixtureNodes() []Node {
	return []Node{
		{ID: idRoute, Kind: KindRoute, Name: "POST /{lang}/groups", Detail: "POST /en/groups POST /hu/groups",
			Pos: Pos{File: "internal/adapter/http/handler.go", StartLine: 80, StartCol: 3}, Attrs: map[string]string{"method": "POST"}},
		{ID: idHandler, Kind: KindHandler, Name: "create", Package: "github.com/tewecske/goweb/internal/adapter/http",
			Pos: Pos{File: "internal/adapter/http/group.go", StartLine: 40, StartCol: 1, EndLine: 90, EndCol: 2}},
		{ID: idAuth, Kind: KindMethod, Name: "Authenticate", Package: "github.com/tewecske/goweb/internal/adapter/http"},
		{ID: idService, Kind: KindMethod, Name: "Create", Package: "github.com/tewecske/goweb/internal/service"},
		{ID: idIface, Kind: KindInterfaceCall, Name: "CreateGroup", Package: "github.com/tewecske/goweb/internal/service"},
		{ID: idRepo, Kind: KindMethod, Name: "CreateGroup", Package: "github.com/tewecske/goweb/internal/store/postgres",
			Detail: "func (r *GroupRepository) CreateGroup(ctx context.Context, group service.Group) (service.Group, error)"},
		{ID: idSink, Kind: KindSinkSQL, Name: "INSERT groups", Package: "github.com/tewecske/goweb/internal/store/postgres",
			Detail: "INSERT INTO groups (name, name_norm, invite_code, created_by) VALUES ($1, $2, $3, $4)",
			Attrs:  map[string]string{"op": "insert"}},
		{ID: idGroups, Kind: KindSQLTable, Name: "groups"},
		{ID: idMembers, Kind: KindSQLTable, Name: "group_members"},
		{ID: idName, Kind: KindSQLColumn, Name: "name"},
	}
}

func fixtureEdges() []Edge {
	at := func(line int) Pos { return Pos{File: "internal/adapter/http/group.go", StartLine: line, StartCol: 5} }
	return []Edge{
		{From: idRoute, To: idHandler, Kind: EdgeHandledBy},
		// Inserted out of source order to check Neighbors sorts by position.
		{From: idHandler, To: idService, Kind: EdgeCalls, Pos: at(60)},
		{From: idHandler, To: idAuth, Kind: EdgeCalls, Pos: at(45)},
		{From: idRoute, To: idAuth, Kind: EdgeGuardedBy, Pos: at(45)},
		{From: idService, To: idIface, Kind: EdgeCalls},
		{From: idIface, To: idRepo, Kind: EdgeDispatchesTo},
		{From: idRepo, To: idSink, Kind: EdgeCalls},
		// A recursive call: path search must not loop.
		{From: idRepo, To: idService, Kind: EdgeCalls, Pos: Pos{File: "x.go", StartLine: 1}},
		{From: idSink, To: idGroups, Kind: EdgeQueries, Attrs: map[string]string{"op": "insert"}},
		{From: idGroups, To: idName, Kind: EdgeHasColumn},
		{From: idMembers, To: idGroups, Kind: EdgeFK, Attrs: map[string]string{"column": "group_id", "onDelete": "cascade"}},
	}
}

func newFixture(t *testing.T) *Graph {
	t.Helper()
	g := newGraph(t)
	err := g.Write(t.Context(), func(w *Writer) error {
		for _, n := range fixtureNodes() {
			if err := w.AddNode(n); err != nil {
				return err
			}
		}
		for _, e := range fixtureEdges() {
			if err := w.AddEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func newGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return g
}

func TestNodeRoundTrip(t *testing.T) {
	g := newFixture(t)
	for _, want := range fixtureNodes() {
		got, err := g.Node(t.Context(), want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Node(%s)\n got %+v\nwant %+v", want.ID, got, want)
		}
	}
	if _, err := g.Node(t.Context(), "func:missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing node: err = %v, want ErrNotFound", err)
	}
}

func TestGraphsAreIsolated(t *testing.T) {
	g1 := newFixture(t)
	g2 := newGraph(t)
	if _, err := g2.Node(t.Context(), idRoute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second graph sees first graph's data: err = %v", err)
	}
	if _, err := g1.Node(t.Context(), idRoute); err != nil {
		t.Fatal(err)
	}
}

func TestWriteValidation(t *testing.T) {
	g := newFixture(t)
	tests := []struct {
		name string
		fn   func(w *Writer) error
		want string
	}{
		{"empty node ID", func(w *Writer) error { return w.AddNode(Node{Kind: KindFunc}) }, "empty ID"},
		{"unknown node kind", func(w *Writer) error { return w.AddNode(Node{ID: "x", Kind: "bogus"}) }, `unknown kind "bogus"`},
		{"unknown edge kind", func(w *Writer) error { return w.AddEdge(Edge{From: idRoute, To: idHandler, Kind: "bogus"}) }, `unknown kind "bogus"`},
		{"missing endpoint", func(w *Writer) error { return w.AddEdge(Edge{From: idRoute, To: "func:missing", Kind: EdgeCalls}) }, "FOREIGN KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.Write(t.Context(), tt.fn)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestWriteRollsBackOnError(t *testing.T) {
	g := newGraph(t)
	boom := errors.New("boom")
	err := g.Write(t.Context(), func(w *Writer) error {
		if err := w.AddNode(Node{ID: "func:a", Kind: KindFunc, Name: "a"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, err := g.Node(t.Context(), "func:a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("node written despite rollback: err = %v", err)
	}
}

func TestAddNodeUpsertsAndReindexes(t *testing.T) {
	g := newFixture(t)
	updated := Node{ID: idRepo, Kind: KindMethod, Name: "InsertTeam", Package: "example/store"}
	if err := g.Write(t.Context(), func(w *Writer) error { return w.AddNode(updated) }); err != nil {
		t.Fatal(err)
	}
	got, err := g.Node(t.Context(), idRepo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Errorf("got %+v, want %+v", got, updated)
	}
	// Edges to the replaced node survive.
	if nbs, _ := g.Neighbors(t.Context(), idRepo, In, EdgeDispatchesTo); len(nbs) != 1 {
		t.Errorf("incoming dispatches_to edges = %d, want 1", len(nbs))
	}
	if ids := searchIDs(t, g, "InsertTeam", SearchOptions{}); !slices.Equal(ids, []string{idRepo}) {
		t.Errorf("search new name = %v", ids)
	}
	if ids := searchIDs(t, g, "context.Context", SearchOptions{Kinds: []NodeKind{KindMethod}}); len(ids) != 0 {
		t.Errorf("search old detail still matches: %v", ids)
	}
}

func TestDuplicateEdgeIgnored(t *testing.T) {
	g := newFixture(t)
	before, _ := g.Counts(t.Context())
	if err := g.Write(t.Context(), func(w *Writer) error { return w.AddEdge(fixtureEdges()[0]) }); err != nil {
		t.Fatal(err)
	}
	after, _ := g.Counts(t.Context())
	if !reflect.DeepEqual(before, after) {
		t.Errorf("counts changed: %v -> %v", before, after)
	}
}

func TestNodesFilter(t *testing.T) {
	g := newFixture(t)
	nodes, err := g.Nodes(t.Context(), NodeFilter{Kinds: []NodeKind{KindSQLTable, KindSQLColumn}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := nodeIDs(nodes), []string{idName, idMembers, idGroups}; !slices.Equal(got, want) {
		t.Errorf("by kind = %v, want %v", got, want)
	}
	nodes, err = g.Nodes(t.Context(), NodeFilter{Kinds: []NodeKind{KindMethod}, Package: "github.com/tewecske/goweb/internal/service"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := nodeIDs(nodes), []string{idService}; !slices.Equal(got, want) {
		t.Errorf("by kind+package = %v, want %v", got, want)
	}
}

func TestNeighbors(t *testing.T) {
	g := newFixture(t)
	out, err := g.Neighbors(t.Context(), idHandler, Out, EdgeCalls)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := neighborIDs(out), []string{idAuth, idService}; !slices.Equal(got, want) {
		t.Errorf("callees in source order = %v, want %v", got, want)
	}
	in, err := g.Neighbors(t.Context(), idGroups, In)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := neighborIDs(in), []string{idSink, idMembers}; !sameSet(got, want) {
		t.Errorf("incoming = %v, want %v", got, want)
	}
	for _, nb := range in {
		if nb.Edge.Kind == EdgeFK && nb.Edge.Attrs["onDelete"] != "cascade" {
			t.Errorf("fk edge attrs = %v", nb.Edge.Attrs)
		}
	}
}

func TestPaths(t *testing.T) {
	g := newFixture(t)
	ctx := t.Context()

	paths, err := g.Paths(ctx, idRoute, idGroups, PathOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{idRoute, idHandler, idService, idIface, idRepo, idSink, idGroups}
	if len(paths) != 1 || !slices.Equal(pathNodes(paths[0]), want) {
		t.Fatalf("paths = %v, want one path %v", pathsNodes(paths), want)
	}
	if paths[0][5].Attrs["op"] != "insert" {
		t.Errorf("last edge attrs = %v", paths[0][5].Attrs)
	}

	paths, err = g.Paths(ctx, idRoute, idAuth, PathOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pathsNodes(paths); len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 3 {
		t.Errorf("route -> auth paths = %v, want direct guard edge then via handler", got)
	}

	paths, err = g.Paths(ctx, idRoute, idAuth, PathOptions{EdgeKinds: []EdgeKind{EdgeHandledBy, EdgeCalls}})
	if err != nil {
		t.Fatal(err)
	}
	if got := pathsNodes(paths); len(got) != 1 || len(got[0]) != 3 {
		t.Errorf("filtered paths = %v, want only via handler", got)
	}

	if paths, err = g.Paths(ctx, idRoute, idGroups, PathOptions{MaxDepth: 3}); err != nil || paths != nil {
		t.Errorf("depth-limited paths = %v, %v; want none", pathsNodes(paths), err)
	}
	if paths, err = g.Paths(ctx, idGroups, idRoute, PathOptions{}); err != nil || paths != nil {
		t.Errorf("reverse paths = %v, %v; want none", pathsNodes(paths), err)
	}
}

func TestSearch(t *testing.T) {
	g := newFixture(t)
	tests := []struct {
		name string
		q    string
		opts SearchOptions
		want []string
	}{
		{"substring of name", "creategr", SearchOptions{}, []string{idIface, idRepo}},
		{"SQL text in detail", "insert into groups", SearchOptions{}, []string{idSink}},
		{"kind filter", "group", SearchOptions{Kinds: []NodeKind{KindSQLTable}}, []string{idGroups, idMembers}},
		{"short term uses LIKE", "hu", SearchOptions{Kinds: []NodeKind{KindRoute, KindSQLTable}}, []string{idRoute}},
		{"short term matches package", "hu", SearchOptions{Kinds: []NodeKind{KindInterfaceCall}}, []string{idIface}},
		{"short term no match", "zq", SearchOptions{}, nil},
		{"quotes are literal", `"groups`, SearchOptions{}, nil},
		{"qualified name across package and name", "postgres.(*GroupRepository).Create", SearchOptions{}, []string{idRepo}},
		{"limit", "group", SearchOptions{Limit: 1}, []string{idGroups}},
		{"blank", "  ", SearchOptions{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchIDs(t, g, tt.q, tt.opts)
			if !sameSet(got, tt.want) {
				t.Errorf("Search(%q) = %v, want %v", tt.q, got, tt.want)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	g := newFixture(t)
	c, err := g.Counts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if c.Nodes[KindSQLTable] != 2 || c.Nodes[KindMethod] != 3 || c.Edges[EdgeCalls] != 5 || c.Edges[EdgeFK] != 1 {
		t.Errorf("counts = %+v", c)
	}
}

func TestExport(t *testing.T) {
	g := newFixture(t)
	var buf bytes.Buffer
	if err := g.Export(t.Context(), &buf); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != len(fixtureNodes()) || len(got.Edges) != len(fixtureEdges()) {
		t.Fatalf("exported %d nodes, %d edges", len(got.Nodes), len(got.Edges))
	}
	for i, want := range fixtureEdges() {
		e := got.Edges[i]
		if e.From != want.From || e.To != want.To || e.Kind != want.Kind || !reflect.DeepEqual(e.Attrs, want.Attrs) {
			t.Errorf("edge %d = %+v, want %+v", i, e, want)
		}
	}

	var empty bytes.Buffer
	if err := newGraph(t).Export(t.Context(), &empty); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.String(), `"nodes": []`) {
		t.Errorf("empty export = %s", empty.String())
	}
}

func TestConcurrentReads(t *testing.T) {
	g := newFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Go(func() {
			if _, err := g.Paths(ctx, idRoute, idGroups, PathOptions{}); err != nil {
				errs <- err
			}
			if _, err := g.Search(ctx, "group", SearchOptions{}); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func searchIDs(t *testing.T, g *Graph, q string, opts SearchOptions) []string {
	t.Helper()
	nodes, err := g.Search(t.Context(), q, opts)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return nodeIDs(nodes)
}

func nodeIDs(nodes []Node) []string {
	var ids []string
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

func neighborIDs(nbs []Neighbor) []string {
	var ids []string
	for _, nb := range nbs {
		ids = append(ids, nb.Node.ID)
	}
	return ids
}

func pathNodes(p []Edge) []string {
	if len(p) == 0 {
		return nil
	}
	ids := []string{p[0].From}
	for _, e := range p {
		ids = append(ids, e.To)
	}
	return ids
}

func pathsNodes(paths [][]Edge) [][]string {
	var out [][]string
	for _, p := range paths {
		out = append(out, pathNodes(p))
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
