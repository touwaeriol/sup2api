package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

// checkResult is what `registry check` reports (--json prints it as is).
type checkResult struct {
	OK                bool            `json:"ok"`
	NeedsManualReview bool            `json:"needs_manual_review"`
	Publisher         string          `json:"publisher,omitempty"`
	Errors            []string        `json:"errors"`
	ManualReview      []string        `json:"manual_review"`
	Versions          []versionReport `json:"versions"`
}

// versionReport describes one verified new version.
type versionReport struct {
	Plugin        string           `json:"plugin"`
	Version       string           `json:"version"`
	URL           string           `json:"url"`
	SHA256        string           `json:"sha256"`
	Size          int64            `json:"size"`
	Previous      string           `json:"previous,omitempty"`
	PreviousError string           `json:"previous_error,omitempty"`
	Changes       *manifestChanges `json:"changes"`
}

// cmdRegistryCheck reviews a registry pull request: the head checkout
// against the base checkout.
func cmdRegistryCheck(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("registry check", stderr)
	head := fs.String("registry", "", "registry checkout of the pull request head (required)")
	base := fs.String("base", "", "registry checkout of the pull request base (required)")
	author := fs.String("author", "", "GitHub login of the pull request author (required)")
	summary := fs.String("summary", "", "also write the Markdown summary to this file")
	asJSON := fs.Bool("json", false, "print the result as JSON instead of Markdown")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *head == "" || *base == "" || *author == "" {
		return errors.New("--registry, --base and --author are required")
	}
	res := runRegistryCheck(*head, *base, *author, newRemote())
	if err := reportCheck(res, *summary, *asJSON, stdout); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("%d problem(s)", len(res.Errors))
	}
	return nil
}

func reportCheck(res *checkResult, summary string, asJSON bool, stdout io.Writer) error {
	md := checkMarkdown(res)
	if summary != "" {
		if err := os.WriteFile(summary, []byte(md), 0o644); err != nil {
			return err
		}
	}
	out := []byte(md)
	if asJSON {
		var err error
		if out, err = encodeJSON(res); err != nil {
			return err
		}
	}
	_, err := stdout.Write(out)
	return err
}

// checker holds one review.
type checker struct {
	head, base *registry
	author     string
	remote     *remote
	res        *checkResult
	owners     map[string]bool // publishers whose files change
}

func runRegistryCheck(headRoot, baseRoot, author string, r *remote) *checkResult {
	c := &checker{author: author, remote: r, owners: map[string]bool{},
		res: &checkResult{Errors: []string{}, ManualReview: []string{}, Versions: []versionReport{}}}
	var errHead, errBase, errDiff error
	c.head, errHead = loadRegistry(headRoot)
	c.base, errBase = loadRegistry(baseRoot)
	changes, errDiff := diffTrees(baseRoot, headRoot)
	for _, err := range []error{errHead, errBase, errDiff} {
		for _, line := range errorLines(err) {
			c.fail("%s", line)
		}
	}
	if len(c.res.Errors) == 0 {
		c.review(changes)
	}
	c.res.OK = len(c.res.Errors) == 0
	c.res.NeedsManualReview = len(c.res.ManualReview) > 0
	if len(c.owners) == 1 {
		c.res.Publisher = slices.Collect(maps.Keys(c.owners))[0]
	}
	return c.res
}

func (c *checker) fail(format string, args ...any) {
	c.res.Errors = append(c.res.Errors, fmt.Sprintf(format, args...))
}

func (c *checker) manual(format string, args ...any) {
	c.res.ManualReview = append(c.res.ManualReview, fmt.Sprintf(format, args...))
}

// review applies the rules to every changed file.
func (c *checker) review(changes []fileChange) {
	byPlugin := map[string][]fileChange{}
	for _, ch := range changes {
		dir, rest, _ := strings.Cut(ch.path, "/")
		switch dir {
		case regPublishersDir:
			id := strings.TrimSuffix(rest, regJSONExt)
			c.owners[id] = true
			c.manual("%s: publisher file %s", ch.path, ch.kind)
		case regPluginsDir:
			key, _, _ := strings.Cut(rest, "/")
			byPlugin[key] = append(byPlugin[key], ch)
		default:
			c.manual("%s: %s outside %s/ and %s/", ch.path, ch.kind, regPublishersDir, regPluginsDir)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(byPlugin)) {
		c.pluginChanges(key, byPlugin[key])
	}
	if len(c.owners) > 1 {
		c.fail("a pull request may change the files of one publisher only; this one changes %s", strings.Join(slices.Sorted(maps.Keys(c.owners)), ", "))
	}
}

// pluginChanges reviews the changed files of plugins/<key>/.
func (c *checker) pluginChanges(key string, chs []fileChange) {
	old, cur := c.base.plugins[key], c.head.plugins[key]
	switch {
	case old == nil:
		c.owners[cur.plugin.Publisher] = true
		c.manual("plugins/%s: new plugin", key)
	case cur == nil:
		c.owners[old.plugin.Publisher] = true
		c.manual("plugins/%s: plugin removed", key)
	default:
		c.owners[cur.plugin.Publisher] = true
		c.owners[old.plugin.Publisher] = true
		c.requireAuthor(old.plugin.Publisher, "plugins/"+key)
	}
	for _, ch := range chs {
		c.pluginFileChange(key, ch)
	}
}

func (c *checker) pluginFileChange(key string, ch fileChange) {
	rel := strings.TrimPrefix(ch.path, regPluginsDir+"/"+key+"/")
	ver, isVersion := strings.CutPrefix(rel, regVersionsDir+"/")
	ver = strings.TrimSuffix(ver, regJSONExt)
	switch {
	case !isVersion:
		c.manual("%s %s", ch.path, ch.kind)
	case ch.kind == changeDeleted:
		c.manual("%s: version %s deleted", ch.path, ver)
	case ch.kind == changeModified:
		c.immutable(ch.path, c.base.plugins[key].versions[ver], c.head.plugins[key].versions[ver])
	default:
		c.verifyNew(key, c.head.plugins[key].versions[ver])
	}
}

// requireAuthor fails unless the author is one of the base publisher's
// GitHub logins and the publisher is active.
func (c *checker) requireAuthor(pubID, what string) {
	pub := c.base.publishers[pubID]
	switch {
	case pub.Status != "active":
		c.fail("%s: publisher %s is %s", what, pubID, pub.Status)
	case !slices.ContainsFunc(pub.GitHub, func(l string) bool { return strings.EqualFold(l, c.author) }):
		c.fail("%s: %s is not in the github list of %s/%s%s", what, c.author, regPublishersDir, pubID, regJSONExt)
	}
}

// immutable allows a published version file only to gain (or reword) its
// deprecation.
func (c *checker) immutable(file string, old, cur *regVersion) {
	o, n := *old, *cur
	if o.Deprecated != nil && n.Deprecated == nil {
		c.fail("%s: a deprecation cannot be removed", file)
	}
	o.Deprecated, n.Deprecated = nil, nil
	if o != n {
		c.fail("%s: a published version may only add deprecated; %s", file, versionMismatch(o, n))
	}
}

// verifyNew reads the release a new version's url names, downloads its
// package and compares the version file with what they say.
func (c *checker) verifyNew(key string, v *regVersion) {
	file := path.Join(regPluginsDir, key, regVersionsDir, v.Version+regJSONExt)
	p := &c.head.plugins[key].plugin
	tag, err := releaseAssetTag(p.Repo, v.URL)
	var info *pkgInfo
	var want regVersion
	if err == nil {
		info, want, err = c.remote.releaseVersion(p, tag)
	}
	if err != nil {
		c.fail("%s: %v", file, err)
		return
	}
	want.Deprecated = v.Deprecated
	if want != *v {
		c.fail("%s: does not match the package: %s", file, versionMismatch(*v, want))
		return
	}
	c.res.Versions = append(c.res.Versions, c.versionReport(key, v, info))
}

// versionReport compares a new version with the newest non-deprecated
// version the base lists.
func (c *checker) versionReport(key string, v *regVersion, info *pkgInfo) versionReport {
	rep := versionReport{Plugin: key, Version: v.Version, URL: v.URL, SHA256: v.SHA256, Size: v.Size}
	var prevInfo *pkgInfo
	if old := c.base.plugins[key]; old != nil {
		if prev := old.newestVersion(func(p *regVersion) bool { return p.Deprecated == nil }); prev != nil {
			rep.Previous = prev.Version
			var err error
			if prevInfo, err = c.remote.fetchPackage(&old.plugin, prev.URL); err != nil {
				rep.PreviousError = err.Error()
			}
		}
	}
	rep.Changes = compareManifests(factsOf(prevInfo), factsOf(info))
	return rep
}

// versionMismatch lists the fields where file and want differ.
func versionMismatch(file, want regVersion) string {
	fields := [][3]string{
		{"version", file.Version, want.Version},
		{"url", file.URL, want.URL},
		{"sha256", file.SHA256, want.SHA256},
		{"size", strconv.FormatInt(file.Size, 10), strconv.FormatInt(want.Size, 10)},
		{"host_compat", file.HostCompat, want.HostCompat},
		{"release_url", file.ReleaseURL, want.ReleaseURL},
	}
	var out []string
	for _, f := range fields {
		if f[1] != f[2] {
			out = append(out, fmt.Sprintf("%s is %q, expected %q", f[0], f[1], f[2]))
		}
	}
	return strings.Join(out, "; ")
}

// errorLines splits a joined error into its lines.
func errorLines(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}
