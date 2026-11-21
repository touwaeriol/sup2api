package billing

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing/expr"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Price sources: administrators type prices in (manual) or import them from
// a price sync source (sync). Plugins never provide prices.
const (
	SourceManual = "manual"
	SourceSync   = "sync"
)

type priceSnapshot struct {
	at      time.Time
	byModel map[string]core.PriceRule // one price per complete model id
	bases   catalog                   // the same prices as the pricing endpoints show them
	rules   ruleSet                   // price rules of groups and segments
}

// priceTraits is what the expression of a price tells of the requests it
// serves: a price that reads video usage facts serves video requests only; a
// visual price that reads no token count is due per request.
type priceTraits struct{ videoOnly, perRequest bool }

func traitsOf(mode string, program *expr.Program) priceTraits {
	return priceTraits{
		videoOnly:  slices.ContainsFunc(program.Facts(), func(fact string) bool { return strings.HasPrefix(fact, "video_") }),
		perRequest: mode == expr.ModeVisual && len(program.UsedVars()) == 0,
	}
}

// apply sets the traits on r.
func (t priceTraits) apply(r *core.PriceRule) { r.VideoOnly, r.PerRequest = t.videoOnly, t.perRequest }

type resolved struct {
	at   time.Time
	rule *core.PriceRule // nil = no match
}

func (s *Service) snapshot(ctx context.Context) (*priceSnapshot, error) {
	s.mu.Lock()
	snap := s.prices
	epoch := s.epoch
	s.mu.Unlock()
	if snap != nil && time.Since(snap.at) < s.cacheTTL {
		return snap, nil
	}
	// A snapshot lives for cacheTTL: requests read it, not the table.
	var rows []model.ModelPrice
	err := enabledPrices(store.G(ctx, s.db.Gorm), "id", "model", "mode", "expression", "expr_version", "expr_hash", "config").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	snap = &priceSnapshot{at: time.Now(), byModel: map[string]core.PriceRule{}, bases: catalog{}}
	for _, row := range rows {
		r := core.PriceRule{ID: row.ID, Model: row.Model, Mode: row.Mode, Expression: row.Expression,
			ExprVersion: core.ValueOr(row.ExprVersion, 0), ExprHash: row.ExprHash}
		if program, err := expr.CompileCached(r.Expression); err == nil {
			traitsOf(r.Mode, program).apply(&r)
		}
		snap.byModel[r.Model] = r
		snap.bases[r.Model] = PriceDef{Mode: r.Mode, Config: row.Config, Expression: r.Expression}
	}
	if snap.rules, err = loadRules(ctx, s.db.Gorm, ""); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.epoch == epoch {
		s.prices = snap
		s.resolved = map[core.PriceQuery]resolved{}
	}
	s.mu.Unlock()
	return snap, nil
}

// match returns the price of the exact model id or nil.
func (snap *priceSnapshot) match(model string) *core.PriceRule {
	r, ok := snap.byModel[model]
	if !ok {
		return nil
	}
	return &r
}

// resolve returns the list price q asks for, or nil when the model has no
// global price: the first price rule that applies to the group and segment of
// q, else the global price (CONTRACTS §97).
func (snap *priceSnapshot) resolve(q core.PriceQuery) *core.PriceRule {
	global := snap.match(q.Model)
	if global == nil {
		return nil
	}
	rule, source := snap.rules.levels(q).applied()
	return rule.price(*global, source)
}

// Resolve implements core.Pricer. Prices are global per model: the platform
// and account type serving the request do not matter (ARCHITECTURE 7.3). A
// price rule of the caller's group or segment may replace or scale the
// global price; without a global price no rule applies.
func (s *Service) Resolve(ctx context.Context, q core.PriceQuery) (*core.PriceRule, error) {
	s.mu.Lock()
	hit, ok := s.resolved[q]
	s.mu.Unlock()
	var rule *core.PriceRule
	if ok && time.Since(hit.at) < s.cacheTTL {
		rule = hit.rule
	} else {
		snap, err := s.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		rule = snap.resolve(q)
		s.mu.Lock()
		if s.prices == snap {
			s.resolved[q] = resolved{at: snap.at, rule: rule}
		}
		s.mu.Unlock()
	}
	if rule == nil {
		return nil, core.ErrPriceNotConfigured.WithDetails(map[string]any{"model": q.Model})
	}
	return rule.Clone(), nil
}

// Inputs implements core.Pricer: the body paths and header names the
// expression reads, to be captured by the gateway at request time; under a
// custom rule also those the global price (Base) reads, which settlement
// evaluates for the base cost.
func (s *Service) Inputs(rule *core.PriceRule) (bodyPaths []string, headerNames []string) {
	if rule == nil {
		return nil, nil
	}
	sources := []string{rule.Expression}
	if rule.Base != nil {
		sources = append(sources, rule.Base.Expression)
	}
	for _, src := range sources {
		if p, err := expr.CompileCached(src); err == nil {
			bodyPaths, headerNames = append(bodyPaths, p.Params()...), append(headerNames, p.Headers()...)
		}
	}
	slices.Sort(bodyPaths)
	slices.Sort(headerNames)
	return slices.Compact(bodyPaths), slices.Compact(headerNames)
}

// factsFor returns the u() keys declared anywhere in the generation: usage
// rules of every platform (built-in and plugin) and of their endpoints, and
// the per-platform usage overrides of account types, since a price applies to
// a model whichever platform or account type serves it. It returns nil when
// they cannot be determined (no registry or generation).
func (s *Service) factsFor() map[string]bool {
	if s.registry == nil {
		return nil
	}
	gen := s.registry.Current()
	if gen == nil {
		return nil
	}
	return declaredFacts(gen)
}

func declaredFacts(gen core.Generation) map[string]bool {
	facts := map[string]bool{}
	add := func(u *manifest.UsageRules) {
		if u == nil {
			return
		}
		for k := range u.Facts {
			facts[k] = true
		}
	}
	for _, b := range gen.Platforms() {
		add(&b.Platform.Usage)
		for _, e := range b.Platform.Endpoints {
			add(e.Usage)
		}
	}
	for _, b := range gen.AccountTypes() {
		for _, p := range b.Type.Platforms {
			for _, u := range p.Usage {
				add(&u)
			}
		}
	}
	return facts
}
