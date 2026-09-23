package live

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// fake returns analyses with an empty graph, counting calls.
func fake(t *testing.T, fail *bool) (AnalyzeFunc, *int) {
	n := 0
	return func(ctx context.Context) (*analysis.Result, error) {
		if *fail {
			return nil, errors.New("does not build")
		}
		n++
		g, err := graph.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return &analysis.Result{Module: "m", Graph: g}, nil
	}, &n
}

func TestReanalyzeSwapsAndKeepsOldOnFailure(t *testing.T) {
	fail := false
	analyze, calls := fake(t, &fail)
	first, _ := analyze(t.Context())
	c := New(first, nil, analyze)
	defer func() { _ = c.Close() }()

	if err := c.Reanalyze(t.Context()); err != nil {
		t.Fatal(err)
	}
	var cur *analysis.Result
	_ = c.With(func(r *analysis.Result) error { cur = r; return nil })
	if cur == first || c.Generation() != 2 || *calls != 2 {
		t.Errorf("after reanalysis: same=%v gen=%d calls=%d", cur == first, c.Generation(), *calls)
	}
	if _, err := first.Graph.Counts(t.Context()); err == nil {
		t.Error("old graph not closed")
	}

	fail = true
	if err := c.Reanalyze(t.Context()); err == nil {
		t.Fatal("reanalysis should fail")
	}
	var still *analysis.Result
	_ = c.With(func(r *analysis.Result) error { still = r; return nil })
	if still != cur || c.Generation() != 2 {
		t.Error("failed reanalysis replaced the current analysis")
	}
}

func TestReadersFinishBeforeSwap(t *testing.T) {
	fail := false
	analyze, _ := fake(t, &fail)
	first, _ := analyze(t.Context())
	c := New(first, nil, analyze)
	defer func() { _ = c.Close() }()

	inside := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = c.With(func(r *analysis.Result) error {
			close(inside)
			<-release
			// The graph must still be open while we hold it.
			if _, err := r.Graph.Counts(context.Background()); err != nil {
				t.Errorf("graph closed under a reader: %v", err)
			}
			return nil
		})
	})
	<-inside
	done := make(chan error, 1)
	go func() { done <- c.Reanalyze(context.Background()) }()
	close(release)
	wg.Wait()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClosed(t *testing.T) {
	fail := false
	analyze, _ := fake(t, &fail)
	first, _ := analyze(t.Context())
	c := New(first, nil, analyze)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.With(func(*analysis.Result) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("With after Close: %v", err)
	}
}
