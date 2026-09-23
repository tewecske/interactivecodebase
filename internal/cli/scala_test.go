// Runs sbt and the Scala extractor, two JVMs; make test-scala runs this
// after building the extractor, without the race detector.

//go:build !race

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/scala"
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
		{"func:zioapp.backend.http.NoteRoutes.routes", "interface_call:(zioapp.backend.service.NoteService).find", graph.EdgeCalls},
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

	code, stdout, stderr := run(t, "query", dir, "search", "NoteServiceLive")
	if code != ExitOK {
		t.Fatalf("query search: exit code %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"method:(zioapp.backend.service.NoteServiceLive).create", "type:zioapp.backend.service.NoteServiceLive"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("search output missing %s:\n%s", want, stdout)
		}
	}
}
