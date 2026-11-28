package check

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// boundHeadersCase is one accountBoundHeaders entry and the error code
// it gets ("" = none).
type boundHeadersCase struct {
	name     string
	platform string
	pass     []string
	bound    []string
	tooling  bool
	code     string
}

var turnHeader = []string{"x-codex-turn-state"}

var boundHeadersCases = []boundHeadersCase{
	{"absent", "openai", turnHeader, nil, false, ""},
	{"own passHeaders", "openai", turnHeader, []string{"x-codex-turn-state"}, false, ""},
	{"case-insensitive", "openai", turnHeader, []string{"X-Codex-Turn-State"}, false, ""},
	{"empty", "openai", turnHeader, []string{}, false, "required"},
	{"not passed", "openai", turnHeader, []string{"openai-beta"}, false, "not_passed"},
	{"duplicate", "openai", turnHeader, []string{"x-codex-turn-state", "X-CODEX-TURN-STATE"}, false, "duplicate"},
	{"none passed", "openai", []string{}, []string{"x-codex-turn-state"}, false, "not_passed"},
	{"built-in platform's list", "openai", nil, []string{"openai-beta"}, false, ""},
	{"built-in platform's list misses it", "openai", nil, []string{"x-codex-turn-state"}, false, "not_passed"},
	{"own platform's list", "mine", nil, []string{"x-mine-token"}, false, ""},
	{"other plugin's platform", "video", nil, []string{"x-video-session"}, false, ""},
	{"missing platform", "missing", nil, []string{"x-a"}, false, "unknown_platform"},
	{"missing platform offline", "missing", nil, []string{"x-a"}, true, ""},
	{"missing platform with its own list", "missing", []string{"x-a"}, []string{"x-a"}, false, ""},
}

func TestAccountBoundHeaders(t *testing.T) {
	own := &manifest.Platform{ID: "mine", PassHeaders: []string{"X-Mine-Token"}}
	for _, tc := range boundHeadersCases {
		t.Run(tc.name, func(t *testing.T) {
			v := validator{opt: ValidateOptions{Tooling: tc.tooling, OtherPlatforms: []PlatformOwner{{ID: "video", PassHeaders: []string{"x-video-session"}}}}}
			var p *manifest.Platform
			if tc.platform == "mine" {
				p = own
			}
			v.accountBoundHeaders("platforms[0]", manifest.AccountPlatform{Platform: tc.platform, PassHeaders: tc.pass, AccountBoundHeaders: tc.bound}, p)
			if (tc.code == "" && len(v.errs) != 0) || (tc.code != "" && (len(v.errs) != 1 || v.errs[0].Code != tc.code)) {
				t.Fatalf("errors = %+v, want %q", v.errs, tc.code)
			}
		})
	}
}

// accountPlatform runs the accountBoundHeaders check for every account type
// entry, and OthersFromManifests carries another plugin's passHeaders to it.
func TestAccountPlatformChecksAccountBoundHeaders(t *testing.T) {
	v := validator{}
	v.accountPlatform("accountTypes[0].platforms[0]", manifest.AccountPlatform{Platform: "openai", AccountBoundHeaders: []string{"x-nope"}}, map[string]bool{}, nil)
	if len(v.errs) != 1 || v.errs[0].Field != "accountTypes[0].platforms[0].accountBoundHeaders[0]" || v.errs[0].Code != "not_passed" {
		t.Fatalf("errors = %+v", v.errs)
	}
	pfs, _ := OthersFromManifests([]*manifest.Manifest{{Key: "video", Platforms: []manifest.Platform{{ID: "video", PassHeaders: []string{"x-video-session"}}}}})
	if len(pfs) != 1 || len(pfs[0].PassHeaders) != 1 || pfs[0].PassHeaders[0] != "x-video-session" {
		t.Fatalf("others = %+v", pfs)
	}
}
