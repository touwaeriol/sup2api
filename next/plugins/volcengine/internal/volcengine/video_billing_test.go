package volcengine

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

func TestVideoBillingFacts(t *testing.T) {
	p := New()
	r := p.videoUsage(&pluginv1.ExtractUsageRequest{
		Meta: &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-2-5"}, Status: 200, Body: []byte(`{"id":"task"}`),
		Fields: map[string]string{"resolution": `"720p"`, "ratio": `"16:9"`, "duration": "5", "content.#.type": `["video_url"]`, "draft": "true"},
	})
	for k, want := range map[string]string{"video_width": "1280", "video_height": "720", "video_pixels": "921600", "video_seconds": "5", "video_input": "true", "video_estimated": "true", "video_draft": "true"} {
		if r.GetFacts()[k] != want || r.GetReserve().GetFacts()[k] != want {
			t.Fatalf("%s = %s want %s", k, r.GetFacts()[k], want)
		}
	}
	final := p.readTaskQuery(&pluginv1.ReconcileEntry{Model: "doubao-seedance-2-5"},
		&pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded","usage":{"completion_tokens":100000},"resolution":"1080p","ratio":"9:16","duration":4}`)})
	if final.GetFacts()["video_estimated"] != "false" || final.GetFacts()["video_width"] != "1080" || final.GetFacts()["video_height"] != "1920" || final.GetFacts()["video_seconds"] != "4" {
		t.Fatalf("final: %v", final.GetFacts())
	}
	if _, ok := final.GetFacts()["video_input"]; ok {
		t.Fatal("must retain input fact from submission")
	}
}

func TestVideoEstimateBeforeUpstreamTaskExists(t *testing.T) {
	r, err := New().EstimateUsage(context.Background(), &pluginv1.EstimateUsageRequest{Meta: &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-2-5"}, Fields: map[string]string{"duration": "5", "resolution": `"720p"`, "ratio": `"16:9"`}, PreConsumeTokens: 500})
	if err != nil || r.GetTokens().GetOutputTokens() != 108900 || r.GetReserve() != nil || r.GetFacts()["video_estimated"] != "true" {
		t.Fatalf("estimate %v %v", r, err)
	}
}

func TestVideoDimensionsMatchEveryPublishedArea(t *testing.T) {
	for _, tiers := range pixelArea {
		for _, ratios := range tiers {
			for ratio, area := range ratios {
				w, h := dimensionsForArea(area, ratio)
				if w*h != area || w < 200 || h < 200 {
					t.Fatalf("%s %d -> %dx%d", ratio, area, w, h)
				}
			}
		}
	}
}

func TestEstimateUsageUnimplementedForNonVideo(t *testing.T) {
	p := New()
	for _, protocol := range []string{ProtocolMessages, ""} {
		_, err := p.EstimateUsage(context.Background(), &pluginv1.EstimateUsageRequest{
			Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: "doubao-pro-256k"},
		})
		if err == nil {
			t.Fatalf("protocol %q: expected Unimplemented error, got nil", protocol)
		}
		if st, ok := status.FromError(err); !ok || st.Code() != codes.Unimplemented {
			t.Fatalf("protocol %q: expected Unimplemented, got %v", protocol, err)
		}
	}
}
