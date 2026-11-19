package openai

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// Protocol ids of the built-in openai platform (sdk/platforms/openai.json).
const (
	ProtocolChat           = "openai.chat"
	ProtocolResponses      = "openai.responses"
	ProtocolResponsesWS    = "openai.responses_ws"
	ProtocolEmbeddings     = "openai.embeddings"
	ProtocolImages         = "openai.images"
	ProtocolImagesEdit     = "openai.images_edit"
	ResponsesWebSocketBeta = "responses_websockets=2026-02-06" // openai-beta of the websocket handshake
)

// APIKey is an OpenAI API key account type: Bearer key on
// https://api.openai.com (a trailing /v1 not repeated), chat completions
// with the usage of a stream requested, responses over HTTP and websocket,
// embeddings, image generations and edits (JSON bodies, forwarded as they
// are: their stream reports its usage unasked), the OpenAI error policy, a
// one-token chat test (and the cheapest request of responses, embeddings and
// image generations for an account serving those) and the model listing. It
// filters no request field.
func APIKey(id string) standard.Type {
	return standard.Type{
		ID: id,
		Spec: apikey.Spec{
			DefaultBaseURL: "https://api.openai.com",
			Versions:       []string{"/v1"},
			// The non-string key keeps code "pattern" and this wording: the
			// frozen golden cases ("validate non-string key") pin them.
			Texts: apikey.Texts{
				UnsupportedAccountType: "unsupported account type {type} / 不支持的账号类型",
				KeyNotString:           "API key must be a string / api_key 必须是字符串",
				KeyNotStringCode:       "pattern",
			},
		},
		Auth:    standard.Auth{Header: "authorization", Prefix: "Bearer "},
		Headers: []standard.Header{standard.Set("content-type", standard.Fixed("application/json"))},
		Routes: map[string]standard.Route{
			ProtocolChat:      {Path: standard.Path("/v1/chat/completions"), Edit: IncludeStreamUsage},
			ProtocolResponses: {Path: standard.Path("/v1/responses")},
			ProtocolResponsesWS: {
				Path:      standard.Path("/v1/responses"),
				WebSocket: true,
				Headers:   []standard.Header{standard.AppendTo("openai-beta", standard.Fixed(ResponsesWebSocketBeta))},
			},
			ProtocolEmbeddings: {Path: standard.Path("/v1/embeddings")},
			ProtocolImages:     {Path: standard.Path("/v1/images/generations")},
			ProtocolImagesEdit: {Path: standard.Path("/v1/images/edits")},
		},
		DefaultProtocol: ProtocolChat,
		Classify:        Policy,
		Test:            &standard.Test{Protocol: ProtocolChat, Model: "gpt-4o-mini", Body: func(m string) string { return ChatPingBody(m, 1) }},
		Tests: []standard.Test{
			{Protocol: ProtocolResponses, Model: "gpt-4o-mini", Body: ResponsesPingBody},
			{Protocol: ProtocolEmbeddings, Model: "text-embedding-3-small", Body: EmbeddingsPingBody},
			{Protocol: ProtocolImages, Model: "gpt-image-1-mini", Body: ImagesPingBody},
		},
		Models: &apikey.ModelList{Path: "/v1/models", IdsPath: "data.#.id"},
	}
}

// IncludeStreamUsage is the edit of a chat completion: a stream asks for
// the usage chunk (stream_options.include_usage), which the host's usage
// rules read; without it the host reads no usage. A stream is meta.stream
// or a body the core made stream (Request.Stream, pluginsdk.RequestStream).
func IncludeStreamUsage(d *jsonedit.Doc, r *standard.Request) {
	StreamUsage(d, r.Stream)
}

// StreamUsage is IncludeStreamUsage for a plugin that edits the body itself:
// stream is pluginsdk.RequestStream of the execution, so that a type whose
// upstream only streams (the core forces the body's stream flag) is asked
// for the usage chunk too.
func StreamUsage(d *jsonedit.Doc, stream bool) {
	if stream {
		d.Set("stream_options.include_usage", []byte("true"))
	}
}
