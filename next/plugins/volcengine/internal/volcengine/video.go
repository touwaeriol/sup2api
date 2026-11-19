package volcengine

// Ark video (Seedance) uses the core's managed async tasks. The host durably
// binds owner/account/model and returns its own public task ID before success.
// A single claimed core poll supplies both billing and the shared query snapshot.
//
//	POST /api/v3/contents/generations/tasks        video_submit (usageSource plugin)
//	GET  /api/v3/contents/generations/tasks/:id    video_query  (core snapshot, billing free)
//
// Both are also served under /doubao/api/v3 (endpoints doubao_video_submit /
// doubao_video_query, same protocols, task kind "doubao_video"). The plugin
// sees only the protocol, never the client path, so nothing here depends on
// which prefix the client used; the response carries the core's public task
// ID, not a URL, so there is no polling path to keep in step with the prefix.
//
//	ParseTaskSubmission    parses the upstream ID, initial snapshot and estimate
//	videoUsage             computes that estimate without storing task identity
//	Monitor                one query through the host's fetch, result returned
//
// The submit is relayed by the host, which waits and hands back the bounded
// answer; client queries read the host's snapshot. The core schedules Monitor
// and binds its execution context to the original account, proxy and
// deadline. Monitor requests exactly one HTTP exchange through that context
// and never opens its own socket or retries. Response parsing needs no plugin
// database.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// Protocol ids of the two video endpoints declared in manifest.json.
const (
	// ProtocolVideoSubmit is Seedance task submission, served to clients at
	// POST /api/v3/contents/generations/tasks. Its usage is read by the
	// plugin (usageSource "plugin") because the submit response carries only
	// the task id: the real usage is not known until the task finishes, so
	// the submission carries a Reservation instead of a usage number.
	ProtocolVideoSubmit = "volcengine.video_submit"
	// ProtocolVideoQuery is the client's poll of one task, GET
	// /api/v3/contents/generations/tasks/:task_id. The request has no model,
	// so the core looks it up from the managed task; the endpoint is
	// billing "free" because charging happens only in the reconcile loop, no
	// matter how many times a client polls.
	ProtocolVideoQuery = "volcengine.video_query"
)

// TaskIDParam is the path parameter of the query endpoint, and the key the
// core fills in RequestMeta.path_params (CONTRACTS §25.1).
const TaskIDParam = "task_id"

// videoTasksPath is Ark's own task collection, under the account's video
// prefix. It is the upstream path, independent of the client-facing one
// (/api/v3/... or /doubao/api/v3/...).
//
// A function rather than a constant because the prefix is per account: a relay
// may mount Ark's native video tasks under its own namespace
// ("/doubao/api/v3") while serving text at the root, and the first real
// upstream this plugin was verified against does exactly that.
func videoTasksPath(prefix string) string { return prefix + "/contents/generations/tasks" }

// Submission, direct query and background polling resolve the same collection.
// The endpoint override is populated only for the relay account type.
func videoTaskURL(base string, px prefixes, id string) (string, error) {
	var collection string
	var err error
	if px.videoEndpoint != "" {
		collection, err = resolveEndpoint(base, px.videoEndpoint)
	} else {
		collection, err = upstreamURL(base, videoTasksPath(px.video))
	}
	if err != nil {
		return "", err
	}
	if id != "" {
		collection += "/" + url.PathEscape(id)
	}
	return collection, nil
}

// Resolution tiers: the vocabulary of the "resolution" fact declared on the
// video_submit endpoint, and the tiers the token estimate understands.
const (
	Res480  = "480p"
	Res720  = "720p"
	Res1080 = "1080p"
	Res4K   = "4k"
)

// FactResolution is the metering fact key the endpoint declares.
const FactResolution = "resolution"

// VideoResolutions is the enum declared for the resolution fact, and the tiers
// the pixel table in videospec.go has areas for. Ark publishes no "2k", so
// there is none here.
var VideoResolutions = []string{Res480, Res720, Res1080, Res4K}

// DeadlineSec is how long a task can be reconciled at all: Ark keeps a video
// task queryable for 7 days, after which no answer exists to be had. The
// plugin states this upstream fact; the core clamps it with the
// max_reconcile_age_sec setting, whose default is also 7 days (604800) since
// CONTRACTS §25.6 - so this value passes through unchanged and a task running
// for days is reconciled rather than abandoned on its estimate.
const DeadlineSec = 7 * 24 * 60 * 60

// firstCheckSec / runningCheckSec pace the reconcile poll. Seedance tasks
// finish in tens of seconds to a few minutes, so there is no point asking in
// the first few seconds. The core clamps both into its own 5s..6h range and
// uses them instead of its backoff ladder, because the plugin is the only
// party that knows how long this upstream takes (CONTRACTS §25.4).
const (
	firstCheckSec   = 20
	runningCheckSec = 30
)

// The estimate itself lives in videospec.go. What used to be here - a
// tier-to-16:9-pixels switch, a fixed 10-second assumption and a
// ModelMaxResolution table - could not do better, because the usage reading was not
// shown the request (CONTRACTS §25.5 gap 1). It is now, so the estimate reads
// the requested resolution, ratio and duration instead of guessing them, and
// uses Ark's own per-generation pixel table rather than assuming 16:9.

// ---------------------------------------------------------------- submission

// taskIDPaths are the response fields a task id may arrive in, in order. Ark
// documents "id"; the others are cheap insurance against a relay that wraps
// the answer, and cost one gjson probe on a path that only runs once per
// submit. It equals every task endpoint's task.idPaths in manifest.json, by
// which the core rewrites public ids (TestTaskIDPathsMatchTheManifest).
var taskIDPaths = []string{"id", "task_id", "data.id", "result.id"}

var _ pluginsdk.TaskSubmissionParser = (*Plugin)(nil)

// ParseTaskSubmission is synchronous on the managed task path. It describes
// the upstream task and initial query response; only the host persists owner,
// original account, task ID and reservation before returning to the client.
func (p *Plugin) ParseTaskSubmission(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
	if in.GetMeta().GetProtocol() != ProtocolVideoSubmit {
		return nil, status.Error(codes.Unimplemented, "only video submissions create tasks")
	}
	if in.GetStatus() < 200 || in.GetStatus() >= 300 || !gjson.ValidBytes(in.GetBody()) || !gjson.ParseBytes(in.GetBody()).IsObject() {
		return nil, status.Error(codes.DataLoss, "video submission did not return a complete successful JSON object")
	}
	usage := p.videoUsage(in)
	rv := usage.GetReserve()
	ref := rv.GetRefId()
	if ref == "" {
		return nil, status.Error(codes.DataLoss, "video submission did not return a task id")
	}
	snapshot, err := videoSnapshot(in.GetBody(), ref, in.GetMeta().GetModel(), true)
	if err != nil {
		return nil, status.Error(codes.DataLoss, "invalid video submission snapshot")
	}
	return &pluginv1.TaskSubmission{UpstreamRefId: ref, SnapshotJson: snapshot, Usage: usage,
		NextCheckAfterSec: rv.GetNextCheckAfterSec(), DeadlineSec: rv.GetDeadlineSec()}, nil
}

// Preserve the complete upstream payload (including result URLs and errors),
// adding only query fields absent from submit/relay responses. The core rewrites
// the upstream IDs to its opaque public task ID before serving the snapshot.
func videoSnapshot(body []byte, ref, model string, initial bool) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", err
	}
	if obj == nil {
		return "", errors.New("task response must be an object")
	}
	if len(obj["id"]) == 0 {
		obj["id"], _ = json.Marshal(ref)
	}
	if initial {
		if len(obj["status"]) == 0 {
			obj["status"] = json.RawMessage(`"queued"`)
		}
		if len(obj["model"]) == 0 && model != "" {
			obj["model"], _ = json.Marshal(model)
		}
	}
	out, err := json.Marshal(obj)
	return string(out), err
}

// videoUsage is the estimate of a video submission. The submit response is
// {"id": "..."} and carries nothing billable, so this reads the task id and
// returns a Reservation: the core pre-charges the estimate now and reconciles
// the real usage later. A response without a task id gets no reservation, and
// ParseTaskSubmission refuses it.
func (p *Plugin) videoUsage(in *pluginv1.ExtractUsageRequest) *pluginv1.UsageReport {
	taskID := ""
	for _, path := range taskIDPaths {
		v := gjson.GetBytes(in.GetBody(), path)
		if v.Type == gjson.String && strings.TrimSpace(v.Str) != "" {
			taskID = strings.TrimSpace(v.Str)
			break
		}
	}
	if taskID == "" {
		// A submit that returned no id can never be polled or reconciled.
		p.log.Warn("volcengine: video submit response carried no task id, not reserving",
			"request_id", in.GetMeta().GetRequestId(), "status", in.GetStatus())
		return &pluginv1.UsageReport{}
	}
	model := in.GetMeta().GetModel()
	spec := readVideoSpec(in.GetFields())
	est := estimateVideo(model, spec)
	if len(est.Assumed) > 0 {
		// The one line that explains an unexpectedly large pre-charge. An
		// estimate that had to bound something is not an error - a client may
		// legitimately leave duration to the model - but it is the difference
		// between "you were charged for what you asked for" and "you were
		// charged for the most this model could have done", and nobody should
		// have to guess which happened.
		p.log.Info("volcengine: video estimate had to assume part of the request",
			"request_id", in.GetMeta().GetRequestId(), "task_id", taskID, "model", model,
			"assumed", strings.Join(est.Assumed, ","), "resolution", est.Resolution,
			"est_output_tokens", est.Tokens)
	}
	tokens := &pluginv1.UsageTokens{OutputTokens: est.Tokens}
	facts := estimatedVideoFacts(est, in.GetFields())

	// The report and its reservation carry the same estimate. The
	// reservation is the single source of truth while the task runs: the core
	// prices, charges and records it as this request's usage until a
	// reconcile replaces it.
	return &pluginv1.UsageReport{
		Tokens: tokens,
		Facts:  facts,
		Reserve: &pluginv1.Reservation{
			RefId:             taskID,
			Tokens:            tokens,
			Facts:             facts,
			NextCheckAfterSec: firstCheckSec,
			DeadlineSec:       DeadlineSec,
		},
	}
}

// ---------------------------------------------------------------- reconcile

// Monitor implements pluginsdk.TaskMonitor: one GET of
// {base}{video_api_prefix}/contents/generations/tasks/{ref_id} (the same
// collection the submission used; a wrong path reports not found) with the
// original account's key, through the host's fetch. A host failure is
// returned, never retried in this process.
func (p *Plugin) Monitor(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) {
	query, err := taskQuery(in.GetEntry(), in.GetAccount())
	if err != nil {
		return nil, err
	}
	answer, err := pluginsdk.Fetch(ctx, query)
	if err != nil {
		return nil, err
	}
	return p.readTaskQuery(in.GetEntry(), answer), nil
}

// taskQuery is the upstream query of one task.
func taskQuery(entry *pluginv1.ReconcileEntry, acc *pluginv1.Account) (*pluginv1.ExecutionHTTPRequest, error) {
	ref := strings.TrimSpace(entry.GetRefId())
	if ref == "" {
		return nil, status.Error(codes.InvalidArgument, "reconcile entry has no ref_id")
	}
	cfg, px, err := accountConfig(acc)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "the task query needs the account credentials: %v", err)
	}
	u, err := videoTaskURL(cfg.BaseURL, px, ref)
	if err != nil {
		return nil, err
	}
	name, value := credentialHeader(acc.GetType(), ProtocolVideoQuery, cfg.APIKey)
	return &pluginv1.ExecutionHTTPRequest{Method: http.MethodGet, Url: u, Headers: map[string]string{name: value}}, nil
}

// readTaskQuery reads Ark's answer to a task query and states what the core
// should do with the pre-charged row.
//
//	queued / running           -> PENDING, ask again
//	succeeded + a usage figure  -> SETTLED, tokens = usage.completion_tokens
//	succeeded, no usage figure  -> SETTLED_ESTIMATE, the estimate is the charge
//	failed / expired/cancelled -> FAILED, reason from error.message
//	404 / 410                 -> NOT_FOUND
//	query / parse error       -> POLL_FAILED; host owns retries and the cutoff
func (p *Plugin) readTaskQuery(entry *pluginv1.ReconcileEntry, answer *pluginv1.ExecutionHTTPResponse) *pluginv1.ReconcileResult {
	if result := pluginsdk.ClassifyPollResponse(answer.GetStatus(), answer.GetTransportError()); result != nil {
		return result
	}
	ref, body := strings.TrimSpace(entry.GetRefId()), answer.GetBody()
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return pluginsdk.PollFailure("invalid task query JSON")
	}
	snapshot, err := videoSnapshot(body, ref, entry.GetModel(), false)
	if err != nil {
		return pluginsdk.PollFailure("invalid task query snapshot")
	}
	switch st := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String())); st {
	case "queued", "pending", "running", "processing":
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_PENDING, NextCheckAfterSec: runningCheckSec, TaskSnapshotJson: snapshot}
	case "succeeded":
		tokens := succeededTokens(body)
		if tokens <= 0 {
			// Ark confirmed the work and reported no usage for it. Answering
			// SETTLED with the zero we can read would reprice the row at zero
			// and REFUND the whole reservation: the video was delivered, so
			// that is one given away, and nothing in the record would say so.
			//
			// SETTLED_ESTIMATE is the core's answer for exactly this (CONTRACTS
			// §25.6): the reservation stands as the final usage and the final
			// charge, no money moves, and the row is marked reconcile=estimated
			// so an operator - and /usage/summary's estimated_cost column - can
			// tell it from a row whose usage was confirmed. tokens and facts
			// are ignored in this state, so none are sent.
			p.log.Warn("volcengine: succeeded video task reported no usage, settling on the reserved estimate",
				"task_id", ref)
			return &pluginv1.ReconcileResult{
				State:            pluginv1.ReconcileResult_SETTLED_ESTIMATE,
				Reason:           "Ark reported the task as succeeded without a usage object",
				TaskSnapshotJson: snapshot,
			}
		}
		return &pluginv1.ReconcileResult{
			State:            pluginv1.ReconcileResult_SETTLED,
			Tokens:           &pluginv1.UsageTokens{OutputTokens: tokens},
			Facts:            completedVideoFacts(body, entry.GetModel()),
			TaskSnapshotJson: snapshot,
		}
	case "failed", "expired", "cancelled", "canceled":
		reason := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
		if reason == "" {
			reason = st
		}
		return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_FAILED, Reason: reason, TaskSnapshotJson: snapshot}
	default:
		// An unrecognised status is not guessed into "done" (which settles)
		// or "failed" (which refunds). The host counts this query failure.
		p.log.Warn("volcengine: unrecognised video task status", "task_id", ref, "status", st)
		return pluginsdk.PollFailure("unrecognized task status")
	}
}

// succeededTokens is the real output-token usage of a finished task:
// usage.completion_tokens, falling back to usage.total_tokens. It returns 0
// when Ark reports a succeeded task with NO usage at all, which the caller
// turns into SETTLED_ESTIMATE rather than into a settle at zero.
//
// It does not read the historical plugin est_tokens column: that column was this
// plugin's workaround for a core that could not express "keep the estimate",
// and reading it here meant the plugin restating a figure the core already
// held on the reserved row - two copies of one number, with the plugin's copy
// the one that could be missing (a ledger write that failed at submit, a task
// from before the table existed) and silently settle the row at zero.
func succeededTokens(body []byte) int64 {
	if n := gjson.GetBytes(body, "usage.completion_tokens").Int(); n > 0 {
		return n
	}
	return gjson.GetBytes(body, "usage.total_tokens").Int()
}

// succeededFacts reports the REAL resolution as the resolution fact when Ark
// names one the manifest declares - replacing the estimate's guess.
// content.resolution first (where a finished task carries it), then a
// top-level resolution.
func succeededFacts(body []byte) map[string]string {
	r := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "content.resolution").String()))
	if r == "" {
		r = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "resolution").String()))
	}
	for _, known := range VideoResolutions {
		if r == known {
			return map[string]string{FactResolution: r}
		}
	}
	return nil
}
