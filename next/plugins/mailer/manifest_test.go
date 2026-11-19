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

	"github.com/Sub2API-Devs/sup2api/next/plugins/mailer/internal/mailer"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// TestManifestServes starts the plugin with its embedded manifest: the SDK
// reports notify.channel.v1 and the NotificationService answers.
func TestManifestServes(t *testing.T) {
	h := pluginsdktest.Start(t, mailer.New(), pluginsdktest.Options{Manifest: manifestJSON})
	if !slices.Contains(h.Info.GetCapabilities(), manifest.CapNotifyChannel) {
		t.Fatalf("capabilities = %v", h.Info.GetCapabilities())
	}
}

// TestManifest checks manifest.json against the SDK types (no unknown
// fields), the declared channel and the native settings form.
func TestManifest(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if m.Key != "mailer" || m.Notifications == nil || len(m.Notifications.Channels) != 1 ||
		m.Notifications.Channels[0].ID != mailer.Channel || m.Notifications.Channels[0].Recipient != manifest.RecipientEmail {
		t.Fatalf("manifest = %+v", m)
	}
	for _, hp := range m.HostPermissions {
		if _, ok := manifest.HostPermissionRisk[hp.ID]; !ok {
			t.Errorf("unknown host permission %q", hp.ID)
		}
	}
	// The password is the settings' one secret, so that
	// GET /plugins/mailer/settings masks it.
	pluginsdktest.SettingsSecrets(t, manifestJSON, manifest.SecretField{Path: []string{"password"}, Credential: true})
	if m.UI.Settings.Mode != "native" || m.UI.Settings.Component != "MailerSettings" || m.UI.Settings.Page != "" || m.UI.Native == nil || m.UI.Native.Entry != "ui/native/entry.js" {
		t.Errorf("ui = %+v", m.UI)
	}
}

// TestManifestPassesCoreChecks runs manifest.json through the checks the
// server runs before it installs a plugin (Tooling: the runtime binaries and
// the native UI entry are produced by the build).
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

// packageFiles are the files the package would hold (no Go sources, hidden
// files or node_modules).
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
