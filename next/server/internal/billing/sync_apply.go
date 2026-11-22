package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// SyncCurrent is the local price compared in a preview.
type SyncCurrent struct {
	ID           int64           `json:"id"`
	Mode         string          `json:"mode"`
	Config       json.RawMessage `json:"config"`
	Expression   string          `json:"expression"`
	Source       string          `json:"source"`
	SyncSourceID *int64          `json:"sync_source_id"`
	Enabled      bool            `json:"enabled"`
	hash         string
}

// SyncItem is one model of a preview. Action: create (no local price),
// update (local synced price differs), manual (local manual price differs;
// replaced only when selected), unchanged.
type SyncItem struct {
	Model    string       `json:"model"`
	Action   string       `json:"action"`
	Incoming *syncPrice   `json:"incoming"`
	Current  *SyncCurrent `json:"current"`
}

// SyncPreview is the response of POST /price-sources/:id/preview.
type SyncPreview struct {
	SourceID  int64      `json:"source_id"`
	FetchedAt time.Time  `json:"fetched_at"`
	Total     int        `json:"total"`
	Skipped   int        `json:"skipped"`
	Items     []SyncItem `json:"items"`
}

func (s *Service) loadAndFetch(ctx context.Context, id int64) (*PriceSource, *fetched, error) {
	src, err := s.getSource(ctx, s.db.Gorm, id)
	if err != nil {
		return nil, nil, err
	}
	f, err := s.fetchSource(ctx, src)
	if err != nil {
		msg := truncate(err.Error(), 500)
		_ = setSourceStatus(store.G(context.WithoutCancel(ctx), s.db.Gorm), id, map[string]any{"last_error": msg}).Error
		return src, nil, core.ErrUnavailable.WithMessage("fetch price source: " + msg)
	}
	return src, f, nil
}

func (s *Service) currentPrices(ctx context.Context, q *gorm.DB) (map[string]*SyncCurrent, error) {
	var rows []model.ModelPrice
	err := store.G(ctx, q).Select("id", "model", "mode", "config", "expression", "expr_hash", "source", "sync_source_id", "enabled").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*SyncCurrent, len(rows))
	for _, r := range rows {
		out[r.Model] = &SyncCurrent{ID: r.ID, Mode: r.Mode, Config: r.Config, Expression: r.Expression, Source: r.Source,
			SyncSourceID: r.SyncSourceID, Enabled: core.ValueOr(r.Enabled, false), hash: r.ExprHash}
	}
	return out, nil
}

func syncAction(in *syncPrice, cur *SyncCurrent) string {
	switch {
	case cur == nil:
		return "create"
	case cur.hash == in.hash && cur.Mode == in.Mode:
		return "unchanged"
	case cur.Source == SourceManual:
		return "manual"
	}
	return "update"
}

func (s *Service) previewSync(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	_, f, err := s.loadAndFetch(ctx, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	cur, err := s.currentPrices(ctx, s.db.Gorm)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	out := &SyncPreview{SourceID: id, FetchedAt: time.Now().UTC(), Total: len(f.prices), Skipped: f.skipped, Items: []SyncItem{}}
	for model, p := range f.prices {
		out.Items = append(out.Items, SyncItem{Model: model, Action: syncAction(p, cur[model]), Incoming: p, Current: cur[model]})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Model < out.Items[j].Model })
	httpapi.OK(c, out)
}

// SyncSkip is a model apply did not import.
type SyncSkip struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// SyncResult is the response of POST /price-sources/:id/apply.
type SyncResult struct {
	Created   int        `json:"created"`
	Updated   int        `json:"updated"`
	Unchanged int        `json:"unchanged"`
	Skipped   []SyncSkip `json:"skipped"`
}

// applySync imports the selected models ({models: []}) from a fresh fetch.
// Selected manual prices are replaced; imported rows become source=sync.
func (s *Service) applySync(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Models []string `json:"models"`
	}
	if !httpapi.BindJSON(c, &in) {
		return
	}
	if len(in.Models) == 0 {
		httpapi.Fail(c, core.InvalidFields(core.FieldError{Field: "models", Code: "required", Message: "select at least one model"}))
		return
	}
	_, f, err := s.loadAndFetch(ctx, id)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	res := &SyncResult{Skipped: []SyncSkip{}}
	if err := s.db.Tx(ctx, func(tx *gorm.DB) error { return s.importPrices(ctx, tx, id, f, in.Models, res) }); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.changed(ctx, "prices")
	httpapi.OK(c, res)
}

// importPrices writes the selected models of f from the source id, counting
// them in res, then records the sync on the source and in the audit log.
func (s *Service) importPrices(ctx context.Context, tx *gorm.DB, id int64, f *fetched, models []string, res *SyncResult) error {
	cur, err := s.currentPrices(ctx, tx)
	if err != nil {
		return err
	}
	uid, _ := core.UserID(ctx)
	for _, name := range x.Unique(models) {
		if err := importPrice(ctx, tx, syncedRow{source: id, by: uid, model: name}, f.prices[name], cur[name], res); err != nil {
			return err
		}
	}
	g := store.G(ctx, tx)
	if err := setSourceStatus(g, id, map[string]any{"last_synced_at": gorm.Expr("now()"), "last_error": ""}).Error; err != nil {
		return err
	}
	return audit.Audit(ctx, g, uid, "price.sync", "price_source", fmt.Sprint(id), map[string]any{"created": res.Created,
		"updated": res.Updated, "unchanged": res.Unchanged, "skipped": len(res.Skipped)})
}

// syncedRow names a price an import writes: its model, the source and the
// administrator.
type syncedRow struct {
	source, by int64
	model      string
}

// importPrice writes the incoming price p of one selected model over the
// local price c (nil: none), or counts it unchanged or skipped.
func importPrice(ctx context.Context, tx *gorm.DB, row syncedRow, p *syncPrice, c *SyncCurrent, res *SyncResult) error {
	if p == nil {
		res.Skipped = append(res.Skipped, SyncSkip{Model: row.model, Reason: "not_in_source"})
		return nil
	}
	if syncAction(p, c) == "unchanged" {
		res.Unchanged++
		return nil
	}
	prog, err := expr.CompileCached(p.Expression)
	if err != nil {
		res.Skipped = append(res.Skipped, SyncSkip{Model: row.model, Reason: "invalid_expression"})
		return nil
	}
	if err := recordHistory(ctx, tx, prog); err != nil {
		return err
	}
	g := store.G(ctx, tx)
	if c == nil {
		res.Created++
		return g.Create(&syncedPrice{Model: row.model, Mode: p.Mode, Config: p.Config, Expression: p.Expression, ExprVersion: p.version,
			ExprHash: p.hash, Source: SourceSync, SyncSourceID: row.source, Enabled: true, UpdatedBy: nullID(row.by)}).Error
	}
	res.Updated++
	return updatePriceRow(g, c.ID, map[string]any{
		"mode": p.Mode, "config": p.Config, "expression": p.Expression, "expr_version": p.version, "expr_hash": p.hash,
		"source": SourceSync, "sync_source_id": row.source, "synced_at": gorm.Expr("now()"), "updated_by": nullID(row.by),
	}).Error
}

// syncedPrice is the model_prices row of a price a sync imports: every
// column written as given, synced_at the database's now(), and nothing read
// back.
type syncedPrice struct {
	Model        string          `gorm:"column:model"`
	Mode         string          `gorm:"column:mode"`
	Config       json.RawMessage `gorm:"column:config"`
	Expression   string          `gorm:"column:expression"`
	ExprVersion  int             `gorm:"column:expr_version"`
	ExprHash     string          `gorm:"column:expr_hash"`
	Source       string          `gorm:"column:source"`
	SyncSourceID int64           `gorm:"column:sync_source_id"`
	SyncedAt     store.DBNow     `gorm:"column:synced_at"`
	Enabled      bool            `gorm:"column:enabled"`
	Note         string          `gorm:"column:note"`
	UpdatedBy    *int64          `gorm:"column:updated_by"`
}

func (syncedPrice) TableName() string { return model.ModelPrice{}.TableName() }
