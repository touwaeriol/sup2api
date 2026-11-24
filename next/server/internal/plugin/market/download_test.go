package market

import (
	"net/url"
	"testing"
)

// A package of the official store comes from a GitHub release asset; a
// local market (SUB2API_MARKET_BASE_URL or SUB2API_MARKET_ALLOW_HTTP)
// serves its packages anywhere.
func TestDownloadURLReleaseAsset(t *testing.T) {
	const asset = "https://github.com/touwaeriol/sup2api-plugin/releases/download/codex-v1.2.0/codex-1.2.0.s2plugin"
	others := []string{
		"https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-main/codex.s2plugin",
		"https://github.com/acme/tool/archive/v1.s2plugin",
		"https://github.com/acme/tool/releases/download/v1/tool.zip",
		"https://evil.example/acme/tool/releases/download/v1/tool.s2plugin",
		"pkgs/tool-1.1.0.s2plugin",
	}
	base, _ := url.Parse(channelURL(OfficialBase, "main"))
	for _, c := range []struct {
		opt     Options
		allowed []string
		refused []string
	}{
		{Options{}, []string{asset}, others},
		{Options{BaseURL: "https://market.example/m"}, append(others, asset), nil},
		{Options{AllowHTTP: true}, append(others, asset), nil},
	} {
		s := New(nil, fakeInstaller{}, nil, c.opt)
		for _, u := range c.allowed {
			if _, err := s.downloadURL(&entry{base: base, version: &IndexVersion{URL: u}}); err != nil {
				t.Errorf("%+v %s: %v", c.opt, u, err)
			}
		}
		for _, u := range c.refused {
			if _, err := s.downloadURL(&entry{base: base, version: &IndexVersion{URL: u}}); err == nil {
				t.Errorf("%+v %s: allowed", c.opt, u)
			}
		}
	}
}
