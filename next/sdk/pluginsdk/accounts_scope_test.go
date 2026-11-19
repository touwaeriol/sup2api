package pluginsdk_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

func userReq(uid int64) *pluginv1.HTTPRequest {
	return &pluginv1.HTTPRequest{Caller: &pluginv1.Caller{UserId: uid}}
}

// scopeHost starts a plugin against a fake host where user 1 reaches every
// account, user 2 only the even ones they "created", and user 3 none. The
// host is serving a request of each of them and of user 9, whose answer
// fails; of nobody else.
func scopeHost(t *testing.T) (pluginsdk.Host, *[][]int64) {
	t.Helper()
	var calls [][]int64
	fh := pluginsdktest.NewFakeHost()
	fh.AccountScope = func(uid int64, action string, ids []int64) (bool, []int64, error) {
		calls = append(calls, ids)
		switch {
		case uid == 9:
			return false, nil, status.Error(codes.Unavailable, "database down")
		case uid == 1:
			return true, nil, nil
		case uid == 2:
			return false, slices.DeleteFunc(slices.Clone(ids), func(id int64) bool { return id%2 != 0 }), nil
		}
		return false, nil, status.Error(codes.PermissionDenied, "permission_denied: account:"+action)
	}
	for _, uid := range []int64{1, 2, 3, 9} {
		t.Cleanup(fh.Serve(uid))
	}
	p := &locker{}
	pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})
	return p.host, &calls
}

func TestResolveAccountScope(t *testing.T) {
	ctx := context.Background()
	h, calls := scopeHost(t)

	all, err := pluginsdk.ResolveAccountScope(ctx, h, userReq(1), "read", 5, 6)
	require.NoError(t, err)
	require.True(t, all.All && all.Allows(5) && all.Allows(12345))
	require.Equal(t, []int64{9, 5, 9}, all.Filter([]int64{9, 5, 9}))

	own, err := pluginsdk.ResolveAccountScope(ctx, h, userReq(2), "update", 4, 3, 2, 4)
	require.NoError(t, err)
	require.False(t, own.All)
	require.True(t, own.Allows(4) && own.Allows(2))
	require.False(t, own.Allows(3) || own.Allows(8), "an id that was not asked about is not allowed")
	require.Equal(t, []int64{4, 2, 4}, own.Filter([]int64{4, 3, 2, 4, 8}))

	// No ids: one call that still tells whether the user reaches all.
	*calls = nil
	none, err := pluginsdk.ResolveAccountScope(ctx, h, userReq(2), "read")
	require.NoError(t, err)
	require.False(t, none.All || none.Allows(2))
	require.Len(t, *calls, 1)

	// More ids than one call takes are asked in batches, each id once.
	*calls = nil
	var many []int64
	for id := int64(1); id <= manifest.MaxAccountBatch+10; id++ {
		many = append(many, id, id)
	}
	big, err := pluginsdk.ResolveAccountScope(ctx, h, userReq(2), "read", many...)
	require.NoError(t, err)
	require.Len(t, *calls, 2)
	require.Len(t, (*calls)[0], manifest.MaxAccountBatch)
	require.Len(t, (*calls)[1], 10)
	require.True(t, big.Allows(manifest.MaxAccountBatch+10) && !big.Allows(manifest.MaxAccountBatch+9))

	_, err = pluginsdk.ResolveAccountScope(ctx, h, userReq(3), "read", 1)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	for _, req := range []*pluginv1.HTTPRequest{nil, {}, userReq(0)} {
		_, err = pluginsdk.ResolveAccountScope(ctx, h, req, "read", 1)
		require.ErrorIs(t, err, pluginsdk.ErrNoCaller)
	}
	_, err = pluginsdk.ResolveAccountScope(ctx, nil, userReq(1), "read", 1)
	require.True(t, errors.Is(err, pluginsdk.ErrHostNotReady))
}

func TestRequireAccounts(t *testing.T) {
	ctx := context.Background()
	h, calls := scopeHost(t)
	cases := []struct {
		name   string
		host   pluginsdk.Host
		uid    int64
		action string
		ids    []int64
		status int32 // 0: nil, every account may be acted on
		code   string
	}{
		{name: "all scope", host: h, uid: 1, action: "update", ids: []int64{1, 2, 3}},
		{name: "all scope without ids", host: h, uid: 1, action: "read"},
		{name: "own scope, every id owned", host: h, uid: 2, action: "update", ids: []int64{2, 4, 2}},
		{name: "own scope, one id not owned", host: h, uid: 2, action: "update", ids: []int64{2, 3}, status: 404, code: "not_found"},
		{name: "own scope without ids", host: h, uid: 2, action: "read", status: 403, code: "permission_denied"},
		{name: "neither key", host: h, uid: 3, action: "read", ids: []int64{2}, status: 403, code: "permission_denied"},
		{name: "a user the host is not serving", host: h, uid: 5, action: "read", ids: []int64{2}, status: 403, code: "permission_denied"},
		{name: "no caller", host: h, uid: 0, action: "read", ids: []int64{2}, status: 401, code: "unauthenticated"},
		{name: "bad id", host: h, uid: 2, action: "read", ids: []int64{-1}, status: 400, code: "invalid_argument"},
		{name: "unknown action", host: h, uid: 1, action: "delete", ids: []int64{2}, status: 400, code: "invalid_argument"},
		{name: "host failure", host: h, uid: 9, action: "read", ids: []int64{2}, status: 503, code: "unavailable"},
		{name: "no host", host: nil, uid: 2, action: "read", ids: []int64{2}, status: 503, code: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			*calls = nil
			resp := pluginsdk.RequireAccounts(ctx, tc.host, userReq(tc.uid), tc.action, tc.ids...)
			if tc.status == 0 {
				require.Nil(t, resp)
				return
			}
			require.NotNil(t, resp)
			require.Equal(t, tc.status, resp.GetStatus(), string(resp.GetBody()))
			require.Equal(t, tc.code, errorCodeOf(t, resp))
			if tc.status == 401 || tc.uid == 5 {
				require.Empty(t, *calls, "the scope of nobody, or of a user without a request in flight, is not computed")
			}
		})
	}
	// 403 does not say which key is missing, 404 not which account.
	denied := pluginsdk.RequireAccounts(ctx, h, userReq(3), "update", 2)
	require.Equal(t, string(pluginsdk.Forbidden().GetBody()), string(denied.GetBody()))
	a := pluginsdk.RequireAccounts(ctx, h, userReq(2), "update", 3)
	b := pluginsdk.RequireAccounts(ctx, h, userReq(2), "update", 2, 5, 7)
	require.Equal(t, string(a.GetBody()), string(b.GetBody()))
}

func TestRequirePermission(t *testing.T) {
	ctx := context.Background()
	fh := pluginsdktest.NewFakeHost()
	var asked []string
	fh.Authz = func(uid int64, perm string) bool {
		asked = append(asked, perm)
		return uid == 1 && perm == "rules:manage"
	}
	p := &locker{}
	pluginsdktest.Start(t, p, pluginsdktest.Options{Host: fh, Manifest: []byte(lockManifest)})
	ran := 0
	next := func(context.Context, *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
		ran++
		return pluginsdk.JSONResponse(200, map[string]bool{"ok": true}), nil
	}
	guarded := pluginsdk.RequirePermission(p.host, "rules:manage", next)

	resp, err := guarded(ctx, userReq(1))
	require.NoError(t, err)
	require.Equal(t, int32(200), resp.GetStatus())
	require.Equal(t, 1, ran)

	for name, tc := range map[string]struct {
		h      pluginsdk.Host
		perm   string
		req    *pluginv1.HTTPRequest
		status int32
		code   string
	}{
		"another user":       {p.host, "rules:manage", userReq(2), 403, "permission_denied"},
		"another permission": {p.host, "rules:read", userReq(1), 403, "permission_denied"},
		"no caller":          {p.host, "rules:manage", userReq(0), 401, "unauthenticated"},
		"nil request":        {p.host, "rules:manage", nil, 401, "unauthenticated"},
		"no host":            {nil, "rules:manage", userReq(1), 503, "unavailable"},
	} {
		asked = nil
		resp, err := pluginsdk.RequirePermission(tc.h, tc.perm, next)(ctx, tc.req)
		require.NoError(t, err, name)
		require.Equal(t, tc.status, resp.GetStatus(), name)
		require.Equal(t, tc.code, errorCodeOf(t, resp), name)
		if tc.status == 401 {
			require.Empty(t, asked, name)
		}
	}
	require.Equal(t, 1, ran, "a refused request never reaches the handler")
	// Every refusal for a missing permission is the same answer.
	x, _ := guarded(ctx, userReq(2))
	y, _ := pluginsdk.RequirePermission(p.host, "other:thing", next)(ctx, userReq(1))
	require.Equal(t, string(x.GetBody()), string(y.GetBody()))
}

func errorCodeOf(t *testing.T, resp *pluginv1.HTTPResponse) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, pluginsdk.DecodeJSON(&pluginv1.HTTPRequest{Body: resp.GetBody()}, &out))
	return out.Error.Code
}
