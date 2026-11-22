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
// group makes of it (CONTRACTS §97). ID is always the global
// price's; VideoOnly and PerRequest are the effective expression's: a custom
// price's own, the global price's under a multiplier.
type PriceRule struct {
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
	// Scale is the multiplier of a multiplier rule: the list price is the
	// expression times it. nil = none.
	Scale *decimal.Decimal `json:",omitempty"`
	// Base is the global price when a custom rule replaced the expression;
	// nil otherwise. It tells what the usage costs at the global price.
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

// Multiplier is what the expression's result is multiplied by to charge at
// rate: the rate times the rule's scale. It is the one place the two meet,
// so precharge and settlement cannot disagree.
func (r *PriceRule) Multiplier(rate decimal.Decimal) decimal.Decimal {
	if r == nil || r.Scale == nil {
		return rate
	}
	return rate.Mul(*r.Scale)
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

// Pricer resolves price rules and tells the gateway which request inputs an
// expression reads (param()/header()), so they can be captured at request
// time and evaluated later during settlement.
type Pricer interface {
	// Resolve returns the list price q asks for, never nil without an error:
	// ErrPriceNotConfigured when the model has no global price.
	Resolve(ctx context.Context, q PriceQuery) (*PriceRule, error)
	Inputs(rule *PriceRule) (bodyPaths []string, headerNames []string)
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
