package supervisor

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// coreProvidedEnv are the variables the shell sets explicitly for every core
// (control.LocalRuntime.Start and the gateway's coreEnvironment); they must
// not be inherited.
var coreProvidedEnv = []string{
	"DATABASE_URL", "REDIS_URL", "NODE_ID", "UPDATER_SOCKET",
	"SUB2API_MANAGED", "SUB2API_CONTROL_SOCKET", "SUB2API_CONTROL_TOKEN", "SUB2API_CORE_BOOT_ID",
	"SUB2API_RELEASE_DIGEST", "SUB2API_HTTP_ADDR", "SUB2API_BUILTIN_PLUGIN_DIR", "SUB2API_UPDATER_TOKEN_FILE",
}

// coreIgnoredEnv are names the core reads that a managed core must not get
// from the shell: the unmanaged store variables (managed cores read
// DATABASE_URL/REDIS_URL) and variables only `sub2api dev` or tests read.
var coreIgnoredEnv = []string{"SUB2API_DATABASE_URL", "SUB2API_REDIS_URL"}

var envRead = regexp.MustCompile(`(?:Getenv|LookupEnv|boolEnv|durationEnv|int64Env|intEnv|\benv)\("([A-Z][A-Z0-9_]*)"`)

// The allowlist follows the core: every variable the core's production code
// reads is either inherited or provided by the shell. A new core setting
// that is neither would silently stop reaching managed cores.
func TestInheritedEnvCoversCoreConfiguration(t *testing.T) {
	root := filepath.Join("..", "..", "..", "server")
	if _, err := os.Stat(filepath.Join(root, "internal", "config", "config.go")); err != nil {
		t.Fatalf("core sources not found next to the gateway module: %v", err)
	}
	known := map[string]bool{}
	for _, list := range [][]string{inheritedEnv, coreProvidedEnv, coreIgnoredEnv} {
		for _, name := range list {
			known[name] = true
		}
	}
	read := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			// The dev command, the model generator, test helpers and
			// fixtures are not run by a managed core.
			case "devenv", "gen-models", "testdata", "registrytest", "node_modules", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range envRead.FindAllStringSubmatch(string(b), -1) {
			read[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(read) < 20 {
		t.Fatalf("found only %d environment reads in the core; the scan is broken", len(read))
	}
	var missing []string
	for name, path := range read {
		if !known[name] {
			missing = append(missing, name+" ("+filepath.ToSlash(path)+")")
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("core reads variables a managed core would not receive; add them to inheritedEnv or coreProvidedEnv:\n%s", strings.Join(missing, "\n"))
	}
	// The other way round: a core setting the core no longer reads leaves
	// the allowlist, unless legacyCoreEnv keeps it for an older core; a
	// legacy name the core reads again is a current setting.
	for _, name := range inheritedEnv {
		_, current := read[name]
		switch legacy := slices.Contains(legacyCoreEnv, name); {
		case legacy && current:
			t.Errorf("%s is read by the core again: move it from legacyCoreEnv to inheritedEnv", name)
		case !legacy && !current && strings.HasPrefix(name, "SUB2API_"):
			t.Errorf("%s is no longer read by the core: drop it, or keep it in legacyCoreEnv while an older core reads it", name)
		}
	}
}

// A 0.1.303 core started by this shell still gets the switch that lets it
// run on a database user without CREATEROLE.
func TestLegacyCoreEnvReachesTheCore(t *testing.T) {
	env := coreEnvironment([]string{"SUB2API_PLUGIN_DB_ROLE_ISOLATION=false"}, nil, nil)
	if !slices.Equal(env, []string{"SUB2API_PLUGIN_DB_ROLE_ISOLATION=false"}) {
		t.Fatalf("legacy setting did not reach the core: %v", env)
	}
}
