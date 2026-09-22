package analysis

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestWebappPages(t *testing.T) {
	r := webapp(t)
	_, exp := fixture.Webapp(t)
	checkPages(t, r, exp.Pages)
	for _, rt := range r.Routes {
		if rt.Method != "GET" && rt.Page != nil {
			t.Errorf("%s should not render a page", rt.Key())
		}
	}
}

// checkPages compares each expected page's template, requests and assets
// with the graph.
func checkPages(t *testing.T, r *Result, pages []fixture.Page) {
	t.Helper()
	for _, p := range pages {
		id := graph.NodeID(graph.KindRoute, p.Route)
		var templates []string
		for _, nb := range neighbors(t, r, id, graph.EdgeRenders) {
			templates = append(templates, nb.Node.Name)
		}
		if !slices.Contains(templates, p.Template) {
			t.Errorf("%s renders %v, want %s", p.Route, templates, p.Template)
		}
		var requests []string
		for _, nb := range neighbors(t, r, id, graph.EdgeRequests) {
			requests = append(requests, nb.Node.Attrs["target"]+" "+nb.Node.Attrs["trigger"])
		}
		for _, req := range p.Requests {
			if want := req.Method + " " + req.URL + " " + req.Trigger; !slices.Contains(requests, want) {
				t.Errorf("%s: missing request %q; got %v", p.Route, want, requests)
			}
		}
		var assets []string
		for _, nb := range neighbors(t, r, id, graph.EdgeLoads) {
			assets = append(assets, nb.Node.Name)
			if nb.Node.Attrs["file"] == "" {
				t.Errorf("%s: asset %s has no file", p.Route, nb.Node.Name)
			}
		}
		if !sameSet(assets, p.Assets) {
			t.Errorf("%s: assets %v, want %v", p.Route, assets, p.Assets)
		}
	}
}

func TestRequestHandledByTargetRoute(t *testing.T) {
	r := webapp(t)
	for _, nb := range neighbors(t, r, graph.NodeID(graph.KindRoute, "GET /{lang}/notes/{id}"), graph.EdgeRequests) {
		targets := neighborIDs(neighbors(t, r, nb.Node.ID, graph.EdgeHandledBy))
		if want := graph.NodeID(graph.KindRoute, "POST /{lang}/notes/{id}/share"); !slices.Equal(targets, []string{want}) {
			t.Errorf("%s handled by %v, want %s", nb.Node.ID, targets, want)
		}
		if nb.Node.Pos.File != filepath.ToSlash("templates/note.html") || nb.Node.Pos.StartLine == 0 {
			t.Errorf("request pos = %+v", nb.Node.Pos)
		}
	}
}

func TestMatchRoute(t *testing.T) {
	routes := []Route{
		{Method: "GET", Pattern: "/"},
		{Method: "GET", Pattern: "/{language}"},
		{Method: "GET", Pattern: "/{language}/{path...}"},
		{Method: "GET", Pattern: "/{lang}/notes", Variants: []string{"/en/notes", "/de/notes"}},
		{Method: "POST", Pattern: "/{lang}/notes"},
		{Method: "GET", Pattern: "/{lang}/notes/{id}"},
		{Method: "POST", Pattern: "/{lang}/notes/{id}/share"},
		{Method: "GET", Pattern: "/static/{path...}"},
	}
	tests := []struct {
		method string
		values []string
		want   string
	}{
		{"GET", []string{"/{?}/notes"}, "GET /{lang}/notes"},
		{"POST", []string{"/{?}/notes"}, "POST /{lang}/notes"},
		{"GET", []string{"/en/notes?page=2#top"}, "GET /{lang}/notes"},
		{"GET", []string{"/{?}/notes/{?}"}, "GET /{lang}/notes/{id}"},
		{"POST", []string{"{?}/share"}, "POST /{lang}/notes/{id}/share"},
		{"GET", []string{"{?}/notes"}, "GET /{lang}/notes"},
		{"GET", []string{"/static/app.css"}, "GET /static/{path...}"},
		{"GET", []string{"/"}, "GET /"},
		{"GET", []string{"{?}"}, ""},
		{"GET", []string{"{?}/{?}"}, ""},
		{"GET", []string{"https://example.com/notes"}, ""},
		{"DELETE", []string{"/{?}/notes"}, ""},
	}
	for _, tt := range tests {
		if got := matchRoute(routes, tt.method, tt.values); got != tt.want {
			t.Errorf("%s %v = %q, want %q", tt.method, tt.values, got, tt.want)
		}
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
