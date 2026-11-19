package bedrock

import (
	"encoding/json"
	"slices"

	"github.com/tidwall/gjson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/jsonedit"
)

// droppedFields are top-level fields InvokeModel refuses: the path names
// the model and whether the answer streams, metadata and provider are not
// in AWS's InvokeModel request body for Claude, and service_tier,
// inference_geo and speed choose Anthropic's own service (priority tier,
// data residency, fast mode), which Bedrock does not offer: it picks the
// geography by inference profile (Claude docs: "Claude on Amazon Bedrock
// (bedrock-runtime)", "Global versus regional endpoints"). output_config
// and output_format stay (the previous implementation removed them):
// InvokeModel reads the output format and the effort from output_config
// (AWS user guide: "Get validated JSON results from models", "Adaptive
// thinking") and output_format is its deprecated spelling.
var droppedFields = []string{"model", "stream", "metadata", "provider", "service_tier", "inference_geo", "speed"}

// anthropicVersion is AnthropicVersion as a JSON value.
var anthropicVersion = []byte(`"` + AnthropicVersion + `"`)

// invokeBody turns a Messages body into the InvokeModel body of model id,
// given the client's anthropic-beta header: droppedFields leave, the
// version Bedrock requires is set, the beta features asked for in the
// header and in the body and those the tools need become the body's
// anthropic_beta (or it leaves when none is left), context_management
// leaves without its feature, and what Claude Code adds deeper inside that
// InvokeModel refuses is cleaned (sanitize). Everything else is the
// client's bytes as they came.
func invokeBody(body []byte, header, id string) ([]byte, error) {
	d, err := jsonedit.Parse(body)
	if err != nil || !d.Root().IsObject() {
		return nil, status.Error(codes.InvalidArgument, "the request body is not a JSON object")
	}
	doc := d.Root()
	m := parseClaude(id)
	betas := bedrockBetas(slices.Concat(headerBetas(header), bodyBetas(doc.Get(fieldBetas)), toolBetas(doc.Get("tools"), m))...)
	sanitize(d, m)
	root := d.Object(doc)
	root.DeleteFunc(func(key string, _ gjson.Result) bool {
		switch key {
		case fieldBetas:
			return len(betas) == 0
		case fieldContextManagement:
			return !slices.Contains(betas, betaContextManagement)
		}
		return slices.Contains(droppedFields, key)
	})
	root.Set("anthropic_version", anthropicVersion)
	if len(betas) > 0 {
		raw, _ := json.Marshal(betas) // strings always marshal
		root.Set(fieldBetas, raw)
	}
	out, err := d.Bytes()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return out, nil
}
