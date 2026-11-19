package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AC 32: failover across accounts under forwarding v2. An upstream error of
// the first account's request, or a failure of its plugin before anything
// was sent (here: a Vertex service account that has no access token yet, so
// its Execute fails), moves the request to the next account; the request is
// billed once. A plugin killed while it waits for a task submission's
// answer leaves the task unregistered and the request free, its pre-charge
// released (DESIGN §4.3, §12.4).
func TestAC32_FailoverAndUnreported(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()

	t.Run("upstream error fails over, billed once", func(t *testing.T) {
		e := e.With(t)
		for _, stream := range []bool{false, true} {
			tn := e.NewTenant(admin, TenantOpts{Accounts: 2, Priorities: []int{50, 1}})
			first, second := tn.Accounts[0], tn.Accounts[1]
			m.SetRule(t, MockRule{APIKey: first.Key, Status: 500, Remaining: 1})
			defer m.ClearRule(t, first.Key)
			mark := m.Mark(t)
			g := e.Messages(tn.APIKey, MessagesBody(tn.Model, "failover", stream), nil)
			if g.Status != 200 {
				t.Fatalf("stream=%v: HTTP %d %s", stream, g.Status, g.Body)
			}
			if used := KeysUsed(m.Since(t, mark), "/v1/messages"); len(used) != 2 || used[0] != first.Key || used[1] != second.Key {
				t.Fatalf("stream=%v: upstream keys %v, want [first(500) second]", stream, used)
			}
			u := e.expectBilled(admin, tn.User.UserID, g.RequestID, second.ID, usageAnthropic, stream)
			if u.Get("attempts").Int() != 2 {
				t.Fatalf("stream=%v: attempts = %d, want 2: %s", stream, u.Get("attempts").Int(), u.Raw)
			}
		}
	})

	t.Run("plugin failure before sending fails over, account not penalized", func(t *testing.T) {
		e := e.With(t)
		k := fmt.Sprintf("AIza-fail-%s-%d", e.RunID, nextSeq())
		tn := e.singleAccount(admin, "grp-plugin-fail", AccountSpec{PluginKey: GeminiPlugin, Type: APIKeyType, APIKey: k, Priority: 1}, k, "gemini", "plugin-fail")
		geminiID := tn.Accounts[0].ID
		// Tried first (priority 50): a Vertex account whose Execute fails.
		e.EnsurePlugin(admin, "vertex", "")
		vertexID := tn.addAccount(e, admin, AccountSpec{PluginKey: "vertex", Type: "vertex_sa", Priority: 50,
			Credentials: map[string]any{"service_account_json": serviceAccountJSON(t, "e2e-"+e.RunID), "project_id": "e2e-project", "location": "global"}}, "").ID
		for i := 0; i < 2; i++ {
			mark := m.Mark(t)
			g := e.Gemini(tn.APIKey, tn.Model, "generateContent", "", GeminiBody("plugin failure"))
			if g.Status != 200 {
				t.Fatalf("request %d: HTTP %d %s", i, g.Status, g.Body)
			}
			for _, r := range m.Since(t, mark) {
				if strings.Contains(r.Path, "/projects/e2e-project/") {
					t.Fatalf("request %d: the failing Vertex account sent a request: %+v", i, r)
				}
			}
			oneMockCall(t, m, mark, k, "gemini fallback")
			u := e.expectBilled(admin, tn.User.UserID, g.RequestID, geminiID, usageGemini, false)
			// The Vertex account was tried first both times: a plugin failure
			// does not cool the account down.
			steps := u.Get("attempt_trail.steps").Array()
			if len(steps) < 2 || steps[0].Get("account_id").Int() != vertexID {
				t.Fatalf("request %d: attempt trail does not start with the Vertex account %d: %s", i, vertexID, u.Get("attempt_trail").Raw)
			}
		}
	})

	t.Run("plugin killed while waiting for a task submission", func(t *testing.T) {
		e := e.With(t)
		e.RequireDocker()
		k := e.Name("video-crash")
		st := e.singleAccount(admin, "grp-task-crash", AccountSpec{PluginKey: "volcengine", Type: "relay", APIKey: k, MaxConcurrency: 4,
			Settings: map[string]any{"video_api_prefix": "/api/v3"}}, k, "doubao-seedance-2-0-mini", e.Name("crash"))
		// The upstream holds the submission long enough to kill the plugin.
		m.SetRule(t, MockRule{APIKey: k, DelayMS: 6000})
		defer m.ClearRule(t, k)
		before := Money(t, e.Balance(st.User))
		mark := m.Mark(t)
		// The goroutine only sends: the test fails on this goroutine.
		type submitted struct {
			res *Resp
			err error
		}
		done := make(chan submitted, 1)
		go func() {
			res, err := NewClient(e.NodeURLs[0]).Try(http.MethodPost, videoPath, map[string]any{
				"model": st.Model, "content": []map[string]string{{"type": "text", "text": "crash"}},
				"resolution": "480p", "ratio": "16:9", "duration": 4,
			}, Header("Authorization", "Bearer "+st.APIKey))
			done <- submitted{res, err}
		}()
		Eventually(t, 20*time.Second, 100*time.Millisecond, "the submission reached the upstream", func() bool {
			return len(mockCallsOf(t, m, mark, k)) > 0
		})
		e.KillPluginProcesses(1, "volcengine")
		sub := <-done
		if sub.err != nil {
			t.Fatalf("submission whose plugin died while waiting: %v", sub.err)
		}
		res := sub.res
		if res.Status < 500 {
			t.Fatalf("submission whose plugin died while waiting: %s (want 5xx)", res)
		}
		rid := res.Header.Get("X-Request-Id")
		if rows := e.SQL(fmt.Sprintf(`SELECT count(*) FROM async_tasks t JOIN usage_logs u ON u.id=t.usage_log_id WHERE u.request_id=%s`, sqlText(rid))); len(rows) != 1 || rows[0] != "0" {
			t.Fatalf("a task was registered: %v", rows)
		}
		u := e.UsageByRequest(admin, st.User.UserID, rid)
		if u.Get("billing_status").String() != "free" || Money(t, u.Get("total_cost").String()).Sign() != 0 {
			t.Fatalf("usage of the unconfirmed submission: %s", u.Raw)
		}
		Eventually(t, 20*time.Second, 500*time.Millisecond, "pre-charge released", func() bool {
			return Money(t, e.Balance(st.User)).Cmp(before) == 0
		})
		e.WaitPlugin(admin, "volcengine", "enabled", "")
	})
}

// serviceAccountJSON is a Google service account key file with a fresh RSA
// key (nothing accepts it: the token endpoint is the mock, which has none).
func serviceAccountJSON(t testing.TB, name string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "e2e-project",
		"private_key_id": fmt.Sprintf("%x", big.NewInt(0).SetBytes(der[:8])),
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   name + "@e2e-project.iam.gserviceaccount.com",
		"client_id":      "1234567890",
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	return string(raw)
}
