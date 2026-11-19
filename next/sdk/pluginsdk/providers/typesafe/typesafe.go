// Package typesafe is the TypeSafe API (https://api.typesafe.ai): its
// platform definition, the one source both the typesafe plugin and a relay
// plugin forwarding to TypeSafe read (Platform), the API key account type
// (APIKey) and its error policy (Policy).
package typesafe

import (
	_ "embed"
	"encoding/json"
	"time"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/textutil"
)

// The platform, its one protocol and the vendor's address.
const (
	PlatformID        = "typesafe"
	ProtocolSystemOne = "typesafe.systemone"
	DefaultBaseURL    = "https://api.typesafe.ai"
)

//go:embed platform.json
var platformJSON []byte

// Platform is the typesafe platform as a manifest declares it: the
// endpoint POST /v1/systemone (protocol typesafe.systemone, non-streaming
// JSON, usage read by the host from the answer's usage, so an account type
// of another plugin may serve it) and the two danger-zone request headers
// a client may pass. Each call decodes it again: the result is the
// caller's to change.
func Platform() manifest.Platform {
	var p manifest.Platform
	if err := json.Unmarshal(platformJSON, &p); err != nil {
		panic("typesafe: platform.json: " + err.Error())
	}
	return p
}

// Policy classifies TypeSafe's errors: a rejected key (401, 403) disables
// the account, a 429 cools it down until Retry-After / retry-after-ms (the
// default cooldown without one), an overload (529) or another server error
// fails over with a short cooldown, and a bad request (400 - also an
// exceeded context, 413, 415, 422) goes back to the client untouched. The
// upstream's error body reaches the client as it is: the policy names no
// client message or error type, so the gateway passes the body through;
// TypeSafe's "detail" (a string, an object's message or the first item's
// msg) goes into the account reason.
var Policy = classify.Policy{
	Detail: detail,
	Rules: []classify.Rule{
		classify.EdgeBlock(),
		classify.Credentials(),
		classify.RateLimit(classify.RetryAfter),
		classify.ServerErrors(10 * time.Second),
	},
}

// detail appends ": <detail>" of a FastAPI-style error body, nothing when
// it has none.
func detail(in *classify.Input) string {
	if len(in.Body) == 0 || !gjson.ValidBytes(in.Body) {
		return ""
	}
	d := gjson.GetBytes(in.Body, "detail")
	msg := d.String()
	switch {
	case d.IsObject():
		msg = d.Get("message").String()
	case d.IsArray():
		msg = d.Get("0.msg").String()
	}
	if msg == "" {
		return ""
	}
	return ": " + textutil.Clip(msg, classify.MaxReasonMessage)
}

// PingBody is the body of a minimal systemone call on model: one yes/no
// question about the state "ping". The result is deterministic JSON
// (encoding/json sorts map keys).
func PingBody(model string) string {
	body, _ := json.Marshal(map[string]any{
		"model":     model,
		"state":     "ping",
		"questions": map[string]any{"ping": map[string]string{"type": "noul", "instructions": "Is this text a greeting?"}},
	})
	return string(body)
}
