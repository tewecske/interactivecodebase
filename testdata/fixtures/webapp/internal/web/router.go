// Package web is the HTTP adapter: routes, guards, handlers and rendering.
package web

import (
	"crypto/rand"
	"net/http"

	"example.com/webapp/internal/lang"
	"example.com/webapp/internal/notes"
	"example.com/webapp/internal/weather"
	"example.com/webapp/static"
)

// Deps are the application services the HTTP adapter uses.
type Deps struct {
	Auth      *Authenticator
	Notes     *notes.Service
	Weather   *weather.Client
	Providers []string
	ExportDir string
}

// New builds the HTTP handler.
func New(d Deps) http.Handler {
	r := newRenderer()
	acct := &accountHandler{auth: d.Auth, render: r}
	notesH := &noteHandler{auth: d.Auth, notes: d.Notes, render: r}
	admin := &adminHandler{auth: d.Auth, notes: d.Notes, exportDir: d.ExportDir}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", redirectToDefault)
	mux.HandleFunc("GET /healthz", health)
	mux.Handle("GET /static/{path...}", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))

	for _, code := range lang.Codes() {
		prefix := "/" + string(code)
		mux.HandleFunc("GET "+prefix+"/sign-in", acct.signIn)
		mux.HandleFunc("POST "+prefix+"/sign-in", acct.signIn)
		mux.HandleFunc("GET "+prefix+"/notes", notesH.list)
		mux.HandleFunc("POST "+prefix+"/notes", notesH.create)
		mux.HandleFunc("GET "+prefix+"/notes/{id}", notesH.detail)
		mux.HandleFunc("POST "+prefix+"/notes/{id}/share", notesH.share)
		mux.HandleFunc("GET "+prefix+"/weather", weatherPage(d.Weather, d.Auth, r))
		mux.HandleFunc("GET "+prefix+"/admin/export", admin.export)
		mux.Handle("POST "+prefix+"/admin/reindex", requireAdmin(d.Auth, http.HandlerFunc(admin.reindex)))
	}
	for _, provider := range d.Providers {
		name := provider
		mux.HandleFunc("GET /auth/"+name+"/callback", func(w http.ResponseWriter, req *http.Request) {
			acct.oauthCallback(w, req, name)
		})
	}
	return withRequestID(mux)
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Request-ID", rand.Text())
		next.ServeHTTP(w, req)
	})
}

func redirectToDefault(w http.ResponseWriter, req *http.Request) {
	http.Redirect(w, req, "/"+string(lang.English)+"/sign-in", http.StatusFound)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("ok\n"))
}
