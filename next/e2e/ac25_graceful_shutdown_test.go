package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// AC25 uses real SIGTERM while a response is already streaming. Readiness
// must be withdrawn before the stream and its accounting finish; heartbeats
// deliberately remain alive during drain, unlike AC17's SIGKILL case.
func TestAC25_GracefulShutdownKeepsInflightAccounting(t *testing.T) {
	e := Setup(t)
	e.RequireDocker()
	if len(e.NodeURLs) != 2 {
		t.Fatal("AC25 requires exactly two nodes")
	}
	admin := e.Admin()
	tn := e.NewTenant(admin, TenantOpts{Accounts: 1, Balance: "10"})
	acct := tn.Accounts[0]
	before := e.NodeHealth(2)
	bootID := before.Get("boot_id").String()
	if bootID == "" {
		t.Fatal("node-2 health response lacks boot_id")
	}
	container := e.Container("node-2")
	policy := strings.TrimSpace(e.Docker("inspect", "--format", "{{.HostConfig.RestartPolicy.Name}}", container))
	if policy != "no" && policy != "" {
		t.Fatalf("AC25 requires restart policy no to observe the completed shutdown; got %q", policy)
	}

	m := e.Mock()
	mark := m.Mark(t)
	// Seven events with a three-second pause each keep a one-word response
	// open for about 21 seconds, safely inside the 30-second HTTP drain budget.
	const reply = "graceful-drain-complete"
	m.SetRule(t, MockRule{APIKey: acct.Key, ChunkDelayMS: 3000, Text: reply})
	t.Cleanup(func() { m.ClearRule(t, acct.Key) })
	t.Cleanup(func() { e.StartNode(2) })
	streamCtx, cancelStream := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelStream()
	started := make(chan string, 1)
	done := make(chan drainStreamResult, 1)
	go func() {
		done <- readDrainStream(streamCtx, e.NodeURLs[1], tn.APIKey, MessagesBody(tn.Model, "finish this response while draining", true), started)
	}()
	var requestID string
	select {
	case requestID = <-started:
		if requestID == "" {
			t.Fatal("stream has no X-Request-Id")
		}
	case result := <-done:
		t.Fatalf("stream ended before SIGTERM could be tested: %+v, %v", result.g, result.err)
	case <-time.After(15 * time.Second):
		t.Fatal("stream did not produce message_start")
	}
	accountSlot := fmt.Sprintf("slot:account:%d", acct.ID)
	// (There are no user slots: concurrency is per account, CONTRACTS §78.)
	if got := e.Redis("ZCARD", accountSlot); got != "1" {
		t.Fatalf("in-flight stream must hold one account slot, got %q", got)
	}
	select {
	case result := <-done:
		t.Fatalf("stream finished before SIGTERM: %+v, %v", result.g, result.err)
	default:
	}

	// Poll before sending the signal so even a remote docker command cannot
	// hide the three-second interval between readiness withdrawal and close.
	healthCtx, cancelHealth := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelHealth()
	draining := make(chan drainHealthResult, 1)
	go func() { draining <- waitForDrainHealth(healthCtx, e.NodeURLs[1], bootID) }()
	e.Docker("kill", "--signal=TERM", container)
	health := <-draining
	if health.err != nil {
		t.Fatal(health.err)
	}

	// Caddy's active health probe removes the draining node. Read health
	// through the real balancer, without creating more billable requests.
	healthySamples := 0
	Eventually(t, 8*time.Second, 100*time.Millisecond, "load balancer uses only the surviving node", func() bool {
		status, body, err := readDrainHealth(context.Background(), e.BaseURL)
		if err == nil && status == 200 && body.Get("node").String() == "node-1" {
			healthySamples++
		} else {
			healthySamples = 0
		}
		return healthySamples >= 8
	})

	var result drainStreamResult
	select {
	case result = <-done:
	case <-streamCtx.Done():
		t.Fatal("stream did not finish within the drain budget")
	}
	if result.err != nil {
		t.Fatalf("SIGTERM truncated the in-flight stream: %v", result.err)
	}
	if !result.finished.After(health.at) {
		t.Fatal("readiness was not withdrawn while the stream was still in flight")
	}
	g := result.g
	types := g.EventTypes()
	wantEvents := []string{"message_start", "content_block_start", "ping", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if g.Status != 200 || !slices.Equal(types, wantEvents) || g.Text() != reply {
		t.Fatalf("incomplete or duplicated stream: HTTP %d, events=%v, text=%q", g.Status, types, g.Text())
	}
	if g.RequestID != requestID {
		t.Fatalf("stream request id changed: %q -> %q", requestID, g.RequestID)
	}

	wantCost := ExpectedTokenCost(RunPrice, MockInputTokens, MockOutputTokens, MockCacheReadTokens, MockCacheCreationTokens, 0, 1)
	assertAccounting := func() {
		t.Helper()
		// The draining node commits the record: forwarding v2 sends the usage
		// of a host_usage request to the asynchronous settlement after the
		// answer ends (as normal forwarding did), so it lands shortly after
		// EOF, while the node drains - exactly one, billed.
		var rows []string
		Eventually(t, 15*time.Second, 200*time.Millisecond, func() string {
			return fmt.Sprintf("exactly one committed, billed usage record after clean EOF: %v", rows)
		}, func() bool {
			rows = e.SQL(fmt.Sprintf(`SELECT count(*), count(*) FILTER (WHERE request_id=%s AND billing_status='billed') FROM usage_logs WHERE user_id=%d`, sqlText(requestID), tn.User.UserID))
			return len(rows) == 1 && rows[0] == "1|1"
		})
		u := e.UsageByRequest(admin, tn.User.UserID, requestID)
		if u.Get("billing_status").String() != "billed" || !u.Get("success").Bool() || u.Get("account_id").Int() != acct.ID || u.Get("node_id").String() != "node-2" {
			t.Fatalf("drained request was not billed successfully by its original node: %s", u.Raw)
		}
		if u.Get("input_tokens").Int() != MockInputTokens || u.Get("output_tokens").Int() != MockOutputTokens {
			t.Fatalf("drained stream lost final usage: %s", u.Raw)
		}
		AssertMoney(t, "drained stream cost", u.Get("total_cost").String(), wantCost)
		ledger := e.UsageDebits(admin, tn.User.UserID)
		if len(ledger) != 1 || ledger[0].Get("ref_id").String() != requestID {
			t.Fatalf("drained stream must be charged exactly once: %v", ledger)
		}
		AssertMoney(t, "drained stream ledger", ledger[0].Get("delta").String(), new(big.Rat).Neg(wantCost))
		AssertMoney(t, "drained stream balance", e.Balance(tn.User), new(big.Rat).Sub(big.NewRat(10, 1), wantCost))
	}
	assertAccounting()
	Eventually(t, 10*time.Second, 200*time.Millisecond, "drained request releases account and user slots", func() bool {
		return e.Redis("ZCARD", accountSlot) == "0"
	})
	// Waiting for stopped state verifies full shutdown, not merely a closed
	// listener. Exit 0 and OOMKilled=false distinguish it from forced death.
	Eventually(t, 100*time.Second, time.Second, "node-2 exits after graceful drain", func() bool {
		return strings.TrimSpace(e.Docker("inspect", "--format", "{{.State.Running}}", container)) == "false"
	})
	state := gjson.Parse(strings.TrimSpace(e.Docker("inspect", "--format", "{{json .State}}", container)))
	if state.Get("ExitCode").Int() != 0 || state.Get("OOMKilled").Bool() || state.Get("Error").String() != "" {
		t.Fatalf("node did not stop cleanly: %s", state.Raw)
	}
	assertAccounting()

	e.StartNode(2)
	if after := e.NodeHealth(2); after.Get("boot_id").String() == "" || after.Get("boot_id").String() == bootID {
		t.Fatalf("restarted node did not report a new boot id: %s", after.Raw)
	}
	assertAccounting()
	var requests int
	for _, req := range m.Since(t, mark) {
		if req.Key() == acct.Key && req.Path == "/v1/messages" {
			requests++
		}
	}
	if requests != 1 {
		t.Fatalf("drain/restart repeated the upstream submission: got %d requests", requests)
	}
}

type drainStreamResult struct {
	g        *GatewayResult
	err      error
	finished time.Time
}

func readDrainStream(ctx context.Context, base, key string, body any, started chan<- string) drainStreamResult {
	result := drainStreamResult{}
	raw, err := json.Marshal(body)
	if err != nil {
		result.err = err
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		result.err = err
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", key)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.err = err
		return result
	}
	defer resp.Body.Close()
	result.g = &GatewayResult{Status: resp.StatusCode, Header: resp.Header, RequestID: resp.Header.Get("X-Request-Id")}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		result.g.Body, _ = io.ReadAll(io.LimitReader(resp.Body, 4096))
		result.err = fmt.Errorf("expected SSE, got HTTP %d: %s", resp.StatusCode, result.g.Body)
		return result
	}
	result.g.Events, result.err = readSSEEvents(resp.Body, start, func(event SSEEvent) {
		if event.Event == "message_start" {
			select {
			case started <- result.g.RequestID:
			default:
			}
		}
	})
	result.finished = time.Now()
	return result
}

type drainHealthResult struct {
	at  time.Time
	err error
}

func waitForDrainHealth(ctx context.Context, base, bootID string) drainHealthResult {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var last string
	for {
		status, body, err := readDrainHealth(ctx, base)
		if err == nil && status == http.StatusServiceUnavailable && body.Get("node").String() == "node-2" && body.Get("boot_id").String() == bootID {
			return drainHealthResult{at: time.Now()}
		}
		last = fmt.Sprintf("HTTP %d %s (%v)", status, body.Raw, err)
		select {
		case <-ctx.Done():
			return drainHealthResult{err: fmt.Errorf("never observed the draining node's own health 503: %s: %w", last, ctx.Err())}
		case <-tick.C:
		}
	}
}

func readDrainHealth(ctx context.Context, base string) (int, gjson.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return 0, gjson.Result{}, err
	}
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return 0, gjson.Result{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, gjson.ParseBytes(body), err
}
