// Package group implements scheduling groups: CRUD and the groups visible to
// the current user.
package group

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Service serves the group endpoints.
type Service struct {
	db  *store.DB
	rdb redis.UniversalClient // optional: drops cached API key principals
	bus core.Bus              // optional: announces membership changes
	reg core.PluginRegistry   // optional: platforms are [] without it
	// grants clears the scoped grants naming a deleted group (CONTRACTS §21).
	grants core.ScopeGrantClearer
	// Protocols reports the protocols the accounts of a group serve, for
	// GET /groups/:id and GET /me/groups; optional (served_protocols is []
	// without it). The account module is built after this one, so it is
	// set afterwards.
	Protocols core.GroupProtocols
	// Parts are the fields of POST /groups and PATCH /groups/:id other
	// modules own (prices, rates), written in the group's transaction; set
	// after New, as the modules are built around this one.
	Parts []core.GroupWritePart
}

// New builds the group service. rdb, bus and reg may be nil; reg resolves
// the platforms a group serves from its account types. grants is needed to
// delete groups.
func New(db *store.DB, rdb redis.UniversalClient, bus core.Bus, reg core.PluginRegistry, grants core.ScopeGrantClearer) *Service {
	return &Service{db: db, rdb: rdb, bus: bus, reg: reg, grants: grants}
}

// RegisterRoutes mounts the group endpoints.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.AuthedRead("/me/groups", s.myGroups)
	r.Perm("GET", "/groups", "group:read", s.list)
	r.Perm("POST", "/groups", "group:manage", s.create)
	r.Perm("GET", "/groups/:id", "group:read", s.get)
	r.Perm("GET", "/groups/:id/models", "group:read", s.models)
	r.Perm("PATCH", "/groups/:id", "group:manage", s.update)
	r.Perm("DELETE", "/groups/:id", "group:manage", s.delete)
}

// Group is the API view of a groups row.
type Group struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	RateMultiplier  string   `json:"rate_multiplier"`
	Visibility      string   `json:"visibility"`
	ModelAllowlist  []string `json:"model_allowlist"`
	ModelFilterMode string   `json:"model_filter_mode"`
	// Environment is the group layer of the environment variables (§74.3).
	Environment core.EnvironmentValues `json:"environment"`
	// ConversionLossPolicy is the group's conversion loss policy (§88);
	// null leaves it to the accounts (default allow).
	ConversionLossPolicy *string `json:"conversion_loss_policy"`
	AccountCount         int64   `json:"account_count"`
	APIKeyCount          int64   `json:"api_key_count"`
	// Platforms the group serves: those of its accounts' types (CONTRACTS §13).
	Platforms []string  `json:"platforms"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	PricingSummary
}

// groupColumns are the columns of a groupRow of the groups g; account_count
// only counts accounts of enabled plugins (both parameters are the keys of
// core.ActivePluginKeys, NULL = no filter).
const groupColumns = `g.id, g.name, g.description, g.status, g.rate_multiplier, g.visibility,
	g.model_allowlist, g.model_filter_mode, g.environment, g.conversion_loss_policy, g.created_at, g.updated_at,
	(SELECT count(*) FROM account_groups ag JOIN accounts a ON a.id = ag.account_id
	  WHERE ag.group_id = g.id AND a.deleted_at IS NULL AND (?::text[] IS NULL OR a.plugin_key = ANY(?))) AS account_count,
	(SELECT count(*) FROM api_keys k WHERE k.group_id = g.id AND k.deleted_at IS NULL) AS api_key_count`

// groupRow is a row of groupColumns, its documents decoded.
type groupRow struct {
	ID                   int64                              `gorm:"column:id"`
	Name                 string                             `gorm:"column:name"`
	Description          string                             `gorm:"column:description"`
	Status               *string                            `gorm:"column:status"`
	RateMultiplier       *decimal.Decimal                   `gorm:"column:rate_multiplier"`
	Visibility           *string                            `gorm:"column:visibility"`
	ModelAllowlist       store.JSON[[]string]               `gorm:"column:model_allowlist"`
	ModelFilterMode      *string                            `gorm:"column:model_filter_mode"`
	Environment          store.JSON[core.EnvironmentValues] `gorm:"column:environment"`
	ConversionLossPolicy *string                            `gorm:"column:conversion_loss_policy"`
	CreatedAt            time.Time                          `gorm:"column:created_at"`
	UpdatedAt            time.Time                          `gorm:"column:updated_at"`
	AccountCount         int64                              `gorm:"column:account_count"`
	APIKeyCount          int64                              `gorm:"column:api_key_count"`
}

// selectGroups reads the groupRows of the groups g the caller narrows;
// keys are core.ActivePluginKeys.
func selectGroups(db *gorm.DB, keys []string) *gorm.DB {
	arg := store.NullStrings(keys) // nil (no registry) counts every account
	return db.Table("groups g").Select(groupColumns, arg, arg)
}

// listFilter narrows the groups g to those GET /groups asks for: the name
// contains q, the status is status (both "" = any) and, unless providing is
// nil, the id is one of providing.
func listFilter(q, status string, providing []int64) func(*gorm.DB) *gorm.DB {
	var f store.Conds
	if q != "" {
		f.And("g.name ILIKE ?", "%"+q+"%")
	}
	if status != "" {
		f.And("g.status = ?", status)
	}
	if providing != nil {
		f.And("g.id = ANY(?)", store.Int64s(providing))
	}
	return f.Scope()
}

// group is the API view of the row, without what fill adds.
func (r *groupRow) group() *Group {
	return &Group{ID: r.ID, Name: r.Name, Description: r.Description, Status: core.ValueOr(r.Status, ""), RateMultiplier: r.RateMultiplier.String(),
		Visibility: core.ValueOr(r.Visibility, ""), ModelAllowlist: apiAllowlist(r.ModelAllowlist), ModelFilterMode: core.ValueOr(r.ModelFilterMode, ""),
		Environment: r.Environment.V, ConversionLossPolicy: r.ConversionLossPolicy,
		AccountCount: r.AccountCount, APIKeyCount: r.APIKeyCount, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// apiAllowlist is a model_allowlist as the API shows it: [] for none, never
// null.
func apiAllowlist(stored store.JSON[[]string]) []string {
	if stored.V == nil {
		return []string{}
	}
	return stored.V
}

func t(ctx context.Context, en, zh string) string { return core.Text(ctx, en, zh) }

// groupNotFound is the not_found of a group that does not exist.
func groupNotFound(ctx context.Context) error {
	return core.ErrNotFound.WithMessage(t(ctx, "account group not found", "账号分组不存在"))
}

func (s *Service) list(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	var providing []int64 // the groups ?model= leaves; nil = all
	if model := strings.TrimSpace(c.Query("model")); model != "" {
		var err error
		if providing, err = s.groupsProviding(ctx, model); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	filter := listFilter(strings.TrimSpace(c.Query("q")), c.Query("status"), providing)
	var total int64
	if err := store.G(ctx, s.db.Gorm).Table("groups g").Scopes(filter).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items, err := s.listPage(ctx, filter, page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// listPage reads a page of the groups filter leaves, by id, with what fill
// adds.
func (s *Service) listPage(ctx context.Context, filter func(*gorm.DB) *gorm.DB, page, size int) ([]*Group, error) {
	var rows []groupRow
	if err := selectGroups(store.G(ctx, s.db.Gorm), core.ActivePluginKeys(s.reg)).Scopes(filter).
		Order("g.id").Limit(size).Offset((page - 1) * size).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]*Group, 0, len(rows))
	for i := range rows {
		items = append(items, rows[i].group())
	}
	return items, s.fill(ctx, items)
}

// fill sets what a group row carries beyond its own columns.
func (s *Service) fill(ctx context.Context, gs []*Group) error {
	if err := s.fillPlatforms(ctx, gs); err != nil {
		return err
	}
	return s.fillSummary(ctx, gs)
}

// fillPlatforms sets Platforms of the groups with one query.
func (s *Service) fillPlatforms(ctx context.Context, gs []*Group) error {
	ids := make([]int64, len(gs))
	for i, g := range gs {
		ids[i] = g.ID
	}
	ps, err := s.Platforms(ctx, ids)
	if err != nil {
		return err
	}
	for _, g := range gs {
		g.Platforms = ps[g.ID]
	}
	return nil
}

// loadRow reads the row of a group, without what fill adds.
func (s *Service) loadRow(ctx context.Context, id int64) (*Group, error) {
	var row groupRow
	err := selectGroups(store.G(ctx, s.db.Gorm), core.ActivePluginKeys(s.reg)).Where("g.id = ?", id).Take(&row).Error
	if store.IsNoRows(err) {
		return nil, groupNotFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	return row.group(), nil
}

func (s *Service) load(ctx context.Context, id int64) (*Group, error) {
	g, err := s.loadRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, []*Group{g}); err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Service) get(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	g, err := s.load(ctx, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	served, err := s.servedProtocols(ctx, []int64{id})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, Detail{Group: g, ServedProtocols: served[id]})
}

// servedProtocols asks the account module what the accounts of the groups
// serve; every group has a list, empty without the account module.
func (s *Service) servedProtocols(ctx context.Context, ids []int64) (map[int64][]core.GroupServedProtocol, error) {
	if s.Protocols != nil {
		return s.Protocols.GroupServedProtocols(ctx, ids)
	}
	out := make(map[int64][]core.GroupServedProtocol, len(ids))
	for _, id := range ids {
		out[id] = []core.GroupServedProtocol{}
	}
	return out, nil
}

// Detail is GET /groups/:id: the group and what only its own page shows.
type Detail struct {
	*Group
	// ServedProtocols are the inbound protocols the accounts of the group
	// serve, each with the number of accounts serving it; read-only.
	ServedProtocols []core.GroupServedProtocol `json:"served_protocols"`
}
