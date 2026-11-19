#!/bin/sh
# build-ui.sh <src> <out>
# Builds the console (web/ -> server/web/dist) and every native plugin UI
# (plugins/<name>/ui/native -> ui/native/dist). Without web/package.json the
# console keeps its placeholder page, but the repository's plugin UIs build
# with web/'s vite (web/node_modules), so they then fail: web/ is required
# whenever a plugin has a native UI. Results are copied to <out> with the
# same layout and overlaid on the Go build context.
set -eu
SRC=$1
OUT=$2
mkdir -p "$OUT/server/web"
if [ -n "${NPM_CONFIG_REGISTRY:-}" ]; then npm config set registry "$NPM_CONFIG_REGISTRY"; fi

# A project with its own dependencies commits its package-lock.json. The
# repository's plugin UIs have none: they build with the console's vite, so
# web/ is installed first.
npm_install() {
  if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; fi
}

if [ -f "$SRC/web/package.json" ]; then
  echo "==> building web/"
  (cd "$SRC/web" && npm_install && npm run build)
else
  echo "==> web/package.json not found, keeping the placeholder console page"
fi
cp -a "$SRC/server/web/dist" "$OUT/server/web/dist"

for pkg in "$SRC"/plugins/*/ui/native/package.json; do
  [ -f "$pkg" ] || continue
  dir=$(dirname "$pkg")
  rel=${dir#"$SRC"/}
  echo "==> building $rel"
  (cd "$dir" && npm_install && npm run build)
  if [ -d "$dir/dist" ]; then
    mkdir -p "$OUT/$rel"
    cp -a "$dir/dist" "$OUT/$rel/dist"
  fi
done
