package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/payment/internal/payment"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestPassesCoreChecks runs manifest.json through the checks the
// server runs before it installs a plugin (sdk/manifest/check). Tooling mode
// only drops the checks that need an installing host or a finished build.
func TestManifestPassesCoreChecks(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	fields, _ := check.Fields(check.Validate(&m, packageFiles(t), check.ValidateOptions{Tooling: true}))
	for _, f := range fields {
		t.Errorf("%s: %s (%s)", f.Field, f.Message, f.Code)
	}
}

// TestManifestShape checks manifest.json against the SDK types (no unknown
// fields) and the identity and grants the plugin relies on.
func TestManifestShape(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.APIVersion != manifest.APIVersion || m.Key != "payment" || m.Runtime != "grpc" {
		t.Fatalf("identity = %d %s %s", m.APIVersion, m.Key, m.Runtime)
	}
	if m.Name["en"] == "" || m.Name["zh"] == "" || m.Description["en"] == "" || m.Description["zh"] == "" {
		t.Fatal("name and description need en and zh")
	}
	perms := map[string]manifest.HostPermission{}
	for _, p := range m.HostPermissions {
		if _, ok := manifest.HostPermissionRisk[p.ID]; !ok {
			t.Errorf("unknown host permission %q", p.ID)
		}
		if p.Reason["en"] == "" || p.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", p.ID)
		}
		perms[p.ID] = p
	}
	// The host reads exactly these keys (server/internal/plugin/grpcruntime
	// host.go); any other key would leave the ledger uncapped.
	lc, ok := perms["ledger.credit"]
	if !ok {
		t.Fatal("ledger.credit missing")
	}
	for _, k := range []string{"maxPerTx", "maxPerDay"} {
		if _, ok := lc.Scope[k]; !ok {
			t.Errorf("ledger.credit scope lacks %s: %v", k, lc.Scope)
		}
	}
	if len(lc.Scope) != 2 {
		t.Errorf("ledger.credit scope has unknown keys: %v", lc.Scope)
	}
}

// TestRoutesHaveHandlers sends every manifest route to the plugin and fails
// when the router has no handler for it.
func TestRoutesHaveHandlers(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	h := pluginsdktest.Start(t, payment.New(), pluginsdktest.Options{
		Manifest: manifestJSON,
	})
	for _, r := range m.Routes {
		path := strings.ReplaceAll(strings.ReplaceAll(r.Path, ":provider", "stripe"), ":id", "1")
		resp := h.Do(r.Method, path, nil, nil)
		if strings.Contains(string(resp.GetBody()), "no route for") {
			t.Errorf("%s %s: no handler registered", r.Method, r.Path)
		}
	}
}

// packageFiles is what `sub2api-plugin pack` would put in the package, minus
// the built artifacts and Go sources.
func packageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
			return nil
		case strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, ".go"):
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(p)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
