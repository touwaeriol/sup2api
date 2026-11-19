//go:build linux

package sandbox

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The rights and their values as include/uapi/linux/landlock.h defines them
// (torvalds/linux, checked at v7.3): x/sys's constants and the three this
// package defines itself must match.
func TestLandlockConstantsMatchUAPI(t *testing.T) {
	uapi := map[string][2]uint64{
		"LANDLOCK_ACCESS_FS_EXECUTE":           {unix.LANDLOCK_ACCESS_FS_EXECUTE, 1 << 0},
		"LANDLOCK_ACCESS_FS_WRITE_FILE":        {unix.LANDLOCK_ACCESS_FS_WRITE_FILE, 1 << 1},
		"LANDLOCK_ACCESS_FS_READ_FILE":         {unix.LANDLOCK_ACCESS_FS_READ_FILE, 1 << 2},
		"LANDLOCK_ACCESS_FS_READ_DIR":          {unix.LANDLOCK_ACCESS_FS_READ_DIR, 1 << 3},
		"LANDLOCK_ACCESS_FS_REMOVE_DIR":        {unix.LANDLOCK_ACCESS_FS_REMOVE_DIR, 1 << 4},
		"LANDLOCK_ACCESS_FS_REMOVE_FILE":       {unix.LANDLOCK_ACCESS_FS_REMOVE_FILE, 1 << 5},
		"LANDLOCK_ACCESS_FS_MAKE_CHAR":         {unix.LANDLOCK_ACCESS_FS_MAKE_CHAR, 1 << 6},
		"LANDLOCK_ACCESS_FS_MAKE_DIR":          {unix.LANDLOCK_ACCESS_FS_MAKE_DIR, 1 << 7},
		"LANDLOCK_ACCESS_FS_MAKE_REG":          {unix.LANDLOCK_ACCESS_FS_MAKE_REG, 1 << 8},
		"LANDLOCK_ACCESS_FS_MAKE_SOCK":         {unix.LANDLOCK_ACCESS_FS_MAKE_SOCK, 1 << 9},
		"LANDLOCK_ACCESS_FS_MAKE_FIFO":         {unix.LANDLOCK_ACCESS_FS_MAKE_FIFO, 1 << 10},
		"LANDLOCK_ACCESS_FS_MAKE_BLOCK":        {unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK, 1 << 11},
		"LANDLOCK_ACCESS_FS_MAKE_SYM":          {unix.LANDLOCK_ACCESS_FS_MAKE_SYM, 1 << 12},
		"LANDLOCK_ACCESS_FS_REFER":             {unix.LANDLOCK_ACCESS_FS_REFER, 1 << 13},
		"LANDLOCK_ACCESS_FS_TRUNCATE":          {unix.LANDLOCK_ACCESS_FS_TRUNCATE, 1 << 14},
		"LANDLOCK_ACCESS_FS_IOCTL_DEV":         {unix.LANDLOCK_ACCESS_FS_IOCTL_DEV, 1 << 15},
		"LANDLOCK_ACCESS_FS_RESOLVE_UNIX":      {llFSResolveUnix, 1 << 16},
		"LANDLOCK_ACCESS_NET_BIND_TCP":         {unix.LANDLOCK_ACCESS_NET_BIND_TCP, 1 << 0},
		"LANDLOCK_ACCESS_NET_CONNECT_TCP":      {unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, 1 << 1},
		"LANDLOCK_ACCESS_NET_BIND_UDP":         {llNetBindUDP, 1 << 2},
		"LANDLOCK_ACCESS_NET_CONNECT_SEND_UDP": {llNetConnectSendUDP, 1 << 3},
		"LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET":  {unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET, 1 << 0},
		"LANDLOCK_SCOPE_SIGNAL":                {unix.LANDLOCK_SCOPE_SIGNAL, 1 << 1},
	}
	for name, v := range uapi {
		if v[0] != v[1] {
			t.Errorf("%s = %#x, uapi %#x", name, v[0], v[1])
		}
	}
	// struct landlock_ruleset_attr up to ABI 9: handled_access_fs,
	// handled_access_net, scoped (the kernel accepts a shorter struct).
	if n := unsafe.Sizeof(unix.LandlockRulesetAttr{}); n != 24 {
		t.Errorf("ruleset attr is %d bytes", n)
	}
}

// Each ABI handles everything up to it; network rights only with strict
// network; every reported layer is a version of the ruleset.
func TestLandlockHandledPerABI(t *testing.T) {
	fs1 := uint64(1<<13 - 1)
	for _, c := range []struct {
		abi             int
		fs, net, scoped uint64
	}{
		{1, fs1, 0, 0},
		{2, fs1 | 1<<13, 0, 0},
		{3, fs1 | 1<<13 | 1<<14, 0, 0},
		{4, 1<<15 - 1, 3, 0},
		{5, 1<<16 - 1, 3, 0},
		{6, 1<<16 - 1, 3, 3},
		{8, 1<<16 - 1, 3, 3},
		{9, 1<<17 - 1, 3, 3},
		{10, 1<<17 - 1, 15, 3},
		{12, 1<<17 - 1, 15, 3},
	} {
		a := landlockHandled(c.abi, true)
		if a.Access_fs != c.fs || a.Access_net != c.net || a.Scoped != c.scoped {
			t.Errorf("abi %d: %#x %#x %#x", c.abi, a.Access_fs, a.Access_net, a.Scoped)
		}
		if landlockHandled(c.abi, false).Access_net != 0 {
			t.Errorf("abi %d: network handled without strict network", c.abi)
		}
	}
	for _, l := range Layers {
		if !slices.ContainsFunc(landlockABIRights, func(r struct {
			abi             int
			fs, net, scoped uint64
		}) bool {
			return r.abi == l.ABI
		}) {
			t.Errorf("layer %s: ABI %d adds nothing to the ruleset", l.Name, l.ABI)
		}
	}
}

// The allow list: no data directory without "fs.data", no CA certificates
// without "net", never the run directory itself (TMPDIR), the shared
// temporary directory or anything of other plugins.
func TestLandlockRulesAllowList(t *testing.T) {
	env := map[string]string{envUnixSocketDir: "/data/.run/p-1/plugin-dir1", "SSL_CERT_DIR": "/etc/ssl/certs:/e2e-tls", "TMPDIR": "/data/.run/p-1/tmp"}
	paths := func(o *execOptions) map[string]uint64 {
		m := map[string]uint64{}
		for _, r := range landlockRules(o, func(k string) string { return env[k] }) {
			m[r.path] = r.access
		}
		return m
	}
	base := paths(&execOptions{Binary: "/opt/p/bin"})
	want := []string{"/opt/p/bin", "/data/.run/p-1/plugin-dir1", "/dev/null", "/usr/share/zoneinfo", "/etc/localtime"}
	if len(base) != len(want) {
		t.Fatalf("rules %v", base)
	}
	for _, p := range want {
		if _, ok := base[p]; !ok {
			t.Fatalf("%s missing from %v", p, base)
		}
	}
	if base["/opt/p/bin"] != unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_EXECUTE {
		t.Fatal("binary rights")
	}
	if base["/data/.run/p-1/plugin-dir1"]&(unix.LANDLOCK_ACCESS_FS_EXECUTE|unix.LANDLOCK_ACCESS_FS_MAKE_DIR|unix.LANDLOCK_ACCESS_FS_MAKE_FIFO|unix.LANDLOCK_ACCESS_FS_MAKE_SYM) != 0 {
		t.Fatal("socket directory grants more than sockets")
	}
	full := paths(&execOptions{Binary: "/opt/p/bin", DataDir: "/data/p/work", CACerts: true})
	if full["/data/p/work"] != llDataDir || full["/e2e-tls"] != llRead || full["/etc/ssl/certs"] != llRead {
		t.Fatalf("fs.data / net rules %v", full)
	}
	shared := filepath.Clean(os.TempDir())
	for p := range full {
		if p == "/data/.run/p-1" || p == env["TMPDIR"] || p == shared || strings.HasPrefix(shared+"/", p+"/") {
			t.Fatalf("rule grants %s", p)
		}
	}
}

// ---------------------------------------------------------------- processes

// llProbe is what the "landlock" child tried, by name: "" when allowed,
// otherwise the errno name (EACCES, EPERM, ...).
type llProbe map[string]string

// llEnv names the paths the child probes.
const (
	llEnvSecret   = "LL_SECRET"   // a 0600 file of the same user (the gateway's token)
	llEnvTruncate = "LL_TRUNCATE" // a writable file of the same user outside the sandbox
	llEnvOther    = "LL_OTHER"    // a file of another plugin
	llEnvOutside  = "LL_OUTSIDE"  // a writable directory outside the sandbox
	llEnvData     = "LL_DATA"     // the plugin's data directory
	llEnvSockOut  = "LL_SOCK_OUT" // a path-based socket outside the run directory
	llEnvBroker   = "LL_BROKER"   // the core's broker socket in the socket directory
	llEnvAbstract = "LL_ABSTRACT" // an abstract socket of the parent
	llEnvPeerPID  = "LL_PEER_PID" // a process outside the sandbox
	llEnvTCP      = "LL_TCP"      // a TCP listener of the parent
	llEnvUDP      = "LL_UDP"      // a UDP listener of the parent
	llEnvStrict   = "LL_STRICT"   // landlock-net: handle network rights
	llEnvCACert   = "LL_CA_CERT"  // a CA bundle
	llEnvSockDir  = envUnixSocketDir
)

// llZone is read from /usr/share/zoneinfo (the test binary embeds no tzdata).
const llZone = "Asia/Shanghai"

func errnoName(err error) string {
	if err == nil {
		return ""
	}
	var en syscall.Errno
	if errors.As(err, &en) {
		return unix.ErrnoName(en)
	}
	return "error: " + err.Error()
}

func platformChild(mode string) int {
	var r llProbe
	switch mode {
	case "landlock":
		r = landlockProbe()
	case "landlock-net":
		r = landlockNetProbe()
	case "goplugin":
		return goPluginChild()
	default:
		return 99
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	return 0
}

func landlockProbe() llProbe {
	r := llProbe{}
	try := func(name string, f func() error) { r[name] = errnoName(f()) }
	g := os.Getenv
	try("read_secret", func() error { _, err := os.ReadFile(g(llEnvSecret)); return err })
	try("read_other_plugin", func() error { _, err := os.ReadFile(g(llEnvOther)); return err })
	try("list_outside", func() error { _, err := os.ReadDir(g(llEnvOutside)); return err })
	try("write_outside", func() error { return os.WriteFile(filepath.Join(g(llEnvOutside), "x"), nil, 0o600) })
	try("truncate_outside", func() error { return unix.Truncate(g(llEnvTruncate), 0) })
	try("read_proc_parent", func() error { _, err := os.ReadFile("/proc/" + strconv.Itoa(os.Getppid()) + "/environ"); return err })
	try("create_tmpdir", func() error {
		f, err := os.CreateTemp("", "t")
		if err == nil {
			_ = f.Close()
		}
		return err
	})
	try("read_ca", func() error { _, err := os.ReadFile(g(llEnvCACert)); return err })
	try("read_zoneinfo", func() error { _, err := time.LoadLocation(llZone); return err })
	try("dev_null", func() error { return os.WriteFile("/dev/null", []byte("x"), 0) })

	sock := g(llEnvSockDir)
	try("listen_socket_dir", func() error {
		ln, err := net.Listen("unix", filepath.Join(sock, "own.sock"))
		if err != nil {
			return err
		}
		defer ln.Close()
		c, err := net.Dial("unix", ln.Addr().String())
		if err == nil {
			_ = c.Close()
		}
		return err
	})
	try("reserve_socket_name", func() error { // go-plugin's serverListener_unix
		f, err := os.CreateTemp(sock, "plugin")
		if err != nil {
			return err
		}
		_ = f.Close()
		return os.Remove(f.Name())
	})
	try("mkfifo_socket_dir", func() error { return unix.Mkfifo(filepath.Join(sock, "fifo"), 0o600) })
	try("symlink_socket_dir", func() error { return os.Symlink("/etc/passwd", filepath.Join(sock, "ln")) })
	try("mkdir_socket_dir", func() error { return os.Mkdir(filepath.Join(sock, "d"), 0o700) })
	dial := func(addr string) func() error {
		return func() error {
			c, err := net.Dial("unix", addr)
			if err == nil {
				_ = c.Close()
			}
			return err
		}
	}
	try("dial_broker", dial(g(llEnvBroker)))
	try("dial_outside_socket", dial(g(llEnvSockOut)))
	try("dial_abstract", dial(g(llEnvAbstract)))
	peer, _ := strconv.Atoi(g(llEnvPeerPID))
	try("signal_outside", func() error { return unix.Kill(peer, 0) })
	try("signal_self", func() error { return unix.Kill(os.Getpid(), 0) })

	if d := g(llEnvData); d != "" {
		try("data_write", func() error { return os.WriteFile(filepath.Join(d, "f"), []byte("abc"), 0o600) })
		try("data_read", func() error { _, err := os.ReadFile(filepath.Join(d, "f")); return err })
		try("data_mkdir", func() error { return os.MkdirAll(filepath.Join(d, "sub"), 0o700) })
		try("data_rename", func() error { return os.Rename(filepath.Join(d, "f"), filepath.Join(d, "sub", "g")) })
		try("data_truncate", func() error { return os.Truncate(filepath.Join(d, "sub", "g"), 1) })
		try("data_remove", func() error { return os.RemoveAll(filepath.Join(d, "sub")) })
		try("data_mkfifo", func() error { return unix.Mkfifo(filepath.Join(d, "fifo"), 0o600) })
		try("data_symlink", func() error { return os.Symlink("/etc/passwd", filepath.Join(d, "ln")) })
		try("data_socket", func() error {
			ln, err := net.Listen("unix", filepath.Join(d, "s"))
			if err == nil {
				_ = ln.Close()
			}
			return err
		})
	}
	return r
}

// landlockNetProbe applies the ruleset to its own thread only (no seccomp,
// so the Landlock network layer is what answers) and dials the parent.
func landlockNetProbe() llProbe {
	runtime.LockOSThread()
	r := llProbe{}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		r["setup"] = err.Error()
		return r
	}
	exe, _ := os.Executable()
	if err := applyLandlock(&execOptions{Landlock: true, StrictNetwork: os.Getenv(llEnvStrict) == "1", Binary: exe}); err != nil {
		r["setup"] = err.Error()
		return r
	}
	r["dial_tcp"] = errnoName(func() error {
		c, err := net.DialTimeout("tcp", os.Getenv(llEnvTCP), 5*time.Second)
		if err == nil {
			_ = c.Close()
		}
		return err
	}())
	r["send_udp"] = errnoName(func() error {
		c, err := net.Dial("udp", os.Getenv(llEnvUDP))
		if err != nil {
			return err
		}
		defer c.Close()
		_, err = c.Write([]byte("x"))
		return err
	}())
	return r
}

var static struct {
	once sync.Once
	path string
	err  error
}

// staticPlugin is this test binary as a plugin binary: statically linked,
// as sub2api-plugin builds plugins (CGO_ENABLED=0). Landlock grants a
// plugin no dynamic loader or libraries, so a cgo or -race test binary is
// rebuilt without cgo once.
func staticPlugin(t *testing.T) string {
	t.Helper()
	exe, _ := os.Executable()
	if f, err := elf.Open(exe); err == nil {
		dynamic := slices.ContainsFunc(f.Progs, func(p *elf.Prog) bool { return p.Type == elf.PT_INTERP })
		_ = f.Close()
		if !dynamic {
			return exe
		}
	}
	static.once.Do(func() {
		dir, err := os.MkdirTemp("", "llbin")
		if err != nil {
			static.err = err
			return
		}
		static.path = filepath.Join(dir, "plugin.test")
		cmd := exec.Command("go", "test", "-c", "-o", static.path, ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			static.err = fmt.Errorf("%v: %s", err, out)
		}
	})
	if static.err != nil {
		t.Skipf("the test binary is dynamically linked and a static one could not be built: %v", static.err)
	}
	return static.path
}

func cleanupPlatform() {
	if static.path != "" {
		_ = os.RemoveAll(filepath.Dir(static.path))
	}
}

func kernelABI(t *testing.T) int {
	t.Helper()
	abi, err := landlockABI()
	var u unix.Utsname
	_ = unix.Uname(&u)
	t.Logf("kernel %s, Landlock ABI %d (%v)", unix.ByteSliceToString(u.Release[:]), abi, err)
	if err != nil || abi < 1 {
		t.Skipf("no Landlock on this kernel (%v): the file layer needs Linux 5.13", err)
	}
	return abi
}

// llFixture is a node's file system as a plugin process would see it.
type llFixture struct {
	root, runDir, sockDir, data string
	env                         []string
}

func newLLFixture(t *testing.T) *llFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "ll") // short: unix socket paths below it
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	f := &llFixture{root: root, runDir: filepath.Join(root, "run", "me-1")}
	f.sockDir = filepath.Join(f.runDir, "plugin-dir1")
	f.data = filepath.Join(root, "plugins", "me", "work")
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{f.sockDir, filepath.Join(f.runDir, "tmp"), f.data, filepath.Join(root, "outside")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "state", "shell.token"), strings.Repeat("ab", 32))
	write(filepath.Join(root, "outside", "keep"), "keep")
	write(filepath.Join(root, "plugins", "other", "work", "f"), "other")
	write(filepath.Join(f.data, "f"), "mine") // what the plugin wrote while it held "fs.data"
	ca := filepath.Join(root, "ca", "ca.pem")
	write(ca, "-----BEGIN CERTIFICATE-----\n")

	listen := func(addr string) {
		ln, err := net.Listen("unix", addr)
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
	}
	broker := filepath.Join(f.sockDir, "broker.sock")
	outside := filepath.Join(root, "outside.sock")
	abstract := fmt.Sprintf("@sbfs-ll-%d-%d", os.Getpid(), time.Now().UnixNano())
	listen(broker)
	listen(outside)
	listen(abstract)

	exe, _ := os.Executable()
	peer := exec.Command(exe)
	peer.Env = append(os.Environ(), "SANDBOX_TEST_CHILD=sleep")
	if err := peer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Process.Kill(); _ = peer.Wait() })

	f.env = []string{
		"SANDBOX_TEST_CHILD=landlock", "TMPDIR=" + filepath.Join(f.runDir, "tmp"), "SSL_CERT_FILE=" + ca,
		llEnvSockDir + "=" + f.sockDir, llEnvSecret + "=" + filepath.Join(root, "state", "shell.token"),
		llEnvTruncate + "=" + filepath.Join(root, "outside", "keep"),
		llEnvOther + "=" + filepath.Join(root, "plugins", "other", "work", "f"), llEnvOutside + "=" + filepath.Join(root, "outside"),
		llEnvBroker + "=" + broker, llEnvSockOut + "=" + outside, llEnvAbstract + "=" + abstract,
		llEnvPeerPID + "=" + strconv.Itoa(peer.Process.Pid), llEnvCACert + "=" + ca,
	}
	return f
}

// run starts the "landlock" child through plugin-exec as the launcher
// would (strict network, seccomp, Landlock) with the extra flags.
func (f *llFixture) run(t *testing.T, flags ...string) llProbe {
	t.Helper()
	exe, _ := os.Executable()
	args := append([]string{SubcommandName, "--strict-network=true", "--seccomp=true", "--landlock"}, flags...)
	cmd := exec.Command(exe, append(args, "--", staticPlugin(t))...)
	cmd.Env = append(os.Environ(), f.env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("plugin-exec: %v: %s", err, stderr.String())
	}
	var r llProbe
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	t.Logf("probe: %v", r)
	return r
}

// expect checks each probe: allowed ("") or refused with errno.
func expect(t *testing.T, r llProbe, want map[string]string) {
	t.Helper()
	for k, v := range want {
		got, ok := r[k]
		if !ok {
			t.Errorf("%s: not probed", k)
			continue
		}
		if got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}
}

// What a plugin without "fs.data" and "net" can reach: its socket
// directory, /dev/null and the time zones; no other file, not even of its
// own user. The socket and signal scopes and truncation are checked per
// kernel: below their ABI the gap they close is still open.
func TestLandlockPluginWithoutFilePermission(t *testing.T) {
	abi := kernelABI(t)
	f := newLLFixture(t)
	r := f.run(t)
	want := map[string]string{
		"read_secret": "EACCES", "read_other_plugin": "EACCES", "list_outside": "EACCES", "write_outside": "EACCES",
		"read_proc_parent": "EACCES", "create_tmpdir": "EACCES", "read_ca": "EACCES",
		"read_zoneinfo": "", "dev_null": "",
		"listen_socket_dir": "", "reserve_socket_name": "", "dial_broker": "",
		"mkfifo_socket_dir": "EACCES", "symlink_socket_dir": "EACCES", "mkdir_socket_dir": "EACCES",
		"signal_self": "",
	}
	gap := func(name string, minABI int, refused string) {
		if abi >= minABI {
			want[name] = refused
		} else {
			want[name] = ""
			t.Logf("%s allowed: Landlock ABI %d < %d", name, abi, minABI)
		}
	}
	gap("truncate_outside", 3, "EACCES")
	gap("dial_abstract", 6, "EPERM")
	gap("signal_outside", 6, "EPERM")
	gap("dial_outside_socket", 9, "EACCES")
	if _, err := time.LoadLocation(llZone); err != nil {
		delete(want, "read_zoneinfo") // no time zone files on this system
	}
	expect(t, r, want)
	if b, _ := os.ReadFile(filepath.Join(f.root, "outside", "keep")); abi >= 3 && string(b) != "keep" {
		t.Fatal("the plugin truncated a file outside its sandbox")
	}
	if abi < 6 {
		t.Log("SKIP part: abstract unix socket and signal scopes need Linux 6.12 (Landlock ABI 6)")
	}
	if abi < 9 {
		t.Log("SKIP part: path-based unix socket restriction needs Linux 7.1 (Landlock ABI 9)")
	}
}

// With "fs.data" the plugin reads and writes its data directory (files,
// directories, moves, truncation) and nothing else; with "net" it reads the
// CA certificates.
func TestLandlockPluginWithFilePermission(t *testing.T) {
	abi := kernelABI(t)
	f := newLLFixture(t)
	f.env = append(f.env, llEnvData+"="+f.data)
	r := f.run(t, "--data-dir="+f.data, "--ca-certs")
	rename := ""
	if abi < 2 {
		rename = "EXDEV" // Landlock ABI 1 refuses every move between directories
	}
	expect(t, r, map[string]string{
		"data_write": "", "data_read": "", "data_mkdir": "", "data_rename": rename, "data_truncate": "", "data_remove": "",
		"data_mkfifo": "EACCES", "data_symlink": "EACCES", "data_socket": "EACCES",
		"read_ca": "", "read_secret": "EACCES", "read_other_plugin": "EACCES", "write_outside": "EACCES",
	})

	// The same directory without the grant: nothing.
	f2 := newLLFixture(t)
	f2.env = append(f2.env, llEnvData+"="+f2.data)
	r = f2.run(t)
	expect(t, r, map[string]string{"data_write": "EACCES", "data_read": "EACCES", "data_mkdir": "EACCES"})
}

// The network layer (second line below seccomp): with strict network every
// TCP port, and from ABI 10 every UDP port, is refused; without strict
// network nothing is.
func TestLandlockNetworkLayer(t *testing.T) {
	abi := kernelABI(t)
	tcp := tcpListener(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	run := func(strict string) llProbe {
		exe, _ := os.Executable()
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "SANDBOX_TEST_CHILD=landlock-net", llEnvStrict+"="+strict, llEnvTCP+"="+tcp, llEnvUDP+"="+udp.LocalAddr().String())
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var r llProbe
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		t.Logf("strict=%s: %v", strict, r)
		return r
	}
	want := map[string]string{"dial_tcp": "", "send_udp": ""}
	if abi >= 4 {
		want["dial_tcp"] = "EACCES"
	} else {
		t.Log("SKIP part: the TCP layer needs Linux 6.7 (Landlock ABI 4)")
	}
	if abi >= 10 {
		want["send_udp"] = "EACCES"
	} else {
		t.Log("SKIP part: the UDP layer needs Linux 7.2 (Landlock ABI 10)")
	}
	expect(t, run("1"), want)
	expect(t, run("0"), map[string]string{"dial_tcp": "", "send_udp": ""})
}

// ProbeStatus matches the kernel: every layer up to its ABI is active.
func TestProbeStatusMatchesKernel(t *testing.T) {
	abi, _ := landlockABI()
	st := ProbeStatus(true, true, true)
	if st.ABI != abi || len(st.Active)+len(st.Missing) != len(Layers) {
		t.Fatalf("%+v", st)
	}
	for _, l := range Layers {
		if slices.Contains(st.Active, l.Name) != (abi >= l.ABI) {
			t.Fatalf("layer %s: %+v", l.Name, st)
		}
	}
	off := ProbeStatus(true, false, true)
	if len(off.Active) != 0 || len(off.Missing) != len(Layers) {
		t.Fatalf("disabled: %+v", off)
	}
	if lax := ProbeStatus(true, true, false); slices.Contains(lax.Active, "tcp") || slices.Contains(lax.Missing, "tcp") {
		t.Fatalf("tcp reported without strict network: %+v", lax)
	}
}
