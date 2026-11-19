package main

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/typesafe/internal/typesafe"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	preset "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
)

// TestConformance runs the platform plugin conformance suite: systemone is
// non-streaming only, the key in authorization is the only authoritative
// header.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform:    typesafe.New(),
		Manifest:    manifestJSON,
		Protocols:   []string{preset.ProtocolSystemOne},
		Credentials: []string{"authorization"},
	}, conformance.Case{
		Account: &pluginv1.Account{Id: 1, Type: typesafe.TypeAPIKey, CredentialsJson: `{"api_key":"ts-conformance-0123456789"}`},
		Meta:    &pluginv1.RequestMeta{Protocol: preset.ProtocolSystemOne, Model: "jev-latest"},
		Body:    []byte(`{"model":"jev-latest","state":"hi","questions":{"q":{"type":"noul","instructions":"Greeting?"}}}`),
	})
}
