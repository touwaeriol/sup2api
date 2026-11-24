# sub2api-next 开发进度与交接记录

> **2026-11-11 提示词审核 0.8.2 与核心 0.1.275：0.8.1 上线后的整理**（分支 `chore/moderation-081-cleanup`，已合入 develop `b33650368`；**2026-11-11 北京时间 23:16–23:25 上线**，部署记录见 `next/deploy/gateway/ovh/README.md`）：
> - **moderation 0.8.2**：不改 0.8.1 的缓存规则。三处小的行为变化：分几次调用的片段键写中途失败时，已写成的键出本节点内存、得到指针的照常广播；告警状态的 KV 失败按缓存的规则计数；片段键在 KV 里的 TTL 向上取整到整秒。界面：概览页显示 `runtime.kv_skipped`，`kv_writing` 一行换了文案，删掉六个没用到的文案键。内部：`cache.go` 用词统一、写的逻辑共用，测试拆成五个文件、运行时间减半。`hostCompat` 不变（`>=0.1.273 <0.2.0`），没有新权限、迁移和设置变化，KV 里的键和值不变，可以与 0.8.1 混跑；回滚是装回 0.8.1。
> - **核心 0.1.275 与 SDK**：插件 KV 的规则只写在 `sdk/protocol/kv.go` 一处（核心、SDK、测试替身的批量调用共用，同样的校验顺序和错误信息）；单键 `KVSet` 的 TTL 有了上界；`KVGetMany` 在合计超限的那个值处就拒绝。SDK：负的 TTL 一律拒绝；`batch.New` 接受 `WithInterval`、`WithMaxBatch`。不改协议，Host API 仍是 9。
> - **文档**：CONTRACTS §72（72.3、72.5 第 8 步、72.16 的 0.8.2 条目）和 §93（各处标了 0.1.275）；设计文档第 14 节末尾；发布说明第 8 节。三份文档里的标识符和测试名用脚本对过代码。

> **2026-11-11 提示词审核 0.8.1 与核心 0.1.274：判定缓存改用批量 KV 调用**（`e496cea25`；**2026-11-11 北京时间 21:20–21:28 随核心 0.1.274 上线**，发布提交 `ba75109f3`，记录在 `deploy/gateway/ovh/README.md`）：
> - **原因**：0.8.0 上线当天在生产上量到，承载流量的节点上插件的每次 KV 调用约 50 ms（授权复核约 27 ms 加 Redis 约 23 ms），0.8.0 的判定缓存逐键读写、带预算，一个约 150 个片段的请求在 200 ms 内只读得到约 32 个，5 分钟一次的重读永远读不完。
> - **核心与 SDK**：`HostService.KVGetMany` / `KVSetMany` / `KVDeleteMany`，一次调用处理多个键，授权只复核一次、Redis 只往返一次（CONTRACTS §93；Host API 仍是 9，无迁移，没有新的宿主权限）。它们进了核心 0.1.273 的构建；0.1.273 构建、签名之后没有导入也没有发布，第一个发布的带它们的核心是 0.1.274。
> - **moderation 0.8.1**：内存层只是 KV 的缓存，不再定期重读；缓存用批量调用读写，没有预算和个数上限；一次运行的结论在钩子回答之后先写 KV、写成功后才进内存；`/status` 新增 `runtime.kv_writing`、`runtime.kv_skipped`，概览页显示前者。没有迁移，没有设置变化，可以与 0.8.0 混跑。0.8.0 与 0.8.1 的对照表在 CONTRACTS §72 开头。
> - **上线**：`hostCompat` 抬到 `>=0.1.273`，必须先升核心；核心 0.1.274 滚动升级完成后，内置同步把插件从 0.8.0 升到 0.8.1，两个节点都在 0.8.1 上。**不需要管理员重新批准**。回滚是装回 0.8.0；核心也要退回 0.1.272 时先退插件。
> - **已知的代价**（发布说明第 7 节）：结论写进缓存之前的重复请求再审一次，命中计为新的违规；广播丢失的节点不再在 5 分钟后自己跟上；KV 既不报错也不回答时先审的请求会等、后台写会积压。
> - **文档**：契约 CONTRACTS §72（0.8.1 对照表、72.5 第 8 步、72.9）和 §93；设计 [DESIGN-2026-11-10-MODERATION-PARITY.md](DESIGN-2026-11-10-MODERATION-PARITY.md) 5.3 和第 14 节"上线后修改"；发布与回滚 [RELEASE-MODERATION-0.8.0.md](RELEASE-MODERATION-0.8.0.md) 第 7 节。

> **2026-11-10 提示词审核 0.8.0：与 new-api 内容审核对齐**（分支 `feat/moderation-newapi-parity`，从 `c31b3c4cb` 起；已合并到 `develop`，**2026-11-11 上线**：只升级插件，核心仍是 0.1.272，记录在 `deploy/gateway/ovh/README.md`）：
> - **内容**：跟进 new-api 2026-11-01 至 11-08 的改动（参照提交 `207797b76`，cp.294）。流程引擎（失败级跳过、前面的命中保留；补审级；初审命中传给后级；结果映射）；新审核器类型 `llm_questions`（题表式 LLM）；判定缓存（放行和命中都缓存、命中重放、`POST /verdict-cache/clear`、人工处理同步缓存）；审核时机（`mode` 兼作默认级别、白名单、黑名单；`exempt_user_ids` 退役）；Jev 长上下文和图片重画；`record_pass_input_message`；绑定回落。
> - **提交**：设计 `26b90a1fa`；T1 `6e326492e`、T2 `52c25d6cd`、T3 `007c00fb3`、T5 `bceec0ac0`、T4 `aa7341999`；代码审核后的修改 `cb5a1c2c5`（界面）、`25a383f17`（后端）、`6c032357a`（判定缓存）；T6（本条）文档、版本号 0.8.0、发布说明。
> - **不改核心**，没有新的宿主权限，scope 不变：**升级不需要管理员重新批准**。一个新迁移 `0010_newapi_parity.sql`（幂等，回滚后无害）。
> - **现有配置下就会变的行为**（上线前必须告知）：复核出错或超时不再放行已命中的请求；`mode=enforce` 从"所有人先审"变为"默认级别为拦截"；绑定到停用或未发布流程的分组和账号回落到默认流程；繁忙时得到 503 的用户范围变大；升级后第一天缓存重建、Jev 送审更多图片，审核调用和费用上升；流程异常率的含义变了。
> - **有意保留的限制**：后审的请求被缓存命中重放时，命中集合之外的新内容这一次不审（与 new-api 相同）；存过新设置键之后回滚到 0.7.2，要先通过核心设置接口去掉四个新键。
> - **写这一条时还没做的**：在 0.7.2 数据副本上的升级演练、构建机拉取 `golang.org/x/image` 的确认、界面的浏览器走查（开发环境没有浏览器）、发布。发布已在 2026-11-11 完成；其余几项做没做，以发布记录为准，这里没有核对。
> - **文档**：设计与审核后的修改 [DESIGN-2026-11-10-MODERATION-PARITY.md](DESIGN-2026-11-10-MODERATION-PARITY.md)；契约 CONTRACTS §72；发布与回滚 [RELEASE-MODERATION-0.8.0.md](RELEASE-MODERATION-0.8.0.md)；与 new-api 的差异清单 [HANDOFF-2026-10-31-MODERATION-GAPS.md](HANDOFF-2026-10-31-MODERATION-GAPS.md) 第 6 节。

> **2026-10-05 取消二次验证、账号测试记录、CC OAuth 在账号页授权、预设映射留空**（未提交时的工作区状态，由主控统一提交）：
> - **取消二次验证**（用户决定）：删除 `POST /auth/step-up`、`X-Step-Up-Token`、`step_up_required`、`Authorizer.IsSensitive`、`Router.PermStepUp`；调整余额、删除、查看凭证、角色授权、插件安装卸载、系统更新只看登录态 + RBAC。`sensitive` 只剩展示标记。CONTRACTS §3.3。下文历史记录里的 step-up 描述均已失效。
> - **账号测试**：迁移 0031 给 `accounts` 加 `last_test_*` 可空列；每次 `POST /accounts/:id/test` 后记录，账号 JSON 新增 `last_test`；TestResult 新增 `requested_model`。测试仍不改变账号状态。控制台模型测试弹窗（测试单个/全部/所选模型、批量测试账号）由前端并发调用单模型测试实现。CONTRACTS §15.9、§50。
> - **CC OAuth（CCGateway 每账号容器）**：授权在"账号管理 → 新建/编辑 CCGateway 账号"里完成：保存即 Kick 立刻准备容器 → 取授权链接 → 登录 Claude → 粘贴 code#state；插件页只显示容器状态并跳到账号页。账号必须绑代理（否则 `blocked`）。核心在 Redis 保存未完成的授权会话（刷新可恢复，`GET .../session`），`start` 在容器刚就绪时短暂重试；每账号接口接受 `settings:*` 或该账号的 `account:read`/`update`（含 own 级）；`/system/ccgateway/connect` 在每账号容器模式下跳过共享状态检查直接建账号。（2026-11-12 权限加固：connect 已删除，按账号的运行环境接口不再接受 `settings:*`，见 CONTRACTS §49.5、§49.6。）CONTRACTS §49。
> - **插件预设模型映射一般留空**（用户决定）：anthropic 0.2.5、claude-oauth 0.2.1、ccgateway 0.1.6、gemini 0.2.4、openai 0.3.4 删除 `defaultModelMapping` 和只靠映射才能用的退役模型；volcengine 保留。CONTRACTS §41.4。

> **2026-10-02 主节点优先升级完成验收**：先停所有从核心、主节点停机迁移并恢复、再逐个恢复从节点；节点间用 Redis 中每节点一份的可复用密钥鉴权。实现已在隔离环境通过全部 Go 模块 `-race`、真实 PG/Redis 和真实三节点（真实新增迁移、签名插件、Redis 登记丢失恢复、失败候选与基线恢复）验收，见 [验证记录](audits/2026-10-02/MULTINODE-VALIDATION.md)。规约见 [MULTINODE-SYNC-PROTOCOL.md](MULTINODE-SYNC-PROTOCOL.md)。已提交 `7ad11483c` 并部署到 `single/` 业务栈；外壳托管部署未上线。

> **2026-10-01 补充**：本文件下面保留 09-25 的历史进度。当前多节点整改、统一 Execute / Monitor 调度及实际验收结果以 [整改记录](audits/2026-10-01/REMEDIATION.md) 为准；已新增 [隔离双节点 E2E 环境](../deploy/e2e/README.md)，不再依赖下文已经删除的旧测试拓扑。

> ⚠️ **本文件停在 2026-09-25（第四轮）。之后的「插件执行、核心记录」五期改造与火山方舟插件不在这里。**
> **接手工作请先读 [HANDOVER.md](HANDOVER.md)**（环境、纪律、待办、坑），再读 [ROUND-2026-09-PLUGIN-MECHANISM.md](ROUND-2026-09-PLUGIN-MECHANISM.md)（那一轮的前因后果）。

> 用途：记录派发给开发 agent 的任务、交付结果、待办事项与环境信息，保证上下文压缩或换人接手后能完整恢复现场。
> **每次合并分支、派发新任务、做出决策后都要更新本文件。**
> 最后更新：2026-09-25，第四轮全部合并；sup2api 清库重建后第四轮验证全部通过（部署 `549710fa6`）；数据库单测已在临时测试容器补跑并通过（见 §11）。

相关文档：[HANDOVER.md](HANDOVER.md)（交接）· [ROUND-2026-09-PLUGIN-MECHANISM.md](ROUND-2026-09-PLUGIN-MECHANISM.md)（最近一轮的前因后果）· [ARCHITECTURE.md](ARCHITECTURE.md)（设计）· [CONTRACTS.md](CONTRACTS.md)（开发契约）

---

## 1. 总体状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 | 备份、架构文档、契约（proto、manifest、核心表、core 接口、基础设施骨架） | ✅ 完成 |
| 1 | 9 个 agent 并行开发各模块 | ✅ 全部合并 |
| 2 | 网关（G）、事件投递与任务（H） | ✅ 全部合并 |
| 3 | 主控组装 `internal/app`、在 ovh 上用 compose 部署、跑 17 条验收测试 | ✅ 17/17 通过 |

**分支**：开发分支 `feat/next-platform`（本地），每个 agent 在独立 worktree 的 `next/<代号>` 分支上开发，完成后由主控 `git merge --no-ff` 合并。
**备份**：tag `legacy/v0.2.8`、分支 `legacy/main`（均已推送）。
**远程推送**：`feat/next-platform` 已全部推送到 origin。本机访问 GitHub 时好时坏，推送失败就稍后重试。

---

## 2. Agent 任务与交付

所有 agent 的任务说明都要求：先 `git merge --ff-only feat/next-platform` 再建分支；只改自己的目录；遵守 CONTRACTS；测试组件只用 docker compose；不 push；最终报告写清构造函数与契约变更需求。

| 代号（SendMessage 名称） | 负责目录 | 分支 | 状态 | 提交 | 合并提交 |
|---|---|---|---|---|---|
| a1-identity | `server/internal/iam`、`authz` | next/a1-identity | ✅ 已合并 | 3819efc0b、2ebef13d4 | 2591ae00f |
| a2-resources | `apikey`、`group`、`proxy`、`account` | next/a2-resources | ✅ 已合并 | 02312e823、e2dfca146、1b1ec5cf5、db777e933 | b1f39bfae |
| b-billing | `billing`（含 `expr`）、`usage`、`event` | next/b-billing | ✅ 已合并 | 6221b7db8 | 3041351c8 |
| c1-lifecycle | `plugin/pkg`、`install`、`market`、`api` | next/c1-lifecycle | ✅ 已合并 | 172997d83 | 60937afd0 |
| c2-runtime | `plugin/registry`、`grpcruntime`、`rollout`、`dbschema`、`routes` | next/c2-runtime | ✅ 已合并 | 21067e642 | 53ac81a60 |
| d-sandbox | `cluster`、`plugin/sandbox`、`plugin/egress`、`sdk/pluginsdk/egress` | next/d-sandbox | ✅ 已合并 | 009b0ed9f | f85721f9f |
| e-sdk-plugins | `sdk/pluginsdk`、`plugins/anthropic`、`plugins/guard`（Go）、`tools/sub2api-plugin` | next/e-sdk-plugins | ✅ 已合并 | 237873003 | 22b4387d6 |
| f-frontend | `web/`、`plugins/guard/ui/native` | next/f-frontend | ✅ 已合并（含 3 个子 agent） | 2c8cfe488、3fdab6209、a4af8d707 | 3bbdc760e |
| qa-deploy | `Dockerfile`、`deploy/`、`e2e/` | next/qa-deploy | ✅ 已合并 | 7d7ea1c15、ce5111dc5 | e55c32a85 |
| g-gateway（阶段 2） | `server/internal/gateway`（含粘性会话） | next/g-gateway | ✅ 已合并 | 6a1e91436 | da90fc0ec |
| h-events-jobs（阶段 2） | `event/delivery`、`job` | next/h-events-jobs | ✅ 已合并 | 04a6c87fb | c8caaca75 |

主控自己的提交：`6de334386` 架构文档 · `5a3f05dc9` 阶段 0 契约 · `d2d44ad1b` 迁移测试 · `6e3d0fadc` 插件协议/签名/加密 · `8586025b6` 发布/schema/默认值接口与格式 · `7e8518afc` compose 测试规则 · `c3a19b057` Ledger.ApplyTx、0002 迁移、gofmt · `b84f458e9` 阶段 2 接口 · `7227ff4d5` 阶段 1 契约变更并入 CONTRACTS · `222d4b194` 前端 SPA 处理（CSP nonce）与 plugin-exec · `e7a64d627` 测试模板库 · `d87ae94d7` 插件迁移改用插件角色登录执行（修复 RESET ROLE 提权） · `8944ebb86` 移除误提交的 mock-upstream 二进制 · `d09008a43` `internal/app` 组装 · `a69f68f55` 挂载网关、插件详情接 JobTrigger/HookStatsSource · `896ea01a5`、`5bc00f3df`、`e6f7e4483` e2e 修复（见第 8 节）· `e96962948` 第二批契约并入。

### 已发给 agent 的补充约定（均已落实）
- **g-gateway**：`cache_creation_tokens` 是总量（含 1 小时），填 `UsageTokens` 时 `CacheCreation = 总量 − cache_creation_1h_tokens`；钩子 `prompt_text` 传纯文本；请求 ID 必须服务端生成（任务说明里已写）；已安装未启用插件的端点返回 503（2026-09-24 用户改为 404：未启用即端点不存在）。
- **c2-runtime**：Windows 开发模式二进制为 `plugin.exe`；`SkipHostEnv=true`；`config_enc` AAD 为 `"plugin-config:"+key`；配置变更广播 `{"type":"config","plugin_key":k}`；Drop（purge）要显式删除迁移记录（0002 去掉了级联）；平台插件处理自己平台账号时必须携带解密凭证；可用 E 的 `build-demo.sh` 产出真实插件包测试。

---

## 3. 组装（`server/internal/app/app.go`）

所有模块已在 `app.Run` 中组装，启动顺序：PG + 核心迁移 → Redis → cluster（心跳）→ event/registry/packages → billing、usage → authz.Start → iam.Bootstrap → group/apikey/proxy/account → egress（`AlwaysAllow` 含 PG 地址）、launcher、dbschema、grpcruntime → gateway → DefaultsApplier(authz, billing, gateway) → rollout.Start → install、market.EnsureOfficial → job、delivery → HTTP。关闭按相反顺序（先 `srv.Shutdown`）。

HTTP 挂载：`/healthz`（节点自我隔离时 503）→ `httpapi.NewRouter` → 各模块 `RegisterRoutes` → `routes.RegisterAssets(engine)`（`/plugin-ui`）→ `engine.NoRoute(gw.Middleware(), webui.Serve)`：先按插件声明分发网关端点，未命中再走控制台 SPA 回退（`/api/`、`/plugin-ui/` 前缀返回 JSON 404；index.html 每次替换 `__CSP_NONCE__` 并发 CSP 头）。

`main.go`：`plugin-exec` 子命令 → `sandbox.RunExec`；JSON 日志；`app.Run`。

---

## 4. 待写入 CONTRACTS.md 的变更

> A1、A2、B、C1、D、E、QA 的变更已于 `7227ff4d5` 写入 CONTRACTS。以下是 C2、G、H、F 交付后新提出的。
> **第二批已写入 CONTRACTS**（§5.5 补充、§5.7 补充、§7 Redis key 与节点状态格式、§11.6、§11.7）：处理意见为"写入"的各项，以及 F 列出的"已实现但未写入"。
> **仍待办**：`ClientRequestID`（core + 迁移 + B）、`/settings/gateway` 归属、SSRF 拨号钩子、F 列出的"仍缺"接口说明、标为"可选"的项。

| 来源 | 变更 | 处理意见 |
|---|---|---|
| C2 | Redis `plugin:ledger:{key}:{credit\|debit}:{yyyymmdd}`（每日累计，TTL 48h） | 写入 §7 |
| C2 | `plugin:events` 广播 `{"type":"rollout"\|"config","plugin_key","rollout_id"}`；`ReportPlugin` JSON = `rollout.NodePluginState`（serving、standby、rollout_id、rollout 状态、instances） | 写入 §7 |
| C2 | Disable 立即提交；Enable 优先 `active_version` 否则最新已批准版本；plugins 行删除后各节点一次对账内停实例 | 写入 §5.7 |
| C2 | MigrateData 可能因协调者接管重复执行，插件必须幂等 | ✅ 已写入 CONTRACTS §11.7，并收进 §27.4「插件作者的多节点须知」；SDK `Migration` 接口注释已同步（2026-10-01） |
| C2 | 超时：控制台平台调用 10s、热路径 2s、Scheduler 200ms、钩子最多 2s | 写入 §11 |
| C2 | ~~`SET LOCAL ROLE` 迁移可被 `RESET ROLE` 绕过~~ | ✅ 主控已修（`d87ae94d7`，0003 迁移 + 插件角色登录执行） |
| G | `UsageRecord.ClientRequestID` + `usage_logs.client_request_id` 列 | 待做（core + 0005 迁移 + B） |
| G | Redis `hook:stats:{plugin}:{hook}`（HASH）、`hook:statidx:{plugin}`（SET），TTL 7d；`hook:breaker` 值为熔断截止毫秒，TTL 30s | 写入 §7 |
| G | `usage_logs.error_type` 新增 `model_not_allowed`、`price_not_configured`、`rate_limited`、`invalid_request`、`plugin_unavailable` | 写入 §6 / 表注释 |
| G | `PlatformBinding` 缺凭证授权标记，网关总是给平台插件传解密凭证 | 符合"平台插件默认拿自己平台账号凭证"，保持 |
| G | `/settings/gateway` GET/PUT 无归属 | 待定：建议 G 在 gateway 包补 |
| G | `plain` 错误格式即核心 REST 格式；anthropic 格式 `error.code` 可带 `guard_blocked` | 写入 §3 |
| G | `HookBinding` 加 `ID`（现在未写 id 时用 manifest 下标） | 可选；C1 详情已按同规则匹配 |
| G | SSRF：共享代理客户端无法拨号时校验，防不住 DNS rebinding | 待 D 提供拨号钩子 |
| G | `sticky_rules UNIQUE(name, source)` 使两个插件不能声明同名默认规则 | 保持（后来者跳过并记日志） |
| H | ~~`core.Locker` 增加续租（`Extend`）~~ | ✅ 已完成（2026-10-01）：换成 redsync，`Lock.Extend` + `core.KeepLock`，去掉 PG 兜底；七个调用点与第二重保护见 CONTRACTS §27.1、§27.2 |
| C2 / E | ~~插件没有互斥手段（KV 无 SETNX / CAS）~~ | ✅ 已完成（2026-10-01）：宿主权限 `lock`（medium）+ `HostService.LockAcquire/LockRenew/LockRelease`，proto、manifest、SDK（`Host.Locks()`）、宿主侧（`grpcruntime/lock.go`，`core.TokenLocker` 新增 `TryLockToken`、`ReleaseToken`）均已实现；owner token 由插件生成，结果未知时 SDK 用同一 token 补偿释放。见 CONTRACTS §27.3、§27.4 |
| H | core 定义"有新事件"频道 `events:appended`，B 的 Publisher 提交后发布 | 可选（现最多延迟 1s） |
| H | job cron 默认 UTC（支持 `CRON_TZ=`）、`@every` 对齐周期；`OnEvents` 确认 id 不超过游标算失败 | 写入 §11 |
| H | `plugin_job_runs.triggered_by`；`core.JobTrigger.NextRun` | 可选 |
| F | 已实现但未写入：`/ui/plugins` 的 `host_ui_compat`；插件设置 `{schema, ui_schema, values}`；价格保存 `confirm` 与 `details.confirmation_required`；角色 `permission_keys`、`user_count` | 写入 §5 |
| F | 仍缺：`POST /me/api-keys`、`PATCH /api-keys/:id` 字段；列表筛选参数；`/nodes` 响应结构；代理密码 `"******"` 不修改；`/sticky-rules/stats` 对应键与 flush 返回；发布记录 `migrations[]`；核心版本与资源上限查询接口 | 按后端现状补写；核心版本可放 `/healthz` 或 `/me` |
| F | 插件包 `i18n/*.json` 未自动加载（原生插件需 `host.addMessages`） | 后续 |

---

## 5. 已知问题与待办

| # | 问题 | 负责 | 状态 |
|---|---|---|---|
| 1 | 客户端 `X-Request-Id` 不能用作计费幂等键 | G | ✅ 服务端生成 |
| 2 | `main.go` 未嵌入前端 | 主控 | ✅ `webui` 包 |
| 3 | 插件出现新外部域名时告警 | D | ✅ 第四轮：`plugin_egress_domains` + `plugin.egress_new_domain` 事件 |
| 4 | 登录限速、refresh token 重放检测 | A1 | ✅ 第四轮（a4） |
| 5 | 测试建库慢 | 主控 | ✅ 模板库（瓶颈主要在经隧道的查询延迟，约 290ms/次） |
| 6 | 卸载插件时清除其账号 | A2 + C1 | ✅ 第四轮：`purge_accounts=true` |
| 7 | 钩子统计接到 C1 详情页 | 主控 | ✅ |
| 8 | guard 包缺原生界面 | F | ✅ Docker 构建时 build-ui.sh 产出 |
| 9 | `api_keys.group_id` 外键无级联 | — | 保持现状 |
| 10 | 本机 `python` 是应用商店占位程序 | — | 用编辑工具或 perl |
| 11 | C2：旧版本缓存目录不清理、`Packages` 持有文件句柄；`/api/v1/p` 未接节点自我隔离；资源限制改动下次重启生效；节点只校验 sha256 不重复验签 | C2 | ✅ 第四轮（d4、c4）：旧版本缓存清理、插件接口接自我隔离、资源限制改动广播并即时重启、节点加载前重新验签 |
| 12 | SSRF DNS rebinding（见第 4 节 G） | D + G | ✅ 第四轮：直连拨号时校验目标地址 |
| 13 | 市场页无法判断兼容性（没有接口暴露核心版本） | 主控 | ✅ 第四轮：市场返回 `host_version` 与每个版本的 `compatible` |
| 14 | 出口日志只在连接关闭时写入，长连接（keep-alive、数据库）在关闭前不出现在"外部访问"页 | D | ✅ 第四轮：建立连接时写 `open` 行，关闭时更新 |
| 15 | guard 规则改动在其他节点最多延迟 5 秒生效（插件内轮询）；插件没有广播通道 | E / 核心 | ✅ 第四轮：插件集群广播 `app.broadcast.v1`，guard 改规则后广播 `rules.changed`，5 秒轮询保留兜底 |
| 16 | 钩子熔断按节点计数（每节点连续 10 次失败），集群内各节点独立熔断 | G | 符合 §6.3，保持 |
| 17 | OpenAI chat 流式：客户端未带 `stream_options.include_usage=true` 时上游不返回用量，会按零用量计费；将来的 openai 账号类型插件应在 BuildUpstreamRequest 中强制打开（或定为核心行为） | 插件 / 主控 | ✅ 第四轮：openai 插件对流式 chat 强制 `include_usage=true`，网关忽略无用量的块 |
| 18 | Gemini 思考 token：`candidatesTokenCount` 不含 `thoughtsTokenCount`，默认按 token 定价会少计；目前暴露为计量值 `u("thoughts_tokens")`；需要用量映射支持多路径求和或新增输出附加字段 | G | ✅ 第四轮：用量映射支持 `a+b` 求和，gemini `output_tokens` = candidates + thoughts |
| 19 | Gemini 流式：插件拿不到客户端 query，应始终向上游请求 `?alt=sse`，网关按客户端要求（带不带 `alt=sse`）重新组装 | 插件 | ✅ 第四轮：gemini 插件始终请求 `alt=sse`，网关在客户端未带时重组为 JSON 数组 |

---

## 6. 环境

**本机（Windows）**
- Go 1.27（mise，`GOBIN=/d/mise/installs/go/1.27.0/bin`，内有 buf、protoc-gen-go、protoc-gen-go-grpc）；Node 24、npm 11；无 Docker、PG、Redis、WSL
- Go 依赖代理：命令内临时设置 `GOPROXY=https://goproxy.cn,direct`（与仓库 Dockerfile 一致）
- 常驻 SSH 隧道（后台任务）：`ssh -N -o ServerAliveInterval=15 -L 45432:127.0.0.1:45432 -L 36379:127.0.0.1:36379 -L 3120:127.0.0.1:3120 ovh`（测试库、Redis、Caddy 控制台；网络抖动会断，断了重建）
- 浏览器实测控制台：`.claude/launch.json` 里的 `sub2api-next-ovh` 是本机 Node 反向代理（5174 → 3120）
- 测试环境变量：`TEST_DATABASE_URL=postgres://postgres:sub2api@127.0.0.1:45432/postgres?sslmode=disable`、`TEST_REDIS_URL=redis://127.0.0.1:36379/0`
- proto 生成：`cd next/sdk && buf generate`

**测试服务器 ovh**（`ssh ovh`，15.204.107.38，Debian 13，Docker 29 + Compose v5；机器上有大量其他服务，**只许动 `~/sub2api-next-test/` 与 `sub2api-next-*` 项目**，所有组件只用 docker compose）

> **2026-09-24 起 ovh 上只运行 `sup2api` 一个服务（用户要求不要启动多个服务）**。`sub2api-next-test`（3120）与 `sub2api-next-testdb`（45432/36379）已 `docker compose down`（数据卷保留）。需要跑 e2e 或数据库单测时先征得用户同意再临时启动，用完即停。

| compose 项目 | 目录 | 内容 | 端口（仅本机回环） |
|---|---|---|---|
| sub2api-next-testdb | `~/sub2api-next-test/testdb` | postgres:16（密码 sub2api）、redis:7 | 127.0.0.1:45432、127.0.0.1:36379 |
| ~~sub2api-next-test~~ | ~~`~/sub2api-next-test/src/next/deploy`~~ | 2026-09-27 已删除其部署文件（compose.yml、caddy/、scripts/、.env.example）；服务器上早已 down | ~~127.0.0.1:3120~~ |
| sub2api-next-ci | `next/deploy/ci/compose.yml` | `gotest` 容器，接入 testdb 网络，用于 Linux 专有测试 | 无 |
| **sup2api**（测试部署，双节点） | `~/sup2api`（`src` 为 GitHub 稀疏克隆，`.env` 为密钥与管理员账号） | app（sup2api-1）、app-2（sup2api-2，共用 pg/redis）、pg、redis、market（内置签名插件市场，仅内网）；`SUB2API_PLUGIN_VERIFY_SIGNATURES=false` | 0.0.0.0:3130、:3131 |

- sup2api 部署/更新：先 `git push`，再 `ssh ovh 'bash ~/sup2api/src/next/deploy/single/deploy.sh feat/next-platform'`（服务器从 GitHub 拉分支，见 `next/deploy/single/README.md`）
- 访问：http://15.204.107.38:3130 与 :3131（两个节点共用数据库与 Redis）
- e2e（`next/e2e`）仍按 3120 + Caddy + mock-upstream 的布局编写，需要新的目标环境后才能再跑
- 当前 ovh 上运行的是 QA 的骨架版本（只有 `/healthz`），市场索引为空

**插件打包**（在 `next/` 下）：`go run ./tools/sub2api-plugin keygen --key-id sub2api-dev --out <keydir>`，然后 `sh tools/sub2api-plugin/scripts/build-demo.sh <outdir> <keydir> sub2api-dev`，产出 anthropic 0.1.0/0.2.0、guard 0.1.0、`test/guard-0.1.1-test` 与签名索引。

---

## 7. 关键决策记录

| 决策 | 内容 |
|---|---|
| 插件运行方式 | gRPC 独立进程（go-plugin）；宿主只依赖 `Runtime/Instance` 与 core 能力接口，预留 JS 运行方式 |
| 平台 | 生产只支持 Linux（含 Docker）；Windows/macOS 仅插件开发模式 |
| 多节点 | 节点对等；Redis 存节点注册与实时状态；插件发布"先准备、再激活"，发起节点协调、租约过期由其他节点接管，PG 里的 CAS 为提交点 |
| 资源限制 | 不用 cgroup；oom_score_adj + nice + GOMEMLIMIT + 内存看门狗 + rlimit |
| 网络 | 默认不限制只记录；Linux 严格模式用 seccomp 保证插件所有连接走 gRPC 出口隧道；UDP 默认禁止，DNS 由核心代解析 |
| 权限 | 用户/角色/权限 RBAC；插件可注册用户权限（禁用保留、卸载销毁）；插件申请宿主能力需管理员分级确认 |
| 计费 | 按次/按 token/表达式三种方式统一为表达式；参考 new-api 设计但不复制代码（new-api 为 AGPL，本项目 LGPL）；只支持管理员调整余额；表达式没写的缓存类别（`cr`/`cc`/`cc1h`）不收费，`p` 始终是不含缓存的纯输入（2026-09-24 用户决定） |
| 粘性会话 | 核心负责调度，平台插件提供默认规则，管理员可覆盖，复杂取值可由插件扩展点计算 |
| 网关端点 | 由插件在 manifest 中声明，核心按声明执行通用流水线 |
| 内置插件 | anthropic 为官方内置插件：随镜像提供，启动时自动安装并启用，只能禁用、不能卸载（2026-09-24 用户决定） |
| 测试与部署 | 所有测试组件只用 docker compose；ovh 独立目录部署，对外只绑定 127.0.0.1 |

---

## 8. 接下来 / e2e 记录

**e2e 运行方式**（本机，经 `127.0.0.1:3120` 隧道到 ovh Caddy）：
```bash
cd next/e2e
export E2E_ADMIN_EMAIL=$(ssh ovh "grep ^SUB2API_BOOTSTRAP_ADMIN_EMAIL= ~/sub2api-next-test/.env | cut -d= -f2-")
export E2E_ADMIN_PASSWORD=$(ssh ovh "grep ^SUB2API_BOOTSTRAP_ADMIN_PASSWORD= ~/sub2api-next-test/.env | cut -d= -f2-")
export E2E_DOCKER_HOST=ovh E2E_RUN_PENDING=1
go test -count=1 -timeout 50m -v ./...
```

| 轮次 | 部署提交 | 结果 | 根因与修复 |
|---|---|---|---|
| 1 | `a69f68f55` | 19 个用例全失败 | 多数是 `WaitPlugin` 等不到节点 `state=active` 连带超时：C2 上报的节点 JSON 没有契约里的 `state` 字段 → 补派生 `state`；`/healthz` 返回 `"OK"` → 改 `"ok"`；e2e 建角色没带 step-up、review 账号类型键是 `id`；派生 guard 包沿用原二进制导致 GetInfo key 不符 → e2e 用 `-X buildKey` 重编；升级不增权限也要求确认 → C1 自动沿用授权（`896ea01a5`） |
| 2 | `896ea01a5` | 中止 | e2e 把节点 `state` 当字符串读（实际是上报对象，取 `state.state`）；建带角色的用户要 step-up |
| 3 | `896ea01a5` | 7/17 通过（AC01–03、14–16） | 其余都卡在 e2e 以数字发 `rate_multiplier`（契约为十进制字符串） |
| 4 | `896ea01a5` | 13/17 通过 | AC08：预览不按结算方式归一化 → 服务端改为同一 `expr.Normalize`；列表不含 `price_id` → e2e 取详情；表达式未给缓存定价时缓存按输入价计（后按用户决定改为不收费，见第 7 节）。AC10：其他节点规则 5 秒内刷新 → e2e 等一个周期；熔断按单节点连续 10 次失败 → e2e 循环到熔断打开。AC11：guard 按分钟桶统计 → e2e 用整分钟窗口；游标字段 `cursor.last_event_id`。AC13：设置接口体为 `{values:{...}}`；出口日志在连接关闭时写入（keep-alive 最长 90 秒） |
| 5 | `5bc00f3df` | AC08/10/13 单独通过（AC08 仅剩价格历史比较：历史存规范形式 `v1:...`，e2e 改为按哈希和去前缀比较） | |
| 6 | `5bc00f3df` | 全量（含 `E2E_LONG=1`）：AC01–09 通过；AC10 起本机 SSH 隧道断开（网络问题）导致后续全失败 | 重建隧道（PG、Redis、3120 合为一个后台任务） |
| 7 | `5bc00f3df` | 重跑 AC10–17：AC12、AC17 失败 | AC12：任务接口返回 `{jobs, recent_runs}`，e2e 按数组读 → 改读 `recent_runs`；AC17：计时包含 ssh 执行 docker kill 的耗时 → 改为 kill 返回后计时 |
| 8 | `5bc00f3df` | AC12、AC17 通过 → **17 条验收全部通过** | 另在浏览器实测控制台（经本机 Node 反向代理）：登录、概览、guard 原生插槽卡片、插件详情正常；修复插件文字图标 `text:X` 显示为破图（`e6f7e4483`） |

**后续**
1. 第 4 节"仍待办"：`ClientRequestID`、`/settings/gateway` 归属、F 列出的"仍缺"接口说明。
2. 第 5 节未做项按优先级排期：登录限速与 refresh token 重放检测（#4）、卸载插件清账号（#6）、SSRF 拨号钩子（#12）、新外部域名告警（#3）、出口日志长连接（#14）。
3. 每个里程碑后推送 `feat/next-platform`（网络不稳时重试）。

---

## 9. 第二轮：账号类型与端点多对多、全局定价（2026-09-24 起）

**用户决定**
- 插件未启用时端点不存在，返回 404（已完成：`0398c0bc3`）
- 平台、账号类型、端点多对多：任何插件都能声明账号类型，账号类型声明上游原生支持的协议；同一分组可混放多种类型账号共同服务一个端点
- 协议转换：核心内置转换器，能转换就自动转换，否则只调度原生支持的账号（本轮做框架，具体协议对等有对应上游时再补）
- 计费：价格只按模型全局设置，为基础价格，只用倍率调整
- 本轮不启动测试数据库，直接在 sup2api 上验证；sup2api 可清空重建

**主控已完成**：设计 ARCHITECTURE §6.6、§7.3"定价范围"；契约 CONTRACTS §12；manifest / proto / core / 迁移 0005（提交 `10a112551`、`929e0ee3f`）

**派发的 agent（基于 `929e0ee3f`，各自 worktree）**

| 代号 | 目录 | 分支 | 状态 |
|---|---|---|---|
| c-plugin-types | `plugin/registry`、`pkg`、`install`、`api`、`grpcruntime`、`rollout`、`routes` | next/c-plugin-types | ✅ `cfc97cd0d`，合并 `ad7cafbd6` |
| a-account-billing | `account`、`billing`、`usage`、`event` | next/a-account-billing | ✅ `a617df00b`、`dec826607`、`20a87feb7`，合并 `8f11cfa99` |
| g-gateway-types | `gateway`（含 `gateway/convert` 转换器框架） | next/g-gateway-types | ✅ `e58dc14c8`，合并 `dc12b4a45` |
| f-web-types | `web/` | next/f-web-types | ✅ `9023bb6d1`，合并 `95765f35e` |
| e-plugins-types | `sdk/pluginsdk`、`plugins/*`（含新插件 relay）、`tools/sub2api-plugin`、`e2e`（新增 AC18）、`build-go.sh` | next/e-plugins-types | ✅ `72fc5ebbe`、`50a2417dc`、`a6a352224`、`faa257e12`，合并 `048c48695` |

**主控整合（已做）**：`app` 组装共享转换器注册表；账号类型需要 `accounts.credentials` 授权才注册（未授权插件的账号不参与调度）；价格优先级改为"模式越具体越优先，同样具体再按安装先后"（billing agent 原实现是安装先后优先）。

**sup2api 验证（2026-09-25，数据已清空重建，部署 `6c6b69b`）**：迁移 0001–0005 在新库上执行成功；内置 anthropic 自动启用；从市场安装并启用 relay；两种账号类型的 `/account-types` 都显示原生服务 `POST /v1/messages`、`/v1/messages/count_tokens`；同一分组放 anthropic apikey 与 relay relay_key 各一个账号（上游为 httpbin 回显、假 Key），16 次请求分别由两个账号服务（7 / 9）；使用记录的 `plugin_key`、`account_type`、`upstream_protocol` 正确；禁用 relay 后只调度 anthropic 账号、relay 账号保留。验证数据已删除，relay 保持启用。验证中发现并修复：删除用户不删除其 API Key（导致分组无法删除）。部署时修复：market-init 内存 64M 不够，改 256M。

**合并后主控要做**：`internal/app` 组装（gateway 的 ProtocolConverters 交给 account）；清空 sup2api 数据卷重新部署；在 sup2api 上验证混合账号类型服务 `/v1/messages`；更新本节。

---

## 10. 第三轮：平台声明端点、账号类型声明平台、内置三平台（2026-09-25 起）

**用户决定**
- 账号 → 账号类型 → 声明支持的平台 → 平台声明端点；不同账号类型可以支持同一个平台；不同平台的端点不能冲突
- 分组 = 一组账号一起提供服务（可混放类型）；API Key 只绑定一个分组
- 核心内置 anthropic、openai、gemini 三个平台及端点；插件可以声明新平台，id 不能与内置或其他插件重复，端点不能冲突
- 第二轮已完成的全局定价、协议转换框架、凭证授权、未启用插件端点 404 保持

**第二轮收尾**：f-web-types 已合并（`95765f35e`）并部署到 sup2api（`1b47af5`），控制台与第二轮后端一致。

**主控已完成**：ARCHITECTURE §6.4、§6.6 重写；CONTRACTS §13；manifest（`platforms[]` 含 endpoints、`accountTypes[].platforms`）、core（`EndpointBinding.Platform`、`PlatformBinding.Builtin`、`Platforms()`、`PlatformForProtocol`、`AccountTypesForPlatform`、`Supports`）、新包 `server/internal/platforms`（嵌入 JSON，anthropic 已从插件移入）——提交 `989d87973`、`025f7f617`

**派发的 agent（基于 `025f7f617`）**

| 代号 | 目录 | 分支 | 状态 |
|---|---|---|---|
| g3-platforms-gateway | `gateway`、`platforms/openai.json`、`gemini.json` | next/g3-platforms-gateway | ✅ `8d5ed9e94`，合并 `0628a34c1` |
| c3-registry | `plugin/*` | next/c3-registry | ✅ `4c2b1f803`，合并 `2bc6a7ae7` |
| a3-accounts | `account`、`billing`、`usage`、`group`、`apikey`（新接口 `/platforms`，分组/Key 的 `platforms`） | next/a3-accounts | ✅ `544e8010a`、`886cec1a7`、`3b5d09d4b`、`49aa41d97`，已合并；app 已改 group/apikey 构造函数 |
| e3-plugins | `plugins/*`、`tools/sub2api-plugin`、`e2e`（AC19）、`sdk/pluginsdk` | next/e3-plugins | ✅ `4e3b40354`、`033940989`、`042799583`，合并 `54d6e76e6` |
| f3-web | `web/`（平台页、分组/Key 显示可访问平台） | next/f3-web | ✅ `56ee98453`，合并 `c92c7c0a8`；主控补 `/me/menus` 平台菜单与 `/platforms` 的 plugin_name |

**第三轮完成（2026-09-25）**。

**sup2api 验证（部署 `298041c`，数据清空重建）**：三个内置平台及端点都在 `/platforms`；anthropic 插件只声明 apikey 账号类型（→ anthropic 平台）；市场安装 relay，relay_key → anthropic；空分组 platforms=[]，放入两种类型账号后分组与 API Key 的 platforms=[anthropic]；16 次 `/v1/messages` 两种账号各服务 8 次；同一 Key 调 openai 端点 → 503 openai 格式，调 gemini 端点 → 503 gemini 格式，未知路径 → 404；禁用内置 anthropic 插件后 anthropic 平台仍在、只由 relay 账号服务，重新启用后恢复；内置粘性规则 `claude-code-session`、`openai-prompt-cache-key`（source=builtin）已同步。验证数据已删除。

**合并后主控要做**：`internal/app` 组装（group/apikey 可能新增 registry 依赖）；清空 sup2api 重建并验证；更新本节。（全部完成：f3-web 合并后部署 `3eec84b`，浏览器实测平台页、分组"可服务的平台"正常，菜单含"平台"）

---

## 11. 第四轮：OpenAI/Gemini 账号接入、安全加固、插件运行时完善、接口收尾（2026-09-25 起）

**用户选择**：四批全做（OpenAI/Gemini 账号接入、安全加固、插件运行时完善、契约与接口收尾）。

**主控已完成（`fada0e988`）**：proto `HostService.Publish`、`AppService.OnBroadcast`；manifest 能力 `app.broadcast.v1`、权限 `broadcast`、用量映射 `a+b` 求和；core `UsageRecord.ClientRequestID`、`PluginAccountPurger`、事件 `plugin.egress_new_domain`；迁移 0006（client_request_id、plugin_egress_domains、egress 日志 closed_at、refresh token 家族）；CONTRACTS §14。决定：openai、gemini 两个账号类型插件与 anthropic 一样作为内置插件。

**派发的 agent（基于 `fada0e988`）**

| 代号 | 目录 | 任务 | 状态 |
|---|---|---|---|
| a4-identity-accounts | `iam`、`account`、`proxy`、`usage` | 登录限速、refresh 重放检测、`/me/platforms`、PluginAccountPurger、直连拨号 SSRF 校验、client_request_id | ✅ `414cc44fb`、`c57820207`、`05a470bbf`、`14fd7b00f`，合并 `bc7443e29`；主控组装 proxy AllowPrivate、install Accounts、可信代理配置 |
| g4-gateway | `gateway`、`platforms` | 用量求和、gemini 思考 token 计入输出、ClientRequestID、`/settings/gateway`、网关自我隔离 | ✅ `ac29f9dbb`，已合并 |
| c4-lifecycle | `plugin/install`、`market`、`api`、`routes`、`pkg` | 卸载清账号、市场兼容性、出口域名接口、资源限制广播、插件接口自我隔离、broadcast 校验 | ✅ `44c41864b`，已合并；routes 健康检查已组装，install.Accounts 已组装 |
| d4-runtime | `plugin/grpcruntime`、`rollout`、`registry`、`egress`、`sandbox` | 插件集群广播、资源限制即时重启、旧版本缓存清理、节点重新验签、新域名记录与告警、出口长连接 | ✅ `6f1423e52`，合并 `1ffa5dd89`；主控 `aecc4a114` 节点验签改用 VerifyInstalled（已装插件不因签名密钥过期而停），`99614fdd6` 组装验签器、egress Events、runtime Bus |
| e4-plugins | `sdk/pluginsdk`、`plugins/*`（新增 openai、gemini）、`tools`、`e2e`（AC20）、`mock-upstream`、`build-go.sh` | 两个内置账号类型插件、SDK 广播、guard 规则即时生效、mock 上游支持 openai/gemini | ✅ `44f88c1bf`、`0f643d3ab`、`eb7d39024`、`4e0a067ae`，合并 `164f4ece9`；内置插件改为 `anthropic openai gemini` |
| f4-web | `web/` | 对应的控制台改动 | ✅ `332ee0de1`、`b923baefc`，合并 `73676fa1b`；含代理密码按 §15.4、refresh 单飞（同标签页共用、跨标签页 Web Locks） |
| docs4-contracts | CONTRACTS §15 | 按代码现状补齐前端提出的缺失接口说明 | ✅ `754cb51f8`，合并 `04b649149`；§15.10 列出 11 处与前文不一致，已于 `c00e2ed78` 裁定（9 处以代码为准改文档；插件列表统一用 `node_summary`、共享绑定的粘性规则 flush 返回 409 两处改代码）；发现代理密码掩码问题已转 f4 |

**合并后主控要做**：`internal/app` 组装（proxy AllowPrivate、install Accounts、routes/gateway 健康检查、registry 验签、egress 事件发布等）；清空 sup2api 重建并验证 openai/gemini（上游用 httpbin 回显或真实 Key，由用户提供）；更新本节。

**sup2api 验证（2026-09-25，清库重建后部署 `549710fa6`）**：两个脚本在 ovh 上对 127.0.0.1:3130 运行，上游用 httpbin 回显，测试数据用后清理，全部通过：
- 内置插件：anthropic、openai、gemini 均为内置并已启用；openai、gemini 平台分别由各自的 apikey 类型服务；卸载内置插件返回 403。
- 登录限速：同一邮箱加 IP 失败 5 次后返回 429，带 `retry_after_seconds=900`。
- refresh 重放检测：轮换返回 200；重放旧 token 返回 401，同一家族的新 token 也随之失效。
- 分组、`/me/platforms`、Key 的平台列表都正确。
- 网关转发：
  - openai chat 以 `Authorization: Bearer` 转到 `/v1/chat/completions`；
  - gemini 以 `x-goog-api-key` 转到 `/v1beta/models/…:generateContent`；
  - 只有 openai 加 gemini 账号的 Key 调 `/v1/messages` 返回 503 `no_available_account`（端点存在但没有账号类型能服务，§12）。
- `X-Request-Id` 记录为 `client_request_id`，可以按它筛选。
- 市场：返回 `host_version`，每个版本都带 `compatible`。
- `/settings/gateway`：读取正常；非法值返回字段错误；可以保存。
- 卸载 relay 时带 `purge_accounts=true` 返回 `accounts_deleted=1`。
- guard（从市场安装）：申请 broadcast 权限；经隧道连接插件库后出现在出口域名 `pg` 中，长连接显示为 `open`，并产生 `plugin.egress_new_domain` 事件。
- 说明：平台适配插件只负责组装请求，由核心发送，所以上游域名不算插件出口，openai 的出口域名为空是正确的。
- 顺带修复 `549710fa6`：卸载插件时清除它的 `plugin_egress_domains`（该表没有外键），重装后再次连接同一域名会重新告警。

**数据库单测补跑（2026-09-25，用户同意）**：在 ovh 上用 `~/sub2api-next-test/testdb`（compose）加 `deploy/ci/compose.yml` 的 Go 容器补跑，跑完已 `down -v` 清理，只剩 sup2api 在运行。
- 结果：server 271 个测试通过；sdk、5 个插件（含 guard 广播测试）、tools、mock-upstream 全部通过。唯一跳过的是 `TestDemoPlugins`，它需要 `S2P_DEMO_DIR`。
- 发现 2 个测试没跟上前几轮的行为变更，已修正（`c6b91604c`）：
  - billing：价格优先级先比模式具体程度，再比安装先后；
  - proxy：测试 SQL 还在用第三轮删掉的 `accounts.platform` 列。
- 注意：sup2api 的 PG 不能拿来跑测试。dbschema 测试会创建和删除与线上插件同名的 `plugin_<key>` 角色。

---

## 12. 模型价格只用完整模型 ID（2026-09-25，用户要求）

**决定**：价格按完整模型 ID 精确匹配，禁止通配符（CONTRACTS §16）。分组模型白名单、粘性规则、钩子匹配仍然支持通配符。

**改动（`fccaec5e3`）**：
- **校验**：`manifest.ValidModelID`，只允许字母、数字和 `. _ : / @ + -`，1–200 个字符。价格接口、manifest 服务端校验、`sub2api-plugin` 和插件默认价格同步这四处都用它校验，并拒绝重复模型。
- **数据库**：迁移 0007 把 `model_pattern` 改名为 `model`，删除已有的通配符行，并加 CHECK 约束。
- **计费**：`Resolve` 改为按模型 ID 查表；优先级为管理员价格，其次最早安装的插件。
- **接口与前端**：接口字段改名 `model`；前端价格页、使用记录详情、mock 同步修改，价格编辑页会提示通配符不合法。
- **内置插件**：anthropic、openai、gemini 升到 0.1.1，默认价格展开成完整 ID：别名和带日期的 ID 各写一条，共 66 条。
  - 去掉了不存在的 `claude-opus-4-2*`；gemini 的 `gemini-3-pro*` 换成 `gemini-3.1-pro-preview`，因为 3 Pro Preview 已于 2026-03 下线。
  - 价格数值没有改。
  - 同版本号的内置包启动时不会重新上传，所以必须升版本，已部署的环境才会自动升级并重写默认价格。
- **测试**：server 全部数据库测试在临时测试容器中通过（跑完已清理）；sdk、插件、tools 通过；web 构建通过。
- **sup2api**：没有清库，直接升级。三个内置插件自动升到 0.1.1，共 66 条价格，没有通配符；接口对 `claude-*`、`gpt-4?`、`gpt 4o` 返回 400 `model invalid`；数据库 CHECK 约束也会拒绝通配符。

**注意**：别名更新到新快照时，新的带日期 ID 需要补价格，否则按"找不到价格"的策略处理（默认拒绝）。

---

## 13. 模型价格归核心、管理员配置，价格同步源（2026-09-25，用户要求）

**决定**：模型价格与插件无关，只能由管理员在核心中配置。可以从权威价格库（LiteLLM、models.dev）或上游 sup2api 同步，先预览再选择导入（CONTRACTS §17，取代 §16 中"插件默认价格"的部分）。

**改动**：
- **后端（主控，`d6f96f368`）**：
  - 迁移 0008：新建表 `price_sync_sources`，预置 LiteLLM、models.dev 两条源；`model_prices` 删除 `plugin_key`，`source` 只有 manual/sync，每个模型唯一。
  - 新文件 `billing/sync.go`：三种源的解析，preview 和 apply 接口，以及供下游同步的 `GET /key/prices`（用 API Key 鉴权，只返回分组白名单允许的模型，并附带分组倍率）。上游 Key 用 AES-GCM 加密保存。
  - 删除 `core.PriceCatalog` 和 `/prices/:id/override`；manifest 删除 `pricing`（声明了会报 `unsupported`），打包工具同步。
  - 内置插件 anthropic、openai、gemini 升到 0.1.2，不再带价格。
- **前端（f5-web，`d8f57aa50`，合并 `f949b5ffc`）**：
  - 价格列表按来源筛选，页头有"同步价格"入口；同步价格的编辑页显示提示。
  - 新增同步源页 `/prices/sources`（增删改查），以及同步预览页 `/prices/sources/:id/sync`：统计、按 action 分标签、勾选规则、分页，能流畅处理约 2400 条。
  - mock 同步修改。
- **测试**：server 全部测试（含数据库）在临时测试容器中通过，跑完已清理；插件、sdk、tools 通过；web 构建通过，f5 用 mock 自测 47 项通过。

**sup2api 实际操作**：
- 部署后迁移清掉了插件带来的价格。
- 从 LiteLLM 导入 177 条（含 1 小时缓存写入价、200K 以上的第二档）；从 models.dev 只导入它独有的 13 条，共 190 条。
- models.dev 另有 31 条"更新"没有应用：它缺 1 小时缓存写入价，应用会覆盖 LiteLLM 更完整的价格。
- 用户的 Key `sk-s2a-c2oH7…`（admin，分组 123，anthropic 账号 codingplus）调用 claude-opus-5-5：
  - 余额为 0 时返回 402，主控给 admin 加了 $10 测试余额；
  - 流式和非流式都返回 200；
  - 计费与 LiteLLM 价格一致（输入 $4、输出 $20、缓存读 $0.2、缓存写 $5 每百万 token）。
  - 上游是会注入约 1.8 万 token Claude Code 系统提示词的中转。

---

## 14. 账号的模型列表与映射、优先级与权重、RPM/TPM/TPD/SPM 限流；新建账号界面（2026-09-25，用户要求）

**决定**（CONTRACTS §18）：模型列表、模型映射、权重和限流都是核心账号属性，由管理员在账号上配置，与插件无关；插件不再提供模型映射。优先级语义不变（数值越小越先用），同优先级内按权重加权随机。限流四项：rpm、tpm、tpd（UTC 自然日）、spm（每分钟会话数，口径参考 new-api：60 秒滚动窗口内去重的会话数，会话 = 粘性会话键，无粘性规则时每请求一会话，窗口内已有的会话总是放行）。

**改动**：
- **插件（plugins6 / plugins6b，`237cc15d7`）**：anthropic、openai、gemini 升到 0.1.3，relay 升到 0.1.1；表单、`settingsFields` 和代码里的 `model_mapping` 全部删除，`sdk/pluginsdk/apikey` 删除 `MapModel`；旧账号残留的 `model_mapping` 键解析时忽略。
- **后端（主控，`438db154c`）**：
  - 迁移 0009：`accounts` 新增 `models text[]`、`model_mapping jsonb`、`weight`、`rpm_limit`、`tpm_limit`、`tpd_limit`、`spm_limit`；把原来 `settings.model_mapping` 里的完整模型 ID 映射搬到核心字段（通配符项丢弃）。
  - `core.AccountRef` 新增对应字段和 `ServesModel`/`MapModel`；新端口 `core.AccountLimiter`，`account.Limiter` 用 Redis 实现（`rl:account:{id}:*`）。
  - 网关：候选按 `models` 过滤；`pick` 先剔除达到上限的账号，再按优先级、权重加权随机排序；选定账号后核心改写请求模型（body / 路径参数 / `RequestMeta.model`）再交给插件，`upstream_model` 记录映射后的模型；每次尝试计 rpm/spm，响应结束后累加 tpm/tpd；全部账号被限流时返回 429 `rate_limited`。
  - 账号接口：新字段的校验（`models[i]`、`model_mapping.<from>` 等字段错误）、`?model=` 筛选、排序 `priority, weight DESC, id`、`rate_usage`；测试连接前先做映射。
  - e2e、构建脚本的版本引用更新。
- **前端（web6b，`77d71f289`）**：新建账号第 1 步改为紧凑卡片网格（端点折叠）；编辑表单分为基本信息 / 调度 / 限流 / 模型 / 模型映射（表格或 JSON）/ 凭证；列表新增"调度"与"限流"列和 `?model=` 筛选；详情展示模型、映射、限流与用量；mock 同步。
- **文档**：CONTRACTS §5.4、§7、§18；ARCHITECTURE 4.3、6.2、A.4。
- **测试**：server 全部测试（含数据库，在临时测试容器中）通过，跑完已清理容器和缓存卷；四个插件与 sdk 通过；e2e `go vet` 通过；web typecheck/build 通过。新增测试：`gateway/scheduling_test.go`（模型过滤、映射改写、限流跳过与 429、粘性会话作为 spm 身份、加权顺序）、`account/limiter_test.go`（四种窗口）、`account/models_test.go`（接口校验、列表筛选、快照、测试连接映射）。

**sup2api 实际验证**（部署后未清库；三个内置插件自动升到 0.1.3，迁移 0009 把 codingplus 账号原有的插件映射搬到了核心字段）：
- 校验：`models:["claude-*"]`、`weight:0`、`model_mapping:{"a b":"c"}`、`spm_limit:-1` 各返回对应字段错误。
- 映射：把 `claude-sonnet-5 → claude-fable-5-1` 配到账号上，客户端请求 `claude-sonnet-5`，上游返回 `claude-fable-5-1`；使用记录 `model=claude-sonnet-5`、`upstream_model=claude-fable-5-1`，按 claude-sonnet-5 计费。
- 模型列表：账号只列 fable-5-1 和 sonnet-5 时，请求 haiku 返回 503 `no_available_account`；`?model=gpt-4o` 列表为空。
- rpm=2：三次调用依次 200、200、429（`all accounts are busy or rate limited`），`rate_usage` 为 rpm 2 / tpm 32。
- spm=1：会话 A 两次都是 200，会话 B 429，`rate_usage.spm` 为 1。
- 上游中转当时对 claude-opus-5-5 返回 503 "无可用渠道"（上游自身问题，不是本项目），验证改用 claude-sonnet-5 / claude-fable-5-1；测试完账号配置已恢复。

**遗留**：
- "从上游拉取模型列表"需要新增插件 RPC（本地没有 buf/protoc），本轮没做；模型列表的候选来自已配置价格的模型。
- 分组白名单、粘性规则和钩子的 `match.models` 仍支持通配符，用户没有要求改。

---

## 15. 从上游拉取模型列表（2026-09-25，用户要求；同步上游价格暂不做）

**决定**（CONTRACTS §19）：插件新增一个可选的 gRPC 方法 `BuildModelsRequest`，只负责构造"列出模型"的请求；核心经账号的代理发出请求并提取模型 ID。控制台模型列表区新增"从上游获取"按钮。

**改动**（主控，`90c89a0f7`、`5df03a25c`）：
- **proto / SDK**：`PlatformService.BuildModelsRequest`，返回 `{method, url, headers, body_json, ids_path, strip_prefix}`；本地用 buf v1.57 + protoc-gen-go v1.36.12 + protoc-gen-go-grpc v1.6.2 重新生成（装在 `go env GOBIN`）。SDK 新增可选接口 `pluginsdk.ModelLister`，没实现的插件由 SDK 回答 `UNIMPLEMENTED`；核心把它转成 501 `unsupported`。
- **内置插件**：anthropic、relay 用 `GET /v1/models?limit=1000`，openai 用 `GET /v1/models`，gemini 用 `GET /v1beta/models?pageSize=1000`（取 `models.#.name`，去掉 `models/` 前缀）。anthropic、openai、gemini 升到 0.1.4，relay 升到 0.1.2。
- **核心**：
  - 新增接口 `POST /account-types/:plugin_key/:type/models/fetch`（新建时用表单里的凭证，先走 Schema 和插件校验）和 `POST /accounts/:id/models/fetch`（可传凭证覆盖，`******` 保留原值）。
  - 结果去重、排序，非法 ID 计入 `skipped`，最多 5000 条；上游非 2xx 返回 503 `unavailable`，`details.status` 是上游状态码；有内网地址保护，30 秒超时。
  - 新增错误码 `unsupported`（501）。
- **控制台**：模型区的"从上游获取"会用当前表单的凭证拉取，弹出勾选框（默认全选，已在列表里的会标出来），确认后合并进模型列表；mock 同步。
- **mock-upstream**：新增 `GET /v1/models`、`GET /v1beta/models`；新增 e2e AC21。
- **e2e 修正**：AC06 在插件禁用期间账号本来就算 orphaned（§15.9），改为重新启用后再检查；AC07 粘性默认规则现在是内置平台的 `builtin`（§15.5）。

**测试**：
- server 全部测试（含数据库）在临时容器中通过；sdk、五个插件、tools 通过；web typecheck/build 通过。
- 在临时 e2e 栈（两节点 + mock）上跑完整 e2e：全部通过（AC12 需 `E2E_LONG=1`，按惯例跳过）。
- 跑完已清理 e2e 栈、测试库、CI 缓存卷。

**sup2api 实际验证**：三个内置插件自动升到 0.1.4。codingplus 账号拉到上游的 11 个模型（claude-fable-5、claude-fable-5-1、claude-opus-5-5、claude-sonnet-5 等）；openai 类型用错误 Key 拉取返回 503，`details.status=401`，message 带上游的错误信息。

---

## 16. 提示词审核插件 moderation（2026-09-25，用户要求）

**决定**（CONTRACTS §20）：做成内置插件 `moderation`。网关钩子取请求里最新一条用户消息，交给管理员配置的 OpenAI 兼容 LLM；LLM 拿到工具 `submit_verdict`，必须调用它写回结论（pass / flag / block、分类、严重度、理由）。没调用或参数不合法时，插件把错误作为 tool 消息或追加提示回给 LLM 再来一轮（小型 agent 循环，最多 `max_turns` 轮）；不支持工具的模型退回解析 JSON 文本。参考旧 sub2api 的"内容审计 / 提示词审计"：观察（异步记录）和拦截（同步拒绝）两种模式、违规计数自动封禁、审核记录页面。

**核心改动**（`ba4dc35b9`）：钩子超时上限 2s → 30s（gateway `maxHookTimeout`、`grpcruntime.TimeoutHookMax`、包校验 `MaxHookTimeout`；`default_hook_timeout_ms` 仍限 50–2000）；钩子延迟直方图末尾加 10s/20s/30s 三档；manifest `needs` 支持 gjson 查询（如 `messages|@reverse|#(role=="user")`，只取最后一条用户消息），查询字段只读、不能打补丁。mock-upstream 对带 `submit_verdict` 工具的 chat completions 模拟审核模型（MOD-BLOCK / MOD-FLAG / MOD-NOTOOL / MOD-BADARGS 标记）。

**插件**（`0bad81a0a`、`aeb21d000`，`plugins/moderation`，0.1.0）：
- 设置（Schema 表单）：模式、Base URL、API Key（secret）、模型、系统提示词（空用内置，`{{categories}}` 展开分类）、自定义分类、tool_choice、轮数、temperature、max_tokens、单次超时、失败放行/拒绝、并发、观察队列、文本上限（头 2/3 + 尾 1/3 截断）、最短长度、抽样率、分组/模型/豁免用户过滤、结论缓存 TTL、拒绝状态码与文案、记录通过项、保存文本、保留天数、自动封禁阈值/窗口/时长。
- 钩子流程：off/未配置直接放行（0 分配）→ 封禁用户 403 `moderation_user_blocked` → 分组/模型过滤 → 取文本（anthropic messages、openai chat、responses、gemini contents 四种形态，剥离 `<system-reminder>`）→ 自身标记（HMAC，base_url 指向本网关也不会递归审核）→ 缓存（内存 LRU + KV 跨节点，键含策略版本）→ observe 入队放行 / enforce 同步审核（singleflight、信号量），block 则拒绝 `moderation_blocked`。结论写进 usage 的 hook note。
- 记录批量写库；block 累计违规，达阈值自动封禁并广播，各节点刷新封禁表；解封后计数重新开始；cleanup 任务清理过期记录和封禁。
- 接口：`/overview`、`/events`（筛选 + 分页 + 详情/删除）、`/blocks`（列表、手动封禁、解封）、`/test`（在线测试，返回结论和完整 agent 对话）、`/defaults`。权限 `moderation:read` / `moderation:manage`。
- native 控制台页"提示词审核"：概览（统计卡、趋势图、分类/违规用户排行、本节点运行状态）、审核记录、封禁用户、在线测试；设置按钮跳插件详情的设置页。
- 打包：`build-demo.sh` 增加 moderation 包，`build-go.sh` 的 `BUILTIN_PLUGINS` 默认含 moderation。

**测试**：plugins/moderation 单元测试（提取、截断、标记、agent 循环各分支、缓存、钩子各模式、路由）和数据库测试在临时 CI 容器中通过；server 全部测试通过；新增 e2e AC22（拦截/缓存/agent 追问/观察/自动封禁与解封/在线测试/经网关自审自身识别）；在全新 e2e 栈上跑完整 e2e 全部通过（AC12 按惯例跳过）。e2e 的设置助手改为等每个节点的 `runtime.settings_hash` 更新，避免节点还在用旧设置。`TestGatewayFailoverAndCooldown` 出现过一次超时类偶发失败（2 秒冷却窗口内的时序），单独重跑两次都通过，与本次改动无关（钩子 off 时延迟 0ms）。

**sup2api 实际验证**：moderation 0.1.0 自动安装启用，菜单出现"提示词审核"。上游用 codingplus 的 OpenAI 兼容接口 + claude-sonnet-5：在线测试"写快速排序"→ pass、"合成冰毒步骤"→ block [illegal] high、"小说反派独白"→ pass（能区分虚构），每次一轮工具调用、约 2–3 秒、1.6k prompt tokens。网关拦截模式下违规请求 403 `moderation_blocked`，usage 记为 `blocked_by_hook`、免费，note 带分类和理由；同样文本第二次命中缓存、0ms。先前把 Base URL 指向网关自身 openai.chat 时因分组没有 OpenAI 账号得到 503，审核记为 error 并按 `on_error=allow` 放行，符合预期。验证后模式已设回 off（上游配置保留）。

**遗留**：插件每实例 64 个并发调用上限由钩子、路由、任务共享，enforce 模式高并发时后来的请求会排队；`inbound_headers` 钩子仍拿不到；native 页面只做了构建和接口联调，没有截图目视。

---

## 17. 账号与代理的所有权授权、受限 base_url、保存账号自动关联代理（2026-09-25，用户要求）

**需求**：供应商角色只维护自己创建的账号和代理、不能改 base_url（只能用官方地址）；只读角色能看全部账号但不能改、看不到密钥；保存账号时直接填代理串，后端自动解析并复用同配置的代理或新建再关联。

**决定**（CONTRACTS §21；用户裁定）：现有 `account:*`/`proxy:*` key 不改名、语义为"全部"；新增自己级 key **拆开**（`account:own:read/create/update/delete/test/credential:view`🔐、`proxy:own:read/manage`）与 `account:settings:custom`；`own:delete` 不要求 step-up；relay 等未声明 guard 的类型供应商照常可用；历史 `created_by=NULL` 的数据只有全部级可见；审计一起做；只读角色可见 `settings.base_url` 与代理 `username`。

**核心**（`c03bbdb2b`）：`Router.PermAny(method, path, handler, keys...)`，命中 key 放 ctx（`core.Granted`），任一命中 key 敏感即 step-up；`core.OwnerScope(ctx, allKey) *int64`（nil=全部）；范围条件落 SQL、越权 404；审计公共包 `internal/audit`（从 plugin/install 上提）；迁移 `0010_ownership.sql`（`proxies.created_by` + 索引）；代理模块 own 范围、`created_by`/`created_by_email`、`mine`/`created_by` 筛选、审计；`core.ParseProxyURL`（解析器放 core，账号模块不 import 代理模块）、`core.ProxyResolver.FindOrCreate`（匹配 protocol/host/port/username/password，密码解密后常量时间比较，跳过 disabled，取最小 id，事务内 advisory lock）+ `AuditAutoCreate`、`ProxyDirectory.HTTPClientFor`（临时客户端）。

**manifest**（`02dcb92b6`）：`accountTypes[].guardedSettings [{field, allowed[]}]`，pkg 校验；anthropic/openai/gemini 声明 base_url 官方地址并升到 0.1.5；`GET /account-types` 返回 `guarded_settings`。

**账号模块**（`5a17af470`）：路由 PermAny；own 范围贯穿列表/详情/改/删/测试/拉模型/reveal；`proxy_url`（与 `proxy_id` 互斥，需 `proxy:manage|proxy:own:manage`，在调用者可见的代理范围内匹配，与账号写入同事务）、响应 `proxy_created`；受限设置在插件归一化后校验（空/官方/原值放行，等价形式规范化），无权限 400 `credentials.base_url/forbidden`；form 按权限改写 `enum` + `ui:readonly`；`account.create/update/delete` 审计。

**前端**（`4cf01e05f`）：`useOwnership()` 行级判定；账号/代理页创建人列、只看我的、创建人 ID 筛选；账号表单代理"选择已有 | 粘贴代理串"；代理页粘贴自动填充；`url-presets` 有 enum 时只能选；菜单/路由 anyOf；mock 加 vendor/readonly 身份。

**测试**：server 全部测试（含库）在临时 CI 容器通过；新增 e2e AC23（供应商建账号带 proxy_url 复用/新建、越权 404、base_url forbidden/官方/原值、只读 403、管理员筛选、own:delete 免 step-up、审计行）；全新 e2e 栈跑完整 e2e 全部通过（AC12 跳过）。用完清理 e2e 栈、测试库、CI 缓存卷。

**sup2api 实际验证**（`2d0813db9` 部署）：9 个新权限进目录；插件 0.1.5；临时供应商用户：菜单只有账号/代理/平台，form 的 base_url 变 enum+readonly（管理员不变），`proxy_url` 首次 `proxy_created=true`、`SOCKS5H://…/` 等价串复用同一代理，改 base_url 为非官方 400 forbidden、官方地址通过，非法串错误不含密码，对管理员账号 GET/PATCH 404，删自己账号 204 无需 step-up；管理员 `mine`/`created_by` 筛选正确。验证数据已清理。

**遗留**：供应商能否使用某账号类型未做限制。（native 页面目视、`account_count` 排除孤立账号见 §18。）

---

## 18. 控制台目视检查与修正、base_url 默认值交给插件、插件自有侧栏区、禁用插件后隐藏账号（2026-09-25 ~ 26，用户要求）

**控制台目视**：浏览器预览工具在本机不可用，改用本机 Chrome 无头模式（puppeteer-core）登录 sup2api 截图检查账号、代理、角色、提示词审核页面，以及临时供应商用户视角。发现并修复（`0743e424e`）：代理串提示文案里的 `@` 被 vue-i18n 当成链接消息语法，点"粘贴代理串"时编辑器直接报错；改为 `{'@'}` 转义。之前只跑了 typecheck/build 和 mock 冒烟，没覆盖到这个运行时错误。

**base_url 默认值由插件提示**（`217fa9d0a`，取代 `8d61c8f14` 的核心实现）：用户要求"每个账号类型都有默认 base url，留空就用默认，由插件自己完成"。三个内置插件的 apikey 表单去掉 `base_url` 的 `default`（不再预填）、`pattern` 允许空串、`ui:placeholder` 写"留空使用默认地址 …"，空值由插件 `ValidateCredentials` 归一化为官方地址；anthropic/openai/gemini 升到 0.1.6。核心撤掉了为 base_url 加的特判，只保留两处通用行为：`url-presets` 清空即不发该键；受限（只读）字段只显示"由管理员设置"。

**插件自有侧栏区**（`4da9f65b2`、`26ce1fca4`，CONTRACTS §22）：manifest `ui.sections [{id, label, order}]` 声明插件自己的侧栏区，`ui.menus[].section` 可以是 `plugins`、核心区 id（追加到该区）或自己声明的区；核心区 order 固定（概览 100 … 插件 600），插件区按 order 穿插。`/me/menus` 中插件区为 `<插件key>:<id>`、带 label；校验拒绝未知区和占用核心 id。moderation 升到 0.1.1，声明"安全"区（order 350），"提示词审核"显示在财务和系统之间。说明给用户：插件页面代码（`plugins/moderation/ui/native/*`）随插件包发布，核心只提供声明格式、加载挂载、权限过滤、`@sub2api/ui` 组件库与 `@sub2api/host` 宿主 API。

**禁用插件后隐藏账号**（`a846ac90f`、`dee595466`）：用户裁定"插件禁用后账号看不到，数据库保留数据"。`GET /accounts` 默认只列已启用插件的账号（`plugin_key = ANY(已启用插件)`），`?orphaned=true` 只列孤立的、`?orphaned=all` 都列，详情接口不变；控制台加"显示已禁用插件的账号"开关，孤立账号不显示编辑按钮。分组、代理的 `account_count` 同样只统计已启用插件的账号（`core.ActivePluginKeys`，注册表为空时不过滤）；删除代理的 409 仍按全部引用判断。

**测试**：authz 菜单测试覆盖插件自有区与插件项进核心区；manifest 校验加正反用例；账号、分组、代理测试覆盖孤立账号过滤与计数；server 全部测试（含库）在临时 CI 容器通过。e2e：AC22 断言"安全"区位置；AC06 断言禁用后详情仍在、列表默认隐藏、`orphaned=all` 可见；AC18 新增卸载 relay + `purge_accounts=true`（账号软删除、anthropic 账号不受影响，再从市场重装）。全新 e2e 栈跑完整 e2e：25 通过、AC12 按惯例跳过。用完清理 e2e 栈、测试库、CI 缓存卷。

**sup2api 实际验证**：插件 0.1.6/0.1.1 自动升级；空 base_url 建账号 201，存为 `https://api.anthropic.com`；截图确认新建表单 base_url 空白 + 默认地址占位、供应商视角只读下拉 + "由管理员设置"、侧栏"安全 → 提示词审核"。验证数据已清理。

**过程问题**：
- 用 Python 在 Windows 改过的文件变成 CRLF；提交时 git 归一化所以 GitHub 部署正常，但 `sync.sh` 打包工作区，e2e 镜像构建时 `build-go.sh` 报 `set: Illegal option -`。已把 37 个文件转回 LF，之后改文件用 Edit 工具或 `newline=''`。
- 本机一度连不上 GitHub，推送经 ovh 的 SSH SOCKS 隧道（`git -c http.proxy=socks5h://127.0.0.1:18080 push`）。

**遗留**：供应商可用的账号类型未做限制。
