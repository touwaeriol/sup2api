# 上游插件 New API、Sub2API、TypeSafe，账号余额接通，OpenAI 生图（设计稿）

> 2026-11-19 修订（所有者决定）：`missing_price_policy` 删除，没有价格的模型一律拒绝（403 `model_price_not_configured`），没有免费放行；下文提到它的地方以 CONTRACTS §17.3 为准。同日另一决定：账号测试不查价格、不计费，下文“账号测试”的价格检查已撤掉（CONTRACTS §17.2）。

2026-11-18，所有者要求（语音转写整理）：账号余额功能接通（参考 new-api 的渠道余额查询；Codex 显示周限制之类的用量窗口，能查余额的查余额，查不了的先不做）；新增上游插件 **New API**（站点的 API Key，能查余额，同协议自动转发 OpenAI Chat / Responses / 生图、Gemini、Anthropic Messages，并能转发 TypeSafe 的 Jev 模型）、**Sub2API**（"我们的旧版本"，能查余额，自动转发，可以经协议绑定指定模型，支持 OpenAI、Gemini、Anthropic Messages）、**TypeSafe**（直接接入 TypeSafe，支持它的协议和调用）。所有者的工作方式是"先按自己的理解做，做完一起汇报"，所以本文对每个取舍都给出决定，替他做的决定集中在第 4 节。

本文只是设计与任务拆分，**没有实现**。事实来源：两份只读调查摘要（余额、三个上游插件）、外部公开资料摘要（TypeSafe 官方文档与 OpenAPI、OpenAI Images reference、New API 文档，抓取于 2026-11-18）和本文写作时对 develop `1fbe32aa9` 代码的逐处核对；摘要与代码不一致的地方以代码为准，列在 2.5。new-api 仓库是 AGPL，本文只描述它的行为和接口形状，实现时不得抄它的代码。

契约改动（实现时同步文档）：`sdk/proto`（余额消息、测试请求的协议）、`sdk/manifest`（`balance` 去掉 `currency`；两个图片用量字段）、`server/internal/core`（`PlatformPlugin` 加余额两个方法、`UsageTokens` 加两个字段）、核心迁移 0112、0113、`sdk/platforms/openai.json`（两个图片端点）；CONTRACTS 重写 §52，新增 §118（OpenAI 生图）、§119（三个上游插件），改 §19、§112.13、§116.3 和内置插件表。

## 0. 结论

1. **余额照"额度主动查询"的路接通**：插件描述请求 → 核心经账号代理发出 → 插件解析。删掉核心里没人注册的 `BalanceProvider` 注册表；SDK 加可选接口 `pluginsdk.BalanceReader` 和 `standard.Type.Balance`。proto 要改：`BalanceResult` 加 `unlimited`（"不限"），一次余额查询允许多个请求（New API 要先后读 `/api/usage/token/` 和 `/api/status`）；这两个 RPC 从来没有任何调用方和实现方，改形状不影响任何插件。
2. 余额请求的每个目标先过 §112.17 的主机规则和管理员的插件联网（`core.CheckPluginUpstream`，来自正在开发的 `feat/plugin-domain-policy-core`），再经 `sendPluginCall`（SSRF、账号代理），**不跟随重定向**。
3. 手动刷新 + 定时刷新（集群任务 `core.account.balance_sweep`，按清单 `updateInterval`）。**只显示，不联动**：不停用、不影响调度、不告警。
4. 这一版能查余额的只有 New API、Sub2API 两个新插件（都折成 USD）；TypeSafe 没有余额接口，不声明。Codex 的 5 小时 / 7 天窗口**已经显示**在账号列表"余额"列里（响应头被动采样），缺口只是"没发过请求的账号没有数据、刷新按钮对它无效"，补法是可选的包 P8。
5. SDK 加**路由级鉴权与失败分类**和 `standard.Merge`（把几家预设合成一个多平台类型），New API、Sub2API 各自用它，各协议用官方原生鉴权头；TypeSafe 的预设与平台定义放进 `sdk/pluginsdk/providers/typesafe`，TypeSafe 插件和 New API 插件共用。
6. 核心加 `openai.images`（`POST /v1/images/generations`）和 `openai.images_edit`（`POST /v1/images/edits`，**只收 JSON 形式**，multipart 照现有网关回 400），用量字段 `image_input_tokens` / `image_output_tokens` 接到早已存在却恒为 0 的表达式变量 `img` / `img_o`（迁移 0112）。
7. TypeSafe 插件自带平台 `typesafe`、端点 `POST /v1/systemone`、协议 `typesafe.systemone`，默认官方地址 `https://api.typesafe.ai`，模型列表 `GET /v1/models`；用量由核心按应答 `usage` 读，不用 `usageSource:"plugin"`，所以 New API 的账号也能服务它。
8. 三个插件都是"内置、仅安装、默认不启用"，都不需要 `net` 权限。
9. 9 个工作包，最多 4 个并行：P1 契约 → {P2 SDK、P3 核心余额、P4 核心生图、P8 codex} → {P5 New API、P6 Sub2API、P7 TypeSafe} → P9 集成与 e2e。
10. 发布：进尚未发布的 0.1.304 批次（迁移从 0112 起接在 0111 后面）；若 0.1.304 先发了，见第 8 节。

## 1. 目标与范围

**这一版做**

| 块 | 内容 |
|---|---|
| 余额 | 核心、SDK、控制台接通；定时刷新；修掉调查列出的小问题；重写 CONTRACTS §52 |
| Codex | 核对用量窗口显示（已满足）；可选包 P8 给它加主动查询 |
| SDK | 路由级鉴权、路由级失败分类、`standard.Merge`、按协议的测试请求、余额辅助、TypeSafe 预设 |
| 核心生图 | `openai.images`、`openai.images_edit`（JSON）、`img` / `img_o` 的数据来源、迁移 0112 |
| 插件 | `newapi`、`sub2api`、`typesafe` 三个新插件 |
| 测试 | 单元、合规套件、黄金样例回放、e2e（AC30 三行、AC39 余额、AC40 生图）、mock 上游补接口 |

**以后可以做**（这一版不做，各自的理由）

| 项 | 为什么以后 |
|---|---|
| 余额联动：余额为 0 或低于阈值时告警、停用或降低调度权重（new-api 的批量刷新会自动停用余额 ≤ 0 的渠道） | 所有者的硬规矩：不自作主张加限制；联动要所有者另定规则 |
| 其它账号类型的余额（见 3.1.9 的可行性表） | 官方 API Key 类没有可用接口；volcengine、relay 需要额外设计 |
| `/v1/images/edits` 的 multipart 形式 | 网关只收 JSON（§112.16 保留的唯一请求体规则），收 multipart 要改网关读请求体、取模型、审核取提示词 |
| Responses 里 `image_generation` 工具的生图部分另计（旧设计稿 3.3） | 要扩展 `AdditionalUsageRule`（对象快照、模型从请求取），是单独的契约改动。**不做**：所有者定为一条请求一个计价模型，生图工具的 token 计入本条请求，见 3.8 |
| codex 设备码账号能否生图的实测（旧设计稿 3.4） | 需要真实账号。**已实测**（2026-11-18，可以，PLUGIN-CODEX 第 12 节） |
| 价格编辑器的结构化分档里加图片价、价格同步源导入图片 token 价、统计页（`usage_hourly`）汇总图片 token | 这一版管理员用表达式价格即可计对，见 3.4.5 |
| 插件自报用量（`pluginv1.UsageTokens`）里的图片字段 | 没有插件自己解析生图用量 |
| TypeSafe `/v1/systemone` 的 multipart 形式（JSON 请求放在名为 `request` 的 part、图片各一个 part） | 同 edits，网关只收 JSON；JSON 形式用 `data_base64` 带图，功能等价 |
| 网关自己的错误用 TypeSafe 的 `{"detail": …}` 形状（新的 `errorFormat`） | 要改核心的错误格式表；上游的错误本来就原样透传，见 3.5.3 |
| 审核插件（moderation）的 TypeSafe 渠道改为用 TypeSafe 插件的账号 | 那是插件内部的调用，与网关转发无关，所有者没提 |
| New API、Sub2API 的更多协议（`/v1/completions`、音频、rerank、视频、异步生图等） | 所有者说"后面再考虑" |
| 账号测试对话框里手动选测试协议 | 这一版按账号的协议绑定自动选（3.3.3），够用 |

## 2. 现状

### 2.1 余额

- proto 里 `BuildBalanceRequest` / `ParseBalanceResponse`（`sdk/proto/sub2api/plugin/v1/platform.proto:118-130`，消息 728-781）早就有，但**三层都没接**：`core.PlatformPlugin`（`server/internal/core/ports_plugin.go:34`）没有这两个方法，`plugin/grpcruntime/adapters.go` 没有适配，SDK 的 `platformServer`（`sdk/pluginsdk/serve.go:438` 起）不转发，也没有对应的可选接口。
- 核心唯一的数据来源是按账号类型注册的 `BalanceProvider`（`server/internal/account/balance.go:69-94`），全仓库没有注册者（只有 `balance_provider_test.go`）。所以没有任何账号查得到余额。
- 还在、基本能用：清单 `accountTypes[].balance{currency, updateInterval}`（`sdk/manifest/manifest.go:424-435`，校验 `check/balance.go`：`currency` 必填 ISO 4217，`updateInterval` 60–86400，默认 180）；表 `account_balances`（迁移 0034：`amount_micros`、`currency`、`source`、`updated_at` 都 NOT NULL，`error`、`error_at`）；Redis 缓存 `balance_cache.go`；`GET /accounts/:id/balance[?force=true]`；账号列表 / 详情的 `balance`；控制台 `web/src/views/accounts/AccountBalanceCell.vue` 和"刷新"按钮，与额度窗口同在账号列表的"余额"列（`accounts.balance.column`）。
- 已知问题（核对属实）：首次查询失败且没有旧快照时写入 `updated_at = NULL`，违反 NOT NULL，错误被 `_ =` 吞掉；`updateInterval` 核心从不读，没有定时任务；没有 singleflight；改凭证不失效余额；前端 `AccountBalance`（`web/src/api/types.ts:795`）没有 `supported`、`amount` 必填、多一个 `source`，和后端 `{supported:true}` 对不上；`balanceQueryFloor`、`check/balance.go` 的 `defaultBalanceUpdateInterval`、`store.BalanceActive` 没人用；代码注释写 §51，实际是 §52；CONTRACTS §52 多处与代码不符（消息字段、错误码含义、迁移路径、表定义、并不存在的 `POST /refresh-balance`）。
- 参照：额度主动查询（§44）`account/quota.go` 的 `accountQuota` → `runQuotaQuery` → 插件 `BuildQuotaRequest` → `sendPluginCall`（URL 校验、`guardUpstream`、`Proxies.HTTPClient(proxyID)`、读整份应答）→ `ParseQuotaResponse`；`quotaFlight` singleflight、PG 租约、节点任务 `QuotaSweepTask`；适配器走 `classConsole`。

### 2.2 多平台账号类型

- 账号类型的 `platforms[]` 列多个平台，这些平台的协议都是"本平台协议"，默认全部开放、原样转发；跨平台转换默认关闭（§110）。现成的多平台例子是 volcengine（openai、anthropic、自己的 volcengine），不是 relay（relay 只服务 anthropic）。
- `sdk/pluginsdk/standard`：`Type.Routes` 按协议 id 配路由，但 `Auth`、`Headers`、`Classify`、`Test`、`Models` 都只有类型一级（`standard.go:27-84`）。三份预设 `providers/{openai,anthropic,gemini}/preset.go` 分别是 Bearer、`x-api-key` + 默认 `anthropic-version`、`x-goog-api-key`。所以"一个类型、各协议各用官方鉴权头"现在写不出来。
- 一个插件的账号类型可以引用别的插件声明的平台（`check/account_check.go`、`registry.go:585-600`），对方没装时注册表每次换代记一条 WARN、类型服务不了那个平台；被引用的端点若是 `usageSource:"plugin"` 或任务提交则报 `foreign_facts`（§112.8）。
- 平台插件的 `hostCompat` 下限必须**等于** `protocol.FirstCoreVersion`（`0.1.304`，`tools/sub2api-plugin/manifest_test.go` 的 `TestPluginsStartAtTheFirstCoreOfTheProtocol`）。

### 2.3 生图

- 内置 `openai` 平台只有 `chat_completions`、`responses`、`responses_websocket`、`embeddings`（`sdk/platforms/openai.json`），`POST /v1/images/generations` 现在 404。
- 表达式变量 `img`、`img_o` **已经存在**：`billing/expr/expr.go:38` 的 `TokenVars`、`eval.go` 的 `env` / `Vars` 都有，表达式里写它们校验能过；但 `expr.Tokens`、`Normalize`、`core.UsageTokens`、用量规则、`usage_logs` 都没有图片字段，**它们恒为 0**。控制台价格页（`web/src/views/prices/priceExpr.ts` 的 `TOKEN_VARS`、试算面板）不认识它们。
- 价格同步：LiteLLM 源只导入 chat、completion、responses、embedding 模式（§17.1）；旧设计稿实测 models.dev 源给 `gpt-image-1.5` 的是只有文字价的 `per_token`。

### 2.4 TypeSafe

- 公开接口只有两个（官方 OpenAPI 0.2.0）：`POST /v1/systemone`、`GET /v1/models`（`{"models":[{"name","description","release_date"}]}`）；官方地址 `https://api.typesafe.ai`；只认 `Authorization: Bearer`；没有余额 / 用量查询接口，用量只在每次应答的 `usage{input_tokens,output_tokens}` 里；非流式。
- systemone 请求 JSON 顶层 `model`、`state`、`questions`（`noul` / `choice` / `score` / `box` 四种，都只是请求体内容）、`assets`（可选，`data_base64` 或 multipart 的 `part`）；两个可选请求头 `TypeSafe-Danger-Zone-Images`、`TypeSafe-Danger-Zone-Long-Context`（值只能是 `true`）。
- 错误：400（`{"detail": string|object}`，超上下文带 `max_tokens_exceeded`）、401、413、415、422（FastAPI 风格 `detail` 数组）、429、529。
- 模型：`jev-1.14.0`、`jev-1.13.0`，别名 `jev-latest`、`jev-preview` 都指向 1.14.0。价格只收输入：$0.042 / 百万 token，输出免费，图片折成输入 token。
- 仓库里：moderation 插件经自己的出口直接调 systemone（`plugins/moderation/internal/moderation/engine.go:482` 起），mock 上游已有 `POST /v1/systemone`。网关没有任何 TypeSafe 端点。

### 2.5 与调查摘要、旧设计稿、给定方向不一致的地方

| 出处 | 原说法 | 核对结果与处理 |
|---|---|---|
| 生图旧设计稿 §1 | `img`、`img_o` 只是文档预留，未实现 | 变量已在 `expr` 里（2.3），缺的是数据来源。改动面因此小一截：不用碰 `expr` 的变量表 |
| 生图旧设计稿 §3.2 | openai 插件的类型默认支持全部端点，加端点后"自动可用" | 转发 V2 之后不成立：调度会把图片请求交给没有 `endpoints` 子集的类型，但插件没有这个协议的路由时 `BuildUpstream` 报 `unsupported upstream protocol`。要给 openai 预设加路由；volcengine 的 `apikey`、`relay` 两个类型在 openai 平台上没写子集，必须补子集（它的 `manifest_test.go:379-385` 也会因为内置协议多了而失败）。azure、codex 写了子集，不受影响 |
| 生图旧设计稿 §3.2 | `images_edits` 是 multipart，要等网关支持 | 官方 reference：edits 也接受 JSON（`images[{image_url\|file_id}]`）。JSON 形式只是多一个端点，纳入本版；multipart 请求在网关第一步就回 400 `request body is not valid JSON`（`gateway/pipeline.go` 的 `checkBodyJSON`），不调度、不计费 |
| 生图旧设计稿 §3.2 | 流式读 `image_generation.completed` 的 `usage`，图片输出取 `output_tokens_details.image_tokens` | 官方文档里流式 completed 事件的 usage 没列 `output_tokens_details`；用量规则按 JSON / SSE 分开写，见 3.4.3 |
| 余额摘要 | 余额路径加 `UpstreamHostAllowed` 和域名策略即可 | 还要**不跟随重定向**：`sendPluginCall` 默认跟随，3xx 能把带 Key 的请求带到主机规则之外（`SendAccountRequest` 已经是 `noRedirects`） |
| 余额摘要 | 最小接通方案"proto 不用改" | 要改：无限额度没有表示方式；New API 一次余额要两个请求。两个 RPC 从未被实现或调用，见 3.1.2 |
| 给定方向 | TypeSafe `base_url` 必填 | 有官方地址后照 openai/anthropic/gemini 插件：默认官方地址、`externalServices`、`guardedSettings` 限定（3.5.3） |
| 给定方向 | 照 `QuotaSweepTask` 做定时 | 照它的注册方式，但用**集群任务**而不是节点任务 + PG 租约：余额只读、只显示，重复一次 GET 没有代价，见 3.1.5 |
| CONTRACTS §113.2 | 插件联网"只管出口隧道" | `feat/plugin-domain-policy-core`（工作区里尚未提交）正在把它扩到所有核心代发路径，加 `core.PluginNetworkAccess`、`core.CheckPluginUpstream`、`CheckPluginNetwork`，`guardUpstream` 带插件 key。本文按这些名字写，以该分支合并后的名字为准 |
| 插件摘要 | hostCompat 下限必须等于 0.1.304 | 属实；0.1.304 还在"未发布"（CHANGELOG），这决定了发布方式（第 8 节） |

## 3. 设计

### 3.1 余额接通

> 2026-11-18 注：所有者改为不做任何换算（推翻 D3、D6）：余额是上游的数字本身，单位是上游给的原文、没给就没有，核心不校验单位格式，New API 不再读 `/api/status`，SDK 的 `USDMicros` 改为 `Micros`。现行规则见 CONTRACTS §52；本节以下是原设计。

#### 3.1.1 数据流

```
控制台 GET /accounts/:id/balance[?force]  或  集群任务 balance_sweep
  → account.queryBalance（singleflight，按账号）
     → 插件 BuildBalanceRequest(account 含凭证)   → requests[1..n]
     → 每个 request：core.CheckPluginUpstream（清单主机规则 + 管理员插件联网）  ← 任一被拒：都不发，记错误
     → 逐个 sendPluginCall（plugin key、noRedirects、账号代理、SSRF）            → answers[1..n]
     → 插件 ParseBalanceResponse(account, answers) → BalanceResult
  → 归一化 → account_balances upsert → Redis 写穿 → 视图
```

#### 3.1.2 proto（`platform.proto`）

两个 RPC 的签名不变，消息改形状（没有调用方也没有实现方，旧字段号 `reserved`，不留兼容）：

```proto
message BuildBalanceRequestResponse {
  reserved 1 to 4; reserved "method", "url", "headers", "body_json";
  // Sent in this order, each like a quota request; at least one.
  repeated BalanceRequest requests = 5;
}
message BalanceRequest { string method = 1; string url = 2; map<string,string> headers = 3; string body_json = 4; }

message ParseBalanceResponseRequest {
  Account account = 1;
  reserved 2 to 6; reserved "status", "headers", "body", "transport_error", "truncated";
  repeated BalanceAnswer answers = 7;   // one per request, same order
}
message BalanceAnswer { int32 status = 1; map<string,string> headers = 2; bytes body = 3; string transport_error = 4; }

message BalanceResult {
  ... 1-4 不变（amount_micros、currency、error_type、error_message）
  // The account has no limit at this level ("unlimited"): amount_micros
  // and currency are ignored.
  bool unlimited = 5;
}
```

- `currency` 的注释改为"成功且非 `unlimited` 时必填，ISO 4217"，不再"必须与清单一致"。
- 同批还有 `BuildTestRequestRequest.protocols`（3.3.3）。
- 只加字段、改从未使用的消息，按仓库先例**不升 Host API**（仍是 10），插件线协议仍是 2。

#### 3.1.3 清单（`sdk/manifest`）

- `AccountBalance` 删掉 `Currency`，只剩 `UpdateInterval`（秒，60–86400，缺省 180）。声明支持就写 `"balance": {}` 或带 `updateInterval`。币种由插件按账号在结果里给（中转站可能是美元、人民币）。
- `check/balance.go` 删掉币种校验和没人用的 `defaultBalanceUpdateInterval`（缺省值只在核心一处定义）。
- 现有清单里没有任何类型声明 `balance`（claude-oauth 已删），删字段不影响任何插件；`sub2api-plugin` 严格解码时还写着 `currency` 的第三方清单会报 unknown field，这是想要的。

#### 3.1.4 核心（`server/internal/account`、`core`、`grpcruntime`）

- `core.PlatformPlugin` 加 `BuildBalanceRequest`、`ParseBalanceResponse`（注释照 quota 那两条：可选，没实现回 Unimplemented）；`grpcruntime/adapters.go` 用 `classConsole`、`TimeoutPlatformConsole`（与额度相同）；测试替身同步。
- `balance.go` 重写：删 `BalanceProvider`、`RegisterBalanceProvider`、`getBalanceProvider`、`balance_provider_test.go`、`balanceQueryFloor`；`accountBalance` 改为调插件。整次查询的时限沿用文件里已有的 `balanceQueryTimeout`（20 秒，与 `quotaQueryTimeout` 同值），**不新增时限**。
- 主机检查（"两道"在这里）：拿到 `requests` 后、发出第一个之前，对**每一个** URL 调 `core.CheckPluginUpstream(ctx, s.d.NetworkAccess, bt.Plugin.Key, bt.Plugin.Manifest, url, a.Type, settings, plainCreds)`：第一道是 §112.17 的 `UpstreamHostAllowed`（`externalServices` 或账号 `upstreamFields` 里的地址），第二道是管理员的插件联网。任一被拒，一个都不发，快照记 `transient: <拒绝信息>`，不调 `ParseBalanceResponse`。通过后逐个 `sendPluginCall(pluginCall{plugin: key, what: "balance", noRedirects: true, …}, a.ProxyID)`；`guardUpstream` 里再过一次插件联网（缓存判断，与 `SendPluginAccountRequest` 同样的写法）和 SSRF。
- 归一化（`normalizedBalance`）：`error_type` 非 0 → 错误快照，信息用已有的 `quotaErrorDetail` 清洗；成功时 `unlimited` 为真则忽略金额；否则 `currency` 必须 `^[A-Z]{3}$`，不满足按插件错误记 `transient`。Unimplemented → 视为不支持，不写库。
- `AUTH_REJECTED` 只记在快照上，不停用、不冷却账号（proto 原有语义，CONTRACTS 旧文写错了）。
- 首次失败：迁移 0113 让 `updated_at` 可空，错误快照的 `updated_at` 留空、`amount_micros` 0、`currency` 空串；视图在 `updated_at` 为空时不给金额。写库失败不再吞掉：返回 500。
- 凭证、设置或代理变更：`handlers.go` 的 `update` 里已有 `if p != nil || extensionSettings != nil || setProxy { s.quota.forget(id); s.evictQuotaCache(...) }`，同一处在事务里删 `account_balances` 行，提交后删 Redis 键。下一轮定时任务把它当作"从未查过"重新查。
- 视图 `BalanceView`：`{supported:true, amount?, currency?, unlimited?, updated_at?, error?, error_at?}`（去掉不存在的 `source`）。不支持的类型没有 `balance` 字段；`GET /balance` 对它们照旧 404 `unsupported`。

迁移 `0113_account_balance_unlimited.sql`：

```sql
ALTER TABLE account_balances ALTER COLUMN updated_at DROP NOT NULL;
ALTER TABLE account_balances ADD COLUMN IF NOT EXISTS unlimited boolean NOT NULL DEFAULT false;
ALTER TABLE account_balances DROP COLUMN IF EXISTS source;   -- 只有 "active" 一种取值，没人读
```

表在所有部署上都是空的（从来没有查询器），迁移瞬间完成。之后 `go run ./cmd/gen-models`，`store/account_balance.go` 的列表和 `balanceRow` 同步。

#### 3.1.5 定时刷新、缓存与并发

- **用 `updateInterval`**（不删）：快照"到期" = 距上次查询（`updated_at` 与 `error_at` 的较晚者）超过类型的间隔；没有快照也算到期。它同时取代写死的 `balanceFresh`（3 分钟）和 `balanceErrorTTL`（1 分钟）：成功、失败都按同一间隔，手动 `force` 不受它约束。非强制的 `GET /balance` 也按到期判断。
- 集群任务 `core.account.balance_sweep`（`account/tasks.go` 加一项 `core.TaskSpec`，`Scope: core.TaskCluster`，`Every: time.Minute`，`Timeout` 用任务框架必填的值，与 `CredentialRefreshTask` 同为 15 分钟）：对每个声明 `balance` 且插件已注册的类型，按间隔选到期账号（未删除，且可调度或从未查过——与额度清扫 `dueQuotaIDs` 的选法相同，停用账号只在管理员手动刷新时查），按上次查询时间排序，页大小复用 `quotaSweepPage`，逐个查询（不加并发数常量）。没跑完的下一轮按到期接着跑。
- 并发：节点内 `balanceFlight singleflight.Group` 按账号合并手动刷新与定时任务；**不加 PG 租约**：集群任务同一时刻只在一个节点跑，跨节点的重复只可能是"某个节点上的手动刷新 + 任务"，代价是对上游多一次只读 GET，不值得一张租约。
- Redis 缓存（`balance_cache.go`）保留：每次写库后写穿；删除快照时删键。缓存条目带上 `unlimited`。

#### 3.1.6 控制台各状态

`AccountBalance` 类型改为 `{supported: boolean; amount?: Money; currency?: string; unlimited?: boolean; updated_at?: string; error?: string; error_at?: string}`，mock 同步。`AccountBalanceCell.vue`：

| 状态 | 判断 | 显示 |
|---|---|---|
| 不支持 | 没有 `balance` 字段 | 不出现这个单元格（与现在相同） |
| 支持、未查 | `supported` 且没有 `updated_at`、没有 `error` | "未查询" + "刷新" |
| 有余额 | 有 `updated_at`，`unlimited` 为假 | 金额与币种（`formatBalance`），"x 分钟前" |
| 不限 | `unlimited` | "不限"，提示"上游报告此 Key 不限额度"（不显示假数字） |
| 查询失败 | 有 `error` | 红色徽标"查询失败"，提示里是 `error` 和 `error_at`；有旧金额时金额仍显示、标为旧值 |

i18n 中英同步。不加任何颜色阈值、告警（"只显示"）。

#### 3.1.7 SDK

- `pluginsdk.BalanceReader` 可选接口（两个方法），`serve.go` 的 `platformServer` 转发，没实现回 Unimplemented（照 `QuotaReader`）。
- `standard.Type.Balance *Balance`：

```go
type Balance struct {
	// Requests are GETs on the account's base URL, sent in this order.
	Requests []BalanceRequest
	// Read turns the answers (same order) into the result.
	Read func(r *Request, answers []*pluginv1.BalanceAnswer) *pluginv1.BalanceResult
}
type BalanceRequest struct {
	Path string
	Auth *Auth // nil: the type's; &Auth{}: no credential (a public endpoint)
}
```

  `standard.Platform` 实现 `BalanceReader`，`Balance` 为 nil 的类型回 Unimplemented。再给两个小函数：`standard.BalanceFailure(a *BalanceAnswer) *pluginv1.BalanceResult`（传输错误、429、5xx → TRANSIENT；401/403 → AUTH_REJECTED；2xx → nil）和 `standard.USDMicros(units, perUSD float64) int64`。具体读法（New API、Sub2API 的字段）留在各自插件里，不进 SDK。
- `pluginsdktest`：`Harness.Balance(ctx, account, answer func(*pluginv1.BalanceRequest) *pluginv1.BalanceAnswer)`，按清单的 `UpstreamHostAllowed` 检查每个请求的主机，与 FakeHost 的中继规则一致。
- 合规套件加一条：清单里声明 `balance` 的类型，插件必须实现 `BalanceReader`；对样例账号构造的请求主机过主机规则；`Read` 对空回答和传输错误不 panic、给出错误结果。

#### 3.1.8 修掉的小问题（对应 2.1）

首次失败写库违反 NOT NULL（迁移 0113 + 不吞错误）；前后端类型对不上（3.1.6）；改凭证不失效（3.1.4）；没人用的 `balanceQueryFloor`、`defaultBalanceUpdateInterval`、`store.BalanceActive`、`source` 列删除；注释 §51 → §52；CONTRACTS §52 按本文重写，§116.3 的"账号余额"一条改为"已接通，见 §52"。

#### 3.1.9 各账号类型的余额

| 类型 | 这一版 | 可行性（以后） |
|---|---|---|
| `newapi` / `sub2api` 的 `apikey` | 做（3.5） | — |
| codex 两种 | 不是金额，看用量窗口（3.2） | — |
| `typesafe` | 不做 | 官方没有余额 / 用量接口 |
| anthropic、openai、gemini 官方 API Key | 不做 | 没有用 API Key 能调的余额接口（OpenAI 的 dashboard billing 要会话令牌，Anthropic 要 Admin Key） |
| vertex、bedrock、azure_openai | 不做 | 云账单接口要单独的账单权限、给的是花费不是余额 |
| volcengine | 不做 | 费用中心 `QueryBalanceAcct`（AK/SK 签名，`billing.volcengineapi.com`，CNY）可能可用；权限和字段未核实；它的 `apikey` 类型已有 `secret_key` 字段，可以以后试 |
| relay（Claude 中转） | 不做 | 要知道对方是哪种站点；可以加一个"站点类型"设置复用 New API / Sub2API 的读法 |
| DeepSeek、Moonshot、SiliconFlow、OpenRouter | 没有对应插件 | 都有 API Key 可调的余额接口，加插件时一起做 |

### 3.2 Codex 的用量窗口（核对结论）

- **已满足"显示周限制"**：codex 两种类型声明 `quota.headers`（主、次两组 `x-codex-*` 头）和窗口 `5h`、`7d`（`plugins/codex/manifest.json:38-47`），核心按窗口时长命名（§105），账号列表"余额"列用 `AccountQuotaCell` 显示两条进度条（百分比、重置倒计时），详情页同样。
- **不完整的地方**：只有经过网关的请求才采样，新账号或长期没请求的账号没有数据，窗口过了重置时间显示为过期；`quota.query` 为假，单元格上的"刷新"对它什么都不做（`accountQuota` 里 `decl.Query` 为假直接返回快照）。
- **补法（可选包 P8）**：codex 实现 `QuotaReader`，`GET {chatgpt}/backend-api/wham/usage`（Codex CLI 的 `/status` 用的接口；带与 Responses 请求相同的令牌和账号头），把 `rate_limit.primary_window` / `secondary_window` 的 `used_percent`、`limit_window_seconds`、`reset_at` 按与响应头相同的窗口命名写成两个窗口；清单 `quota.query: true`、`autoRefresh: false`：只在管理员点"刷新"时查，不增加对 ChatGPT 后端的定时请求，也不改变准入读到的数据来源。应答字段名来自 Codex CLI 的开源实现，**本仓库未核实**，P8 第一步用真实账号录一次应答；字段对不上就放弃 P8，不影响其它包。

### 3.3 SDK：路由级鉴权、多平台组合、按协议测试

#### 3.3.1 路由级鉴权与失败分类（`sdk/pluginsdk/standard`）

- `Route.Auth *Auth`：nil 用 `Type.Auth`。`BuildUpstream`、`BuildTestRequest` 按路由取；`BuildModelsRequest`、余额按类型（或 `BalanceRequest.Auth`）。`Check` 对路由级 `Auth` 做与类型级相同的检查（小写头名、不是不能带凭证的头）。
- `Route.Classify *classify.Policy`：nil 用 `Type.Classify`。`ClassifyError` 按 `meta.protocol` 找路由取策略；没有协议时用类型的。理由：Anthropic、OpenAI、Gemini 的错误体和 529 / 配额语义不同，中转站对各协议返回各自原生的错误。
- 已有类型不写这两个字段，行为不变；黄金样例全部照旧通过。

#### 3.3.2 `standard.Merge`

```go
// Merge is one account type serving the routes of several presets: each
// route keeps its preset's auth, headers and error policy. The models
// listing, the balance and the default test are the first part's.
func Merge(id string, spec apikey.Spec, parts ...Type) Type
```

- 每个 part 的类型级 `Headers` 前置到它每条路由的 `Headers`，`Auth` 写进路由级 `Auth`（第一个 part 的也写，便于读），`Classify` 写进路由级 `Classify`；结果的 `Headers` 为空、`Auth` 与 `Classify` 取第一个 part 的、`DefaultProtocol` 为 ""（网关请求总带协议）、`Models` 取第一个 part 的、`Test` 取第一个 part 的、`Tests` 收集所有 part 的测试。
- 两个 part 有同一协议、某个 part 有 `FilterFields`（合并后无处安放；三份预设都没有）时 panic（编程错误，与 `New` 相同）。
- 三份预设本身不改。New API、Sub2API 各自写一行 `standard.Merge("apikey", spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""), …)`，插件之间不共享代码。

#### 3.3.3 按协议的测试请求

- 问题：多平台类型只有一个测试请求；上游 Key 可能只开通了一类协议（旧版 Sub2API 的 Key 只服务它所在分组的平台）。
- proto `BuildTestRequestRequest` 加 `repeated string protocols = 3`：核心按偏好顺序列出这次测试可用的协议。`account/testreq.go` 用已有的 `accountServedProtocols(a)` 取账号开放的本平台协议（`via` 为原生，按类型 `platforms` 与平台端点的声明顺序），去掉 WebSocket、计数协议；控制台指定了模型时只留 `ProtocolRules.Serves(model, p, false)` 为真的。核心再把插件返回的 `usage_protocol` 写进给 `ClassifyError` 的 `meta.protocol`，测试结果的"效果"提示按对的策略判断。
- `standard.Type.Tests []Test`（新增，可选）：`BuildTestRequest` 取 `protocols` 里第一个在 `Test` 或 `Tests` 里有测试的协议；都没有用 `Test`。单平台插件不写 `Tests`，行为不变。
- 效果：New API 账号默认测 `openai.chat`（openai 排第一）；运营方把账号绑定成只开 `anthropic.messages` 时自动测 Messages；把账号绑定成只开 Gemini 时测 `generateContent`。这正对应所有者说的"通过协议绑定指定"。

#### 3.3.4 TypeSafe 预设与平台定义（`sdk/pluginsdk/providers/typesafe`）

- `ProtocolSystemOne = "typesafe.systemone"`、`DefaultBaseURL = "https://api.typesafe.ai"`。
- `Platform() manifest.Platform`：TypeSafe 平台的定义（3.5.3 的 JSON，嵌入 SDK 包），**唯一来源**：TypeSafe 插件的清单测试断言自己清单里的平台与它相同；New API 的合规测试经新增的 `conformance.Plugin.Platforms`（别的插件的平台）拿到端点形式。这样两个插件不互相读文件。
- `APIKey(id) standard.Type`：Bearer；`content-type: application/json`；路由 `typesafe.systemone` → `/v1/systemone`，请求体原样；`Spec{DefaultBaseURL, StripSuffixes: ["/v1"]}`；`Classify = Policy`（3.5.3）；`Test`：`{"model":"jev-latest","state":"ping","questions":{"ping":{"type":"noul","instructions":"Is this text a greeting?"}}}`；`Models = &apikey.ModelList{Path: "/v1/models", IdsPath: "models.#.name"}`（gjson 能表达，核对过 `ModelListRequest` 只是把它原样交给核心解析）。

#### 3.3.5 合规套件与黄金样例

- 合规套件：`Plugin.Platforms`（3.3.4）、余额规则（3.1.7）。
- 黄金样例（`goldentest`）：冻结的四份文件是 anthropic、gemini、openai、relay 的录制，账号类型 id、Key 校验与合并类型不同。给 `goldentest` 加选择用例和改写账号类型的选项（只取 `kind: execute`、账号自带 `base_url` 的用例），New API、Sub2API 回放 openai、anthropic、gemini 三份里它们服务的协议的用例；TypeSafe 没有录制来源，不回放，用单元测试钉住请求字节。

### 3.4 核心：OpenAI 生图

#### 3.4.1 端点（`sdk/platforms/openai.json`）

| id | 方法 路径 | 协议 | request | response |
|---|---|---|---|---|
| `images_generations` | `POST /v1/images/generations` | `openai.images` | `modelPath: model`、`streamPath: stream`、`promptTextPaths: ["prompt"]` | `stream: sse`、`nonStream: json` |
| `images_edits` | `POST /v1/images/edits` | `openai.images_edit` | 同上 | 同上 |

两个都是 `kind: proxy`、`auth.headers: ["authorization"]`、`errorFormat: openai`、`billing: usage`、`billingTypes: ["per_request","per_token","expression"]`。`model` 在官方接口里必须显式给（旧的 dall-e 默认值已下线），网关的"model is required"正合适。

**edits 只收 JSON 的代价**：在同一个平台 JSON 里多一个端点、多一条预设路由、用量规则只换事件名，所以纳入。客户端发 multipart 时网关在调度之前回 400 `invalid_argument`，信息 `request body is not valid JSON`，不选账号、不预扣、不发上游；记录类型 `invalid_request`。CONTRACTS §118 写明这一点。

#### 3.4.2 用量字段

- 清单标准字段加 `image_input_tokens`、`image_output_tokens`（`manifest.UsageImageInputTokens` / `UsageImageOutputTokens`，`check/usage.go` 允许列表）。
- `usagerules.Acc` 读这两个字段；`core.UsageTokens` 加 `ImageInput`、`ImageOutput`；`expr.Tokens` 加同名字段；`billing.go`、`usage/settler.go` 调 `Normalize` 时带上。
- `Normalize` 的规则（与缓存一致）：表达式用到 `img` 时 `Img = ImageInput`，`p` 再扣掉它；用到 `img_o` 时 `ImgO = ImageOutput`，`c` 扣掉它；没用到就留在 `p` / `c` 里按文字价算——**不会变成免费**。`p`、`c` 不为负。图片 token 本来就包含在 `input_tokens` / `output_tokens` 里（OpenAI，`semantics: inclusive`），所以 `usage_logs` 的总量、`usage_hourly` 与账号用量触发器都不用改。
- 迁移 `0112_usage_image_tokens.sql`：

```sql
SET LOCAL lock_timeout = '10s';
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_input_tokens  bigint NOT NULL DEFAULT 0,
                       ADD COLUMN IF NOT EXISTS image_output_tokens bigint NOT NULL DEFAULT 0;
```

  常量默认值的 `ADD COLUMN` 只改目录、不重写表；`lock_timeout` 照 0108 的先例。之后 `go run ./cmd/gen-models`。
- 用量接口（`/usage`、`/me/usage` 列表与详情）返回两个字段；控制台 `UsageTokenFacts.vue` 在非 0 时显示"图片输入 / 图片输出"；价格试算面板加 `img`、`img_o` 两个输入。价格页的结构化分档编辑器和价格同步不改（以后）。

#### 3.4.3 用量规则

```json
"usage": {"semantics": "inclusive",
  "json": {"map": {"model": "model", "input_tokens": "usage.input_tokens", "output_tokens": "usage.output_tokens",
                   "image_input_tokens": "usage.input_tokens_details.image_tokens",
                   "image_output_tokens": "usage.output_tokens_details.image_tokens"}},
  "sse": [{"event": "image_generation.completed",
           "map": {"input_tokens": "usage.input_tokens", "output_tokens": "usage.output_tokens",
                   "image_input_tokens": "usage.input_tokens_details.image_tokens",
                   "image_output_tokens": "usage.output_tokens"}}]}
```

edits 相同，事件名 `image_edit.completed`。`partial_image` 事件没有用量，不列。

- 非流式按官方字段读图片输出；应答不带 `output_tokens_details` 时图片输出读成 0、留在 `c` 里。
- 流式：官方文档里 completed 事件的 usage 没有 `output_tokens_details`，图片输出取整个 `output_tokens`（这个端点的输出几乎全是图片）。代价：`gpt-image-1.5` 若流式也有文字输出 token，它按图片输出价计，略多收。实现时用真实流式应答核对，事件里有明细就改成与 JSON 相同的路径（不需要所有者提供什么）。
- SSE 事件匹配的是 `event:` 行；mock 按官方格式同时写 `event:` 与 `data.type`。

#### 3.4.4 插件侧

- `providers/openai/preset.go` 加 `ProtocolImages`、`ProtocolImagesEdit` 两条路由（请求体原样，不加 `include_usage`）。openai 插件合规测试的 `httpProtocols` 加两项；`defaultModels` 加 `gpt-image-2`、`gpt-image-1.5`、`gpt-image-1-mini`（不加计划 2026-12-01 下线的 `chatgpt-image-latest`）。
- volcengine 的 `apikey`、`relay` 两个类型在 `openai` 平台上写明 `endpoints: ["chat_completions","responses","responses_websocket","embeddings"]`（它现在实际服务的四个；方舟自己的生图在 volcengine 平台上），`manifest_test.go:379` 的断言照此改。
- azure_openai、codex 已有子集，不动。New API、Sub2API 的 openai 平台不写子集，经 `Merge` 的 openai 预设自动得到两条路由。

#### 3.4.5 价格：管理员要做什么

插件不能带价格（清单写 `pricing` 校验失败，§17）。没有价格的模型按 `/settings/billing` 的 `missing_price_policy` 处理（与其它模型相同）。要按图片 token 正确计费，管理员在价格页给生图模型录**表达式价格**（单位与现有表达式相同，美元 / 百万 token）。参考价（OpenAI 官方价格页，标准档，2026-11-18 抓取，录入前以官方为准；模型名照抓取转述，录入前核对拼写）：

| 模型 | 文字输入 `p` | 缓存输入 `cr` | 图片输入 `img` | 图片输出 `img_o` | 文字输出 `c` | 表达式 |
|---|---|---|---|---|---|---|
| `gpt-image-2`，`gpt-image-2.5-sunburst`、`-2.5-flare`、`-2.6-sunburst`、`-2.6-flare` | 5.00 | 1.25 | 8.00 | 30.00 | 无（写 30） | `p*5 + cr*1.25 + img*8 + img_o*30 + c*30` |
| `gpt-image-1.5`（`chatgpt-image-latest` 同价，2026-12-01 下线） | 5.00 | 1.25 | 8.00 | 32.00 | 10.00 | `p*5 + cr*1.25 + img*8 + img_o*32 + c*10` |
| `gpt-image-1-mini` | 2.00 | 0.20 | 2.50 | 8.00 | 无（写 8） | `p*2 + cr*0.2 + img*2.5 + img_o*8 + c*8` |

- 没有文字输出价的模型把 `c` 写成图片输出价：上游不给输出明细时 `c` 里其实就是图片输出，这样两种情况都算对。
- 缓存的图片输入（官方 2.00 / 0.25）没有单独变量，并入 `cr` 按文字缓存价算，略少收；以后可以做。
- `gpt-image-1`（2026-10-23 下线）、`dall-e-2/3`（2026-05-12 下线）不用录。官方已没有按张计价的表；`svg_usage` 的计费方式未查到，这一版不读。

### 3.5 三个插件

#### 3.5.1 共同的部分

| 项 | New API（`newapi`） | Sub2API（`sub2api`） | TypeSafe（`typesafe`） |
|---|---|---|---|
| 目录 / 模块 | `plugins/newapi` | `plugins/sub2api` | `plugins/typesafe` |
| 文件 | 照 relay：`manifest.json`、`main.go`、`internal/<name>/<name>.go`、`forms/apikey.{schema,ui}.json`、`go.mod`、`go.sum`、`conformance_test.go`、`manifest_test.go`；图标用 `text:N` / `text:S` / `text:T`（无资源文件） | 同左 | 同左 |
| 版本 / hostCompat | `0.1.0`，`>=0.1.304 <0.2.0` | 同 | 同 |
| 账号类型 id | `apikey` | `apikey` | `apikey` |
| 凭据 / 设置 | `api_key`（`sensitiveFields`）、`base_url`（`settingsFields`，必填） | 同 | `api_key`、`base_url`（可选，默认官方） |
| `upstreamFields` | `["base_url"]` | `["base_url"]` | `["base_url"]` |
| `guardedSettings` | 无（地址就是运营方填的） | 无 | `[{field:"base_url", allowed:["https://api.typesafe.ai"]}]`（照 openai/anthropic/gemini 插件：没有 `account:settings:custom` 的人只能用官方地址） |
| `externalServices` | 无 | 无 | `["api.typesafe.ai"]` |
| `hostPermissions` | `platform.register`、`accounts.credentials {"types":"own"}` | 同 | 加 `gateway.endpoint` |
| `capabilities` | `platform.adapter.v1`、`platform.websocket.v1` | 同 | `platform.adapter.v1` |
| `net` 权限 | 不要 | 不要 | 不要 |
| 余额 | `balance: {updateInterval: 600}` | `balance: {updateInterval: 600}` | 不声明 |
| 默认模型 | 不写（账号默认服务所有模型；运营方用"拉取模型"） | 不写 | `jev-latest`、`jev-1.14.0`、`jev-1.13.0`、`jev-preview` |
| 内置 | 仅安装（`BUILTIN_PLUGINS_INSTALL_ONLY`） | 同 | 同 |

- Key 校验：New API、Sub2API 不限前缀（站点可自定义），用 anthropic 预设的"8–512 个不含空格的可见字符"文案；TypeSafe 同。
- `base_url` 归一化去掉粘贴的 `/v1`（Sub2API、New API 另去 `/v1beta`）。
- `passHeaders` 一律不写：取各平台自己的列表（openai、anthropic、gemini 的平台定义，TypeSafe 平台的两个危险区头）。

#### 3.5.2 New API 与 Sub2API

**平台与端点**

| 平台 | New API | Sub2API |
|---|---|---|
| `openai` | 全部端点（chat、responses、responses_websocket、embeddings、images、images_edit） | 全部端点 |
| `anthropic` | 全部（messages、count_tokens；New API 的 count_tokens 不计费，转发照常） | 全部 |
| `gemini` | `endpoints: ["generate_content","stream_generate_content"]`：New API 的 `:countTokens` 直接 404，去掉后核心对 `gemini.count_tokens` 走本地估算（§88.2） | 全部 |
| `typesafe` | 列（转发 Jev，3.6） | 不列（所有者只要求三个协议族） |

`platforms` 的顺序 openai、anthropic、gemini（、typesafe）：决定测试的默认协议和 `served_protocols` 的顺序。

**平台实现**：`standard.New(standard.Merge("apikey", spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey("")[, typesafe.APIKey("")]))`，`spec` 是 `RequireBaseURL: true`、无默认地址、`StripSuffixes`、上面的 Key 校验。各协议的鉴权头因此是官方原生的：OpenAI 与模型列表、余额 `Authorization: Bearer`，Anthropic `x-api-key`（加默认 `anthropic-version`），Gemini `x-goog-api-key`，TypeSafe Bearer。这三个站点都接受（New API 的 `x-api-key` 只在 `/v1/messages`、`/v1/models`，`x-goog-api-key` 只在 `/v1beta/models…` 接受，正好对应；新版 sub2api 作上游时 Gemini 只认 `x-goog-api-key`）。OpenAI 流式照预设加 `stream_options.include_usage`（New API 默认也返回用量，加了无害）。

**模型列表**：openai 预设的 `GET /v1/models`、`data.#.id`、Bearer。New API 默认返回 OpenAI 格式；旧版 Sub2API 对 Gemini 分组的 Key 返回什么格式**未核实**，拉不到时运营方手填模型。

**测试请求**：3.3.3，默认 `openai.chat` + `gpt-4o-mini`（预设的），按账号绑定换协议；运营方可在测试时指定模型。

**余额（New API）**：两个请求，都在 `base_url` 上（因此过 `upstreamFields` 的主机规则）。

1. `GET /api/usage/token/`（末尾斜杠；`Authorization: Bearer <key>`；令牌过期或用尽也能查）→ `{code:true, data:{total_available, unlimited_quota, user_quota?:{total_available}}}`，单位是站内原始额度。
2. `GET /api/status`（公开，`Auth: &Auth{}` 不带 Key）→ `data.quota_per_unit`（默认 500000 = 1 美元）。

读法：令牌限额时可用 = `total_available`，有 `user_quota` 时取两者较小；令牌不限额时有 `user_quota` 取它，没有则 `unlimited = true`。金额 = 可用 ÷ `quota_per_unit`，币种 `USD`（站点的"显示为人民币 / token"只是显示设置，不影响这里）。失败：`standard.BalanceFailure`；`code` 不为真 → TRANSIENT，信息带 `message`；读不到正数的 `quota_per_unit` → TRANSIENT"无法读取站点的额度换算比例"，**不猜 500000**。

**余额（Sub2API）**：一个请求 `GET /v1/usage`（Bearer；旧版和新版都接受）。依次读 `remaining`、`balance`、`quota.remaining`，取第一个存在的数；`-1` → `unlimited`（旧版订阅分组什么限额都没配时）；币种取 `unit`（缺省 `USD`；不是三位大写字母 → TRANSIENT）。`isValid` 为假 → AUTH_REJECTED"上游报告此 Key 已停用或过期"。读法同时覆盖新旧两版的形状（新版 `remaining` 总有、不会是 -1）。

#### 3.5.3 TypeSafe

**清单里的平台**（与 `providers/typesafe.Platform()` 相同）：

```json
{"id": "typesafe", "label": {"en": "TypeSafe", "zh": "TypeSafe"},
 "passHeaders": ["typesafe-danger-zone-images", "typesafe-danger-zone-long-context"],
 "endpoints": [{"id": "systemone", "method": "POST", "path": "/v1/systemone", "protocol": "typesafe.systemone",
   "kind": "proxy", "auth": {"headers": ["authorization"]},
   "request": {"modelPath": "model", "promptTextPaths": ["state"]},
   "response": {"nonStream": "json"}, "errorFormat": "openai", "billing": "usage",
   "usage": {"semantics": "exclusive", "json": {"map": {"model": "model",
     "input_tokens": "usage.input_tokens", "output_tokens": "usage.output_tokens"}}},
   "billingTypes": ["per_token", "per_request", "expression"]}]}
```

- 账号类型 `platforms: [{"platform": "typesafe"}]`。
- **不用 `usageSource: "plugin"`**：核心按 `usage` 规则读，别的插件（New API）的账号类型因此能服务这个端点，不触发 `foreign_facts`。应答里的 `model` 是实际作答的模型（别名会被解析成具体版本），用量记录的上游模型取它；计价按客户端请求的模型名（与其它平台一致），所以别名和具体版本都要有价格，或只给运营方开放其中一种。
- 四种问题类型、`assets`、两个危险区头都只是请求内容，原样转发，网关不检查（厂商的请求大小、图片尺寸限制由厂商拒绝，§112.16）。两个头由客户端决定带不带，`passHeaders` 放行。
- `promptTextPaths: ["state"]`：钩子（审核）能读到要判断的文本，与其它端点一致。
- **错误**：上游错误原样透传给客户端（状态码与 `detail` 体都不改）；`errorFormat: "openai"` 只决定网关**自己**产生的错误（鉴权失败、没有可用账号、余额不足）的形状。TypeSafe 的 `detail` 形状不在核心支持的四种里，加一种要改核心，放"以后"。
- **失败分类**（`providers/typesafe.Policy`）：解析 `detail`（字符串，或对象的 `message`，或数组第一项的 `msg`）作信息；401/403 → `Credentials()`（停用账号）；429 → `RateLimit`，重置时间读 `Retry-After` 或 `retry-after-ms`，都没有用默认冷却（文档没说服务端一定带，两种都兼容）；529（过载）与其它 5xx → `ServerErrors`（换号并短暂冷却；529 本来就在 5xx 里）；400（含 `max_tokens_exceeded`）、413、415、422 → 不匹配任何规则，原样返回客户端、不动账号。
- 模型列表 `GET /v1/models`、`models.#.name`；测试请求见 3.3.4。

### 3.6 协议绑定在这三个插件上的表现

- **新建账号**：`protocols` 为 `null`，账号类型能服务的本平台协议全部开放（包括以后新增的）：New API 是 openai 六个、`anthropic.messages`（计数跟随）、`gemini.generate` / `stream_generate`（计数本地估算），TypeSafe 插件启用时再加 `typesafe.systemone`；Sub2API 是 openai 六个、anthropic、gemini 全部；TypeSafe 只有 `typesafe.systemone`。跨平台转换默认关闭。
- **所有者说的"绑定协议、指定模型"** = 账号编辑器"协议与模型"分区的两张卡片（§110、协议绑定设计 5.1）：卡片一"本平台协议"选"指定"、只勾这个 Key 真正能用的协议（写 `protocols`）；卡片二"模型的协议限定"选中模型、指定它在哪些协议下提供（写 `model_protocols`）。例：旧版 Sub2API 的 Key 在一个 anthropic 分组里，就只勾 Messages；同一站点的 Gemini 分组 Key 另建账号只勾 Gemini。
- **上游 Key 实际不支持某个协议时**：账号仍会被调度到这个协议的请求，上游回什么错误决定后果，按各协议预设的失败分类（3.3.1）处理：400 / 404 原样返回客户端，账号不受影响、不换号；5xx 换号并短暂冷却；401 / 403 停用账号（中转站对"无权使用"可能回 403，这会误停用）；New API 对"分组里没有这个模型的渠道"回什么状态码**未核实**。所以首选是用上面的协议绑定把不支持的协议关掉：被绑定过滤的账号在调度时只做内存比较、根本不发请求（§110.4）。需要更细时运营方用故障策略（§58）按关键词改写分类。插件不另写中转站专用的规则：这与 relay 的先例一致，也避免替所有者加规则。
- **New API 列了 TypeSafe 的平台**：
  - 安装审查：清单检查不要求被引用的平台存在，装 New API 不需要先装 TypeSafe，审查里不出现 TypeSafe。
  - TypeSafe 未启用：注册表每次换代对 New API 的类型记一条 WARN（平台不存在），`served_protocols` 和协议绑定界面里没有这个协议，`/v1/systemone` 整个不存在（404）。
  - TypeSafe 启用后：New API 账号若 `protocols` 为 `null`，自动开放 `typesafe.systemone`；若已"指定"，要运营方勾上。`/v1/systemone` 的请求在 TypeSafe 账号与 New API 账号之间按分组调度。上游 New API 站点没装它自己的 TypeSafe 扩展时，请求得到上游的 404，原样返回客户端；这样的账号应在卡片一取消勾选。
  - 停用 / 卸载 TypeSafe：平台消失，同上"未启用"；账号里显式写着的 `typesafe.systemone` 留在库里不生效，下次保存这个账号时校验报"不是本平台协议"，控制台已有提示让运营方移除；重新启用即恢复。
- 控制台协议名：`openai.images`、`openai.images_edit` 加中英名称（"Images"、"Image Edits"）；`typesafe.systemone` 由插件声明，按现有规则显示 id 的后半段 `systemone`，平台自动得到颜色和首字母图标。

### 3.7 安全

- 凭据只由核心代发：插件构造请求、核心经 `RelayUpstream` / `sendPluginCall` 发出，插件不开套接字，三个插件都**不申请 `net`**。
- 每个代发请求都过：§112.17 主机规则（中继、余额；New API / Sub2API 只能到账号自己的 `base_url` 主机，TypeSafe 到 `api.typesafe.ai` 或管理员许可的自定义地址）→ 管理员插件联网（合并 `feat/plugin-domain-policy-core` 后）→ SSRF → 账号代理。余额请求不跟随重定向。
- New API 的 `/api/status` 不带 Key：公开接口不需要凭据，不把 Key 发给不需要它的地址。
- 自定义上游地址的账号（New API、Sub2API 总是，TypeSafe 改了地址时）照 §21.3 需要 `account:relay` 权限。

### 3.8 一条请求一个计价模型（2026-11-18，所有者规则；取代同日的“生图工具另计”）

同日合入的 P10（CONTRACTS 旧 §120）让 Responses 里生图工具的那部分按**另一个模型**计价，模型取自上游回显、工具参数或写死的默认名 `gpt-image-2`，没有价格时只记异常不扣费。所有者否掉了这种做法，定下的规则：

1. 一条请求只有一个计价模型：**客户请求的模型**。预扣和实际扣费都按它。
2. 绝不按上游返回的任何东西取价（应答里回显的模型名、工具信息都不算）；token 数仍取自上游应答。
3. 模型找不到价格就报错；没有默认模型名、兜底模型、退回别的价格。
4. 账号的模型测试也一样：被测的模型没有价格就直接报错，不向上游发测试请求。

做法：

- **删掉 P10 为另计生图加的东西**：附加用量规则的对象形式（`object`、`modelSources`、`defaultModel`）及只为它服务的代码（`Acc.WithRequest`、按应答取价 `priceResponseModel`、`AdditionalUsage.ResponseModel`、WebSocket 每轮记附加用量）、`additional_unpriced` 异常与控制台的警示、使用记录卡片里为它加的附加用量列表与 mock 记录。附加用量的数组形式（Anthropic `usage.iterations`，模型调度前准入）是 P10 之前就有的，不动。
- **生图工具的 token 是这条请求自己的用量**：只改 `sdk/platforms/openai.json` 里 `responses`、`responses_websocket` 的用量规则，用已有的 `+` 求和：`input_tokens` = `usage.input_tokens + tool_usage.image_gen.input_tokens`，`output_tokens` = `usage.output_tokens + tool_usage.image_gen.output_tokens`，`image_input_tokens` / `image_output_tokens` = `tool_usage.image_gen.{input,output}_tokens_details.image_tokens`（流式与 WebSocket 读 `response.` 下的同名路径）。实测的上游形状里 `usage` 只含对话、`tool_usage.image_gen` 另列，所以要相加。图片 token 是输入、输出的一部分（§118.2），表达式写了 `img` / `img_o` 按图片价，否则留在 `p` / `c` 里，都按客户请求的模型的价格算。
- 请求的计价不用改：`prepareBilling` 本来就按客户请求的模型取价、调度前冻结（`missing_price_policy` 照旧），上游回显的 `model` 只记进 `upstream_model`。
- **账号测试**（`POST /accounts/:id/test`）：插件构造好测试请求之后、核心发出之前，按被测的模型（调用方给的或账号的默认测试模型，取账号映射之前的名字；都没有时是插件报的模型）取全局价格，没有价格就回 403 `model_price_not_configured`，不看 `missing_price_policy` 的 `free`，不发上游，记为这个账号最后一次测试的失败（`error_source: gateway`）。

## 4. 替所有者做的决定

| # | 决定 | 理由 | 想改时改哪里 |
|---|---|---|---|
| D1 | 余额只显示，不停用、不影响调度、不告警 | 所有者规矩：不自作主张加限制 | 以后另加一节；联动逻辑放 `account` 模块 |
| D2 | 定时刷新用清单 `updateInterval`；两个新插件写 600 秒（10 分钟） | 余额只是展示，10 分钟够看，手动刷新随时可用；不新造间隔 | 各插件清单的 `updateInterval` |
| D3 | 删掉清单 `balance.currency`，币种由插件按账号在结果里给；New API、Sub2API 都折成 USD（**2026-11-18 所有者改为不做任何换算，见 CONTRACTS §52**：余额是上游的数字本身，单位是上游给的原文、没给就没有；清单删 `currency` 保留） | 中转站币种因站而异；统一美元便于比较 | `sdk/manifest` 与两个插件的读法 |
| D4 | "无限额度"用 `BalanceResult.unlimited`，控制台显示"不限" | 不显示假数字 | proto 与 `AccountBalanceCell.vue` |
| D5 | 一次余额查询可以有多个请求（改 proto；2026-11-18 起 New API 只发一个，见 D6 的注） | New API 要先后读令牌额度和换算比例；两个 RPC 从未被使用，改形状没有兼容成本 | 3.1.2 |
| D6 | New API 读不到 `quota_per_unit` 时报错，不按 500000 猜（**2026-11-18 所有者改为不做任何换算，见 CONTRACTS §52**：不再读 `/api/status`，额度点数原样显示、不带单位） | 猜错会差几个数量级 | newapi 插件 |
| D7 | New API 令牌不限额且站点不给用户额度时显示"不限"（new-api 自己在这种情况下报错，提示去开"允许查询用户额度"） | 所有者给的方向；令牌这一层确实不限 | newapi 插件，改成报错只要一行 |
| D8 | Sub2API 应答 `isValid` 为假时记"认证被拒" | 停用或过期的 Key 显示金额没有意义 | sub2api 插件 |
| D9 | 余额请求不跟随重定向 | 否则 3xx 能把带 Key 的请求带到主机规则之外 | `account/balance.go` |
| D10 | 定时刷新用集群任务，不加 PG 租约；停用账号只在手动刷新时查 | 只读、只显示，重复一次 GET 没代价；与额度清扫的选法一致 | `account/tasks.go` |
| D11 | 三个插件都是"内置、仅安装、默认不启用" | 所有者把它们当一等的上游类型，不想每个部署手工上传；与 volcengine 相同，启用后才生效 | `deploy/docker/build-go.sh` 两个列表、CONTRACTS 内置插件表 |
| D12 | 插件 key `newapi`、`sub2api`、`typesafe`，账号类型都叫 `apikey` | 与官方插件的命名一致；`sub2api` 与发布者名同名但不冲突（不同命名空间） | 发布前改，发布后不能改 |
| D13 | New API、Sub2API 的 openai、anthropic 平台不写端点子集（含 embeddings、count_tokens 和以后新增的端点）；New API 的 gemini 去掉 countTokens | 原样转发不花代价，运营方能按账号关掉；New API 的 countTokens 会 404 | 两个插件清单的 `endpoints` |
| D14 | 测试请求按账号开放的协议自动选（proto 加 `protocols`），不加界面 | 正对"上游 Key 只开通某类协议"，用的是运营方已经做的绑定 | 3.3.3；以后可加界面 |
| D15 | New API 列 `typesafe` 平台，Sub2API 不列 | 所有者对 New API 明说了 Jev，对 Sub2API 只要了三个协议族 | sub2api 清单加一行即可 |
| D16 | 生图用 `img` / `img_o` 加列（有迁移），本版做 generations 和 edits 的 JSON 形式 | 统计可见；edits 的 JSON 形式代价只有一个端点 | 3.4 |
| D17 | 流式生图的图片输出取整个 `output_tokens` | 官方流式事件没有输出明细；这个端点的输出几乎全是图片 | `openai.json` 的 SSE 规则 |
| D18 | volcengine 在 openai 平台上改为显式端点子集，不服务 OpenAI 生图 | 方舟的生图在它自己的平台上，形状不同 | volcengine 清单 |
| D19 | openai 插件默认模型加 `gpt-image-2`、`gpt-image-1.5`、`gpt-image-1-mini` | 默认模型只是表单预填 | openai 清单 |
| D20 | 中转站的失败分类沿用各协议预设，不另写规则 | relay 先例；首选手段是协议绑定 | `standard.Merge` 之后的 `Classify` |
| D21 | TypeSafe：默认官方地址，`guardedSettings` 只允许官方地址（有自定义权限的人可改） | 照 openai/anthropic/gemini 插件的先例 | typesafe 清单 |
| D22 | TypeSafe：网关自己的错误用 OpenAI 形状；429、529 换号冷却，400/413/415/422 原样返回 | 核心的四种错误格式里没有 `detail`；上游错误本来就原样透传 | `providers/typesafe` |
| D23 | Codex 主动查询只在手动刷新时查（`autoRefresh: false`），作为可选包 | 不增加对 ChatGPT 后端的定时请求；字段名未核实 | codex 清单 |
| D24 | 删掉 `account_balances.source` 列，保留 Redis 缓存 | 只有一种取值没人读；缓存与额度一致 | 迁移 0113 |
| D25 | 这批改动进还没发布的 0.1.304 | 平台插件的 hostCompat 下限必须等于 0.1.304；0.1.304 本来就要停机、迁移 | 第 8 节 |

## 5. 缺口

外部资料（2026-11-18）补上之后，**没有必须由所有者提供才能交付的事实**。原来的缺口：

| 缺口 | 状态 |
|---|---|
| TypeSafe 官方地址 | 关闭：`https://api.typesafe.ai` |
| TypeSafe 模型列表 | 关闭：`GET /v1/models`，`models.#.name` |
| TypeSafe 余额 / 用量接口 | 关闭：官方没有，不做 |
| TypeSafe 除 systemone 外的"相关协议" | 关闭：公开接口只有 systemone 和 models，本版都支持（multipart 形式以后） |
| TypeSafe 价格 | 关闭：$0.042 / 百万输入 token，输出免费，图片折成输入 token。管理员给每个 Jev 模型名（别名和具体版本都要）录 `per_token` 价：输入 0.042，输出 0（来源：TypeSafe 官方文档，2026-11-18 抓取） |
| OpenAI 生图用量字段、流式事件、edits 是否收 JSON | 关闭（官方 reference） |
| New API 余额接口 | 关闭：官方文档与代码一致 |

剩下的都是实现时兼容两种情况、或用真实应答核对即可，不需要所有者提供：

- TypeSafe 限速时是否带 `Retry-After` / `retry-after-ms`：两种都读，都没有用默认冷却。
- `max_tokens_exceeded` 在 400 `detail` 里的确切位置：400 一律原样返回客户端，不需要识别。
- 流式 `image_generation.completed` / `image_edit.completed` 的 usage 是否带 `output_tokens_details`：先按没有写（D17），实现时用真实流式应答核对。
- New API 对"分组里没有这个模型 / 协议"回的状态码；旧版 Sub2API 对 Gemini 分组的 `/v1/models` 格式：只影响错误后果和"拉取模型"，有协议绑定兜底。
- 上游 New API 站点是否提供 `/v1/systemone`：取决于站点自己装没装 TypeSafe 扩展，运营方按站点勾选。
- Codex `/backend-api/wham/usage` 的字段：P8 第一步用真实账号录一次；对不上就不做 P8。这是唯一需要真实账号的一项，若所有者能给一个可用于测试的 codex 账号会更快，没有也不影响其余交付。

## 6. 测试计划

**单元**

- 核心 `account`：余额查询的成功、`unlimited`、首次失败（`updated_at` 为空、视图无金额）、有旧值的失败、AUTH_REJECTED 不改账号、Unimplemented、多请求按序发送与回答顺序、主机规则拒绝时一个都不发（含管理员插件联网拒绝）、不跟随重定向（mock 回 302 到别的主机）、经账号代理、singleflight 合并、改凭证 / 设置 / 代理后快照与缓存被删、定时任务按 `updateInterval` 选到期账号（跳过停用账号、包括从未查过的）、`GET /balance` 的 404 / 强制刷新。数据库测试走 `testutil.DB(t)`，报告前确认没有成片 SKIP。
- 核心 `testreq`：`protocols` 的推导（默认、`protocols` 指定、模型限定、去掉 WebSocket 与计数协议）；`meta.protocol` 传给分类。
- 核心生图：`usagerules` 读两个新字段；`Normalize` 用到 / 没用到 `img`、`img_o` 时 `p`、`c` 的扣减与不为负；结算写两列；用量接口返回；`openai.json` 校验通过、两个端点的规则对 JSON 和 SSE 样例读出正确数；multipart 请求在 `checkBodyJSON` 被拒、不调度。
- 迁移：`migrations_db_test` 覆盖 0112、0113；`gen-models` 之后 `TestGenerated*` 一致。
- SDK：路由级 `Auth` / `Classify` 的选择与 `Check`；`Merge`（头前置、鉴权、分类、模型列表、测试集合、重复协议与 `FilterFields` 的 panic）；`Tests` 按 `protocols` 选；`BalanceReader` 转发；`standard.Balance` 与两个辅助函数；`Harness.Balance` 的主机检查；`providers/typesafe` 的请求字节、分类（401、429 带 / 不带 `Retry-After`、529、400、422）。
- 插件：New API 余额读法的每个分支（限额、取较小、不限有用户额度、不限无用户额度、`code:false`、`/api/status` 失败、`quota_per_unit` 缺失）；Sub2API（`remaining`、`balance`、`quota.remaining`、`-1`、`unit`、`isValid:false`、401）；各协议的鉴权头（Gemini 必须是 `x-goog-api-key`，Messages 是 `x-api-key`）；TypeSafe 清单平台与 `providers/typesafe.Platform()` 相同。
- 控制台：`AccountBalanceCell` 五种状态、`UsageTokenFacts` 图片行、试算面板 `img`/`img_o`、协议名；`npm run typecheck`、`npm test`、`npm run lint`。

**合规套件**（`conformance.Run`）：三个新插件各一次，`Protocols` 覆盖它们服务的每个 HTTP 协议（New API 的 `typesafe.systemone` 经 `Plugin.Platforms`），`Credentials` 列出 `authorization`、`x-api-key`、`x-goog-api-key`；新加的余额规则对 New API、Sub2API 生效；openai、volcengine 插件跟着新端点调整后照旧通过。

**黄金样例**：New API、Sub2API 用 3.3.5 的选项回放 openai、anthropic、gemini 三份冻结样例里本类型服务的协议；openai、anthropic、gemini、relay 原有回放全部照旧通过（路由级字段不影响它们）。

**e2e**（e2e 栈，不在 CI；P9 统一改 mock 和用例）

- mock 上游（`deploy/mock-upstream/main.go`）加：`GET /api/usage/token/`、`GET /api/status`（New API 形状，额度、无限额、`user_quota` 由 `__control` 规则切换）、`GET /v1/usage`（旧版 Sub2API 的三种形状与 `-1`、新版形状）、`POST /v1/images/generations` 与 `/v1/images/edits`（JSON 与流式，usage 含 `input_tokens_details.image_tokens`、`output_tokens_details`，流式按官方写 `event:` 行，只有 completed 带 usage）、`GET /v1/models` 对 TypeSafe 的 `models[].name` 形状（按请求头或路径区分，现有的 `/v1/models` 保持原样）。`/v1/systemone` 已有。
- AC30 平台矩阵加三行：`newapi`（chat，检查 `Authorization: Bearer` 与 `include_usage`）、`sub2api`（Gemini，检查 `x-goog-api-key`——验证路由级鉴权）、`typesafe`（systemone，检查请求体原样、危险区头透传、用量入账）。e2e 需要启用三个仅安装的内置插件（`Env.Enable`）。
- AC39 账号余额：New API 账号（限额、不限）、Sub2API 账号（金额、`-1`）的手动刷新；新建账号不手动刷新、等定时任务（≤ 2 分钟）出现余额；mock 回 401 时显示失败、账号仍可调度；把 mock 主机加进插件联网屏蔽列表后余额查询失败且 mock 没收到请求（依赖 `feat/plugin-domain-policy-core`），用例结束恢复设置。
- AC40 OpenAI 生图：openai API Key 账号、New API 账号各发一次 generations（非流式、流式）和一次 edits（JSON），使用记录有图片 token，表达式价格算出的费用与手算一致；multipart 的 edits 得到 400 且没有使用记录。

## 7. 任务拆分

**一览**

| 包 | 范围 | 依赖 | 改的契约 |
|---|---|---|---|
| P1 契约 | `sdk/proto`（余额消息、`unlimited`、`BuildTestRequestRequest.protocols`）+ `buf generate`；`sdk/manifest`（`AccountBalance` 去 `Currency`、`check/balance.go`、图片用量字段常量与 `check/usage.go`） | 无 | proto、manifest |
| P2 SDK | `sdk/pluginsdk`（`BalanceReader`、`serve.go`）、`standard`（路由级 `Auth`/`Classify`、`Merge`、`Tests`、`Balance` 与辅助）、`standard/goldentest`（选项）、`pluginsdktest`（`Harness.Balance`）、`pluginsdktest/conformance`（`Platforms`、余额规则）、新包 `providers/typesafe`；`standard/README.md`、`PLUGIN-DEVELOPMENT.md`、CONTRACTS §112.13 | P1 | —（SDK 公开接口） |
| P3 核心余额 | `server/internal/core/ports_plugin.go`、`plugin/grpcruntime/adapters.go` 与替身、`account/balance*.go`、`account/tasks.go`、`account/handlers.go`（失效）、`account/testreq.go`（`protocols`、`meta.protocol`）、`store/account_balance.go`、迁移 0113 + `gen-models`；`web` 的余额类型、单元格、mock、i18n；CONTRACTS §52 重写、§19、§116.3 | P1；`feat/plugin-domain-policy-core` 已合并 | core、迁移 0113、CONTRACTS |
| P4 核心生图 | `sdk/platforms/openai.json`、`providers/openai/preset.go`、`plugins/openai`（清单默认模型、合规测试）、`plugins/volcengine`（端点子集、`manifest_test.go`）、`server/internal/usagerules`、`core/ports_billing.go`、`billing/expr/normalize.go`、`billing/billing.go`、`usage/settler.go` 与用量接口、迁移 0112 + `gen-models`；`web` 的用量详情、试算面板、协议名；CONTRACTS 新 §118、ARCHITECTURE 7.3 的变量表 | P1 | core、迁移 0112、platforms、CONTRACTS |
| P5 New API | `plugins/newapi/**` | P2、P4 | — |
| P6 Sub2API | `plugins/sub2api/**` | P2、P4 | — |
| P7 TypeSafe | `plugins/typesafe/**` | P2 | — |
| P8 codex 主动查询（可选） | `plugins/codex/**`（`QuotaReader`、清单 `quota.query`） | 无（先用真实账号录应答） | — |
| P9 集成与 e2e | `next/go.work` + `go work sync`、`server/internal/plugin/install/reserved.go`（三个 key、`reservedPlatforms["typesafe"]`）、`deploy/docker/build-go.sh`（`BUILTIN_PLUGINS_INSTALL_ONLY`）、`deploy/mock-upstream`、`e2e`（常量、AC30 三行、AC39、AC40）、`deploy/e2e/README.md`；CONTRACTS 新 §119 与内置插件表（约 2211 行）、`CHANGELOG.md` | P3–P7 | CONTRACTS |

**交付标准**（每个包都要：`gofmt -l .` 无输出、`go vet`、`go build`（核心另 `GOOS=linux`）、本模块 `go test -count=1` 全过；核心加 `-race`；改了 `web` 的跑 typecheck / test / lint；按 AGENTS 规则同步本包负责的文档）

- P1：`cd next/sdk && go test ./...`；proto 生成物与 `buf generate` 一致；`sdk/manifest` 的检查测试覆盖去掉 `currency` 后的 `balance: {}`。生成物变更后核心与全部插件仍能编译（核心和 SDK 现在都不引用余额消息，改形状不影响它们）。
- P2：SDK 全部测试；九个现有平台插件的合规与黄金样例照旧通过（在各插件目录跑一遍）。
- P3：`go test -race ./internal/account/... ./internal/plugin/grpcruntime/... ./internal/migrations/...` 与全量核心测试；数据库测试真的跑了。
- P4：核心全量测试；`plugins/openai`、`plugins/volcengine` 的测试；`sdk/platforms` 的测试。
- P5–P7：插件目录 `go test`（合规、黄金样例、清单测试）；`sub2api-plugin pack` 成功；P5–P7 在 P9 加 `go.work` 之前用 `GOWORK=off` 测（`go.mod` 照 AGENTS 规则 `replace` `next/sdk`、`next/runtime-contract`）。
- P8：codex 测试；用真实账号录下的应答作测试数据（去掉身份信息）。
- P9：`tools/sub2api-plugin` 测试（`TestReservedCoverRepo`、`TestPluginsStartAtTheFirstCoreOfTheProtocol`、`TestReleasedPluginsBumped`）；`sh .github/next-ci/next-check-module.sh` 对三个新模块；e2e 栈上 `-run '^TestAC(30|39|40)'` 全过，再跑整套一次。

**公共文件的归属**（避免重叠）

| 文件 | 由谁改 |
|---|---|
| `next/go.work`、`go.work.sum`、`reserved.go`、`build-go.sh`、`deploy/mock-upstream`、`e2e/**`、`CHANGELOG.md`、CONTRACTS 内置插件表与 §119 | 只由 P9 改；其它包把要写进 CHANGELOG 的条目写在自己的提交说明里，P9 汇总 |
| CONTRACTS | 按节归属：P2 §112.13；P3 §52、§19、§116.3；P4 §118；P9 §119 与内置插件表。各包只改自己的节，合并时没有重叠的块 |
| `sdk/pluginsdk/providers/openai` | 只由 P4 改（P2 不碰三份预设） |
| `server/internal/core` | P3 改 `ports_plugin.go`，P4 改 `ports_billing.go`，不同文件 |
| 迁移编号 | P4 用 0112，P3 用 0113（固定分配，谁先合并都不冲突；`gen-models` 生成的文件分别是 `usage_logs.gen.go`、`account_balances.gen.go`） |
| `web` | P3 只改 `views/accounts/AccountBalanceCell.vue`、`api/types.ts` 的余额类型、余额 mock 与 i18n；P4 只改用量详情、价格试算、协议名的 i18n。`api/types.ts` 两包改不同的类型 |

**顺序与并行度**：P1（约半天，单人）→ P2、P3、P4、P8 四个并行 → P5、P6、P7 三个并行（P7 只等 P2，可以先开始）→ P9。关键路径是 P1 → P2 / P4 → P5 → P9。P3 等 `feat/plugin-domain-policy-core` 合并；若它晚到，P3 先按 `UpstreamHostAllowed` 写好，合并后改成 `CheckPluginUpstream`，并补 AC39 的插件联网一步。

## 8. 升级与发布须知

- **迁移**：0112（`usage_logs` 加两列，只改目录，`lock_timeout` 10 秒，拿不到锁迁移失败、重试即可）、0113（`account_balances`，表为空）。改库的版本走 `primary-first-v1`。两条迁移都不是单向的障碍：旧核心忽略多出来的列；但 0.1.304 批次本身已是单向（0106–0111），回退照那批的手册连同数据库回到备份。
- **版本线**：这批改动随 0.1.304 一起发布（CHANGELOG"未发布"一节里加小节）。原因：平台插件的 `hostCompat` 下限必须等于 `protocol.FirstCoreVersion`（0.1.304），三个新插件、openai 的新端点、余额 RPC 都要求核心有这些能力；同批发布时"能加载插件的核心"和"有这些能力的核心"是同一个集合。**若 0.1.304 已先发布**：这批改动作为 0.1.305 发布，同时把 `TestPluginsStartAtTheFirstCoreOfTheProtocol` 改为允许下限高于 `FirstCoreVersion`（用到新能力的插件写 `>=0.1.305`，AGENTS 开发规则 4），openai、volcengine、codex 各升一个版本。
- **插件版本**：三个新插件 `0.1.0`，不在 `released.json` 里，`TestReleasedPluginsBumped` 跳过；发布后按该测试的说明写进去。openai（0.5.0）、volcengine（0.13.0）、codex（0.2.0）在 0.1.304 批次里已高于 `released.json`（0.4.3、0.12.8、0.1.11），本批再改内容不必另升；若改为 0.1.305 发布，则各升补丁版本。
- **Host API / 插件协议**：不变（10 / 2）。proto 只改了从未被实现或调用的余额消息、加了字段。
- **内置清单变化**：`BUILTIN_PLUGINS_INSTALL_ONLY` 变为 `volcengine mailer dynweight newapi sub2api typesafe`。现有部署升级后，三个插件随镜像安装但**不启用**（§37），什么都不发生；管理员在插件页启用、建账号后才生效。`reserved.go` 保留这三个 key 和 `typesafe` 平台 id，第三方包不能再用。
- **对现有部署的可见变化**：OpenAI API Key 账号能服务 `/v1/images/generations`、`/v1/images/edits`（JSON），没有价格的生图模型按 `missing_price_policy` 处理——管理员按 3.4.5 录价格；volcengine 账号不服务这两个端点（以前也不服务）；账号列表里声明余额的类型（只有两个新插件）显示余额；协议绑定"指定"过的 OpenAI 账号不会自动开放图片协议（§110 的既定行为），要的话运营方勾上。
- **TypeSafe 价格**：管理员给 `jev-latest`、`jev-1.14.0`、`jev-1.13.0`、`jev-preview` 录 `per_token` 输入 0.042、输出 0（3.4.5、第 5 节）。New API 转发 Jev 时按同一模型名计价。
