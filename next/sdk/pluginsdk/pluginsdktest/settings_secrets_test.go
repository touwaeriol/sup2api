package pluginsdktest_test

import (
	"runtime"
	"testing"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
)

// recorder is a testing.TB whose Fatalf records that it failed and stops
// the goroutine, as testing.T's does.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(string, ...any) {
	r.failed = true
	runtime.Goexit()
}

// failed reports whether check failed r.
func failed(t *testing.T, check func(testing.TB)) bool {
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		check(r)
	}()
	<-done
	return r.failed
}

func TestSettingsSecrets(t *testing.T) {
	password := manifest.SecretField{Path: []string{"password"}, Credential: true}
	for _, c := range []struct {
		name, manifest string
		want           []manifest.SecretField
		fails          bool
	}{
		{"none, written out", `{"ui":{"settings":{"mode":"native","secrets":[]}}}`, nil, false},
		{"one", `{"ui":{"settings":{"mode":"native","secrets":[{"path":"password"}]}}}`, []manifest.SecretField{password}, false},
		{"not written out", `{"ui":{"settings":{"mode":"native"}}}`, nil, true},
		{"another secret", `{"ui":{"settings":{"mode":"native","secrets":[{"path":"token"}]}}}`, []manifest.SecretField{password}, true},
		{"one missing", `{"ui":{"settings":{"mode":"native","secrets":[]}}}`, []manifest.SecretField{password}, true},
		{"not JSON", `{`, nil, true},
	} {
		got := failed(t, func(tb testing.TB) { pluginsdktest.SettingsSecrets(tb, []byte(c.manifest), c.want...) })
		if got != c.fails {
			t.Errorf("%s: failed = %v, want %v", c.name, got, c.fails)
		}
	}
}
