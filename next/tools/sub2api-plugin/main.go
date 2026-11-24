// Command sub2api-plugin is the developer tool for sub2api-next plugins:
// key generation, cross-compilation, packaging, signing and verification,
// the plugin store's registry and index (index, registry, released-check), a local core for
// development (dev), and the migrations of a plugin's GORM models (db).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `sub2api-plugin - sub2api-next plugin developer tool

Usage:
  sub2api-plugin keygen --key-id <id> --out <dir>
  sub2api-plugin build  --dir <plugin dir> [--out <dir>] [--dev] [--tags a,b] [--overlay <dir>] [--platforms linux/amd64,...]
  sub2api-plugin pack   --dir <plugin dir> (--out <file.s2plugin> | --out-dir <dir>) [--runtimes <dir>] [--overlay <dir>] [--allow-missing-ui]
                        [--skip-db-check]   (pack runs db check, overlay included, when the plugin has internal/model
                                             and a database; see db below for the PostgreSQL it needs;
                                             source.json records the digest of the plugin directory's sources)
  sub2api-plugin sign   --key <private key file> --key-id <id> [--publisher <name>] <file.s2plugin>
  sub2api-plugin verify --pub <public key file|base64> <file.s2plugin>
  sub2api-plugin index  --registry <registry dir> [--out <index.json>] [--dev]
                        (validate the registry, write index.json v2; --dev: a local market, relative urls allowed)
  sub2api-plugin registry check --registry <head dir> --base <base dir> --author <GitHub login> [--summary <file.md>] [--json]
                        (review a registry pull request; downloads every new version's package)
  sub2api-plugin registry add-version --registry <dir> --plugin <publisher/key | key of sub2api> [--tag <tag>]
                        (write plugins/<publisher>/<key>/versions/<version>.json from the plugin repo's GitHub release,
                         source_digest from the package's source.json; GITHUB_TOKEN is used if set)
  sub2api-plugin registry codeowners --registry <dir> [--default-owner <login>]   (print CODEOWNERS)
  sub2api-plugin registry import --registry <dir> --repo <owner/name> [--trust <level>] [--url-prefix <url>] <file.s2plugin>...
                        (register local packages for a development market)
  sub2api-plugin released-check --plugins <dir> --index <index.json url> [--strict]
                        (fail when a plugin's sources changed under a version the index registers with another
                         source_digest; an unreadable index only warns unless --strict)
  sub2api-plugin manifest --dir <plugin dir> [--overlay <dir>]   (print the effective manifest)
  sub2api-plugin dev    [--dir <plugin dir>] [--with <dir|file.s2plugin>]... [--addr 127.0.0.1:8080] [--core <sub2api>] [--reset] [--no-watch]
                        (run a local core with the plugin installed; rebuild on change)
  sub2api-plugin db diff  --dir <plugin dir> [--name <name>]   (write migrations/NNNN_<name>.sql from the GORM models)
  sub2api-plugin db check --dir <plugin dir> [--overlay <dir>] (fail while the GORM models and migrations/ disagree)
                        db diff, db check and pack compare on TEST_DATABASE_URL (a superuser DSN). Unset, they start a
                        local PostgreSQL 16 (embedded-postgres: about 25 MB downloaded once from Maven Central and
                        cached) and stop it when done, unless it was running already; SUB2API_TESTPG=off refuses to.

Run "sub2api-plugin <command> -h" for the flags of one command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// commands are the subcommands by name.
var commands = map[string]func([]string, io.Writer, io.Writer) error{
	"keygen":         cmdKeygen,
	"build":          cmdBuild,
	"pack":           cmdPack,
	"sign":           cmdSign,
	"verify":         cmdVerify,
	"index":          cmdIndex,
	"manifest":       cmdManifest,
	"dev":            cmdDev,
	"db":             cmdDB,
	"registry":       cmdRegistry,
	"released-check": cmdReleasedCheck,
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	fn, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err := fn(args[1:], stdout, stderr); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(stderr, "sub2api-plugin %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

// parseInterspersed parses flags that may appear before or after positional
// arguments and returns the positionals.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// parseNoArgs parses flags and refuses any positional argument.
func parseNoArgs(fs *flag.FlagSet, args []string) error {
	pos, err := parseInterspersed(fs, args)
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	return err
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
