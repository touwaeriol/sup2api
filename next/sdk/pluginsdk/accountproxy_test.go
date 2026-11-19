package pluginsdk_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

// The proxy of an account is read whole, and no formatting prints its
// password.
func TestAccountProxyOf(t *testing.T) {
	if pluginsdk.AccountProxyOf(&pluginv1.Account{Id: 1}) != nil || pluginsdk.AccountProxyOf(nil) != nil {
		t.Fatal("an account without a proxy has none")
	}
	p := pluginsdk.AccountProxyOf(&pluginv1.Account{Proxy: &pluginv1.AccountProxy{
		Id: 7, Protocol: "socks5", Host: "proxy.example", Port: 1080, Username: "u", Password: "s3cret-pw"}})
	want := pluginsdk.AccountProxy{ID: 7, Protocol: "socks5", Host: "proxy.example", Port: 1080, Username: "u", Password: "s3cret-pw"}
	if p == nil || *p != want {
		t.Fatalf("proxy = %#v", p)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("x", "proxy", *p, "ptr", p)
	for _, out := range []string{
		fmt.Sprint(*p), fmt.Sprintf("%v %+v %#v %s %d %x %q", *p, *p, *p, *p, *p, *p, *p),
		fmt.Sprintf("%v %+v %#v %d", p, p, p, p), logged.String(),
	} {
		if strings.Contains(out, "s3cret-pw") || !strings.Contains(out, "proxy.example") {
			t.Errorf("formatted proxy: %s", out)
		}
	}
}
