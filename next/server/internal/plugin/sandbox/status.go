package sandbox

import (
	"log/slog"
	"slices"
	"strconv"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Layer is one Landlock restriction of the plugin sandbox and the kernel it
// needs (CONTRACTS §43.8).
type Layer struct {
	Name    string
	ABI     int    // Landlock ABI version that brings it
	Kernel  string // first Linux release with that ABI
	Network bool   // only enforced with strict network
	// Gap is what a plugin can do while the layer is missing.
	Gap string
}

// Layers are the Landlock layers in ABI order.
var Layers = []Layer{
	{Name: "fs", ABI: 1, Kernel: "5.13", Gap: "plugins can read and write every file the core's user can, the gateway's management token included"},
	{Name: "fs_truncate", ABI: 3, Kernel: "6.2", Gap: "plugins can truncate every file the core's user can write"},
	{Name: "tcp", ABI: 4, Kernel: "6.7", Network: true, Gap: "only seccomp keeps plugins from TCP sockets"},
	{Name: "scope", ABI: 6, Kernel: "6.12", Gap: "plugins can connect to abstract unix sockets and signal processes outside their sandbox"},
	{Name: "unix", ABI: 9, Kernel: "7.1", Gap: "plugins can connect to path-based unix sockets outside their run directory"},
	{Name: "udp", ABI: 10, Kernel: "7.2", Network: true, Gap: "only seccomp keeps plugins from UDP sockets"},
}

// ProbeStatus reports what the sandbox enforces on this node: wrapped is
// whether plugins run under plugin-exec, landlock and strictNetwork the
// configuration. The kernel is asked once; plugin-exec applies the same
// ruleset (landlockHandled) on the same kernel.
func ProbeStatus(wrapped, landlock, strictNetwork bool) core.PluginSandboxStatus {
	st := core.PluginSandboxStatus{Wrapped: wrapped, Landlock: landlock, Active: []string{}, Missing: []string{}}
	if abi, err := landlockABI(); err == nil {
		st.ABI = abi
	}
	for _, l := range Layers {
		switch {
		case l.Network && !strictNetwork:
		case wrapped && landlock && st.ABI >= l.ABI:
			st.Active = append(st.Active, l.Name)
		default:
			st.Missing = append(st.Missing, l.Name)
		}
	}
	return st
}

// LogStatus logs st once per node start: INFO when every layer is in force,
// otherwise one WARN per missing layer with what plugins can do and the
// kernel it needs. Plugins start either way.
func LogStatus(log *slog.Logger, st core.PluginSandboxStatus) {
	if len(st.Missing) == 0 {
		log.Info("plugin sandbox: every Landlock layer in force", "landlock_abi", st.ABI, "layers", st.Active)
		return
	}
	if len(st.Active) > 0 {
		log.Info("plugin sandbox: Landlock layers in force", "landlock_abi", st.ABI, "layers", st.Active)
	}
	reason := "kernel"
	switch {
	case !st.Wrapped:
		reason = "plugins are not sandboxed (development mode or not Linux)"
	case !st.Landlock:
		reason = "Landlock disabled by SUB2API_PLUGIN_LANDLOCK=false"
	}
	for _, l := range Layers {
		if !slices.Contains(st.Missing, l.Name) {
			continue
		}
		attrs := []any{"layer", l.Name, "consequence", l.Gap, "landlock_abi", st.ABI}
		if reason == "kernel" {
			attrs = append(attrs, "needs", "Linux "+l.Kernel+" (Landlock ABI "+strconv.Itoa(l.ABI)+")")
		} else {
			attrs = append(attrs, "reason", reason)
		}
		log.Warn("plugin sandbox: Landlock layer not in force", attrs...)
	}
}
