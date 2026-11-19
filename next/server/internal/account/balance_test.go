package account

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// fakeBalance is the plugin side of a balance query. BuildBalanceRequest
// builds one GET per url: the first carries the account's key as x-api-key,
// the others no credential (a public endpoint). ParseBalanceResponse
// records the answers and answers result when set, else reads the first
// answer: 200 {"micros": n} is n micro-USD, 401 auth_rejected, anything
// else transient.
type fakeBalance struct {
	urls   []string
	result *pluginv1.BalanceResult
	// gate, when set, holds BuildBalanceRequest until closed; entered
	// receives one value per call first.
	gate, entered chan struct{}

	mu      sync.Mutex
	builds  int
	answers [][]*pluginv1.BalanceAnswer
}

func (p *fakePlatform) BuildBalanceRequest(_ context.Context, in *pluginv1.BuildBalanceRequestRequest) (*pluginv1.BuildBalanceRequestResponse, error) {
	b := p.balance
	if b == nil {
		return nil, status.Error(codes.Unimplemented, "not implemented")
	}
	b.mu.Lock()
	b.builds++
	b.mu.Unlock()
	if b.gate != nil {
		b.entered <- struct{}{}
		<-b.gate
	}
	out := &pluginv1.BuildBalanceRequestResponse{}
	for i, u := range b.urls {
		r := &pluginv1.BalanceRequest{Url: u}
		if i == 0 {
			r.Headers = map[string]string{"x-api-key": gjson.Get(in.Account.CredentialsJson, "api_key").String()}
		}
		out.Requests = append(out.Requests, r)
	}
	return out, nil
}

func (p *fakePlatform) ParseBalanceResponse(_ context.Context, in *pluginv1.ParseBalanceResponseRequest) (*pluginv1.ParseBalanceResponseResponse, error) {
	b := p.balance
	if b == nil {
		return nil, status.Error(codes.Unimplemented, "not implemented")
	}
	b.mu.Lock()
	b.answers = append(b.answers, in.GetAnswers())
	res := b.result
	b.mu.Unlock()
	if res != nil {
		return &pluginv1.ParseBalanceResponseResponse{Result: res}, nil
	}
	a := in.GetAnswers()[0]
	switch a.GetStatus() {
	case http.StatusOK:
		res = &pluginv1.BalanceResult{AmountMicros: gjson.GetBytes(a.GetBody(), "micros").Int(), Currency: "USD"}
	case http.StatusUnauthorized:
		res = &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_AUTH_REJECTED, ErrorMessage: "key revoked"}
	default:
		res = &pluginv1.BalanceResult{ErrorType: pluginv1.BalanceResult_ERROR_TYPE_TRANSIENT,
			ErrorMessage: fmt.Sprintf("status %d %s", a.GetStatus(), a.GetTransportError())}
	}
	return &pluginv1.ParseBalanceResponseResponse{Result: res}, nil
}

func (b *fakeBalance) set(res *pluginv1.BalanceResult) {
	b.mu.Lock()
	b.result = res
	b.mu.Unlock()
}

func (b *fakeBalance) calls() (builds int, answers [][]*pluginv1.BalanceAnswer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builds, b.answers
}

// balanceUpstream answers /balance with {"micros": micros} (status, when
// set, instead), /rate with {"rate": 1}, /moved with a redirect to /balance;
// it records "path key=<x-api-key>" per request.
type balanceUpstream struct {
	srv    *httptest.Server
	micros atomic.Int64
	status atomic.Int32
	mu     sync.Mutex
	hits   []string
}

func newBalanceUpstream(t *testing.T) *balanceUpstream {
	u := &balanceUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.hits = append(u.hits, r.URL.Path+" key="+r.Header.Get("x-api-key"))
		u.mu.Unlock()
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "/balance", http.StatusFound)
		case "/rate":
			_, _ = w.Write([]byte(`{"rate":1}`))
		default:
			if s := u.status.Load(); s != 0 {
				w.WriteHeader(int(s))
				return
			}
			_, _ = fmt.Fprintf(w, `{"micros":%d}`, u.micros.Load())
		}
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *balanceUpstream) requests() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.hits...)
}

// withBalance makes anthropic/apikey declare decl, read by b, and lets its
// manifest reach the account's own base_url (upstreamFields), as a relay
// site's plugin does.
func (e *env) withBalance(b *fakeBalance, decl *manifest.AccountBalance) {
	at := &e.gen.types[0]
	at.Type.Balance = decl
	at.Type.UpstreamFields = []string{"base_url"}
	at.Plugin.Manifest = &manifest.Manifest{Key: "anthropic", AccountTypes: []manifest.AccountType{at.Type}}
	e.plat.balance = b
}

// mkBalanceAccount is an anthropic/apikey account on base, enabled and
// schedulable or disabled.
func (e *env) mkBalanceAccount(name, base string, enabled bool) int64 {
	e.t.Helper()
	enc, err := e.svc.d.Cipher.Encrypt([]byte(`{"api_key":"sk-good-key-123"}`), aad("anthropic"))
	if err != nil {
		e.t.Fatal(err)
	}
	status := "active"
	if !enabled {
		status = "disabled"
	}
	return e.exec1(`INSERT INTO accounts (name, plugin_key, type, credentials_enc, settings, status, schedulable)
		VALUES ($1, 'anthropic', 'apikey', $2, jsonb_build_object('base_url', $3::text), $4, $5) RETURNING id`,
		name, enc, base, status, enabled)
}

// balanceIn is the balance of account id in the account list, nil when it
// has none.
func (e *env) balanceIn(id int64) map[string]any {
	e.t.Helper()
	code, out := e.do("GET", "/accounts", nil)
	if code != 200 {
		e.t.Fatalf("list: %d %v", code, out)
	}
	for _, it := range out["data"].([]any) {
		if a := it.(map[string]any); int64(a["id"].(float64)) == id {
			b, _ := a["balance"].(map[string]any)
			return b
		}
	}
	e.t.Fatalf("account %d not listed", id)
	return nil
}

func (e *env) getBalance(id int64, force bool) (int, map[string]any) {
	e.t.Helper()
	code, out := e.do("GET", fmt.Sprintf("/accounts/%d/balance?force=%t", id, force), nil)
	return code, data(out)
}

// keys renders the fields of a view as "<count> <sorted names>".
func keys(m map[string]any) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return fmt.Sprint(len(ks), " ", strings.Join(ks, ","))
}

// The balance of an account: read through the plugin when due or forced,
// stored, shown in the account views; a failure keeps the last balance; an
// unlimited key shows no amount; the unit is the plugin's as it is, none
// or any text; a rejected key changes nothing on the account.
func TestAccountBalanceAPI(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	fb := &fakeBalance{urls: []string{up.srv.URL + "/balance"}}
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	relay := e.mkRawAccount("relay", "relay_key", "relay", "sk-good-key-123", true)

	// A type without balance: 404, no balance in the views.
	if code, _ := e.getBalance(id, false); code != 404 {
		t.Fatalf("undeclared: %d", code)
	}
	e.withBalance(fb, &manifest.AccountBalance{})
	if b := e.balanceIn(id); keys(b) != "1 supported" || b["supported"] != true {
		t.Fatalf("never read: %v", b)
	}
	if b := e.balanceIn(relay); b != nil {
		t.Fatalf("relay: %v", b)
	}
	if n := len(up.requests()); n != 0 {
		t.Fatalf("the list read the upstream: %d", n)
	}

	// No snapshot: the first read goes out, with the key.
	up.micros.Store(12_500_000)
	code, d := e.getBalance(id, false)
	if code != 200 || keys(d) != "4 amount,currency,supported,updated_at" || d["amount"] != "12.500000" || d["currency"] != "USD" {
		t.Fatalf("first read: %d %v", code, d)
	}
	if got := strings.Join(up.requests(), ";"); got != "/balance key=sk-good-key-123" {
		t.Fatalf("requests: %s", got)
	}
	// Within the interval (default 180 s) a read serves the snapshot; a
	// forced one goes out.
	up.micros.Store(9_000_000)
	if _, d = e.getBalance(id, false); d["amount"] != "12.500000" || len(up.requests()) != 1 {
		t.Fatalf("not due: %v %d", d, len(up.requests()))
	}
	if _, d = e.getBalance(id, true); d["amount"] != "9.000000" || len(up.requests()) != 2 {
		t.Fatalf("forced: %v", d)
	}
	readAt := d["updated_at"]

	// A failure keeps the last balance and its time.
	up.status.Store(503)
	if _, d = e.getBalance(id, true); d["amount"] != "9.000000" || d["updated_at"] != readAt ||
		!strings.HasPrefix(fmt.Sprint(d["error"]), "transient: status 503") || d["error_at"] == nil {
		t.Fatalf("failure: %v", d)
	}
	if b := e.balanceIn(id); b["error"] != d["error"] || b["amount"] != "9.000000" {
		t.Fatalf("list after failure: %v", b)
	}
	// A rejected key is recorded only: the account stays enabled,
	// schedulable and without a cooldown.
	up.status.Store(401)
	if _, d = e.getBalance(id, true); d["error"] != "auth_rejected: key revoked" {
		t.Fatalf("rejected: %v", d)
	}
	var status string
	var sched bool
	if err := e.db.SQL.QueryRowContext(t.Context(), `SELECT status, schedulable FROM accounts WHERE id = $1`, id).Scan(&status, &sched); err != nil {
		t.Fatal(err)
	}
	if status != "active" || !sched || e.mr.Exists(cooldownKey(id)) {
		t.Fatalf("a rejected balance key touched the account: %s %v", status, sched)
	}
	// A success clears the error; unlimited shows no amount.
	fb.set(&pluginv1.BalanceResult{Unlimited: true, AmountMicros: 5, Currency: "bogus"})
	if _, d = e.getBalance(id, true); keys(d) != "3 supported,unlimited,updated_at" || d["unlimited"] != true {
		t.Fatalf("unlimited: %v", d)
	}
	// No unit: the number alone; any unit is kept as it is (CONTRACTS §52).
	fb.set(&pluginv1.BalanceResult{AmountMicros: 1_250_000_000_000})
	if _, d = e.getBalance(id, true); keys(d) != "3 amount,supported,updated_at" || d["amount"] != "1250000.000000" {
		t.Fatalf("no unit: %v", d)
	}
	fb.set(&pluginv1.BalanceResult{AmountMicros: 7_500_000, Currency: "usd credits / 额度"})
	if _, d = e.getBalance(id, true); d["amount"] != "7.500000" || d["currency"] != "usd credits / 额度" || d["error"] != nil {
		t.Fatalf("a unit as it is: %v", d)
	}
	if b := e.balanceIn(id); b["currency"] != "usd credits / 额度" {
		t.Fatalf("list, a unit as it is: %v", b)
	}
	fb.set(&pluginv1.BalanceResult{AmountMicros: -1_250_000, Currency: "CNY"})
	if _, d = e.getBalance(id, true); d["amount"] != "-1.250000" || d["currency"] != "CNY" || d["unlimited"] != nil {
		t.Fatalf("negative: %v", d)
	}
}

// A first read that fails is stored without an amount (it used to break the
// NOT NULL updated_at and be lost); the console shows the failure.
func TestAccountBalanceFirstFailure(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	e.withBalance(&fakeBalance{urls: []string{up.srv.URL + "/balance"}}, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	up.status.Store(500)
	code, d := e.getBalance(id, false)
	if code != 200 || keys(d) != "3 error,error_at,supported" || d["error"] != "transient: status 500" {
		t.Fatalf("first failure: %d %v", code, d)
	}
	snap, err := e.db.AccountBalance(t.Context(), id)
	if err != nil || snap == nil || snap.UpdatedAt != nil || snap.ErrorAt == nil {
		t.Fatalf("stored: %+v %v", snap, err)
	}
	if b := e.balanceIn(id); b["error"] != "transient: status 500" || b["amount"] != nil {
		t.Fatalf("list: %v", b)
	}
	// Not due again within the interval, failed or not.
	if _, d = e.getBalance(id, false); len(up.requests()) != 1 {
		t.Fatalf("failure retried at once: %v", up.requests())
	}
}

// Every request of a query is sent, in order, even after one failed: the
// plugin gets one answer per request. The second request carries no
// credential (the core adds none), and a redirect is answered as it is.
func TestBalanceRequestsAreAllSentWithoutRedirects(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	fb := &fakeBalance{urls: []string{up.srv.URL + "/balance", up.srv.URL + "/rate"}}
	e.withBalance(fb, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	up.status.Store(502)
	fb.set(&pluginv1.BalanceResult{AmountMicros: 1, Currency: "USD"})
	if _, d := e.getBalance(id, true); d["amount"] != "0.000001" {
		t.Fatalf("read: %v", d)
	}
	if got := strings.Join(up.requests(), ";"); got != "/balance key=sk-good-key-123;/rate key=" {
		t.Fatalf("requests: %s", got)
	}
	_, answers := fb.calls()
	if len(answers) != 1 || len(answers[0]) != 2 || answers[0][0].GetStatus() != 502 || answers[0][1].GetStatus() != 200 ||
		string(answers[0][1].GetBody()) != `{"rate":1}` {
		t.Fatalf("answers: %v", answers)
	}

	// A redirect is handed over as it is, and a transport failure is an
	// answer too.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	fb.urls = []string{up.srv.URL + "/moved", gone.URL + "/x", up.srv.URL + "/rate"}
	e.getBalance(id, true)
	_, answers = fb.calls()
	last := answers[len(answers)-1]
	if len(last) != 3 || last[0].GetStatus() != http.StatusFound || last[0].GetHeaders()["location"] != "/balance" ||
		last[1].GetStatus() != 0 || last[1].GetTransportError() == "" || last[2].GetStatus() != 200 {
		t.Fatalf("answers: %v", last)
	}
	if got := up.requests(); got[len(got)-2] != "/moved key=sk-good-key-123" || got[len(got)-1] != "/rate key=" {
		t.Fatalf("the redirect was followed: %v", got)
	}
}

// Every target passes the manifest's upstream host rules and the
// administrator's plugin network access before anything is sent: one
// refused target sends none of the requests, and the plugin is not asked
// to read anything.
func TestBalanceHostChecksSendNothing(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	other := strings.Replace(up.srv.URL, "127.0.0.1", "localhost", 1)
	fb := &fakeBalance{urls: []string{up.srv.URL + "/balance", other + "/rate"}}
	e.withBalance(fb, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	// localhost is not the account's upstream address.
	_, d := e.getBalance(id, true)
	if msg := fmt.Sprint(d["error"]); !strings.HasPrefix(msg, "transient: plugin anthropic: host \"localhost\" is neither") {
		t.Fatalf("host rule: %v", d)
	}
	// The administrator's plugin network access.
	fb.urls = []string{up.srv.URL + "/balance"}
	blocked := &blockedHosts{hosts: []string{"127.0.0.1"}}
	e.svc.d.NetworkAccess = blocked
	if _, d = e.getBalance(id, true); d["error"] != "transient: plugin anthropic: "+blockedMsg {
		t.Fatalf("network access: %v", d)
	}
	if got := up.requests(); len(got) != 0 {
		t.Fatalf("sent: %v", got)
	}
	if _, answers := fb.calls(); len(answers) != 0 {
		t.Fatalf("the plugin read refused requests: %v", answers)
	}
	// Allowed again.
	blocked.mu.Lock()
	blocked.hosts = nil
	blocked.mu.Unlock()
	if _, d = e.getBalance(id, true); d["amount"] != "0.000000" || d["error"] != nil {
		t.Fatalf("allowed: %v", d)
	}
}

// The requests leave through the account's proxy; with a disabled proxy
// nothing is built or sent, never a direct connection.
func TestBalanceThroughAccountProxy(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	var via []string
	var mu sync.Mutex
	prx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		via = append(via, r.URL.String())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"micros":7}`))
	}))
	defer prx.Close()
	fb := &fakeBalance{urls: []string{up.srv.URL + "/balance"}}
	e.withBalance(fb, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	port := prx.Listener.Addr().(*net.TCPAddr).Port
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('p', 'http', '127.0.0.1', $1) RETURNING id`, port)
	off := e.exec1(`INSERT INTO proxies (name, protocol, host, port, status) VALUES ('off', 'http', '127.0.0.1', $1, 'disabled') RETURNING id`, port)
	setProxy := func(p int64) {
		if _, err := e.db.SQL.ExecContext(t.Context(), `UPDATE accounts SET proxy_id = $1 WHERE id = $2`, p, id); err != nil {
			t.Fatal(err)
		}
	}
	setProxy(pid)
	if _, d := e.getBalance(id, true); d["amount"] != "0.000007" {
		t.Fatalf("via proxy: %v", d)
	}
	setProxy(off)
	built := func() int { fb.mu.Lock(); defer fb.mu.Unlock(); return fb.builds }
	before := built()
	if _, d := e.getBalance(id, true); d["error"] != "transient: proxy disabled" || d["amount"] != "0.000007" || built() != before {
		t.Fatalf("disabled proxy: %v, builds %d -> %d", d, before, built())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(via) != 1 || via[0] != up.srv.URL+"/balance" || len(up.requests()) != 0 {
		t.Fatalf("proxy saw %v, upstream %v", via, up.requests())
	}
}

// Concurrent reads of one account on a node share one query.
func TestBalanceReadsShareOneQuery(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	fb := &fakeBalance{urls: []string{up.srv.URL + "/balance"}, gate: make(chan struct{}), entered: make(chan struct{}, 8)}
	e.withBalance(fb, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	up.micros.Store(3_000_000)
	var wg sync.WaitGroup
	results := make([]map[string]any, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = e.getBalance(id, true)
		}()
		if i == 0 {
			<-fb.entered // the first read holds the query
		}
	}
	time.Sleep(200 * time.Millisecond) // the others join it
	close(fb.gate)
	wg.Wait()
	for _, r := range results {
		if r["amount"] != "3.000000" {
			t.Fatalf("results: %v", results)
		}
	}
	if builds, _ := fb.calls(); builds != 1 || len(up.requests()) != 1 {
		t.Fatalf("builds %d, requests %v", builds, up.requests())
	}
}

// A plugin declaring balance without implementing it: no balance, nothing
// stored.
func TestBalanceUnimplemented(t *testing.T) {
	e := setup(t)
	e.withBalance(nil, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", "https://api.example.com", true)
	if code, _ := e.getBalance(id, true); code != 404 {
		t.Fatalf("GET: %d", code)
	}
	if n, err := e.svc.sweepDueBalances(t.Context()); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if snap, err := e.db.AccountBalance(t.Context(), id); snap != nil || err != nil {
		t.Fatalf("stored: %+v %v", snap, err)
	}
}

// Changing an account's key, settings or proxy drops its balance: the next
// read starts afresh. Other changes keep it.
func TestBalanceDroppedWhenCredentialsChange(t *testing.T) {
	e := setup(t)
	up := newBalanceUpstream(t)
	e.withBalance(&fakeBalance{urls: []string{up.srv.URL + "/balance"}}, &manifest.AccountBalance{})
	id := e.mkBalanceAccount("bal", up.srv.URL, true)
	read := func() {
		t.Helper()
		if _, d := e.getBalance(id, true); d["updated_at"] == nil {
			t.Fatalf("read: %v", d)
		}
		e.balanceIn(id) // cached
		if !e.mr.Exists(balanceCacheKey(id)) {
			t.Fatal("not cached")
		}
	}
	read()
	path := fmt.Sprintf("/accounts/%d", id)
	if code, out := e.do("PATCH", path, map[string]any{"name": "renamed"}); code != 200 {
		t.Fatalf("rename: %d %v", code, out)
	}
	if s, _ := e.db.AccountBalance(t.Context(), id); s == nil || !e.mr.Exists(balanceCacheKey(id)) {
		t.Fatal("a rename dropped the balance")
	}
	if code, out := e.do("PATCH", path, map[string]any{"credentials": map[string]any{"api_key": "sk-good-key-456", "base_url": up.srv.URL}}); code != 200 {
		t.Fatalf("new key: %d %v", code, out)
	}
	if s, _ := e.db.AccountBalance(t.Context(), id); s != nil || e.mr.Exists(balanceCacheKey(id)) {
		t.Fatalf("kept after a key change: %+v", s)
	}
	if b := e.balanceIn(id); keys(b) != "1 supported" {
		t.Fatalf("view after a key change: %v", b)
	}
	read()
	pid := e.exec1(`INSERT INTO proxies (name, protocol, host, port) VALUES ('p', 'http', 'proxy.example', 3128) RETURNING id`)
	if code, out := e.do("PATCH", path, map[string]any{"proxy_id": pid}); code != 200 {
		t.Fatalf("proxy: %d %v", code, out)
	}
	if s, _ := e.db.AccountBalance(t.Context(), id); s != nil {
		t.Fatalf("kept after a proxy change: %+v", s)
	}
}
