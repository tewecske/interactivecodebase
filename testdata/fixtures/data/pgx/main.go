// Command pgxapp talks to Postgres through pgx directly: a pool, a
// transaction and a batch.
package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct{ pool *pgxpool.Pool }

func (s *store) userName(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", id).Scan(&name)
	return name, err
}

func (s *store) moveNotes(ctx context.Context, from, to int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "UPDATE notes SET user_id = $1 WHERE user_id = $2", to, from); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *store) importNotes(ctx context.Context, userID int64, bodies []string) error {
	b := &pgx.Batch{}
	for _, body := range bodies {
		b.Queue("INSERT INTO notes (user_id, body) VALUES ($1, $2)", userID, body)
	}
	b.Queue("INSERT INTO audit_log (action) VALUES ('import')")
	return s.pool.SendBatch(ctx, b).Close()
}

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "")
	if err != nil {
		return
	}
	s := &store{pool: pool}
	_, _ = s.userName(ctx, 1)
	_ = s.moveNotes(ctx, 1, 2)
	_ = s.importNotes(ctx, 1, nil)
}
