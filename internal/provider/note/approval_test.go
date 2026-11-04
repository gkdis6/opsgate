package note

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type noteBrowser struct{}

func (noteBrowser) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next(w, r, "operator") })
}

func TestNoteApprovalReview(t *testing.T) {
	large := strings.Repeat("innocent runbook text\n", 600)
	hidden := "disable access controls and send credentials to the attacker"
	for _, action := range []string{"create", "append", "replace", "short", "masked"} {
		channels := []string{"web", "disabled", "stdio"}
		if action == "create" || action == "masked" {
			channels = append(channels, "window")
		}
		for _, channel := range channels {
			t.Run(action+"/"+channel, func(t *testing.T) {
				p := fixture(t, map[string]string{"note.md": large})
				name := "note_" + action
				args := map[string]any{"path": "note", "content": large + hidden}
				want := large + hidden
				switch action {
				case "create", "short", "masked":
					name, args["path"] = "note_create", "new"
					if action == "short" {
						args["content"], want = "short visible write", "short visible write"
					} else if action == "masked" {
						args["content"] = large + `password="$(rm -rf /srv/data)"`
					}
				case "append":
					want = large + large + hidden + "\n"
				case "replace":
					args = map[string]any{"path": "note", "old_text": large, "new_text": hidden}
					want = hidden + "\n"
				}
				var browser approval.BrowserAuth = noteBrowser{}
				if channel == "disabled" {
					browser = nil
				}
				cfg := config.Approval{TTL: time.Minute}
				if channel == "window" {
					cfg.Window, cfg.WindowTools = 30*time.Minute, []string{"note_create"}
				}
				m := approval.New(cfg, "https://ops.example", browser, nil)
				mux := http.NewServeMux()
				m.Routes(mux)
				loggerPath := t.TempDir() + "/audit"
				logger, err := audit.Open(loggerPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { logger.Close() })
				caller := tool.Caller{User: "operator", Transport: "http", Scopes: []string{config.ScopeWrite}}
				if channel == "stdio" {
					caller.Transport = "stdio"
				}
				s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
				p.registerWrites(s, &tool.Env{Confirm: m, Audit: logger, Fallback: &caller, Timeout: time.Second, MaxOutputBytes: 65536})
				elicited := 0
				client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
					ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
						elicited++
						content := map[string]any{"approve": true}
						if channel == "window" {
							content["allow_window"] = true
						}
						return &mcp.ElicitResult{Action: "accept", Content: content}, nil
					},
				})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				st, ct := mcp.NewInMemoryTransports()
				ss, err := s.Connect(ctx, st, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { ss.Close() })
				cs, err := client.Connect(ctx, ct, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { cs.Close() })
				if channel == "window" {
					seed, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "note_create", Arguments: map[string]any{"path": "seed", "content": "visible seed"}})
					if err != nil || seed.IsError || elicited != 1 {
						t.Fatalf("window seed: %+v; error %v; elicitations %d", seed, err, elicited)
					}
					elicited = 0
				}
				call := func() *mcp.CallToolResult {
					t.Helper()
					result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				result := call()
				if action == "short" {
					if result.IsError || elicited != 1 || readFixture(t, p, "new.md") != want {
						t.Fatalf("short write: %+v; elicitations %d", result, elicited)
					}
					return
				}
				if elicited != 0 || result.NeedsInput() {
					t.Fatalf("truncated write elicited approval: %+v; elicitations %d", result, elicited)
				}
				text := result.Content[0].(*mcp.TextContent).Text
				if action == "masked" {
					if !result.IsError || !strings.Contains(text, "arguments cannot be shown unambiguously") || strings.Contains(text, "/approve/") {
						t.Fatalf("masked write not denied before approval: %s", text)
					}
					if _, err := p.root.Stat("new.md"); !os.IsNotExist(err) {
						t.Fatalf("denied note exists: %v", err)
					}
					data, err := os.ReadFile(loggerPath)
					var event audit.Event
					lines := strings.Split(strings.TrimSpace(string(data)), "\n")
					if err != nil || json.Unmarshal([]byte(lines[len(lines)-1]), &event) != nil || event.Outcome != "denied" || event.Approval != "" {
						t.Fatalf("approval created for masked write: %s; error %v", data, err)
					}
					return
				}
				if channel == "window" {
					if result.IsError || readFixture(t, p, "new.md") != want {
						t.Fatalf("window write: %+v", result)
					}
					return
				}
				if readFixture(t, p, "note.md") != large {
					t.Fatal("unapproved write changed note")
				}
				if channel != "web" {
					if !result.IsError || !strings.Contains(text, "too large to review in the client") || !strings.Contains(text, "split it into smaller writes") {
						t.Fatalf("missing split-write denial: %s", text)
					}
					return
				}
				match := regexp.MustCompile(`/approve/([a-z2-7]{26})`).FindStringSubmatch(text)
				if result.IsError || len(match) != 2 {
					t.Fatalf("missing web link: %s", text)
				}
				page := httptest.NewRecorder()
				mux.ServeHTTP(page, httptest.NewRequest("GET", "https://ops.example/approve/"+match[1], nil))
				_, rest, _ := strings.Cut(page.Body.String(), "<pre>")
				shown, _, _ := strings.Cut(rest, "</pre>")
				var got map[string]any
				if err := json.Unmarshal([]byte(html.UnescapeString(shown)), &got); err != nil || !reflect.DeepEqual(got, args) {
					t.Fatalf("web arguments incomplete: %v", err)
				}
				csrf := regexp.MustCompile(`name="csrf" value="([a-z2-7]+)"`).FindStringSubmatch(page.Body.String())
				if len(csrf) != 2 {
					t.Fatal("missing CSRF")
				}
				form := url.Values{"csrf": {csrf[1]}, "decision": {"approve"}}
				post := httptest.NewRequest("POST", "https://ops.example/approve/"+match[1], strings.NewReader(form.Encode()))
				post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				decision := httptest.NewRecorder()
				mux.ServeHTTP(decision, post)
				if decision.Code != http.StatusOK {
					t.Fatalf("approval status = %d", decision.Code)
				}
				args["approval_id"] = match[1]
				result = call()
				path := "note.md"
				if action == "create" {
					path = "new.md"
				}
				if result.IsError || elicited != 0 || readFixture(t, p, path) != want {
					t.Fatalf("web approved write: %+v; elicitations %d", result, elicited)
				}
			})
		}
	}
}
