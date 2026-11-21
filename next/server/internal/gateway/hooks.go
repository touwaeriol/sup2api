package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

const (
	pointGatewayRequest = manifest.HookPointRequest
	pointGatewayAttempt = manifest.HookPointAttempt
	fieldPromptText     = "prompt_text"

	breakerThreshold = 10
	breakerOpenFor   = 30 * time.Second

	defaultMaxPromptBytes = 32 << 10
	maxHookNoteBytes      = 1 << 10

	hookStatsTTL = 7 * 24 * time.Hour
)

// Hook decisions recorded in usage_logs.hook_decisions.
const (
	decisionAllow          = "allow"
	decisionDeny           = "deny"
	decisionErrorOpen      = "error_open"
	decisionErrorClosed    = "error_closed"
	decisionSkippedBreaker = "skipped_breaker"
)

// latencyBuckets are the upper bounds (ms) of the hook latency histogram
// used for the approximate P99; the last implicit bucket is +Inf. Buckets
// may only be appended (the Redis hash stores them by index); the upper ones
// cover hooks with long timeouts (maxHookTimeout, CONTRACTS §20.1).
var latencyBuckets = []int{1, 2, 5, 10, 20, 50, 100, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 10000, 20000, 30000}

type hookKey struct{ plugin, hook string }

func breakerKey(k hookKey) string   { return "hook:breaker:" + k.plugin + ":" + k.hook }
func hookStatsKey(k hookKey) string { return "hook:stats:" + k.plugin + ":" + k.hook }
func hookIndexKey(plugin string) string {
	return "hook:statidx:" + plugin
}

type hookCounters struct {
	point    string
	calls    int64
	denies   int64
	timeouts int64
	errors   int64
	buckets  []int64 // len(latencyBuckets)+1
}

func newHookCounters(point string) *hookCounters {
	return &hookCounters{point: point, buckets: make([]int64, len(latencyBuckets)+1)}
}

type breakerState struct {
	fails     int
	openUntil time.Time
	checkedAt time.Time
}

// hookRuntime holds the per-node breaker state and the stats accumulator
// (flushed to Redis every few seconds so the hot path never waits on it).
type hookRuntime struct {
	rdb      redis.UniversalClient
	now      func() time.Time
	mu       sync.Mutex
	breakers map[hookKey]*breakerState
	pending  map[hookKey]*hookCounters
}

func newHookRuntime(rdb redis.UniversalClient) *hookRuntime {
	return &hookRuntime{rdb: rdb, now: time.Now, breakers: map[hookKey]*breakerState{}, pending: map[hookKey]*hookCounters{}}
}

func (h *hookRuntime) state(k hookKey) *breakerState {
	st := h.breakers[k]
	if st == nil {
		st = &breakerState{}
		h.breakers[k] = st
	}
	return st
}

// breakerOpen reports whether calls to the hook are suspended. The open
// state is shared through Redis; each node re-reads it at most once a second.
func (h *hookRuntime) breakerOpen(ctx context.Context, k hookKey) bool {
	now := h.now()
	h.mu.Lock()
	st := h.state(k)
	if now.Before(st.openUntil) {
		h.mu.Unlock()
		return true
	}
	if h.rdb == nil || now.Sub(st.checkedAt) < time.Second {
		h.mu.Unlock()
		return false
	}
	st.checkedAt = now
	h.mu.Unlock()

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	v, err := h.rdb.Get(rctx, breakerKey(k)).Result()
	if err != nil {
		return false
	}
	until := now.Add(time.Second)
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		until = time.UnixMilli(ms)
	}
	if !now.Before(until) {
		return false
	}
	h.mu.Lock()
	st.openUntil = until
	h.mu.Unlock()
	return true
}

// recordResult tracks consecutive failures and opens the breaker after
// breakerThreshold of them.
func (h *hookRuntime) recordResult(ctx context.Context, k hookKey, failed bool) {
	h.mu.Lock()
	st := h.state(k)
	if !failed {
		st.fails = 0
		h.mu.Unlock()
		return
	}
	st.fails++
	if st.fails < breakerThreshold {
		h.mu.Unlock()
		return
	}
	st.fails = 0
	until := h.now().Add(breakerOpenFor)
	st.openUntil = until
	h.mu.Unlock()
	slog.WarnContext(ctx, "gateway: hook breaker opened", "plugin", k.plugin, "hook", k.hook, "until", until)
	if h.rdb == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if err := h.rdb.Set(rctx, breakerKey(k), strconv.FormatInt(until.UnixMilli(), 10), breakerOpenFor).Err(); err != nil {
		slog.WarnContext(ctx, "gateway: store hook breaker", "err", err)
	}
}

type hookOutcome int

const (
	outcomeAllow hookOutcome = iota
	outcomeDeny
	outcomeTimeout
	outcomeError
)

func (h *hookRuntime) observe(k hookKey, point string, o hookOutcome, latency time.Duration) {
	ms := int(latency / time.Millisecond)
	b := len(latencyBuckets)
	for i, ub := range latencyBuckets {
		if ms <= ub {
			b = i
			break
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.pending[k]
	if c == nil {
		c = newHookCounters(point)
		h.pending[k] = c
	}
	c.calls++
	c.buckets[b]++
	switch o {
	case outcomeDeny:
		c.denies++
	case outcomeTimeout:
		c.timeouts++
	case outcomeError:
		c.errors++
	}
}

// flush moves the local counters to Redis (cluster-wide sums).
func (h *hookRuntime) flush(ctx context.Context) {
	if h.rdb == nil {
		return // keep counting locally
	}
	h.mu.Lock()
	pending := h.pending
	h.pending = map[hookKey]*hookCounters{}
	h.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	pipe := h.rdb.Pipeline()
	for k, c := range pending {
		key := hookStatsKey(k)
		pipe.HSet(ctx, key, "point", c.point)
		pipe.HIncrBy(ctx, key, "calls", c.calls)
		for name, v := range map[string]int64{"denies": c.denies, "timeouts": c.timeouts, "errors": c.errors} {
			if v != 0 {
				pipe.HIncrBy(ctx, key, name, v)
			}
		}
		for i, v := range c.buckets {
			if v != 0 {
				pipe.HIncrBy(ctx, key, "b"+strconv.Itoa(i), v)
			}
		}
		pipe.Expire(ctx, key, hookStatsTTL)
		pipe.SAdd(ctx, hookIndexKey(k.plugin), k.hook)
		pipe.Expire(ctx, hookIndexKey(k.plugin), hookStatsTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		slog.WarnContext(ctx, "gateway: flush hook stats", "err", err)
		// Put the counters back so they are retried.
		h.mu.Lock()
		for k, c := range pending {
			if cur := h.pending[k]; cur != nil {
				cur.calls += c.calls
				cur.denies += c.denies
				cur.timeouts += c.timeouts
				cur.errors += c.errors
				for i := range cur.buckets {
					cur.buckets[i] += c.buckets[i]
				}
			} else {
				h.pending[k] = c
			}
		}
		h.mu.Unlock()
	}
}

func p99(buckets []int64, calls int64) int {
	if calls <= 0 {
		return 0
	}
	need := (calls*99 + 99) / 100
	var acc int64
	for i, v := range buckets {
		acc += v
		if acc >= need {
			if i < len(latencyBuckets) {
				return latencyBuckets[i]
			}
			return latencyBuckets[len(latencyBuckets)-1] * 2
		}
	}
	return latencyBuckets[len(latencyBuckets)-1] * 2
}

// HookStats implements core.HookStatsSource: cluster-wide counters of every
// hook of the plugin seen in the last week, plus the hooks of the active
// generation that have not been called yet.
func (g *Gateway) HookStats(ctx context.Context, pluginKey string) ([]core.HookStat, error) {
	h := g.hooks
	h.flush(ctx)
	byID := map[string]*core.HookStat{}
	if g.d.Registry != nil {
		if gen := g.d.Registry.Current(); gen != nil {
			for _, point := range []string{pointGatewayRequest, pointGatewayAttempt} {
				for _, hb := range gen.Hooks(point) {
					if hb.Plugin.Key == pluginKey {
						id := hookID(hb)
						byID[id] = &core.HookStat{HookID: id, Point: hb.Hook.Point}
					}
				}
			}
		}
	}
	if h.rdb == nil {
		h.mu.Lock()
		for k, c := range h.pending {
			if k.plugin != pluginKey {
				continue
			}
			byID[k.hook] = &core.HookStat{HookID: k.hook, Point: c.point, Calls: c.calls, Denies: c.denies,
				Timeouts: c.timeouts, Errors: c.errors, P99Ms: p99(c.buckets, c.calls)}
		}
		for k, st := range h.breakers {
			if k.plugin == pluginKey && h.now().Before(st.openUntil) {
				if s := byID[k.hook]; s != nil {
					s.BreakerOpen = true
				}
			}
		}
		h.mu.Unlock()
	} else {
		ids, err := h.rdb.SMembers(ctx, hookIndexKey(pluginKey)).Result()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if byID[id] == nil {
				byID[id] = &core.HookStat{HookID: id}
			}
		}
		pipe := h.rdb.Pipeline()
		type q struct {
			all *redis.MapStringStringCmd
			brk *redis.IntCmd
		}
		qs := map[string]q{}
		for id := range byID {
			k := hookKey{pluginKey, id}
			qs[id] = q{all: pipe.HGetAll(ctx, hookStatsKey(k)), brk: pipe.Exists(ctx, breakerKey(k))}
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for id, r := range qs {
			s := byID[id]
			m := r.all.Val()
			if p := m["point"]; p != "" && s.Point == "" {
				s.Point = p
			}
			num := func(f string) int64 { n, _ := strconv.ParseInt(m[f], 10, 64); return n }
			s.Calls, s.Denies, s.Timeouts, s.Errors = num("calls"), num("denies"), num("timeouts"), num("errors")
			buckets := make([]int64, len(latencyBuckets)+1)
			for i := range buckets {
				buckets[i] = num("b" + strconv.Itoa(i))
			}
			s.P99Ms = p99(buckets, s.Calls)
			s.BreakerOpen = r.brk.Val() > 0
		}
	}
	out := make([]core.HookStat, 0, len(byID))
	for _, s := range byID {
		if s.Point == "" {
			s.Point = pointGatewayRequest
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HookID < out[j].HookID })
	return out, nil
}

// hookID is the manifest hook id, or its index in manifest.hooks.
func hookID(hb core.HookBinding) string {
	if hb.Hook.ID != "" {
		return hb.Hook.ID
	}
	if m := hb.Plugin.Manifest; m != nil {
		for i, h := range m.Hooks {
			if reflect.DeepEqual(h, hb.Hook) {
				return strconv.Itoa(i)
			}
		}
	}
	return fmt.Sprintf("%s#%d", hb.Hook.Point, hb.Hook.Order)
}

// ---------------------------------------------------------------- execution

// matchesRequest reports whether a manifest match block (hooks §20.1,
// scheduler.rank §24.1 — both use manifest.HookMatch) covers this request.
func (c *call) matchesRequest(m manifest.HookMatch) bool {
	return matchList(m.Protocols, c.ep.Protocol) &&
		matchList(m.Models, c.model) &&
		matchList(m.Groups, c.principal.Group.Name, itoa(c.principal.Group.ID))
}

func (c *call) hookMatches(hb core.HookBinding) bool {
	return c.matchesRequest(hb.Hook.Match)
}

// runHooks executes the matching gateway.request hooks in order. It returns
// the error to send when a hook denies (or fails closed).
func (c *call) runHooks(ctx context.Context) *gwError {
	return c.runPoint(ctx, pointGatewayRequest, nil)
}

// runAttemptHooks executes the matching gateway.attempt hooks for the
// account the request is about to be sent to (CONTRACTS §85), except those
// of plugins whose gateway.request hook settled the request
// (skip_attempt_hooks). A deny ends the request: it is about the request,
// not the account, so it never fails over.
func (c *call) runAttemptHooks(ctx context.Context, ref *core.AccountRef) *gwError {
	return c.runPoint(ctx, pointGatewayAttempt, ref)
}

// runPoint runs the hooks of one point in order; ref is the attempt's
// account (nil for gateway.request).
func (c *call) runPoint(ctx context.Context, point string, ref *core.AccountRef) *gwError {
	var hooks []core.HookBinding
	for _, hb := range c.gen.Hooks(point) {
		if hb.Client != nil && !(ref != nil && c.attemptHooksSkipped[hb.Plugin.Key]) && c.hookMatches(hb) {
			hooks = append(hooks, hb)
		}
	}
	sort.SliceStable(hooks, func(i, j int) bool { return hooks[i].Hook.Order < hooks[j].Hook.Order })
	for _, hb := range hooks {
		dec, deny := c.runHook(ctx, hb, ref)
		c.rec.HookDecisions = append(c.rec.HookDecisions, dec)
		if deny != nil {
			return deny
		}
	}
	return nil
}

// hookMeta is the request meta a hook sees: an attempt hook gets the account
// and its environment layer.
func (c *call) hookMeta(ref *core.AccountRef) *pluginv1.RequestMeta {
	m := c.meta()
	if ref != nil {
		m.AccountId, m.Environment = ref.ID, c.environment(ref)
	}
	return m
}

// skipAttemptHooks remembers that a plugin's gateway.request hook settled
// the request for every attempt.
func (c *call) skipAttemptHooks(plugin string) {
	if c.attemptHooksSkipped == nil {
		c.attemptHooksSkipped = map[string]bool{}
	}
	c.attemptHooksSkipped[plugin] = true
}

func (c *call) runHook(ctx context.Context, hb core.HookBinding, ref *core.AccountRef) (core.HookDecision, *gwError) {
	g := c.g
	id := hookID(hb)
	k := hookKey{hb.Plugin.Key, id}
	dec := core.HookDecision{PluginKey: hb.Plugin.Key, HookID: id}
	if ref != nil {
		dec.AccountID = ref.ID
	}
	closed := strings.EqualFold(hb.Hook.Failure, "closed")
	failed := func(decisionNote string) (core.HookDecision, *gwError) {
		dec.Note = truncateUTF8(decisionNote, maxHookNoteBytes)
		if !closed {
			if dec.Decision == "" {
				dec.Decision = decisionErrorOpen
			}
			return dec, nil
		}
		if dec.Decision == "" {
			dec.Decision = decisionErrorClosed
		}
		return dec, &gwError{Status: 503, Code: "hook_unavailable",
			Message: "request check is temporarily unavailable, please retry later", RecordType: errTypeBlockedByHook}
	}

	if g.hooks.breakerOpen(ctx, k) {
		dec.Decision = decisionSkippedBreaker
		return failed("breaker open")
	}

	timeout := time.Duration(c.gw.DefaultHookTimeoutMs) * time.Millisecond
	if hb.Hook.TimeoutMs > 0 {
		timeout = time.Duration(hb.Hook.TimeoutMs) * time.Millisecond
	}
	if timeout > maxHookTimeout {
		timeout = maxHookTimeout
	}
	req := &pluginv1.GatewayRequestHookRequest{Meta: c.hookMeta(ref), HookId: id, Fields: c.hookFields(hb)}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	resp, err := hb.Client.OnGatewayRequest(hctx, req)
	latency := time.Since(start)
	timedOut := errors.Is(hctx.Err(), context.DeadlineExceeded)
	cancel()
	dec.LatencyMs = int(latency / time.Millisecond)

	if ctx.Err() != nil {
		// The client went away; not the hook's fault.
		dec.Decision = decisionErrorOpen
		dec.Note = "client canceled"
		return dec, &gwError{Status: statusClientClosed, RecordType: errTypeClientCanceled, Message: "client canceled"}
	}
	if err == nil && resp == nil {
		err = errors.New("empty hook response")
	}
	if err != nil {
		o := outcomeError
		note := "error: " + err.Error()
		if timedOut {
			o, note = outcomeTimeout, fmt.Sprintf("timeout after %s", timeout)
		}
		g.hooks.observe(k, hb.Hook.Point, o, latency)
		g.hooks.recordResult(ctx, k, true)
		return failed(note)
	}

	if resp.GetDecision() == pluginv1.GatewayRequestHookResponse_DECISION_DENY {
		g.hooks.observe(k, hb.Hook.Point, outcomeDeny, latency)
		g.hooks.recordResult(ctx, k, false)
		dec.Decision = decisionDeny
		dec.Note = truncateUTF8(resp.GetNote(), maxHookNoteBytes)
		status := int(resp.GetDenyStatus())
		if status < 400 || status > 599 {
			status = 403
		}
		code := resp.GetDenyCode()
		if code == "" {
			code = "request_blocked"
		}
		msg := resp.GetDenyMessage()
		if msg == "" {
			msg = "request blocked by gateway policy"
		}
		return dec, &gwError{Status: status, Code: code, Message: msg, RecordType: errTypeBlockedByHook}
	}

	// ALLOW (or unspecified): apply patches, only on granted paths. The body
	// is final once an account is chosen (billing and routing were planned
	// from it), so an attempt hook may not patch it.
	body, perr := applyHookPatches(c.body, resp.GetPatches(), hb.GrantedFields)
	if perr == nil && ref != nil && len(resp.GetPatches()) > 0 {
		perr = errors.New("gateway.attempt hooks cannot patch the body")
	}
	if perr != nil {
		g.hooks.observe(k, hb.Hook.Point, outcomeError, latency)
		g.hooks.recordResult(ctx, k, true)
		slog.WarnContext(ctx, "gateway: hook patch rejected", "plugin", hb.Plugin.Key, "hook", id, "err", perr)
		return failed("patch rejected: " + perr.Error())
	}
	g.hooks.observe(k, hb.Hook.Point, outcomeAllow, latency)
	g.hooks.recordResult(ctx, k, false)
	if len(resp.GetPatches()) > 0 {
		c.setBody(body)
	}
	if ref == nil && resp.GetSkipAttemptHooks() {
		c.skipAttemptHooks(hb.Plugin.Key)
	}
	dec.Decision = decisionAllow
	dec.Note = truncateUTF8(resp.GetNote(), maxHookNoteBytes)
	return dec, nil
}

// applyHookPatches applies all patches or none.
func applyHookPatches(body []byte, patches []*pluginv1.BodyPatch, granted []string) ([]byte, error) {
	if len(patches) == 0 {
		return body, nil
	}
	allowed := make([]string, 0, len(granted))
	for _, f := range granted {
		if f != fieldPromptText && !isQueryField(f) {
			allowed = append(allowed, f)
		}
	}
	for _, p := range patches {
		if !pathCovered(p.GetPath(), allowed) {
			return nil, fmt.Errorf("path %q is outside the hook's granted fields", p.GetPath())
		}
	}
	return applyPatches(body, patches)
}

// isQueryField reports whether a granted field is a gjson query (modifiers,
// pipes, filters, wildcards, multipaths) rather than a plain dotted path.
// Such fields are read-only: they are handed to the hook but never allow a
// patch, since sjson cannot write through a query.
func isQueryField(f string) bool {
	return strings.ContainsAny(f, "|@#*?[]{}()=!<>%\"\\")
}

// applyPatches applies sjson set/delete instructions to a copy of body.
func applyPatches(body []byte, patches []*pluginv1.BodyPatch) ([]byte, error) {
	if len(patches) == 0 {
		return body, nil
	}
	out := append([]byte(nil), body...)
	var err error
	for _, p := range patches {
		path := p.GetPath()
		if path == "" {
			return nil, errors.New("empty patch path")
		}
		switch p.GetOp() {
		case pluginv1.BodyPatch_OP_DELETE:
			out, err = sjson.DeleteBytes(out, path)
		case pluginv1.BodyPatch_OP_SET:
			v := p.GetValueJson()
			// The shape scan comes first (gjson's validator recurses), and
			// the body stays free of repeated keys after a patch too, so a
			// later hook reads what the upstream will (CONTRACTS §25.9).
			s := scanJSONShape([]byte(v))
			if s.tooDeep || !gjson.Valid(v) {
				return nil, fmt.Errorf("patch %q: value_json is not valid JSON", path)
			}
			if s.dup {
				return nil, fmt.Errorf("patch %q: value_json repeats the object key %q", path, truncateUTF8(s.dupKey, maxDupKeyShown))
			}
			out, err = sjson.SetRawBytes(out, path, []byte(v))
		default:
			return nil, fmt.Errorf("patch %q: unknown op", path)
		}
		if err != nil {
			return nil, fmt.Errorf("patch %q: %w", path, err)
		}
	}
	return out, nil
}

// hookFields builds the granted field values: raw JSON for body paths and
// plain text for prompt_text.
func (c *call) hookFields(hb core.HookBinding) map[string]string {
	fields := make(map[string]string, len(hb.GrantedFields))
	for _, f := range hb.GrantedFields {
		if f == fieldPromptText {
			limit := hb.Hook.MaxPromptBytes
			if limit <= 0 {
				limit = defaultMaxPromptBytes
			}
			fields[f] = c.promptText(limit)
			continue
		}
		if r := gjson.GetBytes(c.body, f); r.Exists() {
			fields[f] = r.Raw
		}
	}
	return fields
}

// promptText extracts (once per body version and limit) the prompt text.
func (c *call) promptText(limit int) string {
	if c.promptCache == nil {
		c.promptCache = map[int]string{}
	}
	if s, ok := c.promptCache[limit]; ok {
		return s
	}
	s := extractPromptText(c.body, c.ep.Request.PromptTextPaths, limit)
	c.promptCache[limit] = s
	return s
}

// setBody replaces the request body and drops values derived from it.
func (c *call) setBody(b []byte) {
	c.body = b
	c.promptCache = nil
	// The model a platform plugin derived from the old body may be wrong for
	// the new one, so the next checkModel asks again (endpoints with
	// request.modelSource "plugin" only).
	c.modelResolved = false
	// The reference scans belong to the old bytes (body_state.go).
	c.bodyVersion++
	c.doc, c.docValid = gjson.Result{}, false
	c.routeUsage = nil
}
