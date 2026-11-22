package iam

import (
	"context"
	"log/slog"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam/tokenstore"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// How a login session started.
const (
	loginMethodPassword  = "password"
	loginMethodTwoFactor = "two_factor"
	loginMethodPasskey   = "passkey"
)

// LoginSession is one signed-in browser or device: a session in Valkey.
type LoginSession struct {
	ID         string    `json:"id"`
	Method     string    `json:"method"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Current    bool      `json:"current"`
}

type sessionMeta struct {
	method, ip, userAgent string
}

type sessionMetaKey struct{}

func sessionContext(c *gin.Context, method string) context.Context {
	return context.WithValue(c.Request.Context(), sessionMetaKey{}, sessionMeta{method: method, ip: c.ClientIP(), userAgent: clipUserAgent(c.Request.UserAgent())})
}

// clipUserAgent is ua as a session keeps it: 512 characters at most.
func clipUserAgent(ua string) string {
	if utf8.RuneCountInString(ua) > 512 {
		return string([]rune(ua)[:512])
	}
	return ua
}

// usedFrom is where the request using sess comes from (core.ClientIP,
// core.UserAgent), what sess last recorded where the request does not say.
func usedFrom(ctx context.Context, sess tokenstore.Session) (ip, userAgent string) {
	ip, userAgent = core.ClientIP(ctx), clipUserAgent(core.UserAgent(ctx))
	if ip == "" {
		ip = sess.IP
	}
	if userAgent == "" {
		userAgent = sess.UserAgent
	}
	return ip, userAgent
}

func withLoginMethod(ctx context.Context, method string) context.Context {
	meta, _ := ctx.Value(sessionMetaKey{}).(sessionMeta)
	meta.method = method
	return context.WithValue(ctx, sessionMetaKey{}, meta)
}

// auditSuperuserLogin records a new login of a superuser in tx, with how it
// started and from where, and logs a warning.
func (s *Service) auditSuperuserLogin(ctx context.Context, tx *gorm.DB, userID int64) error {
	su, err := s.authz.IsSuperuser(ctx, tx, userID)
	if err != nil || !su {
		return err
	}
	meta, _ := ctx.Value(sessionMetaKey{}).(sessionMeta)
	if err := auditSelf(audit.WithClientIP(ctx, meta.ip), tx, userID, "auth.superadmin_login",
		map[string]any{"method": meta.method, "ip": meta.ip}); err != nil {
		return err
	}
	slog.WarnContext(ctx, "iam: super admin login", "user_id", userID, "method", meta.method, "ip", meta.ip)
	return nil
}

// Sessions lists the user's live sessions, most recently used first.
// currentSession marks the caller's own.
func (s *Service) Sessions(ctx context.Context, userID int64, currentSession string) ([]LoginSession, error) {
	if s.sessions == nil {
		return nil, tokenstore.ErrUnavailable
	}
	list, err := s.sessions.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	// A session a bump ended may still be in Valkey (its cleanup failed,
	// the server CLI): it is not listed, as it is not accepted.
	_, version, err := s.isActiveWithVersion(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]LoginSession, 0, len(list))
	for _, o := range list {
		if o.TokenVersion < version {
			continue
		}
		out = append(out, LoginSession{ID: o.ID, Method: o.Method, IP: o.IP, UserAgent: o.UserAgent,
			CreatedAt: o.CreatedAt, LastUsedAt: o.LastSeen, Current: o.ID == currentSession})
	}
	return out, nil
}

// RevokeSession ends one of the user's sessions at once.
func (s *Service) RevokeSession(ctx context.Context, userID int64, id string) error {
	return s.changeSessions(ctx, userID, oneSession(id), func(tx *gorm.DB) error {
		live, err := s.hasSession(ctx, store.G(ctx, tx), userID, id)
		if err != nil {
			return err
		}
		if !live {
			return core.ErrNotFound
		}
		return auditSelf(ctx, tx, userID, "user.session.revoke", nil)
	})
}

// hasSession reports whether id is a live session of userID that no bump
// ended. One a bump ended may still be in Valkey (its cleanup failed, the
// server CLI); it is not there, as the sessions list and authentication
// tell.
func (s *Service) hasSession(ctx context.Context, g *gorm.DB, userID int64, id string) (bool, error) {
	if s.sessions == nil {
		return false, tokenstore.ErrUnavailable
	}
	var version int
	if err := userTokenVersion(g, userID).Row().Scan(&version); err != nil {
		return false, err
	}
	return s.sessions.Has(ctx, userID, id, version)
}

// RevokeOtherSessions ends every session of the user but keepSession, the
// caller's, and then gives that one a new token in place of presented (the
// one the caller sent), which it returns: a copy of the caller's token,
// which the sessions list cannot tell from the caller, stops working with
// the grace of a rotation, as after a step-up. "" when there is no new
// token: the caller's session ended meanwhile, or another tab's rotation
// left presented behind (that tab's token is the one to use). The
// revocation commits first. When Valkey cannot be reached for the
// rotation, the caller's session ends too, as every session does when the
// revocation itself cannot reach it (a stampless bump): a copy of its token
// does not outlive the request.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, keepSession, presented string) (string, error) {
	err := s.changeSessions(ctx, userID, otherSessions(keepSession), func(tx *gorm.DB) error {
		return auditSelf(ctx, tx, userID, "user.session.revoke_others", nil)
	})
	if err != nil {
		return "", err
	}
	token, err := s.rotateSession(ctx, userID, keepSession, presented)
	if !tokenstore.IsUnavailable(err) {
		return token, err
	}
	slog.WarnContext(ctx, "iam: the caller's session not renewed, a token_version bump ends it", "user_id", userID, "err", err)
	return "", s.changeSessions(ctx, userID, allSessions, nil)
}

// sessionsToEnd picks the sessions a change ends: only that one, or every
// one but keep (empty fields select all).
type sessionsToEnd struct{ only, keep string }

var allSessions = sessionsToEnd{}

func otherSessions(keep string) sessionsToEnd { return sessionsToEnd{keep: keep} }

func oneSession(id string) sessionsToEnd { return sessionsToEnd{only: id} }

// Sessions end in Valkey, and through the token_version stamp each one
// carries (VerifySession rejects one below the row): a bump ends every
// session stamped below it, without Valkey. A bump that restamps the
// sessions it keeps steps by 1. A bump that does not reach Valkey steps by
// stamplessStep: a restamp whose transaction rolled back leaves a stamp one
// ahead of the row (never more: each restamp is to the row plus 1, once per
// transaction, keepingBump), and every session must be passed. Since a
// restamp only moves the sessions stamped at the row or above
// (tokenstore.Bump), a session any bump ended stays ended.
const stamplessStep = 2

// endAllSessions ends every session of userID in tx, with PostgreSQL alone:
// a stampless bump rejects them all. Valkey being down thus does not stop
// an operator from disabling a stolen account. The caller holds the user
// row lock and calls sessionsEnded after the commit, which removes the
// sessions from Valkey.
func (s *Service) endAllSessions(ctx context.Context, tx *gorm.DB, userID int64) error {
	return bumpTokenVersion(store.G(ctx, tx), userID, stamplessStep).Error
}

// endSomeSessions ends the sessions `which` selects and keeps the others,
// in tx: one session alone, with no bump (endOneSession); every one but
// one through a bump that keeps it (keepingBump). The caller calls
// sessionsChanged after the commit, and calls this once per transaction
// (keepingBump).
func (s *Service) endSomeSessions(ctx context.Context, tx *gorm.DB, userID int64, which sessionsToEnd) error {
	g := store.G(ctx, tx)
	if which.only != "" {
		return s.endOneSession(ctx, g, userID, which.only)
	}
	return s.keepingBump(ctx, g, userID, which.keep)
}

// endOneSession ends the session id of userID, and nothing else. When
// Valkey cannot be reached, a stampless bump ends it with every other
// session: the change goes through, erring on the safe side.
func (s *Service) endOneSession(ctx context.Context, g *gorm.DB, userID int64, id string) error {
	return s.reviseOrBump(ctx, g, userID, tokenstore.Selection{Only: id}, tokenstore.Bump{})
}

// keepingBump ends every session of userID but keep, with a bump by 1 that
// stamps keep (and any session issued since) with the new version. The
// bump is what lets a revocation Valkey runs late, after the caller gave up
// on it and fell back to a stampless bump, tell the sessions issued after
// the call from those it was meant to end (tokenstore.SessionStore.Revoke):
// without one, a late run would end the logins since. When Valkey cannot
// be reached the bump goes on to a stampless one, which ends keep too.
//
// The restamp is safe on two conditions. The version it starts from is the
// latest committed one, read here under the row lock, so no login slips a
// session in with the old version and no restamp starts below a bump that
// committed meanwhile. And a transaction restamps once at most: a stamp is
// then never more than one ahead of the row, even when the transaction
// rolls back, which stamplessStep relies on; a second restamp in the same
// transaction would stamp two ahead. endSomeSessions is its only caller.
func (s *Service) keepingBump(ctx context.Context, g *gorm.DB, userID int64, keep string) error {
	if err := store.RequireTx(g); err != nil {
		return err
	}
	var from int
	if err := userTokenVersion(g, userID).Clauses(store.ForUpdate()).Row().Scan(&from); err != nil {
		return err
	}
	if err := bumpTokenVersion(g, userID, 1).Error; err != nil {
		return err
	}
	return s.reviseOrBump(ctx, g, userID, tokenstore.Selection{Keep: keep}, tokenstore.Bump{From: from, To: from + 1})
}

// reviseOrBump runs a tokenstore Revoke, whose bump b the caller has
// already made in tx. When Valkey cannot be reached (or there is no store:
// the server CLI) it bumps token_version on to a stampless bump in all:
// stamplessStep less what b already stepped.
func (s *Service) reviseOrBump(ctx context.Context, g *gorm.DB, userID int64, sel tokenstore.Selection, b tokenstore.Bump) error {
	if s.sessions != nil {
		err := s.sessions.Revoke(ctx, userID, sel, b)
		if !tokenstore.IsUnavailable(err) {
			return err
		}
		slog.WarnContext(ctx, "iam: sessions not revised, a token_version bump ends them", "user_id", userID, "err", err)
	}
	return bumpTokenVersion(g, userID, stamplessStep-(b.To-b.From)).Error
}

// bumpTokenVersion ends every session of userID stamped below the new
// version, stepping it by step.
func bumpTokenVersion(db *gorm.DB, userID int64, step int) *gorm.DB {
	return db.Model(&model.User{}).Where("id = ?", userID).Update("token_version", gorm.Expr("token_version + ?", step))
}

// changeSessions runs fn (optional) and ends the sessions `which` selects
// in one transaction holding the user row lock, then announces the change.
// It takes no authz lock; a caller that needs one opens its own transaction
// in the authz -> user lock order and calls endAllSessions and
// sessionsEnded itself.
func (s *Service) changeSessions(ctx context.Context, userID int64, which sessionsToEnd, fn func(tx *gorm.DB) error) error {
	all := which == allSessions
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := lockUserRow(ctx, tx, userID); err != nil {
			return err
		}
		if fn != nil {
			if err := fn(tx); err != nil {
				return err
			}
		}
		if all {
			return s.endAllSessions(ctx, tx, userID)
		}
		return s.endSomeSessions(ctx, tx, userID, which)
	})
	if err != nil {
		return err
	}
	if all {
		s.sessionsEnded(ctx, userID)
	} else {
		s.sessionsChanged(ctx, userID)
	}
	return nil
}

// sessionsEnded follows the commit of endAllSessions of uids:
// sessionsChanged, then the sessions the bump ended leave Valkey.
// Only those stamped below the version committed go: a login since the
// commit stays. The bump already rejects them, so this is cleanup: a
// failure is logged, and the sessions go when next presented, or idle out.
func (s *Service) sessionsEnded(ctx context.Context, uids ...int64) {
	s.sessionsChanged(ctx, uids...)
	if s.sessions == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, uid := range uids {
		var tv int
		err := userTokenVersion(store.G(ctx, s.db.Gorm), uid).Row().Scan(&tv)
		if err == nil {
			err = s.sessions.Prune(ctx, uid, tv)
		}
		if err != nil {
			slog.ErrorContext(ctx, "iam: removing ended sessions failed, token_version rejects them", "user_id", uid, "err", err)
		}
	}
}

// sessionsChanged drops the cached status of uids on this node and asks the
// other nodes to do the same (iam:user:changed). A lost message is covered by
// the reconnect reset and statusCacheTTL.
func (s *Service) sessionsChanged(ctx context.Context, uids ...int64) {
	for _, uid := range uids {
		s.forgetStatus(uid)
		if s.bus == nil {
			continue
		}
		if err := s.bus.Publish(ctx, core.ChannelUserSessionsChanged, []byte(strconv.FormatInt(uid, 10))); err != nil {
			slog.WarnContext(ctx, "iam: publish iam:user:changed failed", "user_id", uid, "err", err)
		}
	}
}

// currentSession is the caller's login session (core.Session).
func (s *Service) currentSession(c *gin.Context) string {
	return core.Session(c.Request.Context())
}

// UpdateProfile changes what the user may change about themselves.
func (s *Service) UpdateProfile(ctx context.Context, userID int64, displayName string) error {
	displayName, f := normalizeDisplayName(displayName)
	if f != nil {
		return core.InvalidFields(*f)
	}
	return s.db.Tx(ctx, func(tx *gorm.DB) error {
		if err := setLiveProfile(store.G(ctx, tx), userID, displayName).Error; err != nil {
			return err
		}
		return auditSelf(ctx, tx, userID, "user.profile.update", map[string]any{"display_name": displayName})
	})
}

// setLiveProfile changes the display name of the undeleted user userID.
func setLiveProfile(db *gorm.DB, userID int64, displayName string) *gorm.DB {
	return liveUsers(db).Where("id = ?", userID).
		Updates(map[string]any{"display_name": displayName, "updated_at": gorm.Expr("now()")})
}
