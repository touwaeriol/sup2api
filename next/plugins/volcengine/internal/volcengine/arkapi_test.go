package volcengine

// Tests of the asset library's upstream calls. The signature is the part
// that has to be right and cannot be eyeballed, so it is verified by
// RECOMPUTING it from the request the server received: the canonical request
// of volcengine V4 (github.com/volcengine/volc-sdk-golang/base sign.go) is
// rebuilt here from the wire, signed with the test secret key and compared
// byte for byte. A test that only asserted "an Authorization header is
// present" would pass with a signature over the wrong region, the wrong
// service or an empty body.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

const (
	testAK = "AKLTtest0000000000000000"
	testSK = "c2VjcmV0LWtleS1mb3ItdGVzdHMtb25seQ=="
)

// capturedRequest is what the fake Ark control plane saw.
type capturedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Host   string
	Body   []byte
}

// fakeArk is an httptest server standing in for the Ark asset OpenAPI,
// reached only through send, which plays the core's SendAccountRequest.
type fakeArk struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []capturedRequest
	sent     []*pluginv1.SendAccountRequestRequest

	// reply is called for every request; nil means "empty Result".
	reply func(c capturedRequest) (status int, body string)
}

func newFakeArk(t *testing.T) *fakeArk {
	t.Helper()
	f := &fakeArk{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c := capturedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
			Header: r.Header.Clone(), Host: r.Host, Body: body,
		}
		f.mu.Lock()
		f.requests = append(f.requests, c)
		reply := f.reply
		f.mu.Unlock()
		status, payload := http.StatusOK, `{"ResponseMetadata":{"RequestId":"req-1"},"Result":{}}`
		if reply != nil {
			status, payload = reply(c)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// send records what the plugin handed the host and sends it the way the
// core's SendAccountRequest does (account sendPluginCall): every header set
// as given, Content-Type only when none is, no redirect followed, the whole
// answer back, a transport failure as transport_error. In production this
// is pluginsdk.SendAccountRequest, so "every upstream call goes through the
// host" is exactly what the recording proves.
func (f *fakeArk) send(ctx context.Context, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
	f.mu.Lock()
	f.sent = append(f.sent, in)
	f.mu.Unlock()
	return hostSend(f.srv.Listener.Addr().String())(ctx, in)
}

// hostSend is the core's sending of a SendAccountRequest, with every
// connection dialled to addr whatever host the URL names.
func hostSend(addr string) sendFunc {
	client := &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(ctx context.Context, in *pluginv1.SendAccountRequestRequest) (*pluginv1.ExecutionHTTPResponse, error) {
		req, err := http.NewRequestWithContext(ctx, in.GetMethod(), in.GetUrl(), bytes.NewReader(in.GetBody()))
		if err != nil {
			return nil, err
		}
		for k, v := range in.GetHeaders() {
			req.Header.Set(k, v)
		}
		if len(in.GetBody()) > 0 && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err() // the gRPC call ends with its context
			}
			return &pluginv1.ExecutionHTTPResponse{TransportError: err.Error()}, nil
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return &pluginv1.ExecutionHTTPResponse{Status: int32(resp.StatusCode), Body: raw}, nil
	}
}

func (f *fakeArk) config() *AssetConfig {
	return &AssetConfig{
		AccountID: 7, Name: "ark-1", AccessKey: testAK, SecretKey: testSK,
		BaseURL: f.srv.URL, Region: DefaultAssetRegion,
	}
}

func (f *fakeArk) last(t *testing.T) capturedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("upstream received no request")
	}
	return f.requests[len(f.requests)-1]
}

// ---------------------------------------------------------------- signature

var authRe = regexp.MustCompile(`^HMAC-SHA256 Credential=([^/]+)/(\d{8})/([^/]+)/([^/]+)/request, SignedHeaders=([a-z0-9;.\-]+), Signature=([0-9a-f]{64})$`)

// verifySignature recomputes the volcengine V4 signature of c and returns the
// credential scope it was signed under.
func verifySignature(t *testing.T, c capturedRequest, ak, sk, region, service string) {
	t.Helper()
	auth := c.Header.Get("Authorization")
	m := authRe.FindStringSubmatch(auth)
	if m == nil {
		t.Fatalf("Authorization is not a volcengine V4 header: %q", auth)
	}
	gotAK, date, gotRegion, gotService, signedHeaders, signature := m[1], m[2], m[3], m[4], m[5], m[6]
	if gotAK != ak {
		t.Errorf("credential access key = %q, want %q", gotAK, ak)
	}
	if gotRegion != region {
		t.Errorf("credential scope region = %q, want %q", gotRegion, region)
	}
	if gotService != service {
		t.Errorf("credential scope service = %q, want %q", gotService, service)
	}
	xDate := c.Header.Get("X-Date")
	if _, err := time.Parse("20060102T150405Z", xDate); err != nil {
		t.Fatalf("X-Date = %q: %v", xDate, err)
	}
	if !strings.HasPrefix(xDate, date) {
		t.Errorf("credential scope date %q does not match X-Date %q", date, xDate)
	}
	// The body hash is both a header and the last line of the canonical
	// request: this is what binds the request body into the signature.
	payloadHash := recomputeSHA256Hex(c.Body)
	if got := c.Header.Get("X-Content-Sha256"); got != payloadHash {
		t.Fatalf("X-Content-Sha256 = %q, want the body hash %q", got, payloadHash)
	}

	headerValue := func(name string) string {
		switch name {
		case "host":
			return c.Host
		default:
			return strings.TrimSpace(c.Header.Get(name))
		}
	}
	names := strings.Split(signedHeaders, ";")
	if !sort.StringsAreSorted(names) {
		t.Errorf("SignedHeaders is not sorted: %q", signedHeaders)
	}
	for _, must := range []string{"host", "x-content-sha256", "x-date"} {
		if !contains(names, must) {
			t.Errorf("SignedHeaders %q does not cover %q", signedHeaders, must)
		}
	}
	var headersToSign strings.Builder
	for _, n := range names {
		headersToSign.WriteString(n + ":" + headerValue(n) + "\n")
	}
	query := strings.ReplaceAll(c.Query.Encode(), "+", "%20")
	canonical := strings.Join([]string{
		c.Method, c.Path, query, headersToSign.String(), signedHeaders, payloadHash,
	}, "\n")
	scope := strings.Join([]string{date, region, service, "request"}, "/")
	stringToSign := strings.Join([]string{"HMAC-SHA256", xDate, scope, recomputeSHA256Hex([]byte(canonical))}, "\n")

	key := recomputeHMAC(recomputeHMAC(recomputeHMAC(recomputeHMAC([]byte(sk), date), region), service), "request")
	want := hex.EncodeToString(recomputeHMAC(key, stringToSign))
	if signature != want {
		t.Fatalf("signature = %s, recomputed %s\ncanonical request:\n%s", signature, want, canonical)
	}
}

// recomputeHMAC and recomputeSHA256Hex are deliberately NOT the production
// helpers of arksign.go. verifySignature is worth what its independence from
// the code under test is worth: if it called signV4's own primitives, a bug in
// one of them would cancel out on both sides and the test would pass.
func recomputeHMAC(key []byte, content string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(content))
	return mac.Sum(nil)
}

func recomputeSHA256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- tests

// TestSignedCallShape is the core signing test: one CreateAssetGroup goes out
// through the injected dialler, carries a volcengine V4 Authorization header
// whose signature recomputes exactly, and puts Action/Version in the query
// with the parameters in a JSON body.
func TestSignedCallShape(t *testing.T) {
	f := newFakeArk(t)
	f.reply = func(capturedRequest) (int, string) {
		return http.StatusOK, `{"ResponseMetadata":{"RequestId":"req-1"},"Result":{"Id":"g-123","Name":"shots"}}`
	}
	api := newArkAPI(f.send)

	result, callErr := api.call(context.Background(), f.config(), ActionCreateAssetGroup, map[string]any{
		"Name": "shots", "GroupType": "AIGC",
	})
	if callErr != nil {
		t.Fatalf("call: %v", callErr)
	}
	if g := groupFromResult(result); g.ID != "g-123" || g.Name != "shots" {
		t.Fatalf("result = %+v", g)
	}

	c := f.last(t)
	verifySignature(t, c, testAK, testSK, DefaultAssetRegion, AssetServiceName)

	if c.Method != http.MethodPost || c.Path != "/" {
		t.Errorf("request line = %s %s, want POST /", c.Method, c.Path)
	}
	if c.Query.Get("Action") != ActionCreateAssetGroup || c.Query.Get("Version") != AssetAPIVersion {
		t.Errorf("query = %v", c.Query)
	}
	if ct := c.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(c.Body, &body); err != nil {
		t.Fatalf("body %q: %v", c.Body, err)
	}
	if body["Name"] != "shots" || body["GroupType"] != "AIGC" {
		t.Errorf("body = %v", body)
	}
	// Everything went through the host, for the account of the config.
	f.mu.Lock()
	sent := append([]*pluginv1.SendAccountRequestRequest(nil), f.sent...)
	f.mu.Unlock()
	if len(sent) != 1 || sent[0].GetAccountId() != f.config().AccountID || !strings.HasPrefix(sent[0].GetUrl(), f.srv.URL+"/?") {
		t.Fatalf("handed to the host: %v", sent)
	}
}

// TestRequestIsTheReleasedOne pins what goes upstream to what the released
// plugin (0.12.8, its own HTTP client over the egress tunnel) sent for the
// same call at the same instant: method, path, query, every header and the
// body, byte for byte. The expected values were recorded from that client;
// the headers net/http adds itself (User-Agent, Accept-Encoding,
// Content-Length) are the core's client's as they were the plugin's. A
// signed header the host added or changed would show here and break the
// signature upstream.
func TestRequestIsTheReleasedOne(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	api.now = func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) }
	cfg := &AssetConfig{AccountID: 7, AccessKey: testAK, SecretKey: testSK,
		BaseURL: "http://ark.cn-beijing.volcengineapi.com", Region: DefaultAssetRegion}
	body := map[string]any{"Filter": map[string]any{"GroupIds": []string{"g-1"}}, "PageNumber": 1, "PageSize": 100}
	if _, err := api.call(context.Background(), cfg, ActionListAssets, body); err != nil {
		t.Fatal(err)
	}
	const (
		wantBody = `{"Filter":{"GroupIds":["g-1"]},"PageNumber":1,"PageSize":100}`
		wantAuth = "HMAC-SHA256 Credential=AKLTtest0000000000000000/20261118/cn-beijing/ark/request, " +
			"SignedHeaders=content-type;host;x-content-sha256;x-date, " +
			"Signature=2e6fcf2f0e9a22f1b32f09092bd2ba7630fe5f2ecc2e92be5bf969e58316760b"
	)
	plugin := map[string]string{
		"Accept":           "application/json",
		"Authorization":    wantAuth,
		"Content-Type":     "application/json; charset=utf-8",
		"X-Content-Sha256": "9f28663d57d04e7eaf3b3ce542c9c0c425dabb2476a9ce4de3d691ffe29cf78d",
		"X-Date":           "20261118T100000Z",
	}
	in := f.sent[0]
	if in.GetMethod() != http.MethodPost || in.GetUrl() != "http://ark.cn-beijing.volcengineapi.com/?Action=ListAssets&Version=2024-01-01" ||
		!maps.Equal(in.GetHeaders(), plugin) || string(in.GetBody()) != wantBody {
		t.Fatalf("handed to the host: %s %s %v %s", in.GetMethod(), in.GetUrl(), in.GetHeaders(), in.GetBody())
	}
	c := f.last(t)
	wire := map[string]string{"Accept-Encoding": "gzip", "Content-Length": "61", "User-Agent": "Go-http-client/1.1"}
	maps.Copy(wire, plugin)
	got := map[string]string{}
	for k, v := range c.Header {
		got[k] = strings.Join(v, ",")
	}
	if c.Method != http.MethodPost || c.Path != "/" || c.Query.Encode() != "Action=ListAssets&Version=2024-01-01" ||
		c.Host != "ark.cn-beijing.volcengineapi.com" || !maps.Equal(got, wire) || string(c.Body) != wantBody {
		t.Fatalf("upstream got %s %s?%s host %s %v %s", c.Method, c.Path, c.Query.Encode(), c.Host, got, c.Body)
	}
	verifySignature(t, c, testAK, testSK, DefaultAssetRegion, AssetServiceName)
}

// TestSignatureBindsSecretRegionAndBody: the signature is not a constant. A
// different secret key, a different signing region and a different body each
// change it - which is what makes it a signature and not decoration.
func TestSignatureBindsSecretRegionAndBody(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	ctx := context.Background()

	sig := func() string {
		return authRe.FindStringSubmatch(f.last(t).Header.Get("Authorization"))[6]
	}

	base := f.config()
	if _, err := api.call(ctx, base, ActionListAssetGroups, map[string]any{"PageNumber": 1, "PageSize": 10}); err != nil {
		t.Fatalf("call: %v", err)
	}
	first := sig()
	verifySignature(t, f.last(t), testAK, testSK, DefaultAssetRegion, AssetServiceName)

	other := *base
	other.SecretKey = testSK + "x"
	if _, err := api.call(ctx, &other, ActionListAssetGroups, map[string]any{"PageNumber": 1, "PageSize": 10}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if sig() == first {
		t.Error("the signature does not depend on the secret key")
	}

	region := *base
	region.Region = BytePlusAssetRegion
	if _, err := api.call(ctx, &region, ActionListAssetGroups, map[string]any{"PageNumber": 1, "PageSize": 10}); err != nil {
		t.Fatalf("call: %v", err)
	}
	verifySignature(t, f.last(t), testAK, testSK, BytePlusAssetRegion, AssetServiceName)
	if sig() == first {
		t.Error("the signature does not depend on the signing region")
	}

	if _, err := api.call(ctx, base, ActionListAssetGroups, map[string]any{"PageNumber": 2, "PageSize": 10}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if sig() == first {
		t.Error("the signature does not cover the request body")
	}
	verifySignature(t, f.last(t), testAK, testSK, DefaultAssetRegion, AssetServiceName)
}

// TestEveryActionIsSigned walks all ten Actions: each one is accepted, sent
// with its own Action parameter and correctly signed.
func TestEveryActionIsSigned(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	if len(AssetActions) != 10 {
		t.Fatalf("AssetActions = %v, want ten (five per resource)", AssetActions)
	}
	for _, action := range AssetActions {
		if _, err := api.call(context.Background(), f.config(), action, map[string]any{"Id": "x-1"}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		c := f.last(t)
		if c.Query.Get("Action") != action {
			t.Errorf("%s: query Action = %q", action, c.Query.Get("Action"))
		}
		verifySignature(t, c, testAK, testSK, DefaultAssetRegion, AssetServiceName)
	}
}

// TestUnknownActionNeverReachesUpstream: a route can never talk the plugin
// into signing an arbitrary control-plane call with an account's AK/SK.
func TestUnknownActionNeverReachesUpstream(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	_, err := api.call(context.Background(), f.config(), "DeleteEndpoint", map[string]any{})
	if err == nil || err.Code != "InvalidAction" {
		t.Fatalf("err = %v, want InvalidAction", err)
	}
	f.mu.Lock()
	n := len(f.requests)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("upstream saw %d requests for an unknown action", n)
	}
}

// TestUpstreamErrorClassification covers the three cases the routes branch
// on. Note the first one: volcengine reports a missing resource inside an
// HTTP 200 as well, so NotFound cannot be a status-code check alone.
func TestUpstreamErrorClassification(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		notFound      bool
		denied        bool
		throttled     bool
		wantCode      string
		wantSubstring string
	}{
		{
			name:     "not found inside a 200",
			status:   http.StatusOK,
			body:     `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"NotFound.AssetGroup","Message":"group not found"}}}`,
			notFound: true, wantCode: "NotFound.AssetGroup", wantSubstring: "group not found",
		},
		{
			name:     "404",
			status:   http.StatusNotFound,
			body:     `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"ResourceNotFound","Message":"gone"}}}`,
			notFound: true, wantCode: "ResourceNotFound",
		},
		{
			name:   "wrong signature",
			status: http.StatusForbidden,
			body:   `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"SignatureDoesNotMatch","Message":"bad signature"}}}`,
			denied: true, wantCode: "SignatureDoesNotMatch",
		},
		{
			name:      "throttled",
			status:    http.StatusTooManyRequests,
			body:      `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"FlowLimitExceeded","Message":"slow down"}}}`,
			throttled: true, wantCode: "FlowLimitExceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeArk(t)
			f.reply = func(capturedRequest) (int, string) { return tc.status, tc.body }
			api := newArkAPI(f.send)
			_, err := api.call(context.Background(), f.config(), ActionGetAssetGroup, map[string]any{"Id": "g-1"})
			if err == nil {
				t.Fatal("want an error")
			}
			if err.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", err.Code, tc.wantCode)
			}
			if tc.wantSubstring != "" && !strings.Contains(err.Message, tc.wantSubstring) {
				t.Errorf("message = %q, want it to contain %q", err.Message, tc.wantSubstring)
			}
			if err.NotFound() != tc.notFound {
				t.Errorf("NotFound() = %v, want %v (%+v)", err.NotFound(), tc.notFound, err)
			}
			if err.Denied() != tc.denied {
				t.Errorf("Denied() = %v, want %v (%+v)", err.Denied(), tc.denied, err)
			}
			if err.Throttled() != tc.throttled {
				t.Errorf("Throttled() = %v, want %v (%+v)", err.Throttled(), tc.throttled, err)
			}
			// The error text must not carry the secret key.
			if strings.Contains(err.Error(), testSK) {
				t.Fatal("the upstream error message leaks the secret key")
			}
		})
	}
}

// TestCanceledContextNeverDials: a request that is already dead must not reach
// upstream at all.
func TestCanceledContextNeverDials(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.call(ctx, f.config(), ActionListAssets, nil); err == nil || err.Code != "Canceled" {
		t.Fatalf("err = %v, want Canceled", err)
	}
	f.mu.Lock()
	n := len(f.requests)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("upstream saw %d requests for a cancelled context", n)
	}
}

// TestCancelAbortsTheCallInFlight is the defect PLUGIN-VOLCENGINE-ARK §10.3
// recorded against the vendor SDK, asserted directly rather than by its
// absence: universal.DoCall took no context, so a console request that went
// away left a goroutine and a socket in hc.Do until the client timeout - up to
// 30 seconds each, in a plugin allowed 128 open files.
//
// The proof is on BOTH sides, because "call returned quickly" alone would also
// be true of a signer that abandoned the request and left it running: the
// upstream handler is asked whether ITS request context was cancelled. That is
// the socket going away.
func TestCancelAbortsTheCallInFlight(t *testing.T) {
	arrived := make(chan struct{})
	serverSaw := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body has to be drained before the wait: net/http only starts the
		// background read that cancels r.Context() on a client disconnect once
		// the handler is done with the request body. A handler that ignores it
		// never learns the client went away - which would make this test fail
		// for a reason that has nothing to do with the code under test.
		_, _ = io.Copy(io.Discard, r.Body)
		close(arrived)
		select {
		case <-r.Context().Done():
			serverSaw <- "cancelled"
		case <-time.After(10 * time.Second):
			// Long enough to be a real answer about the connection, short
			// enough not to hang the suite.
			serverSaw <- "still connected after 10s"
		}
	}))
	defer srv.Close()

	api := newArkAPI(hostSend(srv.Listener.Addr().String()))
	cfg := &AssetConfig{
		AccountID: 7, AccessKey: testAK, SecretKey: testSK,
		BaseURL: srv.URL, Region: DefaultAssetRegion,
	}

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		err     *ArkError
		elapsed time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		// A Get, so the retry ladder is in play too: cancelling must stop the
		// whole Action, not just the attempt in flight.
		_, err := api.call(ctx, cfg, ActionGetAsset, map[string]any{"Id": "a-1"})
		done <- outcome{err, time.Since(start)}
	}()

	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the upstream call never arrived")
	}
	cancel()

	select {
	case got := <-done:
		if got.err == nil || got.err.Code != "Canceled" {
			t.Fatalf("err = %v, want Canceled", got.err)
		}
		// The call has no deadline of its own: only the cancellation can
		// have ended it.
		if got.elapsed > 5*time.Second {
			t.Fatalf("the call took %v to notice the cancellation", got.elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the call did not return after its context was cancelled")
	}
	if saw := <-serverSaw; saw != "cancelled" {
		t.Fatalf("upstream says: %s - the request was abandoned, not aborted", saw)
	}
}

// TestCreateIsNeverRetried is a money-adjacent rule about upstream state, not
// about money: a retried Create mints a SECOND group or asset upstream, and
// the create route only ever learns the id of the last attempt, so the first
// becomes an orphan no compensation can reach. The Actions that address a
// resource by id are retried, because sending them twice lands on the same
// state.
func TestCreateIsNeverRetried(t *testing.T) {
	cases := map[string]int{
		ActionCreateAssetGroup: 1,
		ActionCreateAsset:      1,
		ActionGetAssetGroup:    assetMaxAttempts,
		ActionListAssets:       assetMaxAttempts,
		ActionUpdateAsset:      assetMaxAttempts,
		ActionDeleteAsset:      assetMaxAttempts,
	}
	for action, want := range cases {
		t.Run(action, func(t *testing.T) {
			f := newFakeArk(t)
			f.reply = func(capturedRequest) (int, string) {
				return http.StatusServiceUnavailable, `{"ResponseMetadata":{"RequestId":"r"}}`
			}
			api := newArkAPI(f.send)
			if _, err := api.call(context.Background(), f.config(), action, map[string]any{"Id": "x"}); err == nil {
				t.Fatal("want an error")
			}
			f.mu.Lock()
			n := len(f.requests)
			f.mu.Unlock()
			if n != want {
				t.Fatalf("%s was sent %d times, want %d", action, n, want)
			}
		})
	}
}

// TestRetryRecoversAndDoesNotRetryAVerdict: a 503 that clears on the second
// try is not surfaced, and a 403 (a wrong signature, the same answer forever)
// is not asked about again.
func TestRetryRecoversAndDoesNotRetryAVerdict(t *testing.T) {
	f := newFakeArk(t)
	var n int
	f.reply = func(capturedRequest) (int, string) {
		n++
		if n == 1 {
			return http.StatusBadGateway, `bad gateway`
		}
		return http.StatusOK, `{"ResponseMetadata":{"RequestId":"r"},"Result":{"Id":"g-9"}}`
	}
	api := newArkAPI(f.send)
	result, err := api.call(context.Background(), f.config(), ActionGetAssetGroup, map[string]any{"Id": "g-9"})
	if err != nil {
		t.Fatalf("a 502 that cleared on retry surfaced as %v", err)
	}
	if groupFromResult(result).ID != "g-9" {
		t.Fatalf("result = %v", result)
	}

	f2 := newFakeArk(t)
	f2.reply = func(capturedRequest) (int, string) {
		return http.StatusForbidden, `{"ResponseMetadata":{"RequestId":"r","Error":{"Code":"SignatureDoesNotMatch","Message":"no"}}}`
	}
	api2 := newArkAPI(f2.send)
	if _, err := api2.call(context.Background(), f2.config(), ActionGetAssetGroup, map[string]any{"Id": "g-1"}); err == nil {
		t.Fatal("want an error")
	}
	f2.mu.Lock()
	sent := len(f2.requests)
	f2.mu.Unlock()
	if sent != 1 {
		t.Fatalf("a 403 was sent %d times; it answers the same way every time", sent)
	}
}

// TestNonEnvelopeAnswers: a proxy's HTML error page, an empty body and a JSON
// document that is not a volcengine envelope each have to become a readable
// ArkError rather than a panic or an empty success. The request goes through
// the host and the account's proxy, so "something in between answered instead
// of Ark" is a real case.
func TestNonEnvelopeAnswers(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantIn   string
	}{
		{"html 502 from something in between", http.StatusBadGateway,
			"<html>\n<body>502 Bad Gateway</body>\n</html>", "HTTPError", "502 Bad Gateway"},
		{"empty 500", http.StatusInternalServerError, "", "HTTPError", "(empty body)"},
		{"a 200 that is not JSON at all", http.StatusOK, "not json", "InternalError", "not json"},
		{"a 200 whose envelope is an array", http.StatusOK, "[1,2,3]", "InternalError", "[1,2,3]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeArk(t)
			f.reply = func(capturedRequest) (int, string) { return tc.status, tc.body }
			api := newArkAPI(f.send)
			// A Create, so the retry ladder does not multiply the assertions.
			_, err := api.call(context.Background(), f.config(), ActionCreateAsset, map[string]any{"URL": "u"})
			if err == nil {
				t.Fatal("want an error")
			}
			if err.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q (message %q)", err.Code, tc.wantCode, err.Message)
			}
			if !strings.Contains(err.Message, tc.wantIn) {
				t.Fatalf("message = %q, want it to contain %q", err.Message, tc.wantIn)
			}
			if strings.Contains(err.Error(), testSK) {
				t.Fatal("the error message leaks the secret key")
			}
		})
	}

	// A success with no Result is an empty object, never nil: every caller
	// indexes into it.
	f := newFakeArk(t)
	f.reply = func(capturedRequest) (int, string) {
		return http.StatusOK, `{"ResponseMetadata":{"RequestId":"r"}}`
	}
	api := newArkAPI(f.send)
	result, err := api.call(context.Background(), f.config(), ActionDeleteAsset, map[string]any{"Id": "a-1"})
	if err != nil {
		t.Fatalf("a Result-less success surfaced as %v", err)
	}
	if result == nil {
		t.Fatal("result is nil; callers index into it")
	}
}

// TestAnActionWithNoParametersSendsAnObject: an Action called with a nil body
// still has to carry "{}" - and that body is what the signature covers, so
// sending "null" while signing "{}" (or the reverse) is a 403.
func TestAnActionWithNoParametersSendsAnObject(t *testing.T) {
	f := newFakeArk(t)
	api := newArkAPI(f.send)
	if _, err := api.call(context.Background(), f.config(), ActionListAssets, nil); err != nil {
		t.Fatal(err)
	}
	c := f.last(t)
	if string(c.Body) != "{}" {
		t.Fatalf("body = %q, want {}", c.Body)
	}
	// The signature has to be over what was actually sent.
	verifySignature(t, c, testAK, testSK, DefaultAssetRegion, AssetServiceName)
}

// TestResultItemsAndTotal pins the tolerant reading of list results: the
// array key differs per resource upstream and TotalCount may be absent.
func TestResultItemsAndTotal(t *testing.T) {
	var result map[string]any
	dec := json.NewDecoder(strings.NewReader(
		`{"TotalCount":42,"Items":[{"Id":"a-1","Name":"one","Status":"Succeeded"},{"Id":"a-2"}]}`))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		t.Fatal(err)
	}
	items := resultItems(result, "Assets")
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	if a := assetFromResult(items[0]); a.ID != "a-1" || a.Name != "one" || a.Status != "Succeeded" {
		t.Fatalf("asset = %+v", a)
	}
	if got := totalOf(result, len(items)); got != 42 {
		t.Fatalf("total = %d, want 42", got)
	}
	if got := totalOf(map[string]any{}, 3); got != 3 {
		t.Fatalf("total without TotalCount = %d, want the item count 3", got)
	}
	if resultItems(map[string]any{"Items": "not an array"}) != nil {
		t.Error("a non-array Items must not be read as a list")
	}
}
