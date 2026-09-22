package sqlparse

import (
	"fmt"
	"testing"
)

func TestPostgresQuery(t *testing.T) {
	tests := []struct {
		sql     string
		ops     string
		tables  string // "name:op[cols]" in order
		partial bool
		err     bool
	}{
		{"SELECT id, title FROM notes WHERE owner_id = $1", "[select]", "notes:select[id title owner_id]", false, false},
		{"SELECT * FROM notes n WHERE n.owner_id = $1", "[select]", "notes:select[* owner_id]", false, false},
		{"SELECT u.id, u.email FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = $1", "[select]",
			"sessions:select[user_id id] users:select[id email]", false, false},
		{"INSERT INTO sessions (id, user_id) SELECT $1, id FROM users WHERE email = $2", "[insert]",
			"sessions:insert[id user_id] users:select[]", false, false},
		{"UPDATE groups SET name = $1, version = version + 1 WHERE id = $2", "[update]", "groups:update[name version id]", false, false},
		{"DELETE FROM notes WHERE id = $1", "[delete]", "notes:delete[id]", false, false},
		{"WITH gone AS (DELETE FROM notes WHERE id = $1 RETURNING id) SELECT count(*) FROM gone", "[select]", "notes:delete[id]", false, false},
		{"SELECT COUNT(*) FROM users WHERE {?}", "[select]", "users:select[]", true, false},
		{"SELECT id FROM users LIMIT ${?}{?} OFFSET ${?}{?}", "[select]", "users:select[id]", true, false},
		{"CREATE TABLE IF NOT EXISTS schema_migrations (version bigint)", "[ddl]", "schema_migrations:ddl[]", false, false},
		{"SELECT pg_advisory_xact_lock($1)", "[select]", "", false, false},
		{"BEGIN; INSERT INTO a (x) VALUES (1); UPDATE b SET y = 2; COMMIT", "[other insert update other]", "a:insert[x] b:update[y]", false, false},
		{"INSERT INTO notes VALUES (", "[insert]", "notes:insert[]", true, true},
		{"DELETE FROM sessions WHERE", "[delete]", "sessions:delete[]", true, true},
	}
	for _, tt := range tests {
		q := Postgres.Query(tt.sql)
		tables := ""
		for i, u := range q.Tables {
			if i > 0 {
				tables += " "
			}
			tables += fmt.Sprintf("%s:%s%v", u.Name, u.Op, u.Columns)
		}
		if got := fmt.Sprint(q.Ops); got != tt.ops || tables != tt.tables || q.Partial != tt.partial || (q.Err != nil) != tt.err {
			t.Errorf("%s\n got ops=%s tables=%q partial=%v err=%v\nwant ops=%s tables=%q partial=%v err=%v",
				tt.sql, got, tables, q.Partial, q.Err, tt.ops, tt.tables, tt.partial, tt.err)
		}
	}
}
