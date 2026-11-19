package main

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/plugins/sub2api/internal/sub2api"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/standardtest"
)

const (
	apiKey = "sk-sub2api-0123456789"
	site   = "https://sub2api.example.com"
)

var platform = sub2api.New()

func account(settings string) *pluginv1.Account {
	return &pluginv1.Account{Id: 1, Type: sub2api.TypeAPIKey, CredentialsJson: `{"api_key":"` + apiKey + `"}`, SettingsJson: settings}
}

// TestGolden: for the protocols the type serves, the bytes sent upstream
// are what the official openai, anthropic and gemini plugins send (their
// frozen golden cases on an account with its own base URL), except on a
// base URL ending in /v1beta, which a site's type takes for a version on
// purpose (the openai and anthropic types do not).
func TestGolden(t *testing.T) {
	if err := standard.Check(sub2api.APIKey()); err != nil {
		t.Fatal(err)
	}
	keepsV1beta := func(c *goldentest.Case) bool {
		base, _ := c.BaseURL()
		return !strings.HasSuffix(base, "/v1beta")
	}
	for plugin, protocols := range map[string][]string{
		"openai":    {openai.ProtocolChat, openai.ProtocolResponses, openai.ProtocolResponsesWS, openai.ProtocolEmbeddings},
		"anthropic": {anthropic.ProtocolMessages, anthropic.ProtocolCountTokens},
		"gemini":    {gemini.ProtocolGenerate, gemini.ProtocolStreamGenerate, gemini.ProtocolCountTokens},
	} {
		t.Run(plugin, func(t *testing.T) {
			standardtest.Run(t, plugin, "manifest.json", platform, goldentest.Select(goldentest.Forwarded(sub2api.TypeAPIKey, protocols...)), goldentest.Select(keepsV1beta))
		})
	}
}

// TestRequest pins each protocol's request on the site: the official
// method, path and auth header, the body as it is (a chat stream asks for
// its usage, as the openai preset does) appended to the root of the saved
// base URL: the address without the one /v1 or /v1beta it may end in, so
// neither is doubled, a site under a /v1 of its own is reached at
// .../v1/v1, and an escaped "/" goes out as it was written.
func TestRequest(t *testing.T) {
	bearer := map[string]string{"authorization": "Bearer " + apiKey}
	json := map[string]string{"content-type": "application/json"}
	for _, c := range []struct {
		protocol, model, body string
		stream                bool
		url, sent             string
		headers, defaults     map[string]string
	}{
		{openai.ProtocolChat, "gpt-4o-mini", `{"model":"gpt-4o-mini","messages":[]}`, false,
			"/v1/chat/completions", "", bearer, json},
		{openai.ProtocolChat, "gpt-4o-mini", `{"model":"gpt-4o-mini","messages":[],"stream":true}`, true,
			"/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[],"stream":true,"stream_options":{"include_usage":true}}`, bearer, json},
		{openai.ProtocolResponses, "gpt-4o-mini", `{"model":"gpt-4o-mini","input":"hi","stream":true}`, true,
			"/v1/responses", "", bearer, json},
		{openai.ProtocolEmbeddings, "text-embedding-3-small", `{"model":"text-embedding-3-small","input":"hi"}`, false,
			"/v1/embeddings", "", bearer, json},
		{openai.ProtocolImages, "gpt-image-1.5", `{"model":"gpt-image-1.5","prompt":"a cat","stream":true}`, true,
			"/v1/images/generations", "", bearer, json},
		{openai.ProtocolImagesEdit, "gpt-image-1.5", `{"model":"gpt-image-1.5","prompt":"a hat","images":[{"image_url":"https://example.com/c.png"}]}`, false,
			"/v1/images/edits", "", bearer, json},
		{anthropic.ProtocolMessages, "claude-sonnet-4-6", `{"model":"claude-sonnet-4-6","max_tokens":1,"messages":[]}`, false,
			"/v1/messages", "", map[string]string{"x-api-key": apiKey},
			map[string]string{"anthropic-version": anthropic.DefaultAPIVersion, "content-type": "application/json"}},
		{anthropic.ProtocolCountTokens, "claude-sonnet-4-6", `{"model":"claude-sonnet-4-6","messages":[]}`, false,
			"/v1/messages/count_tokens", "", map[string]string{"x-api-key": apiKey},
			map[string]string{"anthropic-version": anthropic.DefaultAPIVersion, "content-type": "application/json"}},
		{gemini.ProtocolGenerate, "gemini-2.5-flash", `{"contents":[]}`, false,
			"/v1beta/models/gemini-2.5-flash:generateContent", "", map[string]string{"x-goog-api-key": apiKey}, json},
		{gemini.ProtocolStreamGenerate, "gemini-2.5-flash", `{"contents":[]}`, true,
			"/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse", "", map[string]string{"x-goog-api-key": apiKey}, json},
		{gemini.ProtocolCountTokens, "gemini-2.5-flash", `{"contents":[]}`, false,
			"/v1beta/models/gemini-2.5-flash:countTokens", "", map[string]string{"x-goog-api-key": apiKey}, json},
	} {
		for base, root := range map[string]string{
			site: site, site + "/": site, site + "/v1": site, site + "/v1beta": site,
			site + "/v1/v1": site + "/v1", site + "/v1beta/v1beta": site + "/v1beta", site + "/p/a%2Fb": site + "/p/a%2Fb",
		} {
			out, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
				Account: account(`{"base_url":"` + base + `"}`),
				Meta:    &pluginv1.RequestMeta{Protocol: c.protocol, Model: c.model, Stream: c.stream},
				Body:    []byte(c.body),
			})
			sent := c.sent
			if sent == "" {
				sent = c.body
			}
			if err != nil || out.Method != "POST" || out.Url != root+c.url || string(out.Body) != sent ||
				!reflect.DeepEqual(out.Headers, c.headers) || !reflect.DeepEqual(out.DefaultHeaders, c.defaults) {
				t.Errorf("%s on %s: %v %s %s %s headers %v default %v", c.protocol, base, err, out.GetMethod(), out.GetUrl(), out.GetBody(), out.GetHeaders(), out.GetDefaultHeaders())
			}
		}
	}
	if _, err := platform.BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
		Account: account(`{}`), Meta: &pluginv1.RequestMeta{Protocol: openai.ProtocolChat}, Body: []byte(`{}`),
	}); err == nil || !strings.Contains(err.Error(), "is required") {
		t.Errorf("no base URL: %v", err)
	}
}

// TestAccountTest: the account test goes to the first protocol the core
// lists (the account's binding) that has a test, with that protocol's
// auth; without a list it is the openai chat.
func TestAccountTest(t *testing.T) {
	for _, c := range []struct {
		protocols         []string
		url, header, used string
	}{
		{nil, "/v1/chat/completions", "authorization", openai.ProtocolChat},
		{[]string{anthropic.ProtocolMessages}, "/v1/messages", "x-api-key", anthropic.ProtocolMessages},
		{[]string{anthropic.ProtocolCountTokens, anthropic.ProtocolMessages, openai.ProtocolChat}, "/v1/messages", "x-api-key", anthropic.ProtocolMessages},
		{[]string{gemini.ProtocolGenerate}, "/v1beta/models/gemini-2.5-flash-lite:generateContent", "x-goog-api-key", gemini.ProtocolGenerate},
	} {
		out, err := platform.BuildTestRequest(context.Background(), &pluginv1.BuildTestRequestRequest{Account: account(`{"base_url":"` + site + `"}`), Protocols: c.protocols})
		if err != nil || out.Url != site+c.url || out.UsageProtocol != c.used || !strings.HasSuffix(out.Headers[c.header], apiKey) {
			t.Errorf("%v: %v %v", c.protocols, out, err)
		}
	}
}

// TestModels: the listing is GET /v1/models with Bearer, its ids at
// data.#.id, which both shapes a site answers there carry.
func TestModels(t *testing.T) {
	out, err := platform.BuildModelsRequest(context.Background(), &pluginv1.BuildModelsRequestRequest{Account: account(`{"base_url":"` + site + `/v1"}`)})
	if err != nil || out.Url != site+"/v1/models" || out.IdsPath != "data.#.id" ||
		!reflect.DeepEqual(out.Headers, map[string]string{"authorization": "Bearer " + apiKey}) {
		t.Fatalf("models = %v, %v", out, err)
	}
	for shape, body := range map[string]string{
		"openai":    `{"object":"list","data":[{"id":"gpt-4o","object":"model","owned_by":"openai"},{"id":"claude-sonnet-4-6","object":"model"}]}`,
		"anthropic": `{"data":[{"type":"model","id":"gpt-4o","display_name":"GPT-4o"},{"type":"model","id":"claude-sonnet-4-6"}],"has_more":false}`,
	} {
		var ids []string
		for _, id := range gjson.Get(body, out.IdsPath).Array() {
			ids = append(ids, id.String())
		}
		if !slices.Equal(ids, []string{"gpt-4o", "claude-sonnet-4-6"}) {
			t.Errorf("%s: ids %v", shape, ids)
		}
	}
}

const usageURL = site + "/v1/usage"

// TestBalance: one GET /v1/usage with the key, read the same way for the
// three kinds of key of the legacy release and the next release's answer:
// the number and the unit as the site gives them, none without unit, never
// converted (CONTRACTS §52).
func TestBalance(t *testing.T) {
	h := pluginsdktest.Start(t, platform, pluginsdktest.Options{Manifest: manifestJSON})
	transient := pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT
	for name, c := range map[string]struct {
		body string
		want *pluginv1.BalanceResult
	}{
		"legacy, a key with a quota": {`{"mode":"quota_limited","isValid":true,"status":"active","quota":{"limit":10,"used":2.5,"remaining":7.5,"unit":"USD"},"remaining":7.5,"unit":"USD",` +
			`"rate_limits":[{"window":"5h","limit":5,"used":1,"remaining":4}]}`, &pluginv1.BalanceResult{AmountMicros: 7500000, Currency: "USD"}},
		"legacy, a subscription": {`{"mode":"unrestricted","isValid":true,"planName":"pro","unit":"USD","remaining":3.25,` +
			`"subscription":{"daily_usage_usd":1.75,"daily_limit_usd":5}}`, &pluginv1.BalanceResult{AmountMicros: 3250000, Currency: "USD"}},
		"legacy, a subscription without limits": {`{"mode":"unrestricted","isValid":true,"planName":"pro","unit":"USD","remaining":-1,"subscription":{}}`, &pluginv1.BalanceResult{Unlimited: true}},
		"legacy, the wallet":                    {`{"mode":"unrestricted","isValid":true,"planName":"钱包余额","remaining":42.125,"unit":"USD","balance":42.125}`, &pluginv1.BalanceResult{AmountMicros: 42125000, Currency: "USD"}},
		"next": {`{"mode":"quota_limited","isValid":true,"status":"active","planName":"","remaining":0.5,"unit":"USD","balance":20,` +
			`"quota":{"limit":1,"used":0.5,"remaining":0.5,"unit":"USD"},"rate_limits":[{"type":"rpm","window":"1m","limit":60,"used":0,"remaining":60}]}`, &pluginv1.BalanceResult{AmountMicros: 500000, Currency: "USD"}},
		"balance only":        {`{"balance":3}`, &pluginv1.BalanceResult{AmountMicros: 3000000}},
		"quota only":          {`{"quota":{"remaining":7.25}}`, &pluginv1.BalanceResult{AmountMicros: 7250000}},
		"many decimals":       {`{"remaining":0.1234567,"unit":"USD"}`, &pluginv1.BalanceResult{AmountMicros: 123457, Currency: "USD"}},
		"a large amount":      {`{"remaining":123456789012.5,"unit":"CNY"}`, &pluginv1.BalanceResult{AmountMicros: 123456789012_500000, Currency: "CNY"}},
		"any unit":            {`{"remaining":1,"unit":"dollars"}`, &pluginv1.BalanceResult{AmountMicros: 1000000, Currency: "dollars"}},
		"in CNY":              {`{"isValid":true,"remaining":88.8,"unit":"CNY"}`, &pluginv1.BalanceResult{AmountMicros: 88800000, Currency: "CNY"}},
		"overdrawn":           {`{"isValid":true,"remaining":-0.25,"unit":"USD"}`, &pluginv1.BalanceResult{AmountMicros: -250000, Currency: "USD"}},
		"disabled or expired": {`{"mode":"quota_limited","isValid":false,"status":"disabled","remaining":1,"unit":"USD"}`, &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "the site reports the key disabled or expired / 上游报告此 Key 已停用或过期"}},
		"no amount":           {`{"mode":"quota_limited","isValid":true,"status":"active"}`, &pluginv1.BalanceResult{ErrorType: transient, ErrorMessage: "no remaining, balance or quota.remaining in /v1/usage / 应答里没有余额"}},
		"not a number":        {`{"remaining":"plenty","unit":"USD"}`, &pluginv1.BalanceResult{ErrorType: transient, ErrorMessage: `amount "\"plenty\"" is not a number in range / 余额不是可读的数字`}},
	} {
		var seen []*pluginv1.BalanceRequest
		got, err := h.Balance(context.Background(), account(`{"base_url":"`+site+`/v1"}`), func(r *pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
			seen = append(seen, r)
			return &pluginv1.BalanceAnswer{Status: 200, Body: []byte(c.body)}
		})
		if err != nil || got.String() != c.want.String() {
			t.Errorf("%s: %v %v, want %v", name, got, err, c.want)
		}
		if len(seen) != 1 || seen[0].Url != usageURL || seen[0].Method != "GET" ||
			!reflect.DeepEqual(seen[0].Headers, map[string]string{"authorization": "Bearer " + apiKey}) {
			t.Fatalf("%s: requests %v", name, seen)
		}
	}
	// The request goes to the root of the base URL, like every protocol's.
	for base, url := range map[string]string{site + "/v1beta": usageURL, site + "/v1/v1": site + "/v1/v1/usage"} {
		var seen []*pluginv1.BalanceRequest
		if _, err := h.Balance(context.Background(), account(`{"base_url":"`+base+`"}`), func(r *pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
			seen = append(seen, r)
			return &pluginv1.BalanceAnswer{Status: 404}
		}); err != nil || len(seen) != 1 || seen[0].Url != url {
			t.Errorf("%s: requests %v, %v, want %s", base, seen, err, url)
		}
	}
	got, err := h.Balance(context.Background(), account(`{"base_url":"`+site+`"}`), func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
		return &pluginv1.BalanceAnswer{Status: 401, Body: []byte(`{"error":{"type":"authentication_error","message":"Invalid API key"}}`)}
	})
	if err != nil || got.GetErrorType() != pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED || !strings.HasPrefix(got.GetErrorMessage(), "upstream returned HTTP 401") {
		t.Errorf("401: %v %v", got, err)
	}
}
