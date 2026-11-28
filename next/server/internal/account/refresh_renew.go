package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// One renewal of an account's credentials under its refresh lock
// (CONTRACTS §48.3): re-read the account, check that the reason renews it
// now, let the plugin describe the request and read the answer, and save
// the merged credentials only over the ones it started from.

// refreshTarget is an account as a renewal reads it under the lock.
type refreshTarget struct {
	row  *row
	decl *manifest.AccountRefresh
	bt   core.AccountTypeBinding
	// plain is the stored credentials decrypted, creds the same decoded
	// (numbers kept as written); hash is the SHA-256 of the ciphertext.
	plain []byte
	creds map[string]any
	hash  [sha256.Size]byte
}

// refreshTarget reads account id for a renewal; nil when its type does not
// (or no longer) declare refresh.
func (s *Service) refreshTarget(ctx context.Context, id int64) (*refreshTarget, error) {
	a, err := s.loadRow(ctx, s.d.DB.Gorm, id, core.AllScope, false)
	if err != nil {
		return nil, err
	}
	decl, bt := s.refreshDecl(a.PluginKey, a.Type)
	if decl == nil {
		return nil, nil
	}
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt account %d: %w", a.ID, err)
	}
	creds, err := decodeCreds(plain)
	if err != nil {
		return nil, fmt.Errorf("account %d credentials: %w", a.ID, err)
	}
	return &refreshTarget{row: a, decl: decl, bt: bt, plain: plain, creds: creds, hash: sha256.Sum256(a.CredEnc)}, nil
}

// expiry is the expiry of t's credentials as they stand, nil when unreadable.
func (t *refreshTarget) expiry() *time.Time {
	if e, ok := t.decl.ExpiresAt(t.creds); ok {
		return &e
	}
	return nil
}

// renewHeld renews account id for reason; the caller holds its refresh
// lock. Upstream and plugin failures are recorded and reported in the
// outcome; only database errors are returned.
func (s *Service) renewHeld(ctx context.Context, id int64, reason refreshReason) (*RefreshOutcome, error) {
	t, err := s.refreshTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return &RefreshOutcome{Skipped: refreshNotDue}, nil
	}
	return s.renew(ctx, t, reason)
}

// renew renews t for reason, unless renewalSkipped says not now.
func (s *Service) renew(ctx context.Context, t *refreshTarget, reason refreshReason) (*RefreshOutcome, error) {
	skipped, err := s.renewalSkipped(ctx, t, reason)
	if err != nil {
		return nil, err
	}
	if skipped != "" {
		return &RefreshOutcome{Skipped: skipped}, nil
	}
	state := store.RefreshState{AccountID: t.row.ID, ExpiresAt: t.expiry()}
	patch, errType, msg := s.callRefresh(ctx, t.bt, t.row, t.plain, reason)
	if errType != "" {
		return s.recordRefreshFailure(ctx, t, state, errType, msg)
	}
	return s.saveRenewal(ctx, t, patch, state)
}

// renewalSkipped is why reason does not renew t now ("" = renew). The sweep
// and the renewal after a save only renew credentials whose expiry is read
// and within beforeExpirySec; a 401 recovery renews whatever the expiry
// (the upstream has just refused them). Every reason but an administrator's
// skips credentials whose refresh was refused for good, until they change.
func (s *Service) renewalSkipped(ctx context.Context, t *refreshTarget, reason refreshReason) (string, error) {
	if reason == reasonForced {
		return "", nil
	}
	if reason != reasonUnauthorized {
		exp, ok := t.decl.ExpiresAt(t.creds)
		if !ok {
			return refreshNoExpiry, nil
		}
		if time.Until(exp) > t.decl.Before() {
			return refreshNotDue, nil
		}
	}
	states, err := s.d.DB.AccountRefreshStates(ctx, []int64{t.row.ID})
	if err != nil {
		return "", err
	}
	if st := states[t.row.ID]; st != nil && st.ErrorType == store.RefreshAuthRejected && bytes.Equal(st.RejectedCredHash, t.hash[:]) {
		return refreshRejected, nil
	}
	return "", nil
}

// recordRefreshFailure records a failed renewal of t; a refusal for good
// also records which credentials were refused.
func (s *Service) recordRefreshFailure(ctx context.Context, t *refreshTarget, state store.RefreshState, errType, msg string) (*RefreshOutcome, error) {
	state.ErrorType, state.Error = errType, truncate(msg, maxRefreshError)
	if errType == store.RefreshAuthRejected {
		state.RejectedCredHash = t.hash[:]
	}
	slog.WarnContext(ctx, "account: credential refresh failed", "account", t.row.ID, "type", t.row.Type, "error_type", errType, "error", state.Error)
	if err := store.RecordAccountRefresh(ctx, s.d.DB.Gorm, state); err != nil {
		return nil, err
	}
	return &RefreshOutcome{ErrorType: state.ErrorType, Error: state.Error}, nil
}

// saveRenewal merges patch into t's credentials and saves them, unless the
// account changed meanwhile; a saved renewal is told to every node.
func (s *Service) saveRenewal(ctx context.Context, t *refreshTarget, patch map[string]any, state store.RefreshState) (*RefreshOutcome, error) {
	enc, err := s.mergedCredentials(ctx, t, patch)
	if err != nil {
		return nil, err
	}
	state.ExpiresAt = t.expiry()
	saved, err := s.storeRenewal(ctx, t, enc, state)
	if err != nil {
		return nil, err
	}
	id := t.row.ID
	if !saved {
		slog.WarnContext(ctx, "account: credentials changed while refreshing; renewal discarded", "account", id)
		return &RefreshOutcome{Skipped: refreshChanged}, nil
	}
	s.quota.forget(id)
	s.evictQuotaCache(ctx, id)
	s.changed(ctx, id)
	slog.InfoContext(ctx, "account: credentials refreshed", "account", id, "type", t.row.Type, "expires_at", state.ExpiresAt)
	return &RefreshOutcome{Refreshed: true}, nil
}

// mergedCredentials merges patch into t.creds and returns them encrypted:
// listed keys replace, null removes, the rest is kept. Settings fields live
// in their own column and are never touched here.
func (s *Service) mergedCredentials(ctx context.Context, t *refreshTarget, patch map[string]any) ([]byte, error) {
	for k, v := range patch {
		switch {
		case slices.Contains(t.bt.Type.SettingsFields, k):
			slog.WarnContext(ctx, "account: refresh patch names a settings field; ignored", "plugin", t.row.PluginKey, "type", t.row.Type, "field", k)
		case v == nil:
			delete(t.creds, k)
		default:
			t.creds[k] = v
		}
	}
	plain, err := json.Marshal(t.creds)
	if err != nil {
		return nil, err
	}
	return s.d.Cipher.Encrypt(plain, aad(t.row.PluginKey))
}

// storeRenewal saves enc as t's credentials, only over the ones the renewal
// started from (an administrator who saved new ones meanwhile wins), with
// its audit entry, refresh state and event in the same transaction.
func (s *Service) storeRenewal(ctx context.Context, t *refreshTarget, enc []byte, state store.RefreshState) (bool, error) {
	id, saved := t.row.ID, false
	err := s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		res := store.G(ctx, tx).Model(&model.Account{}).Where("id = ? AND credentials_enc = ? AND deleted_at IS NULL", id, t.row.CredEnc).
			Updates(map[string]any{"credentials_enc": enc, "updated_at": gorm.Expr("clock_timestamp()")})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		saved = true
		detail := map[string]any{"expires_at": state.ExpiresAt}
		if err := audit.Audit(ctx, tx, 0, "account.credentials_refresh", "account", itoa(id), detail); err != nil {
			return err
		}
		if err := store.RecordAccountRefresh(ctx, tx, state); err != nil {
			return err
		}
		return s.d.Events.Emit(ctx, tx, core.Event{Type: core.EventAccountUpdated,
			Payload: basicPayload(id, t.row.PluginKey, t.row.Type, t.row.Name)})
	})
	return saved, err
}

// callRefresh asks the plugin for the request of a renewal for reason,
// sends it and lets the plugin read the answer. It returns the credential
// patch, or an error type (store.RefreshAuthRejected / RefreshTransient) and
// message.
func (s *Service) callRefresh(ctx context.Context, bt core.AccountTypeBinding, a *row, plain []byte, reason refreshReason) (patch map[string]any, errType, msg string) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	// The refresh goes out through the row's proxy; without it the plugin is
	// not called.
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		return nil, store.RefreshTransient, core.AsError(err).Message
	}
	acct := a.pluginAccount(plain, eg.Proxy)
	built, errType, msg := buildRefresh(ctx, bt, &pluginv1.BuildRefreshRequestRequest{Account: acct, Reason: reason})
	if errType != "" {
		return nil, errType, msg
	}
	ans := s.sendPluginCall(ctx, pluginCall{plugin: bt.Plugin.Key, what: "refresh", method: built.GetMethod(), defaultMethod: http.MethodPost,
		url: built.GetUrl(), headers: built.GetHeaders(), body: []byte(built.GetBodyJson())}, eg)
	return parseRefresh(ctx, bt, &pluginv1.ParseRefreshResponseRequest{Account: acct,
		Status: ans.status, Headers: ans.headers, Body: ans.body, TransportError: ans.transportError})
}

// buildRefresh asks the plugin to describe the refresh request.
func buildRefresh(ctx context.Context, bt core.AccountTypeBinding, in *pluginv1.BuildRefreshRequestRequest) (_ *pluginv1.BuildRefreshRequestResponse, errType, msg string) {
	built, err := bt.Client.BuildRefreshRequest(ctx, in)
	switch status.Code(err) {
	case codes.Unimplemented:
		return nil, store.RefreshTransient, "plugin declares refresh but does not implement BuildRefreshRequest"
	case codes.FailedPrecondition:
		// These credentials cannot be renewed at all (no refresh token):
		// like a refusal, nothing changes until they do.
		return nil, store.RefreshAuthRejected, status.Convert(err).Message()
	}
	if err == nil && built == nil {
		err = errors.New("empty response")
	}
	if err != nil {
		return nil, store.RefreshTransient, "plugin: " + err.Error()
	}
	return built, "", ""
}

// parseRefresh lets the plugin read the refresh answer.
func parseRefresh(ctx context.Context, bt core.AccountTypeBinding, in *pluginv1.ParseRefreshResponseRequest) (patch map[string]any, errType, msg string) {
	res, err := bt.Client.ParseRefreshResponse(ctx, in)
	if err == nil && res == nil {
		err = errors.New("empty response")
	}
	if err != nil {
		return nil, store.RefreshTransient, "plugin: " + err.Error()
	}
	switch res.GetErrorType() {
	case pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED:
	case pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED:
		return nil, store.RefreshAuthRejected, res.GetErrorMessage()
	default:
		return nil, store.RefreshTransient, res.GetErrorMessage()
	}
	patch, err = decodeCreds([]byte(res.GetCredentialsPatchJson()))
	if err != nil || len(patch) == 0 {
		return nil, store.RefreshTransient, "plugin returned no credentials to save"
	}
	return patch, "", ""
}
