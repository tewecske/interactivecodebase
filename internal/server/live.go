package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

// Live serves whatever analysis is current, keeping one Server (with its
// caches) per analysis.
type Live struct {
	cur *live.Current
	ui  http.Handler
	lsp *lsp.Client

	mu  sync.Mutex
	r   *analysis.Result
	srv *Server
}

// NewLive returns a handler over the current analysis in cur, with gopls
// features when lspClient is not nil.
func NewLive(cur *live.Current, ui http.Handler, lspClient *lsp.Client) *Live {
	return &Live{cur: cur, ui: ui, lsp: lspClient}
}

func (l *Live) serverFor(r *analysis.Result) *Server {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.r != r {
		l.r, l.srv = r, New(r, l.ui, l.lsp)
	}
	return l.srv
}

// ServeHTTP serves the request against the current analysis, which stays
// valid for the whole request. The analysis status and its event stream
// are served outside it, so they never hold up a swap.
func (l *Live) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		switch req.URL.Path {
		case "/api/status":
			jsonHandler(func(*http.Request) (any, error) { return l.cur.Status(), nil }).ServeHTTP(w, req)
			return
		case "/api/events":
			l.events(w, req)
			return
		}
	}
	err := l.cur.With(func(r *analysis.Result) error {
		l.serverFor(r).ServeHTTP(w, req)
		return nil
	})
	if err != nil {
		http.Error(w, "analysis unavailable: "+err.Error(), http.StatusServiceUnavailable)
	}
}

// events streams the analysis status as server-sent events: the current
// status on connect, then every change, with a comment every 30s so
// proxies keep the connection open.
func (l *Live) events(w http.ResponseWriter, req *http.Request) {
	rc := http.NewResponseController(w)
	ch, unsubscribe := l.cur.Subscribe()
	defer unsubscribe()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	send := func(s any) error {
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", data); err != nil {
			return err
		}
		return rc.Flush()
	}
	if send(l.cur.Status()) != nil {
		return
	}
	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case s := <-ch:
			if send(s) != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
