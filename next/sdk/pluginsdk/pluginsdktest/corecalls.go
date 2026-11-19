package pluginsdktest

import (
	"context"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

func (f *FakeHost) ClearStickyBindings(ctx context.Context, in *pluginv1.ClearStickyBindingsRequest) (*pluginv1.ClearStickyBindingsResponse, error) {
	if f.ClearStickyBindingsFunc == nil {
		return nil, status.Error(codes.Unimplemented, "no ClearStickyBindings handler")
	}
	return f.ClearStickyBindingsFunc(ctx, in)
}

func (f *FakeHost) ListNotificationChannels(ctx context.Context, in *pluginv1.ListNotificationChannelsRequest) (*pluginv1.ListNotificationChannelsResponse, error) {
	if f.ListNotificationChannelsFunc == nil {
		return nil, status.Error(codes.Unimplemented, "no ListNotificationChannels handler")
	}
	return f.ListNotificationChannelsFunc(ctx, in)
}

func (f *FakeHost) Notify(ctx context.Context, in *pluginv1.NotifyRequest) (*pluginv1.NotifyResponse, error) {
	if f.NotifyFunc == nil {
		return nil, status.Error(codes.Unimplemented, "no Notify handler")
	}
	return f.NotifyFunc(ctx, in)
}

// Serve marks userID as the caller of a request in flight until done is
// called, as the host does around HTTPService.HandleHTTP. AccountScopeCheck
// only answers about such a user.
func (f *FakeHost) Serve(userID int64) (done func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serving == nil {
		f.serving = map[int64]int{}
	}
	f.serving[userID]++
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.serving[userID]--
	}
}

// AccountScopeCheck answers with FakeHost.AccountScope after the checks of
// the host: the arguments, and that the user is the caller of a request in
// flight (Serve).
func (f *FakeHost) AccountScopeCheck(_ context.Context, in *pluginv1.AccountScopeCheckRequest) (*pluginv1.AccountScopeCheckResponse, error) {
	ids := in.GetAccountIds()
	if in.GetUserId() <= 0 || len(ids) > manifest.MaxAccountBatch || slices.ContainsFunc(ids, func(id int64) bool { return id <= 0 }) ||
		!slices.Contains([]string{manifest.AccountsRead, manifest.AccountsUpdate, manifest.AccountsTest}, in.GetAction()) {
		return nil, status.Errorf(codes.InvalidArgument, "user_id, a known action and at most %d positive account_ids are required", manifest.MaxAccountBatch)
	}
	f.mu.Lock()
	fn, serving := f.AccountScope, f.serving[in.GetUserId()] > 0
	f.mu.Unlock()
	if !serving {
		return nil, status.Error(codes.PermissionDenied, "user_id is not the caller of a request in flight")
	}
	if fn == nil {
		return &pluginv1.AccountScopeCheckResponse{All: true}, nil
	}
	all, allowed, err := fn(in.GetUserId(), in.GetAction(), ids)
	if err != nil {
		return nil, err
	}
	return &pluginv1.AccountScopeCheckResponse{All: all, AllowedIds: allowed}, nil
}

// GetAccount answers from FakeHost.Accounts and records the call in
// AccountReads; an id not there is NOT_FOUND, as the host answers for an
// account of another plugin.
func (f *FakeHost) GetAccount(_ context.Context, in *pluginv1.GetAccountRequest) (*pluginv1.GetAccountResponse, error) {
	if in.GetAccountId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AccountReads = append(f.AccountReads, in.GetAccountId())
	acc, ok := f.Accounts[in.GetAccountId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return &pluginv1.GetAccountResponse{Account: proto.Clone(acc).(*pluginv1.Account)}, nil
}

func (f *FakeHost) GetUsers(_ context.Context, in *pluginv1.GetUsersRequest) (*pluginv1.GetUsersResponse, error) {
	if len(in.GetUserIds()) > protocol.GetUsersMaxIDs {
		return nil, status.Error(codes.InvalidArgument, "at most 500 user_ids")
	}
	out := &pluginv1.GetUsersResponse{}
	seen := map[int64]bool{}
	for _, id := range in.GetUserIds() {
		if u, ok := f.Users[id]; ok && !seen[id] {
			seen[id] = true
			out.Users = append(out.Users, u)
		}
	}
	return out, nil
}
