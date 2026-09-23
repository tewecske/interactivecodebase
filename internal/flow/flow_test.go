package flow

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// synthetic builds: route -> handler -> {helper x2, a, log(external)};
// a -> b -> a (cycle); a -> sink -> table; helper -> a (ref); handler's
// call to get() only runs for GET.
func synthetic(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	fn := func(name string, attrs map[string]string) graph.Node {
		return graph.Node{ID: "func:" + name, Kind: graph.KindFunc, Name: name, Package: "example.com/app", Pos: graph.Pos{File: "app.go", StartLine: len(name)}, Attrs: attrs}
	}
	at := func(line int) graph.Pos { return graph.Pos{File: "app.go", StartLine: line} }
	nodes := []graph.Node{
		{ID: "route:POST /x", Kind: graph.KindRoute, Name: "POST /x", Attrs: map[string]string{"method": "POST"}},
		{ID: "route:GET /x", Kind: graph.KindRoute, Name: "GET /x", Attrs: map[string]string{"method": "GET"}},
		fn("handler", map[string]string{"methodBranches": "GET"}), fn("helper", nil), fn("a", nil), fn("b", nil), fn("get", nil), fn("idle", nil),
		{ID: "func:log.Print", Kind: graph.KindFunc, Name: "Print", Package: "log", Attrs: map[string]string{"external": "true"}},
		{ID: "sink.sql:app.go:9:1", Kind: graph.KindSinkSQL, Name: "(*sql.DB).Exec", Detail: "UPDATE t\n  SET x = 1"},
		{ID: "sql_table:t", Kind: graph.KindSQLTable, Name: "t"},
	}
	edges := []graph.Edge{
		{From: "route:POST /x", To: "func:handler", Kind: graph.EdgeHandledBy},
		{From: "route:GET /x", To: "func:handler", Kind: graph.EdgeHandledBy},
		{From: "func:handler", To: "func:get", Kind: graph.EdgeCalls, Pos: at(1), Attrs: map[string]string{"methods": "GET"}},
		{From: "func:handler", To: "func:a", Kind: graph.EdgeCalls, Pos: at(2), Attrs: map[string]string{"methods": "*"}},
		{From: "func:handler", To: "func:helper", Kind: graph.EdgeCalls, Pos: at(3)},
		{From: "func:handler", To: "func:helper", Kind: graph.EdgeCalls, Pos: at(4)},
		{From: "func:handler", To: "func:log.Print", Kind: graph.EdgeCalls, Pos: at(5)},
		{From: "func:handler", To: "func:idle", Kind: graph.EdgeCalls, Pos: at(6)},
		{From: "func:a", To: "func:b", Kind: graph.EdgeCalls, Pos: at(7)},
		{From: "func:a", To: "sink.sql:app.go:9:1", Kind: graph.EdgeCalls, Pos: at(8)},
		{From: "func:b", To: "func:a", Kind: graph.EdgeCalls, Pos: at(9)},
		{From: "func:helper", To: "func:a", Kind: graph.EdgeCalls, Pos: at(10)},
		{From: "func:get", To: "func:a", Kind: graph.EdgeCalls, Pos: at(11)},
		{From: "sink.sql:app.go:9:1", To: "sql_table:t", Kind: graph.EdgeQueries, Attrs: map[string]string{"op": "update", "columns": "x"}},
	}
	err = g.Write(t.Context(), func(w *graph.Writer) error {
		for _, n := range nodes {
			if err := w.AddNode(n); err != nil {
				return err
			}
		}
		for _, e := range edges {
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

func render(t *testing.T, g *graph.Graph, route string, opts Options) string {
	t.Helper()
	tree, err := Build(t.Context(), g, route, opts)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, tree); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestBuildPruneSinks(t *testing.T) {
	g := synthetic(t)
	got := render(t, g, "route:POST /x", Options{})
	want := `POST /x
└─ app.handler  app.go:7
   ├─ app.a  app.go:1
   │  └─ sink.sql (*sql.DB).Exec "UPDATE t SET x = 1"
   │     └─ table t (update: x)
   └─ app.helper ×2  app.go:6
      └─ app.a  app.go:1  (see above)
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildPruneNone(t *testing.T) {
	g := synthetic(t)
	got := render(t, g, "route:POST /x", Options{Prune: PruneNone})
	for _, want := range []string{"app.b  app.go:1\n", "app.a  app.go:1  (recursive)", "log.Print", "app.idle"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildMethodBranches(t *testing.T) {
	g := synthetic(t)
	post := render(t, g, "route:POST /x", Options{Prune: PruneModule})
	get := render(t, g, "route:GET /x", Options{Prune: PruneModule})
	if strings.Contains(post, "app.get") || !strings.Contains(post, "├─ app.a") {
		t.Errorf("POST runs a (the * branch) but not get:\n%s", post)
	}
	if !strings.Contains(get, "app.get") || strings.Contains(get, "├─ app.a") {
		t.Errorf("GET runs get but not the * branch directly:\n%s", get)
	}
	anyMethod := render(t, g, "route:POST /x", Options{Prune: PruneModule, Method: "ANY"})
	if !strings.Contains(anyMethod, "app.get") || !strings.Contains(anyMethod, "├─ app.a") {
		t.Errorf("ANY runs both branches:\n%s", anyMethod)
	}
}

func TestBuildDepthLimit(t *testing.T) {
	g := synthetic(t)
	got := render(t, g, "route:POST /x", Options{Prune: PruneModule, MaxDepth: 2})
	if !strings.Contains(got, "(depth limit)") || strings.Contains(got, "sink.sql") {
		t.Errorf("depth 2 should stop below the handler:\n%s", got)
	}
}

func TestBuildRejectsNonRoute(t *testing.T) {
	g := synthetic(t)
	if _, err := Build(t.Context(), g, "func:a", Options{}); err == nil || !strings.Contains(err.Error(), "not a route") {
		t.Errorf("err = %v", err)
	}
}

func TestGroupParallel(t *testing.T) {
	step := func(id, group, branch string) *Step {
		s := &Step{Node: graph.Node{ID: id}}
		if group != "" {
			s.Edge.Attrs = map[string]string{"parallel": group, "branch": branch}
		}
		return s
	}
	got := groupParallel([]*Step{
		step("a", "", ""), step("d", "g", "2"), step("b", "", ""), step("c", "g", "1"), step("e", "h", "each"), step("f", "g", "each"),
	})
	var ids []string
	for _, s := range got {
		ids = append(ids, s.Node.ID)
	}
	if want := []string{"a", "c", "d", "f", "b", "e"}; !slices.Equal(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
}
