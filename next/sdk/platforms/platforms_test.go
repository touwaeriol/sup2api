package platforms

import (
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

func TestBuiltinPlatforms(t *testing.T) {
	ids := []string{}
	protocols := map[string]string{}
	for _, p := range Builtin() {
		ids = append(ids, p.ID)
		if len(p.Endpoints) == 0 {
			t.Fatalf("%s has no endpoints", p.ID)
		}
		if p.Usage.Semantics != "exclusive" && p.Usage.Semantics != "inclusive" {
			t.Errorf("%s: usage semantics %q", p.ID, p.Usage.Semantics)
		}
		endpointIDs := map[string]bool{}
		for _, e := range p.Endpoints {
			if !strings.HasPrefix(e.Protocol, p.ID+".") {
				t.Errorf("%s: protocol %q must start with the platform id", p.ID, e.Protocol)
			}
			if owner, dup := protocols[e.Protocol]; dup && owner != p.ID {
				t.Errorf("protocol %s declared by %s and %s", e.Protocol, owner, p.ID)
			}
			protocols[e.Protocol] = p.ID
			if endpointIDs[e.ID] {
				t.Errorf("%s: duplicate endpoint id %s", p.ID, e.ID)
			}
			endpointIDs[e.ID] = true
			if (e.Kind != manifest.EndpointKindProxy && e.Kind != manifest.EndpointKindWebSocket) || (e.Billing != "usage" && e.Billing != "free") || e.ErrorFormat != p.ID {
				t.Errorf("%s/%s: kind %q billing %q errorFormat %q", p.ID, e.ID, e.Kind, e.Billing, e.ErrorFormat)
			}
			if e.Request.ModelPath == "" && e.Request.ModelParam == "" {
				t.Errorf("%s/%s: no model source", p.ID, e.ID)
			}
			if e.Request.ModelParam != "" && !strings.Contains(e.Path, ":"+e.Request.ModelParam) {
				t.Errorf("%s/%s: modelParam %q not in path %s", p.ID, e.ID, e.Request.ModelParam, e.Path)
			}
			if len(e.Auth.Headers) == 0 && e.Auth.Query == "" {
				t.Errorf("%s/%s: no auth source", p.ID, e.ID)
			}
			if u := e.Usage; u != nil && u.Semantics == "" {
				t.Errorf("%s/%s: endpoint usage without semantics", p.ID, e.ID)
			}
		}
		for _, r := range p.StickyRules {
			// Built-in rules match by protocol only (user requirement 2026-11-10).
			if len(r.Match.Protocols) == 0 || len(r.Match.Models) > 0 || len(r.Match.UserAgentContains) > 0 {
				t.Errorf("%s: sticky rule %s must match by protocol only, got %+v", p.ID, r.Name, r.Match)
			}
			for _, pr := range r.Match.Protocols {
				if !slices.Contains(p.Protocols(), pr) {
					t.Errorf("%s: sticky rule %s matches foreign protocol %s", p.ID, r.Name, pr)
				}
			}
		}
	}
	if !slices.Equal(ids, []string{manifest.PlatformAnthropic, manifest.PlatformGemini, manifest.PlatformOpenAI}) {
		t.Fatalf("builtin ids %v", ids)
	}
	if !IsBuiltin("anthropic") || !IsBuiltin("openai") || !IsBuiltin("gemini") || IsBuiltin("relay") {
		t.Fatal("IsBuiltin")
	}
}

// No two built-in endpoints may match the same request (same method and
// overlapping path patterns, including ":param" and ":param:suffix").
func TestBuiltinEndpointsDoNotConflict(t *testing.T) {
	type ep struct{ owner, method, path string }
	var all []ep
	for _, p := range Builtin() {
		for _, e := range p.Endpoints {
			all = append(all, ep{p.ID + "/" + e.ID, e.Method, e.Path})
		}
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a.method == b.method && patternsOverlap(a.path, b.path) {
				t.Errorf("%s %s overlaps %s %s", a.owner, a.path, b.owner, b.path)
			}
		}
	}
	// The overlap check itself.
	if !patternsOverlap("/v1/:x", "/v1/messages") || patternsOverlap("/v1beta/models/:m:generateContent", "/v1beta/models/:m:countTokens") ||
		!patternsOverlap("/v1beta/models/:m:countTokens", "/v1beta/models/:model") || patternsOverlap("/v1/a", "/v1/a/b") {
		t.Fatal("patternsOverlap")
	}
}

func TestProtocols(t *testing.T) {
	want := map[string][]string{
		"anthropic": {"anthropic.messages", "anthropic.count_tokens"},
		"openai":    {"openai.chat", "openai.responses", "openai.responses_ws", "openai.embeddings", "openai.images", "openai.images_edit"},
		"gemini":    {"gemini.generate", "gemini.stream_generate", "gemini.count_tokens", "gemini.embed", "gemini.batch_embed"},
	}
	for _, p := range Builtin() {
		if got := p.Protocols(); !slices.Equal(got, want[p.ID]) {
			t.Errorf("%s protocols %v, want %v", p.ID, got, want[p.ID])
		}
	}
}

// Only the image endpoints restrict the account test: a generation costs an
// image (explicit), an edit needs an input image (never).
func TestEndpointTests(t *testing.T) {
	want := map[string]string{"openai.images": manifest.EndpointTestExplicit, "openai.images_edit": manifest.EndpointTestNever}
	for _, p := range Builtin() {
		for _, e := range p.Endpoints {
			if e.Test != want[e.Protocol] {
				t.Errorf("%s: test %q, want %q", e.Protocol, e.Test, want[e.Protocol])
			}
		}
	}
}

// patternsOverlap reports whether some path matches both patterns (segments
// "static", ":param", ":param:suffix").
func patternsOverlap(a, b string) bool {
	sa, sb := strings.Split(strings.Trim(a, "/"), "/"), strings.Split(strings.Trim(b, "/"), "/")
	if len(sa) != len(sb) {
		return false
	}
	for i := range sa {
		if !segmentsOverlap(sa[i], sb[i]) {
			return false
		}
	}
	return true
}

func segmentsOverlap(a, b string) bool {
	suffix := func(s string) (param bool, suf string) {
		if !strings.HasPrefix(s, ":") {
			return false, s
		}
		if i := strings.IndexByte(s[1:], ':'); i >= 0 {
			return true, s[1+i:]
		}
		return true, ""
	}
	pa, xa := suffix(a)
	pb, xb := suffix(b)
	switch {
	case !pa && !pb:
		return a == b
	case pa && pb:
		return strings.HasSuffix(xa, xb) || strings.HasSuffix(xb, xa)
	case pa:
		return len(b) > len(xa) && strings.HasSuffix(b, xa)
	default:
		return len(a) > len(xb) && strings.HasSuffix(a, xb)
	}
}
