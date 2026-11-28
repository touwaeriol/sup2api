package account

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Subscription quota of accounts (CONTRACTS §44), modelled on sub2api's
// AccountUsageService: the snapshot is fed mainly by the quota headers of
// the gateway's own upstream responses (passive, no extra upstream call) and,
// for account types whose plugin can ask the upstream, by an active query
// every 30 minutes, earlier when the plugin asks for it (a window resets) or
// to retry a failed query. Explicit force refreshes have no elapsed-time
// cooldown; passive response observations remain timely.

const (
	// quotaFresh: a snapshot younger than this (either source) is served as
	// is (sub2api apiCacheTTL), and the regular gap between active queries.
	quotaFresh = 30 * time.Minute
	// quotaErrorTTL caps the retry backoff after failed active queries
	// (sub2api apiErrorCacheTTL); quotaRetryFirst is its first step.
	quotaErrorTTL   = quotaFresh
	quotaRetryFirst = time.Minute
	// quotaQueryFloor is the minimum gap for non-forced/background queries
	// of one account, on any node (enforced in PG). It only stops bursts: at
	// 30 minutes it also held back the plugin's check at a window reset and
	// every retry, and the account with them (2026-10-21 audit).
	quotaQueryFloor = time.Minute
	// quotaPassiveInterval is the minimum gap between two passive writes of
	// one account on one node; a status change is written at once.
	quotaPassiveInterval = 30 * time.Second
	quotaQueryTimeout    = 20 * time.Second
	maxQuotaError        = 512
	maxQuotaErrorDetail  = 256
	quotaFlushTick       = 5 * time.Second // also core.account.quota_sweep's period
	quotaIdle            = 10 * time.Minute
)

var errQuotaQueryUnimplemented = errors.New("quota query is not implemented")

// quotaKeyOrder orders the windows the console knows; other keys follow the
// account type's declaration, then the alphabet.
func quotaWindowRank(decl *manifest.AccountQuota, key string) int {
	for i, w := range decl.Windows {
		if w.Key == key {
			return i
		}
	}
	for i, h := range decl.Headers {
		if h.Key == key {
			return len(decl.Windows) + i
		}
	}
	return len(decl.Windows) + len(decl.Headers)
}

// QuotaSnapshot is the API view of an account's subscription quota. Account
// types without quota have no snapshot (View.quota is null).
type QuotaSnapshot struct {
	Supported bool `json:"supported"`
	// Source of the newest data: "passive", "active", or "" before any.
	Source    string     `json:"source"`
	UpdatedAt *time.Time `json:"updated_at"`
	// Error of the last active query, cleared by the next successful sample.
	Error         string        `json:"error"`
	Windows       []QuotaWindow `json:"windows"`
	NextCheckAt   *time.Time    `json:"next_check_at,omitempty"`
	RefreshStatus string        `json:"refresh_status"`
}

// QuotaWindow is one window of a QuotaSnapshot. A window whose reset time
// has passed is shown as reset: utilization 0, no reset time, no status.
type QuotaWindow struct {
	Key                string            `json:"key"`
	Utilization        float64           `json:"utilization"`
	UtilizationUnknown bool              `json:"utilization_unknown,omitempty"`
	ResetsAt           *time.Time        `json:"resets_at"`
	Status             string            `json:"status"`
	Used               int64             `json:"used,omitempty"`
	Limit              int64             `json:"limit,omitempty"`
	Expired            bool              `json:"expired"`
	Label              map[string]string `json:"label,omitempty"`
	ShortLabel         map[string]string `json:"short_label,omitempty"`
}

// quotaView renders a stored snapshot (nil = none yet) for an account type
// declaring decl.
func quotaView(decl *manifest.AccountQuota, snap *store.QuotaSnapshot, now time.Time) *QuotaSnapshot {
	v := &QuotaSnapshot{Supported: true, Windows: []QuotaWindow{}, RefreshStatus: "pending"}
	if snap == nil {
		return v
	}
	v.Source, v.UpdatedAt, v.Error = snap.Source, snap.UpdatedAt, snap.Error
	v.NextCheckAt = snap.NextCheckAt
	if snap.UpdatedAt != nil {
		v.RefreshStatus = "ready"
	}
	if snap.Error != "" {
		v.RefreshStatus = "error"
	}
	if snap.UpdatedAt == nil {
		v.Source = ""
	}
	for key, w := range snap.Windows {
		qw := QuotaWindow{Key: key, Utilization: w.Utilization, UtilizationUnknown: w.UtilizationUnknown, ResetsAt: w.ResetsAt, Status: w.Status, Used: w.Used, Limit: w.Limit}
		qw.Expired = qw.ResetsAt != nil && !now.Before(*qw.ResetsAt)
		for _, definition := range decl.Windows {
			if definition.Key == key {
				qw.Label, qw.ShortLabel = definition.Label, definition.ShortLabel
				break
			}
		}
		v.Windows = append(v.Windows, qw)
	}
	rank := func(key string) int { return quotaWindowRank(decl, key) }
	sort.Slice(v.Windows, func(i, j int) bool {
		ri, rj := rank(v.Windows[i].Key), rank(v.Windows[j].Key)
		if ri != rj {
			return ri < rj
		}
		return v.Windows[i].Key < v.Windows[j].Key
	})
	return v
}

// quotaDecl returns the quota declaration of an account's type, nil when the
// type has none or is not registered (plugin disabled).
func (s *Service) quotaDecl(pluginKey, typ string) (*manifest.AccountQuota, core.AccountTypeBinding) {
	bt, ok := s.accountType(pluginKey, typ)
	if !ok || !bt.Type.Quota.Supported() {
		return nil, bt
	}
	return bt.Type.Quota, bt
}

// fillQuota sets View.Quota for the accounts whose type has quota, from one
// batch read of the stored snapshots. It never calls an upstream.
func (s *Service) fillQuota(ctx context.Context, rows []*row, out []*View) {
	decls := make([]*manifest.AccountQuota, len(rows))
	var ids []int64
	for i, a := range rows {
		if decls[i], _ = s.quotaDecl(a.PluginKey, a.Type); decls[i] != nil {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	snaps := s.cachedAccountQuotas(ctx, ids)
	now := time.Now()
	for i, a := range rows {
		if decls[i] != nil {
			out[i].Quota = quotaView(decls[i], snaps[a.ID], now)
		}
	}
}

// ---------------------------------------------------------------- active query

// getQuota is GET /accounts/:id/quota[?force=true].
func (s *Service) getQuota(c *gin.Context) {
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
	v, err := s.accountQuota(ctx, a, force)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, v)
}

// accountQuota returns the account's snapshot, refreshing it with one active
// query first when the type supports it and the snapshot is stale (or force
// is set, bypassing the elapsed-time floor). Concurrent callers for one
// account share the query. Upstream and plugin failures do not fail the
// call: they are recorded on the snapshot (negative cache, quotaErrorTTL).
func (s *Service) accountQuota(ctx context.Context, a *row, force bool) (*QuotaSnapshot, error) {
	decl, bt := s.quotaDecl(a.PluginKey, a.Type)
	if decl == nil {
		return &QuotaSnapshot{Windows: []QuotaWindow{}}, nil
	}
	snap, err := s.d.DB.AccountQuota(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	// A type that does not auto-refresh (it reads its quota from response
	// headers, CONTRACTS §70) is queried only when the user asks for it.
	if decl.Query && bt.Client != nil && (force || decl.AutoRefresh) && shouldQueryQuota(snap, force, time.Now()) {
		v, err, _ := s.quotaFlight.Do(itoa(a.ID), func() (any, error) {
			// Not tied to the first caller: the others wait for the same result.
			qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaQueryTimeout+5*time.Second)
			defer cancel()
			return s.queryQuota(qctx, bt, a, force)
		})
		if err != nil {
			return nil, err
		}
		if v != nil {
			snap = v.(*store.QuotaSnapshot)
		}
	}
	return quotaView(decl, snap, time.Now()), nil
}

// shouldQueryQuota decides, on this node's reading of the snapshot, whether
// an active query is worth attempting. LeaseAccountQuota re-checks the
// non-forced floor and the in-flight lease atomically in PG.
func shouldQueryQuota(snap *store.QuotaSnapshot, force bool, now time.Time) bool {
	if force || snap == nil {
		return true
	}
	if snap.LastActiveAt != nil && now.Sub(*snap.LastActiveAt) < quotaQueryFloor {
		return false
	}
	if snap.NextCheckAt != nil {
		return !now.Before(*snap.NextCheckAt)
	}
	if snap.UpdatedAt != nil && now.Sub(*snap.UpdatedAt) < quotaFresh {
		return false
	}
	if snap.Error != "" && snap.LastActiveAt != nil && now.Sub(*snap.LastActiveAt) < quotaErrorTTL {
		return false
	}
	return true
}

// queryQuota claims the account's query slot, asks the plugin for the
// request, sends it through the account's proxy and stores what the plugin
// read out of the answer. It returns the snapshot as stored afterwards; only
// database errors are returned as errors.
func (s *Service) queryQuota(ctx context.Context, bt core.AccountTypeBinding, a *row, force bool) (*store.QuotaSnapshot, error) {
	floor := quotaQueryFloor
	if force {
		floor = 0
	}
	return s.executeQuotaQuery(ctx, bt, a, true, floor)
}

// runQuotaQuery performs one active query and saves its windows. It returns
// the error to record on the snapshot, "" on success or when the plugin does
// not implement the query after all.
func (s *Service) runQuotaQuery(ctx context.Context, bt core.AccountTypeBinding, a *row) (*pluginv1.QuotaResult, error) {
	plain, err := s.decrypt(a.PluginKey, a.CredEnc)
	if err != nil {
		return nil, errors.New("quota credentials unavailable")
	}
	// The query goes out through the row's proxy; without it the plugin is
	// not called.
	eg, err := s.proxyEgress(ctx, a.ProxyID)
	if err != nil {
		return nil, queryFailure(false, core.AsError(err).Message, "")
	}
	acct := a.pluginAccount(plain, eg.Proxy)
	built, err := bt.Client.BuildQuotaRequest(ctx, &pluginv1.BuildQuotaRequestRequest{Account: acct})
	if status.Code(err) == codes.Unimplemented {
		slog.DebugContext(ctx, "account: plugin declares quota.query but does not implement it", "plugin", bt.Plugin.Key, "type", a.Type)
		return nil, errQuotaQueryUnimplemented
	}
	if err != nil || built == nil {
		return nil, errors.New("quota request builder unavailable")
	}
	in := s.sendQuotaRequest(ctx, bt.Plugin.Key, built, eg)
	in.Account = acct
	res, err := bt.Client.ParseQuotaResponse(ctx, in)
	if status.Code(err) == codes.Unimplemented {
		return nil, errQuotaQueryUnimplemented
	}
	if err != nil || res == nil {
		return nil, errors.New("quota response parser unavailable")
	}
	return res, nil
}

// quotaErrorDetail is the plugin's reason for a failed quota query, kept
// after the error kind so the console shows it and the plugin's model
// availability can read it back: one line, at most maxQuotaErrorDetail bytes.
func quotaErrorDetail(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(message, ""))
	message = strings.Join(strings.Fields(message), " ")
	for len(message) > maxQuotaErrorDetail {
		_, size := utf8.DecodeLastRuneInString(message)
		message = message[:len(message)-size]
	}
	return message
}

// queryFailure is the error recorded on the snapshot of a quota or balance
// query the plugin reports failed: "auth_rejected: " or "transient: ", then
// the plugin's reason (quotaErrorDetail), fallback when it gives none.
func queryFailure(authRejected bool, message, fallback string) error {
	kind := "transient: "
	if authRejected {
		kind = "auth_rejected: "
	}
	return errors.New(kind + cmp.Or(quotaErrorDetail(message), fallback))
}

func normalizedQuota(res *pluginv1.QuotaResult) (map[string]store.QuotaWindow, error) {
	if res == nil {
		return nil, errors.New("empty quota observation")
	}
	if res.GetErrorType() != pluginv1.QuotaResult_ERROR_TYPE_UNSPECIFIED {
		return nil, queryFailure(res.GetErrorType() == pluginv1.QuotaResult_ERROR_TYPE_AUTH_REJECTED, res.GetErrorMessage(), "quota query failed")
	}
	if len(res.Windows) > 64 {
		return nil, errors.New("too many quota windows")
	}
	windows := make(map[string]store.QuotaWindow, len(res.Windows))
	for _, w := range res.Windows {
		if w == nil || !validQuotaKey(w.Key) || math.IsNaN(w.Utilization) || math.IsInf(w.Utilization, 0) || w.Utilization < 0 || w.Used < 0 || w.Limit < 0 || w.ResetsAtUnix < 0 || w.ResetsAtUnix > 253402300799 {
			return nil, errors.New("invalid quota window")
		}
		if w.UtilizationUnknown && (w.Utilization != 0 || w.Used != 0 || w.Limit != 0) {
			return nil, errors.New("unknown quota utilization must not contain amounts")
		}
		if _, exists := windows[w.Key]; exists {
			return nil, errors.New("duplicate quota window")
		}
		status := manifest.NormalizeQuotaStatus(w.Status)
		if w.Status != "" && status == "" {
			return nil, errors.New("invalid quota status")
		}
		sw := store.QuotaWindow{Utilization: w.Utilization, UtilizationUnknown: w.UtilizationUnknown, Status: status, Used: w.Used, Limit: w.Limit}
		if !sw.UtilizationUnknown && sw.Utilization == 0 && sw.Limit > 0 && sw.Used > 0 {
			sw.Utilization = float64(sw.Used) / float64(sw.Limit) * 100
		}
		if w.ResetsAtUnix > 0 {
			t := time.Unix(w.ResetsAtUnix, 0).UTC()
			sw.ResetsAt = &t
		}
		windows[w.Key] = sw
	}
	return windows, nil
}

// validQuotaKey mirrors the manifest rule for window keys.
func validQuotaKey(k string) bool {
	if k == "" || len(k) > 32 {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '_' && i > 0:
		default:
			return false
		}
	}
	return true
}

// sendQuotaRequest sends the request the plugin built through the account's
// proxy (behind netguard for a direct connection) and collects what the
// plugin needs to read the answer. A failure is not an error: the plugin is
// shown the transport error, as ClassifyError is.
func (s *Service) sendQuotaRequest(ctx context.Context, pluginKey string, b *pluginv1.BuildQuotaRequestResponse, eg core.Egress) *pluginv1.ParseQuotaResponseRequest {
	a := s.sendPluginCall(ctx, pluginCall{plugin: pluginKey, what: "quota", method: b.GetMethod(), defaultMethod: http.MethodGet,
		url: b.GetUrl(), headers: b.GetHeaders(), body: []byte(b.GetBodyJson())}, eg)
	return &pluginv1.ParseQuotaResponseRequest{Status: a.status, Headers: a.headers, Body: a.body,
		TransportError: a.transportError}
}

// pluginCall is an upstream request a plugin described (quota, balance,
// refresh, HostService.SendAccountRequest).
type pluginCall struct {
	plugin                string // the plugin's key, for plugin network access
	what                  string // for the invalid-URL message
	method, defaultMethod string
	url                   string
	headers               map[string]string
	body                  []byte // sent as is; Content-Type: application/json unless headers set one
	// noRedirects answers a redirect as it is instead of following it.
	noRedirects bool
}

// pluginAnswer is what the plugin is shown of the upstream's answer.
type pluginAnswer struct {
	status         int32
	headers        map[string]string // lower-cased, first value
	body           []byte            // the whole answer
	transportError string
}

// sendPluginCall sends a request a plugin described through eg, the egress
// of the account's row the caller just read, whose proxy the plugin was
// handed (behind netguard for a direct connection). A failure is not an error: the plugin is shown the
// transport error, as ClassifyError is; an answer that cannot be read whole
// is such a failure.
func (s *Service) sendPluginCall(ctx context.Context, b pluginCall, eg core.Egress) pluginAnswer {
	var out pluginAnswer
	u, err := url.Parse(b.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		out.transportError = "plugin built an invalid " + b.what + " URL"
		return out
	}
	if err := s.guardUpstream(ctx, b.plugin, u, eg.Proxied); err != nil {
		out.transportError = err.Error()
		return out
	}
	method := strings.ToUpper(b.method)
	if method == "" {
		method = b.defaultMethod
	}
	var body io.Reader
	if len(b.body) > 0 {
		body = bytes.NewReader(b.body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		out.transportError = transportError(upstreamAddr(u), err)
		return out
	}
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := eg.Client
	if b.noRedirects {
		c := *hc
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		hc = &c
	}
	resp, err := hc.Do(req)
	if err != nil {
		out.transportError = transportError(upstreamAddr(u), err)
		return out
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		// An answer cut short is a transport failure, not a shorter answer.
		out.transportError = transportError(upstreamAddr(u), err)
		return out
	}
	out.status, out.body = int32(resp.StatusCode), raw
	out.headers = map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			out.headers[strings.ToLower(k)] = v[0]
		}
	}
	return out
}

// ---------------------------------------------------------------- passive samples

// ObserveQuotaHeaders implements core.QuotaObserver: it reads the declared
// quota headers of one gateway upstream response (2xx, 429, or the 101 of a
// WebSocket handshake) and queues them for the throttled writer. Cheap and
// non-blocking.
func (s *Service) ObserveQuotaHeaders(accountID int64, decls []manifest.QuotaHeader, status int, h http.Header) {
	if len(decls) == 0 || (status != http.StatusTooManyRequests && status != http.StatusSwitchingProtocols && (status < 200 || status > 299)) {
		return
	}
	now := time.Now()
	s.observeQuota(accountID, manifest.ReadQuotaHeaders(decls, h.Get, now), now)
}

var _ core.QuotaObserver = (*Service)(nil)

// quotaRecorder throttles passive quota writes per account on this node: at
// most one write every quotaPassiveInterval, except that a window changing
// status (allowed -> rejected and back) is written at once. Samples arriving
// in between are merged and written by the next flush, so the last sample of
// a burst is never lost.
type quotaRecorder struct {
	save func(ctx context.Context, accountID int64, windows map[string]store.QuotaWindow) error

	mu      sync.Mutex
	writeMu sync.Mutex
	entries map[int64]*quotaEntry
	kick    chan struct{}
}

type quotaEntry struct {
	pending   map[string]store.QuotaWindow // nil = nothing to write
	status    map[string]string            // last status seen per window
	urgent    bool
	lastWrite time.Time
	seen      time.Time
}

func newQuotaRecorder(save func(context.Context, int64, map[string]store.QuotaWindow) error) *quotaRecorder {
	return &quotaRecorder{save: save, entries: map[int64]*quotaEntry{}, kick: make(chan struct{}, 1)}
}

// observe merges one sample into the account's pending write and wakes the
// writer when the write is due.
func (r *quotaRecorder) observe(accountID int64, windows map[string]store.QuotaWindow, now time.Time) {
	r.mu.Lock()
	e := r.entries[accountID]
	if e == nil {
		e = &quotaEntry{status: map[string]string{}}
		r.entries[accountID] = e
	}
	if e.pending == nil {
		e.pending = make(map[string]store.QuotaWindow, len(windows))
	}
	for k, w := range windows {
		if prev, ok := e.status[k]; ok && prev != w.Status {
			e.urgent = true
		}
		e.status[k] = w.Status
		e.pending[k] = w
	}
	e.seen = now
	due := e.urgent || now.Sub(e.lastWrite) >= quotaPassiveInterval
	r.mu.Unlock()
	if due {
		select {
		case r.kick <- struct{}{}:
		default:
		}
	}
}

// take removes and returns the pending writes that are due (all of them
// when all is set), and forgets accounts idle for quotaIdle.
func (r *quotaRecorder) take(now time.Time, all bool) map[int64]map[string]store.QuotaWindow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[int64]map[string]store.QuotaWindow{}
	for id, e := range r.entries {
		if e.pending == nil {
			if now.Sub(e.seen) > quotaIdle {
				delete(r.entries, id)
			}
			continue
		}
		if all || e.urgent || now.Sub(e.lastWrite) >= quotaPassiveInterval {
			out[id] = e.pending
			e.pending, e.urgent, e.lastWrite = nil, false, now
		}
	}
	return out
}

// flush writes the due samples. A failed write is logged and dropped: the
// next sample supersedes it.
func (r *quotaRecorder) flush(ctx context.Context, all bool) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	for id, w := range r.take(time.Now(), all) {
		if err := r.save(ctx, id, w); err != nil {
			slog.WarnContext(ctx, "account: save passive quota", "account", id, "err", err)
		}
	}
}

// run writes due samples on wake-ups and every quotaFlushTick until ctx is
// done, then writes what is left.
func (r *quotaRecorder) run(ctx context.Context) {
	tk := time.NewTicker(quotaFlushTick)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.flush(fctx, true)
			cancel()
			return
		case <-r.kick:
			r.flush(ctx, false)
		case <-tk.C:
			r.flush(ctx, false)
		}
	}
}

// forget drops the samples of one account not written yet (its quota
// snapshot was just cleared).
func (r *quotaRecorder) forget(accountID int64) {
	r.mu.Lock()
	delete(r.entries, accountID)
	r.mu.Unlock()
}
