// Package group implements scheduling groups: CRUD and the groups visible to
// the current user.
package group

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/segment"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
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

// MyGroup is the reduced view returned by GET /me/groups.
type MyGroup struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	RateMultiplier  string   `json:"rate_multiplier"`
	ModelAllowlist  []string `json:"model_allowlist"`
	ModelFilterMode string   `json:"model_filter_mode"`
	Platforms       []string `json:"platforms"`
	// ServedProtocols are the inbound protocols the accounts of the group
	// serve, as GET /groups/:id reports them but without the number of
	// accounts serving each: what the caller's keys of the group can call.
	ServedProtocols []MyServedProtocol `json:"served_protocols"`
	// EffectiveRate is the rate the caller pays in the group and RateSource
	// where it comes from (core.SegmentRate); RateMultiplier stays the
	// group's default.
	EffectiveRate decimal.Decimal `json:"effective_rate"`
	RateSource    string          `json:"rate_source"`
	// Exclusive marks a private group the caller's segment is granted.
	Exclusive bool `json:"exclusive"`
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

func (s *Service) list(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	q := strings.TrimSpace(c.Query("q"))
	status := c.Query("status")
	var providing []int64 // the groups ?model= leaves; nil = all
	if model := strings.TrimSpace(c.Query("model")); model != "" {
		var err error
		if providing, err = s.groupsProviding(ctx, model); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	g, filter := store.G(ctx, s.db.Gorm), listFilter(q, status, providing)
	var total int64
	if err := g.Table("groups g").Scopes(filter).Count(&total).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	var rows []groupRow
	if err := selectGroups(g, core.ActivePluginKeys(s.reg)).Scopes(filter).
		Order("g.id").Limit(size).Offset((page - 1) * size).Scan(&rows).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := make([]*Group, 0, len(rows))
	for i := range rows {
		items = append(items, rows[i].group())
	}
	if err := s.fill(ctx, items); err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
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
		return nil, core.ErrNotFound.WithMessage(t(ctx, "group not found", "分组不存在"))
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

type groupInput struct {
	Name            *string   `json:"name"`
	Description     *string   `json:"description"`
	Status          *string   `json:"status"`
	RateMultiplier  *string   `json:"rate_multiplier"`
	Visibility      *string   `json:"visibility"`
	ModelAllowlist  *[]string `json:"model_allowlist"`
	ModelFilterMode *string   `json:"model_filter_mode"`
	// Environment replaces the group layer as a whole.
	Environment *core.EnvironmentValues `json:"environment"`
	// ConversionLossPolicy sets or, with null, clears the policy (§88).
	ConversionLossPolicy core.LossPolicyInput `json:"conversion_loss_policy"`
}

// validate checks the provided fields; create requires name.
func (in *groupInput) validate(ctx context.Context, create bool) error {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 {
			add("name", "invalid", "name must be 1-100 characters", "名称长度须为 1-100 个字符")
		}
	} else if create {
		add("name", "required", "name is required", "名称必填")
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 2000 {
		add("description", "too_long", "description is too long", "描述过长")
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		add("status", "invalid", "status must be active or disabled", "状态必须为 active 或 disabled")
	}
	if in.Visibility != nil && *in.Visibility != "public" && *in.Visibility != "restricted" {
		add("visibility", "invalid", "visibility must be public or restricted", "可见性必须为 public 或 restricted")
	}
	if in.RateMultiplier != nil {
		d, ok := core.ParseRate(*in.RateMultiplier)
		if !ok {
			add("rate_multiplier", "invalid", "rate multiplier must be a non-negative number with at most 4 decimals",
				"倍率必须为非负数，最多 4 位小数")
		} else {
			v := d.String()
			in.RateMultiplier = &v
		}
	}
	if in.ModelFilterMode != nil && *in.ModelFilterMode != "whitelist" && *in.ModelFilterMode != "blacklist" {
		add("model_filter_mode", "invalid", "model filter mode must be whitelist or blacklist", "模型过滤模式必须为白名单或黑名单")
	}
	if in.ModelAllowlist != nil {
		clean := make([]string, 0, len(*in.ModelAllowlist))
		for _, m := range *in.ModelAllowlist {
			m = strings.TrimSpace(m)
			if m == "" {
				add("model_allowlist", "invalid", "model patterns must not be empty", "模型模式不能为空")
				break
			}
			clean = append(clean, m)
		}
		in.ModelAllowlist = &clean
	}
	if in.Environment != nil {
		values, errs := core.ValidateEnvironmentValues(ctx, core.EnvLayerGroup, "environment", *in.Environment)
		fe = append(fe, errs...)
		in.Environment = &values
	}
	fe = append(fe, in.ConversionLossPolicy.Validate(ctx, "conversion_loss_policy")...)
	if len(fe) > 0 {
		return core.InvalidFields(fe...)
	}
	return nil
}

// columns are the groups columns a PATCH writes: the fields present, as
// written, and updated_at = now(), as the raw SQL had it.
func (in *groupInput) columns() map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("now()")}
	store.SetIf(set, "name", in.Name)
	store.SetIf(set, "description", in.Description)
	store.SetIf(set, "status", in.Status)
	store.SetIf(set, "rate_multiplier", in.RateMultiplier)
	store.SetIf(set, "visibility", in.Visibility)
	store.SetIf(set, "model_filter_mode", in.ModelFilterMode)
	if in.ModelAllowlist != nil {
		b, _ := json.Marshal(*in.ModelAllowlist)
		set["model_allowlist"] = string(b)
	}
	store.SetIf(set, "environment", in.Environment)
	if in.ConversionLossPolicy.Set {
		set["conversion_loss_policy"] = in.ConversionLossPolicy.Stored()
	}
	return set
}

func dupName(ctx context.Context) error {
	return core.InvalidFields(core.FieldError{Field: "name", Code: "duplicate",
		Message: t(ctx, "a group with this name already exists", "分组名称已存在")})
}

func (s *Service) create(c *gin.Context) {
	ctx := c.Request.Context()
	var in groupInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.validate(ctx, true); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// The fields of in present are written as given (a 0 rate included),
	// the column defaults stand for the others.
	row := &model.Group{Name: *in.Name, Description: core.ValueOr(in.Description, ""), Status: in.Status,
		Visibility: in.Visibility, ModelFilterMode: in.ModelFilterMode, ConversionLossPolicy: in.ConversionLossPolicy.Stored()}
	if in.RateMultiplier != nil {
		rate := decimal.RequireFromString(*in.RateMultiplier) // validate normalized it
		row.RateMultiplier = &rate
	}
	if in.ModelAllowlist != nil {
		row.ModelAllowlist, _ = json.Marshal(*in.ModelAllowlist)
	}
	if in.Environment != nil {
		row.Environment, _ = json.Marshal(*in.Environment)
	}
	err := store.G(ctx, s.db.Gorm).Create(row).Error
	if store.IsUniqueViolation(err, "") {
		httpapi.Fail(c, dupName(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	g, err := s.load(ctx, row.ID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.Created(c, g)
}

func (s *Service) update(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in groupInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if err := in.validate(ctx, false); err != nil {
		httpapi.Fail(c, err)
		return
	}
	// A field left out keeps its value.
	res := store.G(ctx, s.db.Gorm).Model(&model.Group{}).Where("id = ?", id).Updates(in.columns())
	err := res.Error
	if store.IsUniqueViolation(err, "") {
		httpapi.Fail(c, dupName(ctx))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if res.RowsAffected == 0 {
		httpapi.Fail(c, core.ErrNotFound.WithMessage(t(ctx, "group not found", "分组不存在")))
		return
	}
	s.dropKeyCache(ctx, id)
	g, err := s.load(ctx, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, g)
}

// delete removes a group. Its API keys are only loosely bound to it: with
// ?move_keys_to=<group id> they move to that group, otherwise they stay
// (soft-deleted ones included) with no group and authenticate as
// group_deleted until their owner picks another one.
func (s *Service) delete(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var moveTo *int64
	if v := strings.TrimSpace(c.Query("move_keys_to")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 || n == id {
			httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "move_keys_to", Code: "invalid",
				Message: t(ctx, "pick another existing group", "请选择另一个已存在的分组")}))
			return
		}
		moveTo = &n
	}
	var hashes []string
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		// Lock the group and the move target in id order, so two deletes
		// moving keys into each other's group cannot deadlock.
		ids := []int64{id}
		if moveTo != nil {
			ids = append(ids, *moveTo)
		}
		g := store.G(ctx, tx)
		var locked []int64
		if err := g.Model(&model.Group{}).Where("id = ANY(?)", store.Int64s(ids)).Order("id").
			Clauses(store.ForUpdate()).Pluck("id", &locked).Error; err != nil {
			return err
		}
		if !slices.Contains(locked, id) {
			return core.ErrNotFound.WithMessage(t(ctx, "group not found", "分组不存在"))
		}
		if moveTo != nil && !slices.Contains(locked, *moveTo) {
			return core.InvalidFields(core.FieldError{Field: "move_keys_to", Code: "not_found",
				Message: t(ctx, "the target group does not exist", "目标分组不存在")})
		}
		// Every key of the group moves, soft-deleted ones included.
		var moved []model.APIKey
		if err := g.Model(&moved).Clauses(clause.Returning{Columns: []clause.Column{{Name: "key_hash"}}}).
			Where("group_id = ?", id).Update("group_id", moveTo).Error; err != nil {
			return err
		}
		hashes = make([]string, len(moved))
		for i, k := range moved {
			hashes[i] = k.KeyHash
		}
		if err := g.Delete(&model.Group{}, id).Error; err != nil {
			return err
		}
		uid, _ := core.UserID(ctx)
		_, err := s.grants.ClearScopeGrants(ctx, tx, uid, core.ScopeRefGroup, []int64{id})
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.delKeys(ctx, hashes)
	s.announceAccounts(ctx)
	httpapi.NoContent(c)
}

func (s *Service) myGroups(c *gin.Context) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	var rows []struct {
		ID              int64                `gorm:"column:id"`
		Name            string               `gorm:"column:name"`
		Description     string               `gorm:"column:description"`
		RateMultiplier  *decimal.Decimal     `gorm:"column:rate_multiplier"`
		ModelAllowlist  store.JSON[[]string] `gorm:"column:model_allowlist"`
		ModelFilterMode *string              `gorm:"column:model_filter_mode"`
		Exclusive       bool                 `gorm:"column:exclusive"`
	}
	if err := store.G(ctx, s.db.Gorm).Table("groups g").
		Select("g.id, g.name, g.description, g.rate_multiplier, g.model_allowlist, g.model_filter_mode, g.visibility = 'restricted' AS exclusive").
		Where(segment.UsableGroup, uid).Order("g.id").Scan(&rows).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := make([]MyGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, MyGroup{ID: r.ID, Name: r.Name, Description: r.Description, RateMultiplier: r.RateMultiplier.String(),
			ModelAllowlist: apiAllowlist(r.ModelAllowlist), ModelFilterMode: core.ValueOr(r.ModelFilterMode, ""), Exclusive: r.Exclusive})
	}
	ids := make([]int64, len(out))
	for i, g := range out {
		ids[i] = g.ID
	}
	ps, err := s.Platforms(ctx, ids)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	terms, err := segment.UserTerms(ctx, s.db.Gorm, uid, ids)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	served, err := s.servedProtocols(ctx, ids)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// A group deleted since it was listed has no terms: it is left out.
	mine := out[:0]
	for _, g := range out {
		tm, ok := terms[g.ID]
		if !ok {
			continue
		}
		g.Platforms = ps[g.ID]
		g.ServedProtocols = myServed(served[g.ID])
		g.EffectiveRate, g.RateSource = tm.Rate, tm.RateSource
		mine = append(mine, g)
	}
	httpapi.OK(c, mine)
}

// MyServedProtocol is a protocol a group serves as GET /me/groups shows it.
type MyServedProtocol struct {
	Protocol string `json:"protocol"`
}

// myServed drops the account counts of served; never nil.
func myServed(served []core.GroupServedProtocol) []MyServedProtocol {
	out := make([]MyServedProtocol, len(served))
	for i, p := range served {
		out[i] = MyServedProtocol{Protocol: p.Protocol}
	}
	return out
}

// dropKeyCache deletes cached API key principals ("apikey:{sha256}", see
// CONTRACTS §7) for the keys of the group, so a change of the group applies
// immediately instead of after the cache TTL.
func (s *Service) dropKeyCache(ctx context.Context, groupID int64) {
	if s.rdb == nil {
		return
	}
	var hashes []string
	if err := store.G(ctx, s.db.Gorm).Model(&model.APIKey{}).Where("group_id = ?", groupID).Pluck("key_hash", &hashes).Error; err != nil {
		slog.WarnContext(ctx, "group: list api keys for cache drop", "err", err)
		return
	}
	s.delKeys(ctx, hashes)
}

func (s *Service) delKeys(ctx context.Context, hashes []string) {
	if s.rdb == nil || len(hashes) == 0 {
		return
	}
	for start := 0; start < len(hashes); start += 500 {
		end := min(start+500, len(hashes))
		keys := make([]string, 0, end-start)
		for _, h := range hashes[start:end] {
			keys = append(keys, "apikey:"+h)
		}
		if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
			slog.WarnContext(ctx, "group: drop api key cache", "err", err)
			return
		}
	}
}

func (s *Service) announceAccounts(ctx context.Context) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(ctx, core.ChannelAccountChanged, []byte(`{"reason":"group_deleted"}`)); err != nil {
		slog.WarnContext(ctx, "group: publish account:changed", "err", err)
	}
}
