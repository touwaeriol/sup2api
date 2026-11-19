package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Balance declarations of an account type (CONTRACTS §52).
func TestAccountTypeBalance(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID:        "oauth",
		Label:     manifest.LocalizedText{"en": "OAuth"},
		Form:      keyForm(m),
		Platforms: []manifest.AccountPlatform{{Platform: "video"}},
	}}
	files := map[string][]byte{}

	// "balance": {} is a complete declaration (the host's default interval),
	// and the bounds themselves are accepted.
	for _, interval := range []int{0, 60, 600, 86400} {
		m.AccountTypes[0].Balance = &manifest.AccountBalance{UpdateInterval: interval}
		if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
			t.Fatalf("updateInterval=%d rejected: %v", interval, codes(err))
		}
	}

	for _, interval := range []int{-1, 59, 86401} {
		m.AccountTypes[0].Balance = &manifest.AccountBalance{UpdateInterval: interval}
		if got := codes(Validate(m, files, ValidateOptions{Tooling: true})); got["accountTypes[0].balance.updateInterval"] != "out_of_range" {
			t.Errorf("updateInterval=%d: codes = %v", interval, got)
		}
	}
}
