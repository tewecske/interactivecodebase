package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/fixture"
)

// Scenario is a question an agent might ask, the tool call that answers
// it, and what the answer must (and must not) contain.
type Scenario struct {
	Question string         `json:"question"`
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args"`
	Expect   []string       `json:"expect"`
	Reject   []string       `json:"reject,omitempty"`
}

// runScenarios plays every scenario in file against a session, reporting
// each failure with the question and the full answer.
func runScenarios(t *testing.T, cs *mcp.ClientSession, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []Scenario
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&scenarios); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	for _, sc := range scenarios {
		t.Run(sc.Question, func(t *testing.T) {
			out, isErr := call(t, cs, sc.Tool, sc.Args)
			if isErr {
				t.Fatalf("%s %v failed: %s", sc.Tool, sc.Args, out)
			}
			for _, want := range sc.Expect {
				if !strings.Contains(out, want) {
					t.Errorf("answer lacks %q\n%s %v returned:\n%s", want, sc.Tool, sc.Args, out)
				}
			}
			for _, bad := range sc.Reject {
				if strings.Contains(out, bad) {
					t.Errorf("answer contains %q\n%s %v returned:\n%s", bad, sc.Tool, sc.Args, out)
				}
			}
		})
	}
}

func TestWebappScenarios(t *testing.T) {
	runScenarios(t, connect(t, nil), filepath.Join(fixture.WebappDir(), "mcp-scenarios.json"))
}
