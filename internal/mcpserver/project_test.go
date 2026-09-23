package mcpserver

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// imported is the webapp graph exported and imported again: a project
// without Go analysis, as an extractor for another language produces.
var imported = sync.OnceValues(func() (*analysis.Project, error) {
	r, err := webapp()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := r.Graph.Export(context.Background(), &buf); err != nil {
		return nil, err
	}
	return analysis.ImportProject(context.Background(), "scala", r.Module, r.Dir, &buf)
})

// TestImportedProject checks that the tools answer the same from an
// imported graph as from the Go analysis.
func TestImportedProject(t *testing.T) {
	p, err := imported()
	if err != nil {
		t.Fatal(err)
	}
	goSession, importedSession := connect(t, nil), connectTo(t, p, nil)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"list_routes", nil},
		{"list_routes", map[string]any{"access": "admin"}},
		{"list_entry_points", nil},
		{"get_route", map[string]any{"route": "GET /{lang}/notes/{id}"}},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes"}},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes", "format": "mermaid"}},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes/{id}/share", "prune": "module"}},
		{"get_node", map[string]any{"id": "noteHandler).create"}},
		{"get_source", map[string]any{"file": "internal/web/router.go", "start": 1, "end": 5}},
		{"find_callers", map[string]any{"symbol": "Authenticator).Authenticate"}},
		{"find_callees", map[string]any{"symbol": "(*example.com/webapp/internal/store/postgres.NoteRepository).Create"}},
		{"find_paths", map[string]any{"from": "route:POST /{lang}/notes", "to": "sql_table:audit_events"}},
		{"routes_touching_table", map[string]any{"table": "notes"}},
		{"list_tables", nil},
		{"get_table", map[string]any{"name": "notes"}},
		{"get_table", map[string]any{"name": "notes", "format": "mermaid"}},
		{"search", map[string]any{"query": "notes"}},
		{"search", map[string]any{"query": "SMTPMailer", "kind": "type"}},
	} {
		want, wantErr := call(t, goSession, c.tool, c.args)
		got, gotErr := call(t, importedSession, c.tool, c.args)
		if wantErr {
			t.Errorf("%s %v: Go project error: %s", c.tool, c.args, want)
		}
		if got != want || gotErr != wantErr {
			t.Errorf("%s %v: imported project differs:\n%s\nwant:\n%s", c.tool, c.args, got, want)
		}
	}
	if err := needGo(p); err == nil || !strings.Contains(err.Error(), "this is a scala project, not Go") {
		t.Errorf("lsp tools on an imported project: %v", err)
	}
}

// TestFrontendPages checks that list_routes lists a single-page app's
// frontend pages after the routes and that get_route describes one.
func TestFrontendPages(t *testing.T) {
	g, err := graph.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	notes := graph.Node{ID: "page:/app/notes", Kind: graph.KindPage, Name: "/app/notes", Pos: graph.Pos{File: "AppRouter.scala", StartLine: 20},
		Attrs: map[string]string{"method": "GET", "pattern": "/app/notes", "access": "public", "handler": "app.NotesPage.render"}}
	route := graph.Node{ID: "route:GET /api/notes", Kind: graph.KindRoute, Name: "GET /api/notes",
		Attrs: map[string]string{"method": "GET", "pattern": "/api/notes", "access": "public", "handler": "app.NoteRoutes.list"}}
	request := graph.Node{ID: "htmx_call:NoteApi.scala:9:53", Kind: graph.KindHTMXCall, Name: route.Name, Pos: graph.Pos{File: "NoteApi.scala", StartLine: 9},
		Attrs: map[string]string{"method": "GET", "url": "/api/notes", "trigger": "fetch", "target": route.Name}}
	err = g.Write(t.Context(), func(w *graph.Writer) error {
		for _, n := range []graph.Node{notes, route, request} {
			if err := w.AddNode(n); err != nil {
				return err
			}
		}
		if err := w.AddEdge(graph.Edge{From: notes.ID, To: request.ID, Kind: graph.EdgeRequests}); err != nil {
			return err
		}
		return w.AddEdge(graph.Edge{From: request.ID, To: route.ID, Kind: graph.EdgeHandledBy})
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectTo(t, &analysis.Project{Lang: analysis.LangScala, Graph: g}, nil)
	out, isErr := call(t, session, "list_routes", nil)
	if isErr || !strings.Contains(out, "1 routes") || !strings.Contains(out, "1 frontend pages (ID, view, position)\npage:/app/notes\tapp.NotesPage.render\tAppRouter.scala:20") {
		t.Errorf("list_routes:\n%s", out)
	}
	if out, _ := call(t, session, "list_routes", map[string]any{"method": "POST"}); strings.Contains(out, "frontend pages") {
		t.Errorf("list_routes for POST lists pages:\n%s", out)
	}
	out, isErr = call(t, session, "get_route", map[string]any{"route": "page:/app/notes"})
	if isErr || !strings.Contains(out, "requests from its page:\n  GET /api/notes\tfetch\tNoteApi.scala:9") {
		t.Errorf("get_route of a page:\n%s", out)
	}
}
