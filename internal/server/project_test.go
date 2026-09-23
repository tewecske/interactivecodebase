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
