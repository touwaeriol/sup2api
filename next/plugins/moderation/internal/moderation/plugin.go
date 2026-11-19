// Package moderation implements the prompt moderation plugin (CONTRACTS §20,
// 0.2 per §72): a gateway.request hook that runs the flow bound to the
// request's group. Flows are stages of auditors (OpenAI moderation, TypeSafe
// Jev, LLM verdict) on configured channels. Users on the watch list at the
// enforce level (or everyone in enforce mode) are moderated before the
// upstream call and refused on a hit; the others pass at once and are
// moderated in the background. Suspicious users join the watch list
// automatically, and repeated violations may ban them.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/batch"
)

// Plugin is the moderation plugin. It implements pluginsdk.Hook,
// pluginsdk.JobRunner, pluginsdk.HTTP, pluginsdk.BroadcastHandler and the
// lifecycle interfaces.
type Plugin struct {
	*pluginsdk.Router
	*pluginsdk.BroadcastMux

	host       pluginsdk.Host
	log        *slog.Logger
	now        func() time.Time
	cfg        atomic.Pointer[config]
	httpClient *http.Client
	proxies    proxyClients

	agent *agent
	cache atomic.Pointer[lru]
	// kvMu: this node's changes of verdict cache keys in the KV take turns
	// (kvLock). A lock is held while its channel holds a value, so that
	// waiting for one can end with a context.
	kvMu   [kvLockStripes]chan struct{}
	sf     singleflight.Group
	sem    atomic.Pointer[semaphore] // background (post) calls
	preSem atomic.Pointer[semaphore] // calls a request waits for

	snap   atomic.Pointer[snapshot]
	snapMu sync.Mutex
	// attemptHook: the core calls the gateway.attempt hook (granted), so
	// account bindings apply; attempts tracks the requests left to it.
	attemptHook atomic.Bool
	attempts    *attemptRuns

	watch        atomic.Pointer[map[int64]watchEntry]
	watchMu      sync.Mutex
	watchVersion atomic.Int64

	// upgraded: the one-time steps of the 0.11.0 upgrade ran (upgradeOnce).
	upgraded atomic.Bool
	// parityDone: the one-time 0.8.0 conversion of the watch list ran
	// (convertParityOnce).
	parityDone atomic.Bool
	// parityEnd: when the conversion stops looking for entries added by
	// 0.7.x nodes (unix nanoseconds; read once parityDone is set).
	parityEnd atomic.Int64
	// configured: Configure ran at least once, so the one-time conversions
	// see the real settings and not the defaults.
	configured atomic.Bool

	// Post queue: workers = max_concurrency, capacity = queue_size.
	// poolMu also guards closed (Shutdown began: no new tracked goroutines)
	// and eventsStopped (the event writer is stopped: finish stores
	// suspicious records itself).
	poolMu sync.RWMutex
	// queuedBytes is what the queued requests hold; enqueue refuses beyond
	// maxQueueBytes so a burst of image requests cannot exhaust memory.
	queuedBytes atomic.Int64
	// inflightBytes is what the hook requests in progress hold; the hook
	// answers at once beyond max_inflight_mb (admitBytes, CONTRACTS §72.5).
	inflightBytes atomic.Int64
	pool          *workerPool
	closed        bool
	eventsStopped bool
	// postCtx runs the post jobs; Shutdown cancels it once the queue had
	// shutdownDrain to finish (CONTRACTS §72.5 step 9).
	postCtx    context.Context
	postCancel context.CancelFunc

	eventWriter *batch.Writer[eventRec]
	// eventFlushEvery is the time limit before the event writer flushes;
	// zero is the SDK's default (batch.DefaultInterval). Set before Init.
	eventFlushEvery time.Duration

	hourly     *statsAcc
	categories categoryCache
	staff      staffCache
	priceMu    sync.Mutex
	priceCache map[string]priceStateEntry

	// blocked maps user id -> expiry (zero = until unblocked).
	blocked        atomic.Pointer[map[int64]time.Time]
	blockReloadMu  sync.Mutex
	blockReloadSeq uint64
	refreshEvery   time.Duration
	refreshNow     chan struct{}

	notices chan notice

	stats struct {
		calls, errors, cacheHits, dropped, droppedEvents, kvErrors atomic.Int64
		inflight, latencyMsSum                                     atomic.Int64
		droppedNotices, noticeErrors                               atomic.Int64
		busy, shutdownDropped, suspiciousRetries                   atomic.Int64
		// imagesRefused: the auditor calls whose images the upstream refused
		// (Outcome.ImagesRefused).
		imagesRefused atomic.Int64
		// preWaiting: the requests waiting for their run (runPre);
		// kvWriting: the runs whose verdicts are on their way to the KV
		// (Runtime.KVWriting).
		preWaiting, kvWriting atomic.Int64
		// kvSkipped: the runs whose verdicts were not written
		// (Runtime.KVSkipped).
		kvSkipped atomic.Int64
	}

	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns the plugin.
func New() *Plugin {
	p := &Plugin{
		Router:       pluginsdk.NewRouter(),
		BroadcastMux: pluginsdk.NewBroadcastMux(),
		log:          slog.Default(),
		now:          time.Now,
		httpClient:   newHTTPClient(),
		agent:        newAgent(),
		refreshEvery: 5 * time.Second,
		refreshNow:   make(chan struct{}, 1),
		hourly:       newStatsAcc(),
		notices:      make(chan notice, 256),
		priceCache:   map[string]priceStateEntry{},
	}
	for i := range p.kvMu {
		p.kvMu[i] = make(chan struct{}, 1)
	}
	p.attempts = newAttemptRuns(func() time.Time { return p.now() })
	p.bg, p.cancel = context.WithCancel(context.Background())
	p.postCtx, p.postCancel = context.WithCancel(context.Background())
	c := compile(DefaultSettings())
	p.cfg.Store(c)
	p.cache.Store(newLRU(c.CacheEntries))
	p.sem.Store(newSemaphore(c.MaxConcurrency))
	p.preSem.Store(newSemaphore(preSlots(c.MaxConcurrency)))
	empty := map[int64]time.Time{}
	p.blocked.Store(&empty)
	noWatch := map[int64]watchEntry{}
	p.watch.Store(&noWatch)
	p.watchVersion.Store(-1)
	p.snap.Store(emptySnapshot())
	p.routes()
	p.OnTopic(TopicBlocksChanged, p.onBroadcast(p.reloadBlocks))
	p.OnTopic(TopicConfigChanged, p.onBroadcast(p.reloadConfig))
	p.OnTopic(TopicWatchChanged, p.onBroadcast(p.reloadWatch))
	p.OnTopic(TopicVerdictChanged, p.onVerdictChanged)
	return p
}

// Init implements pluginsdk.Initializer: starts the event writer, the
// refresher and the post workers.
func (p *Plugin) Init(_ context.Context, h pluginsdk.Host) error {
	p.host = h
	p.log = h.Logger()
	jevLogger.Store(p.log)
	// A batch that fails is dropped; drops are counted here and by finish
	// (the writer's own drop count would also see a full queue whose
	// suspicious record finish then writes itself). Only Shutdown stops it,
	// after the workers and hooks it waits for have sent their records.
	p.eventWriter = batch.New(context.WithoutCancel(p.bg), 4096, func(ctx context.Context, evs []eventRec) error {
		err := p.storeEvents(ctx, evs)
		if err == nil {
			return nil
		}
		p.log.Warn("moderation: write events failed", "error", err.Error(), "events", len(evs))
		kept := p.retrySuspicious(ctx, evs)
		p.stats.droppedEvents.Add(int64(len(evs) - kept))
		return nil
	}, nil, batch.WithInterval(p.eventFlushEvery))
	p.wg.Add(3)
	go p.refreshLoop(p.bg)
	go p.statsLoop(p.bg)
	go p.noticeSender(p.bg)
	c := p.cfg.Load()
	p.resizePool(c.MaxConcurrency, c.QueueSize)
	p.kickRefresh()
	return nil
}

// Configure implements pluginsdk.Configurer. Saved settings passed
// ValidateSettings; stored settings it would refuse now (retired keys,
// values of older bounds) are applied as DecodeSettings reads them, odd
// values clamped, and what CheckSettings refuses is logged (parseSettings).
func (p *Plugin) Configure(_ context.Context, cfg pluginsdk.Config) error {
	s, refused, err := parseSettings(cfg.JSON)
	if err != nil {
		return pluginsdk.FieldErrors{}.Add("", "invalid_json", "settings must be a JSON object / 设置必须是 JSON 对象").Err()
	}
	if len(refused) > 0 {
		p.log.Warn("moderation: stored settings the settings check refuses are applied as read", "error", refused.Error())
	}
	c := compile(s)
	c.settingsHash = settingsDigest(cfg.JSON)
	p.attemptHook.Store(attemptHookGranted(cfg))
	old := p.cfg.Swap(c)
	if old == nil || old.MaxConcurrency != c.MaxConcurrency {
		p.sem.Store(newSemaphore(c.MaxConcurrency))
		p.preSem.Store(newSemaphore(preSlots(c.MaxConcurrency)))
	}
	if old == nil || old.CacheEntries != c.CacheEntries {
		p.cache.Store(newLRU(c.CacheEntries))
	}
	if p.host != nil {
		p.resizePool(c.MaxConcurrency, c.QueueSize)
		// The Jev default set is compiled into the auditors.
		if old != nil && RulesVersionOf(&old.jevDefault) != RulesVersionOf(&c.jevDefault) {
			go func() {
				ctx, cancel := context.WithTimeout(p.bg, 10*time.Second)
				defer cancel()
				_ = p.reloadConfig(ctx)
			}()
		}
		// A shortened retention applies at once (new-api schedules a cleanup
		// on save). Every node gets the change; the node that takes the
		// purge lock deletes, in batches (purgeRetention).
		if old != nil && p.configured.Load() && c.retentionCut(old) {
			go func() {
				ctx, cancel := context.WithTimeout(p.bg, 10*time.Minute)
				defer cancel()
				db, err := p.db(ctx)
				if err == nil {
					_, err = p.purgeRetention(ctx, db, p.now().UTC())
				}
				if err != nil && p.bg.Err() == nil {
					p.log.Warn("moderation: retention cleanup failed", "error", err.Error())
				}
			}()
		}
	}
	if !p.configured.Swap(true) || (c.enabled && (old == nil || !old.enabled)) {
		p.kickRefresh()
	}
	return nil
}

// Health implements pluginsdk.HealthChecker.
func (p *Plugin) Health(context.Context) (*pluginv1.HealthResponse, error) {
	qlen, _ := p.queueLen()
	return &pluginv1.HealthResponse{Healthy: true, Metrics: map[string]float64{
		"calls":            float64(p.stats.calls.Load()),
		"errors":           float64(p.stats.errors.Load()),
		"queue":            float64(qlen),
		"dropped":          float64(p.stats.dropped.Load()),
		"dropped_events":   float64(p.stats.droppedEvents.Load()),
		"cache_hits":       float64(p.stats.cacheHits.Load()),
		"inflight":         float64(p.stats.inflight.Load()),
		"inflight_bytes":   float64(p.inflightBytes.Load()),
		"kv_errors":        float64(p.stats.kvErrors.Load()),
		"kv_writing":       float64(p.stats.kvWriting.Load()),
		"kv_skipped":       float64(p.stats.kvSkipped.Load()),
		"redraw_panics":    float64(jevRedrawPanics.Load()),
		"connect_retries":  float64(connectRetries.Load()),
		"busy":             float64(p.stats.busy.Load()),
		"shutdown_dropped": float64(p.stats.shutdownDropped.Load()),
		"blocked_users":    float64(p.blockedCount()),
		"watched_users":    float64(len(*p.watch.Load())),
	}}, nil
}

// shutdownDrain is how long Shutdown lets the post workers work through the
// queue before the remaining jobs are dropped (and counted).
var shutdownDrain = 5 * time.Second

// Shutdown implements pluginsdk.Shutdowner: the post workers get
// shutdownDrain to finish the queued jobs (no new ones are taken), then the
// rest are dropped and counted; it waits for the tracked goroutines
// (goTracked), then flushes the pending event records. A record concluded
// after that is stored by finish itself.
func (p *Plugin) Shutdown(ctx context.Context) error {
	p.cancel()
	p.poolMu.Lock()
	p.closed = true
	var pool *workerPool
	pool, p.pool = p.pool, nil
	p.poolMu.Unlock()
	queued := 0
	if pool != nil {
		queued = len(pool.ch)
	}
	drained := make(chan struct{})
	go func() {
		if pool != nil {
			close(pool.ch)
			pool.wg.Wait()
		}
		close(drained)
	}()
	var err error
	drain := time.NewTimer(shutdownDrain)
	defer drain.Stop()
	select {
	case <-drained:
	case <-ctx.Done():
	case <-drain.C:
	}
	p.postCancel() // the jobs still queued are dropped (postWorker counts them)
	done := make(chan struct{})
	go func() {
		<-drained
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		p.flushStats(ctx)
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(5 * time.Second):
		err = errors.New("moderation: workers did not stop in time")
	}
	if n := p.stats.shutdownDropped.Load(); n > 0 || queued > 0 {
		p.log.Warn("moderation: shutdown", "queued_jobs", queued, "dropped_jobs", n)
	}
	p.poolMu.Lock()
	p.eventsStopped = true
	p.poolMu.Unlock()
	return errors.Join(err, p.eventWriter.Stop())
}

// recoverTracked keeps a panic in a background goroutine from ending the
// plugin process.
func (p *Plugin) recoverTracked() {
	if r := recover(); r != nil {
		p.log.Error("moderation: background task panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	}
}

// goTracked runs fn in a goroutine Shutdown waits for before it stops the
// event writer. Once Shutdown began, fn runs untracked.
func (p *Plugin) goTracked(fn func()) {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()
	if p.closed {
		go func() {
			defer p.recoverTracked()
			fn()
		}()
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer p.recoverTracked()
		fn()
	}()
}

// sendEvent queues a record for the event writer; false when the queue is
// full or the writer is stopped.
func (p *Plugin) sendEvent(rec eventRec) bool {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()
	return !p.eventsStopped && p.eventWriter.Send(rec)
}

// errDBUnavailable marks a database outage; HandleHTTP answers it with 503.
var errDBUnavailable = errors.New("moderation: database unavailable")

// HandleHTTP serves the routes, answering a database outage with 503.
func (p *Plugin) HandleHTTP(ctx context.Context, req *pluginv1.HTTPRequest) (*pluginv1.HTTPResponse, error) {
	resp, err := p.Router.HandleHTTP(ctx, req)
	if errors.Is(err, errDBUnavailable) {
		return pluginsdk.Unavailable(err.Error()), nil
	}
	return resp, err
}

// db returns the plugin database handle, bound to ctx.
func (p *Plugin) db(ctx context.Context) (*gorm.DB, error) {
	if p.host == nil {
		return nil, errors.New("moderation: host not initialised")
	}
	db, err := p.host.DB(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDBUnavailable, err)
	}
	return db, nil
}

// ---------------------------------------------------------------- concurrency

// maxQueueBytes caps the bytes held by queued requests (per node).
const maxQueueBytes = 96 << 20

// preSlots is the number of concurrent calls requests may wait for.
func preSlots(maxConcurrency int) int { return max(64, 4*maxConcurrency) }

// semaphore bounds concurrent upstream calls (max_concurrency).
type semaphore struct{ ch chan struct{} }

func newSemaphore(n int) *semaphore { return &semaphore{ch: make(chan struct{}, n)} }

// acquire waits for a slot until ctx is done.
func (p *Plugin) acquire(ctx context.Context, pre bool) (func(), error) {
	s := p.sem.Load()
	if pre {
		s = p.preSem.Load()
	}
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for a free moderation slot: %w", ctx.Err())
	}
}

// ---------------------------------------------------------------- post queue

type workerPool struct {
	ch      chan *moderation
	workers int
	wg      sync.WaitGroup
}

// resizePool replaces the post pool when its size changed. The old workers
// finish the jobs already queued and exit.
func (p *Plugin) resizePool(workers, size int) {
	p.poolMu.Lock()
	if p.closed || (p.pool != nil && p.pool.workers == workers && cap(p.pool.ch) == size) {
		p.poolMu.Unlock()
		return
	}
	np := &workerPool{ch: make(chan *moderation, size), workers: workers}
	np.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.postWorker(np)
	}
	old := p.pool
	p.pool = np
	p.poolMu.Unlock()
	if old != nil {
		close(old.ch)
	}
}

// enqueue adds a post job; false when the queue is full.
func (p *Plugin) enqueue(m *moderation) bool {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()
	if p.pool == nil || p.closed {
		return false
	}
	if p.queuedBytes.Add(m.size) > maxQueueBytes {
		p.queuedBytes.Add(-m.size)
		return false
	}
	select {
	case p.pool.ch <- m:
		return true
	default:
		p.queuedBytes.Add(-m.size)
		return false
	}
}

func (p *Plugin) queueLen() (length, capacity int) {
	p.poolMu.RLock()
	defer p.poolMu.RUnlock()
	if p.pool == nil {
		return 0, 0
	}
	return len(p.pool.ch), cap(p.pool.ch)
}

func (p *Plugin) postWorker(pool *workerPool) {
	defer pool.wg.Done()
	for m := range pool.ch {
		if p.postCtx.Err() == nil {
			p.runPostSafely(m)
		} else {
			p.stats.shutdownDropped.Add(1)
		}
		p.queuedBytes.Add(-m.size)
	}
}

// runPostSafely runs one queued job and keeps a panic in it from ending the
// plugin process: nothing else recovers in a worker goroutine. (On the pre
// path the SDK recovers a panic of the hook itself; the shared run, which
// singleflight executes, recovers its own - runPre.)
func (p *Plugin) runPostSafely(m *moderation) {
	defer func() {
		if r := recover(); r != nil {
			p.postPanicked(m, r)
		}
	}()
	p.runPost(m)
}

// postPanicked concludes a queued job that panicked with r: it is counted
// and recorded as a run that errored, like a run in which nothing judged.
// What the job had counted before the panic is not counted again: the run
// (m.counted), and the request on its user's day (m.userCounted) - then the
// panic came from writing the record, the statistics already have the job
// with its real result, and it is only logged. A panic while the error is
// recorded is logged as well.
func (p *Plugin) postPanicked(m *moderation, r any) {
	p.log.Error("moderation: post run panicked", "request_id", m.rec.RequestID, "counted", m.counted, "user_counted", m.userCounted,
		"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	if m.userCounted {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("moderation: recording the panicked post run failed", "request_id", m.rec.RequestID,
				"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	tr := panickedRun()
	if !m.counted {
		p.countRun(m, tr)
	}
	p.finish(m, tr, false)
}

// Background store of suspicious records the event writer could not take
// (CONTRACTS §72.5 step 10): the hook never waits for it. Each record is
// tried suspiciousAttempts times with growing pauses.
var (
	suspiciousAttempts = 4
	suspiciousPause    = time.Second
)

func (p *Plugin) storeSuspicious(rec eventRec) {
	p.goTracked(func() {
		pause := suspiciousPause
		var err error
		for attempt := 1; attempt <= suspiciousAttempts; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = p.storeEvents(ctx, []eventRec{rec})
			cancel()
			if err == nil {
				return
			}
			p.stats.suspiciousRetries.Add(1)
			if attempt < suspiciousAttempts {
				time.Sleep(pause)
				pause *= 2
			}
		}
		p.stats.droppedEvents.Add(1)
		p.log.Warn("moderation: suspicious record lost", "user_id", rec.UserID, "request_id", rec.RequestID, "error", err.Error())
	})
}
