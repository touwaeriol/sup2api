package volcengine

// Database-backed tests of the asset library routes. They run against
// TEST_DATABASE_URL (skipped when unset) with the real migrations applied,
// and against the fake Ark control plane of arkapi_test.go, so every
// upstream call in here is a real signed HTTP request.
//
// The host is faked locally instead of with pluginsdktest.FakeHost because
// that one does not serve ListAccounts / SendAccountRequest (it embeds
// UnimplementedHostServiceServer). pluginsdktest.Options.Host takes the
// concrete *FakeHost, so it cannot be substituted - hence testHost below,
// which also lets these tests COUNT account reads, which is the property
// that matters most here.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"

	"github.com/Sub2API-Devs/sup2api/next/plugins/volcengine/internal/model"
)

// testHost implements pluginsdk.Host. The embedded interface is nil: a
// method this file does not need panics loudly if a route ever starts using
// it, and the struct keeps compiling when the SDK adds one.
type testHost struct {
	pluginsdk.Host

	db *gorm.DB

	mu sync.Mutex
	// accounts of this plugin's account type, as GetAccount answers.
	accounts map[int64]*pluginv1.Account
	// credReads counts GetAccount calls, one audit row each.
	credReads map[int64]int
	listCalls int

	// manifest and ark serve SendAccountRequest (Client).
	manifest *manifest.Manifest
	ark      *fakeArk
}

func (h *testHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// Client answers SendAccountRequest as the core does: an account of this
// plugin's type, a URL host the manifest allows for that account
// (manifest.UpstreamHostAllowed), then the fake Ark control plane.
func (h *testHost) Client() pluginv1.HostServiceClient { return hostClient{h: h} }

type hostClient struct {
	pluginv1.HostServiceClient
	h *testHost
}

func (c hostClient) SendAccountRequest(ctx context.Context, in *pluginv1.SendAccountRequestRequest, _ ...grpc.CallOption) (*pluginv1.ExecutionHTTPResponse, error) {
	c.h.mu.Lock()
	a, ok := c.h.accounts[in.GetAccountId()]
	c.h.mu.Unlock()
	if !ok {
		return nil, statusNotFound()
	}
	u, err := url.Parse(in.GetUrl())
	if err != nil || !c.h.manifest.UpstreamHostAllowed(u.Hostname(), a.GetType(), a.GetSettingsJson(), a.GetCredentialsJson()) {
		return nil, status.Errorf(codes.PermissionDenied, "host of %q is not allowed for account %d", in.GetUrl(), a.GetId())
	}
	return c.h.ark.send(ctx, in)
}

func (h *testHost) DB(ctx context.Context) (*gorm.DB, error) { return h.db.WithContext(ctx), nil }

func (h *testHost) ListAccounts(_ context.Context, q pluginsdk.AccountQuery) ([]pluginsdk.AccountSummary, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listCalls++
	out := []pluginsdk.AccountSummary{}
	for _, a := range h.accounts {
		if q.Type != "" && q.Type != a.GetType() {
			continue
		}
		out = append(out, pluginsdk.AccountSummary{ID: a.GetId(), Name: a.GetName(), Type: a.GetType(),
			Status: a.GetProperties().GetStatus(), Enabled: a.GetProperties().GetSchedulable(), SettingsJSON: a.GetSettingsJson()})
	}
	return out, "", nil
}

func (h *testHost) Account(_ context.Context, id int64) (*pluginv1.Account, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.credReads[id]++
	a, ok := h.accounts[id]
	if !ok {
		// The real host answers the same NOT_FOUND for "no such account" and
		// for "another plugin's account type" (CONTRACTS §124).
		return nil, statusNotFound()
	}
	return proto.Clone(a).(*pluginv1.Account), nil
}

func (h *testHost) reads(id int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.credReads[id]
}

// statusNotFound is the gRPC status the real host answers for both "no such
// account" and "an account of another plugin's account type".
func statusNotFound() error {
	return status.Error(codes.NotFound, "account not found")
}

// startRoutes brings up the plugin with a fresh schema, the fake Ark server
// and one asset-library-enabled account (id 1) plus one account without an
// AK/SK (id 2).
func startRoutes(t *testing.T) (*Plugin, *testHost, *fakeArk) {
	t.Helper()
	dsn, schema := pluginsdktest.NewSchema(t, "plg_volcengine_t")
	pluginsdktest.ApplyMigrations(t, dsn, schema, filepath.Join("..", "..", "migrations"))
	db, err := pgdb.Open(dsn, pgdb.Options{})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = pgdb.Close(db) })

	f := newFakeArk(t)
	var m manifest.Manifest
	if err := json.Unmarshal(manifestFile(t), &m); err != nil {
		t.Fatal(err)
	}
	h := &testHost{db: db, accounts: map[int64]*pluginv1.Account{}, credReads: map[int64]int{}, manifest: &m, ark: f}
	active := &pluginv1.AccountProperties{Status: "active", Schedulable: true}
	h.accounts[1] = &pluginv1.Account{Id: 1, Name: "ark-with-assets", Type: AccountTypeAPIKey, Properties: active,
		SettingsJson:    `{"base_url":"` + DefaultBaseURL + `","asset_base_url":"` + f.srv.URL + `","asset_region":"` + DefaultAssetRegion + `"}`,
		CredentialsJson: `{"api_key":"ark-key-12345678","access_key":"` + testAK + `","secret_key":"` + testSK + `"}`,
	}
	h.accounts[2] = &pluginv1.Account{Id: 2, Name: "ark-plain", Type: AccountTypeAPIKey, Properties: active,
		SettingsJson:    `{"base_url":"` + DefaultBaseURL + `"}`,
		CredentialsJson: `{"api_key":"ark-key-87654321"}`,
	}

	p := New()
	if err := p.Init(context.Background(), h); err != nil {
		t.Fatalf("init: %v", err)
	}
	return p, h, f
}

// do calls one route through the plugin's router, the way the host does
// (route_path is the manifest pattern, path params come pre-extracted).
func do(t *testing.T, p *Plugin, method, routePath string, params, query map[string]string, body any) *pluginv1.HTTPResponse {
	t.Helper()
	req := &pluginv1.HTTPRequest{
		Caller: &pluginv1.Caller{UserId: 1}, Method: method, Path: routePath, RoutePath: routePath,
		PathParams: params, Query: map[string]*pluginv1.HeaderValues{},
	}
	for k, v := range query {
		req.Query[k] = &pluginv1.HeaderValues{Values: []string{v}}
	}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req.Body = b
	}
	resp, err := p.HandleHTTP(context.Background(), req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, routePath, err)
	}
	return resp
}

func dataOf[T any](t *testing.T, resp *pluginv1.HTTPResponse, wantStatus int) T {
	t.Helper()
	if int(resp.GetStatus()) != wantStatus {
		t.Fatalf("HTTP %d, want %d: %s", resp.GetStatus(), wantStatus, resp.GetBody())
	}
	var out struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(resp.GetBody(), &out); err != nil {
		t.Fatalf("%v: %s", err, resp.GetBody())
	}
	return out.Data
}

func errorCode(t *testing.T, resp *pluginv1.HTTPResponse) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.GetBody(), &out); err != nil {
		t.Fatalf("%v: %s", err, resp.GetBody())
	}
	return out.Error.Code
}

// arkResult makes the fake control plane answer one Result per Action.
func arkResult(f *fakeArk, byAction map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = func(c capturedRequest) (int, string) {
		if payload, ok := byAction[c.Query.Get("Action")]; ok {
			return http.StatusOK, `{"ResponseMetadata":{"RequestId":"r"},"Result":` + payload + `}`
		}
		return http.StatusOK, `{"ResponseMetadata":{"RequestId":"r"},"Result":{}}`
	}
}

// ---------------------------------------------------------------- tests

// TestGroupAndAssetLifecycle is the happy path end to end: create a group
// and an asset upstream, see them in the index (with the account name merged
// in), read one back, rename it and delete it.
func TestGroupAndAssetLifecycle(t *testing.T) {
	p, h, f := startRoutes(t)
	arkResult(f, map[string]string{
		ActionCreateAssetGroup: `{"Id":"g-1","Name":"shots","GroupType":"AIGC"}`,
		ActionCreateAsset:      `{"Id":"a-1","Name":"hero","AssetType":"Image","Status":"Processing"}`,
		ActionGetAsset:         `{"Id":"a-1","Name":"hero","AssetType":"Image","Status":"Succeeded","URL":"https://cdn.invalid/hero.png"}`,
	})

	group := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "shots"}), http.StatusCreated)
	if group["upstream_id"] != "g-1" {
		t.Fatalf("created group = %v", group)
	}
	groupID := int64(group["id"].(float64))

	asset := dataOf[map[string]any](t, do(t, p, "POST", "/assets", nil, nil,
		map[string]any{"group_id": groupID, "name": "hero", "asset_type": "Image", "url": "https://cdn.invalid/hero.png"}),
		http.StatusCreated)
	if asset["upstream_id"] != "a-1" || int64(asset["account_id"].(float64)) != 1 {
		t.Fatalf("created asset = %v", asset)
	}
	assetID := int64(asset["id"].(float64))

	// The index list carries the account name (merged from ListAccounts, not
	// denormalized into the table) and the group name.
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/assets", nil, nil, nil), http.StatusOK)
	if len(rows) != 1 {
		t.Fatalf("index rows = %v", rows)
	}
	if rows[0]["account_name"] != "ark-with-assets" || rows[0]["group_name"] != "shots" {
		t.Fatalf("row = %v", rows[0])
	}
	if rows[0]["index_status"] != IndexOK || rows[0]["status"] != "Processing" {
		t.Fatalf("row status = %v", rows[0])
	}

	// Reading one asset reconciles with upstream: the status Ark now reports
	// lands in the index.
	got := dataOf[map[string]any](t, do(t, p, "GET", "/assets/:id", map[string]string{"id": strconv.FormatInt(assetID, 10)}, nil, nil), http.StatusOK)
	if up, _ := got["upstream"].(map[string]any); up["Status"] != "Succeeded" {
		t.Fatalf("upstream view = %v", got)
	}
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/assets", nil, nil, nil), http.StatusOK)
	if rows[0]["status"] != "Succeeded" {
		t.Fatalf("the index did not pick up the upstream status: %v", rows[0])
	}

	// Rename: upstream first, then the index.
	name := "hero-2"
	do(t, p, "PATCH", "/assets/:id", map[string]string{"id": strconv.FormatInt(assetID, 10)}, nil,
		map[string]any{"name": name})
	if c := f.last(t); c.Query.Get("Action") != ActionUpdateAsset {
		t.Fatalf("rename did not call UpdateAsset: %v", c.Query)
	}
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/assets", nil, nil, nil), http.StatusOK)
	if rows[0]["name"] != name {
		t.Fatalf("index name = %v", rows[0]["name"])
	}

	// Deleting the group cascades the asset out of the index, because
	// DeleteAssetGroup removes the assets upstream too.
	do(t, p, "DELETE", "/asset-groups/:id", map[string]string{"id": strconv.FormatInt(groupID, 10)}, nil, nil)
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/assets", nil, nil, nil), http.StatusOK)
	if len(rows) != 0 {
		t.Fatalf("assets survived their group: %v", rows)
	}
	_ = h
}

// TestCredentialsAreReadOncePerRequest is the audit-load rule: one request
// reads an account's credentials at most once, because the core writes an
// audit row for every read.
func TestCredentialsAreReadOncePerRequest(t *testing.T) {
	p, h, f := startRoutes(t)
	arkResult(f, map[string]string{ActionCreateAssetGroup: `{"Id":"g-1","Name":"a"}`})

	do(t, p, "POST", "/asset-groups", nil, nil, map[string]any{"account_id": 1, "name": "a"})
	if got := h.reads(1); got != 1 {
		t.Fatalf("credential reads for one create = %d, want 1", got)
	}
	// Listing the index reads no credentials at all: that is what the local
	// index is for.
	before := h.reads(1)
	do(t, p, "GET", "/asset-groups", nil, nil, nil)
	do(t, p, "GET", "/assets", nil, nil, nil)
	if got := h.reads(1); got != before {
		t.Fatalf("listing the index read credentials %d times", got-before)
	}
}

// TestAssetLibraryOffIsNotAnError: an account without an AK/SK is a normal
// Ark account. Mutating routes refuse it with a clear conflict instead of
// pretending to work, and the account list says so without reading any
// credential.
func TestAssetLibraryOffIsNotAnError(t *testing.T) {
	p, h, _ := startRoutes(t)
	resp := do(t, p, "POST", "/asset-groups", nil, nil, map[string]any{"account_id": 2, "name": "x"})
	if resp.GetStatus() != http.StatusConflict || errorCode(t, resp) != "asset_library_disabled" {
		t.Fatalf("HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
	// GET /accounts must not read a single credential: it is a list page,
	// and every read would cost an audit row per account per page load. The
	// price is an honest "unknown" in the asset_library column.
	before1, before2 := h.reads(1), h.reads(2)
	accounts := dataOf[[]AccountView](t, do(t, p, "GET", "/accounts", nil, nil, nil), http.StatusOK)
	if h.reads(1) != before1 || h.reads(2) != before2 {
		t.Fatalf("listing accounts read credentials: %d %d", h.reads(1)-before1, h.reads(2)-before2)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	for _, a := range accounts {
		if a.AssetLibrary != AssetLibraryUnknown {
			t.Errorf("account %d: asset_library = %q without ?check=1", a.ID, a.AssetLibrary)
		}
		// The effective endpoint is always reported, defaults filled in.
		if a.AssetBaseURL == "" || a.AssetRegion == "" {
			t.Errorf("account %d: effective asset endpoint = %q %q", a.ID, a.AssetBaseURL, a.AssetRegion)
		}
	}

	// ?check=1 is the deliberate, audited answer: one credential read per
	// account, and a real on/off.
	before1, before2 = h.reads(1), h.reads(2)
	accounts = dataOf[[]AccountView](t, do(t, p, "GET", "/accounts", nil, map[string]string{"check": "1"}, nil), http.StatusOK)
	if h.reads(1) != before1+1 || h.reads(2) != before2+1 {
		t.Fatalf("?check=1 read credentials %d and %d times, want once each", h.reads(1)-before1, h.reads(2)-before2)
	}
	for _, a := range accounts {
		switch a.ID {
		case 1:
			if a.AssetLibrary != AssetLibraryOn {
				t.Errorf("account 1 has an AK/SK pair: asset_library = %q", a.AssetLibrary)
			}
		case 2:
			if a.AssetLibrary != AssetLibraryOff {
				t.Errorf("account 2 has no AK/SK pair: asset_library = %q", a.AssetLibrary)
			}
		}
	}
}

// TestUnknownAccountDoesNotLeakExistence: the host returns the same
// NOT_FOUND for a missing account and for another plugin's account, and so
// does the route.
func TestUnknownAccountDoesNotLeakExistence(t *testing.T) {
	p, _, _ := startRoutes(t)
	resp := do(t, p, "POST", "/asset-groups", nil, nil, map[string]any{"account_id": 999, "name": "x"})
	if resp.GetStatus() != http.StatusNotFound || errorCode(t, resp) != "not_found" {
		t.Fatalf("HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
}

// TestUpstreamGoneMarksTheIndex: upstream is the truth. A GET whose upstream
// copy has disappeared marks the row missing (and keeps it: a row that
// silently vanished would be indistinguishable from a bug), and a DELETE of
// the same row then succeeds, because the goal state is reached.
func TestUpstreamGoneMarksTheIndex(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{ActionCreateAssetGroup: `{"Id":"g-1","Name":"a"}`})
	group := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "a"}), http.StatusCreated)
	id := strconv.FormatInt(int64(group["id"].(float64)), 10)

	// Upstream now reports the group as gone, inside an HTTP 200 (which is
	// how volcengine does it).
	f.mu.Lock()
	f.reply = func(capturedRequest) (int, string) {
		return http.StatusOK, `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"NotFound.AssetGroup","Message":"gone"}}}`
	}
	f.mu.Unlock()

	resp := do(t, p, "GET", "/asset-groups/:id", map[string]string{"id": id}, nil, nil)
	if resp.GetStatus() != http.StatusNotFound || errorCode(t, resp) != "upstream_not_found" {
		t.Fatalf("HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if len(rows) != 1 || rows[0]["index_status"] != IndexMissing {
		t.Fatalf("the stale row was not marked missing: %v", rows)
	}
	// It is still filterable and still deletable.
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, map[string]string{"index_status": IndexMissing}, nil), http.StatusOK)
	if len(rows) != 1 {
		t.Fatalf("index_status filter = %v", rows)
	}
	if resp := do(t, p, "DELETE", "/asset-groups/:id", map[string]string{"id": id}, nil, nil); resp.GetStatus() != http.StatusOK {
		t.Fatalf("deleting a row whose upstream is gone: HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if len(rows) != 0 {
		t.Fatalf("rows after delete = %v", rows)
	}
}

// TestDeleteKeepsTheIndexWhenUpstreamFails: an upstream failure that is NOT
// "already gone" must leave the index alone, or the only pointer to a group
// that still exists upstream is lost.
func TestDeleteKeepsTheIndexWhenUpstreamFails(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{ActionCreateAssetGroup: `{"Id":"g-1","Name":"a"}`})
	group := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "a"}), http.StatusCreated)
	id := strconv.FormatInt(int64(group["id"].(float64)), 10)

	f.mu.Lock()
	f.reply = func(capturedRequest) (int, string) {
		return http.StatusForbidden, `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"AccessDenied","Message":"no"}}}`
	}
	f.mu.Unlock()

	resp := do(t, p, "DELETE", "/asset-groups/:id", map[string]string{"id": id}, nil, nil)
	if resp.GetStatus() != http.StatusBadGateway || errorCode(t, resp) != "upstream_denied" {
		t.Fatalf("HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if len(rows) != 1 {
		t.Fatalf("the index row was dropped although upstream still has the group: %v", rows)
	}
}

// TestCreateWithoutUpstreamIDIsRefused: a Result with no Id cannot be
// indexed, and inventing one would create a row pointing at nothing.
func TestCreateWithoutUpstreamIDIsRefused(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{ActionCreateAssetGroup: `{"Name":"a"}`})
	resp := do(t, p, "POST", "/asset-groups", nil, nil, map[string]any{"account_id": 1, "name": "a"})
	if resp.GetStatus() != http.StatusBadGateway {
		t.Fatalf("HTTP %d %s", resp.GetStatus(), resp.GetBody())
	}
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if len(rows) != 0 {
		t.Fatalf("an unidentifiable group was indexed: %v", rows)
	}
}

// TestRetriedCreateIsIdempotent: a create whose response was lost upstream
// is retried by the operator; the same upstream id must not produce two
// index rows.
func TestRetriedCreateIsIdempotent(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{ActionCreateAssetGroup: `{"Id":"g-1","Name":"a"}`})
	first := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "a"}), http.StatusCreated)
	second := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "a"}), http.StatusCreated)
	if first["id"] != second["id"] {
		t.Fatalf("the same upstream group produced two index rows: %v %v", first, second)
	}
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
}

// TestLiveUpstreamListing bypasses the index: it is the answer to "the
// Volcengine console shows a group this site does not know about".
func TestLiveUpstreamListing(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{
		ActionListAssetGroups: `{"TotalCount":2,"Items":[{"Id":"g-9","Name":"made-elsewhere"},{"Id":"g-8","Name":"other"}]}`,
	})
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/accounts/:account_id/upstream/asset-groups",
		map[string]string{"account_id": "1"}, nil, nil), http.StatusOK)
	if len(rows) != 2 || rows[0]["upstream_id"] != "g-9" {
		t.Fatalf("live listing = %v", rows)
	}
	c := f.last(t)
	if c.Query.Get("Action") != ActionListAssetGroups {
		t.Fatalf("action = %v", c.Query)
	}
	verifySignature(t, c, testAK, testSK, DefaultAssetRegion, AssetServiceName)
}

// TestFieldValidation: the routes refuse what they cannot act on before any
// credential is read.
func TestFieldValidation(t *testing.T) {
	p, h, _ := startRoutes(t)
	cases := []struct {
		method, route string
		params        map[string]string
		body          any
	}{
		{"POST", "/asset-groups", nil, map[string]any{"name": "x"}},                                       // no account
		{"POST", "/asset-groups", nil, map[string]any{"account_id": 1}},                                   // no name
		{"POST", "/assets", nil, map[string]any{"group_id": 1}},                                           // no url
		{"POST", "/assets", nil, map[string]any{"url": "https://x.invalid/a"}},                            // no group
		{"POST", "/asset-groups", nil, map[string]any{"account_id": 1, "name": strings.Repeat("n", 129)}}, // name too long
		{"POST", "/assets", nil, map[string]any{"group_id": 1, "url": "ftp://x.invalid/a"}},               // not an http(s) or tos URL
		{"PATCH", "/assets/:id", map[string]string{"id": "1"}, map[string]any{}},                          // nothing to change
		{"GET", "/asset-groups/:id", map[string]string{"id": "abc"}, nil},                                 // not an id
		{"DELETE", "/assets/:id", map[string]string{"id": "0"}, nil},                                      // not an id
	}
	for _, tc := range cases {
		resp := do(t, p, tc.method, tc.route, tc.params, nil, tc.body)
		if resp.GetStatus() != http.StatusBadRequest {
			t.Errorf("%s %s %v: HTTP %d %s", tc.method, tc.route, tc.body, resp.GetStatus(), resp.GetBody())
		}
	}
	if h.reads(1) != 0 {
		t.Errorf("a rejected request read credentials %d times", h.reads(1))
	}
}

// TestUpdateCanClearAField: an operator who submits an empty title means it,
// so the index must not keep the old value. (Reads of an upstream result are
// the opposite case - a field the result omits must not blank the index -
// which is why the two paths are separate SQL statements.)
func TestUpdateCanClearAField(t *testing.T) {
	p, _, f := startRoutes(t)
	arkResult(f, map[string]string{
		ActionCreateAssetGroup: `{"Id":"g-1","Name":"a","Title":"old title"}`,
		// A GetAssetGroup that omits Title must leave the index alone.
		ActionGetAssetGroup: `{"Id":"g-1","Name":"a"}`,
	})
	group := dataOf[map[string]any](t, do(t, p, "POST", "/asset-groups", nil, nil,
		map[string]any{"account_id": 1, "name": "a", "title": "old title"}), http.StatusCreated)
	id := strconv.FormatInt(int64(group["id"].(float64)), 10)

	do(t, p, "GET", "/asset-groups/:id", map[string]string{"id": id}, nil, nil)
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if rows[0]["title"] != "old title" {
		t.Fatalf("a result without Title blanked the index: %v", rows[0])
	}

	do(t, p, "PATCH", "/asset-groups/:id", map[string]string{"id": id}, nil, map[string]any{"title": ""})
	rows = dataOf[[]map[string]any](t, do(t, p, "GET", "/asset-groups", nil, nil, nil), http.StatusOK)
	if rows[0]["title"] != "" {
		t.Fatalf("clearing the title did not reach the index: %v", rows[0])
	}
	if rows[0]["name"] != "a" {
		t.Fatalf("a field that was not submitted changed: %v", rows[0])
	}
}

// TestUnknownRoute: the router answers 404 rather than panicking on a path
// the manifest does not declare.
func TestUnknownRoute(t *testing.T) {
	p, _, _ := startRoutes(t)
	if resp := do(t, p, "GET", "/nope", nil, nil, nil); resp.GetStatus() != http.StatusNotFound {
		t.Fatalf("HTTP %d", resp.GetStatus())
	}
}

// ---------------------------------------------------------------- ?q= search

// seedSearchRows puts four groups and four assets in the index directly, so
// the names can contain the characters a search has to treat as text.
func seedSearchRows(t *testing.T, p *Plugin) {
	t.Helper()
	ctx := context.Background()
	db, err := p.host.DB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ upstream, name, title string }{
		{"g-hero", "hero-shots", "Hero shots"},
		{"g-villain", "villain-shots", "Villains"},
		{"g-pct", "100%-done", "literal percent"},
		{"g-us", "a_c", "literal underscore"},
	}
	for _, r := range rows {
		g := model.AssetGroup{AccountID: 1, UpstreamID: r.upstream, Name: r.name, Title: r.title}
		if err := db.Create(&g).Error; err != nil {
			t.Fatal(err)
		}
		a := model.Asset{AccountID: 1, GroupID: g.ID, UpstreamID: "a-" + r.upstream, Name: r.name, URL: "https://cdn.invalid/" + r.upstream + ".png"}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func searchNames(t *testing.T, p *Plugin, route, q string) []string {
	t.Helper()
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", route, nil, map[string]string{"q": q}, nil), http.StatusOK)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["name"].(string))
	}
	sort.Strings(out)
	return out
}

func searchTotal(t *testing.T, p *Plugin, route, q string) int64 {
	t.Helper()
	resp := do(t, p, "GET", route, nil, map[string]string{"q": q}, nil)
	var out struct {
		Page struct {
			Total int64 `json:"total"`
		} `json:"page"`
	}
	if err := json.Unmarshal(resp.GetBody(), &out); err != nil {
		t.Fatalf("%v: %s", err, resp.GetBody())
	}
	return out.Page.Total
}

// TestAssetSearchQ is the whole point of ?q=: both asset routes really filter.
// The console's search box was deleted because these two answered the full
// list to every term, which reads as "no matches were filtered out" and is
// worse than an empty table. So the assertions here are POSITIVE (this term
// returns exactly these rows), never "the count changed".
func TestAssetSearchQ(t *testing.T) {
	p, _, _ := startRoutes(t)
	seedSearchRows(t, p)

	for _, route := range []string{"/asset-groups", "/assets"} {
		// A name substring, case-insensitively, on both routes.
		if got := searchNames(t, p, route, "hero"); len(got) != 1 || got[0] != "hero-shots" {
			t.Fatalf("%s ?q=hero = %v", route, got)
		}
		if got := searchNames(t, p, route, "HERO"); len(got) != 1 || got[0] != "hero-shots" {
			t.Fatalf("%s is case sensitive: ?q=HERO = %v", route, got)
		}
		// A term shared by two rows returns both, and nothing else.
		if got := searchNames(t, p, route, "shots"); len(got) != 2 ||
			got[0] != "hero-shots" || got[1] != "villain-shots" {
			t.Fatalf("%s ?q=shots = %v", route, got)
		}
		// A term nothing matches returns an EMPTY page, not the full list.
		if got := searchNames(t, p, route, "nothing-has-this"); len(got) != 0 {
			t.Fatalf("%s answered %v for a term nothing matches", route, got)
		}
		// An empty / whitespace term is "no filter", not "matches nothing".
		for _, q := range []string{"", "   "} {
			if got := searchNames(t, p, route, q); len(got) != 4 {
				t.Fatalf("%s ?q=%q = %v, want the unfiltered page", route, q, got)
			}
		}
		// The Ark id is searchable: it is what gets pasted in from a usage
		// record or the Volcengine console.
		if got := searchNames(t, p, route, "g-villain"); len(got) != 1 || got[0] != "villain-shots" {
			t.Fatalf("%s ?q=g-villain = %v", route, got)
		}
		// total is filtered too. A count that ignored q would make the console
		// page through rows the filter had already removed.
		if got := searchTotal(t, p, route, "shots"); got != 2 {
			t.Fatalf("%s total for ?q=shots = %d, want 2", route, got)
		}

		// LIKE metacharacters are TEXT, not syntax. "%" used to match every
		// row and "a_c" every three-character name - a search that answers
		// something other than what was asked.
		if got := searchNames(t, p, route, "%"); len(got) != 1 || got[0] != "100%-done" {
			t.Fatalf("%s ?q=%% = %v, want only the row whose name contains a percent sign", route, got)
		}
		if got := searchNames(t, p, route, "a_c"); len(got) != 1 || got[0] != "a_c" {
			t.Fatalf("%s ?q=a_c = %v, want only the row literally named a_c", route, got)
		}
		if got := searchNames(t, p, route, `\`); len(got) != 0 {
			t.Fatalf("%s ?q=backslash = %v, want no rows (no name contains one)", route, got)
		}
	}

	// A group's title is searchable; an asset has none, so the same term finds
	// the group and no asset.
	if got := searchNames(t, p, "/asset-groups", "Villains"); len(got) != 1 || got[0] != "villain-shots" {
		t.Fatalf("group title search = %v", got)
	}

	// q composes with the filters that were already there instead of replacing
	// them: index_status=missing plus a term that matches an 'ok' row is empty.
	rows := dataOf[[]map[string]any](t, do(t, p, "GET", "/assets", nil,
		map[string]string{"q": "hero", "index_status": IndexMissing}, nil), http.StatusOK)
	if len(rows) != 0 {
		t.Fatalf("q replaced index_status instead of narrowing with it: %v", rows)
	}
}

// TestLikeTerm pins the escaping on its own, so a future call site cannot
// reintroduce the wildcard by forgetting it.
func TestLikeTerm(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"   ":     "",
		"  hero ": "hero",
		"100%":    `100\%`,
		"a_c":     `a\_c`,
		`a\b`:     `a\\b`,
		`%_\`:     `\%\_\\`,
	}
	for in, want := range cases {
		if got := LikeTerm(in); got != want {
			t.Errorf("LikeTerm(%q) = %q, want %q", in, got, want)
		}
	}
}

// An id the index does not hold answers 404 on every route that locks the
// row first, before any upstream call: the lock finds no row.
func TestIndexRoutesUnknownRow(t *testing.T) {
	p, _, f := startRoutes(t)
	unknown := map[string]string{"id": "424242"}
	for _, c := range []struct {
		method, route string
		body          any
	}{
		{"GET", "/asset-groups/:id", nil},
		{"PATCH", "/asset-groups/:id", map[string]any{"name": "x"}},
		{"DELETE", "/asset-groups/:id", nil},
		{"GET", "/assets/:id", nil},
		{"PATCH", "/assets/:id", map[string]any{"name": "x"}},
		{"DELETE", "/assets/:id", nil},
	} {
		resp := do(t, p, c.method, c.route, unknown, nil, c.body)
		if resp.GetStatus() != http.StatusNotFound || errorCode(t, resp) != "not_found" {
			t.Fatalf("%s %s = %d %s", c.method, c.route, resp.GetStatus(), resp.GetBody())
		}
	}
	resp := do(t, p, "POST", "/assets", nil, nil, map[string]any{"group_id": 424242, "url": "https://cdn.invalid/a.png"})
	if resp.GetStatus() != http.StatusNotFound || errorCode(t, resp) != "not_found" {
		t.Fatalf("POST /assets into an unknown group = %d %s", resp.GetStatus(), resp.GetBody())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Fatalf("upstream called for rows the index does not hold: %+v", f.requests)
	}
}
