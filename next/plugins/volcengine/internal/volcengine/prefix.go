package volcengine

// Upstream path prefixes. Ark's OpenAI-compatible surface sits under /api/v3
// on its own endpoints, but a relay in front of Ark mounts the same shapes
// wherever it likes: the one this plugin was first verified against serves text
// at the root (/v1/chat/completions, /v1/messages) while keeping Ark's native
// video tasks under /doubao/api/v3. One fixed prefix cannot express that.
//
// Which of the two an account is, is its ACCOUNT TYPE, not a guess: apikey is
// Ark itself and has no path settings, relay has configurable paths. An earlier
// version had one account type and derived the layout from the host of
// base_url, which meant the official case and the relay case shared one form
// and the rule had to special-case BytePlus to avoid treating Ark's own
// overseas endpoint as a relay.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
)

// prefixes are the path prefixes of one account, already normalized.
type prefixes struct {
	api           string
	video         string
	videoEndpoint string
	// Official Messages uses a separate native Anthropic-compatible surface.
	anthropic string
}

// prefixesOf returns the path layout of one account.
//
// An apikey account is on Ark, which serves exactly one layout, so it reads no
// settings at all: api_prefix and video_api_prefix are not in that type's
// settingsFields and not in its form, and a value smuggled past both would land
// in the encrypted credentials blob where this never looks.
//
// A relay account defaults to the standard paths and may name either prefix.
// The two are separate because Ark's native video tasks are the one thing a
// relay commonly mounts somewhere of its own (the first one verified keeps them
// under /doubao/api/v3 while serving text at the root).
func prefixesOf(accountType, settingsJSON string) (prefixes, error) {
	if accountType != AccountTypeRelay {
		return prefixes{api: APIPrefix, video: APIPrefix, anthropic: "/api/compatible/v1"}, nil
	}
	settings, err := apikey.DecodeObject(settingsJSON)
	if err != nil {
		return prefixes{}, fmt.Errorf("settings: %w", err)
	}
	read := func(field string) (string, error) {
		v, ok := settings[field]
		if !ok || v == nil {
			return "", nil
		}
		s, isStr := v.(string)
		if !isStr {
			return "", fmt.Errorf("%s must be a string", field)
		}
		p, err := normalizePrefix(s)
		if err != nil {
			// Name the field. Two settings have the same rules, and an
			// operator reading "must start with /" in a log has no way to tell
			// which of them to go and fix.
			return "", fmt.Errorf("%s %w", field, err)
		}
		return p, nil
	}
	api, err := read(FieldAPIPrefix)
	if err != nil {
		return prefixes{}, err
	}
	video, err := read(FieldVideoAPIPrefix)
	if err != nil {
		return prefixes{}, err
	}
	if api == "" {
		api = RelayAPIPrefix
	}
	if video == "" {
		// Video follows the text surface. A relay that keeps Ark's native task
		// paths elsewhere is exactly what the second setting is for, but the
		// common case of one prefix must need one field.
		video = api
	}
	endpoint := ""
	if value, ok := settings[FieldVideoEndpoint]; ok && value != nil {
		var valid bool
		endpoint, valid = value.(string)
		if !valid {
			return prefixes{}, fmt.Errorf("%s must be a string", FieldVideoEndpoint)
		}
		if endpoint != "" {
			if _, err := resolveEndpoint("https://endpoint.invalid", endpoint); err != nil {
				return prefixes{}, fmt.Errorf("%s: %w", FieldVideoEndpoint, err)
			}
		}
	}
	return prefixes{api: api, video: video, anthropic: api, videoEndpoint: endpoint}, nil
}

// validateRelayBaseURL refuses a relay base_url that already carries a path
// prefix, and refuses a missing one.
//
// Both halves exist because of the same failure, which is a billing failure
// rather than a 404. relaySpec has no Versions (see its comment), so a base URL
// of "https://r/v1" with the default prefix yields "https://r/v1/v1/...". On
// the text surface that is a loud 404. On the video surface the submit 404s,
// but if a relay instead answers 200 for the submit and 404 for the poll - or
// if only one of the two prefixes is wrong - the reconcile poll answers 404 for
// every entry, the core reads that as "still pending" until the deadline, and
// then keeps the estimate: the account is charged its pre-charge for work that
// really finished and reported real usage.
//
// So this is refused at save time with a message naming the setting that
// expresses it, rather than stripped: stripping would silently move the request
// somewhere the operator did not write.
func validateRelayBaseURL(errs pluginsdk.FieldErrors, credentialsJSON, settingsJSON string) pluginsdk.FieldErrors {
	raw := ""
	for _, src := range []string{settingsJSON, credentialsJSON} {
		obj, err := apikey.DecodeObject(src)
		if err != nil {
			continue
		}
		if s, ok := obj["base_url"].(string); ok && strings.TrimSpace(s) != "" {
			raw = strings.TrimSpace(s)
			break
		}
	}
	if raw == "" {
		return errs.Add("base_url", "required",
			"base_url is required for a relay account - there is no default relay address / "+
				"中转账号必须填写 base_url —— 中转站没有默认地址")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// apikey.Spec.Validate has already reported this shape.
		return errs
	}
	// The escaped path, as the address is saved and sent: "a%2Fv1" does not end in a /v1 segment.
	p := strings.TrimRight(u.EscapedPath(), "/")
	for _, known := range []string{RelayAPIPrefix, APIPrefix} {
		if !strings.HasSuffix(p, known) {
			continue
		}
		return errs.Add("base_url", "format", fmt.Sprintf(
			"base_url must be the relay's root address, without %s: put the path in %s instead, so the video paths "+
				"can differ from the text ones / base_url 要填中转站的根地址，不要带 %s —— 路径请填在 %s 里，"+
				"视频路径才能与文本路径不同", known, FieldAPIPrefix, known, FieldAPIPrefix))
	}
	return errs
}

// normalizePrefix trims a prefix and rejects the shapes that would not stay a
// path. An empty result is legal and means "unset".
//
// This is not a blocklist of dangerous characters - those are always on the
// wrong side, and CONTRACTS §25.1 already records what that costs. It is a
// whitelist of one shape: a rooted path. It runs when an account is saved
// and on every request (prefixesOf), so a rule added here also refuses
// stored accounts on the request path; a save-only rule belongs in
// validatePrefixFields. The guarantee that a prefix cannot move the request
// to another host is enforced separately and unconditionally, by upstreamURL
// below: a stored value need not have passed today's rules (an older
// version wrote it, a backup restored it).
func normalizePrefix(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", nil
	}
	if len(p) > MaxPrefixLen {
		return "", fmt.Errorf("must be at most %d characters", MaxPrefixLen)
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("must start with %q", "/")
	}
	// "//host" is a protocol-relative URL, not a path: parsed after a base it
	// replaces the host entirely.
	if strings.HasPrefix(p, "//") {
		return "", fmt.Errorf("must not start with %q", "//")
	}
	if strings.ContainsAny(p, "?#") {
		return "", fmt.Errorf("must not contain a query or fragment")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("must not contain a %q segment", "..")
		}
	}
	return strings.TrimRight(p, "/"), nil
}

// validatePrefixFields reports the prefix settings an operator cannot have
// meant. It is the friendly half of the guarantee - the binding half is
// upstreamURL, which runs on every request. Whitespace inside a prefix is
// refused here only, on save: a stored prefix holding some keeps working.
func validatePrefixFields(errs pluginsdk.FieldErrors, settingsJSON string) pluginsdk.FieldErrors {
	obj, err := apikey.DecodeObject(settingsJSON)
	if err != nil {
		return errs.Add("", "type", err.Error()+" / 字段类型不正确")
	}
	for _, f := range []string{FieldAPIPrefix, FieldVideoAPIPrefix} {
		v, ok := obj[f]
		if !ok || v == nil {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			errs = errs.Add(f, "type", f+" must be a string / "+f+" 必须是字符串")
			continue
		}
		_, err := normalizePrefix(s)
		if err == nil && hasSpace(strings.TrimSpace(s)) {
			err = errors.New("must not contain whitespace")
		}
		if err != nil {
			errs = errs.Add(f, "pattern", fmt.Sprintf(
				"%s %s / %s 必须是以 / 开头的路径前缀（如 /api/v3），留空表示使用默认值", f, err, f))
		}
	}
	if value, ok := obj[FieldVideoEndpoint]; ok && value != nil {
		if endpoint, valid := value.(string); !valid {
			errs = errs.Add(FieldVideoEndpoint, "type", "video_endpoint must be a string")
		} else if endpoint != "" {
			if _, err := resolveEndpoint("https://endpoint.invalid", endpoint); err != nil {
				errs = errs.Add(FieldVideoEndpoint, "format", err.Error())
			}
		}
	}
	return errs
}

// normalizePrefixFields rewrites the two prefix settings in place. An invalid
// value is left as the operator typed it: validatePrefixFields has already
// turned it into a field error, and silently storing a "fixed" version of
// something we refused would be worse than storing what was sent.
func normalizePrefixFields(objJSON string) string {
	obj, err := apikey.DecodeObject(objJSON)
	if err != nil || len(obj) == 0 {
		return objJSON
	}
	changed := false
	for _, f := range []string{FieldAPIPrefix, FieldVideoAPIPrefix} {
		v, ok := obj[f]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		n, err := normalizePrefix(s)
		if err != nil {
			continue
		}
		obj[f] = n
		changed = true
	}
	if !changed {
		return objJSON
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return objJSON
	}
	return string(b)
}

// upstreamURL joins a normalized base URL with a path and returns it only if
// the result is still the same scheme and host.
//
// This is the actual security boundary of the two prefix settings. base_url is
// restricted by guardedSettings (CONTRACTS §21.3) to the official endpoints
// unless the caller holds account:settings:custom - but guardedSettings is a
// URL allow-list (check/validate.go requires every allowed entry to be an
// absolute http(s) URL) and therefore cannot guard a path. A free-text prefix
// of "@evil.com/v3" appended to "https://ark.cn-beijing.volces.com" parses
// with host evil.com, the first half demoted to userinfo: the restriction is
// bypassed and the account's key goes elsewhere. netguard.CheckURL does not
// help, because it asks "is this host private", not "is this the host we
// meant".
//
// So the check is not on the prefix, it is on the answer: parse what we are
// about to send and require the origin we started from. Anything a future
// prefix shape does that normalizePrefix did not foresee still cannot leave
// the host.
func upstreamURL(base, path string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "base_url is not a URL: %v", err)
	}
	full := base + path
	u, err := url.Parse(full)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "the upstream path is not a URL: %v", err)
	}
	if u.Scheme != b.Scheme || u.Host != b.Host {
		return "", status.Errorf(codes.InvalidArgument,
			"the upstream path would move the request to %s://%s, but this account's base URL is %s://%s; "+
				"check %s and %s", u.Scheme, u.Host, b.Scheme, b.Host, FieldAPIPrefix, FieldVideoAPIPrefix)
	}
	return full, nil
}
