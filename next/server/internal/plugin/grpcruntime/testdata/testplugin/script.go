package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// script tells Execute or Monitor what to do, so that a test drives the
// plugin's side of the host client from outside the process.
type script struct {
	Steps []step `json:"script"`
	// Return is what the call answers: for Execute "host_usage", "usage"
	// (input tokens = the bytes of the last observation), "task", "none" or
	// "error"; for Monitor "pending", "settled" or "none".
	Return string `json:"return"`
}

// step is one action. Op is "relay" (Wait, URL, BodySize, Headers),
// "fetch" (URL, BodySize), "sleep" (Ms) or "exit".
type step struct {
	Op       string            `json:"op"`
	Wait     bool              `json:"wait"`
	URL      string            `json:"url"`
	BodySize int               `json:"body_size"`
	Headers  map[string]string `json:"headers"`
	Ms       int               `json:"ms"`
}

// stepResult is what the host answered to one relay or fetch.
type stepResult struct {
	Code        string `json:"code"`
	Message     string `json:"message,omitempty"`
	Status      int32  `json:"status,omitempty"`
	BodyBytes   int    `json:"body_bytes,omitempty"`
	Observation bool   `json:"observation,omitempty"`
	Error       bool   `json:"error,omitempty"`
}

// parseScript reads the script at the start of raw; what follows it is
// padding, for a body of any size.
func parseScript(raw []byte) (script, bool) {
	var sc script
	if !bytes.Contains(raw, []byte(`"script":`)) || json.NewDecoder(bytes.NewReader(raw)).Decode(&sc) != nil {
		return sc, false
	}
	return sc, true
}

// run performs the steps and returns the host's answers as JSON, with the
// size of the last observation body.
func (s *server) run(ctx context.Context, token string, steps []step) (log string, observed int) {
	h := s.hostClient()
	results := []stepResult{}
	for _, st := range steps {
		switch st.Op {
		case "sleep":
			time.Sleep(time.Duration(st.Ms) * time.Millisecond)
		case "exit":
			os.Exit(3)
		case "relay":
			out, err := h.RelayUpstream(ctx, &pluginv1.RelayUpstreamRequest{ExecutionToken: token, Wait: st.Wait,
				Request: &pluginv1.UpstreamRequest{Url: st.URL, Headers: st.Headers, Body: make([]byte, st.BodySize)}})
			r := result(err)
			r.Observation, r.Error = out.GetObservation() != nil, out.GetError() != nil
			if r.Observation {
				observed = len(out.GetObservation().GetBody())
			}
			results = append(results, r)
		case "fetch":
			out, err := h.ExecuteHTTP(ctx, &pluginv1.ExecutionHTTPRequest{ExecutionToken: token, Url: st.URL, Body: make([]byte, st.BodySize)})
			r := result(err)
			r.Status, r.BodyBytes = out.GetStatus(), len(out.GetBody())
			results = append(results, r)
		}
	}
	raw, _ := json.Marshal(results)
	return string(raw), observed
}

func result(err error) stepResult {
	st := status.Convert(err)
	return stepResult{Code: st.Code().String(), Message: st.Message()}
}

// runExecute answers Execute by sc. The step log travels in
// classification.reason, which is independent of the report.
func (s *server) runExecute(ctx context.Context, in *pluginv1.ExecuteRequest, sc script) (*pluginv1.ExecuteResponse, error) {
	log, observed := s.run(ctx, in.GetExecutionToken(), sc.Steps)
	out := &pluginv1.ExecuteResponse{Classification: &pluginv1.ClassifyErrorResponse{Reason: log}}
	switch sc.Return {
	case "host_usage":
		out.Report = &pluginv1.ExecuteResponse_HostUsage{HostUsage: &pluginv1.HostUsage{}}
	case "usage":
		out.Report = &pluginv1.ExecuteResponse_Usage{Usage: &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{InputTokens: int64(observed)}}}
	case "task":
		out.Report = &pluginv1.ExecuteResponse_Task{Task: &pluginv1.TaskSubmission{UpstreamRefId: "task-1"}}
	case "error":
		return nil, status.Error(codes.Internal, "scripted failure")
	}
	return out, nil
}

// Monitor answers by the script in entry.ref_id; the step log travels in
// result.reason.
func (s *server) Monitor(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.MonitorResponse, error) {
	sc, _ := parseScript([]byte(in.GetEntry().GetRefId()))
	log, _ := s.run(ctx, in.GetExecutionToken(), sc.Steps)
	switch sc.Return {
	case "pending":
		return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_PENDING, Reason: log}}, nil
	case "settled":
		return &pluginv1.MonitorResponse{Result: &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_SETTLED, Reason: log}}, nil
	}
	return &pluginv1.MonitorResponse{}, nil
}
