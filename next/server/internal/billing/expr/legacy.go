package expr

import (
	"strings"
)

// LegacyProduct rewrites an expression of the old rule form "A ||| B ||| C"
// (before core migration 0121) as "(A) * (B) * (C)", without the version
// prefix. It reports false when src has no separator outside string
// literals. A part with a line comment gets its ")" on a new line. The split
// is that of the old engine and of migration 0121 (billing_modes_product),
// which writes stored expressions the same way.
func LegacyProduct(src string) (string, bool) {
	body := strings.Trim(src, " \t\r\n")
	if rest, ok := strings.CutPrefix(body, "v1"); ok {
		if r := strings.TrimLeft(rest, " \t\r\n\f\v"); strings.HasPrefix(r, ":") {
			body = strings.TrimLeft(r[1:], " \t\r\n\f\v")
		}
	}
	var parts, closers []string
	var quote byte
	commented := false
	start := 0
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case quote != 0:
			if ch == '\\' && quote != '`' {
				i++
			} else if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'' || ch == '`':
			quote = ch
		case strings.HasPrefix(body[i:], "|||"):
			parts = append(parts, strings.Trim(body[start:i], " \t\r\n"))
			closers = append(closers, closer(commented))
			commented = false
			i += 2
			start = i + 1
		case strings.HasPrefix(body[i:], "//"):
			commented = true
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	parts = append(parts, strings.Trim(body[start:], " \t\r\n"))
	closers = append(closers, closer(commented))
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(" * ")
		}
		b.WriteString("(" + part + closers[i])
	}
	return b.String(), true
}

func closer(commented bool) string {
	if commented {
		return "\n)"
	}
	return ")"
}
