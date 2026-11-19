package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// ============================================================ plugin execution: the one path of a gateway request
//
// Every gateway request goes through PlatformService.Execute of the plugin
// declaring the account type, once per attempt. The plugin receives the whole
// request body, builds the final upstream request and hands it to the host's
// HTTP client; usage and task facts travel only in Execute's return value.
// Owners: the plugin runtime implements ExecutePlugin and MonitorPlugin, the
// gateway supplies the ExecutionClient and acts on the ExecutionResult, the
// usage module implements ExecutionRecords and calls Monitor.

// Host bounds around plugin execution that more than one module names.
const (
	// ExecutionFinishTimeout bounds each write that ends an execution on the
	// gateway: the commit with its retries, then a close.
	ExecutionFinishTimeout = 15 * time.Second
)

// Liveness of an observed execution. While the gateway that owns it is
// alive, it renews the row every ExecutionRenewInterval
// (RenewExecution), from the stored observation until the execution is
// committed or closed; the usage module's recovery sweep closes, as
// unreported, only an observed execution whose last renewal is older than
// ExecutionStaleAfter - its gateway is gone. A live execution has no other
// bound than the existing ones (the upstream and fetch timeouts, a client
// disconnect, the end of the plugin's process). The stale bound is eight intervals:
// a database hiccup that fails a renewal or two (each renewal is one
// statement, bounded by the interval) never closes a live execution;
// only an outage of about two minutes does, and then the gateway's commit
// meets ErrConflict like any execution closed meanwhile. Waited executions
// are rare (endpoints whose usage the plugin reads, task submissions), so
// one timer per execution is cheap enough; nothing is batched.
const (
	ExecutionRenewInterval = 15 * time.Second
	ExecutionStaleAfter    = 2 * time.Minute
)

// ErrExecutionNotObserved is RenewExecution's answer for an execution that
// is no longer observed (committed, unreported) or does not exist: there is
// nothing left to keep alive.
var ErrExecutionNotObserved = errors.New("execution is not observed")

// ErrExecutionRefused marks a refusal of ExecutionRecords that no retry
// changes: facts that do not fit the execution (an invalid commit, another
// identity, no durable observation, a usage record already written). A
// caller retrying failed writes stops at once on it, as on ErrConflict.
var ErrExecutionRefused = errors.New("execution write refused")

// ExecutionHTTP is the fetch form of the host's HTTP client for one Execute
// or Monitor call: one bounded, non-streaming exchange whose answer goes back
// to the plugin. The closure binds the account, its proxy, the SSRF guard
// and the timeouts; it applies neither the operator's header passthrough nor
// overrides and follows no redirect.
//
// The fetches of one call are not counted and their answers are whole:
// neither the runtime nor the core bounds the request body (the gateway
// setting max_request_body_mb applies to the client's request alone). The runtime calls it only after it checked the
// execution token and the URL's host: one of the plugin manifest's externalServices (or a
// subdomain of one), or the host of one of the account's own upstream
// addresses (the values of the address fields its type declares, manifest
// upstreamFields). The implementation need not repeat those. The SSRF policy
// is the implementation's alone: an account address is the operator's entry
// and may name a private target, so every URL goes through the guard
// (netguard, with the allow-private setting of the account's scope) and the
// account's proxy exactly like any other upstream request. A transport failure is not an error:
// it is an answer with Status 0 and TransportError set. An error is for a
// request the host would not send at all. ctx ends when the plugin gives up
// the host call or the Execute/Monitor call ends.
type ExecutionHTTP func(context.Context, *pluginv1.ExecutionHTTPRequest) (*pluginv1.ExecutionHTTPResponse, error)

// ExecutionClient is the host's HTTP client for one Execute call: what the
// plugin's HostService.RelayUpstream and ExecuteHTTP calls reach while that
// call runs. The gateway builds one per attempt, bound to the selected
// account. The runtime binds it to the exact process serving the call and
// drops it when the call returns; a token used afterwards is refused.
//
// The runtime serialises the plugin's host calls of one execution (a second
// one while another is active is FAILED_PRECONDITION), accepts at most one
// relay per execution, and never calls any of these functions after Execute
// returned.
type ExecutionClient struct {
	// Check validates the request of a relay locally, without sending
	// anything: the URL, the SSRF name check, the headers, response_stream
	// and, for a type declaring model availability, the upstream model the
	// plugin names. The body is not inspected (the conformance suite holds
	// plugins to it). The runtime has already refused a nil request, an
	// empty URL and the transport's header names. An error refuses
	// the request: the plugin gets INVALID_ARGUMENT with the error's text
	// and the relay counts as not made. Called for both forms, before the
	// request is parked or Relay is called. Nil accepts.
	Check func(context.Context, *pluginv1.UpstreamRequest) error
	// Relay is the waiting form: send the request now, relay the answer to
	// the client and return when the exchange has ended, with exactly one of
	// Observation (2xx) and Error (non-2xx or transport failure) set. The
	// runtime calls it only for an execution whose ExecuteRequest sets
	// extract_usage or task_submission (otherwise the plugin gets
	// FAILED_PRECONDITION and nothing is called), at most once, after Check
	// passed. ctx ends when the plugin abandons the host call (crash, kill,
	// drain) or the Execute call ends: the implementation must then cut the
	// upstream exchange. An error is passed to the plugin as UNAVAILABLE.
	// Nil means the execution has no waiting form (a websocket turn).
	Relay func(context.Context, *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error)
	// Fetch serves ExecuteHTTP. Nil refuses every fetch.
	Fetch ExecutionHTTP
}

// ExecutionResult is what one Execute call left behind.
type ExecutionResult struct {
	// Response is the plugin's answer; never nil when Execute returned no
	// error.
	Response *pluginv1.ExecuteResponse
	// Relay is the request of a relay that did not wait, for the gateway to
	// send now. It is set only when the plugin made such a relay, Check
	// accepted it and Response reports host_usage; in every other case - no
	// relay, a waited relay, a parked request the plugin did not confirm
	// with host_usage - it is nil and nothing may be sent for the plugin.
	// The message is the gateway's from here on.
	Relay *pluginv1.UpstreamRequest
}

// ExecutePlugin is implemented by the PlatformPlugin of every account type
// served by a plugin process (the Client of an AccountTypeBinding): it is how
// the gateway serves a request with an account of that type.
type ExecutePlugin interface {
	// Execute runs PlatformService.Execute for one attempt on one account.
	// in carries the meta, the account, the whole body, the allowed client
	// headers, the attempt number and the two flags; execution_token is the
	// runtime's to fill (it does not change in).
	//
	// What the runtime guarantees:
	//   - Nothing is sent for a relay that did not wait before Execute has
	//     returned: the request is only checked and kept, and reaches the
	//     caller as ExecutionResult.Relay, and only together with
	//     host_usage. An error, a timeout or a crash of the plugin before
	//     the return leaves nothing sent and nothing to bill; the caller
	//     moves to another account without penalising this one.
	//   - A relay that waited ran inside client.Relay; its facts are the
	//     caller's own. When the plugin crashes or gives up meanwhile,
	//     Relay's ctx ends (the caller cuts the upstream) and Execute
	//     returns an error: the execution is unreported and not billed.
	//   - ctx bounds the whole call. Cancelling it cancels the plugin's
	//     call and every host call in progress. The caller decides whether a
	//     client disconnect reaches ctx; the gateway's rule: it does as long
	//     as nothing was sent upstream for this execution - always for the
	//     form that does not wait, and for the waiting form until client.Relay
	//     starts sending. From then on the client's departure only affects
	//     the upstream exchange, as the disconnect policy decides, and never
	//     the plugin's work after the observation, whose facts must still be
	//     committed.
	//
	// Nothing times the plugin's own work: a hung plugin ends with ctx or
	// with its process. One that hangs after the observation of a relay that
	// waited ends only with its process.
	//
	// Errors: ErrPluginUnavailable when the instance is down, draining or
	// crashed; the context's
	// error when ctx ended; the mapped status of the plugin's own error
	// otherwise. All of them are failures of the plugin, never of the
	// account.
	Execute(ctx context.Context, in *pluginv1.ExecuteRequest, client ExecutionClient) (ExecutionResult, error)
}

// MonitorPlugin is implemented by the PlatformPlugin of a plugin declaring
// platform.monitor.v1: one scheduled observation of a host-managed task.
type MonitorPlugin interface {
	// Monitor runs PlatformService.Monitor once. The plugin queries the
	// upstream through fetch (as often as it needs, its execution token
	// void once the call returns) and states what it read
	// in the result. A nil error comes with a non-nil result: a response
	// without one is an error, and so is a terminal state (anything but
	// PENDING and POLL_FAILED) that no complete, successful fetch of this
	// call backs. The caller treats every error as one failed
	// query. The call is bounded by the runtime's poll timeout (30 s) and
	// by ctx.
	Monitor(ctx context.Context, in *pluginv1.PollRequest, fetch ExecutionHTTP) (*pluginv1.ReconcileResult, error)
}

// ============================================================ execution records (owner: usage)

// States of a plugin_executions row.
const (
	// BeginExecution: identity stored, no durable observation. The request
	// may have been sent: an upstream error answer, a transport or client
	// failure, a refused target or an observation that could not be stored
	// all leave the row here; none of them is billed through it.
	ExecutionStateIntent     = "intent"
	ExecutionStateObserved   = "observed"   // ObserveExecution: the host's reading is durable
	ExecutionStateCommitted  = "committed"  // CommitExecution: the plugin's facts are billed
	ExecutionStateUnreported = "unreported" // CloseExecutionUnreported: no facts, not billed
)

// Operations of an ExecutionCommit (plugin_executions.operation).
const (
	ExecutionOpUsage = "usage" // ExecuteResponse.usage or .host_usage
	ExecutionOpWatch = "watch" // ExecuteResponse.task
)

// ExecutionRecords are the durable records of the waiting form of plugin
// execution (an endpoint whose usage the plugin reads, or a task
// submission). A relay that does not wait writes none: its usage goes
// through Settler.Submit like any forwarded request.
//
// The order per execution is BeginExecution before the request is sent,
// ObserveExecution once the exchange has ended, then exactly one of
// CommitExecution (Execute returned valid facts) and
// CloseExecutionUnreported (it did not). Nothing here falls back to the
// host's reading on its own: an observed execution that is never committed
// is closed as unreported by the caller or, once its gateway is gone, by the
// usage module's recovery sweep, and is not billed.
type ExecutionRecords interface {
	// BeginExecution stores the trusted identity of the execution before
	// any network side effect. A second call with the same ID fails.
	BeginExecution(context.Context, ExecutionIntent) error
	// ObserveExecution stores, for an execution in state intent, the usage
	// record as the host read it (rec, as JSON) and a diagnostic summary of
	// the observation, both of any size. rec is what
	// CloseExecutionUnreported turns into the free record; it is not billed
	// by itself. The request and account of rec must be the execution's.
	ObserveExecution(ctx context.Context, id string, rec *UsageRecord, observation []byte) error
	// RenewExecution marks an observed execution alive now, in one
	// statement that changes nothing else and touches only a row still in
	// state observed: a committed or unreported row is never resurrected or
	// modified. Such a row, or none, is ErrExecutionNotObserved, which ends
	// the renewals. Any other error is a failed renewal; the next one tries
	// again (see ExecutionRenewInterval).
	RenewExecution(ctx context.Context, id string) error
	// CommitExecution accepts the facts Execute returned, in one
	// transaction with the execution row locked: the usage record with its
	// charge and the release of the request's precharge, for ExecutionOpWatch
	// also the task registration with its reserve and monitor, and the row's
	// move to committed. The execution must be observed.
	//
	// It is idempotent per execution: the same Operation and Digest again
	// is a no-op returning nil, so the caller may retry a failed commit with
	// the same facts (it does, within ExecutionFinishTimeout). Other facts
	// for a committed execution are ErrConflict and change nothing. An
	// execution closed as unreported is ErrConflict too. A commit that does
	// not fit the execution wraps ErrExecutionRefused; neither is retried.
	CommitExecution(context.Context, ExecutionCommit) error
	// CloseExecutionUnreported ends an observed execution for which Execute
	// gave no valid facts (nothing stated, the wrong kind, an error, a
	// timeout, a crash, or a commit that kept failing). In one transaction
	// with the row locked it writes the free usage record from the stored
	// observation - success false, ErrorType UsageErrorUnreported, tokens
	// and metrics cleared, billing status free, UsageExtract
	// UsageExtractUnreported - which releases the request's precharge, and
	// moves the row to unreported keeping its record and observation. The
	// account is not penalised; the implementation logs at ERROR with
	// in.Reason.
	//
	// Closing an execution already unreported is a no-op returning nil. A
	// committed one is ErrConflict (its facts stand). One still in state
	// intent has no durable observation and is an error: nothing is written
	// (the gateway submits the request's free record itself, which releases
	// the precharge). When the request already has its usage record - that
	// free record, for an observation stored although its acknowledgement
	// was lost - the row moves to unreported without writing another.
	CloseExecutionUnreported(context.Context, ExecutionUnreported) error
}

// ExecutionIntent identifies one execution before it sends anything.
type ExecutionIntent struct {
	TaskSubmit               bool
	ID, RequestID, PluginKey string
	AccountID                int64
	Attempt                  int
}

// ExecutionCommit is the facts of one execution as the gateway validated
// and priced them.
type ExecutionCommit struct {
	// ID is the execution; Operation one of ExecutionOp*; Digest the
	// FactDigest of what the plugin returned (the UsageReport, nil for
	// host_usage, or the TaskSubmission).
	ID, Operation, Digest string
	// Record is the final usage record; its request, plugin and account
	// must be the execution's.
	Record *UsageRecord
	// Task is set for ExecutionOpWatch and only then.
	Task *TaskRegistration
}

// ExecutionUnreported closes one execution without facts.
type ExecutionUnreported struct {
	ID string
	// Reason says why, for the log: "no report", "wrong report kind",
	// "execute failed: ...", "commit failed: ...".
	Reason string
}

// FactDigest is the digest of the facts a plugin returned for an execution
// or a monitor claim: the hex SHA-256 of the message's deterministic
// encoding, and of nothing for a nil message (host_usage).
func FactDigest(m proto.Message) string {
	var raw []byte
	if m != nil && m.ProtoReflect().IsValid() {
		raw, _ = (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
