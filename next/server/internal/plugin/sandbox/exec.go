// Package sandbox starts plugin processes with resource limits and, on
// Linux, a seccomp filter that blocks direct network access (strict mode).
//
// The launcher re-executes the core binary as the hidden subcommand
// "sub2api plugin-exec", which applies the limits to itself and then
// execve()s the plugin binary, so the plugin inherits every restriction and
// keeps the PID that go-plugin manages.
package sandbox

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
)

// SubcommandName is the hidden subcommand dispatched by main.
const SubcommandName = "plugin-exec"

// DefaultNice is added to the plugin's nice value.
const DefaultNice = 10

// OOMScoreAdj makes the kernel kill plugins before the core under memory
// pressure.
const OOMScoreAdj = 1000

// Exit codes of plugin-exec itself (the plugin's own code is passed through).
const (
	exitUsage    = 2
	exitSetup    = 125
	exitExecFail = 126
	exitNotFound = 127
)

type execOptions struct {
	MemMB         int
	MaxOpenFiles  int
	MaxThreads    int // informational: enforced by the watchdog, not the kernel
	StrictNetwork bool
	Seccomp       bool
	Nice          int
	KeepEnv       []string
	Binary        string
	Args          []string
	DataDir       string // data directory of a plugin granted "fs.data" (Landlock grants it)
	CACerts       bool   // the plugin is granted "net": Landlock lets it read the CA certificates
	Landlock      bool   // restrict files, sockets and signals with Landlock (Linux)
}

func parseExecArgs(args []string, stderr io.Writer) (*execOptions, error) {
	fs := flag.NewFlagSet(SubcommandName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := &execOptions{}
	var keep string
	fs.IntVar(&o.MemMB, "mem-mb", 0, "memory limit in MiB (sets GOMEMLIMIT to 90%)")
	fs.IntVar(&o.MaxOpenFiles, "max-open-files", 0, "RLIMIT_NOFILE")
	fs.IntVar(&o.MaxThreads, "max-threads", 0, "thread budget (watchdog only)")
	fs.BoolVar(&o.StrictNetwork, "strict-network", false, "deny non-unix sockets (Linux)")
	fs.BoolVar(&o.Seccomp, "seccomp", false, "install the seccomp filter (Linux)")
	fs.IntVar(&o.Nice, "nice", DefaultNice, "added to the nice value")
	fs.StringVar(&keep, "keep-env", "", "comma separated SUB2API_* variables passed to the plugin")
	fs.StringVar(&o.DataDir, "data-dir", "", "data directory the plugin may read and write (Landlock)")
	fs.BoolVar(&o.CACerts, "ca-certs", false, "let the plugin read the CA certificates (Landlock)")
	fs.BoolVar(&o.Landlock, "landlock", false, "restrict files, sockets and signals with Landlock (Linux 5.13+)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	rest := fs.Args()
	if len(rest) == 0 || rest[0] == "" {
		return nil, errors.New("missing plugin binary (usage: plugin-exec [flags] -- <binary> [args...])")
	}
	if o.MemMB < 0 || o.MaxOpenFiles < 0 || o.MaxThreads < 0 || o.Nice < 0 {
		return nil, errors.New("limits must not be negative")
	}
	for _, k := range strings.Split(keep, ",") {
		if k = strings.TrimSpace(k); k != "" {
			o.KeepEnv = append(o.KeepEnv, k)
		}
	}
	o.Binary, o.Args = rest[0], rest[1:]
	return o, nil
}

// RunExec implements "sub2api plugin-exec [flags] -- <binary> [args...]".
// On Linux it only returns on failure (the process image is replaced by the
// plugin). Elsewhere it runs the plugin as a child and returns its exit code.
func RunExec(args []string) int {
	o, err := parseExecArgs(args, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin-exec:", err)
		return exitUsage
	}
	return runExec(o)
}

// protectedEnvPrefix marks core configuration (DSNs, master key, secrets)
// that must never reach a plugin.
const protectedEnvPrefix = "SUB2API_"

// pluginEnvAllowed are SUB2API_* variables every plugin may see.
var pluginEnvAllowed = map[string]bool{
	protocol.Handshake.MagicCookieKey: true,
	protocol.EnvPluginKey:             true,
	protocol.EnvPluginVersion:         true,
	protocol.EnvStrictNetwork:         true,
	protocol.EnvDataDir:               true,
}

// buildEnv filters the inherited environment and adds the Go runtime
// limits. Later duplicates win, as with os/exec.
func buildEnv(environ []string, o *execOptions) []string {
	keep := map[string]bool{}
	for _, k := range o.KeepEnv {
		keep[k] = true
	}
	idx := map[string]int{}
	out := make([]string, 0, len(environ)+2)
	set := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			return
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		if strings.HasPrefix(k, protectedEnvPrefix) && !pluginEnvAllowed[k] && !keep[k] {
			continue
		}
		set(kv)
	}
	if o.MemMB > 0 {
		set("GOMEMLIMIT=" + goMemLimit(o.MemMB))
	}
	return out
}

// goMemLimit returns 90% of memMB as a GOMEMLIMIT value.
func goMemLimit(memMB int) string {
	return strconv.FormatInt(int64(memMB)*(1<<20)*9/10, 10)
}
