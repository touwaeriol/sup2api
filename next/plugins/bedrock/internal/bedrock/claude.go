package bedrock

import (
	"regexp"
	"strconv"
	"strings"
)

// claudeVersion finds the family and version in a Bedrock model id:
// "anthropic.claude-opus-4-5-20251101-v1:0" is Opus 4.5,
// "global.anthropic.claude-sonnet-5" Sonnet 5. The minor version has one or
// two digits, so that a date ("claude-sonnet-4-20250514") is not taken for
// one.
var claudeVersion = regexp.MustCompile(`claude-(haiku|sonnet|opus|fable)-(\d+)(?:[-.](\d{1,2})\b)?`)

// claudeModel is what the body rules need of the model a request invokes.
// The zero value is a model the id does not name (an application inference
// profile ARN, a mapped name, another vendor): a rule that depends on the
// version then alters nothing, since the model may well be a recent one.
type claudeModel struct {
	family       string
	major, minor int
}

func parseClaude(id string) claudeModel {
	m := claudeVersion.FindStringSubmatch(strings.ToLower(id))
	if m == nil {
		return claudeModel{}
	}
	major, _ := strconv.Atoi(m[2])
	minor, _ := strconv.Atoi(m[3])
	return claudeModel{family: m[1], major: major, minor: minor}
}

// known reports whether the id named a Claude version.
func (m claudeModel) known() bool { return m.family != "" }

// atLeast reports a model of version major.minor or later.
func (m claudeModel) atLeast(major, minor int) bool {
	return m.major > major || m.major == major && m.minor >= minor
}

// hasCacheTTL: Claude 4.5 and later take a cache_control ttl ("5m", "1h").
func (m claudeModel) hasCacheTTL() bool { return m.atLeast(4, 5) }

// hasToolSearch: Opus, Sonnet and Fable 4.5 and later have tool search,
// Haiku does not.
func (m claudeModel) hasToolSearch() bool { return m.family != "haiku" && m.atLeast(4, 5) }

// adaptiveThinkingOnly: Fable and Opus 4.7 and later think adaptively only,
// without a budget.
func (m claudeModel) adaptiveThinkingOnly() bool {
	return m.family == "fable" || m.family == "opus" && m.atLeast(4, 7)
}
