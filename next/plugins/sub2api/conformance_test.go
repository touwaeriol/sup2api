package main

import (
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/plugins/sub2api/internal/sub2api"
	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest/conformance"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
)

// TestConformance runs the platform plugin conformance suite over every
// HTTP protocol the type serves, a Responses WebSocket turn and the
// balance: each protocol's own auth header is the authoritative one.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Plugin{
		Platform: sub2api.New(),
		Manifest: manifestJSON,
		Protocols: []string{
			openai.ProtocolChat, openai.ProtocolResponses, openai.ProtocolEmbeddings, openai.ProtocolImages, openai.ProtocolImagesEdit,
			anthropic.ProtocolMessages, anthropic.ProtocolCountTokens,
			gemini.ProtocolGenerate, gemini.ProtocolStreamGenerate, gemini.ProtocolCountTokens,
		},
		Credentials: []string{"authorization", "x-api-key", "x-goog-api-key"},
	}, conformanceCases()...)
}

func conformanceCases() []conformance.Case {
	account := &pluginv1.Account{Id: 1, Type: sub2api.TypeAPIKey, CredentialsJson: `{"api_key":"sk-conformance-0123456789"}`,
		SettingsJson: `{"base_url":"https://sub2api.example.com"}`}
	var cases []conformance.Case
	add := func(protocol, model, body string, stream bool) {
		cases = append(cases, conformance.Case{Account: account, Body: []byte(body),
			Meta: &pluginv1.RequestMeta{Protocol: protocol, Model: model, Stream: stream}})
	}
	both := func(protocol, model, body string) {
		add(protocol, model, `{"model":"`+model+`",`+body+`}`, false)
		add(protocol, model, `{"model":"`+model+`",`+body+`,"stream":true}`, true)
	}
	both(openai.ProtocolChat, "gpt-4o-mini", `"messages":[{"role":"user","content":"hi"}]`)
	both(openai.ProtocolResponses, "gpt-4o-mini", `"input":"hi"`)
	add(openai.ProtocolEmbeddings, "text-embedding-3-small", `{"model":"text-embedding-3-small","input":"hi"}`, false)
	both(openai.ProtocolImages, "gpt-image-1.5", `"prompt":"a cat"`)
	both(openai.ProtocolImagesEdit, "gpt-image-1.5", `"prompt":"a hat","images":[{"image_url":"https://example.com/cat.png"}]`)
	both(anthropic.ProtocolMessages, "claude-sonnet-4-6", `"max_tokens":16,"messages":[{"role":"user","content":"Hi"}]`)
	add(anthropic.ProtocolCountTokens, "claude-sonnet-4-6", `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Hi"}]}`, false)
	contents := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	add(gemini.ProtocolGenerate, "gemini-2.5-flash", contents, false)
	add(gemini.ProtocolStreamGenerate, "gemini-2.5-flash", contents, true)
	add(gemini.ProtocolCountTokens, "gemini-2.5-flash", contents, false)
	return append(cases, conformance.Case{Account: account, WebSocket: true,
		Meta: &pluginv1.RequestMeta{Protocol: openai.ProtocolResponsesWS, Model: "gpt-4o-mini", Stream: true},
		Body: []byte(`{"type":"response.create","model":"gpt-4o-mini","input":"hi"}`)})
}
