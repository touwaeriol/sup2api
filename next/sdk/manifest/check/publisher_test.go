package check

import (
	"strings"
	"testing"
)

func TestValidatePublisherID(t *testing.T) {
	for _, id := range []string{"sub2api", "acme_labs", "a", "0x_1", strings.Repeat("a", 27)} {
		if err := ValidatePublisherID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"", "Acme", "-acme", "_acme", "ac me", "acme.io", "ac/me", "acme-labs", "ac__me", "acme_", strings.Repeat("a", 28)} {
		if ValidatePublisherID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestValidatePluginID(t *testing.T) {
	if err := ValidatePluginID("sub2api", "a"+strings.Repeat("b", 29)); err != nil {
		t.Errorf("official 30-byte key: %v", err)
	}
	if err := ValidatePluginID("erio", "codex"); err != nil {
		t.Errorf("erio/codex: %v", err)
	}
	for _, c := range [][2]string{{"erio", "a" + strings.Repeat("b", 23)}, {"erio-x", "codex"}, {"erio", "co__dex"}} {
		if ValidatePluginID(c[0], c[1]) == nil {
			t.Errorf("%s/%s accepted", c[0], c[1])
		}
	}
}
