package manifest

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestServesPluginFacts(t *testing.T) {
	p := Platform{ID: "video", Endpoints: []Endpoint{
		{ID: "plain"},
		{ID: "usage", UsageSource: UsageSourcePlugin},
		{ID: "submit", Task: &AsyncTaskEndpoint{Action: TaskActionSubmit}},
		{ID: "query", Task: &AsyncTaskEndpoint{Action: TaskActionQuery}},
	}}
	for _, tc := range []struct {
		endpoints []string
		want      bool
	}{
		{nil, true},
		{[]string{"plain", "query"}, false},
		{[]string{"plain", "usage"}, true},
		{[]string{"submit"}, true},
	} {
		if got := (AccountPlatform{Platform: "video", Endpoints: tc.endpoints}).ServesPluginFacts(p); got != tc.want {
			t.Errorf("endpoints %v: ServesPluginFacts = %v, want %v", tc.endpoints, got, tc.want)
		}
	}
}

// TestClientHeaders: an account type without passHeaders receives its
// platform's list, one with "passHeaders": [] receives none, and one with a
// list receives that list. Encoding the struct again keeps absent and empty
// apart.
func TestClientHeaders(t *testing.T) {
	p := &Platform{ID: "anthropic", PassHeaders: []string{"anthropic-beta", "user-agent"}}
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"absent", `{"platform":"anthropic"}`, []string{"anthropic-beta", "user-agent"}},
		{"empty", `{"platform":"anthropic","passHeaders":[]}`, []string{}},
		{"listed", `{"platform":"anthropic","passHeaders":["x-app"]}`, []string{"x-app"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ap AccountPlatform
			if err := json.Unmarshal([]byte(tc.raw), &ap); err != nil {
				t.Fatal(err)
			}
			if got := ap.ClientHeaders(p); !slices.Equal(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Fatalf("ClientHeaders = %#v, want %#v", got, tc.want)
			}
			out, err := json.Marshal(ap)
			if err != nil || string(out) != tc.raw {
				t.Fatalf("re-encoded %s (%v), want %s", out, err, tc.raw)
			}
		})
	}
	if got := (AccountPlatform{Platform: "x"}).ClientHeaders(nil); len(got) != 0 {
		t.Fatalf("no platform: %v", got)
	}
}
