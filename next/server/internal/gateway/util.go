package gateway

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/x"
)

// globMatch matches '*' (any run, including '/') and '?' (one byte).
func globMatch(pattern, s string) bool {
	p, i := 0, 0
	star, mark := -1, 0
	for i < len(s) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == s[i]):
			p++
			i++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, i
			p++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// anyGlob reports whether s matches one of patterns; an empty list matches
// everything.
func anyGlob(patterns []string, s string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if globMatch(p, s) {
			return true
		}
	}
	return false
}

// matchList is anyGlob for exact identifiers where "*" means all.
func matchList(list []string, values ...string) bool {
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		for _, v := range values {
			if p == "*" || globMatch(p, v) {
				return true
			}
		}
	}
	return false
}

// extractPromptText concatenates the plain text found under paths: strings,
// and the "text" parts of content arrays (recursing into nested "content").
func extractPromptText(body []byte, paths []string, limit int) string {
	var b strings.Builder
	add := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	var walk func(r gjson.Result, depth int)
	walk = func(r gjson.Result, depth int) {
		if depth > 8 || (limit > 0 && b.Len() >= limit) {
			return
		}
		switch {
		case r.Type == gjson.String:
			add(r.String())
		case r.IsArray():
			r.ForEach(func(_, v gjson.Result) bool {
				walk(v, depth+1)
				return limit <= 0 || b.Len() < limit
			})
		case r.IsObject():
			if t := r.Get("text"); t.Type == gjson.String {
				add(t.String())
			}
			if c := r.Get("content"); c.Exists() {
				walk(c, depth+1)
			}
		}
	}
	for _, p := range paths {
		walk(gjson.GetBytes(body, p), 0)
	}
	return truncateUTF8(b.String(), limit)
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune. A
// non-positive limit is returned unchanged, as before.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return s
	}
	return x.Truncate(s, n)
}

func itoa(n int64) string { return x.Itoa64(n) }

// maxClientRequestID bounds usage_logs.client_request_id (varchar(128)).
const maxClientRequestID = 128

// clientRequestID normalizes the client's X-Request-Id for the usage record:
// trimmed, invalid UTF-8 dropped (PostgreSQL rejects it) and cut to
// maxClientRequestID characters.
func clientRequestID(s string) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	n := 0
	for i := range s {
		if n == maxClientRequestID {
			return s[:i]
		}
		n++
	}
	return s
}

// headerLower normalizes a header name for map keys.
func headerLower(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// pathCovered reports whether patch path p is one of allowed or below one
// of them ("metadata" covers "metadata.user_id").
func pathCovered(p string, allowed []string) bool {
	for _, a := range allowed {
		if a == "" {
			continue
		}
		if p == a || strings.HasPrefix(p, a+".") {
			return true
		}
	}
	return false
}

// getJSON returns the raw JSON at path, or "" when absent.
func getJSON(body []byte, path string) string {
	if r := gjson.GetBytes(body, path); r.Exists() {
		return r.Raw
	}
	return ""
}

func parseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
