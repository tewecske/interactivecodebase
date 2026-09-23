package analysis

import "testing"

func TestFrameworkJoinAndPackages(t *testing.T) {
	chi, gin := chiFramework(), ginFramework()
	for _, c := range []struct {
		fw                   *framework
		prefix, pattern, out string
	}{
		{chi, "/notes", "/", "/notes"},
		{chi, "/notes/", "/{id}", "/notes/{id}"},
		{chi, "", "/", "/"},
		{gin, "/v1", "notes", "/v1/notes"},
		{gin, "/v1/", "/notes/", "/v1/notes/"},
		{gin, "/v1", "", "/v1"},
	} {
		if got := c.fw.join(c.prefix, c.pattern); got != c.out {
			t.Errorf("%s join(%q, %q) = %q, want %q", c.fw.name, c.prefix, c.pattern, got, c.out)
		}
	}
	for path, want := range map[string]bool{
		"github.com/go-chi/chi": true, "github.com/go-chi/chi/v5": true,
		"github.com/go-chi/chi/v5/middleware": false, "github.com/go-chi/chix": false,
	} {
		if got := chi.inPkg(path); got != want {
			t.Errorf("inPkg(%q) = %v", path, got)
		}
	}
}
