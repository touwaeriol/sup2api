package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// ui.settings.secrets: the declared fields, sorted by pointer, names
// unescaped.
func TestFormSecretFields(t *testing.T) {
	f := Form{Mode: "native", Secrets: []SettingsSecret{
		{Path: "notify_webhook_url", Sensitive: true},
		{Path: "api_key"},
		{Path: "upstream/api~1key"},
		{Path: "a~0b"},
	}}
	got := map[string]bool{}
	var order []string
	for _, s := range f.SecretFields() {
		got[s.Pointer()] = s.Credential
		order = append(order, s.Pointer())
	}
	want := map[string]bool{"notify_webhook_url": false, "api_key": true, "upstream/api~1key": true, "a~0b": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(order, []string{"api_key", "a~0b", "notify_webhook_url", "upstream/api~1key"}) {
		t.Fatalf("order = %v", order)
	}
	if fields := f.SecretFields(); !reflect.DeepEqual(fields[3].Path, []string{"upstream", "api/key"}) {
		t.Fatalf("path = %q", fields[3].Path)
	}
	if fields := (Form{}).SecretFields(); fields == nil || len(fields) != 0 {
		t.Fatalf("no secrets = %v", fields)
	}
}

// An absent secrets key (masks everything) and an empty list (masks nothing)
// stay apart through JSON both ways.
func TestFormSecretsAbsentOrEmpty(t *testing.T) {
	for raw, wantNil := range map[string]bool{`{"mode":"native"}`: true, `{"mode":"native","secrets":[]}`: false} {
		var f Form
		if err := json.Unmarshal([]byte(raw), &f); err != nil || (f.Secrets == nil) != wantNil {
			t.Fatalf("%s: secrets = %#v (%v)", raw, f.Secrets, err)
		}
		if b, _ := json.Marshal(f); string(b) != raw {
			t.Fatalf("%s: marshaled %s", raw, b)
		}
	}
}

func TestSecretPath(t *testing.T) {
	deep := strings.Repeat("p/", MaxSecretDepth-1) + "p"
	for pointer, want := range map[string][]string{
		"api_key":   {"api_key"},
		"a/b":       {"a", "b"},
		"a~1b/c~0d": {"a/b", "c~d"},
		"100%":      {"100%"},
		deep:        strings.Split(deep, "/"),
	} {
		if got, ok := SecretPath(pointer); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("SecretPath(%q) = %q, %v; want %q", pointer, got, ok, want)
		}
	}
	for _, pointer := range []string{"", "/a", "a/", "a//b", "a~", "a~2", deep + "/q"} {
		if got, ok := SecretPath(pointer); ok {
			t.Errorf("SecretPath(%q) = %q, want refused", pointer, got)
		}
	}
}
