package webui

import (
	"embed"
	"html/template"
	"net/http"
	"path"
	"strings"
)

//go:embed assets/* templates/*
var files embed.FS
var templates = template.Must(template.ParseFS(files, "templates/*.html"))

func Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.ExecuteTemplate(w, name, data)
}
func ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/assets/ui/")
	if path.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	b, err := files.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == "GET" {
		w.Write(b)
	}
}
