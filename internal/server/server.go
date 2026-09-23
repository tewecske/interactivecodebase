// Package server serves the analysis over HTTP: a JSON API, Mermaid
// diagrams, and the web UI.
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

// Server serves one analyzed module.
type Server struct {
	r   *analysis.Result
	mux *http.ServeMux
	ui  http.Handler
	lsp *lsp.Client // optional: gopls for hover and references

	tablesOnce  sync.Once
	routesByTbl map[string][]TableRoute
	tablesErr   error
}

// New returns a server for r. ui serves the web UI at "/"; nil serves a
// placeholder page. lspClient adds gopls hover and references; nil
// leaves those endpoints answering 501.
func New(r *analysis.Result, ui http.Handler, lspClient ...*lsp.Client) *Server {
	s := &Server{r: r, mux: http.NewServeMux(), ui: ui}
	if len(lspClient) > 0 {
		s.lsp = lspClient[0]
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	api := map[string]func(*http.Request) (any, error){
		"GET /api/summary":            s.summary,
		"GET /api/routes":             s.listRoutes,
		"GET /api/entries":            s.listEntries,
		"GET /api/node":               s.node,
		"GET /api/page":               s.page,
		"GET /api/flow":               s.flow,
		"GET /api/paths":              s.paths,
		"GET /api/source":             s.source,
		"GET /api/refs":               s.refs,
		"GET /api/tables":             s.tables,
		"GET /api/table":              s.table,
		"GET /api/search":             s.search,
		"GET /api/diagrams/sitemap":   s.sitemapDiagram,
		"GET /api/diagrams/flow":      s.flowDiagram,
		"GET /api/diagrams/types":     s.typesDiagram,
		"GET /api/diagrams/er":        s.erDiagram,
		"GET /api/lsp/hover":          s.lspHover,
		"GET /api/lsp/definition":     s.lspLocations((*lsp.Client).Definition),
		"GET /api/lsp/references":     s.lspLocations((*lsp.Client).References),
		"GET /api/lsp/implementation": s.lspLocations((*lsp.Client).Implementations),
	}
	for pattern, h := range api {
		s.mux.Handle(pattern, jsonHandler(h))
	}
	s.mux.Handle("GET /api/", jsonHandler(func(*http.Request) (any, error) { return nil, errNotFound }))
	s.mux.Handle("GET /", s.uiHandler())
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) uiHandler() http.Handler {
	if s.ui != nil {
		return s.ui
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholder))
	})
}

const placeholder = `<!doctype html><meta charset="utf-8"><title>icb</title>
<h1>icb</h1><p>This binary was built without the web UI. Build it with <code>make build</code>
(which runs <code>make ui</code>, needing Node 22+). The JSON API is under <a href="/api/summary">/api/</a>.</p>`

var (
	errNotFound   = errors.New("not found")
	errBadRequest = errors.New("bad request")
)

// jsonHandler writes the handler's result as JSON, mapping errors to
// status codes.
func jsonHandler(h func(*http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := h(r)
		status := http.StatusOK
		switch {
		case errors.Is(err, errNotFound), errors.Is(err, graph.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, errBadRequest):
			status = http.StatusBadRequest
		case errors.Is(err, errUnavailable):
			status = http.StatusNotImplemented
		case err != nil:
			status = http.StatusInternalServerError
			slog.Error("api", "path", r.URL.Path, "err", err)
		}
		if err != nil {
			v = map[string]string{"error": err.Error()}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	})
}

// required returns a query parameter or a bad-request error.
func required(r *http.Request, name string) (string, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return "", badRequest("missing query parameter " + strconv.Quote(name))
	}
	return v, nil
}

func intParam(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, badRequest("invalid " + name)
	}
	return n, nil
}

type wrapped struct {
	kind error
	msg  string
}

func (e wrapped) Error() string   { return e.msg }
func (e wrapped) Unwrap() error   { return e.kind }
func badRequest(msg string) error { return wrapped{errBadRequest, msg} }
func notFound(msg string) error   { return wrapped{errNotFound, msg} }
