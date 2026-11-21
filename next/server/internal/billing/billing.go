// Package billing implements model prices (core.Pricer, price sync sources),
// the balance ledger (core.Ledger), the pre-request balance check
// (core.BalanceGate) and their console APIs (CONTRACTS §5.5).
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Service is the billing module. It implements core.Pricer,
// core.BalanceGate and core.Ledger.
type Service struct {
	db       *store.DB
	rdb      redis.UniversalClient
	bus      core.Bus
	events   core.EventPublisher
	registry core.PluginRegistry

	cacheTTL time.Duration

	slotsOnce      sync.Once
	prechargeSlots chan struct{}

	mu       sync.Mutex
	epoch    uint64
	prices   *priceSnapshot
	resolved map[core.PriceQuery]resolved
	settings *settingsSnapshot

	sync SyncDeps

	// Authz tells whether a caller may read users, which decides how the
	// pricing endpoints name a personal segment; nil counts as "no".
	Authz core.Authorizer
	// Users decides whose balance an operator may adjust; nil refuses all.
	Users core.UserGuard

	unsubscribe func()
}

var (
	_ core.Pricer                 = (*Service)(nil)
	_ core.BalanceGate            = (*Service)(nil)
	_ core.Ledger                 = (*Service)(nil)
	_ core.Quoter                 = (*Service)(nil)
	_ core.ClientDisconnectSource = (*Service)(nil)
)

// New builds the billing service. bus and registry may be nil: without a
// bus, caches only refresh on their TTL (and on local changes); without a
// registry, u() keys are not checked against plugin declarations.
func New(db *store.DB, rdb redis.UniversalClient, bus core.Bus, events core.EventPublisher, registry core.PluginRegistry) *Service {
	s := &Service{
		db:       db,
		rdb:      rdb,
		bus:      bus,
		events:   events,
		registry: registry,
		cacheTTL: time.Minute,
		resolved: map[core.PriceQuery]resolved{},
	}
	if bus != nil {
		s.unsubscribe = bus.Subscribe(core.ChannelConfigChanged, func([]byte) { s.invalidate() })
	}
	return s
}

// Close stops the config:changed subscription.
func (s *Service) Close() {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
}

// invalidate drops the price and settings caches.
func (s *Service) invalidate() {
	s.mu.Lock()
	s.epoch++
	s.prices = nil
	s.settings = nil
	s.resolved = map[core.PriceQuery]resolved{}
	s.mu.Unlock()
}

// changed invalidates locally and tells the other nodes.
func (s *Service) changed(ctx context.Context, key string) {
	s.invalidate()
	if s.bus == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"key": key})
	if err := s.bus.Publish(context.WithoutCancel(ctx), core.ChannelConfigChanged, payload); err != nil {
		slog.WarnContext(ctx, "billing: publish config:changed", "err", err)
	}
}

// PriceRulesChanged drops the cached price rules on every node. Packages
// that delete or copy price_rules rows on their own (a segment going away)
// call it after their transaction committed.
func (s *Service) PriceRulesChanged(ctx context.Context) { s.changed(ctx, "price_rules") }

// RegisterRoutes mounts the billing console API.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	s.registerPriceRoutes(r)
	s.registerSyncRoutes(r)
	s.registerBalanceRoutes(r)
	s.registerSettingsRoutes(r)
	s.registerPricingRoutes(r)
}

// Quote is the single entry point for turning usage into money. It compiles
// the price expression, normalizes tokens, evaluates the expression, applies
// the rate multiplier and rounds to 8 decimal places. Precharge, settlement,
// reserve and reconcile all call this to ensure consistent pricing.
func (s *Service) Quote(ctx context.Context, in core.QuoteInputs) (decimal.Decimal, error) {
	if in.Expression == "" {
		return decimal.Zero, errors.New("price expression not found")
	}
	p, err := expr.CompileCached(in.Expression)
	if err != nil {
		return decimal.Zero, fmt.Errorf("compile price expression: %w", err)
	}
	vars := expr.Normalize(in.UsageSemantics, expr.Tokens(in.Tokens), p.Uses)
	at := in.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	params := make(map[string]string, len(in.PriceParams))
	for k, v := range in.PriceParams {
		if s, ok := v.(string); ok {
			params[k] = s
		}
	}
	result, err := p.Eval(expr.Input{
		Vars:    vars,
		Metrics: in.Metrics,
		Params:  params,
		Headers: in.PriceHeaders,
		At:      at,
	})
	if err != nil {
		return decimal.Zero, err
	}
	return FinalCost(result.Cost, in.RateMultiplier), nil
}
