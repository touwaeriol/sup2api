package grpcruntime

import (
	"context"

	"google.golang.org/grpc"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// Mock RPC clients for testing. Each field is an optional override; unset
// methods return zero values or unimplemented errors.

type platformRPC struct {
	pluginv1.PlatformServiceClient
	validateCreds func(context.Context, *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error)
	classify      func(context.Context, *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error)
	buildTest     func(context.Context, *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error)
	buildModels   func(context.Context, *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error)
	resolve       func(context.Context, *pluginv1.ResolveModelRequest) (*pluginv1.ResolveModelResponse, error)
	estimate      func(context.Context, *pluginv1.EstimateUsageRequest) (*pluginv1.UsageReport, error)
	execute       func(context.Context, *pluginv1.ExecuteRequest) (*pluginv1.ExecuteResponse, error)
	monitor       func(context.Context, *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error)
}

func (p *platformRPC) ValidateCredentials(ctx context.Context, in *pluginv1.ValidateCredentialsRequest, _ ...grpc.CallOption) (*pluginv1.ValidateCredentialsResponse, error) {
	if p.validateCreds != nil {
		return p.validateCreds(ctx, in)
	}
	return &pluginv1.ValidateCredentialsResponse{}, nil
}

func (p *platformRPC) ClassifyError(ctx context.Context, in *pluginv1.ClassifyErrorRequest, _ ...grpc.CallOption) (*pluginv1.ClassifyErrorResponse, error) {
	if p.classify != nil {
		return p.classify(ctx, in)
	}
	return &pluginv1.ClassifyErrorResponse{}, nil
}

func (p *platformRPC) BuildTestRequest(ctx context.Context, in *pluginv1.BuildTestRequestRequest, _ ...grpc.CallOption) (*pluginv1.BuildTestRequestResponse, error) {
	if p.buildTest != nil {
		return p.buildTest(ctx, in)
	}
	return &pluginv1.BuildTestRequestResponse{}, nil
}

func (p *platformRPC) BuildModelsRequest(ctx context.Context, in *pluginv1.BuildModelsRequestRequest, _ ...grpc.CallOption) (*pluginv1.BuildModelsRequestResponse, error) {
	if p.buildModels != nil {
		return p.buildModels(ctx, in)
	}
	return &pluginv1.BuildModelsRequestResponse{}, nil
}

func (p *platformRPC) ResolveModel(ctx context.Context, in *pluginv1.ResolveModelRequest, _ ...grpc.CallOption) (*pluginv1.ResolveModelResponse, error) {
	if p.resolve != nil {
		return p.resolve(ctx, in)
	}
	return &pluginv1.ResolveModelResponse{}, nil
}

func (p *platformRPC) EstimateUsage(ctx context.Context, in *pluginv1.EstimateUsageRequest, _ ...grpc.CallOption) (*pluginv1.UsageReport, error) {
	if p.estimate != nil {
		return p.estimate(ctx, in)
	}
	return &pluginv1.UsageReport{}, nil
}

func (p *platformRPC) Execute(ctx context.Context, in *pluginv1.ExecuteRequest, _ ...grpc.CallOption) (*pluginv1.ExecuteResponse, error) {
	if p.execute != nil {
		return p.execute(ctx, in)
	}
	return &pluginv1.ExecuteResponse{}, nil
}

func (p *platformRPC) Monitor(ctx context.Context, in *pluginv1.PollRequest, _ ...grpc.CallOption) (*pluginv1.MonitorResponse, error) {
	if p.monitor != nil {
		return p.monitor(ctx, in)
	}
	return &pluginv1.MonitorResponse{}, nil
}

type appRPC struct {
	pluginv1.AppServiceClient
	job   func(context.Context, *pluginv1.RunJobRequest) (*pluginv1.RunJobResponse, error)
	event func(context.Context, *pluginv1.OnEventsRequest) (*pluginv1.OnEventsResponse, error)
}

func (a *appRPC) RunJob(ctx context.Context, in *pluginv1.RunJobRequest, _ ...grpc.CallOption) (*pluginv1.RunJobResponse, error) {
	if a.job != nil {
		return a.job(ctx, in)
	}
	return &pluginv1.RunJobResponse{}, nil
}

func (a *appRPC) OnEvents(ctx context.Context, in *pluginv1.OnEventsRequest, _ ...grpc.CallOption) (*pluginv1.OnEventsResponse, error) {
	if a.event != nil {
		return a.event(ctx, in)
	}
	return &pluginv1.OnEventsResponse{}, nil
}
