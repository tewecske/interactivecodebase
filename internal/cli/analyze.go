package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/config"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func runAnalyze(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "analyze", "icb analyze [flags] <dir>")
	asJSON := fs.Bool("json", false, "print the summary as JSON")
	cfgPath := configFlag(fs)
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	opts, err := loadOptions(fs.Arg(0), *cfgPath)
	if err != nil {
		return err
	}
	p, release, err := openAnalysis(ctx, fs.Arg(0), opts)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	counts, err := p.Graph.Counts(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(e, summaryJSON(p, counts))
	}
	s := p.Stats
	fmt.Fprintf(e.stdout, "module %s (%s)\n", p.Module, p.Dir)
	fmt.Fprintf(e.stdout, "%d packages, %d functions in the module, %d in the whole program\n",
		s.Packages, s.ModuleFunctions, s.Functions)
	fmt.Fprintf(e.stdout, "load %v, ssa %v, call graph %v, graph %v, total %v\n\n",
		round(s.Load), round(s.SSA), round(s.CallGraph), round(s.Write), round(s.Total()))
	fmt.Fprintf(e.stdout, "nodes: %s\n", formatCounts(counts.Nodes))
	fmt.Fprintf(e.stdout, "edges: %s\n", formatCounts(counts.Edges))
	return nil
}

// configFlag adds -config to a command's flags.
func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "config file (default: "+config.FileName+" in <dir>, if present)")
}

// loadOptions reads the analysis options from the config for dir.
func loadOptions(dir, path string) (analysis.Options, error) {
	c, _, err := config.Load(dir, path)
	if err != nil {
		return analysis.Options{}, err
	}
	return c.Options(), nil
}

// openAnalysis analyzes dir; the caller must call release when done. Tests
// replace it to share one analysis across commands.
var openAnalysis = func(ctx context.Context, dir string, opts analysis.Options) (p *analysis.Project, release func() error, err error) {
	if err := checkDir(dir); err != nil {
		return nil, nil, err
	}
	r, err := analysis.Analyze(ctx, dir, opts)
	if err != nil {
		return nil, nil, err
	}
	return r.Project(), r.Close, nil
}

type summary struct {
	Module string       `json:"module"`
	Dir    string       `json:"dir"`
	Stats  statsJSON    `json:"stats"`
	Counts graph.Counts `json:"counts"`
}

type statsJSON struct {
	Packages        int   `json:"packages"`
	Functions       int   `json:"functions"`
	ModuleFunctions int   `json:"moduleFunctions"`
	LoadMS          int64 `json:"loadMs"`
	SSAMS           int64 `json:"ssaMs"`
	CallGraphMS     int64 `json:"callGraphMs"`
	WriteMS         int64 `json:"writeMs"`
	TotalMS         int64 `json:"totalMs"`
}

func summaryJSON(p *analysis.Project, counts graph.Counts) summary {
	s := p.Stats
	return summary{
		Module: p.Module,
		Dir:    p.Dir,
		Stats: statsJSON{
			Packages: s.Packages, Functions: s.Functions, ModuleFunctions: s.ModuleFunctions,
			LoadMS: s.Load.Milliseconds(), SSAMS: s.SSA.Milliseconds(), CallGraphMS: s.CallGraph.Milliseconds(),
			WriteMS: s.Write.Milliseconds(), TotalMS: s.Total().Milliseconds(),
		},
		Counts: counts,
	}
}

// formatCounts renders counts largest first: "calls 3030, dispatches_to 156".
func formatCounts[K ~string](counts map[K]int) string {
	keys := slices.Collect(maps.Keys(counts))
	slices.SortFunc(keys, func(a, b K) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, counts[k])
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func round(d time.Duration) time.Duration { return d.Round(time.Millisecond) }

func writeJSON(e *env, v any) error {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
