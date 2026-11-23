#!/bin/sh
# Prepares ~/sup2api-managed on the server: .env from the existing sup2api
# secrets, cluster CA and certificates, gateway image and core releases.
# Re-running keeps existing keys and certificates.
set -eu
M=$HOME/sup2api-managed
SRC=$HOME/sup2api/src/next
GATEWAY_IMAGE=${GATEWAY_IMAGE:-sup2api-gateway:local}
cd "$M"
umask 077
mkdir -p certs keys publish stage config

# ---- .env: same database, Redis and secrets as the single stack
if [ ! -f .env ]; then
  grep -E '^(PG_PASSWORD|REDIS_PASSWORD|SUB2API_MASTER_KEY|SUB2API_BOOTSTRAP_ADMIN_EMAIL|SUB2API_BOOTSTRAP_ADMIN_PASSWORD)=' "$HOME/sup2api/.env" > .env
  kid=$(docker run --rm --entrypoint cat sup2api:latest /opt/sub2api/market/dev-official.keyid | tr -d ' \r\n')
  pub=$(docker run --rm --entrypoint cat sup2api:latest /opt/sub2api/market/dev-official.pub | tr -d ' \r\n')
  printf "PLUGIN_TRUST_KEY=%s=%s\n" "$kid" "$pub" >> .env
  # The plugin store index (CONTRACTS §136), https only in production.
  printf "MARKET_SOURCES='[{\"name\":\"official\",\"url\":\"https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json\"}]'\n" >> .env
fi

# ---- cluster CA, node and release-origin certificates
if [ ! -f certs/ca.key ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 -subj "/CN=sup2api cluster CA" -keyout certs/ca.key -out certs/ca.crt 2>/dev/null
  for n in sup2api-1 sup2api-2 sup2api-3 sup2api-4 releases; do
    mkdir -p certs/$n
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$n" -keyout certs/$n/node.key -out certs/$n/node.csr 2>/dev/null
    printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\n' "$n" > certs/$n/ext.cnf
    openssl x509 -req -in certs/$n/node.csr -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -days 825 -extfile certs/$n/ext.cnf -out certs/$n/node.crt 2>/dev/null
    cp certs/ca.crt certs/$n/ca.crt
    rm certs/$n/node.csr certs/$n/ext.cnf
    # Read by the container user (UID 1000 / caddy); the parent stays private.
    chmod 755 certs/$n && chmod 644 certs/$n/*
  done
fi
chmod 711 certs

# ---- images and node configuration
# Core-only releases can reuse the installed gateway tools without retagging it.
if [ "${SKIP_GATEWAY_BUILD:-0}" = 1 ]; then
  docker image inspect "$GATEWAY_IMAGE" >/dev/null
else
  docker build -q -f "$SRC/deploy/gateway/Dockerfile" -t "$GATEWAY_IMAGE" "$SRC"
fi
for i in 1 2 3 4; do
  cat > config/sup2api-$i.json <<JSON
{
  "root": "/var/lib/sub2api",
  "cluster_id": "sup2api",
  "node_id": "sup2api-$i",
  "primary_node": "sup2api-1",
  "public_addr": ":8080",
  "peer_addr": ":7443",
  "peer_url": "https://sup2api-$i:7443",
  "peer_auth_key": "",
  "trusted_proxies": [],
  "cert_file": "/etc/sub2api/tls/node.crt",
  "key_file": "/etc/sub2api/tls/node.key",
  "ca_file": "/etc/sub2api/tls/ca.crt",
  "release_origin": "https://releases/sup2api",
  "runtime_abi": "linux-static-v1",
  "core_url": "http://127.0.0.1:18080"
}
JSON
  chmod 644 config/sup2api-$i.json
done
chmod 755 config

# ---- core releases (stage = bin/ + builtin/ of the image build stage)
commit=$(git -C "$HOME/sup2api/src" rev-parse --short HEAD)
release() {
  ver=$1
  # A schema-changing release names the version it migrates from.
  from=${2:-}
  if [ -f "publish/v$ver.digests" ]; then
    return 0
  fi
  docker build -q --target build --build-arg VERSION="$ver" -t "sup2api-core-build:$ver" "$SRC" >/dev/null
  rm -rf "stage/$ver" && mkdir -p "stage/$ver"
  cid=$(docker create "sup2api-core-build:$ver")
  docker cp "$cid:/out/bin" "stage/$ver/bin"
  docker cp "$cid:/out/builtin" "stage/$ver/builtin"
  docker rm "$cid" >/dev/null
  chmod -R a+rX stage publish
  chmod 777 publish
  docker run --rm -v "$M/stage/$ver:/stage:ro" -v "$M/publish:/publish" \
    -v "$SRC/deploy/gateway/package-release.sh:/package-release.sh:ro" --user "$(id -u)" --entrypoint sh "$GATEWAY_IMAGE" \
    /package-release.sh /stage /publish "v$ver" "$commit" ${from:+"$(stage/$from/bin/sub2api schema-contract)"} | tee "publish/v$ver.digests"
  chmod 755 publish && chmod 644 publish/*
}
release 0.1.0
release 0.1.1
# 0.1.2 adds migration 0022: plugin packages leave PostgreSQL.
release 0.1.2 0.1.1
# 0.1.3 adds the CPU protection setting (no schema change).
release 0.1.3
# 0.1.4 fixes plugin rollouts failing on an outdated reconcile read.
release 0.1.4
# 0.1.5 adds the OpenAI Responses WebSocket mode (no schema change).
release 0.1.5
# 0.1.6 adds migration 0023 (plugin migrations re-run when changed) and
# upgrades the bundled plugins with the core.
release 0.1.6 0.1.5
# 0.1.7 adds migration 0024, durable plugin history, audit and upgrade UI.
release 0.1.7 0.1.6
# 0.1.8 names the managed entry component gateway in the console (same schema).
# Redis telemetry and upgrade wakeups are installed by updating the gateway image.
release 0.1.8
# 0.1.9 adds the topology view and configurable GitHub core updates.
# Install the matching gateway image before publishing this core release.
release 0.1.9
# 0.1.10 replaces the static topology drawing with an interactive node graph.
release 0.1.10
# 0.1.11 makes upgrade primary/secondary roles explicit in the node graph.
release 0.1.11
# 0.1.12 identifies the current entry gateway and responding core in topology.
# Install the matching gateway first to expose the trusted entry response header.
release 0.1.12
# 0.1.13 simplifies the displayed primary/follower role labels.
release 0.1.13
# 0.1.14 reorganizes the account list and editor (same schema).
release 0.1.14
# 0.1.15 adds CCGateway remote management and the bundled account plugin (same schema).
release 0.1.15
# 0.1.16 puts plugin settings in each plugin row and detail tab (same schema).
release 0.1.16
# 0.1.17 adds account containers and change-driven external proxy egress.
release 0.1.17
# 0.1.18 adds API Key authentication alongside OAuth for CCGateway.
release 0.1.18
# 0.1.19 fixes clipboard copying on HTTP origins.
release 0.1.19
# 0.1.23 adds plugin default models and mappings, the redesigned account editor
# and model mapping on Claude Code connect (same schema).
release 0.1.23
# 0.1.24 adds migrations 0026-0031 (account auto-disable, security hardening,
# indexes, subscription limits, credential refresh, last account test), drops
# step-up, and authorizes Claude Code accounts in the account editor.
release 0.1.24 0.1.23
# 0.1.25 adds migration 0032 (CCGateway draft runtimes): Claude Code accounts
# are authorized before saving, the runtime installs from the console, and a
# login lasts while it is used. Upgrade the cc-max controller first.
release 0.1.25 0.1.24
# 0.1.26 adds migration 0033: re-authorizing a Claude Code account replaces its
# runtime and clears its history. Upgrade the cc-max controller first.
release 0.1.26 0.1.25
# 0.1.27 aligns right-aligned table headers with their cells (same schema).
release 0.1.27
# 0.1.28 makes copying the Claude authorization link the main action (same schema).
release 0.1.28
# 0.1.29 shows the upstream error of failed account tests (same schema).
release 0.1.29
ls -la publish
