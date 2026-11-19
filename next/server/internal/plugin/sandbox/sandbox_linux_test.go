//go:build linux

package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

func fillPlatformReport(r *report) {
	if b, err := os.ReadFile("/proc/self/oom_score_adj"); err == nil {
		r.OOMScoreAdj = strings.TrimSpace(string(b))
	}
	if raw, err := unix.Getpriority(unix.PRIO_PROCESS, 0); err == nil {
		r.Nice = 20 - raw
	}
	var lim syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim) == nil {
		r.NoFile = lim.Cur
	}
	var core syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_CORE, &core) == nil {
		r.CoreLimit = core.Cur + core.Max
	}
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, _ := strings.Cut(line, ":")
			switch k {
			case "NoNewPrivs":
				r.NoNewPrivs = strings.TrimSpace(v)
			case "Seccomp":
				r.SeccompMode = strings.TrimSpace(v)
			}
		}
	}
	var params [120]byte
	_, _, e := syscall.Syscall(unix.SYS_IO_URING_SETUP, 1, uintptr(unsafe.Pointer(&params[0])), 0)
	if e != 0 {
		r.IOUringErrno = e.Error()
	}
}

func tcpListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func runReport(t *testing.T, flags []string, env ...string) report {
	t.Helper()
	exe, _ := os.Executable()
	args := append([]string{SubcommandName}, flags...)
	args = append(args, "--", exe)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), append([]string{"SANDBOX_TEST_CHILD=report"}, env...)...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("plugin-exec: %v", err)
	}
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return r
}

func parentNice(t *testing.T) int {
	raw, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	return 20 - raw
}

func TestStrictNetworkSeccompBlocksDial(t *testing.T) {
	addr := tcpListener(t)
	// Sanity: the parent can dial.
	if c, err := net.Dial("tcp", addr); err != nil {
		t.Fatal(err)
	} else {
		_ = c.Close()
	}
	r := runReport(t, []string{"--strict-network=true", "--seccomp=true", "--mem-mb=256", "--max-open-files=64", "--nice=10"},
		"SANDBOX_TEST_ADDR="+addr, "SUB2API_MASTER_KEY=secret", "SUB2API_PLUGIN_KEY=demo")
	t.Logf("report: %+v", r)
	if !strings.Contains(r.DialErr, "operation not permitted") {
		t.Fatalf("tcp dial not blocked: %q", r.DialErr)
	}
	if r.UnixErr != "" {
		t.Fatalf("unix socket blocked: %s", r.UnixErr)
	}
	if r.OOMScoreAdj != "1000" {
		t.Fatalf("oom_score_adj %q", r.OOMScoreAdj)
	}
	if want := min(parentNice(t)+10, 19); r.Nice != want {
		t.Fatalf("nice %d want %d", r.Nice, want)
	}
	if r.NoFile != 64 {
		t.Fatalf("nofile %d", r.NoFile)
	}
	if r.CoreLimit != 0 {
		t.Fatalf("core dumps not disabled: RLIMIT_CORE %d", r.CoreLimit)
	}
	if r.NoNewPrivs != "1" || r.SeccompMode != "2" {
		t.Fatalf("no_new_privs=%q seccomp=%q", r.NoNewPrivs, r.SeccompMode)
	}
	if r.GoMemLimit != strconv.Itoa(256<<20*9/10) {
		t.Fatalf("GOMEMLIMIT %q", r.GoMemLimit)
	}
	if r.HasSecret || r.PluginKey != "demo" {
		t.Fatalf("env filtering: %+v", r)
	}
	if r.IOUringErrno != "operation not permitted" {
		t.Fatalf("io_uring_setup errno %q", r.IOUringErrno)
	}
}

func TestSeccompWithoutStrictNetworkAllowsDial(t *testing.T) {
	addr := tcpListener(t)
	r := runReport(t, []string{"--strict-network=false", "--seccomp=true"}, "SANDBOX_TEST_ADDR="+addr)
	if r.DialErr != "" {
		t.Fatalf("dial failed without strict network: %s", r.DialErr)
	}
	if r.SeccompMode != "2" || r.IOUringErrno != "operation not permitted" {
		t.Fatalf("%+v", r)
	}
}

func TestNoSeccomp(t *testing.T) {
	addr := tcpListener(t)
	r := runReport(t, nil, "SANDBOX_TEST_ADDR="+addr)
	if r.DialErr != "" || r.OOMScoreAdj != "1000" || r.NoNewPrivs != "1" {
		t.Fatalf("%+v", r)
	}
}

func TestLauncherCommandEndToEnd(t *testing.T) {
	addr := tcpListener(t)
	exe, _ := os.Executable()
	l := NewLauncher(LauncherOptions{Executable: exe})
	cmd, err := l.Command(context.Background(), core.LaunchSpec{
		PluginKey: "demo", Version: "0.1.0", BinaryPath: exe, DataDir: t.TempDir(),
		MemoryMB: 128, MaxOpenFiles: 100, StrictNetwork: true, Seccomp: true,
		Env: []string{"SANDBOX_TEST_CHILD=report", "SANDBOX_TEST_ADDR=" + addr},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(cmd.Env, "SUB2API_MASTER_KEY=leak") // as if go-plugin appended the host env
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if r.Strict != "1" || !strings.Contains(r.DialErr, "operation not permitted") || r.NoFile != 100 || r.HasSecret || r.PluginKey != "demo" {
		t.Fatalf("%+v", r)
	}
}

func TestWatchSamplesRSS(t *testing.T) {
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "SANDBOX_TEST_CHILD=alloc")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child not ready: %q %v", line, err)
	}

	l := NewLauncher(LauncherOptions{WatchInterval: 50 * time.Millisecond})
	var mu sync.Mutex
	var evs []core.ResourceEvent
	stop := l.Watch(core.LaunchSpec{PluginKey: "demo", MemoryMB: 16, MaxThreads: 1}, cmd.Process.Pid, func(e core.ResourceEvent) {
		mu.Lock()
		evs = append(evs, e)
		mu.Unlock()
	})
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	var sample, mem, thr bool
	for time.Now().Before(deadline) && !(sample && mem && thr) {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		for _, e := range evs {
			switch e.Kind {
			case "sample":
				if e.RSSBytes > 48<<20 && e.Threads > 1 && e.PID == cmd.Process.Pid {
					sample = true
				}
			case "memory_exceeded":
				mem = true
			case "threads_exceeded":
				thr = true
			}
		}
		mu.Unlock()
	}
	if !sample || !mem || !thr {
		t.Fatalf("sample=%v mem=%v threads=%v events=%d", sample, mem, thr, len(evs))
	}
	stop()
	stop()

	// The watch ends by itself when the process exits.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
