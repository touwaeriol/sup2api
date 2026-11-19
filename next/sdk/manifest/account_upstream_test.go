package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAccountTypeCustomUpstream(t *testing.T) {
	guard := func(fields ...string) []GuardedSetting {
		out := make([]GuardedSetting, len(fields))
		for i, f := range fields {
			out[i] = GuardedSetting{Field: f, Allowed: []string{"https://api.example.com"}}
		}
		return out
	}
	cases := []struct {
		name   string
		at     AccountType
		fields []string
		known  bool
		custom bool
	}{
		{"undeclared without guard", AccountType{}, nil, false, true},
		{"undeclared with guard", AccountType{GuardedSettings: guard("base_url")}, nil, false, false},
		{"empty list", AccountType{UpstreamFields: []string{}}, []string{}, true, false},
		{"declared", AccountType{UpstreamFields: []string{"base_url"}}, []string{"base_url"}, true, true},
		{"declared guarded", AccountType{UpstreamFields: []string{"base_url"}, GuardedSettings: guard("base_url")}, []string{"base_url"}, true, false},
		{"all guarded", AccountType{UpstreamFields: []string{"base_url", "asset_base_url"}, GuardedSettings: guard("base_url", "asset_base_url")},
			[]string{"base_url", "asset_base_url"}, true, false},
		{"one unguarded", AccountType{UpstreamFields: []string{"base_url", "video_endpoint"}, GuardedSettings: guard("base_url")},
			[]string{"base_url", "video_endpoint"}, true, true},
	}
	for _, tc := range cases {
		fields, known := tc.at.AddressFields()
		if !reflect.DeepEqual(fields, tc.fields) || known != tc.known {
			t.Errorf("%s: AddressFields() = (%v, %v), want (%v, %v)", tc.name, fields, known, tc.fields, tc.known)
		}
		if got := tc.at.CustomUpstream(); got != tc.custom {
			t.Errorf("%s: CustomUpstream() = %v, want %v", tc.name, got, tc.custom)
		}
	}
}

// An explicit [] must survive a JSON round trip: dropping it would turn an
// OAuth type back into the legacy (custom) rule.
func TestAccountTypeUpstreamFieldsJSON(t *testing.T) {
	for _, tc := range []struct {
		fields []string
		json   string
	}{{nil, ""}, {[]string{}, `"upstreamFields":[]`}, {[]string{"base_url"}, `"upstreamFields":["base_url"]`}} {
		b, err := json.Marshal(AccountType{UpstreamFields: tc.fields})
		if err != nil {
			t.Fatal(err)
		}
		var back AccountType
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back.UpstreamFields, tc.fields) {
			t.Errorf("%v: round trip = %#v (%s)", tc.fields, back.UpstreamFields, b)
		}
		if want := tc.json != ""; strings.Contains(string(b), `"upstreamFields"`) != want || want && !strings.Contains(string(b), tc.json) {
			t.Errorf("%v: encoded as %s, want %s", tc.fields, b, tc.json)
		}
	}
}
