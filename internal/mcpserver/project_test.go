package mcpserver

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
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
