package vertex

import (
	"os"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
)

// TestConformance runs the platform conformance suite on the request-path
// inputs of executeCases: every account type and address, every protocol.
func TestConformance(t *testing.T) {
	m, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	var cases []conformance.Case
	for _, tc := range executeCases() {
		cases = append(cases, conformance.Case{Name: tc.name, Account: tc.in.GetAccount(), Meta: tc.in.GetMeta(), Body: tc.in.GetBody(), UpstreamModel: tc.model})
	}
	conformance.Run(t, conformance.Plugin{
		Platform:    p,
		Manifest:    m,
		Protocols:   []string{ProtocolGenerate, ProtocolStreamGenerate, ProtocolCountTokens},
		Credentials: []string{"authorization", "x-goog-api-key"},
		Authoritative: map[string]string{
			"x-goog-user-project": "names the quota project of user credentials, which belong to no project; aiplatform answers 403 without it",
		},
		// A stored token is sent only while it has not expired.
		FixClock: func(now time.Time) { p.now = func() time.Time { return now } },
	}, cases...)
}
