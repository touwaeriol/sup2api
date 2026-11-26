package testutil

import (
	"context"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// SessionUser reads a test console token "<prefix><uid>" as a session of
// user uid, for the core.TokenVerifier fakes. Any other token is
// unauthenticated, as iam answers a token it does not know: a plain error
// would be a server error, which httpapi.Authenticate passes through.
func SessionUser(token, prefix string) (int64, string, error) {
	uid, err := strconv.ParseInt(strings.TrimPrefix(token, prefix), 10, 64)
	if err != nil {
		return 0, "", core.ErrUnauthenticated
	}
	return uid, "", nil
}

// Tokens is a core.TokenVerifier fake: a bearer token "<uid>" is a session
// of user uid (SessionUser).
type Tokens struct{}

func (Tokens) VerifySession(_ context.Context, tok string) (int64, string, error) {
	return SessionUser(tok, "")
}

// Perms is a core.Authorizer fake holding each user's permissions; it lets
// every grant through.
type Perms map[int64][]string

func (p Perms) Can(ctx context.Context, uid int64, key string) (bool, error) {
	set, err := p.PermissionSet(ctx, uid)
	return set.Has(key), err
}

func (p Perms) PermissionSet(_ context.Context, uid int64) (core.PermissionSet, error) {
	return core.PermissionSetOf(p[uid]...), nil
}

func (Perms) CanGrant(context.Context, int64, []string) error { return nil }
