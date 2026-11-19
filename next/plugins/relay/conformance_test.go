package main

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/relay/internal/relay"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
)

func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform:    relay.New(),
		Manifest:    manifestJSON,
		Protocols:   []string{anthropic.ProtocolMessages, anthropic.ProtocolCountTokens},
		Credentials: []string{"x-api-key"},
	}, conformanceCases()...)
}

// conformanceCases are the requests the core sends: messages stream and
// non-stream, count_tokens non-stream (it has no stream form).
func conformanceCases() []conformance.Case {
	account := &pluginv1.Account{Id: 1, Type: relay.TypeRelayKey,
		CredentialsJson: `{"api_key":"sk-relay-conformance"}`, SettingsJson: `{"base_url":"https://relay.example.com"}`}
	request := func(protocol string, stream bool, body string) conformance.Case {
		return conformance.Case{Account: account, Body: []byte(body),
			Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: "claude-haiku-4-5", Stream: stream}}
	}
	return []conformance.Case{
		request(anthropic.ProtocolMessages, false, `{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`),
		request(anthropic.ProtocolMessages, true, `{"model":"claude-haiku-4-5","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		request(anthropic.ProtocolCountTokens, false, `{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`),
	}
}
