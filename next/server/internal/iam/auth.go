package iam

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// LoginResult is the response of a login: the session token (s2s_). When the
// account has two-factor authentication, a correct password answers with
// only TwoFactorRequired and FlowToken; POST /auth/login/2fa gives the
// token. A session has nothing to renew it with: it lives while it is used
// (CONTRACTS §100), and the console takes a 401 for its end.
type LoginResult struct {
	AccessToken       string `json:"access_token,omitempty"`
	ExpiresIn         int64  `json:"expires_in,omitempty"` // the session's idle timeout (a flow's lifetime), seconds
	User              *User  `json:"user,omitempty"`
	TwoFactorRequired bool   `json:"two_factor_required,omitempty"`
	FlowToken         string `json:"flow_token,omitempty"`
}

var (
	errBadCredentials = core.ErrUnauthenticated.WithMessage("invalid email or password")
	errUserDisabled   = core.ErrPermissionDenied.WithMessage("this account is disabled")
	// errNoUser: the user of a session change is gone (lockUserRow).
	errNoUser = core.ErrUnauthenticated.WithMessage("user not found")
)

// Login checks the password and issues tokens. Disabled users are rejected.
// ip is the client address used by the failed-login rate limit (CONTRACTS
// §14.2); while email+IP or IP is locked out the password is not checked.
func (s *Service) Login(ctx context.Context, email, password, ip string) (*LoginResult, error) {
	if wait := s.limiter.check(ctx, email, ip); wait > 0 {
		return nil, rateLimitedError(ctx, wait)
	}
	id, hash, status, err := s.checkLoginPassword(ctx, email, password, ip)
	if err != nil {
		return nil, err
	}
	s.limiter.reset(ctx, email, ip)
	if status != StatusActive {
		return nil, errUserDisabled
	}
	twoFactor, err := hasTwoFactor(store.G(ctx, s.db.Gorm), id)
	if err != nil {
		return nil, err
	}
	if twoFactor {
		return s.startLogin2FA(ctx, id, email, hash)
	}
	// A password change may have committed while bcrypt was running above:
	// the hash is checked again under the lock.
	return s.signIn(ctx, id, login{gone: errBadCredentials, disabled: errUserDisabled, check: func(_ *gorm.DB, current string) error {
		if current != hash {
			return errBadCredentials
		}
		return nil
	}})
}

// checkLoginPassword reads the user of email and checks password against
// its hash, and returns the user's id, hash and status. An unknown email or
// a wrong password counts towards the rate limit and is errBadCredentials;
// an unknown one burns comparable time.
func (s *Service) checkLoginPassword(ctx context.Context, email, password, ip string) (id int64, hash, status string, err error) {
	err = userByEmail(store.G(ctx, s.db.Gorm), strings.ToLower(strings.TrimSpace(email)), "id, password_hash, status").Row().Scan(&id, &hash, &status)
	if store.IsNoRows(err) {
		// Burn comparable time so response timing does not reveal accounts.
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		s.limiter.fail(ctx, email, ip)
		return 0, "", "", errBadCredentials
	}
	if err != nil {
		return 0, "", "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		s.limiter.fail(ctx, email, ip)
		return 0, "", "", errBadCredentials
	}
	return id, hash, status, nil
}

// startLogin2FA answers a correct password of a user with two-factor: the
// flow of the second step (LoginSecondFactor), with the password snapshot.
func (s *Service) startLogin2FA(ctx context.Context, userID int64, email, hash string) (*LoginResult, error) {
	token, err := s.createFlow(ctx, flowLogin2FA, userID, login2FAPayload{Email: limiterEmail(email), Password: tokenstore.Hash(hash)})
	if err != nil {
		return nil, err
	}
	return &LoginResult{TwoFactorRequired: true, FlowToken: token, ExpiresIn: flowTTLSeconds}, nil
}

// login is what a login path adds to signIn: the errors it answers a user
// gone since it began, or not active, with; check, its own checks (the
// password snapshot, the second factor, the passkey's use), given the
// password hash read under the lock; and done (optional), the last step,
// after the session is issued.
type login struct {
	gone, disabled error
	check          func(g *gorm.DB, passwordHash string) error
	done           func() error
}

// signIn ends every login path: in one transaction holding the user's row
// lock (the parent lock of every session change), the user must still be
// there and active and l.check pass; then the login is recorded, a session
// issued (issueTx) and l.done run. ctx carries how it started
// (withLoginMethod).
func (s *Service) signIn(ctx context.Context, userID int64, l login) (*LoginResult, error) {
	var result *LoginResult
	err := s.issueTx(ctx, func(is *issuer) error {
		g := store.G(ctx, is.tx)
		var hash, status string
		if err := lockLiveUser(g, userID, "password_hash, status").Row().Scan(&hash, &status); err != nil {
			if store.IsNoRows(err) {
				return l.gone
			}
			return err
		}
		if status != StatusActive {
			return l.disabled
		}
		if err := l.check(g, hash); err != nil {
			return err
		}
		if err := touchLastLogin(g, userID).Error; err != nil {
			return err
		}
		var err error
		if result, err = is.issue(ctx, userID); err != nil || l.done == nil {
			return err
		}
		return l.done()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// login2FAPayload is what a correct password leaves for the second step: the
// password snapshot, so a password change in between ends the login.
type login2FAPayload struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginSecondFactor finishes a password login with a TOTP or backup code. The
// flow survives a wrong code, under the per-user lockout and the login rate
// limit, and ends with the first correct one.
func (s *Service) LoginSecondFactor(ctx context.Context, flowToken, code, ip string) (*LoginResult, error) {
	var payload login2FAPayload
	userID, err := s.peekFlow(ctx, flowToken, flowLogin2FA, 0, &payload)
	if err != nil {
		return nil, err
	}
	if wait := s.limiter.check(ctx, payload.Email, ip); wait > 0 {
		return nil, rateLimitedError(ctx, wait)
	}
	result, err := s.signIn(withLoginMethod(ctx, loginMethodTwoFactor), userID, s.secondFactorLogin(ctx, userID, flowToken, code, payload.Password))
	if isBadSecondFactor(err) {
		s.limiter.fail(ctx, payload.Email, ip)
		if ferr := s.recordSecondFactorFailure(ctx, userID); ferr != nil {
			return nil, ferr
		}
	}
	if err != nil {
		return nil, err
	}
	s.limiter.reset(ctx, payload.Email, ip)
	return result, nil
}

// secondFactorLogin is the login of the second step of the flow flowToken:
// the password unchanged since the first (passwordHash, its snapshot), then
// a correct code; the flow is taken last.
func (s *Service) secondFactorLogin(ctx context.Context, userID int64, flowToken, code, passwordHash string) login {
	return login{
		gone: errBadCredentials, disabled: errUserDisabled,
		check: func(g *gorm.DB, hash string) error {
			if tokenstore.Hash(hash) != passwordHash {
				return errFlowExpired
			}
			return s.checkSecondFactor(ctx, g, userID, code)
		},
		// Taken last: a failure before leaves the flow for another try (a
		// flow in Valkey does not come back with a rollback).
		done: func() error {
			var used login2FAPayload
			_, err := s.takeFlow(ctx, flowToken, flowLogin2FA, userID, &used)
			return err
		},
	}
}

// newSessionID returns a random session id.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// issuer issues sessions in one transaction (issueTx).
type issuer struct {
	s  *Service
	tx *gorm.DB
	// stored is the session issued, which issueTx ends when the
	// transaction fails.
	stored *tokenstore.Session
}

// issueTx runs fn in a transaction, in which it issues sessions through
// the issuer it gets. A session lives in Valkey, which does not roll back
// with PostgreSQL: when the transaction fails after storing one, it is
// ended, so no session nobody holds stays on the user's list.
func (s *Service) issueTx(ctx context.Context, fn func(is *issuer) error) error {
	is := &issuer{s: s}
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		is.tx = tx
		return fn(is)
	})
	if err == nil || is.stored == nil {
		return err
	}
	uid := is.stored.UserID
	if rerr := s.sessions.Revoke(context.WithoutCancel(ctx), uid, tokenstore.Selection{Only: is.stored.ID}, tokenstore.Bump{}); rerr != nil {
		slog.WarnContext(ctx, "iam: ending a session of a failed login failed", "user_id", uid, "err", rerr)
	}
	return err
}

// issue signs userID in: a new session, stamped with the token_version
// read under the user's row lock, which the caller holds, so a revocation
// cannot slip in between. A superuser's login is audited. The session,
// stored in Valkey, is the last thing done before the commit.
func (is *issuer) issue(ctx context.Context, userID int64) (*LoginResult, error) {
	s, g := is.s, store.G(ctx, is.tx)
	if s.sessions == nil {
		return nil, tokenstore.ErrUnavailable
	}
	var tokenVersion int
	if err := userTokenVersion(g, userID).Row().Scan(&tokenVersion); err != nil {
		return nil, err
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	if err := s.auditSuperuserLogin(ctx, is.tx, userID); err != nil {
		return nil, err
	}
	user, err := s.loginUser(ctx, is.tx, userID)
	if err != nil {
		return nil, err
	}
	meta, _ := ctx.Value(sessionMetaKey{}).(sessionMeta)
	sess := tokenstore.Session{ID: id, UserID: userID, Method: meta.method, IP: meta.ip, UserAgent: meta.userAgent, TokenVersion: tokenVersion}
	token, err := s.sessions.Create(ctx, sess)
	if err != nil {
		return nil, err
	}
	is.stored = &sess
	return &LoginResult{AccessToken: token, ExpiresIn: int64(s.sessionIdle / time.Second), User: user}, nil
}

// loginUser is the user a login answers with: no balance or segment.
func (s *Service) loginUser(ctx context.Context, tx *gorm.DB, userID int64) (*User, error) {
	user, err := s.getUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	user.Balance = nil
	user.Segment = core.SegmentRef{}
	return user, nil
}

// userTokenVersion reads the token_version of userID, deleted or not.
func userTokenVersion(db *gorm.DB, userID int64) *gorm.DB {
	return db.Model(&model.User{}).Select("token_version").Where("id = ?", userID)
}

// Logout ends the caller's own session (the route takes sessions only),
// whether or not it is still there.
func (s *Service) Logout(ctx context.Context, userID int64) error {
	return s.changeSessions(ctx, userID, oneSession(core.Session(ctx)), nil)
}

// ChangePassword changes the caller's password and ends every other session
// at once but keepSession, the caller's own ("" through an access key:
// none). The route has verified the caller (step-up self.password.change:
// the password and a second factor).
func (s *Service) ChangePassword(ctx context.Context, userID int64, keepSession, newPassword string) error {
	if f := validatePassword("new_password", newPassword); f != nil {
		return core.InvalidFields(*f)
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cost)
	if err != nil {
		return err
	}
	return s.changeSessions(ctx, userID, otherSessions(keepSession), func(tx *gorm.DB) error {
		res := setLivePassword(store.G(ctx, tx), userID, string(newHash))
		if res.Error == nil && res.RowsAffected == 0 {
			return core.ErrUnauthenticated
		}
		return res.Error
	})
}

// setLivePassword changes the password hash of the undeleted user userID.
func setLivePassword(db *gorm.DB, userID int64, hash string) *gorm.DB {
	return liveUsers(db).Where("id = ?", userID).
		Updates(map[string]any{"password_hash": hash, "updated_at": gorm.Expr("now()")})
}

// Me is the response of GET /me.
type Me struct {
	ID          int64    `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	Superuser   bool     `json:"superuser"`
	// AuthzVersion is the authz version Permissions were read at; a larger
	// X-Authz-Version on a later response means they may have changed.
	AuthzVersion int64 `json:"authz_version"`
}

// GetMe returns the caller's profile and effective permissions (superusers
// get every active permission).
func (s *Service) GetMe(ctx context.Context, userID int64) (*Me, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	set, err := s.authz.PermissionSet(ctx, userID)
	if err != nil {
		return nil, err
	}
	me := &Me{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Roles: u.Roles, Superuser: set.Superuser, AuthzVersion: set.Version}
	if set.Superuser {
		if me.Permissions, err = s.authz.ActivePermissionKeys(ctx); err != nil {
			return nil, err
		}
	} else {
		// Canonical keys (account:read:own, account:update:group/12…).
		me.Permissions = set.KeyList()
	}
	sort.Strings(me.Permissions)
	return me, nil
}
