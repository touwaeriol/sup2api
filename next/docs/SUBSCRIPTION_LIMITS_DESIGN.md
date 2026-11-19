# 订阅账号套餐额度：设计说明

> 接口与语义的真相源是 `CONTRACTS.md` §44；本文记录为什么这样设计，以及与 sub2api 参考实现的对应关系。
> 2026-10-05 按 sub2api 重做。此前按 new-api 的 Kimi 方案做的骨架（插件拿解密凭证与 proxy_url 自己发请求的 `QuerySubscriptionLimits`、5 分钟缓存 + 进程内防抖、`/subscription/limits` 路由）从未发布，已整体替换。

## 1. 需求

- API Key 类账号不管；订阅类账号（Claude OAuth / setup token 等）记录 **5 小时限制、周限制、周 Fable 限制**。
- 能**重置状态**。
- 参考 sub2api，**尽量不要频繁查询**上游。

## 2. sub2api 怎么做（参考点）

| 关注点 | sub2api 出处 | 结论 |
|---|---|---|
| 被动采样 | `backend/internal/service/ratelimit_service.go` `samplePassiveUsageFromHeaders`（约 2057–2103 行），由 `UpdateSessionWindow`（1983 行起）在每次 Anthropic 响应后调用；429 路径 `persistAnthropicFableWindowLimit`（1612 行起）也调用它 | 头名 `anthropic-ratelimit-unified-{5h,7d,7d_oi}-{utilization,reset}`，另有 `-status`。utilization 是 **0–1 小数**（测试 `ratelimit_session_window_test.go` 用 `"0.15"`、`"0.87"`）；reset 是 **Unix 秒**，`> 1e11` 视为毫秒 |
| 被动 → 百分比 | `account_usage_service.go` `buildPassiveUsageWindow`（647–670 行）`Utilization: util * 100` | 存储/展示统一用百分比 |
| 主动查询 | `backend/internal/repository/claude_usage_service.go`（16–108 行） | `GET https://api.anthropic.com/api/oauth/usage`，头 `Accept: application/json, text/plain, */*`、`Content-Type: application/json`、`Authorization: Bearer`、`anthropic-beta: oauth-2025-04-20`、`User-Agent: claude-code/2.1.7` |
| 主动响应 | `account_usage_service.go` `ClaudeUsageResponse`（255–273 行）、`buildUsageInfo`（1635–1702 行） | `five_hour` / `seven_day` / `seven_day_sonnet` / `seven_day_overage_included`（= Fable，对应头 `7d_oi`）；utilization **已是百分比**（`syncActiveToPassive` 回写被动缓存时 `/100`）；5h 总是给，其余窗口仅在有 `resets_at` 时给 |
| 不频繁查询 | `account_usage_service.go` 107–127 行、`getUsageForAccount` 393–455 行 | 成功缓存 3 分钟（`apiCacheTTL`）、失败负缓存 1 分钟（`apiErrorCacheTTL`）、`singleflight` 按账号合并；账号列表（`GetUsageBatch`）对 Anthropic 只走被动链路，从不触发上游 |
| setup token | `estimateSetupTokenUsage`（1704–1765 行） | 没有 profile 权限，不能调 usage API，只靠响应头；窗口过期显示为 0 |
| 重置状态 | `routes/admin.go` 403 行 `POST /accounts/:id/clear-rate-limit` → `RateLimitService.ClearRateLimit`（`ratelimit_service.go` 2105–2128 行） | 清 `rate_limited_at` / `rate_limit_reset_at` / `overload_until`、模型级限流、临时不可调度（库与缓存）、OpenAI 403 计数，并通知调度器；**不改 `Status`**（清错误状态是另一个操作 `ClearError`） |

## 3. next 的落法

### 3.1 为什么拆成"被动声明 + 主动 RPC"

next 的原则是"插件描述、核心执行与记录"（`PLUGIN-EXECUTES-CORE-RECORDS.md`）：插件不开网络、不列账号、不自带定时器。

- **被动采样是纯数据映射**，用 manifest 声明（`accountTypes[].quota.headers`：窗口 key → 头名 + 换算）就够了，核心在网关热路径上读几个头，零插件调用、零额外上游请求。这也是 sub2api 账号列表的主数据源。
- **主动查询要凭证和上游知识**，沿用 `BuildTestRequest` 的形状：`BuildQuotaRequest` 描述请求、核心经账号代理 + netguard 发送、`ParseQuotaResponse` 把响应读成窗口与错误类型。插件不需要 `net` 权限，限频完全由核心掌握。
- manifest 再用 `quota.query: true` 声明"插件实现了主动查询"。这样核心不必试探调用就知道某类型支持额度（账号列表要据此决定 `quota` 是 null 还是快照），也不会对没声明的插件发 RPC。插件声明了却回 `Unimplemented` 时按"只支持被动"处理。

### 3.2 存储与合并

单表 `account_quota_snapshots`（迁移 0029，原地改写，从未部署）。窗口存成以 key 为键的 jsonb 对象，写入用 `windows || EXCLUDED.windows`：一次采样只覆盖它带来的窗口。这对应 sub2api 的做法——它把各窗口分别写进 `Account.Extra`，主动结果缺 Fable 窗口时再用被动值回填。

`last_passive_at` 与 `last_active_at` 分开：前者用于观察被动数据新鲜度，后者是主动查询**尝试**时间，30 秒下限用 `INSERT … ON CONFLICT DO UPDATE … WHERE last_active_at <= now() - 30s` 在 PG 中原子抢占，多节点有效（sub2api 是单进程内存缓存，没有这个问题）。

`error` 只表示"最近一次主动查询失败的原因"，任何一次成功写入都会清掉它；负缓存靠 `error != '' && now - last_active_at < 1min` 判断。

### 3.3 限频参数

| 参数 | 值 | 对应 sub2api |
|---|---|---|
| 快照新鲜度（被动或主动都算） | 3 分钟 | `apiCacheTTL` |
| 主动失败负缓存 | 1 分钟 | `apiErrorCacheTTL` |
| 主动查询下限（含 force，跨节点） | 30 秒 | 无（next 新增，满足"尽量不要频繁查询"且 force 也不能刷爆上游） |
| 被动写库间隔（每节点每账号） | 30 秒，状态变化立即写 | 无（sub2api 每次响应都写库；next 网关吞吐更高，必须限频） |
| 并发合并 | singleflight（节点内）+ PG 抢占（跨节点） | `apiFlight` |

sub2api 在缓存未命中时还加 0–800 ms 随机抖动以打散多账号同时查询；next 只在单账号详情请求时查询、列表从不查询，没有批量风暴，故不加。

### 3.4 被动写入器

`account.quotaRecorder`：网关 goroutine 里只做头解析和 map 合并（加锁、O(窗口数)），到期时向容量 1 的通道非阻塞投递唤醒；后台 goroutine（`Service.Run` 启动）在唤醒和每 5 秒的定时器上把到期样本写库。间隔内的样本合并保留最后值并在到期后补写，因此一阵突发流量的最后一个样本不会丢；进程退出时写出全部待写样本。10 分钟没有样本的账号从内存表移除。

### 3.5 重置状态

next 里限流、过载、失败冷却都落在同一个 Redis 键 `cooldown:account:<id>`（`SetCooldown`），没有 sub2api 那些分散的列，所以 `POST /accounts/:id/reset-status` = 删冷却键 + 审计 + （确有冷却时）`account.status_changed` + `account:changed` 广播。与 sub2api 一致，不恢复 `status = disabled`（§42 自动禁用或管理员禁用），也不清额度快照。

### 3.6 claude-oauth（已删除）

插件在 0.1.304 删除（CONTRACTS §116），本节原文见 `git show 77158211c:next/docs/SUBSCRIPTION_LIMITS_DESIGN.md`。现在没有第一方插件声明 `query`，codex 只用响应头被动采样。

## 4. 已知限制

- 主动响应不含窗口状态，主动写入的窗口 `status` 为空，直到下一次被动采样带回状态。
- 被动限频按节点计：N 个节点时同一账号最多 N 次写库 / 30 秒。
- 额度快照只用于展示；调度仍按网关对 429 等错误的分类冷却（§42），没有按 utilization 提前避让（sub2api 的 scheduling threshold 未移植）。
