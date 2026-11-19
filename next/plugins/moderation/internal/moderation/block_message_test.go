package moderation

import (
	"strings"
	"testing"
)

// An empty block_message reads as the default; a stored value is kept
// (trimmed, capped at 500 runes).
func TestBlockMessageDefault(t *testing.T) {
	long := strings.Repeat("字", 501)
	for _, tc := range []struct{ name, stored, want string }{
		{"unset", `{}`, DefaultBlockMessage},
		{"empty", `{"block_message": ""}`, DefaultBlockMessage},
		{"blank", `{"block_message": "  "}`, DefaultBlockMessage},
		{"wrong type", `{"block_message": 7}`, DefaultBlockMessage},
		{"custom", `{"block_message": " 请求被拒绝 "}`, "请求被拒绝"},
		{"too long", `{"block_message": "` + long + `"}`, long[:500*len("字")]},
	} {
		s, err := DecodeSettings([]byte(tc.stored))
		if err != nil || s.BlockMessage != tc.want {
			t.Errorf("%s: block_message = %q (%v), want %q", tc.name, s.BlockMessage, err, tc.want)
		}
	}
	if got := DefaultSettings().BlockMessage; got != DefaultBlockMessage {
		t.Fatalf("settings default = %q, want %q", got, DefaultBlockMessage)
	}
}

// The default the settings form showed (forms/settings.schema.json, removed
// in 0.12.2) and the settings pages show (settingsForm.ts): unchanged.
func TestBlockMessageFormDefault(t *testing.T) {
	if DefaultBlockMessage != "Your request was blocked by content moderation. Please revise your input and try again." {
		t.Fatalf("DefaultBlockMessage = %q", DefaultBlockMessage)
	}
}
