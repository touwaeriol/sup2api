package bedrock

import (
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

// InvokeModel takes beta features in the body (anthropic_beta), not in the
// anthropic-beta header, and refuses the ones it does not know. The lists
// below are those of the previous implementation
// (backend/internal/service/bedrock_request.go) plus the anthropic_beta
// values the AWS user guide names for InvokeModel on the bedrock-runtime
// endpoint (https://docs.aws.amazon.com/bedrock/latest/userguide/):
//
//   - compact-2026-09-04: the model cards of Opus 4.6 to Fable 5.1
//     (model-card-anthropic-claude-*.html, "Compaction");
//   - fallback-credit-2026-07-01: the model cards of Fable 5, Opus 5 and
//     Fable 5.1 ("Fallback credit for refused requests");
//   - thinking-display-updates-2026-08-18 and the four per-turn effort
//     values: claude-messages-adaptive-thinking.html.
//
// The list is not per model: a feature a model does not have is the
// upstream's to refuse.

// betaHeader is the client header carrying the beta features.
const betaHeader = "anthropic-beta"

// Request body fields the beta features decide on.
const (
	// fieldBetas is where InvokeModel reads the beta features. A client
	// written for Bedrock sets it itself.
	fieldBetas = "anthropic_beta"
	// fieldContextManagement needs betaContextManagement.
	fieldContextManagement = "context_management"
)

const (
	betaComputerUse2501   = "computer-use-2025-01-24"
	betaComputerUse2511   = "computer-use-2025-11-24"
	betaContextManagement = "context-management-2025-06-27"
	betaToolSearch        = "tool-search-tool-2025-10-19"
	betaToolExamples      = "tool-examples-2025-10-29"
	betaAdvancedToolUse   = "advanced-tool-use-2025-11-20"
)

// supportedBetas are the beta features InvokeModel accepts.
var supportedBetas = []string{
	betaComputerUse2501,
	betaComputerUse2511,
	"context-1m-2025-08-07",
	betaContextManagement,
	"compact-2026-01-12",
	"compact-2026-09-04",
	"fallback-credit-2026-07-01",
	"thinking-display-updates-2026-08-18",
	"mid-conversation-output-config-2026-07-01",
	"mid-conversation-effort-2026-08-01",
	"per-turn-control-2026-07-01",
	"per-message-effort-2026-07-01",
	"fine-grained-tool-streaming-2025-05-14",
	betaToolSearch,
	betaToolExamples,
}

// betaAliases are features Bedrock knows under another name.
var betaAliases = map[string]string{
	betaAdvancedToolUse: betaToolSearch,
}

// headerBetas are the features of an anthropic-beta header value.
func headerBetas(header string) []string {
	return strings.Split(header, ",")
}

// bodyBetas are the features of the body's anthropic_beta: an array of
// names, or one string in the header's format. Anything else names no
// feature.
func bodyBetas(value gjson.Result) []string {
	if value.Type == gjson.String {
		return headerBetas(value.Str)
	}
	var out []string
	jsonedit.EachElement(value, func(item gjson.Result) {
		if item.Type == gjson.String {
			out = append(out, item.Str)
		}
	})
	return out
}

// toolBetas are the features the tools of a request need whether or not
// the client named them (the previous implementation, after litellm): a
// computer use tool needs its computer use feature; programmatic tool
// calling, input examples and, on a model that has it, the tool search tool
// need tool search.
func toolBetas(tools gjson.Result, m claudeModel) []string {
	var out []string
	jsonedit.EachElement(tools, func(tool gjson.Result) {
		switch typ := tool.Get("type").String(); {
		case typ == "computer_20250124":
			out = append(out, betaComputerUse2501)
		case strings.HasPrefix(typ, "computer_20"):
			out = append(out, betaComputerUse2511)
		case isToolSearchTool(typ) && m.hasToolSearch(), usesAdvancedToolUse(tool):
			out = append(out, betaToolSearch)
		}
	})
	return out
}

// isToolSearchTool reports the server tools of tool search.
func isToolSearchTool(typ string) bool {
	return typ == "tool_search_tool_regex_20251119" || typ == "tool_search_tool_bm25_20251119"
}

// usesAdvancedToolUse reports a tool that code execution may call, or one
// with input examples.
func usesAdvancedToolUse(tool gjson.Result) bool {
	callers := false
	jsonedit.EachElement(tool.Get("allowed_callers"), func(c gjson.Result) { callers = callers || c.Str == "code_execution_20250825" })
	examples := tool.Get("input_examples")
	return callers || examples.IsArray() && examples.Get("#").Int() > 0
}

// bedrockBetas are the features that go into anthropic_beta, out of those
// asked for: renamed where Bedrock has its own name, the unsupported ones
// dropped, each once, in the order given. Tool search brings tool examples
// with it.
func bedrockBetas(asked ...string) []string {
	var out []string
	for _, token := range asked {
		token = strings.TrimSpace(token)
		if alias, ok := betaAliases[token]; ok {
			token = alias
		}
		if slices.Contains(supportedBetas, token) && !slices.Contains(out, token) {
			out = append(out, token)
		}
	}
	if slices.Contains(out, betaToolSearch) && !slices.Contains(out, betaToolExamples) {
		out = append(out, betaToolExamples)
	}
	return out
}
