package pluginsdktest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// SettingsSecrets fails t unless manifestJSON writes ui.settings.secrets
// out and it declares exactly want (none: a native settings form without
// secrets). Without the key the core cannot tell which settings are
// secrets and masks every one of them (CONTRACTS §75.1).
func SettingsSecrets(t testing.TB, manifestJSON []byte, want ...manifest.SecretField) {
	t.Helper()
	var declared struct {
		UI struct {
			Settings map[string]json.RawMessage `json:"settings"`
		} `json:"ui"`
	}
	if err := json.Unmarshal(manifestJSON, &declared); err != nil || declared.UI.Settings["secrets"] == nil {
		t.Fatalf("ui.settings.secrets must be written out: %v", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if got := m.UI.Settings.SecretFields(); (len(got) != 0 || len(want) != 0) && !reflect.DeepEqual(got, want) {
		t.Fatalf("ui.settings secrets = %+v, want %+v", got, want)
	}
}
