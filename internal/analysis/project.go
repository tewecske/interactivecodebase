package analysis

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// Languages of a Project.
const (
	LangGo    = "go"
	LangScala = "scala"
)

// Project is an analyzed code base in any language: what the API, UI and
// MCP server work from. Everything they show comes from Graph; Go, when
// set, adds what needs Go type information (the detailed types diagram,
// identifier references, gopls).
type Project struct {
	Lang string
	// Module names the project (a Go module path); Dir is its root. Source
	// positions in the graph are relative to Dir.
	Module        string
	Dir           string
	Graph         *graph.Graph
	MigrationDirs []string
	Stats         Stats
	// Go is the Go analysis behind the graph, or nil for a graph imported
	// from another language's extractor.
	Go *Result
}

// Project returns the language-neutral view of r, sharing its graph.
func (r *Result) Project() *Project {
	return &Project{
		Lang: LangGo, Module: r.Module, Dir: r.Dir, Graph: r.Graph,
		MigrationDirs: r.MigrationDirs, Stats: r.Stats, Go: r,
	}
}

// Close releases the project's graph.
func (p *Project) Close() error { return p.Graph.Close() }

// ImportProject builds a project from a graph in graph.Export's format
// (docs/graph.schema.json), as written by an extractor for lang. Stats.Write
// is the time the import took.
func ImportProject(ctx context.Context, lang, module, dir string, r io.Reader) (*Project, error) {
	start := time.Now()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	g, err := graph.Open(ctx)
	if err != nil {
		return nil, err
	}
	if err := g.Import(ctx, r); err != nil {
		return nil, errors.Join(err, g.Close())
	}
	p := &Project{Lang: lang, Module: module, Dir: abs, Graph: g}
	p.Stats.Write = time.Since(start)
	return p, nil
}
