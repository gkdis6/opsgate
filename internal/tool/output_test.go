package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
)

func TestRedactionBeforeTruncation(t *testing.T) {
	for _, secret := range []struct{ name, text, want string }{
		{"PEM", "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("private-material", 50) + "\n-----END RSA PRIVATE KEY-----", redact.Mask},
		{"quoted password", `password="` + strings.Repeat("private material ", 50) + `"`, `password="` + redact.Mask + `"`},
		{"ceiling", "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("private-material", maxProviderOutputBytes/16+100) + "\n-----END PRIVATE KEY-----", "[output exceeds 4 MiB limit; omitted by opsgate]"},
	} {
		for _, write := range []bool{false, true} {
			for _, failure := range []bool{false, true} {
				t.Run(secret.name+map[bool]string{false: "/read", true: "/write"}[write]+map[bool]string{false: "/output", true: "/error"}[failure], func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "audit")
					logger, err := audit.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { logger.Close() })
					env := &Env{Audit: logger, MaxOutputBytes: 128, Timeout: time.Second,
						Fallback: &Caller{User: "alice", Transport: "stdio", Scopes: config.Scopes},
						Confirm:  confirmFunc(func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error) { return "test", nil }),
					}
					payload := "prefix " + secret.text + " suffix"
					answer := func() (string, error) {
						if failure {
							return "", errors.New(payload)
						}
						return payload, nil
					}
					var cs *mcp.ClientSession
					var args any
					if write {
						cs = connectWrite(t, env, WriteSpec{Name: "check"}, WriteHandler[writeTestInput]{
							Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
								return Plan{Summary: "change target"}, nil
							},
							Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) { return answer() },
						})
						args = map[string]any{"target": "service", "password": ""}
					} else {
						cs = connectTool(t, env, Spec{Name: "check", ReadOnly: true}, func(context.Context, Caller, testInput) (string, error) { return answer() })
						args = map[string]any{"message": "", "password": ""}
					}
					result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: args})
					if err != nil {
						t.Fatal(err)
					}
					want := "prefix " + secret.want + " suffix"
					if secret.name == "ceiling" {
						want = secret.want
					}
					text := result.Content[0].(*mcp.TextContent).Text
					if result.IsError != failure || text != want {
						t.Fatalf("error = %v; text = %q, want %q", result.IsError, text, want)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var ev audit.Event
					if err := json.Unmarshal(data, &ev); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(data), "private") || failure && ev.Error != want {
						t.Fatalf("unsafe audit = %s", data)
					}
				})
			}
		}
	}
}
