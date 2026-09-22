package analysis

import (
	"slices"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestWebappNavigation(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	for _, p := range exp.Pages {
		got := navTargets(t, r, p.Route, "")
		for _, want := range p.NavigatesTo {
			if !slices.Contains(got, want) {
				t.Errorf("%s: no navigation to %s; got %v", p.Route, want, got)
			}
		}
	}
	checkNavigation(t, r, []navCase{
		{"POST /{lang}/sign-in", "GET /{lang}/notes", "redirect"},
		{"GET /{lang}/notes", "GET /{lang}/sign-in", "redirect"}, // unauthenticated
		{"POST /{lang}/notes", "GET /{lang}/notes/{id}", "hx-redirect"},
		{"GET /{lang}/notes", "POST /{lang}/notes", "form"},
		{"GET /{$}", "GET /{lang}/sign-in", "redirect"},
	})
	// The layout's nav links are attributed to the "head" template.
	for _, nb := range neighbors(t, r, graph.NodeID(graph.KindRoute, "GET /{lang}/weather"), graph.EdgeNavigatesTo) {
		if nb.Node.ID == graph.NodeID(graph.KindRoute, "GET /{lang}/notes") && nb.Edge.Attrs["template"] != "head" {
			t.Errorf("weather -> notes link template = %q, want head", nb.Edge.Attrs["template"])
		}
	}
}

type navCase struct{ from, to, trigger string }

func checkNavigation(t *testing.T, r *Result, cases []navCase) {
	t.Helper()
	for _, c := range cases {
		if got := navTargets(t, r, c.from, c.trigger); !slices.Contains(got, c.to) {
			t.Errorf("%s: no %s to %s; got %v", c.from, c.trigger, c.to, got)
		}
	}
}

// navTargets lists the routes a route navigates to, by one trigger or all.
func navTargets(t *testing.T, r *Result, from, trigger string) []string {
	t.Helper()
	var out []string
	for _, nb := range neighbors(t, r, graph.NodeID(graph.KindRoute, from), graph.EdgeNavigatesTo) {
		if trigger == "" || nb.Edge.Attrs["trigger"] == trigger {
			out = append(out, nb.Node.Name)
		}
		if nb.Edge.Pos.File == "" || nb.Edge.Pos.StartLine == 0 {
			t.Errorf("%s -> %s: navigation edge has no source position", from, nb.Node.Name)
		}
	}
	return out
}
