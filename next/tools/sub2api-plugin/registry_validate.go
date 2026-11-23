package main

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
)

// Registry identifiers. A plugin key follows the manifest's rule
// (check.ValidatePluginKey); a publisher id is the manifest's publisher. A
// GitHub login is alphanumeric with single inner hyphens, at most 39 bytes.
var (
	regPublisherRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	regRepoRe      = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9])*/[A-Za-z0-9._-]{1,100}$`)
	regLoginRe     = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9])*$`)
	regSHA256Re    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// regLoginMaxLen is GitHub's limit on a login (and an owner) name.
const regLoginMaxLen = 39

var (
	regTrustLevels = []string{"official", "verified", "community"}
	regStatuses    = []string{"active", "revoked"}
	regLanguages   = []string{"zh", "en"}
)

// problems collects "field: message" lines.
type problems []string

func (p *problems) addf(format string, args ...any) { *p = append(*p, fmt.Sprintf(format, args...)) }

// when adds the message when bad holds.
func (p *problems) when(bad bool, format string, args ...any) {
	if bad {
		p.addf(format, args...)
	}
}

func (p *problems) text(field string, t manifest.LocalizedText) {
	for _, lang := range regLanguages {
		p.when(strings.TrimSpace(t[lang]) == "", "%s.%s is required", field, lang)
	}
}

func (p *problems) deprecated(field string, d *deprecation) {
	if d != nil {
		p.text(field+".reason", d.Reason)
	}
}

func (p *problems) httpsURL(field, raw string) {
	u, err := url.Parse(raw)
	p.when(raw != "" && (err != nil || u.Scheme != "https" || u.Host == ""), "%s must be an https URL", field)
}

func checkPublisher(name string, pub *regPublisher) []string {
	var p problems
	p.when(pub.ID != name, "id %q must equal the file name %q", pub.ID, name)
	p.when(!regPublisherRe.MatchString(pub.ID), "id %q must match %s", pub.ID, regPublisherRe)
	p.text("name", pub.Name)
	p.httpsURL("homepage", pub.Homepage)
	p.when(!slices.Contains(regTrustLevels, pub.Trust), "trust must be one of %v", regTrustLevels)
	p.when(!slices.Contains(regStatuses, pub.Status), "status must be one of %v", regStatuses)
	p.logins(pub.GitHub)
	return p
}

// logins requires a non-empty list of distinct valid GitHub logins.
func (p *problems) logins(list []string) {
	p.when(len(list) == 0, "github must list at least one GitHub login")
	seen := map[string]bool{}
	for _, login := range list {
		p.when(!validLogin(login), "github login %q is invalid", login)
		p.when(seen[strings.ToLower(login)], "github login %q is listed twice", login)
		seen[strings.ToLower(login)] = true
	}
}

func validLogin(s string) bool { return len(s) <= regLoginMaxLen && regLoginRe.MatchString(s) }

// validRepo accepts owner/name with a GitHub owner and a name that is not
// "." or "..".
func validRepo(repo string) bool {
	owner, name, _ := strings.Cut(repo, "/")
	return regRepoRe.MatchString(repo) && validLogin(owner) && name != "." && name != ".."
}

func checkPlugin(name string, pl *regPlugin) []string {
	var p problems
	p.when(pl.Key != name, "key %q must equal the directory name %q", pl.Key, name)
	if err := check.ValidatePluginKey(pl.Key); err != nil {
		p.addf("key %q: %v", pl.Key, err)
	}
	p.when(pl.Publisher == "", "publisher is required")
	p.text("name", pl.Name)
	p.text("description", pl.Description)
	p.when(!validRepo(pl.Repo), "repo %q must be a GitHub owner/name", pl.Repo)
	p.httpsURL("source", pl.Source)
	p.deprecated("deprecated", pl.Deprecated)
	return p
}

func checkVersion(name string, v *regVersion) []string {
	var p problems
	p.when(v.Version != name, "version %q must equal the file name %q", v.Version, name)
	sv, err := semver.StrictNewVersion(v.Version)
	p.when(err != nil || sv.Metadata() != "", "version %q is not semver without build metadata", v.Version)
	u, err := url.Parse(v.URL)
	p.when(v.URL == "" || err != nil || strings.ContainsAny(v.URL, " \t\r\n") || u.Fragment != "" || !strings.HasSuffix(u.Path, PackageExt),
		"url %q is invalid (a %s file)", v.URL, PackageExt)
	p.when(!regSHA256Re.MatchString(v.SHA256), "sha256 must be 64 lowercase hex digits")
	p.when(v.Size <= 0 || v.Size > runtimecontract.PluginPackageMaxBytes, "size must be 1..%d", runtimecontract.PluginPackageMaxBytes)
	_, err = semver.NewConstraint(v.HostCompat)
	p.when(err != nil, "host_compat %q is not a semver range", v.HostCompat)
	p.when(v.ReleaseURL == "", "release_url is required")
	p.httpsURL("release_url", v.ReleaseURL)
	p.deprecated("deprecated", v.Deprecated)
	return p
}

// newestVersion is the highest version of e accepted by keep (nil: none).
func (e *regEntry) newestVersion(keep func(*regVersion) bool) *regVersion {
	for _, v := range e.sortedVersions() {
		if keep(v) {
			return v
		}
	}
	return nil
}

// sortedVersions lists the versions newest first.
func (e *regEntry) sortedVersions() []*regVersion {
	out := make([]*regVersion, 0, len(e.versions))
	for _, v := range e.versions {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b *regVersion) int {
		if c := semver.MustParse(b.Version).Compare(semver.MustParse(a.Version)); c != 0 {
			return c
		}
		return strings.Compare(b.Version, a.Version)
	})
	return out
}
