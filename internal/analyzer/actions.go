package analyzer

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func remediationLinks(c Config, out *mcp.CallToolResult) []alert.ActionLink {
	if c.Report.ApprovalURL == "" || !c.Report.SuggestActions {
		return nil
	}
	origin, err := url.Parse(c.Report.ApprovalURL)
	if err != nil {
		return nil
	}
	var links []alert.ActionLink
	for _, part := range out.Content {
		text, ok := part.(*mcp.TextContent)
		if !ok {
			continue
		}
		raw := text.Text
		if strings.HasPrefix(raw, "cluster: ") {
			_, rest, found := strings.Cut(raw, "\n")
			if !found {
				continue
			}
			raw = rest
		}
		var body struct {
			Actions []struct {
				URL string `json:"url"`
			} `json:"actions"`
		}
		if json.Unmarshal([]byte(raw), &body) != nil {
			continue
		}
		for _, action := range body.Actions {
			u, err := url.Parse(action.URL)
			if err != nil || len(action.URL) > 512 || u.Scheme != "https" || u.Host != origin.Host || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.Path, "/approve/restore-image/") {
				continue
			}
			label := "조치 승인: 이미지 복구"
			if c.Report.Language == "en" {
				label = "Approve action: restore image"
			}
			duplicate := false
			for _, old := range links {
				if old.URL == action.URL {
					duplicate = true
				}
			}
			if !duplicate {
				links = append(links, alert.ActionLink{Label: label, URL: action.URL})
			}
			if len(links) >= 5 {
				return links
			}
		}
	}
	return links
}

func mergeLinks(current, next []alert.ActionLink) []alert.ActionLink {
	for _, action := range next {
		found := false
		for _, old := range current {
			if old.URL == action.URL {
				found = true
			}
		}
		if !found && len(current) < 5 {
			current = append(current, action)
		}
	}
	return current
}
