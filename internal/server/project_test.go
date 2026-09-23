package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
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

func serve(t *testing.T, p *analysis.Project, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	New(p, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestImportedProject checks that everything the graph holds is served
// the same from an imported graph as from the Go analysis.
func TestImportedProject(t *testing.T) {
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	p, err := imported()
	if err != nil {
		t.Fatal(err)
	}
	if p.Go != nil {
		t.Fatal("imported project has Go analysis")
	}
	for _, path := range []string{
		"/api/routes",
		"/api/routes?access=admin",
		"/api/entries",
		"/api/node?id=" + q("method:(*example.com/webapp/internal/web.noteHandler).create"),
		"/api/page?route=" + q("GET /{lang}/notes"),
		"/api/flow?route=" + q("POST /{lang}/notes"),
		"/api/flow?route=" + q("POST /{lang}/notes") + "&prune=module",
		"/api/paths?from=" + q("route:POST /{lang}/notes") + "&to=" + q("sql_table:audit_events"),
		"/api/source?file=internal/web/router.go&start=1&end=3",
		"/api/tables",
		"/api/table?name=notes",
		"/api/table?name=users",
		"/api/search?q=notes",
		"/api/search?q=SMTPMailer&kind=type",
		"/api/search?kind=sink.sql",
		"/api/diagrams/sitemap",
		"/api/diagrams/sitemap?get=1&access=authenticated,admin&q=notes",
		"/api/diagrams/flow?route=" + q("POST /{lang}/notes"),
		"/api/diagrams/flow?route=" + q("POST /{lang}/notes/{id}/share"),
		"/api/diagrams/er",
		"/api/diagrams/er?table=notes",
	} {
		want, got := serve(t, r.Project(), path), serve(t, p, path)
		if want.Code != 200 {
			t.Errorf("%s: Go project status %d", path, want.Code)
		}
		if got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Errorf("%s: imported project differs: %d\n%s\nwant %d\n%s", path, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
	}

	// What needs Go type information answers 501.
	for _, path := range []string{
		"/api/refs?file=internal/web/handlers.go",
		"/api/lsp/hover?file=internal/web/auth.go&line=1&col=1",
	} {
		if rec := serve(t, p, path); rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "Go type information") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestImportedTypesDiagram draws the types diagram from the graph alone:
// the Go graph has type nodes for interfaces and their implementations.
func TestImportedTypesDiagram(t *testing.T) {
	p, err := imported()
	if err != nil {
		t.Fatal(err)
	}
	rec := serve(t, p, "/api/diagrams/types?id="+q("type:example.com/webapp/internal/notes.Repository"))
	var d mermaid.Diagram
	if rec.Code != 200 || decode(rec, &d) != nil {
		t.Fatalf("types diagram %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"<<interface>>", "..|> notes_Repository", "click notes_Repository call icbClick()"} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("types diagram lacks %q:\n%s", want, d.Mermaid)
		}
	}
	if rec := serve(t, p, "/api/diagrams/types?id=nope"); rec.Code != 404 {
		t.Errorf("unknown node: %d", rec.Code)
	}
}

// TestGraphTypesDiagram covers uses_type edges, which an extractor writes
// for a function's signature and a type's fields.
func TestGraphTypesDiagram(t *testing.T) {
	g, err := graph.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	typ := func(pkg, name, kind string) graph.Node {
		return graph.Node{ID: graph.NodeID(graph.KindType, pkg+"."+name), Kind: graph.KindType, Name: name, Package: pkg, Detail: kind}
	}
	service := typ("com.example.notes", "NoteService", "class")
	repo := typ("com.example.notes", "NoteRepository", "trait")
	quill := typ("com.example.db", "QuillNoteRepository", "class")
	note := typ("com.example.notes", "Note", "class")
	create := graph.Node{ID: "method:com.example.notes.NoteService.create", Kind: graph.KindMethod, Name: "create"}
	err = g.Write(t.Context(), func(w *graph.Writer) error {
		for _, n := range []graph.Node{service, repo, quill, note, create} {
			if err := w.AddNode(n); err != nil {
				return err
			}
		}
		for _, e := range []graph.Edge{
			{From: create.ID, To: service.ID, Kind: graph.EdgeUsesType},
			{From: create.ID, To: note.ID, Kind: graph.EdgeUsesType},
			{From: service.ID, To: repo.ID, Kind: graph.EdgeUsesType},
			{From: quill.ID, To: repo.ID, Kind: graph.EdgeImplements},
		} {
			if err := w.AddEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &analysis.Project{Lang: analysis.LangScala, Graph: g}
	rec := serve(t, p, "/api/diagrams/types?id="+q(create.ID))
	var d mermaid.Diagram
	if rec.Code != 200 || decode(rec, &d) != nil {
		t.Fatalf("types diagram %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"class notes_NoteService {", "class notes_Note {", "class db_QuillNoteRepository {",
		"<<interface>>", "notes_NoteService --> notes_NoteRepository", "db_QuillNoteRepository ..|> notes_NoteRepository",
	} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("types diagram lacks %q:\n%s", want, d.Mermaid)
		}
	}
}

func decode(rec *httptest.ResponseRecorder, v any) error {
	return json.Unmarshal(rec.Body.Bytes(), v)
}

// TestFrontendPages covers the page nodes an extractor writes for a
// single-page app's frontend: listed with the routes on request, drawn in
// the site map with their navigation, and drilled into by ID.
func TestFrontendPages(t *testing.T) {
	g, err := graph.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	page := func(path, view string) graph.Node {
		return graph.Node{ID: graph.NodeID(graph.KindPage, path), Kind: graph.KindPage, Name: path, Attrs: map[string]string{
			"method": "GET", "pattern": path, "access": "public", "handler": view,
		}}
	}
	notes := page("/app/notes", "app.NotesPage.render")
	detail := page("/app/notes/{noteId}", "app.NoteDetailPage.render")
	route := graph.Node{ID: "route:GET /api/notes/{id}", Kind: graph.KindRoute, Name: "GET /api/notes/{id}", Attrs: map[string]string{
		"method": "GET", "pattern": "/api/notes/{id}", "access": "public", "handler": "app.NoteRoutes.get",
	}}
	request := graph.Node{ID: "htmx_call:NoteApi.scala:11:60", Kind: graph.KindHTMXCall, Name: route.Name, Attrs: map[string]string{
		"method": "GET", "url": "/api/notes/{noteId}", "trigger": "fetch", "target": route.Name,
	}}
	view := graph.Node{ID: "func:app.NoteDetailPage.render", Kind: graph.KindFunc, Name: "NoteDetailPage.render"}
	err = g.Write(t.Context(), func(w *graph.Writer) error {
		for _, n := range []graph.Node{notes, detail, route, request, view} {
			if err := w.AddNode(n); err != nil {
				return err
			}
		}
		for _, e := range []graph.Edge{
			{From: detail.ID, To: view.ID, Kind: graph.EdgeHandledBy},
			{From: detail.ID, To: request.ID, Kind: graph.EdgeRequests},
			{From: request.ID, To: route.ID, Kind: graph.EdgeHandledBy},
			{From: notes.ID, To: detail.ID, Kind: graph.EdgeNavigatesTo, Attrs: map[string]string{"trigger": "link"}},
			{From: detail.ID, To: notes.ID, Kind: graph.EdgeNavigatesTo, Attrs: map[string]string{"trigger": "navigate"}},
		} {
			if err := w.AddEdge(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &analysis.Project{Lang: analysis.LangScala, Graph: g}

	var routes []RouteInfo
	if rec := serve(t, p, "/api/routes"); rec.Code != 200 || decode(rec, &routes) != nil || len(routes) != 1 {
		t.Errorf("routes without pages: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, p, "/api/routes?pages=1"); rec.Code != 200 || decode(rec, &routes) != nil || len(routes) != 3 {
		t.Fatalf("routes with pages: %d %s", rec.Code, rec.Body.String())
	}
	for _, ri := range routes {
		if isPage := strings.HasPrefix(ri.ID, "page:"); ri.Page != isPage {
			t.Errorf("%s: page %v", ri.ID, ri.Page)
		}
	}

	var d PageDetail
	if rec := serve(t, p, "/api/page?route="+q(detail.ID)); rec.Code != 200 || decode(rec, &d) != nil {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	if d.Route.Pattern != "/app/notes/{noteId}" || d.Route.Handler != "app.NoteDetailPage.render" ||
		len(d.Requests) != 1 || d.Requests[0].Node.Attrs["target"] != route.Name ||
		len(d.Links) != 1 || d.Links[0].Node.ID != notes.ID || d.Links[0].Edge.Attrs["trigger"] != "navigate" {
		t.Errorf("page detail: %+v", d)
	}

	var sitemap mermaid.Diagram
	if rec := serve(t, p, "/api/diagrams/sitemap?get=1"); rec.Code != 200 || decode(rec, &sitemap) != nil {
		t.Fatalf("site map: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`("/app/notes")`, `("/app/notes/{noteId}")`, `("GET /api/notes/{id}")`, "r0 --> r1", "r1 --> r0"} {
		if !strings.Contains(sitemap.Mermaid, want) {
			t.Errorf("site map lacks %q:\n%s", want, sitemap.Mermaid)
		}
	}
}
