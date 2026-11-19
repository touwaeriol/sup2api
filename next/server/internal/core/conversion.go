package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

// Conversion loss policies (CONVERT-MATRIX §2.4, CONTRACTS §88): what a
// converted request may lose before an account refuses it. An account's
// policy overrides its group's; unset everywhere means allow.
const (
	LossPolicyAllow  = "allow"  // serve, recording the losses
	LossPolicySafe   = "safe"   // refuse when a tool definition, choice or call would be lost
	LossPolicyStrict = "strict" // refuse any loss
)

// ValidLossPolicy reports a known policy name.
func ValidLossPolicy(p string) bool {
	return p == LossPolicyAllow || p == LossPolicySafe || p == LossPolicyStrict
}

// EffectiveLossPolicy is the policy an account applies within a group: its
// own, else the group's, else allow.
func EffectiveLossPolicy(account, group string) string {
	switch {
	case account != "":
		return account
	case group != "":
		return group
	}
	return LossPolicyAllow
}

// LossPolicyInput is a conversion_loss_policy request field: absent (Set
// false), null (inherit, Value "") or a policy.
type LossPolicyInput struct {
	Set   bool
	Value string
}

func (in *LossPolicyInput) UnmarshalJSON(b []byte) error {
	in.Set = true
	if string(b) == "null" {
		in.Value = ""
		return nil
	}
	return json.Unmarshal(b, &in.Value)
}

// Validate returns the field error of an unknown policy, nil otherwise.
func (in LossPolicyInput) Validate(ctx context.Context, field string) []FieldError {
	if !in.Set || in.Value == "" || ValidLossPolicy(in.Value) {
		return nil
	}
	return []FieldError{{Field: field, Code: "invalid",
		Message: Text(ctx, "conversion loss policy must be allow, safe, strict or null", "协议转换丢失策略必须为 allow、safe、strict 或 null")}}
}

// Stored is the value written to the column: nil for inherit.
func (in LossPolicyInput) Stored() *string {
	if in.Value == "" {
		return nil
	}
	return &in.Value
}

// Conversion describes how the core serves a client protocol with an
// upstream protocol (ARCHITECTURE 6.6, CONVERT-MATRIX §2.1).
type Conversion struct {
	// Rank orders conversions, lower is better (the converter's grade).
	Rank int
	// Grade names the rank: good, fair or discouraged.
	Grade string
	// Hops are the protocols passed through, client first.
	Hops []string
}

// ConvertTarget is an endpoint of an account type that a converted request
// goes to.
type ConvertTarget struct {
	Platform   manifest.AccountPlatform // the type's entry for the endpoint's platform
	Definition manifest.Platform
	Endpoint   manifest.Endpoint
	Conversion
}

// BestConvertTarget returns the endpoint of b's available platforms that
// clientProtocol converts to best, among the endpoints of the type's subset
// accept admits that may receive a converted request at all (a proxy
// endpoint that neither is a WebSocket nor counts tokens): the lowest rank,
// then the fewest hops, then the first declared, platforms in the type's
// order and endpoints in the platform's (CONVERT-MATRIX §5.1). An endpoint
// of the client endpoint's own platform is a target only when the type
// lists the client endpoint in adapted (CONTRACTS §83.3).
func BestConvertTarget(g Generation, b AccountTypeBinding, conv ProtocolConverters, clientProtocol string,
	accept func(manifest.AccountPlatform, manifest.Endpoint) bool) (best ConvertTarget, ok bool) {
	if conv == nil {
		return best, false
	}
	client, _ := g.PlatformForProtocol(clientProtocol)
	for _, ap := range b.Type.Platforms {
		pb, found := g.Platform(ap.Platform)
		if !found || ap.Platform == client.Platform.ID && !ap.Adapts(&pb.Platform, clientProtocol) {
			continue
		}
		for _, ep := range pb.Platform.Endpoints {
			if !ap.SupportsEndpoint(ep.ID) || !convertible(ep) || !accept(ap, ep) {
				continue
			}
			c, found := conv.Conversion(clientProtocol, ep.Protocol)
			if found && (!ok || c.Rank < best.Rank || c.Rank == best.Rank && len(c.Hops) < len(best.Hops)) {
				best, ok = ConvertTarget{Platform: ap, Definition: pb.Platform, Endpoint: ep, Conversion: c}, true
			}
		}
	}
	return best, ok
}

// convertible reports an endpoint that may receive a converted request.
func convertible(ep manifest.Endpoint) bool {
	return (ep.Kind == "" || ep.Kind == manifest.EndpointKindProxy) && !strings.HasSuffix(ep.Protocol, ".count_tokens")
}

// ServesProtocol reports whether an account type serves a protocol, natively
// (an endpoint of its subset speaks it) or through a conversion.
func ServesProtocol(g Generation, b AccountTypeBinding, conv ProtocolConverters, protocol string) bool {
	for _, ap := range b.Type.Platforms {
		pb, found := g.Platform(ap.Platform)
		if !found {
			continue
		}
		for _, ep := range pb.Platform.Endpoints {
			if ep.Protocol == protocol && ap.SupportsEndpoint(ep.ID) {
				return true
			}
		}
	}
	_, ok := BestConvertTarget(g, b, conv, protocol, func(manifest.AccountPlatform, manifest.Endpoint) bool { return true })
	return ok
}

// CountTokensFamily is the generation protocol whose servers can estimate a
// count_tokens protocol locally (CONVERT-MATRIX §8), "" for any other
// protocol.
func CountTokensFamily(protocol string) string {
	switch protocol {
	case "anthropic.count_tokens":
		return "anthropic.messages"
	case "gemini.count_tokens":
		return "gemini.generate"
	}
	return ""
}

// ConversionTrace is usage_logs.conversion (CONVERT-MATRIX §7): how the
// request was converted for the account that served it, and which account
// types refused it. nil for a native request nothing was refused for.
type ConversionTrace struct {
	V     int    `json:"v"`
	Grade string `json:"grade"`
	// Hops are the protocols passed through, client first; absent when the
	// final route is native.
	Hops []string `json:"hops,omitempty"`
	// Policy is the effective loss policy of the serving account.
	Policy string `json:"policy,omitempty"`
	// Lost and ResponseLost list the request and response paths left out,
	// by loss category ("1:" prefixes a path of the second hop).
	Lost         map[string][]string `json:"lost,omitempty"`
	ResponseLost map[string][]string `json:"response_lost,omitempty"`
	// Omitted counts the paths beyond the cap.
	Omitted  int                   `json:"omitted"`
	Rejected []ConversionRejection `json:"rejected,omitempty"`
}

// ConversionRejection is an account type that could not serve the request:
// its policy refused a loss (Policy, Category, Path) or the request cannot
// be converted to its protocol at all (Error).
type ConversionRejection struct {
	Type     string `json:"type"` // plugin_key/type
	Policy   string `json:"policy,omitempty"`
	Category string `json:"category,omitempty"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
}
