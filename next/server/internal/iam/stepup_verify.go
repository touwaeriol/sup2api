package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// POST /me/verify (CONTRACTS §80.2): checking the caller by one method of
// the step-up policy (stepup.go) and issuing the proof.

// VerifyInput is the body of POST /me/verify. Context is the operation the
// proof is for, "METHOD path" (details.context of verification_required).
type VerifyInput struct {
	Scope      string          `json:"scope"`
	Context    string          `json:"context"`
	Method     string          `json:"method"`
	Password   string          `json:"password"`
	Code       string          `json:"code"`
	FlowToken  string          `json:"flow_token"`
	Credential json.RawMessage `json:"credential"`
}

// StepUpProof is the response of POST /me/verify. AccessToken is the new
// token of a caller signed in with a session: the console uses it from
// then on (the old one works for a few seconds more).
type StepUpProof struct {
	Proof       string    `json:"proof"`
	ExpiresAt   time.Time `json:"expires_at"`
	Method      string    `json:"method"`
	Scope       string    `json:"scope"`
	Context     string    `json:"context"`
	AccessToken string    `json:"access_token,omitempty"`
}

var errMethodUnavailable = core.InvalidFields(core.FieldError{Field: "method", Code: "unavailable", Message: "this verification method is not available"})

// VerifyStepUp checks the caller by the chosen method and issues a proof.
// Failures count as they do at login: the password towards the login rate
// limit, a wrong code towards the two-factor lockout, a failed passkey
// towards the IP limit.
func (s *Service) VerifyStepUp(ctx context.Context, r *http.Request, userID int64, in VerifyInput, ip string) (*StepUpProof, error) {
	password, err := s.verifyStepUpMethod(ctx, r, userID, in, ip)
	if err != nil {
		return nil, err
	}
	return s.issueStepUpProof(ctx, r, userID, in, password)
}

// verifyStepUpMethod checks the caller by in.Method, and the password with
// it when the scope asks for it, and reports whether the password was
// checked.
func (s *Service) verifyStepUpMethod(ctx context.Context, r *http.Request, userID int64, in VerifyInput, ip string) (bool, error) {
	methods, err := s.StepUpMethods(ctx, userID, in.Scope, in.Context)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(in.Context) == "" || len(in.Context) > 512 {
		return false, core.InvalidFields(core.FieldError{Field: "context", Code: "invalid", Message: "the operation to verify for"})
	}
	if !methods.usable(in.Method) {
		return false, errMethodUnavailable
	}
	password := methods.PasswordRequired || in.Method == methodPassword
	if password {
		if err := s.confirmPassword(ctx, userID, in.Password, ip); err != nil {
			return false, err
		}
	}
	switch in.Method {
	case methodTOTP:
		err = s.confirmCode(ctx, userID, in.Code)
	case methodPasskey:
		err = s.finishStepUpPasskey(ctx, r, userID, in.FlowToken, in.Credential, ip)
	}
	return password, err
}

// issueStepUpProof issues the proof of a verified caller, audits it and
// gives a caller's session a new token (rotateSession).
func (s *Service) issueStepUpProof(ctx context.Context, r *http.Request, userID int64, in VerifyInput, password bool) (*StepUpProof, error) {
	payload := proofPayload{Scope: in.Scope, Operation: in.Context, Subject: stepUpSubject(ctx), Method: in.Method, Password: password}
	detail := stepUpAuditDetail(ctx, in)
	// The proof, its audit and the new token of a session go together: the session is rotated last, and when it fails neither the
	// audit nor the proof stays. The price is the commit failing after the
	// rotation: the new token is lost and the old one ends with its grace,
	// signing the console out. Only the commit itself fails there, and
	// signing in again is the safe outcome of a verification that left no
	// record.
	var token, access string
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		if token, err = s.createFlow(ctx, flowStepUpProof, userID, payload); err != nil {
			return err
		}
		if err := auditSelf(ctx, tx, userID, "user.security_verify", detail); err != nil {
			return err
		}
		access, err = s.rotateSession(ctx, userID, core.Session(ctx), bearerToken(r))
		return err
	})
	if err != nil {
		if token != "" { // a proof in Valkey does not roll back
			_, _ = s.flows.TakeFlow(context.WithoutCancel(ctx), flowStepUpProof, token)
		}
		return nil, err
	}
	return &StepUpProof{Proof: token, ExpiresAt: time.Now().Add(flowTTL), Method: in.Method, Scope: in.Scope, Context: in.Context,
		AccessToken: access}, nil
}

// stepUpAuditDetail is what the audit of a verification records: the
// method, scope and operation, and whether it came through a session or an
// access key (which one).
func stepUpAuditDetail(ctx context.Context, in VerifyInput) map[string]any {
	detail := map[string]any{"method": in.Method, "scope": in.Scope, "context": in.Context, "via": "session"}
	if k, ok := core.AccessKey(ctx); ok {
		detail["via"], detail["access_key_id"] = "access_key", k.KeyID
	}
	return detail
}

// rotateSession gives the caller's session id a new token in place of
// presented, the one the request carried, so a token taken before is not
// the one that carries on (session fixation, a copied token); the session
// id, which proofs and step-up windows are bound to, stays. "" for an
// access key (no id), which never reaches Valkey here, for a session that
// ended meanwhile, and for a presented token another rotation left behind
// since the request was sent (another tab verified first): the token that
// rotation issued is the caller's to use, and rotating it would end it.
func (s *Service) rotateSession(ctx context.Context, userID int64, id, presented string) (string, error) {
	if id == "" {
		return "", nil
	}
	if s.sessions == nil {
		return "", tokenstore.ErrUnavailable
	}
	token, err := s.sessions.Rotate(ctx, userID, id, presented)
	if errors.Is(err, tokenstore.ErrNotFound) || errors.Is(err, tokenstore.ErrSuperseded) {
		return "", nil
	}
	return token, err
}

// bearerToken is the token r authenticated with; "" without one.
func bearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

// confirmPassword checks the caller's password for a sensitive change, under
// the login rate limit.
func (s *Service) confirmPassword(ctx context.Context, userID int64, password, ip string) error {
	var email, hash string
	err := liveUser(store.G(ctx, s.db.Gorm), userID, "email, password_hash").Row().Scan(&email, &hash)
	if store.IsNoRows(err) {
		return core.ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	if wait := s.limiter.check(ctx, email, ip); wait > 0 {
		return loginLocked(ctx, wait)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.limiter.fail(ctx, email, ip)
		return core.InvalidFields(core.FieldError{Field: "password", Code: "incorrect", Message: "incorrect password"})
	}
	return nil
}

// confirmCode checks a TOTP or backup code of userID; a wrong one counts
// towards the lockout.
func (s *Service) confirmCode(ctx context.Context, userID int64, code string) error {
	if strings.TrimSpace(code) == "" {
		return errSecondFactorRequired
	}
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockUserRow(ctx, tx, userID); err != nil {
			return err
		}
		return s.checkSecondFactor(ctx, tx, userID, code)
	})
	if isBadSecondFactor(err) {
		if ferr := s.recordSecondFactorFailure(ctx, userID); ferr != nil {
			return ferr
		}
	}
	return err
}

// errStepUpPasskeyFailed is a refused assertion of a signed-in caller: a
// field error, not the login's 401, which the console would take for an
// expired session.
var errStepUpPasskeyFailed = core.InvalidFields(core.FieldError{Field: "credential", Code: "failed", Message: "passkey verification failed"})

var errSecondFactorRequired = core.InvalidFields(core.FieldError{Field: "code", Code: "required", Message: "enter a verification code"})

// BeginStepUpPasskey starts the passkey assertion of a step-up (POST
// /me/verify/begin): the login ceremony, for the caller's own passkeys.
func (s *Service) BeginStepUpPasskey(ctx context.Context, r *http.Request, userID int64, scope, ip string) (*PasskeyCeremony, error) {
	methods, err := s.StepUpMethods(ctx, userID, scope, "")
	if err != nil {
		return nil, err
	}
	if !methods.usable(methodPasskey) {
		return nil, errMethodUnavailable
	}
	if wait := s.limiter.check(ctx, "", ip); wait > 0 {
		return nil, loginLocked(ctx, wait)
	}
	if wait := s.limiter.allowPasskeyBegin(ctx, ip); wait > 0 {
		return nil, loginLocked(ctx, wait)
	}
	wa, err := s.webAuthnFor(r, "")
	if err != nil {
		return nil, err
	}
	u, _, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	options, session, err := wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, core.ErrInvalidArgument.WithMessage("no passkey can be used here").WithCause(err)
	}
	token, err := s.createFlow(ctx, flowStepUpPasskey, userID, session)
	if err != nil {
		return nil, err
	}
	return &PasskeyCeremony{FlowToken: token, ExpiresIn: flowTTLSeconds, Options: options}, nil
}

// finishStepUpPasskey verifies the assertion of a BeginStepUpPasskey
// ceremony and records the passkey's use.
func (s *Service) finishStepUpPasskey(ctx context.Context, r *http.Request, userID int64, flowToken string, credential json.RawMessage, ip string) error {
	if wait := s.limiter.check(ctx, "", ip); wait > 0 {
		return loginLocked(ctx, wait)
	}
	var session webauthn.SessionData
	if _, err := s.takeFlow(ctx, flowToken, flowStepUpPasskey, userID, &session); err != nil {
		return err
	}
	wa, err := s.webAuthnFor(r, session.RelyingPartyID)
	if err != nil {
		return err
	}
	u, _, err := s.loadWebAuthnUser(ctx, userID)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credential)
	var used *webauthn.Credential
	if err == nil {
		used, err = wa.ValidateLogin(u, session, parsed)
	}
	if err != nil {
		s.limiter.fail(ctx, "", ip)
		return errStepUpPasskeyFailed
	}
	raw, err := json.Marshal(used)
	if err != nil {
		return err
	}
	return usePasskey(userPasskeyByCredential(store.G(ctx, s.db.Gorm), userID, used.ID), raw).Error
}
