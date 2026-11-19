package core

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// A proxy printed or logged by accident never shows its password.
func TestProxySpecRedacted(t *testing.T) {
	ref := ProxyRef{ID: 7, ProxySpec: ProxySpec{Protocol: "socks5", Host: "proxy.example", Port: 1080, Username: "u", Password: "s3cret-pw"}}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("x", "ref", ref, "spec", ref.ProxySpec, "ptr", &ref)
	for _, out := range []string{
		fmt.Sprintf("%v %+v %#v %s %d %x %q", ref, ref, ref, ref, ref, ref, ref),
		fmt.Sprintf("%v %+v %#v %s %d %x %q", ref.ProxySpec, ref.ProxySpec, ref.ProxySpec, ref.ProxySpec, ref.ProxySpec, ref.ProxySpec, ref.ProxySpec),
		fmt.Sprintf("%v %+v %#v %s %d %x %q", &ref, &ref, &ref, &ref, &ref, &ref, &ref),
		fmt.Sprintf("%v %+v %#v %s %d %x %q", &ref.ProxySpec, &ref.ProxySpec, &ref.ProxySpec, &ref.ProxySpec, &ref.ProxySpec, &ref.ProxySpec, &ref.ProxySpec),
		logged.String(),
	} {
		if strings.Contains(out, "s3cret-pw") || !strings.Contains(out, "proxy.example") {
			t.Errorf("formatted proxy: %s", out)
		}
	}
}
