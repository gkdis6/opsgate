package dev

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestClientRejectsInvalidIdentity(t *testing.T) {
	for _, user := range []string{"", " owner", "owner ", "owner\nadmin", "owner\tadmin", "\xff", strings.Repeat("a", 257)} {
		t.Run(user, func(t *testing.T) {
			p := fakeProvider(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid identity reached runner") }))
			var out any
			if err := p.request(context.Background(), tool.Caller{User: user}, "GET", "/v1/tasks", nil, &out); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}
