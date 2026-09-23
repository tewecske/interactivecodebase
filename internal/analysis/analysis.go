// Package analysis loads a Go module, builds its SSA form and call graph, and
// writes functions, calls, interface dispatch and implementations into a
// code graph.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/sqlparse"
)

// DefaultExclude lists package paths whose functions are left out of the
// graph as call targets: they are everywhere and rarely explain a flow.
// An entry matches the package itself and everything below it.
var DefaultExclude = []string{
	"bytes", "cmp", "context", "errors", "fmt", "iter", "log", "maps", "math",
	"reflect", "runtime", "slices", "sort", "strconv", "strings", "sync",
	"time", "unicode", "unsafe", "internal", "go.opentelemetry.io",
}

// Options configures Analyze.
type Options struct {
	// Exclude lists package path prefixes to leave out as call targets.
	// Nil means DefaultExclude; an empty non-nil slice excludes nothing.
	Exclude []string
	// Tests includes the module's test files.
	Tests bool
	// ExtraSinks are sink rules added to DefaultSinks.
	ExtraSinks []SinkRule
	// AuthFuncs are go/ssa names of functions to treat as authentication
	// checks, in addition to request-taking functions named *authenticat*.
	AuthFuncs []string
	// MigrationDirs are searched for SQL migrations, relative to the module
	// root. Nil means sqlparse.DefaultMigrationDirs.
	MigrationDirs []string
}

// Result is an analyzed module.
type Result struct {
	// Module is the main module's path; Dir is its root directory. Source
	// positions in the graph are relative to Dir.
	Module string
	Dir    string
	Graph  *graph.Graph
	// Program, Packages and CallGraph are kept for later analysis passes.
	Program   *ssa.Program
	Packages  []*packages.Package
	CallGraph *callgraph.Graph
	// Routes are the HTTP routes registered in the module, in source order.
	Routes []Route
	// Sinks are the calls that leave the program: SQL, files, HTTP, SMTP,
	// processes and environment reads.
	Sinks []Sink
	// Schema is the database schema built from the migrations found in
	// MigrationDirs (the directories actually used).
	Schema        *sqlparse.Schema
	MigrationDirs []string
	// Templates are the parsed template files.
	Templates []*Template
	// Navigation lists the ways to get from one route to another.
	Navigation []Navigation
	Stats      Stats

	scopes    map[*ssa.Function]*methodScope
	funcOnce  sync.Once
	funcIndex map[string]*ssa.Function
}

// Stats describes an analysis run.
type Stats struct {
	Packages        int           `json:"packages"`        // packages matched by ./...
	Functions       int           `json:"functions"`       // functions in the whole program
	ModuleFunctions int           `json:"moduleFunctions"` // functions in the analyzed module
	Load            time.Duration `json:"load"`
	SSA             time.Duration `json:"ssa"`
	CallGraph       time.Duration `json:"callGraph"`
	Write           time.Duration `json:"write"`
}

// Total is the wall time of the run.
func (s Stats) Total() time.Duration { return s.Load + s.SSA + s.CallGraph + s.Write }

// Close releases the result's graph.
func (r *Result) Close() error { return r.Graph.Close() }

// Analyze analyzes the Go module containing dir, loading all packages
// under dir.
func Analyze(ctx context.Context, dir string, opts Options) (*Result, error) {
	if opts.Exclude == nil {
		opts.Exclude = DefaultExclude
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &Result{}
	sqlparse.Warm() // compile the SQL parser while packages load

	start := time.Now()
	pkgs, err := load(ctx, abs, opts.Tests)
	if err != nil {
		return nil, err
	}
	r.Packages = pkgs
	r.Stats.Packages = len(pkgs)
	if r.Module, r.Dir, err = mainModule(pkgs); err != nil {
		return nil, err
	}
	r.Stats.Load = time.Since(start)

	start = time.Now()
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	r.Program = prog
	r.Stats.SSA = time.Since(start)

	start = time.Now()
	funcs := ssautil.AllFunctions(prog)
	r.Stats.Functions = len(funcs)
	r.CallGraph = vta.CallGraph(funcs, cha.CallGraph(prog))
	r.CallGraph.DeleteSyntheticNodes()
	r.Stats.CallGraph = time.Since(start)

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start = time.Now()
	if r.Graph, err = graph.Open(ctx); err != nil {
		return nil, err
	}
	own := moduleFunctions(r, funcs)
	r.Routes = discoverRoutes(r, own)
	r.Sinks = detectSinks(r, own, append(slices.Clone(DefaultSinks), opts.ExtraSinks...))
	analyzeQueries(r.Sinks, sqlparse.Postgres)
	r.scopes = methodScopes(own)
	newAuthClassifier(r, own, r.scopes, opts.AuthFuncs).classify(r.Routes)
	buildPages(r, own)
	buildNavigation(r, own)
	if r.Schema, r.MigrationDirs, err = sqlparse.LoadMigrations(r.Dir, opts.MigrationDirs); err != nil {
		return nil, err
	}
	b := newBuilder(r, own, opts.Exclude)
	if err := r.Graph.Write(ctx, b.write); err != nil {
		return nil, errors.Join(err, r.Graph.Close())
	}
	r.Stats.ModuleFunctions = b.moduleFuncs
	r.Stats.Write = time.Since(start)
	return r, nil
}

func load(ctx context.Context, dir string, tests bool) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.LoadAllSyntax | packages.NeedModule,
		Dir:     dir,
		Tests:   tests,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("analysis: load %s: %w", dir, err)
	}
	var errs []error
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e)
		}
	})
	for _, e := range errs {
		if strings.Contains(e.Error(), "does not contain main module") {
			return nil, fmt.Errorf("analysis: %s is not inside a Go module (no go.mod found)", dir)
		}
	}
	if len(errs) > 0 {
		const show = 5
		if len(errs) > show {
			errs = append(errs[:show], fmt.Errorf("... and %d more", len(errs)-show))
		}
		return nil, fmt.Errorf("analysis: %s does not build:\n%w", dir, errors.Join(errs...))
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("analysis: no Go packages in %s", dir)
	}
	return pkgs, nil
}

func mainModule(pkgs []*packages.Package) (path, dir string, err error) {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Main {
			return p.Module.Path, p.Module.Dir, nil
		}
	}
	return "", "", errors.New("analysis: packages do not belong to a Go module")
}

// excluded reports whether pkgPath matches one of the prefixes.
func excluded(pkgPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if pkgPath == p || strings.HasPrefix(pkgPath, p+"/") {
			return true
		}
	}
	return false
}
