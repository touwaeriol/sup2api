package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Every plugin of the platform is built for the current plugin protocol, so
// its hostCompat starts no earlier than the first core of that protocol: the
// version the outdated-contract message sends an operator to look for. A
// plugin using a later core's additions starts at that core (AGENTS.md).
func TestPluginsStartAtTheFirstCoreOfTheProtocol(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*", "manifest.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no plugin manifests: %v", err)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			HostCompat string `json:"hostCompat"`
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var lower []string
		for _, term := range strings.Fields(m.HostCompat) {
			if v, ok := strings.CutPrefix(term, ">="); ok {
				lower = append(lower, v)
			}
		}
		if len(lower) != 1 || semver.MustParse(lower[0]).LessThan(semver.MustParse(protocol.FirstCoreVersion)) {
			t.Errorf("%s: hostCompat %q, want one lower bound of at least >=%s (protocol.FirstCoreVersion)", path, m.HostCompat, protocol.FirstCoreVersion)
		}
	}
}
