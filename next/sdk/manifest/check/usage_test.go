package check

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// A platform that satisfies every endpoint and usage rule; the tables below
// break one thing at a time.
func validPlatform() manifest.Platform {
	return manifest.Platform{
		ID: "video",
		Endpoints: []manifest.Endpoint{{
			ID: "gen", Method: "POST", Path: "/video/v1/generations", Protocol: "video.gen", Kind: "proxy",
			Auth:        manifest.EndpointAuth{Headers: []string{"authorization"}},
			Request:     manifest.EndpointRequest{ModelPath: "model"},
			Response:    manifest.EndpointResp{NonStream: "json"},
			ErrorFormat: "plain", Billing: "usage", BillingTypes: []string{"per_request", "per_token", "expression"},
		}},
		Usage: manifest.UsageRules{
			Semantics: "inclusive",
			JSON: &manifest.UsageMap{Map: map[string]string{
				"model":        "model",
				"input_tokens": "usage.prompt_tokens",
			}},
			SSE: []manifest.SSEUsageMap{{Map: map[string]string{
				"output_tokens": "usage.completion_tokens + usage.thoughts_tokens",
			}}},
			Facts: map[string]manifest.UsageFact{
				"images": {Type: "number", Unit: "image", Path: "usage.generated_images"},
			},
		},
	}
}

func platformCodes(p manifest.Platform) map[string]string {
	out := map[string]string{}
	for _, fe := range CheckPlatform(p) {
		out[fe.Field] = fe.Code
	}
	return out
}

func TestValidPlatformPasses(t *testing.T) {
	if got := platformCodes(validPlatform()); len(got) > 0 {
		t.Fatalf("valid platform rejected: %v", got)
	}
	// An endpoint that always streams needs no response.nonStream, and an
	// endpoint override inherits semantics from the platform. The override
	// must still carry an sse rule: it replaces the platform rules wholesale,
	// and a streaming response is metered from usage.sse alone.
	p := validPlatform()
	p.Endpoints[0].Request = manifest.EndpointRequest{ModelPath: "model", Stream: true}
	p.Endpoints[0].Response = manifest.EndpointResp{Stream: "sse"}
	p.Endpoints[0].Usage = &manifest.UsageRules{
		JSON: &manifest.UsageMap{Map: map[string]string{"input_tokens": "usage.in"}},
		SSE:  []manifest.SSEUsageMap{{Map: map[string]string{"output_tokens": "usage.out"}}},
	}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("streaming endpoint rejected: %v", got)
	}
}

// The rules the core never applied before: an endpoint field the gateway reads
// at runtime may not be left to a default (ARCHITECTURE 6.6).
func TestEndpointContract(t *testing.T) {
	cases := []struct {
		name        string
		mut         func(e *manifest.Endpoint)
		field, code string
	}{
		{"errorFormat missing", func(e *manifest.Endpoint) { e.ErrorFormat = "" },
			"endpoints[0].errorFormat", "required"},
		{"errorFormat unknown", func(e *manifest.Endpoint) { e.ErrorFormat = "xml" },
			"endpoints[0].errorFormat", "invalid"},
		{"errorFormat case", func(e *manifest.Endpoint) { e.ErrorFormat = "Anthropic" },
			"endpoints[0].errorFormat", "invalid"},
		{"billing missing", func(e *manifest.Endpoint) { e.Billing = "" },
			"endpoints[0].billing", "required"},
		{"billing unknown", func(e *manifest.Endpoint) { e.Billing = "metered" },
			"endpoints[0].billing", "invalid"},
		{"nonStream missing", func(e *manifest.Endpoint) { e.Response = manifest.EndpointResp{} },
			"endpoints[0].response.nonStream", "required"},
		{"model source both", func(e *manifest.Endpoint) {
			e.Path, e.Request = "/video/v1/models/:model:run", manifest.EndpointRequest{ModelPath: "model", ModelParam: "model"}
		}, "endpoints[0].request", "mutually_exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPlatform()
			tc.mut(&p.Endpoints[0])
			if got := platformCodes(p); got[tc.field] != tc.code {
				t.Fatalf("want %s=%s, got %v", tc.field, tc.code, got)
			}
		})
	}
}

// Usage rules decide what is billed, so everything they declare is checked:
// a rule the host cannot read costs money silently.
func TestUsageRules(t *testing.T) {
	cases := []struct {
		name        string
		mut         func(u *manifest.UsageRules)
		field, code string
	}{
		{"semantics required on the platform", func(u *manifest.UsageRules) { u.Semantics = "" },
			"usage.semantics", "required"},
		{"semantics unknown", func(u *manifest.UsageRules) { u.Semantics = "cumulative" },
			"usage.semantics", "invalid"},

		// Map keys: an unknown key is filed as a metric nothing can reach.
		{"json map key", func(u *manifest.UsageRules) { u.JSON.Map["total_tokens"] = "usage.total_tokens" },
			"usage.json.map.total_tokens", "unknown_field"},
		{"sse map key", func(u *manifest.UsageRules) { u.SSE[0].Map["ouput_tokens"] = "usage.out" },
			"usage.sse[0].map.ouput_tokens", "unknown_field"},

		// Map values are gjson paths, optionally summed with "+".
		{"json map path malformed", func(u *manifest.UsageRules) { u.JSON.Map["input_tokens"] = "usage..[[" },
			"usage.json.map.input_tokens", "invalid_path"},
		{"json map path empty", func(u *manifest.UsageRules) { u.JSON.Map["input_tokens"] = "" },
			"usage.json.map.input_tokens", "required"},
		{"json map path query", func(u *manifest.UsageRules) { u.JSON.Map["input_tokens"] = "#(kind==a).n" },
			"usage.json.map.input_tokens", "invalid_path"},
		{"sse map sum term malformed", func(u *manifest.UsageRules) { u.SSE[0].Map["output_tokens"] = "usage.a + @nope" },
			"usage.sse[0].map.output_tokens", "invalid_path"},
		{"sse map sum term empty", func(u *manifest.UsageRules) { u.SSE[0].Map["output_tokens"] = "usage.a++usage.b" },
			"usage.sse[0].map.output_tokens", "invalid_path"},

		// Facts: the u() keys of price expressions.
		{"fact path required", func(u *manifest.UsageRules) {
			u.Facts["images"] = manifest.UsageFact{Type: "number"}
		}, "usage.facts.images.path", "required"},
		{"fact path malformed", func(u *manifest.UsageRules) {
			u.Facts["images"] = manifest.UsageFact{Type: "number", Path: "usage..[["}
		}, "usage.facts.images.path", "invalid_path"},
		{"fact type required", func(u *manifest.UsageRules) {
			u.Facts["images"] = manifest.UsageFact{Path: "usage.generated_images"}
		}, "usage.facts.images.type", "required"},
		{"fact type unknown", func(u *manifest.UsageRules) {
			u.Facts["images"] = manifest.UsageFact{Type: "vector", Path: "usage.generated_images"}
		}, "usage.facts.images.type", "invalid"},
		{"fact enum empty", func(u *manifest.UsageRules) {
			u.Facts["tier"] = manifest.UsageFact{Type: "enum", Path: "usage.tier"}
		}, "usage.facts.tier.enum", "required"},
		{"fact enum without enum type", func(u *manifest.UsageRules) {
			u.Facts["images"] = manifest.UsageFact{Type: "number", Path: "usage.n", Enum: []string{"a"}}
		}, "usage.facts.images.enum", "unsupported"},
		{"fact key empty", func(u *manifest.UsageRules) {
			u.Facts[""] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.", "invalid_format"},
		// The key is both the u("…") literal of a price expression and a JSON
		// key of usage_logs.metrics, so it must be a plain snake_case name.
		{"fact key upper case", func(u *manifest.UsageRules) {
			u.Facts["Images"] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.Images", "invalid_format"},
		{"fact key dotted", func(u *manifest.UsageRules) {
			u.Facts["usage.images"] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.usage.images", "invalid_format"},
		{"fact key non-ascii", func(u *manifest.UsageRules) {
			u.Facts["图片"] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.图片", "invalid_format"},
		{"fact key leading digit", func(u *manifest.UsageRules) {
			u.Facts["1st"] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.1st", "invalid_format"},
		{"fact key too long", func(u *manifest.UsageRules) {
			u.Facts["a"+strings.Repeat("b", 64)] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.a" + strings.Repeat("b", 64), "invalid_format"},
		// A fact named like a standard usage field would put a second,
		// different number under a name usage_logs already uses.
		{"fact key shadows a usage field", func(u *manifest.UsageRules) {
			u.Facts["input_tokens"] = manifest.UsageFact{Type: "number", Path: "usage.n"}
		}, "usage.facts.input_tokens", "reserved"},
		{"fact key shadows model", func(u *manifest.UsageRules) {
			u.Facts["model"] = manifest.UsageFact{Type: "enum", Path: "usage.m", Enum: []string{"a"}}
		}, "usage.facts.model", "reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPlatform()
			tc.mut(&p.Usage)
			if got := platformCodes(p); got[tc.field] != tc.code {
				t.Fatalf("want %s=%s, got %v", tc.field, tc.code, got)
			}
		})
	}

	// semantics is required of the platform only: an endpoint or account type
	// override inherits it, and both still get every other usage check.
	p := validPlatform()
	p.Endpoints[0].Usage = &manifest.UsageRules{JSON: &manifest.UsageMap{Map: map[string]string{"total_tokens": "usage.t"}}}
	if got := platformCodes(p); got["endpoints[0].usage.semantics"] != "" ||
		got["endpoints[0].usage.json.map.total_tokens"] != "unknown_field" {
		t.Fatalf("endpoint override: %v", got)
	}
}

// The image token fields are standard fields: a rule may map them, and a fact
// may not take their names.
func TestUsageImageTokenFields(t *testing.T) {
	p := validPlatform()
	p.Usage.JSON.Map[manifest.UsageImageInputTokens] = "usage.input_tokens_details.image_tokens"
	p.Usage.SSE[0].Map[manifest.UsageImageOutputTokens] = "usage.output_tokens"
	require.Empty(t, platformCodes(p))

	p = validPlatform()
	p.Usage.Facts[manifest.UsageImageOutputTokens] = manifest.UsageFact{Type: "number", Path: "usage.n"}
	require.Equal(t, "reserved", platformCodes(p)["usage.facts.image_output_tokens"])
}

// TestStreamingEndpointNeedsSSERules pins the shape that silently swallows
// usage: response.stream declared, billing not "free", and no non-empty sse
// rule in the usage rules that are actually in effect for the endpoint.
func TestStreamingEndpointNeedsSSERules(t *testing.T) {
	const field = "endpoints[0].usage.sse"

	// Platform rules carry the sse map: an endpoint without its own usage
	// inherits them and passes.
	p := validPlatform()
	p.Endpoints[0].Response = manifest.EndpointResp{Stream: "sse", NonStream: "json"}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("stream with platform sse rules rejected: %v", got)
	}

	// The endpoint overrides usage with a JSON-only map. The override replaces
	// the platform rules wholesale, so nothing is left to read a streaming
	// response with: this is the case that bills every stream as zero tokens.
	p.Endpoints[0].Usage = &manifest.UsageRules{JSON: &manifest.UsageMap{Map: map[string]string{"input_tokens": "usage.in"}}}
	if got := platformCodes(p); got[field] != "required" {
		t.Fatalf("stream with empty sse override accepted: %v", got)
	}

	// An sse entry with an empty map extracts nothing either.
	p.Endpoints[0].Usage.SSE = []manifest.SSEUsageMap{{Event: "done"}}
	if got := platformCodes(p); got[field] != "required" {
		t.Fatalf("stream with an empty sse map accepted: %v", got)
	}

	// One non-empty sse map is enough.
	p.Endpoints[0].Usage.SSE = []manifest.SSEUsageMap{{Event: "done", Map: map[string]string{"output_tokens": "usage.out"}}}
	if got := platformCodes(p); len(got) > 0 {
		t.Fatalf("stream with an sse override rejected: %v", got)
	}

	// billing "free" is never metered, so the same endpoint is fine without
	// any sse rule.
	free := validPlatform()
	free.Endpoints[0].Response = manifest.EndpointResp{Stream: "sse", NonStream: "json"}
	free.Endpoints[0].Billing = "free"
	free.Endpoints[0].BillingTypes = nil
	free.Endpoints[0].Usage = &manifest.UsageRules{JSON: &manifest.UsageMap{Map: map[string]string{"input_tokens": "usage.in"}}}
	if got := platformCodes(free); len(got) > 0 {
		t.Fatalf("free streaming endpoint rejected: %v", got)
	}

	// An endpoint that does not declare response.stream is out of scope even
	// when it bills and has no sse rule at all (the Ark image endpoint).
	nonStream := validPlatform()
	nonStream.Endpoints[0].Usage = &manifest.UsageRules{JSON: &manifest.UsageMap{Map: map[string]string{"input_tokens": "usage.in"}}}
	if got := platformCodes(nonStream); len(got) > 0 {
		t.Fatalf("non-streaming endpoint rejected: %v", got)
	}

	// And the empty platform default is caught for an endpoint that does not
	// override usage at all.
	bare := validPlatform()
	bare.Usage = manifest.UsageRules{Semantics: "inclusive",
		JSON: &manifest.UsageMap{Map: map[string]string{"input_tokens": "usage.in"}}}
	bare.Endpoints[0].Response = manifest.EndpointResp{Stream: "sse", NonStream: "json"}
	if got := platformCodes(bare); got[field] != "required" {
		t.Fatalf("empty platform sse rules accepted: %v", got)
	}
}

// TestBuiltinPlatformsHaveStreamUsage holds the core's own platforms to the
// rule: CheckPlatform runs with no manifest, and a rule that reads v.m there
// would either never fire or reject everything the core ships.
func TestBuiltinPlatformsHaveStreamUsage(t *testing.T) {
	for _, p := range platforms.Builtin() {
		if fe := CheckPlatform(p); len(fe) > 0 {
			t.Errorf("built-in platform %s: %v", p.ID, fe)
		}
	}
}

// The account type overrides go through the same rules (Validate, not
// CheckPlatform, reaches them).
func TestAccountTypeUsageOverride(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "apikey", Label: manifest.LocalizedText{"en": "Key"},
		Form:      keyForm(m),
		Platforms: []manifest.AccountPlatform{{Platform: "video", Usage: map[string]manifest.UsageRules{"video.gen": {}}}},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("valid override rejected: %v", codes(err))
	}
	m.AccountTypes[0].Platforms[0].Usage["video.gen"] = manifest.UsageRules{
		Facts: map[string]manifest.UsageFact{"secs": {Type: "number", Path: "usage..[["}},
	}
	got := codes(Validate(m, files, ValidateOptions{Tooling: true}))
	if got["accountTypes[0].platforms[0].usage.video.gen.facts.secs.path"] != "invalid_path" {
		t.Fatalf("codes = %v", got)
	}
}

func TestValidUsagePath(t *testing.T) {
	good := []string{
		"model", "usage.input_tokens", "usage.cache_creation.ephemeral_1h_input_tokens",
		"choices.0.message.content", `a\.b`, "data.#", "usageMetadata.promptTokenCount",
	}
	for _, p := range good {
		if !ValidUsagePath(p) {
			t.Errorf("ValidUsagePath(%q) = false, want true", p)
		}
	}
	// ".a" is deliberately absent: it is the key "" of the key "a", an odd
	// but well-formed path that gjson reads, so the round trip accepts it.
	bad := []string{
		"", ".", "a.", "a..b", "usage..[[", "#", "#(a==1).b", "#(a==1.b",
		"@ugly", "@nope", "@this", "{a,b}", "{a,b", "a|b", "messages.#.content", "usage.*",
	}
	for _, p := range bad {
		if ValidUsagePath(p) {
			t.Errorf("ValidUsagePath(%q) = true, want false", p)
		}
	}
}

// Default models and mapping of an account type (CONTRACTS §41) follow the
// rules of the account fields they prefill.
func TestAccountTypeDefaultModels(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	m.AccountTypes = []manifest.AccountType{{
		ID: "apikey", Label: manifest.LocalizedText{"en": "Key"},
		Form:                keyForm(m),
		Platforms:           []manifest.AccountPlatform{{Platform: "video"}},
		DefaultModels:       []string{"video-2", "video-2-260101"},
		DefaultModelMapping: map[string]string{"video-2": "video-2-260101"},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("valid defaults rejected: %v", codes(err))
	}
	off := false
	control := manifest.AccountCommonControl{Configurable: &off, Default: json.RawMessage(`false`)}
	require.False(t, control.IsConfigurable())
	require.False(t, control.DefaultValue())
	require.True(t, (manifest.AccountCommonControl{}).IsConfigurable())
	require.True(t, (manifest.AccountCommonControl{}).DefaultValue())
	m.AccountTypes[0].CommonControls = map[string]manifest.AccountCommonControl{"auto_disable": control, "schedulable": {}}
	require.NoError(t, Validate(m, files, ValidateOptions{Tooling: true}))
	m.AccountTypes[0].CommonControls["status"] = control
	require.Equal(t, "unsupported", codes(Validate(m, files, ValidateOptions{Tooling: true}))["accountTypes[0].commonControls.status"])
	delete(m.AccountTypes[0].CommonControls, "status")
	// A mapping without a model list is fine: an empty list serves every model.
	m.AccountTypes[0].DefaultModels = nil
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("mapping without models rejected: %v", codes(err))
	}
	m.AccountTypes[0].DefaultModels = []string{"video-*", "a", "a", "video-3"}
	m.AccountTypes[0].DefaultModelMapping = map[string]string{"video-2": "video-2-260101", "video-3": "bad id"}
	got := codes(Validate(m, files, ValidateOptions{Tooling: true}))
	want := map[string]string{
		"accountTypes[0].defaultModels[0]":            "invalid",
		"accountTypes[0].defaultModels[2]":            "duplicate",
		"accountTypes[0].defaultModelMapping.video-2": "not_in_models",
		"accountTypes[0].defaultModelMapping.video-3": "invalid",
	}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	m.AccountTypes[0].DefaultModelMapping = nil
	m.AccountTypes[0].DefaultModels = make([]string, manifest.MaxDefaultModels+1)
	for i := range m.AccountTypes[0].DefaultModels {
		m.AccountTypes[0].DefaultModels[i] = "m-" + strconv.Itoa(i)
	}
	if got := codes(Validate(m, files, ValidateOptions{Tooling: true})); got["accountTypes[0].defaultModels"] != "too_many" {
		t.Fatalf("codes = %v", got)
	}
}

func TestAccountTypeDefaultLimits(t *testing.T) {
	m := minimal()
	m.Capabilities = []manifest.Capability{{ID: manifest.CapPlatformAdapter}}
	m.HostPermissions = []manifest.HostPermission{
		{ID: "gateway.endpoint"}, {ID: "platform.register"},
	}
	m.Platforms = []manifest.Platform{validPlatform()}
	prio, weight, rpm, spm := 5, 20, 60, 3
	m.AccountTypes = []manifest.AccountType{{
		ID: "apikey", Label: manifest.LocalizedText{"en": "Key"},
		Form:      keyForm(m),
		Platforms: []manifest.AccountPlatform{{Platform: "video"}},
		DefaultLimits: &manifest.AccountDefaultLimits{Priority: &prio, Weight: &weight, RPMLimit: &rpm, SPMLimit: &spm,
			ModelLimits: []manifest.DefaultModelLimit{{Models: []string{"video-2", "video-3"}, MaxConcurrency: 2, SPMLimit: 1}}},
	}}
	files := map[string][]byte{}
	if err := Validate(m, files, ValidateOptions{Tooling: true}); err != nil {
		t.Fatalf("valid default limits rejected: %v", codes(err))
	}
	zero, negative := 0, -1
	m.AccountTypes[0].DefaultLimits.Weight = &zero
	m.AccountTypes[0].DefaultLimits.SPMLimit = &negative
	m.AccountTypes[0].DefaultLimits.ModelLimits = append(m.AccountTypes[0].DefaultLimits.ModelLimits,
		manifest.DefaultModelLimit{Models: []string{"video-*", "video-3"}, RPMLimit: -1, SPMLimit: 10_000_001}, manifest.DefaultModelLimit{})
	got := codes(Validate(m, files, ValidateOptions{Tooling: true}))
	f := "accountTypes[0].defaultLimits."
	want := map[string]string{
		f + "weight":                   "invalid",
		f + "spmLimit":                 "invalid",
		f + "modelLimits[1].models[0]": "invalid",
		f + "modelLimits[1].models[1]": "duplicate",
		f + "modelLimits[1].rpmLimit":  "invalid",
		f + "modelLimits[1].spmLimit":  "invalid",
		f + "modelLimits[2].models":    "required",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}
