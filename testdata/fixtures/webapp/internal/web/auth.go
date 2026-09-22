package web

import (
	"context"
	"errors"
	"net/http"

	"example.com/webapp/internal/store/postgres"
)

// ErrUnauthenticated means the request has no valid session.
var ErrUnauthenticated = errors.New("web: unauthenticated")

const sessionCookie = "session"

type sessionStore interface {
	UserBySession(ctx context.Context, sessionID string) (postgres.SessionUser, error)
	CreateSession(ctx context.Context, email string) (string, error)
}

// Authenticator resolves the signed-in user from the session cookie.
type Authenticator struct {
	sessions sessionStore
}

// NewAuthenticator returns an Authenticator backed by sessions.
func NewAuthenticator(sessions *postgres.SessionRepository) *Authenticator {
	return &Authenticator{sessions: sessions}
}

// Authenticate returns the request's user or ErrUnauthenticated.
func (a *Authenticator) Authenticate(req *http.Request) (postgres.SessionUser, error) {
	c, err := req.Cookie(sessionCookie)
	if err != nil {
		return postgres.SessionUser{}, ErrUnauthenticated
	}
	u, err := a.sessions.UserBySession(req.Context(), c.Value)
	if err != nil {
		return postgres.SessionUser{}, ErrUnauthenticated
	}
	return u, nil
}

// requireAdmin lets only administrators through to next.
func requireAdmin(auth *Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, err := auth.Authenticate(req)
		if err != nil || !u.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req)
	})
}
