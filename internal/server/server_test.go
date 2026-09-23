package server

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
)

var update = flag.Bool("update", false, "rewrite golden Mermaid files")

var webapp = sync.OnceValues(func() (*analysis.Result, error) {
	return analysis.Analyze(context.Background(), fixture.WebappDir(), analysis.Options{})
})

func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	if r, err := webapp(); err == nil {
		_ = r.Close()
	}
	os.Exit(code)
}

// get requests path from a server over the webapp analysis and decodes
// the JSON response into v, returning the status.
func get(t *testing.T, path string, v any) int {
	t.Helper()
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New(r, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(path, "/api/") && ct != "application/json" {
		t.Errorf("%s: content type %q", path, ct)
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, rec.Body.String())
		}
	}
	return rec.Code
}

func q(s string) string { return url.QueryEscape(s) }

func TestSummaryAndRoutes(t *testing.T) {
	var sum Summary
	if code := get(t, "/api/summary", &sum); code != 200 || sum.Module != "example.com/webapp" || sum.Counts.Nodes["route"] != 13 {
		t.Errorf("summary %d %+v", code, sum)
	}
	var routes []RouteInfo
	get(t, "/api/routes?access=admin", &routes)
	if len(routes) != 2 || routes[0].Pattern != "/{lang}/admin/export" || routes[1].Middleware[0] != "example.com/webapp/internal/web.requireAdmin" {
		t.Errorf("admin routes = %+v", routes)
	}
	get(t, "/api/routes?method=GET&q=notes", &routes)
	if len(routes) != 2 || !routes[0].Page {
		t.Errorf("GET notes routes = %+v", routes)
	}
}

func TestNodePageFlowPaths(t *testing.T) {
	var d NodeDetail
	get(t, "/api/node?id="+q("method:(*example.com/webapp/internal/web.noteHandler).create"), &d)
	if len(d.Out["calls"]) == 0 || len(d.In["handled_by"]) != 1 {
		t.Errorf("node detail out=%d in=%v", len(d.Out["calls"]), d.In["handled_by"])
	}
	var p PageDetail
	get(t, "/api/page?route="+q("GET /{lang}/notes"), &p)
	if len(p.Requests) != 2 || len(p.Assets) != 2 || len(p.Renders) != 1 || len(p.Links) == 0 {
		t.Errorf("page = %d requests, %d assets, %d renders, %d links", len(p.Requests), len(p.Assets), len(p.Renders), len(p.Links))
	}
	var tree map[string]any
	if code := get(t, "/api/flow?route="+q("POST /{lang}/notes")+"&prune=module", &tree); code != 200 || tree["children"] == nil {
		t.Errorf("flow %d %v", code, tree)
	}
	var paths []any
	get(t, "/api/paths?from="+q("route:POST /{lang}/notes")+"&to="+q("sql_table:notes"), &paths)
	if len(paths) != 0 {
		// Routes reach tables through handled_by + calls + queries.
		t.Logf("%d paths", len(paths))
	}
}

func TestSourceAndTables(t *testing.T) {
	var src Source
	if code := get(t, "/api/source?file=internal/web/router.go&start=1&end=3", &src); code != 200 || len(src.Lines) != 3 || src.Lines[0] != "// Package web is the HTTP adapter: routes, guards, handlers and rendering." {
		t.Errorf("source %d %+v", code, src)
	}
	var tables []map[string]any
	get(t, "/api/tables", &tables)
	if len(tables) != 4 {
		t.Errorf("tables = %d", len(tables))
	}
	var tbl TableDetail
	get(t, "/api/table?name=notes", &tbl)
	if len(tbl.Columns) != 5 || len(tbl.FKsOut) != 1 || len(tbl.FKsIn) != 1 || len(tbl.Queries) == 0 || len(tbl.Routes) == 0 {
		t.Errorf("notes table = %d cols, %d refs, %d referenced by, %d queries, routes %v",
			len(tbl.Columns), len(tbl.FKsOut), len(tbl.FKsIn), len(tbl.Queries), tbl.Routes)
	}
	found := false
	for _, r := range tbl.Routes {
		found = found || (r.Route == "POST /{lang}/notes" && r.Op == "insert")
	}
	if !found {
		t.Errorf("notes table routes %v lack POST /{lang}/notes insert", tbl.Routes)
	}
	// Routes reaching several tables with the same operation list each one:
	// the session lookup reads sessions and users.
	var users TableDetail
	get(t, "/api/table?name=users", &users)
	if !slices.ContainsFunc(users.Routes, func(r TableRoute) bool { return r.Route == "GET /{lang}/notes" && r.Op == "select" }) {
		t.Errorf("users table routes %v lack GET /{lang}/notes select", users.Routes)
	}
	var results []map[string]any
	get(t, "/api/search?q=SMTPMailer&kind=type", &results)
	if len(results) != 1 {
		t.Errorf("search = %v", results)
	}
	get(t, "/api/search?kind=sink.sql", &results)
	if len(results) != 6 {
		t.Errorf("list sink.sql = %d", len(results))
	}
	var e map[string]string
	if code := get(t, "/api/search", &e); code != 400 {
		t.Errorf("empty search: %d", code)
	}
}

func TestErrors(t *testing.T) {
	var e map[string]string
	tests := []struct {
		path string
		code int
	}{
		{"/api/node", 400},
		{"/api/node?id=nope", 404},
		{"/api/page?route=" + q("GET /nope"), 404},
		{"/api/flow?route=" + q("GET /healthz") + "&prune=bogus", 400},
		{"/api/source?file=../go.mod", 400},
		{"/api/source?file=" + q("/etc/passwd"), 400},
		{"/api/source?file=missing.go", 404},
		{"/api/table?name=nope", 404},
		{"/api/diagrams/er?table=nope", 404},
		{"/api/nope", 404},
	}
	for _, tt := range tests {
		if code := get(t, tt.path, &e); code != tt.code || e["error"] == "" {
			t.Errorf("%s: %d %v, want %d with an error", tt.path, code, e, tt.code)
		}
	}
}

func TestSourceRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/etc", filepath.Join(dir, "etc")); err != nil {
		t.Skip(err)
	}
	// The source endpoint only needs the module directory.
	rec := httptest.NewRecorder()
	New(&analysis.Result{Dir: dir}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/source?file=etc/hostname", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("symlink escape: status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlaceholderUI(t *testing.T) {
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	New(r, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/api/summary") {
		t.Errorf("placeholder %d %s", rec.Code, rec.Body.String())
	}
}

func TestDiagramsGolden(t *testing.T) {
	tests := []struct{ name, path string }{
		{"sitemap", "/api/diagrams/sitemap"},
		{"sitemap-get", "/api/diagrams/sitemap?get=1"},
		{"sitemap-filtered", "/api/diagrams/sitemap?get=1&access=authenticated,admin&q=notes"},
		{"flow-post-notes", "/api/diagrams/flow?route=" + q("POST /{lang}/notes")},
		{"flow-share", "/api/diagrams/flow?route=" + q("POST /{lang}/notes/{id}/share")},
		{"types-notes-service-create", "/api/diagrams/types?id=" + q("method:(*example.com/webapp/internal/notes.Service).Create")},
		{"types-repository", "/api/diagrams/types?id=" + q("type:example.com/webapp/internal/notes.Repository")},
		{"er-notes", "/api/diagrams/er?table=notes"},
		{"er-all", "/api/diagrams/er"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d mermaid.Diagram
			if code := get(t, tt.path, &d); code != 200 {
				t.Fatalf("status %d", code)
			}
			golden := filepath.Join(fixture.RepoRoot(), "testdata", "golden", "mermaid", "webapp-"+tt.name+".mmd")
			if *update {
				if err := os.WriteFile(golden, []byte(d.Mermaid), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if d.Mermaid != string(want) {
				t.Errorf("diagram differs from %s:\n%s", golden, d.Mermaid)
			}
			if !strings.HasPrefix(tt.name, "flow") && !strings.HasPrefix(tt.name, "er") && len(d.IDs) == 0 {
				t.Error("diagram has no clickable ids")
			}
		})
	}
}

func TestRefs(t *testing.T) {
	var refs []Ref
	if code := get(t, "/api/refs?file=internal/web/handlers.go", &refs); code != 200 || len(refs) == 0 {
		t.Fatalf("refs %d, %d found", code, len(refs))
	}
	var auth, service *Ref
	for i := range refs {
		switch {
		case refs[i].Name == "Authenticate" && auth == nil:
			auth = &refs[i]
		case refs[i].Name == "Create" && refs[i].Kind == "method" && service == nil:
			service = &refs[i]
		}
	}
	if auth == nil || auth.Target == nil || auth.Target.File != "internal/web/auth.go" ||
		auth.Node != "method:(*example.com/webapp/internal/web.Authenticator).Authenticate" {
		t.Errorf("Authenticate ref = %+v", auth)
	}
	if service == nil || service.Node != "method:(*example.com/webapp/internal/notes.Service).Create" {
		t.Errorf("Service.Create ref = %+v", service)
	}
	for _, r := range refs {
		if r.EndCol-r.Col != len(r.Name) {
			t.Errorf("ref %+v: column range does not match the name", r)
		}
	}
	var e map[string]string
	if code := get(t, "/api/refs?file=templates/notes.html", &e); code != 404 {
		t.Errorf("non-Go file: %d", code)
	}
}
