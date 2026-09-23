// Command muxapp registers routes through gorilla/mux: Methods on routes,
// path-prefix subrouters with middleware, a Handler type and a static
// file server.
package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

type itemsHandler struct{}

func (itemsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

func main() {
	r := mux.NewRouter()
	r.Use(logging)
	r.HandleFunc("/", home).Methods(http.MethodGet)
	r.HandleFunc("/notes", createNote).Methods("POST").Name("createNote")
	r.HandleFunc("/notes/{id}", note).Methods("GET", "PUT")
	api := r.PathPrefix("/api").Subrouter()
	api.Use(requireAuth)
	api.HandleFunc("/me", me)
	api.Handle("/items", itemsHandler{}).Methods("GET")
	r.Methods("DELETE").Path("/notes/{id}").HandlerFunc(deleteNote)
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.Handle("/", r)
	_ = http.ListenAndServe(":8080", nil)
}

func home(w http.ResponseWriter, r *http.Request)       {}
func createNote(w http.ResponseWriter, r *http.Request) {}
func note(w http.ResponseWriter, r *http.Request)       {}
func me(w http.ResponseWriter, r *http.Request)         {}
func deleteNote(w http.ResponseWriter, r *http.Request) {}

// authenticate checks the request's bearer token.
func authenticate(r *http.Request) bool { return r.Header.Get("Authorization") != "" }

func logging(next http.Handler) http.Handler { return next }

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
