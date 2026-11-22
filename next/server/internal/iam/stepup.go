package iam

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Step-up verification (CONTRACTS §80.2, docs/DESIGN-2026-11-12-AUTHZ-HARDENING.md
// 期 4): the one implementation of core.StepUpVerifier. A caller verifies
// once (VerifyStepUp) and gets a proof: five minutes, one use, bound to the
// user, the session or access key, the scope and the operation. Proofs are
// flows like the other short steps (§79.1, flow.go): shared by the nodes and
// taken once. A session that verifies gets a new token of the same session
// (§100.4), so the proof's binding to the session holds.

// The verification methods.
const (
	methodPasskey  = "passkey"
	methodTOTP     = "totp" // a TOTP or backup code
	methodPassword = "password"
)

// Why a method is unavailable.
const (
	reasonNotEnrolled = "not_enrolled"
	reasonAccessKey   = "access_key"    // passkeys need the browser of a session
	reasonStrongFirst = "strong_method" // a passkey or two-factor is set up: the password alone is not enough
)

const (
	flowStepUpProof   = "step_up_proof"
	flowStepUpPasskey = "step_up_passkey"
)

// stepUpScopes are the scopes a proof can be issued for; true marks the
// ones that need the password as well as a second factor (§8.1: the super
// admin transfer cannot be undone, and changing the password replaces the
// credential itself).
var stepUpScopes = map[string]bool{
	core.StepUpCredentialReveal:   false,
	core.StepUpProxyReveal:        false,
	core.StepUpSuperAdminTransfer: true,
	core.StepUpSelfPassword:       true,
	core.StepUpSelfEmail:          false,
	core.StepUpSelf2FAEnable:      false,
	core.StepUpSelf2FADisable:     false,
	core.StepUpSelfBackupCodes:    false,
	core.StepUpSelfPasskeyAdd:     false,
	core.StepUpSelfPasskeyDelete:  false,
	core.StepUpUserPasswordReset:  false,
	core.StepUpUserSecurityReset:  false,
	core.StepUpUserDelete:         false,
	core.StepUpPluginInstall:      false,
	core.StepUpPluginGrant:        false,
	core.StepUpPublisherManage:    false,
	core.StepUpSystemUpdate:       false,
	core.StepUpAccessKeyWrite:     false,
	core.StepUpSuperAdminIdentity: false,
}

// KnownStepUpScope reports whether a proof can be issued for scope, for the
// start-up route check.
func KnownStepUpScope(scope string) bool {
	_, ok := stepUpScopes[scope]
	return ok
}

// VerifyMethod is one way to verify, for GET /me/verify/methods.
type VerifyMethod struct {
	Method    string `json:"method"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// VerifyMethods is the response of GET /me/verify/methods: the methods of
// one scope. With PasswordRequired the password goes with the chosen method
// (with the password method, it is the method).
type VerifyMethods struct {
	Scope            string         `json:"scope"`
	PasswordRequired bool           `json:"password_required"`
	Methods          []VerifyMethod `json:"methods"`
}

// usable reports whether method is available.
func (m *VerifyMethods) usable(method string) bool {
	return slices.ContainsFunc(m.Methods, func(v VerifyMethod) bool { return v.Method == method && v.Available })
}

func unknownScope() error {
	return core.InvalidFields(core.FieldError{Field: "scope", Code: "invalid", Message: "unknown verification scope"})
}

// StepUpMethods applies the policy (§8.1) to userID for scope and the
// operation verified for ("METHOD path", "" when unknown): a passkey (not
// through an access key) or a two-factor code when the user has set one
// up, the password only when neither is; the password-too scopes, and the
// super admin removing its last factor (lastSuperFactor), ask for the
// password with it.
func (s *Service) StepUpMethods(ctx context.Context, userID int64, scope, operation string) (*VerifyMethods, error) {
	return s.stepUpMethods(ctx, s.db.Gorm, userID, scope, operation)
}

// stepUpMethods is StepUpMethods read through q: the pool, or the
// transaction of a caller that holds one.
func (s *Service) stepUpMethods(ctx context.Context, q *gorm.DB, userID int64, scope, operation string) (*VerifyMethods, error) {
	passwordToo, ok := stepUpScopes[scope]
	if !ok {
		return nil, unknownScope()
	}
	var totp bool
	var passkeys int
	err := userFactors(store.G(ctx, q), userID).Row().Scan(&totp, &passkeys)
	if err != nil {
		return nil, err
	}
	if !passwordToo {
		if passwordToo, err = s.lastSuperFactor(ctx, q, userID, scope, operation, totp, passkeys); err != nil {
			return nil, err
		}
	}
	return &VerifyMethods{Scope: scope, PasswordRequired: passwordToo, Methods: verifyMethods(ctx, userID, totp, passkeys)}, nil
}

// verifyMethods are the methods of userID, who has two-factor (totp) and
// passkeys: a passkey unless there is none or the caller is an access key,
// a code with two-factor, and the password only without either.
func verifyMethods(ctx context.Context, userID int64, totp bool, passkeys int) []VerifyMethod {
	passkey := passkeys > 0
	pk := VerifyMethod{Method: methodPasskey, Available: passkey}
	switch {
	case !passkey:
		pk.Reason = reasonNotEnrolled
	case core.ByAccessKey(ctx, userID):
		pk.Available, pk.Reason = false, reasonAccessKey
	}
	code := VerifyMethod{Method: methodTOTP, Available: totp}
	if !totp {
		code.Reason = reasonNotEnrolled
	}
	pw := VerifyMethod{Method: methodPassword, Available: !totp && !passkey}
	if !pw.Available {
		pw.Reason = reasonStrongFirst
	}
	return []VerifyMethod{pk, code, pw}
}

// userFactors reads whether userID has two-factor and how many passkeys.
func userFactors(db *gorm.DB, userID int64) *gorm.DB {
	passkeys := db.Model(&model.UserPasskey{}).Select("count(*)").Where("user_id = ?", userID)
	return store.OneRow(db).Select("EXISTS (?), (?)", totpOf(db, userID).Select("1"), passkeys)
}

// lastSuperFactor reports the super admin about to remove the last of its
// strong factors (passkeys, two-factor): its own security reset, disabling
// two-factor without a passkey, deleting its only passkey without
// two-factor. Like the transfer, that takes the password as well as a
// factor: one factor alone must not be enough to leave the account to the
// password. Other users are not affected.
func (s *Service) lastSuperFactor(ctx context.Context, q *gorm.DB, userID int64, scope, operation string, totp bool, passkeys int) (bool, error) {
	var last bool
	switch scope {
	case core.StepUpUserSecurityReset:
		last = resetTarget(operation) == userID
	case core.StepUpSelf2FADisable:
		last = totp && passkeys == 0
	case core.StepUpSelfPasskeyDelete:
		last = !totp && passkeys == 1
	}
	if !last {
		return false, nil
	}
	return s.authz.IsSuperuser(ctx, q, userID)
}

// resetTarget is the user a DELETE /users/:id/security operation acts on,
// its id read as the route reads it (httpapi.PathID: "01" and "+1" are 1),
// 0 for any other operation.
func resetTarget(operation string) int64 {
	rest, ok := strings.CutPrefix(operation, http.MethodDelete+" "+httpapi.APIPrefix+"/users/")
	id, found := strings.CutSuffix(rest, "/security")
	if !ok || !found {
		return 0
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	return n
}

// stepUpSubject is the session or access key a proof is bound to.
func stepUpSubject(ctx context.Context) string {
	if k, ok := core.AccessKey(ctx); ok {
		return "key:" + strconv.FormatInt(k.KeyID, 10)
	}
	return "session:" + core.Session(ctx)
}

// proofPayload is what a proof is bound to.
type proofPayload struct {
	Scope     string `json:"scope"`
	Operation string `json:"operation"`
	Subject   string `json:"subject"`
	Method    string `json:"method"`
	// Password: the password was checked with the method.
	Password bool `json:"password,omitempty"`
}

// ConsumeStepUp implements core.StepUpVerifier.
func (s *Service) ConsumeStepUp(ctx context.Context, scope string) error {
	if core.StepUpSpent(ctx) == scope {
		return nil
	}
	uid, _ := core.UserID(ctx)
	proof, operation, ok := core.StepUpRequest(ctx)
	if !ok || uid == 0 {
		return core.ErrVerificationRequired.WithDetails(map[string]any{"scope": scope})
	}
	if proof != "" {
		var p proofPayload
		_, err := s.takeFlow(ctx, proof, flowStepUpProof, uid, &p)
		if err != nil && !errors.Is(err, errFlowExpired) {
			return err
		}
		if err == nil && p.Scope == scope && p.Operation == operation && p.Subject == stepUpSubject(ctx) {
			enough, err := s.proofStillEnough(ctx, uid, p)
			if err != nil {
				return err
			}
			if enough {
				core.MarkStepUpSpent(ctx, scope, passwordChecked(p))
				return nil
			}
		}
	}
	return s.verificationRequired(ctx, s.db.Gorm, uid, scope, operation)
}

// proofStillEnough applies the policy again when the proof is spent: the
// factors may have changed since it was issued (a proof for disabling
// two-factor, issued while a passkey was left, spent after that passkey was
// deleted), and a proof given without the password the operation now asks
// for (lastSuperFactor) is not enough.
func (s *Service) proofStillEnough(ctx context.Context, userID int64, p proofPayload) (bool, error) {
	if passwordChecked(p) {
		return true, nil
	}
	methods, err := s.StepUpMethods(ctx, userID, p.Scope, p.Operation)
	if err != nil {
		return false, err
	}
	return !methods.PasswordRequired, nil
}

// passwordChecked reports whether p was issued with the password: recorded
// since the last-factor rule, and always so for the password-too scopes
// (proofs issued before the record was kept).
func passwordChecked(p proofPayload) bool {
	return p.Password || stepUpScopes[p.Scope]
}

// keepLastFactor applies lastSuperFactor once more inside the transaction
// removing a factor of userID (scope self.2fa.disable or
// self.passkey.delete), locking the user's row first: two removals spent at
// once, each leaving the other's factor, cannot both pass the check made
// when their proofs were spent. A request whose proof was issued with the
// password passes; so do calls outside a console request.
func (s *Service) keepLastFactor(ctx context.Context, tx *gorm.DB, userID int64, scope string) error {
	if err := store.RequireTx(tx); err != nil {
		return err
	}
	_, operation, ok := core.StepUpRequest(ctx)
	if !ok || core.StepUpPassword(ctx) {
		return nil
	}
	g := store.G(ctx, tx)
	if err := lockUserIfAny(g, userID).Error; err != nil {
		return err
	}
	// A statement of its own after the lock: under READ COMMITTED (store.DB.Tx)
	// it sees a removal the lock waited for.
	var totp bool
	var passkeys int
	if err := userFactors(g, userID).Row().Scan(&totp, &passkeys); err != nil {
		return err
	}
	last, err := s.lastSuperFactor(ctx, tx, userID, scope, operation, totp, passkeys)
	if err != nil || !last {
		return err
	}
	return s.verificationRequired(ctx, tx, userID, scope, operation)
}

// lockUserIfAny locks the row of userID: unlike lockUser, a user without a
// row is no error.
func lockUserIfAny(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.User{}).Select("1").Where("id = ?", userID).Clauses(store.ForUpdate()).Find(&[]int{})
}

// verificationRequired is the 403 that tells the console what to verify:
// the scope, the operation to bind the proof to and the methods.
func (s *Service) verificationRequired(ctx context.Context, q *gorm.DB, userID int64, scope, operation string) error {
	methods, err := s.stepUpMethods(ctx, q, userID, scope, operation)
	if err != nil {
		return err
	}
	return core.ErrVerificationRequired.WithMessage(t(ctx, "verify it's you to continue", "请先完成二次验证")).WithDetails(map[string]any{
		"scope": scope, "context": operation, "password_required": methods.PasswordRequired, "methods": methods.Methods,
	})
}
