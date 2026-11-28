package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Renewal after an upstream 401 (CONTRACTS §141.2), as the Codex CLI's
// unauthorized recovery does: when the upstream refuses an account's
// credentials and its type declares refresh, the account module renews them
// at once (core.CredentialRecovery) and the attempt runs again on the same
// account with the new ones, before the 401 is classified. It is not
// another attempt: no failover, no cooldown, no attempt counted. Each
// account is recovered once per request; a failed renewal, or a second 401,
// is classified as before.
//
// Only attempts run through recovering recover: an HTTP attempt
// (attemptRecovering) and a WebSocket handshake (wsSession.connect).

// unauthorizedRecovery is a request's renewals after a 401.
type unauthorizedRecovery struct {
	// armed is set while recovering runs an attempt.
	armed bool
	// tried holds the accounts the request has recovered, or tried to.
	tried map[int64]bool
}

// attemptRecovering is attempt n on ref, run through recovering.
func (c *call) attemptRecovering(ctx context.Context, ref *core.AccountRef, n int) attemptResult {
	return c.recovering(ctx, func() attemptResult { return c.attempt(ctx, ref, n) })
}

// recovering runs try, one attempt on one account (an HTTP attempt, or a
// WebSocket handshake: s.dial), with 401 recovery: when the upstream's 401
// to it renewed the account's credentials (recoverUnauthorized), try runs
// once more, on the same account, and its result is the attempt's. try must
// load the account afresh (AccountDirectory.Load serves the renewed
// credentials by then).
func (c *call) recovering(ctx context.Context, try func() attemptResult) attemptResult {
	c.recovery.armed = true
	defer func() { c.recovery.armed = false }()
	res := try()
	if res.kind != attemptRecovered {
		return res
	}
	if ctx.Err() != nil {
		return attemptResult{kind: attemptCanceled, err: canceledErr()}
	}
	return try()
}

// recoverUnauthorized is the first step of classifying an upstream failure
// (classify; waitedRelay.classified in the waiting form), before the plugin
// is asked or its answer applied: a 401 to an attempt recovering runs, on an
// account of a type declaring refresh that the request has not recovered
// yet, has the account module renew its credentials. It reports whether
// they were renewed, so that the attempt runs again instead of being
// classified. A task submission never recovers: its failure is final (it
// never fails over either).
func (c *call) recoverUnauthorized(ctx context.Context, rt *typeRoute, acct *pluginv1.Account, status int) bool {
	id := acct.GetId()
	if status != http.StatusUnauthorized || !c.recovery.armed || c.recovery.tried[id] || c.g.d.Recovery == nil ||
		rt.binding.Type.Refresh == nil || c.ep.TaskSubmit() {
		return false
	}
	if c.recovery.tried == nil {
		c.recovery.tried = map[int64]bool{}
	}
	c.recovery.tried[id] = true
	ok, err := c.g.d.Recovery.RecoverCredentials(ctx, id, json.RawMessage(acct.GetCredentialsJson()))
	if err != nil {
		slog.WarnContext(ctx, "gateway: renew credentials after 401", "request_id", c.rid, "account", id, "err", err)
	}
	return ok
}
