package vertex

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"maps"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// testNow is the fixed clock of the tests.
var testNow = time.Unix(1_800_000_000, 0)

// testKey is the RSA key of the test service account, generated once.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// pkcs8PEM is key the way Google writes it into a key file.
func pkcs8PEM(key *rsa.PrivateKey) string {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

const (
	testEmail   = "gateway@my-project-123456.iam.gserviceaccount.com"
	testKeyID   = "0123456789abcdef0123456789abcdef01234567"
	testProject = "my-project-123456"
)

// fileJSON is base with over written on top; a nil value removes the key.
func fileJSON(base, over map[string]any) string {
	m := maps.Clone(base)
	for k, v := range over {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// saJSON is a service account key file for testKey, with over on top.
func saJSON(over map[string]any) string {
	return fileJSON(map[string]any{
		"type":           "service_account",
		"project_id":     testProject,
		"private_key_id": testKeyID,
		"private_key":    pkcs8PEM(testKey()),
		"client_email":   testEmail,
		"client_id":      "123456789012345678901",
		"auth_uri":       "https://accounts.google.com/o/oauth2/auth",
		"token_uri":      TokenURL,
	}, over)
}

// userJSON is an application default credentials file, with over on top.
func userJSON(over map[string]any) string {
	return fileJSON(map[string]any{
		"type":             "authorized_user",
		"client_id":        "764086051850-example.apps.googleusercontent.com",
		"client_secret":    "d-FL95Q19q7MQmFpd7hHD0Ty",
		"refresh_token":    "1//0refresh-token-example",
		"quota_project_id": testProject,
	}, over)
}

// newTestPlugin is the plugin for ep with the fixed clock.
func newTestPlugin(ep Endpoints) *Plugin {
	p := NewWithEndpoints(ep)
	p.now = func() time.Time { return testNow }
	return p
}

// startPlugin runs p in the SDK harness: over gRPC, against the fake host,
// the way the core calls it.
func startPlugin(t *testing.T, p *Plugin) *pluginsdktest.Harness {
	t.Helper()
	m, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	// The fake host knows the manifest: it relays only to the hosts the
	// core would.
	return pluginsdktest.Start(t, p, pluginsdktest.Options{Manifest: m, SDK: []pluginsdk.Option{pluginsdk.WithInfo("vertex", Version)}})
}

// noDial fails t when anything dials through the default HTTP transport
// until t ends: the plugin opens no socket, the core sends every request.
func noDial(t *testing.T) {
	t.Helper()
	saved := http.DefaultTransport
	http.DefaultTransport = &http.Transport{DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
		t.Errorf("dialed %s %s", network, address)
		return nil, errors.New("this test opens no connection")
	}}
	t.Cleanup(func() { http.DefaultTransport = saved })
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
	return &pluginv1.Account{Id: 7, Platform: PlatformID, Type: typ, CredentialsJson: mustJSON(creds), SettingsJson: mustJSON(settings)}
}

// fullTarget are the settings of an account in a region.
var fullTarget = map[string]any{fieldProject: testProject, fieldLocation: "us-central1"}

// saAccount is a service account account holding an access token.
func saAccount(settings map[string]any) *pluginv1.Account {
	return stored(TypeServiceAccount, map[string]any{"service_account_json": saJSON(nil), fieldAccessToken: "ya29.token"}, settings)
}

// validate calls ValidateCredentials and decodes what the core would store.
func validate(t *testing.T, typ string, creds, settings map[string]any) (resp *pluginv1.ValidateCredentialsResponse, outCreds, outSettings map[string]any) {
	t.Helper()
	resp, err := newTestPlugin(Endpoints{}).ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{
		AccountType: typ, CredentialsJson: mustJSON(creds), SettingsJson: mustJSON(settings),
	})
	if err != nil {
		t.Fatalf("ValidateCredentials: %v", err)
	}
	return resp, normalized(t, resp.GetNormalizedCredentialsJson(), creds), normalized(t, resp.GetNormalizedSettingsJson(), settings)
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

// fieldErrors renders the errors as "field:code".
func fieldErrors(resp *pluginv1.ValidateCredentialsResponse) []string {
	var out []string
	for _, e := range resp.GetErrors() {
		out = append(out, e.GetField()+":"+e.GetCode())
	}
	return out
}

func validateRequest(typ, creds, settings string) *pluginv1.ValidateCredentialsRequest {
	return &pluginv1.ValidateCredentialsRequest{AccountType: typ, CredentialsJson: creds, SettingsJson: settings}
}
