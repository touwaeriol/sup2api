package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	runtimecontract "github.com/Sub2API-Devs/sup2api/next/runtime-contract"
	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pkgsig"
)

// PackageExt is the plugin package file extension.
const PackageExt = ".s2plugin"

// zipEpoch is the fixed modification time of package entries, so packing the
// same inputs yields the same bytes.
var zipEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Directories copied verbatim into the package.
var packDirs = []string{"migrations", "i18n", "assets"}

// cmdPack collects the package files and writes an unsigned .s2plugin.
func cmdPack(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("pack", stderr)
	dir := fs.String("dir", ".", "plugin directory")
	out := fs.String("out", "", "output file (.s2plugin)")
	outDir := fs.String("out-dir", "", "output directory; file name is <key>-<version>.s2plugin")
	runtimesDir := fs.String("runtimes", "", "directory containing runtimes/ (default: --dir)")
	overlay := fs.String("overlay", "", "overlay directory: manifest override plus extra/replacement files")
	allowMissingUI := fs.Bool("allow-missing-ui", false, "pack even when ui/native/dist is missing")
	skipDBCheck := fs.Bool("skip-db-check", false, "do not compare the GORM models with migrations/ (where no PostgreSQL is at hand)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if (*out == "") == (*outDir == "") {
		return fmt.Errorf("exactly one of --out or --out-dir is required")
	}
	ov, err := resolveOverlay(*dir, *overlay)
	if err != nil {
		return err
	}
	rt := *runtimesDir
	if rt == "" {
		rt = *dir
	}
	files, m, warnings, err := collectPackage(*dir, rt, ov, *allowMissingUI)
	if err != nil {
		return err
	}
	if !*skipDBCheck {
		// A plugin without GORM models (no internal/model) or without a
		// database has nothing to check. The overlay's migrations are packed,
		// so they are checked too.
		if err := dbCheck(*dir, ov, stderr); err != nil && !nothingToCheck(err) {
			return fmt.Errorf("db check: %w", err)
		}
	}
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	dest := *out
	if dest == "" {
		dest = filepath.Join(*outDir, m.Key+"-"+m.Version+PackageExt)
	}
	if err := writePackageMode(dest, files, true); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "packed %s %s: %d files\n", m.Key, m.Version, len(files))
	fmt.Fprintln(stdout, dest)
	return nil
}

// collectPackage gathers package path -> content.
func collectPackage(dir, runtimesDir, overlay string, allowMissingUI bool) (map[string][]byte, *manifest.Manifest, []string, error) {
	raw, m, err := effectiveManifest(dir, overlay)
	if err != nil {
		return nil, nil, nil, err
	}
	var warnings []string
	files := map[string][]byte{pkgsig.ManifestFile: raw}
	add := func(root, prefix string, skip func(rel string) bool) error {
		return addTree(files, root, prefix, skip)
	}

	for _, d := range packDirs {
		if err := add(filepath.Join(dir, d), d, nil); err != nil {
			return nil, nil, nil, err
		}
	}
	// ui/: everything except the native sources; native comes from its dist.
	if err := add(filepath.Join(dir, "ui"), "ui", func(rel string) bool {
		return rel == "native" || strings.HasPrefix(rel, "native/")
	}); err != nil {
		return nil, nil, nil, err
	}
	if err := add(filepath.Join(dir, "ui", "native", "dist"), "ui/native", nil); err != nil {
		return nil, nil, nil, err
	}
	// Runtimes.
	if err := add(filepath.Join(runtimesDir, "runtimes"), "runtimes", nil); err != nil {
		return nil, nil, nil, err
	}
	if err := addIcons(files, dir, m); err != nil {
		return nil, nil, nil, err
	}
	// Overlay files (except the manifest override files) win.
	if overlay != "" {
		if err := add(overlay, "", func(rel string) bool { return rel == overlayManifest || rel == overlayPatch }); err != nil {
			return nil, nil, nil, err
		}
	}
	// The digest of the plugin directory's sources (not the overlay's).
	if files[sourceFile], err = sourceJSON(dir); err != nil {
		return nil, nil, nil, err
	}

	w, err := checkPackage(files, m, allowMissingUI)
	if err != nil {
		return nil, nil, nil, err
	}
	return files, m, append(warnings, w...), nil
}

// addIcons stores the plugin's and its account types' icon files.
func addIcons(files map[string][]byte, dir string, m *manifest.Manifest) error {
	icons := []string{m.Icon}
	for _, at := range m.AccountTypes {
		icons = append(icons, at.Icon)
	}
	for _, icon := range icons {
		if icon != "" && !strings.HasPrefix(icon, "text:") {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(icon)))
			if err != nil {
				return fmt.Errorf("icon: %w", err)
			}
			files[icon] = b
		}
	}
	return nil
}

// addTree adds every regular file below root (if it exists) under prefix.
// Dot files are skipped.
func addTree(files map[string][]byte, root, prefix string, skip func(rel string) bool) error {
	st, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || (skip != nil && skip(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[path.Join(prefix, rel)] = b
		return nil
	})
}

// checkPackage verifies that the files referenced by the manifest exist.
func checkPackage(files map[string][]byte, m *manifest.Manifest, allowMissingUI bool) ([]string, error) {
	var warnings, missing []string
	// The manifest is held to the rules the installing host applies
	// (sdk/manifest/check), so a package that packs here also installs.
	if invalid := validateManifest(m, files); len(invalid) > 0 {
		return nil, fmt.Errorf("invalid manifest: %s", strings.Join(invalid, "; "))
	}
	if m.UI != nil && m.UI.Native != nil {
		if _, ok := files[m.UI.Native.Entry]; !ok {
			if !allowMissingUI {
				return nil, fmt.Errorf("native UI entry %s missing: build ui/native first (dist/) or pass --allow-missing-ui", m.UI.Native.Entry)
			}
			warnings = append(warnings, "native UI entry "+m.UI.Native.Entry+" missing (--allow-missing-ui)")
		}
	}
	if m.Database != nil {
		prefix := strings.TrimSuffix(m.Database.Migrations, "/") + "/"
		n := 0
		for p := range files {
			if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, ".sql") {
				n++
			}
		}
		if n == 0 {
			missing = append(missing, prefix+"*.sql (database migrations)")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing package files: %s", strings.Join(missing, ", "))
	}
	// Every plugin has a process; the manifest check above requires its
	// binaries template.
	tmpl := m.Entry.GRPC.Binaries
	found := 0
	for _, plat := range defaultPlatforms {
		goos, goarch, _ := strings.Cut(plat, "/")
		p := strings.NewReplacer("{os}", goos, "{arch}", goarch).Replace(tmpl)
		if _, ok := files[p]; ok {
			found++
		} else {
			warnings = append(warnings, "runtime binary "+p+" missing")
		}
	}
	if found == 0 {
		hasAny := false
		for p := range files {
			hasAny = hasAny || strings.HasPrefix(p, "runtimes/")
		}
		if !hasAny {
			return nil, fmt.Errorf("no runtime binaries: run `sub2api-plugin build` first")
		}
	}
	return warnings, nil
}

// fileMode is the zip mode of a package path.
func fileMode(p string) os.FileMode {
	if strings.HasPrefix(p, "runtimes/") {
		return 0o755
	}
	return 0o644
}

// writePackage writes files as a zip, sorted, with fixed timestamps.
// Signing intentionally replaces an existing archive; pack uses exclusive mode
// so a fixture with the same key/version cannot overwrite a release package.
func writePackage(dest string, files map[string][]byte) error {
	return writePackageMode(dest, files, false)
}

func writePackageMode(dest string, files map[string][]byte, exclusive bool) error {
	if len(files) > runtimecontract.PluginPackageMaxFiles {
		return fmt.Errorf("package has %d files, the limit is %d", len(files), runtimecontract.PluginPackageMaxFiles)
	}
	var total int64
	for _, b := range files {
		total += int64(len(b))
	}
	if total > runtimecontract.PluginUnpackedMaxBytes {
		return fmt.Errorf("package files total %d bytes, the unpacked limit is %d", total, runtimecontract.PluginUnpackedMaxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// The archive is streamed to its file (a package may be up to a
	// gigabyte; it is not buffered a second time in memory).
	var f *os.File
	var err error
	if exclusive {
		f, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing package %s; use a unique plugin key/version or a fresh output directory", dest)
		}
	} else {
		f, err = os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	}
	if err != nil {
		return err
	}
	name := f.Name()
	writeErr := writeZip(&limitedWriter{w: f, left: runtimecontract.PluginPackageMaxBytes}, files)
	if writeErr == nil && !exclusive {
		writeErr = f.Chmod(0o644)
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil && !exclusive {
		writeErr = os.Rename(name, dest)
	}
	if writeErr != nil {
		_ = os.Remove(name)
		return writeErr
	}
	return nil
}

// writeZip writes files sorted, manifest.json first, with fixed timestamps.
func writeZip(w io.Writer, files map[string][]byte) error {
	zw := zip.NewWriter(w)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// manifest.json first for readers that stream the archive.
	sort.SliceStable(paths, func(i, j int) bool { return paths[i] == pkgsig.ManifestFile && paths[j] != pkgsig.ManifestFile })
	for _, p := range paths {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate, Modified: zipEpoch}
		h.SetMode(fileMode(p))
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := fw.Write(files[p]); err != nil {
			return err
		}
	}
	return zw.Close()
}

// limitedWriter fails once more than left bytes are written.
type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, fmt.Errorf("package exceeds the %d byte limit", runtimecontract.PluginPackageMaxBytes)
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// fileSHA256 streams a file through SHA-256.
func fileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// readPackage reads every file of a .s2plugin, within the package limits.
func readPackage(src string) (map[string][]byte, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if st.Size() > runtimecontract.PluginPackageMaxBytes {
		return nil, fmt.Errorf("%s is %d bytes, the package limit is %d", src, st.Size(), runtimecontract.PluginPackageMaxBytes)
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return nil, fmt.Errorf("unsafe path %q in package", name)
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("duplicate entry %q in package", name)
		}
		if len(files) >= runtimecontract.PluginPackageMaxFiles {
			return nil, fmt.Errorf("%s has more than %d files", src, runtimecontract.PluginPackageMaxFiles)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		// Never trust the declared size: read one byte past the limit.
		b, err := io.ReadAll(io.LimitReader(rc, runtimecontract.PluginUnpackedMaxBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(b))
		if total > runtimecontract.PluginUnpackedMaxBytes {
			return nil, fmt.Errorf("%s unpacks to more than %d bytes", src, runtimecontract.PluginUnpackedMaxBytes)
		}
		files[name] = b
	}
	if _, ok := files[pkgsig.ManifestFile]; !ok {
		return nil, fmt.Errorf("%s: manifest.json missing", src)
	}
	return files, nil
}
