package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// BeginPasskeyLogin starts a discoverable login: the browser offers the
// passkeys it has for this site, so no email is asked for or revealed.
func (s *Service) BeginPasskeyLogin(ctx context.Context, r *http.Request, ip string) (*PasskeyCeremony, error) {
	if wait := s.limiter.check(ctx, "", ip); wait > 0 {
		return nil, rateLimitedError(ctx, wait)
	}
	if wait := s.limiter.allowPasskeyBegin(ctx, ip); wait > 0 {
		return nil, rateLimitedError(ctx, wait)
	}
	wa, err := s.webAuthnFor(r, "")
	if err != nil {
		return nil, err
	}
	options, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	token, err := s.createFlow(ctx, flowPasskeyLogin, 0, session)
	if err != nil {
		return nil, err
	}
	return &PasskeyCeremony{FlowToken: token, ExpiresIn: flowTTLSeconds, Options: options}, nil
}

// FinishPasskeyLogin verifies the assertion and issues tokens. A passkey checks
// the user's presence and verification itself, so two-factor is not asked for
// again. Every failure looks the same and counts towards the login rate limit.
func (s *Service) FinishPasskeyLogin(ctx context.Context, r *http.Request, flowToken string, credential json.RawMessage, ip string) (*LoginResult, error) {
	if wait := s.limiter.check(ctx, "", ip); wait > 0 {
		return nil, rateLimitedError(ctx, wait)
	}
	var session webauthn.SessionData
	if _, err := s.takeFlow(ctx, flowToken, flowPasskeyLogin, 0, &session); err != nil {
		return nil, err
	}
	wa, err := s.webAuthnFor(r, session.RelyingPartyID)
	if err != nil {
		return nil, err
	}
	used, err := s.validatePasskeyLogin(ctx, wa, session, credential)
	if err != nil {
		s.limiter.fail(ctx, "", ip)
		return nil, errPasskeyFailed
	}
	raw, err := json.Marshal(used.credential)
	if err != nil {
		return nil, err
	}
	return s.signIn(withLoginMethod(ctx, loginMethodPasskey), used.user.id, login{gone: errPasskeyFailed, disabled: errPasskeyFailed,
		check: passkeyUseCheck(used.passkeyID, used.user.id, raw)})
}

// passkeyLogin is a verified assertion: the user, the passkey's id and its
// credential as just verified (its sign count).
type passkeyLogin struct {
	user       *webAuthnUser
	passkeyID  int64
	credential *webauthn.Credential
}

// validatePasskeyLogin verifies the assertion credential of a
// BeginPasskeyLogin ceremony; any failure is errPasskeyFailed's to answer.
func (s *Service) validatePasskeyLogin(ctx context.Context, wa *webauthn.WebAuthn, session webauthn.SessionData, credential json.RawMessage) (*passkeyLogin, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	if err != nil {
		return nil, err
	}
	var used passkeyLogin
	found, verified, err := wa.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		u, passkeyID, err := s.passkeyOwner(ctx, session.RelyingPartyID, rawID, handle)
		if err != nil {
			return nil, err
		}
		used.user, used.passkeyID = u, passkeyID
		return u, nil
	}, session, parsed)
	if err != nil || found == nil || used.user == nil {
		return nil, errPasskeyFailed
	}
	used.credential = verified
	return &used, nil
}

// passkeyOwner is the active user of the passkey the browser names by
// rawID, with the passkey's id: the passkey must be of the ceremony's
// relying party rpID and handle its user's.
func (s *Service) passkeyOwner(ctx context.Context, rpID string, rawID, handle []byte) (*webAuthnUser, int64, error) {
	var passkeyID, owner int64
	var passkeyRP string
	if err := passkeyByCredential(store.G(ctx, s.db.Gorm), rawID).Row().Scan(&passkeyID, &owner, &passkeyRP); err != nil {
		return nil, 0, err
	}
	if passkeyRP != rpID || !bytes.Equal(handle, userHandle(owner)) {
		return nil, 0, errPasskeyFailed
	}
	u, status, err := s.loadWebAuthnUser(ctx, owner)
	if err != nil {
		return nil, 0, err
	}
	if status != StatusActive {
		return nil, 0, errPasskeyFailed
	}
	return u, passkeyID, nil
}

// passkeyUseCheck is the check of a passkey login under the user's lock:
// the passkey passkeyID of userID records its use (usePasskey), and one
// deleted meanwhile fails the login.
func passkeyUseCheck(passkeyID, userID int64, credential []byte) func(*gorm.DB, string) error {
	return func(g *gorm.DB, _ string) error {
		res := usePasskey(passkeyByID(g, passkeyID, userID), credential)
		if res.Error == nil && res.RowsAffected == 0 {
			return errPasskeyFailed
		}
		return res.Error
	}
}

// passkeyByCredential reads the passkey of a credential the browser names.
func passkeyByCredential(db *gorm.DB, credentialID []byte) *gorm.DB {
	return db.Model(&model.UserPasskey{}).Select("id, user_id, rp_id").Where("credential_id = ?", credentialID)
}
