package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

var errNotPending = errors.New("usage row is not pending")

// settle computes the cost and, in one transaction, writes the ledger row,
// the usage billing fields and the usage.recorded event.
func (s *Service) settle(ctx context.Context, p *pending, skipLocked bool) error {
	var err error
	var ledgerRes *core.LedgerResult
	err = s.db.Tx(ctx, func(tx *gorm.DB) error {
		var err error
		ledgerRes, err = s.settleTx(ctx, tx, p, skipLocked)
		return err
	})
	if errors.Is(err, errNotPending) {
		return nil
	}
	if err != nil {
		return err
	}
	s.cacheBalance(ctx, p.UserID, ledgerRes)
	return nil
}

func (s *Service) markFailed(ctx context.Context, p *pending, cause error) {
	attempts := p.Attempts + 1
	slog.WarnContext(ctx, "usage: settlement failed", "request_id", p.RequestID, "attempts", attempts, "err", cause)
	if attempts >= s.opts.MaxAttempts {
		slog.ErrorContext(ctx, "usage: settlement gave up", "request_id", p.RequestID, "attempts", attempts, "err", cause)
	}
	patch := jsonOr(map[string]any{"attempts": attempts, "error": trunc(cause.Error(), 1000), "inputs": p.Inputs}, "{}")
	// Only a row still unsettled; patch is merged into its billing detail.
	if err := store.G(ctx, s.db.Gorm).Model(&model.UsageLog{}).
		Where("request_id = ? AND billing_status IN ('pending', 'failed')", p.RequestID).
		Updates(map[string]any{
			"billing_status": StatusFailed,
			"billing_detail": gorm.Expr("billing_detail || ?::jsonb", patch),
		}).Error; err != nil {
		slog.ErrorContext(ctx, "usage: mark failed", "request_id", p.RequestID, "err", err)
	}
}

func recordedEvent(p *pending, total decimal.Decimal, status string) core.Event {
	t := p.Tokens
	return core.Event{Type: core.EventUsageRecorded, Payload: map[string]any{
		"request_id": p.RequestID, "user_id": p.UserID, "api_key_id": p.APIKeyID, "group_id": p.GroupID,
		"account_id": p.AccountID, "plugin_key": p.PluginKey, "platform": p.Platform, "protocol": p.Protocol,
		"account_type": p.AccountType, "upstream_protocol": p.UpstreamProtocol,
		"model": p.Model, "success": p.Success, "status_code": p.StatusCode, "error_type": p.ErrorType,
		"input_tokens": t.Input, "output_tokens": t.Output, "cache_read_tokens": t.CacheRead,
		"cache_creation_tokens": t.CacheCreation, "cache_creation_1h_tokens": t.CacheCreation1h,
		"image_input_tokens": t.ImageInput, "image_output_tokens": t.ImageOutput,
		"total_cost": total, "billing_status": status, "latency_ms": p.LatencyMs,
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339Nano),
	}}
}

// ---------------------------------------------------------------- retry

// SettlementRetryTask is the node task that retries pending settlements.
const SettlementRetryTask = "core.usage.settlement_retry"

// RetryTaskSpec is core.usage.settlement_retry: every Options.RetryInterval
// on each node that may work, RetryPending (rows are shared through SKIP
// LOCKED and settled under their idempotency keys).
func (s *Service) RetryTaskSpec() core.TaskSpec {
	return core.TaskSpec{
		Name:  SettlementRetryTask,
		Title: core.LocalizedText{"en": "Retry pending settlements", "zh": "重试待结算用量"},
		Description: core.LocalizedText{
			"en": "Releases expired pre-charges and settles again the usage rows that are still pending or failed; every node that may work takes its share.",
			"zh": "释放过期的预扣，并重新结算仍处于待结算或失败的用量记录；每个可工作节点都跑，各自分摊。",
		},
		Scope:       core.TaskNode,
		Every:       s.opts.RetryInterval,
		Timeout:     5 * time.Minute,
		RequireWork: true,
		Handler: func(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
			n, err := s.RetryPending(ctx)
			if n > 0 {
				slog.InfoContext(ctx, "usage: retried pending settlements", "rows", n)
			}
			return core.TaskResult{Count: n}, err
		},
	}
}

// frozenPrice reads usage_logs u back for pricing, the pendingColumns and
// more: h holds the expression the row froze, by its hash in the price
// history, and mp the current expression of its price for a row whose hash
// is not there. A row under a custom price rule gets no mp: its price_id is
// the global price, not what it was priced at, so without its history it
// has no expression and priceOne refuses it.
func frozenPrice(db *gorm.DB, more string) *gorm.DB {
	return db.Table("usage_logs u").Select(pendingColumns + more + ", COALESCE(h.expression, mp.expression, '')").
		Joins("LEFT JOIN model_price_history h ON h.expr_hash = u.expr_hash").
		Joins(`LEFT JOIN model_prices mp ON mp.id = u.price_id AND h.expr_hash IS NULL
			AND u.billing_detail #> '{inputs,pricing,base}' IS NULL`)
}

// pendingColumns are the usage_logs u columns of a pending, in scanPending's
// order.
const pendingColumns = `u.request_id, u.user_id, u.api_key_id, u.group_id, u.account_id, u.plugin_key, u.platform,
		u.protocol, u.account_type, u.upstream_protocol, u.model, u.success, u.status_code, u.error_type,
		u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_creation_tokens, u.cache_creation_1h_tokens,
		u.image_input_tokens, u.image_output_tokens, u.metrics, u.latency_ms, u.created_at, u.rate_multiplier, COALESCE(u.price_id, 0), u.billing_mode, u.billing_detail`

// scanPending reads a frozenPrice row into p: pendingColumns, then more,
// then the expression.
func scanPending(row store.RowScanner, p *pending, more ...any) error {
	var metrics, detail []byte
	dest := append([]any{&p.RequestID, &p.UserID, &p.APIKeyID, &p.GroupID, &p.AccountID, &p.PluginKey, &p.Platform,
		&p.Protocol, &p.AccountType, &p.UpstreamProtocol, &p.Model, &p.Success, &p.StatusCode, &p.ErrorType,
		&p.Tokens.Input, &p.Tokens.Output, &p.Tokens.CacheRead, &p.Tokens.CacheCreation, &p.Tokens.CacheCreation1h,
		&p.Tokens.ImageInput, &p.Tokens.ImageOutput, &metrics, &p.LatencyMs, &p.CreatedAt, &p.Rate, &p.PriceID, &p.Mode, &detail}, more...)
	if err := row.Scan(append(dest, &p.Expression)...); err != nil {
		return err
	}
	_ = json.Unmarshal(metrics, &p.Metrics)
	var pd pendingDetail
	_ = json.Unmarshal(detail, &pd)
	p.Inputs, p.Attempts = pd.Inputs, pd.Attempts
	return nil
}

// RetryPending settles pending/failed rows older than Options.RetryAfter
// that have not exhausted their attempts. It returns the rows examined.
func (s *Service) RetryPending(ctx context.Context) (int, error) {
	if s.opts.CanRetry != nil && !s.opts.CanRetry() {
		return 0, nil
	}
	if s.ledger == nil {
		return 0, errors.New("no ledger configured")
	}
	if err := s.ledger.ReleaseExpiredPrecharges(ctx); err != nil {
		return 0, err
	}
	if err := s.recoverExecutions(ctx); err != nil {
		slog.WarnContext(ctx, "usage: recover execution observations", "err", err)
	}
	list, err := s.retryable(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range list {
		if ctx.Err() != nil {
			break
		}
		if err := s.settle(ctx, p, true); err != nil {
			s.markFailed(ctx, p, err)
		}
	}
	return len(list), nil
}

// retryable loads up to 500 pending/failed rows older than
// Options.RetryAfter that have not exhausted their attempts, oldest first.
func (s *Service) retryable(ctx context.Context) ([]*pending, error) {
	return store.CollectRows(frozenPrice(store.G(ctx, s.db.Gorm), "").
		Where(`u.billing_status IN ('pending', 'failed')
		  AND u.created_at < now() - make_interval(secs => ?)
		  AND COALESCE((u.billing_detail->>'attempts')::int, 0) < ?`, s.opts.RetryAfter.Seconds(), s.opts.MaxAttempts).
		Order("u.id").Limit(500), func(rows *sql.Rows) (*pending, error) {
		p := &pending{}
		return p, scanPending(rows, p)
	})
}

func (s *Service) settleTx(ctx context.Context, tx *gorm.DB, p *pending, skipLocked bool) (*core.LedgerResult, error) {
	if err := store.RequireTx(tx); err != nil {
		return nil, err
	}
	cost, detail, exprHash, err := s.priceOf(ctx, p)
	if err != nil {
		return nil, err
	}
	total := cost.total
	g := store.G(ctx, tx)
	if err := lockPending(g, p.RequestID, skipLocked); err != nil {
		return nil, err
	}
	ledgerRes, err := s.settleLedgerTx(ctx, tx, p.UserID, p.RequestID, total)
	if err != nil {
		return nil, err
	}
	if total.Sign() > 0 {
		detail.LedgerID = &ledgerRes.LedgerID
	}
	if err := markBilled(g, p, cost, detail, exprHash); err != nil {
		return nil, err
	}
	if s.events != nil {
		return ledgerRes, s.events.Emit(ctx, tx, recordedEvent(p, total, StatusBilled))
	}
	return ledgerRes, nil
}

// lockPending locks the usage row of requestID FOR UPDATE; errNotPending
// when it is not pending or failed, or, with skipLocked, when another
// settlement holds it (no row).
func lockPending(g *gorm.DB, requestID string, skipLocked bool) error {
	if err := store.RequireTx(g); err != nil {
		return err
	}
	lock := store.ForUpdate()
	if skipLocked {
		lock = lock.SkipLocked()
	}
	var status string
	if err := g.Model(model.UsageLog{}).Select("billing_status").Where("request_id = ?", requestID).
		Clauses(lock).Row().Scan(&status); err != nil {
		if store.IsNoRows(err) {
			return errNotPending
		}
		return err
	}
	if status != StatusPending && status != StatusFailed {
		return errNotPending
	}
	return nil
}

// markBilled writes the settled cost of p onto its usage row.
func markBilled(g *gorm.DB, p *pending, cost amounts, detail BillingDetail, exprHash string) error {
	var priceID *int64
	if p.PriceID > 0 {
		priceID = &p.PriceID
	}
	// The model is a value: an UPDATE through a pointer to one copies each
	// assigned value into it, on every settlement, for nobody to read.
	return g.Model(model.UsageLog{}).Where("request_id = ?", p.RequestID).Updates(map[string]any{
		"total_cost": cost.total, "billing_status": StatusBilled, "billing_detail": store.Bytes(jsonOr(detail, "{}")),
		"matched_tier": trunc(detail.Tier, 100), "expr_hash": exprHash, "billing_mode": p.Mode, "price_id": priceID,
		"rate_multiplier": p.Rate, "base_cost": cost.base,
	}).Error
}
