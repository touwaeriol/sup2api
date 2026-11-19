package volcengine

import (
	"context"
	"strconv"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// submitTask uses the synchronous managed task parser. The host owns durable
// task identity and billing; invoking this parser must not write plugin tables.
func submitTask(t *testing.T, p *Plugin, accountID, userID int64, model, taskID string, fields map[string]string) *pluginv1.Reservation {
	t.Helper()
	task, err := p.ParseTaskSubmission(context.Background(), &pluginv1.ExtractUsageRequest{
		Meta:    &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: model, UserId: userID},
		Account: &pluginv1.Account{Id: accountID}, Status: 200,
		Body: []byte(`{"id":"` + taskID + `"}`), Fields: fields,
	})
	if err != nil {
		t.Fatalf("ParseTaskSubmission: %v", err)
	}
	if task.GetUsage().GetReserve() == nil {
		t.Fatal("no reservation")
	}
	return task.GetUsage().GetReserve()
}

func requested(resolution, ratio string, seconds int) map[string]string {
	return map[string]string{
		PathResolution: `"` + resolution + `"`, PathRatio: `"` + ratio + `"`, PathDuration: strconv.Itoa(seconds),
		PathContentCount: `1`, "content.0.text": `"a cat yawning at the camera"`,
	}
}

func TestVideoSubmissionReservation(t *testing.T) {
	p, _, _ := startRoutes(t)
	rv := submitTask(t, p, 1, 42, "doubao-seedance-2-0-260128", "same-upstream-id", requested(Res1080, Ratio169, 5))
	want := int64(5*videoFPS+1) * 1920 * 1080 / 1024
	if rv.GetTokens().GetOutputTokens() != want || rv.GetDeadlineSec() != DeadlineSec {
		t.Fatalf("reservation=%v", rv)
	}
}

// A succeeded task without usage retains the host's reservation: settling
// on the zero it could read would refund delivered work.
func TestVideoReconcileEstimateFallback(t *testing.T) {
	p, _, _ := startRoutes(t)
	rv := submitTask(t, p, 1, 1, "doubao-seedance-1-0-pro-250528", "cgt-nousage", requested(Res720, Ratio169, 5))
	if rv.GetTokens().GetOutputTokens() <= 0 {
		t.Fatal("no estimate to retain")
	}
	r := p.readTaskQuery(&pluginv1.ReconcileEntry{RefId: "cgt-nousage", TaskKind: "video", Model: "m"},
		&pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded","content":{"video_url":"https://x/v.mp4"}}`)})
	if r.GetState() != pluginv1.ReconcileResult_SETTLED_ESTIMATE || r.GetTokens() != nil || len(r.GetFacts()) != 0 || r.GetReason() == "" || r.GetTaskSnapshotJson() == "" {
		t.Fatalf("no usage: %v", r)
	}
	r = p.readTaskQuery(&pluginv1.ReconcileEntry{RefId: "cgt-nousage", TaskKind: "video"},
		&pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(`{"status":"succeeded","usage":{"completion_tokens":5},"content":{"resolution":"480p"}}`)})
	if r.GetState() != pluginv1.ReconcileResult_SETTLED || r.GetTokens().GetOutputTokens() != 5 || r.GetFacts()[FactResolution] != Res480 {
		t.Fatalf("real usage must win: %v", r)
	}
}
