package bedrock

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
)

// testNow is the fixed clock of the tests.
var testNow = time.Unix(1_800_000_000, 0)

// Test credentials. The secret of an access key follows the convention of
// the mock upstream (deploy/mock-upstream/awswire.go): "mock-secret-" + id.
const (
	testAPIKey       = "ABSKQmVkcm9ja0FQSUtleS1leGFtcGxl"
	testAccessKeyID  = "AKIAIOSFODNN7EXAMPLE"
	testSTSKeyID     = "ASIAIOSFODNN7EXAMPLE"
	testSessionToken = "IQoJb3JpZ2luX2VjEXAMPLE//////////wEaCXVzLWVhc3QtMSJ"
	testHaiku        = "anthropic.claude-haiku-4-5-20251001-v1:0"
	testARN          = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abc123def456"
)

func mockSecret(accessKeyID string) string { return "mock-secret-" + accessKeyID }

// Submitted credentials of each type.
var (
	apiKeyCreds = map[string]any{fieldAPIKey: testAPIKey}
	akskCreds   = map[string]any{fieldAccessKeyID: testAccessKeyID, fieldSecretAccessKey: mockSecret(testAccessKeyID)}
	stsCreds    = map[string]any{fieldAccessKeyID: testSTSKeyID, fieldSecretAccessKey: mockSecret(testSTSKeyID), fieldSessionToken: testSessionToken}
	typeCreds   = map[string]map[string]any{TypeAPIKey: apiKeyCreds, TypeAccessKey: akskCreds, TypeSTS: stsCreds}
)

// newTestPlugin is the plugin for ep with the fixed clock.
func newTestPlugin(ep Endpoints) *Plugin {
	p := NewWithEndpoints(ep)
	p.now = func() time.Time { return testNow }
	return p
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// stored is an account as the core hands it to the plugin.
func stored(typ string, creds, settings map[string]any) *pluginv1.Account {
	return &pluginv1.Account{Id: 7, Type: typ, CredentialsJson: mustJSON(creds), SettingsJson: mustJSON(settings)}
}

// settingsOf are account settings: region and inference profile prefix
// ("" leaves a field out).
func settingsOf(region, prefix string) map[string]any {
	s := map[string]any{}
	if region != "" {
		s[fieldRegion] = region
	}
	if prefix != "" {
		s[fieldProfilePrefix] = prefix
	}
	return s
}

// claudeBody is a Messages request for model as the core hands it over:
// what Claude Code sends Anthropic, with the top-level fields InvokeModel
// refuses.
func claudeBody(model string, stream bool) string {
	return `{"model":` + mustJSON(model) + `,"max_tokens":64,"stream":` + mustJSON(stream) + `,"messages":[{"role":"user","content":"ping"}],` +
		`"metadata":{"user_id":"u-1"},"provider":{"order":["bedrock"]},"context_management":{"edits":[]},"output_config":{"effort":"low"},` +
		`"anthropic_beta":["claude-code-20250219","compact-2026-01-12"]}`
}

// executeRequest is an Execute call for the account: the core's meta and
// the whole body.
func executeRequest(acc *pluginv1.Account, model string, stream bool, body string) *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		Account: acc,
		Meta:    &pluginv1.RequestMeta{Protocol: ProtocolMessages, Model: model, Stream: stream},
		Body:    []byte(body),
	}
}

// build calls BuildUpstream of the production plugin.
func build(t *testing.T, in *pluginv1.ExecuteRequest) *pluginv1.UpstreamRequest {
	t.Helper()
	up, err := newTestPlugin(Endpoints{}).BuildUpstream(context.Background(), in)
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	return up
}

// fieldErrors renders validation errors as "field:code".
func fieldErrors(resp *pluginv1.ValidateCredentialsResponse) []string {
	var out []string
	for _, e := range resp.GetErrors() {
		out = append(out, e.GetField()+":"+e.GetCode())
	}
	return out
}

// normalized decodes the normalized JSON, or returns the input when the
// plugin left it as it was.
func normalized(t *testing.T, raw string, input map[string]any) map[string]any {
	t.Helper()
	if raw == "" {
		return input
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("normalized JSON %q: %v", raw, err)
	}
	return out
}

// ---------------------------------------------------------------- SigV4, recomputed
//
// An implementation of the server side of Signature Version 4 that shares
// nothing with sdk/reqsign: it recomputes the signature of a request as it
// arrived, the way the mock upstream does.

var sigV4Authorization = regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=([^/ ]+)/(\d{8})/([^/ ]+)/([^/ ]+)/aws4_request, ?SignedHeaders=([a-z0-9;-]+), ?Signature=([0-9a-f]{64})$`)

// sigV4 is what a verified Authorization header says.
type sigV4 struct {
	AccessKeyID, Region, Service, SignedHeaders string
}

// verifySigV4 checks the SigV4 signature of r, received with body at now,
// against the mock secret of its access key.
func verifySigV4(r *http.Request, body []byte, now time.Time) (sigV4, error) {
	m := sigV4Authorization.FindStringSubmatch(r.Header.Get("Authorization"))
	if m == nil {
		return sigV4{}, errors.New("the Authorization header is not AWS4-HMAC-SHA256")
	}
	sig := sigV4{AccessKeyID: m[1], Region: m[3], Service: m[4], SignedHeaders: m[5]}
	date, amzDate := m[2], r.Header.Get("X-Amz-Date")
	at, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil || !strings.HasPrefix(amzDate, date) {
		return sig, fmt.Errorf("x-amz-date %q does not fit the credential scope date %s", amzDate, date)
	}
	if age := now.Sub(at); age < -5*time.Minute || age > 5*time.Minute {
		return sig, fmt.Errorf("Signature expired: %s is now earlier than %s", amzDate, now.UTC().Format(time.RFC3339))
	}
	signed := ";" + sig.SignedHeaders + ";"
	for _, name := range []string{"host", "x-amz-date"} {
		if !strings.Contains(signed, ";"+name+";") {
			return sig, fmt.Errorf("%s must be signed", name)
		}
	}
	if r.Header.Get("X-Amz-Security-Token") != "" && !strings.Contains(signed, ";x-amz-security-token;") {
		return sig, errors.New("x-amz-security-token must be signed")
	}

	var headers strings.Builder
	for _, name := range strings.Split(sig.SignedHeaders, ";") {
		value := strings.Join(r.Header.Values(name), ",")
		if name == "host" {
			value = r.Host
		}
		headers.WriteString(name + ":" + strings.Join(strings.Fields(value), " ") + "\n")
	}
	// Every service but S3 escapes the path a second time.
	segments := strings.Split(r.URL.EscapedPath(), "/")
	for i, seg := range segments {
		segments[i] = awsEscape(seg)
	}
	var query []string
	for key, values := range r.URL.Query() {
		for _, v := range values {
			query = append(query, awsEscape(key)+"="+awsEscape(v))
		}
	}
	sort.Strings(query)
	canonical := strings.Join([]string{r.Method, strings.Join(segments, "/"), strings.Join(query, "&"),
		headers.String(), sig.SignedHeaders, sha256Hex(body)}, "\n")
	scope := []string{date, sig.Region, sig.Service, "aws4_request"}
	key := []byte("AWS4" + mockSecret(sig.AccessKeyID))
	for _, part := range scope {
		key = hmacSHA256(key, part)
	}
	want := hex.EncodeToString(hmacSHA256(key, strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, strings.Join(scope, "/"), sha256Hex([]byte(canonical))}, "\n")))
	if !hmac.Equal([]byte(want), []byte(m[6])) {
		return sig, errors.New("The request signature we calculated does not match the signature you provided.")
	}
	return sig, nil
}

// awsEscape percent-encodes everything but the RFC 3986 unreserved set.
func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func hmacSHA256(key []byte, s string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(s))
	return mac.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
