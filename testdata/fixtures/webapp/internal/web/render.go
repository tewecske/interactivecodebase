package web

import (
	"html/template"
	"net/http"

	"example.com/webapp/internal/weather"
	"example.com/webapp/templates"
)

type renderer struct {
	tmpl *template.Template
}

func newRenderer() *renderer {
	return &renderer{tmpl: template.Must(template.ParseFS(templates.FS, "*.html"))}
}

func (r *renderer) page(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

type signInPage struct {
	Lang      string
	ActionURL string
}

type notesPage struct {
	Lang      string
	CreateURL string
	Notes     []noteLink
}

type noteLink struct {
	Title string
	URL   string
}

type notePage struct {
	Lang     string
	Title    string
	Body     string
	ShareURL string
	BackURL  string
}

type weatherView struct {
	Lang     string
	Email    string
	Forecast weather.Forecast
}
