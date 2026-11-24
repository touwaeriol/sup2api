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
	"unicode"
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
	promotion := fs.Bool("promotion", false, "the pull request promotes develop to main: skip the author, one-publisher and package checks done on develop")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *head == "" || *base == "" || *author == "" {
		return errors.New("--registry, --base and --author are required")
	}
	res := runRegistryCheck(*head, *base, *author, *promotion, newRemote())
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

// checker holds one review. A promotion (develop merged into main) brings
// changes each checked by its own pull request on develop: it is held to the
// file rules and the immutability of published versions only, and lists its
// new versions for the maintainer instead of downloading them again.
type checker struct {
	head, base *registry
	author     string
	promotion  bool
	remote     *remote
	res        *checkResult
	owners     map[string]bool             // publishers whose files change
	previous   map[string]*previousPackage // by plugin ref
}

// previousPackage is the version a plugin's new versions are compared with.
type previousPackage struct {
	version string
	info    *pkgInfo
	err     string
}

func runRegistryCheck(headRoot, baseRoot, author string, promotion bool, r *remote) *checkResult {
	budget := int64(checkDownloadBudget)
	r.budget = &budget
	c := &checker{author: author, promotion: promotion, remote: r, owners: map[string]bool{}, previous: map[string]*previousPackage{},
		res: &checkResult{Errors: []string{}, ManualReview: []string{}, Versions: []versionReport{}}}
	var err error
	c.head, err = loadRegistry(headRoot, false)
	c.lines(err, c.fail, "")
	c.base, err = loadRegistry(baseRoot, false)
	// A base that fails today's rules (they were tightened) is read as far
	// as it loads; a person looks at the pull request.
	c.lines(err, c.manual, "the base does not pass the current checks: ")
	changes, err := diffTrees(baseRoot, headRoot)
	c.lines(err, c.fail, "")
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

// lines reports each line of a joined error through add.
func (c *checker) lines(err error, add func(string, ...any), prefix string) {
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			add("%s%s", prefix, line)
		}
	}
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
		if ref := c.classify(ch); ref != "" {
			byPlugin[ref] = append(byPlugin[ref], ch)
		}
	}
	for _, ref := range slices.Sorted(maps.Keys(byPlugin)) {
		c.pluginChanges(ref, byPlugin[ref])
	}
	if len(c.owners) > 1 && !c.promotion {
		c.fail("a pull request may change the files of one publisher only; this one changes %s", strings.Join(slices.Sorted(maps.Keys(c.owners)), ", "))
	}
}

// classify reviews a change outside the plugin directories and returns the
// plugin ref of one inside them: the publisher always comes from the path.
func (c *checker) classify(ch fileChange) string {
	dir, rest, _ := strings.Cut(ch.path, "/")
	switch {
	case strings.ContainsFunc(ch.path, unicode.IsControl):
		c.fail("%q: a path with control characters", ch.path)
	case dir == regPublishersDir:
		c.owners[strings.TrimSuffix(rest, regJSONExt)] = true
		c.manual("%s: publisher file %s", ch.path, ch.kind)
	case dir == regPluginsDir:
		parts := strings.SplitN(rest, "/", 3)
		c.owners[parts[0]] = true
		if len(parts) == 3 {
			return regRef(parts[0], parts[1])
		}
		c.manual("%s %s", ch.path, ch.kind)
	default:
		c.manual("%s: %s outside %s/ and %s/", ch.path, ch.kind, regPublishersDir, regPluginsDir)
	}
	return ""
}

// pluginChanges reviews the changed files of plugins/<ref>/.
func (c *checker) pluginChanges(ref string, chs []fileChange) {
	pub, _, _ := strings.Cut(ref, refSep)
	old, cur := c.base.plugins[ref], c.head.plugins[ref]
	what := pluginDir(ref)
	switch {
	case old == nil:
		c.manual("%s: new plugin", what)
		if _, known := c.base.publishers[pub]; known {
			c.requireAuthor(pub, what)
		}
	case cur == nil:
		c.manual("%s: plugin removed", what)
	default:
		c.requireAuthor(pub, what)
	}
	for _, ch := range chs {
		c.pluginFileChange(ref, ch)
	}
}

func (c *checker) pluginFileChange(ref string, ch fileChange) {
	rel := strings.TrimPrefix(ch.path, pluginDir(ref)+"/")
	ver, isVersion := strings.CutPrefix(rel, regVersionsDir+"/")
	ver = strings.TrimSuffix(ver, regJSONExt)
	old := c.baseVersion(ref, ver)
	switch {
	case !isVersion:
		c.manual("%s %s", ch.path, ch.kind)
	case ch.kind == changeDeleted:
		c.manual("%s: version %s deleted", ch.path, ver)
	case ch.kind == changeModified && old != nil:
		c.immutable(ch.path, old, c.head.plugins[ref].versions[ver])
	case c.promotion:
		c.manual("%s: version %s promoted from develop", ch.path, ver)
	default: // added, or a base version that did not load
		c.verifyNew(ref, c.head.plugins[ref].versions[ver])
	}
}

func (c *checker) baseVersion(ref, ver string) *regVersion {
	if e := c.base.plugins[ref]; e != nil {
		return e.versions[ver]
	}
	return nil
}

// requireAuthor fails unless the author is one of the base publisher's
// GitHub logins and the publisher is active.
func (c *checker) requireAuthor(pubID, what string) {
	pub := c.base.publishers[pubID]
	switch {
	case c.promotion: // each change was checked on develop
	case pub == nil:
		c.manual("%s: publisher %s did not load from the base, the author is not checked", what, pubID)
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
// package and compares the version file with what they say, source_digest
// included (the package's source.json).
func (c *checker) verifyNew(ref string, v *regVersion) {
	file := path.Join(pluginDir(ref), regVersionsDir, v.Version+regJSONExt)
	p := &c.head.plugins[ref].plugin
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
	prev := c.previousOf(ref)
	c.res.Versions = append(c.res.Versions, versionReport{Plugin: ref, Version: v.Version, URL: v.URL, SHA256: v.SHA256,
		Size: v.Size, Previous: prev.version, PreviousError: prev.err, Changes: compareManifests(factsOf(prev.info), factsOf(info))})
}

// previousOf is the newest non-deprecated version the base lists for ref,
// downloaded once per plugin.
func (c *checker) previousOf(ref string) *previousPackage {
	if prev, ok := c.previous[ref]; ok {
		return prev
	}
	prev := &previousPackage{}
	c.previous[ref] = prev
	old := c.base.plugins[ref]
	if old == nil {
		return prev
	}
	if v := old.newestVersion(func(v *regVersion) bool { return v.Deprecated == nil }); v != nil {
		prev.version = v.Version
		info, err := c.remote.fetchPackage(&old.plugin, v.URL)
		if prev.info = info; err != nil {
			prev.err = err.Error()
		}
	}
	return prev
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
		{"source_digest", file.SourceDigest, want.SourceDigest},
	}
	var out []string
	for _, f := range fields {
		if f[1] != f[2] {
			out = append(out, fmt.Sprintf("%s is %q, expected %q", f[0], f[1], f[2]))
		}
	}
	return strings.Join(out, "; ")
}
