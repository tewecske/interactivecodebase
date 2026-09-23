// Runs sbt and the Scala extractor, two JVMs; make test-scala runs this
// after building the extractor, without the race detector.

//go:build !race

package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/scala"
	"github.com/tewecske/interactivecodebase/internal/views"
)

// TestScalaFixture analyzes testdata/fixtures/scala/zioapp through sbt and
// icb-scala ($ICB_SCALA, default extractors/scala/target/icb-scala) and
// checks the code graph and a query on it.
func TestScalaFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("runs sbt and the Scala extractor")
	}
	for _, cmd := range []string{"sbt", "java"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("%s not installed", cmd)
		}
	}
	extractor := os.Getenv(scala.ExtractorEnv)
	if extractor == "" {
		extractor = filepath.Join(fixture.RepoRoot(), "extractors", "scala", "target", "icb-scala")
	}
	if _, err := os.Stat(extractor); err != nil {
		t.Skipf("no extractor (make scala-extractor builds it): %v", err)
	}
	t.Setenv(scala.ExtractorEnv, extractor)
	dir := filepath.Join(fixture.RepoRoot(), "testdata", "fixtures", "scala", "zioapp")

	ctx := context.Background()
	p, release, err := openProject(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	if p.Lang != analysis.LangScala || p.Go != nil || p.Module != "zioapp" {
		t.Errorf("project %s %q, Go analysis %v", p.Lang, p.Module, p.Go != nil)
	}
	g := p.Graph
	nodes := map[string]graph.Node{}
	all, err := g.Nodes(ctx, graph.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		nodes[n.ID] = n
	}
	for id, kind := range map[string]graph.NodeKind{
		"func:zioapp.backend.Main.run":                             graph.KindFunc,
		"func:zioapp.backend.http.NoteRoutes.routes":               graph.KindFunc,
		"func:zioapp.backend.http.NoteRoutes.publicRoutes$2":       graph.KindFunc,
		"func:zioapp.shared.validTitle":                            graph.KindFunc,
		"method:(zioapp.backend.service.NoteServiceLive).create":   graph.KindMethod,
		"method:(zioapp.backend.db.NoteRepositoryLive).find":       graph.KindMethod,
		"interface_call:(zioapp.backend.service.NoteService).find": graph.KindInterfaceCall,
		"interface_call:(zioapp.backend.db.NoteRepository).insert": graph.KindInterfaceCall,
		"type:zioapp.backend.service.NoteService":                  graph.KindType,
		"type:zioapp.backend.db.NoteRepositoryLive":                graph.KindType,
		"type:zioapp.shared.Note":                                  graph.KindType,
	} {
		if n, ok := nodes[id]; !ok || n.Kind != kind {
			t.Errorf("node %s: %+v, want kind %s", id, n, kind)
		}
	}
	if n := nodes["method:(zioapp.backend.service.NoteServiceLive).create"]; n.Pos.File != "modules/backend/src/main/scala/zioapp/backend/service/NoteService.scala" ||
		n.Pos.StartLine == 0 || !strings.Contains(n.Detail, "(in: CreateNote)") {
		t.Errorf("create: pos %+v, detail %q", n.Pos, n.Detail)
	}
	if d := nodes["type:zioapp.backend.service.NoteService"].Detail; d != "trait" {
		t.Errorf("NoteService detail %q, want trait", d)
	}
	for _, e := range []struct {
		from, to string
		kind     graph.EdgeKind
	}{
		{"func:zioapp.backend.http.NoteRoutes.publicRoutes$2", "interface_call:(zioapp.backend.service.NoteService).find", graph.EdgeCalls},
		{"route:GET /api/notes/{id}", "func:zioapp.backend.http.NoteRoutes.publicRoutes$2", graph.EdgeHandledBy},
		{"route:POST /api/notes", "func:zioapp.backend.http.RouteSupport.authenticated", graph.EdgeGuardedBy},
		{"interface_call:(zioapp.backend.service.NoteService).find", "method:(zioapp.backend.service.NoteServiceLive).find", graph.EdgeDispatchesTo},
		{"method:(zioapp.backend.service.NoteServiceLive).find", "interface_call:(zioapp.backend.db.NoteRepository).find", graph.EdgeCalls},
		{"interface_call:(zioapp.backend.db.NoteRepository).find", "method:(zioapp.backend.db.NoteRepositoryLive).find", graph.EdgeDispatchesTo},
		{"method:(zioapp.backend.service.NoteServiceLive).create", "func:zioapp.shared.validTitle", graph.EdgeCalls},
		{"type:zioapp.backend.service.NoteServiceLive", "type:zioapp.backend.service.NoteService", graph.EdgeImplements},
		{"type:zioapp.backend.db.NoteRepositoryLive", "type:zioapp.backend.db.NoteRepository", graph.EdgeImplements},
		{"type:zioapp.backend.service.NoteServiceLive", "type:zioapp.backend.db.NoteRepository", graph.EdgeUsesType},
	} {
		nbs, err := g.Neighbors(ctx, e.from, graph.Out, e.kind)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, nb := range nbs {
			found = found || nb.Node.ID == e.to
		}
		if !found {
			t.Errorf("no %s edge %s -> %s", e.kind, e.from, e.to)
		}
	}

	compareScalaRoutes(t, filepath.Join(dir, "routes.json"), all)
	if mux := nodes["route:GET /health"].Attrs["muxMiddleware"]; mux != "zioapp.backend.http.RouteSupport.requestLogging,zioapp.backend.http.RouteSupport.handleFailures" {
		t.Errorf("GET /health muxMiddleware %q", mux)
	}

	code, stdout, stderr := run(t, "query", dir, "search", "NoteServiceLive")
	if code != ExitOK {
		t.Fatalf("query search: exit code %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"method:(zioapp.backend.service.NoteServiceLive).create", "type:zioapp.backend.service.NoteServiceLive"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("search output missing %s:\n%s", want, stdout)
		}
	}

	compareScalaSinks(ctx, t, filepath.Join(dir, "sinks.json"), g, all)
	if n := nodes["sql_table:notes"]; n.Pos.File != "modules/backend/src/main/resources/db/migration/V1__notes.sql" || n.Attrs["inferred"] != "" {
		t.Errorf("notes table from the Flyway migration: %+v", n)
	}
	if !slices.Equal(p.MigrationDirs, []string{"modules/backend/src/main/resources/db/migration"}) {
		t.Errorf("migration dirs %v", p.MigrationDirs)
	}
	if er, err := views.ER(ctx, g, "", 1); err != nil || !strings.Contains(er.Mermaid, "notes") {
		t.Errorf("ER diagram: %v\n%s", err, er.Mermaid)
	}

	// The flow of a route goes through its handler and the repository down
	// to the query and its table.
	code, stdout, stderr = run(t, "query", dir, "flow", "GET /api/notes/{id}")
	if code != ExitOK {
		t.Fatalf("query flow: exit code %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"NoteRoutes.publicRoutes$2", "NoteServiceLive.find", "NoteRepositoryLive.find", "SELECT t0.id", "notes"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("flow output missing %s:\n%s", want, stdout)
		}
	}
}

// scalaSink is a sink of the Scala fixture and, for SQL, the tables it
// touches, as "table:op".
type scalaSink struct {
	Kind   string   `json:"kind"`
	Caller string   `json:"caller"`
	Callee string   `json:"callee"`
	Detail string   `json:"detail"`
	Tables []string `json:"tables,omitempty"`
}

// compareScalaSinks checks the sink nodes and the tables the SQL ones
// query against the golden sinks in path.
func compareScalaSinks(ctx context.Context, t *testing.T, path string, g *graph.Graph, nodes []graph.Node) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []scalaSink
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var got []scalaSink
	for _, n := range nodes {
		if !strings.HasPrefix(string(n.Kind), "sink.") {
			continue
		}
		if e := n.Attrs["parseError"]; e != "" {
			t.Errorf("%s: %s", n.ID, e)
		}
		s := scalaSink{Kind: string(n.Kind), Caller: n.Attrs["caller"], Callee: n.Attrs["callee"], Detail: n.Detail}
		nbs, err := g.Neighbors(ctx, n.ID, graph.Out, graph.EdgeQueries)
		if err != nil {
			t.Fatal(err)
		}
		for _, nb := range nbs {
			s.Tables = append(s.Tables, nb.Node.Name+":"+nb.Edge.Attrs["op"])
		}
		slices.Sort(s.Tables)
		got = append(got, s)
	}
	slices.SortFunc(got, func(a, b scalaSink) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Caller, b.Caller), cmp.Compare(a.Detail, b.Detail))
	})
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("sinks:\n%s\nwant:\n%s", gotJSON, wantJSON)
	}
}

// compareScalaRoutes checks the route nodes against the golden routes in
// path, like the Go analysis's compareRoutes.
func compareScalaRoutes(t *testing.T, path string, nodes []graph.Node) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []fixture.Route
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	got := map[string]fixture.Route{}
	for _, n := range nodes {
		if n.Kind != graph.KindRoute {
			continue
		}
		a := n.Attrs
		rt := fixture.Route{
			Method: a["method"], Pattern: a["pattern"], Handler: a["handler"], Access: a["access"],
			OptionalAuth: a["optionalAuth"] == "true", Conditional: a["conditional"] == "true",
		}
		if mw := a["middleware"]; mw != "" {
			rt.Middleware = strings.Split(mw, ",")
		}
		if n.Name != rt.Key() {
			t.Errorf("route %s: name %q", rt.Key(), n.Name)
		}
		got[rt.Key()] = rt
	}
	for _, w := range want {
		g, ok := got[w.Key()]
		if !ok {
			t.Errorf("missing route %s", w.Key())
			continue
		}
		delete(got, w.Key())
		if !reflect.DeepEqual(g, w) {
			t.Errorf("route %s:\n got %+v\nwant %+v", w.Key(), g, w)
		}
	}
	for key := range got {
		t.Errorf("unexpected route %s", key)
	}
}
