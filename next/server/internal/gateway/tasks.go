package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func (c *call) taskPair(query bool) (manifest.Endpoint, bool) {
	for _, b := range c.gen.Endpoints() {
		e := b.Endpoint
		if b.Plugin.Key == c.plugin.Key && e.Task != nil && e.Task.Kind == c.ep.Task.Kind && e.TaskQuery() == query {
			return e, true
		}
	}
	return manifest.Endpoint{}, false
}

func (c *call) resolveTask(ctx context.Context) *gwError {
	if c.task != nil {
		c.model = c.task.Model
		return nil
	}
	if c.g.d.Tasks == nil {
		return fromCore(core.ErrUnavailable.WithMessage("task service unavailable"), errTypeInternal)
	}
	submit, ok := c.taskPair(false)
	if !ok {
		return fromCore(core.ErrUnavailable.WithMessage("task submit endpoint unavailable"), errTypeInternal)
	}
	t, err := c.g.d.Tasks.FindTask(ctx, core.TaskQuery{ID: c.params[c.ep.Task.IDParam], PluginKey: c.plugin.Key, Kind: c.ep.Task.Kind,
		UserID: c.principal.UserID, GroupID: c.principal.Group.ID, SubmitProtocol: submit.Protocol, IDPaths: c.ep.Task.IDPaths})
	if err != nil {
		return fromCore(core.AsError(err), errTypeInvalidRequest)
	}
	c.task = t
	c.model = t.Model
	return nil
}

func (c *call) serveTaskSnapshot() {
	t := c.task
	if t.State == "failed" && t.FailureCode != "" {
		status := http.StatusBadGateway
		if t.FailureCode == "task_not_found" {
			status = http.StatusNotFound
		}
		if t.FailureCode == "task_timeout" {
			status = http.StatusGatewayTimeout
		}
		c.fail(&gwError{Status: status, Code: t.FailureCode, Message: "task observation failed", RecordType: errTypeUpstream})
		return
	}
	if t.ObservationStatus == "abandoned" && t.State == "pending" {
		c.fail(&gwError{Status: http.StatusGone, Code: "task_observation_expired", Message: "task observation expired", RecordType: errTypeUpstream})
		return
	}
	if len(t.Body) == 0 {
		c.fail(&gwError{Status: http.StatusServiceUnavailable, Code: "task_snapshot_pending", Message: "task snapshot is being recovered", RecordType: errTypeUpstream})
		return
	}
	body, err := protocol.RewriteTaskID(t.Body, t.IDPaths, t.UpstreamID, t.PublicID)
	if err != nil {
		c.fail(fromCore(core.ErrUnavailable.WithMessage("invalid stored task snapshot").WithCause(err), errTypeInternal))
		return
	}
	c.rec.StatusCode = http.StatusOK
	c.rec.Success = true
	c.c.Data(http.StatusOK, "application/json", body)
}

func (c *call) beginTask(ctx context.Context) error {
	if c.g.d.Tasks == nil {
		return errors.New("task service unavailable")
	}
	r := c.rec
	if r.AccountID == nil {
		return errors.New("task has no submitting account")
	}
	id, err := c.g.d.Tasks.BeginTask(ctx, core.TaskIntent{RequestID: c.rid, PluginKey: c.plugin.Key, Kind: c.ep.Task.Kind,
		UserID: r.UserID, APIKeyID: r.APIKeyID, GroupID: r.GroupID, AccountID: *r.AccountID, Model: c.model, Protocol: c.ep.Protocol})
	if err == nil {
		c.taskPublicID = id
	}
	return err
}

func (c *call) recordTaskFailure(ctx context.Context, status int, body []byte, reason string) {
	if c.taskPublicID == "" || c.g.d.Tasks == nil {
		return
	}
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := c.g.d.Tasks.ReceiveTask(bctx, c.rid, status, body, reason); err != nil {
		slog.ErrorContext(bctx, "task receipt update failed", "request_id", c.rid, "err", err)
	}
}

// taskFailure ends a task submission that could not be registered: its
// record is the free unreported one (nothing billed, the precharge
// released), the receipt keeps why.
func (c *call) taskFailure(ctx context.Context, reason string, err error) attemptResult {
	c.recordTaskFailure(ctx, c.rec.StatusCode, nil, reason)
	c.usage = nil
	c.rec.SetUnreported(reason)
	slog.WarnContext(ctx, "task submission could not be registered", "request_id", c.rid, "reason", reason, "err", err)
	return attemptResult{kind: attemptReturn, err: fromCore(core.ErrUnavailable.WithMessage(reason).WithCause(err), errTypeUpstream)}
}
