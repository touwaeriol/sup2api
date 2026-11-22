# AGENTS.md：在 sup2api 仓库工作的 AI 与成员

这是仓库的总规则，开始任何工作前先读完它。子目录可以有自己的 `AGENTS.md`，它只补充本目录特有的规则，本文件在那里同样适用。

本文只写入口、结论和规则。细节以文档为准：`next/docs/CONTRACTS.md`（开发契约，权威）、`next/docs/ARCHITECTURE.md`（整体设计）、`CHANGELOG.md`（发布记录）。文档之间冲突时 CONTRACTS 优先；文档与代码冲突时以代码为准，并把文档改对。

## 沟通

- 始终用中文回复，文档也用中文写。代码、注释、提交说明用英文。
- 用户的错别字和大小写不一致，按上下文和常识理解。
- 不凭记忆回答项目问题，先读文档和代码。报告里要指出上级（用户、主控、设计文档）写错的地方。
- 报告"测试通过"前确认数据库测试真的跑了，没有成片的 SKIP。

## 仓库里有什么，哪部分是现行的

**现行代码全部在 `next/`**（sub2api-next 平台）。它是一个 Go 1.27 多模块工作区（`next/go.work`）加一个 Vue 3 控制台：

| 路径 | 是什么 |
|---|---|
| `next/server` | 核心服务（模块化单体）：控制台 API、网关流水线、计费、插件运行时，二进制 `sub2api` |
| `next/gateway` | 节点外壳 `sub2api-gateway`：公网入口、管理本机核心进程（supervisor）、托管升级；另有发布打包工具 `sub2api-release` |
| `next/runtime-contract` | 外壳与核心的协议，以及核心、网关、SDK 共用的数据库层 `gormpg`（打开连接、组合器、SQL 检查器 `gormpg/sqlcheck`） |
| `next/sdk` | 插件契约：`proto`/`gen`（gRPC）、`manifest`、`pluginsdk`（插件开发包）、`pgdb`（插件的 GORM 句柄）、`hierarchy`（表里的树：物化路径，核心的用户上级也用它）、`testpg`（测试用 PostgreSQL） |
| `next/plugins/*` | 官方插件，每个是独立 Go 模块；`plugins/released.json` 记录线上版本 |
| `next/tools/sub2api-plugin` | 插件 CLI：keygen、build、pack、sign、verify、index、`db diff/check`、`dev` |
| `next/web` | 控制台（Vue 3 + Vite + TS + Pinia + Tailwind），`packages/` 下是共享给插件的 `@sub2api/ui`、`@sub2api/host`、`@sub2api/vite-preset` |
| `next/deploy` | 镜像构建脚本、网关托管部署（`gateway/`）、单机栈（`single/`）、e2e 栈、单向发布进行中的发布与回退手册（`rollback/`，发布完成、不能再回退后删除；现在有协议绑定的升级手册 `protocol-binding-release.md` 和它的只读查询 `protocol-binding-conversions.sql`，以及 0.1.307 计费改造（迁移 0122/0123）的单向发布手册 `0.1.307-billing-release.md`） |
| `next/e2e` | 验收测试，需要部署好的栈；CI 只编译不运行 |
| `next/protocol-codec` | 早期为 ccgateway 引入的协议转换库。`next/` 里没有模块 import 它（next CI 仍按普通模块检查它）；但根目录旧版 `backend/` 经 `replace` 依赖它（根目录 `Dockerfile` 也复制它），所以暂不能删（CONVERT-MATRIX.md 开头）。不要在新代码里引用 |
| `next/docs` | 契约、架构、设计稿、交接、审计记录 |

CCGateway 插件（Claude Code 托管账号、它的 companions 与 `cli-reverse`、核心里的 `internal/ccgateway` 和只为它服务的 helper history、provider resources、fallback credits）已在 0.1.290 删除，只留在 Git 历史里：最后的源码是 `317b0c331`（`git show 317b0c331:next/plugins/ccgateway/manifest.json`）。迁移 0102 删掉了它的表和插件行（CONTRACTS §101.5）。

**仓库根目录的 `backend/`、`frontend/` 是旧版 Sub2API**（Ent ORM + Gin，前端用 pnpm），连同根目录的 `Dockerfile`、`Makefile`、`deploy/`、`docs/`、`openspec/`、`DEV_GUIDE.md` 以及 `.github/workflows/backend-ci.yml`、`release.yml`。它们只作参考保留（ARCHITECTURE §16.1）：`next/` 不引用它们，`go.work` 不包含它们，2026-11-05 导入以来没有提交改动过它们。除非用户明确要求，不要改动、不要在它们上面开发新功能。`DEV_GUIDE.md` 描述的就是这套旧代码。

根目录的 `CHANGELOG.md` 记录的是 `next/` 的发布，属于现行文档。

## 架构

先读 ARCHITECTURE §2（部署、节点内部、多节点协作图）和 §3（核心与插件的划分），再按需读 CONTRACTS 对应章节。下面是现状要点。

### 核心（`next/server`）

- HTTP 用 Gin（`internal/httpapi`）。控制台接口在 `/api/v1`，网关在 `/v1` 等平台声明的端点，插件路由在 `/api/v1/p/:key/*`。路由用 `r.Public` / `r.Self` / `r.Perm` / `r.PermAny` / `r.PermScoped` 注册，启动时 `httpapi.CheckRoutes` 自检路由表（CONTRACTS §3.3）。
- 模块化单体：`internal/` 下每个业务模块（`iam`、`authz`、`apikey`、`group`、`account`、`billing`、`usage`、`gateway`、`plugin`、`event`、`job`、`task`、`cluster` 等）只暴露构造函数、实现 `core` 接口的类型和 `RegisterRoutes`。**模块之间只通过 `internal/core` 里的接口依赖**，不 import 别人的包（`core`、`store`、`httpapi`、`config`、`testutil` 除外），组装在 `internal/app`（CONTRACTS §1）。`core` 自己不 import `store`（gormguard `[core-store]`）。
- 改 `CONTRACTS.md`、`sdk/proto`、`sdk/manifest`、`server/internal/core`、核心迁移，属于改契约，要在交付说明里写清楚并同步文档。

### 数据库：PostgreSQL 16 + GORM

- 驱动是 pgx v5 的 `stdlib`，连接池是 `database/sql`，GORM 跑在上面。连接由 `runtime-contract/gormpg` 一处打开，核心、网关、插件 SDK 共用；连接池大小只由环境变量（核心 `SUB2API_PG_MAX_CONNS`、网关 `SUB2API_GATEWAY_POOL_MAX_CONNS`）或默认值决定，DSN 里不写 `pool_*` 参数。PostgreSQL 错误的 SQLSTATE、约束名也只经 `gormpg`（核心经 `store` 的同名函数）读取。
- **表结构只以 SQL 迁移为准**：核心迁移在 `server/internal/migrations/NNNN_*.sql`（目前到 0123），已应用的文件不许再改。核心的 GORM 模型由迁移生成（`store/modelgen`，加或改迁移后在 `next/server` 下跑 `go run ./cmd/gen-models`），生成的 `internal/store/model` 不手改。运行时不跑 AutoMigrate。
- **只用 GORM 的模型和查询构造器写语句**（`Create`、`Updates`、`Where`、`clause.OnConflict`、`clause.Returning`、`store.ForUpdate()` 等；子查询和 `UNION ALL` 用 `(?)` 组合）。整条手写的 SQL 只允许出现在允许列表登记过的地方：账本的 4 条 data-modifying CTE（`billing` 的 precharge、apply、release、settle）、`store` 里非数据语句的小助手（事务级 advisory 锁、`SetLocal`、`ClockTimestamp` 等）、`DDL`、迁移执行器、`modelgen`，以及 `gormpg` 的薄组合器（`Exists`、`InsertSelect`、`NextIDs`、`RelationExists`、`TryXactLock`）。
- 交给 GORM 的 SQL 文本必须由常量构成，**值只走 `?` 参数**；运行时选的列名经固定白名单变成 `clause.Column`/`clause.OrderByColumn`。
- 这些规则由源码检查强制执行，检查器是 `runtime-contract/gormpg/sqlcheck`，各模块只保留自己的允许列表：核心是 `server/internal/store/gormguard`（`[raw-sql]`、`[sql-const]`、`[tx-lock]`、`[gorm-tx]`、`[pgx-import]`、`[casbin-api]` 等，说明见 `gormguard/doc.go`，允许列表在 `rawsql_test.go`），网关是 `gateway/internal/sqlguard`，SDK 与插件是 `sdk/pgdb` 的 `TestNoRawSQL`、`TestNoPgxImports`。允许列表里没有命中的条目也算失败。
- 事务只在 `store.DB.Tx` 里开；加锁的函数必须在事务里调用（`store.RequireTx`）；不要用 `Debug()` 或覆盖会话配置的 `Session`。
- 除 `runtime-contract/gormpg` 外没有代码 import pgx（核心 gormguard `[pgx-import]`，其他模块 `sdk/pgdb` 的 `TestNoPgxImports`）。插件通过 `Host.DB(ctx)` 拿 `*gorm.DB`，每次调用重新取，不要缓存（CONTRACTS §2“插件数据库”）。

### 权限：Casbin（CONTRACTS §99）

- 授予和角色指派**只存在 `casbin_rule` 表**（gorm-adapter 的列布局），唯一读写者是 `internal/authz/casbinrule`，写入在业务事务里用 GORM 执行，不经 Casbin 的管理 API 或 adapter 的写入。
- 每个节点一个 `SyncedCachedEnforcer`（`internal/authz/casbinx`），用 Casbin 自带的角色管理器和决策缓存；装入时只读授予，用户的角色在第一次判断时按需读入并有上限。每个权限判断都是 `Enforce`；判断不了时返回 503，不当成拒绝（`core.Can` 返回 `(bool, error)`，调用方把错误原样返回）。
- `authz_meta` 有两个版本：`version` 每次授权写入都升，`policy_version` 只随策略内容升。写入提交后先交给本节点的 enforcer，再广播 `authz:changed`，其他节点按版本原地重载或换 enforcer。绕过服务直接改库（包括测试夹具）要自己升版本（`testutil.BumpAuthz`）。
- 防提权、DataScope、访问密钥收窄仍是自研代码，但“用户是否持有某个 key”一律问 Casbin。
- 插件的用户权限由插件在 manifest 里声明（`userPermissions`，带 `risk`、`defaults`、`scope`），安装时按“权限 × 角色”矩阵分配。前端权限常量 `web/src/generated/permissions.ts` 由核心目录生成（`UPDATE_GOLDEN=1 go test ./internal/authz -run TestGeneratedPermissionsTS`）。

### 控制台会话（CONTRACTS §100）

- 登录只签发不透明的 `s2s_` 令牌，存在 Valkey（键前缀 `s2a:`，只存哈希，`internal/iam/tokenstore`），服务端滑动续期，没有 refresh token。**没有 JWT**，不要再引入。
- Valkey 故障时会话鉴权和登录返回 503，不做本地兜底。访问密钥 `sak_` 和网关 API Key `sk-` 不读会话。

### Valkey、任务、事件

- Valkey（或任何 Redis 7+ 协议服务，go-redis v9）保存实时状态：会话、节点心跳、并发槽位、冷却、余额缓存、分布式锁（redsync）、集群广播频道（`authz:changed`、`plugin:events`、`account:changed`、`config:changed`）。PostgreSQL 是期望状态和账本的唯一依据。
- 定时任务统一走 `core.Tasks`（CONTRACTS §77）：`node` 任务用 gocron，`cluster` 任务用 River（`database/sql` 驱动，只轮询，最多晚 1 秒取走）。业务代码不直接 import gocron 或 River。
- 事件用 outbox 表 `events`，按插件投递（CONTRACTS §6、ARCHITECTURE §8）。

### 插件系统（ARCHITECTURE §5，CONTRACTS §26、§36）

- 插件是独立进程，经 hashicorp/go-plugin + gRPC（本机 unix socket）与核心通信；只有 `plugin/grpcruntime` 直接用 go-plugin。Linux 上有 Landlock（默认开启：插件不申请 `fs.data` 就读写不了文件，CONTRACTS §43.8）、seccomp、rlimit、内存看门狗（只在管理员给插件设了内存上限或节点封顶时生效，默认不限，CONTRACTS §111.5）和出口隧道（插件的对外连接经核心转发）。
- 插件包：`manifest.json`、`migrations/*.sql`、`i18n/`、`assets/`、`ui/`（`ui/iframe/` 沙箱页面或 `ui/native/` 原生微前端）。插件界面只用代码写：没有声明式的 schema 表单与表格页，也没有 `forms/`；账号凭证与插件设置只由插件的 `ValidateCredentials`、`ValidateSettings` 把关，设置的密钥在清单 `ui.settings.secrets` 里声明（CONTRACTS §23、§75.1）。包用 Ed25519 签名，市场索引是签名的静态 JSON。
- 插件的表在核心的模式里，表、索引、序列一律以 `plg_<key>__` 开头（`manifest.TablePrefix`）；每个插件有自己的数据库身份 `plg<库OID>_<key>`，表归它所有，迁移和运行时都以它登录，核心的表和别的插件的表它碰不到，要用插件以外的表在清单 `database.access` 里申请（宿主权限 `db.core_read`，CONTRACTS §130）。插件迁移由核心执行一次，脚本必须幂等、对旧版本向后兼容；各节点独立切换版本（CONTRACTS §36）。插件的 GORM 模型放在 `internal/model`，`sub2api-plugin db check` 对照迁移检查，`pack` 会自动执行。
- 一次请求上的超时和大小上限只有管理员配置的（请求体上限、上游超时、钩子和热路径超时等），加上安全、钱算对和防止崩溃的检查；不要在请求路径上加写死的超时、大小或次数上限（所有者决定，完整列表见 CONTRACTS §112.16）。
- 平台插件（声明账号类型的插件）都是有进程的代码插件，没有只有清单的插件。每个网关请求只有一条路径（CONTRACTS §112）：核心做完协议转换和运营方的改写，把完整请求体交给账号类型插件的 `PlatformService.Execute`；插件构造最终请求，经 `HostService.RelayUpstream` 交给核心的 HTTP 客户端发出（插件不开套接字，账号代理、SSRF、超时、运营方的请求头设置由核心强制；插件随 `Account` 收到账号的凭证和代理详情，但代理只是数据，不得用于自行联网，CONTRACTS §124）；用量与任务事实只在 `Execute` / `Monitor` 的返回值里。插件写法、`sdk/pluginsdk/standard` 库、`jsonedit`、一致性套件见 `next/docs/PLUGIN-DEVELOPMENT.md`。
- 平台插件必须遵守全部系统设置：每项设置在 HTTP 与 WebSocket 两种传输上由谁生效、插件要配合什么，见 `next/docs/PLUGIN-DEVELOPMENT.md` 第 5 节，改动相关行为时同步它。
- 内置插件随核心升级（CONTRACTS §37）。**插件内容变了必须升版本号**：`tools/sub2api-plugin` 的 `TestReleasedPluginsBumped` 对照 `next/plugins/released.json`，内容变了而版本没升就失败；每次发布后按该测试的说明重写这个文件。
- Host API 版本在 `runtime-contract.HostAPIVersion`（现为 10），`sdk/protocol.HostAPIVersion` 就定义为它。插件线协议是 `sdk/protocol.ProtocolVersion`（现为 2）：改了不兼容的插件契约就升它，旧插件包在握手和清单检查时以同一条信息被拒绝（CONTRACTS §112.10）。

### 网关与托管升级（CONTRACTS §33、§34、§39、§82、§102）

- 每个节点由 `sub2api-gateway` 监听公网入口，核心只监听回环地址；外壳的 supervisor 启停本机核心。节点之间用 HTTPS 和 Redis 里每节点一份的密钥鉴权。
- 核心以发布包分发（清单不签名，按摘要校验，CONTRACTS §102.10），升级计划持久化在 PG 的 `updater` schema。改库的版本走 `primary-first-v1`：从节点先转发到主节点并停止，主节点最后停机、迁移、启动，再逐个恢复从节点。不改库的版本可走 `rolling-v1`。
- 网关二进制的变更要单独更新网关，单发核心安装包不会生效。每个节点都运行能自更新的网关（0.1.135 起）后，控制台可以一键更新网关（`gateway-rolling-v1`，CONTRACTS §102）；第一次换上这样的网关仍要手动换镜像。注意 `next/gateway`（外壳）与 `server/internal/gateway`（核心的业务网关流水线）是两回事。

### 前端（`next/web`）

- 控制台构建产物输出到 `server/web/dist`，由核心用 `embed` 嵌入。`server/web/dist/index.html` 是被跟踪的占位文件，本地 `npm run build` 会覆盖它，**提交前 `git checkout` 还原**。
- 插件的原生界面在各插件的 `ui/native/`，依赖 `@sub2api/ui`、`@sub2api/host`。改这三个包的公开接口要同步升级它们的版本（目前 4.0.0，`HOST_UI_VERSION`；带原生界面的插件声明 `hostUICompat: "^4.0"`）和 CONTRACTS §23、`web/packages/ui/README.md`。表单用 `@sub2api/ui` 的 `useForm`/`SForm`、`createApiKeyForm`、`usePluginSettings` 等写成代码，测试辅助在 `@sub2api/ui/testing`（只能在 spec 里引用）。
- 插件 UI 与控制台的写法约定见 CONTRACTS §23.4：用 `@sub2api/ui` 组件，不用控制台全局类和 Tailwind 工具类，颜色只用契约里的 8 个 CSS 变量，操作按钮写文字，私有样式以插件 key 为前缀，接口走 `host.api` / `host.pluginApi`（原始字节类请求用 `fetchWithSession`）。

## 开发规则

1. 代码要抽象、复用、解耦、低嵌套、简洁、低侵入：先找已有的抽象和辅助函数再写新的；改已有文件尽量小，功能没启用的路径行为不变。
2. **迁移完成后不留兼容或过渡代码**：不保留双写、影子比较、开关、旧字段兼容层。旧路径在同一次改动里删干净。确需跨版本保留的（例如网关仍要启动旧核心）必须写明原因和删除时机。
3. 改动要同步文档，和代码在同一个提交或同一组提交里完成：
   - 接口、表结构、行为约定 → `next/docs/CONTRACTS.md` 对应章节（新约定加新的一节）；
   - 架构变化 → `next/docs/ARCHITECTURE.md`；
   - 用户能看到的变化、版本、迁移 → `CHANGELOG.md` 的“未发布”一节；
   - 发布、回退步骤 → `next/deploy/rollback/`（单向发布完成、不能再回退后删除）和 `next/deploy/gateway/*/README.md` 的手册。
4. 插件改了内容就升版本（见上文 `released.json`）；manifest 用到新核心能力时同时提高 `hostCompat` 下限。
5. 新增 Go 模块要加进 `next/go.work`，并在 `go.mod` 里按相对路径 `replace` `next/sdk` 和 `next/runtime-contract`（CONTRACTS §2）。整理依赖用 `GOWORK=off go mod tidy`，然后 `cd next && go work sync`，提交 `go.sum`/`go.work.sum` 的变化（CI 检查它们没有未提交的改动）。
6. Linux 专有代码用 `//go:build linux`，并提供非 Linux 的空实现。
7. 测试不假设某个端口空闲：要证明“连不上”就 `net.Listen(":0")` 拿端口再立刻关掉。
8. 本文件不写主机名、IP、密钥和个人信息；这些只放在部署手册或本机环境里。

## 构建与测试

以下命令在 `next/` 下执行（单个模块进入该模块目录）。CI（`.github/workflows/next-ci.yml`）对每个模块跑的门禁就是 `.github/next-ci/next-check-module.sh`，本地也可以直接用：`sh .github/next-ci/next-check-module.sh next/server`（在仓库根执行）。

### 每个 Go 模块

```sh
gofmt -l .          # 有输出即失败
go vet ./...
go build ./...      # 核心另外确认 GOOS=linux go build ./...
go test -count=1 -timeout 30m ./...
```

模块在工作区模式下检查（不设 `GOWORK`）。需要单独验证 `go.mod` 的 `replace` 时用 `GOWORK=off go build ./...`。

### 核心（`next/server`）

```sh
cd next/server
go test -race -count=1 -timeout 30m ./...
# 只跑一个包：go test -race -count=1 ./internal/authz/...
```

- 数据库测试用 `testutil.DB(t)`（插件用 `pluginsdktest.NewSchema`），库来自 `sdk/testpg`：设了 `TEST_DATABASE_URL`（超级用户 DSN，CI 的 `server` 任务就是这样）就用它；没设时由 Go 在本机启动 PostgreSQL 16（首次下载约 25 MB，之后常驻共用；CI 的 `plugins`、`sdk-tools` 任务就是这样，取不到时测试失败，不会跳过）。停掉用 `go run ./sdk/testpg/cmd/testpg stop`（在 `next/` 下）。有代理变量时加 `NO_PROXY=127.0.0.1,localhost`。`SUB2API_TESTPG=off` 会跳过数据库测试，正常不要用。
- Redis 单测用 miniredis；集成测试可设 `TEST_REDIS_URL`。
- CI 另跑：`go test -race -count=1 -timeout=10m ./gateway/... ./runtime-contract/... ./server/internal/app/... ./server/internal/background/...`（在 `next/` 下）。
- 热路径基准与基线：`server/internal/store/gormtest/BASELINE.md`（命令、机器、各批次的 allocs/op 对照）。改热路径的语句时按那里的命令前后各跑一次，比较 allocs/op。`casbinx` 的基准在 `internal/authz/casbinx/bench_test.go`。
- 本机起一个完整核心：`go run ./cmd/sub2api dev`（内嵌 PG 和 miniredis，CONTRACTS §47）。

### 网关、SDK、runtime-contract、工具、插件

```sh
cd next/gateway && go test -race -count=1 ./...          # 控制器测试需要 PG，同样走 testpg
cd next/runtime-contract && go test -race -count=1 ./...
cd next/sdk && go test -count=1 ./...                    # 含 pgdb 的 TestNoRawSQL、TestNoPgxImports
cd next/tools/sub2api-plugin && go test -count=1 ./...   # 含 TestReleasedPluginsBumped、TestDBCheck
cd next/plugins/<name> && go test -count=1 ./...
```

- 平台插件的测试里调一致性套件 `sdk/pluginsdk/pluginsdktest/conformance`（`conformance.Run`）；它在运行期间替换进程的默认传输和解析器，不要和用网络的测试并行。用 `standard` 库写的插件另跑黄金样例（`sdk/pluginsdk/standard/goldentest`）。

- 插件打包：`sub2api-plugin pack` 默认先跑 `db check`（需要 PostgreSQL，规则同上）。
- 重新生成 proto：`cd next/sdk && buf generate`（buf 1.47.2、protoc-gen-go v1.31.0、protoc-gen-go-grpc v1.2.0）。
- e2e 不在 CI 里运行，需要专用的 e2e 栈，见 `next/deploy/e2e/README.md`（建 `.env`、构建镜像、`up -d --wait` 之后在仓库根执行 `sh next/deploy/e2e/test.sh`；只跑几个用例时加 `-run '^TestAC(30|36)'`）。整套约 27 分钟，脚本默认 `-timeout=150m`（直接 `go test` 时自己带上）。AC30–AC36 是转发 V2 的用例（十一个平台插件经 `Execute`、运营方设置、换号与未上报、旧契约被拒、Responses WebSocket、请求体上限、官方 OpenAI / Anthropic Go SDK 作客户端），AC37 是插件联网，AC38 是 codex 授权经账号表单的代理（代理票据），AC39 是账号余额，AC40 是 OpenAI 生图，AC41 是 Responses 生图工具的 token 计入请求模型，AC42 是 Codex 账号经核心转换服务 OpenAI 生图，AC43 是插件账号上下文（`GetAccount` 的审计、账号请求经账号代理、带旧账号授权的包被拒）。mock 按客户端请求的主机名（SNI）用自建 CA 现签证书，厂商主机名只写在 e2e `compose.yml` 的网络别名里。

### 控制台（`next/web`，Node 24，npm）

```sh
cd next/web
npm ci
npm run typecheck   # vue-tsc -b
npm test            # vitest run
npm run lint        # eslint .
npm run build       # 产物写到 server/web/dist，提交前还原 index.html
```

CI 只跑 `npm ci`、`typecheck`、`test`；`lint` 本地也要跑。

插件的原生界面（`plugins/<name>/ui/native`，每个是独立的 npm 工程，脚本统一为 `typecheck`、`test`、`build`，三个都用控制台 `next/web/node_modules` 里的 vite、vue-tsc、vitest，配置继承 `web/packages/vite-preset` 的 `tsconfig.plugin.json`、`vite.plugin.config.js`；本仓库的工程没有自己的依赖和 lockfile）：先在 `next/web` 下 `npm ci`，再在仓库根执行 `sh .github/next-ci/next-check-plugin-ui.sh`（不带参数检查全部工程，也可以只传一个目录），它对每个工程跑 `typecheck`、`test`、`build`（工程有自己的 `package-lock.json` 时先 `npm ci`），任何一个工程的任何一步失败都以非零退出，CI 的 `plugin UIs` 任务就是它。镜像构建和发布打包要求每个声明了 native 界面的插件都有 `ui/native/dist/entry.js`，缺了直接失败，先跑 `deploy/docker/build-ui.sh`。

## Git 流程

- 开发分支是 `develop`。新工作从 `develop` 开分支，在 `git worktree` 里做，完成后合回 `develop`；不要在主工作区里切来切去，也不要直接在 `main` 上开发。
- 提交时写明路径（`git add next/server/internal/authz/...`），不要用 `git add -A`。工作区里有别人的改动时不要一起提交；生成物、构建产物、`server/web/dist` 的改动不要提交。
- 提交说明用英文，格式是 `<范围>: <小写开头的短句>`，范围写模块或包名，多个用逗号分开，例如 `authz: lazy enforcer users, in-place reloads`、`store, billing: ...`、`web(session): ...`、`docs: ...`；发布用 `release 0.1.290: ...`。说清楚改了什么、为什么，不写空泛的 `fix bug`。
- 推送前先问用户。
- 上游 `Sub2API-Devs/sup2api` 只在用户明确要求时同步，同步一律走 PR，不直接推送。
- 发布（核心、网关、插件三条版本线）按 `next/deploy/` 下的手册做：`deploy/gateway/README.md`（发布包与托管升级）、`deploy/gateway/*/README.md`（各环境的部署记录）、`deploy/rollback/<版本>-*.md`（单向迁移的发布与回退）。发布后更新 `CHANGELOG.md` 和 `next/plugins/released.json`。

## 读代码从哪里开始

- 契约与构建规则：`next/docs/CONTRACTS.md` §1–§3；最近的大改动：§112（平台插件转发 V2）、§113（插件联网）、§114（插件包不依赖主节点）、§130（插件数据库隔离，取代 §115）、§131（计费方式只留可视化与表达式）、§116（删除 Claude OAuth）、§117（代理票据）、§118（OpenAI 生图）、§119（上游插件 newapi、sub2api、typesafe）、§124（插件按归属收到账号的全部信息）、§125（删除 growth）、§127（密钥与基础地址）、§128（删除 volcengine）、§129（删除 anthropic 的模型目录）、§23 与 §75（插件界面只用代码写、界面包 4.0）、§52（账号余额）、§111（请求体上限、插件内存）、§110（协议绑定）、§99（Casbin）、§100（会话）、§2 的“核心的 SQL”“插件数据库”。
- 架构与数据模型：`next/docs/ARCHITECTURE.md` §2–§5、§13、§16。
- 最近发生了什么：`CHANGELOG.md` 的“未发布”与最新几节。
- 组装入口：`next/server/cmd/sub2api/main.go`、`next/server/internal/app`。
- 插件开发：`next/docs/PLUGIN-DEVELOPMENT.md`（平台插件）、`next/sdk/pluginsdk/doc.go`、`next/sdk/pluginsdk/standard/README.md`、`next/plugins/guard`（演示插件）、CONTRACTS §26、§47.2、§112。
- 多节点与升级：`next/docs/MULTINODE-SYNC-PROTOCOL.md`、`next/docs/GATEWAY-MANAGED-UPDATES.md`、`next/deploy/gateway/README.md`。
- 交接与审计记录（`HANDOVER*.md`、`HANDOFF-*.md`、`audits/`）是当时的快照，不能代替当前状态。
