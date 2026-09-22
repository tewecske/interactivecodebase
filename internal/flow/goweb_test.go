//go:build goweb

package flow_test

import (
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
)

func TestGowebFlows(t *testing.T) {
	dir, exp := fixture.Goweb(t)
	r, err := analysis.Analyze(t.Context(), dir, analysis.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	checkFlows(t, r.Graph, exp.Flows)

	// The sign-in handler branches on the method inside a helper
	// (handlePasswordPage): GET only checks the session.
	get := reaches(t, r.Graph, "GET /{lang}/sign-in")
	post := reaches(t, r.Graph, "POST /{lang}/sign-in")
	if get["sessions insert"] || !post["sessions insert"] {
		t.Errorf("sign-in: GET reaches %v, POST reaches %v", keys(get), keys(post))
	}
}
