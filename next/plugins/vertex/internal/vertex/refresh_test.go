package vertex

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// verifyAssertion checks an RS256 JWT against pub and returns its header and
// claims. It is written against the JWT format with crypto/rsa, not with
// the library that signs the assertion.
func verifyAssertion(t *testing.T, assertion string, pub *rsa.PublicKey) (header, claims map[string]any) {
	t.Helper()
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature encoding: %v", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	for i, dst := range []*map[string]any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("part %d encoding: %v", i, err)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
	}
	return header, claims
}

func buildRefresh(t *testing.T, p *Plugin, acc *pluginv1.Account) (*pluginv1.BuildRefreshRequestResponse, url.Values) {
	t.Helper()
	req, err := p.BuildRefreshRequest(context.Background(), &pluginv1.BuildRefreshRequestRequest{Account: acc})
	if err != nil {
		t.Fatalf("BuildRefreshRequest: %v", err)
	}
	form, err := url.ParseQuery(req.GetBodyJson())
	if err != nil {
		t.Fatalf("body is not a form: %v", err)
	}
	return req, form
}

func TestServiceAccountRefreshRequest(t *testing.T) {
	// A new account: no token yet, expires_at as ValidateCredentials left it.
	acc := stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldExpiresAt: expiredAt}, fullTarget)
	req, form := buildRefresh(t, newTestPlugin(Endpoints{}), acc)

	if req.GetMethod() != http.MethodPost || req.GetUrl() != TokenURL {
		t.Fatalf("request = %s %s", req.GetMethod(), req.GetUrl())
	}
	if req.GetHeaders()["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers = %v", req.GetHeaders())
	}
	if len(form) != 2 || form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Fatalf("form = %v", form)
	}
	header, claims := verifyAssertion(t, form.Get("assertion"), &testKey().PublicKey)
	if len(header) != 3 || header["alg"] != "RS256" || header["typ"] != "JWT" || header["kid"] != testKeyID {
		t.Fatalf("header = %v", header)
	}
	// Issued ten seconds ago, so a clock running fast is not refused, and
	// valid for Google's maximum of one hour from then.
	iat := float64(testNow.Unix() - 10)
	if len(claims) != 5 || claims["iss"] != testEmail || claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" ||
		claims["aud"] != "https://oauth2.googleapis.com/token" || claims["iat"] != iat || claims["exp"] != iat+3600 {
		t.Fatalf("claims = %v", claims)
	}
	// The private key never leaves the plugin.
	if strings.Contains(req.GetBodyJson(), "PRIVATE KEY") || strings.Contains(req.String(), "PRIVATE") {
		t.Fatal("the request carries the private key")
	}

	// A changed payload no longer verifies.
	parts := strings.Split(form.Get("assertion"), ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"attacker"}`))
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + forged))
	if rsa.VerifyPKCS1v15(&testKey().PublicKey, crypto.SHA256, sum[:], sig) == nil {
		t.Fatal("a forged payload verifies")
	}
}

// The token endpoint and the audience stay Google's whatever the test
// endpoint is; a key file without kid still signs; PKCS #1 keys are read.
func TestServiceAccountRefreshVariants(t *testing.T) {
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey())}))
	acc := stored(TypeServiceAccount, map[string]any{
		"service_account_json": saJSON(map[string]any{"private_key": pkcs1, "private_key_id": nil}),
	}, fullTarget)
	req, form := buildRefresh(t, newTestPlugin(Endpoints{Token: "http://127.0.0.1:1/token"}), acc)
	if req.GetUrl() != "http://127.0.0.1:1/token" {
		t.Fatalf("url = %s", req.GetUrl())
	}
	header, claims := verifyAssertion(t, form.Get("assertion"), &testKey().PublicKey)
	if _, has := header["kid"]; has || claims["aud"] != TokenURL {
		t.Fatalf("header = %v claims = %v", header, claims)
	}
}

func TestAuthorizedUserRefreshRequest(t *testing.T) {
	acc := stored(TypeAuthorizedUser, map[string]any{"authorized_user_json": userJSON(nil), fieldExpiresAt: expiredAt}, fullTarget)
	req, form := buildRefresh(t, newTestPlugin(Endpoints{}), acc)
	if req.GetMethod() != http.MethodPost || req.GetUrl() != TokenURL || req.GetHeaders()["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("request = %s %s %v", req.GetMethod(), req.GetUrl(), req.GetHeaders())
	}
	want := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"764086051850-example.apps.googleusercontent.com"},
		"client_secret": {"d-FL95Q19q7MQmFpd7hHD0Ty"},
		"refresh_token": {"1//0refresh-token-example"},
	}
	if form.Encode() != want.Encode() {
		t.Fatalf("form = %v", form)
	}
}

func TestRefreshRequestRefusals(t *testing.T) {
	p := newTestPlugin(Endpoints{})
	for _, tc := range []struct {
		name string
		acc  *pluginv1.Account
		want codes.Code
	}{
		// The core records FAILED_PRECONDITION like a refusal: nothing is
		// retried until the credentials change.
		{"unreadable key file", stored(TypeServiceAccount, map[string]any{"service_account_json": "{"}, fullTarget), codes.FailedPrecondition},
		{"no key file", stored(TypeAuthorizedUser, map[string]any{}, fullTarget), codes.FailedPrecondition},
		{"no project", stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil)}, nil), codes.FailedPrecondition},
		{"unknown type", stored("vertex_other", map[string]any{}, fullTarget), codes.FailedPrecondition},
		{"api key type", stored(TypeAPIKey, map[string]any{fieldAPIKey: "AQ.Ab8RN6-example"}, nil), codes.InvalidArgument},
		{"credentials not JSON", &pluginv1.Account{Type: TypeServiceAccount, CredentialsJson: "{"}, codes.InvalidArgument},
	} {
		_, err := p.BuildRefreshRequest(context.Background(), &pluginv1.BuildRefreshRequestRequest{Account: tc.acc})
		if status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE") {
			t.Errorf("%s: the error echoes the key: %v", tc.name, err)
		}
	}
}

func TestParseRefreshResponse(t *testing.T) {
	const (
		ok        = pluginv1.RefreshResult_ERROR_TYPE_UNSPECIFIED
		rejected  = pluginv1.RefreshResult_ERROR_TYPE_AUTH_REJECTED
		transient = pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT
	)
	accounts := map[string]*pluginv1.Account{
		TypeServiceAccount: stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldExpiresAt: expiredAt}, fullTarget),
		TypeAuthorizedUser: stored(TypeAuthorizedUser, map[string]any{"authorized_user_json": userJSON(nil), fieldExpiresAt: expiredAt}, fullTarget),
	}
	saKey, _ := parseServiceAccount(saJSON(nil))
	userKey, _ := parseAuthorizedUser(userJSON(nil))
	fingerprints := map[string]string{TypeServiceAccount: saKey.fingerprint(), TypeAuthorizedUser: userKey.fingerprint()}

	for _, tc := range []struct {
		name      string
		in        *pluginv1.ParseRefreshResponseRequest
		want      pluginv1.RefreshResult_ErrorType
		expiresIn int64 // of a success
	}{
		{"token", &pluginv1.ParseRefreshResponseRequest{Status: 200,
			Body: []byte(`{"access_token":"ya29.new","expires_in":3599,"token_type":"Bearer"}`)}, ok, 3599},
		{"token with id_token and scope", &pluginv1.ParseRefreshResponseRequest{Status: 200,
			Body: []byte(`{"access_token":" ya29.new ","expires_in":3599,"scope":"https://www.googleapis.com/auth/cloud-platform","id_token":"x.y.z"}`)}, ok, 3599},
		{"token without expires_in", &pluginv1.ParseRefreshResponseRequest{Status: 200, Body: []byte(`{"access_token":"ya29.new"}`)}, ok, 3600},
		{"200 without token", &pluginv1.ParseRefreshResponseRequest{Status: 200, Body: []byte(`{"token_type":"Bearer"}`)}, transient, 0},
		{"200 not JSON", &pluginv1.ParseRefreshResponseRequest{Status: 200, Body: []byte(`<html>`)}, transient, 0},
		{"transport error", &pluginv1.ParseRefreshResponseRequest{TransportError: "dial tcp: i/o timeout"}, transient, 0},

		{"invalid_grant: key deleted", &pluginv1.ParseRefreshResponseRequest{Status: 400,
			Body: []byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)}, rejected, 0},
		{"invalid_grant: account not found", &pluginv1.ParseRefreshResponseRequest{Status: 400,
			Body: []byte(`{"error":"invalid_grant","error_description":"Invalid grant: account not found"}`)}, rejected, 0},
		{"invalid_grant: refresh token revoked", &pluginv1.ParseRefreshResponseRequest{Status: 400,
			Body: []byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)}, rejected, 0},
		{"invalid_grant without description", &pluginv1.ParseRefreshResponseRequest{Status: 400, Body: []byte(`{"error":"invalid_grant"}`)}, rejected, 0},
		{"invalid_client", &pluginv1.ParseRefreshResponseRequest{Status: 400,
			Body: []byte(`{"error":"invalid_client","error_description":"The OAuth client was not found."}`)}, rejected, 0},
		{"401", &pluginv1.ParseRefreshResponseRequest{Status: 401,
			Body: []byte(`{"error":"invalid_client","error_description":"Unauthorized"}`)}, rejected, 0},
		{"403", &pluginv1.ParseRefreshResponseRequest{Status: 403,
			Body: []byte(`{"error":"access_denied","error_description":"Account restricted"}`)}, rejected, 0},

		// The node's clock, not the key.
		{"invalid_grant: clock skew", &pluginv1.ParseRefreshResponseRequest{Status: 400, Body: []byte(
			`{"error":"invalid_grant","error_description":"Invalid JWT: Token must be a short-lived token (60 minutes) and in a reasonable timeframe. Check your iat and exp values in the JWT claim."}`)}, transient, 0},
		{"invalid_request", &pluginv1.ParseRefreshResponseRequest{Status: 400,
			Body: []byte(`{"error":"invalid_request","error_description":"Missing required parameter: grant_type"}`)}, transient, 0},
		{"400 not JSON", &pluginv1.ParseRefreshResponseRequest{Status: 400, Body: []byte(`Bad Request`)}, transient, 0},
		{"429", &pluginv1.ParseRefreshResponseRequest{Status: 429, Body: []byte(`{"error":"rate_limit_exceeded"}`)}, transient, 0},
		{"500", &pluginv1.ParseRefreshResponseRequest{Status: 500, Body: []byte(`{"error":"internal_failure"}`)}, transient, 0},
		{"503 with an invalid_grant body", &pluginv1.ParseRefreshResponseRequest{Status: 503, Body: []byte(`{"error":"invalid_grant"}`)}, transient, 0},
	} {
		for typ, acc := range accounts {
			t.Run(tc.name+"/"+typ, func(t *testing.T) {
				res, err := newTestPlugin(Endpoints{}).ParseRefreshResponse(context.Background(), &pluginv1.ParseRefreshResponseRequest{
					Account: acc, Status: tc.in.GetStatus(), Body: tc.in.GetBody(),
					TransportError: tc.in.GetTransportError(),
				})
				if err != nil {
					t.Fatalf("ParseRefreshResponse: %v", err)
				}
				if res.GetErrorType() != tc.want {
					t.Fatalf("error type = %s (%s), want %s", res.GetErrorType(), res.GetErrorMessage(), tc.want)
				}
				if tc.want != ok {
					if res.GetCredentialsPatchJson() != "" || res.GetErrorMessage() == "" || len(res.GetErrorMessage()) > 512 {
						t.Fatalf("failure = %+v", res)
					}
					return
				}
				var patch map[string]any
				if err := json.Unmarshal([]byte(res.GetCredentialsPatchJson()), &patch); err != nil {
					t.Fatalf("patch: %v", err)
				}
				if len(patch) != 3 || patch[fieldAccessToken] != "ya29.new" || patch[fieldTokenFor] != fingerprints[typ] ||
					patch[fieldExpiresAt] != float64(testNow.Unix()+tc.expiresIn) {
					t.Fatalf("patch = %v", patch)
				}
			})
		}
	}
}

// A long upstream answer is clipped to what the core stores.
func TestParseRefreshResponseClipsMessage(t *testing.T) {
	res, err := newTestPlugin(Endpoints{}).ParseRefreshResponse(context.Background(), &pluginv1.ParseRefreshResponseRequest{
		Account: saAccount(fullTarget), Status: 502, Body: []byte(strings.Repeat("x", 4000)),
	})
	if err != nil || res.GetErrorType() != pluginv1.RefreshResult_ERROR_TYPE_TRANSIENT || len(res.GetErrorMessage()) > 512 {
		t.Fatalf("%v %d", err, len(res.GetErrorMessage()))
	}
}
