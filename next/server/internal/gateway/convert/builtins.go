package convert

// builtinPair is a built-in pair declared by its request side; the
// response side is generic (CONVERT-MATRIX §3).
type builtinPair struct {
	from, to string
	grade    Grade
	req      requestFunc
}

// builtinPairs are the built-in pairs besides chat -> responses
// (CONVERT-MATRIX §0, §9.9). Their requests convert through canonReq. The
// two Gemini protocols take and give the same bodies; a Gemini client's
// model and streaming come from the endpoint (geminiRequest).
var builtinPairs = []builtinPair{
	{"openai.responses", "openai.chat", GradeGood, canonRequest(parseResponsesRequest, buildChatRequest("max_completion_tokens"))},
	{"openai.chat", "anthropic.messages", GradeFair, canonRequest(parseChatRequest, buildAnthropicRequest)},
	{"openai.responses", "anthropic.messages", GradeFair, canonRequest(parseResponsesRequest, buildAnthropicRequest)},
	{"anthropic.messages", "openai.chat", GradeFair, canonRequest(parseAnthropicRequest, buildChatRequest("max_tokens"))},
	{"anthropic.messages", "openai.responses", GradeFair, canonRequest(parseAnthropicRequest, buildResponsesRequest)},
	{"openai.chat", "gemini.generate", GradeFair, canonRequest(parseChatRequest, buildGeminiRequest)},
	{"openai.chat", "gemini.stream_generate", GradeFair, canonRequest(parseChatRequest, buildGeminiRequest)},
	{"openai.responses", "gemini.generate", GradeFair, canonRequest(parseResponsesRequest, buildGeminiRequest)},
	{"openai.responses", "gemini.stream_generate", GradeFair, canonRequest(parseResponsesRequest, buildGeminiRequest)},
	{"gemini.generate", "openai.chat", GradeFair, geminiRequest(buildChatRequest("max_tokens"))},
	{"gemini.stream_generate", "openai.chat", GradeFair, geminiRequest(buildChatRequest("max_tokens"))},
	{"gemini.generate", "openai.responses", GradeFair, geminiRequest(buildResponsesRequest)},
	{"gemini.stream_generate", "openai.responses", GradeFair, geminiRequest(buildResponsesRequest)},
}

// The image pairs (openai.images, openai.images_edit -> openai.responses,
// CONTRACTS §121) are independent too, and never composed.

// builtinConverters returns new instances of the built-in converters.
func builtinConverters() []Converter {
	out := []Converter{&chatResponses{}, newImagesResponses(protocolImages), newImagesResponses(protocolImagesEdit)}
	for _, p := range builtinPairs {
		out = append(out, newPair(p.from, p.to, p.grade, p.req))
	}
	return out
}
