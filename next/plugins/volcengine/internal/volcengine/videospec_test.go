package volcengine

// Tests of the pre-charge estimate. Everything here is a money test: the
// number estimateVideo returns is charged against a real balance the moment
// the submit succeeds, and it is only corrected days later when the task
// finishes. The three ways it can be wrong without anyone noticing are
//
//	a value read out of the request incorrectly,
//	a value the host would not carry read as a value the client never sent,
//	a request the estimate cannot fully see, bounded downwards,
//
// and each has its own test below.

import (
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- the pixel table

// arkPixelTable is Ark's published table written out a second time, as
// dimensions instead of areas, in the layout of the documentation (one row per
// resolution x generation, the six ratios in VideoRatios order). pixelArea
// holds the products; this holds the factors, so a transposed or mistyped cell
// shows up as a mismatch rather than as a plausible number.
var arkPixelTable = []struct {
	gen  videoGeneration
	tier string
	dims [6]string // 16:9, 4:3, 1:1, 3:4, 9:16, 21:9
}{
	{gen25, Res480, [6]string{"854x480", "752x560", "640x640", "560x752", "480x854", "992x432"}},
	{gen25, Res720, [6]string{"1280x720", "1112x834", "960x960", "834x1112", "720x1280", "1470x630"}},
	{gen25, Res1080, [6]string{"1920x1080", "1664x1248", "1440x1440", "1248x1664", "1080x1920", "2206x946"}},

	{gen20, Res480, [6]string{"864x496", "752x560", "640x640", "560x752", "496x864", "992x432"}},
	{gen20, Res720, [6]string{"1280x720", "1112x834", "960x960", "834x1112", "720x1280", "1470x630"}},
	{gen20, Res1080, [6]string{"1920x1080", "1664x1248", "1440x1440", "1248x1664", "1080x1920", "2206x946"}},
	{gen20, Res4K, [6]string{"3840x2160", "3326x2494", "2880x2880", "2494x3326", "2160x3840", "4398x1886"}},

	{gen10, Res480, [6]string{"864x480", "736x544", "640x640", "544x736", "480x864", "960x416"}},
	{gen10, Res720, [6]string{"1248x704", "1120x832", "960x960", "832x1120", "704x1248", "1504x640"}},
	{gen10, Res1080, [6]string{"1920x1088", "1664x1248", "1440x1440", "1248x1664", "1088x1920", "2176x928"}},
}

func dims(t *testing.T, s string) int64 {
	t.Helper()
	w, h, ok := strings.Cut(s, "x")
	if !ok {
		t.Fatalf("bad dimensions %q", s)
	}
	wi, err1 := strconv.ParseInt(w, 10, 64)
	hi, err2 := strconv.ParseInt(h, 10, 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("bad dimensions %q", s)
	}
	return wi * hi
}

// TestPixelAreasMatchArksTable checks pixelArea cell by cell against the
// dimensions, and that neither table has a cell the other lacks.
func TestPixelAreasMatchArksTable(t *testing.T) {
	seen := map[videoGeneration]map[string]bool{}
	for _, row := range arkPixelTable {
		if seen[row.gen] == nil {
			seen[row.gen] = map[string]bool{}
		}
		seen[row.gen][row.tier] = true
		for i, ratio := range VideoRatios {
			want := dims(t, row.dims[i])
			got, ok := pixelArea[row.gen][row.tier][ratio]
			if !ok {
				t.Errorf("pixelArea[%d][%s][%s] is missing, want %d (%s)", row.gen, row.tier, ratio, want, row.dims[i])
				continue
			}
			if got != want {
				t.Errorf("pixelArea[%d][%s][%s] = %d, want %d (%s)", row.gen, row.tier, ratio, got, want, row.dims[i])
			}
		}
	}
	for gen, tiers := range pixelArea {
		for tier, row := range tiers {
			if !seen[gen][tier] {
				t.Errorf("pixelArea has [%d][%s], which Ark's table does not", gen, tier)
			}
			if len(row) != len(VideoRatios) {
				t.Errorf("pixelArea[%d][%s] has %d ratios, want %d", gen, tier, len(row), len(VideoRatios))
			}
		}
	}
	// The tiers each generation does NOT have, which is half of what the table
	// says: 4k is one model's, and 2k is nobody's.
	if _, ok := pixelArea[gen25][Res4K]; ok {
		t.Error("Seedance 2.5 does not do 4k; giving it a 4k area would over-charge a tier it cannot render")
	}
	if _, ok := pixelArea[gen10][Res4K]; ok {
		t.Error("Seedance 1.0 does not do 4k")
	}
	for gen := range pixelArea {
		if _, ok := pixelArea[gen]["2k"]; ok {
			t.Errorf("generation %d has a 2k area; Ark publishes no 2k tier at all", gen)
		}
	}
}

// TestTierAreaWithoutARatioTakesTheLargest: an unknown, adaptive or
// contradictory ratio has to bound the area from ABOVE, because the ratio is
// what decides the pixel count within a tier.
func TestTierAreaWithoutARatioTakesTheLargest(t *testing.T) {
	for _, row := range arkPixelTable {
		var max int64
		for i := range VideoRatios {
			if a := dims(t, row.dims[i]); a > max {
				max = a
			}
		}
		if got := tierArea(row.gen, row.tier, ""); got != max {
			t.Errorf("tierArea(%d, %s, unknown ratio) = %d, want the row maximum %d", row.gen, row.tier, got, max)
		}
		// And a known ratio is that ratio's own cell, not the maximum.
		for i, ratio := range VideoRatios {
			if got, want := tierArea(row.gen, row.tier, ratio), dims(t, row.dims[i]); got != want {
				t.Errorf("tierArea(%d, %s, %s) = %d, want %d", row.gen, row.tier, ratio, got, want)
			}
		}
	}
}

// TestTheWidestRatioIsNotAlwaysTheDearest pins the fact that stops "just
// assume 21:9" from being a valid shortcut: on Seedance 1.0, 21:9 is the most
// expensive ratio at 720p and the CHEAPEST at 1080p. A reader who "simplifies"
// tierArea into a single rule breaks one of these two.
func TestTheWidestRatioIsNotAlwaysTheDearest(t *testing.T) {
	if a, b := pixelArea[gen10][Res720][Ratio219], pixelArea[gen10][Res720][Ratio169]; a <= b {
		t.Errorf("gen10 720p: 21:9 (%d) should be dearer than 16:9 (%d)", a, b)
	}
	if a := pixelArea[gen10][Res1080][Ratio219]; a != tierArea(gen10, Res1080, "") {
		// Expected: it is NOT the maximum. Assert that positively.
		if a >= tierArea(gen10, Res1080, "") {
			t.Errorf("gen10 1080p: 21:9 (%d) should not be the largest", a)
		}
	} else {
		t.Error("gen10 1080p: 21:9 is the largest area, which contradicts Ark's table")
	}
	// Seedance 2.x really is near-constant within a tier, which is why an
	// unknown ratio costs almost nothing there - worth pinning so a future
	// table edit that breaks it is noticed.
	for _, tier := range []string{Res720, Res1080, Res4K} {
		row := pixelArea[gen20][tier]
		var min, max int64
		for _, a := range row {
			if min == 0 || a < min {
				min = a
			}
			if a > max {
				max = a
			}
		}
		if spread := (max - min) * 1000 / min; spread > 10 { // 1.0%
			t.Errorf("gen20 %s spread is %d per mille; an unknown ratio is no longer a cheap assumption", tier, spread)
		}
	}
	// Seedance 1.0's 720p row is the opposite: nearly 10% apart, so it cannot
	// be modelled as one area per tier.
	row := pixelArea[gen10][Res720]
	if spread := (row[Ratio219] - row[Ratio169]) * 100 / row[Ratio169]; spread < 5 {
		t.Errorf("gen10 720p spread is only %d%%; the table has changed shape", spread)
	}
}

// ---------------------------------------------------------------- model profiles

func TestProfileOf(t *testing.T) {
	cases := map[string]modelProfile{
		// The six Seedance model ids Ark currently lists.
		"doubao-seedance-2-5-260628": {gen: gen25, defaultResolution: Res720, maxResolution: Res1080, maxDurationSec: 30},
		"doubao-seedance-2-0-260128": {gen: gen20, defaultResolution: Res720, maxResolution: Res4K, maxDurationSec: 15},
		// fast and mini stop at 720p, so their maximum is NOT 1080p.
		"doubao-seedance-2-0-fast-260128":     {gen: gen20, defaultResolution: Res720, maxResolution: Res720, maxDurationSec: 15},
		"doubao-seedance-2-0-mini-260615":     {gen: gen20, defaultResolution: Res720, maxResolution: Res720, maxDurationSec: 15},
		"doubao-seedance-1-0-pro-250528":      {gen: gen10, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 12},
		"doubao-seedance-1-0-pro-fast-251015": {gen: gen10, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 12},
		// An Endpoint ID says nothing about the model behind it, and neither
		// does a model nobody has registered.
		"ep-20260101-abcde":                {gen: genUnknown, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 30},
		"some-model-nobody-registered-yet": {gen: genUnknown, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 30},
		"":                                 {gen: genUnknown, defaultResolution: Res1080, maxResolution: Res1080, maxDurationSec: 30},
		// Case and surrounding space are not part of the identity.
		"  DOUBAO-SEEDANCE-2-5-260628 ": {gen: gen25, defaultResolution: Res720, maxResolution: Res1080, maxDurationSec: 30},
	}
	for model, want := range cases {
		if got := profileOf(model); got != want {
			t.Errorf("profileOf(%q) = %+v, want %+v", model, got, want)
		}
	}
	// The one that reads backwards: 2.5 is newer than 2.0 and has a LOWER
	// maximum tier, because 4k is 2.0's alone. A "later is bigger" shortcut
	// would over-charge every 2.5 request whose resolution is not readable.
	if profileOf("doubao-seedance-2-5-260628").maxResolution == Res4K {
		t.Error("Seedance 2.5 does not support 4k")
	}
}

// ---------------------------------------------------------------- reading the request

// jsonFields builds an ExtractUsageRequest.fields map the way the host does:
// the RAW JSON of each value, so a string arrives quoted and a number does not.
func jsonFields(kv map[string]string) map[string]string { return kv }

func TestReadVideoSpecStructuredFields(t *testing.T) {
	spec := readVideoSpec(jsonFields(map[string]string{
		PathResolution:   `"1080p"`,
		PathRatio:        `"21:9"`,
		PathDuration:     `7`,
		PathContentCount: `1`,
		"content.0.text": `"a cat yawning at the camera"`,
	}))
	want := videoSpec{Resolution: Res1080, ResolutionStated: true, Ratio: Ratio219, Seconds: 7, PromptFullyRead: true}
	if spec != want {
		t.Fatalf("spec = %+v, want %+v", spec, want)
	}

	// frames wins over duration, the way Ark documents it, and is used as a
	// frame count rather than being turned into whole seconds first (133 frames
	// is 5.54s, which Ark itself reports as the integer 5 - rounding here would
	// under-count by half a second every time).
	spec = readVideoSpec(map[string]string{
		PathResolution: `"720p"`, PathDuration: `5`, PathFrames: `133`, PathContentCount: `1`,
	})
	if spec.Frames != 133 || spec.Seconds != 5 {
		t.Fatalf("frames/seconds = %d/%d", spec.Frames, spec.Seconds)
	}
	est := estimateVideo("doubao-seedance-1-0-pro-250528", spec)
	if want := int64(133) * pixelArea[gen10][Res720][Ratio219] / 1024; est.Tokens != want {
		t.Fatalf("frames estimate = %d, want %d (133 frames, not 5 seconds)", est.Tokens, want)
	}

	// -1 is "the model chooses", not a duration.
	spec = readVideoSpec(map[string]string{PathResolution: `"720p"`, PathDuration: `-1`, PathContentCount: `1`})
	if spec.Seconds != 0 {
		t.Fatalf("duration -1 read as %d seconds", spec.Seconds)
	}
	// A client that sends a number as a string is still read, rather than
	// falling through to the model's maximum.
	spec = readVideoSpec(map[string]string{PathDuration: `"6"`})
	if spec.Seconds != 6 {
		t.Fatalf(`duration "6" read as %d`, spec.Seconds)
	}
	// An unrecognised tier is not rounded to a neighbour: Ark publishes no
	// pixel size for "2k", so there is no number to derive.
	spec = readVideoSpec(map[string]string{PathResolution: `"2k"`})
	if spec.Resolution != "" {
		t.Fatalf(`resolution "2k" read as %q`, spec.Resolution)
	}
	// adaptive is a real request value and not a concrete ratio.
	spec = readVideoSpec(map[string]string{PathRatio: `"adaptive"`})
	if spec.Ratio != "" {
		t.Fatalf("adaptive read as ratio %q", spec.Ratio)
	}
}

// TestReadVideoSpecPromptCommands: Ark accepts the same parameters as "--"
// suffixes on the prompt, on every model, as an equivalent weakly validated
// form. An estimate that only read the structured fields would price a
// "--rs 1080p --dur 10" request at the model's defaults - which is a client
// paying the 720p rate for a 1080p render.
func TestReadVideoSpecPromptCommands(t *testing.T) {
	spec := readVideoSpec(map[string]string{
		PathContentCount: `1`,
		"content.0.text": `"小猫对着镜头打哈欠 --rs 1080p --rt 16:9 --dur 10 --seed 11 --cf false --wm true"`,
	})
	want := videoSpec{Resolution: Res1080, Ratio: Ratio169, Seconds: 10, PromptFullyRead: true}
	if spec != want {
		t.Fatalf("spec = %+v, want %+v", spec, want)
	}

	// The long forms too. They are not in Ark's current documentation, so
	// whether they are honoured is unknown - reading them can only over-
	// estimate, which a reconcile refunds, while ignoring them under-charges.
	spec = readVideoSpec(map[string]string{
		PathContentCount: `1`,
		"content.0.text": `"a cat --resolution 4k --duration 12 --ratio 4:3"`,
	})
	if spec.Resolution != Res4K || spec.Seconds != 12 || spec.Ratio != Ratio43 {
		t.Fatalf("long-form commands: %+v", spec)
	}
	// "--flag=value" as well as "--flag value".
	spec = readVideoSpec(map[string]string{PathContentCount: `1`, "content.0.text": `"x --rs=720p --dur=4"`})
	if spec.Resolution != Res720 || spec.Seconds != 4 {
		t.Fatalf("equals form: %+v", spec)
	}
	// The command may sit in any of the declared content elements: an
	// image-to-video request puts the image first and the text second.
	spec = readVideoSpec(map[string]string{
		PathContentCount: `2`,
		"content.1.text": `"a cat --rs 1080p --dur 8"`,
	})
	if spec.Resolution != Res1080 || spec.Seconds != 8 {
		t.Fatalf("command in content[1]: %+v", spec)
	}
	// A prompt that merely contains dashes is not a command.
	spec = readVideoSpec(map[string]string{
		PathContentCount: `1`, "content.0.text": `"a well-lit scene -- dramatic, high-contrast"`,
	})
	if spec.Resolution != "" || spec.Seconds != 0 || spec.Ratio != "" {
		t.Fatalf("prose read as commands: %+v", spec)
	}
}

// TestTheMoreExpensiveReadingWins: when the structured field and the prompt
// disagree, Ark documents NO precedence. Picking the cheaper one would be an
// under-charge every time a client contradicts itself, deliberately or not.
func TestTheMoreExpensiveReadingWins(t *testing.T) {
	// Structured 480p, prompt 4k. Neither form is authoritative, so 4k.
	spec := readVideoSpec(map[string]string{
		PathResolution: `"480p"`, PathDuration: `4`, PathContentCount: `1`,
		"content.0.text": `"a cat --rs 4k --dur 15"`,
	})
	if spec.Resolution != Res4K {
		t.Fatalf("resolution = %q, want 4k (the dearer of the two readings)", spec.Resolution)
	}
	if spec.Seconds != 15 {
		t.Fatalf("seconds = %d, want 15 (the longer of the two readings)", spec.Seconds)
	}
	// The other way round: prompt cheap, structured expensive.
	spec = readVideoSpec(map[string]string{
		PathResolution: `"4k"`, PathDuration: `15`, PathContentCount: `1`,
		"content.0.text": `"a cat --rs 480p --dur 4"`,
	})
	if spec.Resolution != Res4K || spec.Seconds != 15 {
		t.Fatalf("spec = %+v, want the expensive reading either way", spec)
	}
	// Two ratios that disagree cannot both be right, and a ratio is not
	// ordered by price the way a tier is, so the answer is "unknown" - which
	// takes the tier's largest area.
	spec = readVideoSpec(map[string]string{
		PathRatio: `"1:1"`, PathContentCount: `1`, "content.0.text": `"a cat --rt 21:9"`,
	})
	if spec.Ratio != "" {
		t.Fatalf("contradictory ratios read as %q, want unknown", spec.Ratio)
	}
}

// TestUnreadableRequestIsBoundedFromAbove: a request that states something
// this plugin cannot read is priced at the model's maximum, never at its
// documented default.
func TestUnreadableRequestIsBoundedFromAbove(t *testing.T) {
	const model = "doubao-seedance-2-0-260128" // the one 4k model, 15s maximum

	absentRes := estimateVideo(model, readVideoSpec(map[string]string{PathDuration: `5`,
		PathContentCount: `1`, "content.0.text": `"a cat"`}))
	if absentRes.Resolution != Res720 {
		t.Fatalf("absent resolution priced at %q, want the model's documented default", absentRes.Resolution)
	}
	// A tier the client stated that this plugin has no pixel size for - "2k",
	// or whatever Ark adds next: something was asked for, and there is no
	// cell for it. Ark accepted it (a wrong value never reaches a 2xx), so it
	// is real, and the bound is the model's maximum.
	unknownTier := estimateVideo(model, readVideoSpec(map[string]string{
		PathResolution: `"2k"`, PathDuration: `5`, PathContentCount: `1`, "content.0.text": `"a cat"`,
	}))
	if unknownTier.Resolution != Res4K {
		t.Fatalf(`resolution "2k" priced at %q, want the model's maximum tier`, unknownTier.Resolution)
	}

	// A content array longer than the declared paths: the fourth element's
	// text is not delivered at all, so a command in it would never be seen,
	// and nothing the request said can be trusted - not even the structured
	// fields, because Ark states no precedence between the two forms.
	longArray := estimateVideo(model, readVideoSpec(map[string]string{
		PathResolution: `"480p"`, PathDuration: `4`, PathContentCount: `4`,
		"content.0.text": `"a cat"`,
	}))
	if !hasAssumption(longArray.Assumed, assumedPrompt) {
		t.Fatalf("a %d-element content array was treated as fully read: %v", 4, longArray.Assumed)
	}
	// It must be the model's worst case, because that is the only bound
	// available: 15 seconds at 4k.
	if want := int64(15*videoFPS+1) * tierArea(gen20, Res4K, "") / 1024; longArray.Tokens != want {
		t.Fatalf("content.# = 4 estimated %d, want the model's worst case %d", longArray.Tokens, want)
	}
	// Three elements is exactly what is declared, so it IS fully read.
	if spec := readVideoSpec(map[string]string{PathContentCount: `3`, "content.0.text": `"a cat"`}); !spec.PromptFullyRead {
		t.Fatal("a 3-element content array is fully covered by the declared paths")
	}
}

func hasAssumption(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- the estimate

// TestEstimateVideoExactNumbers checks the arithmetic against Ark's formula
// written the other way round: seconds x width x height x fps / 1024, using
// the dimensions rather than the areas.
func TestEstimateVideoExactNumbers(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		fields map[string]string
		// wantTokens is written as (seconds x fps + 1) x w x h / 1024.
		sec, w, h  int64
		wantTier   string
		wantAssume []string
	}{
		{
			name:  "2.0 pro, everything stated",
			model: "doubao-seedance-2-0-260128",
			fields: map[string]string{PathResolution: `"1080p"`, PathRatio: `"16:9"`, PathDuration: `5`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 5, w: 1920, h: 1080, wantTier: Res1080,
		},
		{
			name:  "1.0 pro at 720p 16:9 is 1248x704, not 1280x720",
			model: "doubao-seedance-1-0-pro-250528",
			fields: map[string]string{PathResolution: `"720p"`, PathRatio: `"16:9"`, PathDuration: `6`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 6, w: 1248, h: 704, wantTier: Res720,
		},
		{
			name:  "1.0 pro at 720p 21:9 is the dearest cell of that row",
			model: "doubao-seedance-1-0-pro-250528",
			fields: map[string]string{PathResolution: `"720p"`, PathRatio: `"21:9"`, PathDuration: `6`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 6, w: 1504, h: 640, wantTier: Res720,
		},
		{
			name:  "4k, the one model that has it",
			model: "doubao-seedance-2-0-260128",
			fields: map[string]string{PathResolution: `"4k"`, PathRatio: `"16:9"`, PathDuration: `4`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 4, w: 3840, h: 2160, wantTier: Res4K,
		},
		{
			name:  "a stated ratio with no resolution takes the model's documented default tier",
			model: "doubao-seedance-2-5-260628",
			fields: map[string]string{PathRatio: `"16:9"`, PathDuration: `5`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 5, w: 1280, h: 720, wantTier: Res720, wantAssume: []string{assumedResolution},
		},
		{
			name:  "a stated resolution with no ratio takes the tier's largest cell",
			model: "doubao-seedance-2-5-260628",
			fields: map[string]string{PathResolution: `"1080p"`, PathDuration: `5`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 5, w: 2206, h: 946, wantTier: Res1080, wantAssume: []string{assumedRatio},
		},
		{
			name:  "duration -1 on 2.5 is the model's 30s maximum",
			model: "doubao-seedance-2-5-260628",
			fields: map[string]string{PathResolution: `"720p"`, PathRatio: `"16:9"`, PathDuration: `-1`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 30, w: 1280, h: 720, wantTier: Res720, wantAssume: []string{assumedDuration},
		},
		{
			name:  "duration -1 on 2.0 is its 15s maximum, not 2.5's 30s",
			model: "doubao-seedance-2-0-fast-260128",
			fields: map[string]string{PathResolution: `"720p"`, PathRatio: `"16:9"`, PathDuration: `-1`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 15, w: 1280, h: 720, wantTier: Res720, wantAssume: []string{assumedDuration},
		},
		{
			name:  "duration -1 on 1.0 pro is its 12s maximum",
			model: "doubao-seedance-1-0-pro-250528",
			fields: map[string]string{PathResolution: `"720p"`, PathRatio: `"16:9"`, PathDuration: `-1`,
				PathContentCount: `1`, "content.0.text": `"a cat"`},
			sec: 12, w: 1248, h: 704, wantTier: Res720, wantAssume: []string{assumedDuration},
		},
		{
			name:  "the prompt's commands are read",
			model: "doubao-seedance-2-0-260128",
			fields: map[string]string{PathContentCount: `1`,
				"content.0.text": `"a cat --rs 480p --rt 1:1 --dur 8"`},
			sec: 8, w: 640, h: 640, wantTier: Res480,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est := estimateVideo(tc.model, readVideoSpec(tc.fields))
			want := (tc.sec*videoFPS + 1) * tc.w * tc.h / 1024
			if est.Tokens != want {
				t.Errorf("tokens = %d, want %d ((%ds x %dfps + 1) x %dx%d / 1024)",
					est.Tokens, want, tc.sec, videoFPS, tc.w, tc.h)
			}
			if est.Resolution != tc.wantTier {
				t.Errorf("resolution fact = %q, want %q", est.Resolution, tc.wantTier)
			}
			if len(est.Assumed) != len(tc.wantAssume) {
				t.Errorf("assumed = %v, want %v", est.Assumed, tc.wantAssume)
			}
			for _, a := range tc.wantAssume {
				if !hasAssumption(est.Assumed, a) {
					t.Errorf("assumed = %v, want it to name %q", est.Assumed, a)
				}
			}
		})
	}
}

// TestEstimateIsNeverZero: an estimate of zero makes the submit free, and a
// free submit is a video nobody is charged for. No input may produce one.
func TestEstimateIsNeverZero(t *testing.T) {
	models := []string{
		"doubao-seedance-2-5-260628", "doubao-seedance-2-0-260128", "doubao-seedance-2-0-mini-260615",
		"doubao-seedance-1-0-pro-250528", "ep-20260101-abcde", "",
	}
	specs := []map[string]string{
		nil,
		{},
		{PathResolution: `"4k"`}, // a tier this model may not have
		{PathResolution: `"2k"`}, // a tier nobody has
		{PathResolution: `null`, PathDuration: `null`}, // JSON nulls
		{PathResolution: `""`, PathRatio: `""`},        // empty strings
		{PathDuration: `0`},                            // zero
		{PathDuration: `-99`},                          // nonsense
		{PathFrames: `0`, PathDuration: `0`},           // both zero
		{PathContentCount: `"not a number"`},           // wrong type
		{"content.0.text": `"--rs --dur --rt"`},        // flags with no values
		{"content.0.text": `"--dur notanumber"`},       // unparseable value
		{PathResolution: `{"nested":"object"}`},        // wrong shape
	}
	for _, model := range models {
		for i, f := range specs {
			est := estimateVideo(model, readVideoSpec(f))
			if est.Tokens <= 0 {
				t.Errorf("model %q spec #%d estimated %d tokens", model, i, est.Tokens)
			}
			if est.Resolution == "" {
				t.Errorf("model %q spec #%d reported no resolution fact", model, i)
			}
		}
	}
}

// TestFourKAskedOfAModelWithoutIt: a request stating 4k reaches videoUsage
// only if Ark accepted it, so the tier is not clamped to the model's table -
// but if it arrived via the prompt (weak validation, Ark may have ignored it)
// the estimate still has to produce a number for a generation that publishes
// no 4k row.
func TestFourKAskedOfAModelWithoutIt(t *testing.T) {
	est := estimateVideo("doubao-seedance-2-5-260628", readVideoSpec(map[string]string{
		PathResolution: `"4k"`, PathRatio: `"16:9"`, PathDuration: `5`,
		PathContentCount: `1`, "content.0.text": `"a cat"`,
	}))
	// 2.5 has no 4k row, so the largest 4k area any generation publishes is
	// used - over, not under.
	if want := int64(5*videoFPS+1) * pixelArea[gen20][Res4K][Ratio169] / 1024; est.Tokens != want {
		t.Fatalf("tokens = %d, want %d", est.Tokens, want)
	}
	if !hasAssumption(est.Assumed, assumedPixelArea) {
		t.Fatalf("assumed = %v, want it to name the borrowed pixel area", est.Assumed)
	}
	// And a tier that exists nowhere falls back to the model's own maximum
	// rather than to nothing.
	est = estimateVideo("doubao-seedance-1-0-pro-250528", videoSpec{
		Resolution: "8k", Seconds: 5, PromptFullyRead: true,
	})
	if est.Resolution != Res1080 || est.Tokens <= 0 {
		t.Fatalf("an impossible tier gave %+v", est)
	}
}

// The one number in this file that did not come from documentation: a real
// Seedance task, submitted through the gateway to a real upstream on
// 2026-09-30 (doubao-seedance-2-0-mini-260615, 480p, 16:9, duration 4), came
// back with usage.completion_tokens = 40594. That is floor(97 x 864x496 / 1024):
// Ark bills seconds x 24 + 1 frames. The published formula (seconds x 24)
// gives 40176 - what this plugin reserved before - so this test fails on it.
func TestEstimateMatchesARealArkTask(t *testing.T) {
	fields := map[string]string{PathResolution: `"480p"`, PathRatio: `"16:9"`, PathDuration: `4`,
		PathContentCount: `1`, "content.0.text": `"A red ball slowly rolls across a wooden table, soft daylight"`}
	est := estimateVideo("doubao-seedance-2-0-mini-260615", readVideoSpec(fields))
	if est.Tokens != 40594 || est.Resolution != Res480 || len(est.Assumed) != 0 {
		t.Fatalf("estimate = %d tokens at %s (assumed %v), want 40594 at 480p with nothing assumed - the completion_tokens Ark really billed",
			est.Tokens, est.Resolution, est.Assumed)
	}
}
