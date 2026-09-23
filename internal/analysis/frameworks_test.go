// The race detector triples the memory of analyzing four modules; make
// test-routers runs this without it.

//go:build !race

package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
)

// TestRouterFrameworks checks each router fixture against its golden
// route list. The fixtures run one after another: each analysis loads a
// framework and its dependencies.
func TestRouterFrameworks(t *testing.T) {
	if testing.Short() {
		t.Skip("analyzes four modules")
	}
	for _, name := range []string{"chi", "gin", "echo", "gorillamux"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "testdata", "fixtures", "routers", name)
			data, err := os.ReadFile(filepath.Join(dir, "routes.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want []fixture.Route
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			r, err := Analyze(t.Context(), dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			compareRoutes(t, want, r.Routes)
		})
	}
}
