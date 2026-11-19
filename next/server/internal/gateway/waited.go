package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/execfetch"
)

// The relay that waits (design §4.3): an endpoint whose usage the plugin
// reads, or a task submission. The request is sent inside the plugin's
// Execute call (ExecutionClient.Relay, on the runtime's goroutine while the
// handler's waits in Execute); the plugin gets the bounded observation and
// states the facts in Execute's return value. The execution's records make
// those facts durable before anything is published: BeginExecution before
// the send, ObserveExecution with the core's reading once the exchange
// ended, then CommitExecution with the facts or CloseExecutionUnreported
// without them (a free record, the precharge released, nothing billed).
// When the plugin gives up its call meanwhile the upstream is cut.

// commitTimeout bounds the records' writes of one execution, a commit with
// its retries included.
const commitTimeout = core.ExecutionFinishTimeout

// commitRetryDelay spaces the retries of a failed commit.
const commitRetryDelay = 250 * time.Millisecond

// relayState is how the exchange of a relay that waits ended, which decides
// how the attempt settles once Execute returned (settle).
type relayState int

const (
	// relaySent: nothing came back to settle - the execution could not be
	// recorded, the request failed without an answer for the plugin, a task
	// answer could not be received. The attempt is what send decided.
	relaySent relayState = iota
	// relayFailed: the upstream's error answer was handed to the plugin;
	// its classification decides.
	relayFailed
	// relayObserved: a 2xx answer was relayed and the core's reading of it
	// stored (ObserveExecution); the facts Execute returns decide.
	relayObserved
	// relayUnconfirmed: a 2xx answer was relayed but the core's reading
	// could not be stored: the execution owns no usage and the request's
	// own free record is submitted.
	relayUnconfirmed
)

// waitedRelay is the exchange of one relay that waits.
type waitedRelay struct {
	x       *execution
	ctx     context.Context // the plugin's host call; its end cuts the upstream
	id      string
	records core.ExecutionRecords
	state   relayState
	// result is what send decided: the attempt's in state relaySent, the
	// cause in relayUnconfirmed.
	result attemptResult
	// relayFailed: the upstream's error answer handed to the plugin, with
	// the parts kept for the client.
	failure   *pluginv1.ClassifyErrorRequest
	header    http.Header
	raw       []byte
	errorBody *upstreamErrorBody
	// relayObserved: the observation the plugin reads, whether its usage
	// may be read from it, the core's reading, the answer held back until
	// the facts commit, and a task submission's answer.
	observation *pluginv1.ExtractUsageRequest
	extractable bool
	base        *core.UsageRecord
	spool       *bufferedResponse
	taskBody    []byte
	// committing is the record of the facts handed to CommitExecution: the
	// request's once the commit is found to have landed.
	committing *core.UsageRecord
	// alive renews the observed execution until it ends (heartbeat).
	alive heartbeat
}

// errClientLeft is a relay that waits asked for after the client left: it
// is not sent.
var errClientLeft = errors.New("the client left before the relay was sent")

// relay is ExecutionClient.Relay: send req now and hand the plugin exactly
// one of the observation and the upstream's error, or an error when the
// exchange ended without either. From here on the client leaving no longer
// cancels the Execute call (execution.run).
func (x *execution) relay(ctx context.Context, req *pluginv1.UpstreamRequest) (*pluginv1.RelayUpstreamResponse, error) {
	if !x.detach() {
		// The client left before anything was sent: the call is cancelled.
		return nil, errClientLeft
	}
	c := x.c
	w := &waitedRelay{x: x, ctx: ctx, id: uuid.NewString()}
	x.waited = w
	records, ok := c.g.d.Settler.(core.ExecutionRecords)
	if !ok {
		w.result = attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("execution records unavailable"), errTypeInternal)}
		return nil, errors.New(w.result.err.Message)
	}
	w.records = records
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	err := records.BeginExecution(bctx, core.ExecutionIntent{TaskSubmit: c.ep.TaskSubmit(), ID: w.id, RequestID: c.rid,
		PluginKey: x.rt.binding.Plugin.Key, AccountID: x.acc.ID, Attempt: c.rec.Attempts})
	cancel()
	if err != nil {
		w.result = attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("execution could not be recorded").WithCause(err), errTypeInternal)}
		return nil, err
	}
	w.result = c.send(x.network, x, req)
	switch {
	case w.state == relayFailed:
		return &pluginv1.RelayUpstreamResponse{Error: w.failure}, nil
	case w.state == relayObserved:
		return &pluginv1.RelayUpstreamResponse{Observation: w.observation}, nil
	case w.result.err != nil:
		return nil, errors.New(w.result.err.Message)
	}
	return nil, errors.New("the upstream exchange ended without an answer")
}

// fail keeps an upstream failure for the plugin to classify; the attempt's
// result waits for its classification (settle).
func (w *waitedRelay) fail(status int, header http.Header, raw []byte, transportErr string) attemptResult {
	x := w.x
	w.state, w.header, w.raw = relayFailed, header, raw
	w.failure = &pluginv1.ClassifyErrorRequest{Meta: x.c.metaFor(x.rt), Account: x.acct, Status: int32(status),
		Headers: execfetch.FirstHeaderValues(header), BodyPrefix: core.ClassifyPrefix(raw), TransportError: transportErr}
	return attemptResult{kind: attemptReturn}
}

// answer relays a 2xx answer for the plugin to read, then stores the core's
// reading of it.
func (w *waitedRelay) answer(ctx context.Context, resp *http.Response) attemptResult {
	if w.x.c.ep.TaskSubmit() {
		if res, ok := w.receiveTask(ctx, resp); !ok {
			return res
		}
	} else {
		w.forward(ctx, resp)
	}
	return w.read(ctx, resp)
}

// receiveTask reads a task submission's answer whole and keeps it, with the
// task's receipt; ok is false when it cannot (the attempt's end).
func (w *waitedRelay) receiveTask(ctx context.Context, resp *http.Response) (_ attemptResult, ok bool) {
	x, c := w.x, w.x.c
	c.rec.StatusCode = resp.StatusCode
	raw, err := io.ReadAll(io.LimitReader(resp.Body, protocol.MaxTaskSnapshotBytes+1))
	if err != nil || len(raw) > protocol.MaxTaskSnapshotBytes {
		return c.taskFailure(ctx, "task response incomplete", err), false
	}
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	err = c.g.d.Tasks.ReceiveTask(bctx, c.rid, resp.StatusCode, raw, "")
	cancel()
	if err != nil {
		return c.taskFailure(ctx, "task response receipt failed", err), false
	}
	w.taskBody = raw
	c.armUsageExtraction(x.rt, x.acct, resp, &usageCapture{body: raw}, x.body)
	c.rec.Success = true
	return attemptResult{}, true
}

// forward relays the answer to the client: an event stream as it comes,
// anything else into the spool, published once the facts commit. The
// attempt's result is settled from the facts, not from the forwarding.
func (w *waitedRelay) forward(ctx context.Context, resp *http.Response) {
	x, c := w.x, w.x.c
	original := c.c.Writer
	if !isSSE(resp.Header.Get("Content-Type")) {
		w.spool = newBufferedResponse(original)
		c.c.Writer = w.spool
	}
	defer func() { c.c.Writer = original }()
	c.forward(ctx, x.rt, x.acct, resp, x.body, newUsageCapture(x.rt))
}

// read builds the observation the plugin reads from the answer and stores
// the core's reading: relayObserved, or relayUnconfirmed when it could not
// be stored.
func (w *waitedRelay) read(ctx context.Context, resp *http.Response) attemptResult {
	x, c := w.x, w.x.c
	if p := c.usage; p != nil {
		w.observation = &pluginv1.ExtractUsageRequest{Meta: p.meta, Account: p.account, Status: int32(p.status), Headers: p.headers,
			Body: p.cap.body, Events: p.cap.events, Fields: p.fields}
		w.extractable = x.rt.pluginUsage && !c.ep.TaskSubmit()
	} else {
		w.observation = &pluginv1.ExtractUsageRequest{Meta: c.metaFor(x.rt), Account: x.acct, Status: int32(resp.StatusCode)}
	}
	c.usage = nil
	c.rec.LatencyMs = int(c.g.now().Sub(c.start) / time.Millisecond)
	c.stampSticky()
	w.base = cloneUsage(c.rec)
	if err := w.observe(ctx); err != nil {
		w.state = relayUnconfirmed
		return attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("usage recording could not be confirmed").WithCause(err), errTypeInternal)}
	}
	w.state = relayObserved
	return attemptResult{kind: attemptDone}
}

// observe stores the core's reading of the answer: the record the
// execution is closed with when no facts come.
func (w *waitedRelay) observe(ctx context.Context) error {
	c := w.x.c
	reading := cloneUsage(w.base)
	if w.x.rt.pluginUsage {
		reading.UsageExtract = core.UsageExtractFallback
	}
	c.finalizeBillability(ctx, reading, c.ep.Billing)
	o := w.observation
	summary, _ := json.Marshal(map[string]any{"status": c.rec.StatusCode, "body_bytes": len(o.GetBody()), "event_count": len(o.GetEvents()),
		"task_submission": c.ep.TaskSubmit()})
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	if err := w.records.ObserveExecution(bctx, w.id, reading, summary); err != nil {
		return err
	}
	w.alive.start(ctx, w.records, w.id, cmp.Or(c.g.renewInterval, core.ExecutionRenewInterval))
	return nil
}

// heartbeat renews an observed execution (ExecutionRecords.RenewExecution)
// so that the recovery sweep knows its gateway is alive, however long the
// execution takes. It runs from the stored observation until stop - which
// execute defers for the whole attempt, so every exit (commit, close, an
// error, a panic) ends it - or until a renewal finds the row no longer
// observed. One timer per execution: waited executions are rare.
type heartbeat struct {
	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
}

func (h *heartbeat) start(ctx context.Context, records core.ExecutionRecords, id string, every time.Duration) {
	ctx = context.WithoutCancel(ctx) // the execution outlives a client that left
	var renew func()
	renew = func() {
		rctx, cancel := context.WithTimeout(ctx, every)
		err := records.RenewExecution(rctx, id)
		cancel()
		h.mu.Lock()
		defer h.mu.Unlock()
		switch {
		case h.stopped:
		case errors.Is(err, core.ErrExecutionNotObserved):
			h.stopped = true // committed or closed: nothing left to keep alive
		default:
			if err != nil {
				slog.WarnContext(ctx, "gateway: execution renewal failed", "execution", id, "err", err)
			}
			h.timer = time.AfterFunc(every, renew)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.stopped {
		h.timer = time.AfterFunc(every, renew)
	}
}

// stop ends the renewals; a renewal already running does not schedule
// another.
func (h *heartbeat) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	if h.timer != nil {
		h.timer.Stop()
	}
}

// settle ends the attempt once Execute returned: the upstream's error with
// the plugin's classification, the facts of an observed answer, or what
// send decided when neither came.
func (w *waitedRelay) settle(ctx context.Context, res core.ExecutionResult, err error) attemptResult {
	switch w.state {
	case relayFailed:
		return w.classified(ctx, res.Response.GetClassification())
	case relayUnconfirmed:
		return w.unreported(ctx, "observation not stored", errors.New(w.result.err.Message))
	case relayObserved:
		switch {
		case err != nil:
			return w.unreported(ctx, "execute failed: "+err.Error(), err)
		case w.spool != nil && w.spool.err != nil:
			// The held-back answer cannot be delivered (its temp file
			// failed): nothing is committed for an answer the client never
			// gets.
			return w.unreported(ctx, "response not deliverable: "+w.spool.err.Error(), w.spool.err)
		}
		return w.commit(ctx, res.Response)
	}
	return w.result
}

// classified applies the plugin's classification of the upstream's error,
// the default one when it gave none. A task submission is never retried.
func (w *waitedRelay) classified(ctx context.Context, cls *pluginv1.ClassifyErrorResponse) attemptResult {
	x, f := w.x, w.failure
	if cls == nil {
		cls = defaultClassification(int(f.GetStatus()))
	}
	res := x.c.applyClassification(ctx, x.rt, x.acct, int(f.GetStatus()), w.header, w.raw, f.GetTransportError(), cls)
	if x.c.ep.TaskSubmit() {
		res.kind = attemptReturn
	}
	x.c.preserveHTTPError(res.err, int(f.GetStatus()), w.header, w.errorBody)
	return res
}

// commit validates and prices the facts Execute returned and commits them,
// retrying the same commit within commitTimeout (it is idempotent); then the
// held-back answer is published.
func (w *waitedRelay) commit(ctx context.Context, out *pluginv1.ExecuteResponse) attemptResult {
	c := w.x.c
	previous := c.rec
	commit, body, err := w.facts(ctx, out)
	if err == nil {
		w.committing = commit.Record
		err = w.commitWithRetry(ctx, commit)
		if err != nil && c.ep.TaskSubmit() {
			err = w.confirmTask(ctx, err)
		}
	}
	if err != nil {
		c.rec = previous
		return w.unreported(ctx, err.Error(), err)
	}
	c.executionOwnsUsage = true
	if body != nil && w.spool == nil {
		w.spool = newBufferedResponse(c.c.Writer)
		w.spool.Header().Set("Content-Type", "application/json")
		w.spool.WriteHeader(c.rec.StatusCode)
		_, _ = w.spool.Write(body)
	}
	return w.deliver(ctx)
}

// deliver publishes the held-back answer of a committed execution. Whether
// it can depends only on the forwarding itself (w.base, the reading taken
// before the plugin's report): an error the plugin read out of a complete
// 2xx answer makes the record a failure, but the client keeps the bytes.
func (w *waitedRelay) deliver(ctx context.Context) attemptResult {
	c := w.x.c
	if w.spool == nil {
		return attemptResult{kind: attemptDone}
	}
	if w.spool.err != nil || !w.base.Success {
		// The usage is committed although the buffered answer is incomplete;
		// dispatch counts no tokens for an attempt that is not done.
		c.countTokens(context.WithoutCancel(ctx), w.x.acc.ID)
		e := fromCore(core.ErrUnavailable.WithMessage("upstream response incomplete"), errTypeUpstream)
		e.LocalFailure = w.spool.err != nil // the spool failed here, not the account
		return attemptResult{kind: attemptReturn, err: e}
	}
	if err := w.spool.publish(); err != nil {
		slog.WarnContext(ctx, "gateway: committed response delivery failed", "request_id", c.rid, "err", err)
	}
	return attemptResult{kind: attemptDone}
}

// facts turns the report Execute returned into the commit, leaving the
// priced record in c.rec, and the answer to publish for a task (its
// upstream id replaced by the public one). A report of the wrong kind, or
// none, is no fact; nor is host_usage on an endpoint whose usage the plugin
// reads when the host's reading holds no usage (hostUsageChosen).
func (w *waitedRelay) facts(ctx context.Context, out *pluginv1.ExecuteResponse) (core.ExecutionCommit, []byte, error) {
	c, rt := w.x.c, w.x.rt
	c.rec = cloneUsage(w.base)
	commit := core.ExecutionCommit{ID: w.id, Operation: core.ExecutionOpUsage, Record: c.rec}
	var body []byte
	switch r := out.GetReport().(type) {
	case *pluginv1.ExecuteResponse_Usage:
		if c.ep.TaskSubmit() {
			return commit, nil, errors.New("usage reported for a task submission")
		}
		if w.extractable {
			c.applyUsageReport(ctx, rt, rt.binding.Plugin.Key, r.Usage)
		} else {
			c.usageFallback(ctx, rt, "the observation was too large to read the usage from", nil)
		}
		commit.Digest = core.FactDigest(r.Usage)
	case *pluginv1.ExecuteResponse_HostUsage:
		if c.ep.TaskSubmit() {
			return commit, nil, errors.New("host_usage reported for a task submission")
		}
		if rt.pluginUsage {
			if err := c.hostUsageChosen(); err != nil {
				return commit, nil, err
			}
		}
		commit.Digest = core.FactDigest(nil)
	case *pluginv1.ExecuteResponse_Task:
		task, returned, err := w.task(ctx, r.Task)
		if err != nil {
			return commit, nil, err
		}
		commit.Operation, commit.Digest, commit.Task, body = core.ExecutionOpWatch, core.FactDigest(r.Task), task, returned
	default:
		return commit, nil, errors.New("no report")
	}
	c.finalizeBillability(ctx, c.rec, c.ep.Billing)
	commit.Record = c.rec
	if commit.Task != nil {
		commit.Task.Record = c.rec
	}
	return commit, body, nil
}

// task checks a task submission's facts against the answer and registers
// it under the request's public id.
func (w *waitedRelay) task(ctx context.Context, parsed *pluginv1.TaskSubmission) (*core.TaskRegistration, []byte, error) {
	c := w.x.c
	if !c.ep.TaskSubmit() {
		return nil, nil, errors.New("task reported for an endpoint that submits none")
	}
	ref := parsed.GetUpstreamRefId()
	if !refIDRe.MatchString(ref) {
		return nil, nil, errors.New("invalid task identity")
	}
	returned, err := protocol.RewriteTaskID(w.taskBody, c.ep.Task.IDPaths, ref, c.taskPublicID)
	if err != nil {
		return nil, nil, err
	}
	query, ok := c.taskPair(true)
	if !ok {
		return nil, nil, errors.New("missing task query contract")
	}
	snapshot := []byte(parsed.GetSnapshotJson())
	if _, err = protocol.RewriteTaskID(snapshot, query.Task.IDPaths, ref, c.taskPublicID); err != nil {
		return nil, nil, err
	}
	if rv := parsed.GetUsage().GetReserve(); rv != nil && rv.GetRefId() != ref {
		return nil, nil, errors.New("reservation identity mismatch")
	}
	if parsed.GetUsage() != nil {
		c.applyUsageReport(ctx, w.x.rt, c.plugin.Key, parsed.GetUsage())
	}
	return &core.TaskRegistration{PublicID: c.taskPublicID, UpstreamID: ref, Kind: c.ep.Task.Kind, Snapshot: snapshot, IDPaths: query.Task.IDPaths,
		NextCheckAfter: time.Duration(parsed.GetNextCheckAfterSec()) * time.Second, Deadline: time.Duration(parsed.GetDeadlineSec()) * time.Second}, returned, nil
}

// commitWithRetry commits, retrying a storage failure within
// commitTimeout; a conflict (other facts, or the execution closed
// meanwhile) and a refusal of the facts (core.ErrExecutionRefused) are
// final.
func (w *waitedRelay) commitWithRetry(ctx context.Context, commit core.ExecutionCommit) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	for {
		err := w.records.CommitExecution(cctx, commit)
		if err == nil || isConflict(err) || errors.Is(err, core.ErrExecutionRefused) {
			return err
		}
		select {
		case <-cctx.Done():
			return fmt.Errorf("commit failed: %w", err)
		case <-time.After(commitRetryDelay):
		}
	}
}

// confirmTask establishes what a task commit that failed (err) left behind
// before the submission is reported as failed: the last try may have landed
// after the deadline, the task registered and its reserve charged. The
// execution is closed unreported unless it turns out committed, which is
// nil: the task stands and its answer is delivered. Closed, its free record
// is the request's; a close that fails leaves the free record to submit.
// A conflict is final as it is.
func (w *waitedRelay) confirmTask(ctx context.Context, err error) error {
	if isConflict(err) {
		return err
	}
	committed, cerr := w.close(ctx, err.Error())
	if committed {
		return nil
	}
	if cerr == nil {
		w.x.c.executionOwnsUsage = true
	}
	return err
}

// unreported ends an execution whose answer reached the upstream but whose
// facts did not come (or could not be committed). A task is not registered:
// taskFailure leaves the free record for submit (the records' recovery
// never touches a task submission). Any other request whose observation is
// stored is closed without facts - a free record, the precharge released,
// nothing billed - and its records own the request's usage; one whose
// observation could not be confirmed has no execution record to own it, so
// submit writes the same free record (the settler retries it) and releases
// the precharge at once. An answer held back is not published (503), one
// already streamed ends abruptly.
func (w *waitedRelay) unreported(ctx context.Context, reason string, cause error) attemptResult {
	c := w.x.c
	c.usage = nil
	if c.ep.TaskSubmit() {
		return c.taskFailure(ctx, "task execution could not be confirmed", cause)
	}
	recordType := errTypeInternal
	switch {
	case w.state != relayObserved:
		recordType = core.UsageErrorUnreported
		c.rec.SetUnreported(reason)
	case w.closed(ctx, reason):
		// Committed meanwhile by a retry that timed out here: the facts
		// stand, and so does their record (the rate limits count it).
		c.rec, c.executionOwnsUsage = w.committing, true
		return w.deliver(ctx)
	default:
		c.executionOwnsUsage = true
	}
	if c.c.Writer.Written() {
		panic(http.ErrAbortHandler)
	}
	return attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage("usage recording could not be confirmed").WithCause(cause), recordType)}
}

// close closes the execution unreported; it reports whether it turned out
// to be committed already (ErrConflict), else the close's error.
func (w *waitedRelay) close(ctx context.Context, reason string) (committed bool, err error) {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
	defer cancel()
	err = w.records.CloseExecutionUnreported(bctx, core.ExecutionUnreported{ID: w.id, Reason: reason})
	if isConflict(err) {
		return true, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "gateway: close unreported execution", "request_id", w.x.c.rid, "execution", w.id, "err", err)
	}
	return false, err
}

// closed is close reporting only whether the execution was committed.
func (w *waitedRelay) closed(ctx context.Context, reason string) (committed bool) {
	committed, _ = w.close(ctx, reason)
	return committed
}

// isConflict reports core.ErrConflict.
func isConflict(err error) bool {
	var ce *core.Error
	return errors.As(err, &ce) && ce.Code == core.ErrConflict.Code
}

// release frees the held-back answer.
func (w *waitedRelay) release() {
	if w.spool != nil {
		w.spool.close()
	}
}

// cloneUsage is a deep copy of r, the host's cache evidence included.
func cloneUsage(r *core.UsageRecord) *core.UsageRecord {
	raw, _ := json.Marshal(r)
	var out core.UsageRecord
	_ = json.Unmarshal(raw, &out)
	out.Metrics = preserveHostCacheEvidence(r.Metrics, out.Metrics)
	for i := range out.Additional {
		out.Additional[i].Metrics = preserveHostCacheEvidence(r.Additional[i].Metrics, out.Additional[i].Metrics)
	}
	for i := range out.Replacement {
		out.Replacement[i].Metrics = preserveHostCacheEvidence(r.Replacement[i].Metrics, out.Replacement[i].Metrics)
	}
	return &out
}
