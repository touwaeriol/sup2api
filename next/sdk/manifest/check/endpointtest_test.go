package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// endpoint.test is absent, "explicit" or "never" (CONTRACTS §122); the
// built-in openai platform marks its two image endpoints.
func TestEndpointTest(t *testing.T) {
	for _, value := range []string{"", manifest.EndpointTestExplicit, manifest.EndpointTestNever} {
		p := validPlatform()
		p.Endpoints[0].Test = value
		if got := platformCodes(p); got["endpoints[0].test"] != "" {
			t.Errorf("test %q rejected: %v", value, got)
		}
	}
	for _, value := range []string{"always", "Explicit", " never"} {
		p := validPlatform()
		p.Endpoints[0].Test = value
		if got := platformCodes(p); got["endpoints[0].test"] != "invalid" {
			t.Errorf("test %q: %v", value, got)
		}
	}
}
