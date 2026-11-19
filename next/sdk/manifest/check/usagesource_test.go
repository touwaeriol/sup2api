package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// endpoint.usageSource "plugin" (CONTRACTS §25.3): the endpoint's usage is
// read by PlatformService.ExtractUsage instead of by the declarative gjson
// rules. The field sits on the ENDPOINT, not on the usage rules, so the §13
// override chain cannot reach it (see manifest.Endpoint.UsageSource).

// pluginSourcePlatform is validPlatform with the usage read by the plugin:
// no maps at all (that is why an endpoint picks this source), one declared
// fact without a path (the plugin supplies the value), and the events the
// host is to collect from a stream.
func pluginSourcePlatform() manifest.Platform {
	p := validPlatform()
	p.Endpoints[0].UsageSource = manifest.UsageSourcePlugin
	p.Endpoints[0].UsageStreamEvents = []string{"message_delta"}
	p.Usage = manifest.UsageRules{
		Semantics: "inclusive",
		Facts:     map[string]manifest.UsageFact{"images": {Type: "number", Unit: "image"}},
	}
	return p
}

func TestUsageSourcePlugin(t *testing.T) {
	if got := platformCodes(pluginSourcePlatform()); len(got) > 0 {
		t.Fatalf("plugin usage source rejected: %v", got)
	}
	// "rules" is the explicit spelling of the default and needs none of the
	// plugin knobs.
	p := validPlatform()
	p.Endpoints[0].UsageSource = manifest.UsageSourceRules
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("explicit rules source rejected: %v", got)
	}

	cases := []struct {
		name        string
		mut         func(e *manifest.Endpoint)
		field, code string
	}{
		{"unknown source", func(e *manifest.Endpoint) { e.UsageSource = "gjson" },
			"endpoints[0].usageSource", "invalid"},
		// usageStreamEvents is rejected, not ignored,
		// outside the plugin source: the author believes something the host
		// does not do and the symptom looks exactly like success.
		{"usageStreamEvents without the plugin source", func(e *manifest.Endpoint) {
			e.UsageSource = ""
			e.UsageStreamEvents = []string{"message_delta"}
		}, "endpoints[0].usageStreamEvents", "unsupported"},
		{"too many events", func(e *manifest.Endpoint) {
			e.UsageStreamEvents = nil
			for i := range MaxUsageStreamEvents + 1 {
				e.UsageStreamEvents = append(e.UsageStreamEvents, string(rune('a'+i))+"_ev")
			}
		}, "endpoints[0].usageStreamEvents", "too_many"},
		{"event name format", func(e *manifest.Endpoint) { e.UsageStreamEvents = []string{"message delta"} },
			"endpoints[0].usageStreamEvents[0]", "invalid_format"},
		{"duplicate event name", func(e *manifest.Endpoint) {
			e.UsageStreamEvents = []string{"message_delta", "message_delta"}
		}, "endpoints[0].usageStreamEvents[1]", "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pluginSourcePlatform()
			tc.mut(&p.Endpoints[0])
			got := platformCodes(p)
			if got[tc.field] != tc.code {
				t.Fatalf("codes = %v, want %s=%s", got, tc.field, tc.code)
			}
		})
	}

	// A fact still has to be declared, and its type still has to be one the
	// host understands: the declaration is what makes the u() key exist and
	// what the host type-checks the plugin's value against.
	factCases := []struct {
		name        string
		fact        manifest.UsageFact
		field, code string
	}{
		{"fact type still required", manifest.UsageFact{}, "usage.facts.images.type", "required"},
		{"fact path still checked when given", manifest.UsageFact{Type: "number", Path: "usage..[["},
			"usage.facts.images.path", "invalid_path"},
	}
	for _, tc := range factCases {
		t.Run(tc.name, func(t *testing.T) {
			p := pluginSourcePlatform()
			p.Usage.Facts["images"] = tc.fact
			if got := platformCodes(p); got[tc.field] != tc.code {
				t.Fatalf("codes = %v, want %s=%s", got, tc.field, tc.code)
			}
		})
	}
}

// A fact of a platform none of whose endpoints reads its usage from a plugin
// still needs its path: without one it is never extracted and offers a u()
// key that is always 0.
func TestUsageFactPathRequiredOnlyWithoutPluginSource(t *testing.T) {
	p := validPlatform()
	p.Usage.Facts["images"] = manifest.UsageFact{Type: "number"}
	if got := platformCodes(p); got["usage.facts.images.path"] != "required" {
		t.Fatalf("codes = %v", got)
	}
	// One endpoint asking the plugin for its usage is enough: the platform's
	// rules are that endpoint's defaults too.
	p.Endpoints[0].UsageSource = manifest.UsageSourcePlugin
	if got := platformCodes(p); got["usage.facts.images.path"] != "" {
		t.Fatalf("codes = %v", got)
	}
}

// A streaming endpoint whose usage a plugin reports is exempt from the
// "must have an sse rule" check: it took that source precisely because no sse
// map can express its usage. The cost (a fallback that counts zero) is paid
// at runtime, where it is warned about and recorded.
func TestStreamingEndpointWithPluginUsageNeedsNoSSERules(t *testing.T) {
	p := pluginSourcePlatform()
	p.Endpoints[0].Response = manifest.EndpointResp{Stream: "sse", NonStream: "json"}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("streaming plugin-usage endpoint rejected: %v", got)
	}
	// Dropping back to the declarative source puts the requirement back.
	p.Endpoints[0].UsageSource = ""
	p.Endpoints[0].UsageStreamEvents = nil
	if got := platformCodes(p); got["endpoints[0].usage.sse"] != "required" {
		t.Fatalf("codes = %v", got)
	}
}

// endpoint.usageRequestFields (CONTRACTS §25.5 gap 1): the request body paths
// the host hands to ExtractUsage, and nothing else of the request. The list
// is bounded, each path must read one value, and it means nothing outside
// the plugin source.
func TestUsageRequestFields(t *testing.T) {
	p := pluginSourcePlatform()
	p.Endpoints[0].UsageRequestFields = []string{"resolution", "duration", "content.0.type", "content.#.type", "options.ratio"}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("request fields rejected: %v", got)
	}
	cases := []struct {
		name        string
		mut         func(e *manifest.Endpoint)
		field, code string
	}{
		// Rejected, not ignored, outside the plugin source: the author
		// believes a plugin will see these and none is ever asked.
		{"without the plugin source", func(e *manifest.Endpoint) {
			e.UsageSource = ""
			e.UsageStreamEvents = nil
			e.UsageRequestFields = []string{"resolution"}
		}, "endpoints[0].usageRequestFields", "unsupported"},
		{"too many", func(e *manifest.Endpoint) {
			for i := range MaxUsageRequestFields + 1 {
				e.UsageRequestFields = append(e.UsageRequestFields, "f"+string(rune('a'+i)))
			}
		}, "endpoints[0].usageRequestFields", "too_many"},
		{"empty path", func(e *manifest.Endpoint) { e.UsageRequestFields = []string{" "} },
			"endpoints[0].usageRequestFields[0]", "required"},
		// A typo does not fail at runtime, it reads nothing - and an estimate
		// made from a missing "resolution" is the cheapest tier every time.
		{"path that reads nothing", func(e *manifest.Endpoint) { e.UsageRequestFields = []string{"content.#(type==image)"} },
			"endpoints[0].usageRequestFields[0]", "invalid_path"},
		{"untrimmed path", func(e *manifest.Endpoint) { e.UsageRequestFields = []string{" resolution"} },
			"endpoints[0].usageRequestFields[0]", "invalid_path"},
		{"duplicate", func(e *manifest.Endpoint) { e.UsageRequestFields = []string{"resolution", "resolution"} },
			"endpoints[0].usageRequestFields[1]", "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pluginSourcePlatform()
			tc.mut(&p.Endpoints[0])
			got := platformCodes(p)
			if got[tc.field] != tc.code {
				t.Fatalf("codes = %v, want %s=%s", got, tc.field, tc.code)
			}
		})
	}
}

// billing "free" with usageSource "plugin" (CONTRACTS §25.5 gap 3): the host
// never prices a free endpoint, so whatever the plugin reports - and any
// Reservation it returns - is dropped before the settler sees it. That used
// to happen silently; it is now refused at install time. The rule reads the
// endpoint only, so it holds on the CheckPlatform path as well.
func TestPluginUsageSourceRefusesFreeBilling(t *testing.T) {
	p := pluginSourcePlatform()
	p.Endpoints[0].Billing = "free"
	p.Endpoints[0].BillingTypes = nil
	if got := platformCodes(p); got["endpoints[0].billing"] != "conflict" {
		t.Fatalf("codes = %v", got)
	}
	// The declarative source is what a free endpoint should use, and it is
	// accepted exactly as before.
	p = validPlatform()
	p.Endpoints[0].Billing = "free"
	p.Endpoints[0].BillingTypes = nil
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("free endpoint on the declarative rules rejected: %v", got)
	}
}

// pluginSourceManifest is a manifest declaring pluginSourcePlatform, with the
// permissions a platform needs (CONTRACTS §13) but WITHOUT
// platform.adapter.v1 - the case §25.1 requires to be rejected.
func pluginSourceManifest() (*manifest.Manifest, map[string][]byte) {
	m := minimal()
	m.HostPermissions = []manifest.HostPermission{{ID: "gateway.endpoint"}, {ID: "platform.register"}}
	m.Platforms = []manifest.Platform{pluginSourcePlatform()}
	return m, nil
}

// Declaring a platform needs only gateway.endpoint + platform.register, so a
// perfectly legal plugin can declare a platform and have no PlatformService
// at all. An endpoint whose usage that missing service is supposed to report
// must be refused at install time; at request time the host would have
// nothing to call.
func TestPluginUsageSourceRequiresPlatformAdapter(t *testing.T) {
	m, files := pluginSourceManifest()
	got := codes(Validate(m, files, ValidateOptions{Tooling: true}))
	if got["platforms[0].endpoints[0].usageSource"] != "missing_capability" {
		t.Fatalf("codes = %v", got)
	}
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("with platform.adapter.v1: %v", codes(err))
	}
}

// CheckPlatform has no manifest around the platform, so the capability rule
// must not fire there: the core's own built-in platforms come through that
// path, and a rule that wrongly fires rejects what the core ships
// (CONTRACTS §25.2, validator.standalone).
func TestPluginUsageSourceCapabilityNotCheckedStandalone(t *testing.T) {
	if got := platformCodes(pluginSourcePlatform()); len(got) > 0 {
		t.Fatalf("standalone CheckPlatform reported %v", got)
	}
	for _, p := range platforms.Builtin() {
		if fe := CheckPlatform(p); len(fe) > 0 {
			t.Errorf("built-in platform %s: %v", p.ID, fe)
		}
	}
}

// The hole the move closes: an account type override replaces the whole
// UsageRules block, so while the source lived in there an override that did
// not mention it silently switched the endpoint back to the declarative
// rules. The override has no way to reach the source any more - it is not a
// field of UsageRules at all - so an account type can override the maps and
// facts of a plugin-sourced endpoint without touching who answers.
func TestAccountTypeOverrideCannotReachUsageSource(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{pluginSourcePlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "apikey", Label: manifest.LocalizedText{"en": "Key"},
		Form: keyForm(m),
		Platforms: []manifest.AccountPlatform{{Platform: "video", Usage: map[string]manifest.UsageRules{
			// No source to write here, and the fact it declares needs no path
			// because the endpoint it overrides is answered by the plugin.
			"video.gen": {Facts: map[string]manifest.UsageFact{"images": {Type: "number"}}},
		}}},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("account type override rejected: %v", codes(err))
	}
	// The endpoint still says "plugin": nothing in the account type moved it.
	if !m.Platforms[0].Endpoints[0].PluginUsage() {
		t.Fatal("the endpoint's usage source was reachable from an account type")
	}
}
