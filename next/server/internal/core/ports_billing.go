package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ============================================================ billing (owner: B billing)

// PriceRule is the list price of a model for one caller: the global
// model_prices row (ARCHITECTURE 7.3), or what a price rule of the caller's
// group makes of it, or the group's own price of a model without a global
// price (CONTRACTS §97). VideoOnly and PerRequest are the effective
// expression's: a custom price's own, else the global price's.
type PriceRule struct {
	// ID is always the global price's (model_prices.id, usage_logs.price_id);
	// 0 under a group's price of a model without one.
	ID          int64
	Model       string // complete model id (exact match, no wildcards)
	Mode        string // visual | expression
	Expression  string
	ExprVersion int
	ExprHash    string
	VideoOnly   bool // expressions that read video_* usage facts require declared video support
	// PerRequest marks a visual price that reads no token count (its tiers
	// are priced by the request): it is due once the upstream completed the
	// work, with or without usage, and is admitted as billing type
	// per_request (BillingType). The effective expression's, like VideoOnly.
	PerRequest bool `json:",omitempty"`
	// Source is where the list price comes from (PriceSource*); "" on prices
	// stored before price rules existed, which were all global. Prices
	// frozen before core migration 0123 may say "segment" (a user group's
	// rule, since removed): they settle as any other rule, by Scale and Base.
	Source string `json:",omitempty"`
	// Scale is frozen data only: the multiplier of a multiplier rule, on
	// prices frozen with usage items before core migration 0124 turned
	// those rules into model rates (the list price was the expression times
	// it). Resolve never sets it; usage's settlement applies it to retry
	// such an item as it was priced. nil = none.
	//
	// Compatibility, kept for the usage records priced before 0124 and not
	// settled yet (billing_status pending, failed or reserved). Remove it,
	// with PricingTrace.Scale and usage's frozenRule.scale, in the first
	// release after the check of CONTRACTS §97.4 counts none on every
	// deployment.
	Scale *decimal.Decimal `json:",omitempty"`
	// Base is the global price when a custom rule replaced the expression;
	// nil otherwise, also under a group's price of a model without a global
	// price. It tells what the usage costs at the global price.
	Base *PriceBase `json:",omitempty"`
}

// BillingType is the endpoint billing type (manifest billingTypes) the price
// is admitted under: video for a VideoOnly price, per_request or per_token
// for a visual price as it reads token counts or not, else its mode
// (CONTRACTS §131.1).
func (r *PriceRule) BillingType() string {
	switch {
	case r.VideoOnly:
		return "video"
	case r.Mode != "visual":
		return r.Mode
	case r.PerRequest:
		return "per_request"
	}
	return "per_token"
}

// Clone copies the rule with its scale and base.
func (r *PriceRule) Clone() *PriceRule {
	if r == nil {
		return nil
	}
	c := *r
	if r.Scale != nil {
		scale := *r.Scale
		c.Scale = &scale
	}
	if r.Base != nil {
		base := *r.Base
		c.Base = &base
	}
	return &c
}

// Pricer resolves what a caller pays for a model and tells the gateway
// which request inputs an expression reads (param()/header()), so they can
// be captured at request time and evaluated later during settlement.
type Pricer interface {
	// Resolve returns the list price q asks for, never nil without an
	// error, and levels with the rates set on its model added: its group's,
	// and that of segment segmentID in the group (CONTRACTS §97). Both come
	// from one price snapshot, so a change to prices and rates applies to a
	// call as a whole. ErrPriceNotConfigured when the model has no price
	// in the group: neither the group's own nor an enabled global one.
	Resolve(ctx context.Context, q PriceQuery, segmentID int64, levels RateLevels) (*PriceRule, RateLevels, error)
	Inputs(rule *PriceRule) (bodyPaths []string, headerNames []string)
}

// PricedModels tells which models have a list price in account groups: an
// enabled global price or a price of the group (CONTRACTS §97.1). billing
// implements it from the price snapshot requests are billed by, the one
// place the rule is written; group and lookup ask it.
type PricedModels interface {
	// PricedModels returns, by group id, the models with a list price in
	// each of the groups, sorted; with model set, only it, where it has one.
	PricedModels(ctx context.Context, groupIDs []int64, model string) (map[int64][]string, error)
}

// What a request pays when its client hangs up while the upstream is
// answering (settings "billing".client_disconnect_policy, CONTRACTS §25.8).
const (
	// DisconnectPrecharge stops the upstream and bills the pre-charge
	// estimate: what every request paid before the setting existed.
	DisconnectPrecharge = "precharge"
	// DisconnectContinue, the default, reads the upstream to its end and
	// bills what it reports. The reading is bounded by the same timeouts as
	// any upstream exchange (environment variables, CONTRACTS §74.3); a read
	// that cannot finish falls back to DisconnectPrecharge.
	DisconnectContinue = "continue"
	// DisconnectFree stops the upstream and bills nothing.
	DisconnectFree = "free"
)

// ClientDisconnectSource is implemented by a BalanceGate that carries the
// setting; a gateway whose gate does not uses DisconnectPrecharge.
type ClientDisconnectSource interface {
	ClientDisconnectPolicy(ctx context.Context) (string, error)
}

// BalanceGate requires both the cached balance check and atomic precharge;
// a gateway must never silently run with only the cached check.
type BalanceGate interface {
	RequestPrecharger
	CheckBalance(ctx context.Context, userID int64) error
}

// QuoteInputs holds the parameters for calculating a price quote.
type QuoteInputs struct {
	Expression     string
	UsageSemantics string
	Tokens         UsageTokens
	Metrics        map[string]any
	PriceParams    map[string]any
	PriceHeaders   map[string]string
	RateMultiplier decimal.Decimal
	CreatedAt      time.Time
}

// Quoter provides the unified price calculation entry point. Settlement,
// reserve, reconcile and precharge all call Quote to ensure consistent pricing.
type Quoter interface {
	Quote(ctx context.Context, in QuoteInputs) (decimal.Decimal, error)
}

// LedgerChange is one balance mutation. Amount is always positive; Kind
// decides the sign (usage/plugin_debit subtract, others add unless noted).
type LedgerChange struct {
	UserID         int64
	Amount         decimal.Decimal
	Credit         bool // true adds, false subtracts
	Kind           string
	RefType        string
	RefID          string
	IdempotencyKey string
	OperatorID     *int64
	PluginKey      string
	Note           string
}

type LedgerResult struct {
	LedgerID     int64
	BalanceAfter decimal.Decimal
	Duplicate    bool
	// UserID, Delta, Kind are returned when Duplicate=true to enable conflict detection
	UserID int64
	Delta  decimal.Decimal
	Kind   string
}

// Ledger is the only way to change balances (admin adjust, plugin
// credit/debit, usage settlement). Applies change + ledger row + event in one
// transaction; idempotent on IdempotencyKey.
type Ledger interface {
	Apply(ctx context.Context, ch LedgerChange) (*LedgerResult, error)
	// ApplyTx applies the change inside the caller's transaction (used by
	// settlement so the usage row and the ledger row commit together).
	ApplyTx(ctx context.Context, tx *gorm.DB, ch LedgerChange) (*LedgerResult, error)
}

// BalanceCacher refreshes the balance cache. ApplyTx cannot do it itself:
// the caller's transaction may still roll back, so the caller calls it after
// the commit with the result's ledger id and balance.
type BalanceCacher interface {
	CacheBalance(ctx context.Context, userID, ledgerID int64, balance decimal.Decimal)
}

// CachingLedger is the ledger of a module that credits inside its own
// transaction (redeem codes, top-up orders); billing's *Service is one.
type CachingLedger interface {
	Ledger
	BalanceCacher
}

// LedgerChange.Kind values; billing accepts no other.
const (
	LedgerKindUsage        = "usage"
	LedgerKindAdminAdjust  = "admin_adjust"
	LedgerKindPluginCredit = "plugin_credit"
	LedgerKindPluginDebit  = "plugin_debit"
	LedgerKindRefund       = "refund"
	// Online top-up, its promo bonus and redeem codes (CONTRACTS §140).
	LedgerKindTopup      = "topup"
	LedgerKindPromoBonus = "promo_bonus"
	LedgerKindRedeem     = "redeem"
)

// Caps on the money a user is credited outside an administrator's adjust:
// one credit (a top-up order, a redeem code) is at most MaxCreditPerTx, and a
// user's top-up credits over any 24 hours at most MaxCreditPerDay. The ledger
// itself has no cap; these carry over the old payment plugin's approved
// ledger.credit scope (maxPerTx, maxPerDay).
var (
	MaxCreditPerTx  = decimal.NewFromInt(100000)
	MaxCreditPerDay = decimal.NewFromInt(1000000)
)

// ============================================================ events (writer owner: B; delivery owner: H)

// Event types; payload schemas in docs/CONTRACTS.md.
const (
	EventUsageRecorded        = "usage.recorded"
	EventAccountCreated       = "account.created"
	EventAccountUpdated       = "account.updated"
	EventAccountDeleted       = "account.deleted"
	EventAccountStatusChanged = "account.status_changed"
	EventUserCreated          = "user.created"
	EventUserUpdated          = "user.updated"
	EventBalanceChanged       = "balance.changed"
	EventPluginEnabled        = "plugin.enabled"
	EventPluginDisabled       = "plugin.disabled"
	// EventPluginEgressNewDomain: a plugin connected to a host it never used
	// before (payload {plugin_key, host, port, node_id, first_seen_at}).
	EventPluginEgressNewDomain = "plugin.egress_new_domain"
)

type Event struct {
	Type    string
	Payload any // marshalled to JSON
}

// EventPublisher writes to the events outbox. Use Emit inside the business
// transaction so events are never lost or emitted for rolled-back work.
type EventPublisher interface {
	Emit(ctx context.Context, tx *gorm.DB, events ...Event) error
}

// RawJSON is a helper alias for pre-encoded payloads.
type RawJSON = json.RawMessage
