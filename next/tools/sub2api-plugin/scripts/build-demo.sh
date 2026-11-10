#!/bin/sh
# Builds, packs and signs every plugin under next/plugins and writes a signed
# market index. POSIX sh; run from anywhere.
#
# Usage: build-demo.sh <outdir> <keydir> <keyid> [publisher]
#
#   <keydir>/<keyid>.key must exist (create it once with
#   `sub2api-plugin keygen --key-id <keyid> --out <keydir>`); it is reused,
#   never regenerated. The same key signs the packages and the index.
#
# Output:
#   <outdir>/<name>-<version>.s2plugin for every next/plugins/<name> that has a
#     manifest.json -- the list is DISCOVERED, not enumerated here, so a new
#     plugin lands in the market with no change to this script,
#   <outdir>/anthropic-0.3.2-test.s2plugin  (upgrade test fixture),
#   <outdir>/index.json, index.json.sig,
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
#   REQUIRE_UI=1    fail when a plugin's ui/native/dist is missing (guard,
#                   moderation); default: pack without the native UI and warn.
#   PLATFORMS=...   override target platforms (default linux/amd64,linux/arm64).
#   PACK_IMAGES=... space separated <plugin>=<role,role,...>: bundle
#                   plugins/<plugin>/images (images.json and the container
#                   image archives it lists, CONTRACTS §53.10) into that
#                   plugin's package, requiring exactly these roles. Only
#                   the plugin's own package; overlay fixtures never get
#                   images. Set by deploy/docker/build-go.sh for ccgateway.
set -eu

if [ $# -lt 3 ]; then
  sed -n '2,30p' "$0"
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
  [ -n "$overlay" ] && set -- "$@" --overlay "$overlay"
  image_roles=
  for spec in ${PACK_IMAGES:-}; do
    case "$spec" in "$plugin="?*) image_roles=${spec#*=} ;; esac
  done
  if [ -n "$image_roles" ] && [ -z "$overlay" ]; then
    set -- "$@" --images "$dir/images" --image-roles "$image_roles"
  fi
  if [ -f "$dir/manifest.json" ] && grep -q '"native"' "$dir/manifest.json" && [ ! -f "$dir/ui/native/dist/entry.js" ]; then
    if [ "${REQUIRE_UI:-0}" = "1" ]; then
      echo "$dir/ui/native/dist/entry.js missing (REQUIRE_UI=1)" >&2
      exit 1
    fi
    echo "warning: $plugin: ui/native/dist missing, packing without native UI" >&2
    set -- "$@" --allow-missing-ui
  fi
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
package guard guard-test "$OUT/test" testdata/guardtest guardtest

"$BIN" index --dir "$OUT" --key "$KEY"
if [ -f "$KEYDIR/$KEYID.pub" ]; then
  "$BIN" verify --pub "$KEYDIR/$KEYID.pub" "$OUT"/*.s2plugin "$OUT"/test/*.s2plugin
fi
