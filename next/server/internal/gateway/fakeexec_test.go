package gateway

import (
	"context"
	"errors"
	"fmt"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// fakeBuilder is an in-process plugin as the SDK's ExecuteDefault sees it:
// it builds the final upstream request of an execution, classifies an
// upstream error, reads usage, parses a task submission.
type fakeBuilder interface {
	buildUpstream(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error)
	ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
	ExtractUsage(ctx context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error)
	ParseTaskSubmission(ctx context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error)
}

// fakeExecute plays both sides of Execute for an in-process fake plugin:
// the SDK's ExecuteDefault (relay once, wait when a flag asks, answer
// host_usage, usage or task, classify a waited error) and the runtime's
// guarantees the gateway relies on (core ExecutePlugin): every relay passes
// client.Check first, a relay without waiting
// is only kept and reaches the caller as ExecutionResult.Relay with
// host_usage, a waiting one runs in client.Relay.
func fakeExecute(ctx context.Context, b fakeBuilder, in *pluginv1.ExecuteRequest, client core.ExecutionClient) (core.ExecutionResult, error) {
	req, err := b.buildUpstream(ctx, in)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	if client.Check != nil {
		if err := client.Check(ctx, req); err != nil {
			return core.ExecutionResult{}, fmt.Errorf("relay refused: %w", err)
		}
	}
	if !in.GetExtractUsage() && !in.GetTaskSubmission() {
		return core.ExecutionResult{Response: &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}, Relay: req}, nil
	}
	if client.Relay == nil {
		return core.ExecutionResult{}, errors.New("FAILED_PRECONDITION: no waiting relay")
	}
	out, err := client.Relay(ctx, req)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	res, err := fakeAnswer(ctx, b, in, out)
	return core.ExecutionResult{Response: res}, err
}

// fakeAnswer is ExecuteDefault's answer after a relay that waited.
func fakeAnswer(ctx context.Context, b fakeBuilder, in *pluginv1.ExecuteRequest, out *pluginv1.RelayUpstreamResponse) (*pluginv1.ExecuteResponse, error) {
	if out.GetError() != nil {
		cls, err := b.ClassifyError(ctx, out.GetError())
		if err != nil {
			cls = nil
		}
		return &pluginv1.ExecuteResponse{Classification: cls}, nil
	}
	if in.GetTaskSubmission() {
		task, err := b.ParseTaskSubmission(ctx, out.GetObservation())
		if err != nil {
			return nil, err
		}
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Task{Task: task}}, nil
	}
	if report, err := b.ExtractUsage(ctx, out.GetObservation()); err == nil && report != nil {
		return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_Usage{Usage: report}}, nil
	}
	return &pluginv1.ExecuteResponse{Report: &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}}, nil
}
