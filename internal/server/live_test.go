package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/live"
)

func TestLiveStatusAndEvents(t *testing.T) {
	empty := func(ctx context.Context) (*analysis.Project, error) {
		g, err := graph.Open(ctx)
		return &analysis.Project{Module: "m", Graph: g}, err
	}
	first, err := empty(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cur := live.New(first, nil, empty)
	defer func() { _ = cur.Close() }()
	ts := httptest.NewServer(NewLive(cur, nil, nil))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var s live.Status
	err = json.NewDecoder(res.Body).Decode(&s)
	_ = res.Body.Close()
	if err != nil || s.Generation != 1 {
		t.Fatalf("status = %+v, %v", s, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := bufio.NewScanner(res.Body)
	next := func() live.Status {
		t.Helper()
		for events.Scan() {
			if data, ok := strings.CutPrefix(events.Text(), "data: "); ok {
				var s live.Status
				if err := json.Unmarshal([]byte(data), &s); err != nil {
					t.Fatal(err)
				}
				return s
			}
		}
		t.Fatalf("stream ended: %v", events.Err())
		return live.Status{}
	}
	if s := next(); s.Generation != 1 {
		t.Errorf("first event = %+v", s)
	}
	if err := cur.Reanalyze(t.Context()); err != nil {
		t.Fatal(err)
	}
	for s := next(); s.Generation != 2 || s.Analyzing; s = next() {
		if s.Generation != 1 || !s.Analyzing {
			t.Fatalf("unexpected event %+v", s)
		}
	}
}
