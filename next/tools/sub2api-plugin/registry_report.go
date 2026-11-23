package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
)

// listDiff is what a new version adds to and removes from a list.
type listDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// manifestChanges is what a reviewer looks at in a new version.
type manifestChanges struct {
	HostPermissions listDiff `json:"host_permissions"`
	HighRiskAdded   []string `json:"high_risk_added"` // added host permissions of risk high or critical
	UserPermissions listDiff `json:"user_permissions"`
	Migrations      listDiff `json:"migrations"`
	EgressDomains   listDiff `json:"egress_domains"`
}

// manifestFacts are the reviewed lists of one package.
type manifestFacts struct {
	host, user, migrations, egress []string
}

// factsOf extracts the reviewed lists; nil (no earlier version) has none.
func factsOf(i *pkgInfo) manifestFacts {
	var f manifestFacts
	if i == nil {
		return f
	}
	m := i.m
	for _, hp := range m.HostPermissions {
		f.host = append(f.host, hp.ID)
		if domains, ok := check.StringList(hp.Scope, "domains"); ok && hp.ID == "net" {
			f.egress = append(f.egress, domains...)
		}
	}
	for _, up := range m.UserPermissions {
		f.user = append(f.user, fmt.Sprintf("%s (%s)", up.Key, up.RiskLevel()))
	}
	if m.Database != nil {
		f.migrations = check.MigrationFiles(i.files, m.Database.Migrations)
	}
	f.egress = append(f.egress, m.ExternalServices...)
	return f
}

func compareManifests(old, cur manifestFacts) *manifestChanges {
	c := &manifestChanges{
		HostPermissions: diffLists(old.host, cur.host),
		UserPermissions: diffLists(old.user, cur.user),
		Migrations:      diffLists(old.migrations, cur.migrations),
		EgressDomains:   diffLists(old.egress, cur.egress),
		HighRiskAdded:   []string{},
	}
	for _, id := range c.HostPermissions.Added {
		if risk := manifest.HostPermissionRisk[id]; risk == manifest.RiskHigh || risk == manifest.RiskCritical {
			c.HighRiskAdded = append(c.HighRiskAdded, fmt.Sprintf("%s (%s)", id, risk))
		}
	}
	return c
}

// diffLists returns the sorted, distinct entries only in cur and only in old.
func diffLists(old, cur []string) listDiff {
	only := func(a, b []string) []string {
		out := []string{}
		for _, v := range a {
			if !slices.Contains(b, v) && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		slices.Sort(out)
		return out
	}
	return listDiff{Added: only(cur, old), Removed: only(old, cur)}
}

// checkMarkdown is the pull request summary of a check.
func checkMarkdown(res *checkResult) string {
	var b strings.Builder
	b.WriteString("## Plugin registry check\n\n")
	fmt.Fprintf(&b, "- Result: **%s**\n", pick(res.OK, "passed", "failed"))
	fmt.Fprintf(&b, "- Needs manual review: **%s**\n", pick(res.NeedsManualReview, "yes", "no"))
	if res.Publisher != "" {
		fmt.Fprintf(&b, "- Publisher: `%s`\n", res.Publisher)
	}
	writeSection(&b, "Problems", res.Errors)
	writeSection(&b, "Needs manual review", res.ManualReview)
	for _, v := range res.Versions {
		writeVersionReport(&b, v)
	}
	return b.String()
}

func writeSection(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### %s\n\n", title)
	for _, l := range lines {
		fmt.Fprintf(b, "- %s\n", l)
	}
}

func writeVersionReport(b *strings.Builder, v versionReport) {
	fmt.Fprintf(b, "\n### `%s` %s\n\n", v.Plugin, v.Version)
	fmt.Fprintf(b, "- Package: %s (%d bytes, sha256 `%s`)\n", v.URL, v.Size, v.SHA256)
	switch {
	case v.Previous == "":
		b.WriteString("- Compared with: no earlier version\n")
	case v.PreviousError != "":
		fmt.Fprintf(b, "- Compared with: %s, **unavailable** (%s); everything is listed as added\n", v.Previous, v.PreviousError)
	default:
		fmt.Fprintf(b, "- Compared with: %s\n", v.Previous)
	}
	c := v.Changes
	if len(c.HighRiskAdded) > 0 {
		fmt.Fprintf(b, "- **High-risk host permissions added: %s**\n", codeList(c.HighRiskAdded))
	}
	writeDiff(b, "Host permissions", c.HostPermissions)
	writeDiff(b, "User permissions", c.UserPermissions)
	writeDiff(b, "Migrations", c.Migrations)
	writeDiff(b, "Egress domains", c.EgressDomains)
}

func writeDiff(b *strings.Builder, title string, d listDiff) {
	var parts []string
	if len(d.Added) > 0 {
		parts = append(parts, "added "+codeList(d.Added))
	}
	if len(d.Removed) > 0 {
		parts = append(parts, "removed "+codeList(d.Removed))
	}
	fmt.Fprintf(b, "- %s: %s\n", title, pick(len(parts) > 0, strings.Join(parts, "; "), "no change"))
}

func codeList(items []string) string {
	return "`" + strings.Join(items, "`, `") + "`"
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
