package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// cmdDev runs a local core with the plugin under development installed, and
// rebuilds and reinstalls it when its files change.
//
// The core is `sub2api dev`: PostgreSQL run from Go, Redis in memory, secrets
// and an admin account generated once. The plugin reaches it the way
// built-in plugins do: each build is packed as <version>-dev.<time> (newer
// than every earlier build), signed with a local key the core trusts as
// official, and put in the core's built-in directory; the core installs,
// approves and enables or upgrades it at startup. A change restarts the core,
// which takes a second or two: the database keeps running.
func cmdDev(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("dev", stderr)
	dir := fs.String("dir", ".", "plugin directory")
	var with multiFlag
	fs.Var(&with, "with", "another plugin to install: a plugin directory (built and watched too) or a .s2plugin file; repeatable")
	addr := fs.String("addr", "127.0.0.1:8080", "core HTTP listen address")
	core := fs.String("core", "", "sub2api binary (default: built from the sup2api repository around --dir, else sub2api on PATH)")
	state := fs.String("state", "", "state directory shared with `sub2api dev` (default <user cache>/sub2api-dev)")
	reset := fs.Bool("reset", false, "start from an empty database and plugin data")
	noWatch := fs.Bool("no-watch", false, "do not rebuild on changes")
	overlay := fs.String("overlay", "", "overlay directory of --dir (see pack)")
	tags := fs.String("tags", "", "comma-separated Go build tags")
	allowMissingUI := fs.Bool("allow-missing-ui", false, "pack even when ui/native/dist is missing")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	stateDir := *state
	if stateDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		stateDir = filepath.Join(base, "sub2api-dev")
	}
	stateDir, err := filepath.Abs(stateDir)
	if err != nil {
		return err
	}
	d := &devSession{
		state:          stateDir,
		builtin:        filepath.Join(stateDir, "builtin"),
		work:           filepath.Join(stateDir, "work"),
		addr:           *addr,
		tags:           splitList(*tags),
		allowMissingUI: *allowMissingUI,
		stderr:         stderr,
	}
	main, err := newDevSource(*dir, *overlay)
	if err != nil {
		return err
	}
	d.sources = append(d.sources, main)
	for _, w := range with {
		s, err := newDevSource(w, "")
		if err != nil {
			return err
		}
		d.sources = append(d.sources, s)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if d.core, err = resolveCore(ctx, *core, main.dir, stateDir, stderr); err != nil {
		return err
	}
	if err := d.prepare(); err != nil {
		return err
	}
	if err := d.start(*reset); err != nil {
		return err
	}
	defer d.stopCore()
	d.waitReady(ctx, stdout)
	if *noWatch {
		<-ctx.Done()
		return nil
	}
	d.watch(ctx, stdout)
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// devSource is one plugin of the session: a directory built from source or
// a ready package.
type devSource struct {
	dir     string // plugin directory, or "" for a package
	pkg     string // .s2plugin file, or "" for a directory
	overlay string
	key     string
}

func newDevSource(path, overlay string) (*devSource, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		if !strings.HasSuffix(abs, PackageExt) {
			return nil, fmt.Errorf("%s: want a plugin directory or a %s file", path, PackageExt)
		}
		return &devSource{pkg: abs}, nil
	}
	ov, err := resolveOverlay(abs, overlay)
	if err != nil {
		return nil, err
	}
	if ov != "" {
		if ov, err = filepath.Abs(ov); err != nil {
			return nil, err
		}
	}
	_, m, err := effectiveManifest(abs, ov)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &devSource{dir: abs, overlay: ov, key: m.Key}, nil
}

type devSession struct {
	state, builtin, work, addr string
	core                       []string // command line of `sub2api`, without "dev"
	sources                    []*devSource
	tags                       []string
	allowMissingUI             bool
	stderr                     io.Writer

	officialKeys []string // keyId=base64 of the publishers seen so far
	versions     map[string]string

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	exited  chan struct{}
	exitErr error
}

// prepare builds every source and replaces the packages in the built-in
// directory. On error the directory is left as it was.
func (d *devSession) prepare() error {
	stamp := time.Now().UTC().Format("20060102150405")
	staging := filepath.Join(d.work, "staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	versions := map[string]string{}
	seen := map[string]bool{}
	for _, s := range d.sources {
		files, m, err := d.packSource(s, stamp)
		if err != nil {
			return err
		}
		if seen[m.Key] {
			return fmt.Errorf("plugin %s is given twice", m.Key)
		}
		seen[m.Key] = true
		if err := d.sign(files, m); err != nil {
			return err
		}
		if err := writePackage(filepath.Join(staging, m.Key+PackageExt), files); err != nil {
			return err
		}
		versions[m.Key] = m.Version
	}
	if err := os.MkdirAll(d.builtin, 0o755); err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(d.builtin, "*"+PackageExt))
	if err != nil {
		return err
	}
	for _, p := range old {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	for key := range versions {
		if err := os.Rename(filepath.Join(staging, key+PackageExt), filepath.Join(d.builtin, key+PackageExt)); err != nil {
			return err
		}
	}
	d.versions = versions
	return nil
}

// packSource builds a directory source at a dev version, or re-reads a
// package source, and returns its files without signature.
func (d *devSession) packSource(s *devSource, stamp string) (map[string][]byte, *manifest.Manifest, error) {
	if s.pkg != "" {
		files, err := readPackage(s.pkg)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", s.pkg, err)
		}
		delete(files, pkgsig.SignatureFile)
		m, err := parseManifest(files[pkgsig.ManifestFile])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", s.pkg, err)
		}
		return files, m, nil
	}
	raw, m, err := effectiveManifest(s.dir, s.overlay)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", s.dir, err)
	}
	version, err := devVersion(m.Version, stamp)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: manifest version %q: %w", s.dir, m.Version, err)
	}
	// The dev version goes into a full manifest in a generated overlay, next
	// to the files of the plugin's own overlay.
	ov := filepath.Join(d.work, m.Key, "overlay")
	if err := os.RemoveAll(ov); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(ov, 0o755); err != nil {
		return nil, nil, err
	}
	if s.overlay != "" {
		if err := copyTree(s.overlay, ov, func(rel string) bool { return rel == overlayManifest || rel == overlayPatch }); err != nil {
			return nil, nil, err
		}
	}
	raw, err = setManifestVersion(raw, version)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(ov, overlayManifest), raw, 0o644); err != nil {
		return nil, nil, err
	}
	out := filepath.Join(d.work, m.Key)
	if err := os.RemoveAll(filepath.Join(out, "runtimes")); err != nil {
		return nil, nil, err
	}
	local := runtime.GOOS + "/" + runtime.GOARCH
	if _, err := buildRuntimes(s.dir, out, m.Key, version, []string{local}, d.tags, ".", d.stderr); err != nil {
		return nil, nil, err
	}
	files, pm, warnings, err := collectPackage(s.dir, out, ov, d.allowMissingUI)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", s.dir, err)
	}
	for _, w := range warnings {
		// Only the local platform is built in dev mode.
		if !strings.HasPrefix(w, "runtime binary ") {
			fmt.Fprintln(d.stderr, "warning:", w)
		}
	}
	return files, pm, nil
}

// devVersion is a version newer than v and than every earlier dev build of
// v: 1.2.3 -> 1.2.4-dev.<stamp> (still older than the next release 1.2.4);
// a pre-release 1.2.3-rc.1 -> 1.2.3-rc.1.dev.<stamp>.
func devVersion(v, stamp string) (string, error) {
	sv, err := semver.StrictNewVersion(v)
	if err != nil {
		return "", err
	}
	if sv.Prerelease() != "" {
		return fmt.Sprintf("%d.%d.%d-%s.dev.%s", sv.Major(), sv.Minor(), sv.Patch(), sv.Prerelease(), stamp), nil
	}
	return fmt.Sprintf("%d.%d.%d-dev.%s", sv.Major(), sv.Minor(), sv.Patch()+1, stamp), nil
}

// setManifestVersion rewrites "version", keeping numbers as written.
func setManifestVersion(raw []byte, version string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	doc["version"] = version
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var keyIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sign signs files with the local dev key of the manifest's publisher. The
// core registers an official key to exactly one publisher, so every
// publisher gets its own key; it is generated on first use.
func (d *devSession) sign(files map[string][]byte, m *manifest.Manifest) error {
	if m.Publisher == "" {
		return fmt.Errorf("%s: manifest.publisher is empty; dev packages are signed for their publisher", m.Key)
	}
	keyID := "dev-" + strings.Trim(keyIDUnsafe.ReplaceAllString(m.Publisher, "-"), "-")
	if !keyIDPattern.MatchString(keyID) || len(keyID) > 100 {
		sum := sha256.Sum256([]byte(m.Publisher))
		keyID = fmt.Sprintf("dev-%x", sum[:8])
	}
	keyDir := filepath.Join(d.state, "keys")
	privPath := filepath.Join(keyDir, keyID+".key")
	priv, err := loadPrivateKey(privPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(keyDir, 0o700); err != nil {
			return err
		}
		_, priv, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.WriteFile(privPath, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	sig, err := pkgsig.Sign(files, m.Publisher, keyID, priv)
	if err != nil {
		return fmt.Errorf("%s: %w", m.Key, err)
	}
	b, err := json.MarshalIndent(sig, "", "  ")
	if err != nil {
		return err
	}
	files[pkgsig.SignatureFile] = append(b, '\n')
	trust := keyID + "=" + base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	for _, k := range d.officialKeys {
		if k == trust {
			return nil
		}
	}
	d.officialKeys = append(d.officialKeys, trust)
	return nil
}

// resolveCore finds the core: --core, else a build of server/cmd/sub2api of
// the sup2api repository around the plugin, else sub2api on PATH.
func resolveCore(ctx context.Context, flagCore, pluginDir, stateDir string, stderr io.Writer) ([]string, error) {
	if flagCore != "" {
		p, err := exec.LookPath(flagCore)
		if err != nil {
			return nil, fmt.Errorf("--core: %w", err)
		}
		return []string{p}, nil
	}
	for _, start := range []string{pluginDir, mustGetwd()} {
		if root := findRepo(start); root != "" {
			bin := filepath.Join(stateDir, "bin", "sub2api")
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			fmt.Fprintf(stderr, "building the core from %s\n", root)
			cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./server/cmd/sub2api")
			cmd.Dir = root
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				return nil, fmt.Errorf("build the core: %w", err)
			}
			return []string{bin}, nil
		}
	}
	if p, err := exec.LookPath("sub2api"); err == nil {
		return []string{p}, nil
	}
	return nil, errors.New("no core found: run inside the sup2api repository, put sub2api on PATH, or pass --core <path to sub2api>")
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// findRepo returns the directory above start holding go.work and
// server/cmd/sub2api (the next/ directory of the sup2api repository).
func findRepo(start string) string {
	for d := start; d != ""; {
		if _, err := os.Stat(filepath.Join(d, "server", "cmd", "sub2api", "main.go")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "go.work")); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
	return ""
}

// start launches `sub2api dev`. Its stdin is a pipe: closing it stops the
// core gracefully on every OS, and the core also stops when this process
// dies.
func (d *devSession) start(reset bool) error {
	args := append(append([]string{}, d.core[1:]...), "dev", "--addr", d.addr, "--state", d.state,
		"--builtin-dir", d.builtin, "--exit-on-stdin-close")
	if reset {
		args = append(args, "--reset")
	}
	cmd := exec.Command(d.core[0], args...)
	keys := strings.Join(d.officialKeys, ",")
	if have := os.Getenv("SUB2API_PLUGIN_OFFICIAL_KEYS"); have != "" {
		keys = have + "," + keys
	}
	cmd.Env = append(os.Environ(), "SUB2API_PLUGIN_OFFICIAL_KEYS="+keys)
	cmd.Stdout = d.stderr
	cmd.Stderr = &builtinErrorTap{w: d.stderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start core: %w", err)
	}
	exited := make(chan struct{})
	d.mu.Lock()
	d.cmd, d.stdin, d.exited = cmd, stdin, exited
	d.mu.Unlock()
	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		d.exitErr = err
		d.mu.Unlock()
		close(exited)
	}()
	return nil
}

// stopCore closes the core's stdin and waits for it to stop.
func (d *devSession) stopCore() {
	d.mu.Lock()
	cmd, stdin, exited := d.cmd, d.stdin, d.exited
	d.cmd = nil
	d.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = stdin.Close()
	select {
	case <-exited:
	case <-time.After(45 * time.Second):
		fmt.Fprintln(d.stderr, "core did not stop in 45s; killing it")
		_ = cmd.Process.Kill()
		<-exited
	}
}

// waitReady polls /healthz, which reports ready once the built-in plugins
// run at least at the versions in the built-in directory.
func (d *devSession) waitReady(ctx context.Context, stdout io.Writer) bool {
	d.mu.Lock()
	exited := d.exited
	d.mu.Unlock()
	url := "http://" + d.addr + "/" + manifest.RouteHealthz
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	began := time.Now()
	hinted := false
	for {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				keys := make([]string, 0, len(d.versions))
				for k := range d.versions {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				fmt.Fprintf(stdout, "\nready in %s: http://%s\n", time.Since(began).Round(100*time.Millisecond), d.addr)
				for _, k := range keys {
					fmt.Fprintf(stdout, "  %s %s\n", k, d.versions[k])
				}
				fmt.Fprintln(stdout)
				return true
			}
		}
		if !hinted && time.Since(began) > 90*time.Second {
			hinted = true
			fmt.Fprintln(d.stderr, "the core is not ready after 90s: look for \"builtin plugin\" errors above (a package the core refuses never becomes ready)")
		}
		select {
		case <-ctx.Done():
			return false
		case <-exited:
			d.mu.Lock()
			err := d.exitErr
			d.mu.Unlock()
			fmt.Fprintf(d.stderr, "core exited: %v\n", err)
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// builtinErrorTap passes the core's log through and points at failed
// plugin installs, which otherwise only show as a core that never gets
// ready.
type builtinErrorTap struct {
	w   io.Writer
	buf []byte
}

func (t *builtinErrorTap) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := t.buf[:i+1]
		if _, err := t.w.Write(line); err != nil {
			return 0, err
		}
		if bytes.Contains(line, []byte("level=ERROR")) && bytes.Contains(line, []byte("builtin plugin")) {
			fmt.Fprintln(t.w, ">>> the core refused a plugin package: fix the error above and save to retry")
		}
		t.buf = t.buf[i+1:]
	}
	return len(p), nil
}

// watch rebuilds on changes. A failed build keeps the running core.
func (d *devSession) watch(ctx context.Context, stdout io.Writer) {
	fmt.Fprintln(d.stderr, "watching for changes (Ctrl+C to stop)")
	last := d.fingerprint()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(700 * time.Millisecond):
		}
		cur := d.fingerprint()
		if cur == last {
			continue
		}
		// Wait for the editor or generator to finish writing.
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
			next := d.fingerprint()
			if next == cur {
				break
			}
			cur = next
		}
		last = cur
		fmt.Fprintln(d.stderr, "\nchange detected; rebuilding")
		if err := d.prepare(); err != nil {
			fmt.Fprintf(d.stderr, "build failed, the core keeps the previous build: %v\n", err)
			continue
		}
		d.stopCore()
		if err := d.start(false); err != nil {
			fmt.Fprintln(d.stderr, err)
			continue
		}
		d.waitReady(ctx, stdout)
	}
}

// fingerprint summarizes the watched files of every source: names, sizes
// and modification times.
func (d *devSession) fingerprint() string {
	h := sha256.New()
	for _, s := range d.sources {
		roots := []string{s.dir}
		if s.pkg != "" {
			roots = []string{s.pkg}
		}
		if s.overlay != "" && !strings.HasPrefix(s.overlay, s.dir+string(filepath.Separator)) {
			roots = append(roots, s.overlay)
		}
		for _, root := range roots {
			_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(root, p)
				rel = filepath.ToSlash(rel)
				if e.IsDir() {
					if p != root && skipWatchDir(e.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), PackageExt) || strings.HasSuffix(e.Name(), ".exe") || nativeSource(rel) {
					return nil
				}
				info, err := e.Info()
				if err != nil {
					return nil
				}
				fmt.Fprintf(h, "%s\x00%d\x00%d\n", p, info.Size(), info.ModTime().UnixNano())
				return nil
			})
		}
	}
	return strconv.Quote(string(h.Sum(nil)))
}

// skipWatchDir: build outputs and dependencies.
func skipWatchDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "runtimes"
}

// nativeSource reports a file of the native UI sources: only
// ui/native/dist is packaged, a change elsewhere needs a UI build first.
func nativeSource(rel string) bool {
	return strings.HasPrefix(rel, "ui/native/") && !strings.HasPrefix(rel, "ui/native/dist/")
}

// copyTree copies the regular files below src into dst, except those skip
// matches (slash-separated paths relative to src).
func copyTree(src, dst string, skip func(rel string) bool) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if e.IsDir() || skip(filepath.ToSlash(rel)) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}
