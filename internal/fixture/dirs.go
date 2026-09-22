package fixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Environment variables for the goweb golden test.
const (
	// GowebDirEnv overrides where the goweb checkout is looked up.
	GowebDirEnv = "ICB_GOWEB_DIR"
	// GowebRequiredEnv, when non-empty, turns a missing or mismatched
	// checkout into a failure instead of a skip (set in CI).
	GowebRequiredEnv = "ICB_GOWEB_REQUIRED"
)

// RepoRoot returns the interactivecodebase repository root.
func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("fixture: cannot locate source file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// WebappDir returns the webapp fixture module directory.
func WebappDir() string {
	return filepath.Join(RepoRoot(), "testdata", "fixtures", "webapp")
}

// Webapp loads the webapp fixture's expectations.
func Webapp(t testing.TB) (dir string, exp Expectations) {
	t.Helper()
	dir = WebappDir()
	exp, err := Load(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, exp
}

// Goweb returns the goweb checkout and its expectations. It skips the test
// when the checkout is missing or not at the pinned commit, or fails if
// $ICB_GOWEB_REQUIRED is set. The checkout is $ICB_GOWEB_DIR, or ../goweb
// next to this repository.
func Goweb(t testing.TB) (dir string, exp Expectations) {
	t.Helper()
	exp, err := Load(filepath.Join(RepoRoot(), "testdata", "golden", "goweb.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir = os.Getenv(GowebDirEnv)
	if dir == "" {
		dir = filepath.Join(RepoRoot(), "..", "goweb")
	}
	skipf := t.Skipf
	if os.Getenv(GowebRequiredEnv) != "" {
		skipf = t.Fatalf
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		skipf("goweb checkout not found at %s (set %s): %v", dir, GowebDirEnv, err)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		skipf("cannot read goweb commit in %s: %v", dir, err)
	}
	if head := strings.TrimSpace(string(out)); head != exp.Commit {
		skipf("goweb at %s is at %s, expectations are pinned to %s", dir, head, exp.Commit)
	}
	return dir, exp
}
