package grpcruntime

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Plugin cluster locks (CONTRACTS §27): HostService.LockAcquire / LockRenew /
// LockRelease on Options.Locker. The host keeps no state per lock; the
// plugin holds the token it chose and the lock lapses after its ttl when the
// plugin goes away. The name, ttl and token rules live in sdk/protocol so the
// SDK checks exactly what the host enforces.

// PermLock is the host permission required by the lock calls.
const PermLock = "lock"

// LockKey is the core lock key of a plugin's lock name; the locker stores it
// as lock:plugin:{pluginKey}:{name}. A plugin id (manifest.PluginID) holds
// only [a-z0-9_], so it never contains ':' and the first ':' after
// "plugin:" always ends the id: two plugins cannot reach each other's locks
// whatever names they pick.
func LockKey(pluginKey, name string) string { return "plugin:" + pluginKey + ":" + name }

// lockReq validates a lock call and returns the core key, the ttl and the
// locker. withTTL is false for LockRelease, which carries no ttl (a sentinel
// ttl_ms value would let a plugin skip the check by sending it).
func (h *hostServer) lockReq(ctx context.Context, name, token string, withTTL bool, ttlMs int64) (string, time.Duration, core.TokenLocker, error) {
	if err := h.require(ctx, PermLock); err != nil {
		return "", 0, nil, err
	}
	if !protocol.ValidLockName(name) {
		return "", 0, nil, status.Error(codes.InvalidArgument, "lock name must match "+protocol.LockNamePattern)
	}
	var ttl time.Duration
	if withTTL {
		var ok bool
		if ttl, ok = protocol.LockTTLFromMs(ttlMs); !ok {
			return "", 0, nil, status.Errorf(codes.InvalidArgument, "ttl_ms must be %d..%d",
				protocol.LockMinTTL.Milliseconds(), protocol.LockMaxTTL.Milliseconds())
		}
	}
	if !protocol.ValidLockToken(token) {
		return "", 0, nil, status.Error(codes.InvalidArgument, "lock token must match "+protocol.LockTokenPattern)
	}
	locker := h.i.rt.o.Locker
	if locker == nil {
		return "", 0, nil, status.Error(codes.Unavailable, "locks unavailable")
	}
	return LockKey(h.key(), name), ttl, locker, nil
}

// validMs is the validity left on lk, in milliseconds, never negative.
func validMs(lk core.Lock) int64 {
	return max(time.Until(lk.Until()).Milliseconds(), 0)
}

func (h *hostServer) lockUnavailable(call, name string, err error) error {
	h.i.log.Warn("plugin lock call failed", "call", call, "lock", name, "err", err)
	return status.Error(codes.Unavailable, "locks unavailable")
}

// LockAcquire implements HostService.LockAcquire: one attempt, with the
// plugin's token as the lock's value.
func (h *hostServer) LockAcquire(ctx context.Context, in *pluginv1.LockAcquireRequest) (*pluginv1.LockAcquireResponse, error) {
	key, ttl, locker, err := h.lockReq(ctx, in.GetName(), in.GetToken(), true, in.GetTtlMs())
	if err != nil {
		return nil, err
	}
	lk, ok, err := locker.TryLockToken(ctx, key, in.GetToken(), ttl)
	if err != nil {
		// Possibly taken after all; the plugin can release it with its token.
		return nil, h.lockUnavailable("acquire", in.GetName(), err)
	}
	if !ok {
		return &pluginv1.LockAcquireResponse{}, nil
	}
	return &pluginv1.LockAcquireResponse{Acquired: true, ValidMs: validMs(lk)}, nil
}

// LockRenew implements HostService.LockRenew.
func (h *hostServer) LockRenew(ctx context.Context, in *pluginv1.LockRenewRequest) (*pluginv1.LockRenewResponse, error) {
	key, ttl, locker, err := h.lockReq(ctx, in.GetName(), in.GetToken(), true, in.GetTtlMs())
	if err != nil {
		return nil, err
	}
	lk := locker.Resume(key, in.GetToken(), ttl)
	ok, err := lk.Extend(ctx)
	if err != nil {
		return nil, h.lockUnavailable("renew", in.GetName(), err)
	}
	if !ok {
		// Definitely lost: expired, or someone else's now. Not an error.
		return &pluginv1.LockRenewResponse{}, nil
	}
	return &pluginv1.LockRenewResponse{Held: true, ValidMs: validMs(lk)}, nil
}

// LockRelease implements HostService.LockRelease. It uses ReleaseToken rather
// than Resume(...).Release(): Lock.Release takes no context and swallows its
// errors, so the call could neither honour the request's deadline nor tell
// the plugin that Redis did not answer.
func (h *hostServer) LockRelease(ctx context.Context, in *pluginv1.LockReleaseRequest) (*pluginv1.LockReleaseResponse, error) {
	key, _, locker, err := h.lockReq(ctx, in.GetName(), in.GetToken(), false, 0)
	if err != nil {
		return nil, err
	}
	if err := locker.ReleaseToken(ctx, key, in.GetToken()); err != nil {
		return nil, h.lockUnavailable("release", in.GetName(), err)
	}
	return &pluginv1.LockReleaseResponse{}, nil
}
