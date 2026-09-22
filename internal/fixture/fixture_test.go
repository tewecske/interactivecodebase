package fixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebappBuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not in PATH")
	}
	dir := WebappDir()
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet in %s: %v\n%s", dir, err, out)
	}
}

func TestWebappExpectationsMatchSource(t *testing.T) {
	dir, exp := Webapp(t)
	assertValid(t, dir, exp)
}

func TestValidateReportsProblems(t *testing.T) {
	dir, exp := Webapp(t)
	exp.Routes[5].Handler = "(*example.com/webapp/internal/web.noteHandler).missing"
	exp.Routes[9].Handler = "example.com/webapp/internal/web.weatherPage$2"
	exp.Routes[7].Access = "anonymous"
	exp.Routes[5].OptionalAuth = true
	exp.Routes = append(exp.Routes, exp.Routes[0])
	exp.Tables = exp.Tables[1:]
	exp.ForeignKeys[0].OnDelete = "restrict"
	exp.Implementations[0].Interface = "example.com/webapp/internal/notes.Service"
	exp.Sinks[0].Tables = []string{"nope"}
	exp.Flows[0].Route = "GET /nowhere"
	exp.Pages[0].Assets = append(exp.Pages[0].Assets, "/static/missing.js")
	exp.Pages[0].NavigatesTo = append(exp.Pages[0].NavigatesTo, "GET /{lang}/missing")

	problems, err := Validate(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"function (*example.com/webapp/internal/web.noteHandler).missing not found",
		"function example.com/webapp/internal/web.weatherPage$2 not found",
		`unknown access "anonymous"`,
		"optionalAuth only applies to public routes",
		"GET /{$}: duplicate",
		"table users: created in migrations but not expected",
		`sessions.user_id -> users.id (on delete "restrict"): not in migrations`,
		"sessions.user_id -> users.id: in migrations but not expected",
		"interface example.com/webapp/internal/notes.Service not found",
		"unknown table nope",
		"flow GET /nowhere: no such route",
		"asset /static/missing.js not found",
		"navigates to unknown route GET /{lang}/missing",
	}
	all := strings.Join(problems, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing problem %q", w)
		}
	}
	if len(problems) != len(want) {
		t.Errorf("got %d problems, want %d:\n%s", len(problems), len(want), all)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exp.json")
	if err := os.WriteFile(path, []byte(`{"module": "m", "routez": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "routez") {
		t.Errorf("err = %v, want unknown field error", err)
	}
}

func assertValid(t *testing.T, dir string, exp Expectations) {
	t.Helper()
	problems, err := Validate(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
