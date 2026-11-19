package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"gorm.io/gorm"

	sdkegress "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/egress"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// memStore is an in-memory logStore.
type memStore struct {
	mu       sync.Mutex
	nextID   int64
	rows     map[int64]*logEntry
	closedAt map[int64]time.Time
	resets   []string // node ids passed to resetOpen
	opens    int      // insertOpen calls
	closes   int      // closeRows calls
	domains  map[domainKey]*domainDelta
	upserts  [][]domainDelta
	failDom  error
	pruned   []time.Time // before of each prune
}

func newMemStore() *memStore {
	return &memStore{rows: map[int64]*logEntry{}, closedAt: map[int64]time.Time{}, domains: map[domainKey]*domainDelta{}}
}

func (m *memStore) resetOpen(_ context.Context, nodeID string, _ time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets = append(m.resets, nodeID)
	return 0, nil
}

func (m *memStore) insertOpen(_ context.Context, rows []logEntry) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opens++
	ids := make([]int64, len(rows))
	for i, r := range rows {
		m.nextID++
		r := r
		m.rows[m.nextID] = &r
		ids[i] = m.nextID
	}
	return ids, nil
}

func (m *memStore) insertDone(_ context.Context, rows []logEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		m.nextID++
		r := r
		m.rows[m.nextID] = &r
		m.closedAt[m.nextID] = r.StartedAt.Add(time.Duration(r.DurationMS) * time.Millisecond)
	}
	return nil
}

func (m *memStore) closeRows(_ context.Context, rows []closedRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closes++
	for _, c := range rows {
		r := m.rows[c.ID]
		if r == nil || r.Result != ResultOpen {
			continue
		}
		r.Result, r.Error, r.DurationMS, r.BytesIn, r.BytesOut = c.Result, c.Error, c.DurationMS, c.BytesIn, c.BytesOut
		m.closedAt[c.ID] = c.ClosedAt
	}
	return nil
}

func (m *memStore) prune(_ context.Context, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruned = append(m.pruned, before)
	return nil
}

func (m *memStore) upsertDomains(ctx context.Context, ds []domainDelta, onNew func(context.Context, *gorm.DB, []domainDelta) error) error {
	m.mu.Lock()
	if m.failDom != nil {
		err := m.failDom
		m.mu.Unlock()
		return err
	}
	m.upserts = append(m.upserts, append([]domainDelta(nil), ds...))
	var news []domainDelta
	for _, d := range ds {
		k := domainKey{d.PluginKey, d.Host}
		if cur := m.domains[k]; cur != nil {
			cur.Connections += d.Connections
			cur.LastSeen = d.LastSeen
			continue
		}
		d := d
		m.domains[k] = &d
		news = append(news, d)
	}
	m.mu.Unlock()
	if len(news) > 0 && onNew != nil {
		return onNew(ctx, nil, news)
	}
	return nil
}

func (m *memStore) find(host, network string) []logEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []logEntry
	for _, r := range m.rows {
		if r.Host == host && r.Network == network {
			out = append(out, *r)
		}
	}
	return out
}

func (m *memStore) domain(host string) *domainDelta {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.domains[domainKey{"demo", host}]; d != nil {
		c := *d
		return &c
	}
	return nil
}

type memEvents struct {
	mu  sync.Mutex
	evs []core.Event
}

func (e *memEvents) Emit(_ context.Context, _ *gorm.DB, evs ...core.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evs = append(e.evs, evs...)
	return nil
}

func (e *memEvents) list() []core.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]core.Event(nil), e.evs...)
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func flushUntil(t *testing.T, e *env, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = e.p.Flush(context.Background())
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLongConnectionLoggedOpenThenClosed(t *testing.T) {
	st := newMemStore()
	e := newEnvStore(t, st, Options{})
	addr := echoTCP(t)
	_, port, _ := net.SplitHostPort(addr)

	c, err := sdkegress.DialContext(context.Background(), "tcp", "long.test:"+port)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("hello"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	// While the connection is open, its row is already visible.
	flushUntil(t, e, "open row", func() bool {
		rows := st.find("long.test", "tcp")
		return len(rows) == 1 && rows[0].Result == ResultOpen
	})
	if st.resets[0] != "node-1" {
		t.Fatalf("resetOpen at start: %v", st.resets)
	}

	_ = c.(interface{ CloseWrite() error }).CloseWrite()
	_, _ = io.ReadAll(c)
	_ = c.Close()
	flushUntil(t, e, "closed row", func() bool {
		rows := st.find("long.test", "tcp")
		return len(rows) == 1 && rows[0].Result == ResultOK
	})
	r := st.find("long.test", "tcp")[0]
	if r.BytesIn != 5 || r.BytesOut != 5 || r.NodeID != "node-1" || r.Port == 0 {
		t.Fatalf("closed row %+v", r)
	}
	st.mu.Lock()
	closes := st.closes
	var closedAt time.Time
	for id, row := range st.rows {
		if row.Host == "long.test" {
			closedAt = st.closedAt[id]
		}
	}
	st.mu.Unlock()
	if closes == 0 || closedAt.IsZero() {
		t.Fatal("close was not written as an update of the open row")
	}
}

func TestShortConnectionWrittenOnce(t *testing.T) {
	st := newMemStore()
	e := newEnvStore(t, st, Options{FlushInterval: time.Hour})
	addr := echoTCP(t)
	_, port, _ := net.SplitHostPort(addr)
	c, err := sdkegress.DialContext(context.Background(), "tcp", "short.test:"+port)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.(interface{ CloseWrite() error }).CloseWrite()
	_, _ = io.ReadAll(c)
	_ = c.Close()
	// Wait until the server side finished before the first flush, so open
	// and close land in the same batch.
	time.Sleep(200 * time.Millisecond)
	flushUntil(t, e, "row", func() bool { return len(st.find("short.test", "tcp")) == 1 })
	if r := st.find("short.test", "tcp")[0]; r.Result != ResultOK {
		t.Fatalf("row %+v", r)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.opens != 0 {
		t.Fatalf("open+close within one batch should be one insert (insertOpen calls %d)", st.opens)
	}
}

func TestOpenRowsResetOnShutdown(t *testing.T) {
	st := newMemStore()
	w := newLogWriter(st, Options{NodeID: "n", Logger: discardLogger(), FlushInterval: time.Hour})
	oc := w.open(logEntry{PluginKey: "demo", NodeID: "n", Network: "tcp", Host: "h", Port: 1, StartedAt: time.Now()})
	_ = oc
	if err := w.flushNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.shutdown()
	rows := st.find("h", "tcp")
	if len(rows) != 1 || rows[0].Result != ResultReset {
		t.Fatalf("rows after shutdown %+v", rows)
	}
}

// The log table is pruned by the cluster task, with the retention.
func TestLogsPruneTask(t *testing.T) {
	st := newMemStore()
	p := &Provider{logs: newLogWriter(st, Options{NodeID: "n", Logger: discardLogger(), FlushInterval: time.Hour, Retention: 48 * time.Hour})}
	defer p.logs.shutdown()
	spec := p.LogsPruneTaskSpec()
	if spec.Name != LogsPruneTask || spec.Scope != core.TaskCluster || spec.Every != time.Hour {
		t.Fatalf("prune task: %+v", spec)
	}
	if _, err := spec.Handler(context.Background(), core.TaskRun{Name: spec.Name}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.pruned) != 1 || time.Since(st.pruned[0]) < 47*time.Hour {
		t.Fatalf("pruned %v", st.pruned)
	}
}

func TestLogWriterFallbacks(t *testing.T) {
	// A close whose open row was never inserted is written as a full row.
	st := newMemStore()
	w := newLogWriter(st, Options{NodeID: "n", Logger: discardLogger(), FlushInterval: time.Hour})
	defer w.shutdown()
	orphan := &openConn{entry: logEntry{PluginKey: "demo", NodeID: "n", Network: "tcp", Host: "lost", Port: 2, StartedAt: time.Now()}}
	w.close(orphan, ResultReset, "boom", 1, 2, time.Now())
	if err := w.flushNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := st.find("lost", "tcp")
	if len(rows) != 1 || rows[0].Result != ResultReset || rows[0].BytesOut != 2 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestNewDomainDetection(t *testing.T) {
	st := newMemStore()
	events := &memEvents{}
	e := newEnvStore(t, st, Options{Events: events, DomainUpdateInterval: time.Hour})
	addr := echoTCP(t)
	_, port, _ := net.SplitHostPort(addr)
	dial := func(host string) {
		c, err := sdkegress.DialContext(context.Background(), "tcp", host+":"+port)
		if err != nil {
			return
		}
		_ = c.(interface{ CloseWrite() error }).CloseWrite()
		_, _ = io.ReadAll(c)
		_ = c.Close()
	}

	dial("API.New.test")
	flushUntil(t, e, "domain row", func() bool { return st.domain("api.new.test") != nil })
	evs := events.list()
	if len(evs) != 1 || evs[0].Type != core.EventPluginEgressNewDomain {
		t.Fatalf("events %+v", evs)
	}
	p := evs[0].Payload.(map[string]any)
	if p["plugin_key"] != "demo" || p["host"] != "api.new.test" || p["node_id"] != "node-1" || p["port"] == 0 || p["first_seen_at"] == nil {
		t.Fatalf("payload %+v", p)
	}

	// More connections to a known host are counted in memory and written
	// at most once per interval: no new upsert, no new event.
	st.mu.Lock()
	upserts := len(st.upserts)
	st.mu.Unlock()
	for i := 0; i < 3; i++ {
		dial("api.new.test")
	}
	time.Sleep(100 * time.Millisecond)
	_ = e.p.logs.flushDomains(false)
	st.mu.Lock()
	if len(st.upserts) != upserts {
		st.mu.Unlock()
		t.Fatal("known host written again within the update interval")
	}
	st.mu.Unlock()
	if n := len(events.list()); n != 1 {
		t.Fatalf("events after repeat: %d", n)
	}

	// Denied connections count too; DNS queries do not.
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowlist, Net: true})
	dial("blocked.test")
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowAll, Net: true})
	rawDNS(t, "resolved.test.", dnsmessage.TypeA)
	flushUntil(t, e, "blocked domain", func() bool { return st.domain("blocked.test") != nil })
	if st.domain(DNSHost) != nil || st.domain("resolved.test") != nil {
		t.Fatal("DNS queries recorded as domains")
	}
	// Flush (forced) writes the pending counters of the known host.
	if d := st.domain("api.new.test"); d == nil || d.Connections != 4 {
		t.Fatalf("known host counters %+v", d)
	}
	if n := len(events.list()); n != 2 {
		t.Fatalf("events: %d", n)
	}
}

// The database's virtual address is the core's own service: its connections
// are logged like any other but are no external host (no domain row, no
// new-domain event).
func TestDatabaseNotAnExternalHost(t *testing.T) {
	st := newMemStore()
	events := &memEvents{}
	proxy := func(_ context.Context, conn net.Conn, _ string) {
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}
	e := newEnvStore(t, st, Options{Events: events, DatabaseProxy: proxy})
	e.policy.set(core.EgressPolicy{Mode: PolicyAllowAll, Net: true, Database: true})
	c, err := sdkegress.DialContext(context.Background(), "tcp", net.JoinHostPort(DatabaseHost, "5432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	flushUntil(t, e, "database row", func() bool { return len(st.find(DatabaseHost, "tcp")) == 1 })
	if st.domain(DatabaseHost) != nil || len(events.list()) != 0 {
		t.Fatalf("database recorded as an external host: domain %+v, events %+v", st.domain(DatabaseHost), events.list())
	}
}

func TestDomainTracker(t *testing.T) {
	tr := newDomainTracker(time.Minute)
	t0 := time.Unix(1_700_000_000, 0)
	if !tr.observe("p", "a.com", 443, t0) {
		t.Fatal("first observation should ask for a flush")
	}
	tr.observe("p", "a.com", 443, t0.Add(time.Second))
	ds := tr.due(t0.Add(2*time.Second), false)
	if len(ds) != 1 || ds[0].Connections != 2 || !ds[0].FirstSeen.Equal(t0) || !ds[0].LastSeen.Equal(t0.Add(time.Second)) {
		t.Fatalf("due %+v", ds)
	}
	if tr.observe("p", "a.com", 443, t0.Add(3*time.Second)) {
		t.Fatal("known host asked for an immediate flush")
	}
	if ds := tr.due(t0.Add(30*time.Second), false); len(ds) != 0 {
		t.Fatalf("written again within the interval: %+v", ds)
	}
	if ds := tr.due(t0.Add(63*time.Second), false); len(ds) != 1 || ds[0].Connections != 1 {
		t.Fatalf("due after interval %+v", ds)
	}
	// A failed write is restored and retried at once.
	tr.observe("p", "a.com", 443, t0.Add(70*time.Second))
	ds = tr.due(t0.Add(200*time.Second), false)
	tr.restore(ds)
	if ds := tr.due(t0.Add(201*time.Second), false); len(ds) != 1 || ds[0].Connections != 1 {
		t.Fatalf("restored %+v", ds)
	}
	// Idle hosts are evicted.
	tr.evict(t0.Add(24 * time.Hour))
	if tr.size() != 0 {
		t.Fatal("idle host not evicted")
	}
}

func TestDomainWriteFailureRetried(t *testing.T) {
	st := newMemStore()
	st.failDom = errors.New("db down")
	w := newLogWriter(st, Options{NodeID: "n", Logger: discardLogger(), FlushInterval: time.Hour})
	defer w.shutdown()
	w.observe("demo", "x.com", 443)
	if err := w.flushNow(context.Background()); err == nil {
		t.Fatal("expected domain write error")
	}
	st.mu.Lock()
	st.failDom = nil
	st.mu.Unlock()
	if err := w.flushNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := st.domain("x.com"); d == nil || d.Connections != 1 {
		t.Fatalf("domain after retry %+v", d)
	}
}
