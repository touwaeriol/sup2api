# 网关托管的主节点优先升级

> 状态（2026-10-03）：OVH 已采用网关托管四节点部署，核心 v0.1.7 上线记录见 [验证记录](../../docs/audits/2026-10-02/MULTINODE-VALIDATION.md)。组件源码、构建入口和命令统一使用 `gateway`。为兼容已有部署，容器仍读取 `/etc/sub2api/shell.json`，现有管理 socket、数据库字段及协议名称保持兼容。从旧 mTLS/`allowed_peers` 网关迁移须先人工更新全部网关。

本入口适用于 Linux、共享 PostgreSQL 和 Redis 的集群。网关监听公网入口并管理本机核心进程，核心只监听回环地址。节点间通过HTTPS传输，并使用Redis中每节点一份的可复用密钥鉴权。每个节点运行自己的核心及插件，不通过远程RPC代替插件运行。

升级允许短暂停机：所有从节点先转发到主节点并停止核心，随后主节点停止、执行迁移并启动目标版本。主节点就绪后恢复服务；从节点在后台逐个更新，核心及插件就绪后恢复本地服务。主节点停机期间所有入口返回 503，管理页面也暂时不可用；网关及其本机管理 socket 保持在线。

核心发布清单不签名，按摘要校验；内置插件包仍用插件签名密钥签名。核心替换不会覆盖数据库中已安装、停用或已删除的插件状态。新版本必须包含实际可执行的数据迁移和旧任务兼容逻辑，停机并不自动转换任务数据。

## 构建与发布

正式发布由 GitHub Actions 完成：推 tag `core-v<X.Y.Z>` 或 `gateway-v<X.Y.Z>`，流水线构建、打包并发布 GitHub Release，节点经更新源检测并一键更新（见 [github/README.md](github/README.md) 与下文“更新源与一键更新”）。本节其余内容是不经 GitHub 的离线、手工发布。

从 `next/` 构建网关镜像（`--build-arg VERSION=<X.Y.Z>` 写入网关版本，不传时为 `0.0.0-dev`）：

```sh
docker build -f deploy/gateway/Dockerfile --build-arg VERSION=X.Y.Z -t sup2api-gateway:local .
```

网关镜像不包含业务核心。现有 `next/Dockerfile` 的 `build` 阶段继续用于构建带控制台的核心和插件；取出 `/out/bin/` 和需要随首装提供的 `/out/builtin/`，组成独立的暂存目录。不要把市场缓存、私钥或构建目录放入暂存目录。

在可信 Linux 构建机安装 `sub2api-release`（`go build ./gateway/cmd/sub2api-release`），然后打包：

```sh
sh deploy/gateway/package-release.sh ./stage ./publish v1.0.0 SOURCE_COMMIT
```

脚本从实际核心读取迁移库存摘要，再写出清单（不签名，清单文件的 sha256 就是发布摘要）。输出 `<manifest-digest>.json` 和 `<bundle-digest>.tar.gz`，将两个文件以原名发布在 `release_origin` HTTPS 目录下。例如 `https://releases.example.com/sup2api/`。不得覆盖已有内容或复用同一个 release ID 发布不同内容。网关拒绝重定向下载、摘要或大小不符、符号链接和未声明文件；`release_origin` 就是网关信任的发布源，只放你自己打的发布。

含数据库迁移时，在脚本最后追加第五个参数 `SOURCE_SCHEMA`，填写升级前基线核心的 `schema-contract` 输出。目标摘要仍从暂存目录的新版核心读取。例如：

```sh
sh deploy/gateway/package-release.sh ./stage ./publish v1.1.0 SOURCE_COMMIT SOURCE_SCHEMA
```

不提供 `SOURCE_SCHEMA` 表示数据库契约不变；若与实际集群不符，预检会拒绝升级。直接使用 `sub2api-release pack` 时，对应参数是 `--schema-before` 和 `--schema`。发布前备份数据库；数据库迁移开始后不能通过切回旧二进制撤销。

`linux-static-v1` 要求核心及插件使用兼容的 Linux 静态二进制；发布机和运行机架构须一致。跨架构构建时直接调用 `pack --os linux --arch ... --schema ...`，多个平台用可重复的 `--platform linux/<arch>=<stage>` 打进同一个清单；schema 必须来自该源版本的实际核心，不能填写任意标签。

## 配置节点

复制 `.env.example` 到 `.env`，填入同一集群的数据库、Redis 和主密钥。迁移已有集群时必须沿用原密钥及数据库。外壳只运行 0.1.290 起的核心。

修改 `config/node-a.json` 和 `config/node-b.json`：

- 两节点使用相同的 `cluster_id`、`primary_node` 和发布源。没有发布公钥要配（旧配置里的 `trusted_keys` 不再读取，可以删掉）。
- `allow_private_update_sources`（可选，默认 false）：`url` 类型的更新源允许解析到内网地址，用于集群内网的发布镜像。GitHub 来源始终只连公网。
- 节点 ID、状态卷、TLS 私钥各自独立。示例把 `node-a` 指定为升级协调节点。
- `peer_url` 必须能被另一节点访问，服务器证书DNS SAN必须包含相应主机名并允许ServerAuth。客户端验证CA及主机名，不再要求客户端证书或`allowed_peers`列表。
- `peer_auth_key` 留空时自动生成节点密钥；也可在配置中指定至少32字节的独立随机密钥。非空的`SUB2API_PEER_AUTH_KEY`环境变量覆盖配置文件，空值视为未设置；Compose分别使用`NODE_A_PEER_AUTH_KEY`与`NODE_B_PEER_AUTH_KEY`，不要给两个节点填写同一个密钥。
- 心跳只续期Redis登记，不更换密钥。登记丢失时自动模式生成新值，配置模式登记原值，其他节点直接读取新登记。普通缓存过期不重启核心；Redis不可用时拒绝新的节点间请求，已通过鉴权的流式请求按原有生命周期收尾。登记失败（节点被停用、同 ID 实例仍在线或 Redis 不可用）时网关不退出，保持维护态并持续重试。
- `plugin_max_bytes`（可选，默认 64 MiB，与核心的插件包上限同一常量 `runtimecontract.PluginPackageMaxBytes`）限制网关保存的单个插件包；`plugin_store_max_bytes`（可选，默认 16 GiB）限制整个 `plugin-blobs/` 目录，满了以后存包失败，直到 GC 清掉未引用的包。插件包保存在状态卷的 `plugin-blobs/`：每个节点保存自己接到的上传和自己用过的包，缺包时向任一在线的持有节点取，节点之间不推送；插件包与主节点无关，`set-primary` 不影响插件的安装与升级。持有某个包的节点都离线时，需要它的节点装不上，控制台显示 `package_gone` 与问过哪些节点（CONTRACTS §114）。
- Redis 登记只保存节点密钥的 SHA-256（peer 协议 2，2026-11-05 起）。更换网关版本时集群内所有网关必须一起换，期间不要创建核心升级计划；旧网关与新网关互不接受。`REDIS_URL` 走 TCP 时必须带密码，否则网关拒绝启动（`allow_unauthenticated_redis: true` 仅供隔离测试）。
- 网关默认为其核心设置 `SUB2API_PLUGIN_LANDLOCK=true`（Linux）：管理 socket 令牌文件对同 UID 的插件没有隔离作用，隔离来自 Landlock（CONTRACTS §43.8）。核心 0.1.304 起自己也默认开启；网关仍显式写 `true`，是为了它可能启动的旧核心。`core_env` 里显式给出的值优先；`plugin_landlock_disabled: true` 写 `SUB2API_PLUGIN_LANDLOCK=false` 关闭它。核心启动日志里的 `plugin sandbox:` 几行和控制台节点页的“插件沙箱”一列说明这个节点的内核实际限制到哪一层（文件 5.13、截断 6.2、TCP 6.7、抽象 socket 与信号 6.12、路径型 unix socket 7.1、UDP 7.2），缺的层逐条记 WARN；容器的 seccomp 配置须放行 `landlock_*` 三个系统调用（Docker 默认配置放行）。
- `release_origin` 下载使用系统根证书及 `ca.crt`，可用公开证书；内容由清单摘要约束，节点密钥不会发往发布源。
- `trusted_proxies` 默认为空；若前置 TLS 终止代理，填入其实际连接源 CIDR，代理须覆盖客户端带来的转发头。仅这些直接代理的单值 `http` / `https` 协议头可信。
- 每节点的 `certs/<node>/` 放置 `node.crt`、`node.key` 和 `ca.crt`，权限须允许容器 UID 1000 读取。不要挂载 CA 私钥。

Compose 仅映射两个回环 HTTP 入口，生产反向代理分别指向 8081/8082，检查 `/readyz`。7443 只供容器内网使用；跨机器部署应通过私网连接，核心 18080 不对外开放。外层反向代理的可信网段需要通过网关配置显式设置，不能信任任意客户端转发头。

## 隔离加固（2026-10-10）

插件与网关、核心目前以同一 UID 1000 运行，文件权限无法把它们分开。以下措施各自缩小插件能拿到的东西，彻底解决要靠插件独立 UID（见审查文档 P0-1）。

- **管理 socket 令牌**：网关每次 `serve` 生成 32 字节随机令牌，写入 `management_token_file`（默认管理 socket 旁的 `runtime/shell.token`，0600，目录 0700，原子替换，不跟随符号链接）。管理 socket 上的所有请求（升级 API、插件包、`/metrics`）必须带 `Authorization: Bearer <令牌>`，否则 401。核心只收到令牌文件路径 `SUB2API_UPDATER_TOKEN_FILE`，不收到令牌本身。`status`/`pause`/`resume`/`cancel`/`rollback`/`enable-node`/`disable-node` 自动读同一文件，须以网关用户（UID 1000）在网关运行时执行；`-token-file` 可指定其他路径。令牌每次网关启动更换，旧令牌立即失效。
- **不带令牌一律拒绝**：曾经的迁移开关 `allow_tokenless_management` / `SUB2API_ALLOW_TOKENLESS_MANAGEMENT` 已删除（核心自 2026-10 起都带令牌）。旧配置里留着这个键或环境变量不影响启动，外壳直接忽略，可以顺手删掉。
- **核心环境白名单**：网关不再把自己的整份环境传给核心，只传进程基本变量与核心实际读取的配置（`internal/supervisor` 的 `inheritedEnv`，测试会扫描 `server/` 源码确认没有遗漏），再加上网关显式设置的 `DATABASE_URL`、`REDIS_URL` 与托管变量。`SUB2API_PEER_AUTH_KEY`、`SUB2API_GATEWAY_*` 及其他无关变量留在网关。确需额外变量时，在配置中用 `core_env_inherit` 列名或用 `core_env` 显式给值。`HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`（含小写写法）属于进程基本变量，会传给核心：设了它们，协议转换到 Gemini 时下载客户媒体链接就经这个代理（不设则直连），插件市场与插件包下载也经它；账号的上游请求只走账号自己的代理，不受影响；插件进程拿不到这些变量（CONTRACTS §88.5）。经这个代理下载时核心只能拦字面私网地址，域名由代理解析：环境代理应当自行拒绝访问内网地址；这一层核心无法检查。
- **网关进程不可转储**：`serve` 启动时设置 `PR_SET_DUMPABLE=0`，同 UID 的进程（插件）不能再读网关的 `/proc/<pid>/environ`、内存和文件描述符。
- **Redis 密码**：缓存必须开启 `requirepass`，`REDIS_URL` 写成 `redis://:<密码>@host:6379/0`（`valkey://` 同理）。密码用十六进制（`openssl rand -hex 32`），否则须百分号编码。网关检测到无密码 URL 时记警告。go-redis 用 `HELLO 3 AUTH default <密码>` 认证，对尚未设密码的服务器同样成功，因此可以先改客户端、再给服务器设密码。
- **容器**：节点 `security_opt: no-new-privileges:true`、`cap_drop: [ALL]`，镜像保持 `USER 1000:1000`。

## 首次启动

初始化独立的 `updater` schema 并导入基线发布（命令输出该清单摘要）：

```sh
docker compose run --rm node-a init -manifest https://releases.example.com/sup2api/MANIFEST_DIGEST.json
```

全新业务数据库首次启动主节点时显式传入 `serve -bootstrap`。该参数允许初次业务迁移、管理员创建及内建插件安装；准备阶段仅允许推进插件激活，仍不接收业务请求或领取业务任务。正常升级的迁移许可与首装独立，不重做管理员创建或内建插件导入。可通过临时 Compose override 将 `node-a.command` 设为 `["serve", "-bootstrap"]`，启动完成后移除 override 并恢复普通 `serve`。

已有且契约一致的业务数据库直接启动：

```sh
docker compose up -d node-a
docker compose exec node-a sub2api-gateway status
docker compose up -d node-b
```

先确认主节点完成首装，再启动从节点。从节点从主节点拉取已校验的核心制品；插件依据共享数据库中的安装状态自行同步（插件包向持有它的任一在线节点取），全部就绪后才能准入。插件包不再依赖主节点，但**新节点加入（以及从节点的核心升级）仍要主节点在线**：核心发布包只从主节点取（非主节点的准备步骤以主节点的 `<peer_url>/internal/blobs` 为来源，`internal/control/engine.go`），主节点离线时新节点拿不到核心、起不来。

## 更新源与一键更新

网关的升级主节点每 6 小时从更新源检测核心与网关两条线的新稳定版（启动约 1 分钟后第一次，保存设置后立即一次）。有 `system:update:read` 的管理员在控制台左上角版本号打开“版本与更新”看结果，有 `system:update:execute` 的可以点“一键更新”：网关导入并校验发布、预检、建升级计划，进度在“核心升级”页。不会自动升级。契约见 [CONTRACTS §102](../../docs/CONTRACTS.md)。

**更新源**在控制台“设置 → 在线更新”（`settings:manage`，保存要二次验证）：

- **GitHub**：填 `owner/repo`，默认 `Sub2API-Devs/sup2api`。公开仓库不用令牌。私有仓库打开“私有仓库”，填只读的细粒度令牌（Contents: Read）。只读最新 50 个 Release，只认 `core-v*`、`gateway-v*` tag 的稳定版。
- **自定义 URL**：填 HTTPS 发布目录（其下有 `releases.json`，格式 `sub2api-releases-v1`）。需要鉴权时打开“需要访问密钥”，填 Bearer 密钥。目录在内网时各节点配置 `allow_private_update_sources: true`；否则只允许公网地址。

令牌和密钥只写不读，用 `SUB2API_MASTER_KEY` 加密存在 PG（网关取 `core_env` 里的值，没有再取自己的环境变量；各节点必须相同），只发给 `api.github.com` 或该目录的源站，不随跨源跳转发出。网关没有主密钥时保存密钥会失败（`master_key_missing`），公开来源不受影响。改了来源类型、仓库或地址而没重新填密钥时，旧密钥被清除。发布不签名：网关安装更新源发布的版本，只核对下载内容与清单摘要，所以来源只填你信任的仓库或目录。这防得住下载途中或镜像上的损坏与替换，防不住仓库、GitHub 账号或来源服务器本身被攻破。更换更新源本身不升级任何节点。

**内网镜像**：节点不能直连 GitHub，或想把发布固定在自己的服务器上时，用 `sub2api-release mirror` 把 Release 原样（清单字节与摘要不变）转存成 `url` 来源的目录，用任意 HTTPS 服务器发布，控制台填目录 URL：

```sh
cd next/gateway && go build -o /usr/local/bin/sub2api-release ./cmd/sub2api-release
# 私有仓库：令牌放在环境变量里，用 --token-env 传变量名，不写在命令行上
sub2api-release mirror --github owner/repo --token-env GITHUB_TOKEN --latest 3 --out /srv/sub2api-releases
sub2api-release mirror --github owner/repo --tags core-v0.1.293,gateway-v0.1.136 --out /srv/sub2api-releases
```

再次运行只补新的版本、合并 `releases.json`。目录的源站需要鉴权时，在控制台给 `url` 来源填同一个 Bearer 密钥。

**核心一键更新**：不改库的版本走 `rolling-v1`，直接建计划；要停服的 `primary-first-v1` 先弹出原因，确认后才建计划。主节点经更新源下载核心包，从节点照旧从主节点拉取。

**网关一键更新**：只有每个启用节点都报告了网关版本且能自更新（Linux，`<root>/gateway` 可写）时出现；否则面板提示用清单里的镜像手动换（见下文“升级和恢复”最后一段）。计划 `gateway-rolling-v1`：各节点先各自经更新源下载并校验新网关，然后从节点在前、主节点最后，每个节点让出流量（`/readyz` 503，入口切走）、停核心、换网关二进制、重新拉起原版本核心、确认新网关登记、收回流量。核心版本不变；停用的节点不参与，保留原网关。只有一个节点时没有节点承接流量，中断约 10 秒，控制台要求确认。暂停的网关计划可以 `rollback`（`gateway-rollback-v1`，把每个已切换的节点换回上一版本）。

自更新的文件都在状态卷（`root`，Compose 里是 `/var/lib/sub2api`）的 `gateway/` 下：

| 路径 | 内容 |
|---|---|
| `gateway/<版本>/sub2api-gateway` | 已安装的网关（按清单摘要校验过，0755） |
| `gateway/current` | JSON：启动哪个版本（`version`、`digest`），以及切换前的版本（`previous`，回滚用；镜像自带的记为 `bundled`） |
| `gateway/handoff` | 启动交接的失败计数 |

容器每次启动先运行镜像自带的网关：`current` 指向更高的已安装版本时，它校验该二进制的 sha256 后 exec 过去（环境变量 `SUB2API_GATEWAY_HANDOFF` 记被交接的版本）。`current` 损坏、摘要不符、版本不比镜像新、或该版本连续 3 次启动都没走到服务时，记 WARN 并用镜像自带的网关服务。确认新网关后只保留 `current` 与 `previous` 两个版本。`sub2api-gateway yield-status` 显示在跑的 `gateway_version` 与镜像自带的 `bundled_version`；`sub2api-gateway version` 打印二进制版本。

**插件签名密钥**（核心发布用它重签内置插件）只放在 GitHub 仓库的环境 `release` 里：Secret `SUB2API_PLUGIN_SIGNING_KEY`、Variable `SUB2API_PLUGIN_KEY_ID`（配置方法见 [github/README.md](github/README.md)）。私钥不进仓库、不上节点，不要在终端、日志或工单里输出。发布清单不签名，没有发布密钥。

**从网关 0.1.135 过渡**：0.1.135 及更早的网关只认签名清单，装不了不签名的发布，所以第一次换上不验签的网关必须手动换镜像，而且只能换到 0.1.136（它负责转换旧的签名发布记录，0.1.137 起不再带这段转换）（见下文“升级和恢复”最后一段），之后核心与网关都能一键更新。混跑期间新旧网关都不要导入或应用更新；基线换成不签名的核心之后，网关不能再回到 0.1.135。

## 升级和恢复

常规做法是控制台“版本与更新”里的“一键更新”（见上一节）。离线发布先导入，再在控制台“核心升级”页选择版本、执行预检并创建计划：

```sh
docker compose exec node-a sub2api-gateway import -manifest https://releases.example.com/sup2api/NEXT_MANIFEST_DIGEST.json
```

改库的版本走主节点优先（`primary-first-v1`），执行顺序如下；不改库且清单策略为 `rolling` 的版本在至少两个节点就绪时走 `rolling-v1`，逐个节点让出流量后重启，服务不中断（CONTRACTS §82）。主节点优先的顺序：

1. 所有节点预下载并验证发布包。
2. 所有从节点将入口转发到主节点，排空并停止核心及插件；必须收到真实停止确认，不能把失联当作停止。
3. 主节点进入维护，排空并停止旧核心。此时整个集群暂时不可用。
4. 只在主节点启动目标核心并运行数据库迁移，按已批准版本加载插件。检查通过后恢复主节点服务。
5. 从节点依次启动同一目标核心、同步插件并通过检查，再恢复本地流量和后台任务；此前继续转发主节点。

带迁移 0106（账号协议绑定，CONTRACTS §110）的核心版本升级后，跨平台协议转换默认关闭，依赖转换的客户端会收到 404，直到在账号上为模型开启。升级前先按 [../rollback/protocol-binding-release.md](../rollback/protocol-binding-release.md) 导出近 30 天实际发生过的转换，升级后照着配置。

### 停机重新部署（计划带不过去的版本）

Host API 变了的核心版本（例如 0.1.304，见它在 CHANGELOG 里的升级须知）不能用计划升级：新核心要求的内置插件只在计划完成后才升级，主节点一直不就绪。改为停机重新部署（网关 0.1.139 起，CONTRACTS §34“停机重新部署”）：

停机前准备好（不影响服务）：核心清单和 bundle 已放进发布源（`release_origin` 能取到）或已由更新源导入；新网关镜像已拉到每台机器（`pull_policy: never` 时尤其如此）。

0. 先 `docker compose exec node-a sub2api-gateway status`：有 running/paused 计划时先 `cancel`（尚未切流）或 `rollback` 收尾，`redeploy` 遇到在途计划会拒绝。
1. 停掉全部节点的网关（`docker compose stop`），确认每个节点的停止命令都已返回、容器已退出；把镜像换成支持目标核心的网关版本。这次升级单向（带迁移），需要回退时只能连同数据库一起恢复，所以在这里确认有一份停机后的数据库备份（或数据库服务的时间点恢复覆盖到这一刻）。
2. 再等 20 秒，让各节点在 Redis 里的实时心跳过期；在任一节点以同一份配置执行一次：

   ```sh
   docker compose run --rm node-a redeploy -config /etc/sub2api/shell.json -manifest <核心清单 URL>
   ```

   版本已由更新源（GitHub Release）或 `import` 导入时改用 `-digest <摘要>`。它先建好网关表，给了 `-manifest` 就导入清单（同 `import`，写 `updater.releases`）；有节点心跳还新鲜、有核心还登记在线或有 running/paused 计划时拒绝；否则把集群基线换成这个版本、清掉本集群的准入记录。不停进程，不碰核心的库。被拒时清单已经导入（多一行 `updater.releases`，无害），处理完原因后原样重跑即可。
3. 以 `serve -bootstrap` 启动主节点（临时 Compose override 把 `command` 设为 `[serve, -bootstrap, -config, /etc/sub2api/shell.json]`）：按首次启动处理，迁移、内置插件导入都在这一步。`/readyz` 200 后去掉 override。主节点没就绪就要重启时，仍带 `-bootstrap`（准入记录还是空的）。这一步日志里会有一批预期的 ERROR，来自启动时还没换成新版本的旧内置插件：`was built for an older plugin protocol`，以及随后它们被停掉时的 `plugin process exited … signal: killed`（核心 0.1.304 演练里各约 20 行和 10 行），内置插件升级完就不再出现；判断成败看 `/readyz`。
4. 主节点 `/readyz` 200 之后再启动其余节点，它们跟随主节点启动同一版本。从节点抢在主节点之前启动会先写下准入记录，主节点就不再按首次启动处理；这时停掉全部节点，重新执行第 2 步。
5. 把非内置插件逐个装上为新核心构建的版本。

回退只能用旧版网关、旧版核心和升级前的数据库备份一起恢复。

核心升级期间，主节点不把流量转发给从节点；正常服务期间的 CPU 保护可将主节点新请求转移到满足条件的从节点。网关执行后台升级，不要求浏览器保持连接。清单不等于启动授权：核心会核对数据库中的本次 boot ID、摘要及准入记录。所有节点完成才提交新集群基线。

管理页面暂时不可用时，仍可从本机 Unix 管理 socket 操作：

```sh
docker compose exec node-a sub2api-gateway status
docker compose exec node-a sub2api-gateway pause -id UPGRADE_ID
docker compose exec node-a sub2api-gateway resume -id UPGRADE_ID
docker compose exec node-a sub2api-gateway rollback -id FAILED_UPGRADE_ID
```

节点管理可从控制台或本机恢复命令进行：

```sh
docker compose exec node-a sub2api-gateway disable-node -id node-b
docker compose exec node-a sub2api-gateway enable-node -id node-b
```

停用节点会持久撤销身份和核心准入，再使Redis登记失效；重新启用只允许它重新登记，仍须完成版本同步、插件加载和准入。主节点停机期间通过本机管理socket执行恢复命令，不依赖控制台在线。

更换主节点用 `sub2api-gateway set-primary -node <节点>`（在集群变更锁内执行；没有进行中的升级计划，新主节点必须已启用且运行当前基线），它只改 PG 里的 `primary_node`。**之后还要把每个节点网关配置（容器里的 `/etc/sub2api/shell.json`，本目录示例是 `config/node-*.json`）里的 `primary_node` 改成新主节点，并逐个重启网关**：升级计划和核心制品来源读 PG，但网关切流的检查读配置文件（`LocalRuntime.PrimaryNode`，`internal/control/runtime.go` 的 `redirectLocked`：主节点不转发给从节点、从节点只转发给“配置的”主节点）。配置不改时，下一次主节点优先升级里从节点转发到新主节点会被拒绝（`followers may redirect only to the configured primary`），升级停在转发步骤。

暂停在步骤边界生效，已开始的排空/替换会安全收尾。仅尚未切流的计划允许 `cancel`。失败后先修复原因再恢复同一计划；从节点更新失败时，已就绪主节点继续服务，从节点继续转发。主节点更新失败时保留维护状态，不擅自启动旧从节点。

`rollback` 仅用于数据库和相关协议契约相同、当前插件仍兼容的版本，回到最近一次全部节点完成的基线。取得协调锁及全部节点操作锁后，以新的主节点优先恢复计划接替原计划，也会产生停机窗口。跨数据库契约或不兼容协议的回退被拒绝；需修复迁移或通过数据库备份恢复。不会直接改软链接、强杀写入进程或自动执行向下迁移。从数据库备份恢复后不需要清理 Valkey：核心 0.1.290 起授权策略只在各节点内存里，Valkey 里没有授权的键（CONTRACTS §99.4）。

锁用 Redis 互斥并续期；失锁取消执行，不写成功结果。进程或整机重启后，重新取锁并根据 PG 步骤和本地日志恢复。发现上一进程仍存活、遗留子进程组或无法确认的启动意图时，会拒绝启动第二个核心；运维须先核对进程和日志，不能仅凭锁过期删除状态文件。

网关二进制可以一键更新（上一节）；基础镜像与系统依赖仍通过容器镜像更新：先 `sub2api-gateway yield` 让出并排空，等 `/readyz` 变 503 且进行中的请求降到 0，再重建容器（CONTRACTS §82.3）。新镜像自带的网关版本高于 `<root>/gateway/current` 里的已安装版本时直接用镜像自带的。核心处于 running/paused 计划时不要换网关；不要让新旧网关同时访问同一节点状态卷。

## 自动验证入口

`go test -race ./gateway/... ./runtime-contract/... ./server/internal/app/...` 执行基础测试。设置 `TEST_DATABASE_URL`、`TEST_REDIS_URL` 可运行真实持久化/锁测试；Linux 下再设置 `TEST_CORE_V1`、`TEST_CORE_V2` 指向两个不同源快照构建的核心，执行 `TestRealCoreRollingUpgrade`。该测试需测试数据库用户具备 CREATEDB，仅使用隔离测试环境。

`TEST_SHELL_NODES=3` 可追加三节点验证。设置 `TEST_BUILTIN_DIR`（包含已签名 `.s2plugin`）和 `TEST_BUILTIN_KEY=keyId=base64-public-key` 后，会首装真实插件并验证升级前后插件批准版本/状态不变。`TEST_EXPECT_MIGRATION` 填写只加在 R2 独立源码副本中的测试迁移文件名（该迁移须创建只有一行的 `managed_upgrade_probe` 表），测试会确认它只应用一次。默认保留插件安全隔离；仅在明确不验证沙箱的环境中才使用 `TEST_PLUGIN_DEV_MODE`。再设置 `TEST_MOCK_URL`（用 `deploy/mock-upstream` 启动的模拟上游，核心须能访问）并让 R2 测试迁移包含 `pg_sleep`，会运行 `TestRealCoreFaultsDuringPrimaryFirstUpgrade`：网关重启、新节点加入、迁移中断、Redis 断连、跨停机的异步视频任务与跨登记过期的 SSE。2026-10-02 所用脚本见 [final-check.sh](../../docs/audits/2026-10-02/evidence/final-check.sh)。

全部 `TestRealCore*` 的准备与环境变量以 [check.sh](../ci/check.sh) 的 `plugins`、`realcore` 两步为准：两个核心的版本号（`-X main.Version`）须落在内置插件的 `hostCompat`（`>=0.1.304 <0.2.0`，dynweight 是 `>=0.1.312`）里，否则核心拒装它们；R2 另带 `TEST_BUILTIN_NEXT_DIR` 里 dynweight 的升级夹具 `0.7.1-test`（`plugins/dynweight/testdata/upgrade`，迁移 0004）；插件包测试还要 `TEST_UPLOAD_PACKAGE`（带原生 UI 的 guard）、`TEST_MARKET_DIR`、`TEST_MARKET_KEY`。测试数据库用户要有 CREATEROLE（或是超级用户），PostgreSQL 16 及以上：核心给每个用数据库的插件建自己的数据库身份，没有 CREATEROLE 时 dynweight、guard 等迁移失败，报 `plugin_db_isolation_unavailable`（CONTRACTS §130.1）。测试自己 `CREATE DATABASE`，所以这个用户是测试库的属主，核心启动时能收回 PUBLIC 的 `TEMPORARY`；`pg_hba.conf` 还要允许插件身份 `plg<库OID>_*` 以密码登录测试库。Redis 可带密码（`redis://:密码@主机:端口/0`）。
