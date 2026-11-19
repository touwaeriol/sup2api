package main

import (
	"encoding/json"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrandAssetsPackWithAccountTypes(t *testing.T) {
	runtimes := t.TempDir()
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		writeFile(t, filepath.Join(runtimes, "runtimes", platform, "plugin"), "test runtime")
	}
	for _, key := range []string{"anthropic", "openai", "gemini", "volcengine"} {
		t.Run(key, func(t *testing.T) {
			dir := filepath.Join("..", "..", "plugins", key)
			// The brand assets only: the native UI is built separately.
			pkg := strings.TrimSpace(runOK(t, "pack", "--dir", dir, "--out-dir", t.TempDir(), "--runtimes", runtimes, "--allow-missing-ui"))
			files, err := readPackage(pkg)
			if err != nil {
				t.Fatal(err)
			}
			var m manifest.Manifest
			if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
				t.Fatal(err)
			}
			icons := []string{m.Icon}
			for _, at := range m.AccountTypes {
				if at.Label["en"] == "API Key" {
					t.Fatal("authentication method used as product name")
				}
				if at.Icon != "" {
					icons = append(icons, at.Icon)
				}
			}
			for _, icon := range icons {
				if !strings.Contains(string(files[icon]), "<svg") {
					t.Fatalf("missing SVG %s", icon)
				}
			}
			if !strings.Contains(string(files["assets/LOBE-ICONS-LICENSE.txt"]), "MIT License") {
				t.Fatal("missing artwork license")
			}
		})
	}
}
