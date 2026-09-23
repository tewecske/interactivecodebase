package analysis

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// TestLinkSQL imports an extractor's graph with SQL sinks and links them to
// the tables of a Flyway-style migration directory.
func TestLinkSQL(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "db", "migrations")
	if err := os.MkdirAll(mig, 0o755); err != nil {
		t.Fatal(err)
	}
	schema := "CREATE TABLE users (id bigint PRIMARY KEY, email text NOT NULL);\n" +
		"CREATE TABLE sessions (id text PRIMARY KEY, user_id bigint REFERENCES users (id));\n"
	if err := os.WriteFile(filepath.Join(mig, "V1__init.sql"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := `{"version": 1, "nodes": [
		{"id": "func:app.Repo.find", "kind": "func", "name": "Repo.find"},
		{"id": "sink.sql:Repo.scala:3:5", "kind": "sink.sql", "name": "ZioJdbcContext.run",
		 "detail": "SELECT t0.id, t1.email FROM sessions t0, users t1",
		 "attrs": {"callee": "io.getquill.context.qzio.ZioJdbcContext.run", "caller": "app.Repo.find", "resolved": "true"}},
		{"id": "sink.sql:Repo.scala:9:5", "kind": "sink.sql", "name": "Connection.prepareStatement",
		 "detail": "UPDATE users SET email = ? WHERE id = ?",
		 "attrs": {"callee": "java.sql.Connection.prepareStatement", "caller": "app.Repo.find", "resolved": "true"}},
		{"id": "sink.sql:Repo.scala:12:5", "kind": "sink.sql", "name": "Connection.prepareStatement",
		 "detail": "INSERT INTO audit (msg) VALUES ({?})",
		 "attrs": {"callee": "java.sql.Connection.prepareStatement", "caller": "app.Repo.find", "resolved": "false", "partial": "true"}}
	], "edges": [
		{"from": "func:app.Repo.find", "to": "sink.sql:Repo.scala:3:5", "kind": "calls"}
	]}`
	ctx := t.Context()
	p, err := ImportProject(ctx, LangScala, "app", dir, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if err := p.LinkSQL(ctx, []string{"db/migrations"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.MigrationDirs, []string{"db/migrations"}) {
		t.Errorf("migration dirs %v", p.MigrationDirs)
	}
	uses := func(id string) []string {
		nbs, err := p.Graph.Neighbors(ctx, id, graph.Out, graph.EdgeQueries)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, nb := range nbs {
			out = append(out, nb.Node.Name+":"+nb.Edge.Attrs["op"]+":"+nb.Edge.Attrs["columns"])
		}
		slices.Sort(out)
		return out
	}
	if got, want := uses("sink.sql:Repo.scala:3:5"), []string{"sessions:select:id", "users:select:email"}; !slices.Equal(got, want) {
		t.Errorf("select uses %v, want %v", got, want)
	}
	// JDBC's ? placeholders are rewritten before parsing.
	if got, want := uses("sink.sql:Repo.scala:9:5"), []string{"users:update:email,id"}; !slices.Equal(got, want) {
		t.Errorf("update uses %v, want %v", got, want)
	}
	n, err := p.Graph.Node(ctx, "sink.sql:Repo.scala:9:5")
	if err != nil {
		t.Fatal(err)
	}
	if n.Attrs["op"] != "update" || n.Attrs["parseError"] != "" || n.Attrs["callee"] != "java.sql.Connection.prepareStatement" {
		t.Errorf("update sink attrs %v", n.Attrs)
	}
	// A table the migrations do not create is inferred; the extractor's
	// partial mark is kept.
	audit, err := p.Graph.Node(ctx, "sink.sql:Repo.scala:12:5")
	if err != nil {
		t.Fatal(err)
	}
	if audit.Attrs["partial"] != "true" {
		t.Errorf("audit sink attrs %v", audit.Attrs)
	}
	for id, attr := range map[string]string{"sql_table:users": "", "sql_table:sessions": "", "sql_table:audit": "true"} {
		n, err := p.Graph.Node(ctx, id)
		if err != nil || n.Attrs["inferred"] != attr {
			t.Errorf("%s: %+v, %v", id, n, err)
		}
	}
	fks, err := p.Graph.Neighbors(ctx, "sql_table:sessions", graph.Out, graph.EdgeFK)
	if err != nil || len(fks) != 1 || fks[0].Node.ID != "sql_table:users" {
		t.Errorf("sessions foreign keys %+v, %v", fks, err)
	}
}
