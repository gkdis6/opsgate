package approval

import (
	"net/url"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/tool"
)

// splitUndo separates a stored result from its undo link. The link becomes a
// button only if it is a catalog action link on this server; it is returned
// as a local path so the button can never leave the approval origin.
func (m *Manager) splitUndo(result string) (text, local, full string) {
	text, full = tool.SplitUndo(result)
	if full == "" {
		return result, "", ""
	}
	u, err := url.Parse(full)
	if err != nil {
		return text, "", ""
	}
	if u.Scheme != "" || u.Host != "" {
		origin, err := url.Parse(m.publicURL)
		if err != nil || origin.Host == "" || u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil {
			return text, "", ""
		}
	}
	if _, _, _, err := action.Parse(u); err != nil {
		return text, "", ""
	}
	return text, u.RequestURI(), full
}
