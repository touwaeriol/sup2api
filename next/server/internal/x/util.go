// Package x provides small utility functions used across the codebase.
package x

import (
	"strconv"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// Itoa64 converts an int64 to a string.
func Itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

// Unique keeps the first occurrence of each value, preserving input order.
func Unique[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	out := make([]T, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// TruncUTF8 truncates s to at most n bytes, cutting at a rune boundary.
func TruncUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Truncate is the shared byte cap for display and storage-bound text: it
// shortens s to at most n bytes without splitting a UTF-8 rune.
func Truncate(s string, n int) string { return TruncUTF8(s, n) }

// TruncateEllipsis is Truncate with an ellipsis marking the cut: text that
// already fits is returned unchanged, anything longer is trimmed at a rune
// boundary and gets "…" appended.
func TruncateEllipsis(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return TruncUTF8(s, n) + "…"
}

// Exact reports whether d has at most places decimals, so that storing it
// at that scale loses nothing.
func Exact(d decimal.Decimal, places int32) bool { return d.Equal(d.Round(places)) }
