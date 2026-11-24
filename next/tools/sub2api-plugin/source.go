package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// A package's source digest (CONTRACTS §135): pack writes the digest of
// what the package is built from (pluginDigest) into source.json, the
// registry records it per version (source_digest) and released-check
// compares it with a plugin directory, so sources that changed under a
// version already registered are caught before a release.
const sourceFile = "source.json"

// sourceInfo is the content of source.json.
type sourceInfo struct {
	Digest string `json:"digest"`
}

var sourceDigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// sourceJSON is the source.json of the plugin in dir.
func sourceJSON(dir string) ([]byte, error) {
	digest, err := pluginDigest(dir)
	if err != nil {
		return nil, fmt.Errorf("source digest: %w", err)
	}
	return encodeJSON(sourceInfo{Digest: digest})
}

// packageSourceDigest is the digest a package's source.json records ("" for
// a package without one, packed before source digests).
func packageSourceDigest(files map[string][]byte) (string, error) {
	raw, ok := files[sourceFile]
	if !ok {
		return "", nil
	}
	var s sourceInfo
	if err := json.Unmarshal(raw, &s); err != nil || !sourceDigestRe.MatchString(s.Digest) {
		return "", fmt.Errorf(`%s must be {"digest": "sha256:<64 lowercase hex digits>"}`, sourceFile)
	}
	return s.Digest, nil
}

var (
	// unpackedDirs hold no package input: tests, dependencies, build
	// outputs.
	unpackedDirs = map[string]bool{"testdata": true, "node_modules": true, "dist": true, "runtimes": true, "__tests__": true}
	// packedDirs are copied into the package (pack) or built into its ui.
	packedDirs = append([]string{"ui/"}, func() (out []string) {
		for _, d := range packDirs {
			out = append(out, d+"/")
		}
		return out
	}()...)
	// unpackedFiles are tests, docs and lock files: dependency-only changes
	// (go.mod as well; the SDK lives outside the plugin too) rebuild the
	// same plugin, and a release may keep the package production has.
	unpackedFiles = regexp.MustCompile(`(_test\.go|\.(test|spec)\.[cm]?[jt]sx?|\.md)$|(^|/)(go\.mod|go\.sum|package-lock\.json)$`)
)

// pluginDigest hashes what the package of the plugin in dir is built from:
// manifest.json, the directories pack copies, ui/ and every Go file of the
// plugin's binaries; see unpackedDirs and unpackedFiles for what is left out.
func pluginDigest(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			return skipUnpackedDir(rel, d.Name())
		}
		if !packedSource(rel, d) {
			return nil
		}
		return hashSourceFile(h, rel, p)
	})
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), err
}

// skipUnpackedDir skips a directory that holds no package input.
func skipUnpackedDir(rel, name string) error {
	if rel != "." && (unpackedDirs[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
		return filepath.SkipDir
	}
	return nil
}

// packedSource reports whether the file rel is package input.
func packedSource(rel string, d fs.DirEntry) bool {
	packed := rel == pkgsig.ManifestFile || strings.HasSuffix(rel, ".go")
	for _, prefix := range packedDirs {
		packed = packed || strings.HasPrefix(rel, prefix)
	}
	return packed && !unpackedFiles.MatchString(rel) && !strings.HasPrefix(d.Name(), ".") && d.Type().IsRegular()
}

func hashSourceFile(h hash.Hash, rel, p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
	h.Write(b)
	return nil
}
