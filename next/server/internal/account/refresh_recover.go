package account

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Renewal after an upstream 401 (CONTRACTS §141.2): the gateway asks
// RecoverCredentials for an account whose upstream has just refused its
// credentials, and tries the same request once more on it when the answer
// is yes.

var _ core.CredentialRecovery = (*Service)(nil)

// refreshLockPoll is how often RecoverCredentials tries again for a refresh
// lock another renewal holds.
const refreshLockPoll = 100 * time.Millisecond

// recoveryAction is what RecoverCredentials does once it has the lock.
type recoveryAction int

const (
	recoverRenew       recoveryAction = iota // renew the refused credentials now
	recoverWithCurrent                       // they changed meanwhile: retry with the current ones
	recoverGiveUp                            // a renewal of them ran while this one waited
)

// RecoverCredentials implements core.CredentialRecovery. The renewal it
// runs is checked like a 401 recovery's (renewalSkipped: whatever the
// expiry, not credentials refused for good) and recorded like any other.
// Waiting for the lock ends with ctx (the client leaving) or once it is
// free; the renewal itself is not cut short by ctx (renewalContext).
func (s *Service) RecoverCredentials(ctx context.Context, id int64, used json.RawMessage) (bool, error) {
	release, before, err := s.awaitRefreshLock(ctx, id)
	if err != nil || release == nil {
		return false, err
	}
	defer release()
	ctx, cancel := renewalContext(ctx)
	defer cancel()
	t, err := s.refreshTarget(ctx, id)
	if err != nil || t == nil {
		return false, err
	}
	step, err := s.recoveryStep(ctx, t, used, before)
	if err != nil || step == recoverGiveUp {
		return false, err
	}
	if step == recoverWithCurrent {
		// Another node may have saved them: this node's snapshots must not
		// serve the refused ones while its account:changed is on the way.
		s.invalidateNode(id)
		return true, nil
	}
	out, err := s.renew(ctx, t, reasonUnauthorized)
	return err == nil && out.Refreshed, err
}

// recoveryStep decides a recovery of t from the credentials the refused
// attempt used and, when it waited for the lock, the account's last renewal
// attempt as it stood before (before): credentials that changed since are
// retried as they are; unchanged ones are renewed, unless a renewal was
// attempted on them while this one waited. A holder that only read them
// (another recovery, a sweep finding them not due) does not count.
func (s *Service) recoveryStep(ctx context.Context, t *refreshTarget, used []byte, before *lastAttempt) (recoveryAction, error) {
	if !bytes.Equal(t.plain, used) {
		return recoverWithCurrent, nil
	}
	if before == nil {
		return recoverRenew, nil
	}
	now, err := s.lastRefreshAttempt(ctx, t.row.ID)
	if err != nil {
		return recoverRenew, err
	}
	if before.advancedTo(now) {
		return recoverGiveUp, nil
	}
	return recoverRenew, nil
}

// lastAttempt is an account's last renewal attempt (nil At: none yet).
type lastAttempt struct{ At *time.Time }

// advancedTo reports whether now records a later attempt than a.
func (a *lastAttempt) advancedTo(now *lastAttempt) bool {
	return now.At != nil && (a.At == nil || now.At.After(*a.At))
}

// lastRefreshAttempt reads account id's last renewal attempt.
func (s *Service) lastRefreshAttempt(ctx context.Context, id int64) (*lastAttempt, error) {
	states, err := s.d.DB.AccountRefreshStates(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if st := states[id]; st != nil {
		return &lastAttempt{At: st.LastAttemptAt}, nil
	}
	return &lastAttempt{}, nil
}

// awaitRefreshLock takes account id's refresh lock (lockRefresh), waiting
// while another renewal holds it, until ctx ends; a holder that dies leaves
// it free after refreshLockTTL. before is the account's last renewal
// attempt as it stood when the wait began, nil when the lock was free at
// once; release is nil when the lock was not had.
func (s *Service) awaitRefreshLock(ctx context.Context, id int64) (release func(), before *lastAttempt, err error) {
	for {
		release, held, err := s.lockRefresh(ctx, id)
		if err != nil || held {
			return release, before, err
		}
		if before == nil {
			if before, err = s.lastRefreshAttempt(ctx, id); err != nil {
				return nil, nil, err
			}
		}
		if !core.Pause(ctx, refreshLockPoll) {
			return nil, before, nil
		}
	}
}
