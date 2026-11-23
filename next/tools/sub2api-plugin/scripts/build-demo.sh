#!/bin/sh
# Builds, packs and signs every plugin under next/plugins and writes a local
# market: the packages and their index.json (CONTRACTS §135).
# POSIX sh; run from anywhere.
#
# Usage: build-demo.sh <outdir> <keydir> <keyid> [publisher]
#
#   <keydir>/<keyid>.key must exist (create it once with
#   `sub2api-plugin keygen --key-id <keyid> --out <keydir>`); it is reused,
#   never regenerated. It signs the packages (built-in and uploaded packages
#   are still verified by signature; the market index is not signed).
#   Every plugin with a native UI needs its ui/native/dist first
#   (deploy/docker/build-ui.sh builds them all); pack refuses a missing one.
#
# Output:
#   <outdir>/<name>-<version>.s2plugin for every next/plugins/<name> that has a
#     manifest.json -- the list is DISCOVERED, not enumerated here, so a new
#     plugin lands in the market with no change to this script,
#   <outdir>/anthropic-0.4.1-test.s2plugin, dynweight-0.7.1-test.s2plugin
#     (upgrade test fixtures),
#   <outdir>/index.json  index v2 of the packages above, built from a
#     temporary registry (publisher trust official, relative package urls),
#   <outdir>/test/guard-<version>-test.s2plugin  (guardtest build, not indexed)
#
#   Which plugins are *built into the image* is a separate, deliberate
#   deployment decision and stays an explicit list: BUILTIN_PLUGINS in
#   deploy/docker/build-go.sh. Being in the market is not being built in.
#
# Environment:
#   SUB2API_PLUGIN  CLI to use (default: sub2api-plugin on PATH, else built
#                   from tools/sub2api-plugin into a temp dir).
#   PLUGINS=...     space separated plugin names to package instead of every
#                   plugins/*/manifest.json (debugging / partial rebuilds).
#   PLATFORMS=...   override target platforms (default linux/amd64,linux/arm64).
#   SKIP_DB_CHECK=1 pack without comparing the GORM models with migrations/
#                   (pack --skip-db-check), where no PostgreSQL can run: the
#                   image build (root, no network). TestDBCheck covers it.
set -eu

if [ $# -lt 3 ]; then
  sed -n '2,37p' "$0"
  exit 2
fi

OUT=$1
KEYDIR=$2
KEYID=$3
PUBLISHER=${4:-sub2api}

NEXT=$(cd "$(dirname "$0")/../../.." && pwd)
mkdir -p "$OUT/test"
OUT=$(cd "$OUT" && pwd)
KEYDIR=$(cd "$KEYDIR" && pwd)
KEY="$KEYDIR/$KEYID.key"
if [ ! -f "$KEY" ]; then
  echo "missing $KEY; create it with: sub2api-plugin keygen --key-id $KEYID --out $KEYDIR" >&2
  exit 1
fi

WORK=$(mktemp -d 2>/dev/null || mktemp -d -t sub2api-plugin)
trap 'rm -rf "$WORK"' EXIT INT TERM

BIN=${SUB2API_PLUGIN:-}
if [ -z "$BIN" ]; then
  if command -v sub2api-plugin >/dev/null 2>&1; then
    BIN=$(command -v sub2api-plugin)
  else
    BIN="$WORK/sub2api-plugin"
    case "$(go env GOOS)" in windows) BIN="$BIN.exe" ;; esac
    (cd "$NEXT/tools/sub2api-plugin" && CGO_ENABLED=0 go build -o "$BIN" .)
  fi
fi

# package <plugin> <stage name> <dest dir> [overlay] [tags]
package() {
  plugin=$1; name=$2; dest=$3; overlay=${4:-}; tags=${5:-}
  dir="$NEXT/plugins/$plugin"
  stage="$WORK/stage/$name"

  set -- build --dir "$dir" --out "$stage"
  [ -n "$overlay" ] && set -- "$@" --overlay "$overlay"
  [ -n "$tags" ] && set -- "$@" --tags "$tags"
  [ -n "${PLATFORMS:-}" ] && set -- "$@" --platforms "$PLATFORMS"
  "$BIN" "$@" >/dev/null

  set -- pack --dir "$dir" --runtimes "$stage" --out-dir "$dest"
  # An overlay fixture carries migrations ahead of the plugin's GORM models
  # on purpose (dynweight's upgrade adds a column no model maps): db check,
  # which compares the overlay's migrations too, does not apply to it.
  if [ -n "$overlay" ] || [ "${SKIP_DB_CHECK:-0}" = "1" ]; then
    set -- "$@" --skip-db-check
  fi
  [ -n "$overlay" ] && set -- "$@" --overlay "$overlay"
  pkg=$("$BIN" "$@")
  "$BIN" sign --key "$KEY" --key-id "$KEYID" --publisher "$PUBLISHER" "$pkg"
}

# The plugins that go into the market are discovered, not listed: every
# next/plugins/<name> holding a manifest.json is packaged at its manifest
# version. Adding a plugin therefore needs no edit here.
if [ -n "${PLUGINS:-}" ]; then
  names=$PLUGINS
else
  names=
  for dir in "$NEXT"/plugins/*/; do
    [ -f "$dir/manifest.json" ] || continue
    names="$names $(basename "$dir")"
  done
fi
if [ -z "$(printf '%s' "$names" | tr -d ' ')" ]; then
  echo "no plugins with a manifest.json under $NEXT/plugins" >&2
  exit 1
fi
for name in $names; do
  if [ ! -f "$NEXT/plugins/$name/manifest.json" ]; then
    echo "$NEXT/plugins/$name/manifest.json missing" >&2
    exit 1
  fi
  echo "==> packaging $name" >&2
  package "$name" "$name-base" "$OUT"
done

# Extra packages the e2e suite needs. These are overlays of a plugin already
# packaged above (a second version, a differently tagged build), not plugins of
# their own, so they stay explicit.
package anthropic anthropic-upgrade "$OUT" testdata/upgrade
package dynweight dynweight-upgrade "$OUT" testdata/upgrade
package guard guard-test "$OUT/test" testdata/guardtest guardtest

# The market's registry: each package's version file with a relative url,
# so the index works wherever the directory is served.
"$BIN" registry import --registry "$WORK/registry" --repo touwaeriol/sup2api --trust official "$OUT"/*.s2plugin >/dev/null
"$BIN" index --registry "$WORK/registry" --out "$OUT/index.json"
if [ -f "$KEYDIR/$KEYID.pub" ]; then
  "$BIN" verify --pub "$KEYDIR/$KEYID.pub" "$OUT"/*.s2plugin "$OUT"/test/*.s2plugin
fi
