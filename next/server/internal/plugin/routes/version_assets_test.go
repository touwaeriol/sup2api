package routes_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/routes"
)

type oldVersionAssets struct {
	vh string
	// info, when set, is the package the stub answers with.
	info *core.PluginInfo
	// calls counts ReadVersionAsset: each is a package load.
	calls int
}

func (a *oldVersionAssets) ReadVersionAsset(_ context.Context, key, vh, name string) (core.PluginInfo, []byte, string, error) {
	a.calls++
	want := a.vh
	if want == "" {
		want = "0.9.0-12345678"
	}
	if key != "demo" || vh != want {
		return core.PluginInfo{}, nil, "", core.ErrNotFound
	}
	if a.info != nil {
		return *a.info, []byte("old asset"), "image/svg+xml", nil
	}
	return core.PluginInfo{Key: key, Version: "0.9.0"}, []byte("old asset"), "text/javascript", nil
}

// A name no package may serve is refused for a version this node does not
// serve before that version's package is loaded; a public directory or a
// possible icon is still looked up, and the loaded manifest has the last word.
func TestVersionAssetsRefusedBeforeLoadingThePackage(t *testing.T) {
	assets := &oldVersionAssets{info: &core.PluginInfo{Key: "demo", Version: "0.9.0",
		Manifest: &manifest.Manifest{Icon: "assets/logo.svg", AccountTypes: []manifest.AccountType{{Icon: "assets/type.PNG"}}}}}
	engine, _, _, _ := setup(t, routes.WithVersionAssets(assets))
	for _, tc := range []struct {
		path   string
		status int
		loads  int
	}{
		{"secrets.txt", 404, 0},
		{"manifest.json", 404, 0},
		{"runtimes/linux-amd64/plugin", 404, 0},
		{"migrations/0001.sql", 404, 0},
		{"assets/logo.svg.bak", 404, 0},
		{"assets/logo", 404, 0},
		{"ui", 404, 0},
		{"ui/", 404, 0},
		{"ui//", 404, 0},
		{"ui/main.js", 200, 1},
		{"forms/settings.schema.json", 404, 0},
		{"i18n/zh.json", 200, 1},
		{"assets/logo.svg", 200, 1},
		{"assets/type.PNG", 200, 1},
		{"assets/other.svg", 404, 1}, // icon-like, but not an icon of this package
		{"assets/logo.webp", 404, 1},
		{"assets/photo.jpeg", 404, 1},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			assets.calls = 0
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, httptest.NewRequest(method, "/plugin-ui/demo/0.9.0-12345678/"+tc.path, nil))
			if rec.Code != tc.status || assets.calls != tc.loads {
				t.Errorf("%s %s: status %d (want %d), package loads %d (want %d)", method, tc.path, rec.Code, tc.status, assets.calls, tc.loads)
			}
			if method == "GET" && tc.status == 200 && rec.Body.String() != "old asset" {
				t.Errorf("%s: body %q", tc.path, rec.Body.String())
			}
		}
	}
}

func TestVersionAssetsSurviveDifferentLocalGeneration(t *testing.T) {
	engine, _, _, _ := setup(t, routes.WithVersionAssets(&oldVersionAssets{}))
	for _, tc := range []struct {
		path   string
		status int
	}{{"ui/main.js", 200}, {"runtimes/linux-amd64/plugin", 404}, {"../manifest.json", 404}} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest("GET", "/plugin-ui/demo/0.9.0-12345678/"+tc.path, nil))
		if rec.Code != tc.status {
			t.Fatalf("%s: status %d body %s", tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestVersionAssetsRecoverWhenCurrentPackageRetired(t *testing.T) {
	assets := &oldVersionAssets{}
	engine, _, reg, pkg := setup(t, routes.WithVersionAssets(assets))
	info, _ := reg.Current().Plugin("demo")
	assets.vh = strings.TrimPrefix(info.AssetBase, "/plugin-ui/demo/")
	if err := pkg.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest("GET", info.AssetBase+"/ui/main.js", nil))
	if rec.Code != 200 || rec.Body.String() != "old asset" {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
