package x

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestItoa64(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{123, "123"},
		{-456, "-456"},
		{9223372036854775807, "9223372036854775807"},
	}
	for _, tt := range tests {
		if got := Itoa64(tt.n); got != tt.want {
			t.Errorf("Itoa64(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTruncUTF8(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"你好世界", 6, "你好"},
		{"你好世界", 7, "你好"},
		{"你好世界", 4, "你"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := TruncUTF8(tt.s, tt.n); got != tt.want {
			t.Errorf("TruncUTF8(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

func TestExact(t *testing.T) {
	for s, want := range map[string]bool{"1": true, "1.5": true, "0.00000001": true, "0.000000001": false, "-2.123456789": false} {
		if got := Exact(decimal.RequireFromString(s), 8); got != want {
			t.Errorf("Exact(%s, 8) = %v", s, got)
		}
	}
}
