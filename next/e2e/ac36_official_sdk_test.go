package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// The official OpenAI and Anthropic Go SDKs as test clients: they parse
// every answer and stream event of the gateway strictly, so a malformed
// event fails the test where a hand-written reader would let it pass.
// SDKs are test clients only, never used by the core or the plugins.

const mockText = "Hello from mock-upstream."

// The tool offered in the SDK tests; the marker in the prompt makes the
// mock call it (deploy/mock-upstream tools.go).
const (
	sdkTool       = "lookup"
	sdkToolPrompt = "MOCK-TOOL-CALL: look something up"
)

func openaiSDK(e *Env, apiKey string) openai.Client {
	return openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(e.BaseURL+"/v1/"), option.WithMaxRetries(0),
		option.WithRequestTimeout(60*time.Second))
}

func anthropicSDK(e *Env, apiKey string) anthropic.Client {
	return anthropic.NewClient(anthropicoption.WithAPIKey(apiKey), anthropicoption.WithBaseURL(e.BaseURL+"/"), anthropicoption.WithMaxRetries(0),
		anthropicoption.WithRequestTimeout(60*time.Second))
}

func openaiChatParams(model, prompt string, tools bool) openai.ChatCompletionNewParams {
	p := openai.ChatCompletionNewParams{Model: model, Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)}}
	if tools {
		p.Tools = []openai.ChatCompletionToolUnionParam{openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name: sdkTool, Parameters: openai.FunctionParameters{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
		})}
	}
	return p
}

func anthropicParams(model, prompt string, tools bool) anthropic.MessageNewParams {
	p := anthropic.MessageNewParams{Model: model, MaxTokens: 64,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))}}
	if tools {
		p.Tools = []anthropic.ToolUnionParam{anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{
			Properties: map[string]any{"query": map[string]any{"type": "string"}}}, sdkTool)}
	}
	return p
}

// openaiChatSuite drives chat completions with the OpenAI SDK: non-stream,
// stream (accumulated), and a tool call round trip both ways.
func openaiChatSuite(t *testing.T, c openai.Client, model string) {
	openaiChatPlain(t, c, model)
	for _, streamed := range []bool{false, true} {
		openaiChatToolRoundTrip(t, c, model, streamed)
	}
}

// openaiChatPlain sends a text prompt, non-stream then stream.
func openaiChatPlain(t *testing.T, c openai.Client, model string) {
	ctx := context.Background()
	resp, err := c.Chat.Completions.New(ctx, openaiChatParams(model, "hello sdk", false))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != mockText || resp.Usage.CompletionTokens != MockOutputTokens {
		t.Fatalf("chat answer: %s", resp.RawJSON())
	}
	acc := openai.ChatCompletionAccumulator{}
	stream := c.Chat.Completions.NewStreaming(ctx, openaiChatParams(model, "hello sdk stream", false))
	for stream.Next() {
		if !acc.AddChunk(stream.Current()) {
			t.Fatalf("chunk refused by the accumulator: %s", stream.Current().RawJSON())
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	if len(acc.Choices) != 1 || acc.Choices[0].Message.Content != mockText || acc.Choices[0].FinishReason != "stop" {
		t.Fatalf("accumulated chat stream: %+v", acc.ChatCompletion)
	}
}

// openaiChatToolRoundTrip gets a tool call (streamed or not), answers it
// with a tool result and requires the answer that saw it.
func openaiChatToolRoundTrip(t *testing.T, c openai.Client, model string, streamed bool) {
	params := openaiChatParams(model, sdkToolPrompt, true)
	msg := openaiToolCall(t, c, params, streamed)
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != sdkTool {
		t.Fatalf("stream=%v: tool calls %+v", streamed, msg.ToolCalls)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args); err != nil || args["query"] != "mock" {
		t.Fatalf("stream=%v: tool arguments %q: %v", streamed, msg.ToolCalls[0].Function.Arguments, err)
	}
	params.Messages = append(params.Messages, msg.ToParam(), openai.ToolMessage("42", msg.ToolCalls[0].ID))
	resp, err := c.Chat.Completions.New(context.Background(), params)
	if err != nil {
		t.Fatalf("stream=%v: tool result turn: %v", streamed, err)
	}
	if !strings.HasPrefix(resp.Choices[0].Message.Content, "TOOL-RESULT-SEEN") {
		t.Fatalf("stream=%v: answer after the tool result: %s", streamed, resp.RawJSON())
	}
}

// openaiToolCall sends params and returns the assistant message calling
// the tool.
func openaiToolCall(t *testing.T, c openai.Client, params openai.ChatCompletionNewParams, streamed bool) openai.ChatCompletionMessage {
	ctx := context.Background()
	if !streamed {
		resp, err := c.Chat.Completions.New(ctx, params)
		if err != nil {
			t.Fatalf("tool call: %v", err)
		}
		if resp.Choices[0].FinishReason != "tool_calls" {
			t.Fatalf("tool call answer: %s", resp.RawJSON())
		}
		return resp.Choices[0].Message
	}
	acc := openai.ChatCompletionAccumulator{}
	s := c.Chat.Completions.NewStreaming(ctx, params)
	var finished []openai.FinishedChatCompletionToolCall
	for s.Next() {
		acc.AddChunk(s.Current())
		if tc, ok := acc.JustFinishedToolCall(); ok {
			finished = append(finished, tc)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatalf("tool call stream: %v", err)
	}
	if len(finished) != 1 || finished[0].Name != sdkTool || finished[0].Arguments != `{"query":"mock"}` {
		t.Fatalf("streamed tool call: %+v", finished)
	}
	return acc.Choices[0].Message
}

// anthropicSuite drives Messages with the Anthropic SDK: non-stream, stream
// (accumulated), and a tool use round trip both ways.
func anthropicSuite(t *testing.T, c anthropic.Client, model string) {
	anthropicPlain(t, c, model)
	for _, streamed := range []bool{false, true} {
		anthropicToolRoundTrip(t, c, model, streamed)
	}
}

// anthropicPlain sends a text prompt, non-stream then stream.
func anthropicPlain(t *testing.T, c anthropic.Client, model string) {
	ctx := context.Background()
	msg, err := c.Messages.New(ctx, anthropicParams(model, "hello sdk", false))
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != mockText || msg.Usage.OutputTokens != MockOutputTokens {
		t.Fatalf("messages answer: %s", msg.RawJSON())
	}
	acc := anthropic.Message{}
	stream := c.Messages.NewStreaming(ctx, anthropicParams(model, "hello sdk stream", false))
	for stream.Next() {
		if err := acc.Accumulate(stream.Current()); err != nil {
			t.Fatalf("event refused by Accumulate: %v: %s", err, stream.Current().RawJSON())
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("messages stream: %v", err)
	}
	if len(acc.Content) != 1 || acc.Content[0].Text != mockText || acc.StopReason != "end_turn" {
		t.Fatalf("accumulated messages stream: %+v", acc)
	}
}

// anthropicToolRoundTrip gets a tool use (streamed or not), answers it
// with a tool result and requires the answer that saw it.
func anthropicToolRoundTrip(t *testing.T, c anthropic.Client, model string, streamed bool) {
	params := anthropicParams(model, sdkToolPrompt, true)
	got := anthropicToolUse(t, c, params, streamed)
	if got.StopReason != "tool_use" || len(got.Content) != 1 || got.Content[0].Type != "tool_use" || got.Content[0].Name != sdkTool {
		t.Fatalf("stream=%v: tool use answer: %+v", streamed, got)
	}
	var input map[string]string
	if err := json.Unmarshal(got.Content[0].Input, &input); err != nil || input["query"] != "mock" {
		t.Fatalf("stream=%v: tool input %s: %v", streamed, got.Content[0].Input, err)
	}
	params.Messages = append(params.Messages, got.ToParam(),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock(got.Content[0].ID, "42", false)))
	m, err := c.Messages.New(context.Background(), params)
	if err != nil {
		t.Fatalf("stream=%v: tool result turn: %v", streamed, err)
	}
	if len(m.Content) == 0 || !strings.HasPrefix(m.Content[0].Text, "TOOL-RESULT-SEEN") {
		t.Fatalf("stream=%v: answer after the tool result: %s", streamed, m.RawJSON())
	}
}

// anthropicToolUse sends params and returns the message using the tool.
func anthropicToolUse(t *testing.T, c anthropic.Client, params anthropic.MessageNewParams, streamed bool) anthropic.Message {
	ctx := context.Background()
	if !streamed {
		m, err := c.Messages.New(ctx, params)
		if err != nil {
			t.Fatalf("tool use: %v", err)
		}
		return *m
	}
	var got anthropic.Message
	s := c.Messages.NewStreaming(ctx, params)
	for s.Next() {
		if err := got.Accumulate(s.Current()); err != nil {
			t.Fatalf("tool use stream event: %v", err)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatalf("tool use stream: %v", err)
	}
	return got
}

// AC 36: the official SDKs parse what the gateway answers, for the
// account's own protocol and across protocols once the account enables the
// conversion for the model (404 before).
func TestAC36_OfficialSDKClients(t *testing.T) {
	e := Setup(t)
	admin := e.Admin()
	m := e.Mock()
	oaKey := fmt.Sprintf("sk-sdk-oa-%s-%d", e.RunID, nextSeq())
	oa := e.singleAccount(admin, "grp-sdk-openai", AccountSpec{PluginKey: OpenAIPlugin, Type: APIKeyType, APIKey: oaKey}, oaKey, "gpt", "sdk")
	anKey := fmt.Sprintf("sk-ant-sdk-%s-%d", e.RunID, nextSeq())
	an := e.singleAccount(admin, "grp-sdk-anthropic", AccountSpec{APIKey: anKey}, anKey, "claude", "sdk")
	gpt, claude := oa.Model, an.Model

	t.Run("openai sdk, openai account", func(t *testing.T) {
		c := openaiSDK(e, oa.APIKey)
		openaiChatSuite(t, c, gpt)
		ctx := context.Background()
		r, err := c.Responses.New(ctx, responses.ResponseNewParams{Model: gpt, Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello responses")}})
		if err != nil || r.OutputText() != mockText || r.Usage.OutputTokens != MockOutputTokens {
			t.Fatalf("responses: %v %+v", err, r)
		}
		s := c.Responses.NewStreaming(ctx, responses.ResponseNewParams{Model: gpt, Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello responses stream")}})
		var text strings.Builder
		completed := false
		for s.Next() {
			ev := s.Current()
			text.WriteString(ev.Delta)
			completed = completed || ev.Type == "response.completed"
		}
		if err := s.Err(); err != nil || !completed || text.String() != mockText {
			t.Fatalf("responses stream: err %v completed %v text %q", err, completed, text.String())
		}
	})

	t.Run("openai sdk, responses websocket", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c := openaiSDK(e, oa.APIKey)
		mark := m.Mark(t)
		conn, err := c.Responses.Connect(ctx, responses.ResponseConnectionOptions{})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			if err := conn.Create(ctx, responses.ResponsesClientEventResponseCreateParam{Model: gpt,
				Input: responses.ResponsesClientEventResponseCreateInputUnionParam{OfString: openai.String(fmt.Sprintf("ws turn %d", i))}}); err != nil {
				t.Fatalf("turn %d: create: %v", i, err)
			}
			r, err := conn.FinalResponse(ctx)
			if err != nil || r.Status != "completed" || r.OutputText() != mockText {
				t.Fatalf("turn %d: %v %+v", i, err, r)
			}
		}
		if handshakes, _ := wsCalls(mockCallsOf(t, m, mark, oaKey)); len(handshakes) != 1 {
			t.Fatalf("upstream handshakes = %d, want 1 for two turns", len(handshakes))
		}
	})

	t.Run("anthropic sdk, anthropic account", func(t *testing.T) {
		anthropicSuite(t, anthropicSDK(e, an.APIKey), claude)
	})

	t.Run("cross protocol is off by default", func(t *testing.T) {
		mark := m.Mark(t)
		oc, ac := openaiSDK(e, an.APIKey), anthropicSDK(e, oa.APIKey)
		_, err := oc.Chat.Completions.New(context.Background(), openaiChatParams(claude, "no conversion", false))
		var oe *openai.Error
		if !errors.As(err, &oe) || oe.StatusCode != http.StatusNotFound || oe.Message != notServedMessage {
			t.Fatalf("openai sdk on an anthropic account without conversion: %v", err)
		}
		_, err = ac.Messages.New(context.Background(), anthropicParams(gpt, "no conversion", false))
		var ae *anthropic.Error
		if !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound || !strings.Contains(ae.Error(), notServedMessage) {
			t.Fatalf("anthropic sdk on an openai account without conversion: %v", err)
		}
		if calls := append(mockCallsOf(t, m, mark, oaKey), mockCallsOf(t, m, mark, anKey)...); len(calls) != 0 {
			t.Fatalf("a request no account serves reached the upstream: %+v", calls)
		}
	})

	t.Run("cross protocol enabled on the account", func(t *testing.T) {
		e.With(t).enableProtocols(admin, an.Accounts[0].ID, claude, "anthropic.messages", "openai.chat")
		e.With(t).enableProtocols(admin, oa.Accounts[0].ID, gpt, "openai.chat", "anthropic.messages")
		mark := m.Mark(t)
		t.Run("openai sdk, anthropic account", func(t *testing.T) { openaiChatSuite(t, openaiSDK(e, an.APIKey), claude) })
		expectAllOnPath(t, mockCallsOf(t, m, mark, anKey), "/v1/messages", "converted chat")
		mark = m.Mark(t)
		t.Run("anthropic sdk, openai account", func(t *testing.T) { anthropicSuite(t, anthropicSDK(e, oa.APIKey), gpt) })
		expectAllOnPath(t, mockCallsOf(t, m, mark, oaKey), "/v1/chat/completions", "converted messages")
	})
}

// expectAllOnPath requires at least one upstream call, all of them on path.
func expectAllOnPath(t *testing.T, calls []MockRequest, path, what string) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatalf("%s: no upstream call with the account's key", what)
	}
	for _, r := range calls {
		if r.Path != path {
			t.Fatalf("%s reached %s, want %s", what, r.Path, path)
		}
	}
}
