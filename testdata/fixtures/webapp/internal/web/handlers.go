package web

import (
	"net/http"
	"strconv"
	"strings"

	"example.com/webapp/internal/export"
	"example.com/webapp/internal/notes"
	"example.com/webapp/internal/store/postgres"
	"example.com/webapp/internal/weather"
)

// langOf returns the language segment of a /{lang}/... path.
func langOf(req *http.Request) string {
	code, _, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
	return code
}

type accountHandler struct {
	auth   *Authenticator
	render *renderer
}

// signIn shows the form, redirecting signed-in users, and starts a session on POST.
func (h *accountHandler) signIn(w http.ResponseWriter, req *http.Request) {
	l := langOf(req)
	if req.Method == http.MethodGet {
		if _, err := h.auth.Authenticate(req); err == nil {
			http.Redirect(w, req, "/"+l+"/notes", http.StatusSeeOther)
			return
		}
		h.render.page(w, "signin.html", signInPage{Lang: l, ActionURL: "/" + l + "/sign-in"})
		return
	}
	id, err := h.auth.sessions.CreateSession(req.Context(), req.PostFormValue("email"))
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, HttpOnly: true})
	http.Redirect(w, req, "/"+l+"/notes", http.StatusSeeOther)
}

func (h *accountHandler) oauthCallback(w http.ResponseWriter, req *http.Request, provider string) {
	if req.URL.Query().Get("code") == "" {
		http.Error(w, "missing code from "+provider, http.StatusBadRequest)
		return
	}
	http.Redirect(w, req, "/en/notes", http.StatusSeeOther)
}

type noteHandler struct {
	auth   *Authenticator
	notes  *notes.Service
	render *renderer
}

// user is the in-handler guard: it redirects to sign-in when there is no session.
func (h *noteHandler) user(w http.ResponseWriter, req *http.Request) (postgres.SessionUser, bool) {
	u, err := h.auth.Authenticate(req)
	if err != nil {
		http.Redirect(w, req, "/"+langOf(req)+"/sign-in", http.StatusSeeOther)
		return postgres.SessionUser{}, false
	}
	return u, true
}

func (h *noteHandler) list(w http.ResponseWriter, req *http.Request) {
	u, ok := h.user(w, req)
	if !ok {
		return
	}
	list, err := h.notes.List(req.Context(), u.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	l := langOf(req)
	page := notesPage{Lang: l, CreateURL: "/" + l + "/notes"}
	for _, n := range list {
		page.Notes = append(page.Notes, noteLink{Title: n.Title, URL: "/" + l + "/notes/" + strconv.FormatInt(n.ID, 10)})
	}
	h.render.page(w, "notes.html", page)
}

// create checks configuration before the guard runs.
func (h *noteHandler) create(w http.ResponseWriter, req *http.Request) {
	if h.notes == nil {
		http.Error(w, "notes disabled", http.StatusServiceUnavailable)
		return
	}
	u, ok := h.user(w, req)
	if !ok {
		return
	}
	n, err := h.notes.Create(req.Context(), u.ID, req.PostFormValue("title"), req.PostFormValue("body"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("HX-Redirect", "/"+langOf(req)+"/notes/"+strconv.FormatInt(n.ID, 10))
	w.WriteHeader(http.StatusCreated)
}

func (h *noteHandler) detail(w http.ResponseWriter, req *http.Request) {
	if _, ok := h.user(w, req); !ok {
		return
	}
	id, _ := strconv.ParseInt(req.PathValue("id"), 10, 64)
	n, err := h.notes.Get(req.Context(), id)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	l := langOf(req)
	h.render.page(w, "note.html", notePage{Lang: l, Title: n.Title, Body: n.Body,
		ShareURL: "/" + l + "/notes/" + req.PathValue("id") + "/share", BackURL: "/" + l + "/notes"})
}

func (h *noteHandler) share(w http.ResponseWriter, req *http.Request) {
	if _, ok := h.user(w, req); !ok {
		return
	}
	id, _ := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err := h.notes.Share(req.Context(), id, req.PostFormValue("to")); err != nil {
		http.Error(w, "could not share", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// weatherPage is a handler factory: public, personalised when signed in.
func weatherPage(client *weather.Client, auth *Authenticator, r *renderer) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		page := weatherView{Lang: langOf(req)}
		if u, err := auth.Authenticate(req); err == nil {
			page.Email = u.Email
		}
		f, err := client.Forecast(req.Context(), req.URL.Query().Get("city"))
		if err != nil {
			http.Error(w, "forecast unavailable", http.StatusBadGateway)
			return
		}
		page.Forecast = f
		r.page(w, "weather.html", page)
	}
}

type adminHandler struct {
	auth      *Authenticator
	notes     *notes.Service
	exportDir string
}

// export guards itself; reindex relies on the requireAdmin middleware.
func (h *adminHandler) export(w http.ResponseWriter, req *http.Request) {
	u, err := h.auth.Authenticate(req)
	if err != nil || !u.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	list, err := h.notes.List(req.Context(), u.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	path, err := export.WriteCSV(req.Context(), h.exportDir, "notes", list)
	if err != nil {
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	http.ServeFile(w, req, path)
}

func (h *adminHandler) reindex(w http.ResponseWriter, req *http.Request) {
	if _, err := h.notes.List(req.Context(), 0); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
