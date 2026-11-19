package volcengine

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

func (p *Plugin) EstimateUsage(ctx context.Context, in *pluginv1.EstimateUsageRequest) (*pluginv1.UsageReport, error) {
	if in.GetMeta().GetProtocol() != ProtocolVideoSubmit {
		// Non-video protocols: return Unimplemented to signal that the plugin
		// does not provide usage estimation. Core will fall back to its own
		// local tokenizer for image/text protocols.
		return nil, pluginsdk.ErrUnimplemented("usage estimation only available for video protocol")
	}
	est := estimateVideo(in.GetMeta().GetModel(), readVideoSpec(in.GetFields()))
	return &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{OutputTokens: est.Tokens}, Facts: estimatedVideoFacts(est, in.GetFields())}, nil
}

// dimensionsForArea selects the integer factor pair nearest the requested
// aspect ratio. All published table dimensions are integral; the area remains
// exact, including the non-16:9 sizes and portrait orientation.
func dimensionsForArea(area int64, ratio string) (int64, int64) {
	a, b, ok := strings.Cut(ratio, ":")
	x, e1 := strconv.ParseFloat(a, 64)
	y, e2 := strconv.ParseFloat(b, 64)
	if !ok || e1 != nil || e2 != nil || x <= 0 || y <= 0 {
		return 0, 0
	}
	target := x / y
	best := math.Inf(1)
	var width, height int64
	for h := int64(1); h*h <= area; h++ {
		if area%h != 0 {
			continue
		}
		for _, pair := range [][2]int64{{area / h, h}, {h, area / h}} {
			delta := math.Abs(math.Log(float64(pair[0]) / float64(pair[1]) / target))
			if delta < best {
				best = delta
				width, height = pair[0], pair[1]
			}
		}
	}
	return width, height
}

func estimatedVideoFacts(est videoEstimate, fields map[string]string) map[string]string {
	facts := map[string]string{
		FactResolution:    est.Resolution,
		"video_estimated": "true",
		"video_seconds":   strconv.FormatFloat(est.Seconds, 'f', -1, 64),
		"video_pixels":    strconv.FormatInt(est.Pixels, 10),
		"video_width":     strconv.FormatInt(est.Width, 10),
		"video_height":    strconv.FormatInt(est.Height, 10),
		"video_input":     "false",
		"video_draft":     strconv.FormatBool(gjson.Parse(fields["draft"]).Bool()),
	}
	for _, item := range gjson.Parse(fields["content.#.type"]).Array() {
		if item.String() == "video_url" {
			facts["video_input"] = "true"
			break
		}
	}
	return facts
}

// Missing response fields stay absent, allowing the core to retain immutable
// submission facts. Never reset video_input just because a poll omits content.
func completedVideoFacts(body []byte, model string) map[string]string {
	facts := succeededFacts(body)
	if facts == nil {
		facts = map[string]string{}
	}
	facts["video_estimated"] = "false"
	for key, paths := range map[string][]string{
		"video_seconds": {"duration", "content.duration"},
		"video_width":   {"width", "content.width"},
		"video_height":  {"height", "content.height"},
	} {
		for _, path := range paths {
			v := gjson.GetBytes(body, path)
			if v.Type == gjson.Number && v.Float() > 0 {
				facts[key] = v.Raw
				break
			}
		}
	}
	w, _ := strconv.ParseInt(facts["video_width"], 10, 64)
	h, _ := strconv.ParseInt(facts["video_height"], 10, 64)
	if w == 0 || h == 0 {
		ratio := gjson.GetBytes(body, "ratio").String()
		if ratio == "" {
			ratio = gjson.GetBytes(body, "content.ratio").String()
		}
		if facts[FactResolution] != "" && ratio != "" {
			w, h = dimensionsForArea(tierArea(profileOf(model).gen, facts[FactResolution], ratio), ratio)
		}
	}
	if w > 0 && h > 0 && w <= 16384 && h <= 16384 {
		facts["video_width"] = strconv.FormatInt(w, 10)
		facts["video_height"] = strconv.FormatInt(h, 10)
		facts["video_pixels"] = strconv.FormatInt(w*h, 10)
	} else if facts[FactResolution] != "" {
		// The real resolution is known but its exact dimensions are not. Do
		// not keep an estimate's size-specific rate for a different output.
		facts["video_width"], facts["video_height"], facts["video_pixels"] = "0", "0", "0"
	}
	return facts
}
