package account

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// maxModelLimits bounds the model lists of an account (CONTRACTS §68).
const maxModelLimits = manifest.MaxAccountModelLimits

// spmLimitEN/ZH and spmWindowEN/ZH explain an spm_limit and an
// spm_window_seconds out of range, of an account or a model list.
const (
	spmLimitEN  = "session limit must be 0-10000000 (0 = unlimited)"
	spmLimitZH  = "会话数上限必须为 0-10000000（0 表示不限）"
	spmWindowEN = "session window must be 60-86400 seconds"
	spmWindowZH = "会话周期必须为 60-86400 秒"
)

// validSPMWindow reports whether secs is a session window an account or a
// model list may count over (CONTRACTS §71.1).
func validSPMWindow(secs int) bool {
	return secs >= manifest.MinSPMWindowSeconds && secs <= manifest.MaxSPMWindowSeconds
}

// withSPMWindow gives a list saved without a session window (before 0094)
// or sent without one the default.
func withSPMWindow(l core.ModelLimit) core.ModelLimit {
	if l.SPMWindowSeconds == 0 {
		l.SPMWindowSeconds = manifest.DefaultSPMWindowSeconds
	}
	return l
}

// parseList decodes a jsonb column holding a list; never nil.
func parseList[T any](raw []byte) []T {
	out := []T{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// listJSON encodes a list for a jsonb column, nil as [].
func listJSON[T any](in []T) string {
	if in == nil {
		in = []T{}
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func parseModelLimits(raw []byte) []core.ModelLimit {
	out := parseList[core.ModelLimit](raw)
	for i := range out {
		out[i] = withSPMWindow(out[i])
	}
	return out
}

func modelLimitsJSON(in []core.ModelLimit) string { return listJSON(in) }

// entryModels normalizes the model lists of the entries of an account field
// (model_limits, model_protocols), in which a model belongs to one entry
// only.
type entryModels struct {
	// other is accepted next to the complete model ids: core.OtherModels
	// where an entry may stand for every other model, else "".
	other string
	// invalid and duplicate word (en, zh) a model that is no complete model
	// id and one another entry lists.
	invalid, duplicate [2]string
	// owner is the entry each model seen so far is in.
	owner map[string]int
}

// of returns the models of entry i, at field: trimmed, without the blank and
// the repeated ones, and without those it reports as invalid or duplicate.
func (e *entryModels) of(ctx context.Context, field string, i int, in []string) ([]string, []core.FieldError) {
	var fe []core.FieldError
	refuse := func(j int, code string, msg [2]string) {
		fe = append(fe, core.FieldError{Field: field + ".models[" + strconv.Itoa(j) + "]", Code: code, Message: t(ctx, msg[0], msg[1])})
	}
	out := make([]string, 0, len(in))
	for j, m := range in {
		m = strings.TrimSpace(m)
		k, owned := e.owner[m]
		switch {
		case m == "" || slices.Contains(out, m):
		case m != e.other && !manifest.ValidModelID(m):
			refuse(j, "invalid", e.invalid)
		case owned && k != i:
			refuse(j, "duplicate", e.duplicate)
		default:
			e.owner[m] = i
			out = append(out, m)
		}
	}
	return out, fe
}

// normalizeModelLimits trims the model lists, drops repeated models within a
// list and lists without models, and checks the limits. A model may be in one
// list only, and every entry must be a complete model id.
func normalizeModelLimits(ctx context.Context, in []core.ModelLimit) ([]core.ModelLimit, []core.FieldError) {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	out := make([]core.ModelLimit, 0, len(in))
	lists := entryModels{owner: map[string]int{},
		invalid:   [2]string{"not a complete model id (wildcards are not allowed)", "不是完整的模型 ID（不允许通配符）"},
		duplicate: [2]string{"the model is already limited by another list", "该模型已在另一个列表中限速"}}
	for i, l := range in {
		field := "model_limits[" + strconv.Itoa(i) + "]"
		models, errs := lists.of(ctx, field, i, l.Models)
		fe = append(fe, errs...)
		if len(models) > maxModels {
			add(field+".models", "too_many", "at most 500 models per list", "每个列表最多 500 个模型")
		}
		if l.MaxConcurrency < 0 || l.MaxConcurrency > maxConcurrency {
			add(field+".max_concurrency", "invalid", "max concurrency must be 0-100000 (0 = unlimited)", "最大并发必须为 0-100000（0 表示不限）")
		}
		if l.RPMLimit < 0 || l.RPMLimit > maxRPMLimit {
			add(field+".rpm_limit", "invalid", "rpm limit must be 0-10000000 (0 = unlimited)", "每分钟请求数上限必须为 0-10000000（0 表示不限）")
		}
		if l.TPMLimit < 0 || l.TPMLimit > maxTokenLimit {
			add(field+".tpm_limit", "invalid", "tpm limit must be 0-1000000000000 (0 = unlimited)", "每分钟 token 上限必须为 0-1000000000000（0 表示不限）")
		}
		if l.SPMLimit < 0 || l.SPMLimit > maxRPMLimit {
			add(field+".spm_limit", "invalid", spmLimitEN, spmLimitZH)
		}
		if l = withSPMWindow(l); !validSPMWindow(l.SPMWindowSeconds) {
			add(field+".spm_window_seconds", "invalid", spmWindowEN, spmWindowZH)
		}
		if len(models) == 0 {
			continue
		}
		l.Models = models
		out = append(out, l)
	}
	if len(out) > maxModelLimits {
		add("model_limits", "too_many", "at most 50 model lists", "最多 50 个模型列表")
	}
	return out, fe
}

// fillModelInUse sets the requests in flight of each model list of the views,
// one Redis read per distinct list, run concurrently (at most
// modelInUseParallel at a time). It must run after RateUsage is set: it
// writes into RateUsage.Models. Any read error leaves every count at 0.
func (s *Service) fillModelInUse(ctx context.Context, out []*View) {
	if s.d.Slots == nil {
		return
	}
	byKey := map[string][]int64{}
	for _, v := range out {
		for i := range v.ModelLimits {
			k := v.ModelLimits[i].Key()
			byKey[k] = append(byKey[k], v.ID)
		}
	}
	if len(byKey) == 0 {
		return
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	results := make([]map[int64]int, len(keys))
	errs := make([]error, len(keys))
	tasks := make([]func(), len(keys))
	for i, k := range keys {
		tasks[i] = func() { results[i], errs[i] = s.d.Slots.InUseMany(ctx, core.ModelLimitSlots(k), byKey[k]) }
	}
	runParallel(ctx, modelInUseParallel, tasks...)
	counts := make(map[string]map[int64]int, len(keys))
	for i, k := range keys {
		if errs[i] != nil {
			slog.WarnContext(ctx, "account: read model list slots", "err", errs[i])
			return
		}
		counts[k] = results[i]
	}
	for _, v := range out {
		for i := range v.ModelLimits {
			if i < len(v.RateUsage.Models) {
				v.RateUsage.Models[i].InUse = counts[v.ModelLimits[i].Key()][v.ID]
			}
		}
	}
}
