package lookup

import (
	"context"
	"slices"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// entities are the kinds of GET /lookup/:kind (CONTRACTS §96). They are kept
// together rather than one per module: each is a few lines over tables the
// usage log already joins by name, and together they are the permission table
// of the endpoint. A range must match the list endpoint it is named after.
var entities = []*entity{
	{
		// Found by email or display name, as the q of GET /users, in the
		// same scope: every user or the caller's team.
		kind: "users", from: "users u", id: "u.id", name: "u.email", hint: "u.display_name", status: "u.status",
		matches: []string{"u.display_name"},
		live:    "u.deleted_at IS NULL",
		read:    "user:read", table: core.UserRows,
	},
	{
		// Groups have no platform column: the platforms of a group follow
		// from its accounts' types (CONTRACTS §13), so there is no hint.
		kind: "groups", from: "groups g", id: "g.id", name: "g.name", status: "g.status",
		// account:group:bind alone finds the groups the caller can use: the
		// groups an "own" account holder may bind.
		read: "group:read", ownRead: "account:group:bind",
		// A group-scoped grant (account:read:group/G) shows its group by
		// name to a holder of neither.
		named: core.PermissionSet.ScopeGroups,
		// GET /me/groups.
		mine: &ownRows{scope: callerIs(groupOpenTo)},
	},
	{
		// The usage log shows every key's id, name and owner to usage:all:read
		// already; no other permission lists the keys of other users. The
		// owner is a subquery, not a join: it is read for the rows returned
		// only, after the limit, instead of for every key.
		kind: "api-keys", from: "api_keys k", id: "k.id", name: "k.name",
		hint: "(SELECT u.email FROM users u WHERE u.id = k.user_id)", status: "k.status",
		live: "k.deleted_at IS NULL",
		read: "usage:all:read",
		// GET /me/api-keys. The owner is the caller, so the hint is the
		// key's prefix instead.
		mine:   &ownRows{perms: []string{"apikey:self:manage"}, scope: callerIs("k.user_id = ?"), hint: "k.key_prefix"},
		narrow: []ref{{param: "user_id", cond: "k.user_id = ?"}},
	},
	{
		// GET /accounts, except that accounts of a disabled or uninstalled
		// plugin are found too: the usage log still has their requests.
		kind: "accounts", from: "accounts a", id: "a.id", name: "a.name", hint: "a.plugin_key", status: "a.status",
		live: "a.deleted_at IS NULL",
		read: "account:read", table: core.AccountRows,
		mine:   &ownRows{scope: callerIs("a.owner_id = ?")},
		prefer: []ref{{param: "prefer_group_id", cond: "EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = ?)"}},
	},
	{
		// The models are those of the price table (GET /prices), disabled
		// prices included, and those an account group prices on its own
		// (CONTRACTS §97.1). A price is enabled or not; it has no status
		// value.
		kind: "models", from: "(SELECT model FROM model_prices UNION SELECT model FROM price_rules) mp", name: "mp.model",
		read: "price:read",
		mine: &ownRows{perms: []string{"apikey:self:manage"}, scope: usableModels},
	},
	{
		// GET /proxies.
		kind: "proxies", from: "proxies p", id: "p.id", name: "p.name",
		hint: "p.protocol || '://' || p.host || ':' || p.port", status: "p.status",
		read: "proxy:read", table: core.ProxyRows,
		mine: &ownRows{scope: callerIs("p.created_by = ?")},
	},
}

// groupOpenTo holds for the groups a user ("?") can use: the range of GET
// /me/groups.
const groupOpenTo = segment.UsableGroup

// callerIs is the Scope of a kind whose own rows one condition selects; its
// "?" takes the caller's id.
func callerIs(cond string) func(context.Context, *Service, int64, *store.Conds) error {
	return func(_ context.Context, _ *Service, uid int64, f *store.Conds) error {
		f.And(cond, uid)
		return nil
	}
}

// usableModels leaves the models the caller's groups allow: a model with a
// list price in one of them (core.PricedModels: an enabled global price or
// the group's own) that its model policy lets through. The picker on a
// user's own pages is not tied to a group, so this is the union over all of
// them. GET /me/prices?group_id= lists a subset of it: of these, only the
// models the accounts of that group provide. The group policy is a glob list
// (core.GroupInfo.AllowsModel), evaluated here rather than rewritten in SQL.
func usableModels(ctx context.Context, s *Service, uid int64, f *store.Conds) error {
	groups, err := modelPolicies(store.G(ctx, s.db.Gorm), uid)
	if err != nil {
		return err
	}
	priced, err := s.priced.PricedModels(ctx, core.GroupIDs(groups), "")
	if err != nil {
		return err
	}
	usable := []string{}
	for _, g := range groups {
		usable = append(usable, slices.DeleteFunc(priced[g.ID], func(m string) bool { return !g.AllowsModel(m) })...)
	}
	f.And("mp.model = ANY(?)", store.Strings(x.Unique(usable)))
	return nil
}

// policyRow is the model policy of one group. SQL NULL and JSON null read
// as no list.
type policyRow struct {
	ID              int64
	ModelAllowlist  store.JSON[[]string]
	ModelFilterMode string
}

// policyRows reads into dst the model policies of the groups user uid can
// use.
func policyRows(db *gorm.DB, uid int64, dst *[]policyRow) *gorm.DB {
	return db.Table("groups g").Select("g.id, g.model_allowlist, g.model_filter_mode").Where(groupOpenTo, uid).Scan(dst)
}

// modelPolicies is policyRows as core.GroupInfo.
func modelPolicies(db *gorm.DB, uid int64) ([]core.GroupInfo, error) {
	var rows []policyRow
	if err := policyRows(db, uid, &rows).Error; err != nil {
		return nil, err
	}
	groups := make([]core.GroupInfo, len(rows))
	for i, r := range rows {
		groups[i] = core.GroupInfo{ID: r.ID, ModelAllowlist: r.ModelAllowlist.V, ModelFilterMode: r.ModelFilterMode}
	}
	return groups, nil
}
