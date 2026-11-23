#!/bin/sh
# Container entrypoint for sub2api-next.
#
# When the image carries a dev signing key (/opt/sub2api/market/dev-official.pub,
# written at build time) and the operator did not configure trust explicitly,
# derive:
#   SUB2API_PLUGIN_OFFICIAL_KEYS = "<keyId>=<pub>"
#   SUB2API_MARKET_SOURCES       = [{"name":"local-dev","url":$SUB2API_DEV_MARKET_URL}]
# The market index is not signed (CONTRACTS §11.1); the bundled market's
# packages are. Production deployments set both variables and ignore the dev key.
set -eu
MARKET=/opt/sub2api/market
if [ -f "$MARKET/dev-official.pub" ] && [ "${SUB2API_USE_DEV_KEYS:-true}" = "true" ]; then
  pub=$(tr -d ' \r\n' < "$MARKET/dev-official.pub")
  kid=$(tr -d ' \r\n' < "$MARKET/dev-official.keyid" 2>/dev/null || echo sub2api-dev)
  if [ -z "${SUB2API_PLUGIN_OFFICIAL_KEYS:-}" ]; then
    export SUB2API_PLUGIN_OFFICIAL_KEYS="$kid=$pub"
  fi
  if [ -z "${SUB2API_MARKET_SOURCES:-}" ] && [ -n "${SUB2API_DEV_MARKET_URL:-}" ]; then
    export SUB2API_MARKET_SOURCES="[{\"name\":\"local-dev\",\"url\":\"$SUB2API_DEV_MARKET_URL\"}]"
  fi
fi
exec "$@"
