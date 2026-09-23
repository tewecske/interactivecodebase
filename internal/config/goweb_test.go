//go:build goweb

package config

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// TestGowebExampleConfig analyzes goweb with the committed example config:
// the configured OAuth providers give the OAuth routes concrete paths, and
// the configured interface sinks show where mail and OAuth leave.
func TestGowebExampleConfig(t *testing.T) {
	dir, exp := fixture.Goweb(t)
	c, _, err := Load(dir, filepath.Join(fixture.RepoRoot(), "examples", "goweb", FileName))
	if err != nil {
		t.Fatal(err)
	}
	r, err := analysis.Analyze(t.Context(), dir, c.Options())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	var keys []string
	for _, rt := range r.Routes {
		keys = append(keys, rt.Key())
	}
	for _, want := range []string{"GET /{lang}/oauth/github", "GET /{lang}/oauth/google/callback"} {
		if !slices.Contains(keys, want) {
			t.Errorf("missing route %s", want)
		}
	}
	if slices.Contains(keys, "GET /{lang}/oauth/{?}") {
		t.Error("OAuth route still unresolved")
	}
	if len(r.Routes) != len(exp.Routes)+2 {
		t.Errorf("%d routes, want the %d expected with two OAuth providers each", len(r.Routes), len(exp.Routes))
	}
	labels := map[graph.NodeKind][]string{}
	for _, s := range r.Sinks {
		if s.Label != "" {
			labels[s.Kind] = append(labels[s.Kind], s.Caller.String())
		}
	}
	if len(labels[graph.KindSinkHTTP]) == 0 || len(labels[graph.KindSinkSMTP]) == 0 {
		t.Errorf("labeled sinks = %v, want OAuth (http) and mail (smtp)", labels)
	}
}
