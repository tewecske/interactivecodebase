package server

import (
	"net/http"
	"sync"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/live"
)

// Live serves whatever analysis is current, keeping one Server (with its
// caches) per analysis.
type Live struct {
	cur *live.Current
	ui  http.Handler

	mu  sync.Mutex
	r   *analysis.Result
	srv *Server
}

// NewLive returns a handler over the current analysis in cur.
func NewLive(cur *live.Current, ui http.Handler) *Live {
	return &Live{cur: cur, ui: ui}
}

func (l *Live) serverFor(r *analysis.Result) *Server {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.r != r {
		l.r, l.srv = r, New(r, l.ui)
	}
	return l.srv
}

// ServeHTTP serves the request against the current analysis, which stays
// valid for the whole request.
func (l *Live) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	err := l.cur.With(func(r *analysis.Result) error {
		l.serverFor(r).ServeHTTP(w, req)
		return nil
	})
	if err != nil {
		http.Error(w, "analysis unavailable: "+err.Error(), http.StatusServiceUnavailable)
	}
}
