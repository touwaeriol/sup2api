package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// meterNothing gives every account type the usage rules of the protocol's
// platform without the parts metered apart: the top-level usage only.
func meterNothing(t *testing.T, e *env, platform, protocol string) {
	t.Helper()
	var rules manifest.UsageRules
	for _, p := range e.gen.platforms {
		if p.Platform.ID == platform {
			rules = p.Platform.Usage
		}
	}
	require.NotEmpty(t, rules.Semantics, "platform %s", platform)
	rules.Additional, rules.Attempts = nil, nil
	for i := range e.gen.accountTypes {
		for j := range e.gen.accountTypes[i].Type.Platforms {
			e.gen.accountTypes[i].Type.Platforms[j].Usage = map[string]manifest.UsageRules{protocol: rules}
		}
	}
}

// What a request asks to be metered apart never decides where it goes
// (CONTRACTS §120.4): an account whose usage rules cannot meter it gets the
// request as it is, and the response is billed on its top-level usage at
// the requested model.
func TestUnmeteredUsageDoesNotGateRequests(t *testing.T) {
	for _, kind := range []string{"plain", "null", "compaction", "context", "fallbacks"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			meterNothing(t, e, "anthropic", "anthropic.messages")
			b := body(testModel, false)
			switch kind {
			case "null":
				b["compaction"] = nil
			case "compaction":
				b["compaction"] = map[string]any{"type": "summarize"}
			case "context":
				b["context_management"] = map[string]any{"edits": []any{}}
			case "fallbacks":
				b["fallbacks"] = []any{map[string]any{"model": testModel}}
			}
			res := e.messages(b)
			rec := e.record()
			require.Equal(t, 200, res.status, string(res.body))
			assert.Equal(t, []string{"acc-1"}, e.up.keys())
			assert.Equal(t, 1, rec.Attempts)
			assert.True(t, rec.Billable)
			assert.Empty(t, rec.Additional)
			assert.Empty(t, rec.Replacement)
			require.NotNil(t, rec.Price)
			assert.EqualValues(t, 9, rec.Price.ID)
		})
	}
}

// Nor does an account's parameter override that adds such an operation:
// the attempt is not moved to another account, and the operation goes
// upstream.
func TestParamOverrideUnmeteredOperationIsSent(t *testing.T) {
	e := newEnv(t)
	meterNothing(t, e, "anthropic", "anthropic.messages")
	setParamOverride(t, e, 1, `{"operations":[{"mode":"set","path":"context_management","value":{"edits":[{"type":"compact_20260112"}]}}]}`)
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, []string{"acc-1"}, e.up.keys())
	assert.True(t, gjson.GetBytes(e.up.last().body, "context_management").Exists())
	rec := e.record()
	require.NotNil(t, rec.AccountID)
	assert.EqualValues(t, 1, *rec.AccountID)
	assert.Equal(t, 1, rec.Attempts)
	assert.True(t, rec.Billable)
}

// The same holds for the first turn of a WebSocket.
func TestWebSocketParamOverrideUnmeteredOperationIsSent(t *testing.T) {
	e := newWSEnv(t)
	rule := manifest.AdditionalUsageRule{Name: "compact", UsePrimaryModel: true}
	for i := range e.gen.endpoints {
		if ep := &e.gen.endpoints[i].Endpoint; ep.Protocol == "openai.responses_ws" {
			ep.Usage = &manifest.UsageRules{Additional: []manifest.AdditionalUsageRule{rule}}
		}
	}
	// The account type's own rules for the protocol meter no such part.
	for i := range e.gen.accountTypes {
		if e.gen.accountTypes[i].Plugin.Key == "openai" {
			for j := range e.gen.accountTypes[i].Type.Platforms {
				e.gen.accountTypes[i].Type.Platforms[j].Usage = map[string]manifest.UsageRules{"openai.responses_ws": {Semantics: "inclusive"}}
			}
		}
	}
	e.reg.set(e.gen)
	setParamOverride(t, e.env, 11, `{"operations":[{"mode":"set","path":"metadata.compact","value":"yes"}]}`)
	c := e.mustDial()
	c.send(create(wsModel, "hello"))
	c.until("response.completed")
	r := e.record()
	require.NotNil(t, r.AccountID)
	assert.EqualValues(t, 11, *r.AccountID)
	assert.Equal(t, 1, r.Attempts)
}

// A request converted for another platform does not carry its fallbacks:
// the converted account serves it and bills the requested model.
func TestConvertedRequestWithFallbacksBillsRequestedModel(t *testing.T) {
	e, q, _ := convEnv(t, nil)
	e.pricer.rules[advisorModel] = &core.PriceRule{ID: 19, Model: advisorModel, Mode: "per_token", Expression: "p*7+c*11", ExprHash: "advisor-price"}
	disableNative(e)
	res := e.messages(fallbackHTTPBody(false))
	rec := e.record()
	require.Equal(t, 200, res.status, string(res.body))
	require.Len(t, q.bodies, 1)
	assert.Equal(t, 1, rec.Attempts)
	assert.True(t, rec.Billable)
	assert.Empty(t, rec.Replacement)
	require.NotNil(t, rec.Price)
	assert.EqualValues(t, 9, rec.Price.ID)
}
