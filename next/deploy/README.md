# sub2api-next deployment

The long-running deployment is gateway-managed: since 2026-10-02 every node runs
`sub2api-gateway` (`next/gateway`) as its public entry, which supervises the
local core (listening on loopback only) and installs signed core releases with
the primary-first or rolling upgrade flow and Redis per-node peer keys described
in the [multi-node protocol](../docs/MULTINODE-SYNC-PROTOCOL.md). Core and
gateway releases are built and signed by GitHub Actions; the gateway detects
them from the configured update source and applies them when an administrator
clicks "update" in the console (CONTRACTS §102). Publishing releases and
configuring nodes: [gateway/README.md](gateway/README.md);
each environment's deployment record: `gateway/<environment>/README.md`.
One-way releases (migrations that cannot be undone) keep their upgrade and
rollback runbooks in `rollback/` until the release is live everywhere and
rollback is no longer possible; then they are deleted. Open now:
[rollback/protocol-binding-release.md](rollback/protocol-binding-release.md)
(migration 0106: cross-platform conversion is off until enabled per model on
an account; export the conversions in use before upgrading).

`single/` runs the core directly, without the gateway (services `app` and
`app-2` sharing their own PostgreSQL and Valkey). It is no longer the long-running deployment (see the
note at the top of [single/README.md](single/README.md)): do not run its
`deploy.sh` on a host that now runs the gateway-managed stack, since it would
bring the old containers back up on the same ports.

Automated acceptance tests use the separate `e2e/` stack: two nodes behind
Caddy on `127.0.0.1:3120`, their own PostgreSQL/Redis, signed plugin market and
mock upstream. See [e2e/README.md](e2e/README.md) for setup and commands. Never
run these destructive tests against a production stack or the unit-test
`testdb` database.

```
deploy/
├── gateway/               # gateway image, release packaging, node config; one README per environment
├── rollback/              # runbooks (and SQL) of a one-way release in progress; absent otherwise
├── single/                # compose.yml + deploy.sh of a core-only stack without the gateway
├── e2e/                   # isolated two-node stack + Caddy + opt-in Go test runner
├── docker/                # build and entrypoint scripts used by ../Dockerfile
├── mock-upstream/         # mock upstream for every platform plugin, vendor hosts over TLS, forward proxy (standalone Go module, used by e2e)
└── ci/compose.yml         # Go test runner joined to the sub2api-next-testdb network
```

`e2e/` replaces the old Caddy topology removed on 2026-09-27. It provides the
`/__node1`, `/__node2`, `/__mock` and `/market` routes expected by the suite,
using the dedicated Compose project `sub2api-next-e2e` by default.

## Image (`next/Dockerfile`, context `next/`)

- `node:24` builds `web/` into `server/web/dist` and every `plugins/*/ui/native`
  (skipped while `package.json` is missing; the placeholder page stays).
- `golang:1.27-trixie` builds `sub2api` (`-X main.Version=$SUB2API_VERSION`),
  `tools/sub2api-plugin` and the plugins when they exist. Plugins are packed and
  signed with a **dev key** created on the first build and kept in the BuildKit
  cache mount `sub2api-next-devkeys` (stable across rebuilds, never in the
  image). `tools/sub2api-plugin/scripts/build-demo.sh` is used when present;
  otherwise every `plugins/*/manifest.json` is packed generically. Either way
  a plugin whose manifest declares a native UI without `ui/native/dist/entry.js`
  fails the build (the forms and settings pages exist only as that UI).
- Runtime `debian:trixie-slim`, user `sub2api` (1000), `/usr/local/bin/sub2api`,
  `/usr/local/bin/sub2api-plugin`, `/opt/sub2api/market/`, data in `/var/lib/sub2api`.
- The entrypoint derives `SUB2API_PLUGIN_OFFICIAL_KEYS=sub2api-dev=<pub>` and
  `SUB2API_MARKET_SOURCES=[{"name":"local-dev","url":$SUB2API_DEV_MARKET_URL,...}]`
  from the dev public key unless those variables are set.
- Without the plugin CLI the market contains an empty, unsigned `index.json`.

Node settings live in each stack's `compose.yml`. The E2E stack explicitly
enables package signature checks, plugin seccomp, strict networking and
database role isolation. Its private-upstream exception is only for the mock
on that stack's internal network.

## mock-upstream

| Endpoint | Behaviour |
|---|---|
| `POST /v1/messages` | JSON or SSE (`message_start` with input/cache usage, `content_block_*`, `message_delta` with `output_tokens`, `message_stop`). Default usage: input 120, output 42, cache read 50, cache creation 30 (5m) |
| `POST /v1/messages/count_tokens` | `{"input_tokens": N}` |
| `GET /__requests[?since=id]`, `DELETE /__requests` | last 100 requests (x-api-key, model, stream, metadata.user_id, headers, body) |
| `POST /__control` `{api_key, status, delay_ms, chunk_delay_ms, remaining, usage, text}` / `DELETE /__control[?api_key=]` | per-account behaviour, optionally for N requests |
| `ANY /__webhook/*` | accepts and records (plugin webhook target) |

Behaviour triggers, in increasing precedence: `metadata.mock_status` /
`metadata.mock_delay_ms` in the body, `-status-429` / `-delay-500` inside the
API key, a `/__control` rule for the key, request headers `x-mock-status`
(`429` adds `retry-after: 2`), `x-mock-delay-ms`, `x-mock-chunk-delay-ms`,
`x-mock-usage: input=..,output=..,cache_read=..,cache_creation=..,cache_creation_1h=..`.
The gateway does not forward client headers such as `x-mock-*`, so tests
through the gateway use `/__control` rules or key markers.

## End-to-end tests (`next/e2e`)

```bash
# First start the dedicated stack as documented in e2e/README.md.
# Run on its Docker host from the full repository checkout; no host Go needed.
# The full suite takes about 27 minutes: pass -timeout=150m (the script's
# default -timeout=30m is too short; a later -timeout overrides it).
sh next/deploy/e2e/test.sh -timeout=150m
```

The opt-in runner compiles and executes tests under `/src` with Go 1.27.
AC14/AC15 rebuild derived guard plugins, so a standalone test binary without
Go and sources at its original compilation path is insufficient. The runner
mounts the Docker socket for fault injection; `test.sh` checks the dedicated
project and container labels first. This is an accident guard, not a Docker
authorization boundary. See [e2e/README.md](e2e/README.md) for direct Go/SSH
usage and runner options.

| Variable | Default |
|---|---|
| `E2E_BASE_URL` | **required, no default** — `Setup` fails with instructions when it is unset. A stale default (it used to be `http://127.0.0.1:3120`) makes every case skip as "unreachable" and the suite read as green |
| `E2E_ADMIN_EMAIL` / `E2E_ADMIN_PASSWORD` | `admin@sub2api.test` / (required beyond AC 1) |
| `E2E_DOCKER_HOST` | empty; `ovh` runs `ssh ovh docker ...`, `local` runs docker locally. Needed to kill nodes/plugins and to query PG/Redis |
| `E2E_MOCK_URL`, `E2E_MOCK_INTERNAL_URL`, `E2E_NODE_URLS` | Caddy helper routes / `http://mock-upstream:8080` |
| `E2E_PROJECT` | Set to the dedicated Compose project, `sub2api-next-e2e` in the supplied `.env`; container inspection must target that same project |

`Setup` fails (it does not skip) when `E2E_BASE_URL` is unset or the target
does not answer, and there is no longer an `E2E_RUN_PENDING` knob: every
`Pending()` marker named a module that has since been merged, so they were
removed (2026-09-29). The suite is not part of CI — `.github/workflows/next-ci.yml`
covers the Go modules only, because e2e needs a deployed stack.

Request bodies the contract leaves open are assumed as follows (adjust in
`e2e/resources.go` if the modules differ): `POST /roles {key, name, description}`,
`POST /groups {name, description, visibility, rate_multiplier, model_allowlist}`,
`POST /me/api-keys {name, group_id}`, per-request price `config {"price": "0.04"}`,
publisher `POST /publishers {name, trust_level}`, `POST /publishers/:id/keys {key_id, public_key}`,
plugin detail node states under `nodes[].{node_id,state}`.
