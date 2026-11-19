package volcengine

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// The core rewrites public task ids by the manifest's task.idPaths and the
// plugin reads the submitted id by taskIDPaths: the two lists are one.
func TestTaskIDPathsMatchTheManifest(t *testing.T) {
	var m manifest.Manifest
	if err := json.Unmarshal(manifestFile(t), &m); err != nil {
		t.Fatal(err)
	}
	tasks := 0
	for _, p := range m.Platforms {
		for _, e := range p.Endpoints {
			if e.Task == nil {
				continue
			}
			tasks++
			if !slices.Equal(e.Task.IDPaths, taskIDPaths) {
				t.Errorf("%s task.idPaths = %v, the plugin reads %v", e.Protocol, e.Task.IDPaths, taskIDPaths)
			}
		}
	}
	if tasks == 0 {
		t.Fatal("the manifest declares no task endpoint")
	}
}

func TestManagedVideoSubmission(t *testing.T) {
	p := New() // No host: parsing must not read credentials or write a second ledger.
	for _, body := range []string{
		`{"id":"ark-1"}`, `{"task_id":"ark-1"}`, `{"data":{"id":"ark-1"}}`, `{"result":{"id":"ark-1"}}`,
	} {
		task, err := p.ParseTaskSubmission(context.Background(), &pluginv1.ExtractUsageRequest{
			Meta:    &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit, Model: "doubao-seedance-2-0-260128", UserId: 42},
			Account: &pluginv1.Account{Id: 7}, Status: 200, Body: []byte(body), Fields: requested(Res720, Ratio169, 5),
		})
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if task.GetUpstreamRefId() != "ark-1" || task.GetNextCheckAfterSec() != firstCheckSec || task.GetDeadlineSec() != DeadlineSec {
			t.Fatalf("task = %v", task)
		}
		if task.GetUsage().GetReserve().GetRefId() != "ark-1" || task.GetUsage().GetReserve().GetTokens().GetOutputTokens() <= 0 {
			t.Fatalf("missing reservation: %v", task.GetUsage())
		}
		snapshot := task.GetSnapshotJson()
		if !json.Valid([]byte(snapshot)) || gjson.Get(snapshot, "id").String() != "ark-1" ||
			gjson.Get(snapshot, "status").String() != "queued" || gjson.Get(snapshot, "model").String() == "" {
			t.Fatalf("initial query snapshot = %s", snapshot)
		}
	}
}

func TestManagedVideoRejectsIncompleteSubmission(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int32
	}{
		{`{"id":"ark-1"`, 200},
		{`{"id":"ark-1"}`, 500}, {`{}`, 200},
		{`{"id":123}`, 200}, {`{"id":{}}`, 200}, {`null`, 200},
	} {
		_, err := New().ParseTaskSubmission(context.Background(), &pluginv1.ExtractUsageRequest{
			Meta: &pluginv1.RequestMeta{Protocol: ProtocolVideoSubmit}, Body: []byte(tc.body), Status: tc.status,
		})
		if err == nil {
			t.Fatalf("accepted unusable task: %+v", tc)
		}
	}
}

func TestManagedVideoReconcileSnapshot(t *testing.T) {
	for _, tc := range []struct {
		body string
		want pluginv1.ReconcileResult_State
	}{
		{`{"status":"running","progress":0.7}`, pluginv1.ReconcileResult_PENDING},
		{`{"status":"succeeded","usage":{"completion_tokens":10},"content":{"video_url":"https://cdn.invalid/result.mp4"}}`, pluginv1.ReconcileResult_SETTLED},
		{`{"status":"succeeded","content":{"video_url":"https://cdn.invalid/result.mp4"}}`, pluginv1.ReconcileResult_SETTLED_ESTIMATE},
		{`{"status":"failed","error":{"message":"rejected"}}`, pluginv1.ReconcileResult_FAILED},
	} {
		r := New().readTaskQuery(&pluginv1.ReconcileEntry{RefId: "ark-1", TaskKind: "video"},
			&pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(tc.body)})
		if r.GetState() != tc.want || !json.Valid([]byte(r.GetTaskSnapshotJson())) {
			t.Fatalf("%s: %v", tc.body, r)
		}
		var original, snapshot map[string]json.RawMessage
		_ = json.Unmarshal([]byte(tc.body), &original)
		_ = json.Unmarshal([]byte(r.GetTaskSnapshotJson()), &snapshot)
		for key, value := range original {
			if string(value) != string(snapshot[key]) {
				t.Fatalf("snapshot lost %s: %s", key, r.GetTaskSnapshotJson())
			}
		}
		if gjson.Get(r.GetTaskSnapshotJson(), "id").String() != "ark-1" {
			t.Fatalf("snapshot omitted task ID: %s", r.GetTaskSnapshotJson())
		}
	}
}

func TestVideoMalformedResultCannotSettleOrReplaceSnapshot(t *testing.T) {
	for _, body := range []string{
		`{"status":"succeeded","usage":{"completion_tokens":10}`,
		`{"status":"failed"`, `[]`,
	} {
		r := New().readTaskQuery(&pluginv1.ReconcileEntry{RefId: "ark-1", TaskKind: "video"},
			&pluginv1.ExecutionHTTPResponse{Status: 200, Body: []byte(body)})
		if r.GetState() != pluginv1.ReconcileResult_POLL_FAILED || r.GetTaskSnapshotJson() != "" {
			t.Fatalf("malformed response became authoritative: %q -> %v", body, r)
		}
	}
}
