package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

// errUnavailable maps to 501: an optional capability (gopls, Go type
// information) is missing.
var errUnavailable = errors.New("unavailable")

// goAnalysis returns the Go analysis behind the project, or a 501 for a
// project in another language.
func (s *Server) goAnalysis(what string) (*analysis.Result, error) {
	if s.p.Go == nil {
		return nil, wrapped{errUnavailable, what + " needs Go type information; this is a " + s.p.Lang + " project"}
	}
	return s.p.Go, nil
}

// lspPosition reads file, line and col (1-based, bytes).
func lspPosition(r *http.Request) (file string, line, col int, err error) {
	if file, err = required(r, "file"); err != nil {
		return "", 0, 0, err
	}
	if line, err = intParam(r, "line", 0); err != nil || line == 0 {
		return "", 0, 0, badRequest("line is required")
	}
	if col, err = intParam(r, "col", 0); err != nil || col == 0 {
		return "", 0, 0, badRequest("col is required")
	}
	return file, line, col, nil
}

func (s *Server) gopls() (*lsp.Client, error) {
	if _, err := s.goAnalysis("gopls"); err != nil {
		return nil, err
	}
	if s.lsp == nil || !s.lsp.Available() {
		return nil, wrapped{errUnavailable, "gopls is not available (install it: go install golang.org/x/tools/gopls@latest)"}
	}
	return s.lsp, nil
}

// lspHover returns gopls's hover text (Markdown) for a position.
func (s *Server) lspHover(r *http.Request) (any, error) {
	c, err := s.gopls()
	if err != nil {
		return nil, err
	}
	file, line, col, err := lspPosition(r)
	if err != nil {
		return nil, err
	}
	text, err := c.Hover(r.Context(), file, line, col)
	return map[string]string{"markdown": text}, err
}

type locationQuery func(c *lsp.Client, ctx context.Context, file string, line, col int) ([]lsp.Location, error)

// lspLocations serves a gopls query returning locations.
func (s *Server) lspLocations(q locationQuery) func(*http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		c, err := s.gopls()
		if err != nil {
			return nil, err
		}
		file, line, col, err := lspPosition(r)
		if err != nil {
			return nil, err
		}
		locs, err := q(c, r.Context(), file, line, col)
		return nonNil(locs), err
	}
}
