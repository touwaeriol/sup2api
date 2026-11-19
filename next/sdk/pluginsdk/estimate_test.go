package pluginsdk

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

type ordinaryTestPlatform struct {
	Platform
	UpstreamBuilder
}

type countClient struct {
	pluginv1.HostServiceClient
	count int64
	seen  string
}

func (c *countClient) CountTokens(_ context.Context, r *pluginv1.CountTokensRequest, _ ...grpc.CallOption) (*pluginv1.CountTokensResponse, error) {
	c.seen = r.Text
	return &pluginv1.CountTokensResponse{Tokens: c.count, Encoding: "o200k_base"}, nil
}

type explicitEstimator struct{ ordinaryTestPlatform }

func (explicitEstimator) EstimateUsage(context.Context, *pluginv1.EstimateUsageRequest) (*pluginv1.UsageReport, error) {
	return &pluginv1.UsageReport{Facts: map[string]string{"video_seconds": "5"}}, nil
}

func TestExplicitEstimatorIsUsed(t *testing.T) {
	s := platformServer{impl: explicitEstimator{}}
	report, err := s.EstimateUsage(context.Background(), &pluginv1.EstimateUsageRequest{})
	if err != nil || report.GetFacts()["video_seconds"] != "5" {
		t.Fatalf("estimate: %v %v", report, err)
	}
}

// Without a UsageEstimator the answer is UNIMPLEMENTED and the prompt is not
// tokenized: the core falls back to its own estimate.
func TestDefaultEstimatorReturnsUnimplemented(t *testing.T) {
	client := &countClient{count: 12}
	s := platformServer{impl: ordinaryTestPlatform{}, runtime: &runtime{host: &host{client: client}}}
	r, err := s.EstimateUsage(context.Background(), &pluginv1.EstimateUsageRequest{Prompt: "hello", PreConsumeTokens: 500})
	if status.Code(err) != codes.Unimplemented || r != nil || client.seen != "" {
		t.Fatalf("default estimator should return Unimplemented: %v %v tokenizer=%q", r, err, client.seen)
	}
}
