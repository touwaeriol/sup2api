package manifest

import "testing"

func TestUpstreamHostAllowed(t *testing.T) {
	m := &Manifest{
		ExternalServices: []string{"api.example.com", "Auth.Example.org:8443"},
		AccountTypes: []AccountType{
			{ID: "relay", UpstreamFields: []string{"base_url", "video_endpoint"}},
			{ID: "oauth", UpstreamFields: []string{}},
			{ID: "legacy"},
		},
	}
	settings := `{"base_url":"https://Relay.Test:9443/v1","video_endpoint":"video.test","other":"https://other.test"}`
	credentials := `{"base_url":"cred.test:8080"}`
	for _, tc := range []struct {
		host, accountType string
		want              bool
	}{
		{"api.example.com", "", true},
		{"API.example.com", "oauth", true},
		{"eu.api.example.com", "relay", true},
		{"notapi.example.com", "relay", false},
		{"auth.example.org", "", true},
		{"relay.test", "relay", true},
		{"video.test", "relay", true},
		{"cred.test", "relay", true},
		{"other.test", "relay", false},
		{"sub.relay.test", "relay", false},
		{"relay.test", "oauth", false},
		{"relay.test", "legacy", false},
		{"relay.test", "unknown", false},
		{"", "relay", false},
	} {
		if got := m.UpstreamHostAllowed(tc.host, tc.accountType, settings, credentials, "not json"); got != tc.want {
			t.Errorf("UpstreamHostAllowed(%q, %q) = %v, want %v", tc.host, tc.accountType, got, tc.want)
		}
	}
	var none *Manifest
	if none.UpstreamHostAllowed("api.example.com", "relay") {
		t.Error("no manifest allows a host")
	}
}
