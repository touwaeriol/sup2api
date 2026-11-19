package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// sdkPkg is the package whose build-time variables carry key and version.
const sdkPkg = "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"

// defaultPlatforms are the production targets (ARCHITECTURE 5.1).
var defaultPlatforms = []string{"linux/amd64", "linux/arm64"}

// binaryName returns the runtime binary name for an OS. The manifest
// template is runtimes/{os}-{arch}/plugin; Windows (dev mode only) needs the
// .exe suffix to be executable.
func binaryName(goos string) string {
	if goos == "windows" {
		return "plugin.exe"
	}
	return "plugin"
}

// cmdBuild cross-compiles the plugin in --dir into
// <out>/runtimes/{os}-{arch}/plugin, injecting key and version from the
// effective manifest via -ldflags -X.
func cmdBuild(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("build", stderr)
	dir := fs.String("dir", ".", "plugin directory (Go main package + manifest.json)")
	out := fs.String("out", "", "output directory (default: --dir); binaries go to <out>/runtimes/{os}-{arch}/")
	dev := fs.Bool("dev", false, "also build for the local platform ("+runtime.GOOS+"/"+runtime.GOARCH+")")
	tags := fs.String("tags", "", "comma-separated Go build tags")
	overlay := fs.String("overlay", "", "overlay directory (manifest.json / manifest.patch.json override)")
	platforms := fs.String("platforms", "", "comma-separated os/arch list (default linux/amd64,linux/arm64)")
	pkg := fs.String("pkg", ".", "main package, relative to --dir")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	ov, err := resolveOverlay(absDir, *overlay)
	if err != nil {
		return err
	}
	_, m, err := effectiveManifest(absDir, ov)
	if err != nil {
		return err
	}
	outDir := *out
	if outDir == "" {
		outDir = absDir
	}
	if outDir, err = filepath.Abs(outDir); err != nil {
		return err
	}
	targets := defaultPlatforms
	if *platforms != "" {
		targets = splitList(*platforms)
	}
	if *dev {
		local := runtime.GOOS + "/" + runtime.GOARCH
		found := false
		for _, t := range targets {
			found = found || t == local
		}
		if !found {
			targets = append(append([]string{}, targets...), local)
		}
	}
	bins, err := buildRuntimes(absDir, outDir, m.Key, m.Version, targets, splitList(*tags), *pkg, stderr)
	for _, bin := range bins {
		fmt.Fprintln(stdout, bin)
	}
	return err
}

// buildRuntimes compiles the main package pkg of dir for every os/arch
// target into <outDir>/runtimes/{os}-{arch}/, with key and version injected.
// It returns the binaries built so far.
func buildRuntimes(dir, outDir, key, version string, targets, tags []string, pkg string, stderr io.Writer) ([]string, error) {
	ldflags := fmt.Sprintf("-s -w -X %s.buildKey=%s -X %s.buildVersion=%s", sdkPkg, key, sdkPkg, version)
	var bins []string
	for _, t := range targets {
		goos, goarch, ok := strings.Cut(t, "/")
		if !ok || goos == "" || goarch == "" {
			return bins, fmt.Errorf("bad platform %q (want os/arch)", t)
		}
		bin := filepath.Join(outDir, "runtimes", goos+"-"+goarch, binaryName(goos))
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			return bins, err
		}
		goArgs := []string{"build", "-trimpath", "-ldflags", ldflags, "-o", bin}
		if len(tags) > 0 {
			goArgs = append(goArgs, "-tags", strings.Join(tags, ","))
		}
		goArgs = append(goArgs, pkg)
		cmd := exec.Command("go", goArgs...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stdout = stderr
		cmd.Stderr = stderr
		fmt.Fprintf(stderr, "building %s %s for %s/%s\n", key, version, goos, goarch)
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return bins, fmt.Errorf("go build for %s failed", t)
			}
			return bins, err
		}
		bins = append(bins, bin)
	}
	return bins, nil
}
