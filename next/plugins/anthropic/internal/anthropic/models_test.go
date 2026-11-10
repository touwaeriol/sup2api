package anthropic

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

type modelsResp struct {
	Data []map[string]any `json:"data"`
	Page pluginsdk.Page   `json:"page"`
}

func getModels(t *testing.T, h *pluginsdktest.Harness, q map[string]string) modelsResp {
	t.Helper()
	resp := h.Do("GET", "/models", q, nil)
	if resp.GetStatus() != 200 {
		t.Fatalf("GET /models = %d %s", resp.GetStatus(), resp.GetBody())
	}
	var out modelsResp
	if err := json.Unmarshal(resp.GetBody(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestModelCatalogUpgrade applies 0001, checks GET /models, then applies the
// upgrade fixture's migration and checks the backfilled family column is served.
func TestModelCatalogUpgrade(t *testing.T) {
	dsn, schema := pluginsdktest.NewSchema(t, "plg_anthropic_t")
	root := filepath.Join("..", "..")
	applied := pluginsdktest.ApplyMigrations(t, dsn, schema, filepath.Join(root, "migrations"))
	if len(applied) != 1 {
		t.Fatalf("applied = %v", applied)
	}

	fh := pluginsdktest.NewFakeHost()
	fh.SetDSN(dsn, schema)
	h := pluginsdktest.Start(t, New(), pluginsdktest.Options{Host: fh, SDK: []pluginsdk.Option{pluginsdk.WithInfo("anthropic", "0.3.1")}})

	all := getModels(t, h, nil)
	if all.Page.Total != 10 || len(all.Data) != 10 || all.Data[0]["model_id"] != "claude-fable-5-1" {
		t.Fatalf("models = %+v", all)
	}
	if _, ok := all.Data[0]["family"]; ok {
		t.Fatal("family must not exist before 0002")
	}
	paged := getModels(t, h, map[string]string{"page": "2", "page_size": "3", "q": "opus"})
	if paged.Page.Total != 5 || len(paged.Data) != 2 || paged.Page.Page != 2 {
		t.Fatalf("paged = %+v", paged)
	}

	pluginsdktest.ApplyMigrations(t, dsn, schema, filepath.Join(root, "testdata", "upgrade", "migrations"))
	all = getModels(t, h, nil)
	fam := map[string]string{}
	for _, m := range all.Data {
		fam[m["model_id"].(string)], _ = m["family"].(string)
	}
	for id, want := range map[string]string{"claude-opus-5": "opus", "claude-sonnet-4-6": "sonnet", "claude-haiku-4-5": "haiku", "claude-fable-5-1": "fable"} {
		if fam[id] != want {
			t.Errorf("family[%s] = %q, want %q", id, fam[id], want)
		}
	}
}

func TestModelsWithoutDB(t *testing.T) {
	resp := start(t).Do("GET", "/models", nil, nil)
	if resp.GetStatus() != 503 {
		t.Fatalf("status = %d", resp.GetStatus())
	}
}
