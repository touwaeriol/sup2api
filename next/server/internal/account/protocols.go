package account

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

// Protocol binding of an account (DESIGN-2026-11-17-PROTOCOL-BINDING):
// protocols lists the inbound protocols of the account type's own platforms
// the account serves (null = all of them), model_protocols the protocols a
// listed model is served on, which is also where a cross-platform conversion
// is enabled. The scheduler judges both through core.ProtocolRules.

// maxModelProtocols bounds the entries of an account's model_protocols.
const maxModelProtocols = 50

// How an account type serves a protocol (ServedProtocol.Via), in the order
// the protocols of a type are listed.
const (
	viaNative    = "native"    // an endpoint of its subset speaks it
	viaAdapted   = "adapted"   // another endpoint of the same platform serves it
	viaConverted = "converted" // a platform the type does not declare: converted
)

var viaOrder = []string{viaNative, viaAdapted, viaConverted}

// typeProtocol is how an account type serves one inbound protocol.
type typeProtocol struct {
	protocol string
	// own: the account type declares the platform of the protocol. The
	// other protocols reach it through a cross-platform conversion.
	own   bool
	via   string
	grade string
	hops  []string
}

// typeProtocols lists the inbound protocols the gateway routes to the
// account type, each once: the native ones, then the adapted, then the
// converted, each in the order of servedEndpoints. The protocol of a
// WebSocket or a task submission endpoint is listed only when the type
// serves it natively: the gateway never converts those.
func typeProtocols(g core.Generation, b core.AccountTypeBinding, conv core.ProtocolConverters) []typeProtocol {
	if g == nil {
		return nil
	}
	nativeOnly := map[string]bool{}
	for _, e := range g.Endpoints() {
		if e.Endpoint.WebSocket() || e.Endpoint.TaskSubmit() {
			nativeOnly[e.Endpoint.Protocol] = true
		}
	}
	var out []typeProtocol
	for _, v := range servedEndpoints(g, b, conv) {
		if nativeOnly[v.Protocol] && !v.Native || findProtocol(out, v.Protocol) != nil {
			continue
		}
		_, own := b.Supports(v.Platform)
		tp := typeProtocol{protocol: v.Protocol, own: own, via: viaConverted, grade: v.Grade, hops: v.Via}
		switch {
		case v.Native:
			tp.via = viaNative
		case own:
			tp.via = viaAdapted
		}
		out = append(out, tp)
	}
	slices.SortStableFunc(out, func(a, b typeProtocol) int {
		return slices.Index(viaOrder, a.via) - slices.Index(viaOrder, b.via)
	})
	return out
}

// findProtocol is the entry of protocol in served, nil when the account
// type does not serve it.
func findProtocol(served []typeProtocol, protocol string) *typeProtocol {
	if i := slices.IndexFunc(served, func(tp typeProtocol) bool { return tp.protocol == protocol }); i >= 0 {
		return &served[i]
	}
	return nil
}

// parseModelProtocols decodes a model_protocols column; never nil.
func parseModelProtocols(raw []byte) []core.ModelProtocolRule {
	return parseList[core.ModelProtocolRule](raw)
}

func modelProtocolsJSON(in []core.ModelProtocolRule) string { return listJSON(in) }

// protocolBinding is what decides the protocols an account serves: its
// model list (empty = all models), protocols (nil = all of its type's own
// platforms) and model_protocols.
type protocolBinding struct {
	models    []string
	protocols []string
	rules     []core.ModelProtocolRule
}

func (a *row) protocolBinding() protocolBinding {
	return protocolBinding{models: a.Models, protocols: a.Protocols, rules: parseModelProtocols(a.ModelProtocols)}
}

// compile is the binding as the scheduler judges it; nil when the account
// has neither protocols nor model_protocols.
func (b protocolBinding) compile() *core.ProtocolRules {
	return core.NewProtocolRules(b.protocols, b.rules)
}

// setsProtocols reports a request carrying protocols or model_protocols.
func (in *input) setsProtocols() bool { return in.Protocols.Set || in.ModelProtocols != nil }

// checkProtocols validates and normalizes the protocol binding fields of the
// request against the protocols served lists for the account type. cur is
// the saved account an update leaves the absent field of, nil on create.
func (in *input) checkProtocols(ctx context.Context, served []typeProtocol, cur *row) []core.FieldError {
	if !in.setsProtocols() {
		return nil
	}
	var fe []core.FieldError
	var b protocolBinding
	if cur != nil {
		b = cur.protocolBinding()
	}
	if in.Protocols.Set {
		if in.Protocols.Valid {
			var errs []core.FieldError
			in.Protocols.V, errs = normalizeProtocols(ctx, in.Protocols.V, served)
			fe = append(fe, errs...)
		}
		b.protocols = in.Protocols.V
	}
	if in.ModelProtocols != nil {
		rules, errs := normalizeModelProtocols(ctx, *in.ModelProtocols, served)
		fe = append(fe, errs...)
		in.ModelProtocols, b.rules = &rules, rules
	}
	if len(fe) == 0 && b.servesNothing(served) {
		fe = append(fe, core.FieldError{Field: "protocols", Code: "no_protocol", Message: t(ctx,
			"the account serves no protocol: enable one of its own, or a conversion for a model (to stop the account, turn scheduling off)",
			"账号没有开放任何协议：请开放一个本平台协议，或为模型开启协议转换（要停用账号请关闭调度）")})
	}
	return fe
}

// servesNothing reports a binding that closes every protocol of the type's
// own platforms without enabling a conversion for any model.
func (b protocolBinding) servesNothing(served []typeProtocol) bool {
	if b.protocols == nil || len(b.protocols) > 0 {
		return false
	}
	for _, r := range b.rules {
		for _, p := range r.Protocols {
			if tp := findProtocol(served, p); tp != nil && !tp.own {
				return false
			}
		}
	}
	return true
}

// bindableProtocol returns the field error of a protocol an account cannot
// be bound to, nil for one it can: a protocol the account type serves that
// is not a count_tokens protocol (those follow the generation protocol of
// their family) and, with ownOnly, belongs to a platform the type declares.
func bindableProtocol(ctx context.Context, field, protocol string, served []typeProtocol, ownOnly bool) *core.FieldError {
	tp := findProtocol(served, protocol)
	switch {
	case core.CountTokensFamily(protocol) != "":
		return &core.FieldError{Field: field, Code: "invalid", Message: t(ctx,
			"a count_tokens protocol is not listed: it follows the generation protocol of its family",
			"count_tokens 协议不单独列出：它跟随同族的生成协议")}
	case tp == nil:
		return &core.FieldError{Field: field, Code: "unsupported", Message: t(ctx,
			"the account type does not serve this protocol", "该账号类型不能服务此协议")}
	case ownOnly && !tp.own:
		return &core.FieldError{Field: field, Code: "cross_platform", Message: t(ctx,
			"a protocol of another platform is enabled per model, in model_protocols",
			"其他平台的协议要在“模型的协议限定”里按模型开启")}
	}
	return nil
}

// normalizeProtocolList trims the protocols at field, drops blank and
// repeated ones and checks each (bindableProtocol); never nil.
func normalizeProtocolList(ctx context.Context, field string, in []string, served []typeProtocol, ownOnly bool) ([]string, []core.FieldError) {
	var fe []core.FieldError
	out := make([]string, 0, len(in))
	for i, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		if e := bindableProtocol(ctx, field+"["+strconv.Itoa(i)+"]", p, served, ownOnly); e != nil {
			fe = append(fe, *e)
			continue
		}
		out = append(out, p)
	}
	return out, fe
}

// normalizeProtocols checks an account's protocols: each one a protocol of
// the type's own platforms the type serves, natively or adapted.
func normalizeProtocols(ctx context.Context, in []string, served []typeProtocol) ([]string, []core.FieldError) {
	return normalizeProtocolList(ctx, "protocols", in, served, true)
}

// normalizeModelProtocols checks an account's model_protocols: at most
// maxModelProtocols entries of at most maxModels models, a model in one
// entry only, core.OtherModels alone in its entry, and in every entry at
// least one protocol the type serves, natively, adapted or converted.
func normalizeModelProtocols(ctx context.Context, in []core.ModelProtocolRule, served []typeProtocol) ([]core.ModelProtocolRule, []core.FieldError) {
	var fe []core.FieldError
	add := func(field, code, en, zh string) {
		fe = append(fe, core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)})
	}
	out := make([]core.ModelProtocolRule, 0, len(in))
	entries := entryModels{other: core.OtherModels, owner: map[string]int{},
		invalid:   [2]string{"not a complete model id (the only wildcard is * alone, for the other models)", "不是完整的模型 ID（通配只有单独的 *，表示其余模型）"},
		duplicate: [2]string{"the model is already in another entry", "该模型已在另一条里"}}
	for i, r := range in {
		field := "model_protocols[" + strconv.Itoa(i) + "]"
		models, refused := entries.of(ctx, field, i, r.Models)
		fe = append(fe, refused...)
		switch {
		case len(models) == 0 && len(refused) == 0:
			add(field+".models", "required", "choose at least one model", "至少选择一个模型")
		case len(models) > 1 && slices.Contains(models, core.OtherModels):
			add(field+".models", "invalid", "* (the other models) must be alone in its entry", "*（其余模型）只能单独成条")
		case len(models) > maxModels:
			add(field+".models", "too_many", "at most 500 models per entry", "每条最多 500 个模型")
		}
		protocols, errs := normalizeProtocolList(ctx, field+".protocols", r.Protocols, served, false)
		fe = append(fe, errs...)
		if len(protocols) == 0 && len(errs) == 0 {
			add(field+".protocols", "required", "choose at least one protocol", "至少选择一个协议")
		}
		out = append(out, core.ModelProtocolRule{Models: models, Protocols: protocols})
	}
	if len(out) > maxModelProtocols {
		add("model_protocols", "too_many", "at most 50 entries", "最多 50 条")
	}
	return out, fe
}

// ServedProtocol is an inbound protocol an account serves now (GET
// /accounts/:id): how its type serves the protocol and for which models.
type ServedProtocol struct {
	Protocol string `json:"protocol"`
	// Via is native, adapted (another endpoint of the same platform serves
	// it) or converted (a cross-platform conversion).
	Via string `json:"via"`
	// Grade and Hops are those of the type's endpoint (EndpointView): native,
	// the conversion's grade or local; the protocols passed through.
	Grade string   `json:"grade"`
	Hops  []string `json:"hops,omitempty"`
	// Models are the models served on the protocol, sorted; null = every
	// model model_protocols does not bind to other protocols.
	Models []string `json:"models"`
}

// servedProtocols lists what an account with the binding serves of the
// protocols its type serves, in their order; never nil. Every model is
// judged by the compiled rules, as the scheduler judges it.
func (b protocolBinding) servedProtocols(served []typeProtocol) []ServedProtocol {
	out := []ServedProtocol{}
	rules := b.compile()
	named, others := b.boundModels()
	for _, tp := range served {
		if models, ok := modelsOn(rules, tp, named, others); ok {
			out = append(out, ServedProtocol{Protocol: tp.protocol, Via: tp.via, Grade: tp.grade, Hops: tp.hops, Models: models})
		}
	}
	return out
}

// boundModels splits the models of the account (its model list, empty = all
// models) by how model_protocols reaches them: named are those a rule lists
// by name, sorted; others reports that it has models no rule names.
func (b protocolBinding) boundModels() (named []string, others bool) {
	listed := map[string]bool{}
	for _, r := range b.rules {
		for _, m := range r.Models {
			if m == core.OtherModels || listed[m] {
				continue
			}
			listed[m] = true
			if len(b.models) == 0 || slices.Contains(b.models, m) {
				named = append(named, m)
			}
		}
	}
	slices.Sort(named)
	others = len(b.models) == 0 || slices.ContainsFunc(b.models, func(m string) bool { return !listed[m] })
	return named, others
}

// modelsOn returns the models an account serves on a protocol of its type:
// nil when it serves on it the models no rule names (others, from
// boundModels), else those of named it serves on it; false when it serves
// none.
func modelsOn(rules *core.ProtocolRules, tp typeProtocol, named []string, others bool) (models []string, ok bool) {
	if _, serves := rules.ServesOthers(tp.protocol, !tp.own); others && serves {
		return nil, true
	}
	for _, m := range named {
		if _, serves := rules.Serves(m, tp.protocol, !tp.own); serves {
			models = append(models, m)
		}
	}
	return models, len(models) > 0
}

var _ core.GroupProtocols = (*Service)(nil)

// accountServedProtocols lists the protocols account a serves now; empty
// when its type is not registered.
func (s *Service) accountServedProtocols(a *row) []ServedProtocol {
	bt, _ := s.accountType(a.PluginKey, a.Type)
	return a.protocolBinding().servedProtocols(typeProtocols(s.gen(), bt, s.d.Converters))
}

// GroupServedProtocols implements core.GroupProtocols: per group, the
// protocols its live accounts serve for at least one model, each with the
// number of accounts serving it. One read covers all the groups.
func (s *Service) GroupServedProtocols(ctx context.Context, groupIDs []int64) (map[int64][]core.GroupServedProtocol, error) {
	out := make(map[int64][]core.GroupServedProtocol, len(groupIDs))
	for _, id := range groupIDs {
		out[id] = []core.GroupServedProtocol{}
	}
	g := s.gen()
	if g == nil || len(groupIDs) == 0 {
		return out, nil
	}
	var members []struct {
		GroupID        int64         `gorm:"column:group_id"`
		PluginKey      string        `gorm:"column:plugin_key"`
		Type           string        `gorm:"column:type"`
		Models         store.Strings `gorm:"column:models"`
		Protocols      store.Strings `gorm:"column:protocols"`
		ModelProtocols []byte        `gorm:"column:model_protocols"`
	}
	if err := store.G(ctx, s.d.DB.Gorm).Table("accounts a").
		Select("ag.group_id, a.plugin_key, a.type, a.models, a.protocols, NULLIF(a.model_protocols, '[]'::jsonb) AS model_protocols").
		Joins("JOIN account_groups ag ON ag.account_id = a.id").
		Where("ag.group_id = ANY(?) AND a.deleted_at IS NULL", store.Int64s(groupIDs)).Scan(&members).Error; err != nil {
		return nil, err
	}
	types := map[core.AccountTypeKey][]typeProtocol{}
	counts := map[int64]map[string]int{}
	for _, m := range members {
		k := core.AccountTypeKey{PluginKey: m.PluginKey, Type: m.Type}
		served, ok := types[k]
		if !ok {
			if bt, found := g.AccountType(m.PluginKey, m.Type); found {
				served = typeProtocols(g, bt, s.d.Converters)
			}
			types[k] = served
		}
		if counts[m.GroupID] == nil {
			counts[m.GroupID] = map[string]int{}
		}
		b := protocolBinding{models: m.Models, protocols: m.Protocols, rules: parseModelProtocols(m.ModelProtocols)}
		for _, sp := range b.servedProtocols(served) {
			counts[m.GroupID][sp.Protocol]++
		}
	}
	endpoints := g.Endpoints()
	for id, c := range counts {
		for _, e := range endpoints {
			if n, ok := c[e.Endpoint.Protocol]; ok {
				out[id] = append(out[id], core.GroupServedProtocol{Protocol: e.Endpoint.Protocol, Accounts: n})
				delete(c, e.Endpoint.Protocol) // a protocol two endpoints speak is listed once
			}
		}
	}
	return out, nil
}
