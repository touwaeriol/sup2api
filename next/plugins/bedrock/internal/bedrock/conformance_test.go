package bedrock

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
)

// failoverCreds are other valid credentials of each type: the suite's own
// failover account suffixes every credential string, which an access key
// id (AKIA/ASIA + 16 characters) does not survive.
var failoverCreds = map[string]map[string]any{
	TypeAccessKey: {fieldAccessKeyID: "AKIAI44QH8DHBEXAMPLE", fieldSecretAccessKey: mockSecret("AKIAI44QH8DHBEXAMPLE")},
	TypeSTS:       {fieldAccessKeyID: "ASIAI44QH8DHBEXAMPLE", fieldSecretAccessKey: mockSecret("ASIAI44QH8DHBEXAMPLE"), fieldSessionToken: testSessionToken + "-failover"},
}

// TestConformance runs the platform conformance suite on the inputs of
// executeCases: every account type, streamed or not, a model id and an ARN.
func TestConformance(t *testing.T) {
	m, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	var cases []conformance.Case
	var exceptions []conformance.Exception
	for _, tc := range executeCases() {
		acc, model := tc.in.GetAccount(), tc.in.GetMeta().GetModel()
		c := conformance.Case{Name: tc.name, Account: acc, Meta: tc.in.GetMeta(), Body: tc.in.GetBody(), UpstreamModel: model}
		if !strings.HasPrefix(model, "arn:") {
			c.UpstreamModel = "us." + model // the account's inference profile prefix
		}
		if creds, ok := failoverCreds[acc.GetType()]; ok {
			c.Failover = stored(acc.GetType(), creds, settingsOf("us-west-2", "us"))
			c.Failover.Id = acc.GetId() + 1
		}
		cases = append(cases, c)
		exceptions = append(exceptions, conformance.Exception{Check: "04_headers", Case: tc.name,
			Reason: "the client's anthropic-beta goes into the body's anthropic_beta, where InvokeModel reads beta features (AWS user guide, Claude Messages request); Bedrock takes no anthropic-beta header"})
		if tc.in.GetMeta().GetStream() {
			exceptions = append(exceptions, conformance.Exception{Check: "03_body", Case: tc.name,
				Reason: "InvokeModel selects streaming by the URL (invoke-with-response-stream) and refuses a stream field in the body"})
		}
	}
	conformance.Run(t, conformance.Plugin{
		Platform:    p,
		Manifest:    m,
		Protocols:   []string{ProtocolMessages},
		Credentials: []string{"authorization", "x-amz-date", "x-amz-security-token"},
		Authoritative: map[string]string{
			"content-type": "covered by the SigV4 signature: an operator value would break it",
		},
		// The SigV4 signature carries the time.
		FixClock:   func(now time.Time) { p.now = func() time.Time { return now } },
		Exceptions: exceptions,
	}, cases...)
}
