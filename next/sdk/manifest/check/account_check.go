package check

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/platforms"
)

// scopeOwn is the only accepted scope of accounts.auto_state: the plugin acts
// on the accounts of its own account types (ARCHITECTURE 6.6).
var scopeOwn = map[string]any{"types": "own"}

// accountExtensions validates accountTypes[].extensionsSchema: a bounded,
// closed object schema whose properties do not shadow core, credential or
// settings fields.
func (v *validator) accountExtensions(field string, at manifest.AccountType) {
	if len(at.ExtensionsSchema) == 0 {
		return
	}
	var schema map[string]any
	if len(at.ExtensionsSchema) > 65536 || json.Unmarshal(at.ExtensionsSchema, &schema) != nil || schema["type"] != "object" || schema["additionalProperties"] != false {
		v.add(field+".extensionsSchema", "invalid", "extensions schema must be a bounded, closed object schema")
		return
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) > 64 {
		v.add(field+".extensionsSchema.properties", "invalid", "at most 64 extension fields are allowed")
		return
	}
	reserved := []string{"id", "name", "plugin_key", "type", "status", "schedulable", "auto_disable", "priority", "weight", "max_concurrency", "rpm_limit", "tpm_limit", "spm_limit", "proxy_id", "proxy_url", "models", "model_mapping", "credentials", "created_by", "owner_id", "group_ids", "status_reason", "auto_disabled_until", "created_at", "updated_at"}
	for name := range properties {
		if slices.Contains(reserved, name) || slices.Contains(at.SensitiveFields, name) || slices.Contains(at.SettingsFields, name) {
			v.add(field+".extensionsSchema.properties."+name, "conflict", "extensions cannot duplicate core, credential or existing settings fields")
		}
	}
	pending := []any{schema}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		switch node := node.(type) {
		case map[string]any:
			for key, value := range node {
				if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" {
					ref, ok := value.(string)
					if !ok || !strings.HasPrefix(ref, "#") {
						v.add(field+".extensionsSchema", "invalid", "external schema references are not allowed")
					}
				}
				pending = append(pending, value)
			}
		case []any:
			pending = append(pending, node...)
		}
	}
}

// accountTypes validates the top-level accountTypes (ARCHITECTURE 6.6).
func (v *validator) accountTypes() {
	ats := v.m.AccountTypes
	if len(ats) == 0 {
		return
	}
	v.needCap("accountTypes", manifest.CapPlatformAdapter)
	v.needPerm("accountTypes", "platform.register", "account types")
	ownPlatforms := map[string]*manifest.Platform{}
	for i := range v.m.Platforms {
		ownPlatforms[v.m.Platforms[i].ID] = &v.m.Platforms[i]
	}
	ids := map[string]bool{}
	for i, at := range ats {
		f := fmt.Sprintf("accountTypes[%d]", i)
		if !accountTypeIDRe.MatchString(at.ID) {
			v.add(f+".id", "invalid_format", "account type id %q must match %s", at.ID, accountTypeIDRe.String())
		} else if ids[at.ID] {
			v.add(f+".id", "duplicate", "account type %q declared twice", at.ID)
		}
		ids[at.ID] = true
		if strings.TrimSpace(at.Label["en"]) == "" {
			v.add(f+".label", "required", "label.en is required")
		}
		if at.Icon != "" && !strings.HasPrefix(at.Icon, "text:") {
			v.needFile(f+".icon", at.Icon)
		}
		v.form(f+".form", at.Form, false)
		v.guardedSettings(f, at)
		v.upstreamFields(f, at)
		v.accountExtensions(f, at)
		v.defaultModels(f, at)
		v.defaultLimits(f+".defaultLimits", at.DefaultLimits)
		v.commonControls(f, at)
		v.quota(f, at.Quota)
		v.balance(f, at.Balance)
		v.refresh(f, at)
		if len(at.Platforms) == 0 {
			v.add(f+".platforms", "required", "at least one platform is required")
		}
		seen := map[string]bool{}
		for j, ap := range at.Platforms {
			v.accountPlatform(fmt.Sprintf("%s.platforms[%d]", f, j), ap, seen, ownPlatforms)
		}
	}
}

// accountPlatform validates one accountTypes[].platforms entry: the platform
// reference, its endpoint subset and the per-protocol usage overrides.
// seen collects the platform ids already used by this account type.
func (v *validator) accountPlatform(pf string, ap manifest.AccountPlatform, seen map[string]bool, ownPlatforms map[string]*manifest.Platform) {
	switch {
	case ap.Platform == "":
		v.add(pf+".platform", "required", "platform is required")
	case !keyRe.MatchString(ap.Platform):
		v.add(pf+".platform", "invalid_format", "platform id %q must match %s", ap.Platform, keyRe.String())
	case seen[ap.Platform]:
		v.add(pf+".platform", "duplicate", "platform %q listed twice", ap.Platform)
	}
	seen[ap.Platform] = true
	own := ownPlatforms[ap.Platform]
	v.accountEndpoints(pf, ap, own)
	v.foreignFacts(pf, ap, own)
	v.accountStreamOnly(pf, ap, own)
	v.accountAdapted(pf, ap, own)
	v.accountBoundHeaders(pf, ap, own)
	for _, proto := range sortedKeys(ap.Usage) {
		uf := pf + ".usage." + proto
		if own != nil {
			if !slices.Contains(own.Protocols(), proto) {
				v.add(uf, "unknown_protocol", "protocol %q is not a protocol of platform %q", proto, ap.Platform)
			}
		} else if !strings.HasPrefix(proto, ap.Platform+".") {
			v.add(uf, "unknown_protocol", "protocol %q does not belong to platform %q", proto, ap.Platform)
		}
		v.usageRules(uf, ap.Usage[proto], ownerAccountType, protocolPluginUsage(own, proto))
	}
}

func (v *validator) accountEndpoints(field string, ap manifest.AccountPlatform, own *manifest.Platform) {
	if ap.Endpoints == nil {
		return
	}
	if len(ap.Endpoints) == 0 {
		v.add(field+".endpoints", "required", "at least one endpoint is required when endpoints is specified")
	}
	p := v.referencedPlatform(ap.Platform, own)
	if p == nil && !v.opt.Tooling {
		v.add(field+".endpoints", "unknown_platform", "platform %q must be installed to validate endpoint references", ap.Platform)
	}
	seen := map[string]bool{}
	for i, id := range ap.Endpoints {
		f := fmt.Sprintf("%s.endpoints[%d]", field, i)
		switch {
		case strings.TrimSpace(id) == "":
			v.add(f, "required", "endpoint id is required")
		case seen[id]:
			v.add(f, "duplicate", "endpoint %q listed twice", id)
		case p != nil && !slices.ContainsFunc(p.Endpoints, func(e manifest.Endpoint) bool { return e.ID == id }):
			v.add(f, "unknown_endpoint", "endpoint %q does not belong to platform %q", id, ap.Platform)
		}
		seen[id] = true
	}
}

// foreignFacts rejects an account type serving, on another plugin's
// platform, an endpoint whose usage or task facts that plugin states
// (manifest.AccountPlatform.ServesPluginFacts). A built-in platform is no
// plugin's: the registry binds it to every type, and so does the check.
func (v *validator) foreignFacts(pf string, ap manifest.AccountPlatform, own *manifest.Platform) {
	if own != nil || platforms.IsBuiltin(ap.Platform) {
		return
	}
	if p := v.referencedPlatform(ap.Platform, nil); p != nil && ap.ServesPluginFacts(*p) {
		v.add(pf+".platform", "foreign_facts", "platform %q has endpoints whose usage or task the plugin declaring it reads; only that plugin's account types may serve them", ap.Platform)
	}
}

// referencedPlatform resolves the platform an account type entry names: a
// built-in platform, another installed plugin's platform or this plugin's
// own (own may be nil). nil when it is none of these.
func (v *validator) referencedPlatform(id string, own *manifest.Platform) *manifest.Platform {
	p := own
	for _, builtin := range platforms.Builtin() {
		if builtin.ID == id {
			p = &builtin
		}
	}
	for _, other := range v.opt.OtherPlatforms {
		if other.ID == id {
			p = &manifest.Platform{ID: other.ID, Endpoints: other.Endpoints, PassHeaders: other.PassHeaders}
		}
	}
	return p
}

// accountStreamOnly validates accountTypes[].platforms[].streamOnly
// (CONTRACTS §83): every entry names, once, the protocol of a proxy endpoint
// of the platform that the account type serves, answers with SSE
// (response.stream "sse") and carries the client's choice in
// request.streamPath - the path the host sets to true upstream. Offline
// packaging that cannot resolve an external platform only checks that the
// protocol belongs to it; the install check resolves it.
func (v *validator) accountStreamOnly(field string, ap manifest.AccountPlatform, own *manifest.Platform) {
	if ap.StreamOnly == nil {
		return
	}
	field += ".streamOnly"
	if len(ap.StreamOnly) == 0 {
		v.add(field, "required", "at least one protocol is required when streamOnly is specified")
		return
	}
	p := v.referencedPlatform(ap.Platform, own)
	if p == nil && !v.opt.Tooling {
		v.add(field, "unknown_platform", "platform %q must be installed to validate streamOnly protocols", ap.Platform)
		return
	}
	seen := map[string]bool{}
	for i, proto := range ap.StreamOnly {
		f := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case strings.TrimSpace(proto) == "":
			v.add(f, "required", "protocol is required")
			continue
		case seen[proto]:
			v.add(f, "duplicate", "protocol %q listed twice", proto)
			continue
		}
		seen[proto] = true
		if p == nil {
			if !strings.HasPrefix(proto, ap.Platform+".") {
				v.add(f, "unknown_protocol", "protocol %q does not belong to platform %q", proto, ap.Platform)
			}
			continue
		}
		known, streams := false, false
		for _, e := range p.Endpoints {
			if e.Protocol != proto || !ap.SupportsEndpoint(e.ID) {
				continue
			}
			known = true
			if e.Kind == manifest.EndpointKindProxy && e.Response.Stream == "sse" && e.Request.StreamPath != "" {
				streams = true
			}
		}
		switch {
		case !known:
			v.add(f, "unknown_protocol", "protocol %q is not served by an endpoint of platform %q this account type supports", proto, ap.Platform)
		case !streams:
			v.add(f, "not_streamable", "protocol %q needs a proxy endpoint with response.stream \"sse\" and request.streamPath", proto)
		}
	}
}

// accountAdapted validates accountTypes[].platforms[].adapted (CONTRACTS
// §121.1): each key is an endpoint of the platform the account type's
// subset leaves out that the core may convert at all (neither a WebSocket
// nor a task endpoint), and a model, when given, is a complete model id.
// Offline packaging that cannot resolve an external platform checks the
// models only; the install check resolves it. Whether the core converts the
// endpoint to one of the subset is the registry's check.
func (v *validator) accountAdapted(field string, ap manifest.AccountPlatform, own *manifest.Platform) {
	field += ".adapted"
	p := v.referencedPlatform(ap.Platform, own)
	if p == nil && !v.opt.Tooling && len(ap.Adapted) > 0 {
		v.add(field, "unknown_platform", "platform %q must be installed to validate adapted endpoints", ap.Platform)
	}
	for _, id := range sortedKeys(ap.Adapted) {
		f := field + "." + id
		if m := ap.Adapted[id].Model; m != "" && !manifest.ValidModelID(m) {
			v.add(f+".model", "invalid", "%q is not a complete model id", m)
		}
		if p == nil {
			continue
		}
		i := slices.IndexFunc(p.Endpoints, func(e manifest.Endpoint) bool { return e.ID == id })
		switch {
		case i < 0:
			v.add(f, "unknown_endpoint", "endpoint %q does not belong to platform %q", id, ap.Platform)
		case ap.SupportsEndpoint(id):
			v.add(f, "native", "endpoint %q is in the account type's endpoints; only an endpoint left out is adapted", id)
		case p.Endpoints[i].WebSocket() || p.Endpoints[i].Task != nil:
			v.add(f, "not_convertible", "endpoint %q is a WebSocket or task endpoint, which the core never converts", id)
		}
	}
}

// guardedSettings validates accountTypes[].guardedSettings (CONTRACTS §21.3):
// each field is a distinct settings field of the same account type, its
// allowed list holds at least one absolute http(s) URL and its versions are
// distinct segments that start and do not end with "/".
func (v *validator) guardedSettings(f string, at manifest.AccountType) {
	seen := map[string]bool{}
	for i, gs := range at.GuardedSettings {
		gf := fmt.Sprintf("%s.guardedSettings[%d]", f, i)
		switch {
		case gs.Field == "":
			v.add(gf+".field", "required", "field is required")
		case !slices.Contains(at.SettingsFields, gs.Field):
			v.add(gf+".field", "unknown_field", "field %q is not in settingsFields of account type %q", gs.Field, at.ID)
		case seen[gs.Field]:
			v.add(gf+".field", "duplicate", "field %q is guarded twice", gs.Field)
		}
		seen[gs.Field] = true
		if len(gs.Allowed) == 0 {
			v.add(gf+".allowed", "required", "allowed must list at least one URL")
		}
		for j, raw := range gs.Allowed {
			if !isAbsoluteHTTPURL(raw) {
				v.add(fmt.Sprintf("%s.allowed[%d]", gf, j), "invalid_url", "%q is not an absolute http(s) URL", raw)
			}
		}
		for j, ver := range gs.Versions {
			vf := fmt.Sprintf("%s.versions[%d]", gf, j)
			switch {
			case ver == "":
				v.add(vf, "required", "version segment is required")
			case !strings.HasPrefix(ver, "/") || strings.HasSuffix(ver, "/"):
				v.add(vf, "invalid", "version segment %q must start and not end with \"/\"", ver)
			case slices.Contains(gs.Versions[:j], ver):
				v.add(vf, "duplicate", "version segment %q is listed twice", ver)
			}
		}
	}
}

// upstreamFields validates accountTypes[].upstreamFields: required ([] for
// a type without a caller-set address), non-empty, distinct keys.
func (v *validator) upstreamFields(f string, at manifest.AccountType) {
	if at.UpstreamFields == nil {
		v.add(f+".upstreamFields", "required", "upstreamFields is required: the fields that say where requests go, [] when there are none")
	}
	seen := map[string]bool{}
	for i, name := range at.UpstreamFields {
		uf := fmt.Sprintf("%s.upstreamFields[%d]", f, i)
		switch {
		case strings.TrimSpace(name) == "":
			v.add(uf, "required", "field name is required")
		case seen[name]:
			v.add(uf, "duplicate", "field %q is listed twice", name)
		}
		seen[name] = true
	}
}

// defaultLimits validates accountTypes[].defaultLimits (CONTRACTS §68.5)
// with the ranges of the account fields they prefill.
func (v *validator) defaultLimits(f string, d *manifest.AccountDefaultLimits) {
	if d == nil {
		return
	}
	for _, x := range []struct {
		control string
		value   *int64
	}{
		{"priority", widen(d.Priority)}, {"weight", widen(d.Weight)}, {"max_concurrency", widen(d.MaxConcurrency)},
		{"rpm_limit", widen(d.RPMLimit)}, {"tpm_limit", d.TPMLimit}, {"spm_limit", widen(d.SPMLimit)},
	} {
		if x.value != nil {
			v.controlRange(f+"."+camelLimitNames[x.control], x.control, *x.value)
		}
	}
	lists, _ := d.ControlDefaults()["model_limits"].([]manifest.ModelLimitValue)
	v.modelLimitLists(f+".modelLimits", lists, camelLimitNames)
}

// defaultModels validates accountTypes[].defaultModels and
// defaultModelMapping (CONTRACTS §41) with the rules the core applies to the
// account fields they prefill: complete model ids, no duplicates, at most
// manifest.MaxDefaultModels each. A mapping key missing from a non-empty
// defaultModels is rejected too: the account would never be scheduled for it.
func (v *validator) defaultModels(f string, at manifest.AccountType) {
	if len(at.DefaultModels) > manifest.MaxDefaultModels {
		v.add(f+".defaultModels", "too_many", "at most %d default models", manifest.MaxDefaultModels)
	}
	seen := map[string]bool{}
	for i, m := range at.DefaultModels {
		mf := fmt.Sprintf("%s.defaultModels[%d]", f, i)
		switch {
		case !manifest.ValidModelID(m):
			v.add(mf, "invalid", "%q is not a complete model id", m)
		case seen[m]:
			v.add(mf, "duplicate", "model %q listed twice", m)
		}
		seen[m] = true
	}
	if len(at.DefaultModelMapping) > manifest.MaxDefaultModels {
		v.add(f+".defaultModelMapping", "too_many", "at most %d mapping entries", manifest.MaxDefaultModels)
	}
	for _, from := range sortedKeys(at.DefaultModelMapping) {
		mf := f + ".defaultModelMapping." + from
		switch to := at.DefaultModelMapping[from]; {
		case !manifest.ValidModelID(from), !manifest.ValidModelID(to):
			v.add(mf, "invalid", "%q -> %q: both sides must be complete model ids", from, to)
		case len(at.DefaultModels) > 0 && !seen[from]:
			v.add(mf, "not_in_models", "mapped model %q is not in defaultModels", from)
		}
	}
}
