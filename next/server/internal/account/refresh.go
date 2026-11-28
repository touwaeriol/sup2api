package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Credential refresh (CONTRACTS §48), modelled on sub2api's
// TokenRefreshService and OAuthRefreshAPI: a sweep renews the credentials of
// account types declaring manifest refresh some time before they expire;
// each renewal runs under a cluster lock per account, re-reads the account,
// re-checks that it is still due, lets the plugin describe the request and
// read the answer, and saves the merged credentials only if they are still
// the ones it started from (refresh_renew.go). A refresh token the upstream
// refused for good is not tried again until the credentials change. The
// gateway renews an account its upstream answered 401 at once
// (refresh_recover.go, CONTRACTS §141).

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

// refreshReason is why a renewal runs; the plugin is told
// (BuildRefreshRequestRequest.reason, CONTRACTS §141.3) and it decides which
// checks apply (renewalSkipped).
type refreshReason = pluginv1.BuildRefreshRequestRequest_Reason

const (
	reasonScheduled    = pluginv1.BuildRefreshRequestRequest_REASON_SCHEDULED    // the sweep
	reasonForced       = pluginv1.BuildRefreshRequestRequest_REASON_FORCED       // POST refresh-credentials
	reasonSaved        = pluginv1.BuildRefreshRequestRequest_REASON_SAVED        // refreshSaved
	reasonUnauthorized = pluginv1.BuildRefreshRequestRequest_REASON_UNAUTHORIZED // RecoverCredentials
)

// refreshDecl returns the refresh declaration of an account's type, nil when
// the type has none or is not registered (plugin disabled).
func (s *Service) refreshDecl(pluginKey, typ string) (*manifest.AccountRefresh, core.AccountTypeBinding) {
	bt, ok := s.accountType(pluginKey, typ)
	if !ok || bt.Type.Refresh == nil || bt.Client == nil {
		return nil, bt
	}
	return bt.Type.Refresh, bt
}

// refreshAccount renews the credentials of one account, enabled or not (a
// disabled account is only kept out of scheduling), for reason
// (renewalSkipped), unless another renewal of it holds its lock. Upstream
// and plugin failures are recorded and reported in the outcome; only
// database and lock errors are returned.
func (s *Service) refreshAccount(ctx context.Context, id int64, reason refreshReason) (*RefreshOutcome, error) {
	release, held, err := s.lockRefresh(ctx, id)
	if err != nil {
		return nil, err
	}
	if !held {
		return s.refreshOutcome(ctx, id, &RefreshOutcome{Skipped: refreshInProgress})
	}
	defer release()
	ctx, cancel := renewalContext(ctx)
	defer cancel()
	out, err := s.renewHeld(ctx, id, reason)
	if err != nil {
		return nil, err
	}
	return s.refreshOutcome(ctx, id, out)
}

// renewalContext is the context of a renewal that has its lock. Once
// started, a renewal is not cut short by its caller (a closing browser, a
// sweep that lost its lock, a gateway client that left): the upstream may
// already have rotated the refresh token, and only the saved answer holds
// the new one.
func renewalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout+15*time.Second)
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
				out, err := s.refreshAccount(ctx, id, reasonScheduled)
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
// The renewal runs in the background and is checked like the sweep's
// (reasonSaved), not like a forced one: the account lock, the re-read and
// the refused-credentials check all apply, and a failure is recorded in the
// refresh state like any other.
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
		if _, err := s.refreshAccount(ctx, id, reasonSaved); err != nil {
			slog.WarnContext(ctx, "account: credential refresh after save", "account", id, "err", err)
		}
	})
}
