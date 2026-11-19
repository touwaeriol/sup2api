package bedrock

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/classify"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
)

// errorTypeHeader carries the AWS error type, optionally followed by ":" and
// a URL.
const errorTypeHeader = "x-amzn-errortype"

// accessCooldown keeps an account whose request was refused (403) without
// proof that its credentials are bad out of scheduling for a while.
const accessCooldown = time.Minute

// policy classifies the failures of the Bedrock runtime
// (docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md §9.4). Whatever no rule matches
// (400 ValidationException, 404, 424 ModelErrorException) is the request's
// own failure and goes back to the client.
var policy = classify.Policy{
	Parse:     parseError,
	ErrorType: anthropic.Policy.ErrorType,
	Detail:    detail,
	Rules: []classify.Rule{
		classify.EdgeBlock(),
		// ThrottlingException, ModelNotReadyException.
		classify.RateLimit(classify.RetryAfter),
		// A quota of the AWS account (of a model, of a region) is used up:
		// another account may still have room, and this one is not broken.
		{When: classify.All(classify.Status(http.StatusBadRequest), awsError("ServiceQuotaExceeded")),
			Reason: "upstream service quota exceeded (%d)"},
		// The plugin signs with the node's clock: a rejected timestamp or a
		// malformed signature says nothing about the account.
		{When: classify.Any(awsError("RequestExpired"), classify.All(awsError("InvalidSignature"), clockSkew)),
			Reason: "upstream rejected the request time (%d): check the node's clock"},
		{When: awsError("IncompleteSignature"), Reason: "upstream rejected the signature format (%d), the account is not at fault"},
		{When: awsError("ExpiredToken"), Effect: classify.Disable, Reason: "temporary credentials expired (%d): replace them"},
		{When: classify.Any(classify.Status(http.StatusUnauthorized), awsError("UnrecognizedClient", "InvalidClientTokenId", "InvalidSignature")),
			Effect: classify.Disable, Reason: "upstream rejected credentials (%d)"},
		// AccessDeniedException is usually about one model the AWS account
		// has no access to, not about the account as a whole.
		{When: classify.Status(http.StatusForbidden), Effect: classify.Cooldown, Cooldown: accessCooldown, Reason: "upstream denied access (%d)"},
		// 408 ModelTimeoutException, 500, 503 ServiceUnavailableException, 529.
		classify.ServerErrors(0),
	},
}

// parseError reads an error body of the Bedrock runtime
// ({"message": ...}, the type in "__type" when the header lacks it).
func parseError(body []byte) classify.Error {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return classify.Error{}
	}
	msg := gjson.GetBytes(body, "message")
	if !msg.Exists() {
		msg = gjson.GetBytes(body, "Message")
	}
	return classify.Error{Message: msg.String(), Code: shortType(gjson.GetBytes(body, "__type").String())}
}

// shortType is the bare name of an AWS error type: without the namespace
// before "#" and whatever follows ":".
func shortType(t string) string {
	t, _, _ = strings.Cut(t, ":")
	if i := strings.LastIndexByte(t, '#'); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(t)
}

// awsType is the AWS error type of a failure: the header's, else the body's.
func awsType(in *classify.Input) string {
	if t := shortType(in.Headers[errorTypeHeader]); t != "" {
		return t
	}
	return in.Error.Code
}

// awsError matches the given AWS error types. AWS spells the same error with
// and without the "Exception" suffix depending on the protocol, so the
// suffix does not count.
func awsError(names ...string) classify.Match {
	return func(in *classify.Input) bool {
		return slices.Contains(names, strings.TrimSuffix(awsType(in), "Exception"))
	}
}

// clockSkew reports a signature refused for its timestamp, not for its key.
func clockSkew(in *classify.Input) bool {
	msg := strings.ToLower(in.Error.Message)
	return strings.Contains(msg, "signature expired") || strings.Contains(msg, "not yet current")
}

// detail appends " [<AWS error type>]: <message>" to a reason.
func detail(in *classify.Input) string {
	s := ""
	if t := awsType(in); t != "" {
		s = " [" + t + "]"
	}
	return s + classify.DetailMessage(in)
}

// ClassifyError implements pluginsdk.Platform.
func (p *Plugin) ClassifyError(_ context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) {
	return policy.Classify(p.now(), in), nil
}
