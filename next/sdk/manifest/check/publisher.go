package check

import (
	"fmt"
	"regexp"
)

// publisherIDRe is the rule of a publisher id: the manifest's publisher as
// a market index and the plugin registry name it (CONTRACTS §11.1).
var publisherIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidatePublisherID reports why id cannot be a publisher id of a market
// index or the plugin registry (nil when it can). The registry tool and the
// core's market client share it.
func ValidatePublisherID(id string) error {
	if !publisherIDRe.MatchString(id) {
		return fmt.Errorf("publisher id %q must match %s", id, publisherIDRe)
	}
	return nil
}
