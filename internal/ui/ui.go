// Package ui renders the shared, script-free browser pages.
package ui

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed layout.html
var layout string

// NewTemplate adds trusted title and content definitions to the shared layout.
// All request data must be passed to Execute, never interpolated into source.
func NewTemplate(source string) *template.Template {
	return template.Must(template.Must(template.New("layout").Parse(layout)).Parse(source))
}

// Render buffers a page before writing the response so render errors cannot
// expose a partially rendered form.
func Render(w http.ResponseWriter, status int, page *template.Template, data any) {
	var body bytes.Buffer
	if err := page.Execute(&body, data); err != nil {
		http.Error(w, "Cannot render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

var messagePage = NewTemplate(`{{define "title"}}{{.Title}}{{end}}
{{define "content"}}<h1>{{.Title}}</h1><p>{{.Message}}</p>{{end}}`)

// Message renders a short informational or error page in the shared layout.
func Message(w http.ResponseWriter, status int, title, message string) {
	Render(w, status, messagePage, struct{ Title, Message string }{title, message})
}
