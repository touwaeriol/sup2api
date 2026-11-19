package volcengine

// The asset library's own HTTP API, served under /api/v1/p/volcengine
// (manifest routes, scope admin). These routes are NOT gateway traffic: the
// core only tells the plugin who the caller is, so the plugin reads the
// accounts of its own account type through HostService.ListAccounts and
// each account through GetAccount (CONTRACTS §124).
//
// ACCOUNT READS ARE AUDITED. Every GetAccount call writes an
// audit row in the core, on purpose: reading a secret without upstream
// traffic to justify it is exactly what an administrator must be able to
// review afterwards. Consequence for this file: credentials are fetched at
// most ONCE PER ACCOUNT PER REQUEST (see creds), never in a loop, and the
// plaintext lives no longer than the request - it is never written to the
// plugin's schema, never logged and never put in a response.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"

	"github.com/Sub2API-Devs/sup2api/next/plugins/volcengine/internal/model"
)

// User permission keys declared in manifest.json.
const (
	PermAssetsRead   = "assets:read"
	PermAssetsManage = "assets:manage"
)

// Paging of Host.ListAccounts. The host caps a page at 200; the page budget
// is a guard against a misbehaving cursor, not a real limit on how many Ark
// accounts an installation may have.
const (
	accountPageSize = 200
	maxAccountPages = 10
)

func (p *Plugin) routes() {
	p.Handle("GET", "/accounts", p.listAccounts)

	p.Handle("GET", "/asset-groups", p.listGroupIndex)
	p.Handle("POST", "/asset-groups", p.createGroup)
	p.Handle("GET", "/asset-groups/:id", p.getGroup)
	p.Handle("PATCH", "/asset-groups/:id", p.updateGroup)
	p.Handle("DELETE", "/asset-groups/:id", p.deleteGroup)

	p.Handle("GET", "/assets", p.listAssetIndex)
	p.Handle("POST", "/assets", p.createAsset)
	p.Handle("GET", "/assets/:id", p.getAsset)
	p.Handle("PATCH", "/assets/:id", p.updateAsset)
	p.Handle("DELETE", "/assets/:id", p.deleteAsset)

	// Live upstream listings: what the Volcengine side really holds, index
	// or no index. They are the answer to "the console shows a group I did
	// not create here".
	p.Handle("GET", "/accounts/:account_id/upstream/asset-groups", p.listUpstreamGroups)
	p.Handle("GET", "/accounts/:account_id/upstream/assets", p.listUpstreamAssets)
}

// ---------------------------------------------------------------- helpers

// unavailable and notFound wrap the SDK envelopes with this plugin's code
// strings; badRequest is the SDK's one-field 400 as is.
func unavailable(msg string) *pluginv1.HTTPResponse {
	return pluginsdk.Unavailable(msg)
}

func notFound(msg string) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusNotFound, "not_found", msg)
}

func badRequest(field, code, msg string) *pluginv1.HTTPResponse {
	return pluginsdk.BadRequest(field, code, msg)
}

// upstreamFail maps an upstream Action failure onto an HTTP response. The
// upstream code is carried through so an operator can tell a wrong AK/SK
// from a throttle from a genuinely absent resource.
func upstreamFail(e *ArkError) *pluginv1.HTTPResponse {
	switch {
	case e.NotFound():
		return pluginsdk.ErrorResponse(http.StatusNotFound, "upstream_not_found", e.Error())
	case e.Throttled():
		return pluginsdk.ErrorResponse(http.StatusTooManyRequests, "upstream_throttled", e.Error())
	case e.Denied():
		return pluginsdk.ErrorResponse(http.StatusBadGateway, "upstream_denied",
			e.Error()+" / 素材库凭证或签名区域不正确")
	default:
		return pluginsdk.ErrorResponse(http.StatusBadGateway, "upstream_error", e.Error())
	}
}

// pathID reads a positive integer path parameter. The host fills path_params
// from the matched manifest pattern; the last path segment is a fallback so a
// host that ever stops doing so degrades into a working route instead of a
// 400 on every id.
func pathID(req *pluginv1.HTTPRequest, name string) (int64, *pluginv1.HTTPResponse) {
	raw := req.GetPathParams()[name]
	if raw == "" {
		path := strings.TrimSuffix(req.GetPath(), "/")
		if i := strings.LastIndexByte(path, '/'); i >= 0 && !strings.HasPrefix(path[i+1:], ":") {
			raw = path[i+1:]
		}
	}
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest(name, "invalid", name+" must be a positive integer / "+name+" 必须是正整数")
	}
	return id, nil
}

func queryID(req *pluginv1.HTTPRequest, name string) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(pluginsdk.Query(req, name)), 10, 64)
	if id < 0 {
		return 0
	}
	return id
}

// creds is the per-request credential cache. One instance per handler call.
type creds struct {
	p     *Plugin
	byID  map[int64]*AssetConfig
	names map[int64]string
}

func (p *Plugin) newCreds() *creds {
	return &creds{p: p, byID: map[int64]*AssetConfig{}}
}

// config returns the asset library configuration of one account, reading its
// credentials at most once per request. An account that does not take part in
// the asset library (no AK/SK) answers a 409, because every mutating route
// needs a signing key and "silently did nothing" is the worst answer.
func (c *creds) config(ctx context.Context, accountID int64) (*AssetConfig, *pluginv1.HTTPResponse) {
	if cfg, ok := c.byID[accountID]; ok {
		if cfg == nil {
			return nil, assetDisabled(accountID)
		}
		return cfg, nil
	}
	acc, err := c.p.host.Account(ctx, accountID)
	if err != nil {
		// The host answers NOT_FOUND both for "no such account" and for "an
		// account of another plugin's account type", deliberately, so this
		// response must not distinguish them either.
		if grpcNotFound(err) {
			return nil, notFound("no such Ark account / 没有这个方舟账号")
		}
		return nil, unavailable("cannot read the account credentials: " + err.Error())
	}
	cfg, err := AssetConfigOf(acc)
	if err != nil {
		return nil, pluginsdk.ErrorResponse(http.StatusConflict, "account_misconfigured", err.Error())
	}
	c.byID[accountID] = cfg
	if cfg == nil {
		return nil, assetDisabled(accountID)
	}
	return cfg, nil
}

func assetDisabled(accountID int64) *pluginv1.HTTPResponse {
	return pluginsdk.ErrorResponse(http.StatusConflict, "asset_library_disabled",
		"account "+strconv.FormatInt(accountID, 10)+" has no access_key/secret_key, so its asset library is off"+
			" / 该账号未填写 Access Key / Secret Key，素材库未启用")
}

// accountNames returns the id -> name map of this plugin's accounts. It uses
// ListAccounts, which never returns credentials and writes no audit row, so
// it is safe to call on every list request.
func (c *creds) accountNames(ctx context.Context) map[int64]string {
	if c.names != nil {
		return c.names
	}
	c.names = map[int64]string{}
	cursor := ""
	// A fixed page budget, not "until the map stops growing": a host that
	// ever returned the same cursor twice must not spin this loop.
	for page := 0; page < maxAccountPages; page++ {
		accs, next, err := c.p.host.ListAccounts(ctx, pluginsdk.AccountQuery{
			Type: AccountTypeAPIKey, Cursor: cursor, Limit: accountPageSize, IncludeInactive: true,
		})
		if err != nil {
			// A missing name is cosmetic: the index rows are still returned.
			c.p.log.Warn("volcengine: list accounts failed", "error", err.Error())
			return c.names
		}
		for _, a := range accs {
			c.names[a.ID] = a.Name
		}
		if next == "" || next == cursor || len(accs) == 0 {
			return c.names
		}
		cursor = next
	}
	return c.names
}

// grpcNotFound reports whether err is the host's NOT_FOUND, which it answers
// both for "no such account" and for "an account of another plugin's account
// type" - deliberately, so that this call cannot be used as an account-id
// oracle (CONTRACTS §26.6).
func grpcNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

// errNotInitialised is a call before Init gave the plugin its host.
var errNotInitialised = errors.New("plugin not initialised")

// store returns the plugin's database handle, bound to ctx.
func (p *Plugin) store(ctx context.Context) (*gorm.DB, error) {
	if p.host == nil {
		return nil, errNotInitialised
	}
	return p.host.DB(ctx)
}

// db is store for a route: its error is the HTTP answer.
func (p *Plugin) db(ctx context.Context) (*gorm.DB, *pluginv1.HTTPResponse) {
	d, err := p.store(ctx)
	switch {
	case errors.Is(err, errNotInitialised):
		return nil, unavailable(err.Error())
	case err != nil:
		return nil, unavailable("database unavailable: " + err.Error())
	}
	return d, nil
}

// ---------------------------------------------------------------- accounts

// Values of AccountView.AssetLibrary.
const (
	// AssetLibraryUnknown is the default answer: whether an account has an
	// AK/SK pair can only be told by reading its credentials, and doing that
	// for every row of a list page would write one audit row per account per
	// page load.
	AssetLibraryUnknown = "unknown"
	AssetLibraryOn      = "on"
	AssetLibraryOff     = "off"
)

// AccountView is one row of GET /accounts: an Ark account with the asset
// endpoint it would use.
//
// AssetLibrary is "unknown" unless the caller asked for ?check=1, which
// reads each account's credentials - one audited read per account, which is
// a deliberate, explicit act, not something a list page does behind the
// operator's back. AssetSettingsPresent is the free half of the answer: it
// says the account carries asset settings, NOT that it has keys.
type AccountView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`

	AssetLibrary         string `json:"asset_library"`
	AssetSettingsPresent bool   `json:"asset_settings_present"`
	// AssetBaseURL and AssetRegion are the EFFECTIVE values, with the
	// defaults filled in, so the column shows where calls would actually go.
	AssetBaseURL string `json:"asset_base_url"`
	AssetRegion  string `json:"asset_region"`
}

func (p *Plugin) listAccounts(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	if p.host == nil {
		return unavailable("plugin not initialised"), nil
	}
	page, size := pluginsdk.Pagination(req, 50)
	check := pluginsdk.Query(req, "check") == "1"
	c := p.newCreds()
	// ListAccounts is cursor paged; the console list envelope is page based.
	// Walking to the requested page keeps the two compatible without
	// inventing a cursor field in the response.
	var (
		cursor string
		views  []AccountView
	)
	skip := (page - 1) * size
	for i := 0; i < maxAccountPages; i++ {
		accs, next, err := p.host.ListAccounts(ctx, pluginsdk.AccountQuery{
			Type: AccountTypeAPIKey, Cursor: cursor, Limit: accountPageSize, IncludeInactive: true,
		})
		if err != nil {
			return unavailable("cannot list accounts: " + err.Error()), nil
		}
		for _, a := range accs {
			if skip > 0 {
				skip--
				continue
			}
			if len(views) >= size {
				break
			}
			views = append(views, p.accountView(ctx, c, a, check))
		}
		if next == "" || next == cursor || len(accs) == 0 || len(views) >= size {
			break
		}
		cursor = next
	}
	if views == nil {
		views = []AccountView{}
	}
	// Total is unknown without walking every page; the list envelope
	// tolerates it (the console falls back to client-side paging when
	// total <= the page length).
	return pluginsdk.ListResponse(views, pluginsdk.Page{Page: page, PageSize: size, Total: int64(len(views))}), nil
}

func (p *Plugin) accountView(ctx context.Context, c *creds, a pluginsdk.AccountSummary, check bool) AccountView {
	_, _, base, region, _ := assetFields("", a.SettingsJSON)
	base, _ = effectiveAssetEndpoint(a.Type, "", a.SettingsJSON, base)
	v := AccountView{
		ID: a.ID, Name: a.Name, Status: a.Status, Enabled: a.Enabled,
		AssetLibrary:         AssetLibraryUnknown,
		AssetSettingsPresent: AssetEnabled(a.SettingsJSON),
		AssetBaseURL:         base,
		AssetRegion:          region,
	}
	if v.AssetRegion == "" {
		v.AssetRegion = DefaultAssetRegion
	}
	if !check {
		return v
	}
	cfg, bad := c.config(ctx, a.ID)
	switch {
	case cfg != nil:
		v.AssetLibrary = AssetLibraryOn
		v.AssetBaseURL, v.AssetRegion = cfg.BaseURL, cfg.Region
	case bad != nil && bad.GetStatus() == http.StatusConflict:
		// No AK/SK, or a half pair: either way the asset library is off.
		v.AssetLibrary = AssetLibraryOff
	}
	return v
}

// ---------------------------------------------------------------- group index

func (p *Plugin) listGroupIndex(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	page, size := pluginsdk.Pagination(req, 50)
	res, err := listGroups(db, queryID(req, "account_id"), indexStatusQuery(req), searchQuery(req), size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	items := withAccountNames(res.Items, p.newCreds().accountNames(ctx))
	return pluginsdk.ListResponse(items, pluginsdk.Page{Page: page, PageSize: size, Total: res.Total}), nil
}

// searchQuery reads the asset pages' search box.
//
// The parameter name is this plugin's own choice: the native list pages
// (ui/native/src/IndexPage.vue, SResourcePage's `search`) send the typed text
// under it, so the constant below and the pages have to agree.
//
// That is the second half of the fix. These two routes shipped reading only
// index_status and group_id, while the console drew a search box on every
// table page: the box sent nothing, the route answered the full list, and the
// console rendered it as a result set - worse than an empty table, because it
// looks like an answer. The front end deleted the box; it comes back because
// both halves now exist.
const SearchParam = "q"

func searchQuery(req *pluginv1.HTTPRequest) string {
	return LikeTerm(pluginsdk.Query(req, SearchParam))
}

func indexStatusQuery(req *pluginv1.HTTPRequest) string {
	switch s := strings.TrimSpace(pluginsdk.Query(req, "index_status")); s {
	case IndexOK, IndexMissing:
		return s
	default:
		return ""
	}
}

// createGroupBody is the POST /asset-groups request.
type createGroupBody struct {
	AccountID   int64  `json:"account_id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	GroupType   string `json:"group_type"`
}

func (p *Plugin) createGroup(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in createGroupBody
	if err := pluginsdk.DecodeJSON(req, &in); err != nil {
		return badRequest("", "invalid_json", "body must be a JSON object / 请求体必须是 JSON 对象"), nil
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.AccountID <= 0 {
		return badRequest("account_id", "required", "account_id is required / 请选择账号"), nil
	}
	if in.Name == "" {
		return badRequest("name", "required", "name is required / 请填写素材组名称"), nil
	}
	if errs := in.check(); len(errs) > 0 {
		return pluginsdk.FieldErrorResponse("invalid request / 请求参数不正确", errs), nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	cfg, bad := p.newCreds().config(ctx, in.AccountID)
	if bad != nil {
		return bad, nil
	}
	body := map[string]any{"Name": in.Name}
	putIf(body, "Title", in.Title)
	putIf(body, "Description", in.Description)
	groupType := strings.TrimSpace(in.GroupType)
	if groupType == "" {
		groupType = "AIGC"
	}
	body["GroupType"] = groupType

	result, callErr := p.ark.call(ctx, cfg, ActionCreateAssetGroup, body)
	if callErr != nil {
		return upstreamFail(callErr), nil
	}
	g := groupFromResult(result)
	if g.ID == "" {
		return pluginsdk.ErrorResponse(http.StatusBadGateway, "upstream_error",
			"CreateAssetGroup returned no Id / 上游没有返回素材组 Id"), nil
	}
	// Fill in what the create result omits, so the index row is not emptier
	// than what the operator submitted.
	if g.Name == "" {
		g.Name = in.Name
	}
	if g.Title == "" {
		g.Title = in.Title
	}
	if g.Description == "" {
		g.Description = in.Description
	}
	if g.GroupType == "" {
		g.GroupType = groupType
	}
	id, err := insertGroup(db, in.AccountID, g)
	if err != nil {
		// The group EXISTS upstream but is not indexed. Compensate by
		// deleting it again, so a failed create does not leave an invisible
		// group behind; if the compensation fails too, say so with the
		// upstream id, which is the only way to clean it up by hand.
		if delErr := p.compensate(ctx, cfg, ActionDeleteAssetGroup, g.ID); delErr != nil {
			return pluginsdk.ErrorResponse(http.StatusInternalServerError, "index_write_failed",
				"the asset group was created upstream as "+g.ID+" but could not be indexed ("+err.Error()+
					") and could not be deleted again ("+delErr.Error()+"); delete it in the Volcengine console"), nil
		}
		return nil, err
	}
	return pluginsdk.JSONResponse(http.StatusCreated, map[string]any{"data": map[string]any{
		"id": id, "account_id": in.AccountID, "upstream_id": g.ID, "name": g.Name,
	}}), nil
}

// compensate undoes a created upstream resource after the index write
// failed. Errors are returned, not swallowed: an orphaned upstream resource
// the operator is not told about is worse than a noisy error.
func (p *Plugin) compensate(ctx context.Context, cfg *AssetConfig, action, upstreamID string) error {
	if _, err := p.ark.call(context.WithoutCancel(ctx), cfg, action, map[string]any{"Id": upstreamID}); err != nil {
		if err.NotFound() {
			return nil
		}
		p.log.Error("volcengine: rollback of an unindexed asset library resource failed",
			"action", action, "upstream_id", upstreamID, "error", err.Error())
		return err
	}
	return nil
}

func (p *Plugin) getGroup(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, groupResource, id, func(tx *gorm.DB, row *groupRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		result, callErr := p.ark.call(ctx, cfg, ActionGetAssetGroup, map[string]any{"Id": row.UpstreamID})
		if callErr != nil {
			if callErr.NotFound() {
				// Upstream is the truth: the index is stale, and it says so.
				if err := markMissing[model.AssetGroup](tx, id); err != nil {
					p.log.Warn("volcengine: cannot mark an asset group missing", "id", id, "error", err.Error())
				}
				return pluginsdk.ErrorResponse(http.StatusNotFound, "upstream_not_found",
					"asset group "+row.UpstreamID+" no longer exists upstream; the index row is marked missing"+
						" / 上游已经没有这个素材组，索引行已标记为 missing"), nil
			}
			return upstreamFail(callErr), nil
		}
		g := groupFromResult(result)
		if err := refreshGroup(tx, id, g); err != nil {
			p.log.Warn("volcengine: cannot refresh an asset group row", "id", id, "error", err.Error())
		}
		return pluginsdk.DataResponse(map[string]any{
			"id": id, "account_id": row.AccountID, "upstream_id": row.UpstreamID, "upstream": result,
		}), nil
	})
}

// updateBody is the PATCH body of both resources. A nil pointer means "not
// submitted"; an empty string means "clear it upstream".
type updateBody struct {
	Name        *string `json:"name"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

func (p *Plugin) updateGroup(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	var in updateBody
	if err := pluginsdk.DecodeJSON(req, &in); err != nil {
		return badRequest("", "invalid_json", "body must be a JSON object / 请求体必须是 JSON 对象"), nil
	}
	if in.Name == nil && in.Title == nil && in.Description == nil {
		return badRequest("", "empty", "nothing to update / 没有要修改的字段"), nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, groupResource, id, func(tx *gorm.DB, row *groupRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		body := map[string]any{"Id": row.UpstreamID}
		putPtr(body, "Name", in.Name)
		putPtr(body, "Title", in.Title)
		putPtr(body, "Description", in.Description)
		if _, callErr := p.ark.call(ctx, cfg, ActionUpdateAssetGroup, body); callErr != nil {
			if callErr.NotFound() {
				if err := markMissing[model.AssetGroup](tx, id); err != nil {
					p.log.Warn("volcengine: cannot mark an asset group missing", "id", id, "error", err.Error())
				}
			}
			return upstreamFail(callErr), nil
		}
		if err := applyGroupUpdate(tx, id, in.Name, in.Title, in.Description); err != nil {
			return nil, err
		}
		return pluginsdk.DataResponse(map[string]any{"id": id, "upstream_id": row.UpstreamID}), nil
	})
}

func (p *Plugin) deleteGroup(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, groupResource, id, func(tx *gorm.DB, row *groupRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		// Upstream first, index second. An upstream failure other than "already
		// gone" leaves the index alone: dropping the row would lose the only
		// pointer to a group that still exists.
		if _, callErr := p.ark.call(ctx, cfg, ActionDeleteAssetGroup, map[string]any{"Id": row.UpstreamID}); callErr != nil && !callErr.NotFound() {
			return upstreamFail(callErr), nil
		}
		if err := deleteRow[model.AssetGroup](tx, id); err != nil {
			return nil, err
		}
		return pluginsdk.DataResponse(map[string]any{"id": id, "deleted": true}), nil
	})
}

// ---------------------------------------------------------------- asset index

func (p *Plugin) listAssetIndex(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	page, size := pluginsdk.Pagination(req, 50)
	res, err := listAssets(db, queryID(req, "account_id"), queryID(req, "group_id"), indexStatusQuery(req),
		searchQuery(req), size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	items := withAccountNames(res.Items, p.newCreds().accountNames(ctx))
	return pluginsdk.ListResponse(items, pluginsdk.Page{Page: page, PageSize: size, Total: res.Total}), nil
}

// createAssetBody is the POST /assets request. The account is taken from the
// group, so an asset can never be filed under a group of another account.
type createAssetBody struct {
	GroupID   int64  `json:"group_id"`
	Name      string `json:"name"`
	AssetType string `json:"asset_type"`
	URL       string `json:"url"`
}

func (p *Plugin) createAsset(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	var in createAssetBody
	if err := pluginsdk.DecodeJSON(req, &in); err != nil {
		return badRequest("", "invalid_json", "body must be a JSON object / 请求体必须是 JSON 对象"), nil
	}
	in.Name, in.AssetType, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.AssetType), strings.TrimSpace(in.URL)
	if in.GroupID <= 0 {
		return badRequest("group_id", "required", "group_id is required / 请选择素材组"), nil
	}
	if in.URL == "" {
		return badRequest("url", "required", "url is required / 请填写素材地址"), nil
	}
	if errs := in.check(); len(errs) > 0 {
		return pluginsdk.FieldErrorResponse("invalid request / 请求参数不正确", errs), nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	var createdID string
	var createdWith *AssetConfig
	resp, err := withResource(db, groupResource, in.GroupID, func(tx *gorm.DB, group *groupRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, group.AccountID)
		if bad != nil {
			return bad, nil
		}
		body := map[string]any{"GroupId": group.UpstreamID, "URL": in.URL}
		putIf(body, "Name", in.Name)
		putIf(body, "AssetType", in.AssetType)

		result, callErr := p.ark.call(ctx, cfg, ActionCreateAsset, body)
		if callErr != nil {
			return upstreamFail(callErr), nil
		}
		a := assetFromResult(result)
		if a.ID == "" {
			return pluginsdk.ErrorResponse(http.StatusBadGateway, "upstream_error",
				"CreateAsset returned no Id / 上游没有返回素材 Id"), nil
		}
		createdID, createdWith = a.ID, cfg
		if a.Name == "" {
			a.Name = in.Name
		}
		if a.AssetType == "" {
			a.AssetType = in.AssetType
		}
		if a.URL == "" {
			a.URL = in.URL
		}
		id, err := insertAsset(tx, group.AccountID, group.ID, a)
		if err != nil {
			return nil, err
		}
		return pluginsdk.JSONResponse(http.StatusCreated, map[string]any{"data": map[string]any{
			"id": id, "account_id": group.AccountID, "group_id": group.ID, "upstream_id": a.ID,
			"name": a.Name, "status": a.Status,
		}}), nil
	})
	// The transaction can fail at COMMIT as well as INSERT. Compensate only
	// after it has released the group lock, retaining the existing orphan
	// reporting behavior for either failure.
	if err != nil && createdID != "" {
		if delErr := p.compensate(ctx, createdWith, ActionDeleteAsset, createdID); delErr != nil {
			return pluginsdk.ErrorResponse(http.StatusInternalServerError, "index_write_failed",
				"the asset was created upstream as "+createdID+" but could not be indexed ("+err.Error()+
					") and could not be deleted again ("+delErr.Error()+"); delete it in the Volcengine console"), nil
		}
	}
	return resp, err
}

func (p *Plugin) getAsset(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, assetResource, id, func(tx *gorm.DB, row *assetRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		result, callErr := p.ark.call(ctx, cfg, ActionGetAsset, map[string]any{"Id": row.UpstreamID})
		if callErr != nil {
			if callErr.NotFound() {
				if err := markMissing[model.Asset](tx, id); err != nil {
					p.log.Warn("volcengine: cannot mark an asset missing", "id", id, "error", err.Error())
				}
				return pluginsdk.ErrorResponse(http.StatusNotFound, "upstream_not_found",
					"asset "+row.UpstreamID+" no longer exists upstream; the index row is marked missing"+
						" / 上游已经没有这个素材，索引行已标记为 missing"), nil
			}
			return upstreamFail(callErr), nil
		}
		if err := refreshAsset(tx, id, assetFromResult(result)); err != nil {
			p.log.Warn("volcengine: cannot refresh an asset row", "id", id, "error", err.Error())
		}
		return pluginsdk.DataResponse(map[string]any{
			"id": id, "account_id": row.AccountID, "group_id": row.GroupID,
			"upstream_id": row.UpstreamID, "upstream": result,
		}), nil
	})
}

func (p *Plugin) updateAsset(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	var in updateBody
	if err := pluginsdk.DecodeJSON(req, &in); err != nil {
		return badRequest("", "invalid_json", "body must be a JSON object / 请求体必须是 JSON 对象"), nil
	}
	if in.Name == nil && in.Description == nil {
		return badRequest("", "empty", "nothing to update / 没有要修改的字段"), nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, assetResource, id, func(tx *gorm.DB, row *assetRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		body := map[string]any{"Id": row.UpstreamID}
		putPtr(body, "Name", in.Name)
		putPtr(body, "Description", in.Description)
		if _, callErr := p.ark.call(ctx, cfg, ActionUpdateAsset, body); callErr != nil {
			if callErr.NotFound() {
				if err := markMissing[model.Asset](tx, id); err != nil {
					p.log.Warn("volcengine: cannot mark an asset missing", "id", id, "error", err.Error())
				}
			}
			return upstreamFail(callErr), nil
		}
		if err := applyAssetUpdate(tx, id, in.Name); err != nil {
			return nil, err
		}
		return pluginsdk.DataResponse(map[string]any{"id": id, "upstream_id": row.UpstreamID}), nil
	})
}

func (p *Plugin) deleteAsset(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	id, bad := pathID(req, "id")
	if bad != nil {
		return bad, nil
	}
	db, bad := p.db(ctx)
	if bad != nil {
		return bad, nil
	}
	return withResource(db, assetResource, id, func(tx *gorm.DB, row *assetRow) (*pluginv1.HTTPResponse, error) {
		cfg, bad := p.newCreds().config(ctx, row.AccountID)
		if bad != nil {
			return bad, nil
		}
		if _, callErr := p.ark.call(ctx, cfg, ActionDeleteAsset, map[string]any{"Id": row.UpstreamID}); callErr != nil && !callErr.NotFound() {
			return upstreamFail(callErr), nil
		}
		if err := deleteRow[model.Asset](tx, id); err != nil {
			return nil, err
		}
		return pluginsdk.DataResponse(map[string]any{"id": id, "deleted": true}), nil
	})
}

// ---------------------------------------------------------------- live upstream listings

func (p *Plugin) listUpstreamGroups(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	accountID, bad := pathID(req, "account_id")
	if bad != nil {
		return bad, nil
	}
	cfg, bad := p.newCreds().config(ctx, accountID)
	if bad != nil {
		return bad, nil
	}
	page, size := pluginsdk.Pagination(req, 50)
	result, callErr := p.ark.call(ctx, cfg, ActionListAssetGroups, map[string]any{
		"PageNumber": page, "PageSize": size,
	})
	if callErr != nil {
		return upstreamFail(callErr), nil
	}
	items := resultItems(result, "AssetGroups", "Groups")
	return pluginsdk.ListResponse(upstreamGroupViews(items),
		pluginsdk.Page{Page: page, PageSize: size, Total: totalOf(result, len(items))}), nil
}

func (p *Plugin) listUpstreamAssets(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	accountID, bad := pathID(req, "account_id")
	if bad != nil {
		return bad, nil
	}
	cfg, bad := p.newCreds().config(ctx, accountID)
	if bad != nil {
		return bad, nil
	}
	page, size := pluginsdk.Pagination(req, 50)
	body := map[string]any{"PageNumber": page, "PageSize": size}
	// group_id here is the UPSTREAM group id, because this route deliberately
	// bypasses the index: it is the view of what upstream really holds.
	if g := strings.TrimSpace(pluginsdk.Query(req, "group_id")); g != "" {
		body["GroupId"] = g
	}
	result, callErr := p.ark.call(ctx, cfg, ActionListAssets, body)
	if callErr != nil {
		return upstreamFail(callErr), nil
	}
	items := resultItems(result, "Assets")
	views := make([]map[string]any, 0, len(items))
	for _, it := range items {
		a := assetFromResult(it)
		views = append(views, map[string]any{
			"upstream_id": a.ID, "name": a.Name, "asset_type": a.AssetType,
			"url": a.URL, "status": a.Status, "group_upstream_id": a.GroupID,
		})
	}
	return pluginsdk.ListResponse(views, pluginsdk.Page{Page: page, PageSize: size, Total: totalOf(result, len(items))}), nil
}

func upstreamGroupViews(items []map[string]any) []map[string]any {
	views := make([]map[string]any, 0, len(items))
	for _, it := range items {
		g := groupFromResult(it)
		views = append(views, map[string]any{
			"upstream_id": g.ID, "name": g.Name, "title": g.Title,
			"description": g.Description, "group_type": g.GroupType,
		})
	}
	return views
}

// totalOf reads TotalCount out of a list result, falling back to the number
// of returned items when upstream reports none.
func totalOf(result map[string]any, got int) int64 {
	for _, k := range []string{"TotalCount", "Total"} {
		switch v := result[k].(type) {
		case float64:
			return int64(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return n
			}
		}
	}
	return int64(got)
}

func putIf(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func putPtr(m map[string]any, key string, value *string) {
	if value != nil {
		m[key] = *value
	}
}
