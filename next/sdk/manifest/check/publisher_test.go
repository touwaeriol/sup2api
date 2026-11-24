package check

import (
	"strings"
	"testing"
)

func TestValidatePublisherID(t *testing.T) {
	for _, id := range []string{"sub2api", "acme_labs", "a", "0x_1", strings.Repeat("a", PublisherIDMaxLen), strings.Repeat("a_", 12) + "a"} {
		if err := ValidatePublisherID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"", "Acme", "-acme", "_acme", "ac me", "acme.io", "ac/me", "acme-labs", "ac__me", "acme_", strings.Repeat("a", PublisherIDMaxLen+1), strings.Repeat("a_", 13) + "a"} {
		if ValidatePublisherID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestValidatePluginID(t *testing.T) {
	if err := ValidatePluginID(strings.Repeat("p", PublisherIDMaxLen), "ab"); err != nil {
		t.Errorf("longest publisher, shortest key: %v", err)
	}
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

// A manifest without a publisher misses a required field; an invalid one
// is malformed.
func TestManifestPublisherRequired(t *testing.T) {
	for publisher, code := range map[string]string{"": "required", "Acme": "invalid_format"} {
		m := minimal()
		m.Publisher = publisher
		if err := Validate(m, nil, ValidateOptions{Tooling: true}); !hasCode(err, "publisher", code) {
			t.Errorf("publisher %q: %v, want %s", publisher, err, code)
		}
	}
}
