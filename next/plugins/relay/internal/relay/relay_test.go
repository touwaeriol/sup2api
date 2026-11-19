package relay

import (
	"context"
	"slices"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/standardtest"
)

// TestGolden replays the frozen answers of the released plugin, all of
// them except that it now refuses a key the account form never sends.
func TestGolden(t *testing.T) {
	standardtest.Run(t, "relay", "../../manifest.json", New(),
		goldentest.Except("validate valid with other fields", "the account form's keys only: the others are refused"))
}

// TestDifferences pins, on the plugin's own output, the ways relay_key
// differs from the anthropic preset (the removed upstream declaration).
func TestDifferences(t *testing.T) {
	if err := standard.Check(RelayKey()); err != nil {
		t.Fatal(err)
	}
	const key, base = "sk-relay-0123456789", "https://relay.example.com"
	body := `{"model":"m","service_tier":"auto","inference_geo":"us","speed":"fast"}`
	build := func(settings, protocol string) (*pluginv1.UpstreamRequest, error) {
		return New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account:        &pluginv1.Account{Id: 1, Type: TypeRelayKey, CredentialsJson: `{"api_key":"` + key + `"}`, SettingsJson: settings},
			Meta:           &pluginv1.RequestMeta{Protocol: protocol, Model: "m"},
			InboundHeaders: map[string]string{"anthropic-beta": "b", "anthropic-version": "v"}, // the manifest's passHeaders
			Body:           []byte(body),
		})
	}

	// The base URL is required, has no default, and the form says so in the
	// relay's words.
	if _, err := build(`{}`, anthropic.ProtocolMessages); err == nil || !strings.Contains(err.Error(), "invalid base_url: is required") {
		t.Errorf("no base URL: err = %v", err)
	}
	v, err := New().ValidateCredentials(context.Background(), &pluginv1.ValidateCredentialsRequest{
		AccountType: TypeRelayKey, CredentialsJson: `{"api_key":"` + key + `"}`})
	if err != nil || len(v.GetErrors()) != 1 || v.GetErrors()[0].GetField() != "base_url" || v.GetErrors()[0].GetMessage() != BaseURLRequired {
		t.Errorf("validate without base URL = %v, %v", v.GetErrors(), err)
	}
	// No default protocol.
	if _, err := build(`{"base_url":"`+base+`"}`, ""); err == nil || !strings.Contains(err.Error(), `unsupported upstream protocol ""`) {
		t.Errorf("no protocol: err = %v", err)
	}
	// Every field forwarded, the client headers the core hands over too.
	got, err := build(`{"base_url":"`+base+`/v1"}`, anthropic.ProtocolMessages)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetUrl() != base+"/v1/messages" || string(got.GetBody()) != body || got.GetHeaders()["x-api-key"] != key {
		t.Errorf("request = %s %s %v", got.GetUrl(), got.GetBody(), got.GetHeaders())
	}
	headers := got.GetDefaultHeaders()
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"anthropic-beta", "anthropic-version", "content-type"}) || headers["anthropic-version"] != "v" {
		t.Errorf("default headers = %v", headers)
	}
}

// TestBaseURL: a request appends the route's whole path to the saved base
// URL less the one /v1 it may end in, so a pasted /v1 is not doubled, a
// relay whose own address ends in /v1 is reached at .../v1/v1, and an
// escaped "/" goes out as it was written (and is no version segment).
func TestBaseURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://relay.example.com":         "https://relay.example.com/v1/messages",
		"https://relay.example.com/v1":      "https://relay.example.com/v1/messages",
		"https://relay.example.com/v1/v1":   "https://relay.example.com/v1/v1/messages",
		"https://relay.example.com/p/a%2Fb": "https://relay.example.com/p/a%2Fb/v1/messages",
		"https://relay.example.com/a%2Fv1":  "https://relay.example.com/a%2Fv1/v1/messages",
	} {
		out, err := New().BuildUpstream(context.Background(), &pluginv1.ExecuteRequest{
			Account: &pluginv1.Account{Id: 1, Type: TypeRelayKey, CredentialsJson: `{"api_key":"sk-relay-test"}`, SettingsJson: `{"base_url":"` + base + `"}`},
			Meta:    &pluginv1.RequestMeta{Protocol: anthropic.ProtocolMessages, Model: "m"},
			Body:    []byte(`{}`),
		})
		if err != nil || out.GetUrl() != want {
			t.Errorf("%s: url %s, %v, want %s", base, out.GetUrl(), err, want)
		}
	}
}
