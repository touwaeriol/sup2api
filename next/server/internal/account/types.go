package account

import (
	"encoding/json"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/platforms"
)

// AccountTypeView is one entry of GET /account-types (CONTRACTS §12, §13).
type AccountTypeView struct {
	Icon          string                 `json:"icon,omitempty"`
	PluginKey     string                 `json:"plugin_key"`
	PluginName    manifest.LocalizedText `json:"plugin_name"`
	PluginVersion string                 `json:"plugin_version"`
	// AssetBase prefixes form.page / form.component for iframe and native forms.
	AssetBase       string                 `json:"asset_base"`
	Trust           string                 `json:"trust"`
	Type            string                 `json:"type"`
	Label           manifest.LocalizedText `json:"label"`
	Description     manifest.LocalizedText `json:"description,omitempty"`
	CreationGroup   string                 `json:"creation_group,omitempty"`
	AuthMethodLabel manifest.LocalizedText `json:"auth_method_label,omitempty"`
	Form            FormView               `json:"form"`
	SensitiveFields []string               `json:"sensitive_fields"`
	// GuardedSettings are the settings fields restricted to official values
	// for callers without account:settings:custom (CONTRACTS §21.3); empty
	// when the type declares none.
	GuardedSettings []manifest.GuardedSetting `json:"guarded_settings"`
	// CustomUpstream reports that a caller without account:settings:custom
	// can still point an account of the type anywhere
	// (manifest.AccountType.CustomUpstream).
	CustomUpstream bool `json:"custom_upstream"`
	// DefaultModels / DefaultModelMapping are the plugin's suggestions the
	// console prefills into a new account (CONTRACTS §41); never null.
	DefaultModels       []string          `json:"default_models"`
	DefaultModelMapping map[string]string `json:"default_model_mapping"`
	// DefaultLimits are the plugin's suggested scheduling and limits of a new
	// account (CONTRACTS §68.5); absent when the type declares none.
	DefaultLimits    *DefaultLimitsView                       `json:"default_limits,omitempty"`
	CommonControls   map[string]manifest.AccountCommonControl `json:"common_controls,omitempty"`
	ExtensionsSchema json.RawMessage                          `json:"extensions_schema,omitempty"`
	// Platforms the account type declares, in declaration order.
	Platforms []TypePlatformView `json:"platforms"`
	// Endpoints lists the gateway endpoints this type can serve now.
	Endpoints []EndpointView `json:"endpoints"`
}

// DefaultLimitsView is manifest.AccountDefaultLimits with the names of the
// account fields; absent fields keep the console's defaults.
type DefaultLimitsView struct {
	Priority       *int              `json:"priority,omitempty"`
	Weight         *int              `json:"weight,omitempty"`
	MaxConcurrency *int              `json:"max_concurrency,omitempty"`
	RPMLimit       *int              `json:"rpm_limit,omitempty"`
	TPMLimit       *int64            `json:"tpm_limit,omitempty"`
	SPMLimit       *int              `json:"spm_limit,omitempty"`
	ModelLimits    []core.ModelLimit `json:"model_limits,omitempty"`
}

func defaultLimitsView(d *manifest.AccountDefaultLimits) *DefaultLimitsView {
	if d == nil {
		return nil
	}
	v := &DefaultLimitsView{Priority: d.Priority, Weight: d.Weight, MaxConcurrency: d.MaxConcurrency, RPMLimit: d.RPMLimit, TPMLimit: d.TPMLimit, SPMLimit: d.SPMLimit}
	for _, l := range d.ModelLimits {
		v.ModelLimits = append(v.ModelLimits, withSPMWindow(core.ModelLimit{Models: l.Models, MaxConcurrency: l.MaxConcurrency, RPMLimit: l.RPMLimit, TPMLimit: l.TPMLimit, SPMLimit: l.SPMLimit}))
	}
	return v
}

// TypePlatformView is a platform declared by an account type. Available is
// false when the platform is not in the current generation (its plugin is
// disabled or not installed); Label is then null.
type TypePlatformView struct {
	ID        string                 `json:"id"`
	Label     manifest.LocalizedText `json:"label"`
	Builtin   bool                   `json:"builtin"`
	Available bool                   `json:"available"`
}

// FormView describes how the console renders the credentials form.
type FormView struct {
	Mode      string `json:"mode"`
	Page      string `json:"page,omitempty"`
	Component string `json:"component,omitempty"`
}

// EndpointView is a gateway endpoint an account type can serve. Native is
// false when the core converts the endpoint protocol to a protocol of one of
// the type's platforms, or estimates a count_tokens request itself. Grade is
// native, the conversion's grade (good, fair, discouraged) or local; Via
// lists the protocols a conversion passes through, client first
// (CONVERT-MATRIX §5.3).
type EndpointView struct {
	Method   string   `json:"method"`
	Path     string   `json:"path"`
	Protocol string   `json:"protocol"`
	Platform string   `json:"platform"`
	Native   bool     `json:"native"`
	Grade    string   `json:"grade"`
	Via      []string `json:"via,omitempty"`
}

func typeView(g core.Generation, b core.AccountTypeBinding, conv core.ProtocolConverters) AccountTypeView {
	v := AccountTypeView{
		PluginKey:           b.Plugin.Key,
		PluginVersion:       b.Plugin.Version,
		AssetBase:           b.Plugin.AssetBase,
		Trust:               b.Plugin.Trust,
		Icon:                b.Type.Icon,
		Type:                b.Type.ID,
		Label:               b.Type.Label,
		Description:         b.Type.Description,
		CreationGroup:       b.Type.CreationGroup,
		AuthMethodLabel:     b.Type.AuthMethodLabel,
		Form:                FormView{Mode: b.Type.Form.Mode, Page: b.Type.Form.Page, Component: b.Type.Form.Component},
		SensitiveFields:     b.Type.SensitiveFields,
		GuardedSettings:     b.Type.GuardedSettings,
		CustomUpstream:      b.Type.CustomUpstream(),
		DefaultModels:       b.Type.DefaultModels,
		DefaultModelMapping: b.Type.DefaultModelMapping,
		DefaultLimits:       defaultLimitsView(b.Type.DefaultLimits),
		CommonControls:      controlsView(b.Type.CommonControls),
		ExtensionsSchema:    b.Type.ExtensionsSchema,
		Platforms:           typePlatforms(g, b),
		Endpoints:           servedEndpoints(g, b, conv),
	}
	if b.Plugin.Manifest != nil {
		v.PluginName = b.Plugin.Manifest.Name
		if v.Icon == "" {
			v.Icon = b.Plugin.Manifest.Icon
		}
	}
	if v.PluginName == nil {
		v.PluginName = manifest.LocalizedText{"en": b.Plugin.Key}
	}
	if v.SensitiveFields == nil {
		v.SensitiveFields = []string{}
	}
	if v.GuardedSettings == nil {
		v.GuardedSettings = []manifest.GuardedSetting{}
	}
	if v.DefaultModels == nil {
		v.DefaultModels = []string{}
	}
	if v.DefaultModelMapping == nil {
		v.DefaultModelMapping = map[string]string{}
	}
	return v
}

// typePlatforms lists the platforms declared by the account type.
func typePlatforms(g core.Generation, b core.AccountTypeBinding) []TypePlatformView {
	out := []TypePlatformView{}
	seen := map[string]bool{}
	for _, ap := range b.Type.Platforms {
		if seen[ap.Platform] {
			continue
		}
		seen[ap.Platform] = true
		v := TypePlatformView{ID: ap.Platform, Builtin: platforms.IsBuiltin(ap.Platform)}
		if pb, ok := g.Platform(ap.Platform); ok {
			v.Available, v.Builtin, v.Label = true, pb.Builtin, pb.Platform.Label
		}
		out = append(out, v)
	}
	return out
}

// servedEndpoints lists the endpoints the gateway routes to the account
// type: those of its platforms in its endpoint subset (native); the others
// it reaches through the best core conversion (core.BestConvertTarget) -
// to another platform of the type, or, for an endpoint of a supported
// platform left out of the subset, to an endpoint of that platform in the
// subset (CONTRACTS §83.3); and the count_tokens endpoints it serves
// neither way, estimated locally when it serves their family's generation
// protocol (CONVERT-MATRIX §8). The stream mode is not known here; the
// gateway may pick a sibling endpoint of the same grade per request.
func servedEndpoints(g core.Generation, b core.AccountTypeBinding, conv core.ProtocolConverters) []EndpointView {
	out := []EndpointView{}
	for _, e := range g.Endpoints() {
		v := EndpointView{Method: e.Endpoint.Method, Path: e.Endpoint.Path, Protocol: e.Endpoint.Protocol, Platform: e.Platform}
		ap, native := b.Supports(e.Platform)
		t, converted := core.BestConvertTarget(g, b, conv, v.Protocol, func(p manifest.AccountPlatform, _ manifest.Endpoint) bool {
			return (p.Platform == e.Platform) == native
		})
		switch family := core.CountTokensFamily(v.Protocol); {
		case native && ap.SupportsEndpoint(e.Endpoint.ID):
			v.Native, v.Grade = true, "native"
		case converted:
			v.Grade, v.Via = t.Grade, t.Hops
		case family != "" && core.ServesProtocol(g, b, conv, family):
			v.Grade = "local"
		default:
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Native != b.Native {
			return a.Native
		}
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return out
}

func (s *Service) listTypes(c *gin.Context) {
	out := []AccountTypeView{}
	if g := s.gen(); g != nil {
		for _, b := range g.AccountTypes() {
			out = append(out, typeView(g, b, s.d.Converters))
		}
	}
	// The generation lists a plugin's types in manifest order; keep it
	// within each plugin.
	sort.SliceStable(out, func(i, j int) bool { return out[i].PluginKey < out[j].PluginKey })
	httpapi.OK(c, out)
}

// PlatformView is one entry of GET /platforms (CONTRACTS §13).
type PlatformView struct {
	ID      string                 `json:"id"`
	Label   manifest.LocalizedText `json:"label"`
	Builtin bool                   `json:"builtin"`
	// PluginKey is the declaring plugin; null for built-in platforms.
	PluginKey *string `json:"plugin_key"`
	// PluginName is the declaring plugin's name; null for built-in platforms.
	PluginName   manifest.LocalizedText `json:"plugin_name"`
	Endpoints    []PlatformEndpointView `json:"endpoints"`
	AccountTypes []PlatformTypeView     `json:"account_types"`
}

// PlatformEndpointView is a gateway endpoint declared by a platform.
type PlatformEndpointView struct {
	ID       string `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Billing  string `json:"billing"`
}

// PlatformTypeView is a registered account type serving a platform natively.
type PlatformTypeView struct {
	PluginKey string                 `json:"plugin_key"`
	Type      string                 `json:"type"`
	Label     manifest.LocalizedText `json:"label"`
}

// platformViews lists the platforms of the generation: built-in ones first,
// then plugin platforms, each sorted by id.
func platformViews(g core.Generation) []PlatformView {
	out := []PlatformView{}
	if g == nil {
		return out
	}
	for _, pb := range g.Platforms() {
		v := PlatformView{ID: pb.Platform.ID, Label: pb.Platform.Label, Builtin: pb.Builtin,
			Endpoints: []PlatformEndpointView{}, AccountTypes: []PlatformTypeView{}}
		if v.Label == nil {
			v.Label = manifest.LocalizedText{"en": pb.Platform.ID}
		}
		if !pb.Builtin {
			key := pb.Plugin.Key
			v.PluginKey = &key
			if pb.Plugin.Manifest != nil {
				v.PluginName = pb.Plugin.Manifest.Name
			}
		}
		for _, e := range pb.Platform.Endpoints {
			v.Endpoints = append(v.Endpoints, PlatformEndpointView{ID: e.ID, Method: e.Method, Path: e.Path,
				Protocol: e.Protocol, Billing: e.Billing})
		}
		for _, b := range g.AccountTypesForPlatform(pb.Platform.ID) {
			v.AccountTypes = append(v.AccountTypes, PlatformTypeView{PluginKey: b.Plugin.Key, Type: b.Type.ID,
				Label: b.Type.Label})
		}
		// Manifest order within each plugin, as in GET /account-types.
		sort.SliceStable(v.AccountTypes, func(i, j int) bool { return v.AccountTypes[i].PluginKey < v.AccountTypes[j].PluginKey })
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Builtin != b.Builtin {
			return a.Builtin
		}
		return a.ID < b.ID
	})
	return out
}

func (s *Service) listPlatforms(c *gin.Context) {
	httpapi.OK(c, platformViews(s.gen()))
}

// MyPlatformView is one entry of GET /me/platforms (CONTRACTS §14.1): a
// platform available now, without account types or plugin details, so any
// signed-in user can preview the endpoints a key will reach.
type MyPlatformView struct {
	ID        string                 `json:"id"`
	Label     manifest.LocalizedText `json:"label"`
	Builtin   bool                   `json:"builtin"`
	Endpoints []PlatformEndpointView `json:"endpoints"`
}

// myPlatformViews lists the platforms of the generation in the order of
// platformViews (built-in first, then by id).
func myPlatformViews(g core.Generation) []MyPlatformView {
	all := platformViews(g)
	out := make([]MyPlatformView, 0, len(all))
	for _, v := range all {
		out = append(out, MyPlatformView{ID: v.ID, Label: v.Label, Builtin: v.Builtin, Endpoints: v.Endpoints})
	}
	return out
}

func (s *Service) listMyPlatforms(c *gin.Context) {
	httpapi.OK(c, myPlatformViews(s.gen()))
}

// typeLabel returns the label of an account type, or nil when the type is not
// registered in the current generation.
func (s *Service) typeLabel(pluginKey, typ string) manifest.LocalizedText {
	if b, ok := s.accountType(pluginKey, typ); ok {
		return b.Type.Label
	}
	return nil
}
