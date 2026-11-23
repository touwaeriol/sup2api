package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
)

// ghRelease is the part of a GitHub release the registry uses.
type ghRelease struct {
	TagName string    `json:"tag_name"`
	HTMLURL string    `json:"html_url"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"` // "sha256:<hex>" when GitHub has computed it
}

// release fetches the latest release of repo, or the one tagged tag.
func (r *remote) release(repo, tag string) (*ghRelease, error) {
	ref := "/repos/" + repo + "/releases/latest"
	if tag != "" {
		ref = "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	}
	resp, err := r.get(strings.TrimRight(r.apiBase, "/")+ref, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	return &rel, nil
}

// releaseVersion reads release tag of plugin p (the latest when tag is ""),
// downloads its one package and returns the version file the release backs.
func (r *remote) releaseVersion(p *regPlugin, tag string) (*pkgInfo, regVersion, error) {
	rel, err := r.release(p.Repo, tag)
	if err != nil {
		return nil, regVersion{}, err
	}
	asset, err := rel.packageAsset()
	if err != nil {
		return nil, regVersion{}, err
	}
	info, err := r.fetchPackage(p, asset.URL)
	if err == nil {
		err = rel.backs(p, asset, info)
	}
	if err != nil {
		return nil, regVersion{}, fmt.Errorf("release %s of %s: %w", rel.TagName, p.Repo, err)
	}
	v := info.version(asset.URL)
	v.ReleaseURL = rel.HTMLURL
	return info, v, nil
}

// packageAsset is the release's one .s2plugin asset.
func (rel *ghRelease) packageAsset() (*ghAsset, error) {
	var found []*ghAsset
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, PackageExt) {
			found = append(found, &rel.Assets[i])
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("release %s has %d %s assets, want exactly one", rel.TagName, len(found), PackageExt)
	}
	return found[0], nil
}

// backs checks that the release publishes this package: its tag names the
// version, its page is in p.Repo, and the asset is under the tag with the
// digest GitHub reports.
func (rel *ghRelease) backs(p *regPlugin, asset *ghAsset, info *pkgInfo) error {
	ver := info.m.Version
	tags := []string{"v" + ver, p.Key + "-v" + ver, "plugin-" + p.Key + "-v" + ver}
	if !slices.Contains(tags, rel.TagName) {
		return fmt.Errorf("tag %q must be one of %s for version %s", rel.TagName, strings.Join(tags, ", "), ver)
	}
	if want := releasePage(p.Repo, rel.TagName); rel.HTMLURL != want {
		return fmt.Errorf("html_url %q, want %q", rel.HTMLURL, want)
	}
	if tag, _ := releaseAssetTag(p.Repo, asset.URL); tag != rel.TagName {
		return fmt.Errorf("asset %s is not under the tag", asset.URL)
	}
	if asset.Digest != "" && !strings.EqualFold(asset.Digest, "sha256:"+info.sha256) {
		return fmt.Errorf("asset digest %s, the download is sha256:%s", asset.Digest, info.sha256)
	}
	return nil
}

func releasePage(repo, tag string) string {
	return "https://github.com/" + repo + "/releases/tag/" + tag
}

// releaseAssetTag returns the tag of a release asset url of repo, which must
// be https://github.com/<repo>/releases/download/<tag>/<file>.s2plugin.
func releaseAssetTag(repo, u string) (string, error) {
	prefix := "https://github.com/" + repo + "/releases/download/"
	rest, ok := strings.CutPrefix(u, prefix)
	tag, file, _ := strings.Cut(rest, "/")
	parsed, err := url.Parse(u)
	if !ok || err != nil || tag == "" || file == "" || strings.ContainsAny(file, "/?#") ||
		strings.Contains(rest, "..") || parsed.RawQuery != "" || !strings.HasSuffix(file, PackageExt) {
		return "", fmt.Errorf("url %q must be %s<tag>/<file>%s", u, prefix, PackageExt)
	}
	return url.PathUnescape(tag)
}
