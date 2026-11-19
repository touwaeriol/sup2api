package codex

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

const (
	resetToken     = "tok-secret-123"
	resetDeviceID  = 7
	resetOAuthID   = 8
	resetNoTokenID = 9
)

// sentRequest is one SendAccountRequest the plugin made.
type sentRequest struct {
	accountID int64
	method    string
	path      string
	headers   map[string]string
	body      string
}

// resetClient is the HostService client the reset routes see: it sends
// SendAccountRequest to the fake backend (the core's job) and records
// SetAccountAutomaticState; every other call goes to the harness host.
type resetClient struct {
	pluginv1.HostServiceClient

	mu sync.Mutex
	// transportFailures fails that many first sends with status 0; sendErr,
	// when set, fails every send as an RPC error.
	transportFailures int
	sendErr           error
	sent              []sentRequest
	clears            []*pluginv1.SetAccountAutomaticStateRequest
	clearErr          error
}

func (c *resetClient) SendAccountRequest(ctx context.Context, in *pluginv1.SendAccountRequestRequest, _ ...grpc.CallOption) (*pluginv1.ExecutionHTTPResponse, error) {
	if c.sendErr != nil {
		return nil, c.sendErr
	}
	req, err := http.NewRequestWithContext(ctx, in.GetMethod(), in.GetUrl(), bytes.NewReader(in.GetBody()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	c.mu.Lock()
	c.sent = append(c.sent, sentRequest{accountID: in.GetAccountId(), method: in.GetMethod(), path: req.URL.Path, headers: in.GetHeaders(), body: string(in.GetBody())})
	fail := len(c.sent) <= c.transportFailures
	c.mu.Unlock()
	if fail {
		return &pluginv1.ExecutionHTTPResponse{TransportError: "connection reset by peer"}, nil
	}
	for k, v := range in.GetHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &pluginv1.ExecutionHTTPResponse{TransportError: err.Error()}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return &pluginv1.ExecutionHTTPResponse{Status: int32(resp.StatusCode), Body: body}, nil
}

func (c *resetClient) SetAccountAutomaticState(_ context.Context, in *pluginv1.SetAccountAutomaticStateRequest, _ ...grpc.CallOption) (*pluginv1.SetAccountAutomaticStateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clears = append(c.clears, in)
	if c.clearErr != nil {
		return nil, c.clearErr
	}
	return &pluginv1.SetAccountAutomaticStateResponse{Changed: true, Status: "active"}, nil
}

func (c *resetClient) requests() []sentRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sentRequest(nil), c.sent...)
}

func (c *resetClient) clearCalls() []*pluginv1.SetAccountAutomaticStateRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*pluginv1.SetAccountAutomaticStateRequest(nil), c.clears...)
}

// resetHost is the harness host with the plugin's accounts and resetClient;
// kv, when set, replaces the harness KV.
type resetHost struct {
	pluginsdk.Host
	client   *resetClient
	accounts map[int64]*pluginv1.Account
	kv       pluginsdk.KV
}

func (h *resetHost) Client() pluginv1.HostServiceClient { return h.client }

func (h *resetHost) KV() pluginsdk.KV {
	if h.kv != nil {
		return h.kv
	}
	return h.Host.KV()
}

func (h *resetHost) Account(_ context.Context, id int64) (*pluginv1.Account, error) {
	a, ok := h.accounts[id]
	if !ok {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return a, nil
}

// resetEnv is a running plugin with a fake chatgpt.com whose answers
// backend sets per test.
type resetEnv struct {
	p       *Plugin
	h       *pluginsdktest.Harness
	host    *resetHost
	client  *resetClient
	backend func(w http.ResponseWriter, r *http.Request)
}

func newResetEnv(t *testing.T) *resetEnv {
	t.Helper()
	e := &resetEnv{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.backend(w, r) }))
	t.Cleanup(srv.Close)
	p := newTestPlugin(testEndpoints(srv.URL))
	e.p = p
	e.h = startPlugin(t, p)
	e.client = &resetClient{HostServiceClient: p.host.Client()}
	e.host = &resetHost{Host: p.host, client: e.client, accounts: map[int64]*pluginv1.Account{
		resetDeviceID:  resetAccountRow(resetDeviceID, TypeDevice, `{"access_token":"`+resetToken+`","chatgpt_account_id":"acc-1"}`),
		resetOAuthID:   resetAccountRow(resetOAuthID, TypeOAuth, `{"access_token":"`+resetToken+`","chatgpt_account_id":"acc-1"}`),
		resetNoTokenID: resetAccountRow(resetNoTokenID, TypeDevice, `{"refresh_token":"r"}`),
	}}
	// Requests reach the plugin over the harness connection after this write.
	p.host = e.host
	return e
}

func resetAccountRow(id int64, typ, creds string) *pluginv1.Account {
	return &pluginv1.Account{Id: id, Type: typ, CredentialsJson: creds}
}

func (e *resetEnv) consume(body any) *pluginv1.HTTPResponse {
	return e.h.Do("POST", "/reset-credits/consume", nil, body)
}

// consumeAnswer makes the backend answer every consume with status and body.
func (e *resetEnv) consumeAnswer(status int, body string) {
	e.backend = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// checkHeaders asserts the account's client headers (agent.go; built-in
// here) and bearer token, and that no x-codex-* header is sent.
func checkHeaders(t *testing.T, h map[string]string) {
	t.Helper()
	if h["authorization"] != "Bearer "+resetToken || h["chatgpt-account-id"] != "acc-1" ||
		h["originator"] != "sup2api" || h["user-agent"] != "sup2api-codex/"+Version || h["accept"] != "application/json" {
		t.Errorf("headers = %v", h)
	}
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-codex-") {
			t.Errorf("header %s imitates the Codex CLI", k)
		}
	}
}

func TestResetListParsesAndSorts(t *testing.T) {
	e := newResetEnv(t)
	e.backend = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != resetCreditsPath {
			http.Error(w, "unexpected", http.StatusTeapot)
			return
		}
		_, _ = io.WriteString(w, `{"available_count": 2, "credits": [
			{"id":"never","reset_type":"future_kind","status":"available","granted_at":"2026-11-01T00:00:00Z","expires_at":null,"title":null,"description":null},
			{"id":"late","reset_type":"codex","status":"redeemed","granted_at":"2026-10-01T00:00:00.5Z","expires_at":"2026-12-31T00:00:00Z","title":"Late","description":"d"},
			{"id":"bad-time","status":"something_new","granted_at":"yesterday","expires_at":"soon"},
			{"id":"soon","status":"available","granted_at":"2026-11-02T08:00:00+08:00","expires_at":"2026-11-20T00:00:00Z","title":"Soon"}
		]}`)
	}
	r := e.h.Do("POST", "/reset-credits/list", nil, map[string]any{"account_id": resetDeviceID})
	if r.GetStatus() != 200 {
		t.Fatalf("list = %d %s", r.GetStatus(), r.GetBody())
	}
	var got ResetCreditsResponse
	mustData(t, r.GetBody(), &got)
	if got.AvailableCount != 2 || len(got.Credits) != 4 {
		t.Fatalf("list = %+v", got)
	}
	var ids []string
	for _, c := range got.Credits {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "soon,late,never,bad-time" {
		t.Errorf("order = %v, want expiry ascending with unknown expiry last", ids)
	}
	soon, late, never, bad := got.Credits[0], got.Credits[1], got.Credits[2], got.Credits[3]
	if soon.Title != "Soon" || soon.Status != "available" || soon.GrantedAt != time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).Unix() ||
		soon.ExpiresAt == nil || *soon.ExpiresAt != time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("soon = %+v", soon)
	}
	if late.Description != "d" || late.Status != "redeemed" || late.GrantedAt != time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("late = %+v", late)
	}
	if never.ExpiresAt != nil || never.Title != "" || never.Description != "" {
		t.Errorf("never = %+v", never)
	}
	if bad.Status != "something_new" || bad.GrantedAt != 0 || bad.ExpiresAt != nil {
		t.Errorf("bad-time = %+v", bad)
	}
	// expires_at is null, not absent, when unknown.
	if !strings.Contains(string(r.GetBody()), `"id":"never","title":"","description":"","status":"available","granted_at":1793491200,"expires_at":null`) {
		t.Errorf("body = %s", r.GetBody())
	}
	sent := e.client.requests()
	if len(sent) != 1 || sent[0].accountID != resetDeviceID || sent[0].method != http.MethodGet || sent[0].body != "" {
		t.Fatalf("sent = %+v", sent)
	}
	checkHeaders(t, sent[0].headers)
	if _, ok := sent[0].headers["content-type"]; ok {
		t.Errorf("a GET has no content-type: %v", sent[0].headers)
	}
	// A Sign in with Codex account has the same resets.
	if r := e.h.Do("POST", "/reset-credits/list", nil, map[string]any{"account_id": resetOAuthID}); r.GetStatus() != 200 {
		t.Fatalf("oauth list = %d %s", r.GetStatus(), r.GetBody())
	}
	if sent = e.client.requests(); len(sent) != 2 || sent[1].accountID != resetOAuthID {
		t.Fatalf("sent = %+v", sent)
	}
	checkHeaders(t, sent[1].headers)
}

func TestResetListErrors(t *testing.T) {
	e := newResetEnv(t)
	e.backend = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `not json`) }
	for _, tc := range []struct {
		name       string
		body       any
		wantStatus int32
		wantCode   string
	}{
		{"missing account", map[string]any{}, 400, "invalid_argument"},
		{"invalid json", "{", 400, "invalid_argument"},
		{"unknown account", map[string]any{"account_id": 404}, 404, "account_not_found"},
		{"no token", map[string]any{"account_id": resetNoTokenID}, 409, "not_authorized"},
		{"unreadable answer", map[string]any{"account_id": resetDeviceID}, 502, "upstream_error"},
	} {
		r := e.h.Do("POST", "/reset-credits/list", nil, tc.body)
		if r.GetStatus() != tc.wantStatus || decodeError(t, r).Error.Code != tc.wantCode {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.GetStatus(), r.GetBody(), tc.wantStatus, tc.wantCode)
		}
	}
	if n := len(e.client.requests()); n != 1 {
		t.Errorf("sent %d requests, want only the readable account's", n)
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestResetConsumeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantStatus int32
		wantCode   string // error code; "" = success, outcome = name
		wantClear  bool
	}{
		{"reset", 200, `{"code":"reset","windows_reset":2}`, 200, "", true},
		{"already_redeemed", 200, `{"code":"already_redeemed","windows_reset":1}`, 200, "", true},
		{"nothing to reset", 200, `{"code":"nothing_to_reset","windows_reset":0}`, 409, "nothing_to_reset", false},
		{"no credit", 200, `{"code":"no_credit","windows_reset":0}`, 409, "no_credit", false},
		{"unknown code", 200, `{"code":"brand_new","windows_reset":0}`, 502, "upstream_error", false},
		{"not json", 200, `oops`, 502, "upstream_error", false},
		{"unauthorized", 401, `{"detail":"expired"}`, 409, "auth_rejected", false},
		{"forbidden", 403, `{}`, 409, "auth_rejected", false},
		{"server error", 500, `{"detail":"boom"}`, 502, "upstream_error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newResetEnv(t)
			e.consumeAnswer(tc.status, tc.body)
			r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}, "credit_id": " credit-1 "})
			if r.GetStatus() != tc.wantStatus {
				t.Fatalf("consume = %d %s, want %d", r.GetStatus(), r.GetBody(), tc.wantStatus)
			}
			if tc.wantCode != "" {
				if c := decodeError(t, r).Error.Code; c != tc.wantCode {
					t.Errorf("code = %s, want %s", c, tc.wantCode)
				}
			} else {
				var got ResetConsumeResponse
				mustData(t, r.GetBody(), &got)
				if got.Outcome != tc.name || !got.AccountRestored {
					t.Errorf("answer = %+v", got)
				}
			}
			// Only a transport failure is retried.
			if n := len(e.client.requests()); n != 1 {
				t.Errorf("sent %d requests, want 1", n)
			}
			clears := e.client.clearCalls()
			if !tc.wantClear {
				if len(clears) != 0 {
					t.Errorf("CLEAR called on %s", tc.name)
				}
				return
			}
			if len(clears) != 1 || clears[0].GetAccountId() != resetDeviceID || clears[0].GetState().GetAction() != pluginv1.AccountAutomaticState_ACTION_CLEAR {
				t.Errorf("clears = %v", clears)
			}
		})
	}
}

func TestResetConsumeRequest(t *testing.T) {
	e := newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":2}`)
	r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}, "credit_id": " credit-1 "})
	var got ResetConsumeResponse
	mustData(t, r.GetBody(), &got)
	if got != (ResetConsumeResponse{Outcome: "reset", WindowsReset: 2, AccountRestored: true}) {
		t.Errorf("answer = %+v", got)
	}
	// Without credit_id the backend picks the credit.
	e.consume(map[string]any{"account_ids": []int64{resetDeviceID}})

	sent := e.client.requests()
	if len(sent) != 2 {
		t.Fatalf("sent = %+v", sent)
	}
	var first, second consumeBody
	mustJSON(t, []byte(sent[0].body), &first)
	mustJSON(t, []byte(sent[1].body), &second)
	if first.CreditID != "credit-1" || !uuidPattern.MatchString(first.RedeemRequestID) {
		t.Errorf("first body = %s", sent[0].body)
	}
	if strings.Contains(sent[1].body, "credit_id") || second.RedeemRequestID == first.RedeemRequestID {
		t.Errorf("second body = %s (first %s)", sent[1].body, sent[0].body)
	}
	for _, s := range sent {
		if s.method != http.MethodPost || s.path != resetConsumePath || s.headers["content-type"] != "application/json" {
			t.Errorf("request = %+v", s)
		}
		checkHeaders(t, s.headers)
	}
	// The lock is given back after each consume.
	if e.h.Host.Locks.Held(resetLockName(resetDeviceID)) {
		t.Error("reset lock still held")
	}
}

func TestResetConsumeRetriesTransportFailureOnce(t *testing.T) {
	e := newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":1}`)
	e.client.transportFailures = 1
	r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}})
	if r.GetStatus() != 200 {
		t.Fatalf("consume = %d %s", r.GetStatus(), r.GetBody())
	}
	sent := e.client.requests()
	if len(sent) != 2 || sent[0].body != sent[1].body {
		t.Fatalf("sent = %+v, want one retry with the same redeem_request_id", sent)
	}

	// Two transport failures: no third attempt, 502.
	e = newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":1}`)
	e.client.transportFailures = 2
	r = e.consume(map[string]any{"account_ids": []int64{resetDeviceID}})
	if r.GetStatus() != 502 || decodeError(t, r).Error.Code != "upstream_error" || !strings.Contains(decodeError(t, r).Error.Message, "connection reset") {
		t.Fatalf("consume = %d %s", r.GetStatus(), r.GetBody())
	}
	if sent := e.client.requests(); len(sent) != 2 || sent[0].body != sent[1].body {
		t.Fatalf("sent = %+v", sent)
	}
	if len(e.client.clearCalls()) != 0 {
		t.Error("CLEAR after a failed consume")
	}
}

func TestResetConsumeClearFailureStillSucceeds(t *testing.T) {
	e := newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":2}`)
	e.client.clearErr = status.Error(codes.Unavailable, "core down")
	r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}})
	if r.GetStatus() != 200 {
		t.Fatalf("consume = %d %s", r.GetStatus(), r.GetBody())
	}
	var got ResetConsumeResponse
	mustData(t, r.GetBody(), &got)
	if got != (ResetConsumeResponse{Outcome: "reset", WindowsReset: 2}) {
		t.Errorf("answer = %+v, want account_restored false", got)
	}
	if len(e.client.clearCalls()) != 1 {
		t.Error("CLEAR not attempted")
	}
}

func TestResetConsumeBusy(t *testing.T) {
	e := newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":1}`)
	lock, ok, err := e.host.Locks().TryAcquire(context.Background(), resetLockName(resetDeviceID), time.Minute)
	if err != nil || !ok {
		t.Fatalf("take the lock: %v %v", ok, err)
	}
	r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}})
	if r.GetStatus() != 409 || decodeError(t, r).Error.Code != "busy" {
		t.Fatalf("consume = %d %s", r.GetStatus(), r.GetBody())
	}
	if n := len(e.client.requests()); n != 0 {
		t.Fatalf("sent %d requests while the account was locked", n)
	}
	_ = lock.Release(context.Background())
	if r := e.consume(map[string]any{"account_ids": []int64{resetDeviceID}}); r.GetStatus() != 200 {
		t.Fatalf("consume after release = %d %s", r.GetStatus(), r.GetBody())
	}
}

func TestResetConsumeRefusals(t *testing.T) {
	e := newResetEnv(t)
	e.consumeAnswer(200, `{"code":"reset","windows_reset":1}`)
	for _, tc := range []struct {
		name       string
		body       any
		wantStatus int32
		wantCode   string
		wantField  string
	}{
		{"no ids", map[string]any{}, 400, "invalid_argument", "account_ids"},
		{"empty ids", map[string]any{"account_ids": []int64{}}, 400, "invalid_argument", "account_ids"},
		{"two ids", map[string]any{"account_ids": []int64{resetDeviceID, resetNoTokenID}}, 400, "invalid_argument", "account_ids"},
		{"zero id", map[string]any{"account_ids": []int64{0}}, 400, "invalid_argument", "account_ids"},
		{"invalid json", "{", 400, "invalid_argument", "body"},
		{"no token", map[string]any{"account_ids": []int64{resetNoTokenID}}, 409, "not_authorized", ""},
		{"unknown account", map[string]any{"account_ids": []int64{404}}, 404, "account_not_found", ""},
	} {
		r := e.consume(tc.body)
		eb := decodeError(t, r)
		if r.GetStatus() != tc.wantStatus || eb.Error.Code != tc.wantCode {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.GetStatus(), r.GetBody(), tc.wantStatus, tc.wantCode)
		}
		if tc.wantField != "" && (len(eb.Error.Details.Fields) != 1 || eb.Error.Details.Fields[0].Field != tc.wantField) {
			t.Errorf("%s: fields = %+v", tc.name, eb.Error.Details.Fields)
		}
	}
	if n := len(e.client.requests()); n != 0 {
		t.Errorf("sent %d requests for refused consumes", n)
	}
}

func TestResetErrorsNeverEchoTheToken(t *testing.T) {
	e := newResetEnv(t)
	e.backend = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"detail":"bad header `+r.Header.Get("Authorization")+`"}`)
	}
	for _, r := range []*pluginv1.HTTPResponse{
		e.consume(map[string]any{"account_ids": []int64{resetDeviceID}}),
		e.h.Do("POST", "/reset-credits/list", nil, map[string]any{"account_id": resetDeviceID}),
	} {
		msg := decodeError(t, r).Error.Message
		if r.GetStatus() != 502 || !strings.Contains(msg, "status 502") || !strings.Contains(msg, "[redacted]") || strings.Contains(string(r.GetBody()), resetToken) {
			t.Errorf("answer = %d %s", r.GetStatus(), r.GetBody())
		}
	}
}

func TestResetHostCallFailure(t *testing.T) {
	e := newResetEnv(t)
	// A host failure is 503 with the host's reason.
	for _, err := range []error{status.Error(codes.Unimplemented, "unknown method"), status.Error(codes.Unavailable, "core busy")} {
		e.client.sendErr = err
		r := e.h.Do("POST", "/reset-credits/list", nil, map[string]any{"account_id": resetDeviceID})
		if msg := decodeError(t, r).Error.Message; r.GetStatus() != 503 || !strings.Contains(msg, status.Convert(err).Message()) {
			t.Errorf("list = %d %s", r.GetStatus(), r.GetBody())
		}
	}
}

func TestParseResetCreditsRejectsNonJSON(t *testing.T) {
	if _, err := parseResetCredits([]byte(`<html>`)); err == nil {
		t.Fatal("want an error")
	}
	got, err := parseResetCredits([]byte(`{}`))
	if err != nil || got.AvailableCount != 0 || got.Credits == nil || len(got.Credits) != 0 {
		t.Fatalf("empty = %+v %v", got, err)
	}
}

func TestParseResetCreditsCountsAvailableWithoutCount(t *testing.T) {
	got, err := parseResetCredits([]byte(`{"credits":[{"id":"a","status":"available"},{"id":"b","status":"redeemed"},{"id":"c","status":"available"}]}`))
	if err != nil || got.AvailableCount != 2 {
		t.Fatalf("got %+v %v, want 2 available", got, err)
	}
	got, _ = parseResetCredits([]byte(`{"available_count":5,"credits":[]}`))
	if got.AvailableCount != 5 {
		t.Fatalf("explicit count = %d, want 5", got.AvailableCount)
	}
}
