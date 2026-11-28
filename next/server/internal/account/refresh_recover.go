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
	recoverGiveUp                            // a renewal held the lock and left them as they were
)

// RecoverCredentials implements core.CredentialRecovery. The renewal it
// runs is checked like a 401 recovery's (renewalSkipped: whatever the
// expiry, not credentials refused for good) and recorded like any other.
func (s *Service) RecoverCredentials(ctx context.Context, id int64, used json.RawMessage) (bool, error) {
	release, waited, err := s.awaitRefreshLock(ctx, id)
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
	switch recoveryStep(t.plain, used, waited) {
	case recoverWithCurrent:
		// Another node may have saved them: this node's snapshots must not
		// serve the refused ones while its account:changed is on the way.
		s.invalidateNode(id)
		return true, nil
	case recoverGiveUp:
		return false, nil
	}
	out, err := s.renew(ctx, t, reasonUnauthorized)
	return err == nil && out.Refreshed, err
}

// recoveryStep decides a recovery from the credentials as stored now, the
// ones the refused attempt used and whether another renewal held the lock
// first: credentials that changed since are retried as they are; unchanged
// ones are renewed, unless that renewal already ran on them.
func recoveryStep(current, used []byte, waited bool) recoveryAction {
	switch {
	case !bytes.Equal(current, used):
		return recoverWithCurrent
	case waited:
		return recoverGiveUp
	}
	return recoverRenew
}

// awaitRefreshLock takes account id's refresh lock (lockRefresh), waiting
// while another renewal holds it: until ctx ends, or for refreshLockTTL at
// most, the longest a renewal holds it. waited reports that another renewal
// held it; release is nil when the lock was not had.
func (s *Service) awaitRefreshLock(ctx context.Context, id int64) (release func(), waited bool, err error) {
	deadline := time.Now().Add(refreshLockTTL)
	for {
		release, held, err := s.lockRefresh(ctx, id)
		if err != nil || held {
			return release, waited, err
		}
		waited = true
		if !time.Now().Before(deadline) || !pause(ctx, refreshLockPoll) {
			return nil, true, nil
		}
	}
}

// pause waits d, or less when ctx ends first; it reports whether ctx is
// still live.
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
