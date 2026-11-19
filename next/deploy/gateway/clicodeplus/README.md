# sup2api 两节点部署（2026-10-27 起）

用户要求：clicodeplus 和 ovh 各一个节点，api.prophey.ai 先走 clicodeplus 本机节点、容灾到 ovh；数据库沿用 Supabase；Redis 用 valkey.prophey.ai，只允许两台节点服务器连接；不再在 ovh 上跑四个节点。

## 拓扑

```
客户端 ── api.prophey.ai（DNS → clicodeplus 64.186.225.70）
            │ clicodeplus Caddy（/root/caddy/Caddyfile，片段见 Caddyfile.sup2api）
            ├─① 127.0.0.1:3140 → sup2api-ccp（本机节点，/root/sup2api-node）
            └─② 127.0.0.1:3141 跳板 → https://sup2api.15.204.107.38.sslip.io
                   │ ovh Caddy（~/caddy/Caddyfile，片段见 ../ovh/Caddyfile.host-sup2api）
                   └→ 127.0.0.1:3130 → sup2api-1（主节点，~/sup2api-managed）

两节点共用：PostgreSQL（Supabase）+ Redis（rediss://…@valkey.prophey.ai:6380/0）
节点之间：HTTPS 7443（sup2api-1 ↔ sup2api-ccp，证书由 ovh 的 certs/ca.crt 签发）
```

- **选路**：`lb_policy first` 加主动健康检查（每 5 秒查 `/readyz`，3 秒超时）。本机节点健康时只走本机；不健康或连不上时转 ovh。连接失败的请求在 5 秒内重试别的上游。同一组上游不能混用 http 和 https，所以 ovh 经本机 3141 的 HTTP 跳板再转 HTTPS。
- **客户端 IP**：
  - clicodeplus Caddy 把客户端 IP 写进 `X-Forwarded-For`，3141 跳板原样转发。ovh Caddy 信任 64.186.225.70，取出真实地址后用 `{client_ip}` 传下去。
  - 每个节点的 `trusted_proxies` 只有本机 Docker 网关：sup2api-ccp 是 `10.234.10.1/32`（网络 `sup2api-node` 10.234.10.0/24），sup2api-1 是 `192.168.96.1/32`（`sup2api_default`）。
  - 切换前经 api.prophey.ai 进来的请求，`client_ip` 都记成 clicodeplus 的地址；切换后是真实地址。
- **主节点** sup2api-1：负责升级协调和插件包存储（网关换成带 CONTRACTS §114 的版本后，插件包由各节点各自保存、互相取，不再依赖主节点），发布源 `releases` 也在 ovh。核心与网关的发布与更新见 [网关手册“更新源与一键更新”](../README.md#更新源与一键更新)。从节点 sup2api-ccp 经 7443 从主节点拉取核心和插件包；网关一键更新时它自己经更新源下载网关。
- **Redis**：在 40.160.152.211（`ssh valkey`，经 ovh 跳板）的 `~/valkey-stack`。Valkey 8 不发布端口，Caddy 四层代理在 6380 做 TLS 终结。
  - 白名单在 Caddy 的 layer4 路由里，按来源 IP 匹配，只放行 64.186.225.70 与 15.204.107.38，其他连接直接关闭。443 只用于 Let's Encrypt 续签证书。
  - redis-cli 测试要加 `--sni valkey.prophey.ai`（不带 SNI 时 Caddy 选不到证书）；go-redis 会自动带上。
- **节点间端口 7443**：两台机器都直接对外发布，不装防火墙（用户决定）。每个请求都要带 Redis 里登记的节点密钥，否则 401。
- **密钥**：
  - clicodeplus：`/root/sup2api-node/.env`（600）。
  - ovh：`~/sup2api-managed/.env`（600），数据库地址已从 compose 明文移入这里。
  - 节点配置 `config/*.json` 不再写数据库和 Redis 地址，由环境变量提供。

## 切换记录（2026-10-27 UTC）

1. 14:47 Redis 主机 Caddy 加 layer4 白名单。两台服务器 TLS + 密码 PING 通过，不在白名单的地址被直接断开。
2. 签发 `sup2api-ccp` 证书（SAN `sup2api-ccp`，3 年）。网关镜像 `sup2api-gateway:0.1.125` 经 `docker save | docker load` 复制到 clicodeplus，镜像 ID 一致。
3. 14:55 ovh Caddy 加 `sup2api.15.204.107.38.sslip.io`；clicodeplus Caddy 改为本机优先 + ovh 容灾。此时本机节点未启动，流量全部经 HTTPS 到 ovh，没有中断。
4. 14:57 备份 `~/sup2api-managed/backups/topology-2026-10-27/database.dump`，以及 compose、sup2api-1 配置、.env 的 `*.bak-pre-topology-20261027`。
5. `disable-node` 停用 sup2api-2/3/4（PG 中 enabled=f、stopped=t），然后删除容器。
6. 14:58:54 用新 compose 重建 sup2api-1（Redis 换成 valkey.prophey.ai，端口改为 127.0.0.1:3130 + 7443），14:59:06 就绪。**全站中断约 12 秒**，这是唯一一次中断。
7. 14:59:43 启动 sup2api-ccp：从主节点拉取核心 0.1.208，6 个插件（含 ccgateway 0.1.106）启动，15:00:02 就绪，Caddy 随即把流量切到本机。
8. 删除 ovh 的 `sup2api-valkey` 容器、`.env` 里不再使用的 `REDIS_PASSWORD`/`PG_PASSWORD`、sup2api-2/3/4 的状态卷（约 230 GB，都是可重新下载的发布包与插件包缓存，磁盘 47% → 33%）。

核对：
- 切换后真实请求 22 条全部 200，其中 sup2api-ccp 18 条、sup2api-1 4 条。
- 账号测试：35 号经 api.prophey.ai（sup2api-ccp）200，21 号经 sup2api-1 200。36 号在两个节点上都是上游 429，与切换无关。
- Redis 中只有 sup2api-1、sup2api-ccp 两个节点登记。

## 网关 0.1.126（2026-10-27 15:27–15:32 UTC，修复上线时发现的两个问题）

源码 `aceb9e80a`，镜像 `sup2api-gateway:0.1.126`（ID `3c4d56d3…`），在 ovh 的干净 worktree `~/sup2api/release-gateway-0.1.126` 构建，再用 `docker save | docker load` 复制到 clicodeplus。两边 `.env` 里的 `GATEWAY_IMAGE` 都改成了这个版本。

**每个请求多约 0.26 秒（已修）**
- 原因：网关对每个公网请求都向核心查一次是否就绪（`LocalReady` → `/v1/status` → `approvedPluginsReady`），这一步要和 PostgreSQL 来回十几次。核心本身只要 1 ms，网关这一层却花了 366 ms。clicodeplus 到 Supabase 约 30 ms、到 Redis 约 25 ms，所以在这台机器上最明显。
- 修法：`proxy.ReadyCache`，结果在 1 秒内直接复用。过期后，上次是「就绪」的，先照常放行、同时在后台刷新，请求不用等；上次是「未就绪」的，每次都重新检查。（已被 0.1.127 的「懒就绪」替代，见下一节。）
- 效果：

| 路径 | 修复前 | 修复后 |
|---|---|---|
| 本机节点 `/readyz` | 0.36 s | 0.3 ms |
| 本机节点 `/v1/models` | 0.36 s | 约 1 ms |
| api.prophey.ai | 0.38–0.48 s | 0.02–0.03 s |
| 经 ovh 的容灾路径 | 0.10 s | 0.023 s |

**节点管理命令引用了不存在的表（已修）**
- `remove-node`、`set-primary` 和插件包回退都查 `updater.node_artifacts`、`updater.node_plugin_packages` 这两张表，但网关的建表脚本从来没有创建过它们（2026-10-04 审计合并时引入）。
- `remove-node` 现在只删节点登记和准入记录；计划步骤、停止确认和事件保留，作为审计记录。
- 插件包回退改为依次去其他启用中的节点取，对方没有这个包就返回 404。
- `set-primary` 原本把主节点名当成基线版本来比较（`ClusterState` 的返回值顺序读错了），所以对任何节点都拒绝，已一起修正。
- 新增 PG 测试 `TestPostgresRemoveNodeSetPrimaryAndPackageFallback`。在全新数据库上，网关全部测试通过；共享测试库里有 14 个用例因为残留的插件数据失败，未改的代码在同一个库上同样失败。

**上线顺序**
1. 先更新 ovh 的 sup2api-1，它当时只做容灾、几乎没有流量。15:27:14 启动，15:27:28 就绪。
2. 用新网关的 `remove-node` 删除 sup2api-2/3/4 的登记，集群里现在只剩 sup2api-1 和 sup2api-ccp。
3. clicodeplus：先把本机上游从 Caddy 摘掉（流量全部走 ovh），再重建容器（按 180 秒宽限排空，实际停了 8 秒）。15:32:02 就绪后恢复 Caddy。
4. 两个节点这次重建时，`stop_grace_period: 180s` 都已生效。

核对：上线后真实请求全部 200（sup2api-ccp 23 条、sup2api-1 1 条）；账号测试 35 号经 api.prophey.ai 200（1.1 s），21 号经 sup2api-1 200。

## 网关 0.1.127：本机就绪检查改为「懒」（2026-10-27，用户规则，替代 0.1.126 的 1 秒缓存）

源码 `1a8b0ce10`，镜像 `sup2api-gateway:0.1.127`（ID `584ba013…`）。构建与复制的做法同 0.1.126，两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本。

**规则**
- 默认认为本机核心可用。
- 只要请求到达了核心，就继续认为可用。核心回什么状态码都算到达。
- 只有转发时连不上核心（连接被拒或被重置）才进入怀疑状态。客户端自己断开不算。
- 怀疑状态下，最多每 30 秒向核心的 `/v1/status` 问一次。问的过程中其他请求不再重复问。
- 代码：`proxy.LazyReady`（`internal/proxy/readycache.go`）。`router.go` 在 `ModifyResponse` 里调 `LocalSucceeded`，在 `ErrorHandler` 里调 `LocalFailed`，只对本机目标生效。

**效果**：正常情况下每个请求都不再查核心状态，api.prophey.ai 约 0.02–0.04 秒。

**上线顺序**：同 0.1.126。先更新 ovh 的 sup2api-1。clicodeplus 先把本机上游从 Caddy 摘掉，重建容器，`/readyz` 返回 200 后再恢复 Caddy。


## 网关 0.1.128：发布包下载不再有 10 分钟总时限（2026-10-29）

源码 `43bd72fff`（compose 默认镜像 `98495051a`），镜像 `sup2api-gateway:0.1.128`（ID `b1951457…`），在 ovh 干净 worktree `~/sup2api/release-gateway-0.1.128` 构建，经本机复制到 clicodeplus（gzip 44 MB，SHA `ae86cd8e…`）。两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本，备份为 `.env.bak-gw128`。

**起因**：核心 0.1.213 发布时，clicodeplus 下载 477 MB 发布包只有约 0.7 MB/s。第一次下载到 10 分钟被取消，staging 清空后从头重下，整个计划用了 1076 秒。如果链路一直这么慢，节点会永远下载不完。

**修法**
- `release.Manager` 下载发布包时不设总时限：等响应头最多 1 分钟，之后只有连续 2 分钟收不到任何字节才失败（`HeaderTimeout`/`IdleTimeout`）。清单请求仍是 1 分钟总时限（`ManifestTimeout`）。`publisherClient` 去掉了 10 分钟 `Timeout`。
- 真正的元凶是 `control.Engine.StepTimeout`：它就是节点锁 `WithLock` 的工作时限，到 10 分钟会取消整个 Work。现在锁的时限是 `max(StepTimeout, PrepareTimeout)`，`PrepareTimeout` 默认 24 小时，只作兜底。只有 prepare 步骤在这个上下文里执行，其他步骤和 prepare 前后的记账仍受 `StepTimeout` 限制。锁本身是 30 秒租约、持续续期，进程挂掉后 30 秒内就会释放，不会把升级锁住 24 小时。
- 新增单元测试：慢速但不断有数据的下载，超过原来的总时限也能完成；中途停住会按空闲时限失败；清单请求仍有短时限。

**上线顺序**：同 0.1.126。14:50 先更新 ovh 的 sup2api-1，9 秒就绪。然后从 Caddy 摘下 clicodeplus 的 3140（`Caddyfile.bak-gw128`；单文件 bind mount，要用 `cat > Caddyfile` 保持 inode），14:51:34 重建 sup2api-ccp，14:51:54 `/readyz` 返回 200，再恢复 Caddy。

**核对**：两节点镜像都是 0.1.128，核心仍是 0.1.214，没有 ERROR/WARN；`updater.nodes` 两节点 ready；插件进程 `Seccomp=2`；api.prophey.ai `/readyz` 0.025–0.03 秒。44 号 haiku 200；42 号 429 是 5 小时额度已用满。慢链路下载要等下一次核心发布才能实际验证。

## 网关 0.1.129：Host API 8（2026-10-31）

源码 `b55479dad`，镜像 `sup2api-gateway:0.1.129`（ID `b19e596b…`），在 ovh 干净 worktree `~/sup2api/release-gateway-0.1.129` 构建，经本机复制到 clicodeplus（gzip 44 MB）。与 0.1.128 的唯一区别是 `runtime-contract` 的 `HostAPIVersion` 7 → 8；镜像里的 `sub2api-release` 也随之默认签 Host API 8。两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本，备份为 `.env.bak-gw129`。

**上线顺序**：同 0.1.128。先更新 ovh 的 sup2api-1（6 秒就绪）；再从 Caddy 摘下 3140（`Caddyfile.bak-gw129`），重建 sup2api-ccp（15 秒就绪），恢复 Caddy。全程 api.prophey.ai `/readyz` 200。随后发布核心 0.1.222（`v0.1.222-r2`）。

## 网关 0.1.130：自动清理旧核心发布包（2026-11-02）

源码 `6384710fd`，镜像 `sup2api-gateway:0.1.130`（ID `ca0640ac…`），在 ovh 干净 worktree `~/sup2api/release-gateway-0.1.130` 构建，经本机复制到 clicodeplus（gzip 44 MB）。外壳在核心启动成功后（含外壳自身启动时）运行 `release.Manager.GC`：只保留 current、previous 和 6 小时内准备过的发布包及其 bundle blob。此前 GC 没有调用方，ccp 的磁盘因此在 2026-11-02 被占满。两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本，备份为 `.env.bak-gw130`。

**上线顺序**：同 0.1.128。16:16 更新 ovh 的 sup2api-1（10 秒就绪），日志 `release cleanup removed=74`，releases + blobs 由 65 GB 降到 2 GB（ovh `/` 剩 1.1 TB）。从 Caddy 摘下 3140（`Caddyfile.bak-gw130`），16:17:45 重建 sup2api-ccp（9 秒就绪），`removed=1`（发核心 0.1.230 后变旧的 0.1.228），恢复 Caddy。两节点都只剩 0.1.230（current）与 0.1.229（previous）；clicodeplus `/` 剩 20 GB。全程 api.prophey.ai `/readyz` 200，两节点无 ERROR，核心仍是 0.1.230。

## 网关 0.1.131：Host API 9（2026-11-04）

源码 `2701738f6`，镜像 `sup2api-gateway:0.1.131`（ID `ac2826b3…`），在 ovh 干净 worktree `~/sup2api/release-gateway-0.1.131` 构建，经本机复制到 clicodeplus（gzip 44 MB）。与 0.1.130 的唯一区别是 `runtime-contract` 的 `HostAPIVersion` 8 → 9（插件运行时任务，CONTRACTS §77.8），镜像里的 `sub2api-release` 默认签 Host API 9。两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本，备份为 `.env.bak-gw131`。

**上线顺序**：同 0.1.128。19:51 更新 ovh 的 sup2api-1（9 秒就绪）；从 Caddy 摘下 3140（`Caddyfile.bak-gw131`），19:52:51 重建 sup2api-ccp（24 秒就绪），恢复 Caddy。全程 api.prophey.ai `/readyz` 200。随后发布核心 0.1.237。clicodeplus `/` 剩 18 GB。

## 网关 0.1.132：节点密钥只存哈希、转发头与插件包上传收紧（2026-11-04，peer 协议 2）

源码 `d51ac8f0d`（develop），镜像 `sup2api-gateway:0.1.132`（ID `fa071277…`），在 ovh 干净 worktree `~/sup2api/release-gateway-0.1.132` 构建，经本机复制到 clicodeplus。先在 ovh 用同一提交的干净 worktree 跑 `go vet` 与全部网关单元测试（golang:1.27-alpine 容器，PG 依赖的用例按原样跳过）。两边 `.env` 的 `GATEWAY_IMAGE` 都已改成这个版本，备份为 `.env.bak-gw132`。

**内容**（[规约 §14](../../../docs/MULTINODE-SYNC-PROTOCOL.md)，来自 2026-11-04 的节点鉴权审查）：
- Redis 登记只保存节点密钥的 SHA-256（`auth_key_sha256`），peer 协议 1 → 2。读 Redis 只能校验节点，不能再冒充节点。旧网关与新网关互不接受，所以两节点一起换；换的期间不创建核心升级计划。
- `REDIS_URL` 走 TCP 没有密码时网关拒绝启动（`allow_unauthenticated_redis` 只供隔离测试）。线上两边都是带密码的 `rediss://`，不受影响。
- 网关默认给核心加 `SUB2API_PLUGIN_LANDLOCK=true`（compose 本来就设了，两边等价）；管理 socket 令牌文件对同 UID 的插件本身没有隔离作用，隔离来自 Landlock。
- 私有转发入口把转发头收窄为单个 IP、单个协议，删除 `Forwarded`/`X-Real-IP`/`X-Forwarded-Host`。
- 主节点的插件包上传：必须声明 `Content-Length`，每个来源节点最多 2 个并发，整个 `plugin-blobs/` 有 16 GiB 总量上限（`plugin_store_max_bytes`）。插件包读取允许从任意启用节点读取被引用的包（主节点更换后的回退路径此前两端都会 403）。
- 节点身份的 PG 复核在接收网关缓存 2 秒（只缓存肯定答案），授权操作仍逐次读库。

**上线顺序**：同 0.1.128。18:10:29 UTC 更新 ovh 的 sup2api-1（9 秒就绪）；18:10:59 从 Caddy 摘下 3140（`Caddyfile.bak-gw132`），重建 sup2api-ccp，18:11:24 `/readyz` 200（22 秒），同时恢复 Caddy（与备份逐字节一致）。全程 api.prophey.ai `/readyz` 200。两节点启动后无 WARN/ERROR。核心版本不变。

## 网关 0.1.134：随核心 0.1.290 换上（2026-11-17）

源码 `e3906cc68`，镜像 `sup2api-gateway:0.1.134`（ID `b17f0838…`），在 ovh 的 `~/sup2api/release-0.1.290` 构建（`stage/0.1.290/build.sh`，runtime-contract 与 gateway 的 vet/test 通过），经本机复制到 clicodeplus（gzip 约 44 MB，SHA `51fdc8a6…`，`/root/gateway-images/`）。核心 0.1.290 的升级计划仍在 0.1.133 下执行，计划完成后才换外壳：compose 去掉 `SUB2API_JWT_SECRET`（0.1.290 不再读），`.env` 的 `GATEWAY_IMAGE` 改成 0.1.134；换前的 compose、`.env`、config 备份在 `/root/sup2api-node/backup-0.1.290/`。北京 00:28:06 重建、00:28:42 就绪。这次 new-api 渠道 392 正关着、没有流量，所以没有先让出。外壳 0.1.134 不能运行核心 0.1.288；升级前的数据库备份已删除，不再回退（0.1.290 发布手册已删除，原文 `git show 91e0c26bc:next/deploy/rollback/0.1.290-release.md`）。

## 网关 0.1.135：版本检测与一键更新（2026-11-17）

源码 `a0eac3c38`，镜像 `sup2api-gateway:0.1.135`（ID `da491cb9…`，`--build-arg VERSION=0.1.135`），在 ovh 的 `~/sup2api/release-0.1.291` 构建（`stage/0.1.291/build.sh`），经本机复制到 clicodeplus（gzip SHA `96d90c19…`，`/root/gateway-images/`）。核心 0.1.291 的 rolling 计划在 0.1.134 下完成后，按下文“网关换版流程”先换本节点：`/root/sup2api-node/gwswap-0.1.135.sh` 让出（北京 03:57:15）、排空（03:57:19，在途 0）、`.env` 备份为 `.env.bak-gw134` 并把 `GATEWAY_IMAGE` 改成 0.1.135、`docker compose up -d --no-deps sup2api-ccp`，03:57:53 `/readyz` 200；期间 Caddy 把 api.prophey.ai 切到 ovh，没有 5xx。之后才换 ovh 的主节点（03:58:10–03:58:27）。本版起各节点向 `updater.nodes` 报告 `gateway_version` 与能否自更新，之后的网关版本可以在控制台一键更新（`gateway-rolling-v1`）。回退：`.env` 换回 `.env.bak-gw134`，同样先让出再重建。

## 网关 0.1.136：发布清单不再签名（2026-11-17）

源码 `eaf21e48`，镜像 `sup2api-gateway:0.1.136`（ID `0e1cc8f9…`），在 ovh 的 `~/sup2api/release-0.1.293-eaf21e48` 构建（`stage/0.1.293-unsigned/build.sh`），经本机复制到 clicodeplus（gzip SHA `715869ec…`，`/root/gateway-images/`）。0.1.135 装不了不签名的发布，所以这一版仍手动换：`/root/sup2api-node/gwswap-0.1.136.sh` 让出（北京 16:07:58）、排空（16:08:01）、`.env` 备份为 `.env.bak-gw135`、`GATEWAY_IMAGE` 改成 0.1.136、重建，16:08:36 `/readyz` 200；之后换 ovh 主节点，再滚动升级核心 0.1.293。基线换成不签名的核心之后网关不能再回到 0.1.135；以后的网关版本在控制台一键更新。

## 网关 0.1.137：删除 signed_manifest 列（2026-11-17）

第一个由 GitHub Actions 发布的网关（Sub2API-Devs/sup2api Release `gateway-v0.1.137`，源码 `3c8cbbda9`）。用户要求逐个节点手动处理，没有用一键更新：镜像在 ovh 从同一提交构建（`stage/gateway-0.1.137/`，二进制 sha256 `fc74f2f2…2618` 与 Release 一致），经本机复制到 clicodeplus（gzip `fc73d9d6…`，`/root/gateway-images/`）。`/root/sup2api-node/gwswap-0.1.137.sh`：北京 18:23:27 让出、18:23:30 排空、`.env` 备份为 `.env.bak-gw136`、18:24:05 `/readyz` 200；之后换 ovh 主节点，再滚动升级核心 0.1.295。回退：`.env` 换回 `.env.bak-gw136`，同样先让出再重建。

## 网关换版流程（2026-11-08 起，下一版网关（预计 0.1.133）：节点让出，不再改 Caddy）

下一版网关起（CONTRACTS §82），本机管理 socket 提供 `POST /system/nodes/self/yield`。换网关镜像前先让节点把新请求交给另一个节点并让 `/readyz` 返回 503，Caddy 的健康检查会在 5 秒内把流量切到 ovh；重建容器后新网关就绪、`/readyz` 恢复 200，Caddy 自动切回。**不再需要改 Caddyfile。** 旧网关（≤0.1.132）没有这个接口，换到新版的那一次仍按上面“从 Caddy 摘下 3140”的老办法做。

前提：没有 running/paused 的核心升级计划；另一节点（sup2api-1）本地服务就绪。

```sh
# clicodeplus：让出并排空（网关 CLI 读容器内的管理 socket 与令牌，与 status 同一套）
docker exec sup2api-ccp sub2api-shell yield -config /etc/sub2api/shell.json
# 期望：{"data":{"node_id":"sup2api-ccp","yielding":true,"offloading":true,"draining":true,"active_requests":N}}
# 没有节点能承接时返回 HTTP 409（no other serving node can take this node's traffic），什么都不改。
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3140/readyz   # 503 即正确
# 等 Caddy 切走（≥5 秒），看进行中的请求数降到 0（长流最多等一会）：
docker exec sup2api-ccp sub2api-shell yield-status -config /etc/sub2api/shell.json
# 然后重建：
cd /root/sup2api-node && docker compose up -d
# 新容器就绪后 /readyz 200，Caddy 自动切回；让出状态随旧容器消失，不用 unyield。
# 中途反悔：docker exec sup2api-ccp sub2api-shell unyield -config /etc/sub2api/shell.json
```

ovh 的 sup2api-1 同理（它平时只做容灾，流量很少，可以不让出直接重建，但让出更稳妥）。核心升级本身从新版网关起会按 `rolling-v1` 逐节点重启（目标版本不改库、两个节点都是新网关时），控制台预检会显示选中的策略。

## 已知问题

- **资源余量小**：clicodeplus 6 核、7.7 GB 内存，节点约用 1.8 GB，可用约 3.6 GB；磁盘 158 GB 剩约 20 GB（2026-11-02 曾被占满，大头是同机其他服务；外壳 0.1.130 起旧核心发布包会自动清理）。
- **业务查询仍比 ovh 慢**：clicodeplus 离数据库约 30 ms，需要多次查库的接口（例如仪表盘概览约 0.5 秒）仍比 ovh 节点慢。这是距离造成的，不是就绪检查的问题。
