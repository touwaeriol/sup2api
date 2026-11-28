package account

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// The API side of credential refresh (CONTRACTS §48.4): the refresh state
// of the accounts in their views, and the administrator's forced refresh.

// RefreshView is the credential refresh state of an account in the API.
type RefreshView struct {
	// ExpiresAt is the expiry read from the credentials at the last attempt.
	ExpiresAt     *time.Time `json:"expires_at"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	// ErrorType is "", "auth_rejected" (re-authorise the account) or
	// "transient" (retried on the next sweep).
	ErrorType string `json:"error_type"`
	Error     string `json:"error"`
}

// RefreshOutcome is the answer of POST /accounts/:id/refresh-credentials.
type RefreshOutcome struct {
	Refreshed bool   `json:"refreshed"`
	Skipped   string `json:"skipped,omitempty"`
	// ErrorType and Error report a failed attempt, as in RefreshView.
	ErrorType string       `json:"error_type,omitempty"`
	Error     string       `json:"error,omitempty"`
	Refresh   *RefreshView `json:"refresh"`
}

func refreshView(st *store.RefreshState) *RefreshView {
	if st == nil {
		return &RefreshView{}
	}
	return &RefreshView{ExpiresAt: st.ExpiresAt, LastAttemptAt: st.LastAttemptAt, LastSuccessAt: st.LastSuccessAt,
		ErrorType: st.ErrorType, Error: st.Error}
}

// fillRefresh sets View.Refresh for the accounts whose type refreshes its
// credentials, from one batch read. It never calls an upstream.
func (s *Service) fillRefresh(ctx context.Context, rows []*row, out []*View) {
	var ids []int64
	for _, a := range rows {
		if d, _ := s.refreshDecl(a.PluginKey, a.Type); d != nil {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	states, err := s.d.DB.AccountRefreshStates(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "account: read refresh states", "err", err)
	}
	for i, a := range rows {
		if slices.Contains(ids, a.ID) {
			out[i].Refresh = refreshView(states[a.ID])
		}
	}
}

// refreshCredentials is POST /accounts/:id/refresh-credentials: renew now,
// whatever the expiry, unless another renewal of the account is running.
func (s *Service) refreshCredentials(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	a, err := s.loadIn(ctx, id, perm.AccountUpdate)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if d, _ := s.refreshDecl(a.PluginKey, a.Type); d == nil {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage(t(ctx,
			"this account type does not refresh its credentials", "该账号类型不支持刷新凭证")))
		return
	}
	out, err := s.refreshAccount(ctx, id, reasonForced)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// refreshOutcome attaches the stored refresh state to out.
func (s *Service) refreshOutcome(ctx context.Context, id int64, out *RefreshOutcome) (*RefreshOutcome, error) {
	states, err := s.d.DB.AccountRefreshStates(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out.Refresh = refreshView(states[id])
	return out, nil
}
