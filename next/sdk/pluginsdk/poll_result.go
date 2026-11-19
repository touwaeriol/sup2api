package pluginsdk

import (
	"fmt"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// PollFailure reports a failed observation, not a failed upstream job.
// The host owns its persisted retry counter, backoff and final disposition.
func PollFailure(reason string) *pluginv1.ReconcileResult {
	return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_POLL_FAILED, Reason: reason}
}

// TaskNotFound reports an explicit upstream task-not-found/gone response.
func TaskNotFound(reason string) *pluginv1.ReconcileResult {
	return &pluginv1.ReconcileResult{State: pluginv1.ReconcileResult_NOT_FOUND, Reason: reason}
}

// ClassifyPollResponse is an optional default for task query endpoints.
// Nil means the plugin must parse the complete 2xx response. Providers whose
// 404 does not mean a missing task should apply their own interpretation.
// Reasons deliberately omit response bodies and credentials.
func ClassifyPollResponse(status int32, transportError string) *pluginv1.ReconcileResult {
	if transportError != "" {
		return PollFailure("poll transport error")
	}
	if status == 404 || status == 410 {
		return TaskNotFound(fmt.Sprintf("upstream task not found (HTTP %d)", status))
	}
	if status < 200 || status >= 300 {
		return PollFailure(fmt.Sprintf("poll HTTP %d", status))
	}
	return nil
}
