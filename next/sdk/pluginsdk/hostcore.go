package pluginsdk

import (
	"context"
	"slices"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// ClearStickyBindings deletes a share of the sticky session bindings that
// point at an account and could serve (group, model). It needs the
// "sticky.clear" grant (CONTRACTS §74.2).
func ClearStickyBindings(ctx context.Context, h Host, in *pluginv1.ClearStickyBindingsRequest) (*pluginv1.ClearStickyBindingsResponse, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	return h.Client().ClearStickyBindings(ctx, in)
}

// GetEnvironment resolves the environment variables of a group and
// optionally an account (CONTRACTS §74.3). Gateway requests already carry
// theirs in RequestMeta.environment; this is for background work.
func GetEnvironment(ctx context.Context, h Host, groupID, accountID int64) (map[string]int64, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	resp, err := h.Client().GetEnvironment(ctx, &pluginv1.GetEnvironmentRequest{GroupId: groupID, AccountId: accountID})
	if err != nil {
		return nil, err
	}
	return resp.GetEnvironment(), nil
}

// ListNotificationChannels lists the notification channels of the enabled
// plugins; it needs the "notify.send" grant (CONTRACTS §74.4).
func ListNotificationChannels(ctx context.Context, h Host) ([]*pluginv1.NotificationChannel, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	resp, err := h.Client().ListNotificationChannels(ctx, &pluginv1.ListNotificationChannelsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetChannels(), nil
}

// Notify queues a notification on a channel by its id; the core delivers it
// through the plugin declaring the channel. It needs the "notify.send" grant.
func Notify(ctx context.Context, h Host, in *pluginv1.NotifyRequest) (*pluginv1.NotifyResponse, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	return h.Client().Notify(ctx, in)
}

// GetUsers returns the users among ids that exist, once each, in the order
// of ids, asking the host protocol.GetUsersMaxIDs ids at a time. It needs
// the "users.read" grant (CONTRACTS §75.3).
func GetUsers(ctx context.Context, h Host, ids []int64) ([]*pluginv1.UserInfo, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	out := make([]*pluginv1.UserInfo, 0, len(unique))
	for batch := range slices.Chunk(unique, protocol.GetUsersMaxIDs) {
		resp, err := h.Client().GetUsers(ctx, &pluginv1.GetUsersRequest{UserIds: batch})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.GetUsers()...)
	}
	return out, nil
}

// requireHost fails a host call made before the plugin has a host.
func requireHost(h Host) error {
	if h == nil || h.Client() == nil {
		return ErrHostNotReady
	}
	return nil
}

// SendAccountRequest has the host send one HTTP request for one of this
// plugin's own accounts through that account's proxy (no grant: the
// account must be of an account type this plugin declares; the URL's host
// must be one of the manifest's externalServices or a subdomain of one, or the host
// of one of the account's own upstream addresses, as for a relay:
// manifest.Manifest.UpstreamHostAllowed, CONTRACTS §91, §112.17). Neither
// the body nor the answer is bounded. A transport failure comes back in the
// answer's transport_error with status 0, not as an error.
func SendAccountRequest(ctx context.Context, h Host, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	if err := requireHost(h); err != nil {
		return nil, err
	}
	return h.Client().SendAccountRequest(ctx, in)
}
