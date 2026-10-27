// Package ui renders the shared browser pages. Pages are script-free except
// those that define a "scripts" block (passkey ceremonies), which load only a
// same-origin script under a matching Content-Security-Policy.
package ui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"html/template"
	"net/http"
)

//go:embed layout.html
var layout string

//go:embed icon.png
var icon []byte

// ImageSource is the CSP directive pages need for the inline icon. Inline
// data keeps pages free of extra routes that proxies would have to forward.
const ImageSource = "img-src data:"

var iconURL = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(icon))

// NewTemplate adds trusted title and content definitions to the shared layout.
// All request data must be passed to Execute, never interpolated into source.
func NewTemplate(source string) *template.Template {
	funcs := template.FuncMap{"icon": func() template.URL { return iconURL }}
	return template.Must(template.Must(template.New("layout").Funcs(funcs).Parse(layout)).Parse(source))
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
