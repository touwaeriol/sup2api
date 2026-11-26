package billing

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Price is the API view of a model_prices row. Prices are global per model
// (ARCHITECTURE 7.3) and set by administrators only: typed in (manual) or
// imported from a price sync source (sync).
type Price struct {
	ID             int64           `json:"id"`
	Model          string          `json:"model"`
	Mode           string          `json:"mode"`
	Config         json.RawMessage `json:"config"`
	Expression     string          `json:"expression"`
	ExprVersion    int             `json:"expr_version"`
	ExprHash       string          `json:"expr_hash"`
	Source         string          `json:"source"`
	SyncSourceID   *int64          `json:"sync_source_id"`
	SyncSourceName *string         `json:"sync_source_name"`
	SyncedAt       *time.Time      `json:"synced_at"`
	Enabled        bool            `json:"enabled"`
	Note           string          `json:"note"`
	UpdatedBy      *int64          `json:"updated_by"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Analysis       *expr.Analysis  `json:"analysis,omitempty"`
}

const priceColumns = `mp.id, mp.model, mp.mode, mp.config, mp.expression, mp.expr_version, mp.expr_hash,
	mp.source, mp.sync_source_id, ps.name AS sync_source_name, mp.synced_at, mp.enabled, mp.note, mp.updated_by, mp.updated_at`

// pricesWithSource is the model_prices rows (alias mp) with their sync source (ps).
func pricesWithSource(db *gorm.DB) *gorm.DB {
	return db.Table("model_prices mp").Joins("LEFT JOIN price_sync_sources ps ON ps.id = mp.sync_source_id")
}

// priceRow is a row of pricesWithSource read as priceColumns.
type priceRow struct {
	model.ModelPrice
	SyncSourceName *string
}

func (r *priceRow) price() *Price {
	p := &Price{ID: r.ID, Model: r.Model, Mode: r.Mode, Config: r.Config, Expression: r.Expression,
		ExprVersion: core.ValueOr(r.ExprVersion, 0), ExprHash: r.ExprHash, Source: r.Source, SyncSourceID: r.SyncSourceID,
		SyncSourceName: r.SyncSourceName, SyncedAt: r.SyncedAt, Enabled: core.ValueOr(r.Enabled, false), Note: r.Note,
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
	if prog, err := expr.CompileCached(p.Expression); err == nil {
		p.Analysis = prog.Analyze()
	}
	return p
}

func (s *Service) getPrice(ctx context.Context, q *gorm.DB, id int64, lock bool) (*Price, error) {
	db := pricesWithSource(store.G(ctx, q)).Select(priceColumns).Where("mp.id = ?", id)
	if lock {
		if err := store.RequireTx(q); err != nil {
			return nil, err
		}
		db = db.Clauses(store.ForUpdate().Of("mp")) // the price row only, not its source
	}
	var row priceRow
	err := db.Take(&row).Error
	if store.IsNoRows(err) {
		return nil, core.ErrNotFound.WithMessage("price not found")
	}
	if err != nil {
		return nil, err
	}
	return row.price(), nil
}

func (s *Service) registerPriceRoutes(r *httpapi.Router) {
	r.Perm("GET", "/prices", "price:read", s.listPrices)
	r.Perm("POST", "/prices", "price:manage", s.createPrice)
	r.Perm("POST", "/prices/validate", "price:read", s.validatePrice)
	r.Perm("POST", "/prices/preview", "price:read", s.previewPrice)
	r.Perm("GET", "/prices/history/:expr_hash", "price:read", s.priceHistory)
	r.Perm("GET", "/prices/:id", "price:read", s.showPrice)
	r.Perm("PATCH", "/prices/:id", "price:manage", s.updatePrice)
	r.Perm("DELETE", "/prices/:id", "price:manage", s.deletePrice)
}

// GET /prices?mode=&source=&sync_source_id=&enabled=&q=
func (s *Service) listPrices(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := httpapi.Pagination(c)
	f, err := priceFilter(c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	g := store.G(ctx, s.db.Gorm)
	q := pricesWithSource(g).Select(priceColumns).Scopes(f.Scope()).Order("mp.model, mp.id")
	rows, total, err := store.FindPage[priceRow](q, page, size)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := make([]*Price, 0, len(rows))
	for i := range rows {
		items = append(items, rows[i].price())
	}
	httpapi.List(c, items, httpapi.Page{Page: page, PageSize: size, Total: total})
}

// priceFilter is the filter of GET /prices.
func priceFilter(c *gin.Context) (store.Conds, error) {
	var f store.Conds
	if v := c.Query("mode"); v != "" {
		f.And("mp.mode = ?", v)
	}
	if v := c.Query("source"); v != "" {
		f.And("mp.source = ?", v)
	}
	if v := c.Query("sync_source_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return f, core.InvalidFields(core.FieldError{Field: "sync_source_id", Code: "invalid", Message: "must be an integer"})
		}
		f.And("mp.sync_source_id = ?", id)
	}
	if v := c.Query("enabled"); v != "" {
		f.And("mp.enabled = ?", v == "true" || v == "1")
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		pattern := "%" + store.Like(v) + "%"
		f.And("(mp.model ILIKE ? OR mp.note ILIKE ?)", pattern, pattern)
	}
	return f, nil
}

func (s *Service) showPrice(c *gin.Context) {
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	p, err := s.getPrice(c.Request.Context(), s.db.Gorm, id, false)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, p)
}

// priceInput is the body of POST/PATCH /prices.
type priceInput struct {
	Model      *string         `json:"model"`
	Mode       *string         `json:"mode"`
	Config     json.RawMessage `json:"config"`
	Expression *string         `json:"expression"`
	Enabled    *bool           `json:"enabled"`
	Note       *string         `json:"note"`
	// Confirm acknowledges the big-cost warning.
	Confirm bool `json:"confirm"`
}

func (s *Service) createPrice(c *gin.Context) {
	ctx := c.Request.Context()
	var in priceInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	name, mode := deref(in.Model), deref(in.Mode)
	if fields := newPriceFields(ctx, name, mode); len(fields) > 0 {
		httpapi.Fail(c, core.InvalidFields(fields...))
		return
	}
	ck, err := s.check(ctx, s.db.Gorm, mode, in.Config, deref(in.Expression), in.Confirm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var p *Price
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		p, err = s.insertPrice(ctx, tx, name, ck, in)
		return err
	})
	s.replySaved(c, p, err, httpapi.Created)
}

// newPriceFields are the field errors of the model (core.ModelErrors) and
// mode of a new price.
func newPriceFields(ctx context.Context, name, mode string) []core.FieldError {
	fields := core.ModelErrors(ctx, "model", name)
	if mode == "" {
		fields = append(fields, core.FieldError{Field: "mode", Code: "required", Message: "visual or expression"})
	}
	return fields
}

// insertPrice writes a new manual price and keeps its expression where a
// settlement retry reads it back.
func (s *Service) insertPrice(ctx context.Context, tx *gorm.DB, name string, ck *checked, in priceInput) (*Price, error) {
	enabled := in.Enabled == nil || *in.Enabled
	uid, _ := core.UserID(ctx)
	// Every column is written, enabled and expr_version included;
	// updated_at is the default.
	version := ck.prog.Version()
	row := &model.ModelPrice{Model: name, Mode: ck.mode, Config: ck.config, Expression: ck.src, ExprVersion: &version,
		ExprHash: ck.prog.Hash(), Source: SourceManual, Enabled: &enabled, Note: deref(in.Note), UpdatedBy: nullID(uid)}
	if err := store.G(ctx, tx).Create(row).Error; err != nil {
		return nil, err
	}
	p, err := s.getPrice(ctx, tx, row.ID, false)
	if err != nil {
		return nil, err
	}
	return p, recordHistory(ctx, tx, ck.prog)
}

// replySaved answers a price write: a second price of the model is a
// conflict.
func (s *Service) replySaved(c *gin.Context, p *Price, err error, reply func(*gin.Context, any)) {
	if store.IsUniqueViolation(err, "") {
		err = errPriceExists
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(c.Request.Context(), "prices")
	reply(c, p)
}

func (s *Service) updatePrice(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in priceInput
	if !httpapi.BindJSON(c, &in) {
		return
	}
	var out *Price
	err := s.db.Tx(ctx, func(tx *gorm.DB) error {
		cur, err := s.getPrice(ctx, tx, id, true)
		if err != nil {
			return err
		}
		set, err := s.priceChanges(ctx, tx, cur, in)
		if err != nil {
			return err
		}
		if err := updatePriceRow(store.G(ctx, tx), id, set).Error; err != nil {
			return err
		}
		out, err = s.getPrice(ctx, tx, id, false)
		return err
	})
	s.replySaved(c, out, err, httpapi.OK)
}

// reprice checks the mode, config and expression of a price after a PATCH
// body that sets any of them, and keeps the expression in the history.
func (s *Service) reprice(ctx context.Context, tx *gorm.DB, cur *Price, in priceInput, mode string) (*checked, error) {
	config := cur.Config
	if in.Config != nil {
		config = in.Config
	}
	ck, err := s.check(ctx, tx, mode, config, core.ValueOr(in.Expression, cur.Expression), in.Confirm)
	if err != nil {
		return nil, err
	}
	return ck, recordHistory(ctx, tx, ck.prog)
}

// priceChanges is every column of the locked price cur after the PATCH
// body in: a field left out keeps its value; a new mode, config or
// expression is checked (reprice).
func (s *Service) priceChanges(ctx context.Context, tx *gorm.DB, cur *Price, in priceInput) (map[string]any, error) {
	name, mode := core.ValueOr(in.Model, cur.Model), core.ValueOr(in.Mode, cur.Mode)
	if fields := core.ModelErrors(ctx, "model", name); len(fields) > 0 {
		return nil, core.InvalidFields(fields...)
	}
	config, src, hash, version := cur.Config, cur.Expression, cur.ExprHash, cur.ExprVersion
	pricing := in.Mode != nil || in.Config != nil || in.Expression != nil
	if pricing {
		ck, err := s.reprice(ctx, tx, cur, in, mode)
		if err != nil {
			return nil, err
		}
		config, src, hash, version = ck.config, ck.src, ck.prog.Hash(), ck.prog.Version()
	}
	// Editing a synced price makes it manual: later syncs only replace
	// it when an administrator selects it in the preview.
	source, syncID := cur.Source, cur.SyncSourceID
	if pricing || in.Model != nil {
		source, syncID = SourceManual, nil
	}
	uid, _ := core.UserID(ctx)
	return map[string]any{
		"model": name, "mode": mode, "config": config, "expression": src, "expr_version": version, "expr_hash": hash,
		"enabled": core.ValueOr(in.Enabled, cur.Enabled), "note": core.ValueOr(in.Note, cur.Note), "updated_by": nullID(uid),
		"source": source, "sync_source_id": syncID,
	}, nil
}

func (s *Service) deletePrice(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	if _, err := s.getPrice(ctx, s.db.Gorm, id, false); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := store.G(ctx, s.db.Gorm).Where("id = ?", id).Delete(&model.ModelPrice{}).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, "prices")
	httpapi.NoContent(c)
}

// updatePriceRow writes the columns of set to the price id and moves its
// updated_at, as every edit of a price does.
func updatePriceRow(db *gorm.DB, id int64, set map[string]any) *gorm.DB {
	set["updated_at"] = gorm.Expr("now()")
	return db.Model(&model.ModelPrice{}).Where("id = ?", id).Updates(set)
}

var errPriceExists = core.ErrConflict.WithMessage("a price for this model already exists")

func (s *Service) priceHistory(c *gin.Context) {
	ctx := c.Request.Context()
	hash := c.Param("expr_hash")
	var row model.ModelPriceHistory
	err := store.G(ctx, s.db.Gorm).Where("expr_hash = ?", hash).Take(&row).Error
	if store.IsNoRows(err) {
		httpapi.Fail(c, core.ErrNotFound.WithMessage("expression not found"))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, struct {
		ExprHash    string    `json:"expr_hash"`
		Expression  string    `json:"expression"`
		ExprVersion int       `json:"expr_version"`
		CreatedAt   time.Time `json:"created_at"`
	}{row.ExprHash, row.Expression, row.ExprVersion, row.CreatedAt})
}

// POST /prices/validate {mode, config?, expression?}
func (s *Service) validatePrice(c *gin.Context) {
	ctx := c.Request.Context()
	var in struct {
		Mode       string          `json:"mode"`
		Config     json.RawMessage `json:"config"`
		Expression string          `json:"expression"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if in.Mode == "" {
		in.Mode = expr.ModeExpression
	}
	st, _ := s.Settings(ctx)
	rep, err := checkPriceDef(in.Mode, priceConfig(in.Config), in.Expression, expr.ValidateOptions{Facts: s.factsFor(), BigCostUSD: st.BigCostWarningUSD})
	if err != nil {
		issue := expr.Issue{Code: expr.CodeConfig, Message: map[string]string{"en": err.Error(), "zh": err.Error()}}
		if ce, ok := err.(*expr.ConfigError); ok {
			issue = ce.Issue
		}
		rep = &expr.Report{Errors: []expr.Issue{issue}, Warnings: []expr.Issue{}}
	}
	httpapi.OK(c, rep)
}

// FinalCost applies the group multiplier and rounds up to the 8 decimal
// places stored in the database: a charge is never below its price. The
// expression runs in float64, so the product is first rounded to 12 places
// to drop float noise (0.1*3 = 0.30000000000000004) that would otherwise
// add 1e-8 to an exact amount.
func FinalCost(cost, rateMultiplier decimal.Decimal) decimal.Decimal {
	return cost.Mul(rateMultiplier).Round(12).RoundCeil(8)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
