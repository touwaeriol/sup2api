package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/sdk/manifest"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/blobs"
	pluginpkg "github.com/Sub2API-Devs/sup2api/next/server/internal/plugin/pkg"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Package is one verified .s2plugin cached on local disk. Files are read
// straight from the zip; the struct is safe for concurrent use.
type Package struct {
	Key       string
	Version   string
	SHA256    string // hex sha256 of the .s2plugin file
	Manifest  *manifest.Manifest
	Publisher string
	Trust     string // official | verified | community | unsigned
	// ClaimedAt is when the plugin key was first approved (plugins.
	// installed_at): the first claimant of an exclusive resource keeps it.
	// Zero when unknown (dev tooling, tests).
	ClaimedAt time.Time
	// Dir is DataDir/<key>/<version>-<hash8>; the package file and the
	// extracted binary live below it.
	Dir string

	file *os.File
	zr   *zip.Reader
	refs int // Acquire holds not yet done; guarded by Packages.mu
}

// Hash8 is the short package hash used in directory names and asset URLs.
func (p *Package) Hash8() string { return p.SHA256[:8] }

// VersionHash is "<version>-<hash8>".
func (p *Package) VersionHash() string { return p.Version + "-" + p.Hash8() }

// AssetBase is the public URL prefix for package assets.
func (p *Package) AssetBase() string { return core.PluginAssetBase(p.Key, p.VersionHash()) }

// FS exposes the package content (zip) as a read-only file system.
func (p *Package) FS() fs.FS { return p.zr }

// Has reports whether a file exists in the package.
func (p *Package) Has(name string) bool {
	f, err := p.zr.Open(name)
	if err != nil {
		return false
	}
	st, err := f.Stat()
	_ = f.Close()
	return err == nil && !st.IsDir()
}

// ReadFile reads one file from the package.
func (p *Package) ReadFile(name string) ([]byte, error) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	return fs.ReadFile(p.zr, name)
}

// MigrationsFS returns the directory holding SQL migrations, or nil when the
// plugin declares no database.
func (p *Package) MigrationsFS() (fs.FS, error) {
	if p.Manifest.Database == nil || p.Manifest.Database.Migrations == "" {
		return nil, nil
	}
	dir := strings.Trim(path.Clean("/"+p.Manifest.Database.Migrations), "/")
	if dir == "" {
		return p.zr, nil
	}
	if _, err := fs.Stat(p.zr, dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return emptyFS{}, nil
		}
		return nil, err
	}
	return fs.Sub(p.zr, dir)
}

// MigrationIDs lists the SQL migration file names shipped by the package.
func (p *Package) MigrationIDs() ([]string, error) {
	fsys, err := p.MigrationsFS()
	if err != nil || fsys == nil {
		return nil, err
	}
	return fs.Glob(fsys, "*.sql")
}

// BinaryPath resolves entry.grpc.binaries for goos/goarch inside the package.
// On Windows a ".exe" suffix is also accepted.
func (p *Package) BinaryPath(goos, goarch string) (string, error) {
	if p.Manifest.Entry.GRPC == nil || p.Manifest.Entry.GRPC.Binaries == "" {
		return "", errors.New("manifest has no entry.grpc.binaries")
	}
	name := strings.NewReplacer("{os}", goos, "{arch}", goarch).Replace(p.Manifest.Entry.GRPC.Binaries)
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if p.Has(name) {
		return name, nil
	}
	if goos == "windows" && p.Has(name+".exe") {
		return name + ".exe", nil
	}
	return "", fmt.Errorf("package has no binary for %s/%s (%s)", goos, goarch, name)
}

// ExtractBinary writes the plugin binary for goos/goarch to Dir/bin and
// returns its absolute path and sha256. An existing file is reused only when
// its hash matches the package entry.
func (p *Package) ExtractBinary(goos, goarch string) (binPath, sum string, err error) {
	name, err := p.BinaryPath(goos, goarch)
	if err != nil {
		return "", "", err
	}
	data, err := fs.ReadFile(p.zr, name)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(data)
	sum = hex.EncodeToString(h[:])
	binDir := filepath.Join(p.Dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", "", err
	}
	binPath, err = filepath.Abs(filepath.Join(binDir, path.Base(name)))
	if err != nil {
		return "", "", err
	}
	if cur, err := fileSHA256(binPath); err == nil && cur == sum {
		return binPath, sum, os.Chmod(binPath, 0o755)
	}
	tmp := binPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", "", err
	}
	_ = os.Remove(binPath)
	if err := os.Rename(tmp, binPath); err != nil {
		return "", "", err
	}
	return binPath, sum, nil
}

// Close releases the package file handle.
func (p *Package) Close() error {
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

// Packages fetches plugin packages through its blob Source, verifies their
// sha256 (and, with a verifier, their signature) and caches them under
// DataDir.
type Packages struct {
	db       *store.DB
	dataDir  string
	verifier PackageVerifier
	source   *blobs.Source

	mu    sync.Mutex
	cache map[string]*Package // key@version
	// fetch serializes downloads of the same package.
	fetch map[string]*sync.Mutex
}

// PackageVerifier re-checks a package signature on this node before the
// package is unpacked (CONTRACTS §14.3). data is the complete .s2plugin
// file, already matched against plugin_versions.package_sha256.
type PackageVerifier func(ctx context.Context, key, version string, data []byte) error

// PackagesOption configures NewPackages.
type PackagesOption func(*Packages)

// WithVerifier makes Packages verify every package signature before use;
// a failing package is not loaded (Open returns the error).
func WithVerifier(v PackageVerifier) PackagesOption {
	return func(s *Packages) { s.verifier = v }
}

// WithSource sets where package bytes come from (the market or the nodes
// holding them).
func WithSource(src *blobs.Source) PackagesOption {
	return func(s *Packages) { s.source = src }
}

// TrustVerifier verifies packages with the plugin trust store (official
// root keys and publisher_keys read through q). lim bounds unpacking and
// should match the upload limits.
func TrustVerifier(trust *pluginpkg.TrustStore, q *gorm.DB, lim pluginpkg.Limits) PackageVerifier {
	return func(ctx context.Context, key, version string, data []byte) error {
		p, err := pluginpkg.Open(data, lim)
		if err != nil {
			return err
		}
		if p.Manifest.Key != key || p.Manifest.Version != version {
			return fmt.Errorf("package manifest identifies %s@%s", p.Manifest.Key, p.Manifest.Version)
		}
		_, err = trust.VerifyInstalled(ctx, q, p)
		return err
	}
}

func NewPackages(db *store.DB, dataDir string, opts ...PackagesOption) *Packages {
	s := &Packages{db: db, dataDir: dataDir, cache: map[string]*Package{}, fetch: map[string]*sync.Mutex{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// DataDir returns the root directory for plugin files.
func (s *Packages) DataDir() string { return s.dataDir }

// Open returns the verified package of key@version, downloading it from the
// market or another node when it is not cached on disk yet. The package is
// not held: Release may close it at any time, so a caller that reads its
// files outside a running instance uses Acquire.
func (s *Packages) Open(ctx context.Context, key, version string) (*Package, error) {
	p, done, err := s.Acquire(ctx, key, version)
	if err != nil {
		return nil, err
	}
	done()
	return p, nil
}

// ErrPackageInUse is returned by Release while an Acquire of the package
// is not done yet.
var ErrPackageInUse = errors.New("plugin package in use")

// Acquire is Open that holds the package until done is called: Release
// leaves a held package (file handle and directory) alone.
func (s *Packages) Acquire(ctx context.Context, key, version string) (p *Package, done func(), err error) {
	id := key + "@" + version
	s.mu.Lock()
	if p, ok := s.cache[id]; ok {
		p.refs++
		s.mu.Unlock()
		return p, s.doneFunc(p), nil
	}
	lk, ok := s.fetch[id]
	if !ok {
		lk = &sync.Mutex{}
		s.fetch[id] = lk
	}
	s.mu.Unlock()

	lk.Lock()
	defer lk.Unlock()
	s.mu.Lock()
	if p, ok := s.cache[id]; ok {
		p.refs++
		s.mu.Unlock()
		return p, s.doneFunc(p), nil
	}
	s.mu.Unlock()

	if p, err = s.load(ctx, key, version); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.cache[id] = p
	p.refs++
	s.mu.Unlock()
	return p, s.doneFunc(p), nil
}

func (s *Packages) doneFunc(p *Package) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			p.refs--
			s.mu.Unlock()
		})
	}
}

// versionRecord reads what loading key@version needs: its manifest, package
// sha256 and URL, signature status, publisher name and trust level, and
// when the plugin was installed.
func versionRecord(db *gorm.DB, key, version string) *gorm.DB {
	return db.Table("plugin_versions AS v").
		Select("v.manifest, v.package_sha256, v.package_url, v.signature_status, p.name, p.trust_level, (?)",
			db.Model(&model.Plugin{}).Select("installed_at").Where("key = v.plugin_key")).
		Joins("LEFT JOIN publishers p ON p.id = v.publisher_id").
		Where("v.plugin_key = ? AND v.version = ?", key, version)
}

func (s *Packages) load(ctx context.Context, key, version string) (*Package, error) {
	var (
		manifestJSON []byte
		sum          string
		url          string
		sigStatus    string
		publisher    *string
		trust        *string
		claimedAt    *time.Time
	)
	err := versionRecord(store.G(ctx, s.db.Gorm), key, version).
		Row().Scan(&manifestJSON, &sum, &url, &sigStatus, &publisher, &trust, &claimedAt)
	if err != nil {
		if store.IsNoRows(err) {
			return nil, fmt.Errorf("plugin %s@%s: version not found", key, version)
		}
		return nil, err
	}
	sum = strings.ToLower(sum)
	if len(sum) != 64 {
		return nil, fmt.Errorf("plugin %s@%s: invalid package_sha256", key, version)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("plugin %s@%s: decode manifest: %w", key, version, err)
	}
	if m.Key != key || m.Version != version {
		return nil, fmt.Errorf("plugin %s@%s: manifest identifies %s@%s", key, version, m.Key, m.Version)
	}
	p := &Package{Key: key, Version: version, SHA256: sum, Manifest: &m}
	if publisher != nil {
		p.Publisher = *publisher
	}
	if claimedAt != nil {
		p.ClaimedAt = *claimedAt
	}
	switch {
	case sigStatus == "unsigned":
		p.Trust = "unsigned"
	case trust != nil:
		p.Trust = *trust
	default:
		p.Trust = "community"
	}
	p.Dir = filepath.Join(s.dataDir, key, p.VersionHash())
	err = s.materialize(ctx, p, func() ([]byte, error) { return s.source.Fetch(ctx, sum, url) })
	if err != nil {
		return nil, err
	}
	return p, nil
}

// materialize makes sure p.Dir/package.s2plugin holds the package with
// p.SHA256 (downloading it with fetch otherwise), verifies the signature
// when a verifier is configured and opens the zip.
func (s *Packages) materialize(ctx context.Context, p *Package, fetch func() ([]byte, error)) error {
	key, version, sum := p.Key, p.Version, p.SHA256
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(p.Dir, "package.s2plugin")
	var data []byte
	if cur, err := fileSHA256(file); err != nil || cur != sum {
		if data, err = fetch(); err != nil {
			return err
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != sum {
			return fmt.Errorf("plugin %s@%s: package sha256 mismatch", key, version)
		}
		tmp := file + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		_ = os.Remove(file)
		if err := os.Rename(tmp, file); err != nil {
			return err
		}
	}
	if s.verifier != nil {
		if data == nil {
			var err error
			if data, err = os.ReadFile(file); err != nil {
				return err
			}
			h := sha256.Sum256(data)
			if hex.EncodeToString(h[:]) != sum {
				return fmt.Errorf("plugin %s@%s: cached package sha256 mismatch", key, version)
			}
		}
		// attach re-hashes the opened file, so what is served is what was
		// verified here.
		if err := s.verifier(ctx, key, version, data); err != nil {
			return fmt.Errorf("plugin %s@%s: signature verification failed: %w", key, version, err)
		}
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	return p.attach(f)
}

// attach verifies f against p.SHA256 and opens it as a zip.
func (p *Package) attach(f *os.File) error {
	key, version, sum := p.Key, p.Version, p.SHA256
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	// Re-verify what we are about to serve from (the file may have been
	// swapped between the hash check and open).
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, st.Size())); err != nil {
		_ = f.Close()
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		_ = f.Close()
		return fmt.Errorf("plugin %s@%s: cached package sha256 mismatch", key, version)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("plugin %s@%s: open package: %w", key, version, err)
	}
	p.file, p.zr = f, zr
	return nil
}

// LoadPackage writes a package to dir and opens it, taking the manifest
// from the package itself (dev tooling and tests; production packages come
// from Packages.Open).
func LoadPackage(dir string, data []byte, publisher, trust string) (*Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	mj, err := fs.ReadFile(zr, "manifest.json")
	if err != nil {
		return nil, err
	}
	var m manifest.Manifest
	if err := json.Unmarshal(mj, &m); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p := &Package{Key: m.Key, Version: m.Version, SHA256: hex.EncodeToString(sum[:]), Manifest: &m, Publisher: publisher, Trust: trust}
	p.Dir = filepath.Join(dir, m.Key, p.VersionHash())
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(p.Dir, "package.s2plugin")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	if err := p.attach(f); err != nil {
		return nil, err
	}
	return p, nil
}

// Close releases every cached package.
func (s *Packages) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.cache {
		_ = p.Close()
		delete(s.cache, id)
	}
}

// PackageRef names one cached package version.
type PackageRef struct {
	Key     string
	Version string
}

// Adopt caches an already opened package (dev tooling and tests;
// production packages come from Open).
func (s *Packages) Adopt(p *Package) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[p.Key+"@"+p.Version] = p
}

// Cached lists the packages currently held open.
func (s *Packages) Cached() []PackageRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PackageRef, 0, len(s.cache))
	for _, p := range s.cache {
		out = append(out, PackageRef{Key: p.Key, Version: p.Version})
	}
	return out
}

// Release closes the cached package key@version and deletes its directory
// (package file and extracted binary). Call it only once no instance of
// the version runs anymore. Without a cached package every
// "<version>-<hash8>" directory of the version is removed. A package still
// held by Acquire is kept and ErrPackageInUse returned.
func (s *Packages) Release(key, version string) error {
	id := key + "@" + version
	s.mu.Lock()
	lk, ok := s.fetch[id]
	if !ok {
		lk = &sync.Mutex{}
		s.fetch[id] = lk
	}
	s.mu.Unlock()
	lk.Lock()
	defer lk.Unlock()

	s.mu.Lock()
	p := s.cache[id]
	if p != nil && p.refs > 0 {
		s.mu.Unlock()
		return ErrPackageInUse
	}
	delete(s.cache, id)
	s.mu.Unlock()

	var dirs []string
	if p != nil {
		_ = p.Close()
		dirs = append(dirs, p.Dir)
	} else if key != "" && version != "" {
		entries, _ := os.ReadDir(filepath.Join(s.dataDir, key))
		for _, e := range entries {
			if v, ok := versionOfDir(e.Name()); e.IsDir() && ok && v == version {
				dirs = append(dirs, filepath.Join(s.dataDir, key, e.Name()))
			}
		}
	}
	var firstErr error
	for _, d := range dirs {
		if err := removeAllRetry(d); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PruneOrphans deletes package directories (DataDir/<key>/<version>-<hash8>)
// that are neither cached nor kept. keep receives the plugin key, the
// version and the hash8 of the directory. Other directories (such as the
// plugin work directory DataDir/<key>/work) are never touched. Run it at
// node start, before packages are opened. Returns the removed directories.
func (s *Packages) PruneOrphans(keep func(key, version, hash8 string) bool) ([]string, error) {
	keys, err := os.ReadDir(s.dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	inUse := map[string]bool{}
	for _, p := range s.cache {
		inUse[filepath.Clean(p.Dir)] = true
	}
	s.mu.Unlock()
	var removed []string
	var firstErr error
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.dataDir, k.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			version, ok := versionOfDir(name)
			if !ok {
				continue
			}
			dir := filepath.Join(s.dataDir, k.Name(), name)
			if inUse[filepath.Clean(dir)] || keep(k.Name(), version, name[len(name)-8:]) {
				continue
			}
			if err := removeAllRetry(dir); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			removed = append(removed, dir)
		}
	}
	return removed, firstErr
}

// versionOfDir parses "<version>-<hash8>" (hash8 = 8 lower-case hex).
func versionOfDir(name string) (string, bool) {
	if len(name) < 10 || name[len(name)-9] != '-' {
		return "", false
	}
	for _, c := range name[len(name)-8:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return name[:len(name)-9], true
}

// removeAllRetry retries briefly: on Windows a just-killed plugin binary
// can stay locked for a moment.
func removeAllRetry(dir string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return err
}

func fileSHA256(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
