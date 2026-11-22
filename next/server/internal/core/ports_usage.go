package core

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

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
	put(AnomalyAvailability, marshaled(r.Availability))
	put(AnomalyScheduling, marshaled(r.Scheduling))
	put(AnomalyAttempts, marshaled(r.AttemptTrail))
	if r.ReservationDropped != "" {
		put(AnomalyReservation, ReservationDropped)
		put(AnomalyReservationError, r.ReservationDropped)
	}
	return m
}

// marshaled is v as JSON, or "" when v is nil or does not marshal.
func marshaled[T any](v *T) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
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
