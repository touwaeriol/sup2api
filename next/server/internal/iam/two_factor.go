package iam

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"strconv"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Two-factor enrolment and management (CONTRACTS §79): the codes
// themselves are checked in totp.go.

// TwoFactorStatus is the response of GET /me/two-factor.
type TwoFactorStatus struct {
	Enabled         bool       `json:"enabled"`
	EnabledAt       *time.Time `json:"enabled_at,omitempty"`
	BackupCodesLeft int        `json:"backup_codes_left"`
}

// TwoFactorSetup is the response of POST /me/two-factor/setup.
type TwoFactorSetup struct {
	FlowToken  string `json:"flow_token"`
	ExpiresIn  int64  `json:"expires_in"`
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	QRCode     string `json:"qr_code"` // data URL of a PNG
}

// TwoFactor returns the caller's two-factor state.
func (s *Service) TwoFactor(ctx context.Context, userID int64) (*TwoFactorStatus, error) {
	g := store.G(ctx, s.db.Gorm)
	var at time.Time
	err := totpEnabledAt(g, userID).Row().Scan(&at)
	if store.IsNoRows(err) {
		return &TwoFactorStatus{}, nil
	}
	if err != nil {
		return nil, err
	}
	var left int64
	err = backupCodesLeft(g, userID).Count(&left).Error
	return &TwoFactorStatus{Enabled: true, EnabledAt: &at, BackupCodesLeft: int(left)}, err
}

type totpSetupPayload struct {
	Sealed string `json:"sealed"`
}

// StartTwoFactor starts enrolment: the secret stays in a flow until a code
// from it proves the authenticator app has it. Adding a factor is a step-up
// operation (self.2fa.enable, verified by the route): a password alone must
// not add a factor that then passes every step-up of a passkey-only user.
func (s *Service) StartTwoFactor(ctx context.Context, userID int64) (*TwoFactorSetup, error) {
	email, err := s.enrolmentEmail(ctx, userID)
	if err != nil {
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: email, Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		return nil, err
	}
	sealed, err := s.sealTOTP(userID, key.Secret())
	if err != nil {
		return nil, err
	}
	token, err := s.createFlow(ctx, flowTOTPSetup, userID, totpSetupPayload{Sealed: base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		return nil, err
	}
	qr, err := qrCodeDataURL(key)
	if err != nil {
		return nil, err
	}
	return &TwoFactorSetup{FlowToken: token, ExpiresIn: flowTTLSeconds, Secret: key.Secret(), OTPAuthURL: key.URL(), QRCode: qr}, nil
}

// enrolmentEmail is the email of userID, about to enrol in two-factor:
// unauthenticated when the user is gone, a conflict when it has two-factor
// already.
func (s *Service) enrolmentEmail(ctx context.Context, userID int64) (string, error) {
	g := store.G(ctx, s.db.Gorm)
	var email string
	err := liveUser(g, userID, "email").Row().Scan(&email)
	if store.IsNoRows(err) {
		return "", core.ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	enabled, err := hasTwoFactor(g, userID)
	if err != nil {
		return "", err
	}
	if enabled {
		return "", core.ErrConflict.WithMessage("two-factor authentication is already enabled")
	}
	return email, nil
}

// qrCodeDataURL is the QR code of key, as the data URL of a PNG.
func qrCodeDataURL(key *otp.Key) (string, error) {
	img, err := key.Image(240, 240)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// EnableTwoFactor finishes enrolment with a code from the new secret and
// returns the backup codes. A wrong code keeps the flow, so it can be retyped
// until the flow expires.
func (s *Service) EnableTwoFactor(ctx context.Context, userID int64, flowToken, code string) ([]string, error) {
	sealed, step, err := s.checkSetupCode(ctx, userID, flowToken, code)
	if err != nil {
		return nil, err
	}
	var codes []string
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockUserRow(ctx, tx, userID); err != nil {
			return err
		}
		res := insertTOTP(store.G(ctx, tx), userID, sealed, step)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return core.ErrConflict.WithMessage("two-factor authentication is already enabled")
		}
		var out totpSetupPayload
		if _, err := s.takeFlow(ctx, flowToken, flowTOTPSetup, userID, &out); err != nil {
			return err
		}
		if codes, err = s.issueBackupCodes(ctx, tx, userID); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, userID, "user.two_factor.enable", "user", strconv.FormatInt(userID, 10), nil)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// checkSetupCode checks code against the secret of the StartTwoFactor flow,
// which it leaves in place, and returns the sealed secret and the code's
// step.
func (s *Service) checkSetupCode(ctx context.Context, userID int64, flowToken, code string) ([]byte, int64, error) {
	var payload totpSetupPayload
	if _, err := s.peekFlow(ctx, flowToken, flowTOTPSetup, userID, &payload); err != nil {
		return nil, 0, err
	}
	sealed, err := base64.StdEncoding.DecodeString(payload.Sealed)
	if err != nil {
		return nil, 0, err
	}
	secret, err := s.openTOTP(userID, sealed)
	if err != nil {
		return nil, 0, err
	}
	step, ok := totpMatch(secret, code, 0, time.Now())
	if !ok {
		return nil, 0, errBadSecondFactor
	}
	return sealed, step, nil
}

// insertTOTP enables two-factor for userID with a sealed secret and the
// step of the code that proved it, unless it is.
func insertTOTP(db *gorm.DB, userID int64, sealed []byte, lastStep int64) *gorm.DB {
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).
		Create(&enabledTOTP{UserID: userID, SecretEnc: sealed, LastStep: lastStep})
}

// enabledTOTP is a new user_totp row; the other columns are their
// defaults, and nothing is read back.
type enabledTOTP struct {
	UserID    int64       `gorm:"column:user_id"`
	SecretEnc store.Bytes `gorm:"column:secret_enc"`
	LastStep  int64       `gorm:"column:last_step"`
}

func (enabledTOTP) TableName() string { return model.UserTOTP{}.TableName() }

// DisableTwoFactor turns the factor off; the route has verified the caller
// (step-up self.2fa.disable).
func (s *Service) DisableTwoFactor(ctx context.Context, userID int64) error {
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockUserRow(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.keepLastFactor(ctx, tx, userID, core.StepUpSelf2FADisable); err != nil {
			return err
		}
		if err := requireTwoFactor(ctx, tx, userID); err != nil {
			return err
		}
		if err := removeTwoFactor(ctx, tx, userID); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, userID, "user.two_factor.disable", "user", strconv.FormatInt(userID, 10), nil)
	})
}

// RegenerateBackupCodes replaces the backup codes; the route has verified
// the caller (step-up self.2fa.backup_codes).
func (s *Service) RegenerateBackupCodes(ctx context.Context, userID int64) ([]string, error) {
	var codes []string
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockUserRow(ctx, tx, userID); err != nil {
			return err
		}
		if err := requireTwoFactor(ctx, tx, userID); err != nil {
			return err
		}
		var err error
		if codes, err = s.issueBackupCodes(ctx, tx, userID); err != nil {
			return err
		}
		return audit.Audit(ctx, tx, userID, "user.two_factor.backup_codes", "user", strconv.FormatInt(userID, 10), nil)
	})
	return codes, err
}

// requireTwoFactor fails with conflict unless userID has two-factor enabled.
func requireTwoFactor(ctx context.Context, tx *gorm.DB, userID int64) error {
	enabled, err := hasTwoFactor(store.G(ctx, tx), userID)
	if err != nil {
		return err
	}
	if !enabled {
		return core.ErrConflict.WithMessage("two-factor authentication is not enabled")
	}
	return nil
}

func removeTwoFactor(ctx context.Context, tx *gorm.DB, userID int64) error {
	g := store.G(ctx, tx)
	if err := deleteBackupCodes(g, userID).Error; err != nil {
		return err
	}
	return deleteTOTP(g, userID).Error
}

// deletePasskeysOf removes every passkey of userID.
func deletePasskeysOf(db *gorm.DB, userID int64) *gorm.DB {
	return db.Where("user_id = ?", userID).Delete(&model.UserPasskey{})
}

// ResetSecurity is the operator's way back in for a user who lost their
// factors: it removes the user's two-factor and passkeys, only of a user the
// operator manages (authz.CanActOnUsers; oneself only as the super admin,
// FU6), and ends the user's sessions, which the lost device may hold (the
// caller's own survives a reset of oneself). Over HTTP the route has
// verified the caller (step-up user.security.reset), the super admin
// resetting its own factors included.
func (s *Service) ResetSecurity(ctx context.Context, actorID, targetID int64) error {
	// SEC-H2: removing the factors of a user one does not manage would leave
	// that account one password away from takeover.
	if err := s.authz.CanActOnUser(ctx, actorID, core.ActOnIdentity, targetID); err != nil {
		return err
	}
	which := allSessions
	if actorID == targetID {
		which = otherSessions(core.Session(ctx))
	}
	return s.changeSessions(ctx, targetID, which, func(tx *gorm.DB) error {
		if err := removeTwoFactor(ctx, tx, targetID); err != nil {
			return err
		}
		if err := deletePasskeysOf(store.G(ctx, tx), targetID).Error; err != nil {
			return err
		}
		return audit.Audit(ctx, tx, actorID, "user.security.reset", "user", strconv.FormatInt(targetID, 10), nil)
	})
}
