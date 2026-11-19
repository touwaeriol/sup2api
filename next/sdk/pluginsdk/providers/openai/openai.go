// Package openai provides the error classification policy for OpenAI and
// OpenAI-compatible APIs: parsing the error body, mapping statuses to client
// error types, and computing rate-limit resets from x-ratelimit headers.
package openai

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
)

// Policy is the shared OpenAI classify policy.
var Policy = classify.Policy{
	Parse:     parseError,
	ErrorType: errorTypeForStatus,
	Detail:    classify.DetailCodeMessage,
	Rules: []classify.Rule{
		classify.EdgeBlock(),
		classify.Credentials(),
		{When: classify.All(classify.Status(http.StatusTooManyRequests), quotaExhausted), Effect: classify.Disable, Reason: "upstream quota exhausted (insufficient_quota)", NoDetail: true},
		classify.RateLimit(rateLimitReset),
		classify.ServerErrors(10 * time.Second),
	},
}

// ChatPingBody is the body of a minimal chat completion: one user message
// asking the upstream to answer "ping". BuildTestRequest uses it so every
// OpenAI-compatible plugin probes the same shape; maxTokens stays a parameter
// because the minimum a model accepts differs (1 for OpenAI, 16 for some Ark
// models). The result is deterministic JSON: encoding/json sorts map keys.
func ChatPingBody(model string, maxTokens int) string {
	return pingJSON(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	})
}

// ResponsesPingBody is the body of a minimal response: the input "ping" and
// 16 output tokens, the least the Responses API accepts.
func ResponsesPingBody(model string) string {
	return pingJSON(map[string]any{"model": model, "input": "ping", "max_output_tokens": 16})
}

// EmbeddingsPingBody is the body of a minimal embedding: one word.
func EmbeddingsPingBody(model string) string {
	return pingJSON(map[string]any{"model": model, "input": "ping"})
}

// ImagesPingBody is the body of the cheapest image generation: one image of
// the smallest common size at the lowest quality. It generates (and costs)
// a real image, so the core only offers it when the test names the model
// (endpoint test "explicit", CONTRACTS §122).
func ImagesPingBody(model string) string {
	return pingJSON(map[string]any{"model": model, "prompt": "a small white dot", "n": 1, "size": "1024x1024", "quality": "low"})
}

// pingJSON is deterministic JSON of a test body: encoding/json sorts map
// keys.
func pingJSON(body map[string]any) string {
	out, _ := json.Marshal(body)
	return string(out)
}

func parseError(body []byte) classify.Error {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return classify.Error{}
	}
	e := gjson.GetBytes(body, "error")
	if e.Type == gjson.String { // some compatible servers: {"error": "message"}
		return classify.Error{Message: e.String()}
	}
	return classify.Error{
		Message: e.Get("message").String(),
		Type:    e.Get("type").String(),
		Code:    e.Get("code").String(),
	}
}

func errorTypeForStatus(in *classify.Input) string {
	if in.Error.Type != "" && in.Status != 0 {
		return in.Error.Type
	}
	switch in.Status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 0:
		return "server_error"
	default:
		if in.Status >= 500 {
			return "server_error"
		}
		return "invalid_request_error"
	}
}

func quotaExhausted(in *classify.Input) bool {
	return in.Error.Code == "insufficient_quota" || in.Error.Type == "insufficient_quota" ||
		strings.Contains(strings.ToLower(in.Error.Message), "exceeded your current quota")
}

// rateLimitReset works out when a 429'd account can be used again:
//  1. retry-after-ms, then retry-after (seconds or HTTP date);
//  2. x-ratelimit-reset-{requests,tokens} of the exhausted limits
//     (x-ratelimit-remaining-* == 0), the latest one;
//  3. otherwise the earliest x-ratelimit-reset-*;
//  4. otherwise nothing (the classify package defaults to 60s).
//
// Reset values are Go-style durations ("1s", "6m0s", "20ms").
func rateLimitReset(in *classify.Input) (time.Time, string, bool) {
	if t, src, ok := classify.RetryAfter(in); ok {
		return t, src, true
	}
	var exhausted, earliest time.Duration
	found := false
	for _, kind := range []string{"requests", "tokens"} {
		d, err := time.ParseDuration(in.Headers["x-ratelimit-reset-"+kind])
		if err != nil || d < 0 {
			continue
		}
		if in.Headers["x-ratelimit-remaining-"+kind] == "0" && d > exhausted {
			exhausted = d
		}
		if !found || d < earliest {
			earliest, found = d, true
		}
	}
	switch {
	case exhausted > 0:
		return in.Now.Add(exhausted), "x-ratelimit reset", true
	case found:
		return in.Now.Add(earliest), "x-ratelimit reset", true
	default:
		return time.Time{}, "", false
	}
}
