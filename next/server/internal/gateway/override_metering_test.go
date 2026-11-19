package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// An account's parameter override that adds an operation its usage rules
// cannot meter (here Anthropic compaction, context_management) moves the
// attempt to another account, as scheduling does for a client body asking
// for it: the request is never sent unmetered.
func TestParamOverrideUnmeterableOperationSwitchesAccount(t *testing.T) {
	e := newEnv(t)
	for i := range e.gen.accountTypes {
		for j := range e.gen.accountTypes[i].Type.Platforms {
			e.gen.accountTypes[i].Type.Platforms[j].Usage = map[string]manifest.UsageRules{"anthropic.messages": {Semantics: "exclusive"}}
		}
	}
	setParamOverride(t, e, 1, `{"operations":[{"mode":"set","path":"context_management","value":{"edits":[{"type":"compact_20260112"}]}}]}`)
	r := e.messages(body(testModel, false))
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, []string{"acc-2"}, e.up.keys())
	assert.False(t, gjson.GetBytes(e.up.last().body, "context_management").Exists())
	assert.Empty(t, e.accounts.cooldowns, "the account is not blamed")
	rec := e.record()
	require.NotNil(t, rec.AccountID)
	assert.EqualValues(t, 2, *rec.AccountID)
	assert.Equal(t, 2, rec.Attempts)
}

// The same holds for the first turn of a WebSocket: its message goes
// through the same rewrites before Execute.
func TestWebSocketParamOverrideUnmeterableOperationSwitchesAccount(t *testing.T) {
	e := newWSEnv(t)
	rule := manifest.AdditionalUsageRule{Name: "compact", UsePrimaryModel: true, RequiredBy: []string{"metadata.compact"}}
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
	assert.EqualValues(t, 12, *r.AccountID)
	assert.Empty(t, e.accounts.cooldowns)
	e.wsUp.mu.Lock()
	defer e.wsUp.mu.Unlock()
	require.Len(t, e.wsUp.headers, 1, "only the serving account's handshake")
}

// An override of the stream flag cannot undo the forced stream of an
// upstream that only streams (CONTRACTS §83): the upstream still gets
// stream: true and the client its JSON answer.
func TestParamOverrideCannotUndoForcedStream(t *testing.T) {
	e, _, up := streamOnlyEnv(t, openAIStreamOnly())
	setParamOverride(t, e, 10, `{"operations":[{"mode":"set","path":"stream","value":false}]}`)
	r := e.responses(false)
	require.Equal(t, 200, r.status, string(r.body))
	assert.Equal(t, gjson.True, gjson.GetBytes(up.last(), "stream").Type)
	assert.Equal(t, "hello", r.json().Get("output.0.content.0.text").String())
	assert.True(t, e.record().Success)
}
