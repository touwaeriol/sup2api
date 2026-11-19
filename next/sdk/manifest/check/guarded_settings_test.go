package check

import (
	"reflect"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func TestGuardedSettingVersions(t *testing.T) {
	f := "accountTypes[0].guardedSettings[0].versions"
	for _, tc := range []struct {
		versions []string
		want     map[string]string
	}{
		{nil, map[string]string{}},
		{[]string{"/v1", "/v1beta", "/openai/v1"}, map[string]string{}},
		{[]string{""}, map[string]string{f + "[0]": "required"}},
		{[]string{"/"}, map[string]string{f + "[0]": "invalid"}},
		{[]string{"v1"}, map[string]string{f + "[0]": "invalid"}},
		{[]string{"/v1/"}, map[string]string{f + "[0]": "invalid"}},
		{[]string{"/v1", "/v1"}, map[string]string{f + "[1]": "duplicate"}},
	} {
		m := controlsManifest(nil)
		m.AccountTypes[0].SettingsFields = []string{"base_url"}
		m.AccountTypes[0].GuardedSettings = []manifest.GuardedSetting{
			{Field: "base_url", Allowed: []string{"https://api.example.com"}, Versions: tc.versions},
		}
		if got := controlCodes(m); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: codes = %v, want %v", tc.versions, got, tc.want)
		}
	}
}
