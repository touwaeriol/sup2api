package pluginsdk

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Executor is the one path of a gateway request (PlatformService.Execute),
// called once per attempt on an account. The host has selected the account
// and rewritten the body; the plugin receives it whole, builds the final
// upstream request and hands it to the host with Relay or RelayAndWait, after
// any auxiliary Fetch. It opens no socket, never retries and never picks
// another account. Usage and task facts travel only in the response:
// ReportHostUsage, ReportUsage, ReportTask or ReportClassification.
//
// Optional: most platforms implement only UpstreamBuilder, and the SDK runs
// ExecuteDefault for them. A Platform that implements Executor runs its own.
type Executor interface {
	Execute(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error)
}

// UpstreamBuilder builds the final upstream request of one attempt from the
// whole ExecuteRequest: body, inbound headers, account, meta and attempt
// number. Every attempt is built from scratch. It may call Fetch first; to
// end the execution with a classification of a failed Fetch, return
// Classified.
type UpstreamBuilder interface {
	BuildUpstream(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error)
}

// UsageExtractor reads the usage of an endpoint the manifest rules cannot
// express (usageSource "plugin") from the observation of a relay that
// waited. ExecuteDefault calls it in the plugin's process when the request
// sets extract_usage. It is handed the whole body of a non-streaming answer
// or, for a stream, only the events the endpoint listed in usageStreamEvents.
type UsageExtractor interface {
	ExtractUsage(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error)
}

// TaskSubmissionParser describes the task an upstream accepted, from the
// observation of a relay that waited. ExecuteDefault calls it in the
// plugin's process when the request sets task_submission. The host registers
// the task before it publishes the submission response; the plugin starts no
// polling of its own and implements TaskMonitor for the observations.
type TaskSubmissionParser interface {
	ParseTaskSubmission(context.Context, *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error)
}

// ErrorClassifier is the ClassifyError half of Platform. ExecuteDefault uses
// it for the error of a relay that waited.
type ErrorClassifier interface {
	ClassifyError(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
}

// TaskMonitor makes one scheduled observation of a host-managed task
// (PlatformService.Monitor, "platform.monitor.v1"): query the upstream once
// with Fetch and return what was read, PENDING or a terminal state. A nil
// result counts as one failed query; a terminal state is accepted only with
// a complete, successful Fetch made during the call. The host chooses the
// node, the time and the original account and owns retries, leases and the
// settlement. No plugin timer is needed.
type TaskMonitor interface {
	Monitor(context.Context, *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error)
}

// ReportHostUsage is the answer of every execution that relayed without waiting,
// and the explicit fallback of one that waited and could not read the
// observation: the host takes the usage it reads by the declared rules.
func ReportHostUsage() *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}
}

// ReportUsage answers an extract_usage execution with the usage the plugin read
// from the observation.
func ReportUsage(report *pluginv1.UsageReport) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report}}
}

// ReportTask answers a task_submission execution with the accepted upstream task.
func ReportTask(task *pluginv1.TaskSubmission) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Task{Task: task}}
}

// ReportClassification answers an execution whose upstream exchange failed: the
// error of a relay that waited, or a failed Fetch before the relay. nil
// leaves the host to its default classification.
func ReportClassification(c *pluginv1.ClassifyErrorResponse) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{Classification: c}
}

// Classified is the error an UpstreamBuilder returns to end the execution
// with a classification instead of a failure, e.g. after a Fetch the
// upstream refused. Nothing was relayed, so the host may move on safely.
func Classified(c *pluginv1.ClassifyErrorResponse) error { return &classifiedError{c} }

type classifiedError struct {
	classification *pluginv1.ClassifyErrorResponse
}

func (e *classifiedError) Error() string {
	return "upstream request failed: " + e.classification.GetReason()
}

// ExecuteDefault is Execute for a platform that only builds the request: the
// SDK runs it for a Platform that implements UpstreamBuilder and no Executor.
// The two flags of the request choose the form of the exchange:
//
//   - neither: Relay without waiting and answer host_usage. The request is
//     sent after Execute returned.
//   - task_submission: RelayAndWait, then ParseTaskSubmission and answer
//     task. b must implement TaskSubmissionParser; this is checked before
//     anything is built or sent.
//   - extract_usage: RelayAndWait, then ExtractUsage and answer usage. When
//     the usage cannot be read (b is no UsageExtractor, it fails or returns
//     nothing) the answer is host_usage, the documented fallback: the host
//     bills its own reading and records usage_extract=fallback.
//
// The error of a relay that waited is answered with the classification of
// b's ClassifyError when b implements ErrorClassifier (every Platform does),
// otherwise with none. It relays exactly once and never retries.
func ExecuteDefault(ctx context.Context, b UpstreamBuilder, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	if b == nil || in == nil {
		return nil, status.Error(codes.InvalidArgument, "upstream builder and execution request are required")
	}
	parser, _ := b.(TaskSubmissionParser)
	if in.GetTaskSubmission() && parser == nil {
		return nil, status.Error(codes.FailedPrecondition, "a task submission endpoint needs a TaskSubmissionParser")
	}
	request, err := b.BuildUpstream(ctx, in)
	if c := (*classifiedError)(nil); errors.As(err, &c) {
		return ReportClassification(c.classification), nil
	}
	if err != nil {
		return nil, err
	}
	if !in.GetTaskSubmission() && !in.GetExtractUsage() {
		if err := Relay(ctx, request); err != nil {
			return nil, err
		}
		return ReportHostUsage(), nil
	}
	observed, err := RelayAndWait(ctx, request)
	if err != nil {
		return nil, err
	}
	if observed.GetError() != nil {
		return ReportClassification(classifyObserved(ctx, b, observed.GetError())), nil
	}
	if in.GetTaskSubmission() {
		task, err := parser.ParseTaskSubmission(ctx, observed.GetObservation())
		if err != nil {
			return nil, err
		}
		return ReportTask(task), nil
	}
	if report := extractObserved(ctx, b, observed.GetObservation()); report != nil {
		return ReportUsage(report), nil
	}
	return ReportHostUsage(), nil
}

// classifyObserved classifies the error of a waited relay; nil (the host's
// default classification) when b cannot or fails to.
func classifyObserved(ctx context.Context, b UpstreamBuilder, failed *pluginv1.ClassifyErrorRequest) *pluginv1.ClassifyErrorResponse {
	classifier, ok := b.(ErrorClassifier)
	if !ok {
		return nil
	}
	classified, err := classifier.ClassifyError(ctx, failed)
	if err != nil {
		slog.Default().Warn("classifying the upstream error failed, the host applies its default", "error", err.Error())
		return nil
	}
	return classified
}

// executorOf is the Execute of a Platform: its own when it implements
// Executor, otherwise ExecuteDefault over its UpstreamBuilder.
func executorOf(p Platform) (Executor, error) {
	if e, ok := p.(Executor); ok {
		return e, nil
	}
	if b, ok := p.(UpstreamBuilder); ok {
		return defaultExecutor{b}, nil
	}
	return nil, errors.New("a Platform must implement UpstreamBuilder or Executor")
}

// defaultExecutor runs ExecuteDefault over the plugin itself, so that the
// optional interfaces ExecuteDefault looks for are the plugin's.
type defaultExecutor struct{ builder UpstreamBuilder }

func (d defaultExecutor) Execute(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	return ExecuteDefault(ctx, d.builder, in)
}

// extractObserved reads the usage of a waited relay; nil is the fallback to
// the host's reading.
func extractObserved(ctx context.Context, b UpstreamBuilder, observation *pluginv1.ExtractUsageRequest) *pluginv1.UsageReport {
	extractor, ok := b.(UsageExtractor)
	if !ok {
		slog.Default().Warn("extract_usage execution without a UsageExtractor, falling back to the host's usage reading")
		return nil
	}
	report, err := extractor.ExtractUsage(ctx, observation)
	if err != nil {
		slog.Default().Warn("plugin usage extraction failed, falling back to the host's usage reading", "error", err.Error())
		return nil
	}
	return report
}

// checkReport refuses a response whose report contradicts what the execution
// did, so that the mistake names itself in the plugin instead of surfacing
// as an unsent or unbilled request in the host.
func (ex *execution) checkReport(out *pluginv1.ExecuteResponse) error {
	switch out.GetReport().(type) {
	case nil:
		return nil
	case *pluginv1.ExecuteResponse_HostUsage:
		if !ex.relayed.Load() {
			return status.Error(codes.Internal, "Execute answered host_usage without relaying a request")
		}
	default:
		if !ex.waited.Load() {
			return status.Error(codes.Internal, "Execute answered usage or task without a relay that waited (RelayAndWait)")
		}
	}
	return nil
}

// bindExecution puts ex, with the host's client, on a context of the call
// that ends with the returned cancel.
func (rt *runtime) bindExecution(ctx context.Context, ex *execution) (context.Context, context.CancelFunc, error) {
	if ex.token == "" {
		return nil, nil, status.Errorf(codes.InvalidArgument, "%s execution token is required", ex.kind)
	}
	if rt == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "host is not initialised")
	}
	rt.mu.Lock()
	h := rt.host
	rt.mu.Unlock()
	if h == nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "host is not initialised")
	}
	ex.client = h.Client()
	ctx, cancel := context.WithCancel(ctx)
	return withExecution(ctx, ex), cancel, nil
}

func (s platformServer) Execute(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error) {
	ex := &execution{token: in.GetExecutionToken(), kind: kindExecute, mayWait: in.GetExtractUsage() || in.GetTaskSubmission()}
	ctx, cancel, err := s.runtime.bindExecution(ctx, ex)
	if err != nil {
		return nil, err
	}
	defer cancel()
	out, err := s.execute.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = &pluginv1.ExecuteResponse{} // states nothing
	}
	if err := ex.checkReport(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s platformServer) Monitor(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
	p, ok := s.impl.(TaskMonitor)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "this platform does not implement Monitor")
	}
	ctx, cancel, err := s.runtime.bindExecution(ctx, &execution{token: in.GetExecutionToken(), kind: kindMonitor})
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := p.Monitor(ctx, in)
	if err != nil {
		return nil, err
	}
	return &pluginv1.MonitorResponse{Result: result}, nil
}
