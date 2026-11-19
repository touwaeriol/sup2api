package account

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/testutil"
)

// bindingConverters are the conversions of protocolGen: chat is adapted to
// responses on the openai platform, messages converted to responses, and
// (never routed: the gateway relays a WebSocket natively only) the
// WebSocket protocol to responses.
type bindingConverters struct{}

func (bindingConverters) Conversion(client, upstream string) (core.Conversion, bool) {
	ok := upstream == "openai.responses" &&
		(client == "openai.chat" || client == "anthropic.messages" || client == "openai.responses_ws")
	return fairConversion(client, upstream, ok)
}

// protocolGen is testGen with the openai platform as the core declares it
// (chat, embeddings, responses and the responses WebSocket), and two types
// of a codex plugin: chatgpt serving only the responses endpoint of openai,
// and full serving all of them.
func protocolGen() (g *fakeGen, chatgpt, full core.AccountTypeBinding) {
	g = testGen(&fakePlatform{})
	for i := range g.plats {
		if g.plats[i].Platform.ID != "openai" {
			continue
		}
		eps := []manifest.Endpoint{
			ep("POST", "/v1/chat/completions", "openai.chat", "usage"),
			ep("POST", "/v1/embeddings", "openai.embeddings", "usage"),
			ep("POST", "/v1/responses", "openai.responses", "usage"),
			ep("GET", "/v1/responses", "openai.responses_ws", "usage"),
		}
		for j, id := range []string{"chat_completions", "embeddings", "responses", "responses_websocket"} {
			eps[j].ID = id
		}
		eps[3].Kind = manifest.EndpointKindWebSocket
		g.plats[i].Platform.Endpoints = eps
	}
	codex := core.PluginInfo{Key: "codex"}
	chatgpt = core.AccountTypeBinding{Plugin: codex, Type: manifest.AccountType{ID: "chatgpt",
		Platforms: []manifest.AccountPlatform{{Platform: "openai", Endpoints: []string{"responses"},
			Adapted: map[string]manifest.AdaptOptions{"chat_completions": {}}}}}}
	full = core.AccountTypeBinding{Plugin: codex, Type: manifest.AccountType{ID: "full",
		Platforms: []manifest.AccountPlatform{{Platform: "openai"}}}}
	g.plugins = append(g.plugins, codex)
	g.types = append(g.types, chatgpt, full)
	return g, chatgpt, full
}

// protocolList renders type protocols as "protocol via grade", own ones
// marked with a leading "+".
func protocolList(served []typeProtocol) string {
	var s []string
	for _, tp := range served {
		own := ""
		if tp.own {
			own = "+"
		}
		s = append(s, own+tp.protocol+" "+tp.via+" "+tp.grade)
	}
	return strings.Join(s, "; ")
}

func TestTypeProtocols(t *testing.T) {
	g, chatgpt, full := protocolGen()
	// The subset type: responses natively, chat adapted on its own platform,
	// messages (and its count_tokens, estimated locally) across platforms.
	// Embeddings convert to nothing; the WebSocket is never converted.
	want := "+openai.responses native native; +openai.chat adapted fair; " +
		"anthropic.messages converted fair; anthropic.count_tokens converted local"
	require.Equal(t, want, protocolList(typeProtocols(g, chatgpt, bindingConverters{})))
	served := typeProtocols(g, chatgpt, bindingConverters{})
	require.Equal(t, []string{"openai.chat", "openai.responses"}, findProtocol(served, "openai.chat").hops)
	require.Nil(t, findProtocol(served, "openai.responses_ws"))

	want = "+openai.chat native native; +openai.embeddings native native; +openai.responses_ws native native; +openai.responses native native; " +
		"anthropic.messages converted fair; anthropic.count_tokens converted local"
	require.Equal(t, want, protocolList(typeProtocols(g, full, bindingConverters{})))
	// Without converters only the endpoints of the subset; no generation, none.
	require.Equal(t, "+openai.responses native native", protocolList(typeProtocols(g, chatgpt, nil)))
	require.Empty(t, typeProtocols(nil, chatgpt, bindingConverters{}))
}

// fieldErrors renders field errors as "field:code".
func fieldErrors(fe []core.FieldError) []string {
	out := []string{}
	for _, e := range fe {
		out = append(out, e.Field+":"+e.Code)
	}
	return out
}

func TestNormalizeProtocols(t *testing.T) {
	g, chatgpt, _ := protocolGen()
	served := typeProtocols(g, chatgpt, bindingConverters{})
	ctx := context.Background()

	got, fe := normalizeProtocols(ctx, []string{" openai.responses ", "openai.chat", "openai.responses", ""}, served)
	require.Empty(t, fe)
	require.Equal(t, []string{"openai.responses", "openai.chat"}, got, "native and adapted, trimmed, repeated and blank dropped")

	got, fe = normalizeProtocols(ctx, []string{}, served)
	require.Empty(t, fe)
	require.NotNil(t, got, "[] stays a list: every own protocol closed")

	_, fe = normalizeProtocols(ctx, []string{
		"openai.responses",
		"anthropic.messages",     // another platform: enabled per model
		"anthropic.count_tokens", // follows its generation protocol
		"openai.embeddings",      // the type does not serve it
		"openai.responses_ws",    // nor a WebSocket it would have to convert
		"nope.protocol",
	}, served)
	require.Equal(t, []string{"protocols[1]:cross_platform", "protocols[2]:invalid", "protocols[3]:unsupported",
		"protocols[4]:unsupported", "protocols[5]:unsupported"}, fieldErrors(fe))
}

func TestNormalizeModelProtocols(t *testing.T) {
	g, chatgpt, _ := protocolGen()
	served := typeProtocols(g, chatgpt, bindingConverters{})
	ctx := context.Background()
	rule := func(protocols []string, models ...string) core.ModelProtocolRule {
		return core.ModelProtocolRule{Models: models, Protocols: protocols}
	}
	own, converted := []string{"openai.responses", "openai.chat"}, []string{"anthropic.messages"}

	got, fe := normalizeModelProtocols(ctx, []core.ModelProtocolRule{
		rule([]string{" anthropic.messages ", "anthropic.messages"}, " claude-sonnet-4-5 ", "claude-sonnet-4-5", ""),
		rule(append(own, converted...), "gpt-5.6"),
		rule(own, core.OtherModels),
	}, served)
	require.Empty(t, fe)
	require.Equal(t, []core.ModelProtocolRule{rule(converted, "claude-sonnet-4-5"), rule(append(own, converted...), "gpt-5.6"),
		rule(own, core.OtherModels)}, got)

	got, fe = normalizeModelProtocols(ctx, []core.ModelProtocolRule{}, served)
	require.Empty(t, fe)
	require.NotNil(t, got)

	_, fe = normalizeModelProtocols(ctx, []core.ModelProtocolRule{
		rule(own, "gpt-5.6", "bad model", "gpt-*"),
		rule(own, "gpt-5.6", "gpt-4o"),                // gpt-5.6 is in the first entry
		rule(own, core.OtherModels, "gpt-4.1"),        // * with company
		rule(own, core.OtherModels),                   // a second *
		rule(own),                                     // no model
		rule(nil, "o3"),                               // no protocol
		rule([]string{"anthropic.count_tokens"}, "a"), // follows messages
		rule([]string{"openai.embeddings", "openai.responses_ws", "gemini.generate"}, "b"),
	}, served)
	require.Equal(t, []string{
		"model_protocols[0].models[1]:invalid", "model_protocols[0].models[2]:invalid",
		"model_protocols[1].models[0]:duplicate",
		"model_protocols[2].models:invalid",
		"model_protocols[3].models[0]:duplicate",
		"model_protocols[4].models:required",
		"model_protocols[5].protocols:required",
		"model_protocols[6].protocols[0]:invalid",
		"model_protocols[7].protocols[0]:unsupported", "model_protocols[7].protocols[1]:unsupported", "model_protocols[7].protocols[2]:unsupported",
	}, fieldErrors(fe))

	many := make([]core.ModelProtocolRule, maxModelProtocols+1)
	for i := range many {
		many[i] = rule(own, fmt.Sprintf("m-%d", i))
	}
	models := make([]string, maxModels+1)
	for i := range models {
		models[i] = fmt.Sprintf("n-%d", i)
	}
	many[0].Models = models
	_, fe = normalizeModelProtocols(ctx, many, served)
	require.Equal(t, []string{"model_protocols[0].models:too_many", "model_protocols:too_many"}, fieldErrors(fe))
	_, fe = normalizeModelProtocols(ctx, many[1:], served)
	require.Empty(t, fe, "50 entries are accepted")
}

// A request is judged with the saved value of the field it leaves out; an
// account that would serve nothing is refused.
func TestCheckProtocols(t *testing.T) {
	g, chatgpt, _ := protocolGen()
	served := typeProtocols(g, chatgpt, bindingConverters{})
	ctx := context.Background()
	conversion := []core.ModelProtocolRule{{Models: []string{"claude-sonnet-4-5"}, Protocols: []string{"anthropic.messages"}}}
	ownOnly := []core.ModelProtocolRule{{Models: []string{"gpt-5.6"}, Protocols: []string{"openai.chat"}}}
	saved := func(protocols []string, rules []core.ModelProtocolRule) *row {
		return &row{Protocols: protocols, ModelProtocols: []byte(modelProtocolsJSON(rules))}
	}
	closed := nullable[[]string]{Set: true, Valid: true, V: []string{}}
	for _, tc := range []struct {
		name string
		in   input
		cur  *row
		want []string
	}{
		{name: "nothing sent", in: input{}, cur: saved([]string{}, nil)},
		{name: "create, every own protocol closed", in: input{Protocols: closed}, want: []string{"protocols:no_protocol"}},
		{name: "create, closed with a conversion", in: input{Protocols: closed, ModelProtocols: &conversion}},
		{name: "create, closed with own rules only", in: input{Protocols: closed, ModelProtocols: &ownOnly}, want: []string{"protocols:no_protocol"}},
		{name: "create, all protocols", in: input{Protocols: nullable[[]string]{Set: true}}},
		{name: "update closes, the saved rules convert", in: input{Protocols: closed}, cur: saved(nil, conversion)},
		{name: "update closes, the saved rules do not", in: input{Protocols: closed}, cur: saved(nil, ownOnly), want: []string{"protocols:no_protocol"}},
		{name: "update drops the conversion of a closed account", in: input{ModelProtocols: &ownOnly}, cur: saved([]string{}, conversion),
			want: []string{"protocols:no_protocol"}},
		{name: "update drops the rules of an open account", in: input{ModelProtocols: &[]core.ModelProtocolRule{}}, cur: saved([]string{"openai.chat"}, conversion)},
		{name: "update reopens", in: input{Protocols: nullable[[]string]{Set: true}}, cur: saved([]string{}, conversion)},
		{name: "an item error hides no_protocol", in: input{Protocols: nullable[[]string]{Set: true, Valid: true, V: []string{"anthropic.messages"}}},
			want: []string{"protocols[0]:cross_platform"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ElementsMatch(t, tc.want, fieldErrors(tc.in.checkProtocols(ctx, served, tc.cur)))
		})
	}
	// The request is normalized in place.
	in := input{Protocols: nullable[[]string]{Set: true, Valid: true, V: []string{" openai.chat ", "openai.chat"}},
		ModelProtocols: &[]core.ModelProtocolRule{{Models: []string{" gpt-5.6 "}, Protocols: []string{"openai.chat", "openai.chat"}}}}
	require.Empty(t, in.checkProtocols(ctx, served, nil))
	require.Equal(t, []string{"openai.chat"}, in.Protocols.V)
	require.Equal(t, []core.ModelProtocolRule{{Models: []string{"gpt-5.6"}, Protocols: []string{"openai.chat"}}}, *in.ModelProtocols)
}

// servedList renders served protocols as "protocol via[models]"; "*" marks
// a protocol open to every model no rule lists.
func servedList(served []ServedProtocol) string {
	var s []string
	for _, p := range served {
		models := "*"
		if p.Models != nil {
			models = strings.Join(p.Models, ",")
		}
		s = append(s, p.Protocol+" "+p.Via+"["+models+"]")
	}
	return strings.Join(s, "; ")
}

func TestServedProtocols(t *testing.T) {
	g, chatgpt, _ := protocolGen()
	served := typeProtocols(g, chatgpt, bindingConverters{})
	const allOwn = "openai.responses native[*]; openai.chat adapted[*]"
	rules := []core.ModelProtocolRule{
		{Models: []string{"claude-sonnet-4-5", "claude-opus-4-1"}, Protocols: []string{"anthropic.messages"}},
		{Models: []string{"gpt-5.6"}, Protocols: []string{"openai.chat", "openai.responses", "anthropic.messages"}},
	}
	other := core.ModelProtocolRule{Models: []string{core.OtherModels}, Protocols: []string{"openai.responses", "anthropic.messages"}}
	for _, tc := range []struct {
		name string
		b    protocolBinding
		want string
	}{
		{name: "no binding: the own protocols, no conversion", want: allOwn},
		{name: "bound to one protocol", b: protocolBinding{protocols: []string{"openai.responses"}}, want: "openai.responses native[*]"},
		{name: "every own protocol closed", b: protocolBinding{protocols: []string{}}, want: ""},
		{name: "conversions enabled for listed models, count_tokens following", b: protocolBinding{rules: rules},
			want: allOwn + "; anthropic.messages converted[claude-opus-4-1,claude-sonnet-4-5,gpt-5.6]; " +
				"anthropic.count_tokens converted[claude-opus-4-1,claude-sonnet-4-5,gpt-5.6]"},
		{name: "a conversion survives closed own protocols", b: protocolBinding{protocols: []string{}, rules: rules[:1]},
			want: "anthropic.messages converted[claude-opus-4-1,claude-sonnet-4-5]; anthropic.count_tokens converted[claude-opus-4-1,claude-sonnet-4-5]"},
		{name: "a listed own protocol the account closed is not served", b: protocolBinding{protocols: []string{"openai.responses"}, rules: rules[1:]},
			want: "openai.responses native[*]; anthropic.messages converted[gpt-5.6]; anthropic.count_tokens converted[gpt-5.6]"},
		{name: "the other models rule decides the unlisted models", b: protocolBinding{rules: append(rules[1:2:2], other)},
			want: "openai.responses native[*]; openai.chat adapted[gpt-5.6]; anthropic.messages converted[*]; anthropic.count_tokens converted[*]"},
		{name: "models outside the account's model list are left out", b: protocolBinding{models: []string{"gpt-5.6", "claude-opus-4-1", "gpt-4o"}, rules: rules},
			want: allOwn + "; anthropic.messages converted[claude-opus-4-1,gpt-5.6]; anthropic.count_tokens converted[claude-opus-4-1,gpt-5.6]"},
		{name: "a model list whose every model has a rule leaves no other model", b: protocolBinding{models: []string{"gpt-5.6", "claude-opus-4-1"}, rules: rules},
			want: "openai.responses native[gpt-5.6]; openai.chat adapted[gpt-5.6]; " +
				"anthropic.messages converted[claude-opus-4-1,gpt-5.6]; anthropic.count_tokens converted[claude-opus-4-1,gpt-5.6]"},
		{name: "a protocol none of the listed models is served on is not listed", b: protocolBinding{models: []string{"claude-opus-4-1"}, rules: rules[:1]},
			want: "anthropic.messages converted[claude-opus-4-1]; anthropic.count_tokens converted[claude-opus-4-1]"},
		{name: "the other models rule converts no model when the model list has none left", b: protocolBinding{models: []string{"gpt-5.6"}, rules: append(rules[1:2:2], other)},
			want: "openai.responses native[gpt-5.6]; openai.chat adapted[gpt-5.6]; anthropic.messages converted[gpt-5.6]; anthropic.count_tokens converted[gpt-5.6]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.b.servedProtocols(served)
			require.NotNil(t, got)
			require.Equal(t, tc.want, servedList(got))
			requireServedAsScheduled(t, tc.b, served, []string{"gpt-5.6", "claude-opus-4-1", "gpt-4o", "unlisted"})
		})
	}
	hops := (protocolBinding{}).servedProtocols(served)[1]
	require.Equal(t, ServedProtocol{Protocol: "openai.chat", Via: viaAdapted, Grade: "fair", Hops: []string{"openai.chat", "openai.responses"}}, hops)
}

// requireServedAsScheduled checks the view against the scheduler, model by
// model: a protocol lists a model exactly when the account's model list
// holds it and the compiled rules serve it there. null stands for the models
// no rule names, of which the account then has at least one; the models a
// rule names are not told apart under it.
func requireServedAsScheduled(t *testing.T, b protocolBinding, served []typeProtocol, models []string) {
	t.Helper()
	compiled := b.compile()
	view := map[string]ServedProtocol{}
	for _, sp := range b.servedProtocols(served) {
		view[sp.Protocol] = sp
	}
	named, others := b.boundModels()
	for _, tp := range served {
		sp, open := view[tp.protocol]
		require.False(t, open && sp.Models == nil && !others, "%s: open to other models the account does not have", tp.protocol)
		for _, model := range models {
			_, schedules := compiled.Serves(model, tp.protocol, !tp.own)
			schedules = schedules && (len(b.models) == 0 || slices.Contains(b.models, model))
			isNamed := slices.Contains(named, model)
			switch {
			case !open:
				require.False(t, schedules, "%s on %s, not shown", model, tp.protocol)
			case sp.Models != nil:
				require.Equal(t, slices.Contains(sp.Models, model), schedules, "%s on %s", model, tp.protocol)
			case !isNamed:
				require.Equal(t, len(b.models) == 0 || slices.Contains(b.models, model), schedules, "%s on %s, shown for every other model", model, tp.protocol)
			}
		}
	}
}

// Random bindings: whatever the model list, protocols and rules, the view of
// what the account serves is what the scheduler decides.
func TestServedProtocolsMatchScheduling(t *testing.T) {
	g, chatgpt, _ := protocolGen()
	served := typeProtocols(g, chatgpt, bindingConverters{})
	models := []string{"a", "b", "c", "d"}
	protocols := []string{"openai.responses", "openai.chat", "anthropic.messages"}
	rnd := rand.New(rand.NewPCG(1, 2))
	some := func(of []string) []string {
		out := []string{}
		for _, v := range of {
			if rnd.IntN(2) == 0 {
				out = append(out, v)
			}
		}
		return out
	}
	for range 5000 {
		b := protocolBinding{models: some(models)}
		if rnd.IntN(2) == 0 {
			b.protocols = some(protocols[:2])
		}
		for _, m := range some(append([]string{core.OtherModels}, models...)) {
			b.rules = append(b.rules, core.ModelProtocolRule{Models: []string{m}, Protocols: some(protocols)})
		}
		requireServedAsScheduled(t, b, served, append([]string{"unlisted"}, models...))
	}
}

// The binding is saved by create and PATCH, each field replaced as a whole,
// read back in the list and the detail, and reaches the gateway through
// Load. GET /accounts/:id adds what the account serves.
func TestAccountProtocolBinding(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	path := func(id int64) string { return fmt.Sprintf("/accounts/%d", id) }
	served := func(out map[string]any) string {
		var s []string
		for _, v := range data(out)["served_protocols"].([]any) {
			p := v.(map[string]any)
			models := "*"
			if list, ok := p["models"].([]any); ok {
				models = strings.Trim(fmt.Sprint(list), "[]")
			}
			s = append(s, fmt.Sprintf("%v %v %v[%s]", p["protocol"], p["via"], p["grade"], models))
		}
		return strings.Join(s, "; ")
	}
	const own = "anthropic.messages native native[*]; anthropic.count_tokens native native[*]"

	// Without a binding: null, [], and the protocols of the type's platform.
	plain := e.mkAccount("sk-good-key-123")
	code, out := e.do("GET", path(plain), nil)
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, data(out)["protocols"])
	require.Contains(t, data(out), "protocols")
	require.Equal(t, []any{}, data(out)["model_protocols"])
	require.Equal(t, own, served(out))
	acc, err := e.svc.Load(ctx, plain)
	require.NoError(t, err)
	require.Nil(t, acc.ProtocolRules)

	rules := []any{map[string]any{"models": []any{"gpt-5.6", "gpt-5.6-mini"}, "protocols": []any{"openai.chat", "anthropic.messages"}}}
	code, out = e.do("POST", "/accounts", map[string]any{"name": "bound", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"},
		"protocols":   []string{"anthropic.messages"}, "model_protocols": rules})
	require.Equal(t, 201, code, fmt.Sprint(out))
	id := int64(data(out)["id"].(float64))
	require.Equal(t, []any{"anthropic.messages"}, data(out)["protocols"])
	require.Equal(t, rules, data(out)["model_protocols"])
	require.Equal(t, own+"; openai.chat converted fair[gpt-5.6 gpt-5.6-mini]", served(out))

	// The list carries the two fields, not what they amount to.
	_, out = e.do("GET", "/accounts?q=bound", nil)
	item := out["data"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"anthropic.messages"}, item["protocols"])
	require.Equal(t, rules, item["model_protocols"])
	require.NotContains(t, item, "served_protocols")

	acc, err = e.svc.Load(ctx, id)
	require.NoError(t, err)
	for _, q := range []struct {
		model, protocol string
		cross           bool
		want            string
	}{
		{"gpt-5.6", "openai.chat", true, ""},
		{"gpt-5.6", "anthropic.messages", false, ""},
		{"gpt-5.6", "anthropic.count_tokens", false, ""},
		{"claude-sonnet-4-5", "openai.chat", true, core.SchedConversionNotEnabled},
		{"claude-sonnet-4-5", "anthropic.messages", false, ""},
	} {
		reason, _ := acc.ProtocolRules.Serves(q.model, q.protocol, q.cross)
		require.Equal(t, q.want, reason, "%s on %s", q.model, q.protocol)
	}

	// A PATCH without the fields keeps both.
	code, out = e.do("PATCH", path(id), map[string]any{"name": "renamed"})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, []any{"anthropic.messages"}, data(out)["protocols"])
	require.Equal(t, rules, data(out)["model_protocols"])

	// Each field is replaced as a whole, audited by name, and the change is
	// broadcast (account:changed) so every node drops its snapshots.
	changes := e.recordAccountChanges()
	code, out = e.do("PATCH", path(id), map[string]any{"protocols": []string{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, []any{}, data(out)["protocols"])
	require.Equal(t, "openai.chat converted fair[gpt-5.6 gpt-5.6-mini]", served(out))
	require.NotEmpty(t, changes())
	audits := e.audits("account", id)
	require.Equal(t, map[string]any{"fields": []any{"protocols"}}, audits[len(audits)-1].Detail)
	acc, err = e.svc.Load(ctx, id)
	require.NoError(t, err, "account:changed dropped the cached account")
	reason, _ := acc.ProtocolRules.Serves("claude-sonnet-4-5", "anthropic.messages", false)
	require.Equal(t, core.SchedProtocolClosed, reason)

	// Dropping the conversion would leave the account serving nothing.
	code, out = e.do("PATCH", path(id), map[string]any{"model_protocols": []any{}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"protocols:no_protocol"}, fieldsOf(out))

	code, out = e.do("PATCH", path(id), map[string]any{"protocols": nil, "model_protocols": []any{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, data(out)["protocols"])
	require.Equal(t, []any{}, data(out)["model_protocols"])
	require.Equal(t, own, served(out))
	audits = e.audits("account", id)
	require.Equal(t, map[string]any{"fields": []any{"protocols", "model_protocols"}}, audits[len(audits)-1].Detail)
	acc, err = e.svc.Load(ctx, id)
	require.NoError(t, err)
	require.Nil(t, acc.ProtocolRules)
	var protocols *string
	var modelProtocols string
	require.NoError(t, e.db.SQL.QueryRowContext(ctx, `SELECT protocols::text, model_protocols::text FROM accounts WHERE id = $1`, id).
		Scan(&protocols, &modelProtocols))
	require.Nil(t, protocols, "null is stored as NULL")
	require.Equal(t, "[]", modelProtocols)

	// Refused protocols are reported at their place, on create and update.
	bad := map[string]any{"protocols": []string{"anthropic.messages", "openai.chat", "anthropic.count_tokens"},
		"model_protocols": []any{
			map[string]any{"models": []any{"a", "*"}, "protocols": []any{"openai.embeddings"}},
			map[string]any{"models": []any{"a"}, "protocols": []any{}},
		}}
	want := []string{"protocols[1]:cross_platform", "protocols[2]:invalid", "model_protocols[0].models:invalid",
		"model_protocols[0].protocols[0]:unsupported", "model_protocols[1].models[0]:duplicate", "model_protocols[1].protocols:required"}
	code, out = e.do("PATCH", path(id), bad)
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.ElementsMatch(t, want, fieldsOf(out))
	bad["name"], bad["plugin_key"], bad["type"], bad["credentials"] = "refused", "anthropic", "apikey", map[string]any{"api_key": "sk-good-key-123"}
	code, out = e.do("POST", "/accounts", bad)
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.ElementsMatch(t, want, fieldsOf(out))
	code, out = e.do("POST", "/accounts", map[string]any{"name": "closed", "plugin_key": "anthropic", "type": "apikey",
		"credentials": map[string]any{"api_key": "sk-good-key-123"}, "protocols": []string{}})
	require.Equal(t, 400, code, fmt.Sprint(out))
	require.Equal(t, []string{"protocols:no_protocol"}, fieldsOf(out))

	// The protocols of a type are only known while its plugin is enabled.
	e.reg.set(&fakeGen{})
	code, out = e.do("PATCH", path(id), map[string]any{"protocols": []string{"anthropic.messages"}})
	require.Equal(t, 503, code, fmt.Sprint(out))
	code, out = e.do("GET", path(id), nil)
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Equal(t, []any{}, data(out)["served_protocols"])
}

// The group snapshot compiles the binding of the accounts that have one and
// leaves the others without (the NULL, NULL most accounts read as).
func TestGroupSnapshotProtocolRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	group := testutil.InsertGroup(t, e.db, "bound")
	create := func(name string, extra map[string]any) int64 {
		body := map[string]any{"name": name, "plugin_key": "anthropic", "type": "apikey", "group_ids": []int64{group},
			"credentials": map[string]any{"api_key": "sk-good-key-123"}}
		for k, v := range extra {
			body[k] = v
		}
		code, out := e.do("POST", "/accounts", body)
		require.Equal(t, 201, code, fmt.Sprint(out))
		return int64(data(out)["id"].(float64))
	}
	plain := create("plain", nil)
	bound := create("bound", map[string]any{"protocols": []string{"anthropic.messages"}})
	converting := create("converting", map[string]any{"model_protocols": []any{
		map[string]any{"models": []any{"gpt-5.6"}, "protocols": []any{"openai.chat"}},
		map[string]any{"models": []any{"*"}, "protocols": []any{"anthropic.messages"}}}})

	snap, err := readGroupSnap(store.G(ctx, e.db.Gorm), group, time.Now())
	require.NoError(t, err)
	rules := map[int64]*core.ProtocolRules{}
	for _, r := range snap.refs {
		rules[r.ID] = r.ProtocolRules
	}
	require.Len(t, rules, 3)
	require.Nil(t, rules[plain])
	require.NotNil(t, rules[bound])
	require.NotNil(t, rules[converting])
	serves := func(id int64, model, protocol string, cross bool) string {
		reason, _ := rules[id].Serves(model, protocol, cross)
		return reason
	}
	require.Equal(t, "", serves(plain, "m", "anthropic.messages", false))
	require.Equal(t, core.SchedConversionNotEnabled, serves(plain, "m", "openai.chat", true))
	require.Equal(t, "", serves(bound, "m", "anthropic.messages", false))
	require.Equal(t, core.SchedConversionNotEnabled, serves(bound, "m", "openai.chat", true))
	require.Equal(t, "", serves(converting, "gpt-5.6", "openai.chat", true))
	require.Equal(t, core.SchedModelProtocolClosed, serves(converting, "gpt-5.6", "anthropic.messages", false))
	require.Equal(t, "", serves(converting, "claude-sonnet-4-5", "anthropic.messages", false))
	require.Equal(t, core.SchedModelProtocolClosed, serves(converting, "claude-sonnet-4-5", "openai.chat", true))

	// Candidates hands the cached snapshot to the gateway; a changed binding
	// replaces it at once on the node that saved it.
	bindingOf := func(id int64) *core.ProtocolRules {
		refs, err := e.svc.Candidates(ctx, group, nil)
		require.NoError(t, err)
		require.Len(t, refs, 3)
		for _, r := range refs {
			if r.ID == id {
				return r.ProtocolRules
			}
		}
		t.Fatalf("account %d is no candidate", id)
		return nil
	}
	require.Nil(t, bindingOf(plain))
	require.NotNil(t, bindingOf(bound))
	code, out := e.do("PATCH", fmt.Sprintf("/accounts/%d", plain), map[string]any{"protocols": []string{}, "model_protocols": []any{
		map[string]any{"models": []any{"gpt-5.6"}, "protocols": []any{"openai.chat"}}}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	reason, _ := bindingOf(plain).Serves("m", "anthropic.messages", false)
	require.Equal(t, core.SchedProtocolClosed, reason)
	code, out = e.do("PATCH", fmt.Sprintf("/accounts/%d", plain), map[string]any{"protocols": nil, "model_protocols": []any{}})
	require.Equal(t, 200, code, fmt.Sprint(out))
	require.Nil(t, bindingOf(plain))
}

// The protocols of a group are those its live accounts serve, counted per
// account, whatever their status.
func TestGroupServedProtocols(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	group, empty, other := testutil.InsertGroup(t, e.db, "served"), testutil.InsertGroup(t, e.db, "empty"), testutil.InsertGroup(t, e.db, "other")
	create := func(name, plugin, typ string, extra map[string]any) int64 {
		body := map[string]any{"name": name, "plugin_key": plugin, "type": typ, "group_ids": []int64{group},
			"credentials": map[string]any{"api_key": "sk-good-key-123"}}
		for k, v := range extra {
			body[k] = v
		}
		code, out := e.do("POST", "/accounts", body)
		require.Equal(t, 201, code, fmt.Sprint(out))
		return int64(data(out)["id"].(float64))
	}
	// list reads all the groups at once and renders those of groupID.
	list := func(groupID int64) string {
		all, err := e.svc.GroupServedProtocols(ctx, []int64{group, empty, other})
		require.NoError(t, err)
		require.Len(t, all, 3)
		got := all[groupID]
		require.NotNil(t, got)
		var s []string
		for _, p := range got {
			s = append(s, fmt.Sprintf("%s=%d", p.Protocol, p.Accounts))
		}
		return strings.Join(s, " ")
	}
	require.Equal(t, "", list(empty))

	create("elsewhere", "anthropic", "apikey", map[string]any{"group_ids": []int64{other}})
	require.Equal(t, "", list(group), "accounts of another group are not counted")
	require.Equal(t, "anthropic.messages=1 anthropic.count_tokens=1", list(other))
	create("plain", "anthropic", "apikey", nil)
	create("relay", "relay", "relay_key", nil)
	require.Equal(t, "anthropic.messages=2 anthropic.count_tokens=2", list(group), "no conversion without a rule")

	disabled := create("converting", "anthropic", "apikey", map[string]any{"status": "disabled", "protocols": []string{},
		"model_protocols": []any{map[string]any{"models": []any{"gpt-5.6"}, "protocols": []any{"openai.chat"}}}})
	create("video", "video", "vkey", nil)
	// In the generation's endpoint order: the plugin platform, openai, anthropic.
	require.Equal(t, "aivideo.gen=1 openai.chat=1 anthropic.messages=2 anthropic.count_tokens=2", list(group))

	code, _ := e.do("DELETE", fmt.Sprintf("/accounts/%d", disabled), nil)
	require.Equal(t, 204, code)
	require.Equal(t, "aivideo.gen=1 anthropic.messages=2 anthropic.count_tokens=2", list(group))

	// An account whose plugin is not enabled serves nothing.
	e.reg.set(&fakeGen{})
	require.Equal(t, "", list(group))
}
