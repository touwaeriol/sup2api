package manifest

import (
	"strings"
	"testing"
)

func TestPluginID(t *testing.T) {
	cases := []struct{ pub, key, id, display string }{
		{"sub2api", "codex", "codex", "codex"},
		{"erio", "codex", "_erio__codex", "erio/codex"},
		{"0x_1", "a_b", "_0x_1__a_b", "0x_1/a_b"},
	}
	for _, c := range cases {
		id := PluginID(c.pub, c.key)
		if id != c.id || DisplayID(id) != c.display {
			t.Errorf("%s/%s: id %q display %q", c.pub, c.key, id, DisplayID(id))
		}
		if pub, key, ok := ParsePluginID(id); !ok || pub != c.pub || key != c.key {
			t.Errorf("parse %q: %q %q %v", id, pub, key, ok)
		}
	}
	for _, bad := range []string{"", "_", "_erio", "_erio__", "___codex"} {
		if _, _, ok := ParsePluginID(bad); ok {
			t.Errorf("parse %q accepted", bad)
		}
	}
}

// No plugin id's table prefix starts another's, also when an official key
// equals a publisher id.
func TestPluginIDPrefixesDisjoint(t *testing.T) {
	ids := []string{
		PluginID("sub2api", "erio"), PluginID("erio", "codex"), PluginID("erio", "co"),
		PluginID("erio", "codex2"), PluginID("er", "io"), PluginID("sub2api", "codex"),
	}
	for _, a := range ids {
		for _, b := range ids {
			if a != b && strings.HasPrefix(TablePrefix(b), TablePrefix(a)) {
				t.Errorf("prefix of %q starts the prefix of %q", a, b)
			}
		}
	}
}
