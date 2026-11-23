package check

import (
	"strings"
	"testing"
)

func TestValidatePublisherID(t *testing.T) {
	for _, id := range []string{"sub2api", "acme-labs", "a", "0x_1", strings.Repeat("a", 64)} {
		if err := ValidatePublisherID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"", "Acme", "-acme", "_acme", "ac me", "acme.io", "ac/me", strings.Repeat("a", 65)} {
		if ValidatePublisherID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
}
