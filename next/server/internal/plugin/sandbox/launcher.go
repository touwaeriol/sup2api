package sandbox

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/protocol"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// DefaultWatchInterval is the resource sampling period.
const DefaultWatchInterval = 5 * time.Second

// Memory watchdog: restart after this many consecutive samples above
// memoryOverFactor x the limit.
const (
	memoryOverFactor  = 1.10
	memoryOverSamples = 3
)

// LauncherOptions configures a Launcher. Zero values use the defaults.
type LauncherOptions struct {
	// Executable is the core binary that provides "plugin-exec"
	// (default os.Executable()).
	Executable string
	// Nice is added to the plugin's nice value (default 10).
	Nice int
	// WatchInterval is the sampling period (default 5 s).
	WatchInterval time.Duration
	// DisableWrap starts plugins directly even on Linux (no sandbox).
	DisableWrap bool
	// Landlock restricts files, unix sockets and signals of wrapped plugins
	// (SUB2API_PLUGIN_LANDLOCK, on by default on Linux; CONTRACTS §43.8).
	Landlock bool
	Logger   *slog.Logger
}

// Launcher implements core.PluginLauncher.
type Launcher struct {
	opts LauncherOptions
	wrap bool
	log  *slog.Logger
}

var _ core.PluginLauncher = (*Launcher)(nil)

// NewLauncher builds a launcher. On Linux plugins are wrapped by
// "plugin-exec"; elsewhere (development mode) they are started directly.
func NewLauncher(opts LauncherOptions) *Launcher {
	if opts.Nice <= 0 {
		opts.Nice = DefaultNice
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = DefaultWatchInterval
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Launcher{opts: opts, wrap: runtime.GOOS == "linux" && !opts.DisableWrap, log: log}
}

// Command builds the plugin command for go-plugin's ClientConfig.Cmd. The
// environment is complete; the runtime should set ClientConfig.SkipHostEnv
// so core secrets are not inherited (plugin-exec also strips SUB2API_*
// variables as a second line of defence).
func (l *Launcher) Command(_ context.Context, spec core.LaunchSpec) (*exec.Cmd, error) {
	if spec.BinaryPath == "" {
		return nil, errors.New("sandbox: empty plugin binary path")
	}
	env := l.commandEnv(spec)
	var cmd *exec.Cmd
	if l.wrap {
		exe := l.opts.Executable
		if exe == "" {
			var err error
			if exe, err = os.Executable(); err != nil {
				return nil, fmt.Errorf("sandbox: resolve core executable: %w", err)
			}
		}
		cmd = exec.Command(exe, execArgs(spec, l.opts.Nice, l.opts.Landlock)...)
	} else {
		cmd = exec.Command(spec.BinaryPath)
		if spec.MemoryMB > 0 {
			env = append(env, "GOMEMLIMIT="+goMemLimit(spec.MemoryMB))
		}
	}
	// A plugin without a data directory runs in its run directory.
	cmd.Dir = cmp.Or(spec.DataDir, spec.RunDir)
	cmd.Env = env
	return cmd, nil
}

// execArgs builds the plugin-exec argument list.
func execArgs(spec core.LaunchSpec, nice int, landlock bool) []string {
	args := []string{
		SubcommandName,
		"--mem-mb=" + strconv.Itoa(max(spec.MemoryMB, 0)),
		"--max-open-files=" + strconv.Itoa(max(spec.MaxOpenFiles, 0)),
		"--max-threads=" + strconv.Itoa(max(spec.MaxThreads, 0)),
		"--strict-network=" + strconv.FormatBool(spec.StrictNetwork),
		"--seccomp=" + strconv.FormatBool(spec.Seccomp),
		"--nice=" + strconv.Itoa(nice),
	}
	if spec.DataDir != "" {
		args = append(args, "--data-dir="+spec.DataDir)
	}
	if spec.Net {
		args = append(args, "--ca-certs")
	}
	if landlock {
		args = append(args, "--landlock")
	}
	var keep []string
	for _, kv := range spec.Env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, protectedEnvPrefix) && !pluginEnvAllowed[k] {
			keep = append(keep, k)
		}
	}
	if len(keep) > 0 {
		args = append(args, "--keep-env="+strings.Join(keep, ","))
	}
	return append(args, "--", spec.BinaryPath)
}

// baseEnvKeys are inherited from the core process; everything else is
// dropped.
var baseEnvKeys = []string{
	"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ", "TMPDIR", "TEMP", "TMP",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "SYSTEMROOT", "SystemRoot", "WINDIR", "ComSpec",
}

// tempEnvKeys name the temporary directory; with a private run directory the
// shared one of the core is not passed on (the runtime sets them in
// spec.Env).
var tempEnvKeys = map[string]bool{"TMPDIR": true, "TEMP": true, "TMP": true}

func (l *Launcher) commandEnv(spec core.LaunchSpec) []string {
	var env []string
	for _, k := range baseEnvKeys {
		if spec.RunDir != "" && tempEnvKeys[k] {
			continue
		}
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	strict := "0"
	if l.wrap && spec.StrictNetwork {
		strict = "1"
	}
	env = append(env,
		protocol.EnvPluginKey+"="+spec.PluginKey,
		protocol.EnvPluginVersion+"="+spec.Version,
		protocol.EnvStrictNetwork+"="+strict,
	)
	if spec.DataDir != "" {
		env = append(env, protocol.EnvDataDir+"="+spec.DataDir)
	}
	if spec.CPU > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(int(math.Ceil(spec.CPU))))
	}
	return append(env, spec.Env...) // explicit entries win (last duplicate)
}

// watchState turns raw samples into ResourceEvents.
type watchState struct {
	spec          core.LaunchSpec
	pid           int
	prevTicks     uint64
	prevAt        time.Time
	over          int
	threadsAlerts bool
}

// clockTicks is USER_HZ, 100 on every mainstream Linux platform.
const clockTicks = 100

func (w *watchState) observe(s procSample, at time.Time, emit func(core.ResourceEvent)) {
	var cpu float64
	if !w.prevAt.IsZero() && s.CPUTicks >= w.prevTicks {
		if el := at.Sub(w.prevAt).Seconds(); el > 0 {
			cpu = float64(s.CPUTicks-w.prevTicks) / clockTicks / el * 100
		}
	}
	w.prevTicks, w.prevAt = s.CPUTicks, at
	base := core.ResourceEvent{PluginKey: w.spec.PluginKey, PID: w.pid, RSSBytes: s.RSSBytes, Threads: s.Threads, CPUPct: cpu}

	ev := base
	ev.Kind = "sample"
	emit(ev)

	if w.spec.MemoryMB > 0 {
		limit := float64(w.spec.MemoryMB) * (1 << 20)
		if float64(s.RSSBytes) > limit*memoryOverFactor {
			w.over++
		} else {
			w.over = 0
		}
		if w.over >= memoryOverSamples {
			ev := base
			ev.Kind = "memory_exceeded"
			ev.Message = fmt.Sprintf("rss %d MiB above %d%% of the %d MiB limit for %d samples",
				s.RSSBytes>>20, int(memoryOverFactor*100), w.spec.MemoryMB, memoryOverSamples)
			emit(ev)
			w.over = 0
		}
	}
	if w.spec.MaxThreads > 0 {
		if s.Threads > w.spec.MaxThreads {
			if !w.threadsAlerts {
				ev := base
				ev.Kind = "threads_exceeded"
				ev.Message = fmt.Sprintf("%d threads, limit %d", s.Threads, w.spec.MaxThreads)
				emit(ev)
				w.threadsAlerts = true
			}
		} else {
			w.threadsAlerts = false
		}
	}
}
