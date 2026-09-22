package flow_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
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

func TestWebappFlows(t *testing.T) {
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	_, exp := fixture.Webapp(t)
	checkFlows(t, r.Graph, exp.Flows)
}

func TestWebappMethodSplit(t *testing.T) {
	r, err := webapp()
	if err != nil {
		t.Fatal(err)
	}
	get := reaches(t, r.Graph, "GET /{lang}/sign-in")
	post := reaches(t, r.Graph, "POST /{lang}/sign-in")
	if get["sessions insert"] || !get["sessions select"] {
		t.Errorf("GET sign-in reaches %v; want the session lookup only", get)
	}
	if !post["sessions insert"] || post["sessions select"] {
		t.Errorf("POST sign-in reaches %v; want the session insert only", post)
	}
}

// checkFlows verifies that each expected route flow reaches its tables and
// sinks.
func checkFlows(t *testing.T, g *graph.Graph, want []fixture.Flow) {
	t.Helper()
	for _, f := range want {
		got := reaches(t, g, f.Route)
		for _, r := range f.Reaches {
			key := r.Sink
			if r.Table != "" {
				key = r.Table + " " + r.Op
			}
			if !got[key] {
				t.Errorf("flow %s: does not reach %q; reaches %v", f.Route, key, keys(got))
			}
		}
	}
}

// reaches lists "table op" and sink kinds in a route's flow.
func reaches(t *testing.T, g *graph.Graph, route string) map[string]bool {
	t.Helper()
	tree, err := flow.Build(t.Context(), g, graph.NodeID(graph.KindRoute, route), flow.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	flow.Walk(tree, func(s *flow.Step, _ int) {
		switch {
		case s.Node.Kind == graph.KindSQLTable:
			got[s.Node.Name+" "+s.Edge.Attrs["op"]] = true
		case strings.HasPrefix(string(s.Node.Kind), "sink."):
			got[string(s.Node.Kind)] = true
		}
	})
	return got
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
