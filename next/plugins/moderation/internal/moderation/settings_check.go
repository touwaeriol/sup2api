package moderation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/fieldcheck"
)

// The rules of the settings (CONTRACTS §72.10, §75.1): the keys the plugin
// accepts with their type, bounds, options and lengths, as the settings
// form of forms/settings.schema.json declared them until 0.12.2 removed it. The
// console's settings pages (ui/native settingsForm.ts) show the same
// limits while typing; ValidateSettings is the final check. Every key not
// listed is refused (code "unknown"), the retired ones too: the pages drop
// them before saving.
var settingsRules = fieldcheck.Fields{
	"mode":                      fieldcheck.String().OneOf(ModeOff, ModeObserve, ModeEnforce),
	"fail_policy":               fieldcheck.String().OneOf(FailOpen, FailClosePre),
	"block_status":              fieldcheck.Integer().Range(400, 599),
	"block_message":             fieldcheck.String().MaxLen(500),
	"auto_watch":                fieldcheck.Bool(),
	"watch_days":                fieldcheck.Integer().Range(1, 365),
	"escalate_violations":       fieldcheck.Integer().Range(0, 1000),
	"escalate_window_seconds":   fieldcheck.Integer().Range(1, 31536000),
	"ban_threshold":             fieldcheck.Integer().Range(0, 1000),
	"ban_window_seconds":        fieldcheck.Integer().Range(1, 31536000),
	"ban_duration_hours":        fieldcheck.Integer().Range(0, 87600),
	"error_rate_alert_percent":  fieldcheck.Integer().Range(0, 100),
	"notify_webhook_url":        fieldcheck.String().MaxLen(1000).Pattern(`^$|^https?://\S+$`),
	"notify_webhook_secret":     fieldcheck.String().MaxLen(200),
	"record_pass":               fieldcheck.Bool(),
	"record_overturned":         fieldcheck.Bool(),
	"record_errors":             fieldcheck.Bool(),
	"store_text":                fieldcheck.Bool(),
	"record_pass_input_message": fieldcheck.Bool(),
	"hit_retention_days":        fieldcheck.Integer().Range(1, 3650),
	"overturned_retention_days": fieldcheck.Integer().Range(1, 3650),
	"error_retention_days":      fieldcheck.Integer().Range(1, 3650),
	"pass_retention_days":       fieldcheck.Integer().Range(1, 3),
	"stats_retention_days":      fieldcheck.Integer().Range(7, 3650),
	"max_concurrency":           fieldcheck.Integer().Range(1, 256),
	"queue_size":                fieldcheck.Integer().Range(1, 100000),
	"ttft_timeout_ms":           fieldcheck.Integer().Range(1000, 60000),
	"verdict_cache_days":        fieldcheck.Integer().Range(0, 30),
	"hit_replay":                fieldcheck.String().OneOf(HitReplayNewContent, HitReplayWhole),
	"cache_entries":             fieldcheck.Integer().Range(1000, 1000000),
	"input_max_chars":           fieldcheck.Integer().Range(256, 100000),
	"input_max_chunks":          fieldcheck.Integer().Range(1, 32),
	"input_overflow":            fieldcheck.String().OneOf(OverflowSample, OverflowBlock),
	"input_max_images":          fieldcheck.Integer().Range(0, 32),
	"max_inflight_mb":           fieldcheck.Integer().Range(16, 4096),
	"error_text_max_chars":      fieldcheck.Integer().Range(0, 100000),
	"min_chars":                 fieldcheck.Integer().Range(0, 1000),
	// null (the built-in set) or a question table: checkJevSet.
	"jev_default_set": fieldcheck.Any(),
	// The 0.1 API key: no longer read, kept only so the core masks it.
	"api_key": fieldcheck.String().MaxLen(1000),
}

// jevSetRules and jevItemRules are a jev_default_set table and one of its
// rows; a threshold must also be above 0 (checkJevSet), and the rules a row's
// fields alone do not decide are checkJevRow's.
var (
	jevSetRules = fieldcheck.Fields{
		"preamble": fieldcheck.String().MaxLen(maxJevPreambleRunes),
		"items":    fieldcheck.Array().Required().Len(1, maxJevQuestions),
	}
	jevItemRules = fieldcheck.Fields{
		"category":   fieldcheck.String().Required().Pattern(jevCategoryRe.String()),
		"label":      fieldcheck.String().MaxLen(maxJevLabelRunes),
		"question":   fieldcheck.String().Required().Len(1, maxJevQuestionRunes),
		"answer_yes": fieldcheck.String().MaxLen(maxJevAnswerRunes),
		"answer_no":  fieldcheck.String().MaxLen(maxJevAnswerRunes),
		"threshold":  fieldcheck.Number().Required().Max(1),
	}
)

const jevSetKey = "jev_default_set"

// CheckSettings reports what the rules refuse in raw settings (one error per
// field, JSON pointers below the settings); a document that is no JSON
// object is refused as a whole, an empty one or null (no settings, as
// DecodeSettings reads it) is not. ValidateSettings refuses what it reports;
// Configure (parseSettings) applies stored settings anyway.
func CheckSettings(raw []byte) pluginsdk.FieldErrors {
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" {
		return nil
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return pluginsdk.FieldErrors{}.Add("", "invalid_json", "settings must be a JSON object / 设置必须是 JSON 对象")
	}
	errs := fieldcheck.Check(settingsRules, values)
	return checkJevSet(errs, values[jevSetKey])
}

// checkJevSet adds the problems of a jev_default_set value: null, absent or
// a table of jevSetRules whose rows follow jevItemRules.
func checkJevSet(errs pluginsdk.FieldErrors, v any) pluginsdk.FieldErrors {
	if v == nil {
		return errs
	}
	set, ok := v.(map[string]any)
	if !ok {
		return errs.Add(jevSetKey, "type", jevSetKey+" must be null or a question table / "+jevSetKey+" 必须为空或题集")
	}
	errs = nestErrors(errs, jevSetKey, fieldcheck.Check(jevSetRules, set))
	items, _ := set["items"].([]any)
	seen := map[string]bool{}
	for i, item := range items {
		at := jevSetKey + "/items/" + strconv.Itoa(i)
		row, ok := item.(map[string]any)
		if !ok {
			errs = errs.Add(at, "type", at+" must be an object / "+at+" 的类型不正确")
			continue
		}
		rowErrs := fieldcheck.Check(jevItemRules, row)
		if th, ok := row["threshold"].(float64); ok && th <= 0 {
			rowErrs = rowErrs.Add("threshold", "minimum", "threshold must be greater than 0 / threshold 必须大于 0")
		}
		errs = nestErrors(errs, at, checkJevRow(rowErrs, row, seen))
	}
	return errs
}

// checkJevRow adds to the errors of a row what JevQuestions.Validate also
// refuses beyond jevItemRules, each on a field whose own rules passed: the
// category of an earlier row (seen), a custom category without a label
// (jevLabelMissing) and a question of only white space (jevQuestionBlank).
func checkJevRow(errs pluginsdk.FieldErrors, row map[string]any, seen map[string]bool) pluginsdk.FieldErrors {
	failed := map[string]bool{}
	for _, e := range errs {
		failed[e.GetField()] = true
	}
	category, _ := row["category"].(string)
	label, _ := row["label"].(string)
	question, _ := row["question"].(string)
	if !failed["category"] {
		if seen[category] {
			errs = errs.Add("category", "duplicate", "category repeats the category of an earlier question / category 与前面某题的类别重复")
		}
		seen[category] = true
		if !failed["label"] && jevLabelMissing(category, label) {
			errs = errs.Add("label", "required", "label is required for a custom category / 自定义类别的 label 不能为空")
		}
	}
	if !failed["question"] && jevQuestionBlank(question) {
		errs = errs.Add("question", "required", "question is required / question 不能为空")
	}
	return errs
}

// nestErrors adds inner, whose fields are below the pointer at, to errs.
func nestErrors(errs pluginsdk.FieldErrors, at string, inner pluginsdk.FieldErrors) pluginsdk.FieldErrors {
	for _, e := range inner {
		errs = errs.Add(at+"/"+e.GetField(), e.GetCode(), e.GetMessage())
	}
	return errs
}

// ValidateSettings implements pluginsdk.SettingsValidator: the core stores
// the settings only when CheckSettings finds nothing.
func (p *Plugin) ValidateSettings(_ context.Context, cfg pluginsdk.Config) (json.RawMessage, error) {
	return nil, CheckSettings(cfg.JSON).Err()
}

// parseSettings is the settings as Configure applies them (DecodeSettings)
// and what CheckSettings refuses in them. Stored settings are applied even
// when it refuses something: settings saved before ValidateSettings existed
// may hold retired keys (read by retiredKeys until saved again) or values
// of older bounds, which DecodeSettings clamps; refusing them would keep
// the plugin from starting.
func parseSettings(raw []byte) (Settings, pluginsdk.FieldErrors, error) {
	s, err := DecodeSettings(raw)
	if err != nil {
		return s, nil, err
	}
	return s, CheckSettings(raw), nil
}
