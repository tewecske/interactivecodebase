package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	h := Handler()
	if h == nil {
		t.Skip("built without the web UI (run make ui)")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<div id="root">`) || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index: %d %q %q", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
}
