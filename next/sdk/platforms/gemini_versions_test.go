package platforms

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Every Gemini endpoint is served on v1beta and on v1 (CONTRACTS §134): the
// v1 one is the v1beta one with the id suffixed "_v1" and the path's
// version replaced, nothing else, so the two never drift apart.
func TestGeminiEndpointsOnBothVersions(t *testing.T) {
	byID := map[string]manifest.Endpoint{}
	for _, p := range Builtin() {
		if p.ID != manifest.PlatformGemini {
			continue
		}
		for _, e := range p.Endpoints {
			byID[e.ID] = e
		}
	}
	for id, beta := range byID {
		if strings.HasSuffix(id, "_v1") {
			if _, ok := byID[strings.TrimSuffix(id, "_v1")]; !ok {
				t.Errorf("%s has no v1beta endpoint", id)
			}
			continue
		}
		v1, ok := byID[id+"_v1"]
		if !ok || !strings.HasPrefix(beta.Path, "/v1beta/") {
			t.Errorf("%s (%s): no v1 twin or not a /v1beta path", id, beta.Path)
			continue
		}
		v1.ID, v1.Path = beta.ID, strings.Replace(v1.Path, "/v1/", "/v1beta/", 1)
		if !reflect.DeepEqual(v1, beta) {
			t.Errorf("%s_v1 differs from %s beyond id and path:\n v1     %+v\n v1beta %+v", id, id, v1, beta)
		}
	}
}
