package watch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotDiff(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module m\n")
	write(t, filepath.Join(dir, "a.go"), "package m\n")
	write(t, filepath.Join(dir, "README.md"), "ignored\n")
	write(t, filepath.Join(dir, "node_modules", "x.go"), "ignored\n")
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	before := snapshot(dir)
	if len(before) != 3 {
		t.Fatalf("snapshot = %v, want go.mod, a.go and .git/HEAD", before)
	}

	write(t, filepath.Join(dir, "a.go"), "package m // edited\n")
	write(t, filepath.Join(dir, "db", "001.sql"), "create table t (id int);\n")
	write(t, filepath.Join(dir, "README.md"), "still ignored\n")
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/feature\n")
	got := diff(before, snapshot(dir))
	want := []string{filepath.Join(dir, ".git", "HEAD"), filepath.Join(dir, "a.go"), filepath.Join(dir, "db", "001.sql")}
	if !slices.Equal(got, want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

func TestRunDebouncesBursts(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package m\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := make(chan []string, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, dir, Options{Interval: 20 * time.Millisecond, Quiet: 150 * time.Millisecond}, func(p []string) { calls <- p })
	}()

	time.Sleep(50 * time.Millisecond)
	// A burst of saves, each within the quiet period of the last.
	for i := range 3 {
		write(t, filepath.Join(dir, "a.go"), "package m\n"+string(rune('a'+i))+"\n")
		time.Sleep(40 * time.Millisecond)
	}
	write(t, filepath.Join(dir, "b.go"), "package m\n")
	select {
	case p := <-calls:
		want := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}
		slices.Sort(p)
		if !slices.Equal(p, want) {
			t.Errorf("changed = %v, want %v", p, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
	select {
	case p := <-calls:
		t.Errorf("burst reported twice; second = %v", p)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	<-done
}
