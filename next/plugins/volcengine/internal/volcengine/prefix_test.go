package volcengine

// Tests for the two account types: which upstream path layout each one gets,
// the path prefixes only the relay type has, and each native Anthropic surface.
//
// The security case is first and is the reason this file exists. The apikey
// type's base_url is restricted to Ark's own endpoints by guardedSettings
// unless the caller holds account:settings:custom; the relay type's cannot be
// (guardedSettings is a URL allow-list and a relay's address is not an
// enumerable set), so on that type the prefixes are the one free-text input
// that ends up inside an upstream address, and a prefix able to move the
// request to another host would let one account's configuration send another
// upstream's traffic.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// relayAccount builds a relay account with an optional pair of prefixes.
func relayAccount(api, video string) *pluginv1.Account {
	s := `{"base_url":"https://relay.test"`
	if api != "" {
		s += `,"api_prefix":` + jsonStr(api)
	}
	if video != "" {
		s += `,"video_api_prefix":` + jsonStr(video)
	}
	acc := account(testKey, s+`}`)
	acc.Type = AccountTypeRelay
	return acc
}

func jsonStr(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func buildFor(t *testing.T, acc *pluginv1.Account, meta *pluginv1.RequestMeta) (*pluginv1.UpstreamRequest, error) {
	t.Helper()
	return New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
		Meta: meta, Account: acc, Body: modelBody(meta.GetModel()),
	})
}

// modelBody is the smallest request body naming model.
func modelBody(model string) []byte { return []byte(`{"model":` + jsonStr(model) + `}`) }

// TestPrefixCannotLeaveTheHost is the point of upstreamURL. Each of these
// values, appended to a base URL, parses as a DIFFERENT origin - "@evil.com"
// demotes the real host to userinfo, "//evil.com" is a protocol-relative URL,
// and an absolute URL replaces everything.
//
// The prefix is written straight into settings here, not through
// ValidateCredentials: that is the whole point. The form rejects these, and
// this asserts the request path refuses them even when the form did not run -
// a row stored by an older version, restored from a backup, or written by any
// future path that skips normalization.
func TestPrefixCannotLeaveTheHost(t *testing.T) {
	for _, bad := range []string{"@evil.com/v3", "//evil.com/v3", "https://evil.com/v3", "@evil.com"} {
		t.Run(bad, func(t *testing.T) {
			r, err := buildFor(t, relayAccount(bad, ""), &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
			if err == nil {
				t.Fatalf("accepted %q and would have sent %s", bad, r.GetUrl())
			}
			if !strings.Contains(err.Error(), FieldAPIPrefix) {
				t.Errorf("the error should name %s so an operator knows what to fix: %v", FieldAPIPrefix, err)
			}
		})
	}
	// The mirror image: a legitimate prefix must go through, or the check
	// above would be satisfied by refusing everything.
	r, err := buildFor(t, relayAccount("/v1", ""), &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
	if err != nil || r.GetUrl() != "https://relay.test/v1/chat/completions" {
		t.Fatalf("a plain prefix must work: %v %v", r.GetUrl(), err)
	}
	// And the video prefix is checked by the same code on its own field.
	if _, err := buildFor(t, relayAccount("", "@evil.com/v3"),
		&pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"}); err == nil {
		t.Fatal("the video prefix must be checked too")
	}
}

// TestLayoutFollowsTheAccountType is the shape of the feature: the layout is
// the account type, not a guess from the base URL. The apikey type is Ark and
// serves Ark's paths; the relay type defaults to the standard ones. Both were
// measured against the real services.
//
// The previous version derived this from the host of base_url, which is why
// this test keeps BytePlus in it: under that rule Ark's own overseas endpoint
// had to be special-cased or every BytePlus account would have been treated as
// a relay and sent to /v1/*. Here it is just another apikey account, and that
// is the improvement being asserted.
func TestLayoutFollowsTheAccountType(t *testing.T) {
	official := account(testKey, `{}`)                                 // empty base_url -> the official default
	byteplus := account(testKey, `{"base_url":"`+BytePlusBaseURL+`"}`) // Ark's overseas endpoint, NOT a relay
	// The Ark SDK base URL as pasted: its /api/v3 is left out for the root, so
	// the Ark paths are not doubled and Messages goes to the root's
	// /api/compatible/v1. Only one is left out of a doubled /api/v3.
	pasted := account(testKey, `{"base_url":"`+DefaultBaseURL+`/api/v3"}`)
	doubled := account(testKey, `{"base_url":"`+DefaultBaseURL+`/api/v3/api/v3"}`)
	relay := relayAccount("", "")
	for _, c := range []struct {
		name     string
		acc      *pluginv1.Account
		protocol string
		want     string
	}{
		{"official chat", official, ProtocolChat, DefaultBaseURL + "/api/v3/chat/completions"},
		{"official responses", official, ProtocolResponses, DefaultBaseURL + "/api/v3/responses"},
		{"official embeddings", official, ProtocolEmbeddings, DefaultBaseURL + "/api/v3/embeddings"},
		{"official images", official, ProtocolImages, DefaultBaseURL + "/api/v3/images/generations"},
		{"official video", official, ProtocolVideoSubmit, DefaultBaseURL + "/api/v3/contents/generations/tasks"},
		{"byteplus chat", byteplus, ProtocolChat, BytePlusBaseURL + "/api/v3/chat/completions"},
		{"pasted chat", pasted, ProtocolChat, DefaultBaseURL + "/api/v3/chat/completions"},
		{"pasted images", pasted, ProtocolImages, DefaultBaseURL + "/api/v3/images/generations"},
		{"pasted video", pasted, ProtocolVideoSubmit, DefaultBaseURL + "/api/v3/contents/generations/tasks"},
		{"pasted messages", pasted, ProtocolMessages, DefaultBaseURL + "/api/compatible/v1/messages"},
		{"doubled chat", doubled, ProtocolChat, DefaultBaseURL + "/api/v3/api/v3/chat/completions"},
		{"doubled messages", doubled, ProtocolMessages, DefaultBaseURL + "/api/v3/api/compatible/v1/messages"},

		{"relay chat", relay, ProtocolChat, "https://relay.test/v1/chat/completions"},
		{"relay responses", relay, ProtocolResponses, "https://relay.test/v1/responses"},
		{"relay embeddings", relay, ProtocolEmbeddings, "https://relay.test/v1/embeddings"},
		{"relay images", relay, ProtocolImages, "https://relay.test/v1/images/generations"},
		{"relay messages", relay, ProtocolMessages, "https://relay.test/v1/messages"},
		{"relay video", relay, ProtocolVideoSubmit, "https://relay.test/v1/contents/generations/tasks"},
	} {
		r, err := buildFor(t, c.acc, &pluginv1.RequestMeta{Protocol: c.protocol, Model: "m"})
		if err != nil || r.GetUrl() != c.want {
			t.Errorf("%s: url = %q (%v), want %q", c.name, r.GetUrl(), err, c.want)
		}
	}
}

func TestOfficialNativeAnthropicMessages(t *testing.T) {
	for _, stream := range []bool{false, true} {
		body := []byte(`{"model":"m","stream":` + strconv.FormatBool(stream) + `,"max_tokens":8,"messages":[]}`)
		r, err := New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: account(testKey, `{}`), Meta: &pluginv1.RequestMeta{Protocol: ProtocolMessages, Model: "m", Stream: stream},
			InboundHeaders: map[string]string{"anthropic-version": "2023-06-01", "anthropic-beta": "test-beta"},
			Body:           body,
		})
		if err != nil || r.GetUrl() != DefaultBaseURL+"/api/compatible/v1/messages" {
			t.Fatalf("stream=%v: %v %v", stream, r, err)
		}
		if r.Headers["x-api-key"] != "11111111-2222-3333-4444-555555555555" {
			t.Fatal("wrong upstream API key")
		}
		if r.Headers["authorization"] != "" || len(r.DefaultHeaders) != 0 {
			t.Fatal("wrong auth scheme")
		}
		if r.Headers["anthropic-version"] != "2023-06-01" || r.Headers["anthropic-beta"] != "test-beta" || !bytes.Equal(r.Body, body) {
			t.Fatalf("Anthropic wire was altered: %+v", r)
		}
	}
}

// TestOfficialIgnoresPrefixSettings pins the other half of the split: the
// prefixes are not in the apikey type's settingsFields, so a value that reached
// its settings anyway (a row from before the split, a restored backup) must not
// move an official account's paths.
func TestOfficialIgnoresPrefixSettings(t *testing.T) {
	acc := account(testKey, `{"api_prefix":"/v1","video_api_prefix":"/doubao/api/v3","video_endpoint":"https://other.example/tasks"}`)
	for _, c := range []struct{ protocol, want string }{
		{ProtocolChat, DefaultBaseURL + "/api/v3/chat/completions"},
		{ProtocolVideoSubmit, DefaultBaseURL + "/api/v3/contents/generations/tasks"},
		{ProtocolMessages, DefaultBaseURL + "/api/compatible/v1/messages"},
	} {
		r, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: c.protocol, Model: "m"})
		if err != nil || r.GetUrl() != c.want {
			t.Errorf("%s: url = %q (%v), want %q", c.protocol, r.GetUrl(), err, c.want)
		}
	}
}

// TestRelayNeedsABaseURL: relaySpec has no DefaultBaseURL on purpose. Falling
// back to Ark's address would send a relay's key to Ark, which answers 401 -
// an error that says nothing about the missing setting.
func TestRelayNeedsABaseURL(t *testing.T) {
	acc := account(testKey, `{}`)
	acc.Type = AccountTypeRelay
	_, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
	if err == nil {
		t.Fatal("a relay account with no base_url must be refused")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("the error must name the missing setting: %v", err)
	}
}

// TestRelayMirroringArkPaths covers the case api_prefix exists for: a relay
// that mirrors Ark's own paths under its own host. Everything, Anthropic
// included, is then built under the prefix given.
func TestRelayMirroringArkPaths(t *testing.T) {
	acc := relayAccount("/api/v3", "")
	for _, c := range []struct{ protocol, want string }{
		{ProtocolChat, "https://relay.test/api/v3/chat/completions"},
		{ProtocolMessages, "https://relay.test/api/v3/messages"},
		{ProtocolVideoSubmit, "https://relay.test/api/v3/contents/generations/tasks"},
	} {
		r, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: c.protocol, Model: "m"})
		if err != nil || r.GetUrl() != c.want {
			t.Errorf("%s: url = %q (%v), want %q", c.protocol, r.GetUrl(), err, c.want)
		}
	}
}

// TestPrefixAppliesToEveryProtocol pins an explicit prefix across the whole
// mapping.
func TestPrefixAppliesToEveryProtocol(t *testing.T) {
	acc := relayAccount("/v1", "")
	for _, c := range []struct{ protocol, model, want string }{
		{ProtocolChat, "doubao-seed-1-6-250615", "https://relay.test/v1/chat/completions"},
		{ProtocolChat, "bot-20250101", "https://relay.test/v1/bots/chat/completions"},
		{ProtocolResponses, "m", "https://relay.test/v1/responses"},
		{ProtocolEmbeddings, "m", "https://relay.test/v1/embeddings"},
		{ProtocolImages, "m", "https://relay.test/v1/images/generations"},
		{ProtocolMessages, "m", "https://relay.test/v1/messages"},
		{ProtocolVideoSubmit, "m", "https://relay.test/v1/contents/generations/tasks"},
	} {
		r, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: c.protocol, Model: c.model})
		if err != nil || r.GetUrl() != c.want {
			t.Errorf("%s: url = %q (%v), want %q", c.protocol, r.GetUrl(), err, c.want)
		}
	}
}

// TestVideoPrefixIsIndependent covers the layout that made two settings
// necessary: a relay serving text at the root while keeping Ark's native video
// tasks under its own namespace. This is the configuration verified end to end
// against a real relay (docs §13.8).
func TestVideoPrefixIsIndependent(t *testing.T) {
	acc := relayAccount("/v1", "/doubao/api/v3")
	r, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
	if err != nil || r.GetUrl() != "https://relay.test/v1/chat/completions" {
		t.Fatalf("text: %q %v", r.GetUrl(), err)
	}
	r, err = buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"})
	if err != nil || r.GetUrl() != "https://relay.test/doubao/api/v3/contents/generations/tasks" {
		t.Fatalf("submit: %q %v", r.GetUrl(), err)
	}
	// The reconcile poll is the third caller and the one that would go
	// unnoticed: it runs offline, and a poll built against the wrong path
	// answers 404 for every entry, which the core reads as "still pending"
	// until the deadline and then keeps the estimate - the account is charged
	// its pre-charge for work that really finished and reported real usage.
	rr, err := taskQuery(&pluginv1.ReconcileEntry{RefId: "task_1"}, acc)
	if err != nil || rr.GetUrl() != "https://relay.test/doubao/api/v3/contents/generations/tasks/task_1" {
		t.Fatalf("reconcile: %q %v", rr.GetUrl(), err)
	}
	// An unset video prefix follows the api one, so the common case of a
	// single prefix needs one field.
	r, err = buildFor(t, relayAccount("/v1", ""), &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"})
	if err != nil || r.GetUrl() != "https://relay.test/v1/contents/generations/tasks" {
		t.Fatalf("video following api prefix: %q %v", r.GetUrl(), err)
	}
	// And an official account's reconcile poll uses Ark's own path, reading no
	// setting at all.
	rr, err = taskQuery(&pluginv1.ReconcileEntry{RefId: "task_1"}, account(testKey, `{}`))
	if err != nil || rr.GetUrl() != DefaultBaseURL+"/api/v3/contents/generations/tasks/task_1" {
		t.Fatalf("official reconcile: %q %v", rr.GetUrl(), err)
	}
}

func TestVideoEndpointUsedBySubmissionAndPoll(t *testing.T) {
	for _, endpoint := range []string{"/doubao/api/v3/contents/generations/tasks", "https://video.example/custom/tasks"} {
		settings, _ := json.Marshal(map[string]any{"base_url": "https://relay.test", "api_prefix": "/v1", "video_api_prefix": "/legacy", "video_endpoint": endpoint})
		acc := account(testKey, string(settings))
		acc.Type = AccountTypeRelay
		want := endpoint
		if strings.HasPrefix(endpoint, "/") {
			want = "https://relay.test" + endpoint
		}
		created, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "m"})
		if err != nil || created.Url != want || created.Method != "POST" {
			t.Fatalf("submit %s: %+v %v", endpoint, created, err)
		}
		id := "task/a?x#y"
		poll, err := taskQuery(&pluginv1.ReconcileEntry{RefId: id}, acc)
		if err != nil || poll.Url != want+"/"+url.PathEscape(id) || poll.Method != "GET" {
			t.Fatalf("poll: %+v %v", poll, err)
		}
		chat, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
		if err != nil || chat.Url != "https://relay.test/v1/chat/completions" {
			t.Fatal("video endpoint changed legacy text surface", chat, err)
		}
	}
}

func TestEndpointRejectsAmbiguousURLsAtSaveAndRuntime(t *testing.T) {
	for _, endpoint := range []string{"//evil.test/tasks", "https://user:pass@evil.test/tasks", "/tasks?key=x", "/tasks#frag", " /tasks", "/tasks ", "/a/../tasks", "/a/./tasks", "/a/%2e%2e/tasks", "/a/%252e%252e/tasks", "/a%2f..%2ftasks", "/a%20b/tasks", "/a\\b/tasks", "/a%5cb/tasks", "http:/tasks", "ftp://evil.test/tasks", "tasks", strings.Repeat("a", 2049)} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := resolveEndpoint("https://relay.test", endpoint); err == nil {
				t.Fatal("ambiguous endpoint accepted")
			}
			settings, _ := json.Marshal(map[string]any{"base_url": "https://relay.test", "video_endpoint": endpoint})
			validation := New().validateWithAssets(&pluginv1.ValidateCredentialsRequest{AccountType: AccountTypeRelay, CredentialsJson: testKey, SettingsJson: string(settings)})
			found := false
			for _, e := range validation.Errors {
				found = found || e.Field == FieldVideoEndpoint
			}
			if !found {
				t.Fatalf("no endpoint validation error: %+v", validation)
			}
			acc := account(testKey, string(settings))
			acc.Type = AccountTypeRelay
			if _, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit}); err == nil {
				t.Fatal("runtime accepted bypassed validation")
			}
		})
	}
	for _, tc := range []struct{ base, endpoint, want string }{
		{"https://relay.test", "/", "https://relay.test"},
		{"https://relay.test/prefix/", "/assets", "https://relay.test/prefix/assets"},
		{"https://relay.test", "https://asset.test/tasks/", "https://asset.test/tasks"},
	} {
		got, err := resolveEndpoint(tc.base, tc.endpoint)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %s %v", tc, got, err)
		}
	}
}

// TestUnknownAccountTypeIsRefused: the plugin now has two types and picks the
// credential spec by name, so an unknown one must fail loudly instead of
// silently getting the official layout.
func TestUnknownAccountTypeIsRefused(t *testing.T) {
	acc := account(testKey, `{}`)
	acc.Type = "something-else"
	if _, err := buildFor(t, acc, &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"}); err == nil {
		t.Fatal("an unknown account type must be refused")
	}
}

// TestAnthropicHeaders: Anthropic rejects a request with no version header, so
// one is supplied - but never in place of the client's own, which tells the
// upstream which shape the body is in.
func TestAnthropicHeaders(t *testing.T) {
	h := upstreamHeaders(AccountTypeRelay, ProtocolMessages, "k", nil)
	if h["anthropic-version"] != DefaultAnthropicVersion {
		t.Errorf("version = %q, want the default %q", h["anthropic-version"], DefaultAnthropicVersion)
	}
	h = upstreamHeaders(AccountTypeRelay, ProtocolMessages, "k", map[string]string{
		"anthropic-version": "2026-01-01", "anthropic-beta": "x-1", "user-agent": "ua"})
	if h["anthropic-version"] != "2026-01-01" {
		t.Errorf("the client's version must win: %q", h["anthropic-version"])
	}
	if h["anthropic-beta"] != "x-1" {
		t.Errorf("anthropic-beta must be forwarded: %q", h["anthropic-beta"])
	}
	// The OpenAI surface is unchanged: no version header appears there.
	if _, ok := upstreamHeaders(AccountTypeRelay, ProtocolChat, "k", nil)["anthropic-version"]; ok {
		t.Error("the openai surface must not grow an anthropic header")
	}
}

// TestUpstreamURLRejectsAnotherOrigin tests the last line of defence on its
// own. It has to be tested directly, because prefixesOf normalizes first and
// therefore refuses these values before this code is reached - which is the
// point of having both, and also the reason this check would otherwise look
// covered while never running.
//
// The two layers stop different things, and the difference is worth stating
// because it is easy to get backwards:
//
//   - "@evil.com/x" is a real origin escape. Appended to an absolute base it
//     makes the authority "ark.cn-beijing.volces.com@evil.com", which parses
//     with the intended host demoted to userinfo and evil.com as the host.
//     THIS is what upstreamURL exists for.
//   - "//evil.com/x" is NOT an escape here, and this test says so: the base
//     already carries a scheme and authority, so the result is
//     "https://ark.cn-beijing.volces.com//evil.com/x" - host unchanged, the
//     double slash is just an odd path. It is refused one layer earlier by
//     normalizePrefix, because a path prefix starting with // is a mistake
//     whatever it does, and because the same string IS an escape when a base
//     with no host is involved.
func TestUpstreamURLRejectsAnotherOrigin(t *testing.T) {
	const base = "https://ark.cn-beijing.volces.com"
	for _, bad := range []string{
		"@evil.com/v3/chat/completions",
		"@evil.com",
		"https://evil.com/chat",
	} {
		if got, err := upstreamURL(base, bad); err == nil {
			t.Errorf("upstreamURL(%q) = %q, want an error", bad, got)
		}
	}
	// Same host, so upstreamURL passes it; normalizePrefix is the layer that
	// refuses it, and TestNormalizePrefix asserts that.
	if _, err := upstreamURL(base, "//evil.com/x"); err != nil {
		t.Errorf("// keeps the host, so this layer has no reason to refuse it: %v", err)
	}
	if _, err := normalizePrefix("//evil.com/x"); err == nil {
		t.Error("...but normalizePrefix must refuse it")
	}
	for _, ok := range []string{"/api/v3/chat/completions", "/v1/messages", "/doubao/api/v3/contents/generations/tasks"} {
		got, err := upstreamURL(base, ok)
		if err != nil || got != base+ok {
			t.Errorf("upstreamURL(%q) = %q, %v", ok, got, err)
		}
	}
	// The returned string is the raw concatenation, not a re-serialized URL:
	// the core parses these same bytes again for its SSRF guard, and anything
	// that round-tripped through url.String() here could differ from what was
	// checked.
	if got, _ := upstreamURL(base, "/v1/a%2Fb"); got != base+"/v1/a%2Fb" {
		t.Errorf("the escape must survive verbatim: %q", got)
	}
}

func TestNormalizePrefix(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""}, {"  ", ""}, {"/api/v3", "/api/v3"}, {" /v1 ", "/v1"},
		{"/v1/", "/v1"}, {"/doubao/api/v3//", "/doubao/api/v3"}, {"/", ""},
	} {
		got, err := normalizePrefix(c.in)
		if err != nil || got != c.want {
			t.Errorf("normalizePrefix(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"v1", "//x", "@evil.com", "https://evil.com", "/a?b", "/a#b",
		"/a/../b", "/" + strings.Repeat("x", MaxPrefixLen)} {
		if got, err := normalizePrefix(bad); err == nil {
			t.Errorf("normalizePrefix(%q) = %q, want an error", bad, got)
		}
	}
}

// TestStoredPrefixWithSpaceKeepsWorking: whitespace inside a prefix is
// refused on save (validatePrefixFields), not on the request path. A prefix
// stored before that rule (with a vertical tab or a no-break space) still
// reads, and the request is built as before; only upstreamURL's own checks
// apply to it.
func TestStoredPrefixWithSpaceKeepsWorking(t *testing.T) {
	for _, prefix := range []string{"/v1\vx", "/v1\u00a0x"} {
		quoted, _ := json.Marshal(prefix)
		settings := `{"base_url":"https://relay.test","api_prefix":` + string(quoted) + `}`
		if got, err := prefixesOf(AccountTypeRelay, settings); err != nil || got.api != prefix {
			t.Errorf("prefixesOf(%q) = %q, %v; want it as stored", prefix, got.api, err)
		}
		if errs := validatePrefixFields(nil, settings); len(errs) != 1 || errs[0].GetField() != FieldAPIPrefix || errs[0].GetCode() != "pattern" {
			t.Errorf("saving %q: errors = %v, want api_prefix:pattern", prefix, errs)
		}
	}
	r, err := buildFor(t, relayAccount("/v1\u00a0x", ""), &pluginv1.RequestMeta{Protocol: ProtocolChat, Model: "m"})
	if err != nil || r.GetUrl() != "https://relay.test/v1\u00a0x/chat/completions" {
		t.Fatalf("url = %q (%v)", r.GetUrl(), err)
	}
}

// TestValidateRelayBaseURL covers the save-time guard on the relay base URL.
// The prefixes are what express a path, and a base URL that already carries one
// would double it - loudly on text, but on video the poll 404s forever and the
// core keeps the pre-charge, so this is refused rather than stripped.
func TestValidateRelayBaseURL(t *testing.T) {
	validate := func(settings string) []*pluginv1.FieldError {
		return New().validateWithAssets(&pluginv1.ValidateCredentialsRequest{
			AccountType: AccountTypeRelay, CredentialsJson: testKey, SettingsJson: settings,
		}).GetErrors()
	}
	for _, bad := range []string{
		`{"base_url":"https://relay.test/v1"}`,
		`{"base_url":"https://relay.test/api/v3"}`,
		`{"base_url":"https://relay.test/doubao/api/v3/"}`,
		`{}`, // required
	} {
		errs := validate(bad)
		if len(errs) == 0 {
			t.Errorf("%s was accepted", bad)
			continue
		}
		if errs[0].GetField() != "base_url" {
			t.Errorf("%s: error on %q, want base_url", bad, errs[0].GetField())
		}
	}
	if errs := validate(`{"base_url":"https://relay.test","video_api_prefix":"/doubao/api/v3"}`); len(errs) != 0 {
		t.Errorf("the verified configuration must be accepted: %v", errs)
	}
	// An escaped slash stays escaped (saved and sent so): the path does not end in a /v1 segment.
	if errs := validate(`{"base_url":"https://relay.test/a%2Fv1"}`); len(errs) != 0 {
		t.Errorf("https://relay.test/a%%2Fv1 must be accepted: %v", errs)
	}
	// The official type keeps its own rules: base_url is optional there, and
	// "https://ark.cn-beijing.volces.com/api/v3" is a paste of the SDK base URL
	// that spec.Versions exists to accept.
	official := New().validateWithAssets(&pluginv1.ValidateCredentialsRequest{
		AccountType: AccountTypeAPIKey, CredentialsJson: testKey,
		SettingsJson: `{"base_url":"` + DefaultBaseURL + `/api/v3"}`,
	})
	if errs := official.GetErrors(); len(errs) != 0 {
		t.Errorf("the official type must still accept a pasted SDK base URL: %v", errs)
	}
}
