// Package qrcode draws links as QR codes so a person can open an approval
// page or the inbox on a phone by pointing its camera at a screen.
package qrcode

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// quiet is the blank border, in modules, that scanners need around a code.
const quiet = 4

// SVG returns an inline SVG of text as a QR code with a white background.
// It uses only presentation attributes, so a strict style-src still applies.
func SVG(text, label string) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	n := code.Size + 2*quiet
	var path strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	return template.HTML(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="200" height="200" role="img" aria-label="%s" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		n, n, template.HTMLEscapeString(label), n, n, path.String())), nil
}

// Terminal returns text as a QR code for a terminal: two module rows per
// line with half blocks, drawn black on an explicit white background so it
// scans on light and dark themes alike.
func Terminal(text string) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	black := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	n := code.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString("\x1b[30;47m")
		for x := 0; x < n; x++ {
			top, bottom := black(x, y), y+1 < n && black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}
