package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// fakeGoogle is Google's token endpoint and a Vertex AI host. It issues
// tokens only for an assertion signed by testKey (or the test user's refresh
// token) and serves models only to the token it issued last.
type fakeGoogle struct {
	t      *testing.T
	srv    *httptest.Server
	issued atomic.Int64
	// userToken says the last token was issued for user credentials, which
	// the model endpoints serve only with a quota project.
	userToken atomic.Bool
	// apiStatus, when not 0, is what the model endpoints answer.
	apiStatus atomic.Int32
	// served are the model requests answered, in order.
	mu     sync.Mutex
	served []string
}

// paths are the model requests answered so far.
func (g *fakeGoogle) paths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.served)
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", g.token)
	mux.HandleFunc("POST /v1/", g.model)
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) currentToken() string { return fmt.Sprintf("ya29.issued-%d", g.issued.Load()) }

func (g *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_request"}`)
		return
	}
	switch r.PostForm.Get("grant_type") {
	case grantJWTBearer:
		// verifyAssertion fails the test on a bad signature.
		_, claims := verifyAssertion(g.t, r.PostForm.Get("assertion"), &testKey().PublicKey)
		if claims["iss"] != testEmail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Invalid grant: account not found"}`)
			return
		}
	case grantRefreshToken:
		if r.PostForm.Get("refresh_token") != "1//0refresh-token-example" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"unsupported_grant_type"}`)
		return
	}
	g.userToken.Store(r.PostForm.Get("grant_type") == grantRefreshToken)
	g.issued.Add(1)
	_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":3599,"token_type":"Bearer"}`, g.currentToken())
}

func (g *fakeGoogle) model(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	authorized := r.Header.Get("Authorization") == "Bearer "+g.currentToken() || r.Header.Get("x-goog-api-key") == "AQ.Ab8RN6-example"
	switch {
	case !authorized:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(googleError(401, "UNAUTHENTICATED", "Request had invalid authentication credentials."))
		return
	case g.userToken.Load() && r.Header.Get("x-goog-user-project") != testProject:
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(googleError(403, "PERMISSION_DENIED", "The aiplatform.googleapis.com API requires a quota project, which is not set by default."))
		return
	case g.apiStatus.Load() != 0:
		w.WriteHeader(int(g.apiStatus.Load()))
		_, _ = w.Write(googleError(int(g.apiStatus.Load()), "RESOURCE_EXHAUSTED", "Quota exceeded"))
		return
	}
	g.mu.Lock()
	g.served = append(g.served, r.URL.RequestURI())
	g.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"pong\"}]}}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1}}\n\n")
		return
	}
	_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"pong"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`)
}

// coreSend sends a request the plugin described, the way the core does, and
// returns the status and body. Each header layer overwrites the ones before
// it: a relayed request's default headers, then its authoritative ones.
func coreSend(t *testing.T, method, address string, body []byte, layers ...map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, address, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range layers {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// forward is one gateway request for acc: Execute in the harness, then the
// core's send of the one relayed request.
func forward(t *testing.T, h *pluginsdktest.Harness, acc *pluginv1.Account, protocol, model string) (int, []byte, error) {
	t.Helper()
	ex := h.Execute(context.Background(), executeRequest(acc, protocol, model))
	if ex.Err != nil {
		return 0, nil, ex.Err
	}
	r := ex.Relayed(t).Request
	st, body := coreSend(t, r.GetMethod(), r.GetUrl(), r.GetBody(), r.GetDefaultHeaders(), r.GetHeaders())
	return st, body, nil
}

// coreAccount is what the core stores for an account and hands back to the
// plugin: the normalized credentials and settings, with refresh patches
// merged in (CONTRACTS §48.2).
type coreAccount struct {
	typ      string
	creds    map[string]any
	settings map[string]any
}

func (a *coreAccount) proto() *pluginv1.Account { return stored(a.typ, a.creds, a.settings) }

// save runs the submitted fields through ValidateCredentials and keeps what
// the core would store.
func (a *coreAccount) save(t *testing.T, h *pluginsdktest.Harness, creds, settings map[string]any) {
	t.Helper()
	resp, err := h.Platform.ValidateCredentials(context.Background(), validateRequest(a.typ, mustJSON(creds), mustJSON(settings)))
	if err != nil || len(resp.GetErrors()) > 0 {
		t.Fatalf("save: %v %v", fieldErrors(resp), err)
	}
	a.creds = normalized(t, resp.GetNormalizedCredentialsJson(), creds)
	a.settings = normalized(t, resp.GetNormalizedSettingsJson(), settings)
}

// refresh is one credential refresh of the core: build, send, parse, merge.
func (a *coreAccount) refresh(t *testing.T, h *pluginsdktest.Harness) *pluginv1.RefreshResult {
	t.Helper()
	ctx := context.Background()
	req, err := h.Platform.BuildRefreshRequest(ctx, &pluginv1.BuildRefreshRequestRequest{Account: a.proto()})
	if err != nil {
		t.Fatalf("BuildRefreshRequest: %v", err)
	}
	st, body := coreSend(t, req.GetMethod(), req.GetUrl(), []byte(req.GetBodyJson()), req.GetHeaders())
	res, err := h.Platform.ParseRefreshResponse(ctx, &pluginv1.ParseRefreshResponseRequest{Account: a.proto(), Status: int32(st), Body: body})
	if err != nil {
		t.Fatalf("ParseRefreshResponse: %v", err)
	}
	if res.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED {
		return res
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(res.GetCredentialsPatchJson()), &patch); err != nil {
		t.Fatalf("patch: %v", err)
	}
	a.creds = maps.Clone(a.creds)
	maps.Copy(a.creds, patch)
	return res
}

// call forwards one gateway request for the account.
func (a *coreAccount) call(t *testing.T, h *pluginsdktest.Harness, protocol, model string) (int, []byte, error) {
	t.Helper()
	return forward(t, h, a.proto(), protocol, model)
}

// startFakeGoogle runs the plugin in the SDK harness against the fake Google.
func startFakeGoogle(t *testing.T, g *fakeGoogle) *pluginsdktest.Harness {
	t.Helper()
	h := startPlugin(t, newTestPlugin(Endpoints{Token: g.srv.URL + "/token", API: g.srv.URL}))
	h.Host.ExternalServices = []string{"127.0.0.1"} // the fake Google stands in for googleapis.com
	return h
}

// TestKeyFileAccountRoundTrip follows a key file account from the form to an
// answered request: save, first refresh, requests, a renewal, an edit that
// keeps the token and a key replacement that drops it.
func TestKeyFileAccountRoundTrip(t *testing.T) {
	for typ, file := range map[string]string{TypeServiceAccount: saJSON(nil), TypeAuthorizedUser: userJSON(nil)} {
		t.Run(typ, func(t *testing.T) {
			g := newFakeGoogle(t)
			h := startFakeGoogle(t, g)
			field := keyKinds[typ].field
			a := &coreAccount{typ: typ}

			// Saved from the form: no token, an expiry that is already due.
			a.save(t, h, map[string]any{field: file}, map[string]any{fieldLocation: "us-central1"})
			if a.settings[fieldProject] != testProject || a.creds[fieldExpiresAt] != float64(expiredAt) {
				t.Fatalf("stored after save: %v %v", a.creds[fieldExpiresAt], a.settings)
			}
			if _, _, err := a.call(t, h, ProtocolGenerate, "gemini-2.5-flash"); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("request before the first token: %v", err)
			}

			// The first refresh brings the token.
			if res := a.refresh(t, h); res.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED {
				t.Fatalf("first refresh: %s %s", res.GetErrorType(), res.GetErrorMessage())
			}
			if a.creds[fieldAccessToken] != "ya29.issued-1" || a.creds[fieldExpiresAt] != float64(testNow.Unix()+3599) {
				t.Fatalf("stored after refresh: %v %v", a.creds[fieldAccessToken], a.creds[fieldExpiresAt])
			}
			for _, protocol := range []string{ProtocolGenerate, ProtocolStreamGenerate, ProtocolCountTokens} {
				st, body, err := a.call(t, h, protocol, "models/gemini-2.5-flash")
				if err != nil || st != http.StatusOK || !bytes.Contains(body, []byte("usageMetadata")) {
					t.Fatalf("%s: %d %s %v", protocol, st, body, err)
				}
			}
			const base = "/v1/projects/my-project-123456/locations/us-central1/publishers/google/models/gemini-2.5-flash"
			want := []string{base + ":generateContent", base + ":streamGenerateContent?alt=sse", base + ":countTokens"}
			if got := g.paths(); !slices.Equal(got, want) {
				t.Fatalf("paths = %v", got)
			}

			// A renewal replaces the token; the old one is refused upstream
			// and that 401 cools the account down without disabling it.
			old := a.proto()
			if res := a.refresh(t, h); res.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED || a.creds[fieldAccessToken] != "ya29.issued-2" {
				t.Fatalf("renewal: %s %v", res.GetErrorMessage(), a.creds[fieldAccessToken])
			}
			st, body, err := forward(t, h, old, ProtocolGenerate, "gemini-2.5-flash")
			if err != nil {
				t.Fatal(err)
			}
			cls, err := h.Platform.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
				Meta: &pluginv1.RequestMeta{Protocol: ProtocolGenerate}, Account: old, Status: int32(st), BodyPrefix: body,
			})
			if err != nil || st != http.StatusUnauthorized || cls.GetAction() != pluginv1.ClassifyErrorResponse_ACTION_FAILOVER ||
				cls.GetAccountEffect() != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN {
				t.Fatalf("stale token: %d %+v %v", st, cls, err)
			}

			// A 429 from the model endpoint cools the account down.
			g.apiStatus.Store(http.StatusTooManyRequests)
			st, body, err = a.call(t, h, ProtocolGenerate, "gemini-2.5-flash")
			g.apiStatus.Store(0)
			cls, cerr := h.Platform.ClassifyError(context.Background(), &pluginv1.ClassifyErrorRequest{
				Meta: &pluginv1.RequestMeta{Protocol: ProtocolGenerate}, Account: a.proto(), Status: int32(st), BodyPrefix: body,
			})
			if err != nil || cerr != nil || st != http.StatusTooManyRequests || cls.GetAccountEffect() != pluginv1.ClassifyErrorResponse_ACCOUNT_EFFECT_COOLDOWN {
				t.Fatalf("429: %d %+v %v %v", st, cls, err, cerr)
			}

			// Editing another field sends the stored credentials back: the
			// token stays and the account keeps working without a refresh.
			token := a.creds[fieldAccessToken]
			a.save(t, h, a.creds, map[string]any{fieldProject: testProject, fieldLocation: "global"})
			if a.creds[fieldAccessToken] != token {
				t.Fatal("editing the location dropped the token")
			}
			if st, _, err := a.call(t, h, ProtocolGenerate, "gemini-2.5-flash"); err != nil || st != http.StatusOK {
				t.Fatalf("request after the edit: %d %v", st, err)
			}

			// Replacing the key drops the token of the old one.
			replaced := maps.Clone(a.creds)
			replaced[field] = map[string]string{
				TypeServiceAccount: saJSON(map[string]any{"private_key_id": "ffff"}),
				TypeAuthorizedUser: userJSON(map[string]any{"refresh_token": "1//0revoked"}),
			}[typ]
			a.save(t, h, replaced, a.settings)
			if _, has := a.creds[fieldAccessToken]; has || a.creds[fieldExpiresAt] != float64(expiredAt) {
				t.Fatalf("stored after the key change: %v", a.creds[fieldExpiresAt])
			}
			if _, _, err := a.call(t, h, ProtocolGenerate, "gemini-2.5-flash"); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("request with the old key's token: %v", err)
			}
		})
	}
}

// A key Google refuses is reported as AUTH_REJECTED and leaves the stored
// credentials alone.
func TestRefusedKeyRoundTrip(t *testing.T) {
	g := newFakeGoogle(t)
	h := startFakeGoogle(t, g)
	for typ, file := range map[string]string{
		TypeServiceAccount: saJSON(map[string]any{"client_email": "deleted@my-project-123456.iam.gserviceaccount.com"}),
		TypeAuthorizedUser: userJSON(map[string]any{"refresh_token": "1//0revoked"}),
	} {
		a := &coreAccount{typ: typ}
		a.save(t, h, map[string]any{keyKinds[typ].field: file}, map[string]any{fieldProject: testProject})
		res := a.refresh(t, h)
		if res.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED || res.GetCredentialsPatchJson() != "" {
			t.Errorf("%s: %s %s", typ, res.GetErrorType(), res.GetErrorMessage())
		}
		if _, has := a.creds[fieldAccessToken]; has {
			t.Errorf("%s: a refused refresh stored a token", typ)
		}
	}
	if g.issued.Load() != 0 {
		t.Fatalf("tokens issued: %d", g.issued.Load())
	}
}

func TestAPIKeyAccountRoundTrip(t *testing.T) {
	g := newFakeGoogle(t)
	h := startFakeGoogle(t, g)
	a := &coreAccount{typ: TypeAPIKey}
	a.save(t, h, map[string]any{fieldAPIKey: "AQ.Ab8RN6-example"}, nil)
	if st, _, err := a.call(t, h, ProtocolStreamGenerate, "gemini-2.5-flash"); err != nil || st != http.StatusOK {
		t.Fatalf("express: %d %v", st, err)
	}
	a.save(t, h, a.creds, fullTarget)
	if st, _, err := a.call(t, h, ProtocolGenerate, "gemini-2.5-flash"); err != nil || st != http.StatusOK {
		t.Fatalf("with project: %d %v", st, err)
	}
	want := []string{
		"/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		"/v1/projects/my-project-123456/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent",
	}
	if got := g.paths(); !slices.Equal(got, want) {
		t.Fatalf("paths = %v", got)
	}
	// An API key account has no refresh: asking for one is an error, never
	// a token request.
	_, err := h.Platform.BuildRefreshRequest(context.Background(), &pluginv1.BuildRefreshRequestRequest{Account: a.proto()})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("refresh of an API key account: %v", err)
	}
}
