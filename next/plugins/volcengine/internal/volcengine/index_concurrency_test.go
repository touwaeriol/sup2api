package volcengine

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Pause the first upstream response after its observation/mutation, then
// start another node's PATCH. Without resource serialization the second PATCH
// commits first and the delayed response overwrites it with the old value.
func TestIndexOperationsKeepUpstreamOrder(t *testing.T) {
	for _, resource := range []string{"group", "asset"} {
		for _, firstMethod := range []string{"GET", "PATCH"} {
			t.Run(resource+"/"+firstMethod, func(t *testing.T) {
				p, h, f := startRoutes(t)
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				gid, err := insertGroup(h.db, 1, UpstreamGroup{ID: "g-order", Name: "old"})
				if err != nil {
					t.Fatal(err)
				}
				id, route, table := gid, "/asset-groups/:id", "asset_groups"
				if resource == "asset" {
					id, err = insertAsset(h.db, 1, gid, UpstreamAsset{ID: "a-order", Name: "old"})
					if err != nil {
						t.Fatal(err)
					}
					route, table = "/assets/:id", "assets"
				}
				// A second process would have its own plugin object, with the same DB.
				p2 := New()
				if err := p2.Init(ctx, h); err != nil {
					t.Fatal(err)
				}
				firstEntered, secondEntered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unpause := func() { releaseOnce.Do(func() { close(release) }) }
				defer unpause()
				var mu sync.Mutex
				upstreamName, calls := "old", 0
				f.reply = func(c capturedRequest) (int, string) {
					var body map[string]string
					_ = json.Unmarshal(c.Body, &body)
					mu.Lock()
					calls++
					n := calls
					if name, ok := body["Name"]; ok {
						upstreamName = name
					}
					name := upstreamName
					mu.Unlock()
					if n == 1 {
						close(firstEntered)
						select {
						case <-release:
						case <-ctx.Done():
						}
					} else if n == 2 {
						close(secondEntered)
					}
					payload, _ := json.Marshal(map[string]any{"Result": map[string]string{"Id": body["Id"], "Name": name}})
					return http.StatusOK, string(payload)
				}
				type result struct {
					resp *pluginv1.HTTPResponse
					err  error
				}
				call := func(node *Plugin, method, name string) <-chan result {
					done := make(chan result, 1)
					go func() {
						body, _ := json.Marshal(map[string]string{"name": name})
						resp, err := node.HandleHTTP(ctx, &pluginv1.HTTPRequest{Method: method, RoutePath: route,
							PathParams: map[string]string{"id": strconv.FormatInt(id, 10)}, Body: body})
						done <- result{resp, err}
					}()
					return done
				}
				first := call(p, firstMethod, "first")
				select {
				case <-firstEntered:
				case <-ctx.Done():
					t.Fatal("first upstream call never arrived")
				}
				second := call(p2, "PATCH", "new")
				// Wait for positive evidence: the second operation is either
				// blocked on the DB lock, or has incorrectly reached upstream.
				for {
					var waiting bool
					if err := h.db.Raw(`SELECT EXISTS(SELECT 1 FROM pg_locks
						WHERE relation = ?::regclass AND cardinality(pg_blocking_pids(pid)) > 0)`, table).Scan(&waiting).Error; err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					select {
					case <-secondEntered:
						// Let the old implementation commit PATCH before releasing GET.
						r := <-second
						second = nil
						if r.err != nil || r.resp.GetStatus() != http.StatusOK {
							t.Fatalf("second operation: %v %v", r.resp, r.err)
						}
					case <-ctx.Done():
						t.Fatal("second operation neither waited nor reached upstream")
					case <-time.After(10 * time.Millisecond):
						continue
					}
					break
				}
				unpause()
				for _, done := range []<-chan result{first, second} {
					if done == nil {
						continue
					}
					r := <-done
					if r.err != nil || r.resp.GetStatus() != http.StatusOK {
						t.Fatalf("operation: %v %v", r.resp, r.err)
					}
				}
				var indexed string
				if err := h.db.Raw(`SELECT name FROM `+table+` WHERE id=?`, id).Scan(&indexed).Error; err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				want := upstreamName
				mu.Unlock()
				if indexed != "new" || indexed != want {
					t.Fatalf("index = %q, upstream = %q; delayed response undid PATCH", indexed, want)
				}
			})
		}
	}
}

func TestRepeatedCreateDoesNotOverwriteExistingIndex(t *testing.T) {
	_, h, _ := startRoutes(t)
	gid, err := insertGroup(h.db, 1, UpstreamGroup{ID: "g-repeat", Name: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := insertGroup(h.db, 1, UpstreamGroup{ID: "g-repeat", Name: "old"}); err != nil || again != gid {
		t.Fatalf("duplicate group: %d %v", again, err)
	}
	aid, err := insertAsset(h.db, 1, gid, UpstreamAsset{ID: "a-repeat", Name: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := insertAsset(h.db, 1, gid, UpstreamAsset{ID: "a-repeat", Name: "old"}); err != nil || again != aid {
		t.Fatalf("duplicate asset: %d %v", again, err)
	}
	for _, table := range []string{"asset_groups", "assets"} {
		var name string
		if err := h.db.Raw(`SELECT name FROM ` + table).Scan(&name).Error; err != nil || name != "new" {
			t.Fatalf("%s lost the newer index: %q %v", table, name, err)
		}
	}
}

func TestCreateAssetCompensatesCommitFailure(t *testing.T) {
	p, h, f := startRoutes(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	gid, err := insertGroup(h.db, 1, UpstreamGroup{ID: "g-commit", Name: "group"})
	if err != nil {
		t.Fatal(err)
	}
	// The INSERT succeeds. Only COMMIT fails, exercising the transaction
	// boundary added when group deletion and asset creation were serialized.
	if err := h.db.Exec(`CREATE FUNCTION fail_asset_commit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test commit failure'; END $$;
		CREATE CONSTRAINT TRIGGER fail_asset_commit AFTER INSERT ON assets
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_asset_commit()`).Error; err != nil {
		t.Fatal(err)
	}
	arkResult(f, map[string]string{ActionCreateAsset: `{"Id":"a-commit","Name":"new"}`})
	body, _ := json.Marshal(map[string]any{"group_id": gid, "name": "new", "url": "https://cdn.invalid/a.png"})
	_, err = p.HandleHTTP(ctx, &pluginv1.HTTPRequest{Method: "POST", RoutePath: "/assets", Body: body})
	if err == nil {
		t.Fatal("commit failure was reported as success")
	}
	last := f.last(t)
	if last.Query.Get("Action") != ActionDeleteAsset || gjson.GetBytes(last.Body, "Id").String() != "a-commit" {
		t.Fatalf("created upstream asset was not compensated after commit failure: %+v", last)
	}
	var n int
	if err := h.db.Raw(`SELECT count(*) FROM assets`).Scan(&n).Error; err != nil || n != 0 {
		t.Fatalf("failed transaction left an index row: %d %v", n, err)
	}
}
