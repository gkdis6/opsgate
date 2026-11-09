// Package ui renders the shared browser pages. Pages are script-free except
// those that define a "scripts" block (passkey ceremonies and the approval inbox), which load only a
// same-origin script under a matching Content-Security-Policy.
package ui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"html/template"
	"image"
	"image/png"
	"net/http"
	"sync"
	"sync/atomic"
)

//go:embed layout.html
var layout string

//go:embed icon.png
var icon []byte

// ImageSource is the CSP directive pages need for the inline icon. Inline
// data keeps pages free of extra routes that proxies would have to forward.
const ImageSource = "img-src data:"

// AppSource is the CSP directive pages need to link the install manifest.
const AppSource = "manifest-src 'self'"

// app records that the server serves the approval inbox manifest, so every
// page can be added to a phone's Home Screen as the same installable app.
var app atomic.Bool

// EnableApp makes every page link the approval inbox's install manifest and
// icon. iPhone delivers Web Push only to apps added to the Home Screen, and
// people often add the page they are on, such as an approval or sign-in page.
func EnableApp() { app.Store(true) }

var iconURL = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(icon))

// NewTemplate adds trusted title and content definitions to the shared layout.
// All request data must be passed to Execute, never interpolated into source.
func NewTemplate(source string) *template.Template {
	funcs := template.FuncMap{"icon": func() template.URL { return iconURL }, "app": app.Load}
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

var installIcon = sync.OnceValue(func() []byte {
	// The shared favicon is 64px. Install manifests need a 512px icon.
	source, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return icon
	}
	scaled := image.NewRGBA(image.Rect(0, 0, 512, 512))
	bounds := source.Bounds()
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			scaled.Set(x, y, source.At(bounds.Min.X+x*bounds.Dx()/512, bounds.Min.Y+y*bounds.Dy()/512))
		}
	}
	var b bytes.Buffer
	if png.Encode(&b, scaled) != nil {
		return icon
	}
	return b.Bytes()
})

// Icon serves the shared application icon for installed approval inboxes.
func Icon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(installIcon())
}
