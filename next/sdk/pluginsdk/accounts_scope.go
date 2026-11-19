package pluginsdk

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Account scope (HostService.AccountScopeCheck; CONTRACTS §21.0). A console
// user reaches every account with account:<action>, or the accounts of a
// narrower scope of it (own, groups, ids: account:<action>:own,
// account:<action>:group/12 …). The core decides
// which: a plugin route that acts on accounts asks it instead of trusting the
// ids in the request.
//
//	// one or more named accounts
//	if resp := pluginsdk.RequireAccounts(ctx, host, req, "update", id); resp != nil {
//		return resp, nil
//	}
//	// a list: keep what the caller may see
//	scope, err := pluginsdk.ResolveAccountScope(ctx, host, req, "read", ids...)
//	if err != nil {
//		return pluginsdk.Unavailable("cannot check account access"), nil
//	}
//	visible := scope.Filter(ids)
//
// The actions are manifest.AccountsRead, AccountsUpdate and AccountsTest.
//
// A route that declares `accounts` in the manifest is already checked by the
// core before it reaches the plugin; these are for ids the manifest cannot
// name (nested in the body, read from the plugin's own tables).
//
// The host answers any plugin (no host permission), and only about the
// caller of a request the plugin is serving: call these inside the HTTP handler, with its
// request, not from a job or after the handler returned. Otherwise the call
// is codes.PermissionDenied, which RequireAccounts turns into a 403 like a
// user holding account:<action> at no scope.

// AccountScope is what a console user may do to a set of accounts.
type AccountScope struct {
	// All: the user reaches every account.
	All bool
	// allowed are, without All, the asked accounts within the user's scope
	// (own, groups, ids; CONTRACTS §21.0).
	allowed map[int64]bool
}

// Allows reports whether the user may act on account id. Without All it is
// only true for ids that were asked about.
func (s AccountScope) Allows(id int64) bool { return s.All || s.allowed[id] }

// Filter returns the ids the user may act on, in order.
func (s AccountScope) Filter(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if s.Allows(id) {
			out = append(out, id)
		}
	}
	return out
}

// ResolveAccountScope asks the host what req's caller may do (action "read",
// "update" or "test") to the accounts ids. The error is ErrNoCaller for a
// request without a user, codes.PermissionDenied when the user holds
// account:<action> at no scope (or req is not a request in flight), or the
// failed host call.
func ResolveAccountScope(ctx context.Context, h Host, req *pluginv1.HTTPRequest, action string, ids ...int64) (AccountScope, error) {
	uid := req.GetCaller().GetUserId()
	if uid <= 0 {
		return AccountScope{}, ErrNoCaller
	}
	if h == nil || h.Client() == nil {
		return AccountScope{}, ErrHostNotReady
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	batches := slices.Collect(slices.Chunk(slices.Compact(ids), manifest.MaxAccountBatch))
	if len(batches) == 0 {
		batches = [][]int64{nil} // no ids: still learn whether the user reaches all
	}
	scope := AccountScope{allowed: map[int64]bool{}}
	for _, batch := range batches {
		resp, err := h.Client().AccountScopeCheck(ctx, &pluginv1.AccountScopeCheckRequest{UserId: uid, Action: action, AccountIds: batch})
		if err != nil {
			return AccountScope{}, err
		}
		if resp.GetAll() {
			return AccountScope{All: true}, nil
		}
		for _, id := range resp.GetAllowedIds() {
			scope.allowed[id] = true
		}
	}
	return scope, nil
}

// RequireAccounts checks that req's caller may do action to every account in
// ids and returns nil when so. Otherwise it returns the answer to send, the
// same the core gives on its own account routes: 401 without a user, 403
// without any scope of account:<action>, and 404 for an account outside the
// user's scope (as if it did not exist). A caller with a narrower scope must name
// at least one: with no ids only a user reaching every account passes (403).
// A host that cannot answer is a 503. An unknown action or an id that is not
// positive is the plugin's mistake and a 400, whatever the user holds.
func RequireAccounts(ctx context.Context, h Host, req *pluginv1.HTTPRequest, action string, ids ...int64) *pluginv1.HTTPResponse {
	scope, err := ResolveAccountScope(ctx, h, req, action, ids...)
	switch {
	case errors.Is(err, ErrNoCaller):
		return Unauthenticated()
	case status.Code(err) == codes.PermissionDenied:
		return Forbidden()
	case status.Code(err) == codes.InvalidArgument:
		return ErrorResponse(http.StatusBadRequest, "invalid_argument", "invalid account action or account id / 账号操作或账号 id 不合法")
	case err != nil:
		return Unavailable("cannot check account access / 无法校验账号权限")
	case scope.All:
		return nil
	case len(ids) == 0:
		return Forbidden()
	case slices.ContainsFunc(ids, func(id int64) bool { return !scope.Allows(id) }):
		return ErrorResponse(http.StatusNotFound, "not_found", "account not found / 账号不存在")
	}
	return nil
}
