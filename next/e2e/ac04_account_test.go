package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// AC 4: create an account "Anthropic / API Key" through the plugin-provided
// form; it saves and credentials are masked.
func TestAC04_CreateAnthropicAccount(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	e.EnsurePlugin(admin, "anthropic", "")

	types := admin.OK(t, http.MethodGet, "/account-types", nil).Array()
	at, ok := FindAccountType(types, AnthropicPlugin, AnthropicAPIKey)
	if !ok {
		t.Fatalf("anthropic/apikey not offered: %v", types)
	}
	if at.Get("form.mode").String() == "" {
		t.Fatalf("account type without form mode: %s", at.Raw)
	}
	sens := map[string]bool{}
	for _, f := range at.Get("sensitive_fields").Array() {
		sens[f.String()] = true
	}
	if !sens["api_key"] {
		t.Fatalf("api_key must be a sensitive field: %s", at.Raw)
	}

	// The form is the plugin's native component; the core serves no schema.
	ExpectNativeForm(t, admin.Client, at, "AnthropicKeyForm")

	// The plugin's validation: missing api_key -> invalid_argument with field details.
	gid := e.CreateGroup(admin, e.Name("grp"), "public", 1, nil)
	r := admin.API(t, http.MethodPost, "/accounts", map[string]any{
		"name": e.Name("bad"), "plugin_key": AnthropicPlugin, "type": AnthropicAPIKey, "group_ids": []int64{gid},
		"priority": 10, "max_concurrency": 1, "schedulable": true, "credentials": map[string]any{"base_url": e.MockInternalURL},
	})
	if r.Status != 400 || r.ErrCode() != "invalid_argument" {
		t.Fatalf("missing api_key: %s", r)
	}

	secret := "sk-ant-mock-ac04-" + e.RunID
	id := e.CreateAccount(admin, AccountSpec{GroupIDs: []int64{gid}, APIKey: secret})

	got := admin.OK(t, http.MethodGet, fmt.Sprintf("/accounts/%d", id), nil)
	if got.Get("credentials.api_key").String() != "******" {
		t.Fatalf("api_key not masked: %s", got.Raw)
	}
	if got.Get("credentials.base_url").String() != e.MockInternalURL {
		t.Fatalf("base_url (not sensitive) should be visible: %s", got.Raw)
	}
	if got.Get("plugin_key").String() != AnthropicPlugin || got.Get("type").String() != AnthropicAPIKey || got.Get("platform").Exists() {
		t.Fatalf("account identity: %s", got.Raw)
	}

	// Listed with runtime fields.
	list := admin.ListAll(t, "/accounts", "plugin_key", AnthropicPlugin, "type", AnthropicAPIKey)
	row, ok := Find(list, "id", id)
	if !ok {
		t.Fatal("new account not listed")
	}
	for _, f := range []string{"in_use", "orphaned", "type_label"} {
		if !row.Get(f).Exists() {
			t.Errorf("account list row lacks %s: %s", f, row.Raw)
		}
	}

	// PATCH with "******" keeps the secret; reveal needs only the permission.
	admin.OK(t, http.MethodPatch, fmt.Sprintf("/accounts/%d", id), map[string]any{
		"name": e.Name("renamed"), "credentials": map[string]any{"api_key": "******", "base_url": e.MockInternalURL},
	})
	rev := admin.OK(t, http.MethodPost, fmt.Sprintf("/accounts/%d/credentials/reveal", id), nil)
	if rev.Get("api_key").String() != secret && rev.Get("credentials.api_key").String() != secret {
		t.Fatalf("revealed credentials: %s", rev.Raw)
	}

	// Test request hits the mock upstream with this key.
	m := e.Mock()
	mark := m.Mark(t)
	res := admin.OK(t, http.MethodPost, fmt.Sprintf("/accounts/%d/test", id), map[string]any{})
	if !res.Get("ok").Bool() {
		t.Fatalf("account test failed: %s", res.Raw)
	}
	keys := KeysUsed(m.Since(t, mark), "")
	if len(keys) == 0 || keys[len(keys)-1] != secret {
		t.Fatalf("account test did not reach the mock with the account key: %v", keys)
	}
}
