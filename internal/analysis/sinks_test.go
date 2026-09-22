package analysis

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestExpectedSinks(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	checkExpectedSinks(t, r, exp.Sinks)
}

// checkExpectedSinks verifies each expected sink is called by its function.
func checkExpectedSinks(t *testing.T, r *Result, want []fixture.Sink) {
	t.Helper()
	for _, s := range want {
		i := slices.IndexFunc(r.Sinks, func(got Sink) bool {
			return string(got.Kind) == s.Kind && origin(got.Caller).String() == s.In
		})
		if i < 0 {
			t.Errorf("no %s sink in %s", s.Kind, s.In)
		}
	}
}

func TestSinkDetails(t *testing.T) {
	r := webapp(t)
	tests := []struct {
		kind   graph.NodeKind
		caller string
		detail string
	}{
		{graph.KindSinkSQL, "(*" + pg + ".NoteRepository).Get", "SELECT id, owner_id, title, body, created_at FROM notes WHERE id = $1"},
		{graph.KindSinkSQL, "(*" + pg + ".NoteRepository).Create", "INSERT INTO audit_events (note_id, action) VALUES ($1, 'create')"},
		{graph.KindSinkHTTP, "(*example.com/webapp/internal/weather.Client).Forecast", "https://api.weather.example/v1/forecast?city={?}"},
		{graph.KindSinkExec, "example.com/webapp/internal/export.WriteCSV", "gzip"},
		{graph.KindSinkFile, "example.com/webapp/internal/export.WriteCSV", "{?}/notes.csv"},
		{graph.KindSinkFile, web + ".newRenderer", "*.html"},
		{graph.KindSinkEnv, "example.com/webapp/internal/config.Load", "WEBAPP_DATABASE_URL"},
		{graph.KindSinkEnv, "example.com/webapp/cmd/server.main", "os.Getenv passed as a function value"},
	}
	for _, tt := range tests {
		found := false
		for _, nb := range neighbors(t, r, FuncID(findFunc(t, r, tt.caller)), graph.EdgeCalls) {
			if nb.Node.Kind == tt.kind && strings.Contains(nb.Node.Detail, tt.detail) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no %s sink with detail %q", tt.caller, tt.kind, tt.detail)
		}
	}
}

func TestOnlyRuleCallsAreSinks(t *testing.T) {
	// Tx.Rollback (in a deferred closure) and Scan (through the scanner
	// interface) touch the database but are not query sites, so the rules
	// leave them out; every query site resolves to its SQL text.
	r := webapp(t)
	for _, s := range r.Sinks {
		if strings.HasSuffix(s.Callee, ".Rollback") || strings.HasSuffix(s.Callee, ".Scan") {
			t.Errorf("unexpected sink %s in %s", s.Callee, s.Caller)
		}
	}
	var resolved, sql int
	for _, s := range r.Sinks {
		if s.Kind == graph.KindSinkSQL {
			sql++
			if s.Resolved() {
				resolved++
			}
		}
	}
	if sql != 6 || resolved != 6 {
		t.Errorf("sql sinks = %d (resolved %d), want 6 (6)", sql, resolved)
	}
}

func TestEvaluatorModels(t *testing.T) {
	src := `package p

import (
	"fmt"
	"path/filepath"
)

const table = "notes"

func sprintf(id int) string { return fmt.Sprintf("SELECT * FROM %s WHERE id = %d -- 100%%", table, id) }
func join(dir string) string { return filepath.Join("/var", "app", table+".csv") }
func unknown(s string) string { return s + "!" }
`
	prog, pkg := buildSSA(t, src)
	ev := newEvaluator(cha.CallGraph(prog), "example.com/p")
	tests := map[string][]string{
		"sprintf": {"SELECT * FROM notes WHERE id = {?} -- 100%"},
		"join":    {"/var/app/notes.csv"},
		"unknown": {"{?}!"},
	}
	for name, want := range tests {
		fn := pkg.Func(name)
		got := ev.strings(returns(fn)[0])
		if !slices.Equal(got, want) {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func buildSSA(t *testing.T, src string) (*ssa.Program, *ssa.Package) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := types.NewPackage("example.com/p", "p")
	conf := &types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	ssaPkg, _, err := ssautil.BuildPackage(conf, fset, pkg, []*ast.File{f}, ssa.SanityCheckFunctions)
	if err != nil {
		t.Fatal(err)
	}
	return ssaPkg.Prog, ssaPkg
}

func findFunc(t *testing.T, r *Result, name string) *ssa.Function {
	t.Helper()
	for fn := range ssautil.AllFunctions(r.Program) {
		if fn.String() == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}
