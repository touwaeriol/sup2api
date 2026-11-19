# 插件执行、核心记录：插件参与计费与使用记录的契约

> **历史设计稿。** 现行契约是 CONTRACTS §112（转发 V2，2026-11-18）：每个平台插件只有 `Execute` 一条请求路径，插件拿到完整请求体、经核心的 HTTP 客户端发出请求，用量与任务事实只在 `Execute` / `Monitor` 的返回值里。本文里的 `BuildUpstreamRequest`、`requestFields`、`ExtractUsage` 事后调用、回调上报、`BuildReconcileRequest` / `ParseReconcileResponse` 都已删除。托管任务契约见 §28，后台执行器见 §30。本文写的大小预算（观察的 `maxBytes` 默认 256 KiB 与 64 条事件、`usageRequestFields` 单值 4 KiB / 总量 32 KiB 与 `fields_omitted`、`detail_json` 4 KiB、`path_params` 的个数和长度）在转发 V2 收尾时按所有者决定删除，默认都不限，见 CONTRACTS §112.16。

> 状态：**已全部实现**（A–D 四期 + E 期收尾，2026-09-30）。落地记录与设计更正在 [CONTRACTS](CONTRACTS.md) §25.1–§25.6 —— **那里才是真相源**，本文是当初的设计稿，正文里有若干条后来被证伪（每条都在 §25 对应小节里点明）。
> 这份文档讲的是**核心 SDK 契约的扩展**，不是某个插件。第一个用户是 [字节火山方舟 / 豆包插件](PLUGIN-VOLCENGINE-ARK.md)，但每一条都不带厂商语义。

---

## 1. 原则

**插件执行，核心记录。**

| | 谁知道 | 谁做主 |
|---|---|---|
| 模型在这次请求的哪里 | **插件** | — |
| 上游用了多少 token / 生成了几张图 | **插件** | — |
| 上游这个错误意味着什么 | **插件** | — |
| 这个异步任务算完成了没 | **插件** | — |
| 这个模型多少钱 | — | **核心**（管理员在价格表里配） |
| 这笔请求归谁、扣谁的钱、扣多少 | — | **核心** |
| 调度哪个账号、限不限流、能不能过 | — | **核心** |

一句话：**插件只陈述上游事实，核心据此做决定并落账。插件永远不说「扣多少钱」，只说「用了多少」。**

这不是新原则，是把现有设计推到底。价格已经归核心、由管理员配置（ARCHITECTURE 7.3）；`RankAccounts` 已经定死「核心保留最终调度权」（CONTRACTS §24）。缺的是**用量与使用记录这一侧还只能靠 manifest 里的声明式 gjson 规则**，表达不了的场景就卡死。

---

## 2. 现状：哪些已经是这样

| 环节 | 现状 | 够不够 |
|---|---|---|
| 模型提取 | `request.modelPath`（请求体 gjson）或 `request.modelParam`（路径参数），二选一必填 | ❌ 模型不在本次请求里就没法声明端点 |
| 用量提取 | `manifest.UsageRules`：SSE 按事件名 + gjson，JSON 按 gjson，另有 `facts` 供 `u("key")` | ⚠️ 标准形状够用；非标准、需要计算的不够 |
| 定价与扣费 | 核心：价格表达式 × 分组倍率 → `usage_logs` + `balance_ledger` 同事务 | ✅ 正确，不动 |
| 使用记录 | 核心从 `core.UsageRecord` 全量填写；插件只能影响 `upstream_model` 和 `metrics` | ⚠️ 插件补不了上游侧的细节 |
| 异步 / 延迟结算 | **没有**。`usage_logs.request_id` 唯一，一次请求一行，秒级结算完 | ❌ |
| 预扣费 | **没有**。请求前只做余额检查（`CheckBalance`），允许短暂透支 | ❌ |

另外两个已核实的硬约束，决定了某些做法走不通：

- `HostService` 只有 log / kv / db.schema / authz / ledger / broadcast——**插件没有任何列账号、读凭证的接口**，`accounts.read` 这个权限在非测试代码里从未被使用。插件无法自己挑账号发请求。
- `core.PlatformBinding` 是 `{Plugin, Builtin, Platform}`，**没有 Client**。核心目前只握着「账号类型所属插件」的句柄，还调不到「声明该平台的插件」。

---

## 3. 四条扩展

### 3.1 模型由插件提取

端点必须能拿到模型——核心要用它做分组白名单、价格、候选账号的 `models` 过滤和使用记录。声明式的两种取法覆盖不了「模型不在这次请求里」。

```protobuf
// PlatformService，由**声明该平台**的插件实现
rpc ResolveModel(ResolveModelRequest) returns (ResolveModelResponse);

message ResolveModelRequest {
  RequestMeta meta = 1;              // 含新增的 path_params / query
  map<string, string> fields = 2;    // 平台级 requestFields：Endpoint 上没有这个字段，
                                     // 且此时还没选账号，账号类型那层的覆盖不可用
  map<string, string> inbound_headers = 3;
}
message ResolveModelResponse {
  string model = 1;                  // 空 = 400 model is required
  // 只在端点没声明 request.stream 时生效。request.stream: true 是端点级静态
  // 事实，核心据它决定按 SSE 还是 JSON 读上游，不能被远程插件的一个 bool 推翻。
  bool stream = 2;
}
```

manifest：`request.modelSource: "plugin"`，与 `modelPath` / `modelParam` 三选一。

配套：
- `RequestMeta` 增加 `map<string,string> path_params`。**一处改动，五个已有调用点同时受益**：`BuildUpstreamRequest`、`ClassifyError`、`OnGatewayRequest`、`ResolveAffinityKey`、`RankAccounts`（`ResolveModel` 是 B 期才新增的第六个）。非法 UTF-8 净化见 CONTRACTS §25.1（个数和长度的上限已删除）——**净化不是可选项**，不做的话一个带 `%FF` 的 URL 就能稳定打出 500。
- `query` 走**声明式白名单**：端点声明 `request.queryParams: ["alt", "page"]`，核心只填声明过的，`auth.query` 自动排除。**不要**做成「整个 query 兜给插件 + 黑名单挡凭证」——黑名单永远在错的一侧，既误杀 `key_field` 这类无辜参数，又漏掉将来新发明的凭证参数名，而且与 `requestFields` 的既有契约不一致。A 期已把字段占位但恒不填，等 B 期补声明。
- `core.PlatformBinding` 补 `Client`（A 期已完成）。

**安装校验必须新增一条**：声明平台**不要求** `platform.adapter.v1`（只要 `gateway.endpoint` + `platform.register`，见 CONTRACTS §13），所以合法插件可以声明平台却没有 `Client`。任何端点用 `modelSource: "plugin"`（C 期同理 `usage.source: "plugin"`）的插件，**必须同时声明 `platform.adapter.v1`**，否则请求时拿到 nil 只能 500。

### 3.2 用量由插件返回，定价仍归核心

**声明式规则保留为默认**，不是被取代。SSE 场景下核心边转发边累计，不缓冲整个响应——这条契约（`common.proto` 开头）不能破。所以按端点选：

```jsonc
"usage": {
  "source": "rules",      // 默认：现状的 gjson 规则，零额外 RPC
  ...
}
"usage": {
  "source": "plugin",     // 插件返回
  "streamEvents": ["message_delta"],   // 流式时核心只留这些事件给插件
  "maxBytes": 262144
}
```

```protobuf
rpc ExtractUsage(ExtractUsageRequest) returns (UsageReport);

message ExtractUsageRequest {
  RequestMeta meta = 1;
  Account account = 2;
  int32 status = 3;
  map<string, string> headers = 4;
  // 非流式：整个响应体，受 maxBytes 限制，events 为空。
  bytes body = 5;
  // 流式：只有 streamEvents 匹配到的事件，按到达顺序；body 为空。
  repeated StreamEvent events = 6;
}

message StreamEvent {
  string name = 1;   // SSE event 名
  bytes data = 2;    // 该事件的 data，未拼接、未改写
}
```

> 早先的草案是 `bytes body` 和 `repeated string stream_events` 并列，流式时把匹配到的多个事件塞进同一个 `body`。那样**多个事件的 data 怎么分隔没有定义**，而且事件名列表和 body 里的内容对不上顺序。改成 `repeated StreamEvent`，一个事件一条，名字和数据绑在一起。

**流式的处理方式是这条扩展的关键**：核心不把整个 SSE 流交给插件，只把端点声明的那几个事件挑出来（有条数与字节上限）攒着，流结束后一次性交给插件。既让插件能算，又守住「热路径不搬运整个响应体」。

`ExtractUsage` 在**响应转发完之后**调用。但「转发完」不等于「客户端看到响应结束」——**这一点设计稿最初写错了**：响应没有 `Content-Length`（chunked / SSE），**终止分块要等 gin handler 返回**，所以把调用留在 handler 里，客户端就会实打实地多等一个插件往返（实测插件 sleep 150ms → 客户端等 151.6ms）。

正确做法：`forward()` 末尾只**装配**（把需要的东西从 gin context 里拷出来，那个 context 是池化的），真正的调用放进结算提交阶段的 goroutine，handler 立刻返回。**这样才是对客户端延迟零影响**，要有测试用「客户端耗时 < 插件 sleep」钉住。

连带必须一起挪的：TPM/TPD 的 token 计数原本紧跟 `forward()`，会用插件还没上报的（往往是 0 的）数字去记限流。plugin-usage 的端点必须等异步完成后才计。粘性绑定与 last-used 则仍用插件之前的成功状态——那是调度关注点，一个已交付的 200 在那里算成功更合理。

定价这一步完全不变：核心拿 `UsageReport` 里的 token 与 facts，跑管理员配置的价格表达式，乘分组倍率，写账本。插件报的是用量，不是钱。

### 3.3 使用记录实体由插件补字段，核心补齐并写入

```protobuf
message UsageReport {
  UsageTokens tokens = 1;
  map<string, string> facts = 2;        // u("key")
  string upstream_model = 3;
  string error_type = 4;                // 插件比核心更懂上游错误
  string error_message = 5;
  string detail_json = 6;               // 插件自定义的一小块，写进 usage_logs.plugin_detail；原设计上限 4 KiB，现不限（CONTRACTS §25.3）
  Reservation reserve = 7;              // 见 3.4，不预扣时留空
}
```

**核心无条件补齐并覆盖的字段**（插件填了也不算数）：

```
request_id · user_id · api_key_id · group_id · account_id
plugin_key · plugin_version · platform · protocol · endpoint
status_code · success · attempts · latency_ms · first_token_ms
client_ip · user_agent · node_id · created_at
rate_multiplier · price_id · expr_hash · billing_mode · matched_tier
billing_detail · total_cost · billing_status
```

这条边界必须写死在实现里，否则插件能伪造归属与费用。**插件能填的只有「上游事实」那一层**：用量、上游模型、错误解释、自定义细节。

数据：`usage_logs` 增加 `plugin_detail jsonb NOT NULL DEFAULT '{}'`。

### 3.4 异步：插件预扣费 + 核心驱动核对轮询

> 「异步任务：插件支持预扣费、异步核对逻辑；核心配合进行轮询调度、真实扣费。」

#### 提交阶段（在同步请求里完成）

插件在 `ExtractUsage` 的返回里带上预扣：

```protobuf
message Reservation {
  string ref_id = 1;                 // 插件侧的业务 id（任务 id 等）
  UsageTokens tokens = 2;            // 预估用量
  map<string, string> facts = 3;
  int32 next_check_after_sec = 4;
  // 上游事实：这个条目最晚核对到什么时候（如 Ark 视频任务只保留 7 天可查）。
  // 核心用设置 max_reconcile_age_sec 夹住，0 = 用核心默认。
  int32 deadline_sec = 5;
}
```

核心的动作（全部是已有机制的复用）：

1. 按价格表算出**预估费用**
2. 正常写 `usage_logs`，`billing_status = 'reserved'`
3. 正常扣款：`balance_ledger` kind=`usage`，幂等键 `usage:{request_id}`
4. 在新表 `pending_settlements` 登记 `{plugin_key, ref_id, usage_log_id, account_id, attempts, next_check_at}`

预扣的意义：用户不能用 $0 余额提交 1000 个视频任务。这是现在的「请求前只查余额、允许短暂透支」补不上的洞。

#### 核对阶段（核心驱动）

核心跑一个**通用核对循环**——它不认识「任务」，只认识「有条目待核对」：多节点只跑一份（抢锁）、指数退避、次数与时限上限、失败告警。

每一轮，核心对到期的条目做两步，**与 `BuildTestRequest` / `BuildModelsRequest` 完全同一个模式**：

```protobuf
rpc BuildReconcileRequest(BuildReconcileRequestRequest) returns (BuildReconcileRequestResponse);
// → { method, url, headers, body_json }；核心带着账号代理、SSRF 防护、超时去发

rpc ParseReconcileResponse(ParseReconcileResponseRequest) returns (ReconcileResult);
message ReconcileResult {
  enum State { PENDING = 0; SETTLED = 1; FAILED = 2; }
  State state = 1;
  int32 next_check_after_sec = 2;    // PENDING 时
  UsageTokens tokens = 3;            // SETTLED 时的真实用量
  map<string, string> facts = 4;
  string reason = 5;                 // FAILED 时
}
```

核心据此落账：

| 插件返回 | 核心做什么 |
|---|---|
| `PENDING` | 按 `next_check_after_sec` 与退避策略重排，`attempts++` |
| `SETTLED` | 用真实用量重算费用；与预扣差额补扣（kind=`usage`）或退回（kind=`refund`）；`usage_logs` 改 `billed`、写回真实用量与费用；发 `usage.recorded` |
| `FAILED` | 预扣全额退回（kind=`refund`）；`usage_logs` 记 `error_type` 与 `billing_status='free'` |
| 超过上限 | **保留预扣金额**，见下 |

#### 核对超时：保留预扣（2026-09-29 决定）

核对超过次数或时限上限时，**预扣金额就是最终费用，不退**。理由是这笔调用已经真实发往上游、上游侧已经产生成本，核对不上通常是上游异常或任务被上游丢弃，让平台承担更不合理。

落地上有一处必须钉死：

```sql
-- 已存在的重试索引，结算重试循环靠它捞行
CREATE INDEX usage_logs_billing_pending_idx ON usage_logs (id)
  WHERE billing_status IN ('pending', 'failed');
```

放弃核对时**不能**把 `billing_status` 写成 `failed`——钱已经扣过了，写 `failed` 会被这条索引再捞一次，导致**重复扣费**。正确做法：

| 字段 | 值 |
|---|---|
| `usage_logs.billing_status` | `billed`（预扣即最终费用，不再进重试队列） |
| `usage_logs.input/output_tokens`、`metrics` | 保持预估值不变 |
| `usage_logs.billing_detail` | 追加 `{"reconcile": "abandoned", "attempts": N, "last_error": "..."}` |
| `pending_settlements.state` | `abandoned` |
| 告警 | 按放弃条数告警；控制台使用记录详情里标出「按预估结算，未核对」 |

**期限由插件说、由核心定**（与本文的总原则一致）：`Reservation` 可带 `deadline_sec`，插件按上游事实给（如 Ark 视频任务只保留 7 天可查），核心用全局设置 `max_reconcile_age_sec` 夹住，默认 24 小时；退避默认 `10s → 30s → 1m → 5m → 15m`，之后固定 15m。

管理员需要一条人工出口：使用记录详情里对 `abandoned` 的条目提供「重新核对」与「退款」两个动作（退款走 kind=`refund`，幂等键 `refund:{request_id}`）。自动策略偏向平台，人工兜底偏向用户。

**这里有一个很关键的收益**：核对是核心发起的，所以核心可以像 `BuildUpstreamRequest` 一样把 `Account`（含凭证）传给插件——授权模型完全不变（`accounts.credentials` scope `own`）。于是：

- 插件**不需要** `net` 权限（请求由核心代发，走账号代理与 SSRF 防护）
- 插件**不需要**列账号的接口（核心把账号给它）
- 插件**不需要**自己起 `app.jobs.v1` 定时任务（核心驱动节奏）

上一版设计里让插件用后台任务 + `HostService.RecordUsage` 自己上报的方案**作废**——那条路要给插件开放「写账本」和「自己联网」两项能力，而这条路一项都不用开。

---

## 4. 永远不交给插件

- **定价、金额、倍率**：插件只报用量
- **归属**：user_id / api_key_id / group_id / account_id 由核心填
- **直接写** `usage_logs` 或 `balance_ledger`（`ledger.credit` / `ledger.debit` 仍然存在，但那是给插件自己的业务用的，如充值卡，**不是**网关计费通道）
- **最终调度权**：候选集筛选、并发槽位、限流、冷却仍全部由核心把关（CONTRACTS §24 已定）

---

## 5. 对延迟的影响

| RPC | 时机 | 客户端延迟 |
|---|---|---|
| `ResolveModel` | 调度之前 | **有**。只对声明 `modelSource: "plugin"` 的端点；需超时（建议沿用钩子默认 300ms）与失败即 400 |
| `ExtractUsage` | 结算提交阶段的 goroutine 里 | **无**——但前提是**不能**留在 gin handler 里调。chunked/SSE 的终止分块要等 handler 返回，留在里面客户端就会实打实多等一个插件往返 |
| `BuildReconcileRequest` / `ParseReconcileResponse` | 离线循环 | **无** |

四条扩展里只有一条在客户端延迟路径上，而且是可选的。

---

## 6. 数据与迁移

```sql
-- 0012_plugin_usage_reporting.sql（C 期）
ALTER TABLE usage_logs ADD COLUMN plugin_detail jsonb NOT NULL DEFAULT '{}';
-- billing_status 增加取值 'reserved'（D 期）

-- 0013_pending_settlements.sql（D 期）
CREATE TABLE pending_settlements (
  id             bigserial PRIMARY KEY,
  plugin_key     varchar(30)  NOT NULL,
  ref_id         varchar(200) NOT NULL,
  usage_log_id   bigint       NOT NULL REFERENCES usage_logs(id),
  account_id     bigint,
  attempts       int          NOT NULL DEFAULT 0,
  next_check_at  timestamptz  NOT NULL,
  deadline_at    timestamptz  NOT NULL,                     -- 插件给、核心夹住
  state          varchar(20)  NOT NULL DEFAULT 'pending',   -- pending | settled | failed | abandoned
  last_error     text         NOT NULL DEFAULT '',
  created_at     timestamptz  NOT NULL DEFAULT now(),
  UNIQUE (plugin_key, ref_id)
);
CREATE INDEX pending_settlements_due_idx ON pending_settlements (next_check_at) WHERE state = 'pending';
```

`balance_ledger` 不动：`kind` 已经有 `usage` 和 `refund`，`idempotency_key` 已经是唯一键。

设置项（`settings` 表）：`max_reconcile_age_sec`（默认 86400）、`reconcile_backoff`（默认 `10s,30s,1m,5m,15m`）。

---

## 7. 分期

| 期 | 内容 | 谁受益 |
|---|---|---|
| **A** ✅ | `RequestMeta.path_params`（`query` 占位不填）；`PlatformBinding.Client` | 五个已有调用点一起 |
| **B** ✅ | `ResolveModel` + `request.modelSource`；`request.queryParams` 声明式白名单；「用 plugin 源必须声明 `platform.adapter.v1`」校验；facts 键格式约束 | 模型不在请求里的端点 |
| **C** ✅ | `ExtractUsage` + `usage.source`（D 期挪到 Endpoint）+ `streamEvents`；`UsageReport`；`usage_logs.plugin_detail`；热路径超时独立设置 | 非标准用量、插件补日志字段 |
| **D** ✅ | `Reservation` + `pending_settlements` + 核对循环 + `BuildReconcileRequest` / `ParseReconcileResponse`；`usage.source` 挪到 Endpoint；`usage_logs.anomalies` | 异步任务、预扣费、延迟结算 |
| **E** ✅ | 第一个用满四期的插件暴露的三处缺口：`Endpoint.usageRequestFields` + `fields_omitted`；`ReconcileResult.SETTLED_ESTIMATE`；`billing:"free"` + plugin 源改硬错误 + 运行时 `dropReservation` 告警。附 `/usage/summary` 三列、`max_reconcile_age_sec` 默认 7 天、`readBody` 拒非法 UTF-8 | 异步提交类端点能精确预估、上游不给用量时不白送 |

**四期核心契约全部落地，E 期收尾也已落地**（CONTRACTS §25.1–§25.6）。§25.5 提的三条「建议」里有两条方向写错了，实现时都改了，更正见 §25.6。

A 是纯增量，B / C 互不依赖，D 依赖 C（预扣搭在 `UsageReport` 上）。

火山方舟插件的一~三期（对话 / 图片 / 素材库）**不依赖其中任何一条**，可以先发；视频那期依赖 A + B + D。

---

## 8. 待确认

1. **`ExtractUsage` 的流式取舍**：按事件名挑（本稿方案）足够吗？还是需要「最后 N 个事件」这种兜底？
2. ~~预扣兜底策略~~ **已定（2026-09-29）：核对超时保留预扣**，见 §3.4。
3. **`ResolveModel` 失败时**：400 拒绝（本稿）还是退回端点声明的 `modelPath`？倾向 400——模型取不到就没法定价和限额，放行等于免费。
4. **`plugin_detail` 的大小与可见性**：建议上限 4 KiB，控制台使用记录详情里展示，不进列表接口。
