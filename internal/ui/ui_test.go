package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageEscapesText(t *testing.T) {
	w := httptest.NewRecorder()
	Message(w, 403, "<script>title</script>", "<script>message</script>")
	body := w.Body.String()
	if w.Code != 403 || strings.Contains(strings.ToLower(body), "<script") || !strings.Contains(body, "&lt;script&gt;message&lt;/script&gt;") {
		t.Fatalf("message: %d %s", w.Code, body)
	}
}

func TestLayoutIcon(t *testing.T) {
	w := httptest.NewRecorder()
	Message(w, 200, "Title", "Message")
	body := w.Body.String()
	if strings.Count(body, `"data:image/png;base64,`) != 2 || !strings.Contains(body, `<link rel="icon" type="image/png"`) {
		t.Fatalf("icon missing: %s", body[:min(len(body), 400)])
	}
}
