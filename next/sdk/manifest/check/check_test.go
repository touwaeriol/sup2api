package check

import (
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// The host's own test suite (server/internal/plugin/pkg) covers the rules
// end to end. These tests cover what only this package can: the path
// grammar, the built-in platform definitions and the Tooling mode plugin
// tooling runs in.

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/v1/messages", "/v1/messages", true},
		{"/v1/messages", "/v1/messages/", true},
		{"/v1/messages", "/v1/messages/count_tokens", false},
		{"/v1/messages", "/v1/chat", false},
		{"/v1/:x", "/v1/messages", true},
		{"/v1/:x", "/v1/messages/count", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:model:streamGenerateContent", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:m", true},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/gemini:generateContent", true},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/generateContent", false},
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:generateContent", true}, // plain param
		{"/v1beta/models/:model:generateContent", "/v1beta/models/:generateContent:x", false},
		{"/v1beta/models/:a:x.y", "/v1beta/models/:b:y", false},
		{"/v1beta/models/:a:run", "/v1beta/models/:b:run", true},
		{"/v1/*rest", "/v1/a/b/c", true},
		{"/v1/*rest", "/v2/a", false},
		{"/v1/a", "/v1/a/b", false},
	}
	for _, tc := range cases {
		if got := PathsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("PathsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := PathsOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("PathsOverlap(%q, %q) = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

// Endpoint paths are matched on whole segments, so a path that merely
// starts with the text of a core route is free. The core owns /api/v1, not
// all of /api: /api/v3 (Volcengine Ark's official path) is a gateway path.
func TestReservedPath(t *testing.T) {
	for _, p := range []string{"/api", "/api/", "/API", "/api/v1", "/api/v1/x", "/Api/V1/chat", "/api/:version/x", "/api/*rest",
		"/plugin-ui/x", "/healthz", "/HEALTHZ/x"} {
		if msg := checkEndpointPath(p); !strings.Contains(msg, "must not start with /") || !strings.Contains(msg, "(a core route)") {
			t.Errorf("%s: msg = %q, want a reserved-path error", p, msg)
		}
	}
	for _, p := range []string{"/apifoo/v1", "/healthcheck", "/plugin-uix/a", "/v1/messages", "/api/v3/chat/completions",
		"/api/v3/responses", "/api/v1x/a", "/api/v2", "/doubao/api/v3/chat/completions", "/api/v3/:model/x"} {
		if msg := checkEndpointPath(p); msg != "" {
			t.Errorf("%s: %s", p, msg)
		}
	}
}

// Every built-in platform passes the rules a plugin platform is held to.
func TestBuiltinPlatformsPassPluginRules(t *testing.T) {
	for _, p := range platforms.Builtin() {
		for _, fe := range CheckPlatform(p) {
			t.Errorf("%s: %s %s %s", p.ID, fe.Field, fe.Code, fe.Message)
		}
	}
}

func minimal() *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion: manifest.APIVersion,
		Key:        "demo",
		Name:       manifest.LocalizedText{"en": "Demo"},
		Version:    "0.1.0",
		Publisher:  "tester",
		Runtime:    "grpc",
		Entry:      manifest.Entry{GRPC: &manifest.GRPCEntry{Binaries: "runtimes/{os}-{arch}/plugin"}},
		HostCompat: ">=0.1.0 <0.2.0",
	}
}

// keyForm is a valid account type form of m: a native one, for which it gives
// m ui.native, its host permission and hostUICompat.
func keyForm(m *manifest.Manifest) manifest.Form {
	m.UI = &manifest.UI{Native: &manifest.NativeUI{Entry: "ui/native/entry.js"}}
	m.HostUICompat = "^4.0"
	m.HostPermissions = append(m.HostPermissions, manifest.HostPermission{ID: "ui.native"})
	return manifest.Form{Mode: "native", Component: "KeyForm"}
}

func codes(err error) map[string]string {
	out := map[string]string{}
	fields, _ := Fields(err)
	for _, f := range fields {
		out[f.Field] = f.Code
	}
	return out
}

// Tooling relaxes exactly three checks; everything else still applies.
func TestToolingMode(t *testing.T) {
	m := minimal()
	if err := Validate(m, nil, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("minimal manifest: %v", codes(err))
	}
	// The host requires the linux binaries and a matching host version.
	c := codes(Validate(m, nil, ValidateOptions{HostVersion: "0.1.0"}))
	if c["entry.grpc.binaries"] != "binary_missing" {
		t.Fatalf("host mode must require the binaries: %v", c)
	}
	if c := codes(Validate(m, nil, ValidateOptions{HostVersion: "0.9.0"})); c["hostCompat"] != "incompatible" {
		t.Fatalf("host mode must match hostCompat: %v", c)
	}
	// Tooling still rejects a malformed constraint and a missing one.
	m.HostCompat = "nonsense"
	if c := codes(Validate(m, nil, ValidateOptions{Tooling: true})); c["hostCompat"] != "invalid_constraint" {
		t.Fatalf("hostCompat must parse: %v", c)
	}
	m.HostCompat = ""
	if c := codes(Validate(m, nil, ValidateOptions{Tooling: true})); c["hostCompat"] != "required" {
		t.Fatalf("hostCompat is required: %v", c)
	}
	// ...and every rule that does not need a host or a build.
	m = minimal()
	m.Key = "Demo"
	err := Validate(m, nil, ValidateOptions{Tooling: true})
	if codes(err)["key"] != "invalid_format" {
		t.Fatalf("key format: %v", codes(err))
	}
	if !strings.Contains(err.Error(), "key: ") {
		t.Fatalf("error text = %q", err.Error())
	}
	// The core's own names are not plugin keys.
	for _, key := range []string{"core", "system"} {
		m.Key = key
		if c := codes(Validate(m, nil, ValidateOptions{Tooling: true})); c["key"] != "reserved" {
			t.Fatalf("key %q: %v", key, c)
		}
	}
}
