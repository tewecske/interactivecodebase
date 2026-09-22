package analysis

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestWebappRoutes(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	compareRoutes(t, exp.Routes, r.Routes)
	for _, rt := range r.Routes {
		if got := funcNames(rt.MuxMiddleware); got != strings.Join(exp.Middleware, ",") {
			t.Errorf("%s: mux middleware = %q, want %q", rt.Key(), got, exp.Middleware)
		}
	}
}

func TestRouteNodes(t *testing.T) {
	r := webapp(t)
	id := graph.NodeID(graph.KindRoute, "POST /{lang}/admin/reindex")
	n, err := r.Graph.Node(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"method":        "POST",
		"pattern":       "/{lang}/admin/reindex",
		"variants":      "/en/admin/reindex /de/admin/reindex",
		"handler":       "(*" + web + ".adminHandler).reindex",
		"middleware":    web + ".requireAdmin",
		"muxMiddleware": web + ".withRequestID",
	}
	for k, v := range want {
		if n.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, n.Attrs[k], v)
		}
	}
	if n.Pos.File != "internal/web/router.go" || n.Pos.StartLine == 0 {
		t.Errorf("pos = %+v", n.Pos)
	}
	got := neighborIDs(neighbors(t, r, id, graph.EdgeHandledBy))
	if want := []string{graph.NodeID(graph.KindMethod, "(*"+web+".adminHandler).reindex")}; !slices.Equal(got, want) {
		t.Errorf("handled_by = %v, want %v", got, want)
	}

	static, err := r.Graph.Node(t.Context(), graph.NodeID(graph.KindRoute, "GET /static/{path...}"))
	if err != nil {
		t.Fatal(err)
	}
	if static.Attrs["static"] != "true" || static.Attrs["handler"] != "net/http.FileServer" {
		t.Errorf("static route attrs = %v", static.Attrs)
	}
}

func TestSplitAndCollapse(t *testing.T) {
	tests := []struct{ in, method, path, collapsed string }{
		{"GET /en/notes", "GET", "/en/notes", "/{lang}/notes"},
		{"POST  /hu", "POST", "/hu", "/{lang}"},
		{"/healthz", "ANY", "/healthz", ""},
		{"GET /pt-BR/x", "GET", "/pt-BR/x", "/{lang}/x"},
		{"GET /api/x", "GET", "/api/x", ""},
	}
	for _, tt := range tests {
		m, p := splitPattern(tt.in)
		c, ok := collapseLang(p)
		if !ok {
			c = ""
		}
		if m != tt.method || p != tt.path || c != tt.collapsed {
			t.Errorf("%q: got %q %q %q, want %q %q %q", tt.in, m, p, c, tt.method, tt.path, tt.collapsed)
		}
	}
}

// compareRoutes checks discovered routes against expectations on every
// field route discovery owns (not access, which auth classification sets).
func compareRoutes(t *testing.T, want []fixture.Route, got []Route) {
	t.Helper()
	byKey := map[string]Route{}
	for _, rt := range got {
		byKey[rt.Key()] = rt
	}
	for _, w := range want {
		rt, ok := byKey[w.Key()]
		if !ok {
			t.Errorf("missing route %s", w.Key())
			continue
		}
		delete(byKey, w.Key())
		var diffs []string
		check := func(field string, got, want any) {
			if fmt.Sprint(got) != fmt.Sprint(want) {
				diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", field, got, want))
			}
		}
		check("handler", rt.HandlerName(), w.Handler)
		check("variants", rt.Variants, w.Variants)
		check("middleware", middlewareNames(rt.Middleware), w.Middleware)
		check("conditional", rt.Conditional(), w.Conditional)
		check("static", rt.Static, w.Static)
		if len(diffs) > 0 {
			t.Errorf("route %s: %s", w.Key(), strings.Join(diffs, "; "))
		}
	}
	for key := range byKey {
		t.Errorf("unexpected route %s", key)
	}
	if len(want) != len(got) {
		t.Logf("%d routes found, %d expected", len(got), len(want))
	}
}

func middlewareNames(fns []*ssa.Function) []string {
	var names []string
	for _, fn := range fns {
		names = append(names, origin(fn).String())
	}
	return names
}
