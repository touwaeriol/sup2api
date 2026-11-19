package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// AC 21: the console lists the models an account can reach from its
// upstream (CONTRACTS §19): before the account is saved (credentials from
// the form) and for a saved account; upstream failures are reported as 503.
func TestAC21_FetchUpstreamModels(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", "")
	e.EnsurePlugin(admin, "openai", "")
	e.EnsurePlugin(admin, "gemini", "")

	want := "claude-opus-4-1,claude-sonnet-4-5,gemini-2.5-pro,gpt-4.1" // sorted, as the mock lists them
	ids := func(models []string) string { return strings.Join(models, ",") }

	// From the form, one call per built-in account type.
	for _, plugin := range []string{AnthropicPlugin, OpenAIPlugin, GeminiPlugin} {
		d := admin.OK(t, http.MethodPost, "/account-types/"+plugin+"/"+APIKeyType+"/models/fetch", map[string]any{
			"credentials": map[string]any{"api_key": "sk-mock-ac21-" + e.RunID, "base_url": e.MockInternalURL},
		})
		var got []string
		for _, m := range d.Get("models").Array() {
			got = append(got, m.String())
		}
		if ids(got) != want || d.Get("skipped").Int() != 0 {
			t.Fatalf("%s: models = %s", plugin, d.Raw)
		}
	}

	// Form credentials still go through the plugin's validation (no api_key).
	admin.Expect(t, 400, "invalid_argument", http.MethodPost, "/account-types/"+AnthropicPlugin+"/"+APIKeyType+"/models/fetch",
		map[string]any{"credentials": map[string]any{"base_url": e.MockInternalURL}})

	// Saved account; the upstream rejecting the key (marker -status-401)
	// surfaces as 503 unavailable with the upstream status.
	gid := e.CreateGroup(admin, e.Name("grp"), "public", 1, nil)
	id := e.CreateAccount(admin, AccountSpec{GroupIDs: []int64{gid}, APIKey: "sk-mock-ac21-saved-" + e.RunID})
	d := admin.OK(t, http.MethodPost, fmt.Sprintf("/accounts/%d/models/fetch", id), nil)
	if d.Get("models.#").Int() != 4 {
		t.Fatalf("saved account models: %s", d.Raw)
	}
	r := admin.Expect(t, 503, "unavailable", http.MethodPost, fmt.Sprintf("/accounts/%d/models/fetch", id),
		map[string]any{"credentials": map[string]any{"api_key": "sk-mock-ac21-status-401", "base_url": e.MockInternalURL}})
	if r.JSON().Get("error.details.status").Int() != 401 {
		t.Fatalf("upstream status not reported: %s", r)
	}
}
