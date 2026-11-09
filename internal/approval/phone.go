package approval

import (
	"html/template"
	"strings"

	"github.com/gkdis6/opsgate/internal/qrcode"
)

// phoneQR returns a QR code that opens path on this server's public origin,
// for a person reading the page on a computer whose passkey is on a phone,
// or who wants the inbox and its notifications on the phone. The link holds
// no secret: the phone still signs in as the same user.
func (m *Manager) phoneQR(path string) template.HTML {
	if !strings.HasPrefix(m.publicURL, "https://") || !strings.HasPrefix(path, "/") {
		return ""
	}
	svg, err := qrcode.SVG(m.publicURL+path, "QR code that opens this page on a phone")
	if err != nil {
		return ""
	}
	return svg
}

// phoneBlock is the shared markup that shows a page's QR code.
const phoneBlock = `{{if .QR}}<details class="target"><summary>Open on your phone / 폰에서 열기</summary>{{.QR}}
<p class="note">Point the phone camera at the code and sign in there with your passkey.<br>폰 카메라로 코드를 비춘 뒤, 열린 페이지에서 패스키로 로그인하세요.</p></details>{{end}}`
