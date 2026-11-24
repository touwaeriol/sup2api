# sup2api single-node deployment

> 2026-10-02 起 ovh 的 `sup2api` 栈改为网关托管四节点（3130–3133），见 [`../gateway/ovh`](../gateway/ovh/README.md)。不要再在 ovh 上运行本目录的 `deploy.sh`，它会在相同端口重新拉起旧容器。

> 插件包从 2026-10-02 起不再存 PG（核心迁移 0022），没有网关的部署只用本机目录保存插件包，所以本栈的两个节点之间无法同步上传的插件；多节点请使用网关托管部署。

One sub2api-next node with its own PostgreSQL 16, a Valkey 9 cache and the plugin
market bundled in the image, all started by docker compose (project `sup2api`).
The cache is any Redis-protocol server: set `CACHE_IMAGE=redis:7` to run Redis
instead (the service keeps the name `redis`, and `SUB2API_REDIS_URL` accepts
`redis://` as well as `valkey://`). Besides real-time state it holds the console
sign-ins (login sessions and auth flows, CONTRACTS §100.5), so it keeps an
append-only file (`--appendonly yes --appendfsync everysec`) in the `redisdata`
volume: a restart of the cache (host reboot, image update, OOM kill) keeps
everyone signed in, losing at most the last second of writes. Without it, every
restart signs every console user out, with no alert. Switching images: Valkey
9 writes a file Redis 7 may not read, so remove the `redisdata` volume when
switching (`docker compose ... down redis && docker volume rm sup2api_redisdata`);
everyone then signs in again.
PostgreSQL, the cache and the market are reachable only inside the project network;
the service is published on `127.0.0.1:3130` by default; set
`SUP2API_BIND=0.0.0.0` in `.env` to expose it on all interfaces.

| Path on the server | What |
|---|---|
| `~/sup2api/src` | sparse clone of the repo (`next/` only), branch `develop` |
| `~/sup2api/.env` | secrets and admin account, created on the first deploy (mode 600, never in git) |
| volumes `sup2api_pgdata`, `sup2api_appdata`, `sup2api_market` | database, plugin files, market |

## Deploy / update

```bash
# from a checkout (local machine): pushes nothing, the server pulls from GitHub
ssh ovh 'bash -s' < next/deploy/single/deploy.sh
# another branch
ssh ovh 'bash -s -- my-branch' < next/deploy/single/deploy.sh
```

The script clones or fast-forwards `~/sup2api/src`, builds the image, runs
`docker compose up -d --build` and waits for `/healthz`. Data volumes survive
updates; core migrations run on start.

Both application nodes have `stop_grace_period: 180s`. On SIGTERM the core
marks readiness unhealthy, drains HTTP requests, then drains plugin work and
pending usage before closing storage. Keep the container grace period longer
than the application's combined drain budgets when changing these settings.

## Operate

```bash
cd ~/sup2api
C="docker compose -p sup2api -f src/next/deploy/single/compose.yml --env-file .env"
$C ps
$C logs -f app
$C restart app
$C down            # stop, keep data
grep ADMIN .env    # bootstrap admin account
```

Open the console through an SSH tunnel (`ssh -N -L 3130:127.0.0.1:3130 ovh`,
then http://127.0.0.1:3130), or add a site to the server's reverse proxy that
forwards to `127.0.0.1:3130` with streaming enabled (`flush_interval -1` in
Caddy) and set `SUP2API_PUBLIC_URL` in `.env` to the public URL.

The anthropic and guard plugins are in the bundled market (Plugins → Market),
whose packages are signed with the key generated when the image was first built
on this server (the market index itself is not signed, CONTRACTS §11.1). The
official plugin store's base is `SUB2API_MARKET_BASE_URL` in the compose
file, the bundled market (set it in `.env` to
`https://raw.githubusercontent.com/touwaeriol/sup2api-plugin` to use the real
store, CONTRACTS §137.6).
