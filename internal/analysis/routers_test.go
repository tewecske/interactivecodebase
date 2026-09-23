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

func TestPositional(t *testing.T) {
	for in, want := range map[string]string{
		"INSERT INTO users (email, name) VALUES (:email, :name)": "INSERT INTO users (email, name) VALUES ($1, $2)",
		"UPDATE t SET a = :a WHERE b <> ''::text AND c = ':x'":   "UPDATE t SET a = $1 WHERE b <> ''::text AND c = ':x'",
		"SELECT * FROM t WHERE id = :user.id":                    "SELECT * FROM t WHERE id = $1",
		"DELETE FROM t WHERE a < ? AND b = '?'":                  "DELETE FROM t WHERE a < $1 AND b = '?'",
	} {
		if got := positional(in); got != want {
			t.Errorf("positional(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGormNaming(t *testing.T) {
	for in, want := range map[string]string{
		"User": "users", "AuditEntry": "audit_entries", "UserID": "user_ids", "HTTPServer": "http_servers",
		"Person": "people", "Status": "statuses", "Box": "boxes", "Day": "days",
	} {
		if got := plural(snakeCase(in)); got != want {
			t.Errorf("table for %s = %q, want %q", in, got, want)
		}
	}
}
