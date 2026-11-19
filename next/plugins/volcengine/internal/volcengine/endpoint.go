package volcengine

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const FieldVideoEndpoint = "video_endpoint"

// resolveEndpoint resolves an administrator-configured collection endpoint.
// A rooted path is appended to base; an absolute URL explicitly selects its
// own origin. The host still applies account egress/proxy/SSRF policy. Empty
// values are handled by the caller's legacy/default fallback, never here.
func resolveEndpoint(base, configured string) (string, error) {
	validate := func(raw string, pathOnly bool) (*url.URL, error) {
		if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "?#\\") {
			return nil, fmt.Errorf("endpoint must be a non-empty URL or path without query, fragment or backslash")
		}
		if hasSpace(raw) {
			return nil, fmt.Errorf("endpoint must not contain whitespace or control characters")
		}
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Opaque != "" || u.ForceQuery {
			return nil, fmt.Errorf("invalid endpoint URL")
		}
		if pathOnly {
			if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || u.Host != "" || u.Scheme != "" {
				return nil, fmt.Errorf("endpoint path must start with one slash")
			}
		} else if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return nil, fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
		}
		p := u.EscapedPath()
		for depth := 0; ; depth++ {
			for _, r := range p {
				if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' {
					return nil, fmt.Errorf("endpoint path contains invalid characters")
				}
			}
			for _, segment := range strings.Split(p, "/") {
				if segment == "." || segment == ".." {
					return nil, fmt.Errorf("endpoint path must not contain dot segments")
				}
			}
			decoded, err := url.PathUnescape(p)
			if err != nil {
				return nil, fmt.Errorf("invalid endpoint path encoding")
			}
			if decoded == p {
				break
			}
			if depth >= 3 {
				return nil, fmt.Errorf("endpoint path encoding is nested too deeply")
			}
			p = decoded
		}
		return u, nil
	}
	pathOnly := strings.HasPrefix(configured, "/")
	if _, err := validate(configured, pathOnly); err != nil {
		return "", err
	}
	if !pathOnly {
		return strings.TrimRight(configured, "/"), nil
	}
	if _, err := validate(base, false); err != nil {
		return "", fmt.Errorf("base_url: %w", err)
	}
	return strings.TrimRight(base, "/") + strings.TrimRight(configured, "/"), nil
}

// hasSpace reports whether s holds whitespace or a control character. A
// URL or path setting with one is refused when the account is saved: url.Parse
// takes a space in a path, and the request would then go to an escaped
// address the operator never typed.
func hasSpace(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0
}
