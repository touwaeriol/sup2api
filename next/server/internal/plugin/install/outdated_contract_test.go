package install

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest/check"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg/pkgtest"
)

// withRawField sets a field the manifest types no longer have on the
// manifest.json of files (set edits the decoded document).
func withRawField(t *testing.T, files map[string][]byte, set func(doc map[string]any)) map[string][]byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(files[pkgsig.ManifestFile], &doc); err != nil {
		t.Fatal(err)
	}
	set(doc)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	files[pkgsig.ManifestFile] = b
	return files
}

// A package built for the older plugin contract is refused at upload with
// the one message the runtime gives at load, whatever gives it away, and
// nothing of it is recorded.
func TestUploadRefusesOutdatedContract(t *testing.T) {
	e := newEnv(t)
	const key, version = "video", "0.1.0"
	firstAccountType := func(doc map[string]any) map[string]any {
		return doc["accountTypes"].([]any)[0].(map[string]any)
	}
	for _, tc := range []struct {
		name  string
		files func() map[string][]byte
	}{
		{"runtime none", func() map[string][]byte {
			m := pkgtest.Platform(key, version, "sub2api")
			m.Runtime = "none"
			return pkgtest.Files(m)
		}},
		{"upstream block", func() map[string][]byte {
			return withRawField(t, pkgtest.Files(pkgtest.Platform(key, version, "sub2api")), func(doc map[string]any) {
				firstAccountType(doc)["upstream"] = map[string]any{"preset": "openai"}
			})
		}},
		{"capability platform.execute.v1", func() map[string][]byte {
			m := pkgtest.Platform(key, version, "sub2api")
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: "platform.execute.v1"})
			return pkgtest.Files(m)
		}},
		{"capability platform.poll.v1", func() map[string][]byte {
			m := pkgtest.Platform(key, version, "sub2api")
			m.Capabilities = append(m.Capabilities, manifest.Capability{ID: "platform.poll.v1"})
			return pkgtest.Files(m)
		}},
		{"request fields", func() map[string][]byte {
			return withRawField(t, pkgtest.Files(pkgtest.Platform(key, version, "sub2api")), func(doc map[string]any) {
				doc["platforms"].([]any)[0].(map[string]any)["requestFields"] = []any{"model"}
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := pkgtest.Zip(pkgtest.Sign(tc.files(), "sub2api", e.root))
			_, err := e.svc.Upload(context.Background(), data, e.admin, UploadOptions{})
			var ce *core.Error
			if want := protocol.OutdatedContractMessage(key, version, testHostVersion); !errors.As(err, &ce) || ce.Code != core.ErrInvalidArgument.Code || ce.Message != want {
				t.Fatalf("upload:\n got %v\nwant %s", err, want)
			}
			if code := fieldCode(err, "manifest"); code != check.CodeOutdatedContract {
				t.Fatalf("field code = %q", code)
			}
			var n int
			if err := e.db.SQL.QueryRow(`SELECT count(*) FROM plugin_versions WHERE plugin_key = $1`, key).Scan(&n); err != nil || n != 0 {
				t.Fatalf("versions recorded: %d, %v", n, err)
			}
		})
	}
}
