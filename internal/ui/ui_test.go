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
