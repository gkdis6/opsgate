package qrcode

import (
	"strings"
	"testing"
)

func TestSVGAndTerminal(t *testing.T) {
	svg, err := SVG("https://ops.example.com/approvals", `Open "inbox"`)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	if !strings.HasPrefix(s, "<svg ") || !strings.Contains(s, `aria-label="Open &#34;inbox&#34;"`) || strings.Contains(s, "style=") || !strings.Contains(s, "h1v1h-1z") {
		t.Fatalf("svg: %.200s", s)
	}
	term, err := Terminal("https://ops.example.com/approvals")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(term, "\n"), "\n")
	// Version 3 at level L is 29 modules; with the quiet zone 37 wide, 19 lines.
	if len(lines) < 15 || !strings.HasPrefix(lines[0], "\x1b[30;47m") || !strings.HasSuffix(lines[0], "\x1b[0m") || !strings.ContainsAny(term, "▀▄█") {
		t.Fatalf("terminal: %d lines %q", len(lines), lines[0])
	}
	if _, err := SVG(strings.Repeat("x", 5000), "too long"); err == nil {
		t.Fatal("encoded text beyond QR capacity")
	}
}
