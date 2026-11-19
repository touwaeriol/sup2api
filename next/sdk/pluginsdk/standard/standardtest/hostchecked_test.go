package standardtest

import (
	"context"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
)

// A replayed request must go to a host the core relays to: one of the
// manifest's externalServices, or an upstream address of the case's
// account, of the file's account type when the case names none.
func TestHostChecked(t *testing.T) {
	m := &manifest.Manifest{ExternalServices: []string{"api.example.com"},
		AccountTypes: []manifest.AccountType{{ID: "relay", UpstreamFields: []string{"base_url"}}}}
	for url, ok := range map[string]bool{
		"https://api.example.com/v1/chat":    true,
		"https://eu.api.example.com/v1/chat": true,
		"https://relay.test/v1/chat":         true,
		"https://other.test/v1/chat":         false,
	} {
		s := hostChecked(m, "relay", goldentest.Subject{Execute: func(context.Context, *goldentest.ExecuteInput) (*goldentest.UpstreamRequest, error) {
			return &goldentest.UpstreamRequest{URL: url}, nil
		}})
		in := &goldentest.ExecuteInput{Account: &goldentest.Account{ID: 1, SettingsJSON: `{"base_url":"https://relay.test"}`}}
		if _, err := s.Execute(context.Background(), in); (err == nil) != ok {
			t.Errorf("%s: %v, want allowed %v", url, err, ok)
		}
	}
}
