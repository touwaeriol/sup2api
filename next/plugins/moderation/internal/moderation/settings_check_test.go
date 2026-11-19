package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"strings"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// The settings form of forms/settings.schema.json (removed in 0.12.2) as plain
// data: the defaults of every key, and the bounds of the integer keys.
// CheckSettings holds the same rules; these tests keep it to them.
var schemaDefaults = map[string]any{
	"mode": "off", "fail_policy": "open", "block_status": 403, "block_message": DefaultBlockMessage,
	"auto_watch": true, "watch_days": 30, "escalate_violations": 0, "escalate_window_seconds": 3600, "ban_threshold": 0,
	"ban_window_seconds": 86400, "ban_duration_hours": 24, "error_rate_alert_percent": 5, "notify_webhook_url": "",
	"notify_webhook_secret": "", "record_pass": false, "record_overturned": true, "record_errors": true, "store_text": true,
	"record_pass_input_message": true, "hit_retention_days": 180, "overturned_retention_days": 30, "error_retention_days": 7,
	"pass_retention_days": 3, "stats_retention_days": 90, "max_concurrency": 16, "queue_size": 1000, "ttft_timeout_ms": 10000,
	"verdict_cache_days": 7, "hit_replay": "new_content", "cache_entries": 100000, "input_max_chars": 12000, "input_max_chunks": 8,
	"input_overflow": "sample", "input_max_images": 8, "max_inflight_mb": 128, "error_text_max_chars": 2000, "min_chars": 2,
	"jev_default_set": nil, "api_key": "",
}

var schemaIntegers = map[string][2]float64{
	"block_status": {400, 599}, "watch_days": {1, 365}, "escalate_violations": {0, 1000}, "escalate_window_seconds": {1, 31536000},
	"ban_threshold": {0, 1000}, "ban_window_seconds": {1, 31536000}, "ban_duration_hours": {0, 87600}, "error_rate_alert_percent": {0, 100},
	"hit_retention_days": {1, 3650}, "overturned_retention_days": {1, 3650}, "error_retention_days": {1, 3650}, "pass_retention_days": {1, 3},
	"stats_retention_days": {7, 3650}, "max_concurrency": {1, 256}, "queue_size": {1, 100000}, "ttft_timeout_ms": {1000, 60000},
	"verdict_cache_days": {0, 30}, "cache_entries": {1000, 1000000}, "input_max_chars": {256, 100000}, "input_max_chunks": {1, 32},
	"input_max_images": {0, 32}, "max_inflight_mb": {16, 4096}, "error_text_max_chars": {0, 100000}, "min_chars": {0, 1000},
}

var schemaBools = []string{"auto_watch", "record_pass", "record_overturned", "record_errors", "store_text", "record_pass_input_message"}

const jevRow = `{"category":"violence","label":"","question":"q?","answer_yes":"","answer_no":"","threshold":0.5}`

// jevRows is n valid rows of distinct 64-character categories, labelled label.
func jevRows(n int, label string) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"category":"%064d","label":"%s","question":"q","threshold":1}`, i, label)
	}
	return strings.Join(rows, ",")
}

// fullSettings: every key set, the optional ones (secrets, the question set) too.
func fullSettings() map[string]any {
	m := maps.Clone(schemaDefaults)
	m["notify_webhook_url"] = "https://hooks.example/x"
	m["notify_webhook_secret"] = "s3cret"
	m["api_key"] = "sk-old"
	var set any
	_ = json.Unmarshal([]byte(`{"preamble":"p","items":[`+jevRow+`]}`), &set)
	m["jev_default_set"] = set
	return m
}

func check(t *testing.T, m map[string]any) map[string]string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return byField(t, CheckSettings(b))
}

// byField is the code of each error by its field; a field with two errors
// fails the test (CheckSettings reports one per field).
func byField(t *testing.T, errs pluginsdk.FieldErrors) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range errs {
		if c, ok := out[e.GetField()]; ok {
			t.Errorf("%s reported twice: %s and %s", e.GetField(), c, e.GetCode())
		}
		out[e.GetField()] = e.GetCode()
	}
	return out
}

func TestCheckSettingsAcceptsTheDefaults(t *testing.T) {
	if len(schemaDefaults) != 39 {
		t.Fatalf("%d keys", len(schemaDefaults))
	}
	if errs := check(t, schemaDefaults); len(errs) > 0 {
		t.Fatalf("defaults refused: %v", errs)
	}
	if errs := check(t, fullSettings()); len(errs) > 0 {
		t.Fatalf("every key set refused: %v", errs)
	}
	if errs := check(t, map[string]any{}); len(errs) > 0 {
		t.Fatalf("no key refused: %v", errs)
	}
	// DefaultSettings is the same defaults.
	b, _ := json.Marshal(DefaultSettings())
	var d map[string]any
	_ = json.Unmarshal(b, &d)
	for k, v := range schemaDefaults {
		if k == "api_key" {
			continue
		}
		if fmt.Sprint(d[k]) != fmt.Sprint(v) {
			t.Errorf("DefaultSettings %s = %v, the form's default %v", k, d[k], v)
		}
	}
	for _, raw := range []string{"", "null", " {} "} {
		if errs := CheckSettings([]byte(raw)); len(errs) > 0 {
			t.Errorf("%q: %v", raw, errs)
		}
	}
}

// TestCheckSettingsRefusals: a key settingsRules does not list (retired ones
// included) and one value breaking each of its rules, each refused at its
// key with its code.
func TestCheckSettingsRefusals(t *testing.T) {
	type tc struct {
		key   string
		value string // JSON
		field string
		code  string
	}
	cases := []tc{
		{"unknown", `1`, "unknown", "unknown"},
		{"cache_ttl_seconds", `600`, "cache_ttl_seconds", "unknown"},
		{"allowlist_user_ids", `["7"]`, "allowlist_user_ids", "unknown"},
		{"mode", `"bogus"`, "mode", "enum"},
		{"mode", `1`, "mode", "type"},
		{"fail_policy", `"closed"`, "fail_policy", "enum"},
		{"hit_replay", `"all"`, "hit_replay", "enum"},
		{"input_overflow", `"drop"`, "input_overflow", "enum"},
		{"block_message", `"` + strings.Repeat("é", 501) + `"`, "block_message", "maxLength"},
		{"block_message", `5`, "block_message", "type"},
		{"notify_webhook_url", `"https://` + strings.Repeat("x", 993) + `"`, "notify_webhook_url", "maxLength"},
		{"notify_webhook_url", `"ftp://x"`, "notify_webhook_url", "pattern"},
		{"notify_webhook_url", `"https://a b"`, "notify_webhook_url", "pattern"},
		{"notify_webhook_url", `" https://x"`, "notify_webhook_url", "pattern"},
		{"notify_webhook_secret", `"` + strings.Repeat("x", 201) + `"`, "notify_webhook_secret", "maxLength"},
		{"notify_webhook_secret", `true`, "notify_webhook_secret", "type"},
		{"api_key", `"` + strings.Repeat("x", 1001) + `"`, "api_key", "maxLength"},
		{"api_key", `null`, "api_key", "type"},
		// jev_default_set: null or a table {preamble?, items}
		{"jev_default_set", `"builtin"`, "jev_default_set", "type"},
		{"jev_default_set", `{"items":[` + jevRow + `],"extra":1}`, "jev_default_set/extra", "unknown"},
		{"jev_default_set", `{"preamble":""}`, "jev_default_set/items", "required"},
		{"jev_default_set", `{"preamble":5,"items":[` + jevRow + `]}`, "jev_default_set/preamble", "type"},
		{"jev_default_set", `{"preamble":"` + strings.Repeat("x", 2001) + `","items":[` + jevRow + `]}`, "jev_default_set/preamble", "maxLength"},
		{"jev_default_set", `{"items":{}}`, "jev_default_set/items", "type"},
		{"jev_default_set", `{"items":[]}`, "jev_default_set/items", "minItems"},
		{"jev_default_set", `{"items":[` + jevRows(33, "a") + `]}`, "jev_default_set/items", "maxItems"},
		{"jev_default_set", `{"items":["q"]}`, "jev_default_set/items/0", "type"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":0.5,"x":1}]}`, "jev_default_set/items/0/x", "unknown"},
		{"jev_default_set", `{"items":[{"question":"q","threshold":0.5}]}`, "jev_default_set/items/0/category", "required"},
		{"jev_default_set", `{"items":[{"category":"Bad","question":"q","threshold":0.5}]}`, "jev_default_set/items/0/category", "pattern"},
		{"jev_default_set", `{"items":[{"category":"` + strings.Repeat("a", 65) + `","question":"q","threshold":0.5}]}`, "jev_default_set/items/0/category", "pattern"},
		{"jev_default_set", `{"items":[{"category":"a","question":"q","threshold":0.5,"label":"` + strings.Repeat("x", 65) + `"}]}`, "jev_default_set/items/0/label", "maxLength"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","threshold":0.5}]}`, "jev_default_set/items/0/question", "required"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"","threshold":0.5}]}`, "jev_default_set/items/0/question", "required"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"` + strings.Repeat("x", 2001) + `","threshold":0.5}]}`, "jev_default_set/items/0/question", "maxLength"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":0.5,"answer_yes":"` + strings.Repeat("x", 1001) + `"}]}`, "jev_default_set/items/0/answer_yes", "maxLength"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":0.5,"answer_no":"` + strings.Repeat("x", 1001) + `"}]}`, "jev_default_set/items/0/answer_no", "maxLength"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q"}]}`, "jev_default_set/items/0/threshold", "required"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":"0.5"}]}`, "jev_default_set/items/0/threshold", "type"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":0}]}`, "jev_default_set/items/0/threshold", "minimum"},
		{"jev_default_set", `{"items":[{"category":"a","label":"A","question":"q","threshold":1.01}]}`, "jev_default_set/items/0/threshold", "maximum"},
		// the rules of JevQuestions.Validate beyond the fields' bounds
		{"jev_default_set", `{"items":[` + jevRow + `,` + jevRow + `]}`, "jev_default_set/items/1/category", "duplicate"},
		{"jev_default_set", `{"items":[{"category":"custom/x","question":"q","threshold":0.5}]}`, "jev_default_set/items/0/label", "required"},
		{"jev_default_set", `{"items":[{"category":"custom/x","label":" \t","question":"q","threshold":0.5}]}`, "jev_default_set/items/0/label", "required"},
		{"jev_default_set", `{"items":[{"category":"custom/x","label":1,"question":"q","threshold":0.5}]}`, "jev_default_set/items/0/label", "type"},
		{"jev_default_set", `{"items":[{"category":"violence","question":" \n ","threshold":0.5}]}`, "jev_default_set/items/0/question", "required"},
	}
	for k, b := range schemaIntegers {
		cases = append(cases,
			tc{k, fmt.Sprint(b[0] - 1), k, "minimum"},
			tc{k, fmt.Sprint(b[1] + 1), k, "maximum"},
			tc{k, fmt.Sprint(b[0] + 0.5), k, "type"},
			tc{k, `"1"`, k, "type"},
			tc{k, `null`, k, "type"})
	}
	for _, k := range schemaBools {
		cases = append(cases, tc{k, `"true"`, k, "type"}, tc{k, `null`, k, "type"})
	}
	for _, c := range cases {
		m := maps.Clone(schemaDefaults)
		var v any
		if err := json.Unmarshal([]byte(c.value), &v); err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		m[c.key] = v
		got := check(t, m)
		if got[c.field] != c.code || len(got) != 1 {
			t.Errorf("%s = %.60s: errors %v, want %s %s", c.key, c.value, got, c.field, c.code)
		}
	}
	if errs := CheckSettings([]byte(`[1]`)); len(errs) != 1 || errs[0].GetField() != "" || errs[0].GetCode() != "invalid_json" {
		t.Errorf("not an object: %v", errs)
	}
}

// The values at the bounds of the form are accepted.
func TestCheckSettingsAcceptsTheBounds(t *testing.T) {
	for k, b := range schemaIntegers {
		for _, v := range b {
			m := maps.Clone(schemaDefaults)
			m[k] = v
			if errs := check(t, m); len(errs) > 0 {
				t.Errorf("%s = %v: %v", k, v, errs)
			}
		}
	}
	m := maps.Clone(schemaDefaults)
	m["block_message"] = strings.Repeat("é", 500)
	m["notify_webhook_url"] = "https://" + strings.Repeat("例", 992)
	m["notify_webhook_secret"] = strings.Repeat("x", 200)
	var set any
	_ = json.Unmarshal([]byte(`{"items":[`+jevRows(31, strings.Repeat("x", 64))+`,`+jevRow+`]}`), &set)
	m["jev_default_set"] = set
	if errs := check(t, m); len(errs) > 0 {
		t.Errorf("bounds refused: %v", errs)
	}
}

// P12: a string the decoder cuts or drops is refused on save by the same
// limit in CheckSettings, so a value is never changed silently; the decoder
// keeps the cut as a backstop for stored values.
func TestCheckLimitsMatchDecoding(t *testing.T) {
	decode := func(key, value string) Settings {
		t.Helper()
		b, _ := json.Marshal(map[string]string{key: value})
		s, err := DecodeSettings(b)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	accepted := func(key, value string) bool {
		b, _ := json.Marshal(map[string]string{key: value})
		return len(CheckSettings(b)) == 0
	}
	for key, get := range map[string]func(Settings) string{
		"block_message":         func(s Settings) string { return s.BlockMessage },
		"notify_webhook_secret": func(s Settings) string { return s.NotifyWebhookSecret },
		"notify_webhook_url":    func(s Settings) string { return s.NotifyWebhookURL },
	} {
		limit := map[string]int{"block_message": 500, "notify_webhook_secret": 200, "notify_webhook_url": 1000}[key]
		prefix := ""
		if key == "notify_webhook_url" {
			prefix = "https://例.example/"
		}
		at := prefix + strings.Repeat("é", limit-len([]rune(prefix)))
		if got := get(decode(key, at)); got != at || !accepted(key, at) {
			t.Errorf("%s: a value of the limit (%d characters) is changed by the decoder or refused", key, limit)
		}
		if got := get(decode(key, at+"é")); got == at+"é" || accepted(key, at+"é") {
			t.Errorf("%s: a value beyond the limit is kept by the decoder or accepted", key)
		}
	}
	re := regexp.MustCompile(`^$|^https?://\S+$`)
	for _, u := range []string{"", "https://x", "http://x/y", "ftp://x", "https://a b", " https://x"} {
		s := decode("notify_webhook_url", u)
		if kept := s.NotifyWebhookURL == u; kept != accepted("notify_webhook_url", u) && strings.TrimSpace(u) == u {
			t.Errorf("notify_webhook_url %q: decoder keeps %v, check accepts %v", u, kept, accepted("notify_webhook_url", u))
		}
		if accepted("notify_webhook_url", u) != re.MatchString(u) {
			t.Errorf("notify_webhook_url %q: check differs from the form's pattern", u)
		}
	}
}

// Stored settings the check refuses still configure: retired keys and
// values of older bounds are read and clamped, as before ValidateSettings.
func TestParseSettingsAppliesStoredSettings(t *testing.T) {
	s, refused, err := parseSettings([]byte(`{"cache_ttl_seconds":0,"pass_retention_days":9,"mode":"observe"}`))
	if err != nil || len(refused) != 2 {
		t.Fatalf("err %v, refused %v", err, refused)
	}
	if s.VerdictCacheDays != 0 || s.PassRetentionDays != 3 || s.Mode != ModeObserve {
		t.Fatalf("settings = %+v", s)
	}
	if _, _, err := parseSettings([]byte(`[1]`)); err == nil {
		t.Fatal("not an object must fail")
	}
}

// A question table is refused on save exactly when JevQuestions.Validate
// refuses it (it is then dropped for the built-in set at run time), and the
// messages carry no value of the table.
func TestCheckJevSetFollowsValidate(t *testing.T) {
	builtin, _ := json.Marshal(builtinQuestions)
	const secret = "custom/secret-name"
	tables := []string{
		string(builtin),
		`{"items":[` + jevRow + `]}`,
		`{"items":[{"category":"` + secret + `","label":"L","question":"q","threshold":0.5}]}`,
		`{"items":[{"category":"violence","label":" ","question":"q","threshold":0.5}]}`,
		`{"items":[` + jevRow + `,` + jevRow + `]}`,
		`{"items":[{"category":"` + secret + `","label":"L","question":"q","threshold":0.5},{"category":"` + secret + `","label":"M","question":"r","threshold":0.5}]}`,
		`{"items":[{"category":"` + secret + `","question":"q","threshold":0.5}]}`,
		`{"items":[{"category":"` + secret + `","label":"　","question":"q","threshold":0.5}]}`,
		`{"items":[{"category":"violence","question":"\t \n","threshold":0.5}]}`,
	}
	for _, table := range tables {
		var qs JevQuestions
		if err := json.Unmarshal([]byte(table), &qs); err != nil {
			t.Fatal(err)
		}
		var set any
		_ = json.Unmarshal([]byte(table), &set)
		m := maps.Clone(schemaDefaults)
		m["jev_default_set"] = set
		b, _ := json.Marshal(m)
		errs := CheckSettings(b)
		if (qs.Validate() == nil) != (len(errs) == 0) {
			t.Errorf("%.80s: Validate %v, check %v", table, qs.Validate(), errs)
		}
		for _, e := range errs {
			if strings.Contains(e.GetMessage(), "secret-name") {
				t.Errorf("%s: the message holds the value: %s", e.GetField(), e.GetMessage())
			}
		}
	}
}

// Stored settings with a question table Validate refuses still configure
// (the built-in set applies, the warning names no value), while a save of
// them is refused at the rows.
func TestStoredJevSetRefusedOnSaveOnly(t *testing.T) {
	stored := json.RawMessage(`{"mode":"observe","jev_default_set":{"items":[` + jevRow + `,` + jevRow + `,` +
		`{"category":"custom/secret-name","question":"q","threshold":0.5},{"category":"hate","label":"H","question":"  ","threshold":0.5}]}}`)
	p := New()
	var log bytes.Buffer
	p.log = slog.New(slog.NewTextHandler(&log, nil))
	if err := p.Configure(context.Background(), pluginsdk.Config{JSON: stored}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if v := p.cfg.Load().jevDefault.RulesVersion; v != RulesVersionZH {
		t.Fatalf("default set %s, want the built-in %s", v, RulesVersionZH)
	}
	if w := log.String(); !strings.Contains(w, "jev_default_set/items/2/label") || strings.Contains(w, "secret-name") {
		t.Fatalf("warning: %s", w)
	}
	_, err := p.ValidateSettings(context.Background(), pluginsdk.Config{JSON: stored})
	errs, _ := pluginsdk.AsFieldErrors(err)
	got := byField(t, errs)
	want := map[string]string{
		"jev_default_set/items/1/category": "duplicate",
		"jev_default_set/items/2/label":    "required",
		"jev_default_set/items/3/question": "required",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("ValidateSettings: %v, want %v", got, want)
	}
}
