package core

import (
	"context"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
)

// ============================================================ identity & authz (owner: A core-data)

// TokenVerifier validates console session tokens (s2s_, CONTRACTS §100):
// the user and the login session of the token.
type TokenVerifier interface {
	VerifySession(ctx context.Context, token string) (userID int64, session string, err error)
}

// PermissionSet is a user's effective permissions at a given authz version.
// In the grant view (what roles carry, authz.RolesPermissions) it also holds
// the roles the holder may assign: Assignable never holds a superuser role,
// and AssignCustom ("and every custom role") is already expanded into it.
//
// Keys holds the unscoped keys, a scope of everything included (canonical,
// perm.Canonical); Scoped the narrower scopes per action (CONTRACTS §21).
// Build sets with Add or PermissionSetOf, which canonicalize.
//
// A user's set as the Authorizer resolves it (PermissionSet) is decided:
// its lists are what Casbin lists for the user, and Has is Casbin's
// Enforce (CONTRACTS §99.3). A set built from keys (a role's grants, an
// access key's list) compares keys.
type PermissionSet struct {
	Superuser    bool
	Keys         map[string]struct{}
	Scoped       map[perm.Key][]perm.Scope
	Assignable   map[string]struct{}
	AssignCustom bool
	Version      int64
	// decide answers Has for a decided set (Decided), nil for one built
	// from keys.
	decide func(string) bool
}

// Decided returns s answering Has with decide: the authorizer's decision
// for the holder, whose grants s lists.
func (s PermissionSet) Decided(decide func(string) bool) PermissionSet {
	s.decide = decide
	return s
}

// Add grants k (any spelling: trailing wildcards included). A key that does
// not parse grants nothing.
func (s *PermissionSet) Add(k string) {
	p, err := perm.Parse(k)
	if err != nil {
		return
	}
	s.add(p)
}

func (s *PermissionSet) add(p perm.Pattern) {
	if p.Scope.All {
		if s.Keys == nil {
			s.Keys = map[string]struct{}{}
		}
		s.Keys[string(p.Action)] = struct{}{}
		return
	}
	if s.Scoped == nil {
		s.Scoped = map[perm.Key][]perm.Scope{}
	}
	if !slices.Contains(s.Scoped[p.Action], p.Scope) {
		s.Scoped[p.Action] = append(s.Scoped[p.Action], p.Scope)
	}
}

// Has reports whether the set implies k: for a decided set, the
// authorizer's decision; else a superuser, k held unscoped, or a held scope
// implying k's. An unscoped k asks for every row, so Has("account:read") is
// false for a holder of account:read:own only.
func (s PermissionSet) Has(k string) bool {
	if s.decide != nil {
		return s.decide(k)
	}
	return s.lists(k)
}

// lists reports whether the lists of the set imply k (Has of a set built
// from keys).
func (s PermissionSet) lists(k string) bool {
	if s.Superuser {
		return true
	}
	if _, ok := s.Keys[k]; ok {
		return true
	}
	p, err := perm.Parse(k)
	if err != nil {
		return false
	}
	if _, ok := s.Keys[string(p.Action)]; ok || p.Scope.All {
		return ok
	}
	for _, sc := range s.Scoped[p.Action] {
		if perm.Implies(perm.Pattern{Action: p.Action, Scope: sc}, p) {
			return true
		}
	}
	return false
}

// HasAction reports whether the set holds action with any scope: what a
// route of rows the caller may reach some of asks (Router.PermScoped).
func (s PermissionSet) HasAction(action perm.Key) bool {
	_, ok := s.Keys[string(action)]
	return s.Superuser || ok || len(s.Scoped[action]) > 0
}

// DataScope is the rows uid, the holder, reaches with action.
func (s PermissionSet) DataScope(uid int64, action perm.Key) DataScope {
	if _, ok := s.Keys[string(action)]; ok || s.Superuser {
		return AllScope
	}
	return Compile(uid, s.Scoped[action])
}

// ScopeGroups lists the groups its scopes name (group/G, group/G/account/X)
// under any action, sorted: the groups whose names the holder's own grants
// show (lookup's entity.named).
func (s PermissionSet) ScopeGroups() []int64 {
	var out []int64
	for _, scopes := range s.Scoped {
		for _, sc := range scopes {
			if sc.Group > 0 {
				out = append(out, sc.Group)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// KeyList lists the canonical keys of the set, sorted (not the superuser
// flag), without the scopes of an action it holds for every row.
func (s PermissionSet) KeyList() []string {
	out := make([]string, 0, len(s.Keys))
	for k := range s.Keys {
		out = append(out, k)
	}
	for a, scopes := range s.Scoped {
		if _, all := s.Keys[string(a)]; all {
			continue
		}
		for _, sc := range scopes {
			out = append(out, string(perm.Pattern{Action: a, Scope: sc}.Key()))
		}
	}
	slices.Sort(out)
	return out
}

// CanAssign reports whether the holder may assign or revoke role roleKey. A
// superuser may assign any; callers exclude the superuser role themselves.
func (s PermissionSet) CanAssign(roleKey string) bool {
	if s.Superuser {
		return true
	}
	_, ok := s.Assignable[roleKey]
	return ok
}

// AccessKeyPrefix starts every access key, so a bearer token can be told from
// a console session token without a lookup.
const AccessKeyPrefix = "sak_"

// AccessKeyIdentity is who an access key acts for and what it was given.
type AccessKeyIdentity struct {
	UserID      int64
	KeyID       int64
	Permissions []string
}

// AccessKeyVerifier validates access keys (CONTRACTS §80).
type AccessKeyVerifier interface {
	VerifyAccessKey(ctx context.Context, raw, ip string) (AccessKeyIdentity, error)
}

// AuthenticateBearer resolves a console bearer token to its user: a login
// session, whose id then travels in the returned context, or an access key,
// whose limits do. The client's ip goes in the context first (ClientIP): a
// session records where it is used from.
func AuthenticateBearer(ctx context.Context, tokens TokenVerifier, token, ip string) (context.Context, int64, error) {
	ctx = WithClientIP(ctx, ip)
	if !strings.HasPrefix(token, AccessKeyPrefix) {
		uid, session, err := tokens.VerifySession(ctx, token)
		if err != nil {
			return ctx, 0, err
		}
		return WithSession(WithUserID(ctx, uid), session), uid, nil
	}
	keys, ok := tokens.(AccessKeyVerifier)
	if !ok {
		return ctx, 0, ErrUnauthenticated
	}
	id, err := keys.VerifyAccessKey(ctx, token, ip)
	if err != nil {
		return ctx, 0, err
	}
	return WithAccessKey(WithUserID(ctx, id.UserID), id), id.UserID, nil
}

// PermissionSetOf is the set holding exactly keys, not a superuser.
func PermissionSetOf(keys ...string) PermissionSet {
	set := PermissionSet{Keys: make(map[string]struct{}, len(keys))}
	for _, k := range keys {
		set.Add(k)
	}
	return set
}

// Missing is what a wanted set holds beyond the set it is compared to: the
// superuser flag, permission keys and assignable roles (both sorted), and
// the "every custom role" flag.
type Missing struct {
	Superuser    bool
	Keys         []string
	Assign       []string
	AssignCustom bool
}

// Empty reports whether nothing is missing.
func (m Missing) Empty() bool {
	return !m.Superuser && len(m.Keys) == 0 && len(m.Assign) == 0 && !m.AssignCustom
}

// Details is m as the details of a permission_denied error; roles are kept
// apart from permission keys.
func (m Missing) Details() map[string]any {
	return map[string]any{
		"missing":               append([]string{}, m.Keys...),
		"missing_assignable":    append([]string{}, m.Assign...),
		"missing_assign_custom": m.AssignCustom,
		"superuser":             m.Superuser,
	}
}

// Missing returns what want holds that s does not: each key of want that s
// does not imply. A superuser misses nothing; anyone else misses the
// superuser flag when want has it.
func (s PermissionSet) Missing(want PermissionSet) Missing {
	if s.Superuser {
		return Missing{}
	}
	keys := slices.DeleteFunc(want.KeyList(), s.Has)
	if len(keys) == 0 {
		keys = nil
	}
	return Missing{
		Superuser:    want.Superuser,
		Keys:         keys,
		Assign:       missingKeys(s.Assignable, want.Assignable),
		AssignCustom: want.AssignCustom && !s.AssignCustom,
	}
}

// missingKeys lists the keys of want not in have, sorted.
func missingKeys(have, want map[string]struct{}) []string {
	var out []string
	for k := range want {
		if _, ok := have[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Restrict returns the part of the set that keys allow, pattern by pattern:
// a key the set implies is kept as is; a key wider than the set keeps the
// set's narrower scopes of that action it implies (a key of account:read
// on a holder of account:read:own gives account:read:own); scopes neither
// implies, such as group/12 and own, are dropped rather than intersected.
// Never more than the set itself, and no longer a superuser, whose Has is
// true for anything. The loss of the flag is deliberate: an access key never
// acts as a superuser, so it cannot grant or act on what only a superuser
// may (Missing). Assignable roles are not copied, also deliberately: an
// access key never assigns roles. On a decided set, Has of the result is
// the narrowed lists' and the holder's decision both.
func (s PermissionSet) Restrict(keys []string) PermissionSet {
	out := s.restrict(keys)
	if s.decide != nil {
		lists, decide := out, s.decide
		out.decide = func(k string) bool { return lists.lists(k) && decide(k) }
	}
	return out
}

// restrict is Restrict on the lists of the set.
func (s PermissionSet) restrict(keys []string) PermissionSet {
	out := PermissionSet{Keys: make(map[string]struct{}, len(keys)), Version: s.Version}
	for _, k := range keys {
		p, err := perm.Parse(k)
		switch {
		case err != nil:
		case s.lists(k):
			out.add(p)
		default:
			for _, sc := range s.Scoped[p.Action] {
				if held := (perm.Pattern{Action: p.Action, Scope: sc}); perm.Implies(p, held) {
					out.add(held)
				}
			}
		}
	}
	return out
}
