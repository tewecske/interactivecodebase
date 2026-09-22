package analysis

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

const (
	web      = "example.com/webapp/internal/web"
	notesPkg = "example.com/webapp/internal/notes"
	pg       = "example.com/webapp/internal/store/postgres"
)

var webappOnce = sync.OnceValues(func() (*Result, error) {
	return Analyze(context.Background(), fixture.WebappDir(), Options{})
})

// webapp returns the shared analysis of the webapp fixture. Tests must only
// read from it.
func webapp(t *testing.T) *Result {
	t.Helper()
	r, err := webappOnce()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMain(m *testing.M) {
	code := m.Run()
	if r, err := webappOnce(); err == nil {
		_ = r.Close()
	}
	os.Exit(code)
}

func TestWebappResult(t *testing.T) {
	r := webapp(t)
	if r.Module != "example.com/webapp" {
		t.Errorf("module = %q", r.Module)
	}
	if r.Stats.Packages != 11 || r.Stats.ModuleFunctions == 0 {
		t.Errorf("stats = %+v", r.Stats)
	}
}

func TestExpectedHandlersHaveNodes(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	for _, route := range exp.Routes {
		if !strings.HasPrefix(route.Handler, exp.Module) && !strings.HasPrefix(route.Handler, "(*"+exp.Module) {
			continue
		}
		kind := graph.KindFunc
		if strings.HasPrefix(route.Handler, "(") {
			kind = graph.KindMethod
		}
		if _, err := r.Graph.Node(t.Context(), graph.NodeID(kind, route.Handler)); err != nil {
			t.Errorf("route %s: %v", route.Key(), err)
		}
	}
}

func TestImplements(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	for _, impl := range exp.Implementations {
		from := graph.NodeID(graph.KindType, strings.TrimPrefix(impl.Concrete, "*"))
		to := graph.NodeID(graph.KindType, impl.Interface)
		nbs, err := r.Graph.Neighbors(t.Context(), from, graph.Out, graph.EdgeImplements)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(nbs, func(nb graph.Neighbor) bool { return nb.Node.ID == to })
		if i < 0 {
			t.Errorf("%s does not implement %s; neighbors %v", from, to, neighborIDs(nbs))
			continue
		}
		if pointer := strings.HasPrefix(impl.Concrete, "*"); (nbs[i].Edge.Attrs["pointer"] == "true") != pointer {
			t.Errorf("%s -> %s: pointer attr = %v, want %v", from, to, nbs[i].Edge.Attrs, pointer)
		}
	}
}

func TestInterfaceDispatch(t *testing.T) {
	r := webapp(t)
	tests := []struct{ iface, impl string }{
		{"(" + notesPkg + ".Repository).Create", "(*" + pg + ".NoteRepository).Create"},
		{"(" + notesPkg + ".Repository).Get", "(*" + pg + ".NoteRepository).Get"},
		{"(" + notesPkg + ".Repository).ListByOwner", "(*" + pg + ".NoteRepository).ListByOwner"},
		{"(" + notesPkg + ".Mailer).Send", "(*example.com/webapp/internal/mail.SMTPMailer).Send"},
		{"(" + web + ".sessionStore).UserBySession", "(*" + pg + ".SessionRepository).UserBySession"},
		{"(" + web + ".sessionStore).CreateSession", "(*" + pg + ".SessionRepository).CreateSession"},
		// A module interface may dispatch to standard-library implementations.
		{"(" + pg + ".scanner).Scan", "(*database/sql.Row).Scan"},
	}
	for _, tt := range tests {
		got := neighborIDs(neighbors(t, r, graph.NodeID(graph.KindInterfaceCall, tt.iface), graph.EdgeDispatchesTo))
		if want := graph.NodeID(graph.KindMethod, tt.impl); !slices.Contains(got, want) {
			t.Errorf("%s dispatches to %v, want %s", tt.iface, got, want)
		}
	}
}

func TestExternalInterfacesDispatchOnlyToModule(t *testing.T) {
	r := webapp(t)
	ifaces, err := r.Graph.Nodes(t.Context(), graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindInterfaceCall}})
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Package, r.Module) {
			continue
		}
		for _, nb := range neighbors(t, r, iface.ID, graph.EdgeDispatchesTo) {
			if !strings.HasPrefix(nb.Node.Package, r.Module) {
				t.Errorf("external %s dispatches to external %s", iface.ID, nb.Node.ID)
			}
		}
	}
}

func TestCallsInSourceOrder(t *testing.T) {
	r := webapp(t)
	got := neighborIDs(neighbors(t, r, graph.NodeID(graph.KindMethod, "(*"+web+".noteHandler).create"), graph.EdgeCalls))
	want := []string{
		graph.NodeID(graph.KindFunc, "net/http.Error"),
		graph.NodeID(graph.KindMethod, "(*"+web+".noteHandler).user"),
		graph.NodeID(graph.KindMethod, "(*"+notesPkg+".Service).Create"),
	}
	if !isSubsequence(want, got) {
		t.Errorf("create calls %v, want in order %v", got, want)
	}
}

func TestClosures(t *testing.T) {
	r := webapp(t)
	tests := []struct{ closure, callee string }{
		{web + ".weatherPage$1", graph.NodeID(graph.KindMethod, "(*example.com/webapp/internal/weather.Client).Forecast")},
		{web + ".New$1", graph.NodeID(graph.KindMethod, "(*"+web+".accountHandler).oauthCallback")},
		{web + ".requireAdmin$1", graph.NodeID(graph.KindMethod, "(*"+web+".Authenticator).Authenticate")},
	}
	for _, tt := range tests {
		got := neighborIDs(neighbors(t, r, graph.NodeID(graph.KindFunc, tt.closure), graph.EdgeCalls))
		if !slices.Contains(got, tt.callee) {
			t.Errorf("%s calls %v, want %s", tt.closure, got, tt.callee)
		}
	}
}

func TestPathFromHandlerToDatabase(t *testing.T) {
	r := webapp(t)
	paths, err := r.Graph.Paths(t.Context(),
		graph.NodeID(graph.KindMethod, "(*"+web+".noteHandler).create"),
		graph.NodeID(graph.KindMethod, "(*database/sql.Tx).ExecContext"),
		graph.PathOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no path from noteHandler.create to sql.Tx.ExecContext")
	}
	var hops []string
	for _, e := range paths[0] {
		hops = append(hops, e.To)
	}
	if !slices.Contains(hops, graph.NodeID(graph.KindInterfaceCall, "("+notesPkg+".Repository).Create")) {
		t.Errorf("shortest path skips the interface call: %v", hops)
	}
}

func TestNoiseExcluded(t *testing.T) {
	r := webapp(t)
	for _, pkg := range []string{"strconv", "strings", "fmt", "errors"} {
		nodes, err := r.Graph.Nodes(t.Context(), graph.NodeFilter{Package: pkg})
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) > 0 {
			t.Errorf("package %s has %d nodes, want none", pkg, len(nodes))
		}
	}
	ext, err := r.Graph.Node(t.Context(), graph.NodeID(graph.KindMethod, "(*database/sql.DB).QueryRowContext"))
	if err != nil {
		t.Fatal(err)
	}
	if ext.Attrs["external"] != "true" || ext.Pos.File != "" {
		t.Errorf("external node = %+v", ext)
	}
}

func TestPositions(t *testing.T) {
	r := webapp(t)
	n, err := r.Graph.Node(t.Context(), graph.NodeID(graph.KindMethod, "(*"+web+".noteHandler).create"))
	if err != nil {
		t.Fatal(err)
	}
	want := lineOf(t, filepath.Join(r.Dir, "internal/web/handlers.go"), "func (h *noteHandler) create(")
	if n.Pos.File != "internal/web/handlers.go" || n.Pos.StartLine != want || n.Pos.EndLine <= n.Pos.StartLine {
		t.Errorf("pos = %+v, want internal/web/handlers.go:%d spanning the body", n.Pos, want)
	}
	if n.Name != "(*noteHandler).create" || n.Package != web || !strings.HasPrefix(n.Detail, "func(w net/http.ResponseWriter") {
		t.Errorf("node = %+v", n)
	}
}

func TestExcludeOption(t *testing.T) {
	r := analyzeT(t, fixture.WebappDir(), Options{Exclude: []string{}})
	nodes, err := r.Graph.Nodes(t.Context(), graph.NodeFilter{Package: "strconv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Error("with no exclusions, strconv calls should be in the graph")
	}
}

func TestDeterministic(t *testing.T) {
	r2 := analyzeT(t, fixture.WebappDir(), Options{})
	var a, b bytes.Buffer
	if err := webapp(t).Graph.Export(t.Context(), &a); err != nil {
		t.Fatal(err)
	}
	if err := r2.Graph.Export(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two analyses of the same code produced different graphs")
	}
}

func TestAnalyzeReportsBuildErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/broken\n\ngo 1.26.0\n")
	write("main.go", "package main\n\nfunc main() { undefined() }\n")
	_, err := Analyze(t.Context(), dir, Options{})
	if err == nil || !strings.Contains(err.Error(), "does not build") || !strings.Contains(err.Error(), "undefined") {
		t.Errorf("err = %v", err)
	}
}

// analyzeT analyzes dir and closes the result when the test ends.
func analyzeT(t *testing.T, dir string, opts Options) *Result {
	t.Helper()
	r, err := Analyze(t.Context(), dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

// neighbors returns the outgoing neighbors of id over the given edge kinds.
func neighbors(t *testing.T, r *Result, id string, kinds ...graph.EdgeKind) []graph.Neighbor {
	t.Helper()
	nbs, err := r.Graph.Neighbors(t.Context(), id, graph.Out, kinds...)
	if err != nil {
		t.Fatal(err)
	}
	return nbs
}

func neighborIDs(nbs []graph.Neighbor) []string {
	var ids []string
	for _, nb := range nbs {
		ids = append(ids, nb.Node.ID)
	}
	return ids
}

// isSubsequence reports whether want appears in got in order.
func isSubsequence(want, got []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func lineOf(t *testing.T, file, prefix string) int {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, prefix) {
			return i + 1
		}
	}
	t.Fatalf("%s: no line starting with %q", file, prefix)
	return 0
}
