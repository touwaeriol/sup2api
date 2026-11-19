package account

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Account balance (CONTRACTS §52), on the path of the active quota query
// (§44.4): the plugin declaring the account type describes the requests
// (BuildBalanceRequest), the core checks their targets and sends them
// through the account's proxy, the plugin reads the answers
// (ParseBalanceResponse). A balance is only shown: nothing disables,
// reschedules or alerts on it.

const (
	// defaultBalanceInterval is the update interval of a type declaring
	// balance.updateInterval 0.
	defaultBalanceInterval = 180 * time.Second
	// balanceQueryTimeout bounds one balance read, plugin calls included.
	balanceQueryTimeout = 20 * time.Second
)

var errBalanceUnimplemented = errors.New("balance query is not implemented")

// BalanceView is the API view of an account's balance (CONTRACTS §52.5).
// Amount and Currency are the upstream's number and unit (as it gave them,
// never converted; no unit when it gave none) of the last successful read,
// absent before one or when Unlimited; Error is the last failed read,
// cleared by the next successful one.
type BalanceView struct {
	Supported bool       `json:"supported"`
	Amount    string     `json:"amount,omitempty"`
	Currency  string     `json:"currency,omitempty"`
	Unlimited bool       `json:"unlimited,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	ErrorAt   *time.Time `json:"error_at,omitempty"`
}

// balanceView renders a stored snapshot (nil = none yet) for an account type
// declaring balance support.
func balanceView(snap *store.BalanceSnapshot) *BalanceView {
	v := &BalanceView{Supported: true}
	if snap == nil {
		return v
	}
	v.UpdatedAt, v.Error, v.ErrorAt = snap.UpdatedAt, snap.Error, snap.ErrorAt
	switch {
	case snap.UpdatedAt == nil:
	case snap.Unlimited:
		v.Unlimited = true
	default:
		v.Amount, v.Currency = decimal.New(snap.AmountMicros, -6).StringFixed(6), snap.Currency
	}
	return v
}

// balanceType is the binding of the account type of an account when it
// reads balances: false when it declares none or its plugin is not loaded.
func (s *Service) balanceType(pluginKey, typ string) (core.AccountTypeBinding, bool) {
	bt, ok := s.accountType(pluginKey, typ)
	return bt, ok && bt.Type.Balance != nil && bt.Client != nil
}

// balanceInterval is how often the balance of an account of type bt is read.
func balanceInterval(bt core.AccountTypeBinding) time.Duration {
	if n := bt.Type.Balance.UpdateInterval; n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultBalanceInterval
}

// balanceDue reports a snapshot whose last read, successful or not, is at
// least every old; no snapshot is due.
func balanceDue(snap *store.BalanceSnapshot, every time.Duration, now time.Time) bool {
	if snap == nil {
		return true
	}
	last := snap.UpdatedAt
	if snap.ErrorAt != nil && (last == nil || snap.ErrorAt.After(*last)) {
		last = snap.ErrorAt
	}
	return last == nil || now.Sub(*last) >= every
}

// accountBalance returns the account's balance, read first when force is
// set or the snapshot is due; nil when the type has no balance.
func (s *Service) accountBalance(ctx context.Context, a *row, force bool) (*BalanceView, error) {
	bt, ok := s.balanceType(a.PluginKey, a.Type)
	if !ok {
		return nil, nil
	}
	snap, err := s.d.DB.AccountBalance(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if force || balanceDue(snap, balanceInterval(bt), time.Now()) {
		snap, err = s.readBalance(ctx, bt, a)
		if errors.Is(err, errBalanceUnimplemented) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return balanceView(snap), nil
}

// readBalance reads the balance of account a once and stores it; concurrent
// callers on this node share the read. Upstream and plugin failures are
// recorded on the snapshot: the error is a database error, or
// errBalanceUnimplemented (nothing stored).
func (s *Service) readBalance(ctx context.Context, bt core.AccountTypeBinding, a *row) (*store.BalanceSnapshot, error) {
	v, err, _ := s.balanceFlight.Do(itoa(a.ID), func() (any, error) {
		// Not tied to the first caller: the others wait for the same result.
		ctx := context.WithoutCancel(ctx)
		qctx, cancel := context.WithTimeout(ctx, balanceQueryTimeout)
		res, err := s.runBalanceQuery(qctx, bt, a)
		cancel()
		if errors.Is(err, errBalanceUnimplemented) {
			slog.DebugContext(ctx, "account: plugin declares balance but does not implement it", "plugin", bt.Plugin.Key, "type", a.Type)
			return nil, err
		}
		return s.saveBalance(ctx, a.ID, res, err)
	})
	if err != nil {
		return nil, err
	}
	return v.(*store.BalanceSnapshot), nil
}

// runBalanceQuery asks the plugin for the requests, sends them all and has
// the plugin read the answers. Every target passes the upstream host rules
// and the administrator's plugin network access before any is sent; a
// refusal sends none. The error is the failure to record.
func (s *Service) runBalanceQuery(ctx context.Context, bt core.AccountTypeBinding, a *row) (*pluginv1.BalanceResult, error) {
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, queryFailure(false, "balance credentials unavailable", "")
	}
	// The queries go out through the row's proxy; without it the plugin is
	// not called.
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		return nil, queryFailure(false, core.AsError(err).Message, "")
	}
	acct := a.pluginAccount(plain, eg.Proxy)
	built, err := bt.Client.BuildBalanceRequest(ctx, &pluginv1.BuildBalanceRequestRequest{Account: acct})
	if status.Code(err) == codes.Unimplemented {
		return nil, errBalanceUnimplemented
	}
	if err != nil {
		return nil, queryFailure(false, "balance request builder failed: "+core.AsError(err).Message, "")
	}
	reqs := built.GetRequests()
	if len(reqs) == 0 {
		return nil, queryFailure(false, "the plugin built no balance request", "")
	}
	for _, r := range reqs {
		if ce := core.CheckPluginUpstream(ctx, s.d.NetworkAccess, bt.Plugin.Key, bt.Plugin.Manifest, r.GetUrl(), a.Type,
			string(a.Settings), string(plain)); ce != nil {
			return nil, queryFailure(false, ce.Message, "")
		}
	}
	answers := make([]*pluginv1.BalanceAnswer, len(reqs))
	for i, r := range reqs {
		ans := s.sendPluginCall(ctx, pluginCall{plugin: bt.Plugin.Key, what: "balance", method: r.GetMethod(), defaultMethod: http.MethodGet,
			url: r.GetUrl(), headers: r.GetHeaders(), body: []byte(r.GetBodyJson()), noRedirects: true}, eg)
		answers[i] = &pluginv1.BalanceAnswer{Status: ans.status, Headers: ans.headers, Body: ans.body, TransportError: ans.transportError}
	}
	out, err := bt.Client.ParseBalanceResponse(ctx, &pluginv1.ParseBalanceResponseRequest{Account: acct, Answers: answers})
	if status.Code(err) == codes.Unimplemented {
		return nil, errBalanceUnimplemented
	}
	if err != nil {
		return nil, queryFailure(false, "balance response parser failed: "+core.AsError(err).Message, "")
	}
	return out.GetResult(), nil
}

// normalizedBalance writes what the plugin read into snap, the amount and
// the unit as they are, or returns the plugin's failure to record.
func normalizedBalance(res *pluginv1.BalanceResult, snap *store.BalanceSnapshot) error {
	if res.GetErrorType() != pluginv1.BalanceResult_ERROR_TYPE_UNSPECIFIED {
		return queryFailure(res.GetErrorType() == pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, res.GetErrorMessage(), "balance query failed")
	}
	if res.GetUnlimited() {
		snap.Unlimited = true
		return nil
	}
	snap.AmountMicros, snap.Currency = res.GetAmountMicros(), res.GetCurrency()
	return nil
}

// saveBalance stores the outcome of one read: the new balance, or the
// failure on top of the last successful balance. It writes through to the
// cache and returns the snapshot as stored.
func (s *Service) saveBalance(ctx context.Context, id int64, res *pluginv1.BalanceResult, failed error) (*store.BalanceSnapshot, error) {
	now := time.Now().Truncate(time.Microsecond) // as PG keeps it: the view reads the same either way
	snap := &store.BalanceSnapshot{AccountID: id}
	if failed == nil {
		failed = normalizedBalance(res, snap)
	}
	if failed == nil {
		snap.UpdatedAt = &now
	} else {
		prev, err := s.d.DB.AccountBalance(ctx, id)
		if err != nil {
			return nil, err
		}
		if prev != nil {
			snap = prev
		}
		snap.Error, snap.ErrorAt = truncate(failed.Error(), maxQuotaError), &now
	}
	if err := s.d.DB.UpsertBalance(ctx, snap); err != nil {
		return nil, err
	}
	s.writeBalanceCache(ctx, snap)
	return snap, nil
}

// fillBalance populates the Balance field of views for accounts whose type
// declares balance support. It never calls an upstream.
func (s *Service) fillBalance(ctx context.Context, rows []*row, out []*View) {
	var ids []int64
	supported := make([]bool, len(rows))
	for i, a := range rows {
		if _, ok := s.balanceType(a.PluginKey, a.Type); ok {
			supported[i] = true
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	snaps, err := s.cachedAccountBalances(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "account: load balance snapshots", "err", err)
		return
	}
	for i, a := range rows {
		if supported[i] {
			out[i].Balance = balanceView(snaps[a.ID])
		}
	}
}

// getBalance is GET /accounts/:id/balance[?force=true].
func (s *Service) getBalance(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := httpapi.PathID(c, "id")
	if !ok {
		return
	}
	force, _ := strconv.ParseBool(c.Query("force"))
	a, err := s.loadIn(ctx, id, perm.AccountRead)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	v, err := s.accountBalance(ctx, a, force)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if v == nil {
		httpapi.Fail(c, core.NewError(404, "unsupported", "account type does not support balance"))
		return
	}
	httpapi.OK(c, v)
}
