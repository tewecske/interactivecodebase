//go:build goweb

package mcpserver

import (
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/live"
)

func TestGowebScenarios(t *testing.T) {
	dir, _ := fixture.Goweb(t)
	r, err := analysis.Analyze(t.Context(), dir, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cur := live.New(r, nil, nil)
	t.Cleanup(func() { _ = cur.Close() })
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := New(cur, nil).Connect(t.Context(), serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "scenarios"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	runScenarios(t, cs, filepath.Join(fixture.RepoRoot(), "testdata", "golden", "goweb-mcp-scenarios.json"))
}
