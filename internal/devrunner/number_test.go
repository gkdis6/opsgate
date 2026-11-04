package devrunner

import (
	"net/http/httptest"
	"testing"
)

func TestNumberBounds(t *testing.T) {
	const maximum = 100
	for _, tc := range []struct {
		query string
		want  int64
		valid bool
	}{
		{query: "", want: 20, valid: true},
		{query: "?n=0", want: 0, valid: true},
		{query: "?n=100", want: 100, valid: true},
		{query: "?n=101"},
		{query: "?n=-1"},
		{query: "?n=2147483648"},
		{query: "?n=9223372036854775807"},
		{query: "?n=9223372036854775808"},
		{query: "?n=abc"},
		{query: "?n=1&n=2"},
	} {
		got, err := number(httptest.NewRequest("GET", "/"+tc.query, nil), "n", 20, maximum)
		if (err == nil) != tc.valid || got != tc.want {
			t.Errorf("%q: got %d, err %v", tc.query, got, err)
		}
	}
}
