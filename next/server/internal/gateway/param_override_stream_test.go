package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
