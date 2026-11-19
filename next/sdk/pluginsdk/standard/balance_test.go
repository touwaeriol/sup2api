package standard_test

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// relayManifest declares one relay-site type whose upstream address is the
// account's base_url (the host rule of every relay and balance request).
const relayManifest = `{
  "apiVersion": 1, "key": "relaysite", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
  "capabilities": [{"id": "platform.adapter.v1"}],
  "accountTypes": [{"id": "apikey", "upstreamFields": ["base_url"], "balance": {},
    "platforms": [{"platform": "openai"}, {"platform": "anthropic"}, {"platform": "gemini"}]}]
}`

// newAPISite is the New API shape (design §3.5.2): one request, the key's
// quota with the key, in the site's own units.
func newAPISite() standard.Type {
	t := standard.Merge("apikey", relaySpec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""))
	t.Balance = &standard.Balance{Requests: []standard.BalanceRequest{{Path: "/api/usage/token/"}}, Read: readNewAPI}
	return t
}

// readNewAPI reads the number the site answers as it is, without a unit:
// the key's quota, the lower of the key's and its user's when the site
// gives both, no limit when it gives neither.
func readNewAPI(_ *standard.Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult {
	if f := standard.BalanceFailure(answers[0]); f != nil {
		return f
	}
	token := gjson.ParseBytes(answers[0].GetBody())
	if !token.Get("code").Bool() {
		return transientResult("token usage: " + token.Get("message").String())
	}
	var quotas []gjson.Result
	if !token.Get("data.unlimited_quota").Bool() {
		quotas = append(quotas, token.Get("data.total_available"))
	}
	if user := token.Get("data.user_quota.total_available"); user.Exists() {
		quotas = append(quotas, user)
	}
	if len(quotas) == 0 {
		return &pluginv1.BalanceResult{Unlimited: true}
	}
	lowest := int64(math.MaxInt64)
	for _, q := range quotas {
		m, ok := standard.Micros(q.String())
		if !ok {
			return transientResult("quota " + q.Raw)
		}
		lowest = min(lowest, m)
	}
	return &pluginv1.BalanceResult{AmountMicros: lowest}
}

// sub2APISite is the Sub2API shape: one request, the amount under one of
// three names, -1 for no limit, the unit in the answer if any.
func sub2APISite() standard.Type {
	t := standard.Merge("apikey", relaySpec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""))
	t.Balance = &standard.Balance{Requests: []standard.BalanceRequest{{Path: "/v1/usage"}}, Read: readSub2API}
	return t
}

// readSub2API reads the number and the unit the site answers as they are.
func readSub2API(_ *standard.Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult {
	if f := standard.BalanceFailure(answers[0]); f != nil {
		return f
	}
	b := gjson.ParseBytes(answers[0].GetBody())
	if v := b.Get("isValid"); v.Exists() && !v.Bool() {
		return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "key disabled or expired"}
	}
	var amount gjson.Result
	for _, path := range []string{"remaining", "balance", "quota.remaining"} {
		if amount = b.Get(path); amount.Exists() {
			break
		}
	}
	if amount.Float() == -1 {
		return &pluginv1.BalanceResult{Unlimited: true}
	}
	m, ok := standard.Micros(amount.String())
	if !ok {
		return transientResult("amount " + amount.Raw)
	}
	return &pluginv1.BalanceResult{AmountMicros: m, Currency: b.Get("unit").String()}
}

func transientResult(msg string) *pluginv1.BalanceResult {
	return &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT, ErrorMessage: msg}
}

// answers serves the balance requests from bodies by URL, recording what
// was asked.
func answers(bodies map[string]string, seen *[]*pluginv1.BalanceRequest) func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
	return func(r *pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
		*seen = append(*seen, r)
		body, ok := bodies[r.GetUrl()]
		if !ok {
			return &pluginv1.BalanceAnswer{Status: 404}
		}
		return &pluginv1.BalanceAnswer{Status: 200, Body: []byte(body)}
	}
}

func TestBalanceNewAPI(t *testing.T) {
	h := pluginsdktest.Start(t, standard.New(newAPISite()), pluginsdktest.Options{Manifest: []byte(relayManifest)})
	const tokenURL = "https://relay.example.com/api/usage/token/"
	for name, c := range map[string]struct {
		token string
		want  *pluginv1.BalanceResult
	}{
		"limited":               {`{"code":true,"data":{"total_available":1250000,"unlimited_quota":false}}`, &pluginv1.BalanceResult{AmountMicros: 1250000_000000}},
		"the lower of two":      {`{"code":true,"data":{"total_available":1250000,"unlimited_quota":false,"user_quota":{"total_available":250000}}}`, &pluginv1.BalanceResult{AmountMicros: 250000_000000}},
		"unlimited, user quota": {`{"code":true,"data":{"unlimited_quota":true,"user_quota":{"total_available":50000}}}`, &pluginv1.BalanceResult{AmountMicros: 50000_000000}},
		"a large quota":         {`{"code":true,"data":{"total_available":123456789012}}`, &pluginv1.BalanceResult{AmountMicros: 123456789012_000000}},
		"beyond int64":          {`{"code":true,"data":{"total_available":9007199254740993}}`, transientResult("quota 9007199254740993")},
		"unlimited":             {`{"code":true,"data":{"unlimited_quota":true}}`, &pluginv1.BalanceResult{Unlimited: true}},
		"no quota":              {`{"code":true,"data":{}}`, transientResult("quota ")},
		"code false":            {`{"code":false,"message":"no such token"}`, transientResult("token usage: no such token")},
	} {
		var seen []*pluginv1.BalanceRequest
		got, err := h.Balance(context.Background(), relayAccount, answers(map[string]string{tokenURL: c.token}, &seen))
		if err != nil || got.String() != c.want.String() {
			t.Errorf("%s: %v %v, want %v", name, got, err, c.want)
		}
		// One GET, with the key.
		if len(seen) != 1 || seen[0].Url != tokenURL || seen[0].Method != "GET" ||
			!reflect.DeepEqual(seen[0].Headers, map[string]string{"authorization": "Bearer site-key-0123"}) {
			t.Fatalf("%s: requests %v", name, seen)
		}
	}
}

func TestBalanceSub2API(t *testing.T) {
	h := pluginsdktest.Start(t, standard.New(sub2APISite()), pluginsdktest.Options{Manifest: []byte(relayManifest)})
	const usageURL = "https://relay.example.com/v1/usage"
	for body, want := range map[string]*pluginv1.BalanceResult{
		`{"isValid":true,"remaining":12.5,"unit":"USD"}`: {AmountMicros: 12500000, Currency: "USD"},
		`{"balance":3}`: {AmountMicros: 3000000},
		`{"quota":{"remaining":7.25},"unit":"CNY"}`: {AmountMicros: 7250000, Currency: "CNY"},
		`{"remaining":1,"unit":"dollars"}`:          {AmountMicros: 1000000, Currency: "dollars"},
		`{"remaining":-1}`:                          {Unlimited: true},
		`{"isValid":false,"remaining":1}`:           {ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "key disabled or expired"},
		`{"isValid":true}`:                          transientResult("amount "),
	} {
		var seen []*pluginv1.BalanceRequest
		got, err := h.Balance(context.Background(), relayAccount, answers(map[string]string{usageURL: body}, &seen))
		if err != nil || got.String() != want.String() || len(seen) != 1 || seen[0].Headers["authorization"] != "Bearer site-key-0123" {
			t.Errorf("%s: %v %v %v, want %v", body, got, err, seen, want)
		}
	}
	got, err := h.Balance(context.Background(), relayAccount, func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer {
		return &pluginv1.BalanceAnswer{Status: 401, Body: []byte(`{"error":"invalid key"}`)}
	})
	if err != nil || got.GetErrorType() != pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED || got.GetErrorMessage() != `upstream returned HTTP 401: {"error":"invalid key"}` {
		t.Errorf("401: %v %v", got, err)
	}
}

// The fake host refuses a request to a host the manifest does not allow
// for the account, before any is answered, and a type without Balance is
// UNIMPLEMENTED.
func TestBalanceHostRules(t *testing.T) {
	noAddress := `{"apiVersion": 1, "key": "relaysite", "version": "0.1.0", "publisher": "test", "runtime": "grpc",
	  "capabilities": [{"id": "platform.adapter.v1"}], "accountTypes": [{"id": "apikey", "platforms": [{"platform": "openai"}]}]}`
	h := pluginsdktest.Start(t, standard.New(newAPISite()), pluginsdktest.Options{Manifest: []byte(noAddress)})
	called := false
	if _, err := h.Balance(context.Background(), relayAccount, func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer { called = true; return nil }); status.Code(err) != codes.PermissionDenied || called {
		t.Errorf("a request off the host rules: %v, answered %v", err, called)
	}
	h = pluginsdktest.Start(t, standard.New(relaySite()), pluginsdktest.Options{Manifest: []byte(relayManifest)})
	if _, err := h.Balance(context.Background(), relayAccount, nil); status.Code(err) != codes.Unimplemented {
		t.Errorf("no balance: %v", err)
	}
	if _, err := h.Platform.ParseBalanceResponse(context.Background(), &pluginv1.ParseBalanceResponseRequest{Account: relayAccount}); status.Code(err) != codes.Unimplemented {
		t.Errorf("no balance, parse: %v", err)
	}
}

// Read sees one answer per request; another count is a transient result.
func TestBalanceAnswerCount(t *testing.T) {
	p := standard.New(newAPISite())
	ok := &pluginv1.BalanceAnswer{Status: 200, Body: []byte(`{"code":true,"data":{"total_available":1}}`)}
	out, err := p.ParseBalanceResponse(context.Background(), &pluginv1.ParseBalanceResponseRequest{Account: relayAccount, Answers: []*pluginv1.BalanceAnswer{ok, ok}})
	if err != nil || out.GetResult().GetErrorMessage() != "2 answers to 1 balance requests" {
		t.Errorf("two answers to one request: %v %v", out, err)
	}
}

func TestBalanceFailure(t *testing.T) {
	for _, c := range []struct {
		answer *pluginv1.BalanceAnswer
		want   *pluginv1.BalanceResult
	}{
		{&pluginv1.BalanceAnswer{Status: 200}, nil},
		{&pluginv1.BalanceAnswer{Status: 204}, nil},
		{&pluginv1.BalanceAnswer{TransportError: "dial tcp: refused"}, transientResult("transport error: dial tcp: refused")},
		{&pluginv1.BalanceAnswer{Status: 403}, &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "upstream returned HTTP 403"}},
		{&pluginv1.BalanceAnswer{Status: 429, Body: []byte(" slow down \n")}, transientResult("upstream returned HTTP 429: slow down")},
		{&pluginv1.BalanceAnswer{Status: 503}, transientResult("upstream returned HTTP 503")},
		{&pluginv1.BalanceAnswer{Status: 302}, transientResult("upstream returned HTTP 302")},
	} {
		if got := standard.BalanceFailure(c.answer); got.String() != c.want.String() {
			t.Errorf("%v: %v, want %v", c.answer, got, c.want)
		}
	}
}

// Micros is the number in millionths: exact to the sixth decimal, a large
// quota included, the seventh rounded half away from zero; false for what
// is not a plain decimal number or does not fit.
func TestMicros(t *testing.T) {
	for _, c := range []struct {
		number string
		want   int64
		ok     bool
	}{
		{"1250000", 1250000_000000, true},
		{"123456789012", 123456789012_000000, true},
		{"1234567890.123456", 1234567890123456, true},
		{"9223372036854.775807", math.MaxInt64, true},
		{"-9223372036854.775808", math.MinInt64, true},
		{"9223372036854.775808", 0, false},
		{"7.25", 7250000, true},
		{"0.123456", 123456, true},
		{"0.1234565", 123457, true},
		{"0.1234564", 123456, true},
		{"-0.0000005", -1, true},
		{"-1", -1000000, true},
		{"0", 0, true},
		{"", 0, false},
		{"1e3", 0, false},
		{"abc", 0, false},
		{"1.", 0, false},
		{".5", 0, false},
	} {
		got, ok := standard.Micros(c.number)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("Micros(%q) = %d %v, want %d %v", c.number, got, ok, c.want, c.ok)
		}
	}
}

func TestBalanceCheck(t *testing.T) {
	for name, b := range map[string]*standard.Balance{
		"no request":        {Read: readSub2API},
		"no read":           {Requests: []standard.BalanceRequest{{Path: "/v1/usage"}}},
		"a relative path":   {Requests: []standard.BalanceRequest{{Path: "v1/usage"}}, Read: readSub2API},
		"a bad auth header": {Requests: []standard.BalanceRequest{{Path: "/v1/usage", Auth: &standard.Auth{Header: "Authorization"}}}, Read: readSub2API},
	} {
		ty := sub2APISite()
		ty.Balance = b
		if err := standard.Check(ty); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
