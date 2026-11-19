package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// The test binary doubles as the core binary ("plugin-exec") and as the
// plugin binary (SANDBOX_TEST_CHILD selects a child behaviour).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == SubcommandName {
		os.Exit(RunExec(os.Args[2:]))
	}
	if mode := os.Getenv("SANDBOX_TEST_CHILD"); mode != "" {
		os.Exit(childMain(mode))
	}
	code := m.Run()
	cleanupPlatform()
	os.Exit(code)
}

// report is printed by the "report" child as JSON.
type report struct {
	GoMemLimit   string `json:"gomemlimit"`
	HasSecret    bool   `json:"has_secret"`
	PluginKey    string `json:"plugin_key"`
	Strict       string `json:"strict"`
	DialErr      string `json:"dial_err"`
	UnixErr      string `json:"unix_err"`
	OOMScoreAdj  string `json:"oom_score_adj"`
	Nice         int    `json:"nice"`
	NoFile       uint64 `json:"nofile"`
	CoreLimit    uint64 `json:"core_limit"`
	NoNewPrivs   string `json:"no_new_privs"`
	SeccompMode  string `json:"seccomp_mode"`
	IOUringErrno string `json:"io_uring_errno"`
}

func childMain(mode string) int {
	switch mode {
	case "exit7":
		return 7
	case "report":
		r := report{
			GoMemLimit: os.Getenv("GOMEMLIMIT"),
			PluginKey:  os.Getenv(protocol.EnvPluginKey),
			Strict:     os.Getenv(protocol.EnvStrictNetwork),
		}
		_, r.HasSecret = os.LookupEnv("SUB2API_MASTER_KEY")
		if addr := os.Getenv("SANDBOX_TEST_ADDR"); addr != "" {
			c, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				r.DialErr = err.Error()
			} else {
				_ = c.Close()
			}
		}
		dir, _ := os.MkdirTemp("", "sbx")
		if ln, err := net.Listen("unix", dir+"/s.sock"); err != nil {
			r.UnixErr = err.Error()
		} else {
			_ = ln.Close()
		}
		_ = os.RemoveAll(dir)
		fillPlatformReport(&r)
		_ = json.NewEncoder(os.Stdout).Encode(r)
		return 0
	case "alloc":
		buf := make([]byte, 64<<20)
		for i := range buf {
			buf[i] = byte(i)
		}
		fmt.Println("ready")
		time.Sleep(30 * time.Second)
		runtime.KeepAlive(buf)
		return 0
	case "sleep":
		time.Sleep(time.Minute)
		return 0
	}
	return platformChild(mode)
}

func TestParseExecArgs(t *testing.T) {
	o, err := parseExecArgs([]string{
		"--mem-mb=256", "--max-open-files", "64", "--max-threads=8", "--strict-network",
		"--seccomp=true", "--nice=5", "--keep-env=SUB2API_X, SUB2API_Y", "--", "/bin/plugin", "-flag", "x",
	}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if o.MemMB != 256 || o.MaxOpenFiles != 64 || o.MaxThreads != 8 || !o.StrictNetwork || !o.Seccomp || o.Nice != 5 {
		t.Fatalf("%+v", o)
	}
	if o.Binary != "/bin/plugin" || !slices.Equal(o.Args, []string{"-flag", "x"}) {
		t.Fatalf("binary/args %q %q", o.Binary, o.Args)
	}
	if !slices.Equal(o.KeepEnv, []string{"SUB2API_X", "SUB2API_Y"}) {
		t.Fatalf("keep %q", o.KeepEnv)
	}
	d, _ := parseExecArgs([]string{"--", "p"}, os.Stderr)
	if d.Nice != DefaultNice || d.StrictNetwork || d.Seccomp {
		t.Fatalf("defaults %+v", d)
	}
	for _, bad := range [][]string{{}, {"--"}, {"--mem-mb=-1", "--", "p"}, {"--nope", "--", "p"}} {
		if _, err := parseExecArgs(bad, &strings.Builder{}); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	if RunExec([]string{"--bogus"}) != exitUsage {
		t.Fatal("usage exit code")
	}
}

func TestBuildEnv(t *testing.T) {
	env := buildEnv([]string{
		"PATH=/bin",
		"SUB2API_DATABASE_URL=postgres://secret",
		"SUB2API_MASTER_KEY=x",
		"SUB2API_PLUGIN=sub2api-next-plugin-v1",
		"SUB2API_PLUGIN_KEY=demo",
		"SUB2API_EXTRA=keep",
		"PLUGIN_MIN_PORT=1",
		"GOMEMLIMIT=1",
		"broken",
	}, &execOptions{MemMB: 100, KeepEnv: []string{"SUB2API_EXTRA"}})
	want := []string{
		"PATH=/bin", "SUB2API_PLUGIN=sub2api-next-plugin-v1", "SUB2API_PLUGIN_KEY=demo",
		"SUB2API_EXTRA=keep", "PLUGIN_MIN_PORT=1", "GOMEMLIMIT=94371840",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("got %q\nwant %q", env, want)
	}
}

func TestExecArgs(t *testing.T) {
	args := execArgs(core.LaunchSpec{
		BinaryPath: "/p/bin", MemoryMB: 512, MaxOpenFiles: 1024, MaxThreads: 50,
		StrictNetwork: true, Seccomp: true,
		Env: []string{"SUB2API_PLUGIN_KEY=x", "SUB2API_CUSTOM=1", "OTHER=2"},
	}, 10, false)
	want := []string{
		"plugin-exec", "--mem-mb=512", "--max-open-files=1024", "--max-threads=50",
		"--strict-network=true", "--seccomp=true", "--nice=10", "--keep-env=SUB2API_CUSTOM", "--", "/p/bin",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("got %q", args)
	}
	o, err := parseExecArgs(args[1:], os.Stderr)
	if err != nil || o.MemMB != 512 || !o.StrictNetwork || o.Binary != "/p/bin" || o.Landlock {
		t.Fatalf("round trip %+v %v", o, err)
	}
	// The data directory and the CA certificates only for a plugin granted
	// "fs.data" and "net".
	args = execArgs(core.LaunchSpec{BinaryPath: "/p/bin", DataDir: "/data/p", Net: true}, 10, true)
	if o, err = parseExecArgs(args[1:], os.Stderr); err != nil || !o.Landlock || o.DataDir != "/data/p" || !o.CACerts {
		t.Fatalf("landlock round trip %+v %v", o, err)
	}
}

func TestCommandUnwrapped(t *testing.T) {
	l := NewLauncher(LauncherOptions{DisableWrap: true})
	if _, err := l.Command(context.Background(), core.LaunchSpec{}); err == nil {
		t.Fatal("empty binary accepted")
	}
	t.Setenv("SUB2API_MASTER_KEY", "top-secret")
	cmd, err := l.Command(context.Background(), core.LaunchSpec{
		PluginKey: "demo", Version: "1.0.0", BinaryPath: "/x/plugin", DataDir: "/data/demo",
		MemoryMB: 100, CPU: 1.5, StrictNetwork: true, Env: []string{"FOO=bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != "/data/demo" || len(cmd.Args) != 1 {
		t.Fatalf("cmd %+v", cmd.Args)
	}
	for _, kv := range []string{
		"SUB2API_PLUGIN_KEY=demo", "SUB2API_PLUGIN_VERSION=1.0.0", "SUB2API_PLUGIN_STRICT_NETWORK=0",
		"SUB2API_PLUGIN_DATA_DIR=/data/demo", "GOMAXPROCS=2", "FOO=bar", "GOMEMLIMIT=94371840",
	} {
		if !slices.Contains(cmd.Env, kv) {
			t.Fatalf("missing %s in %q", kv, cmd.Env)
		}
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "SUB2API_MASTER_KEY") {
			t.Fatal("secret leaked")
		}
	}
}

func TestCommandWrapped(t *testing.T) {
	l := NewLauncher(LauncherOptions{Executable: "/usr/bin/sub2api"})
	l.wrap = true
	cmd, err := l.Command(context.Background(), core.LaunchSpec{PluginKey: "demo", BinaryPath: "/x/plugin", StrictNetwork: true, Seccomp: true})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args[0] != "/usr/bin/sub2api" || cmd.Args[1] != SubcommandName || cmd.Args[len(cmd.Args)-1] != "/x/plugin" {
		t.Fatalf("args %q", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "SUB2API_PLUGIN_STRICT_NETWORK=1") {
		t.Fatalf("env %q", cmd.Env)
	}
}

func TestRunExecPassesExitCode(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, SubcommandName, "--mem-mb=64", "--", exe)
	cmd.Env = append(os.Environ(), "SANDBOX_TEST_CHILD=exit7")
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("want exit 7, got %v", err)
	}
	cmd = exec.Command(exe, SubcommandName, "--", "definitely-not-a-binary-xyz")
	err = cmd.Run()
	if !errors.As(err, &ee) || ee.ExitCode() != exitNotFound {
		t.Fatalf("want exit %d, got %v", exitNotFound, err)
	}
}

func TestRunExecFiltersEnv(t *testing.T) {
	exe, _ := os.Executable()
	cmd := exec.Command(exe, SubcommandName, "--mem-mb=100", "--", exe)
	cmd.Env = append(os.Environ(), "SANDBOX_TEST_CHILD=report", "SUB2API_MASTER_KEY=x", "SUB2API_PLUGIN_KEY=demo")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if r.HasSecret || r.PluginKey != "demo" || r.GoMemLimit != "94371840" {
		t.Fatalf("%+v", r)
	}
}

func TestParseProc(t *testing.T) {
	var s procSample
	status := "Name:\tplugin\nVmPeak:\t  2000 kB\nVmRSS:\t   1234 kB\nThreads:\t17\n"
	if err := parseStatus(status, &s); err != nil {
		t.Fatal(err)
	}
	if s.RSSBytes != 1234*1024 || s.Threads != 17 {
		t.Fatalf("%+v", s)
	}
	if err := parseStatus("Name: x\n", &s); err == nil {
		t.Fatal("missing threads accepted")
	}
	stat := "4242 (my (odd) plugin) S 1 4242 4242 0 -1 4194560 100 0 0 0 250 50 0 0 30 10 17 0 100 1000 300"
	if err := parseStat(stat, &s); err != nil {
		t.Fatal(err)
	}
	if s.CPUTicks != 300 {
		t.Fatalf("ticks %d", s.CPUTicks)
	}
	if err := parseStat("garbage", &s); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestWatchStateEvents(t *testing.T) {
	w := &watchState{spec: core.LaunchSpec{PluginKey: "p", MemoryMB: 100, MaxThreads: 10}, pid: 1}
	var evs []core.ResourceEvent
	emit := func(e core.ResourceEvent) { evs = append(evs, e) }
	at := time.Unix(1000, 0)
	big := int64(111 << 20) // > 110 MiB
	w.observe(procSample{RSSBytes: big, Threads: 5, CPUTicks: 0}, at, emit)
	w.observe(procSample{RSSBytes: big, Threads: 5, CPUTicks: 500}, at.Add(5*time.Second), emit)
	if evs[1].CPUPct != 100 {
		t.Fatalf("cpu %v", evs[1].CPUPct)
	}
	w.observe(procSample{RSSBytes: 50 << 20, Threads: 5}, at.Add(10*time.Second), emit) // resets the counter
	for i := range 3 {
		w.observe(procSample{RSSBytes: big, Threads: 20}, at.Add(time.Duration(15+5*i)*time.Second), emit)
	}
	var mem, thr int
	for _, e := range evs {
		switch e.Kind {
		case "memory_exceeded":
			mem++
		case "threads_exceeded":
			thr++
		}
	}
	if mem != 1 || thr != 1 {
		t.Fatalf("mem=%d threads=%d events=%+v", mem, thr, evs)
	}
}
