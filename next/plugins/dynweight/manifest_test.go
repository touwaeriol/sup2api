package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/dynweight/internal/dynweight"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestPassesCoreChecks runs manifest.json through the checks the
// server runs before it installs a plugin.
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
// fields) and the jobs, topics and routes the plugin serves.
func TestManifestShape(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "dynweight" || m.Database == nil || m.Database.Schema != "plg_dynweight" {
		t.Fatalf("identity = %s %+v", m.Key, m.Database)
	}
	for _, hp := range m.HostPermissions {
		if hp.Reason["en"] == "" || hp.Reason["zh"] == "" {
			t.Errorf("host permission %s needs an en and zh reason", hp.ID)
		}
	}
	jobs := []string{}
	for _, j := range m.Jobs {
		jobs = append(jobs, j.ID)
	}
	if !slices.Equal(jobs, []string{dynweight.JobEvaluate, dynweight.JobCleanup}) {
		t.Fatalf("jobs = %v", jobs)
	}
	// The evaluate job runs at the interval its cadence is counted in.
	if want := fmt.Sprintf("@every %ds", dynweight.EvaluateEverySeconds); m.Jobs[0].Schedule != want {
		t.Errorf("evaluate runs %q, the plugin counts on %q", m.Jobs[0].Schedule, want)
	}
	if m.Events == nil || !slices.Equal(m.Events.Subscribe, []string{dynweight.EventAccountStatusChanged}) {
		t.Errorf("events = %+v", m.Events)
	}
	if len(m.UI.AccountActions) != 1 || m.UI.AccountActions[0].Route != "POST /reset" {
		t.Errorf("account actions = %+v", m.UI.AccountActions)
	}
	p := dynweight.New()
	for _, r := range m.Routes {
		// Without a host every route answers an error, but not "no route".
		resp, err := p.HandleHTTP(context.Background(), &pluginv1.HTTPRequest{Method: r.Method, Path: r.Path, RoutePath: r.Path})
		if err != nil || resp.GetStatus() == http.StatusNotFound {
			t.Errorf("route %s %s is not served: %v", r.Method, r.Path, err)
		}
	}
}

// TestRouteAccounts pins the accounts the core checks before forwarding: the
// a narrower scope (own, groups, ids) passes for the accounts it reaches, a
// request naming none (a group, the node view) needs the full account:<action>, and the settings routes act
// on no account.
func TestRouteAccounts(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	read := func(from ...string) *manifest.RouteAccounts {
		return &manifest.RouteAccounts{Action: manifest.AccountsRead, From: from}
	}
	want := map[string]*manifest.RouteAccounts{
		"GET /status":   read("query:account_id"),
		"GET /events":   read("query:account_id"),
		"GET /weights":  read("query:account_ids"),
		"GET /degraded": read(),
		"GET /stats":    read(),
		"POST /reset":   {Action: manifest.AccountsUpdate, From: []string{manifest.AccountActionIDs, "body:account_id"}},
	}
	for _, r := range m.Routes {
		sig := r.Method + " " + r.Path
		if got := manifest.EffectiveRouteAccounts(&m, r); !reflect.DeepEqual(got, want[sig]) {
			t.Errorf("%s accounts = %+v, want %+v", sig, got, want[sig])
		}
	}
}

// TestManifestServes starts the plugin with its embedded manifest.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, dynweight.New(), pluginsdktest.Options{Manifest: manifestJSON})
	for _, c := range []string{manifest.CapGatewayObserve, manifest.CapSchedulerRank, manifest.CapAppJobs, manifest.CapAppEvents, manifest.CapHTTPRoutes} {
		if !slices.Contains(h.Info.GetCapabilities(), c) {
			t.Errorf("capability %s missing from %v", c, h.Info.GetCapabilities())
		}
	}
}

// packageFiles collects the files a package would carry.
func packageFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
			return nil
		case strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules":
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
