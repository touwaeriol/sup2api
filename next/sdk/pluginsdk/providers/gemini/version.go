package gemini

import (
	"strings"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Versions of the Gemini API (ai.google.dev/gemini-api/docs/api-versions):
// one protocol, v1 the stable one, v1beta with the experimental features.
const (
	VersionV1     = "v1"
	VersionV1Beta = "v1beta"
)

// ClientVersion is the Gemini API version of the endpoint a Gemini client
// called (CONTRACTS §134): v1 for /v1/..., else v1beta. ok is false for a
// request of another protocol, converted to Gemini, and for calls without
// a request (the account test, the model listing).
func ClientVersion(meta *pluginv1.RequestMeta) (version string, ok bool) {
	if !strings.HasPrefix(meta.GetClientProtocol(), "gemini.") {
		return "", false
	}
	if strings.HasPrefix(meta.GetEndpoint(), "/"+VersionV1+"/") {
		return VersionV1, true
	}
	return VersionV1Beta, true
}

// Version is the Gemini API version a request goes upstream on: a Gemini
// client's keeps the version it called, as it is; any other takes v1beta.
func Version(meta *pluginv1.RequestMeta) string {
	if v, ok := ClientVersion(meta); ok {
		return v
	}
	return VersionV1Beta
}
