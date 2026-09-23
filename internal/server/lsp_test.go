package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

func TestLSPUnavailable(t *testing.T) {
	var e map[string]string
	if code := get(t, "/api/lsp/hover?file=internal/web/auth.go&line=1&col=1", &e); code != http.StatusNotImplemented || !strings.Contains(e["error"], "gopls") {
		t.Errorf("without gopls: %d %v", code, e)
	}
}

func TestLSPEndpoints(t *testing.T) {
	c := lsp.New(fixture.WebappDir())
	if !c.Available() {
		t.Skip("gopls not installed")
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	h := New(r.Project(), nil, c)
	serve := func(path string, v any) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return rec.Code
	}
	// "Authenticate" in `if _, err := h.auth.Authenticate(req); err == nil {`.
	pos := "file=internal/web/handlers.go&line=29&col=23"
	var hover map[string]string
	if code := serve("/api/lsp/hover?"+pos, &hover); code != 200 || !strings.Contains(hover["markdown"], "Authenticate(req *http.Request)") {
		t.Errorf("hover %d %v", code, hover)
	}
	var refs []lsp.Location
	if code := serve("/api/lsp/references?"+pos, &refs); code != 200 || len(refs) < 5 {
		t.Errorf("references %d %d", code, len(refs))
	}
	var defs []lsp.Location
	if code := serve("/api/lsp/definition?"+pos, &defs); code != 200 || len(defs) != 1 || defs[0].File != "internal/web/auth.go" {
		t.Errorf("definition %d %+v", code, defs)
	}
	var e map[string]string
	if code := serve("/api/lsp/hover?file=internal/web/handlers.go&line=29", &e); code != 400 {
		t.Errorf("missing col: %d", code)
	}
}
