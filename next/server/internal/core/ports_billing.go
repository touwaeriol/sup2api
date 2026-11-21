package core

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// ============================================================ billing (owner: B billing)

// PriceRule is the list price of a model for one caller: the global
// model_prices row (ARCHITECTURE 7.3), or what a price rule of the caller's
// group or segment makes of it (CONTRACTS §97). ID is always the global
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

// UsageTokens are normalized token counts (see ARCHITECTURE 7.3).
// ImageInput and ImageOutput are the image share of Input and Output (an
// image generation or edit, CONTRACTS §118): part of them, never added to
// them, so Total and Throughput do not count them again. They are omitted
// from JSON when 0, so the billing_detail of every other request (which
// stores PricedUsage) keeps its bytes.
type UsageTokens struct {
	Input           int64
	Output          int64
	CacheRead       int64
	CacheCreation   int64
	CacheCreation1h int64
	ImageInput      int64 `json:",omitempty"`
	ImageOutput     int64 `json:",omitempty"`
}

// UsageRecord is produced by the gateway for every finished request and
// submitted to the Settler, which persists usage_logs, bills and emits
// usage.recorded asynchronously.
type UsageRecord struct {
	// Nonempty Replacement prices verified attempts instead of top-level tokens.
	Replacement []PricedUsage
	Additional  []PricedUsage
	// RequestedModelSegments are the items of Additional and Replacement
	// billed at the requested model's price instead of their own;
	// SegmentsDropped says why the segments could not be read, the request
	// then being billed on its top-level usage (CONTRACTS §120.4).
	RequestedModelSegments []RequestedModelSegment
	SegmentsDropped        string
	RequestID              string
	// ClientRequestID is the client's X-Request-Id, truncated to 128 chars;
	// recorded only, never used as an idempotency key.
	ClientRequestID string
	UserID          int64
	APIKeyID        int64
	GroupID         int64
	AccountID       *int64
	PluginKey       string
	PluginVersion   string
	Platform        string // platform of the client endpoint
	Protocol        string // protocol of the client endpoint
	AccountType     string // type id of the account (declared by PluginKey)
	// UpstreamProtocol is the protocol sent upstream; differs from Protocol
	// when the core converted the request (ARCHITECTURE 6.6), "local" when
	// the core answered a count_tokens request itself.
	UpstreamProtocol string
	Endpoint         string
	Model            string
	UpstreamModel    string
	Stream           bool
	StatusCode       int
	Success          bool
	ErrorType        string // see usage_logs.error_type
	ErrorMessage     string
	Attempts         int
	// Conversion is usage_logs.conversion (§88); nil for a native request
	// no account type refused.
	Conversion *ConversionTrace
	// FailurePolicy contains bounded core-only decisions, persisted in anomalies.
	FailurePolicy string
	// Availability is the model availability precheck's account of this
	// request (AnomalyAvailability), nil when every checked account passed.
	Availability *AvailabilityTrace
	// Scheduling is why accounts of the group were not used for this request
	// (AnomalyScheduling): always set when no account could be picked, and
	// when an account was passed over for a passing reason (rate limit, full
	// concurrency, a failed attempt) on the way to the one that served.
	Scheduling *SchedulingTrace
	// AttemptTrail is each upstream attempt of the request in order
	// (AnomalyAttempts): set only when there was more than one.
	AttemptTrail   *AttemptTrail
	UsageSemantics string // exclusive | inclusive
	Tokens         UsageTokens
	Metrics        map[string]any // plugin usage facts for u("key")
	// PluginDetail is the free-form JSON object a plugin returned in the
	// UsageReport of its Execute call (usage_logs.plugin_detail). It is the only
	// part of a usage record a plugin writes verbatim, billing never reads
	// it, and it must be a JSON object, of any size. nil = "{}".
	PluginDetail RawJSON
	// UsageExtract records how this request's usage was obtained, when that
	// is worth knowing: empty for the declarative rules (the normal case, and
	// nothing is stored), UsageExtractPlugin when the usage the plugin
	// returned from Execute was used, UsageExtractFallback when the plugin
	// returned host_usage instead, so the request is billed from what the
	// declarative rules found, and UsageExtractUnreported when the plugin
	// stated nothing, or took a host reading that holds no usage, and the
	// request is not billed. The gateway also warns; this is the part that survives
	// into usage_logs.
	UsageExtract  string
	StickyRule    string
	StickyHit     bool
	HookDecisions []HookDecision
	// SchedDecisions is what the scheduler.rank plugins changed for this
	// request; empty when none took part (CONTRACTS §24).
	SchedDecisions []SchedDecision
	// ResponseMismatch names the way the upstream response shape disagreed
	// with what the endpoint's manifest declares in endpoint.response
	// (ResponseMismatchSSENotDeclared / ResponseMismatchJSONWhileStream);
	// empty when they agree or no endpoint declaration was available. The
	// gateway records it in usage_logs.anomalies and changes nothing
	// else: the request is forwarded and billed exactly as before.
	ResponseMismatch string
	// Reservation is set when the plugin reported that this request only
	// STARTED the work upstream and gave an estimate of it (CONTRACTS §25.4).
	// The settler then charges the estimate, marks the row "reserved" and
	// registers it in pending_settlements for the reconcile loop. nil is the
	// ordinary case: the usage is final when the response ends.
	Reservation *UsageReservation
	// ReservationDropped says why a Reservation the plugin DID return is not
	// on this record: the gateway found the record unbillable (the endpoint
	// bills "free", an empty estimate on a per-token price) and
	// cleared it. Empty in the ordinary case. It is recorded in
	// usage_logs.anomalies because the alternative - a job that was really
	// started upstream, never pre-charged, never reconciled, and nothing
	// anywhere saying so - is the silent kind of wrong (CONTRACTS §25.5).
	ReservationDropped string
	Billable           bool              // false for endpoint billing=free or zero usage
	Price              *PriceRule        // nil when not billable
	PriceParams        map[string]string // body path -> raw JSON value captured at request time
	PriceHeaders       map[string]string // lower-case header -> value captured at request time
	RateMultiplier     decimal.Decimal
	RateSource         string // where RateMultiplier comes from (RateSource*); "" = the group's rate
	SegmentID          int64  // segment the user was in when the request was priced
	LatencyMs          int
	FirstTokenMs       int
	UploadMs           int // request start until the body was read (CONTRACTS §63)
	ClientIP           string
	UserAgent          string
	NodeID             string
	CreatedAt          time.Time // request start; time functions evaluate against it
}

// AdditionalUsage is an independently metered component, excluding the main
// response counters. The gateway must authorize its model before pricing it.
type AdditionalUsage struct {
	Metrics        map[string]any `json:",omitempty"`
	Kind           string
	Model          string
	UsageSemantics string
	Tokens         UsageTokens
}

// PricedUsage freezes every price input needed for a settlement retry.
type PricedUsage struct {
	Free           bool
	BillingReason  string
	Kind           string
	Model          string
	Price          *PriceRule
	UsageSemantics string
	Tokens         UsageTokens
	Metrics        map[string]any
	PriceParams    map[string]string
	PriceHeaders   map[string]string
	RateMultiplier decimal.Decimal
}

// AttemptUsage is a host-verified replacement component before price binding.
type AttemptUsage struct {
	Kind, Model, UsageSemantics string
	Tokens                      UsageTokens
	Free                        bool
	BillingReason               string
	Metrics                     map[string]any
}

// Response shape mismatches recorded in usage_logs.anomalies
// ("response_mismatch"). endpoint.request.stream / request.streamPath is what
// the *client* asked for; endpoint.response.stream / response.nonStream is the
// shape the endpoint *promises*. The gateway still picks the forwarding mode
// from the upstream Content-Type - a working client must not break because a
// manifest is incomplete - but an endpoint that never declares the shape it
// actually returns has no usage rules for it either, so its tokens are lost
// silently. These markers make that visible.
const (
	// ResponseMismatchSSENotDeclared: upstream answered text/event-stream but
	// the endpoint declares no response.stream. The dangerous direction: the
	// usage.sse rules are usually missing too, so token usage is not counted.
	ResponseMismatchSSENotDeclared = "sse_not_declared"
	// ResponseMismatchJSONWhileStream: upstream answered JSON but the endpoint
	// declares only response.stream (no response.nonStream, which manifest
	// validation allows for an always-streaming endpoint). Harmless in
	// comparison, recorded for symmetry.
	ResponseMismatchJSONWhileStream = "json_while_stream_declared"
)

// How a usage record's tokens were obtained, recorded in
// usage_logs.anomalies ("usage_extract") for the endpoints that declare
// usageSource "plugin". Endpoints using the declarative rules record
// nothing.
const (
	// UsageExtractPlugin: the plugin returned ExecuteResponse.usage and that
	// report is what was billed.
	UsageExtractPlugin = "plugin"
	// UsageExtractFallback: the endpoint's usage is the plugin's to read and
	// the plugin returned ExecuteResponse.host_usage instead (it could not
	// read the observation, or the host would not hand one over). The
	// request was billed from
	// whatever the declarative rules produced while forwarding, which for an
	// endpoint that chose usage.source "plugin" is often nothing - it chose
	// that source precisely because the rules cannot express its usage. This
	// marker is the difference between under-billing and *silent*
	// under-billing.
	UsageExtractFallback = "fallback"
	// UsageExtractPrecharge: the client hung up before the upstream reported
	// its usage, and the request was billed from the pre-upstream estimate it
	// had been pre-charged for (never less than what the response had shown
	// by then). The upstream did the work; without this the hold would have
	// been released and the output given away (CONTRACTS §25.8).
	UsageExtractPrecharge = "precharge"
	// UsageExtractUnreported: the upstream answered a relay that waited and
	// the plugin's Execute returned no valid usage, host_usage or task (it
	// stated nothing or the wrong kind, failed, timed out or crashed, or the
	// commit of its facts kept failing); or, on an endpoint whose usage the
	// plugin reads, it returned host_usage (waited or not) and the host's
	// reading of the served answer holds no usage. The record is free:
	// nothing falls back to the host's reading
	// (ExecutionRecords.CloseExecutionUnreported; the gateway writes the same
	// record itself for a relay that did not wait).
	UsageExtractUnreported = "unreported"
)

// UsageErrorUnreported is the ErrorType (usage_logs.error_type) of the free
// record of an unreported execution.
const UsageErrorUnreported = "usage_unreported"

// SetUnreported makes r the free record of a request whose plugin stated no
// usage facts: the host's reading (identity, latency, status, routing)
// without anything to bill - success false, ErrorType UsageErrorUnreported
// with reason, no tokens, metrics, additional or replacement usage or their
// audit, price or reservation, UsageExtract UsageExtractUnreported. It is
// the one definition of that record: CloseExecutionUnreported writes it, the
// gateway submits it for an execution without a durable observation, a
// plugin's host_usage without a reading and a task submission that could not
// be registered.
func (r *UsageRecord) SetUnreported(reason string) {
	r.Success, r.ErrorType, r.ErrorMessage = false, UsageErrorUnreported, x.Truncate(reason, 1000)
	r.Tokens, r.Metrics, r.Additional, r.Replacement = UsageTokens{}, nil, nil, nil
	r.RequestedModelSegments, r.SegmentsDropped = nil, ""
	r.Billable, r.Price, r.Reservation, r.ReservationDropped = false, nil, nil, ""
	r.UsageExtract = UsageExtractUnreported
}

// Keys of usage_logs.anomalies: facts about how a usage record was produced,
// never about what it costs. The column exists because billing_detail - whose
// name promises a billing breakdown, and which settlement rewrites wholesale -
// had collected two of them (CONTRACTS §26.5).
const (
	// AnomalyResponseMismatch: one of the ResponseMismatch* values above.
	AnomalyResponseMismatch = "response_mismatch"
	// AnomalyUsageExtract: one of the UsageExtract* values above.
	AnomalyUsageExtract = "usage_extract"
	// AnomalyReconcile: the outcome of the reconcile loop for a pre-charged
	// row when that outcome left the ESTIMATE as the charge: ReconcileAbandoned
	// (the upstream never gave an answer) or ReconcileEstimated (the upstream
	// confirmed the work but reported no usage). A row whose real usage came
	// back carries no marker. Both values mean "the tokens and the cost of
	// this row are a plugin's estimate", which is what a revenue summary has
	// to be able to count; they differ in why, which is what an operator
	// looking at the row has to be able to tell.
	AnomalyReconcile = "reconcile"
	// AnomalyReconcileAttempts / AnomalyReconcileError describe that give-up
	// (or, for ReconcileEstimated, the plugin's note on why no usage exists).
	AnomalyReconcileAttempts = "reconcile_attempts"
	AnomalyReconcileError    = "reconcile_error"
	// AnomalyReservation: ReservationDropped - the plugin returned a
	// Reservation the gateway could not honour (UsageRecord.ReservationDropped
	// says why, under AnomalyReservationError). The request was billed as an
	// ordinary one, i.e. for a free endpoint not at all.
	AnomalyReservation      = "reservation"
	AnomalyReservationError = "reservation_error"
	// AnomalyAvailability: the JSON of an AvailabilityTrace - which accounts
	// the model availability precheck (or the per-attempt guard) blocked or
	// let through without an answer, and why. Like failure_policy it is a
	// trace, not an anomaly of the record: summaries do not count it.
	AnomalyAvailability = "availability"
	// AnomalyScheduling: the JSON of a SchedulingTrace - which accounts of
	// the group the scheduler did not use for this request, and why. A trace
	// like the two above: summaries do not count it.
	AnomalyScheduling = "scheduling"
	// AnomalyAttempts: the JSON of an AttemptTrail - the accounts a request
	// that took more than one attempt was sent to, in order, and how each
	// attempt ended. A trace like the three above: summaries do not count it.
	AnomalyAttempts = "attempts"
)

// AttemptTrail is the attempts of one request (one WebSocket connection's
// handshake) in the order they went upstream (CONTRACTS §98): one step per
// UsageRecord.Attempts, bounded at 10 steps.
type AttemptTrail struct {
	V       int           `json:"v"` // 1
	Steps   []AttemptStep `json:"steps"`
	Omitted int           `json:"omitted"`
}

// AttemptStep is one attempt of an AttemptTrail.
type AttemptStep struct {
	AccountID int64 `json:"account_id,omitempty"`
	// AccountRef stands in for AccountID where the caller may not see
	// accounts: the same number is the same account. AccountName is filled
	// where it may. The usage API sets both; neither is stored.
	AccountRef  int    `json:"account_ref,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	Status      int    `json:"status"`
	ErrorType   string `json:"error_type,omitempty"` // empty when the attempt succeeded
	Ms          int64  `json:"ms"`
	Message     string `json:"message,omitempty"`
	// Next is what the core did after the attempt: AttemptSame or
	// AttemptSwitch; empty on the last step.
	Next string `json:"next,omitempty"`
}

// Values of AttemptStep.Next.
const (
	AttemptSame   = "same"   // the next attempt went to the same account
	AttemptSwitch = "switch" // the next attempt went to another account
)

// SchedulingTrace is the scheduler's account of one request (one WebSocket
// connection): how many accounts the group offered, how many could serve the
// request, and each account that was not used with the reason (CONTRACTS
// §24.6). Bounded: at most 40 skips and 16 count keys.
type SchedulingTrace struct {
	V       int    `json:"v"`       // 1
	Outcome string `json:"outcome"` // SchedulingNone | SchedulingBusy | SchedulingPicked
	// Candidates are the schedulable accounts of the group whose type serves
	// the endpoint; Eligible those of them that can serve this request.
	Candidates int              `json:"candidates"`
	Eligible   int              `json:"eligible"`
	Counts     map[string]int   `json:"counts"` // reason -> accounts
	Skipped    []SchedulingSkip `json:"skipped"`
	Omitted    int              `json:"omitted,omitempty"`
}

// SchedulingSkip is one account the scheduler did not use, once per reason.
type SchedulingSkip struct {
	AccountID int64 `json:"account_id,omitempty"`
	// AccountName is filled by the usage API when the caller may see
	// accounts; never stored.
	AccountName string `json:"account_name,omitempty"`
	Reason      string `json:"reason"` // Sched* reason code
	// Detail narrows the reason: the model the account does not serve, the
	// usage kind its type does not account for, the account's status.
	Detail string `json:"detail,omitempty"`
	// Until is when a timed disable or cooldown ends (unix seconds).
	Until int64 `json:"until,omitempty"`
}

// Values of SchedulingTrace.Outcome.
const (
	SchedulingNone   = "none"   // no account could serve the request
	SchedulingBusy   = "busy"   // accounts could, all rate limited or full
	SchedulingPicked = "picked" // an account served after others were passed over
)

// Reason codes of a SchedulingSkip.
const (
	// The account is in the group but not offered: Detail is "disabled",
	// "unschedulable", "auto_disabled" or "cooldown".
	SchedNotSchedulable = "not_schedulable"
	// The account's type has no route for the endpoint (Detail: plugin/type).
	SchedNoRoute = "no_route"
	// The account's model list lacks the request's model (Detail: the model).
	SchedModelNotServed = "model_not_served"
	// The request names a second model (fallbacks, advisor) the account's
	// model list lacks (Detail: the model).
	SchedReferencedModelNotServed = "referenced_model_not_served"
	// The model availability precheck blocked the account (see availability).
	SchedAvailabilityBlocked = "availability_blocked"
	// The failure policy narrowed the retry to other accounts.
	SchedFailurePolicy = "failure_policy"
	// A scheduler.rank plugin gave the account weight 0.
	SchedRankExcluded = "rank_excluded"
	// The request does not convert to the account type's protocol, or the
	// account's loss policy refuses the conversion.
	SchedConversionRefused = "conversion_refused"
	// The account's protocol binding leaves out the endpoint's protocol, one
	// of its type's own platforms (Detail: the protocol).
	SchedProtocolClosed = "protocol_closed"
	// The request would reach the account through a cross-platform
	// conversion, which the account does not enable for the model (Detail:
	// the model).
	SchedConversionNotEnabled = "conversion_not_enabled"
	// The account serves the model on other protocols only (Detail: the
	// model).
	SchedModelProtocolClosed = "model_protocol_closed"
	// A rate limit window of the account is exhausted, or the limiter did
	// not admit the request.
	SchedRateLimited = "rate_limited"
	// Every concurrency slot of the account is taken.
	SchedConcurrencyFull = "concurrency_full"
	// Requests are queueing for the account; a new session is not bound to it.
	SchedQueued = "queued"
	// An attempt on the account failed and the request moved on.
	SchedAttemptFailed = "attempt_failed"
)

// UnschedulableAccount is an account of a group the scheduler is not offered
// (AccountDirectory.Unschedulable).
type UnschedulableAccount struct {
	ID     int64
	Reason string    // disabled | unschedulable | auto_disabled | cooldown
	Until  time.Time // zero when open-ended
}

// AvailabilityTrace is what the model availability precheck of one request
// (one WebSocket turn) decided, recorded when it blocked an account or let
// one through without a definite answer (MODEL-AVAILABILITY-PRECHECK-DESIGN
// §6.1). Bounded: at most 20 samples, 16 count keys and 5 guard samples.
type AvailabilityTrace struct {
	V          int                  `json:"v"`       // 1
	Outcome    string               `json:"outcome"` // AvailabilityBlockedAll | AvailabilityPartial | AvailabilityFailOpen | AvailabilityPass
	Candidates int                  `json:"candidates"`
	Admitted   int                  `json:"admitted"`
	Counts     map[string]int       `json:"counts"` // reason -> accounts
	RetryAt    int64                `json:"retry_at,omitempty"`
	Samples    []AvailabilitySample `json:"samples"`
	Omitted    int                  `json:"omitted,omitempty"`
	Guard      []AvailabilitySample `json:"guard,omitempty"`
}

// AvailabilitySample is one account (or, for a failure of a whole plugin
// call, one plugin) of an AvailabilityTrace.
type AvailabilitySample struct {
	AccountID int64 `json:"account_id,omitempty"`
	// AccountName is filled by the usage API when the caller may see
	// accounts; never stored.
	AccountName string `json:"account_name,omitempty"`
	Plugin      string `json:"plugin,omitempty"`
	Model       string `json:"model,omitempty"`
	Decision    string `json:"decision"` // AvailabilityBlock | AvailabilityFailOpen
	// Reason is a reason code, followed by the plugin's reason for
	// "unavailable" and "plugin_unknown": "unavailable:quota_rejected:7d_fable".
	Reason  string `json:"reason"`
	RetryAt int64  `json:"retry_at,omitempty"`
	Attempt int    `json:"attempt,omitempty"` // guard samples only
	// Batch marks the one sample of a whole plugin call or snapshot read
	// that failed: it never names an account, so a reader can tell it from
	// an account sample whose id was redacted.
	Batch bool `json:"batch,omitempty"`
}

// Values of AvailabilityTrace.Outcome and AvailabilitySample.Decision.
const (
	AvailabilityBlockedAll = "blocked_all"
	AvailabilityPartial    = "partial"
	AvailabilityFailOpen   = "fail_open"
	// AvailabilityPass: the precheck admitted every account; only the guard
	// had something to record.
	AvailabilityPass  = "pass"
	AvailabilityBlock = "block"
)

// Values of AnomalyReconcile.
const (
	// ReconcileAbandoned: the core stopped trying to reconcile (deadline or
	// attempts). The estimate stands as the final charge.
	ReconcileAbandoned = "abandoned"
	// ReconcileEstimated: the plugin answered SETTLED_ESTIMATE - the work
	// finished, the upstream reported no usage. The estimate stands as the
	// final charge.
	ReconcileEstimated = "estimated"
)

// ReservationDropped is the AnomalyReservation value for a reservation the
// gateway received and cleared.
const ReservationDropped = "dropped"

// Anomalies collects the observability markers of a record for
// usage_logs.anomalies; nil when there is nothing unusual to say, which is
// the normal case.
func (r *UsageRecord) Anomalies() map[string]string {
	var m map[string]string
	put := func(k, v string) {
		if v == "" {
			return
		}
		if m == nil {
			m = map[string]string{}
		}
		m[k] = v
	}
	put(AnomalyResponseMismatch, r.ResponseMismatch)
	put(AnomalyUsageExtract, r.UsageExtract)
	put("failure_policy", r.FailurePolicy)
	if r.Availability != nil {
		if raw, err := json.Marshal(r.Availability); err == nil {
			put(AnomalyAvailability, string(raw))
		}
	}
	if r.Scheduling != nil {
		if raw, err := json.Marshal(r.Scheduling); err == nil {
			put(AnomalyScheduling, string(raw))
		}
	}
	if r.AttemptTrail != nil {
		if raw, err := json.Marshal(r.AttemptTrail); err == nil {
			put(AnomalyAttempts, string(raw))
		}
	}
	if r.ReservationDropped != "" {
		put(AnomalyReservation, ReservationDropped)
		put(AnomalyReservationError, r.ReservationDropped)
	}
	return m
}

// UsageReservation is a plugin's statement that this request started work
// upstream whose real usage is not known yet - a video generation job, say -
// together with its own estimate (PLUGIN-EXECUTES-CORE-RECORDS §3.4,
// UsageReport.reserve). It is filled from what the plugin returned and
// nothing else: the estimate is priced, charged and reconciled by the core.
//
// Its point is a hole the pre-request balance check cannot close: that check
// is a cached read that allows a brief overdraft, so a user with $0 can
// submit a thousand jobs before the first of them settles. A reservation
// charges the estimate up front, at submit time, through the ordinary ledger
// path and the ordinary idempotency key.
//
// The ESTIMATE itself is not in here: it is the record's own Tokens and
// Metrics, because until a reconcile says otherwise it IS this request's
// usage - that is what was priced, charged and recorded, and what stays if
// the core ever gives up asking.
type UsageReservation struct {
	// PluginKey is the plugin the core will ask to reconcile this entry: the
	// one whose Execute returned the reservation (the plugin of the account
	// type, which also declares the platform). Filled by the gateway, never
	// by the plugin.
	PluginKey string
	// RefID is the plugin's own id for the work (a task id). It is unique
	// per plugin: (plugin_key, ref_id) is the key of pending_settlements.
	RefID string
	// NextCheckAfter is when the plugin expects an answer to be available.
	// Zero (or negative) leaves the delay to the core's backoff.
	NextCheckAfter time.Duration
	// Deadline is an UPSTREAM FACT the plugin states: how long this entry can
	// be asked about at all (an Ark video task is queryable for 7 days). The
	// core clamps it with its own max_reconcile_age_sec; zero takes the
	// core's default.
	Deadline time.Duration
}

type HookDecision struct {
	PluginKey string `json:"plugin_key"`
	HookID    string `json:"hook_id"`
	Decision  string `json:"decision"` // allow | deny | error_open | error_closed | skipped_breaker
	LatencyMs int    `json:"latency_ms"`
	Note      string `json:"note,omitempty"`
	// AccountID is the attempt's account of a gateway.attempt hook (§85).
	AccountID int64 `json:"account_id,omitempty"`
}

// SchedDecision is what one scheduler.rank plugin changed while the gateway
// scheduled the request (usage_logs.sched_decisions, CONTRACTS §24). Only
// accounts whose priority or weight the plugin actually moved are listed, and
// only when the rewrite was applied: a run the gateway discarded (every
// candidate excluded) records nothing.
type SchedDecision struct {
	PluginKey string       `json:"plugin"`
	Changed   []RankChange `json:"changed"`
}

// RankChange is the final priority/weight one plugin gave one candidate for
// this request; the account's own configuration is untouched.
type RankChange struct {
	AccountID int64 `json:"account_id"`
	Priority  int   `json:"priority"`
	Weight    int   `json:"weight"` // 0 = not used for this request
	Load      int   `json:"load,omitempty"`
}

// Settler accepts usage records without blocking the request path.
type Settler interface {
	Submit(rec *UsageRecord)
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

// Total is the number of tokens processed (all kinds).
func (t UsageTokens) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheCreation + t.CacheCreation1h
}

// Throughput is what rate limits count: the input outside any cache plus the
// output (CONTRACTS §18.1). Under "inclusive" semantics Input also holds the
// cache tokens.
func (t UsageTokens) Throughput(semantics string) int64 {
	input := t.Input
	if semantics == "inclusive" {
		input = max(input-t.CacheRead-t.CacheCreation-t.CacheCreation1h, 0)
	}
	return input + t.Output
}
