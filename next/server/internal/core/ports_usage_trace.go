package core

import (
	"time"
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
