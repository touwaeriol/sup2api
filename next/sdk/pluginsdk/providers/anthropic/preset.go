package anthropic

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// APIKey is an Anthropic API key account type: x-api-key on
// https://api.anthropic.com (a trailing /v1 not repeated), anthropic-version
// unless the client sends one, messages and count_tokens, the Anthropic
// error policy, a one-token messages test and the model listing. It filters
// no request field.
func APIKey(id string) standard.Type {
	return standard.Type{
		ID: id,
		Spec: apikey.Spec{
			DefaultBaseURL: "https://api.anthropic.com",
			Versions:       []string{"/v1"},
			// The non-string key keeps the recorded code "pattern"; the
			// wording is openai's, written into the golden case ("validate
			// non-string key", with its note) when the 8-512 rule went.
			Texts: apikey.Texts{
				UnsupportedAccountType: "unsupported account type {type} / 不支持的账号类型",
				KeyNotString:           "API key must be a string / api_key 必须是字符串",
				KeyNotStringCode:       "pattern",
				BaseURLFormat:          "base_url {reason} / base_url 必须是不含账号、查询参数的 http(s) 地址",
			},
		},
		Auth: standard.Auth{Header: "x-api-key"},
		Headers: []standard.Header{
			standard.Default("anthropic-version", standard.Fixed(DefaultAPIVersion)),
			standard.Set("content-type", standard.Fixed("application/json")),
		},
		Routes: map[string]standard.Route{
			ProtocolMessages:    {Path: standard.Path("/v1/messages")},
			ProtocolCountTokens: {Path: standard.Path("/v1/messages/count_tokens")},
		},
		DefaultProtocol: ProtocolMessages,
		Classify:        Policy,
		Test:            &standard.Test{Protocol: ProtocolMessages, Model: "claude-haiku-4-5", Body: func(m string) string { return MessagesPingBody(m, 1) }},
		Models:          &apikey.ModelList{Path: "/v1/models?limit=1000", IdsPath: "data.#.id"},
	}
}
