package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/tidwall/gjson"
)

// oldProtocolSource makes a guard build answer the go-plugin handshake with
// plugin protocol 1: what a binary built with the SDK before forwarding v2
// reports.
const oldProtocolSource = `package main

import "github.com/Sub2API-Devs/sup2api/next/sdk/protocol"

func init() { protocol.Handshake.ProtocolVersion = 1 }
`

// AC 33: packages built for the plugin contract before forwarding v2 are
// refused with one fixed message (DESIGN §9.2): a manifest with runtime
// "none", an accountTypes[].upstream block or a removed capability is
// refused at upload, and no version row is left; a binary speaking the old
// plugin protocol is uploaded (its manifest is current) but refused when it
// is loaded, and the plugin list shows why.
func TestAC33_OldContractPackagesRefused(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	hostVersion := e.NodeHealth(1).Get("version").String()
	if hostVersion == "" {
		t.Fatal("/healthz has no version")
	}

	for name, edit := range map[string]func(m map[string]any){
		"runtime none": func(m map[string]any) { m["runtime"] = "none"; delete(m, "entry") },
		"upstream block": func(m map[string]any) {
			m["accountTypes"] = []any{map[string]any{"id": "old", "label": map[string]any{"en": "Old"},
				"platforms": []any{map[string]any{"platform": "anthropic"}}, "upstream": map[string]any{"preset": "anthropic"}}}
		},
		"removed capability": func(m map[string]any) {
			m["capabilities"] = append(m["capabilities"].([]any), map[string]any{"id": "platform.execute.v1"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := e.With(t)
			dp := e.NewDerivedPlugin(admin, "e2eold")
			pkg := dp.Package(e, edit)
			version := fmt.Sprint(pkg.Manifest(t)["version"])
			r := e.UploadPlugin(admin, dp.Key+".s2plugin", pkg.Bytes(t))
			want := protocol.OutdatedContractMessage(dp.Key, version, hostVersion)
			if r.Status != http.StatusBadRequest || r.JSON().Get("error.message").String() != want {
				t.Fatalf("upload: %s\nwant 400 with %q", r, want)
			}
			if f, ok := Find(r.JSON().Get("error.details.fields").Array(), "code", "outdated_contract"); !ok || f.Get("message").String() != want {
				t.Fatalf("upload field error: %s", r.Body)
			}
			if _, ok := e.Plugin(admin, dp.Key); ok {
				t.Fatalf("a refused upload left plugin %s", dp.Key)
			}
			e.RequireDocker()
			if rows := e.SQL(fmt.Sprintf(`SELECT count(*) FROM plugin_versions WHERE plugin_key=%s`, sqlText(dp.Key))); len(rows) != 1 || rows[0] != "0" {
				t.Fatalf("a refused upload left version rows: %v", rows)
			}
		})
	}

	t.Run("old plugin protocol refused at load", func(t *testing.T) {
		e := e.With(t)
		dp := e.NewDerivedPlugin(admin, "e2eoldbin")
		pkg := dp.Package(e)
		m := pkg.Manifest(t)
		version := fmt.Sprint(m["version"])
		for p := range pkg {
			if rest, ok := strings.CutPrefix(p, "runtimes/"); ok {
				osArch, _, _ := strings.Cut(rest, "/")
				goos, goarch, _ := strings.Cut(osArch, "-")
				pkg[p] = buildGuardBinary(t, goos, goarch, dp.Key, version, map[string]string{"zz_e2e_old_protocol.go": oldProtocolSource})
			}
		}
		pkg.Sign(t, dp.Signer)
		rev := e.UploadPackage(admin, pkg)
		e.ConsentAll(admin, rev, []string{"admin"})
		t.Cleanup(func() {
			admin.API(t, http.MethodPost, "/plugins/"+dp.Key+"/disable", nil)
			Eventually(t, 60*time.Second, time.Second, "uninstall "+dp.Key, func() bool {
				r := admin.API(t, http.MethodDelete, "/plugins/"+dp.Key, nil, Query("purge", "true"))
				return r.Status == 200 || r.Status == 204 || r.Status == 404
			})
		})
		admin.API(t, http.MethodPost, "/plugins/"+dp.Key+"/enable", nil)
		want := protocol.OutdatedContractMessage(dp.Key, version, hostVersion)
		// The nodes refuse to prepare the version, with the one message, and
		// the plugin list says so; the refusal the console shows outlives
		// the failed rollout: the rollout kept the code with each node's
		// outcome.
		var listed gjson.Result
		Eventually(t, 90*time.Second, time.Second, func() string { return "refusal in the plugin list: " + listed.Raw }, func() bool {
			listed, _ = Find(admin.ListAll(t, "/plugins"), "key", dp.Key)
			return listed.Get("refusal.code").String() == "outdated_contract"
		})
		if listed.Get("status").String() == "enabled" {
			t.Fatalf("the refused plugin is enabled: %s", listed.Raw)
		}
		if r := listed.Get("refusal"); r.Get("code").String() != "outdated_contract" || r.Get("version").String() != version ||
			r.Get("host_version").String() != hostVersion || r.Get("message").String() != want ||
			r.Get("protocol_version").Int() != protocol.ProtocolVersion || r.Get("first_core_version").String() != protocol.FirstCoreVersion {
			t.Fatalf("refusal: %s", listed.Raw)
		}
		d, _ := e.Plugin(admin, dp.Key)
		for node, st := range PluginNodeStates(d) {
			if st == "active" {
				t.Fatalf("%s runs the refused plugin: %s", node, d.Raw)
			}
		}
	})
}
