package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
)

// SessionUser is the user behind a session.
type SessionUser struct {
	ID      int64
	Email   string
	IsAdmin bool
}

// SessionRepository looks up sessions in PostgreSQL.
type SessionRepository struct {
	db *sql.DB
}

// NewSessionRepository returns a SessionRepository over db.
func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// UserBySession returns the user owning an unexpired session.
func (r *SessionRepository) UserBySession(ctx context.Context, sessionID string) (SessionUser, error) {
	var u SessionUser
	err := r.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.is_admin
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now()`, sessionID,
	).Scan(&u.ID, &u.Email, &u.IsAdmin)
	return u, err
}

// CreateSession starts a session for the user with email and returns its ID.
func (r *SessionRepository) CreateSession(ctx context.Context, email string) (string, error) {
	id := rand.Text()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, expires_at)
		SELECT $1, id, now() + interval '1 day' FROM users WHERE email = $2`, id, email)
	return id, err
}
