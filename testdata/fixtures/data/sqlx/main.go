// Command sqlxapp reads and writes through jmoiron/sqlx, including named
// queries.
package main

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type user struct {
	ID    int64  `db:"id"`
	Email string `db:"email"`
	Name  string `db:"name"`
}

type store struct{ db *sqlx.DB }

func (s *store) user(ctx context.Context, id int64) (user, error) {
	var u user
	err := s.db.GetContext(ctx, &u, "SELECT id, email, name FROM users WHERE id = $1", id)
	return u, err
}

func (s *store) notes(userID int64) ([]string, error) {
	var bodies []string
	err := s.db.Select(&bodies, "SELECT body FROM notes WHERE user_id = $1", userID)
	return bodies, err
}

func (s *store) createUser(u user) error {
	_, err := s.db.NamedExec("INSERT INTO users (email, name) VALUES (:email, :name)", u)
	return err
}

func (s *store) rename(ctx context.Context, tx *sqlx.Tx, u user) error {
	_, err := tx.NamedExecContext(ctx, "UPDATE users SET name = :name WHERE id = :id AND name <> ''::text", u)
	return err
}

func (s *store) audit(action string) {
	s.db.MustExec("INSERT INTO audit_log (action) VALUES ($1)", action)
}

func (s *store) search(q string) (*sqlx.Rows, error) {
	return s.db.Queryx("SELECT id, email FROM users WHERE name LIKE $1", q)
}

func main() {
	db := sqlx.MustConnect("postgres", "")
	s := &store{db: db}
	u, _ := s.user(context.Background(), 1)
	_, _ = s.notes(u.ID)
	_ = s.createUser(u)
	tx := db.MustBegin()
	_ = s.rename(context.Background(), tx, u)
	s.audit("x")
	_, _ = s.search("a%")
}
