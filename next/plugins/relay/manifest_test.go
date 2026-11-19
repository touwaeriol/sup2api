package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/relay/internal/relay"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
)

// TestManifest runs manifest.json through the checks the core runs before
// it installs a package and pins what the package keeps: an
// account-type-only plugin (ARCHITECTURE 6.6) whose relay_key serves the
// built-in anthropic platform with the client headers the code forwards,
// an address the caller sets (account:relay guards it, as before), and its
// own process that reads only its own accounts.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Key != "relay" || m.Version != "0.4.0" || m.Publisher != "sub2api" || m.Runtime != manifest.RuntimeGRPC {
		t.Fatalf("identity = %s %s %s %s", m.Key, m.Version, m.Publisher, m.Runtime)
	}
	if m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "" || m.HostCompat != ">=0.1.304 <0.2.0" {
		t.Fatalf("entry/hostCompat = %+v %s", m.Entry, m.HostCompat)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0].ID != manifest.CapPlatformAdapter {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}
	if len(m.Platforms) != 0 || len(m.AccountTypes) != 1 {
		t.Fatalf("platforms = %+v accountTypes = %+v, want one account type only", m.Platforms, m.AccountTypes)
	}
	at := m.AccountTypes[0]
	if at.ID != relay.TypeRelayKey || len(at.Platforms) != 1 || at.Platforms[0].Platform != "anthropic" {
		t.Fatalf("account type %s platforms = %+v, want relay_key on anthropic", at.ID, at.Platforms)
	}
	// The core hands the plugin only these two client headers.
	if passHeaders := at.Platforms[0].PassHeaders; !slices.Equal(passHeaders, []string{"anthropic-version", "anthropic-beta"}) {
		t.Fatalf("passHeaders = %v", passHeaders)
	}
	if !slices.Equal(at.UpstreamFields, []string{"base_url"}) || !at.CustomUpstream() {
		t.Fatalf("upstreamFields = %v custom = %v, want [base_url] and a custom upstream", at.UpstreamFields, at.CustomUpstream())
	}
	perms := map[string]manifest.HostPermission{}
	for _, perm := range m.HostPermissions {
		perms[perm.ID] = perm
	}
	if len(perms) != 2 || perms["platform.register"].ID == "" || perms["ui.native"].ID == "" {
		t.Fatalf("host permissions = %v", perms)
	}
}

// load decodes manifest.json strictly and checks it, with the files the
// package carries, without errors or warnings.
func load(t *testing.T) manifest.Manifest {
	t.Helper()
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	files := packageFiles(t)
	if err := check.Validate(&m, files, check.ValidateOptions{Tooling: true}); err != nil {
		t.Fatal(err)
	}
	return m
}

// packageFiles are the files a package carries: everything but Go sources
// and dot files.
func packageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p != "." && strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, ".go"):
			return nil
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(p)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
