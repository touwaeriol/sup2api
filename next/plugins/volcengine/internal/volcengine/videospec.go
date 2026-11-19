package volcengine

// What a Seedance submit will cost, read out of the submit request.
//
// The reservation videoUsage returns is a real charge against a real
// balance, so the number below has to be defensible in both directions: too
// low and a $0 balance queues expensive work (the pre-charge exists to stop
// exactly that), too high and a legitimate 5-second clip is refused for want
// of a balance nobody will ever be billed. It is corrected exactly at
// reconcile, so a brief over-estimate costs a user headroom while the task
// runs, and nothing after it.
//
// WHERE THE NUMBERS COME FROM. Ark publishes the formula (model pricing page,
// "视频生成模型"):
//
//	token 用量 = (输入视频时长 + 输出视频时长) x 宽 x 高 x 帧率 / 1024
//
// with the frame rate fixed at 24 fps for every Seedance model, and a table of
// the exact output pixel size per resolution x aspect ratio x model
// generation. It is written here as frames x area / 1024: one number per table
// cell instead of two, and no rounding of a frame count Ark itself counts in
// frames. A clip of N seconds is N x 24 + 1 frames, not N x 24 - see framesFor:
// the published formula is one frame short of what Ark really bills.
//
// Ark calls its own formula an estimate and names usage.completion_tokens as
// the authority. That is exactly the division of labour here: this file
// reserves, Monitor reports completion_tokens for the core to settle.
//
// THREE THINGS THE ESTIMATE CANNOT SEE, each bounded rather than guessed:
//
//  1. Parameters hidden in the prompt. resolution, ratio, duration, frames,
//     seed, camera_fixed and watermark may ALSO be passed as "--rs 1080p
//     --dur 10" suffixes on content[].text, on every model - a weakly
//     validated form Ark documents as equivalent to the structured field,
//     with NO documented precedence between the two. So the prompt is read
//     too, and where the readings disagree the more expensive one wins. Where
//     the prompt could not be read at all (in a content element past the
//     declared paths) every parameter it could have carried is treated as
//     unknown - not as absent.
//  2. "The model decides". duration -1 means Ark picks a length inside the
//     model's range, and -1 is the documented default for Seedance 2.5. Ark
//     documents no default at all for the 2.0 and 1.0 families and does not
//     say what omitting the field does, so an unknown duration means the
//     model's maximum: 30s on 2.5, 15s on 2.0, 12s on 1.0 pro.
//
//     The maximum is a DECIDED policy, not a placeholder (2026-09-30). It
//     prices a bare 2.5 submit - model and prompt, no duration - at 30
//     seconds, roughly six times what a 5 second clip really costs, and that
//     pre-charge is what a small balance has to clear to submit at all. It
//     stands because the reservation is an estimate the reconcile settles: the
//     difference comes back as soon as Ark reports the real usage, so the cost
//     of guessing high is a temporary hold, while the cost of guessing low is
//     a 30 second video rendered against a balance that could only afford 5.
//     Any lower number would be one nothing upstream supports.
//  3. Input video. The formula adds the INPUT video's duration for an edit, an
//     extend or a reference-video task, and the input arrives as a URL or an
//     asset id whose length nothing here can know. Those tasks are therefore
//     under-estimated by the length of their own input, and only the reconcile
//     corrects it. Worth knowing before reading a pre-charge as a promise.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// videoGeneration is a Seedance family. Ark publishes a separate output pixel
// table per family, and they differ - so the family is part of the price.
type videoGeneration int

const (
	// genUnknown is a model id this plugin does not recognise. That includes
	// every Endpoint ID (ep-...), which carries no model name at all, and any
	// model released after this table was written.
	genUnknown videoGeneration = iota
	gen25                      // Seedance 2.5
	gen20                      // Seedance 2.0, 2.0 fast, 2.0 mini
	gen10                      // Seedance 1.0 pro, 1.0 pro fast
)

// Aspect ratios Ark accepts for ratio, plus adaptive.
const (
	Ratio169      = "16:9"
	Ratio43       = "4:3"
	Ratio11       = "1:1"
	Ratio34       = "3:4"
	Ratio916      = "9:16"
	Ratio219      = "21:9"
	RatioAdaptive = "adaptive"
)

// VideoRatios are the concrete ratios. adaptive is deliberately not one of
// them: it resolves upstream from the input material, so it means "the ratio
// is not knowable here", not "a seventh ratio".
var VideoRatios = []string{Ratio169, Ratio43, Ratio11, Ratio34, Ratio916, Ratio219}

// videoFPS is Ark's fixed frame rate for every Seedance model. It is not a
// request parameter - there is no fps field and no documented --fps command;
// a finished task echoes it back as framespersecond.
const videoFPS = 24

// framesFor is the frame count Ark bills for a clip of the given length:
// seconds x 24 + 1. Measured against the real upstream on 2026-09-30
// (doubao-seedance-2-0-mini-260615, 480p 16:9, duration 4): completion_tokens
// 40594 = floor(97 x 864x496 / 1024). 96 frames gives 40176, and no pixel area
// fits 96 frames exactly. Ark's published formula (seconds x fps) omits that
// frame, so every duration-based estimate came out about 1/(24N+1) low. An
// explicit frames parameter is already the rendered count and is used as is.
func framesFor(seconds int64) int64 { return seconds*videoFPS + 1 }

// pixelArea is width x height of one output, by generation, resolution tier
// and aspect ratio, transcribed from Ark's own table on the create-task page
// (its three "宽高像素值" columns, one per generation).
//
// Only the AREA is kept, because that is all the formula multiplies by. The
// dimensions are in the comment on each row so the table can be checked
// against the documentation cell by cell without arithmetic, and
// TestPixelAreasMatchArksTable multiplies them back out.
//
// A missing cell means the generation does not offer the tier: 4k is on
// doubao-seedance-2-0-260128 alone, 1080p is not on 2.0 fast/mini, and there
// is no 2k anywhere in Ark's vocabulary.
var pixelArea = map[videoGeneration]map[string]map[string]int64{
	gen25: {
		Res480: { // 854x480  752x560  640x640  560x752  480x854  992x432
			Ratio169: 409920, Ratio43: 421120, Ratio11: 409600,
			Ratio34: 421120, Ratio916: 409920, Ratio219: 428544,
		},
		Res720: { // 1280x720  1112x834  960x960  834x1112  720x1280  1470x630
			Ratio169: 921600, Ratio43: 927408, Ratio11: 921600,
			Ratio34: 927408, Ratio916: 921600, Ratio219: 926100,
		},
		Res1080: { // 1920x1080  1664x1248  1440x1440  1248x1664  1080x1920  2206x946
			Ratio169: 2073600, Ratio43: 2076672, Ratio11: 2073600,
			Ratio34: 2076672, Ratio916: 2073600, Ratio219: 2086876,
		},
	},
	gen20: {
		Res480: { // 864x496  752x560  640x640  560x752  496x864  992x432
			Ratio169: 428544, Ratio43: 421120, Ratio11: 409600,
			Ratio34: 421120, Ratio916: 428544, Ratio219: 428544,
		},
		Res720: { // 1280x720  1112x834  960x960  834x1112  720x1280  1470x630
			Ratio169: 921600, Ratio43: 927408, Ratio11: 921600,
			Ratio34: 927408, Ratio916: 921600, Ratio219: 926100,
		},
		Res1080: { // 1920x1080  1664x1248  1440x1440  1248x1664  1080x1920  2206x946
			Ratio169: 2073600, Ratio43: 2076672, Ratio11: 2073600,
			Ratio34: 2076672, Ratio916: 2073600, Ratio219: 2086876,
		},
		Res4K: { // 3840x2160  3326x2494  2880x2880  2494x3326  2160x3840  4398x1886
			Ratio169: 8294400, Ratio43: 8295044, Ratio11: 8294400,
			Ratio34: 8295044, Ratio916: 8294400, Ratio219: 8294628,
		},
	},
	gen10: {
		Res480: { // 864x480  736x544  640x640  544x736  480x864  960x416
			Ratio169: 414720, Ratio43: 400384, Ratio11: 409600,
			Ratio34: 400384, Ratio916: 414720, Ratio219: 399360,
		},
		Res720: { // 1248x704  1120x832  960x960  832x1120  704x1248  1504x640
			Ratio169: 878592, Ratio43: 931840, Ratio11: 921600,
			Ratio34: 931840, Ratio916: 878592, Ratio219: 962560,
		},
		Res1080: { // 1920x1088  1664x1248  1440x1440  1248x1664  1088x1920  2176x928
			Ratio169: 2088960, Ratio43: 2076672, Ratio11: 2073600,
			Ratio34: 2076672, Ratio916: 2088960, Ratio219: 2019328,
		},
	},
}

// modelProfile is what the estimate needs to know about a model when the
// request does not say it.
type modelProfile struct {
	gen videoGeneration
	// defaultResolution is the tier Ark uses when the request omits
	// resolution. Ark documents this one per model, unlike duration.
	defaultResolution string
	// maxResolution is the most expensive tier the model offers.
	maxResolution string
	// maxDurationSec is the top of the model's duration range: the bound for
	// duration -1, for an omitted duration, and for a duration that could have
	// been hidden in a prompt this estimate could not read.
	maxDurationSec int64
}

// profileOf classifies a model id.
//
// The table is coarse and has to stay that way: Ark has no API-key-callable
// model list (BuildModelsRequest answers Unimplemented), an administrator
// enters the models, and a model registry here would be a second source of
// truth for something the administrator already states.
//
// An id this does not recognise gets the conservative profile - which is
// deliberately NOT the most expensive profile that exists. Exactly one model
// does 4k, so assuming 4k for every unrecognised id would quadruple the
// pre-charge of every Endpoint ID an administrator registers. And it matters
// much less than it used to: when the request states its resolution, which is
// now the normal case, this default is never reached.
func profileOf(model string) modelProfile {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "seedance-2-5"):
		// 2.5 stops at 1080p. It does NOT do 4k - the newer family is the one
		// without the highest tier, which reads backwards and is the reason
		// this case exists before the 2-0 one.
		return modelProfile{gen: gen25, defaultResolution: Res720, maxResolution: Res1080, maxDurationSec: 30}
	case strings.Contains(m, "seedance-2-0"):
		if strings.Contains(m, "fast") || strings.Contains(m, "mini") {
			// fast and mini offer 480p and 720p only.
			return modelProfile{gen: gen20, defaultResolution: Res720, maxResolution: Res720, maxDurationSec: 15}
		}
		return modelProfile{gen: gen20, defaultResolution: Res720, maxResolution: Res4K, maxDurationSec: 15}
	case strings.Contains(m, "seedance-1-0"):
		return modelProfile{gen: gen10, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 12}
	default:
		return modelProfile{gen: genUnknown, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 30}
	}
}

// tierArea returns the output area of one tier. An empty ratio - unknown,
// adaptive, or two readings that disagree - takes the LARGEST area the tier
// can produce, the only upper bound available.
//
// This cannot be shortened into "assume 21:9": the widest ratio is not the
// largest in every row. On Seedance 1.0, 21:9 is the most expensive tier at
// 720p (1504x640) and the CHEAPEST at 1080p (2176x928).
//
// An unrecognised generation takes the largest area any generation publishes
// for the tier, which is what makes a model id this plugin has never heard of
// an over-estimate rather than a zero.
func tierArea(gen videoGeneration, tier, ratio string) int64 {
	if table, ok := pixelArea[gen]; ok {
		return largestIn(table[tier], ratio)
	}
	var best int64
	for g := range pixelArea {
		if a := largestIn(pixelArea[g][tier], ratio); a > best {
			best = a
		}
	}
	return best
}

func largestIn(row map[string]int64, ratio string) int64 {
	if row == nil {
		return 0
	}
	if ratio != "" {
		if a, ok := row[ratio]; ok {
			return a
		}
	}
	var best int64
	for _, a := range row {
		if a > best {
			best = a
		}
	}
	return best
}

// ---------------------------------------------------------------- the declaration

// The request paths manifest.json declares in usageRequestFields, in the order
// it lists them. The host hands every declared value over whole (CONTRACTS
// §25.6), so the order no longer matters to what arrives.
const (
	PathResolution   = "resolution"
	PathRatio        = "ratio"
	PathDuration     = "duration"
	PathFrames       = "frames"
	PathContentCount = "content.#"
)

// ContentTextPaths are the content elements whose text is read for "--"
// parameters.
//
// A path in usageRequestFields has to read ONE value (the manifest checker
// enforces it with a round trip through sjson), so "content.#.text" is not
// expressible and the array has to be spelt out element by element. Three is
// not a guess about how long the array is: PathContentCount says how long it
// really is, and a request with more elements than these is treated as one
// whose prompt could not be fully read.
var ContentTextPaths = []string{"content.0.text", "content.1.text", "content.2.text"}

// UsageRequestFields is exactly what manifest.json declares, in order.
// TestManifestDeclaresTheFieldsTheEstimateReads asserts the two agree: a path
// the manifest does not declare is never delivered, and the estimate would
// quietly read a default instead of the client's own value.
var UsageRequestFields = append([]string{
	PathResolution, PathRatio, PathDuration, PathFrames, PathContentCount, "content.#.type", "draft",
}, ContentTextPaths...)

// ---------------------------------------------------------------- reading the request

// videoSpec is what the submit request says about the size of the job.
type videoSpec struct {
	// Resolution is a tier from VideoResolutions, "" when not determinable.
	Resolution string
	// ResolutionStated is true when the client sent a resolution, whether or
	// not it could be read: it may name a tier this plugin has no pixel size
	// for.
	//
	// It exists because "" alone is two different situations with two
	// different safe answers. An ABSENT resolution is a known quantity - Ark
	// applies the model's documented default, so the estimate can too. A
	// resolution that was STATED and not readable is not: the client asked for
	// something and the only bound is the model's most expensive tier. Reading
	// the second as the first is how the first version of this file managed to
	// price an unreadable request exactly like an empty one.
	ResolutionStated bool
	// Ratio is a ratio from VideoRatios, "" for adaptive, absent, or two
	// readings that disagree. There is no Stated twin: an absent ratio means
	// adaptive, which is already unknown, so both cases take the tier's
	// largest area.
	Ratio string
	// Frames is the output frame count, 0 when unknown.
	Frames int64
	// Seconds is the output duration, 0 when unknown - which includes Ark's
	// -1, "the model chooses". No Stated twin either: Ark documents no default
	// duration for four of its six models, so absent is already unknown.
	Seconds int64
	// PromptFullyRead is false when a content element's text was not
	// delivered, or when there are more elements than ContentTextPaths covers.
	// Any parameter can hide in that text, so a false here bounds every
	// parameter regardless of what the structured fields said.
	PromptFullyRead bool
}

// readVideoSpec reads the declared request fields the host delivered: fields
// holds the raw JSON of each declared path the request carries. A path that
// is absent means the client left it out, so Ark's documented default
// applies.
func readVideoSpec(fields map[string]string) videoSpec {
	spec := videoSpec{PromptFullyRead: true}

	// The prompt first: whether it was read in full decides what the rest is
	// worth.
	texts := make([]string, 0, len(ContentTextPaths))
	for _, p := range ContentTextPaths {
		if raw, ok := fields[p]; ok {
			texts = append(texts, gjson.Parse(raw).String())
		}
	}
	// content.# is the element count. Absent means the body has no content
	// array at all, hence no prompt to hide anything in.
	if n, ok := jsonInt(fields[PathContentCount]); ok && n > int64(len(ContentTextPaths)) {
		spec.PromptFullyRead = false
	}

	// Structured values and every "--" command in the prompt are all
	// CANDIDATES. Neither form is authoritative: Ark documents them as
	// equivalent and states no precedence, so the caller takes the most
	// expensive reading rather than picking a winner the documentation does
	// not name.
	var (
		resolutions, ratios []string
		durations, frames   []int64
	)
	resolutions, spec.ResolutionStated = readCandidates(PathResolution, fields,
		func(raw string) (string, bool) { return normalizeResolution(gjson.Parse(raw).String()) })
	ratios, _ = readCandidates(PathRatio, fields,
		func(raw string) (string, bool) { return normalizeRatio(gjson.Parse(raw).String()) })
	durations, _ = readCandidates(PathDuration, fields, jsonInt)
	frames, _ = readCandidates(PathFrames, fields, jsonInt)

	for _, text := range texts {
		for key, v := range parsePromptCommands(text) {
			switch key {
			case "rs", "resolution":
				if tier, ok := normalizeResolution(v); ok {
					resolutions = append(resolutions, tier)
				}
			case "rt", "ratio":
				if r, ok := normalizeRatio(v); ok {
					ratios = append(ratios, r)
				}
			case "dur", "duration":
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					durations = append(durations, n)
				}
			case "frames":
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					frames = append(frames, n)
				}
			}
		}
	}

	spec.Resolution = mostExpensiveResolution(resolutions)
	spec.Ratio = agreedRatio(ratios)
	spec.Seconds = largestPositive(durations)
	spec.Frames = largestPositive(frames)
	return spec
}

// readCandidates reads one declared field of the video request as a list of
// candidate values: when present, the value is normalized and appended if it
// parses; when absent, the field contributes no candidate at all. stated
// reports whether the client sent the field, which only the resolution needs.
func readCandidates[T any](path string, fields map[string]string, normalize func(string) (T, bool)) (vals []T, stated bool) {
	raw, ok := fields[path]
	if !ok {
		return nil, false
	}
	if v, ok := normalize(raw); ok {
		vals = append(vals, v)
	}
	return vals, true
}

// jsonInt reads an integer out of a raw JSON value. gjson coerces a quoted
// number, so a client that sends "duration": "5" is read rather than
// estimated at the model's maximum.
func jsonInt(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	r := gjson.Parse(raw)
	if !r.Exists() {
		return 0, false
	}
	return r.Int(), true
}

// mostExpensiveResolution picks between contradictory readings.
func mostExpensiveResolution(candidates []string) string {
	best := ""
	var bestArea int64
	for _, tier := range candidates {
		// Ranked by the largest area any generation gives the tier, so the
		// ranking comes from the table rather than from a hand-written
		// 480<720<1080<4k list a new tier would silently fall off.
		if a := tierArea(genUnknown, tier, ""); a > bestArea {
			best, bestArea = tier, a
		}
	}
	return best
}

// agreedRatio returns the ratio only when every reading names the same
// concrete one. Disagreement, adaptive and absence all mean unknown, which
// tierArea turns into the tier's largest area.
func agreedRatio(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	for _, r := range candidates[1:] {
		if r != candidates[0] {
			return ""
		}
	}
	return candidates[0]
}

// largestPositive is the most expensive of several readings. 0 and negative
// values are not readings: -1 is Ark's "the model chooses".
func largestPositive(vs []int64) int64 {
	var best int64
	for _, v := range vs {
		if v > best {
			best = v
		}
	}
	return best
}

// normalizeResolution maps a value onto a declared tier. "2k" and anything
// else unrecognised is NOT silently rounded to a tier: Ark publishes no pixel
// size for it, so a number derived from it would have nothing behind it. It
// reads as unknown, which bounds the estimate instead.
func normalizeResolution(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, tier := range VideoResolutions {
		if v == tier {
			return tier, true
		}
	}
	return "", false
}

// normalizeRatio maps a value onto a concrete ratio. adaptive is a valid
// request value and not a concrete ratio - it resolves upstream from the
// input material - so it reads as unknown.
func normalizeRatio(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, r := range VideoRatios {
		if v == r {
			return r, true
		}
	}
	return "", false
}

// promptCommandRe matches one "--flag value" or "--flag=value" in a prompt.
// The value ends at whitespace, so "--rs 720p --dur 5" reads as two.
var promptCommandRe = regexp.MustCompile(`--([A-Za-z_]+)[=\s]+([^\s]+)`)

// parsePromptCommands reads the "--" suffixes Ark accepts on content[].text.
//
// Both the short names Ark documents today (--rs --rt --dur, alongside --seed
// --cf --wm which do not affect the price) and the long ones (--resolution
// --ratio --duration --frames) are read. The long forms are NOT in the current
// documentation - they come from the earlier Seedance 1.0 material and are all
// over third-party examples - so whether Ark still honours them is unknown.
// Reading them anyway is the safe direction either way: if Ark honours them
// the estimate is right, and if Ark ignores them the estimate is high, which
// the reconcile refunds. NOT reading them is the direction that loses money.
func parsePromptCommands(text string) map[string]string {
	if !strings.Contains(text, "--") {
		return nil
	}
	out := map[string]string{}
	for _, m := range promptCommandRe.FindAllStringSubmatch(text, -1) {
		key := strings.ToLower(m[1])
		if _, seen := out[key]; !seen {
			out[key] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// ---------------------------------------------------------------- the estimate

// Names of the things an estimate had to bound instead of read. They go into
// the log line that explains an unexpectedly large pre-charge, so they are
// stable strings, not prose.
const (
	assumedPrompt     = "prompt_not_fully_read"
	assumedResolution = "resolution"
	assumedRatio      = "ratio"
	assumedDuration   = "duration"
	assumedPixelArea  = "pixel_area"
)

// videoEstimate is the reservation figure and how much of it was read rather
// than assumed.
type videoEstimate struct {
	Tokens  int64
	Seconds float64
	Pixels  int64
	Width   int64
	Height  int64
	// Resolution is the tier the estimate priced, reported as the resolution
	// fact: the requested tier when that was readable, the model's default or
	// maximum when it was not. So the fact says what was CHARGED FOR, which is
	// the only honest thing for it to say while the task runs; the reconcile
	// replaces it with what Ark really rendered.
	Resolution string
	// Assumed is empty when every input was read from the request.
	Assumed []string
}

// estimateVideo turns a model and a spec into the reservation.
func estimateVideo(model string, spec videoSpec) videoEstimate {
	p := profileOf(model)
	est := videoEstimate{}

	tier, ratio, frames, seconds := spec.Resolution, spec.Ratio, spec.Frames, spec.Seconds
	// Whether an unreadable resolution has to be bounded by the model's
	// MAXIMUM rather than its default. Computed before the prompt check zeroes
	// everything, because that check is one of the two ways it becomes true.
	resolutionUnknowable := !spec.PromptFullyRead || (spec.ResolutionStated && tier == "")

	if !spec.PromptFullyRead {
		// All four could have been set in text that was not delivered, and Ark
		// names no precedence between the two forms - so none of them can be
		// trusted and all four are bounded: reading unread text as "the client
		// sent nothing" would bound nothing and under-charge every time.
		tier, ratio, frames, seconds = "", "", 0, 0
		est.Assumed = append(est.Assumed, assumedPrompt)
	}

	if tier == "" {
		// A resolution nobody could read is bounded by the model's most
		// expensive tier; one the client simply did not send is Ark's
		// documented default for that model, which is a fact rather than a
		// bound.
		if resolutionUnknowable {
			tier = p.maxResolution
		} else {
			tier = p.defaultResolution
		}
		est.Assumed = append(est.Assumed, assumedResolution)
	}
	if ratio == "" {
		est.Assumed = append(est.Assumed, assumedRatio)
	}
	if frames <= 0 {
		if seconds > 0 {
			frames = framesFor(seconds)
		} else {
			frames = framesFor(p.maxDurationSec)
			est.Assumed = append(est.Assumed, assumedDuration)
		}
	}

	area := tierArea(p.gen, tier, ratio)
	if area == 0 {
		// The generation publishes no pixel size for this tier: 4k asked of a
		// model that has none, or a tier Ark added after this table. Take the
		// largest area any generation gives the tier, and failing that the
		// model's own maximum tier - an area of 0 would make the submit free,
		// which is the one answer that must not come out of here.
		if area = tierArea(genUnknown, tier, ratio); area == 0 {
			tier = p.maxResolution
			area = tierArea(p.gen, tier, "")
		}
		est.Assumed = append(est.Assumed, assumedPixelArea)
	}
	est.Resolution = tier
	est.Tokens = frames * area / 1024
	est.Seconds = float64(frames-1) / videoFPS
	est.Pixels = area
	// Recover the exact table dimensions, never dimensions rounded from a
	// resolution tier. Adaptive output uses the largest table entry above.
	if ratio == "" {
		for _, candidate := range VideoRatios {
			if tierArea(p.gen, tier, candidate) == area {
				ratio = candidate
				break
			}
		}
	}
	est.Width, est.Height = dimensionsForArea(area, ratio)
	return est
}
