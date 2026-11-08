package analyzer

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gkdis6/opsgate/internal/action"
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
			URL     string `json:"url"`
			Actions []struct {
				URL string `json:"url"`
			} `json:"actions"`
		}
		if json.Unmarshal([]byte(raw), &body) != nil {
			continue
		}
		urls := []string{}
		if body.URL != "" {
			urls = append(urls, body.URL)
		}
		for _, item := range body.Actions {
			urls = append(urls, item.URL)
		}
		for _, link := range urls {
			u, err := url.Parse(link)
			if err != nil || len(link) > 16384 || u.Scheme != "https" || u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil || u.Fragment != "" {
				continue
			}
			if !strings.HasPrefix(u.Path, "/approve/action/") && !strings.HasPrefix(u.Path, "/approve/restore-image/") {
				continue
			}
			entry, args, source, err := action.Parse(u)
			if err != nil {
				continue
			}
			// Reverting to an unverified image is only offered as an undo.
			if revert, _ := args["revert"].(bool); revert {
				continue
			}
			label := entry.Label(c.Report.Language)
			if source == "ai" {
				if c.Report.Language == "en" {
					label = "AI suggestion: " + label
				} else {
					label = "AI 제안: " + label
				}
			}
			duplicate := false
			for _, old := range links {
				if old.URL == link {
					duplicate = true
				}
			}
			if !duplicate {
				links = append(links, alert.ActionLink{Label: label, URL: link})
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
