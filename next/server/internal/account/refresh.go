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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Credential refresh (CONTRACTS §48), modelled on sub2api's
// TokenRefreshService and OAuthRefreshAPI: a sweep renews the credentials of
// account types declaring manifest refresh some time before they expire;
// each renewal runs under a cluster lock per account, re-reads the account,
// re-checks that it is still due, lets the plugin describe the request and
// read the answer, and saves the merged credentials only if they are still
// the ones it started from. A refresh token the upstream refused for good
// is not tried again until the credentials change.

const (
	// refreshSweepInterval is the period of core.account.credential_refresh
	// (sub2api check_interval 5 min).
	refreshSweepInterval = 5 * time.Minute
	// refreshLockTTL bounds one account's renewal.
	refreshLockTTL = time.Minute
	// refreshTimeout bounds the plugin calls and the upstream request.
	refreshTimeout = 30 * time.Second
	// refreshConcurrency is how many accounts a sweep renews at once
	// (sub2api provider_concurrency).
	refreshConcurrency = 4
	// refreshPage is how many candidate accounts a sweep reads per query.
	refreshPage = 200
	// maxRefreshError bounds the stored error message.
	maxRefreshError = 512
)

// Why a renewal was not attempted (RefreshOutcome.Skipped).
const (
	refreshNotDue     = "not_due"     // expiry further away than beforeExpirySec
	refreshNoExpiry   = "no_expiry"   // no readable expiry: only on request
	refreshInProgress = "in_progress" // another node or request holds the lock
	refreshRejected   = "rejected"    // these exact credentials were refused for good
	refreshChanged    = "changed"     // the account changed while renewing; nothing saved
)

// RefreshView is the credential refresh state of an account in the API.
type RefreshView struct {
	// ExpiresAt is the expiry read from the credentials at the last attempt.
	ExpiresAt     *time.Time `json:"expires_at"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	// ErrorType is "", "auth_rejected" (re-authorise the account) or
	// "transient" (retried on the next sweep).
	ErrorType string `json:"error_type"`
	Error     string `json:"error"`
}

// RefreshOutcome is the answer of POST /accounts/:id/refresh-credentials.
type RefreshOutcome struct {
	Refreshed bool   `json:"refreshed"`
	Skipped   string `json:"skipped,omitempty"`
	// ErrorType and Error report a failed attempt, as in RefreshView.
	ErrorType string       `json:"error_type,omitempty"`
	Error     string       `json:"error,omitempty"`
	Refresh   *RefreshView `json:"refresh"`
}

func refreshView(st *store.RefreshState) *RefreshView {
	if st == nil {
		return &RefreshView{}
	}
	return &RefreshView{ExpiresAt: st.ExpiresAt, LastAttemptAt: st.LastAttemptAt, LastSuccessAt: st.LastSuccessAt,
		ErrorType: st.ErrorType, Error: st.Error}
}

// refreshDecl returns the refresh declaration of an account's type, nil when
// the type has none or is not registered (plugin disabled).
func (s *Service) refreshDecl(pluginKey, typ string) (*manifest.AccountRefresh, core.AccountTypeBinding) {
	bt, ok := s.accountType(pluginKey, typ)
	if !ok || bt.Type.Refresh == nil || bt.Client == nil {
		return nil, bt
	}
	return bt.Type.Refresh, bt
}

// fillRefresh sets View.Refresh for the accounts whose type refreshes its
// credentials, from one batch read. It never calls an upstream.
func (s *Service) fillRefresh(ctx context.Context, rows []*row, out []*View) {
	var ids []int64
	for _, a := range rows {
		if d, _ := s.refreshDecl(a.PluginKey, a.Type); d != nil {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	states, err := s.d.DB.AccountRefreshStates(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "account: read refresh states", "err", err)
	}
	for i, a := range rows {
		if slices.Contains(ids, a.ID) {
			out[i].Refresh = refreshView(states[a.ID])
		}
	}
}

// refreshCredentials is POST /accounts/:id/refresh-credentials: renew now,
// whatever the expiry, unless another renewal of the account is running.
func (s *Service) refreshCredentials(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	a, err := s.loadIn(ctx, id, perm.AccountUpdate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if d, _ := s.refreshDecl(a.PluginKey, a.Type); d == nil {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(t(ctx,
			"this account type does not refresh its credentials", "该账号类型不支持刷新凭证")))
		return
	}
	out, err := s.refreshAccount(ctx, id, true)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// refreshAccount renews the credentials of one account, enabled or not (a
// disabled account is only kept out of scheduling). force skips the expiry
// and rejected-credentials checks (an administrator asked). Upstream and plugin failures are recorded and reported in the
// outcome; only database and lock errors are returned.
func (s *Service) refreshAccount(ctx context.Context, id int64, force bool) (*RefreshOutcome, error) {
	release, held, err := s.lockRefresh(ctx, id)
	if err != nil {
		return nil, err
	}
	if !held {
		return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshInProgress})
	}
	defer release()
	// Once started, a renewal is not cut short by its caller (a closing
	// browser, a sweep that lost its lock): the upstream may already have
	// rotated the refresh token, and only the saved answer holds the new one.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout+15*time.Second)
	defer cancel()

	// Re-read under the lock: another node may have just renewed it, and the
	// refresh token in hand may already be spent.
	a, err := s.loadRow(ctx, s.d.DB.Gorm, id, core.AllScope, false)
	if err != nil {
		return nil, err
	}
	decl, bt := s.refreshDecl(a.PluginKey, a.Type)
	if decl == nil {
		return &RefreshOutcome{Skipped: refreshNotDue}, nil
	}
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt account %d: %w", a.ID, err)
	}
	creds, err := decodeCreds(plain)
	if err != nil {
		return nil, fmt.Errorf("account %d credentials: %w", a.ID, err)
	}
	credHash := sha256.Sum256(a.CredEnc)
	exp, hasExp := decl.ExpiresAt(creds)
	if !force {
		if !hasExp {
			return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshNoExpiry})
		}
		if time.Until(exp) > decl.Before() {
			return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshNotDue})
		}
		states, err := s.d.DB.AccountRefreshStates(ctx, []int64{id})
		if err != nil {
			return nil, err
		}
		if st := states[id]; st != nil && st.ErrorType == store.RefreshAuthRejected && bytes.Equal(st.RejectedCredHash, credHash[:]) {
			return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshRejected})
		}
	}

	state := store.RefreshState{AccountID: id}
	if hasExp {
		state.ExpiresAt = &exp
	}
	patch, errType, msg := s.callRefresh(ctx, bt, a, plain)
	if errType != "" {
		state.ErrorType, state.Error = errType, truncate(msg, maxRefreshError)
		if errType == store.RefreshAuthRejected {
			state.RejectedCredHash = credHash[:]
		}
		slog.WarnContext(ctx, "account: credential refresh failed", "account", id, "type", a.Type, "error_type", errType, "error", state.Error)
		if err := store.RecordAccountRefresh(ctx, s.d.DB.Gorm, state); err != nil {
			return nil, err
		}
		return s.refreshOutcome(ctx, id, &RefreshOutcome{ErrorType: state.ErrorType, Error: state.Error})
	}

	// Merge: listed keys replace, null removes, the rest is kept. Settings
	// fields live in their own column and are never touched here.
	for k, v := range patch {
		if slices.Contains(bt.Type.SettingsFields, k) {
			slog.WarnContext(ctx, "account: refresh patch names a settings field; ignored", "plugin", a.PluginKey, "type", a.Type, "field", k)
			continue
		}
		if v == nil {
			delete(creds, k)
		} else {
			creds[k] = v
		}
	}
	newPlain, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	enc, err := s.d.Cipher.Encrypt(newPlain, aad(a.PluginKey))
	if err != nil {
		return nil, err
	}
	state.ExpiresAt = nil
	if e, ok := decl.ExpiresAt(creds); ok {
		state.ExpiresAt = &e
	}
	saved := false
	err = s.d.DB.Tx(ctx, func(tx *gorm.DB) error {
		// Only over the credentials this renewal started from: an
		// administrator who saved new ones meanwhile wins.
		res := store.G(ctx, tx).Model(&model.Account{}).Where("id = ? AND credentials_enc = ? AND deleted_at IS NULL", id, a.CredEnc).
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
			Payload: basicPayload(id, a.PluginKey, a.Type, a.Name)})
	})
	if err != nil {
		return nil, err
	}
	if !saved {
		slog.WarnContext(ctx, "account: credentials changed while refreshing; renewal discarded", "account", id)
		return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshChanged})
	}
	s.quota.forget(id)
	s.evictQuotaCache(ctx, id)
	s.changed(ctx, id)
	slog.InfoContext(ctx, "account: credentials refreshed", "account", id, "type", a.Type, "expires_at", state.ExpiresAt)
	return s.refreshOutcome(ctx, id, &RefreshOutcome{Refreshed: true})
}

// refreshOutcome attaches the stored refresh state to out.
func (s *Service) refreshOutcome(ctx context.Context, id int64, out *RefreshOutcome) (*RefreshOutcome, error) {
	states, err := s.d.DB.AccountRefreshStates(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out.Refresh = refreshView(states[id])
	return out, nil
}

// callRefresh asks the plugin for the request, sends it and lets the plugin
// read the answer. It returns the credential patch, or an error type
// (store.RefreshAuthRejected / RefreshTransient) and message.
func (s *Service) callRefresh(ctx context.Context, bt core.AccountTypeBinding, a *row, plain []byte) (patch map[string]any, errType, msg string) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	// The refresh goes out through the row's proxy; without it the plugin is
	// not called.
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		return nil, store.RefreshTransient, core.AsError(err).Message
	}
	acct := a.pluginAccount(plain, eg.Proxy)
	built, err := bt.Client.BuildRefreshRequest(ctx, &pluginv1.BuildRefreshRequestRequest{Account: acct})
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
	ans := s.sendPluginCall(ctx, pluginCall{plugin: bt.Plugin.Key, what: "refresh", method: built.GetMethod(), defaultMethod: http.MethodPost,
		url: built.GetUrl(), headers: built.GetHeaders(), body: []byte(built.GetBodyJson())}, eg)
	res, err := bt.Client.ParseRefreshResponse(ctx, &pluginv1.ParseRefreshResponseRequest{Account: acct,
		Status: ans.status, Headers: ans.headers, Body: ans.body, TransportError: ans.transportError})
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

// decodeCreds decodes a credentials object keeping numbers as written.
func decodeCreds(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// lockRefresh takes the refresh lock of one account: the cluster lock when
// there is a Locker, else a mutex of this node. held=false means another
// renewal is running.
func (s *Service) lockRefresh(ctx context.Context, id int64) (release func(), held bool, err error) {
	if s.d.Locker != nil {
		lk, ok, err := s.d.Locker.TryLock(ctx, "account:refresh:"+itoa(id), refreshLockTTL)
		if err != nil {
			return nil, false, core.ErrUnavailable.WithCause(err)
		}
		return lk.Release, ok, nil
	}
	v, _ := s.refreshLocal.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false, nil
	}
	return mu.Unlock, true, nil
}

// ---------------------------------------------------------------- sweep

// SweepRefresh renews the credentials of every active account whose type
// declares refresh and whose expiry is within its type's beforeExpirySec
// (core.account.credential_refresh, one run at a time in the cluster; each
// renewal also holds its account's lock). It returns how many were renewed.
func (s *Service) SweepRefresh(ctx context.Context) (int, error) {
	g := s.gen()
	if g == nil {
		return 0, nil
	}
	var keys []string
	for _, bt := range g.AccountTypes() {
		if bt.Type.Refresh != nil && bt.Client != nil {
			keys = append(keys, bt.Plugin.Key+"/"+bt.Type.ID)
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	renewed := 0
	var mu sync.Mutex
	sem := make(chan struct{}, refreshConcurrency)
	var wg sync.WaitGroup
	var after int64
	for ctx.Err() == nil {
		due, last, n, err := s.dueAccounts(ctx, keys, after)
		if err != nil {
			wg.Wait()
			return renewed, err
		}
		for _, id := range due {
			sem <- struct{}{}
			wg.Add(1)
			go func(id int64) {
				defer func() { <-sem; wg.Done() }()
				out, err := s.refreshAccount(ctx, id, false)
				if err != nil {
					slog.WarnContext(ctx, "account: credential refresh", "account", id, "err", err)
					return
				}
				if out.Refreshed {
					mu.Lock()
					renewed++
					mu.Unlock()
				}
			}(id)
		}
		if n < refreshPage {
			break
		}
		after = last
	}
	wg.Wait()
	return renewed, ctx.Err()
}

// dueAccounts reads one page of accounts of the refreshing types
// after id `after` and returns those that look due on this reading
// (refreshAccount checks again under the lock), the last id read and how
// many rows were read.
func (s *Service) dueAccounts(ctx context.Context, keys []string, after int64) (due []int64, last int64, n int, err error) {
	var page []model.Account
	if err := accountsOfTypes(store.G(ctx, s.d.DB.Gorm), keys, after, refreshPage).Select("id, plugin_key, type, credentials_enc").
		Find(&page).Error; err != nil {
		return nil, 0, 0, err
	}
	type cand struct {
		id      int64
		decl    *manifest.AccountRefresh
		credEnc []byte
		plugin  string
	}
	var cands []cand
	for _, a := range page {
		n++
		last = a.ID
		c := cand{id: a.ID, credEnc: a.CredentialsEnc, plugin: a.PluginKey}
		if c.decl, _ = s.refreshDecl(a.PluginKey, a.Type); c.decl != nil {
			cands = append(cands, c)
		}
	}
	ids := make([]int64, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.id)
	}
	states, err := s.d.DB.AccountRefreshStates(ctx, ids)
	if err != nil {
		return nil, 0, 0, err
	}
	now := time.Now()
	for _, c := range cands {
		if st := states[c.id]; st != nil && st.ErrorType == store.RefreshAuthRejected {
			if h := sha256.Sum256(c.credEnc); bytes.Equal(st.RejectedCredHash, h[:]) {
				continue
			}
		}
		plain, err := s.decrypt(c.plugin, c.credEnc)
		if err != nil {
			slog.WarnContext(ctx, "account: refresh sweep cannot decrypt", "account", c.id, "err", err)
			continue
		}
		creds, err := decodeCreds(plain)
		if err != nil {
			continue
		}
		if refreshDue(c.decl, creds, now) {
			due = append(due, c.id)
		}
	}
	return due, last, n, nil
}

// refreshDue reports whether credentials are inside their type's refresh
// window at now. Credentials without a readable expiry are never due: they
// are only renewed on request.
func refreshDue(decl *manifest.AccountRefresh, creds map[string]any, now time.Time) bool {
	exp, ok := decl.ExpiresAt(creds)
	return ok && exp.Sub(now) <= decl.Before()
}

// ---------------------------------------------------------------- after a save

// refreshSaved renews the credentials of account id right after a save
// stored secret (their plaintext), when the type declares refresh and they
// are already due. An account type whose form cannot fetch the first token
// itself stores an expiry in the past instead, and is then usable seconds
// after the save rather than after the next sweep; credentials that are not
// due (every other save) start nothing.
//
// The renewal runs in the background and is the sweep's, not a forced one:
// the account lock, the re-read and the refused-credentials check all apply,
// and a failure is recorded in the refresh state like any other.
// Nothing waits for it, a shutdown included: an account whose renewal was cut
// is still due and is renewed by the next sweep. A panic in it is recovered
// and logged like one in any other goroutine of the package (runRecovered),
// as no HTTP recovery middleware sees this one.
func (s *Service) refreshSaved(ctx context.Context, id int64, bt core.AccountTypeBinding, secret []byte) {
	if bt.Type.Refresh == nil || bt.Client == nil {
		return
	}
	creds, err := decodeCreds(secret)
	if err != nil || !refreshDue(bt.Type.Refresh, creds, time.Now()) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go runRecovered(ctx, func() {
		if _, err := s.refreshAccount(ctx, id, false); err != nil {
			slog.WarnContext(ctx, "account: credential refresh after save", "account", id, "err", err)
		}
	})
}
