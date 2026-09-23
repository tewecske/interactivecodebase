// Command chiapp registers routes through go-chi/chi: nested Route and
// Group scopes, With and Use middleware, Mount and a helper taking a
// chi.Router.
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type server struct{}

func main() {
	s := &server{}
	_ = http.ListenAndServe(":8080", s.routes())
}

func (s *server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(logging)
	r.Get("/", s.home)
	r.Route("/notes", func(r chi.Router) {
		r.Get("/", s.listNotes)
		r.With(requireAuth).Post("/", s.createNote)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", s.getNote)
			r.Delete("/", s.deleteNote)
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Get("/account", s.account)
		r.Method(http.MethodPut, "/account", http.HandlerFunc(s.updateAccount))
	})
	r.Mount("/admin", s.adminRouter())
	registerAPI(r, s)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	return r
}

func (s *server) adminRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(requireAuth, requireAdmin)
	r.Get("/", s.adminHome)
	r.Post("/users/{id}/ban", s.banUser)
	return r
}

func registerAPI(r chi.Router, s *server) {
	api := r.With(requireAuth)
	api.Get("/api/me", s.me)
	api.MethodFunc(http.MethodPatch, "/api/me", s.updateMe)
}

func (s *server) home(w http.ResponseWriter, r *http.Request)          {}
func (s *server) listNotes(w http.ResponseWriter, r *http.Request)     {}
func (s *server) createNote(w http.ResponseWriter, r *http.Request)    {}
func (s *server) getNote(w http.ResponseWriter, r *http.Request)       {}
func (s *server) deleteNote(w http.ResponseWriter, r *http.Request)    {}
func (s *server) account(w http.ResponseWriter, r *http.Request)       {}
func (s *server) updateAccount(w http.ResponseWriter, r *http.Request) {}
func (s *server) adminHome(w http.ResponseWriter, r *http.Request)     {}
func (s *server) banUser(w http.ResponseWriter, r *http.Request)       {}
func (s *server) me(w http.ResponseWriter, r *http.Request)            {}
func (s *server) updateMe(w http.ResponseWriter, r *http.Request)      {}

func logging(next http.Handler) http.Handler { return next }

type user struct{ IsAdmin bool }

// authenticate finds the user of the session cookie.
func authenticate(r *http.Request) (*user, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		return nil, false
	}
	return &user{IsAdmin: c.Value == "admin"}, true
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticate(r); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := authenticate(r); !ok || !u.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
