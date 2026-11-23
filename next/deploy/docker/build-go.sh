#!/bin/sh
# build-go.sh <src> <out> <keydir>
#
# Builds (inside the Docker build stage, cwd-independent):
#   <out>/bin/sub2api          core server (-X main.Version=$VERSION)
#   <out>/bin/sub2api-plugin   plugin CLI
#   <out>/market/              *.s2plugin signed with the dev key, index.json
#                              (unsigned index v2), dev-official.pub, dev-official.keyid
#   <out>/builtin/             the built-in plugins' packages and trust key
#
# The dev key lives in <keydir> (a BuildKit cache mount) and is created on
# first use; it must never be copied into the image.
#
# GO_BINARIES_ONLY=1 stops after bin/ (cross builds with GOARCH set, whose
# plugin CLI cannot run here). SKIP_DB_CHECK=0 packs plugins with the GORM
# model check (needs PostgreSQL, see build-demo.sh); the image build has none.
set -eu
SRC=$1
OUT=$2
KEYS=$3
VERSION=${VERSION:-0.1.0-dev}
KEY_ID=${SUB2API_DEV_KEY_ID:-sub2api-dev}
if [ -n "${BUILD_MAX_PROCS:-}" ]; then
  case "$BUILD_MAX_PROCS" in
    *[!0-9]*|0) echo 'BUILD_MAX_PROCS must be a positive integer' >&2; exit 1 ;;
  esac
  # A BuildKit RUN may see host CPUs while its ancestor slice has the quota.
  # Limit both each Go process and go-command package compilation concurrency.
  export GOMAXPROCS="$BUILD_MAX_PROCS"
  export GOFLAGS="${GOFLAGS:-} -p=$BUILD_MAX_PROCS"
fi
if [ "${REQUIRE_EXISTING_DEV_KEY:-0}" = 1 ]; then
  [ -s "$KEYS/$KEY_ID.key" ] && [ -s "$KEYS/$KEY_ID.pub" ] || {
    echo 'release build requires the existing plugin signing key; refusing key generation' >&2
    exit 1
  }
fi
mkdir -p "$OUT/bin" "$OUT/market" "$OUT/builtin"
cd "$SRC"
echo "==> go version: $(go version)"
echo "==> building server $VERSION"
(cd server && go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$OUT/bin/sub2api" ./cmd/sub2api)

echo "==> building tools/sub2api-plugin"
CLI="$OUT/bin/sub2api-plugin"
(cd tools/sub2api-plugin && go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$CLI" .)
[ "${GO_BINARIES_ONLY:-0}" = 1 ] && exit 0
export PATH="$OUT/bin:$PATH"

mkdir -p "$KEYS"
if [ ! -f "$KEYS/$KEY_ID.key" ]; then
  echo "==> generating dev signing key $KEY_ID"
  "$CLI" keygen --key-id "$KEY_ID" --out "$KEYS"
fi

manifest_version() {
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n1
}

# build-demo.sh builds, packs and signs one package per
# plugins/<name>/manifest.json (the list is discovered there, so a new plugin
# needs no change in either script), plus the e2e fixtures (the anthropic and
# dynweight upgrade versions, the guard test build), and writes the market
# index.
# Everything packaged here is in the market; only BUILTIN_PLUGINS below is
# built into the image. pack refuses a plugin whose native UI was not built
# (build-ui.sh, the ui stage of next/Dockerfile).
echo "==> tools/sub2api-plugin/scripts/build-demo.sh"
# No PostgreSQL in the image build (initdb refuses root): the GORM models
# are compared with migrations/ by TestDBCheck in CI instead. The release
# workflow has one and passes SKIP_DB_CHECK=0.
SKIP_DB_CHECK=${SKIP_DB_CHECK:-1} sh tools/sub2api-plugin/scripts/build-demo.sh "$OUT/market" "$KEYS" "$KEY_ID"

# Public half of the dev key; the entrypoint derives the official package key
# from it.
cp "$KEYS/$KEY_ID.pub" "$OUT/market/dev-official.pub"
printf '%s\n' "$KEY_ID" > "$OUT/market/dev-official.keyid"
ls -l "$OUT/market"

# Built-in plugins (installed by the core at startup, cannot be uninstalled,
# only disabled): the package matching plugins/<name>/manifest.json's version.
# Two lists:
#   BUILTIN_PLUGINS               installed and enabled on first install
#   BUILTIN_PLUGINS_INSTALL_ONLY  installed but left disabled until an operator
#                                 enables them (written to install-only.txt,
#                                 read by the core's EnsureBuiltin)
# Either way the core grants the requested host permissions itself, so an
# install-only plugin is ready to enable without a consent step.
#
# These lists are DELIBERATELY explicit and must NOT be derived from plugins/*.
# Being in the market (which *is* discovered, see build-demo.sh) means "an
# operator can install it"; being here means "every deployment ships it and
# nobody can remove it", which is a deployment decision, not a consequence of
# a directory existing. A new plugin therefore reaches the market with no
# change and is added here only on purpose.
#
# The anthropic/openai/gemini platforms and their endpoints are built into the
# core itself; the anthropic, openai and gemini plugins only add the API key
# account types of their platform. Like every plugin they have a process: build-demo.sh builds binaries for all of
# them and every package carries its runtimes.
# moderation is the LLM prompt moderation hook (CONTRACTS §20):
# enabled at install, but its mode defaults to off until configured.
# codex adds the ChatGPT subscription account types (Codex device code and
# Sign in with Codex, docs/PLUGIN-CODEX.md) on the built-in openai platform:
# enabled at install; it does nothing until an operator authorizes an account.
#
# mailer is install-only: it provides the "email" notification channel
# (CONTRACTS §74.4) and does nothing until an operator sets its SMTP server.
# dynweight is install-only: once enabled it is called on every scheduled
# request, so it is enabled on purpose (CONTRACTS §74.5).
# newapi is install-only: its account type serves the built-in platforms
# with a site's key and address, so it does nothing until an operator adds
# an account; enabled on purpose (CONTRACTS §119).
# sub2api is install-only for the same reason: a Sub2API site's key and
# address per account (CONTRACTS §119).
# typesafe is install-only: enabling it adds the typesafe platform and the
# POST /v1/systemone endpoint to every node, which a deployment opts into
# (CONTRACTS §119).
#
# Intentionally NOT built in: relay (a market-only account type), guard (an
# optional gateway hook).
BUILTIN_PLUGINS=${BUILTIN_PLUGINS:-anthropic openai gemini moderation codex}
BUILTIN_PLUGINS_INSTALL_ONLY=${BUILTIN_PLUGINS_INSTALL_ONLY:-mailer dynweight newapi sub2api typesafe}
for name in $BUILTIN_PLUGINS_INSTALL_ONLY; do
  case " $BUILTIN_PLUGINS " in
    *" $name "*)
      echo "::error::builtin plugin $name is in both BUILTIN_PLUGINS and BUILTIN_PLUGINS_INSTALL_ONLY" >&2
      exit 1 ;;
  esac
done
mkdir -p "$OUT/builtin"
cp "$KEYS/$KEY_ID.pub" "$OUT/builtin/trust.pub"
printf '%s\n' "$KEY_ID" > "$OUT/builtin/trust.keyid"
: > "$OUT/builtin/install-only.txt"
for name in $BUILTIN_PLUGINS $BUILTIN_PLUGINS_INSTALL_ONLY; do
  ver=$(manifest_version "plugins/$name/manifest.json")
  if [ -f "$OUT/market/$name-$ver.s2plugin" ]; then
    cp "$OUT/market/$name-$ver.s2plugin" "$OUT/builtin/"
    echo "==> builtin plugin $name $ver"
  elif [ -n "${PLUGINS:-}" ]; then
    # The market list was narrowed on purpose (PLUGINS=...), so a built-in
    # whose package was not asked for is expected to be absent.
    echo "==> builtin plugin $name $ver: not in PLUGINS, skipped" >&2
  else
    # A built-in cannot be uninstalled, so an image missing one is an image
    # whose operator has no way to add it back. This used to warn and carry
    # on: the build went green and the plugin was simply not there.
    echo "::error::builtin plugin $name $ver: $OUT/market/$name-$ver.s2plugin was not built" >&2
    exit 1
  fi
done
for name in $BUILTIN_PLUGINS_INSTALL_ONLY; do
  printf '%s\n' "$name" >> "$OUT/builtin/install-only.txt"
  echo "==> builtin plugin $name: install-only (left disabled)"
done
ls -l "$OUT/builtin"
