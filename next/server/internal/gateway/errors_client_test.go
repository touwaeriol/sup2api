package gateway

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Anthropic clients read "no available account" as the API's 529 overload,
// except when model lists leave the request out; other formats, and errors
// relaying an upstream body, are shown as they are.
func TestClientError(t *testing.T) {
	noAccount := fromCore(core.ErrNoAvailableAccount, errTypeNoAccount)
	relayed := &gwError{Status: 503, Code: core.ErrNoAvailableAccount.Code, Raw: []byte(`{}`)}
	for _, tc := range []struct {
		name, format string
		err          *gwError
		status       int
		typ          string
	}{
		{"no available account", FormatAnthropic, noAccount, statusOverloaded, "overloaded_error"},
		{"model lists leave it out", FormatAnthropic, errModelNotServed, 503, "api_error"},
		{"openai format", FormatOpenAI, noAccount, 503, ""},
		{"openai format, model lists", FormatOpenAI, errModelNotServed, 503, ""},
		{"relayed body", FormatAnthropic, relayed, 503, ""},
	} {
		got := clientError(tc.format, tc.err)
		if got.Status != tc.status || got.Type != tc.typ || got.Code != core.ErrNoAvailableAccount.Code {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	if errModelNotServed.Type != "" || errModelNotServed.Status != 503 {
		t.Errorf("the shared error was changed: %+v", errModelNotServed)
	}
}
