package scala

import (
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
