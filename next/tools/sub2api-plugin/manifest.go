package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// Overlay files that change the manifest instead of being packaged.
const (
	overlayManifest = "manifest.json"       // full replacement
	overlayPatch    = "manifest.patch.json" // RFC 7386 JSON merge patch
)

// resolveOverlay returns the overlay directory: as given when it exists,
// otherwise relative to the plugin directory. "" stays "".
func resolveOverlay(dir, overlay string) (string, error) {
	if overlay == "" {
		return "", nil
	}
	candidates := []string{overlay}
	if !filepath.IsAbs(overlay) {
		candidates = append(candidates, filepath.Join(dir, overlay))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("overlay directory %q not found", overlay)
}

// effectiveManifest reads <dir>/manifest.json and applies the overlay.
// It returns the manifest bytes to package and the parsed manifest.
func effectiveManifest(dir, overlay string) ([]byte, *manifest.Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	if overlay != "" {
		if b, err := os.ReadFile(filepath.Join(overlay, overlayManifest)); err == nil {
			raw = b
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if b, err := os.ReadFile(filepath.Join(overlay, overlayPatch)); err == nil {
			if raw, err = applyMergePatch(raw, b); err != nil {
				return nil, nil, fmt.Errorf("apply %s: %w", overlayPatch, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	m, err := parseManifest(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, m, nil
}

// parseManifest decodes strictly (unknown fields are errors) and checks the
// basic identity fields. A manifest of an older plugin contract is reported
// with the one message the host gives it, not with its field errors.
func parseManifest(raw []byte) (*manifest.Manifest, error) {
	if err := outdatedContract(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m manifest.Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if m.Key == "" || m.Version == "" {
		return nil, errors.New("manifest.json: key and version are required")
	}
	if m.APIVersion != manifest.APIVersion {
		return nil, fmt.Errorf("manifest.json: apiVersion %d unsupported (want %d)", m.APIVersion, manifest.APIVersion)
	}
	return &m, nil
}

// outdatedContract refuses a manifest of an older plugin contract with the
// one message the host gives it (check.OutdatedPackage); its fields would
// otherwise fail the strict decode as unknown. The CLI has no core version
// of its own: the message names the first core of the protocol.
func outdatedContract(raw []byte) error {
	var m manifest.Manifest
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	if msg, outdated := check.OutdatedPackage(&m, raw, protocol.FirstCoreVersion); outdated {
		return errors.New(msg)
	}
	return nil
}

// applyMergePatch applies an RFC 7386 JSON merge patch and re-encodes the
// result indented.
func applyMergePatch(doc, patch []byte) ([]byte, error) {
	var d, p any
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, err
	}
	return encodeJSON(mergePatch(d, p))
}

// encodeJSON encodes v indented, without HTML escaping, newline-terminated.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mergePatch(target, patch any) any {
	pm, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	tm, ok := target.(map[string]any)
	if !ok {
		tm = map[string]any{}
	}
	for k, v := range pm {
		if v == nil {
			delete(tm, k)
			continue
		}
		tm[k] = mergePatch(tm[k], v)
	}
	return tm
}

// cmdManifest prints the effective manifest.
func cmdManifest(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("manifest", stderr)
	dir := fs.String("dir", ".", "plugin directory")
	overlay := fs.String("overlay", "", "overlay directory")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ov, err := resolveOverlay(*dir, *overlay)
	if err != nil {
		return err
	}
	raw, _, err := effectiveManifest(*dir, ov)
	if err != nil {
		return err
	}
	_, err = stdout.Write(raw)
	return err
}
