// The race detector triples the memory of analyzing a module; make
// test-libs runs these without it.

//go:build !race

package analysis

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
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

// sqlUse is one SQL sink of a data-access fixture and the tables it
// touches, as "table:op".
type sqlUse struct {
	Caller string   `json:"caller"`
	Callee string   `json:"callee"`
	Tables []string `json:"tables"`
}

// TestDataAccessLibraries checks the SQL sinks of each data-access
// fixture against its golden list.
func TestDataAccessLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("analyzes four modules")
	}
	for _, name := range []string{"sqlx", "sqlc", "gorm", "pgx"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "testdata", "fixtures", "data", name)
			data, err := os.ReadFile(filepath.Join(dir, "sinks.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want []sqlUse
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			r, err := Analyze(t.Context(), dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			var got []sqlUse
			for _, s := range r.Sinks {
				if s.Kind != graph.KindSinkSQL {
					continue
				}
				u := sqlUse{Caller: s.Caller.String(), Callee: s.Callee, Tables: []string{}}
				if s.Query != nil {
					if s.Query.Err != nil {
						t.Errorf("%s: %v", s.Callee, s.Query.Err)
					}
					for _, tu := range s.Query.Tables {
						u.Tables = append(u.Tables, tu.Name+":"+tu.Op)
					}
				}
				slices.Sort(u.Tables)
				got = append(got, u)
			}
			slices.SortFunc(got, func(a, b sqlUse) int {
				return cmp.Or(cmp.Compare(a.Caller, b.Caller), cmp.Compare(a.Callee, b.Callee),
					cmp.Compare(strings.Join(a.Tables, ","), strings.Join(b.Tables, ",")))
			})
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			wantJSON, _ := json.MarshalIndent(want, "", "  ")
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("SQL sinks:\n%s\nwant:\n%s", gotJSON, wantJSON)
			}
		})
	}
}
