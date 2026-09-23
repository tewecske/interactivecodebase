package scala

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModules(t *testing.T) {
	out := `[warn] a warning that made it through
shared / Compile / fullClasspath
/app/modules/shared/target/classes:/cache/zio-json.jar:/cache/scala3-library.jar
backend / Compile / fullClasspath
/app/modules/backend/target/classes:/app/modules/shared/target/classes:/cache/zio-http.jar:/app/lib/vendored.jar
Compile / fullClasspath
/app/target/classes:/cache/scala3-library.jar
/elsewhere/classes:/cache/other.jar
tools / Compile / fullClasspath
/app/modules/tools/target/classes:/cache/scala3-library.jar
/app/modules/tools/target/classes:/cache/scala3-library.jar
`
	got := modules("/app", strings.Split(out, "\n"))
	want := []module{
		{classes: []string{"/app/modules/backend/target/classes", "/app/modules/shared/target/classes"}, classpath: []string{"/cache/zio-http.jar", "/app/lib/vendored.jar"}},
		{classes: []string{"/app/target/classes"}, classpath: []string{"/cache/scala3-library.jar"}},
		{classes: []string{"/app/modules/tools/target/classes"}, classpath: []string{"/cache/scala3-library.jar"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modules =\n%+v\nwant\n%+v", got, want)
	}
}

func TestArgs(t *testing.T) {
	if got := sbtArgs(nil); got[len(got)-1] != "export Compile/fullClasspath" || !strings.HasPrefix(got[3], "-J-Xmx") {
		t.Errorf("sbtArgs(nil) = %v", got)
	}
	if got := sbtArgs([]string{"backend", "frontend"}); !reflect.DeepEqual(got[len(got)-2:], []string{"export backend/Compile/fullClasspath", "export frontend/Compile/fullClasspath"}) {
		t.Errorf("sbtArgs(projects) = %v", got)
	}
	guards := map[string]string{"app.Auth.user": "authenticated", "app.Auth.admin": "admin"}
	got := extractorArgs("/app", "/tmp/g.json", guards, []module{{classes: []string{"/app/a", "/app/b"}, classpath: []string{"/x.jar", "/y.jar"}}})
	want := []string{
		"--root", "/app", "-o", "/tmp/g.json", "--guard", "app.Auth.admin=admin", "--guard", "app.Auth.user=authenticated",
		"--classpath", "/x.jar:/y.jar", "/app/a", "/app/b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractorArgs = %v", got)
	}
}

func TestMissingCommands(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := commands(t.TempDir(), Options{})
	if err == nil || !strings.Contains(err.Error(), "sbt is needed") {
		t.Errorf("no sbt: %v", err)
	}
}

func TestTail(t *testing.T) {
	if tail(nil) != "" {
		t.Error("empty output should add nothing")
	}
	long := strings.Repeat("line\n", 30) + "last"
	if got := tail([]byte(long)); strings.Count(got, "\n") != 20 || !strings.HasSuffix(got, "last") {
		t.Errorf("tail = %q", got)
	}
}

func TestClientArgs(t *testing.T) {
	if got := clientArgs(nil, false); !reflect.DeepEqual(got, []string{"--client", "--no-server", "export Compile/fullClasspath"}) {
		t.Errorf("clientArgs(nil, false) = %v", got)
	}
	want := []string{"--client", "; export backend/Compile/fullClasspath ; export frontend/Compile/fullClasspath"}
	if got := clientArgs([]string{"backend", "frontend"}, true); !reflect.DeepEqual(got, want) {
		t.Errorf("clientArgs(projects, true) = %v", got)
	}
}

func TestClientUnavailable(t *testing.T) {
	for out, want := range map[string]bool{
		"[error] no sbt server is running (sbt.server.autostart=false)\n": true,
		"": true,
		"[info] compiling 1 Scala source\n[error] -- [E103] Syntax Error: Main.scala\n[error] one error found\n": false,
	} {
		if got := clientUnavailable([]byte(out)); got != want {
			t.Errorf("clientUnavailable(%q) = %v", out, got)
		}
	}
}

func TestServerRunning(t *testing.T) {
	dir := t.TempDir()
	if ServerRunning(dir) {
		t.Error("no active.json")
	}
	sock := filepath.Join(t.TempDir(), "sock")
	active := filepath.Join(dir, "project", "target", "active.json")
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte(`{"uri":"local://`+sock+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ServerRunning(dir) {
		t.Error("stale active.json counts as a running server")
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if !ServerRunning(dir) {
		t.Error("server listening on the socket not found")
	}
}
