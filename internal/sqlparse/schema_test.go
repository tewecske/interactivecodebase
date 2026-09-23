package sqlparse

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
)

func TestWebappSchemaMatchesExpectations(t *testing.T) {
	dir, exp := fixture.Webapp(t)
	checkSchema(t, dir, exp)
}

// checkSchema compares the schema built from dir's migrations with the
// expected tables and foreign keys.
func checkSchema(t *testing.T, dir string, exp fixture.Expectations) {
	t.Helper()
	s, used, err := LoadMigrations(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Errors {
		t.Error(e)
	}
	if !slices.Equal(used, []string{"migrations"}) {
		t.Errorf("migration dirs = %v", used)
	}
	var names []string
	for _, tbl := range s.Tables {
		names = append(names, tbl.Name)
	}
	if !sameSet(names, exp.Tables) {
		t.Errorf("tables = %v, want %v", names, exp.Tables)
	}
	var got []fixture.ForeignKey
	for _, tbl := range s.Tables {
		for _, fk := range tbl.ForeignKeys {
			got = append(got, fixture.ForeignKey{
				Table: tbl.Name, Column: strings.Join(fk.Columns, ","),
				References: fk.RefTable + "." + strings.Join(fk.RefColumns, ","), OnDelete: fk.OnDelete,
			})
		}
	}
	for _, fk := range exp.ForeignKeys {
		if !slices.Contains(got, fk) {
			t.Errorf("missing foreign key %+v", fk)
		}
	}
	if len(got) != len(exp.ForeignKeys) {
		t.Errorf("got %d foreign keys, want %d: %+v", len(got), len(exp.ForeignKeys), got)
	}
}

func TestSchemaDDL(t *testing.T) {
	s := &Schema{}
	s.Apply("0001.sql", `
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    legacy INT
);

-- comment before the statement
CREATE TABLE posts (
    id BIGINT,
    author_id BIGINT NOT NULL,
    tags TEXT[],
    PRIMARY KEY (id),
    CONSTRAINT posts_author_fk FOREIGN KEY (author_id) REFERENCES users (id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS users (x int);
CREATE UNIQUE INDEX posts_author_idx ON posts (author_id);
INSERT INTO users (email) VALUES ('seed@example.com');
`)
	s.Apply("0002.sql", `
ALTER TABLE users ADD COLUMN name TEXT, DROP COLUMN legacy;
ALTER TABLE posts DROP CONSTRAINT posts_author_fk;
ALTER TABLE posts ADD CONSTRAINT posts_author_fk2 FOREIGN KEY (author_id) REFERENCES users (id);
ALTER TABLE users RENAME TO accounts;
ALTER TABLE accounts RENAME COLUMN name TO full_name;
CREATE TABLE tmp (id int);
DROP TABLE tmp;
DROP INDEX posts_author_idx;
`)
	for _, e := range s.Errors {
		t.Fatal(e)
	}
	if n := len(s.Tables); n != 2 {
		t.Fatalf("tables = %d, want 2", n)
	}
	users := s.Table("accounts")
	if users == nil {
		t.Fatal("users was not renamed to accounts")
	}
	if got := columnNames(users); !slices.Equal(got, []string{"id", "email", "full_name"}) {
		t.Errorf("accounts columns = %v", got)
	}
	if !slices.Equal(users.PrimaryKey, []string{"id"}) || users.Column("email").Type != "text" || !users.Column("email").NotNull {
		t.Errorf("accounts = %+v", users)
	}
	if users.Pos != (Pos{File: "0001.sql", Line: 2}) {
		t.Errorf("accounts pos = %+v", users.Pos)
	}
	posts := s.Table("posts")
	if posts.Pos.Line != 9 {
		t.Errorf("posts pos = %+v, want line 9 (after the comment)", posts.Pos)
	}
	if !slices.Equal(posts.PrimaryKey, []string{"id"}) || posts.Column("tags").Type != "text[]" {
		t.Errorf("posts = %+v", posts)
	}
	if len(posts.ForeignKeys) != 1 {
		t.Fatalf("posts foreign keys = %+v", posts.ForeignKeys)
	}
	fk := posts.ForeignKeys[0]
	if fk.Name != "posts_author_fk2" || fk.RefTable != "accounts" || fk.OnDelete != "" || fk.Pos != (Pos{File: "0002.sql", Line: 4}) {
		t.Errorf("fk = %+v", fk)
	}
	if len(posts.Indexes) != 0 {
		t.Errorf("posts indexes = %+v, want the dropped index gone", posts.Indexes)
	}
	if !slices.ContainsFunc(users.Indexes, func(ix Index) bool { return ix.Unique && slices.Equal(ix.Columns, []string{"email"}) }) {
		t.Errorf("accounts indexes = %+v, want unique email", users.Indexes)
	}
}

func TestSchemaRecordsParseErrors(t *testing.T) {
	s := &Schema{}
	s.Apply("bad.sql", "CREATE TABLE (;")
	if len(s.Errors) != 1 || !strings.Contains(s.Errors[0].Error(), "bad.sql") {
		t.Errorf("errors = %v", s.Errors)
	}
}

func TestLoadMigrationsLayouts(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("db/migrations/20240101_users.sql", "-- +goose Up\nCREATE TABLE users (id int);\n-- +goose Down\nDROP TABLE users;\n")
	write("db/migrations/20240102_posts.sql", "-- +goose Up\nCREATE TABLE posts (id int);\n")
	write("db/migrations/20240102_posts.down.sql", "DROP TABLE posts;")
	s, used, err := LoadMigrations(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(used, []string{"db/migrations"}) || len(s.Tables) != 2 {
		t.Fatalf("used %v, tables %+v", used, s.Tables)
	}
	if s.Tables[0].Pos != (Pos{File: "db/migrations/20240101_users.sql", Line: 2}) {
		t.Errorf("users pos = %+v", s.Tables[0].Pos)
	}
}

func TestFlywayMigrations(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mig := "modules/backend/src/main/resources/db/migration"
	// V10 renames the table V2 creates: versions, not names, order them.
	write(mig+"/postgresql/V1__init.sql", "CREATE TABLE users (id int);\n")
	write(mig+"/postgresql/V2__words.sql", "CREATE TABLE word (id int);\n")
	write(mig+"/postgresql/V10__rename.sql", "ALTER TABLE word RENAME TO words;\n")
	write(mig+"/postgresql/U10__rename.sql", "ALTER TABLE words RENAME TO word;\n")
	write(mig+"/postgresql/R__view.sql", "CREATE TABLE after_all (id int);\n")
	write(mig+"/h2/V1__init.sql", "CREATE TABLE h2_only (id int);\n")
	write("modules/backend/target/scala-3/classes/db/migration/V1__copy.sql", "CREATE TABLE copied (id int);\n")
	write("other/src/main/resources/db/migration/V1__other.sql", "CREATE TABLE other (id int);\n")

	dirs, err := FlywayDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{mig + "/postgresql", "other/src/main/resources/db/migration"}
	if !slices.Equal(dirs, want) {
		t.Fatalf("FlywayDirs = %v, want %v", dirs, want)
	}
	s, used, err := LoadMigrations(root, dirs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tbl := range s.Tables {
		names = append(names, tbl.Name)
	}
	if !slices.Equal(used, want) || !sameSet(names, []string{"users", "words", "after_all", "other"}) || len(s.Errors) > 0 {
		t.Errorf("used %v, tables %v, errors %v", used, names, s.Errors)
	}
}

func columnNames(t *Table) []string {
	var out []string
	for _, c := range t.Columns {
		out = append(out, c.Name)
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
