package fixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Environment variables for the goweb and gathedge golden tests.
const (
	// GowebDirEnv overrides where the goweb checkout is looked up.
	GowebDirEnv = "ICB_GOWEB_DIR"
	// GowebRequiredEnv, when non-empty, turns a missing or mismatched
	// checkout into a failure instead of a skip (set in CI).
	GowebRequiredEnv = "ICB_GOWEB_REQUIRED"
	// GathedgeDirEnv overrides where the gathedge checkout is looked up.
	GathedgeDirEnv = "ICB_GATHEDGE_DIR"
	// GathedgeRequiredEnv, when non-empty, turns a missing or mismatched
	// gathedge checkout into a failure instead of a skip.
	GathedgeRequiredEnv = "ICB_GATHEDGE_REQUIRED"
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
	checkCheckout(t, "goweb", dir, "go.mod", exp.Commit, GowebDirEnv, GowebRequiredEnv)
	return dir, exp
}

// GathedgeGolden is testdata/golden/gathedge.json, the expectations for
// gathedge, a Scala 3 sbt app.
func GathedgeGolden() string {
	return filepath.Join(RepoRoot(), "testdata", "golden", "gathedge.json")
}

// Gathedge returns the gathedge checkout, skipping the test (or failing
// with $ICB_GATHEDGE_REQUIRED) when it is missing or not at commit. The
// checkout is $ICB_GATHEDGE_DIR, else ../gathedge next to this repository
// or next to its parent directory.
func Gathedge(t testing.TB, commit string) string {
	t.Helper()
	dir := os.Getenv(GathedgeDirEnv)
	if dir == "" {
		dir = filepath.Join(RepoRoot(), "..", "gathedge")
		if up := filepath.Join(RepoRoot(), "..", "..", "gathedge"); !exists(dir) && exists(up) {
			dir = up
		}
	}
	checkCheckout(t, "gathedge", dir, "build.sbt", commit, GathedgeDirEnv, GathedgeRequiredEnv)
	return dir
}

// checkCheckout skips (or with $requiredEnv fails) the test unless dir
// holds marker and its git HEAD is commit.
func checkCheckout(t testing.TB, name, dir, marker, commit, dirEnv, requiredEnv string) {
	t.Helper()
	skipf := t.Skipf
	if os.Getenv(requiredEnv) != "" {
		skipf = t.Fatalf
	}
	if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
		skipf("%s checkout not found at %s (set %s): %v", name, dir, dirEnv, err)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		skipf("cannot read %s commit in %s: %v", name, dir, err)
	}
	if head := strings.TrimSpace(string(out)); head != commit {
		skipf("%s at %s is at %s, expectations are pinned to %s", name, dir, head, commit)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
