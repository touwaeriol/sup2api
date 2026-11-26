package iam

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/runtime-contract/gormpg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

const (
	totpPeriod        = 30
	totpSkewSteps     = 1 // one step either side, for a clock that is off
	totpMaxFailures   = 5
	totpLockout       = 5 * time.Minute
	backupCodeCount   = 10
	backupCodeLetters = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I
	totpIssuer        = "sup2api"
)

var errBadSecondFactor = core.InvalidFields(core.FieldError{Field: "code", Code: "incorrect", Message: "incorrect verification code"})

func totpAAD(userID int64) []byte { return []byte("user_totp:" + strconv.FormatInt(userID, 10)) }

func (s *Service) sealTOTP(userID int64, secret string) ([]byte, error) {
	if s.cipher == nil {
		return nil, core.ErrInternal.WithMessage("two-factor authentication needs the master key")
	}
	return s.cipher.Encrypt([]byte(secret), totpAAD(userID))
}

func (s *Service) openTOTP(userID int64, sealed []byte) (string, error) {
	if s.cipher == nil {
		return "", core.ErrInternal.WithMessage("two-factor authentication needs the master key")
	}
	plain, err := s.cipher.Decrypt(sealed, totpAAD(userID))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// totpMatch returns the time step the code is valid for and newer than
// lastStep. The steps are checked oldest first, so a code cannot be replayed
// by moving forward within the skew.
func totpMatch(secret, code string, lastStep int64, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for step := current - totpSkewSteps; step <= current+totpSkewSteps; step++ {
		if step <= lastStep {
			continue
		}
		want, err := hotp.GenerateCodeCustom(secret, uint64(step), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// normalizeBackupCode accepts a code typed with or without its dash, any case.
func normalizeBackupCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
}

func newBackupCode() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = backupCodeLetters[int(b[i])%len(backupCodeLetters)]
	}
	return string(b[:5]) + "-" + string(b[5:]), nil
}

// issueBackupCodes replaces the user's backup codes and returns the new ones,
// which are shown once and kept only as hashes.
func (s *Service) issueBackupCodes(ctx context.Context, tx *gorm.DB, userID int64) ([]string, error) {
	g := store.G(ctx, tx)
	if err := deleteBackupCodes(g, userID).Error; err != nil {
		return nil, err
	}
	codes := make([]string, backupCodeCount)
	for i := range codes {
		code, err := newBackupCode()
		if err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(normalizeBackupCode(code)), s.cost)
		if err != nil {
			return nil, err
		}
		if err := insertBackupCode(g, userID, string(hash)).Error; err != nil {
			return nil, err
		}
		codes[i] = code
	}
	return codes, nil
}

// insertBackupCode stores the hash of a backup code of userID.
func insertBackupCode(db *gorm.DB, userID int64, hash string) *gorm.DB {
	return db.Create(&backupCode{UserID: userID, CodeHash: hash})
}

// backupCode is a new user_totp_backup_codes row; the other columns are
// their defaults, and nothing is read back.
type backupCode struct {
	UserID   int64  `gorm:"column:user_id"`
	CodeHash string `gorm:"column:code_hash"`
}

func (backupCode) TableName() string { return model.UserTOTPBackupCode{}.TableName() }

// deleteBackupCodes removes the backup codes of userID.
func deleteBackupCodes(db *gorm.DB, userID int64) *gorm.DB {
	return db.Where("user_id = ?", userID).Delete(&model.UserTOTPBackupCode{})
}

// useBackupCode marks the backup code id used.
func useBackupCode(db *gorm.DB, id int64) *gorm.DB {
	return db.Model(&model.UserTOTPBackupCode{}).Where("id = ?", id).Update("used_at", gorm.Expr("now()"))
}

// totpOf is the two-factor row of userID.
func totpOf(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.UserTOTP{}).Where("user_id = ?", userID)
}

// totpEnabledAt reads when userID enabled two-factor.
func totpEnabledAt(db *gorm.DB, userID int64) *gorm.DB {
	return totpOf(db, userID).Select("enabled_at")
}

// deleteTOTP turns two-factor off for userID.
func deleteTOTP(db *gorm.DB, userID int64) *gorm.DB {
	return totpOf(db, userID).Delete(&model.UserTOTP{})
}

// hasTwoFactor reports whether userID has two-factor enabled.
func hasTwoFactor(db *gorm.DB, userID int64) (bool, error) {
	return gormpg.Exists(db, totpOf(db, userID))
}

// totpPassed records a correct code: the step it was of (lastStep, a TOTP
// code only) and no failures or lockout.
func totpPassed(db *gorm.DB, userID int64, lastStep *int64) *gorm.DB {
	set := map[string]any{"failed_attempts": 0, "locked_until": nil}
	store.SetIf(set, "last_step", lastStep)
	return totpOf(db, userID).Updates(set)
}

// totpFailed counts a wrong code; the totpMaxFailures-th locks the factor
// until lockUntil and starts the count again.
func totpFailed(db *gorm.DB, userID int64, lockUntil time.Time) *gorm.DB {
	return totpOf(db, userID).Updates(map[string]any{
		"failed_attempts": gorm.Expr("CASE WHEN failed_attempts + 1 >= ? THEN 0 ELSE failed_attempts + 1 END", totpMaxFailures),
		"locked_until":    gorm.Expr("CASE WHEN failed_attempts + 1 >= ? THEN ?::timestamptz ELSE locked_until END", totpMaxFailures, lockUntil),
	})
}

// checkSecondFactor verifies a TOTP or backup code inside tx, which must hold
// the user row. A correct code is used up. Failures count towards a lockout
// and are written by the caller with recordSecondFactorFailure, after the
// transaction, so they survive its rollback.
func (s *Service) checkSecondFactor(ctx context.Context, tx *gorm.DB, userID int64, code string) error {
	secret, lastStep, err := s.lockedTOTPSecret(ctx, tx, userID)
	if err != nil {
		return err
	}
	g := store.G(ctx, tx)
	if step, ok := totpMatch(secret, code, lastStep, time.Now()); ok {
		return totpPassed(g, userID, &step).Error
	}
	used, err := useMatchingBackupCode(g, userID, code)
	if err != nil {
		return err
	}
	if used {
		return totpPassed(g, userID, nil).Error
	}
	return errBadSecondFactor
}

// lockedTOTPSecret locks the two-factor row of userID (lockTOTP) in tx and
// returns its secret and the step of the last code used: a conflict without
// two-factor, a rate limit while the factor is locked out.
func (s *Service) lockedTOTPSecret(ctx context.Context, tx *gorm.DB, userID int64) (string, int64, error) {
	if err := store.RequireTx(tx); err != nil {
		return "", 0, err
	}
	var sealed []byte
	var lastStep int64
	var lockedUntil *time.Time
	err := lockTOTP(store.G(ctx, tx), userID).Row().Scan(&sealed, &lastStep, &lockedUntil)
	if store.IsNoRows(err) {
		return "", 0, core.ErrConflict.WithMessage("two-factor authentication is not enabled")
	}
	if err != nil {
		return "", 0, err
	}
	if lockedUntil != nil && lockedUntil.After(time.Now()) {
		return "", 0, loginLocked(ctx, time.Until(*lockedUntil))
	}
	secret, err := s.openTOTP(userID, sealed)
	return secret, lastStep, err
}

// useMatchingBackupCode uses up the unused backup code of userID that code
// is, and reports whether there was one.
func useMatchingBackupCode(g *gorm.DB, userID int64, code string) (bool, error) {
	normalized := normalizeBackupCode(code)
	if len(normalized) != 10 {
		return false, nil
	}
	var unused []model.UserTOTPBackupCode
	if err := unusedBackupCodes(g, userID).Find(&unused).Error; err != nil {
		return false, err
	}
	var matched int64
	for _, c := range unused {
		if matched == 0 && bcrypt.CompareHashAndPassword([]byte(c.CodeHash), []byte(normalized)) == nil {
			matched = c.ID
		}
	}
	if matched == 0 {
		return false, nil
	}
	return true, useBackupCode(g, matched).Error
}

// lockTOTP reads the two-factor state of userID, locked until the
// transaction ends.
func lockTOTP(db *gorm.DB, userID int64) *gorm.DB {
	return totpOf(db, userID).Select("secret_enc, last_step, locked_until").Clauses(store.ForUpdate())
}

// backupCodesLeft are the backup codes of userID still to be used.
func backupCodesLeft(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.UserTOTPBackupCode{}).Where("user_id = ? AND used_at IS NULL", userID)
}

// unusedBackupCodes reads the id and hash of backupCodesLeft.
func unusedBackupCodes(db *gorm.DB, userID int64) *gorm.DB {
	return backupCodesLeft(db, userID).Select("id, code_hash")
}

// recordSecondFactorFailure counts a wrong code; the fifth locks the factor
// for five minutes. It runs outside the transaction the check failed in.
func (s *Service) recordSecondFactorFailure(ctx context.Context, userID int64) error {
	return totpFailed(store.G(ctx, s.db.Gorm), userID, time.Now().Add(totpLockout)).Error
}

func isBadSecondFactor(err error) bool { return errors.Is(err, errBadSecondFactor) }
