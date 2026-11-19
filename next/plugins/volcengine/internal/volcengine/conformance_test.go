package volcengine

import (
	"maps"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
)

// TestConformance runs the platform conformance suite on the inputs of
// executeCases, plus the non-stream form of responses. The video query is
// not an HTTP protocol of the plugin: the core answers task queries from
// its own snapshot and never calls Execute for them.
func TestConformance(t *testing.T) {
	var cases []conformance.Case
	for _, tc := range executeCases() {
		c := conformance.Case{Name: tc.name, Account: tc.in.GetAccount(), Meta: tc.in.GetMeta(), Body: tc.in.GetBody()}
		if tc.task {
			c.Endpoint = conformance.TaskSubmission
			c.Observed = pluginsdktest.Observed(200, []byte(`{"id":"`+submittedID+`"}`))
			c.Observed.Observation.Fields = requested(Res720, Ratio169, 5)
		}
		cases = append(cases, c)
	}
	cases = append(cases, conformance.Case{Name: "responses non-stream", Account: account(testKey, ""),
		Meta: &pluginv1.RequestMeta{Protocol: ProtocolResponses, Model: "doubao-seed-1-6-250615", RequestId: "req-1"},
		Body: []byte(`{"model":"doubao-seed-1-6-250615","input":"hi"}`)})
	authoritative := maps.Clone(authoritativeHeaders)
	delete(authoritative, "authorization")
	delete(authoritative, "x-api-key")
	conformance.Run(t, conformance.Plugin{
		Platform:      New(),
		Manifest:      manifestFile(t),
		Protocols:     []string{ProtocolChat, ProtocolResponses, ProtocolEmbeddings, ProtocolMessages, ProtocolImages, ProtocolVideoSubmit},
		Credentials:   []string{"authorization", "x-api-key"},
		Authoritative: authoritative,
	}, cases...)
}
