# ec2pool 部署记录

ec2pool（AWS us-west-2，x86_64）上的 sup2api 是一个独立集群，和 ovh/clicodeplus 集群不共用数据库。同一台机器上还有 sakura-2api、cli-proxy-api、antigravity 和 kin 容器，与 sup2api 无关，操作时不要碰。

## 现状（2026-11-25 起）：网关托管，单节点，网关 0.1.139、核心 0.1.316（本机构建）

> 0.1.306–0.1.311 的发布记录在 `/opt/sup2api-gw/rel306`、`rel307`、`rel311` 与 `stage/own-<版本>/`，本文件没有逐条补录。

- 目录 `/opt/sup2api-gw`，compose 项目 `sup2api-gw`，接入外部网络 `sup2api_default`。
  - `node-a`（容器 `sup2api-gw-a`，主节点，`127.0.0.1:18080`），镜像 `sup2api-gateway:0.1.139`（本机从源码构建），状态卷 `sup2api-gw_node-a`（`/var/lib/sub2api`，持久）。只有一个节点（用户 11-17 决定）：核心更新没有别的节点承接流量，走主节点优先，会短暂停服；网关一键更新单节点中断约 10 秒，控制台要求确认。
  - `releases`（`sup2api-gw-releases`，caddy，`https://sup2api-gw-releases/sup2api` 提供 `publish/sup2api/`）：节点配置的 `release_origin`，离线导入核心清单用；平时更新走控制台的更新源（默认 GitHub `Sub2API-Devs/sup2api`），这里的超管能用密码完成二次验证。
  - 证书是本机自签的 Ed25519 CA（`certs/`，CA 私钥在 `certs/ca/`，0700），SAN 为容器名；`config/node-a.json`：`cluster_id` = `sup2api-ec2pool`，`trusted_proxies` = docker 网关 `10.200.70.1/32`。
  - `.env`（0600）：与旧部署同一个 PostgreSQL、Valkey、`SUB2API_MASTER_KEY` 与引导管理员。
- PostgreSQL 16（`sup2api-pg-1`，库 `sup2api`）、Valkey 9（`sup2api-redis-1`，AOF）与开发插件市场（`sup2api-market-1`）仍属于旧项目 `sup2api`（`/opt/sup2api`，`./compose.sh`），继续运行。
- 入口：宿主机 systemd Caddy 的 `/etc/caddy/sup2api-customer.caddy`（`sub2api-customer.sakura-api.com`、`sup2api.32.191.20.99.nip.io`、`https://sup2api.32.191.20.99.sslip.io`）反代到 `127.0.0.1:18080`。主 Caddyfile 是 `admin off`，`systemctl reload caddy` 会失败，改配置要 `restart`（会断开同一个 Caddy 上 antigravity、cliproxy 站点的连接）。
- 内置插件签名密钥：核心 0.1.298 起在没设 `SUB2API_BUILTIN_TRUST_KEY` 时自己读 `builtin/trust.pub`，compose 里不再设这个变量（0.1.297 期间手动设过，备份 `compose.yml.bak-with-trustkey`；回到 0.1.297 要加回去）。
- 本机构建的内置包用只留在这台机器上的插件密钥 `sup2api-ec2pool-2026` 签名（`keys/plugin-sign/`，0700；公钥 `YnVgIX8xaLoG1A9SgZ8nGIp+vl/opLYEK/UbxRwE+Ts=`），ovh 的 `sup2api-plugins-2026` 私钥没有搬过来。分组 `chatGPT`（id 1）下有 73 个 `codex_device` 账号（id 2–75），是有真实流量的环境。`SUB2API_PLUGIN_MAX_MEMORY_MB=1024` 仍设着（0.1.304 起默认不封顶，平台插件要经手完整请求体，见 CHANGELOG 0.1.304 升级须知）。
- 插件：`SUB2API_PLUGIN_VERIFY_SIGNATURES=false`（沿用旧部署）；已装插件包原来由旧核心用开发密钥 `sub2api-dev` 签名，Release 内置包用 `sup2api-plugins-2026` 签名，同版本的包保留原样。

## 2026-11-25 核心 0.1.316（无迁移，本机构建，codex 0.2.3）

- 用户 11:47 UTC 原话“合并 pr，更新服务”。
- 构建：`src316`（develop `83f734cb3`），`stage/own-0.1.316/`（照 own-0.1.315），162 秒；清单 `93dafeb00beeaa4a2a4927841baa8e93cd9d341de5bf950f20a8ece4980454bb`（amd64 `1185e9b2…b24b`、arm64 `86980084…cab3`），内置 codex 0.2.3 用 `sup2api-ec2pool-2026` 签名。
- 升级（`rel316/`，北京时间）：19:57:19 import，预检 primary-first-v1（expected_revision 22），计划 `385356769033597463fa127f22a335bf` 19:57:29.3→19:57:37.1；`/readyz` 503 19:57:34.3→19:57:37.3 约 3 秒。内置同步 19:57:42 把 codex 升到 0.2.3（rollout 23）。
- 核验：核心 0.1.316，5 个启用插件 active、restarts 0，日志无 ERROR；`chatgpt_test.py` 11 个请求全部 200、`billed`。
- 观察 19:57–20:10：key 1 的 `server_is_overloaded` 失败 7 个，客户端收到 529（使用记录 529），都没有换号，因为 19:58:07 超级管理员在控制台把故障策略改成 revision 2、`retry_enabled: false`（`settings.failure_policy`，旧版本在 `failure_policy_history`）。
- 回滚：无迁移，可用 0.1.315 清单 `ef85b6ef…f0ea` 建计划。

## 2026-11-25 核心 0.1.315（无迁移，本机构建）

- 用户 08:05 UTC 原话“同意发布 0.1.315 到 ovh 和 ec2pool”。
- 构建：`src315`（develop `22e17fd6f`），`stage/own-0.1.315/`（照 own-0.1.314，`LIVE_SCHEMA` = `SCHEMA` = `d3f7a424…b927`，pack 断言 rolling），167 秒；清单 `ef85b6efda27136a7c93ec5be4f488afd65a546302dfa8202162f45d568af0ea`（amd64 `a73ce738…c635`、arm64 `0fcc3f85…aab8`）。
- 升级（`rel315/`，北京时间）：16:10:53 import，预检 primary-first-v1（单节点，expected_revision 20），计划 `dda7e23bda61801c249ba3464f68a91c` 16:11:04.7→16:11:12.4；`/readyz` 503 16:11:09.6→16:11:12.6 约 3 秒。
- 核验：核心 0.1.315，5 个启用插件 active、restarts 0，日志无 ERROR；`evtypes.py` 8 个流式请求都成功，其中一个开头过载在账号 47 记 500、换到账号 63 成功并计费。上线后 16 分钟 key 1 流式 24 个全部成功（1 个开头过载换号成功），升级前一小时是 53 个里 16 个过载失败（CHANGELOG 0.1.315）。查询：`rel315/` 的 `check.sh`，开头过载的原始事件 `docker logs sup2api-gw-a 2>&1 | grep "stream error event"`。
- 回滚：无迁移，可用 0.1.314 清单 `38c993ee…0243` 建计划。

## 2026-11-25 核心 0.1.314（从 0.1.311，经过迁移 0125，主节点优先，本机构建）

- 用户 07:10 UTC 在卡片上选“ovh 滚动 + ec2pool 停机升级”，07:35 UTC 原话“同意发布 0.1.314 到 ovh 和 ec2pool”。
- 发布前（只读）：0125 手册第一节第 1 项两条都是 0 行；`market_sources` 只有 `id=1 local-dev`（`http://market:8080/index.json`，本地开发市场 `sup2api-market-1`），按演练结论不改名，接受 0125 删除；compose 的 `SUB2API_MARKET_SOURCES` 是 `""`、`SUB2API_DEV_MARKET_URL` 照旧，不用改 compose，不重建网关容器。没有另做数据库备份（用户规则）。
- 构建：`src314`（`git -C src311 worktree add`，develop `038a34977`），`stage/own-0.1.314/`（env.sh、build.sh、pack.sh 照 own-0.1.311，`LIVE_SCHEMA` `395364cf…2436`、`SCHEMA` `d3f7a424…b927`，密钥 `sup2api-ec2pool-2026`，`PREV_TRUST_SHA256` `2214b9b9…03ad`），165 秒；清单 `38c993ee64946ab6e7cde8660a03ee4815bc29dee76702517cf4c042d1270243`（amd64 `99e44c68…61b`、arm64 `6f109abc…f1c`），maintenance。核心二进制与 ovh 的构建逐字节相同。
- 升级（`rel314/`，北京时间）：清单与 bundle 放进 `publish/sup2api/`，15:37:19 `docker compose exec -T node-a sub2api-shell import …`；`observe.py` 预检 primary-first-v1、只有 node-a、无阻断（expected_revision 18）；计划 `0307b401bc327db2b4191aaa02d7643e` 15:37:32.3→15:37:40.1（prepare、maintenance、stop、start-primary 时迁移 0125、admit、local），revision 18→20。**停服**：`127.0.0.1:18080` 与公网 sslip `/readyz` 503 15:37:37.2→15:37:40.2 约 3 秒。
- 核验（`check.sh`、`chatgpt_test.py`、`evtypes.py`）：核心 0.1.314、网关 0.1.139；anthropic 0.4.0、codex 0.2.2、gemini 0.4.1、moderation 0.13.0、openai 0.5.0 active、restarts 0；商店是官方商店的 `6 正式 main 默认`、`5 测试 develop`；日志无 ERROR（WARN 只有 Landlock、验签关闭、内置包同版本不同内容这些已知项）；临时密钥（已删）chat、responses 非流式与流式都返回 200。`evtypes.py`（只打印事件名和时间）抓到过载流的真实顺序与错误对象（CHANGELOG 0.1.314），据此在 `a3b1dfdc5` 修正了扣住的判定，0.1.314 上开头的过载仍是 `response_committed`。
- 回滚：经过 0125，只能恢复备份（没有另做），不回退，修复前进。

## 2026-11-21 核心 0.1.305 + 网关 0.1.139（停机重新部署，本机构建，先备份）

- 发布前（只读，`psql305.sh`、`rel305/`）：网关 0.1.138、核心 0.1.303，无在途计划；PostgreSQL 16.16，库 310 MB，核心用户 `sup2api` 是超级用户兼属主，权限四项为真；旧角色与 schema 只有 `plg_anthropic`、`plg_moderation`（无会话）；全部插件内置，volcengine 只安装、无账号无任务；近 30 天跨平台转换 0 行（`rel305/conversions-before-upgrade.csv` 只有表头；`/v1/chat/completions` 打到 codex 账号是同平台适配）；近 7 天用到的 9 个模型都有价格；没有分组设过请求体上限。分组 `chatGPT` 73 个 codex 账号，最近一小时 178 个请求，成功率约 83%（失败都是上游错误）。升级前用 `rel305/chatgpt_test.py` 跑了一遍基线（`chatgpt-before.out`）。
- 构建（`stage/own-0.1.305/` 的 `env.sh`、`build.sh`、`pack.sh`，与 ovh 的 0.1.305 同一套做法）：`src305` 克隆 touwaeriol/sup2api，检出 develop `375f829a7`；`build-ui.sh` 45 秒，`build-go.sh`（amd64 全量加模型检查、arm64 两个二进制）与发布工具共 160 秒；`package-core.sh` 用本机新生成的密钥 `sup2api-ec2pool-2026` 签内置包（`PREV_TRUST_SHA256` 留空：这里换了密钥）。清单 `8054723a5c1b35fd01f40d78a5429a403c544faf9fe29512364bb89117e0e5bd`（amd64 bundle `67755dea…e1ae`、arm64 `06c33b27…5a19`），`schema_before` `19cabc85…c100`、`schema_after` `6c0c081d…ae59`，strategy maintenance。`bin/sub2api`（`f11e0bd6…ad8c`）、`bin/sub2api-plugin` 与 ovh 上的构建逐字节相同；插件包只差签名。网关：`stage/own-gateway-0.1.139/`，`--build-arg VERSION=0.1.139`，镜像 `sha256:f35c9417…31bf`，二进制 `c02f39bfdeccafa6f2f75cb62c91cdc12887e5e22a5c35606236b75131ec9870` 与 Release `gateway-v0.1.139` 相同。
- 停机重新部署（网关手册“停机重新部署”，所有者要求不等在途请求归零）：清单与 bundle 放进 `publish/sup2api/`；04:59:29.0 `docker compose stop node-a`（在途 1），04:59:31.8 `Exited (0)`；`.env` 备份 `.env.bak-gw138`，`GATEWAY_IMAGE=sup2api-gateway:0.1.139`；**备份** `rel305/backup.sh`：`backups/sup2api-pre-0.1.305-20261120T205931Z.dump`（`pg_dump -Fc`，26,833,980 字节，0600，3 秒，`pg_restore --list` 687 项，sha256 `80e78dd3…4e56`）；04:59:53.9 `docker compose run --rm --no-deps -T node-a redeploy -config /etc/sub2api/shell.json -manifest https://sup2api-gw-releases/sup2api/8054723a….json`（0.9 秒，`revision` 11→12）；04:59:54.8 用临时 `compose.bootstrap.yml` 以 `serve -bootstrap` 启动，05:00:14.1 `/readyz` 200（日志存 `rel305/bootstrap.log`）；05:00:29.3 去掉 override 重建（在途 2），05:00:34.8 就绪。**停服**：`https://sup2api.32.191.20.99.sslip.io/readyz` 04:59:29.4→05:00:13.7 约 44 秒，05:00:30.4→05:00:34.6 约 4 秒。
- 核验：核心 0.1.305、网关 0.1.139；`schema_migrations` 120 条；anthropic 0.4.0、codex 0.2.1、gemini 0.4.0、moderation 0.13.0、openai 0.5.0 enabled（5 次 rollout active），dynweight、mailer、newapi、sub2api、typesafe 只安装，volcengine 卸载并记入已退役键；旧 `plg_*` schema 与角色消失，`plg16384_moderation` 拥有 17 张表；账号 73、分组 1、密钥 2、用户 2 不变。bootstrap 日志的 ERROR 只有预期的旧插件 `was built for an older plugin protocol`（15 行）与 `plugin process exited … signal: killed`（3 行）；另有一条 WARN `plugin signature verification is disabled`。
- chatGPT 分组与粘性会话（`rel305/chatgpt_test.py`，输出 `chatgpt-after.out`；公网入口、临时密钥、模型 `gpt-5.6-terra`，密钥已删）：`/v1/chat/completions` 与 `/v1/responses` 的非流式、流式各一个都是 200、`billed`，落在 4 个不同账号；同一个 `prompt_cache_key` 的三个请求都落在账号 48（`openai-prompt-cache-key`，命中 否/是/是，`cache_read` 0/2816/2816），另一个会话两个请求都在账号 72，第三个会话（chat 非流式）两个都在账号 32；全部由 `codex@0.2.1` 处理，与升级前的基线一致。Responses WebSocket 会话没有在线上实测。
- 巡检脚本 `rel305/check.sh [分钟]`（只读：`/readyz`、版本、插件与重启次数、日志级别、使用记录的成功率与失败类型、账号分布、粘性命中率与缓存读取占比，并与升级前一小时对照）。
- 回滚：单向（迁移 0106–0120）。必须回退时：停网关，`.env` 换回 `.env.bak-gw138`，用上面的备份恢复库，再以核心 0.1.303 的清单启动。

## 2026-11-18 核心 0.1.303（codex 0.1.11 等，迁移 0105）

- Release `core-v0.1.303`（`4414334c8`，清单 `d97809231015c5269addc248f406e8db3851bba4f2f222c4c8c97564a92100a8`，amd64 bundle `cf96d8c6…44c0`）由 `fetch303.sh` 下载到 `stage303/`，核对摘要后放进 `publish/sup2api/`，`docker compose exec -T node-a sub2api-shell import …`；预检只有 node-a、primary-first-v1、无阻断（expected_revision 9）；计划 `4e649809f536490fe8b9dc0f9da79522` 05:15:28→05:15:34，`/readyz` 503 05:15:31.3→05:15:35.3 约 4 秒（`observe303.py`，输出 `plan303.out`）；迁移 0105 于 05:15:33 应用。
- 核验（`verify303.py`、`db303.sh`）：核心 0.1.303；anthropic 0.3.4、codex 0.1.11、gemini 0.3.2、moderation 0.12.1、openai 0.4.3 active、ready、restarts 0（05:15:37 内置同步），dynweight、mailer、volcengine 仍只安装未启用；两个 codex 账号（id 2、3）active、可调度；强制检测无更新；日志无 ERROR。
- 回滚：迁移 0105 改了 schema，不回退核心，出问题修复前进。

## 2026-11-18 核心 0.1.302（mailer 0.1.4，无迁移）

- Release `core-v0.1.302`（`92ca8c469`，清单 `188dc02f01e331299f100dcccc4226a08c32e784415c515637e5f69d88c8ec78`，amd64 bundle `ba765c82…d5d9`）下载到 `stage302/`，核对摘要后放进 `publish/sup2api/`，`docker compose exec -T node-a sub2api-shell import …`；预检只有 node-a、primary-first-v1、无阻断（expected_revision 7）；计划 `ee9a061b7c1c6546f64cef3aba926e19` 03:44:02→03:44:09，`/readyz` 503 03:44:05.8→03:44:09.8 约 4 秒（`observe302.py`，输出 `plan302.out`）。
- 核验（`verify302.py`，读 `/opt/sup2api/ADMIN_CREDENTIALS`）：核心 0.1.302；anthropic、codex 0.1.10、gemini、moderation、openai active、ready、restarts 0，dynweight、mailer、volcengine 仍只安装未启用（mailer 0.1.4 包已存入）；两个 codex 账号 active、可调度；强制检测无更新；日志无 ERROR。
- 回滚：核心回 0.1.301（清单 `8fb8ddbd…b0b6`）建主节点优先计划。

## 2026-11-18 核心 0.1.301（codex 0.1.10，删除 custom）

- 发布前（只读）：custom 账号 0；账号只有两个 `codex_device`（id 2、3）。不备份数据库。
- Release `core-v0.1.301`（`e0a985c81`，清单 `8fb8ddbd75afb13eceaa6041f10bfc145249ed2aa7021df4a9add3369611b0b6`，amd64 bundle `3e632f88…8684` 核对摘要）下载到 `publish/sup2api/`，`docker compose exec -T node-a sub2api-shell import -config /etc/sub2api/shell.json -manifest https://sup2api-gw-releases/sup2api/<清单>.json`；预检只有 node-a、primary-first-v1、无阻断（expected_revision 5）；计划 `bf6597af4f50491e9567509e33d397d8` 01:20:07→01:20:13，`/readyz` 503 01:20:09.7→01:20:13.7 约 4 秒（`observe301.py`，输出 `plan301.out`）；迁移 0104 于 01:20:12 应用（`plugin_key_retirements` 记入 custom）。
- 核验（`verify301.py`）：核心 0.1.301；anthropic 0.3.3、codex 0.1.10、gemini 0.3.1、moderation 0.12.1、openai 0.4.2 active、ready、restarts 0，dynweight、mailer、volcengine 仍只安装未启用；无 custom 类型；两个 codex 账号 active、可调度；强制检测无更新、无错误；日志无 ERROR。
- codex 会话缓存（codex 0.1.10 的修复）：上线前 11-17 21:56–22:13 codex-test 22 次，cache_read/input 26.6%（cache_read 945280 / input 3558073）；上线后到记录时还没有新请求，复查用 `usage_logs` 按 `api_key_id`（codex-test）与 `created_at >= '2026-11-17 17:20:13+00'` 汇总 `cache_read_tokens` / `input_tokens`。
- 回滚：迁移 0104 单向，不回退核心，出问题修复前进。

## 2026-11-17 网关 0.1.138 + 核心 0.1.298（codex 0.1.8）

- 网关：镜像 `sup2api-gateway:0.1.138` 从 ovh 复制（二进制 sha256 `82baa5c2…51f4`，与 Release `gateway-v0.1.138` 一致）；`.env` 备份 `.env.bak-gw137` 后改 `GATEWAY_IMAGE`，22:37:44 重建 node-a（在途 0），22:37:49 `/readyz` 200。
- 核心：Release `core-v0.1.298`（`0bbda1192`，清单 `108d2c7d0115cccdb9f83839be85045ea8ca26fedf6a4fbd9e415e8cff2ff9d7`，amd64 bundle `698a2d21…07a4` 核对摘要）放进 `publish/sup2api/` 导入；预检只有 node-a、primary-first-v1、无阻断；计划 `5598fc2899a37cbd5f7212e118ad618d` 22:40:05→22:40:11，`/readyz` 503 22:40:07.8→22:40:11.8 约 4 秒（`plan298.out`）；codex rollout 到 0.1.8。
- 去掉 `SUB2API_BUILTIN_TRUST_KEY`（备份 `compose.yml.bak-with-trustkey`），22:40:36 重建 node-a，22:40:42 就绪；日志没有 `unknown signing key`，内置插件“run the versions of this release”。这次各内置插件版本都已存在，没有触发新的上传验签；下一个带新内置插件版本的核心会真正走到它。
- 核验：核心 0.1.298、网关 0.1.138；6 个启用插件 active、ready（codex 0.1.8）；codex 认证方式“设备码”“Codex”；三个入口域名 `/readyz` 200；检测无更新、无错误；日志无 ERROR。
- 回滚：核心用 0.1.297 清单 `64bc544f…c431` 建主节点优先计划，并把 `SUB2API_BUILTIN_TRUST_KEY` 加回 compose；网关 `.env` 换回 `.env.bak-gw137` 后重建。

## 2026-11-17 核心 0.1.297（codex 0.1.7），删除 ChatGPT 账号

- 20:40:17（北京时间）经控制台接口软删除 `chatgpt_siwc` 账号 id 1 `gpt-plus-1117-01`（用户要求，codex 0.1.7 删除了这个类型）；之后没有 `chatgpt_siwc` 账号。
- Release `core-v0.1.297`（`f40c7831d`，清单 `64bc544f5b13d683d4565cb4fb5a0fefc60369ed6f0e434457285e857710c431`，amd64 bundle `7222fad2…bfa5` 核对摘要）放进 `publish/sup2api/`，`import -manifest https://sup2api-gw-releases/sup2api/<清单>.json`。
- 预检的计划节点仍是 `["node-a","node-b"]`：网关 0.1.137 会把停用节点放进计划，计划走到它会暂停（停止步骤还要等它的停止确认），而 node-b 已不存在。经用户同意（“继续做”）只改一行 `UPDATE updater.nodes SET stopped = true WHERE cluster_id = 'sup2api-ec2pool' AND node_id = 'node-b' AND NOT enabled`（1 行），再 `remove-node -node node-b`（登记与准入记录删除）；重新预检只剩 node-a。网关 0.1.138（`09ee340f6`）起不再需要这样做。
- 计划 `64d9594ce44007181b36ac7dafa1749c` primary-first-v1 completed，21:16:24→21:16:30；入口 `/readyz` 503 21:16:26.9→21:16:31.0 约 4 秒（`observe.py`，输出 `plan297.out`）。
- 内置插件没有升级：`builtin plugin … upload …: plugin signature: unknown signing key "sup2api-plugins-2026"`。在 `compose.yml` 补 `SUB2API_BUILTIN_TRUST_KEY`（备份 `compose.yml.bak-pre-trustkey`）后 21:17:45 重建 node-a（`/readyz` 约 5 秒后 200），codex rollout 升到 0.1.7、旧包删除；其余内置插件同版本已存在，保留原包（WARN `version already stored with other content`）。
- 核验：核心 0.1.297；anthropic 0.3.3、codex 0.1.7、custom 0.1.1、gemini 0.3.1、moderation 0.12.1、openai 0.4.2 active、ready、restarts 0；codex 账号类型只剩 `codex_device`、`codex_oauth`；三个入口域名 `/readyz` 200；30 分钟内没有请求，5xx 0；强制检测核心、网关无更新、无错误。
- 回滚：核心用 0.1.296 清单 `9fbbdae1f86286fa0b72498f74fb61352403805bc9d7ce4aa46d2f13bc7bcf90` 建主节点优先计划（插件只升不降，codex 留在 0.1.7）。

## 2026-11-17 改为单节点

切换后先按两节点（node-b 在 `127.0.0.1:18082`）运行；用户随后说“ec2pool 单节点就行了”。node-b 先 `yield`（在途 0），`disable-node -id node-b` 停用，停容器，从 `compose.yml` 删掉 node-b 服务与卷（两节点版本留在 `compose.yml.bak-two-nodes`），删除容器、卷 `sup2api-gw_node-b`、`config/node-b.json` 与证书；`switch.sh`、`rollback.sh` 改成单节点。node-a 全程没有中断。
- （网关 0.1.138 起已修：停用节点不进计划，`remove-node` 不再要求 `stopped`。）`remove-node` 删不掉 node-b 的登记：它要求 `updater.nodes.stopped` 为 true 且 5 分钟内没有心跳，而 `stopped` 只由升级计划的停止步骤写入，手动停容器或停用都不写。所以 `updater.nodes` 里留一行 `enabled=false` 的 node-b：预检、网关自更新与更新检测只看已启用的节点，不受影响；控制台节点页（核心心跳）只剩 node-a，升级页的节点列表里会显示停用的 node-b。
- Caddy 曾为 node-b 加过备用上游（`lb_policy first` + `/readyz`），因为 `admin off` 没能 reload，随即用备份 `sup2api-customer.caddy.bak-20261117-pre-node-b` 还原，运行中的配置始终未变。
- 核验：`/nodes` 只有 node-a（核心 0.1.296）；强制检测核心、网关都没有更新、无错误；三个入口域名 `/readyz` 200。

## 2026-11-17 由 deploy/single 切到网关托管，核心 0.1.296 + 网关 0.1.137

- 用户要求“ec2pool 也改成 docker compose 的形式更新上去”，在卡片上选了“网关托管，先发 GitHub 0.1.296”。旧部署：`/opt/sup2api`（`next/deploy/single`，非托管，没有网关），app、app-2 两个核心跑 `sup2api:179142ae6`（自报 0.1.295），插件包存在旧核心的本机目录 `plugins/blobs`。
- 核心基线：GitHub Release `core-v0.1.296`（`cf4718dcb`，含 `179142ae6`），清单 sha256 `9fbbdae1f86286fa0b72498f74fb61352403805bc9d7ce4aa46d2f13bc7bcf90`，amd64 bundle `5a59c505…2777` 下载后核对摘要放进 `publish/sup2api/`。网关镜像 `sup2api-gateway:0.1.137` 从 ovh 复制（二进制 sha256 `fc74f2f2…2618`，与 Release `gateway-v0.1.137` 一致）。
- `switch.sh`（北京时间）：19:25:47 起 releases、停旧 app 与 app-2（pg、redis、market 不停）；19:25:59 把旧 `sup2api_appdata` 卷的 `plugins/blobs/`（11 个包）原样复制到 node-a 卷的 `plugin-blobs/`（托管后核心改从网关取包，不复制则已装插件起不来）；19:26:00 `init -manifest https://sup2api-gw-releases/sup2api/<清单>.json` 建 `updater` schema 并导入基线；19:26:05 node-a `/readyz` 200，19:26:09 node-b 200。入口约 6 秒不可用（流量几乎为 0）。
- 核验：两节点网关 0.1.137、核心 0.1.296（`updater.nodes` 两行 `self_update` true）；anthropic 0.3.3、codex 0.1.6、custom 0.1.1、gemini 0.3.1、moderation 0.12.1、openai 0.4.2 两节点 active、ready、restarts 0（dynweight、mailer、volcengine 原本只安装未启用）；三个入口域名 `/readyz` 200，超管经 https 域名登录、`/plugins`、`/accounts`、`/usage` 200；更新源为默认 GitHub，强制检测核心、网关都没有更新、无错误；日志只有验签关闭与 moderation 钩子匹配未安装平台的 WARN。
- 数据：同一个库、同一个 schema（`ab35f2f6…187d`），没有迁移；新增 `updater` schema，旧核心不读它。
- 回滚：`sh /opt/sup2api-gw/rollback.sh`（停 node-a，卷保留；`/opt/sup2api/compose.sh up -d app` 起旧核心，入口端口相同，Caddy 不用改）。旧 compose、镜像 `sup2api:179142ae6` 与 `sup2api_appdata`/`appdata2` 卷都保留。
