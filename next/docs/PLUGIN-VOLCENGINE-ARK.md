# 插件设计：字节火山方舟 / 豆包（`volcengine`）

> 状态：2026-09-29 调研产出的设计稿，**已实现**（0.5.x）。0.13.0 按转发 V2 改写（CONTRACTS §112，需核心 ≥0.1.304），现状见 §5 与 §17；§3、§4、§9–§16 是各期当时的设计与记录，其中的 `BuildUpstreamRequest`、补丁、`requestFields`、`ExtractUsage` 事后调用、`Poll` 与两阶段核对都已删除。**2026-09-30 第一次经真实上游跑通视频全链路，见 §12**；正文里与 §12 冲突的数字以 §12 为准。
> 目标：把 new-api 拆成两个渠道类型的「字节火山方舟」和「豆包通用 / 豆包视频」合成 **一个** sup2api 插件，支持自定义 Base URL 与素材库端点。
> 参考实现：`D:\projects\golang\new-api`（AGPL-3.0，**只读设计、不复制代码**；sup2api 为 LGPL-3.0）。

---

## 1. 需求

| 项 | 要求 |
|---|---|
| 合并 | 火山方舟（对话 / 嵌入 / 图片）与豆包视频在 sup2api 里是**同一个插件、同一种账号类型**，不像 new-api 那样分成两个渠道类型 |
| 自定义 Base URL | 账号可填自己的 Ark 地址（官方国内、BytePlus 海外、第三方中转），留空用官方默认 |
| 自定义素材端点 | 素材库（火山「素材」OpenAPI）的地址可单独配置，留空表示该账号不启用素材库 |

### new-api 的现状（要合并的两边）

| new-api | 渠道类型 | 内容 |
|---|---|---|
| 「字节火山方舟」 | `ChannelTypeVolcEngine = 45`，显示名 `VolcEngine` / 火山方舟 | `relay/channel/volcengine/`：OpenAI 兼容的 `chat/completions`、`embeddings`、`images/generations`、`rerank`、`responses`，加上走 WebSocket 的 TTS |
| 「豆包通用 / 豆包视频」 | `ChannelTypeDoubaoVideo = 54`，显示名 `Doubao` | `plugins/tasks/doubao/plugin.js`（JS 任务插件）：Seedance 视频（异步任务）、Seedream 图片（同步），自带 `usageSchema` 计量与素材库 |

两边默认 Base URL 都是 `https://ark.cn-beijing.volces.com`（`constant/channel.go:116`、`:125`），任务插件的 `meta.channelTypes = [54, 45]` 已经承认「两种渠道说的是同一套线上协议」——**合并成一个插件是符合上游事实的**，不是我们自创的简化。

素材库在 new-api 里的配置形态（`relaykit/dto/channel_settings.go:242`）：

```go
AssetLibraryEndpoint string // 兼容火山的素材 OpenAPI 路径，拼在渠道 Base URL 之后；空 = 不启用
AssetLibraryRegion   string // 签名区域，空 = cn-beijing；BytePlus 海外用 ap-southeast-1
```

凭证格式是 `APIKey` 或 `APIKey|AccessKey|SecretKey`（填了 AK 才启用素材库），素材调用用火山官方 SDK 的 V4 HMAC-SHA256 签名，`ServiceName=ark`、`Version=2024-01-01`，官方端点 `https://ark.cn-beijing.volcengineapi.com`（`service/assetlib/upstream.go:24`）。支持的 Action 是 `CreateAsset(Group)` / `Get` / `List` / `Update` / `Delete` 十个（`service/assetlib/openapi.go:145`）。

---

## 2. 要接入的上游接口面

以下路径以 Ark 为准（来自 new-api 实际可用的代码与火山官方文档），Base 为 `https://ark.cn-beijing.volces.com`：

| 能力 | 方法与路径 | 形态 | 计量 |
|---|---|---|---|
| 对话 | `POST /api/v3/chat/completions`（模型名以 `bot` 开头时为 `/api/v3/bots/chat/completions`） | OpenAI 兼容，支持 SSE | prompt/completion token（inclusive） |
| Responses | `POST /api/v3/responses` | OpenAI 兼容 | token |
| 嵌入 | `POST /api/v3/embeddings` | 同步 JSON | token |
| 重排 | `POST /api/v3/rerank` | 同步 JSON | token |
| 图片（Seedream） | `POST /api/v3/images/generations` | **同步** JSON，图生图也走这个路径 | 张数 + token |
| 视频（Seedance） | `POST /api/v3/contents/generations/tasks` → `{id}` | **异步任务** | 无 |
| 查询视频任务 | `GET /api/v3/contents/generations/tasks/{id}` | 轮询；`status` ∈ queued/running/succeeded/failed/expired；成功时带 `content.video_url` 与 `usage.completion_tokens` | **在这里才拿到用量** |
| 素材库 | `POST {素材端点}/?Action=...&Version=2024-01-01` | 火山 OpenAPI，AK/SK V4 签名 | 不计费 |
| 语音 TTS | `wss://openspeech.bytedance.com/api/v1/tts/ws_binary` | **WebSocket** | — |

计费惯例（火山官方 + 社区中转的一致做法）：**创建视频任务不计费，第一次查到 `succeeded` 时按 `usage.completion_tokens` 结算，并按任务 id 去重**。

---

## 3. sup2api 插件模型能表达什么

先把能力边界钉死，后面的分期完全由它决定。

### 3.1 能直接表达的

- **账号类型服务内置平台**：`accountTypes[].platforms[].platform = "openai"`，插件只实现 `BuildUpstreamRequest` 把请求指到 `{base_url}/api/v3/chat/completions`。`relay` 插件就是这么接 anthropic 的（`next/plugins/relay/manifest.json`）。
- **自定义 Base URL**：`settingsFields: ["base_url"]` + `guardedSettings` 限定官方地址，持有 `account:settings:custom` 权限的管理员才能填别的（CONTRACTS §21.3）。这正好对应 new-api 的「已解锁豆包自定义 API 地址编辑」。
- **插件自己的平台与端点**：`platforms[].endpoints[]`，需要 `platform.register` + `gateway.endpoint` 两个 High 权限（`sdk/manifest/check/validate.go`）。
- **非 token 计量**：`usage.facts` 声明计量键，价格表达式里用 `u("key")` 读（ARCHITECTURE 7.3）。核心已实现（`billing/expr`、`usagerules/usagerules.go:93`）。
- **插件自己的后台接口与页面**：`routes` + `ui.pages` / `ui.native`，走 `/api/v1/p/volcengine/*`。

### 3.2 现在**不能**表达的（硬约束，已逐条核对代码）

| # | 约束 | 出处 | 影响 |
|---|---|---|---|
| A | **每个端点必须能取到模型**：`request.modelPath` 或 `request.modelParam` 二选一必填，且只能是「请求体 gjson 路径」或「路径参数」这两种声明式取法 | `sdk/manifest/check/validate.go` | 查询任务 `GET .../tasks/:id` 没有模型，**端点声明不出来** |
| B | **插件拿不到路径参数**：`BuildUpstreamRequestRequest`、`ResolveAffinityKeyRequest`、`RankAccountsRequest` 都只有 `meta`（无路径）、请求体字段和请求头 | `sdk/proto/.../platform.proto`、`scheduler.proto` | 插件无法拼出 `/tasks/{id}` 的上游 URL，也无法按任务 id 选账号 |
| C | **粘性会话取不到路径参数**：`keySources.type` 只有 `body / header / api_key / user / plugin` | `gateway/sticky.go:296`、`sdk/manifest/manifest.go:268` | 同上 |
| D | **核心不解析上游响应体来建绑定**，绑定只在请求成功后按「本次请求算出的会话键」写入 | ARCHITECTURE 6.5 第 4 步 | 创建任务时任务 id 在**响应体**里，`task_id → 账号` 的对应关系**没有任何一方能记下来** |
| E | **没有「同一任务只计费一次」的概念**：幂等键是 `usage:request_id`，每个 HTTP 请求一条 | ARCHITECTURE 7.4 | 客户端反复查询已成功的任务会**重复计费** |
| F | `kind` 只允许 `"proxy"`，`"custom"`（插件自己处理、复用核心鉴权与计费）是预留未实现 | `sdk/manifest/check/validate.go` | 异步任务类接口没有正式落点 |
| G | ~~端点路径首段保留~~ **已解决**：`manifest.CoreRouteSegments`（`sdk/manifest/routes.go`）成为唯一真相，`app/app.go`、`webui/webui.go`、`manifest/check` 三处全部从它派生，核心与 CLI 行为一致。2026-10-05 起核心只保留 `/api/v1`（控制台与管理 API 实际所在），不再把整个 `api` 首段占掉，`plugin-ui` / `healthz` 仍按首段精确匹配 | `sdk/manifest/routes.go` | 插件 0.12.3 起直接用 Ark 原生的 `/api/v3/...`，另加 `/doubao/api/v3/...` 别名（与 new-api 的豆包路由一致）；原先的 `/ark/v3/...` 已删除 |
| H | 网关只代理 HTTP，没有 WebSocket 通道 | 全局 | 豆包语音 TTS 接不了 |
| I | **`HostService` 没有账号接口**：只有 log / kv / db.schema / authz / ledger / broadcast；`accounts.read` 这个权限在非测试代码里从未被使用 | `sdk/proto/.../host.proto`、全仓检索 | 插件**无法**自己列账号、读凭证，所以「插件完全绕开网关、自己挑账号发请求」这条路走不通 |
| J | **`HostService` 没有「上报一笔用量」接口**：计费只在网关流水线里发生 | ARCHITECTURE 7.4 | 插件后台完成的任务，没法让核心按价格表结算 |
| K | ~~核心不持有平台插件的调用句柄~~ **已解决（A 期）**：`core.PlatformBinding` 有了 `Client`，`registry.go` 会把声明平台的插件的 `PlatformService` 填进去。注意内置平台恒 nil，且**声明平台不要求 `platform.adapter.v1`**，所以插件平台的 Client 也可能是 nil | `core/ports_plugin.go:70`、CONTRACTS §25.1 | 调用方必须做 nil 检查 |

> A / B / C / D 是同一件事的四个切面：**核心与插件之间缺少「按请求上下文问插件」和「把上游响应的少量结果回传插件」这两条通道**。补上之后，异步任务不必成为核心概念（见第 5 节）。

---

## 4. 设计方案

### 4.1 插件标识

| 字段 | 值 |
|---|---|
| `key` | `volcengine` |
| `name` | `{ "en": "Volcengine Ark", "zh": "字节火山方舟" }` |
| `icon` | `text:V`（后续换成火山方舟图标 `ui/icon.svg`） |
| `capabilities` | 一期只有 `platform.adapter.v1`；三期（素材库）再加 `http.routes.v1` |
| `hostPermissions` | 一期只有 `platform.register` + `accounts.credentials{types:own}`；三期再加 `db.schema`、`routes.admin`、`ui.menu` |
| `database.schema` | 三期才有，`plg_volcengine` |

> 这张表按期递增，**不要照最终形态去实现一期**，那会多申请两个高危权限。

### 4.2 账号类型

> **已被 §14 取代（0.7.0）。** 这里写的「只有一个账号类型」不再成立：0.7.0 把它拆成 `apikey`（方舟官方）与 `relay`（Ark 兼容中转站），下面这段是一期的原始设计记录。其中的 `form`（schema 表单）在 0.13.0 改为原生组件 `{"mode": "native", "component": "VolcengineKeyForm"}`（§19；schema 表单已从平台删除）。

```jsonc
{
  "id": "apikey",
  "label": { "en": "Ark API key", "zh": "方舟 API Key" },
  "form": { "mode": "schema", "schema": "forms/apikey.schema.json", "uiSchema": "forms/apikey.ui.json" },
  "sensitiveFields": ["api_key", "secret_key"],
  "settingsFields": ["base_url", "asset_base_url", "asset_region"],
  "guardedSettings": [
    { "field": "base_url", "allowed": [
        "https://ark.cn-beijing.volces.com",
        "https://ark.ap-southeast.bytepluses.com" ] },
    { "field": "asset_base_url", "allowed": [
        "https://ark.cn-beijing.volcengineapi.com" ] }
  ],
  "platforms": [
    { "platform": "openai" },      // 内置：对话 / responses / 嵌入
    { "platform": "volcengine" }   // 本插件的平台：图片、视频（见 4.3）
  ]
}
```

表单字段：

| 字段 | 存放 | 说明 |
|---|---|---|
| `api_key` | 凭证（加密） | Ark API Key，必填 |
| `access_key` / `secret_key` | 凭证（`secret_key` 加密） | 素材库 AK/SK；两者都留空 = 不启用素材库 |
| `base_url` | settings | 留空 = 官方 `https://ark.cn-beijing.volces.com`；受 `guardedSettings` 约束 |
| `asset_base_url` | settings | 素材库端点，留空 = 不启用 |
| `asset_region` | settings | 签名区域，留空 = `cn-beijing`，BytePlus 填 `ap-southeast-1` |

一个账号类型同时服务两个平台，就是「合并成一个插件」在 sup2api 里的落法：**管理员录一次账号，对话、图片、视频、素材全都有了**，不用像 new-api 那样建两个渠道。

### 4.3 平台与端点

对话 / responses / 嵌入**不新建平台**，直接服务内置 `openai` 平台——客户端照常打 `/v1/chat/completions`，与别的 OpenAI 账号混在同一个分组里调度。

其余走插件自己的平台 `volcengine`。端点路径使用 Ark 官方的 `/api/v3/...`（核心保留段已收窄到 `/api/v1`，其余放行），以及 `/v1/contents/...` 别名以兼容旧版 Ark 视频 API（new-api 的 DoubaoVideoLegacyAPI 就是把 `/api/v3` 替换成 `/v1`，报文格式完全相同）：

```jsonc
"platforms": [{
  "id": "volcengine",
  "label": { "en": "Volcengine Ark", "zh": "火山方舟" },
  "usage": { "semantics": "inclusive" },
  "endpoints": [
    { "id": "images", "method": "POST", "path": "/api/v3/images/generations",
      "protocol": "volcengine.images", "kind": "proxy",
      "auth": { "headers": ["authorization"] },
      "request": { "modelPath": "model" },
      "response": { "nonStream": "json" },
      "errorFormat": "openai", "billing": "usage",
      "usage": { "semantics": "inclusive",
        "json": { "map": { "output_tokens": "usage.output_tokens" } },
        "facts": { "images": { "type": "number", "path": "usage.generated_images" } } } },

    // —— 以下两个端点用到的 request.modelSource / usage.source 是四期才存在的
    //    manifest 字段。四期落地之前，这段 jsonc 抄进 manifest 会被
    //    DisallowUnknownFields 直接打回。 ——
    { "id": "video_submit", "method": "POST", "path": "/api/v3/contents/generations/tasks",
      "protocol": "volcengine.video_submit", "kind": "proxy",
      "request": { "modelPath": "model" },
      "usage": { "source": "plugin" },               // 插件读出任务 id 并返回预扣
      "billing": "usage" },                          // 计的是预估费用
    { "id": "video_query", "method": "GET", "path": "/api/v3/contents/generations/tasks/:task_id",
      "protocol": "volcengine.video_query", "kind": "proxy",
      "request": { "modelSource": "plugin" },        // 插件拿 task_id 查表得出模型
      "billing": "free" }                            // 客户端查多少次都不计费
  ]
}]
```

### 4.4 计费

| 能力 | 口径 |
|---|---|
| 对话 / responses / 嵌入 | 内置 openai 平台的 token 规则，`semantics: inclusive`，无需插件参与 |
| 图片 | `usage.generated_images` 进 `facts.images`，`usage.output_tokens` 进输出 token。**Ark 图片响应没有 `input_tokens`**，所有样本里 `total_tokens == output_tokens`；另有 `usage.input_images`（传了参考图时才出现），当前不计费。价格表达式形如 `tier("base", u("images") * 0.03 + c * 2)` |
| 视频 | 提交时插件按分辨率与时长返回预估用量，核心按价格表预扣；核对到 `succeeded` 后插件返回真实 `usage.completion_tokens`（作为输出 token `c`）与 `resolution` fact，核心补扣或退回差额 |

价格仍然归核心、由管理员按完整模型 ID 配置（ARCHITECTURE 7.3），插件只声明计量键，不带价。

### 4.5 素材库

素材库不是 LLM 代理，不走网关，**作为插件自己的后台接口 + 控制台页面**落地：

```
GET    /api/v1/p/volcengine/assets            列出素材（按账号）
POST   /api/v1/p/volcengine/assets            创建素材
DELETE /api/v1/p/volcengine/assets/:id        删除素材
GET    /api/v1/p/volcengine/asset-groups      素材组
```

插件内部用账号上的 `asset_base_url` + `access_key/secret_key` 做火山 V4 HMAC-SHA256 签名调用上游（0.13.0 起由核心经账号代理发出、不需要 `net` 权限，见 §17；此前需要 `net` 权限、经出口隧道直连）。`plg_volcengine` 里存本地索引（账号 id、上游素材 id、名称、类型、状态），控制台出一个 native 页面。

**签名用火山官方 Go SDK，不要手写**（2026-09-29 决定）：`github.com/volcengine/volcengine-go-sdk`（Apache-2.0，与本仓库 LGPL-3.0 兼容），走 `universal.DoCall`，`ServiceName=ark`、`Version=2024-01-01`、`Region` 取账号的 `asset_region`（默认 `cn-beijing`，BytePlus 用 `ap-southeast-1`）。手写 V4 签名是典型的易错活，new-api 也是这么做的（`service/assetlib/upstream.go`）。

> **依赖只能加在 `next/plugins/volcengine/go.mod`**，不许进 `next/sdk/` 或 `next/server/`。SDK 的依赖面刚因为 manifest 校验收敛多了 `semver` 和 `cron`（CONTRACTS §26.1），所有插件的 go.mod 都会跟着带，不能再往里塞厂商 SDK。

### 4.5.2 三期曾被一个核心缺口阻塞（2026-09-29 当天解除）

> **已解除**：`HostService.ListAccounts` / `GetAccountCredentials` 已落地（CONTRACTS §26.6），带「只限本插件账号类型 + NOT_FOUND 不泄露存在性 + 每次读写审计且审计失败即调用失败 + 游标分页」四道约束。三期可以开工。下面保留当时的推演，因为它解释了为什么最终是这个形态。

**素材库现在做不了**，不是设计问题，是核心少一个能力。

素材库是插件自己的 HTTP 路由，而 `HTTPRequest`（`app.proto`）只给 `Caller{user_id, request_id, client_ip, locale}`；`HostService` 的 10 个 RPC（Log / KV×4 / GetDSN / AuthzCheck / Ledger×2 / Publish）里**没有任何账号或凭证接口**。凭证只在 `BuildUpstreamRequest`、`ClassifyError` 这类**网关调用**里由核心随 `Account` 传给插件。

于是：插件要用账号上的 `access_key` / `secret_key` 做火山 V4 签名，却在自己的路由里**拿不到那对密钥**。

排除掉的替代方案：

| 方案 | 为什么不行 |
|---|---|
| AK/SK 放插件全局设置（`ui.settings`），走 `Configure` 拿 | 变成整个安装共用一套素材库，失去 new-api 的按账号隔离（它的凭证格式就是 `APIKey\|AccessKey\|SecretKey`，每个渠道一套） |
| 插件在 `ValidateCredentials` / `BuildUpstreamRequest` 时把看到的凭证缓存进自己的 schema | 插件持久化解密后的凭证，安全上不可接受 |
| 把素材库做成 `volcengine` 平台的网关端点，让核心选账号并传凭证 | V4 签名要对**完整请求体**做哈希，而 `BuildUpstreamRequest` 只拿到端点声明的 `requestFields`——契约明确禁止整体下发请求体。死路 |

**需要的核心能力**：让插件读**自己账号类型**的账号与凭证，受既有的 `accounts.credentials {"types":"own"}` 授权约束（这个 grant 的语义本来就是「本插件账号类型的凭证」，现在只是没有主动读取的入口）。形态建议：

```protobuf
// HostService
rpc ListAccounts(ListAccountsRequest) returns (ListAccountsResponse);      // 只返回本插件账号类型的账号
rpc GetAccountCredentials(GetAccountCredentialsRequest) returns (GetAccountCredentialsResponse);
```

这不该夹带在插件三期里偷偷做——它是一项新的核心能力，且触及凭证，**要单独决策、单独排期**。在此之前三期挂起。

> 顺带：这也解释了 `HostPermissionRisk` 里 `accounts.read`（Medium）为什么在非测试代码里从未被使用——它是为这个入口预留的，入口一直没做。

### 4.5.3 火山官方 SDK 用不到的地方（重要）

**代理路径上用不了它。** 在 sup2api 的架构里上游请求是**核心发的**：插件在 `Execute` 里构造最终请求，交给核心的 HTTP 客户端（`RelayUpstream`），真正的 HTTP 调用连同账号代理、SSRF 防护、用量提取、失败切换、并发槽位全在核心手里（CONTRACTS §112）。插件改用 Ark SDK 自己发请求，就等于把这一整套全部绕过。插件和核心都不用厂商的 SDK 客户端。

任务观察由核心调用一次 `Monitor`，插件通过受执行上下文约束的 `ExecuteHTTP`（SDK 的 `Fetch`）查询，把进度或终态放在返回值里；连接仍由核心代理，沿用原账号的网络与限额约束，账务和结束监控由核心原子提交。

所以本插件里火山官方 SDK 的作用域**只有素材库这一处**：它是插件自己的 HTTP 路由 + 自己的出口调用，不经过网关。

**不做**：new-api 那种「对外提供一个兼容火山签名的素材 OpenAPI 入口」。sup2api 网关只认 API Key 鉴权，没有可插拔的签名鉴权，做这个要动核心鉴权链，性价比太低。

### 4.6 两个上游细节（一期实现时确认）

- **`bot` 前缀模型走另一条路径**：模型名以 `bot` 开头时是「接入点/智能体」，路径为 `/api/v3/bots/chat/completions`。判据只能是 `strings.HasPrefix(model, "bot")`——上游只给了这一个信号，所以名字里带 bot 但不是接入点的模型（如 `doubao-bot`）会被误判。一期已实现并在测试里钉住了这个边界。
- **Ark 错误体的 `type` 不是 OpenAI 语义类型**。Ark 返回 `{"error":{code,message,param,type}}`，但 `type` 是 HTTP 短语（`Unauthorized`、`BadRequest`），不是 OpenAI 的 `invalid_request_error` / `rate_limit_error`。**任何「OpenAI 兼容就直接透传 type」的实现都会把 HTTP 短语吐给 OpenAI SDK**。正确做法是按状态码给标准类型，把 Ark 的 `code` 放进 reason。
- **图片生成的失败可能是 HTTP 200**：整个请求一张也没生成时，Ark 在 200 里放顶层 `error` 对象；组图时 `data[]` 里也可能逐张带 `error`。核心只在非 2xx 才调 `ClassifyError`，所以这两种会被当成功请求记账。目前不会乱收钱（这时 `generated_images` 通常是 0 或缺失，`u("images")` 算出 0），但 `billable` 会因为 `Metrics` 非空而为 true。

### 4.7 明确不做

- **豆包语音 TTS**：WebSocket，网关不支持（约束 H）。
- **从上游拉取模型列表**：方舟**没有** API Key 可调用的模型列表接口（详见 §6 一期说明），`BuildModelsRequest` 返回 `Unimplemented`，模型由管理员手填。

---

## 5. 异步任务：插件执行，核心记录

本轮先接入 [CONTRACTS §28](CONTRACTS.md#28-核心托管异步任务2026-10-01) 的核心托管任务；0.13.0 起提交走 `Execute` 的等待形式、观察走 `Monitor`，事实都在返回值里（CONTRACTS §112）。插件负责 Ark/relay 的请求、状态和计量事实；核心提供任务身份、账号约束、统一调度、共享快照和账务。此前“不让核心拥有任何异步任务概念”的方案未能提供跨节点归属和查询协调，本轮用不带厂商字段的通用契约替代。

插件声明 `platform.adapter.v1`、`platform.tasks.v1` 和 `platform.monitor.v1`；按旧插件协议构建的包被新核心拒绝（CONTRACTS §112.10）。提交时插件在 `Execute` 的返回值里给 `task`；后续查询读核心快照，观察由核心调 `Monitor`。先停止全部旧核心及其后台对账、升级完整集群，再激活此版本；内置插件自动升级和回退限制见 CONTRACTS §28.4。客户端应把提交响应中的 `s2task_` ID 当作不透明字符串，后续原样传回查询端点。

### 5.1 跑起来是这样

```
提交  POST /api/v3/contents/generations/tasks
      核心鉴权 → 调度选本插件账号 → 插件 Execute（task_submission）
      插件构造请求，RelayAndWait：核心持久记录提交意图、请求上游、把有界成功 JSON 交还插件
      插件解析，返回 task：上游 task id、初始 queued 查询快照、按分辨率与时长预估的 Reservation
      核心同事务写任务归属/账号/快照、usage_logs、预扣和 pending_settlements
      核心把提交响应中的上游 ID 换成 s2task_ 公开 ID，再返回客户端
      （插件没返回 task：任务不登记，客户端报错，预扣释放）

轮询  GET /api/v3/contents/generations/tasks/:task_id      billing: free
      核心鉴权、校验任务所属用户及当前分组的模型权限
      直接返回共享快照；不选账号、不使用 RankAccounts、不访问上游

观测  核心认领到期任务 → Monitor（始终使用提交账号）
      → Fetch（核心绑定账号代理和限额）→ 插件解析结果并返回：
          还在跑    → PENDING + task_snapshot_json + next_check_after_sec
          成功      → SETTLED + 真实用量 + task_snapshot_json；无用量则 SETTLED_ESTIMATE
          上游失败  → FAILED + reason + task_snapshot_json
      核心验证认领 token，同事务保存终态快照并补扣/退款；旧持有者迟到响应被拒绝
      核心观测超时不等于上游失败，不自动退款
```

### 5.2 为什么这样是对的

- **钱收得到**：客户端提交完再也不回来查是常态，new-api 正是因此用后台轮询（`fetchMode: "per_task"`）而不是依赖客户端。核对由核心驱动，与客户端来不来无关。
- 提交估计与后续差额结算都由核心事务和幂等账本负责；客户端查询免费，不会触发重复轮询或扣款。
- 视频链路不需要额外 `net`、`app.jobs.v1`、`lock`、`ledger.debit` 或任务表权限。素材库仍有自己的数据库，0.13.0 起它的上游调用由核心发出，插件不再申请 `net`（§17）。
- 余额门禁保留现有允许短时超额的契约，不能把异步预扣宣传成严格并发资金预授权。

### 5.3 历史任务

插件 0.13.0 删除了旧的任务表 `video_tasks`（迁移 `0003_drop_video_tasks.sql`，`DROP TABLE IF EXISTS`；已应用的 `0002_video_tasks.sql` 不改）以及只读它的 `ResolveModel`。转发 V2 之后提交不再写这张表；唯一的读者 `ResolveModel` 只在端点声明 `request.modelSource: "plugin"` 时被核心调用，而线上的 0.12.8 与之前发布过的清单都没有这样声明，所以还在旧版本上的节点也不会读写它，可以直接删表。旧裸 ID 的归属只从核心可信账务恢复。

旧任务首次恢复缺少完整结果时，核心安排统一轮询，查询暂时返回 `task_snapshot_pending`。未知、歧义或其他用户的任务不得退回普通调度；上游结果已超过保留期时明确返回无法继续观测。


---

## 6. 分期计划

| 期 | 范围 | 核心改动 | 产出 |
|---|---|---|---|
| **一期** ✅ | 账号类型 + 对话 / responses / 嵌入（服务内置 openai 平台）、自定义 Base URL、账号测试 | 无 | 可用的「火山方舟」账号，与现有 OpenAI 账号同组调度 |
| **二期** ✅ | `volcengine` 平台 + 同步图片端点（Seedream），张数计量与计费 | 无 | 图片生成可用可计费 |
| **三期** ✅ | 素材库：插件路由 + 火山官方 SDK 做 V4 签名 + `plg_volcengine` 索引 + 控制台页面 | 无 | 自定义素材端点落地（0.3.0） |
| **四期 · 核心** | [插件执行、核心记录](PLUGIN-EXECUTES-CORE-RECORDS.md) 的 A + B + C + D | 是，且都是通用能力 | 插件参与用量与使用记录、预扣费、异步核对 |
| **五期** ✅ | 视频（Seedance）：提交 + 轮询 + 核对结算。真实上游验证见 §12，端点可配与 Anthropic 见 §13 | 无 | 豆包视频可用（0.6.0）；预估从猜变成读，官方 SDK 退场，上游端点可配 |

一到三期覆盖了需求里「合并成一个插件 + 自定义 baseurl + 素材端点」的全部字面要求，可以独立发布。四期是一笔**核心投资**：那四条扩展没有一条带厂商或「任务」语义，做完之后豆包视频只是第一个用户。

---

## 7. 待确认

1. **四期（核心契约扩展）是否现在做**。不做的话一~三期照常能发，视频留到以后；做的话它不只解决豆包视频，是把「插件参与计费与记录」这件事打通。决策点见 [PLUGIN-EXECUTES-CORE-RECORDS.md §8](PLUGIN-EXECUTES-CORE-RECORDS.md)。
2. ~~**端点路径用 `/ark/v3/...` 是否可接受**（`/api` 被核心占用，改不了）。客户端把 Ark SDK 的 `base_url` 设成 `https://本站/ark/v3` 即可。~~ **已解决**：端点改为官方路径 `/api/v3/...`（图片与视频）+ `/doubao/api/v3/...` 别名 + `/v1/contents/...` 别名（仅视频，兼容旧版 Ark API），核心保留段收窄到 `/api/v1`（2026-10-05）。
3. **图片端点要不要同时占用通用的 `/v1/images/generations`**。占了对 OpenAI SDK 更友好，但把一个通用路径绑给了单一厂商插件，以后核心要给内置 openai 平台加图片端点会冲突。**倾向不占**，已确认不加。
4. **素材库是否需要对外 OpenAPI 入口**（new-api 有）。本稿按「只做控制台管理」设计。

---

## 8. 代码出处（便于复核）

**sup2api（约束来源）**
- `next/sdk/manifest/manifest.go` — Platform / Endpoint / StickyRule / UsageRules
- `next/sdk/proto/sub2api/plugin/v1/platform.proto` — `BuildUpstreamRequest` 的入参
- `next/sdk/proto/sub2api/plugin/v1/host.proto` — 插件能向核心要的全部能力（没有账号、没有用量上报）
- `next/sdk/proto/sub2api/plugin/v1/app.proto` — `RunJob`（后台轮询的落点）、`HandleHTTP`
- `next/server/internal/core/ports_plugin.go:70` — `PlatformBinding` 不带 Client
- `next/sdk/manifest/check/validate.go` — 保留路径段、平台权限、端点校验
- `next/server/internal/gateway/sticky.go:296` — 粘性键取值来源
- `next/server/internal/gateway/pipeline.go:246` — 模型从路径参数或请求体读取
- `next/server/internal/gateway/rank.go` — `RankAccounts` 调用时机（可用来把请求钉到指定账号）
- `next/server/internal/usagerules/usagerules.go:93` — facts 提取
- `next/docs/ARCHITECTURE.md` 6.4 / 6.5 / 6.6 / 7.3 / 7.4，`CONTRACTS.md` §6（事件）、§24（RankAccounts）

**new-api（参考，只读）**
- `relay/channel/volcengine/{adaptor,constants,tts}.go`
- `plugins/tasks/doubao/plugin.js`（`meta` 在 244 行，路由在 273 行，提交/查询在 758 / 860 行）
- `constant/channel.go:45,54,116,125`
- `relaykit/dto/channel_settings.go:242`
- `service/assetlib/{upstream,openapi}.go`

**官方文档**
- [创建视频生成任务](https://docs.volcengine.com/docs/ark/create-video-generation-task-api)
- [查询视频生成任务](https://www.volcengine.com/docs/82379/1521309)
- [公共错误码](https://docs.volcengine.com/docs/ark/error-codes)

---

## 9. 实现过程中暴露出来的插件机制问题

都不属于本插件，属于**插件机制**本身。

### 9.1 已解决

| 问题 | 怎么解的 |
|---|---|
| manifest 校验有两份实现（`plugin/pkg` 与 CLI），保留路径清单已分叉 | 校验搬到 `sdk/manifest/check`，`plugin/pkg/validate.go` 缩成转发壳，CLI 直接调同一份；保留路径收敛成 `manifest.CoreRouteSegments` |
| 插件作者 import 不到核心校验（`plugin/pkg` 是 server internal），manifest 没有自动化守卫 | 同上。各插件的 `manifest_test.go` 现在能直接跑核心校验 |
| 内置平台 JSON 在 `server/internal/platforms`，插件够不着 | 移到 `sdk/platforms` |

顺带挖出一个**静默跳过的测试**：一期 `manifest_test.go` 里的 `builtin()` 读内置平台 JSON 时是 `os.IsNotExist → t.Log(skip) → return nil`，所以「与内置 openai 平台交叉校验」那段**一直在空跑**。现已改成硬失败。这类「找不到就跳过」的写法值得在别处也扫一遍。

### 9.2 用量与计费规则的校验缺口（会漏钱）—— **已修复**

二期把 `usage.facts` 真正用起来之后暴露的，原先 `usageRules()` **只校验 `semantics`**，其余一概不看：

| # | 缺口 | 后果 |
|---|---|---|
| 1 | `facts[].path` 不校验是否是合法 gjson 路径 | `"usage..[["` 能装上，运行时静默取不到值。管理员的 `u("images")` 恒为 0，**该计的费一分不计** |
| 2 | `facts[].path` 允许为空 | 该 fact 永远不存在（`usagerules.go:94` 是 `if f.Path != ""` 才提取），但 `u("它")` 能通过价格表达式校验（`billing/pricer.go:134` 只收集键名）。管理员写的表达式恒算 0 |
| 3 | `facts[].type` 不校验 | `"vector"` 能过；`usagerules.go:80` 只认 number/boolean/enum，其余按字符串处理，价格表达式拿到字符串 |
| 4 | `usage.json.map` / `usage.sse[].map` 的**键**不校验 | 写 `"total_tokens"` 能装上。`usagerules.go:69` 的 default 分支把不认识的键**当 metric 塞进 `usage_logs.metrics`** 而不是报错。拼错 `output_tokens` → token 不计费；这个意外的 metric 键又不在 `declaredFacts` 里，`u()` 也取不到，纯粹是看不见的黑洞 |

建议：`map` 的键限定在 `manifest.Usage*` 那 6 个常量内，别的报 `unknown_field`；`facts[].path` 用 gjson 解析校验且不得为空；`type` 限定三选一。

**已于 `sdk/manifest/check/usage.go` 全部实现**，每条正反测试齐备，内置平台 JSON 与 7 个插件全部原样通过（没有为了让规则通过而改资产，也没有为了保住资产而放宽规则）。路径校验的做法是**用同一套语法往返**：`sjson.SetRaw("{}", path, probe)` 物化再 `gjson.Get` 读回——gjson 没有导出的路径校验器，而它对 `usage..[[` 的处理恰恰就是「当成一串永远匹配不上的 key」，这正是要堵的静默失败。

一处**有意的收紧**：gjson 的只读算子（查询 `#(a==1)`、modifier `@this`、管道 `a|b`、通配 `usage.*`、multipath `{a,b}`）在用量路径上被一并拒绝。运行时对结果只做 `Int()/Float()/String()`，只需要单个值；这些算子返回数组或对象，落进 `usage_logs` 就是垃圾。现有资产一个都没用到。

### 9.3 其他核心缺陷

| # | 问题 | 位置 |
|---|---|---|
| 5 | ~~账号类型引用不存在的平台时一声不吭~~ **已修复**：照 hook / rank 的形状加了 `slog.Warn`，绑定照常保留（平台可能来自尚未安装的插件），措辞不断言是拼错 | `plugin/registry/registry.go` |
| 6 | **`endpoint.response` 整个结构体是死声明** —— **已决定：让网关真的读它**（2026-09-29）。两个 agent 独立确认：`Response.Stream` / `Response.NonStream` 在 `server/` 与 `sdk/` 里**零处引用**（除 manifest 定义和测试夹具），是否流式完全由 `request.stream` / `request.streamPath` 决定（`pipeline.go:262`）；走 SSE 还是 JSON 只看上游 `Content-Type`（`forward.go:44`）。后果是真实的：声明了「只有 nonStream」的端点，上游一旦返回 SSE，`usage.json` 规则不生效 → **token 用量完全丢失**，而 `applyFacts` 每个事件都跑，于是按张数收得到钱、按 token 收不到。Ark 图片端点支持 `stream:true`，这是真实场景。落地方案见 §9.6 | `manifest.go:156`、`gateway/forward.go:44`、`gateway/pipeline.go:262` |
| 7 | **插件分类出来的上游错误码到不了客户端**。`ClassifyErrorResponse` 没有 `client_error_code` 字段，`Code` 被硬编码成 `"upstream_error"`；只要插件填了 type 或 message，原始 body 也不透传。于是 Ark 的 `InputTextSensitiveContentDetected`（内容审核）和普通参数错误，到 OpenAI SDK 手里都是 400 + `invalid_request_error` + `code="upstream_error"`，**客户端没法区分「换个 prompt」和「改参数」**。建议加 `client_error_code`，或允许 `passthrough_body=true` | `gateway/dispatch.go:480`、`platform.proto:72` |
| 8 | **`facts[].enum` 运行时完全不认**。`usagerules.go:74` 的 `setMetric` 只区分 boolean / number，`enum` 落进 default 按字符串处理，**从不与 `Enum` 列表比对**。上游返回列表外的值照样写进 `metrics`。校验层现在只能保证「声明 enum 就得非空」，运行时该加一层，否则 `enum` 就是装饰 | `usagerules.go:74` |
| 9 | **`facts[].unit` / `description` 零消费者，管理员看不到可用的 `u()` 键**。全仓库（含 `web/src`）grep `facts` 前端零命中；`declaredFacts` 只收集键名当黑白名单，`expr.Analysis.Facts` 列的是*表达式里用到的* fact 而非可用清单。**fact 这套对管理员目前是盲盒** | `billing/pricer.go:134`、`billing/expr/validate.go:32` |
| 10 | **facts 在每个 SSE 事件上重跑、后写覆盖先写**，manifest 里没有任何办法表达「这个 fact 要累加」或「只看某个事件」（`SSEUsageMap` 有 `event` 过滤，`facts` 没有）。对「每个 chunk 都带、语义是增量」的 fact 会只记最后一个 chunk | `usagerules.go:157,189` |
| 11 | **sticky rule 的 `match.protocols` 指向不存在的协议完全无声**：校验层不查、registry 也不查。与第 5 条同一类缺口，方向是「平台 → 协议」 | `check/validate.go` 的 `stickyRules` |
| 12 | （不是 bug，记一笔）`billing: "free"` 的端点可以同时声明 `usage.facts`，facts 照常写进 `metrics` 但永远不结算。设计上说得通，但没文档说明，容易误以为「声明了 fact 就会计费」 | — |

### 9.4 声明式用量规则表达不了的

Ark 图片响应的 `data[]` 每条带 `size`（如 `"2848x1600"`），**按分辨率分档计价用 facts 表达不出来**——facts 只能取单个 gjson 路径，没法「按 `data[].size` 分组计数」。new-api 正是靠 JS 插件自己算 `images_up_to_1_5k` / `images_above_1_5k` 两档。

这恰好是四期 `ExtractUsage`（插件返回用量）要解决的问题，不是二期的 bug。

### 9.5 尚未解决

**新插件不会被打包**：`deploy/docker/build-go.sh:109` 的 `BUILTIN_PLUGINS` 只有 `anthropic openai gemini moderation`，市场索引走 `tools/sub2api-plugin/scripts/build-demo.sh` 的显式列举。volcengine 写完也进不了镜像和市场，要改这两个脚本。

### 9.6 让网关真的读 `endpoint.response`（2026-09-29 决定）

`endpoint.response` 曾经是纯声明，`usage` 规则却按它的隐含前提工作，结果是**静默吞用量**。决定让网关真的读它，而不是把字段删掉——删掉等于承认「这类不匹配不值得知道」，而它正在悄悄吞钱。

**落地方案**（排在核心 B 期之后做，它要改 `gateway/forward.go` 与 `pipeline.go`，与 B 期冲突）：

1. **仍按上游 `Content-Type` 选转发方式**。不改这一条——因为一个能跑的客户端不该因为 manifest 写漏了就被打断。
2. **拿实际形态与端点声明比对，不一致就出声**：
   - 上游是 SSE，但端点没声明 `response.stream` → `slog.Warn`（带 request_id / protocol / endpoint / plugin），并在 `usage_logs.billing_detail` 里记一个标记（该列是 jsonb、已存在，不需要迁移），例如 `{"response_mismatch":"sse_not_declared"}`
   - 反向（声明了 stream、上游回 JSON）同样记，但危害小
3. **不阻断、不改计费金额**。这一期只做「让它可见」：用量确实丢了，但不要在同一次改动里既改可观测性又改计费口径。
4. `request.stream` / `streamPath` 与 `response.stream` 的关系要在代码注释里写清楚：前者是**客户端请求的意图**，后者是**端点承诺的响应形态**，两者都存在是合理的，不要合并。

这样 `response` 从「刚被加上必填却买不到任何保障」变成「写错了会被抓到」。真正的修复（补 `usage.sse` 规则）仍然在插件作者手上，但至少他会知道要补。

### 9.7 杂项清理轮（排期）

前面几轮攒下的小项，集中一轮做掉，不各占一个轮次。按**文件冲突**分成两批——`server` 这个 Go 模块同一时间只让一个 agent 写，否则彼此的 `go build ./...` 会看到对方的半成品，制造假失败。

**第一批（核心 B 期之后，与 §9.6 同一个 agent 或紧随其后）**——需要改 proto 或 `sdk/manifest/check`，与 B 期冲突：

| # | 项 | 位置 |
|---|---|---|
| 1 | `ClassifyErrorResponse` 增加 `client_error_code`（或 `passthrough_body`）。现在 `Code` 硬编码 `"upstream_error"`，插件分类出的上游错误码到不了客户端，Ark 的内容审核拦截和普通参数错误在 OpenAI SDK 眼里一模一样 | `platform.proto`、`gateway/dispatch.go:480` |
| 2 | sticky rule 的 `match.protocols` 指向不存在的协议完全无声（校验层不查、registry 也不查）。与账号类型那条同类，方向是「平台 → 协议」 | `check/validate.go` 的 `stickyRules`、`registry.go` |
| 3 | `CheckPlatform` 被内置平台与插件平台共用，但内置平台走 JSON、没有 manifest 包裹，`endpoint()` 里任何依赖 `v.m` 的检查在内置平台上空转。现在没有这种检查，但是个静默陷阱——注释写明，或给内置平台造一个假 manifest | `check/validate.go` |

**第二批（与第一批不冲突，可另起一轮）**——运行时与工程侧：

| # | 项 | 位置 |
|---|---|---|
| 4 | `facts[].enum` 运行时完全不认：`setMetric` 只区分 boolean/number，enum 落进 default 按字符串处理，从不与 `Enum` 列表比对 | `usagerules.go:74` |
| 5 | `usagerules.Path` 对求和项 TrimSpace、对单路径不 Trim，两边不一致 | `usagerules.go:104` |
| 6 | 运行时取到的 `input_tokens` 等标准字段若不是数字，至少 warn 一次（现在静默为 0，与键拼错同类，只是换了一层） | `usagerules.go` |
| 7 | 新插件进不了镜像和市场：`BUILTIN_PLUGINS` 只有四个，市场索引走 `build-demo.sh` 的显式列举 | `deploy/docker/build-go.sh:109`、`tools/sub2api-plugin/scripts/build-demo.sh` |
| 8 | `TestTwoNodeRollout` 既有 flaky：`waitFor` 只等 DB 里插件变 enabled，没等两个节点各自 publish 完。在干净 HEAD 上可复现 | `plugin/rollout/rollout_test.go:291` |
| 9 | 保留路径只收敛了一半：`/api/v1`、`/plugin-ui/` 在三处仍是字面量 | `httpapi/router.go:27`、`plugin/routes/routes.go:65`、`registry/package.go:50` |
| 10 | **扫一遍「找不到就跳过」的测试**。插件 `manifest_test.go` 里那段交叉校验因为 `os.IsNotExist → t.Log → return nil`，在内置平台搬家后**一直空跑且显示为绿**。同类写法值得全仓排查 | 全仓 |

**暂不做**（记录在案，不排期）：

- `plugin/pkg` 现在是混合包（一半 SDK 转发壳、一半服务端逻辑）。终局是 18 个 importer 直接 import `sdk/manifest/check`，`plugin/pkg` 只留 `core.Error` 包装——收益是消除「可以往转发壳里加规则」的误导，代价是动 18 个文件的 import
- `ValidateOptions.Tooling` 是策略布尔塞进纯规则包，下次有人想「只跳过二进制不跳过 hostCompat」就会加第二个布尔。滑坡起点，但现在能用
- `facts` 的「累加」与「只看某个事件」语义缺失（`SSEUsageMap` 有 `event` 过滤，`facts` 没有）。这属于能力缺口而非 bug，且四期 `ExtractUsage` 落地后插件可以自己算，优先级下降

### 9.8 静默跳过测试的排查结果（2026-09-29）

起因是插件 `manifest_test.go` 里那段跨模块读内置平台 JSON、`os.IsNotExist → t.Log → return nil` 的交叉校验空跑了很久。全仓排查后两个结论**都和原先的假设不同**：

**结论一：`next/` 没有任何 CI。** `.github/workflows/` 的四个 workflow（`backend-ci` / `cla` / `release` / `security-scan`）没有一个引用 `next/`，`backend-ci.yml` 的 test job 跑的是 `backend/` 的 `make test-unit`。所以「CI 一直是绿的」这个说法本身不成立——**next 的绿全部来自本地**。这是那处空跑能潜伏下来的真正原因，比空跑本身严重。

**结论二：单元测试里现在一处空转都没有。** `sdk/` `plugins/*` `tools/` `server/` 四个模块全量 `go test -v` 逐条核对过 SKIP 输出，全部来自 `TEST_DATABASE_URL`（约 70 条）、Windows 平台（1 条）、`S2P_DEMO_DIR`（1 条）三个合理来源，没有一条无声 `return`。那次事故是**唯一**的同形写法，已随平台搬家一并修好。

**结论三：整个 `next/e2e` 是 0 断言状态**，而且是双重的：

1. `e2e/env.go:45` 的 `E2E_BASE_URL` 默认值是 `http://127.0.0.1:3120`，而 3120 那套部署**已于 2026-09-27 删除**（PROGRESS §138、§144 有记录）。ping 不通 → `Setup()` 整条用例 `t.Skipf`。
2. 即使换成 `:3130` 能连通，`env.go:115` 的 `Pending()` 有 **24 个调用点，覆盖 AC02–AC23 全部用例**，注释写着「等某某模块合并后删掉」，但它们等的模块（a1-identity、gateway、b-billing、guard、round 3/4…）**早就合并了**（PROGRESS 第 8 节记录第 8 轮 17 条验收全部通过）。不加 `E2E_RUN_PENDING=1` 就只剩 `TestAC01` 会跑。

**追加进 §9.7 第二批的三处**：

| # | 项 | 位置 |
|---|---|---|
| 11 | 唯一一处与事故同形的 `t.Log + return`：市场索引签名校验，`/market/index.json.sig` 404 且索引为空时直接 return，后面的 ed25519 校验、`dev-official.pub` 校验、每个插件版本的下载全部不执行且显示 PASS。**市场索引为空本身就该是失败**（测试环境是内置签名市场，空索引说明构建链断了） | `e2e/ac01_infra_test.go:72` |
| 12 | `E2E_DOCKER_HOST` 未设时，CONTRACTS §21.2 的审计行断言整段不执行、用例仍 PASS。应拆成子测试并首行 `RequireDocker()`，让跳过出现在输出里 | `e2e/ac23_ownership_test.go:331` |
| 13 | **定时炸弹**：核心一旦注册第一对协议转换器，这条 `t.Skipf` 会自动生效，把 503 + 错误体格式 + 「未调用上游账号」三段断言一并吞掉，且是函数中途 skip，前半段已 PASS 的断言会被 SKIP 状态盖掉。应改成分支断言：有转换器断言 200 且走了转换，没有断言 503 | `e2e/ac19_builtin_platforms_test.go:77` |
| 14 | `e2e` 的默认目标与陈旧 `Pending()`：默认值改成当前部署或干脆不给默认值（不设就明确报错）；清掉已合并模块的 `Pending()` | `e2e/env.go:45,115` + 24 个调用点 |

**需要单独决策**：`next/` 要不要加 CI。哪怕只跑不依赖外部资源的单测，也能把「本地绿」变成「可验证的绿」。

### 9.9 e2e 复活的真正前置条件（2026-09-29 更正）

上一节说「把 `E2E_BASE_URL` 默认值改掉就能复活 e2e」——**这个判断是错的**。

缺的不是一个 URL，是**整套目标拓扑**。`next/deploy/README.md:15` 自己写着：3120 那套「两节点 + Caddy」的栈已于 2026-09-27 删除，**e2e 仍然按那个布局编写，需要新的目标环境才能再跑**。具体地：`deploy/compose.yml`、`deploy/caddy/`、`deploy/scripts/` 都已不存在；现在只剩 `deploy/single/compose.yml`，两个节点各自直接发布 3130/3131，**没有 Caddy、没有 mock-upstream、没有 `/__node1` `/__node2` `/__mock` 三条辅助路由**。

而 e2e 的 `E2E_MOCK_URL`（默认 `$BASE/__mock`）、`E2E_NODE_URLS`（默认 `$BASE/__node1,...`）、`docker.go`、`mock.go` 全建立在那套拓扑上。所以就算把默认值改成 3130，e2e 仍然是 0 断言——只是从「ping 不通」换成「`/__mock` 404」。

**本轮做到的**：去掉误导性的默认值（不设 `E2E_BASE_URL` 直接 `t.Fatal` 并给出指引）、删掉 23 个陈旧 `Pending()`（逐条核实过对应模块确已落地）、修掉三处会静默吞断言的写法。**没做到的**：让 e2e 真的跑起来。

**下一步需要决策**：e2e 要对着什么跑？

| 方案 | 代价 |
|---|---|
| 重建被删掉的 Caddy 拓扑 | 撤销了 2026-09-27 那次删除决定 |
| 把 e2e 改造成对着 `deploy/single/compose.yml` 跑 | 要重写 `E2E_NODE_URLS` / `E2E_MOCK_URL` 的取值方式，并把 mock-upstream 作为服务加进 single 栈 |
| 起一套独立的 e2e 专用栈 | 最干净，但要新写 compose 与脚本 |

**不能对着线上的 sup2api 栈跑**——e2e 会建用户、账号、扣费。

### 9.10 本轮顺手修掉的一个仓库级陷阱

`.gitignore` 里曾有一条裸 `scripts`，它会忽略树里**任何**叫 `scripts` 的目录。CI 脚本最初写进 `.github/scripts/` 时 git 直接当不存在，连 `git status` 都不显示。而且这不是第一次踩——`next/tools/sub2api-plugin/.gitignore` 里留着 `# The repository root ignores "scripts"; keep this tool's scripts.` + `!/scripts/`。

已收窄成 `/scripts/`（只忽略仓库根的那个）。改动前量过影响面：树里现存的 `scripts` 目录没有任何被静默忽略的文件（`node_modules` 下那几个另有 `node_modules/` 规则兜住），所以这次收窄**没有让任何意外文件变成待跟踪**。

### 9.11 前端杂项第二批的落地与三处判断更正（2026-09-30）

§9.7 里前端侧的三项（`DeclarativeTable` 搜索框、图标名、`/api/v1` 字面量）已落地（`c8a4daf66`）。做的时候发现我原来的描述有三处不准，记下来免得下次按错的前提排活：

**1. 搜索框不是「死的」，是随数据量在能用和不能用之间切换。** `serverPaged = total > items.length` 是个启发式：一页装得下时 `serverPaged=false`，客户端过滤**真的生效**；数据一超过一页就变 `true`，同一个框立刻变哑。比一直死更难发现。

**2. 「没接后端参数 / 后端不认」两个选项都不对。** 核心是**全量转发** query 给插件的（`plugin/routes/routes.go` 把 `URL.Query()` 整个交出去），`anthropic` 的 `/models` 确实实现了 `?q=`（ILIKE，有测试）。真问题是三个 declarative table 页里**只有一个**认 `q`：volcengine 的 `GET /assets`、`GET /asset-groups` 只认 `index_status` / `group_id`。所以「接通」会让 2/3 的页面**返回全量却显示成搜索结果**——比空白更糟的谎。删掉的理由是这个。

（2026-11-19：声明式表格页已从平台删除，这条排期作废，见 §19。）真正的修法（**排期**）：`manifest.Page` 加一条「这个 source 认哪个查询参数」的声明（如 `"search": "q"`），`check` 校验参数名；前端**声明了才渲染搜索框**，输入防抖发 `q=` 并回第 1 页。之后 volcengine 两个 assets 路由要么实现 `q` 要么不声明。顺带 `serverPaged` 也该从启发式改成声明式——服务端分页开没开应来自声明，不该靠比较 `total > len(items)` 猜。

**3. 图标名写错的后果比「空白」严重。** 渲染不存在的图标**不空白、不报错、零控制台输出**，画一个 `M5 5h14v14H5z` 的空心方块——和真图标 `stop`（`M6 6h12v12H6z`）几乎一模一样。manifest 里写错名字**不像坏了，像故意的**。现在：名字表抽到 `packages/ui/src/icons.ts`（导出 `ICON_NAMES` / `hasIcon`），兜底换成方框加问号（没有任何真图标长这样），解析不到时 `console.warn` 一次并指名插件与菜单。

`anthropic` 的 `list` **不是名字写错，是图标集缺了一个**：模型目录菜单本来就该是列表图标，现存 55 个里没有合适替代（`ledger` 是账本、`inbox` 是收件箱、`menu` 是汉堡）。加了 `list` 图标，manifest 不改。

图标名静态校验（**排期**）：`icons.ts` 是纯数据无浏览器依赖，加一个 `npm run icons:json` 用 `node --experimental-strip-types` 导出到 `sdk/manifest/check/icons.json`，Go 侧 `embed` 做校验。**两个 icon 字段是两套词汇表，校验层千万别搞混**：顶层 `manifest.icon` 是 `text:<1-2字>` / 包内相对路径 / URL / `data:`（`PluginAvatar.vue` 渲染，已有兜底，七个插件全是 `text:X`）；**只有 `ui.menus[].icon`** 是 SIcon 名字，该对 `ICON_NAMES` 校验。

**4. `/plugin-ui/` 在前端生产代码里一处硬编码都没有**——插件资源 URL 全来自服务端下发的 `UIPlugin.asset_base`，字面量只在 mock fixture 和 vite dev proxy 里。§9.7 第 9 项的前端部分比描述的小得多。`/api/v1` 收敛到 `packages/host/src/routes.ts`（镜像 `sdk/manifest/routes.go`）。

其中一处**不是整洁问题，是安全边界**：`PluginIframe.vue` 里 `/api/v1` 出现三次——建沙箱 iframe 的路径白名单前缀（拦住插件调 `/api/v1/users` 的那道门）、`'/api/v1'.length` 剥前缀、api client 再用**可被 `configureHttp` 改的** `baseURL` 加回去。三个独立的值，**只要一个不一致，白名单校验的路径就和真正发出的路径脱钩**。今天恰好一致，所以不是活漏洞，是雷。现在全部从 `apiBase()` 推导，`src/host.ts` 那个能改掉其中一个值的冗余覆盖也删了。

**5. 一个仓库级陷阱**：`next/server/web/dist/index.html` 是**被 git 跟踪**的占位文件（给 `//go:embed all:dist` 兜底，Docker 的 `ui` stage 自己 build），而同目录 `assets/` 被 `.gitignore` 的 `dist/` 忽略。每次 `npm run build` 都会把它覆盖成引用一堆 ignored 文件的真 index.html——一旦提交，仓库里就有个指向不存在资源的 index.html。**提交前必须确认它不在 diff 里**。

---

## 10. 三期（素材库）落地记录与代价

10 个 Action、13 条 admin 路由、`plg_volcengine` 本地索引、四个宿主渲染页面（2 table + 2 form），版本 0.3.0。**没有 `ui.native`**，所以不需要 `hostUICompat`，也不用付 Critical 权限。

新增权限五条：`accounts.read`(M)、`db.schema`(H)、`routes.admin`(M)、`ui.menu`(M)、`net`(H, optional，`scope.domains` 只到 `*.volcengineapi.com`)。（`net` 在 0.13.0 删除，§17。）

### 10.1 签名是重算验证过的，不是"能编译"

测试从收到的请求**独立重建**火山 V4 canonical request 并自己推 signing key 链，与 `Authorization` 里的 Signature 逐字节比对；再用「换 SK / 换 region / 换 body 签名都必须变」证明它是签名而不是装饰；10 个 Action 全跑一遍都验签。另有 Action 白名单测试：路由无法诱使插件拿账号的 AK/SK 去签任意控制面调用（如 `DeleteEndpoint`），断言上游收到 0 个请求。

出网全程经注入的拨号器（生产里就是 `egress.DialContext`），`dial == nil` 时直接报错，**绝不回落到直连 socket**。（0.13.0 起插件不再联网，见 §17。）

### 10.2 本地索引与上游不一致的处理

上游是唯一真相，本地只是索引（存在的唯一理由是列表页不必对每个账号各发一次签名调用、各读一次**会写审计**的凭证）。

| 情况 | 处理 |
|---|---|
| 创建：上游成功、索引写失败 | **补偿删除上游**；补偿也失败 → 500 并在消息里给出上游 id（唯一能手工清理的线索）+ error 日志 |
| 创建重试 | `ON CONFLICT (account_id, upstream_id) DO UPDATE`，同一上游 id 不会产生两行 |
| 删除：上游 NotFound | 视为成功，删本地行 |
| 删除：上游其他错误 | **不动索引**并返回错误——丢掉这一行就丢掉了仍存在于上游的资源的唯一指针 |
| 读/改：上游 NotFound | 标 `index_status='missing'`，**行保留不删**（悄悄消失的行和 bug 无法区分），可按此过滤、仍可删除 |
| 字段回写 | 读上游结果用 `CASE WHEN <> ''`（上游没带的字段不得清空索引）；管理员 PATCH 用 `coalesce($n, col)`（提交空串就是要清空） |

凭证每请求每账号最多读一次（请求级缓存），明文不落库、不进日志、不进响应。

### 10.3 火山官方 SDK 的真实代价（需要决策）

指令要求用官方 SDK、禁止手写签名。它能用，但代价比预想的大：

1. **它一度打断了整个 workspace 的构建。** `volcengine-go-sdk` 与 `volc-sdk-golang` 是 `go 1.14` / `go 1.4` 的**未剪枝模块**，传递依赖把**拆分前的单体 `genproto`** 拖进模块图，于是 `go.work` 里**每一个模块**都出现 `ambiguous import: google.golang.org/genproto/googleapis/rpc/status`。已修（在插件自己的 go.mod 里把单体 genproto 顶到拆分之后的版本），代价是 `next/go.work.sum` 多 2 行。
2. **`universal.DoCall` 收不到 `context.Context`**（`DoCallWithType` 也没有）。控制台请求被取消时，**在飞的上游调用无法中止**。现状是调用前查 `ctx.Err()` + 把 deadline 折算成 `http.Client.Timeout`（无 deadline 时 30s）——能做到的最好程度，不是真正的取消。
3. 引入 5 个模块与一张旧模块图。

**替代方案**：自写 V4 签名约 40 行，而且**测试里已经有一份独立实现**（就是上面用来重算验证的那份）。换过去可以一次性去掉上面三条。

代价已经付过且验证通过，所以不急；但 `ctx` 不可取消是持续存在的缺陷。**保留官方 SDK 还是换自写签名，需要决策。**

### 10.4 宿主渲染页面的能力缺口（插件机制）

- **`type: "table"` 的页面是只读的**：`DeclarativeTable.vue` 只有搜索框、刷新、分页，**没有任何行操作**，`manifest.Page` 也没有 `actions` 字段。所以"素材库能在控制台管理"靠 table 做不到，只能再加 `form` 页把创建路径补上。
- **`form` 页里的关联 id 只能手填数字**：schema 表单没有「选项来自插件路由」的能力，所以 `account_id` / `group_id` 要管理员自己抄。体验很糙。正确答案是给声明式表单加一个「选项来自插件路由」的 widget，或者做 native 页——不是现在这样凑。
- （0.13.0 已解决：声明式 table、form 页从平台删除，素材库两个页面改为原生页，新建并进列表页的弹窗，§19。）

### 10.5 又两个"声明了但没人管"的形状

| 问题 | 证据 |
|---|---|
| **服务端分页的表格，搜索框完全是死的**：`DeclarativeTable.vue:33` 在 `serverPaged` 为真时直接返回未过滤的行，而 `:56` 的 watch 只监听 `page`/`pageSize`——`q` **既不过滤也不请求**。anthropic 的 `/models` 后端支持 `?q=` 却永远收不到 | `web/src/components/plugin/DeclarativeTable.vue:33,56` |
| **manifest 的菜单图标名没有任何校验**，写错就静默渲染成空方块。实测 `anthropic/manifest.json` 的 `"icon": "list"` **不在** `SIcon.vue` 的 51 个图标里 | 已用脚本比对全部插件 manifest 与图标表 |

### 10.6 契约缺口：插件无法声明「我需要某个 HostService RPC」

三期依赖 §26.6 新增的 `ListAccounts` / `GetAccountCredentials`，但它们落地时 `hostApiVersion` 仍是 1、核心版本仍是 `0.1.0-dev`。`hostCompat` 最细只能表达 `>=0.1.0 <0.2.0`，所以**装在一个还没有这两个 RPC 的 0.1.0 节点上，manifest 校验会通过**，运行时才以 `Unimplemented` 失败（插件把它兜成了可读的 503）。

要真正修，核心在新增 HostService RPC 时需要一个可声明的版本或能力信号。

---

## 11. 五期收尾：预估从「猜」变成「读」，官方 SDK 退场（2026-09-30，0.5.0）

### 11.1 Ark Seedance 事实表（对账时会反复用到）

token = **帧数 × 输出宽 × 输出高 / 1024**，固定 24fps。以下每一条都核对过官方文档，**其中好几条推翻了插件原来的假设**。⚠️ **N 秒的片子是 N×24+1 帧，不是 N×24**——官方公式漏了这一帧，是真实上游实测出来的（§12.2）：

| 事实 | 说明 |
|---|---|
| **参数有两种等价写法** | `resolution` / `ratio` / `duration` / `frames` / `seed` / `camera_fixed` / `watermark` 七个参数，除了顶层字段，**也可以写成 `--rs 1080p --dur 30` 挂在 `content[].text` 后面，全系模型都认**，而且**官方从未说明两种写法的优先级** |
| `frames` 优先于 `duration` | 且它是**帧数**不是秒（133 帧 = 5.54 秒）。查询接口回显的 `duration` 是 `frames/24` **向下取整**，**不能用来反推 token 对账** |
| `duration: -1` 是 2.5 的**文档化默认值** | 语义是「模型自己在区间内选」。2.0 系列 / 1.0 pro **连默认值都没有文档** |
| **4k 只有 `doubao-seedance-2-0-260128`** | **2.5 反而不支持 4k**，上限 1080p。全套文档**没有 `2k`**。2.0 fast/mini 只到 720p |
| 时长上界 | 2.5 = 30s，2.0 系列 = 15s，1.0 pro = 12s |
| **像素随比例与代际都变** | 1.0 系列 720p 16:9 是 **1248×704** 不是 1280×720；1080p 21:9 是 2206×946 = 208.7 万像素，**比 16:9 的 207.4 万还多** |
| **「21:9 最贵」不能跨档套用** | 1.0 系列 720p 最贵是 21:9，**1080p 最便宜也是 21:9** |
| `seedance-1-0-lite` **已下线** | 现行文档里只有 6 个 Seedance id |

原来的 `resolutionPixels` 写死 16:9 且不分代际，所以**两个方向同时错**：对 21:9 低估，对 1.0 系列 720p 高估。

### 11.2 预估的语义：缺省、读到、读不全

> 起初是三档（另有“字段在 `fields_omitted`：客户端发了、主机没搬 → 用该模型的最贵档”）。转发 V2 收尾（2026-11-18）起核心把声明的请求字段整个交给插件，不再有单值 4 KiB / 总量 32 KiB 的预算（CONTRACTS §25.6、§112.16），`fields_omitted` 随之从协议删除，那一档不存在了。

| 情况 | 含义 | 做法 |
|---|---|---|
| 字段**不在** `fields` | 客户端没发 | 用 Ark **文档化的默认值**（这是事实，不是猜） |
| 字段在 `fields` 但读不懂（例如不认识的分辨率档位） | 客户端发了、插件不认识 | **绝不用默认档**，用该模型的**最贵档** |
| `content.#` > 3 | prompt 没读全（第 4 个及以后的元素不在声明的路径里） | **四个参数全部作废**，整条按模型最坏情况 |

第三行最容易漏：因为两种写法没有优先级，**prompt 读不全时连已经读到的结构化字段也不可信**，哪怕它写着 480p。实现第一版就在这里写错过（只 bound 了 duration、让读不到的 resolution 掉回默认档），结果「全部 absent」和「全部读不到」估出完全相同的数字，被测试抓出来。

声明的路径（当时的顺序是预算优先级：四个短标量排在 prompt 前面，免得一条 4 KiB 的提示词把决定价格的字段挤掉；预算删除后顺序不再有作用）：

```json
"usageRequestFields": ["resolution","ratio","duration","frames","content.#",
                       "content.0.text","content.1.text","content.2.text"]
```

`content.#.text` 在契约里**不可表达**（`ValidUsagePath` 只收单值路径），所以靠 `content.#`（数组长度）判断有没有看不见的元素。见 CONTRACTS §25.7 第 2 条。

### 11.3 预扣金额的变化

| 请求 | 旧预扣 | 新预扣 |
|---|---|---|
| 2.0，明确 1080p / 16:9 / 5s | 1,944,000 | **243,000**（÷8） |
| 2.5，明确 720p / 16:9 / 5s | 486,000 | **108,000**（÷4.5） |
| 2.0，什么都不写 | 1,944,000 | **326,041**（文档默认 720p + 15s 上界） |
| **2.5，什么都不写** | 486,000 | **652,083**（×1.34，见下） |
| prompt 读不全 | — | 模型最坏情况（2.0 = 4k × 15s） |

**一处藏在常量里的产品决策，已定（2026-09-30）：接受上界，靠核对退回。** `duration: -1` 是 2.5 的默认值，所以一条不写时长的 2.5 提交按 **30 秒**预扣 ≈ 45 元，而它多半是 5 秒的活（≈7.6 元），预扣即小额余额用户提交时要跨过的门槛。

**决定保留上界的理由**：预扣是预估，核对会结算——猜高的代价是一段临时占用的余额，Ark 一报真实用量差额就退回；猜低的代价是一个 30 秒的视频渲染在一个只付得起 5 秒的余额上。任何更低的数字都是上游不支持的数字。另外两个出口（加「未知时长按 N 秒预估」的配置旋钮；核心支持「预扣 X、上限 Y」）都没做——前者等于把一个没有依据的秒数搬到配置里，后者是核心契约改动，没有第二个用例之前不值得开。

这条与 §25.4 的 `max_reconcile_age_sec` 是同一形状：**一个看起来像占位符的常量其实是产品决策**，所以 `videospec.go` 的文件头注释里写明了它是定过的，免得下一个人当成随手填的数字调低。

### 11.4 结构性低估（提交时无法修正）

2.5 的 edit / extend / 参考视频任务，官方公式把**输入视频时长**也算进 token，而输入是 URL 或 asset id，**提交时看不到长度**；且这三类任务的输出比例是「与输入素材一致」，可能不在那 6 个比例内，**官方没给对应像素值**，所以「取该档最大面积」对它们不是严格上界。只能靠核对循环的真实 `completion_tokens` 补扣 —— 但如果以后要做「提交时给用户报价」，这里报不出来。

### 11.5 §10.3 结案：官方 SDK 已移除

换成约 120 行自写 V4 签名。收益四条：

1. **`ctx` 真正可取消**。旧实现下控制台请求取消后，`DoCall` 仍占一个 goroutine 和一个 socket 最多 30 秒，而这个插件的 manifest 只给了 **`maxOpenFiles: 128`** —— 反复刷素材列表足以打满 fd，连带数据库一起挂。新测试**两头都断言**：调用方拿到 `Canceled`，**且上游 handler 确认自己的 `r.Context()` 被取消了**（socket 真的断了，不是把请求丢在那儿跑）。
2. **不再重试非幂等的 `Create*`**（§10.3 漏记的一条代价）。旧的 `WithMaxRetries(2)` 会重试 `CreateAssetGroup` / `CreateAsset`，重试一次就在上游多建一个资源，而创建路由只拿得到最后一次尝试的 id ——「上游成功但索引写失败就补偿删除」这条设计**永远够不到第一个**。
3. **go.mod 少 6 个模块**，含 §10.3 当初为压住歧义导入而 pin 的**单体 `google.golang.org/genproto`**（`go mod why` 确认主模块根本不需要它）。那笔 workspace 污染债一并还清。
4. 顺带补了非 JSON 响应的处理（中间代理的 HTML 502、空 body、200 里塞数组），旧代码对这些只会给一个含糊的 `InternalError`。

> 0.13.0（转发 V2 收尾）起素材库调用不再有自己的总时限：原来每个 Action（含重试）最多 30 秒（`assetCallTimeout`），传输的 TLS 握手最多 10 秒，现在都随发起它的控制台请求的 context 结束（AGENTS.md、CONTRACTS §112.16 不留写死的超时）；之后改由核心发出（§17），每次发送受核心 `SendAccountRequest` 已有的单次 30 秒约束。重试（传输错误、429、5xx，最多 3 次，间隔 200 毫秒，`Create*` 不重试）不是为配合那个时限而设的，保留。

**正确性风险由 golden vector 消除**：在删掉 SDK **之前**，先用官方 SDK v1.2.54 真的发出两个请求并把整个请求逐字段冻结（含它随机生成的 `X-Sdk-Invocation-Id`），自写签名对同样输入必须产出**逐字节相同**的 `Authorization`。**这批向量一旦需要重新生成，必须再从官方 SDK 取 —— 用 `signV4` 重算会让测试变成同义反复**（测试文件头写明了这一点和抓取方法）。

> 主控独立复核过一次：从 Go 模块缓存取 `volc-sdk-golang@v1.0.23`（SDK 签名的实际实现），用同样输入独立签名，两个签名 `527a9b6e…` / `3d61934c…`、payload hash 与 SignedHeaders 列表与提交的 golden vector **逐字节相同**。向量的出处是真的。

抓取过程立刻暴露三个**照文档写一定会写错**的细节：

- **签名的 header 集合不是固定列表**，是 `Content-Type` / `Content-Md5` / `Host` / `X-Security-Token` **加上所有 `X-` 开头的 header**。SDK 自己就多签了两个 `X-Sdk-*`
- **`Content-Type` 实际发的是 `application/json; charset=utf-8`**（`universal.DoCall` 明明 `Set` 的是 `application/json`，被下游 handler 覆盖了），而它**参与签名**
- **Host 带 80/443 时签名去端口，其他端口保留**；路径分段用 RFC 3986 unreserved 集，**`url.PathEscape` 不等价**（它放过 `$&+,:;=@`）

为保证独立性，原有那份从收到的请求独立重算的 `verifySignature` **不共用生产代码的原语**（共用的话原语出错会两边抵消，测试照过）。

### 11.6 其余

- 素材库两个路由支持 `?q=`（ILIKE 匹配操作员在表格里看得见的列，`count(*)` 走同一个 where，否则会翻页翻到已被过滤掉的行），并**转义 LIKE 元字符**（`\` `%` `_`）。`anthropic` 的 `/models?q=` **没有转义** —— `?q=%` 返回全量目录并显示成搜索结果，`?q=a_c` 命中 `abc`，与「搜索框返回全量」是同一类静默答非所问。`moderation` 本来就是正确写法。**已另开任务修 anthropic**，也可考虑把 `LikeTerm` 提到 `pluginsdk`。
- 两个 table 页已声明 `"search": "q"`（`manifest.Page.Search`）。核心的 check 能验名字合法、不撞 `page`/`page_size`、`source` 真实存在，但**「路由到底认不认这个名字」只有插件自己的测试能证明** —— `manifest_test.go` 断言声明与 `volcengine.SearchParam` 一致，分叉就红。
- `est_tokens` **保留列、保留写入、停止读取**。不删是因为插件迁移 checksum 不可变（见 CONTRACTS §25.7 末尾）；不「停止读写」是因为该列 `NOT NULL DEFAULT 0` 没有「未知」值，停写会让每一行新记录都声称预扣 0 —— 那正是「整列变成噪音」。它现在记录的是**从客户端真实请求算出来的**预扣数字，是被质疑计费时第一手的证据。
- `profileOf` 是子串匹配，Ark 的 Endpoint ID（`ep-...`）会落进 unknown 画像。管理员若用 Endpoint ID 注册模型，估算会退化 —— 但只在请求没写 resolution 时才用到这个默认值。
- 估算凡是 bound 了东西都会打一条 `assumed` INFO 日志（哪几项被兜底、算出多少 token）。用户问「为什么扣这么多」时先看这条。
- 版本 **0.4.0 → 0.5.0**。

---

## 12. 第一次真实上游验证（2026-09-30，0.5.1）

> **注**：本节提到的 `/doubao/api/v3` 别名路径已在后续版本（2026-10-05）移除，现在只保留官方 `/api/v3` 和旧版 `/v1/contents` 两组路径。

§1.1（HANDOVER）说「没有任何东西被真实上游验证过」，这一节是它的第一次例外。**只覆盖视频链路**；图片没跑通（上游的问题），素材库没测（缺凭证）。

### 12.1 环境与路径

| 项 | 值 |
|---|---|
| 上游 | 一个 Seedance 中转站 `https://cdn.api.codingplus.ai`，**不是官方 Ark** |
| 账号 `base_url` | `https://cdn.api.codingplus.ai/doubao` —— 中转站把 Ark **原生视频**路径挂在 `/doubao` 前缀下，插件拼上 `/api/v3/...` 后原样可用，**这一期不需要改插件**。`NormalizeBaseURL` 只去尾部 `/api/v3`，路径前缀保留。**⚠️ 2026-09-30 补测更正：`/doubao` 下只有视频**，`chat/completions`、`embeddings`、`messages` 在 `/doubao/api/v3/` 下全都**不存在**（返回中转站 SPA 的 HTML + 200）。所以「整个 Ark 面都在 `/doubao` 下」这个当时的理解是错的，只有视频成立。文本面在**根**上的 `/v1/*`。这条促成了 §13 的两个前缀设置 |
| 中转站根路径 | 是 new-api 风格（`/v1/models`、`/v1/video/generations`，顶层 `prompt`），**Ark 原生路径在根上全是 404**。第一次探测以为插件对接不了，是用户指出 `/doubao` 前缀才打通 |
| 部署 | ovh `single` 栈，`2cc0371a3`。volcengine 以**内建只安装**身份进镜像（CONTRACTS §26.8），部署后 `status=installed, builtin=true, active_version=null`，管理员启用后 `enabled 0.5.0`。**这是插件第一次被安装：`plg_volcengine` 的三张表建出来了，0001/0002 迁移第一次执行** |
| 中转站 `/v1/models` | 只列 4 个：`doubao-seedance-2-0-260128`、`-2-0-fast-260128`、`-2-0-mini-260615`、`doubao-seedance-2-5-260628` |

### 12.2 视频：提交 → 预扣 → 核对 → 补扣，全链路通

请求（经网关，用户 API Key）：`doubao-seedance-2-0-mini-260615`，`480p`，`16:9`，`duration: 4`。测试价 `per_token c = 10`（每百万输出 token 10 美元）。

| 步骤 | 实测 |
|---|---|
| 提交 | 200，2.7 s。中转站回 `{"created_at","id","model","status":"queued"}`，**任务 id 形如 `task_…` 而不是 Ark 的 `cgt-…`**，且多带了字段。插件按 `id` 取，兼容 |
| 预扣 | `usage_logs.billing_status=reserved`，`output_tokens=40176`，0.40176 美元，`metrics.resolution=480p`；`pending_settlements` 一条，`ref_id` = 任务 id，`deadline` = 7 天；插件 `video_tasks.est_tokens=40176`，与核心一致 |
| 核对 | 约 2.5 分钟后上游 `succeeded`，核心第 4 次核对拿到结果，`state=settled` |
| 结算 | `billing_status=billed`，**`output_tokens=40594`**，0.40594 美元。账本：预扣 −0.40176（幂等键 `usage:<req>`）+ 补扣 −0.00418（`usage:<req>:reconcile`），余额 10 → 9.59406，**分毫不差** |
| 免费查询 | `GET /ark/v3/contents/generations/tasks/:id` 每次 200，记为 `free`，不影响结算 |

**实测推翻的一条事实（已修，0.5.1）**：上游的 `completion_tokens = 40594 = floor(97 × 864×496 / 1024)`。按 §11.1 的「秒 × 24」是 96 帧 → 40176；**96 帧下没有任何像素面积能整除出 40594，97 帧的 864×496 正好**。所以 Ark 实际计 **N×24+1 帧**（与视频扩散模型惯用的 24n+1 帧一致），官方公式漏了一帧。

- 后果：此前**每个按时长估算的预扣都偏低约 1/(24N+1)**（4 s 1%，5 s 0.8%）。核对按真实用量补扣，所以**钱没算错**，只是预扣系统性偏低——余额刚好够预扣的用户会在结算时被扣到略负
- 修法：`videospec.go` 的 `framesFor(seconds) = seconds×24 + 1`，时长上界那条分支同样 +1。**显式传 `frames` 的不 +1**（那已经是实际帧数）——但这条**没有实测**，是推断
- **修后复测（0.5.1，同一栈、同一模型，480p 16:9，`duration: 5`）**：预扣 `(5×24+1) × 864×496 / 1024 = 50638`，上游 `completion_tokens` **= 50638**，结算时**没有任何补扣流水**（账本只有一条 −0.50638）。两个不同时长（4 s / 5 s）都对上了 N×24+1。同一次部署还顺带验证了**已启用的内建插件随镜像自动升级**：两个节点都从 0.5.0 滚到 0.5.1，`serving=0.5.1`
- 测试：`TestEstimateMatchesARealArkTask` 把 40594 写成字面量；把 `framesFor` 改回 `×24` 时它报 40176 并失败（已验证）。§11.3 表里的数字全部按旧的 24N 帧算，**现在各自多 1 帧**（如 2.0 1080p 16:9 5s：243,000 → 245,025）

**成功任务的真实响应形状**（URL 已略）：

```json
{"id":"task_…","model":"doubao-seedance-2-0-mini-260615","status":"succeeded",
 "resolution":"480p","ratio":"16:9","duration":4,"framespersecond":24,"seed":33139,
 "generate_audio":true,"execution_expires_after":172800,"tools":[],
 "content":{"video_url":"https://ark-acg-cn-beijing.tos-cn-beijing.volces.com/…",
            "kz_video_url":"https://bk-hs-p-bj-lizhen.tos-cn-beijing.volces.com/…","last_frame_url":""},
 "usage":{"completion_tokens":40594,"total_tokens":40594},"created_at":1790766021,"updated_at":1790766099}
```

值得记的三点：

- **`generate_audio: true` 是默认值**（2.0 mini 上没传也开着）。这次的 token 与「无音频」公式完全吻合，所以**至少在 2.0 mini 上音频不改变 token 计数**
- **`execution_expires_after: 172800`（48 小时）**。与 `max_reconcile_age_sec` 的 7 天不是一回事（那是「任务可查」的保留期），但若某些中转站只保留 48 小时，7 天的核对窗口后半段会一直查不到——**未验证**
- `content.kz_video_url` 是中转站自己加的字段；`video_url` 指向 Ark 的 TOS，说明中转站背后确实是 Ark

### 12.3 图片：没跑通，原因在上游

`POST /ark/v3/images/generations`，`doubao-seedream-4-0-250828`，`size: 1K`。网关回 **503 `upstream_error`**，上游原文 `Task request failed`（`server_error`）。

- **插件/核心行为是对的**：这条记为 `free`、不计费，`error_type=upstream_error`，账号**没有**被打入冷却
- 中转站会校验模型名（一个不存在的 id 回 `model "…" is not served by this plugin`），Seedream 4.0 通过了名字校验却在执行时失败。**中转站 `/v1/models` 本来就没列任何 Seedream 模型**，所以大概率是它不支持图片。图片链路仍**未被真实验证**

### 12.4 素材库：**已真实验证**（2026-09-30 补测）

素材端点 `https://cdn.api.codingplus.ai/api/support/v1/asset` 模仿 Ark OpenAPI（`ResponseMetadata` 形状、`Service=ark`、`Version=2024-01-01`），**只接受 AK/SK 签名**，`sk-` key 被拒且错误信息自己指路：`the asset library OpenAPI only accepts an AccessKey (AK/SK); create one under 素材库 → AK/SK`。插件拼的 `{asset_base_url}/?Action=…` 带尾斜杠与不带都认。

用户在中转站后台建了一对 AK/SK 后补测，**四个 Action 全部通过**，走的是插件自己的 `arkAPI.call` + `signV4`（不是 curl 手拼）：

| Action | 结果 |
|---|---|
| `ListAssetGroups` | 200 `{"Items":[],"PageNumber":1,"PageSize":5,"TotalCount":0}` |
| `ListAssets` | 200 同形状 |
| `CreateAssetGroup` | 200 `{"Id":"group-20260930114147-cy5acgfc"}`（`Name` + `GroupType:"AIGC"`） |
| `DeleteAssetGroup` | 200 `{}`，探测组已清除（删前 `ListAssetGroups` 确认看得到它） |

**这一次同时关掉了 §11.5 留下的最后一个风险：自写的 V4 签名被真实服务接受了。** golden vector 只能证明「与厂商 SDK 逐字节一致」，**证明不了服务器会接受** —— 现在两种都成立，而且**带 body 的写请求也过**（签名绑定 payload hash，只读通过不代表写也对）。

真实响应比插件假设的多几个字段（`CreateTime` / `UpdateTime` / `Description` / `ProjectName` / `GroupType`），插件按需取，兼容。

> 验证方式：临时测试文件 + 凭证走环境变量，跑完删除。**没有给仓库新增「缺凭证就跳过」的测试** —— 那正是本轮在消灭的模式（§8.7 / CONTRACTS §26.4）。要复现就照这个做法临时加一个。

### 12.5 仍未验证的

- **图片**（§12.3，这个中转站不提供任何 Seedream 模型，换上游才能验）
- 2.5、2.0 pro/fast，其他分辨率与比例；`frames` 显式传参的计数；参考图 / 视频输入任务
- 失败 / 取消 / 过期任务的核对（`FAILED` 全额退）；核对超时路径
- **素材的上传与索引链路**（只验了组的增删查，`CreateAsset` 与 `plg_volcengine` 的索引写入未测）
- 官方 Ark 本身（这次全程是中转站）
---

## 13. 上游端点可配 + Anthropic Messages（2026-09-30，0.6.0）

用户的要求是「一套通用插件，支持指定端点和默认官方端点」，原则是**协议透传**：客户端用什么协议就以同样协议转发上游，不做转换。

### 13.1 实测的上游布局

| 上游 | 文本 / 图片 | **Anthropic Messages** | Ark 原生视频 |
|---|---|---|---|
| 官方 Ark | `/api/v3/*` | **`/api/coding/v1/messages`** —— **不在 `/api/v3` 下** | `/api/v3/contents/generations/tasks` |
| 本轮验证的中转站 | 根上的 `/v1/*`（`chat/completions`、`responses`、`embeddings`、`images/generations` 全在） | 根上的 **`/v1/messages`** | **`/doubao/api/v3/contents/generations/tasks`** |
| 中转站 `/doubao/api/v3/` 下的其他路径 | **不存在**，返回 SPA 的 HTML + **200** | — | — |

**官方 Ark 那一行是用一个可用的 key 实测出来的**，对照实验如下（这条悬了一整轮，之前两次推断都错了）：

| 请求 | 结果 | 含义 |
|---|---|---|
| `/api/v3/chat/completions` | 404 + **JSON 错误体**（说模型不存在） | 路由存在 |
| `/api/v3/definitely-not-a-route-xyz` | 404 + **空 body** | 路由不存在的形态 |
| **`/api/v3/messages`** | 404 + **空 body** | **与不存在的路由逐字相同 → 官方没有这个端点** |
| **`/api/coding/v1/messages`** | 404 `UnsupportedModel` / 400 `InvalidSubscription` | **路由存在且读了请求** → 这才是官方的 Anthropic 面，属于 Coding Plan，需要订阅 |

> 在此之前我判断过两次都不成立：第一次拿 401 当「路由存在」的证据 —— **Ark 的鉴权跑在路由之前**，不带有效 key 时真假路径都回 401，对照实验当场推翻了它；第二次假定 Anthropic 会与 chat 同居于 `/api/v3`。**只有拿到一个 active 的 key 才能分辨。**

### 13.2 布局由 Base URL 推导，不由运维配置

> **已被 §14 取代（0.7.0）。** 布局现在由**账号类型**决定，不再从 base_url 的主机推导 —— 下面这段（含 `officialBaseURL` 那条 BytePlus 特例）是 0.6.0 的记录，保留是因为 §14.1 的论证要引用它。Anthropic 那一列另见 §13.9：官方那条路后来决定不支持。

**官方 Ark 的 OpenAI 面在 `/api/v3`、Anthropic 面在 `/api/coding/v1`** —— 同一个账号上两个不同的前缀，一个 `api_prefix` 字段表达不了。所以布局不再是运维填的前缀，而是**从他们已经选的地址推导出来的**：

| Base URL | 文本 / 图片 | Anthropic | 视频 |
|---|---|---|---|
| 留空 或 方舟自己的地址（含 BytePlus） | `/api/v3/*` | `/api/coding/v1/messages` | `/api/v3/...`（或视频前缀） |
| 其他任何地址 | `/v1/*` | `/v1/messages` | 视频前缀 |

**两种常见情况都不需要填任何路径设置。**

**判断依据是「base_url 是不是方舟的地址」，不是「base_url 是不是空」** —— BytePlus 是方舟的海外地址，也走 `/api/v3`。按「非空即中转站」判断会把每个 BytePlus 账号都发去 `/v1/*` 打烂。测试专门钉了这一条。

两个设置项剩下的职责：

| 字段 | 什么时候要填 |
|---|---|
| `api_prefix` | **只为一种情况存在**：中转站在自己的域名下**原样镜像方舟路径**（推导会判错）。填了之后所有路径（含 Anthropic）都建在这个前缀下 —— 填前缀的运维是在说「这上游就是 Ark 的样子，只是换了个地方」 |
| `video_api_prefix` | 方舟原生视频任务的位置。中转站常把它们放进自己的命名空间（验证过的那个是 `/doubao/api/v3`），因为这些路径**没有标准位置**。官方留空 |

> **破坏性变更**：原先指向自定义地址、期望走 `/api/v3/*` 的账号，现在会解析成 `/v1/*`，需要补 `api_prefix: "/api/v3"`。`mock-upstream` 的测试正是这个形状，已经补上 —— 它同时也是「什么时候该用这个字段」的文档。

**三处调用点都要用视频前缀**，其中 `BuildReconcileRequest` 最容易漏：它跑在离线循环里，路径错了每次轮询都 404 → 核心读成「仍在进行」→ 到 deadline 放弃 → **保留预扣**。于是一个真的跑完、真的上报了用量的任务，被按预估收费。测试专门钉了这一条。

`spec.Versions` **保持字面 `/api/v3`，不跟随任何设置**。两者看着像、其实是两件事：它的作用是「撤销一次从 Ark SDK 文档粘贴 base URL 的动作」，与上游实际挂载点无关。（2026-11-20 起地址按填写的保存，不再在保存时去掉 `/api/v3`；请求时 `FromAccount` 去掉末尾至多一个 `/api/v3` 得到根，各路径照旧完整接在根上，清单的 `guardedSettings` 写同样的 `versions`，核心按根比较。）

### 13.3 安全：前缀不能把请求移出 base_url 的主机

**这是这一轮最重要的一条。** `base_url` 受 `guardedSettings` 限制成官方地址（没有 `account:settings:custom` 只能填白名单内的），而 `guardedSettings` **管不住路径类设置** —— `check/validate.go` 要求 `allowed` 每项是绝对 http(s) URL。

于是自由文本的前缀填 `@evil.com/v3`，`https://ark.cn-beijing.volces.com` + `@evil.com/v3` 会被解析成**主机 `evil.com`**（前半段降级成 userinfo）：限制被绕过，账号凭证发去别处。核心的 `netguard.CheckURL` 拦不住 —— 它问的是「这个主机是不是私有地址」，不是「这是不是我们想去的主机」。

**不做字符黑名单**（黑名单永远在错的一侧，CONTRACTS §25.1 已有这条教训）。两层，各自独立成立：

| 层 | 做什么 | 何时跑 |
|---|---|---|
| `normalizePrefix` | 只接受一种形状：以单个 `/` 开头的路径（URL 的 authority 在第一个 `/` 处结束，所以 `@` 再也挤不进去）。拒 `//` 开头、`?` `#`、`..` 段、超长 | 保存账号时（`ValidateCredentials`），**唯一能把错误告诉人的地方** |
| `upstreamURL` | 拼出最终 URL 后解析，**scheme 与 host 必须与 base_url 相同** | **每次请求**（`BuildUpstreamRequest` / `BuildTestRequest` / `BuildReconcileRequest`） |

第二层必须无条件成立,因为第一层只在保存时跑：旧版本存下的行、备份恢复的行、以及将来新增的前缀形状都可能绕过它。这是凭证真正离开进程的那一层。

**两层各挡什么，容易搞反，测试里写清了**：

- `@evil.com/x` 是**真的逃逸** —— 拼在绝对 base 后面，authority 变成 `ark.cn-beijing.volces.com@evil.com`，主机是 `evil.com`。这是 `upstreamURL` 存在的理由
- `//evil.com/x` **在这里不是逃逸** —— base 已经带了 scheme 和 authority，结果是 `https://ark.cn-beijing.volces.com//evil.com/x`，**主机没变**，双斜杠只是个古怪的路径。它被前一层拒掉，理由是「以 `//` 开头的路径前缀无论如何都是笔误」，而且同一个字符串在 base 没有主机时**确实**是逃逸

`upstreamURL` 返回的是**原始拼接串**而不是 `u.String()`：核心的 SSRF 防护会重新解析同一批字节，任何在这里经过序列化往返的东西都可能与被检查的不同。

### 13.4 Anthropic Messages

账号类型声明第三个平台 `{"platform":"anthropic", "passHeaders":[...]}`。核心**零个协议转换器**，所以客户端说 Anthropic 就只能由插件按原样转发 —— 请求体到插件手里已经是 Anthropic 形状，唯一要决定的是发到哪。路径由布局决定（§13.2）：官方 `/api/coding/v1/messages`、中转站 `/v1/messages`、填了 `api_prefix` 则是 `{api_prefix}/messages`。

> **官方那条要 CodingPlan 订阅**（实测 `InvalidSubscription`），而套餐的模型名也是另一套。所以在官方 Ark 上启用 Anthropic 面之前，先确认账号有有效订阅，否则每个请求都会 400。

四件连带的事：

**1. `anthropic-version` 之前根本没发。** Anthropic 的 API 要求这个头。现在客户端没送就补默认值 `2023-06-01`，**客户端送了就用它的** —— 它知道自己的请求体是哪个 beta 形状，静默降级会让上游按不同规则解读。`anthropic-beta` 同样转发。注意 `passHeaders` 是**整列替换不是合并**，所以 manifest 和 `upstreamHeaders` 两侧必须写同一组头，否则漏的那一侧会把客户端的头丢掉。

**2. 错误词表之前是 OpenAI 的。** 网关在插件填了 `client_error_type` 时**逐字使用**（只有插件没说话才自己按 `errorFormat` 推导）。所以 Anthropic 客户端会收到 `{"type":"error","error":{"type":"server_error"}}` —— 而 **`server_error` 根本不在 Anthropic 的词表里**（该是 `api_error`），404 该是 `not_found_error` 而不是被折进 `invalid_request_error`。现在按 `meta.protocol` 切词表；其余四个值（`invalid_request_error` / `authentication_error` / `permission_error` / `rate_limit_error`）两边拼写相同。

**3. `count_tokens` 按名字拒绝。** 平台声明了两个端点，而 `AccountPlatform` **没有按端点过滤的能力**，所以声明这个平台就等于连带接受 `count_tokens`；Ark 没有这个端点。带理由失败优于编一个会 404 的路径。**后果**：混合分组里调度到 Ark 账号的 `count_tokens` 会失败一次再 failover 到真 Anthropic 账号（理想行为）；纯 Ark 分组里客户端拿到 503 而不是干净的 404 —— 这是契约现状。

**4. 用量规则故意不覆盖。** 论证不依赖对上游的猜测：这是协议透传，客户端打 `/v1/messages` 拿回的响应体必须是 Anthropic 形状（否则它的 SDK 解析不了），那么 usage 就在 `usage.input_tokens` / `message_start` / `message_delta` 里，正是平台规则读的地方。**覆盖才是危险动作** —— `usagerules.For` 是整块替换，漏了 SSE 列表就等于每个流式请求静默计零。

**还有一条要知道**：平台级 sticky 规则 `claude-code-session` 只按协议 `anthropic.messages` 匹配、不限模型（2026-11-10 起，原先是 `models:["claude-*"]`），所以客户端打任何模型（包括映射到 `doubao-*` 的）只要带 `metadata.user_id` 都**会**命中，把会话钉在一个账号上。无害（就是会话亲和），但排查「为什么总是同一个账号」时要想到它。

### 13.5 为什么没有「视频形状开关」—— 考虑过并拒绝

中转站根上还有一条 new-api 风格的视频路由 `/v1/video/generations`（提交要求**顶层 `prompt`**，明确拒绝 Ark 的 `content[]`；查询是 `GET /v1/video/generations/{id}`）。做成第二种上游形状技术上可行（`BodyPatch` 只有 `OP_SET` / `OP_DELETE`，但声明 `requestFields` 拿到 `content[].text` 后 `OP_SET prompt` + `OP_DELETE content` 走得通），**但不该做**，三条理由各自独立成立：

1. **Ark 原生那条路是全保真透传。** 读中转站的参考实现：它把**整个原始 Ark 请求体原封不动存进 `metadata`**，发上游时再逐字还原 —— `ratio`、`camera_fixed`、`generate_audio`、`content[]` 里的图片与视频输入、prompt 里的 `--rs/--dur` 后缀，全都保留。查询时返回的是**存下来的真实 Ark 响应体**，含 `usage.completion_tokens`。也就是说现有代码**一行都不用改**，而且核对能拿真实用量做 `SETTLED` 结算。
2. **new-api 形状是有损的，且失败不可见。** 走那条路必须把 Ark 字段逐个枚举进 `requestFields` 再重挂。任何没枚举的字段**静默消失** —— 用户要 `16:9` 拿到默认比例，请求 200，没有任何地方报错。而 Seedance 每一代都在加参数。
3. **它的查询更差。** new-api 视频查询的状态词表是 `queued/in_progress/completed/failed`，**且完全没有 usage 数字**。`completed` 会掉进 `ParseReconcileResponse` 的 `default:` 分支 → **永远 PENDING** → 轮询到 7 天 deadline 才把预估当终值。

**所以只保留 Ark 形状**，靠 `video_api_prefix` 指到中转站挂载的位置。连带的好处：`ParseReconcileResponse` 的状态词表不用改，`usageRequestFields` 的 `content.#` / `content.0.text` 也不用改 —— 客户端打的和上游收的都还是 Ark 形状。

> 给中转站作者的一条要求：**new-api 系中转站要接这个插件的视频，必须暴露 Ark 原生视频路由**（参考实现本来就是这么做的）。

### 13.6 两道只为抓静默失败而存在的测试

都是**先把它守的东西弄坏、确认测试变红**之后才留下的：

1. **两个前缀必须出现在 `settingsFields` 里。** 核心的 `split()` 按这个列表分区，漏了的键进**加密 blob**，而插件读的是明文 settings —— 运维改了前缀、没有任何报错、每个请求照旧发往 `/api/v3`。核心**不校验** `settingsFields` 与表单 schema 的一致性，所以这个遗漏在安装期完全无声。
2. **表单的 `maxLength` 必须等于 Go 的 `MaxPrefixLen`。** 两个数字写在两种语言里、没有任何东西把它们连起来；只改一边，表单就会接受一个插件在每次请求时都拒绝的值。

### 13.7 真实验证（2026-09-30，经网关）

在 ovh 的 `single` 栈上部署 `7bafd35`（volcengine **0.6.0 enabled**），账号指向中转站、**经网关**发四次请求。

| # | 协议 | 流式 | input / output | 状态 | 计费 |
|---|---|---|---|---|---|
| 53 | `openai.chat` | 否 | 32 / 20 | 200 | billed |
| 56 | `openai.chat` | **是** | 31 / 20 | 200 | billed |
| 54 | `anthropic.messages` | 否 | 32 / 20 | 200 | billed |
| **55** | **`anthropic.messages`** | **是** | **54** / 24 | 200 | billed |

**第 55 行是这次验证的全部意义。** 同一个 prompt 直接打中转站测过：`message_start` 报 34、`message_delta` 报 54。平台原生规则**只从 `message_start` 读 input**，而 JSON 规则不施加于 SSE —— 所以网关记下的 **54 只能来自 §13.4 那条覆盖**。少记 37% 的问题在真实链路上确认修好了。

其余核对：

- `usage_logs.anomalies` 四行**全为 `{}`** —— 没有响应形态不匹配，也没有用量回落
- `balance_ledger` 四笔扣款与 `usage_logs.total_cost` 逐笔一致，余额递减正确
- `count_tokens` 返回 **503 `no_available_account`，而且错误体是 Anthropic 形状**：`{"type":"error","error":{"type":"overloaded_error"}}` —— `overloaded_error` 正是 Anthropic 对 503 的词表。`usage_logs` 记 `error_type=no_account`。**不静默**，而且比预想的干净：插件按名字拒绝 → 网关当 failover → 没有其他账号能服务 → 503，词表由核心按 `errorFormat` 正确推导
- **破坏性变更在现实中撞上了**：这个账号原来的 `base_url` 是 `https://cdn.api.codingplus.ai/doubao`，新语义下会被当成中转站、把 chat 打到不存在的 `/doubao/v1/…`。改成 `base_url = https://cdn.api.codingplus.ai` + `video_api_prefix = /doubao/api/v3` 才对。**这就是 §13.2 那条迁移说明的现实案例。** 同时确认 `video_api_prefix` 存进了**明文 settings**（`settingsFields` 那道守门有效）

### 13.8 视频在新语义下重跑（2026-09-30，经网关）

新前缀语义下的视频全链路重跑了一次：账号 `base_url = https://cdn.api.codingplus.ai` + `video_api_prefix = /doubao/api/v3`（旧语义是 `base_url` 末尾带 `/doubao`）。

| 步骤 | 实测 |
|---|---|
| 提交 | 200，`{"id":"task_ZjC3XTwLZA8FjMwG1G7k9AWiGsmlGug7"}` —— **`video_api_prefix` 在真实链路上生效** |
| 预扣 | `usage_logs.billing_status=reserved`，`output_tokens=40594`，$0.40594；`pending_settlements` 一条 `pending`，deadline 7 天 |
| 核对 | 第 **5** 次轮询拿到 `succeeded`，`state=settled` |
| 结算 | `billing_status=billed`，`output_tokens` **仍是 40594**、费用不变 |
| 账本 | **只有一条预扣流水,没有补扣也没有退款** |

**最后一行是 0.5.1 那个帧数修复的独立确认**：`(4×24+1) × 864×496 / 1024 = 40594`，上游报的 `completion_tokens` 与预扣**分毫不差**，所以核对没有产生任何差额流水。§12.2 那次（修复前）产生过 −0.00418 的补扣。

一处配置上的坑（部署时撞到）：这个中转站的 key 是**按分组授权模型**的。文本那个 key 的分组只有 `claude-*` 与 `deepseek-flash`，**没有 seedance**，所以用它提交视频会得到上游的 503 `Task request failed`。要两个账号各自带自己的 key、用 `models` 限定（文本账号限 `deepseek-flash`，视频账号限 `doubao-seedance-2-0-mini-260615`），同一个分组里共存。

### 13.9 官方 Ark 的 Anthropic 面：不支持（2026-09-30 决定）

实测确认过官方唯一会说 Anthropic 的地方是 `/api/coding/v1/messages`（返回 `UnsupportedModel` / `InvalidSubscription`，即它读了请求），而 `/api/v3/messages` 根本不存在（404 + 零字节 body，与未注册的路径逐字相同）。

**决定不支持它**：那是订阅制产品，官方文档写着套餐额度「不可直接用于 API 调用」，而且测试账号返回 `InvalidSubscription` —— 指向它会让插件的行为取决于账号是否持有某个套餐。

所以**官方账号对 `anthropic.messages` 按名字拒绝**，和 `count_tokens` 同一个做法，而不是往一个实测不存在的路径上发请求。错误信息里点明怎么修（把 `base_url` 指向支持该协议的 Ark 兼容中转站，或显式填 `api_prefix`）——「方舟不提供这个」只有和「而中转站提供」放在一起才有用。

中转站不受影响:它们的 `/v1/messages` 是使用这个协议的受支持方式,也就是 §13.7 端到端验证过的那条路。

### 13.10 仍未验证

- **图片**（§12.3，这个中转站不提供任何 Seedream 模型，换上游才能验）
- 官方 Ark 的 Anthropic 面（按 §13.9 已决定不支持，所以不再是缺口）

---

## 14. 拆成两个账号类型：官方 / 中转（2026-10-01，0.7.0）

参考 new-api 的渠道划分，把一个 `apikey` 账号类型拆成两个。**这一节取代 §4.2 与 §13.2 的「一个类型 + 从 Base URL 推导布局」。**

### 14.1 为什么是「官方 / 中转」这条轴，不是「视频 / 文本」

要简化的复杂度具体是三样东西，它们**全都长在官方与中转的差别上**：

1. 一个表单 8 个字段，官方只用 3 个（`base_url` / `asset_base_url` / `asset_region`）、中转用 5 个（多两个前缀），彼此的字段对另一边**没有意义**
2. `officialBaseURL()` —— 靠**主机名猜**这个账号是官方还是中转，而且为了不把 BytePlus（方舟自己的海外地址）误判成中转站，必须把它硬编码进判断
3. 同一个类型声明 `anthropic` 平台，于是官方账号也成了 `/v1/messages` 与 `count_tokens` 的候选，只能在请求路径上**按名字拒绝**

按「视频 / 文本」拆一样都解决不了：视频类型照样要两个前缀（中转站的视频前缀正是为它存在的），文本类型照样要猜官方还是中转。

**而这条轴在本仓库里已经有先例**：`anthropic` 插件把 `base_url` guard 到 `api.anthropic.com`，`relay` 插件的 `relay_key` 类型让它自由 —— 同一个协议、两个插件，分界就是官方与中转。理由也相同：**中转站的地址无法用白名单表达**（`sdk/manifest/check/validate.go:807` 要求 `guardedSettings.allowed` 至少一个绝对 http(s) URL，空白名单过不了校验），所以它不能和一个地址被钉死的上游共用账号类型。

new-api 的两个渠道是同一条轴（下表是读代码得到的，不是看文档）：

| | new-api 45 `VolcEngine` | new-api 54 `Doubao` |
|---|---|---|
| api type | `APITypeVolcEngine`（Ark 原生 `/api/v3`） | 落到 default（OpenAI 兼容） |
| 流式 | 在 `streamSupportedChannels` 里 | 不在 |
| 任务插件 | `doubao` | `doubao` |
| 素材库端点 | 官方控制面 `ark.cn-beijing.volcengineapi.com` | **base_url 下的一个路径**（`asset_library_endpoint`） |
| 视频路径 | `/api/v3/...` | `doubao_video_legacy_api` 开关 |

54 也是 OpenAI 兼容地服务文本的（它不在 `api_type.go` 的 switch 里，落到默认分支），所以「中转类型也服务文本」与 new-api 一致 —— 更重要的是，那正是 §13.7 在这个中转站上实测通过的东西。

### 14.2 两个类型

| | `apikey`「方舟 API Key」 | `relay`「豆包中转 Key」 |
|---|---|---|
| 上游 | 方舟自己（含 BytePlus） | Ark 兼容中转站 / 聚合站 |
| `base_url` | 可留空（默认国内），**guarded** 到两个官方地址 | **必填**，**不 guarded**（无法 guard） |
| 路径布局 | 固定 `/api/v3`，**没有前缀字段** | 默认 `/v1`，`api_prefix` + `video_api_prefix` 可配 |
| 平台 | `openai` + `volcengine` | `openai` + **`anthropic`** + `volcengine` |
| `Versions` | `/api/v3`（撤销粘贴 SDK base URL：保存原样，请求时去掉） | **无**（见 14.3） |
| 素材库 | 官方控制面，`asset_base_url` guarded | 中转站自己的端点，完整地址，不 guarded |

`prefixesOf` 于是只有两个分支，且**官方那支不读任何设置**：前缀不在它的 `settingsFields` 里、不在它的表单里，真被写进去也读不到（会落进加密 blob）。测试钉了这一条 —— 一个「看起来能改、其实改不了」的设置比没有更糟。

**官方类型不声明 `anthropic` 平台**，这是拆分带来的实际收益而不只是整洁：

- `count_tokens` 不再被路由到官方账号（在混合分组里，那本来是「失败一次 + failover」）
- `anthropic.messages` 同理

`upstreamPath` 里对两者的拒绝**保留**，但性质变了：`count_tokens` 那条是真在跑的（中转类型声明了平台，而 `AccountPlatform` 没有按端点过滤的能力）；`messages` 那条是防御性的 —— 正常路由到不了，到了就说清楚，而不是往一个实测返回「404 + 零字节」的路径上发请求。

### 14.3 中转 `base_url` 的保存期检查（新增）

`relaySpec` 不设 `Versions`（原名 `StripSuffixes`，2026-11-20 改名后中转类型的地址规则不变），理由和官方那边设它的理由是同一个：官方那边是「撤销一次从 Ark SDK 文档粘贴 base URL 的动作」，而中转类型的 `base_url` 是**中转站的根**、路径由两个前缀表达 —— 去掉任何后缀都等于**把请求悄悄挪到运维没写的地方**（`https://r/api/v3` 配默认前缀会变成 `https://r/v1/...`）。

所以改成**保存时拒绝**：`base_url` 的路径以 `/v1` 或 `/api/v3` 结尾时报字段错误，并点明「把路径填到 `api_prefix` 里」。按转义形式判断（地址就是这样保存和发出的）：`https://r/a%2Fv1` 不以 `/v1` 段结尾，接受。

**为什么值得单独加一道**：文本面上路径错了是响亮的 404，但视频面上 —— 提交 404 而轮询也 404 时，核心把 404 读成「仍在进行」，到 deadline 放弃并**保留预扣**。一个真跑完、真上报了用量的任务被按预估收费。这是 §13.2 已经记过一次的失败形态，端点可配之后它的触发概率变高了，而「base_url 带了前缀」正是最可能的误配。

### 14.4 破坏性变更（需要人工处理）

**原先指向中转站的 `apikey` 账号会失效。** 它们在库里的 `type` 是 `apikey`，而 `apikey` 现在的含义是「方舟官方」：布局固定 `/api/v3`、`base_url` 受 guard、不服务 Anthropic。

没有写迁移，因为这条分支未发布，而且此刻全世界的这类账号就是测试环境里那两个。**处理办法是用 `relay` 类型重建**（api_key、base_url、两个前缀照抄）。

顺带一条：这类账号如果还有未结算的 `pending_settlements`，拆分后核对会按 `/api/v3` 去轮询中转站 → 404 → 保留预扣。重建前先确认没有在途任务。

### 14.5 真实验证（2026-10-01，经网关）

在 ovh 的 `single` 栈上部署 `1ded989`（volcengine **0.7.0 enabled**，内置插件随镜像重建）。

**先证明破坏性变更真的生效**（拆分若没牙齿，后面的验证就没意义）。旧的两个 `apikey` 账号（16 / 17，`base_url` 指向中转站）保持原样发请求：

| 协议 | 结果 | 说明 |
|---|---|---|
| `openai.chat` | 404 `Invalid URL (POST /api/v3/chat/completions)` | **中转站自己说的** —— `apikey` 现在固定 `/api/v3`，布局真的换了 |
| `anthropic.messages` | 503 `no_available_account` | `apikey` 不再声明 anthropic 平台，分组里没有候选 |

还有一处在**授权层**就能看见：给分组 4 新建网关 key，`platforms` 是 `["openai","volcengine"]`；把两个账号换成 `relay` 类型后再建一个，变成 `["anthropic","openai","volcengine"]`。**平台授权跟着账号类型变**，而不是跟着插件变。

然后按 §14.4 重建成 `relay`（18 文本 / 19 视频，`base_url = https://cdn.api.codingplus.ai`、`video_api_prefix = /doubao/api/v3`），四条协议 + 视频全跑一遍：

| # | 协议 | 流式 | in / out | 状态 | 计费 | `account_type` |
|---|---|---|---|---|---|---|
| 62 | `openai.chat` | 否 | 38 / 16 | 200 | billed | relay |
| 63 | `openai.chat` | 是 | 38 / 16 | 200 | billed | relay |
| 64 | `anthropic.messages` | 否 | 38 / 16 | 200 | billed | relay |
| **65** | **`anthropic.messages`** | **是** | **38** / 16 | 200 | billed | relay |
| 66 | `anthropic.count_tokens` | 否 | — | 503 | free | relay |

**第 65 行仍然是关键**：这次流式响应的 `message_start` 报的 `input_tokens` 是 **11**，而网关记的是 **38** —— 和非流式一致。§13.4 那条 `message_delta` 覆盖在新类型下照样生效，少记 37% 的问题没有随拆分回归。

第 66 行是 §13.4 记过的既定行为：插件按名拒绝 → failover → 纯中转分组里没有别的候选 → 503。

视频（账号 19）：

| 步骤 | 实测 |
|---|---|
| 提交 | 200，`task_iInIHVfl32LAGZzicq5rX6LMaxmK4E1J` —— `video_api_prefix` 在 `relay` 类型下生效 |
| 预扣 | `reserved`，40594 output token，$0.40594 |
| 核对 | 第 **4** 次轮询 `settled` |
| 结算 | `billed`，`output_tokens` 仍是 40594，费用不变 |
| 账本 | **只有一条 `-0.40594`，没有补扣也没有退款** |

最后一行第二次独立确认了 0.5.1 的帧数修复（§13.8 是第一次）。

## 15. 官方通用与豆包视频账号（0.11.0，2026-10-03）

本节描述当前行为，覆盖 §14 关于官方账号不支持 Anthropic Messages 的历史结论。账号类型 ID 保持 `apikey` / `relay`，已有账号无需重建。

- **字节火山方舟 · 官方通用**（`apikey`）：OpenAI 与视频请求沿用官方 `/api/v3`；Anthropic Messages 使用官方 `/api/compatible/v1/messages`，上游认证为账号 API Key 的 `x-api-key`。JSON 和 SSE 直接透传，不转为 OpenAI。`count_tokens` 仍明确拒绝，不能据此认为支持全部 Anthropic API。
- **豆包视频**（`relay`）：可配置 `base_url`、`video_endpoint`、`asset_endpoint`。后两者接受完整 HTTP(S) URL 或以 `/` 开始的路径；路径追加到 Base URL，`/` 表示 Base URL 本身。视频端点填写任务集合地址，创建使用该地址，查询与后台核对统一追加转义后的任务 ID。
- 新视频端点优先于 `video_api_prefix`，留空保持旧前缀规则。新素材库端点优先于 `asset_base_url`；两个素材库地址均未配置时关闭素材库，不隐式请求官方控制面。素材库使用独立 AK/SK 和区域签名，签名路径与实际请求路径一致。
- 保留旧 relay 账号的文本、图片和 Anthropic 兼容能力及旧配置字段。新端点没有表单默认值，避免打开旧账号再保存时改变其路由。官方类型不允许设置这两个自定义端点。
- 端点校验拒绝查询串、片段、URL 用户信息、协议相对地址、空白控制字符及路径穿越；最终网络请求仍受宿主出口策略约束。凭证继续分别加密保存，不引入拼接密钥格式。

参考 `new-api` 的 VolcEngine / DoubaoVideo 类型、任务 URL 拼接和素材库签名实现；官方 Messages 以当前[方舟 Messages API 文档](https://docs.volcengine.com/docs/ark/messages-api?lang=en)为准。OpenAI/视频 API Key 与素材库 AK/SK 的作用不同，不能互换。

验证：本地 vet/test、前端类型检查通过；OVH 私有 PG/Redis 环境完整 `go test -race -p4 -count=1 -v ./...` 顶层 106 项通过、0 跳过、0 失败。覆盖官方与中转认证、流式标记、自定义视频提交/查询/后台核对、配置兼容、素材库真实测试 HTTP 路径及签名。浏览器以只读模拟账号检查两个实际 schema 表单。此次未调用付费上游生成视频，详细记录见 `audits/2026-10-02/MULTINODE-VALIDATION.md` §22。

## 16. 视频计费模板与表达式补充（0.12.0，2026-10-04）

参考 `new-api` 的 codingplus 分支 `video_billing.go` 与任务计费实现，视频定价作为宿主现有 `expression` 模式的 `config.video` 保存。插件只上报计量事实，不提供或修改价格。入口为「模型价格 → 表达式 → 视频计费」。

- `price_per_million_tokens`：必填正数，USD/百万输出 Token。
- `video_input_price_per_million_tokens`：含视频输入时替换整段输出的 Token 单价；0/留空使用基础价。
- `video_price_per_second`：可选的 720p 基准每秒预扣价；预扣 = 秒价 × 秒数 × 像素数/921600 × 含视频输入价/基础 Token 价。未配置时沿用插件估算 Token。
- `resolution_prices`：精确尺寸覆盖，不区分横竖屏，各项正价独立覆盖；0/留空回退基础字段。拒绝重复尺寸、负数、非有限数和空覆盖。
- 收到实际 Token 后按提交时的表达式快照重新结算，多退少补；成功无用量继续保留预扣，失败继续走宿主退款。查询接口免费，重复查询不重复扣款。视频模板价格只适用于托管视频提交端点。

方舟官方 `apikey` 和豆包视频 `relay` 共用这些计量字段：`resolution`、`video_seconds`、`video_pixels`、`video_width`、`video_height`、`video_input`、`video_draft`、`video_estimated`。提交时 `video_estimated=true`；返回真实 Token 后为 `false`。最终轮询仅覆盖上游实际提供的字段，保留提交时的视频输入与 Draft 标记。若上游给了分辨率但无法确认精确宽高，不沿用旧尺寸覆盖，回退基础价格。

`usageRequestFields` 新增有界数组属性投影，例如 `content.#.type`。只传内容类型，避免长提示词或内联图片挤掉视频输入标记；仍受已有字段数和字节预算约束（字节预算已在转发 V2 收尾删除，见 §11.2；每个端点最多 16 个字段的上限仍在）。插件 0.12.0 需要同时升级本次宿主与 SDK，沿用内置插件随核心打包的升级流程。

管理员可在表达式源码使用 `u("video_draft")` 等字段自定义规则；试算新增插件计量输入及预扣/结算示例。源码表达式的自定义逻辑由管理员维护，视频模板配置则始终由服务器重新生成，防止旧源码覆盖新配置。

计费入口先检查端点显式声明的 `billingTypes`（per_request / per_token / expression / video）。未声明或不支持直接返回 `billing_type_not_supported`，不调用估算插件、不调度账号、不扣款、不请求上游。文本端点不能声明 video。将视频模板切换成读取 `video_*` 计量字段的源码表达式，也不能绕过此检查。

随后执行真正的上游调用前预扣：插件 `EstimateUsage` 返回用量事实，核心计算金额并锁定用户余额，在同一事务里写入预扣账本和 `request_precharges`。并发请求看见的是已经扣减的余额；失败或免费请求退款，正常结算和异步任务注册在各自事务中抵销预扣、写入最终/任务预估费用。事务失败同时回滚抵销，重试不会重复扣款。超过 24 小时且没有待结算用量的孤立预扣由结算重试任务幂等清理。

系统设置 → 计费新增 `pre_consume_tokens`，默认 500、整数范围 0–100000000。文本采用本地分词结果与此下限的较大值；0 只取消下限，不取消实际用量估算。视频由插件按参数估算，固定文本下限不参与视频定价。没有 `PreConsumedQuota` 旧字段，也没有未声明端点类型时的兼容默认。

核心提供离线 `HostService.CountTokens`，SDK 插件通过 `pluginsdk.CountTokens(ctx, host, text, encoding)` 使用；支持 o200k_base 与 cl100k_base、UTF-8 校验和 1 MiB 文本限制，不访问上游。BPE 文本计数是预估，不能替代视频用量或服务商最终 usage。SDK 默认文本估算器使用该能力，火山插件的视频估算器无需已有 task ID。

预扣与释放接口为核心依赖的编译期必需能力，不再通过可选接口断言静默跳过。任务提交协议必须有插件显式 `UsageEstimator`，否则 SDK 拒绝；普通文本端点仍可使用默认本地分词。没有价格的模型一律拒绝（CONTRACTS §17.3）。

表达式条件倍率连乘：两条规则分别命中 ×2、×3 时总倍率为 ×6，再乘分组倍率。使用日志保存并展示各条规则、命中状态、请求条件取值、基础费用、条件总倍率及最终费用；界面直接使用结算快照，不再猜测旧字段、缺失倍率或上下文长度。当前普通 `per_token` 模式没有独立规则配置，需使用表达式配置 Token 单价与条件规则；按上游实际 Fast/Ultrafast 档位修正结算尚未实现。

后续清理修复了版本资源读取时未关闭临时插件包句柄的问题，Windows 的 `TestVersionAssetsReadApprovedPackageWithoutLocalProcess` 已在本地 PostgreSQL 下复测通过。

验证：相关单元测试、前端回读/类型检查/构建通过；另启动独立本地 PostgreSQL 18.3，计费、用量及插件运行时事务测试通过，覆盖并发余额不足、幂等预扣与退款、最终结算、异步任务预扣接续及失败回滚。扩展 registry 测试中的 TestVersionAssetsReadApprovedPackageWithoutLocalProcess 在 Windows 临时文件清理时遇到占用失败，此项未通过。未调用真实付费视频上游，也未部署。

## 17. 0.13.0：转发 V2（2026-11-18，需核心 ≥0.1.304）

核心与全部平台插件改成一条请求路径（CONTRACTS §112），volcengine 随之改写，行为对客户端不变：

- 插件只实现 `BuildUpstream`，`Execute` 由 SDK 的 `ExecuteDefault` 提供：普通端点（chat、embeddings、图片、Anthropic Messages）不等待地中继、返回 `host_usage`，用量由核心按清单规则读；两个视频提交端点是任务提交（也声明了 `usageSource: "plugin"`），等待形式中继、`ParseTaskSubmission` 读观察、返回 `task`（带按请求字段算的预估）。
- 请求体完整交给插件，原来清单里的 `requestFields` 删除；任务观察只有 `Monitor`（原来的 `Poll` 函数体成为它），两阶段的 `BuildReconcileRequest` / `ParseReconcileResponse` 删除；清单删掉 `platform.execute.v1`、`platform.poll.v1`。
- 插件的头全部保持权威头，运营方的请求头透传和覆盖改不了它们（与改写前相同）。转发的客户端请求头就是核心交来的那些（核心已按清单的 `passHeaders` 过滤：anthropic 平台的 `messages` 是 `anthropic-version`、`anthropic-beta`、`user-agent`，其余协议只有 `user-agent`），插件不再自己维护两份名单（`apikey.ForwardHeaders`）；上游收到的请求没有变化。
- 流式 chat 请求加 `stream_options.include_usage = true`，与 openai 预设同一个改写（`openai.StreamUsage`），“流式”的判断也相同：`pluginsdk.RequestStream`，即 `meta.stream` 或请求体的 `stream` 为 `true`。所以运营方在账号的参数覆盖里给非流式请求加了 `stream:true` 时（参数覆盖在 `Execute` 之前执行），插件同样补上 `stream_options.include_usage`，上游会在最后一块报用量；以前只看 `meta.stream`，这种请求拿不到流式用量。volcengine 没有只流式的账号类型，核心不会替它强制流式。
- 任务提交上插件没有返回 `task`（解析失败）时，任务不登记、客户端报错、预扣释放；等待期间插件崩溃时核心切断上游，这次请求不计费。
- 两个账号类型在 anthropic 平台上只服务 `messages`（清单 `"endpoints": ["messages"]`）。以前没写端点子集，核心把 `anthropic.count_tokens` 原样路由给它们而不用本地估算，插件又拒绝（Ark 没有计数端点），只有 Ark 账号的分组对 `count_tokens` 一律回 503；现在 `count_tokens` 是核心的本地估算。`ProtocolCountTokens` 及其分支删除，上游收到的请求没有变化（以前计数请求也从未到达上游）。下文 §15 关于“按名字拒绝 `count_tokens`、`AccountPlatform` 没有按端点过滤的能力”的说法到此不再成立。
- 视频查询由核心从自己的任务快照回答，从不为 `volcengine.video_query` 调 `Execute`：`BuildUpstream` 里查询的 GET 分支删除，每个请求都是 POST；`ExtractUsage` 只剩任务提交解析里用的内部函数 `videoUsage`。
- **素材库改由核心发出**（转发 V2 收尾）：插件照旧读账号的 AK/SK 签名（`GetAccountCredentials`，每次读写一条审计），签好的方法、地址、请求头、请求体交给 `pluginsdk.SendAccountRequest`，核心为这个账号发出：有代理的账号经代理（此前插件经出口隧道直连，账号代理不起作用），直连时过 SSRF 检查，不跟随重定向（此前插件的客户端会跟随），回答整份交还（此前插件读到 1 MiB 为止），单次 30 秒（核心的 `refreshTimeout`），每次发送另写一条审计（`plugin.account_request`，只记方法、主机、路径和状态）。插件自己的 HTTP 客户端、传输层、拨号器和 1 MiB 上限删除，清单去掉 `net` 权限（插件不再有别的直连）；重试（传输错误、429、5xx，最多 3 次，间隔 200 毫秒，`Create*` 不重试）不变：核心的发送不重试，它仍然有用。
  - **目标主机**：核心只发往清单允许的主机（`manifest.UpstreamHostAllowed`，CONTRACTS §112.17）。默认的官方控制面 `ark.cn-beijing.volcengineapi.com` 在 `externalServices` 里；运营方填的 `asset_base_url`（两种类型）、中转的 `asset_endpoint`（完整地址）和它所相对的 `base_url` 都在类型的 `upstreamFields` 里（原本就在，没有改清单），所以正式账号指向 BytePlus 等别的控制面、中转按自己的 `base_url` 解析的素材路径都能发出。`asset_endpoints_test.go` 对每种配置断言核心会放行。
  - **发给上游的内容不变**：`arkapi_test.go` 的 `TestRequestIsTheReleasedOne` 用改写前的插件客户端在固定时刻录下一次 `ListAssets` 的完整请求（方法、路径、查询、全部请求头、请求体、签名），新实现交给核心的内容与之逐字节相同，经仿照核心 `sendPluginCall` 的发送到达假上游后同样逐字节相同（`User-Agent`、`Accept-Encoding`、`Content-Length` 由 Go 的 HTTP 客户端自己加，核心与原插件的传输层设置相同）。核心对 `SendAccountRequest` 只在没有 `Content-Type` 时补 `application/json`（插件总是自己设置并签名），不增改别的头，所以签名有效。其余签名测试（独立重算、换 SK / 区域 / 请求体签名都变、十个 Action 都验签、取消一直传到上游）照旧经这条路径通过。
- 测试：`internal/volcengine/execute_test.go`（经假宿主的中继形式与返回种类）；测试把 `manifest.json` 交给假宿主（`pluginsdktest.Options{Manifest: ...}`），取回能到的主机按清单判断；`TestConformance` 调一致性套件（chat 流式与非流式、responses、embeddings、两种类型的 messages、图片、视频提交），11 条全部通过，没有例外。

## 18. 账号的全部信息随调用下发，删掉账号授权（2026-11-19，需核心 ≥0.1.304；版本仍是未发布的 0.13.0；契约见 CONTRACTS §124）

- 清单删掉 `accounts.credentials` 和 `accounts.read`：插件收到、列出自己账号类型的账号都不需要授权。前面各节提到这两个授权和 `GetAccountCredentials` 的地方是当时的记录。
- 素材库读账号改用 `Host.Account`（`HostService.GetAccount`，返回与平台调用相同的 `Account`，含代理；每次一条审计 `plugin.account.read`，审计失败即调用失败；代理拿不到时 `UNAVAILABLE`），`AssetConfigOf` 改为接收 `*pluginv1.Account`。每个请求每个账号仍最多读一次。签名后的请求照旧经 `SendAccountRequest` 由核心经账号代理发出，插件不联网。
- 测试：`routes_db_test.go` 的假宿主改实现 `Account`；`assets_test.go`、`asset_endpoints_test.go` 改用 `pluginv1.Account`；`manifest_test.go` 的权限列表去掉这两项。e2e AC43 经 mock 的代理读上游素材组。

## 19. 界面只用代码写（DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY，版本仍是未发布的 0.13.0）

- **账号表单是原生代码**（`ui/native`，清单 `form.mode: "native"`、`ui.native.entry`、`hostUICompat: "^4.0"`（起初 `^3.20`，界面契约 4.0 删除声明式写法后改为 `^4.0`）、宿主权限 `ui.native`）：`apikey` 是 `createApiKeyForm`（`VolcengineKeyForm`；`base_url`、`asset_base_url` 按 `guardedSettings` 只给官方值），`relay` 是自己拼的 `VolcengineRelayForm`（素材库一节；兼容配置一节默认收起，有值或有错时自己展开）。`secret_key` 是可清除的密钥（已存时有“清除已保存的值”）。字段、键名与原 `forms/{apikey,relay}.*.json` 相同，文案只改了一处：视频任务集合端点的帮助里，原来的 `/{task_id}`、`/{任务ID}` 写成 `/task_id`、`/任务ID`（花括号是 vue-i18n 的占位符语法）；原表单删除。两个表单的“素材库（可选）”一节带原来的说明（`SFormSection`）。
- **原 schema 的约束都由 `ValidateCredentials` 保证**（`internal/volcengine/form_constraints_test.go`，每条约束一个反例，写死在测试里）。表单没有的键保存时被拒绝（错误码 `unknown`，同原 schema 的 `additionalProperties:false`，`apikey.Spec.Fields` 声明）：`apikey` 允许 `api_key`、`base_url` 和素材库四个字段（`access_key`、`secret_key`、`asset_base_url`、`asset_region`），`relay` 再加 `video_endpoint`、`asset_endpoint`、`api_prefix`、`video_api_prefix`。原来只有 schema 挡住、这次补进 Go 的：`base_url`、`asset_base_url` 里不能有空白（`url.Parse` 接受路径里的空格，规范化后存成转义的地址；现在由 SDK 的 `apikey.NormalizeBaseURL` 统一拒绝，插件不再另查），`api_prefix`、`video_api_prefix` 里不能有空白（只在保存时检查，`validatePrefixFields`；请求时读已存的前缀不查空白，已存的值照旧可用）。
- **素材库两个页面是原生页**（`VolcengineAssetsPage`、`VolcengineAssetGroupsPage`，`SResourcePage`）：列、格式、`q` 搜索、分页与原 table 页相同；原来的两个 form 页并进各自列表页的“新建”弹窗（需要 `assets:manage`），清单删掉这两个页面和 `forms/asset*.json`。新建成功关弹窗、回第一页刷新；路由的字段错误显示在对应字段、弹窗不关。
- **素材库新建路由自己校验长度和地址**（`route_checks.go`）：`POST /asset-groups` 的 `name` ≤128、`title` ≤256、`description` ≤1024、`group_type` ≤64；`POST /assets` 的 `name` ≤128、`asset_type` ≤64、`url` ≤2048 且是不含空白的 `http(s)://` 或 `tos://` 地址。原来这些只在控制台的 schema 表单里。

## 20. 核心迁移 0118 清理 `apikey` 账号设置里的前缀键（2026-11-19，核心 0.1.304；插件不变；契约见 CONTRACTS §126）

`api_prefix`、`video_api_prefix` 只属于 `relay` 类型（14.2）。线上有 2 个 `apikey` 账号的 `settings` 里还留着 `video_api_prefix`（大概是 14.4 那种原先指向中转站、拆分前保存的账号）：`prefixesOf` 对 `apikey` 不读它，但 19 节起表单没有的键保存时报 `unknown`，于是这两个账号每次编辑凭证都被拒，界面上又删不掉这个键。核心迁移 `0118_volcengine_apikey_prefix_settings.sql` 删掉 `apikey` 账号 `settings` 里的这两个键，其余键和 `relay` 账号不动；请求路径不变。
