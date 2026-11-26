package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Masterminds/semver/v3"
	"github.com/robfig/cron/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
)

var (
	keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)
	// pluginKeyRe is keyRe without "__" or a trailing "_", so that no
	// plugin's table prefix (manifest.TablePrefix) starts another's.
	pluginKeyRe     = regexp.MustCompile(`^[a-z](_?[a-z0-9]){1,29}$`)
	userPermKeyRe   = regexp.MustCompile(`^[a-z0-9_]+(:[a-z0-9_]+)+$`)
	idRe            = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	accountTypeIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,49}$`)
	protocolRe      = regexp.MustCompile(`^[a-z0-9_.-]+$`)
	// coreKeys name the core itself where a plugin key is recorded (the
	// source of a notification, an audit actor): no plugin may take them.
	coreKeys       = map[string]bool{"core": true, "system": true}
	allowedMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	// errorFormats are the upstream error renderings the gateway implements
	// (server/internal/gateway/errors.go writeError); an endpoint declaring
	// anything else would silently get the host's own "plain" envelope.
	errorFormats = []string{"anthropic", "openai", "gemini", "plain"}
)

// KnownCapabilities lists the capability ids host 0.1 understands.
var KnownCapabilities = map[string]bool{
	manifest.CapPlatformAdapter:   true,
	manifest.CapPlatformTasks:     true,
	manifest.CapPlatformMonitor:   true,
	manifest.CapPlatformWebSocket: true,
	manifest.CapGatewayHook:       true,
	manifest.CapGatewayObserve:    true,
	manifest.CapAppJobs:           true,
	manifest.CapAppEvents:         true,
	manifest.CapHTTPRoutes:        true,
	manifest.CapMigrationData:     true,
	manifest.CapSchedulerRank:     true,
	manifest.CapAppBroadcast:      true,
	manifest.CapNotifyChannel:     true,
	manifest.CapPaymentGateway:    true,
}

// Resource caps besides the configurable memory cap.
const (
	MaxCPU          = 16.0
	MaxThreads      = 4096
	MaxOpenFiles    = 65536
	MaxHookTimeout  = 30000 // ms; CONTRACTS §20.1
	MinRankTimeout  = 50    // ms; scheduler.rank runs on the hot path
	MaxRankTimeout  = 1000  // ms
	MaxPromptBytes  = 1 << 20
	RequiredArchAMD = "linux-amd64"
	RequiredArchARM = "linux-arm64"
)

// EndpointOwner is a gateway endpoint of a platform declared by another
// installed plugin.
type EndpointOwner struct {
	PluginID string // manifest.PluginID of the owner
	Platform string
	Method   string
	Path     string
}

// PlatformOwner is a platform declared by another installed plugin.
type PlatformOwner struct {
	PluginID  string // manifest.PluginID of the owner
	ID        string
	Endpoints []manifest.Endpoint
}

// ChannelOwner is a notification channel declared by another installed
// plugin.
type ChannelOwner struct {
	PluginID string // manifest.PluginID of the owner
	ID       string
}

// ValidateOptions carries host facts needed by Validate.
type ValidateOptions struct {
	// HostVersion is the server version; a pre-release suffix is ignored
	// when matching hostCompat ("0.1.0-dev" matches ">=0.1.0 <0.2.0").
	HostVersion string
	// DevMode requires a binary for GOOS/GOARCH instead of linux-amd64 and
	// linux-arm64.
	DevMode bool
	GOOS    string // default runtime.GOOS
	GOARCH  string // default runtime.GOARCH
	// MaxMemoryMB is the global per-plugin memory cap (0 = no cap).
	MaxMemoryMB int
	// OtherEndpoints are the platform endpoints of all other installed
	// plugins (built-in platform endpoints are checked by Validate itself).
	OtherEndpoints []EndpointOwner
	// OtherPlatforms are the platforms declared by all other installed plugins.
	OtherPlatforms []PlatformOwner
	// OtherChannels are the notification channels declared by all other
	// installed plugins.
	OtherChannels []ChannelOwner
	// Tooling relaxes the three checks a manifest cannot satisfy outside an
	// installing host: hostCompat is parsed but not matched against
	// HostVersion (which is unknown), and the two package entries produced by
	// the build rather than written by the author - the runtime binaries and
	// the native UI entry - are not required. Plugin tooling (the packaging
	// CLI, a plugin's own tests) sets it; the host never does, so install-time
	// validation is unchanged.
	Tooling bool
}

// OthersFromManifests collects the platforms and endpoints declared by the
// given manifests of other installed plugins (for ValidateOptions).
func OthersFromManifests(ms []*manifest.Manifest) ([]PlatformOwner, []EndpointOwner) {
	var pfs []PlatformOwner
	var eps []EndpointOwner
	for _, m := range ms {
		for _, p := range m.Platforms {
			pfs = append(pfs, PlatformOwner{PluginID: m.ID(), ID: p.ID, Endpoints: p.Endpoints})
			for _, e := range p.Endpoints {
				eps = append(eps, EndpointOwner{PluginID: m.ID(), Platform: p.ID, Method: e.Method, Path: e.Path})
			}
		}
	}
	return pfs, eps
}

// ChannelsFromManifests collects the notification channels declared by the
// given manifests of other installed plugins (for ValidateOptions).
func ChannelsFromManifests(ms []*manifest.Manifest) []ChannelOwner {
	var out []ChannelOwner
	for _, m := range ms {
		if m.Notifications == nil {
			continue
		}
		for _, c := range m.Notifications.Channels {
			out = append(out, ChannelOwner{PluginID: m.ID(), ID: c.ID})
		}
	}
	return out
}

// Conflicts reports only the exclusive resources of m that other plugins or
// the core already hold: platform ids, gateway endpoints and notification
// channel ids of other plugins (platform_conflict, endpoint_conflict,
// channel_conflict), built-in platform ids
// (builtin_platform) and paths the core reserves (invalid_path). The host
// runs it again whenever a version starts to hold or use them - approval,
// enable, upgrade - because versions awaiting approval hold nothing and a
// newer core may have taken a path or platform since the upload.
func Conflicts(m *manifest.Manifest, others []PlatformOwner, endpoints []EndpointOwner, channels []ChannelOwner) []FieldError {
	v := &validator{m: m, opt: ValidateOptions{OtherPlatforms: others, OtherEndpoints: endpoints, OtherChannels: channels, Tooling: true},
		perms: map[string]*manifest.HostPermission{}}
	v.platforms()
	v.notifications()
	var out []FieldError
	for _, e := range v.errs {
		switch e.Code {
		case "platform_conflict", "endpoint_conflict", "channel_conflict", "builtin_platform", "invalid_path":
			out = append(out, e)
		}
	}
	return out
}

type validator struct {
	m     *manifest.Manifest
	files map[string][]byte
	opt   ValidateOptions
	errs  []FieldError
	perms map[string]*manifest.HostPermission
	// standalone marks a run without a manifest around the platform
	// (CheckPlatform): manifest-level rules - the capabilities and host
	// permissions an endpoint implies - cannot be judged and are skipped.
	//
	// Before adding a rule to endpoint(), usageRules() or stickyRules(), ask:
	// does it read v.m? If it does, it silently passes on the two paths that
	// have no manifest - CheckPlatform (used for the core's built-in
	// platforms, sdk/platforms/*.json) and any plugin calling CheckPlatform on
	// a platform it is about to declare - because v.m is the zero
	// manifest.Manifest{} there: no capabilities, no host permissions, no
	// platforms. A rule of the shape "this endpoint needs capability X" then
	// reads "no capabilities declared" and fires on every built-in endpoint,
	// so it has to be guarded with !v.standalone (as request.modelSource is)
	// or the built-in path has to be given a real manifest to read.
	standalone bool
}

func (v *validator) add(field, code, format string, args ...any) {
	v.errs = append(v.errs, FieldError{Field: field, Code: code, Message: fmt.Sprintf(format, args...)})
}

// needPerm records an error unless the host permission is requested.
func (v *validator) needPerm(field, perm, why string) *manifest.HostPermission {
	p, ok := v.perms[perm]
	if !ok {
		v.add(field, "missing_host_permission", "%s requires host permission %q", why, perm)
		return nil
	}
	return p
}

func (v *validator) needFile(field, p string) {
	if p == "" {
		v.add(field, "required", "file path is required")
		return
	}
	if _, ok := v.files[p]; !ok {
		v.add(field, "file_missing", "file %q not found in package", p)
	}
}

func (v *validator) needCap(field, capID string) {
	for _, c := range v.m.Capabilities {
		if c.ID == capID {
			return
		}
	}
	v.add(field, "missing_capability", "capability %q must be declared", capID)
}

// sections are Validate's checks in the order they report. hostPermissions
// comes before the sections that ask for a permission (v.perms).
var sections = []func(*validator){
	(*validator).basics,
	(*validator).hostPermissions,
	(*validator).capabilities,
	(*validator).platforms,
	func(v *validator) { v.taskPairs(v.m.Platforms) },
	(*validator).accountTypes,
	(*validator).pricing,
	(*validator).hooks,
	(*validator).scheduler,
	(*validator).observations,
	(*validator).notifications,
	(*validator).payments,
	(*validator).events,
	(*validator).jobs,
	(*validator).database,
	(*validator).userPermissions,
	(*validator).retiredUserPermissionFields,
	(*validator).retiredContractFields,
	(*validator).routes,
	(*validator).ui,
	(*validator).resources,
}

// Validate checks a manifest against the package contents and host facts.
// All problems are reported together as a *ValidationError.
func Validate(m *manifest.Manifest, files map[string][]byte, opt ValidateOptions) error {
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if opt.GOARCH == "" {
		opt.GOARCH = runtime.GOARCH
	}
	v := &validator{m: m, files: files, opt: opt, perms: map[string]*manifest.HostPermission{}}
	for _, section := range sections {
		section(v)
	}
	if len(v.errs) > 0 {
		return &ValidationError{Fields: v.errs}
	}
	return nil
}

// CheckPlatform applies the endpoint, usage and sticky rules Validate applies
// to a plugin's platforms to a single platform definition, with no manifest
// around it. It holds the core's own built-in platforms to the same contract
// and lets a plugin check a platform it is about to declare.
//
// The rules that need the rest of the manifest are skipped, not guessed: an
// endpoint with request.modelSource "plugin" is not checked for the
// platform.adapter.v1 capability here (Validate does that).
//
// The trap this creates is worth spelling out, because it is silent: the
// validator runs with an empty manifest.Manifest{}, so any rule inside
// endpoint() (or usageRules / stickyRules) that consults v.m - capabilities,
// host permissions, other platforms, the plugin key - reads nothing at all on
// this path. That is not an error the caller sees; it is either a check that
// never fires or one that always fires. Every new rule added to those methods
// must therefore be asked "does it read v.m?", and if it does, guarded with
// v.standalone (see request.modelSource in endpoint()) - remembering that the
// core's own built-in platforms come through here, so a rule that wrongly
// fires here rejects the platforms the core ships with.
func CheckPlatform(p manifest.Platform) []FieldError {
	v := &validator{m: &manifest.Manifest{}, perms: map[string]*manifest.HostPermission{}, standalone: true}
	for i, e := range p.Endpoints {
		v.endpoint(fmt.Sprintf("endpoints[%d]", i), p, e)
	}
	v.taskPairs([]manifest.Platform{p})
	v.usageRules("usage", p.Usage, ownerPlatform, anyPluginUsage(p))
	v.stickyRules("stickyRules", p.StickyRules)
	return v.errs
}

// anyPluginUsage reports whether any endpoint of p reads its usage from a
// plugin. The platform's own usage rules are the defaults of every endpoint,
// so a fact declared there may be supplied by a plugin (and have no path) as
// soon as one endpoint asks for it.
func anyPluginUsage(p manifest.Platform) bool {
	for _, e := range p.Endpoints {
		if e.PluginUsage() || e.TaskSubmit() {
			return true
		}
	}
	return false
}

// protocolPluginUsage reports whether the endpoints of p speaking protocol
// read their usage from a plugin. p is nil when the account type overrides
// the usage of a platform this manifest does not declare (another plugin's,
// or a built-in one): the answer is then unknowable here, and the lenient one
// is returned - a fact without a path is accepted, because the platform's own
// validation is where a source is decided and a false rejection here would
// stop a legitimate override of a plugin-sourced endpoint.
func protocolPluginUsage(p *manifest.Platform, protocol string) bool {
	if p == nil {
		return true
	}
	for _, e := range p.Endpoints {
		if e.Protocol == protocol && e.PluginUsage() {
			return true
		}
	}
	return false
}

// ValidatePluginKey reports why key cannot be a plugin's key (nil when it
// can): the rule manifest.key is held to, for tools that name plugins by key.
func ValidatePluginKey(key string) error {
	if code, msg := pluginKeyProblem(key); code != "" {
		return errors.New(msg)
	}
	return nil
}

// pluginKeyProblem is the error code and message of an invalid plugin key.
func pluginKeyProblem(key string) (code, msg string) {
	if !pluginKeyRe.MatchString(key) || len(key) > 30 {
		return "invalid_format", fmt.Sprintf("key must match %s and be at most 30 bytes", pluginKeyRe.String())
	}
	if coreKeys[key] {
		return "reserved", fmt.Sprintf("key %q is reserved for the core", key)
	}
	return "", ""
}

func (v *validator) basics() {
	m := v.m
	if m.APIVersion != manifest.APIVersion {
		v.add("apiVersion", "unsupported", "apiVersion must be %d", manifest.APIVersion)
	}
	v.identity()
	if strings.TrimSpace(m.Name["en"]) == "" {
		v.add("name", "required", "name.en is required")
	}
	if _, err := semver.StrictNewVersion(m.Version); err != nil {
		v.add("version", "invalid_semver", "version %q is not a valid semver (x.y.z)", m.Version)
	}

	switch {
	case m.Runtime == retiredRuntimeNone:
		v.add("runtime", CodeOutdatedContract, "runtime %q is no longer supported: every plugin has a process (runtime %q)", m.Runtime, manifest.RuntimeGRPC)
	case m.Runtime != manifest.RuntimeGRPC:
		v.add("runtime", "unsupported", "runtime must be %q", manifest.RuntimeGRPC)
	case m.Entry.GRPC == nil || m.Entry.GRPC.Binaries == "":
		v.add("entry.grpc.binaries", "required", "entry.grpc.binaries is required")
	case !v.opt.Tooling:
		tpl := m.Entry.GRPC.Binaries
		targets := [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}
		if v.opt.DevMode {
			targets = [][2]string{{v.opt.GOOS, v.opt.GOARCH}}
		}
		for _, t := range targets {
			p := BinaryPath(tpl, t[0], t[1])
			b, ok := v.files[p]
			if !ok && t[0] == "windows" {
				b, ok = v.files[p+".exe"]
			}
			if !ok || len(b) == 0 {
				v.add("entry.grpc.binaries", "binary_missing", "binary for %s-%s not found at %q", t[0], t[1], p)
			}
		}
	}
	switch {
	case m.HostCompat == "":
		v.add("hostCompat", "required", "hostCompat is required")
	case v.opt.Tooling:
		// No host to match against: the constraint only has to parse.
		if _, err := semver.NewConstraint(m.HostCompat); err != nil {
			v.add("hostCompat", "invalid_constraint", "hostCompat: %v", err)
		}
	default:
		if ok, err := HostCompatible(m.HostCompat, v.opt.HostVersion); err != nil {
			v.add("hostCompat", "invalid_constraint", "hostCompat: %v", err)
		} else if !ok {
			v.add("hostCompat", "incompatible", "host version %s does not satisfy %q", v.opt.HostVersion, m.HostCompat)
		}
	}
	if m.Icon != "" && !strings.HasPrefix(m.Icon, "text:") {
		v.needFile("icon", m.Icon)
	}
}

// BinaryPath expands the {os}/{arch} template.
func BinaryPath(tpl, goos, goarch string) string {
	return strings.NewReplacer("{os}", goos, "{arch}", goarch).Replace(tpl)
}

// HostCompatible matches a semver range against the host version, ignoring
// any pre-release/build suffix of the host version.
func HostCompatible(constraint, hostVersion string) (bool, error) {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, err
	}
	hv, err := semver.NewVersion(hostVersion)
	if err != nil {
		return false, fmt.Errorf("invalid host version %q", hostVersion)
	}
	release := semver.New(hv.Major(), hv.Minor(), hv.Patch(), "", "")
	return c.Check(release), nil
}

func (v *validator) hostPermissions() {
	for i := range v.m.HostPermissions {
		hp := &v.m.HostPermissions[i]
		f := fmt.Sprintf("hostPermissions[%d]", i)
		if hp.ID == removedAffinityPermission {
			v.add(f+".id", "sticky_not_for_plugins", "host permission %q was removed: plugins take no part in sticky sessions", hp.ID)
		} else if retiredAccountPermissions[hp.ID] {
			v.add(f+".id", CodeOutdatedContract, "host permission %q was removed: a plugin receives the accounts of the account types it declares", hp.ID)
			continue
		} else if _, ok := manifest.HostPermissionRisk[hp.ID]; !ok {
			v.add(f+".id", "unknown", "unknown host permission %q", hp.ID)
			continue
		}
		if _, dup := v.perms[hp.ID]; dup {
			v.add(f+".id", "duplicate", "host permission %q requested twice", hp.ID)
			continue
		}
		v.perms[hp.ID] = hp
		if hp.ID == "accounts.auto_state" && !reflect.DeepEqual(NormalizeScope(hp.Scope), scopeOwn) {
			v.add(f+".scope", "invalid", `%s scope must be {"types": "own"}`, hp.ID)
		}
		if hp.ID == "net" && len(v.m.AccountTypes) > 0 {
			// A platform plugin's upstream traffic leaves only through the
			// core's client, under the account's proxy (CONTRACTS §124.1).
			v.add(f+".id", "platform_net", "a plugin declaring account types cannot request %q: the core sends every request of a platform plugin", hp.ID)
		}
		if hp.ID == "net" {
			if d, ok := StringList(hp.Scope, "domains"); ok {
				for _, dom := range d {
					if dom == "" || strings.ContainsAny(dom, "/ :") {
						v.add(f+".scope.domains", "invalid_domain", "invalid domain %q", dom)
					}
				}
			} else if _, present := hp.Scope["domains"]; present {
				v.add(f+".scope.domains", "invalid_type", "domains must be a list of strings")
			}
		}
	}
}

func (v *validator) capabilities() {
	seen := map[string]bool{}
	for i, c := range v.m.Capabilities {
		f := fmt.Sprintf("capabilities[%d]", i)
		switch {
		case c.ID == removedAffinityCapability:
			v.add(f, "sticky_not_for_plugins", "capability %q was removed: plugins take no part in sticky sessions", c.ID)
		case retiredCapabilities[c.ID]:
			v.add(f, CodeOutdatedContract, "capability %q was removed: %s covers Execute and %s is the only task observation", c.ID, manifest.CapPlatformAdapter, manifest.CapPlatformMonitor)
		case !KnownCapabilities[c.ID]:
			v.add(f, "unknown", "unknown capability %q", c.ID)
		}
		if seen[c.ID] {
			v.add(f, "duplicate", "capability %q declared twice", c.ID)
		}
		seen[c.ID] = true
		if c.ID == manifest.CapPlatformMonitor || c.ID == manifest.CapPlatformWebSocket {
			v.needCap(f, manifest.CapPlatformAdapter)
		}
		// Cluster broadcast (HostService.Publish / AppService.OnBroadcast)
		// needs the matching host permission, like jobs and events.
		if c.ID == manifest.CapAppBroadcast {
			v.needPerm(f, "broadcast", "capability "+manifest.CapAppBroadcast)
		}
	}
}

// isAbsoluteHTTPURL reports whether s is an absolute http or https URL with a
// host and no whitespace.
func isAbsoluteHTTPURL(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formSecrets validates ui.settings.secrets (manifest.SecretPath): native or
// iframe settings must write them ([] for none: without the key the core
// masks every setting), only the settings form takes them, and no path
// repeats or lies inside another (a secret is masked as a whole).
func (v *validator) formSecrets(field string, f manifest.Form, settings bool) {
	switch {
	case settings && f.Secrets == nil && (f.Mode == "native" || f.Mode == "iframe"):
		v.add(field, "required", "secrets is required: list the settings to mask, [] for none")
		return
	case len(f.Secrets) == 0:
		return
	case !settings:
		v.add(field, "unsupported", "only ui.settings takes secrets; an account type marks its secrets in sensitiveFields")
		return
	}
	seen := map[int][]string{}
	for i, s := range f.Secrets {
		path, ok := manifest.SecretPath(s.Path)
		at := fmt.Sprintf("%s[%d].path", field, i)
		if !ok {
			v.add(at, "invalid", "path %q must be a JSON pointer below the values (\"a/b\", \"~0\" and \"~1\" escaped) of non-empty names, at most %d deep",
				s.Path, manifest.MaxSecretDepth)
			continue
		}
		for j := range i {
			if p, ok := seen[j]; ok && (isPrefix(p, path) || isPrefix(path, p)) {
				v.add(at, "duplicate", "path %q repeats or overlaps secrets[%d]: a secret is masked as a whole", s.Path, j)
				break
			}
		}
		seen[i] = path
	}
}

// isPrefix reports whether prefix is the start of path (or path itself).
func isPrefix(prefix, path []string) bool {
	return len(prefix) <= len(path) && slices.Equal(prefix, path[:len(prefix)])
}

// form validates a console form; settings=true for ui.settings.
func (v *validator) form(field string, f manifest.Form, settings bool) {
	if f.Permission != "" && !settings {
		v.add(field+".permission", "unsupported", "only ui.settings takes a permission")
	} else if f.Permission != "" && !v.userPerm(f.Permission) {
		v.add(field+".permission", "unknown", "permission %q is not declared in userPermissions", f.Permission)
	} else if f.Permission != "" && v.m.UserPermissionScope(f.Permission) != nil {
		// The core authorizes the settings form on the permission as a
		// whole, so a scope would mean nothing there.
		v.add(field+".permission", "unsupported", "permission %q declares a scope; settings take an unscoped permission", f.Permission)
	}
	v.formSecrets(field+".secrets", f, settings)
	switch f.Mode {
	case "schema":
		v.add(field+".mode", "unsupported", "form mode schema is no longer supported: write the form as a native component (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY)")
	case "iframe":
		v.needPerm(field, "ui.iframe", "an iframe form")
		v.needFile(field+".page", f.Page)
	case "native":
		v.needPerm(field, "ui.native", "a native form")
		if v.m.UI == nil || v.m.UI.Native == nil {
			v.add(field, "missing_native_ui", "native forms require ui.native")
		}
		if f.Component == "" && !settings {
			v.add(field+".component", "required", "component is required for native forms")
		}
		if settings && f.Component == "" && f.Page == "" {
			v.add(field+".component", "required", "native settings need a component or a native page")
		}
		if settings && f.Page != "" && (v.m.UI == nil || v.m.UI.Pages[f.Page].Type != "native") {
			v.add(field+".page", "unknown", "page %q is not a native page of ui.pages", f.Page)
		}
	default:
		v.add(field+".mode", "invalid", "form mode must be iframe or native")
	}
}

func (v *validator) pricing() {
	// Model prices are set by administrators in the core (CONTRACTS §17);
	// a manifest that still declares them is rejected rather than ignored.
	var raw map[string]json.RawMessage
	if json.Unmarshal(v.files["manifest.json"], &raw) == nil {
		if _, ok := raw["pricing"]; ok {
			v.add("pricing", "unsupported", "plugins cannot declare model prices; administrators set prices in the core")
		}
	}
}

func (v *validator) hooks() {
	if len(v.m.Hooks) == 0 {
		return
	}
	v.needCap("hooks", manifest.CapGatewayHook)
	hp := v.needPerm("hooks", "gateway.hook", "gateway hooks")
	var fields, points []string
	var fieldsOK, pointsOK bool
	if hp != nil {
		fields, fieldsOK = StringList(hp.Scope, "fields")
		points, pointsOK = StringList(hp.Scope, "points")
	}
	ids := map[string]bool{}
	for i, h := range v.m.Hooks {
		f := fmt.Sprintf("hooks[%d]", i)
		if h.ID != "" {
			if ids[h.ID] {
				v.add(f+".id", "duplicate", "hook id %q declared twice", h.ID)
			}
			ids[h.ID] = true
		}
		if h.Point != manifest.HookPointRequest && h.Point != manifest.HookPointAttempt {
			v.add(f+".point", "unsupported", "unsupported hook point %q", h.Point)
		}
		if h.Failure != "" && h.Failure != "open" && h.Failure != "closed" {
			v.add(f+".failure", "invalid", "failure must be open or closed")
		}
		if h.TimeoutMs < 0 || h.TimeoutMs > MaxHookTimeout {
			v.add(f+".timeoutMs", "out_of_range", "timeoutMs must be between 0 and %d", MaxHookTimeout)
		}
		if h.MaxPromptBytes < 0 || h.MaxPromptBytes > MaxPromptBytes {
			v.add(f+".maxPromptBytes", "out_of_range", "maxPromptBytes must be between 0 and %d", MaxPromptBytes)
		}
		if hp == nil {
			continue
		}
		if len(h.Needs) > 0 {
			if !fieldsOK {
				v.add(f+".needs", "scope_missing", "gateway.hook scope.fields must list the requested fields")
			} else if miss := CoveredBy(h.Needs, fields); len(miss) > 0 {
				v.add(f+".needs", "exceeds_scope", "fields %v are not in gateway.hook scope.fields", miss)
			}
		}
		if pointsOK && len(CoveredBy([]string{h.Point}, points)) > 0 {
			v.add(f+".point", "exceeds_scope", "point %q is not in gateway.hook scope.points", h.Point)
		}
	}
}

// scheduler validates manifest.scheduler (scheduling extension points).
// Rank.Match protocols are deliberately not checked: a plugin may match a
// protocol declared by another plugin's platform, which need not be installed
// yet; the registry warns at runtime instead. Rank.Order is unconstrained,
// like hook order.
func (v *validator) scheduler() {
	s := v.m.Scheduler
	if s == nil {
		return
	}
	if s.Rank == nil {
		return
	}
	v.needPerm("scheduler.rank", "scheduler.rank", "rewriting account scheduling parameters")
	v.needCap("scheduler.rank", manifest.CapSchedulerRank)
	if t := s.Rank.TimeoutMs; t != 0 && (t < MinRankTimeout || t > MaxRankTimeout) {
		v.add("scheduler.rank.timeoutMs", "invalid",
			"timeoutMs must be 0 (host default) or between %d and %d", MinRankTimeout, MaxRankTimeout)
	}
}

func (v *validator) events() {
	e := v.m.Events
	if e == nil {
		return
	}
	v.needCap("events", manifest.CapAppEvents)
	hp := v.needPerm("events", "events", "event subscriptions")
	if len(e.Subscribe) == 0 {
		v.add("events.subscribe", "required", "subscribe must not be empty")
	}
	if e.BatchSize < 0 || e.BatchSize > 1000 {
		v.add("events.batchSize", "out_of_range", "batchSize must be between 0 and 1000")
	}
	if hp == nil {
		return
	}
	scope, ok := StringList(hp.Scope, "subscribe")
	if !ok {
		v.add("events.subscribe", "scope_missing", "events scope.subscribe must list the subscribed events")
	} else if miss := CoveredBy(e.Subscribe, scope); len(miss) > 0 {
		v.add("events.subscribe", "exceeds_scope", "events %v are not in events scope.subscribe", miss)
	}
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseSchedule parses a job schedule (5-field cron or "@every <d>").
func ParseSchedule(s string) (cron.Schedule, error) { return cronParser.Parse(s) }

func (v *validator) jobs() {
	if len(v.m.Jobs) == 0 {
		return
	}
	v.needCap("jobs", manifest.CapAppJobs)
	v.needPerm("jobs", "jobs", "background jobs")
	ids := map[string]bool{}
	nodeJobs := 0
	for i, j := range v.m.Jobs {
		f := fmt.Sprintf("jobs[%d]", i)
		if !idRe.MatchString(j.ID) {
			v.add(f+".id", "invalid_format", "job id %q is invalid", j.ID)
		} else if ids[j.ID] {
			v.add(f+".id", "duplicate", "job %q declared twice", j.ID)
		}
		ids[j.ID] = true
		sched, err := cronParser.Parse(j.Schedule)
		if err != nil {
			v.add(f+".schedule", "invalid", "invalid schedule %q: %v", j.Schedule, err)
		}
		// The task framework runs a cluster job for at most an hour and any
		// job at most every 5 seconds (CONTRACTS §77.8): work that takes
		// longer is split into runs that continue each other.
		if every, ok := sched.(cron.ConstantDelaySchedule); ok && every.Delay < manifest.MinJobEvery {
			v.add(f+".schedule", "out_of_range", "a job runs at most every %s", manifest.MinJobEvery)
		}
		if j.TimeoutSec < 0 || (!j.NodeScope() && j.TimeoutSec > manifest.MaxClusterJobTimeoutSec) {
			v.add(f+".timeoutSec", "out_of_range", "timeoutSec must be between 0 and %d; split longer work into runs that continue each other",
				manifest.MaxClusterJobTimeoutSec)
		}
		for lang, text := range j.Title {
			if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxJobTitle {
				v.add(f+".title."+lang, "invalid", "a job title is 1 to %d characters", maxJobTitle)
			}
		}
		switch j.Scope {
		case "", manifest.JobScopeCluster:
		case manifest.JobScopeNode:
			nodeJobs++
			if j.TimeoutSec > manifest.MaxNodeJobTimeoutSec {
				v.add(f+".timeoutSec", "out_of_range", "a node job times out after at most %d seconds", manifest.MaxNodeJobTimeoutSec)
			}
		default:
			v.add(f+".scope", "invalid", "job scope must be %q or %q", manifest.JobScopeCluster, manifest.JobScopeNode)
		}
	}
	if nodeJobs > manifest.MaxNodeJobs {
		v.add("jobs", "out_of_range", "at most %d node jobs", manifest.MaxNodeJobs)
	}
}

// databaseAccess checks the tables outside the plugin it asks for: named
// tables, never a plugin's (no "plg_"), each once, with privileges of
// manifest.DatabasePrivileges.
func (v *validator) databaseAccess(access []manifest.DatabaseAccess) {
	if len(access) == 0 {
		return
	}
	v.needPerm("database.access", "db.core_read", "access to tables outside the plugin")
	tables := map[string]bool{}
	for i, a := range access {
		f := fmt.Sprintf("database.access[%d]", i)
		switch {
		case !sqlIdentRe.MatchString(a.Table):
			v.add(f+".table", "invalid", "must be a table name matching %s", sqlIdentRe)
		case strings.HasPrefix(a.Table, "plg_"):
			v.add(f+".table", "invalid", "tables of plugins cannot be asked for")
		case tables[a.Table]:
			v.add(f+".table", "duplicate", "table %q is listed twice", a.Table)
		}
		tables[a.Table] = true
		for j, c := range a.Columns {
			if !sqlIdentRe.MatchString(c) || slices.Contains(a.Columns[:j], c) {
				v.add(fmt.Sprintf("%s.columns[%d]", f, j), "invalid", "must be a distinct column name matching %s", sqlIdentRe)
			}
		}
		switch {
		case len(a.Privileges) == 0:
			v.add(f+".privileges", "required", "at least one privilege")
		case len(a.Columns) > 0 && slices.Contains(a.Privileges, "delete"):
			// PostgreSQL grants DELETE on whole rows only.
			v.add(f+".privileges", "invalid", "delete applies to whole rows: ask for it in an entry without columns")
		}
		for j, p := range a.Privileges {
			if !slices.Contains(manifest.DatabasePrivileges, p) || slices.Contains(a.Privileges[:j], p) {
				v.add(fmt.Sprintf("%s.privileges[%d]", f, j), "invalid", "must be a distinct one of %v", manifest.DatabasePrivileges)
			}
		}
	}
}

// sqlIdentRe is an unquoted PostgreSQL identifier the core can name as is.
var sqlIdentRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// maxJobTitle bounds a job's title. The job limits are
// manifest.MaxClusterJobTimeoutSec and its neighbours.
const maxJobTitle = 100

func (v *validator) database() {
	d := v.m.Database
	if d == nil {
		return
	}
	v.needPerm("database", "db.schema", "a database")
	v.databaseAccess(d.Access)
	if d.Migrations == "" {
		v.add("database.migrations", "required", "migrations directory is required")
		return
	}
	if len(MigrationFiles(v.files, d.Migrations)) == 0 {
		v.add("database.migrations", "file_missing", "no .sql files under %q", d.Migrations)
	}
}

// MigrationFiles lists *.sql files directly under dir, sorted.
func MigrationFiles(files map[string][]byte, dir string) []string {
	dir = strings.TrimSuffix(dir, "/") + "/"
	var out []string
	for p := range files {
		rest, ok := strings.CutPrefix(p, dir)
		if ok && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".sql") {
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	return out
}

// validUserPermKey reports whether k is a plugin-local permission key. Its
// last segment is never "own": the core would read it as a scope
// (CONTRACTS §21.0); "*" and "/" are outside the pattern already.
func validUserPermKey(k string) bool {
	return userPermKeyRe.MatchString(k) && !strings.HasSuffix(k, ":own")
}

// maxDeclaredScopeValues bounds the values a values scope declares: the
// role editor lists every value. Not the values one request may name (the
// runtime's maxRequestScopeValues).
const maxDeclaredScopeValues = 50

var (
	scopeResourceRe = regexp.MustCompile(manifest.ScopeResourcePattern)
	scopeValueRe    = regexp.MustCompile(manifest.ScopeValuePattern)
)

func (v *validator) userPermissions() {
	groups := v.permissionGroups()
	seen := map[string]bool{}
	for i, up := range v.m.UserPermissions {
		f := fmt.Sprintf("userPermissions[%d]", i)
		if !validUserPermKey(up.Key) || len(manifest.PermissionKey(v.m.ID(), up.Key)) > 150 {
			v.add(f+".key", "invalid_format", "permission key %q must match %s and not end in :own", up.Key, userPermKeyRe.String())
		} else if seen[up.Key] {
			v.add(f+".key", "duplicate", "permission %q declared twice", up.Key)
		}
		seen[up.Key] = true
		if firstText(up.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
		if up.Group != "" && !groups[up.Group] {
			v.add(f+".group", "unknown", "group %q is not declared in permissionGroups", up.Group)
		}
		if up.Risk != "" && !slices.Contains(manifest.UserPermissionRisks, up.Risk) {
			v.add(f+".risk", "invalid_value", "risk must be one of %v", manifest.UserPermissionRisks)
		}
		for j, r := range up.Requires {
			if r == up.Key || !v.userPerm(r) {
				v.add(fmt.Sprintf("%s.requires[%d]", f, j), "unknown", "requires %q must be another permission declared here", r)
			}
		}
		v.inheritFrom(f, up)
		v.permissionScope(f+".scope", up.Scope)
		v.permissionDefaults(f+".defaults", up)
	}
}

// retiredUserPermissionFields rejects the userPermissions fields the
// permission model replaced, which would otherwise decode to nothing: a
// permission still declaring them would silently lose its defaults and risk.
func (v *validator) retiredUserPermissionFields() {
	var raw struct {
		UserPermissions []map[string]json.RawMessage `json:"userPermissions"`
	}
	if json.Unmarshal(v.files["manifest.json"], &raw) != nil {
		return
	}
	for i, up := range raw.UserPermissions {
		for _, field := range []struct{ old, repl string }{{"sensitive", "risk"}, {"defaultRoles", "defaults"}} {
			if _, ok := up[field.old]; ok {
				v.add(fmt.Sprintf("userPermissions[%d].%s", i, field.old), "unsupported", "%s was removed; use %s", field.old, field.repl)
			}
		}
	}
}

// permissionGroups validates permissionGroups and returns the declared keys.
func (v *validator) permissionGroups() map[string]bool {
	groups := map[string]bool{}
	for i, g := range v.m.PermissionGroups {
		f := fmt.Sprintf("permissionGroups[%d]", i)
		if !keyRe.MatchString(g.Key) {
			v.add(f+".key", "invalid_format", "group key %q must match %s", g.Key, keyRe.String())
		} else if groups[g.Key] {
			v.add(f+".key", "duplicate", "group %q declared twice", g.Key)
		}
		groups[g.Key] = true
		if firstText(g.Label) == "" {
			v.add(f+".label", "required", "label is required")
		}
	}
	return groups
}

// inheritFrom validates a user permission's inheritFrom: local keys no
// longer declared here (a rename or a split). A key still declared keeps
// its holders, who would get the new permission besides, unseen; the risk
// of the old key is checked at consent against the catalog.
func (v *validator) inheritFrom(f string, up manifest.UserPermission) {
	for j, from := range up.InheritFrom {
		ff := fmt.Sprintf("%s.inheritFrom[%d]", f, j)
		if _, declared := v.m.FindUserPermission(from); declared || !validUserPermKey(from) {
			v.add(ff, "invalid_value", "inheritFrom %q must be a local permission key this manifest no longer declares", from)
		}
	}
}

// permissionScope validates a user permission's scope: either a values list
// or a resource of the plugin's own offering options, own or both (a
// resource granted only whole needs no scope).
func (v *validator) permissionScope(f string, s *manifest.PermissionScope) {
	if s == nil {
		return
	}
	if len(s.Values) > 0 {
		if s.Resource != "" || s.Options != "" || s.Own {
			v.add(f, "mutually_exclusive", "values excludes resource, options and own")
		}
		v.scopeValues(f+".values", s.Values)
		return
	}
	switch {
	case s.Resource == "":
		v.add(f, "required", "scope declares a resource or values")
		return
	case !scopeResourceRe.MatchString(s.Resource):
		v.add(f+".resource", "invalid_format", "resource %q must match %s", s.Resource, manifest.ScopeResourcePattern)
	case slices.Contains(manifest.ReservedScopeResources, s.Resource):
		v.add(f+".resource", "reserved", "resource %q is reserved (%v); accounts are scoped by routes[].accounts", s.Resource, manifest.ReservedScopeResources)
	}
	if s.Options == "" {
		if !s.Own {
			v.add(f, "required", "a resource scope offers options, own or both")
		}
		return
	}
	// The core calls the options route itself, with nothing to fill a path
	// parameter with.
	r := v.routeRef(f+".options", s.Options, "GET")
	if r != nil && (strings.ToUpper(r.Method) != "GET" || r.Scope != "admin" || hasPathParam(r.Path)) {
		v.add(f+".options", "invalid", "options route %s %s must be a GET admin route without path parameters", r.Method, r.Path)
	}
}

// scopeValues validates the values of a values scope.
func (v *validator) scopeValues(f string, values []manifest.ScopeValue) {
	if len(values) > maxDeclaredScopeValues {
		v.add(f, "too_many", "at most %d values", maxDeclaredScopeValues)
	}
	seen := map[string]bool{}
	for j, sv := range values {
		ff := fmt.Sprintf("%s[%d]", f, j)
		if !scopeValueRe.MatchString(sv.Key) {
			v.add(ff+".key", "invalid_format", "value key %q must match %s", sv.Key, manifest.ScopeValuePattern)
		} else if seen[sv.Key] {
			v.add(ff+".key", "duplicate", "value %q declared twice", sv.Key)
		}
		seen[sv.Key] = true
		if firstText(sv.Label) == "" {
			v.add(ff+".label", "required", "label is required")
		}
	}
}

// permissionDefaults validates a user permission's defaults; roles are
// visited in sorted order so the errors are stable.
func (v *validator) permissionDefaults(f string, up manifest.UserPermission) {
	if len(up.Defaults) == 0 {
		return
	}
	if up.RiskLevel() == manifest.RiskCritical {
		v.add(f, "unsupported", "critical permissions take no defaults; only the super admin grants them")
		return
	}
	for _, role := range slices.Sorted(maps.Keys(up.Defaults)) {
		d, ff := up.Defaults[role], f+"."+role
		switch {
		case !slices.Contains(manifest.BuiltinRoleKeys, role):
			v.add(ff, "invalid_value", "role %q is not one of %v", role, manifest.BuiltinRoleKeys)
		case !d.Unscoped && len(d.Scopes) == 0:
			v.add(ff, "invalid_value", "leave the role out instead of false or []")
		case up.Scope == nil && !d.Unscoped:
			v.add(ff, "invalid_value", "an unscoped permission's default is true")
		case up.Scope != nil && d.Unscoped:
			v.add(ff, "invalid_value", `a scoped permission's default is a list of scopes, e.g. ["*"]`)
		default:
			for j, s := range d.Scopes {
				if _, ok := up.Scope.GrantSuffix(s); !ok {
					v.add(fmt.Sprintf("%s[%d]", ff, j), "invalid_value", `scope %q is not "*", an allowed "own" or a declared value`, s)
				}
			}
		}
	}
}

func (v *validator) userPerm(p string) bool {
	_, ok := v.m.FindUserPermission(p)
	return ok
}

func (v *validator) resources() {
	r := v.m.Resources
	if r == nil {
		return
	}
	if r.MemoryMB < 0 || (v.opt.MaxMemoryMB > 0 && r.MemoryMB > v.opt.MaxMemoryMB) {
		v.add("resources.memoryMB", "out_of_range", "memoryMB must be between 0 and %d", v.opt.MaxMemoryMB)
	}
	if r.CPU < 0 || r.CPU > MaxCPU {
		v.add("resources.cpu", "out_of_range", "cpu must be between 0 and %v", MaxCPU)
	}
	if r.MaxThreads < 0 || r.MaxThreads > MaxThreads {
		v.add("resources.maxThreads", "out_of_range", "maxThreads must be between 0 and %d", MaxThreads)
	}
	if r.MaxOpenFiles < 0 || r.MaxOpenFiles > MaxOpenFiles {
		v.add("resources.maxOpenFiles", "out_of_range", "maxOpenFiles must be between 0 and %d", MaxOpenFiles)
	}
}
