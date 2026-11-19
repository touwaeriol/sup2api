package bedrock

import (
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

// What Claude Code sends Anthropic and InvokeModel refuses, deep inside a
// Messages body (the previous implementation's Bedrock rules,
// backend/internal/service/bedrock_request.go):
//
//   - "custom" in a tool definition ({"defer_loading": true}): "Extra
//     inputs are not permitted";
//   - "scope" in a cache_control, and "ttl" on a model known not to take it;
//   - a tool use id with characters outside [a-zA-Z0-9_-];
//   - a thinking setting the model does not take.

// defaultThinkingBudget is the budget_tokens of an "enabled" thinking
// setting that names none: InvokeModel requires one.
const defaultThinkingBudget = "10000"

// cacheTTLs are the cache_control ttl values InvokeModel takes.
var cacheTTLs = []string{"5m", "1h"}

// idChar keeps the characters InvokeModel takes in a tool use id
// ([a-zA-Z0-9_-]) and maps the others to "_" (for strings.Map).
func idChar(r rune) rune {
	if r == '_' || r == '-' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
		return r
	}
	return '_'
}

// sanitize adds the edits that clean d, a Messages body, for model m.
func sanitize(d *jsonedit.Doc, m claudeModel) {
	doc := d.Root()
	jsonedit.EachElement(doc.Get("tools"), func(tool gjson.Result) {
		d.Object(tool).Delete("custom")
		cacheControl(d, tool, m)
	})
	jsonedit.EachElement(doc.Get("system"), func(block gjson.Result) { cacheControl(d, block, m) })
	jsonedit.EachElement(doc.Get("messages"), func(message gjson.Result) {
		jsonedit.EachElement(message.Get("content"), func(block gjson.Result) {
			cacheControl(d, block, m)
			toolUseID(d, block)
		})
	})
	thinking(d, doc.Get("thinking"), m)
}

// cacheControl removes from the cache_control of block what InvokeModel
// refuses: scope always; ttl when the model is known and does not take it
// with that value. On a model the id does not name the ttl stays as the
// client sent it: Claude 4.5 and later take it, and removing it would turn
// a one-hour cache into a five-minute one without a word.
func cacheControl(d *jsonedit.Doc, block gjson.Result, m claudeModel) {
	d.Object(block.Get("cache_control")).DeleteFunc(func(key string, value gjson.Result) bool {
		return key == "scope" || key == "ttl" && m.known() && !(m.hasCacheTTL() && value.Type == gjson.String && slices.Contains(cacheTTLs, value.Str))
	})
}

// toolUseID replaces the characters InvokeModel refuses in the id of a
// tool_use block and in the tool_use_id of a tool_result block, the same
// way in both, so the pair still matches.
func toolUseID(d *jsonedit.Doc, block gjson.Result) {
	var id gjson.Result
	switch block.Get("type").String() {
	case "tool_use":
		id = block.Get("id")
	case "tool_result":
		id = block.Get("tool_use_id")
	}
	if id.Type != gjson.String {
		return
	}
	if clean := strings.Map(idChar, id.Str); clean != id.Str {
		d.Replace(id, []byte(`"`+clean+`"`)) // clean holds no character JSON escapes
	}
}

// thinking fits the thinking setting to the model: a model that thinks
// adaptively only gets "adaptive" without a budget, and an "enabled"
// setting without a budget gets the default one.
func thinking(d *jsonedit.Doc, setting gjson.Result, m claudeModel) {
	switch typ := setting.Get("type").String(); {
	case m.adaptiveThinkingOnly() && (typ == "enabled" || typ == "adaptive"):
		o := d.Object(setting)
		o.Delete("budget_tokens")
		o.Set("type", []byte(`"adaptive"`))
	case typ == "enabled" && !setting.Get("budget_tokens").Exists():
		d.Object(setting).Set("budget_tokens", []byte(defaultThinkingBudget))
	}
}
