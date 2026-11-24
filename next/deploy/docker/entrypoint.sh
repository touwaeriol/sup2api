#!/bin/sh
# Container entrypoint for sub2api-next.
#
# When the image carries a dev signing key (/opt/sub2api/market/dev-official.pub,
# written at build time) and the operator did not configure trust explicitly,
# derive SUB2API_PLUGIN_OFFICIAL_KEYS = "<keyId>=<pub>": the bundled market's
# packages are signed with it. Production deployments set the variable and
# ignore the dev key. The market is the official store unless a stack points
# SUB2API_MARKET_BASE_URL at its own market (CONTRACTS §137.6).
set -eu
MARKET=/opt/sub2api/market
if [ -f "$MARKET/dev-official.pub" ] && [ "${SUB2API_USE_DEV_KEYS:-true}" = "true" ]; then
  pub=$(tr -d ' \r\n' < "$MARKET/dev-official.pub")
  kid=$(tr -d ' \r\n' < "$MARKET/dev-official.keyid" 2>/dev/null || echo sub2api-dev)
  if [ -z "${SUB2API_PLUGIN_OFFICIAL_KEYS:-}" ]; then
    export SUB2API_PLUGIN_OFFICIAL_KEYS="$kid=$pub"
  fi
fi
exec "$@"
