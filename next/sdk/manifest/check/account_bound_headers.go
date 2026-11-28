package check

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// accountBoundHeaders validates accountTypes[].platforms[].accountBoundHeaders
// (CONTRACTS §141.4): each entry names, once (case-insensitively), a header
// of the passHeaders the account type receives on the platform
// (AccountPlatform.ClientHeaders). When the entry writes no passHeaders of
// its own they are the platform's, so the platform must be known; offline
// packaging that cannot resolve another plugin's platform leaves the check
// to the install.
func (v *validator) accountBoundHeaders(field string, ap manifest.AccountPlatform, own *manifest.Platform) {
	if ap.AccountBoundHeaders == nil {
		return
	}
	field += ".accountBoundHeaders"
	if len(ap.AccountBoundHeaders) == 0 {
		v.add(field, "required", "at least one header is required when accountBoundHeaders is specified")
		return
	}
	var p *manifest.Platform
	if ap.PassHeaders == nil {
		if p = v.referencedPlatform(ap.Platform, own); p == nil {
			if !v.opt.Tooling {
				v.add(field, "unknown_platform", "platform %q must be installed to validate accountBoundHeaders", ap.Platform)
			}
			return
		}
	}
	v.boundHeaderNames(field, ap, ap.ClientHeaders(p))
}

// boundHeaderNames checks the names of accountBoundHeaders against pass, the
// client headers the account type receives.
func (v *validator) boundHeaderNames(field string, ap manifest.AccountPlatform, pass []string) {
	seen := map[string]bool{}
	for i, name := range ap.AccountBoundHeaders {
		f := fmt.Sprintf("%s[%d]", field, i)
		key := strings.ToLower(name)
		switch {
		case seen[key]:
			v.add(f, "duplicate", "header %q listed twice", name)
		case !slices.ContainsFunc(pass, func(h string) bool { return strings.EqualFold(h, name) }):
			v.add(f, "not_passed", "header %q is not one of the passHeaders the account type receives on platform %q", name, ap.Platform)
		}
		seen[key] = true
	}
}
