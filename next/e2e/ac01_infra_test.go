package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// AC 1: `docker compose up` starts PG, Redis, two nodes and Caddy.
func TestAC01_ComposeStack(t *testing.T) {
	e := Setup(t)

	t.Run("both nodes serve through the load balancer", func(t *testing.T) {
		seen := map[string]bool{}
		c := NewClient(e.BaseURL)
		c.HTTP.Timeout = time.Second
		// A healthy least-connection balancer need not alternate sequential
		// requests. Require both nodes within a bound, not a fixed pick order.
		Eventually(t, 5*time.Second, 50*time.Millisecond, func() string {
			return fmt.Sprintf("both healthy nodes through load balancer (seen %v)", seen)
		}, func() bool {
			r := c.Do(t, http.MethodGet, "/healthz", nil)
			if r.Status != 200 || r.JSON().Get("status").String() != "ok" {
				t.Fatalf("healthz: %s", r)
			}
			node := r.JSON().Get("node").String()
			if node != "node-1" && node != "node-2" {
				t.Fatalf("healthz reported unexpected node %q: %s", node, r)
			}
			seen[node] = true
			return seen["node-1"] && seen["node-2"]
		})
	})

	t.Run("each node healthy with the same version", func(t *testing.T) {
		var version string
		for n := range e.NodeURLs {
			h := e.With(t).NodeHealth(n + 1)
			if want := "node-" + itoa(int64(n+1)); h.Get("node").String() != want {
				t.Fatalf("node %d reports %q", n+1, h.Get("node").String())
			}
			if version == "" {
				version = h.Get("version").String()
			} else if h.Get("version").String() != version {
				t.Fatalf("version mismatch: %s vs %s", version, h.Get("version").String())
			}
		}
	})

	t.Run("core migrations applied exactly once", func(t *testing.T) {
		e2 := e.With(t)
		e2.RequireDocker()
		rows := e2.SQL(`SELECT id FROM schema_migrations ORDER BY id`)
		if len(rows) == 0 || rows[0] != "0001_core.sql" {
			t.Fatalf("schema_migrations = %v", rows)
		}
	})

	t.Run("market index served", func(t *testing.T) { checkMarketIndex(t, e) })

	t.Run("mock upstream", func(t *testing.T) {
		m := e.Mock()
		mark := m.Mark(t)
		key := "sk-ant-mock-ac01-" + e.RunID

		// Non-stream JSON with usage.
		g := e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", false), nil)
		if g.Status != 200 || g.JSON().Get("usage.input_tokens").Int() != MockInputTokens ||
			g.JSON().Get("usage.output_tokens").Int() != MockOutputTokens {
			t.Fatalf("non-stream: %d %s", g.Status, g.Body)
		}

		// Stream: events in order and delivered incrementally through Caddy.
		g = e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", true),
			map[string]string{"x-mock-chunk-delay-ms": "150"})
		types := g.EventTypes()
		if g.Status != 200 || len(types) < 6 || types[0] != "message_start" || types[len(types)-1] != "message_stop" {
			t.Fatalf("stream events: %d %v", g.Status, types)
		}
		start := g.Events[0].Data.Get("message.usage")
		if start.Get("input_tokens").Int() != MockInputTokens || start.Get("cache_read_input_tokens").Int() != MockCacheReadTokens ||
			start.Get("cache_creation_input_tokens").Int() != MockCacheCreationTokens {
			t.Fatalf("message_start usage: %s", start.Raw)
		}
		if spread := g.Events[len(g.Events)-1].At - g.Events[0].At; spread < 500*time.Millisecond {
			t.Fatalf("SSE appears buffered: all events within %s", spread)
		}

		// Errors.
		for _, st := range []int{429, 401, 529, 500} {
			g = e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", false),
				map[string]string{"x-mock-status": itoa(int64(st))})
			if g.Status != st || g.JSON().Get("type").String() != "error" {
				t.Fatalf("x-mock-status %d: got %d %s", st, g.Status, g.Body)
			}
			if st == 429 && g.Header.Get("retry-after") != "2" {
				t.Fatalf("429 without retry-after: 2")
			}
		}

		// count_tokens.
		g = e.Gateway(t, e.MockURL, "/v1/messages/count_tokens", key, MessagesBody("claude-mock", "hi", false), nil)
		if g.Status != 200 || g.JSON().Get("input_tokens").Int() <= 0 {
			t.Fatalf("count_tokens: %d %s", g.Status, g.Body)
		}

		// Delay.
		t0 := time.Now()
		e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", false), map[string]string{"x-mock-delay-ms": "300"})
		if time.Since(t0) < 300*time.Millisecond {
			t.Fatal("x-mock-delay-ms ignored")
		}

		// Control rule consumed once, recorded requests carry the key.
		m.SetRule(t, MockRule{APIKey: key, Status: 529, Remaining: 1})
		if g = e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", false), nil); g.Status != 529 {
			t.Fatalf("rule not applied: %d", g.Status)
		}
		if g = e.Gateway(t, e.MockURL, "/v1/messages", key, MessagesBody("claude-mock", "hi", false), nil); g.Status != 200 {
			t.Fatalf("rule not consumed: %d", g.Status)
		}
		reqs := m.Since(t, mark)
		mine := 0
		for _, k := range KeysUsed(reqs, "") {
			if k == key {
				mine++
			}
		}
		if mine < 10 {
			t.Fatalf("mock recorded %d requests with x-api-key %q, want >= 10", mine, key)
		}
	})
}

// checkMarketIndex checks the market index the stack serves: index.json v2
// (CONTRACTS §11.1) listing plugins of active publishers, each version
// downloadable and matching its size and sha256.
func checkMarketIndex(t *testing.T, e *Env) {
	r := NewClient(e.BaseURL).Do(t, http.MethodGet, "/market/index.json", nil)
	if r.Status != 200 {
		t.Fatalf("index: %s", r)
	}
	idx := r.JSON()
	if idx.Get("version").Int() != 2 || !idx.Get("publishers").IsArray() || !idx.Get("plugins").IsArray() {
		t.Fatalf("index format (CONTRACTS §11.1 v2): %s", r.Body)
	}
	// The image always builds tools/sub2api-plugin and packages plugins/*
	// (deploy/docker/build-go.sh), so in this deployment an empty index
	// means the build chain broke, not "not ready yet".
	if len(idx.Get("plugins").Array()) == 0 {
		t.Fatalf("market index lists no plugins: build-go.sh packaged nothing (index: %s)", r.Body)
	}
	for _, p := range idx.Get("plugins").Array() {
		pub := idx.Get(fmt.Sprintf(`publishers.#(id==%q)`, p.Get("publisher").String()))
		if pub.Get("status").String() != "active" || pub.Get("trust").String() == "" {
			t.Fatalf("plugin %s: publisher not listed active: %s", p.Get("key"), r.Body)
		}
		for _, v := range p.Get("versions").Array() {
			e2 := e.With(t)
			e2.MarketPackage(p.Get("key").String(), v.Get("version").String())
		}
	}
}
