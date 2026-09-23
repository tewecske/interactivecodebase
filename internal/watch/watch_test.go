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
	before := snapshot(dir, "")
	if len(before) != 3 {
		t.Fatalf("snapshot = %v, want go.mod, a.go and .git/HEAD", before)
	}

	write(t, filepath.Join(dir, "a.go"), "package m // edited\n")
	write(t, filepath.Join(dir, "db", "001.sql"), "create table t (id int);\n")
	write(t, filepath.Join(dir, "README.md"), "still ignored\n")
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/feature\n")
	got := diff(before, snapshot(dir, ""))
	want := []string{filepath.Join(dir, ".git", "HEAD"), filepath.Join(dir, "a.go"), filepath.Join(dir, "db", "001.sql")}
	if !slices.Equal(got, want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

func TestScalaRelevance(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"build.sbt", "icb.yaml", "project/Deps.scala", "project/build.properties", "project/plugins.sbt",
		"modules/backend/src/main/scala/app/Main.scala",
		"modules/backend/src/main/resources/db/migration/V1__init.sql",
		"modules/shared/src/main/scala/app/Api.scala",
		// Not analyzed: build output, IDE state, tests, other files.
		"modules/backend/target/scala-3.8.4/src_managed/Gen.scala",
		"modules/shared/.jvm/target/streams/x.scala",
		"project/target/config-classes/x.scala",
		"project/project/target/y.sbt",
		".bsp/sbt.json", ".metals/x.scala", ".bloop/x.scala",
		"modules/backend/src/test/scala/app/MainSpec.scala",
		"node_modules/pkg/x.scala",
		"modules/backend/src/main/resources/application.conf",
		"README.md", "main.go",
	} {
		write(t, filepath.Join(dir, filepath.FromSlash(f)), "x\n")
	}
	var got []string
	for p := range snapshot(dir, langScala) {
		rel, _ := filepath.Rel(dir, p)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	want := []string{
		"build.sbt", "icb.yaml",
		"modules/backend/src/main/resources/db/migration/V1__init.sql",
		"modules/backend/src/main/scala/app/Main.scala",
		"modules/shared/src/main/scala/app/Api.scala",
		"project/Deps.scala", "project/build.properties", "project/plugins.sbt",
	}
	if !slices.Equal(got, want) {
		t.Errorf("scala snapshot = %v, want %v", got, want)
	}
	// Go ignores Scala files and keeps its own.
	if relevant("", "Main.scala") || !relevant("", "main.go") || relevant(langScala, "main.go") {
		t.Error("relevance is not per language")
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
