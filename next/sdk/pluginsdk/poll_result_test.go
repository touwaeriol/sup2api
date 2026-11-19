package pluginsdk

import (
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"testing"
)

func TestClassifyPollResponse(t *testing.T) {
	for _, tc := range []struct {
		code      int32
		transport string
		want      pluginv1.ReconcileResult_State
	}{
		{404, "", pluginv1.ReconcileResult_NOT_FOUND}, {410, "", pluginv1.ReconcileResult_NOT_FOUND},
		{401, "", pluginv1.ReconcileResult_POLL_FAILED}, {403, "", pluginv1.ReconcileResult_POLL_FAILED},
		{429, "", pluginv1.ReconcileResult_POLL_FAILED}, {503, "", pluginv1.ReconcileResult_POLL_FAILED},
		{200, "secret transport details", pluginv1.ReconcileResult_POLL_FAILED},
		{200, "", pluginv1.ReconcileResult_PENDING},
	} {
		r := ClassifyPollResponse(tc.code, tc.transport)
		if r.GetState() != tc.want {
			t.Fatalf("%+v: %v", tc, r)
		}
		if r != nil && (r.Reason == "" || r.TaskSnapshotJson != "" || r.Reason == tc.transport) {
			t.Fatalf("unsafe result: %v", r)
		}
	}
}
