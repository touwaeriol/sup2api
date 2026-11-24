package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// indexMaxBytes bounds the index.json released-check reads.
const indexMaxBytes = 16 << 20

// cmdReleasedCheck fails when a plugin directory's sources changed under a
// version the market index already lists (CONTRACTS §135): the core keeps
// the package it has for a version, so such a change would never reach a
// deployment. Index entries without a source_digest are not compared.
func cmdReleasedCheck(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("released-check", stderr)
	plugins := fs.String("plugins", "", "directory of plugin directories, <dir>/*/manifest.json (required)")
	indexURL := fs.String("index", "", "URL of the market index.json to compare with (required)")
	strict := fs.Bool("strict", false, "fail when the index cannot be read (CI) instead of warning")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}
	if *plugins == "" || *indexURL == "" {
		return errors.New("--plugins and --index are required")
	}
	idx, err := readIndex(*indexURL, *strict, stderr)
	if idx == nil {
		return err
	}
	problems, err := releasedProblems(*plugins, registeredDigests(idx))
	if err != nil {
		return err
	}
	for _, p := range problems {
		fmt.Fprintln(stderr, p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d plugin(s) changed without a version bump", len(problems))
	}
	fmt.Fprintf(stdout, "released-check: no registered version changed (%s)\n", *indexURL)
	return nil
}

// readIndex fetches the index; when it cannot, the error is returned if
// strict, otherwise a warning goes to stderr. A nil index means: stop.
func readIndex(u string, strict bool, stderr io.Writer) (*marketIndex, error) {
	idx, err := fetchIndex(u)
	if err == nil || strict {
		return idx, err
	}
	fmt.Fprintf(stderr, "warning: released versions not checked: %v\n", err)
	return nil, nil
}

// fetchIndex downloads and decodes a market index.
func fetchIndex(u string) (*marketIndex, error) {
	resp, err := newRemote().get(u, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var idx marketIndex
	if err := json.NewDecoder(io.LimitReader(resp.Body, indexMaxBytes)).Decode(&idx); err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	return &idx, nil
}

// registeredDigests maps "<plugin id>@<version>" to the source digest the
// index records for it.
func registeredDigests(idx *marketIndex) map[string]string {
	out := map[string]string{}
	for _, p := range idx.Plugins {
		for _, v := range p.Versions {
			if v.SourceDigest != "" {
				out[manifest.PluginID(p.Publisher, p.Key)+"@"+v.Version] = v.SourceDigest
			}
		}
	}
	return out
}

// releasedProblems compares every plugin directory below root with the
// registered digests.
func releasedProblems(root string, registered map[string]string) ([]string, error) {
	manifests, err := filepath.Glob(filepath.Join(root, "*", pkgsig.ManifestFile))
	if err != nil || len(manifests) == 0 {
		return nil, fmt.Errorf("no plugins in %s (%v)", root, err)
	}
	var problems []string
	for _, mf := range manifests {
		p, err := releasedProblem(filepath.Dir(mf), registered)
		if err != nil {
			return nil, err
		}
		if p != "" {
			problems = append(problems, p)
		}
	}
	return problems, nil
}

// releasedProblem describes the plugin in dir when the index registers its
// manifest version with other sources ("" when it does not).
func releasedProblem(dir string, registered map[string]string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, pkgsig.ManifestFile))
	if err != nil {
		return "", err
	}
	m, err := parseManifest(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", dir, err)
	}
	want, ok := registered[m.ID()+"@"+m.Version]
	if !ok {
		return "", nil
	}
	got, err := pluginDigest(dir)
	if err != nil || got == want {
		return "", err
	}
	return fmt.Sprintf("%s: %s %s is registered with other sources (%s, now %s): bump the manifest version (and the plugin's Version constant, if it has one), release it and register it with `registry add-version`",
		dir, manifest.DisplayID(m.ID()), m.Version, want, got), nil
}
