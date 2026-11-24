#!/bin/sh
# package-plugin.sh <tag> <outdir>
#
# Packs the plugin a release tag names, for its GitHub release (CONTRACTS
# §135): tag plugin-<key>-v<version>, where <key> is a next/plugins/*/
# manifest's key and <version> its manifest version. Writes
# <outdir>/<key>-<version>.s2plugin (with source.json) and prints its path.
#
# The package is not signed: an install from the store trusts the index's
# sha256 and publisher, not a signature (CONTRACTS §136). A native UI goes
# through next CI's gate first (next-check-plugin-ui.sh: typecheck, test,
# build; next/web must be installed). pack compares the GORM models with
# migrations/ on TEST_DATABASE_URL.
# Run from the repository root.
set -eu
TAG=$1
OUT=$2

rest=${TAG#plugin-}
key=${rest%-v*}
ver=${rest##*-v}
if [ "$rest" = "$TAG" ] || [ -z "$key" ] || [ "$key-v$ver" != "$rest" ]; then
  echo "::error::tag $TAG is not plugin-<key>-v<version>" >&2
  exit 1
fi

# plugin_dir prints the next/plugins directory whose manifest key is $key.
plugin_dir() {
  for m in next/plugins/*/manifest.json; do
    if [ "$(jq -r .key "$m")" = "$key" ]; then dirname "$m"; return 0; fi
  done
  return 1
}
dir=$(plugin_dir) || { echo "::error::no plugin with key $key under next/plugins" >&2; exit 1; }
have=$(jq -r .version "$dir/manifest.json")
if [ "$have" != "$ver" ]; then
  echo "::error::tag $TAG names version $ver, $dir/manifest.json has $have" >&2
  exit 1
fi

if [ -f "$dir/ui/native/package.json" ]; then
  sh .github/next-ci/next-check-plugin-ui.sh "$dir/ui/native" >&2
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
cli="$work/sub2api-plugin"
(cd next/tools/sub2api-plugin && CGO_ENABLED=0 go build -trimpath -o "$cli" .)
echo "==> building $key $ver runtimes" >&2
"$cli" build --dir "$dir" --out "$work/stage" >&2
mkdir -p "$OUT"
"$cli" pack --dir "$dir" --runtimes "$work/stage" --out-dir "$OUT"
