package codex

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// Plugin KV and cluster lock helpers: the authorization sessions, the reset
// count cache (resetcache.go) and the poll and reset locks.

// lockReleaseTimeout bounds giving a lock back.
const lockReleaseTimeout = 5 * time.Second

// kv is the plugin KV of the host, ErrHostNotReady before Init.
func (p *Plugin) kv() (pluginsdk.KV, error) {
	if p.host == nil {
		return nil, pluginsdk.ErrHostNotReady
	}
	return p.host.KV(), nil
}

// Authorization sessions are caller sessions (pluginsdk.SaveCallerSession):
// only the console user who started one can continue it, and to anyone else
// it looks expired.

// loadSession reads session id of req's caller.
func (p *Plugin) loadSession(ctx context.Context, ns, id string, req *pluginv1.HTTPRequest, v any) (bool, error) {
	kv, err := p.kv()
	if err != nil {
		return false, err
	}
	return pluginsdk.LoadCallerSession(ctx, kv, ns, id, req, v)
}

// newSession stores v as a new session of req's caller and returns its id.
func (p *Plugin) newSession(ctx context.Context, ns string, req *pluginv1.HTTPRequest, v any, ttl time.Duration) (string, error) {
	kv, err := p.kv()
	if err != nil {
		return "", err
	}
	return pluginsdk.SaveCallerSession(ctx, kv, ns, req, v, ttl)
}

// sessionNotStored answers a failed newSession: 401 without a caller, else
// the store is unavailable.
func sessionNotStored(err error) *pluginv1.HTTPResponse {
	if errors.Is(err, pluginsdk.ErrNoCaller) {
		return pluginsdk.Unauthenticated()
	}
	return pluginsdk.Unavailable("store the session / 保存会话失败: " + err.Error())
}

// saveSession rewrites the session loadSession returned for req.
func (p *Plugin) saveSession(ctx context.Context, ns, id string, req *pluginv1.HTTPRequest, v any, ttl time.Duration) error {
	kv, err := p.kv()
	if err != nil {
		return err
	}
	return pluginsdk.UpdateCallerSession(ctx, kv, ns, id, req, v, ttl)
}

// saveCached keeps plugin-wide JSON in the KV (the reset count cache):
// unlike a session it belongs to no caller.
func (p *Plugin) saveCached(ctx context.Context, ns, id string, v any, ttl time.Duration) error {
	kv, err := p.kv()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	return kv.Set(ctx, ns, id, b, ttl)
}

// deleteSession deletes an authorization session. A failure is only
// logged: the session expires with its ttl, and every code is single-use on
// OpenAI's side.
func (p *Plugin) deleteSession(ctx context.Context, ns, id string) { p.deleteEntry(ctx, ns, id) }

// deleteCached deletes JSON saveCached kept. A failure is only logged: the
// entry expires with its ttl, and a stale reset count is only shown.
func (p *Plugin) deleteCached(ctx context.Context, ns, id string) { p.deleteEntry(ctx, ns, id) }

// deleteEntry deletes a KV entry, logging a failure.
func (p *Plugin) deleteEntry(ctx context.Context, ns, id string) {
	kv, err := p.kv()
	if err == nil {
		err = kv.Delete(ctx, ns, id)
	}
	if err != nil {
		p.log.Warn("codex: delete a KV entry", "namespace", ns, "error", err.Error())
	}
}

// releaseLock gives a lock back, even when ctx has ended; a failure is
// logged and the lock lapses after its ttl.
func (p *Plugin) releaseLock(ctx context.Context, l *pluginsdk.Lock) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	if err := l.Release(rctx); err != nil {
		p.log.Warn("codex: release a lock", "lock", l.Name(), "error", err.Error())
	}
}
