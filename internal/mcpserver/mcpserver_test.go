package mcpserver

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

var webapp = sync.OnceValues(func() (*analysis.Result, error) {
	return analysis.Analyze(context.Background(), fixture.WebappDir(), analysis.Options{})
})

func TestMain(m *testing.M) {
	code := m.Run()
	if r, err := webapp(); err == nil {
		_ = r.Close()
	}
	os.Exit(code)
}

// connect starts the server over the shared webapp analysis and returns a
// connected client session. analyze is used by reanalyze.
func connect(t *testing.T, analyze live.AnalyzeFunc) *mcp.ClientSession {
	t.Helper()
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	cur := live.New(r, func() error { return nil }, analyze)
	t.Cleanup(func() { _ = cur.Close() })
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := t.Context()
	if _, err := New(cur, nil).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestToolsListed(t *testing.T) {
	cs := connect(t, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("tool %s lacks a description or schema", tool.Name)
		}
	}
	want := "find_callees find_callers find_paths get_flow get_node get_route get_source get_table list_routes list_tables reanalyze routes_touching_table search"
	if strings.Join(sorted(names), " ") != want {
		t.Errorf("tools = %v", names)
	}
}

func TestTools(t *testing.T) {
	cs := connect(t, nil)
	tests := []struct {
		tool string
		args map[string]any
		want []string
	}{
		{"list_routes", map[string]any{"access": "admin"}, []string{"2 routes", "POST /{lang}/admin/reindex\tadmin\tweb.requireAdmin > (*web.adminHandler).reindex\tinternal/web/router.go:"}},
		{"list_routes", nil, []string{"13 routes", "GET /{lang}/weather\tpublic*"}},
		{"get_route", map[string]any{"route": "GET /{lang}/notes/{id}"}, []string{
			"access: authenticated (authenticated via (*web.noteHandler).user)",
			"handler: (*web.noteHandler).detail (id method:(*example.com/webapp/internal/web.noteHandler).detail)",
			"requests from its page:\n  POST /{lang}/notes/{id}/share\thx-post\ttemplates/note.html:",
			"navigates to:",
			"reached from:",
		}},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes"}, []string{"INSERT INTO notes", "table audit_events (insert"}},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes", "format": "mermaid"}, []string{"sequenceDiagram", "postgres->>Database: INSERT notes"}},
		{"get_node", map[string]any{"id": "noteHandler).create"}, []string{"method (*noteHandler).create", "calls → (", "← handled_by (1):"}},
		{"get_source", map[string]any{"file": "internal/web/router.go", "start": 1, "end": 2}, []string{"lines 1-2", "    1  // Package web is the HTTP adapter"}},
		{"find_callers", map[string]any{"symbol": "Authenticator).Authenticate"}, []string{"web.requireAdmin$1", "(*example.com/webapp/internal/web.noteHandler).user"}},
		{"find_callees", map[string]any{"symbol": "(*example.com/webapp/internal/store/postgres.NoteRepository).Create"}, []string{"calls\tsink.sql:internal/store/postgres/notes.go:"}},
		{"find_paths", map[string]any{"from": "route:POST /{lang}/notes", "to": "sql_table:audit_events"}, []string{"path 1:", "-dispatches_to-> method:(*example.com/webapp/internal/store/postgres.NoteRepository).Create", "-queries-> sql_table:audit_events"}},
		{"routes_touching_table", map[string]any{"table": "notes", "op": "insert"}, []string{"1 routes touch notes", "POST /{lang}/notes\tinsert"}},
		{"list_tables", nil, []string{"4 tables", "notes: id bigserial"}},
		{"get_table", map[string]any{"name": "notes"}, []string{"references: owner_id → users.id on delete cascade", "referenced by: audit_events.note_id on delete set null", "routes:\n", "queries ("}},
		{"get_table", map[string]any{"name": "notes", "format": "mermaid"}, []string{"erDiagram", "notes }o--|| users"}},
		{"search", map[string]any{"query": "SMTPMailer", "kind": "type"}, []string{"1 results", "type:example.com/webapp/internal/mail.SMTPMailer"}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			out, isErr := call(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("error: %s", out)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
		})
	}
}

func TestToolErrors(t *testing.T) {
	cs := connect(t, nil)
	tests := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_route", map[string]any{"route": "GET /nope"}, "no node matches"},
		{"get_route", map[string]any{"route": "sql_table:notes"}, "is a sql_table, not a route"},
		{"get_node", map[string]any{"id": "Create"}, "matches several nodes"},
		{"get_source", map[string]any{"file": "../go.mod"}, "cannot read"},
		{"get_flow", map[string]any{"route": "POST /{lang}/notes", "prune": "bogus"}, "prune must be"},
		{"routes_touching_table", map[string]any{"table": "nope"}, `no table "nope"`},
		{"reanalyze", nil, "no analyzer configured"},
	}
	for _, tt := range tests {
		out, isErr := call(t, cs, tt.tool, tt.args)
		if !isErr || !strings.Contains(out, tt.want) {
			t.Errorf("%s %v: isError=%v %q, want error containing %q", tt.tool, tt.args, isErr, out, tt.want)
		}
	}
}

func TestResources(t *testing.T) {
	cs := connect(t, nil)
	for uri, want := range map[string]string{"icb://routes": "13 routes", "icb://schema": "erDiagram"} {
		res, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Contents) != 1 || !strings.Contains(res.Contents[0].Text, want) {
			t.Errorf("%s: %+v", uri, res.Contents)
		}
	}
}

func TestReanalyze(t *testing.T) {
	cs := connect(t, func(ctx context.Context) (*analysis.Result, error) {
		return analysis.Analyze(ctx, fixture.WebappDir(), analysis.Options{})
	})
	out, isErr := call(t, cs, "reanalyze", nil)
	if isErr || !strings.Contains(out, "13 routes") {
		t.Fatalf("reanalyze: %v %s", isErr, out)
	}
	if out, isErr := call(t, cs, "list_tables", nil); isErr || !strings.Contains(out, "4 tables") {
		t.Errorf("after reanalysis: %v %s", isErr, out)
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestLSPTools(t *testing.T) {
	c := lsp.New(fixture.WebappDir())
	if !c.Available() {
		t.Skip("gopls not installed")
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	cur := live.New(r, func() error { return nil }, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := New(cur, c).Connect(t.Context(), serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	at := map[string]any{"file": "internal/web/handlers.go", "line": 29, "name": "Authenticate"}
	if out, isErr := call(t, cs, "lsp_hover", at); isErr || !strings.Contains(out, "Authenticate(req *http.Request)") {
		t.Errorf("lsp_hover: %v %s", isErr, out)
	}
	if out, isErr := call(t, cs, "lsp_references", at); isErr || !strings.Contains(out, "internal/web/auth.go:") {
		t.Errorf("lsp_references: %v %s", isErr, out)
	}
	iface := map[string]any{"file": "internal/notes/service.go", "line": 24, "name": "Repository"}
	if out, isErr := call(t, cs, "lsp_implementations", iface); isErr || !strings.Contains(out, "internal/store/postgres/notes.go:") {
		t.Errorf("lsp_implementations: %v %s", isErr, out)
	}
	if out, isErr := call(t, cs, "lsp_hover", map[string]any{"file": "internal/web/handlers.go", "line": 29, "name": "Nope"}); !isErr || !strings.Contains(out, "not on line 29") {
		t.Errorf("unknown name: %v %s", isErr, out)
	}
}
