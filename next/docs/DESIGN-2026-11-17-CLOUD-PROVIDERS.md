# 云厂商上游：Vertex AI Gemini、Azure OpenAI、AWS Bedrock，以及 OpenAI 的组织 / 项目参数

2026-11-17，设计稿；**已在分支 `feat/cloud-providers` 上实现，已合并进 develop（`9a503d974`），未发布**。代码依据：develop `10d459e59`（核心最新发布 0.1.302）。文中“核心 0.1.N”指带上第 8 节两个扩展点的下一次核心发布。写完之后核心 0.1.303 已经发布（不含本分支，openai 0.4.3 随它发布），所以第一个带本分支的核心是 0.1.304；bedrock 清单的 `hostCompat` 写的是 `>=0.1.304 <0.2.0`，实际发布号不是 0.1.304 时要改清单和它的测试（azure_openai 不依赖核心改动，是 `>=0.1.290 <0.2.0`）。

> **2026-11-18 转发 V2（CONTRACTS §112）之后**：核心替插件签名的扩展点（`signing`）删除，签名由插件用 `sdk/reqsign` 自己做；`response_stream` 留在插件交给核心的 `UpstreamRequest` 上；azure_openai 和 openai 改成用 `sdk/pluginsdk/standard` 的代码插件；bedrock 拿到完整请求体后清洗 Claude Code 的深层字段（第 12 节的已知限制已解决）。下文里的 `BuildUpstreamRequest`、补丁、`requestFields`、清单 `upstream` 声明都是当时的接口。现行说明见 CONTRACTS §109、§112 和 [PLUGIN-CLOUD-PROVIDERS.md](PLUGIN-CLOUD-PROVIDERS.md)。

正文第 1–15 节是设计时写的，没有逐句改成实现后的样子；**实现与设计不同的地方都列在第 16 节，以第 16 节和代码为准**。没有任何真实云账号可用，所有厂商行为来自 2026-11 的官方文档，第 12 节列出没有实测过的地方。操作说明见 [PLUGIN-CLOUD-PROVIDERS.md](PLUGIN-CLOUD-PROVIDERS.md)，契约见 CONTRACTS §109。

## 1. 背景与目标

现在能接的上游是 OpenAI、Anthropic、Gemini 三家的官方接口和它们的兼容中转（openai、anthropic、gemini、relay 四个声明式插件），加上 codex、volcengine 等代码插件。三大云上的同一批模型接不进来：

- Vertex AI 上的 Gemini：地址带项目和区域，鉴权是服务账号换来的 1 小时令牌。
- Azure OpenAI：资源自己的域名，密钥放在 `api-key` 头，模型名是部署名。
- AWS Bedrock：请求要 SigV4 签名，流式响应是二进制的 `application/vnd.amazon.eventstream`。
- OpenAI 官方的 `OpenAI-Organization`、`OpenAI-Project` 现在只能靠客户端自己带，账号上没地方填。

目标：四项都能建账号、转发、计量、分类错误，账号表单全部用 schema 表单，做法符合六条规范（抽象、复用、解耦、低嵌套、简洁、低侵入）。

## 2. 范围与不做

做：

| 项 | 内容 |
|---|---|
| OpenAI | 账号上可填组织、项目 |
| Azure OpenAI | 自定义资源地址 + API Key；v1 接口和按部署的经典接口 |
| Vertex AI | Gemini 的 generateContent / streamGenerateContent / countTokens；服务账号、API Key、authorized_user 三种鉴权 |
| Bedrock | Claude 走 InvokeModel（anthropic 平台）；API Key、AK/SK、临时凭证三种鉴权。设计时还列了 OpenAI 兼容接口 `/openai/v1/*`（openai 平台），实现后去掉了，见 16.7 第 11 条 |
| 核心 | 两个与厂商无关的扩展点：请求签名、响应流分帧 |

不做（第 15 节列为后续）：Vertex 上的 Claude（rawPredict）、Azure 的 Entra ID 令牌、Bedrock 的 Converse 接口、Bedrock 自定义端点（VPC 端点）、三家的模型列表拉取、Bedrock 临时凭证自动续期、Claude Code 请求体的深度清洗。

## 3. 现状（都核对过代码）

1. **声明式上游**。账号类型在清单里写 `upstream`，核心自己实现校验、构造请求、错误分类、测试、拉模型五个入口（`server/internal/plugin/declarative/declarative.go:32-37`）；清单没覆盖的入口（刷新、额度等）仍然转给插件进程。
   - 模板变量 `{api_key}`、`{base_url}`、`{model}`、`{credentials.<k>}`、`{client_header.<名>}`。`{credentials.<k>}` 可以用在请求头、路径和查询串（`sdk/upstream/template.go:38-52`），先查凭证再查设置（`sdk/upstream/build.go:64-82`）。
   - 请求头的模板里有变量取不到值，这个头就不写（`sdk/upstream/build.go:136-141`）。
   - 路由只按协议选，不能按账号设置分支（`sdk/upstream/build.go:44-53`）；基地址固定是账号的 `base_url`，不能按别的字段拼主机（`sdk/upstream/build.go:86-89`）。
   - 声明式类型一定要有 `api_key`（`sdk/pluginsdk/apikey/apikey.go:176-200`）。
   - `classify` 只能是 `openai`、`anthropic`、`gemini`（`sdk/upstream/check.go:74-76`）。实现中一度加过 `azure_openai`，已撤掉（16.5 第 5 条）。
   - 清单的路由必须和类型服务的端点一一对应（`sdk/manifest/check/upstream.go:32-44`）。
2. **OpenAI 插件**。只有 `api_key`、`base_url`（`plugins/openai/manifest.json`、`forms/apikey.*`）。`openai-organization`、`openai-project` 在 openai 平台的 `passHeaders` 里，客户端带了就原样转给上游。清单版本 0.4.3，线上是 0.4.2（`plugins/released.json`）。预设的 key 校验要求 `sk-` / `sess-` 开头（`sdk/platforms/upstream/openai.json`），所以 Azure 的密钥填不进现有 openai 账号。
3. **网关热路径**。`attempt` 调插件的 `BuildUpstreamRequest`（`server/internal/gateway/dispatch.go:627`），`forwardBuilt` 应用补丁得到最终请求体（`:755`、`:812`），按层写请求头（`:824-833`），发出（`:876`）。响应只认 `text/event-stream` 和 JSON（`server/internal/gateway/forward.go:63-76`）；流式请求在分流前有一次内容类型嗅探（`dispatch.go:923`）。
4. **插件看不到整个请求体**。`BuildUpstreamRequest` 只收清单声明的 `requestFields`，插件执行路径（`Execute`）收到的也是同一个消息（`sdk/proto/sub2api/plugin/v1/platform.proto:197-200`）。所以插件算不出 SigV4 要的请求体哈希。
5. **令牌刷新（§48）**。类型声明 `refresh`，插件实现 `BuildRefreshRequest` / `ParseRefreshResponse`，核心每 5 分钟扫一遍、经账号代理发送、合并凭证补丁（`server/internal/account/refresh.go:41-56`、`:300-341`）。读不出过期时间的账号不会自动刷新（`refresh.go:194-198`、`:487`）。
6. **校验时归一化**。插件的 `ValidateCredentials` 可以返回归一化后的凭证和设置，核心存归一化结果（`server/internal/account/creds.go:363-395`）。
7. **同一产品的多种鉴权**。同插件内 `creationGroup` 相同的类型在新建账号时合成一项，用 `authMethodLabel` 切换（`web/src/views/accounts/accountTypeChoices.ts`、`AccountEditor.vue:66`、`:575-581`），schema 表单和 native 表单都适用。codex 是现成例子。
8. **错误分类**。`openai` 策略：401/403 禁用、429 按 `retry-after-ms` / `retry-after` 冷却、408 和 5xx 换号冷却 10 秒，其余（包括所有 400）原样回给客户端、不动账号（`sdk/pluginsdk/providers/openai/openai.go:18-29`、`sdk/pluginsdk/classify/classify.go:373`）。`api-key` 已经在凭证头名单里（`sdk/paramoverride/headers.go:42-48`）。
9. **旧实现**。仓库根目录的旧版有 Bedrock 和 Vertex 的实现可以对照：`backend/internal/service/bedrock_stream.go:251`（eventstream 解码）、`bedrock_request.go:167`（URL）、`:197`（请求体改写）、`:482`（beta 白名单）、`vertex_service_account.go:218`（JWT 换令牌）。旧的签名用了 aws-sdk-go-v2（`bedrock_signer.go`）。
10. **手写签名的先例**。volcengine 没引厂商 SDK，手写了 V4 签名，用官方 SDK 录下来的样例做黄金测试（`plugins/volcengine/internal/volcengine/arksign.go`）。这次原定照做：只用标准库，不引 aws-sdk、google SDK。**后来改了**：AWS 的签名和分帧用 AWS 的官方库（只用库，不用 SDK 的服务客户端），Vertex 的令牌断言用 `golang-jwt/jwt/v5` 签名，见 16.10。

## 4. 总体方案

| 上游 | 账号类型（鉴权） | 插件 | 插件形态 | 需要核心改动 |
|---|---|---|---|---|
| OpenAI | `apikey`（原有，加组织 / 项目） | `openai` 0.4.4 | 只有清单 | 无 |
| Azure OpenAI | `azure_v1`（API Key，v1 接口） | `azure_openai` 0.1.0（新，目录 `plugins/azure-openai`） | 只有清单 | 保留键一行（见表下） |
| Azure OpenAI | `azure_deployment`（API Key，经典接口） | 同上 | 只有清单 | 同上 |
| Vertex AI | `vertex_sa`（服务账号 JSON） | `vertex` 0.1.0（新） | gRPC 进程 | 保留键一行；令牌用 §48 现有能力 |
| Vertex AI | `vertex_authorized_user`（ADC 用户凭证 JSON） | 同上 | gRPC 进程 | 同上 |
| Vertex AI | `vertex_apikey`（API Key） | 同上 | gRPC 进程 | 保留键一行 |
| Bedrock | `bedrock_apikey`（Bedrock API Key） | `bedrock` 0.1.0（新） | gRPC 进程 | 保留键一行 + 响应流格式（8.2） |
| Bedrock | `bedrock_aksk`（长期 AK/SK） | 同上 | gRPC 进程 | 保留键一行 + 请求签名（8.1）+ 响应流格式 |
| Bedrock | `bedrock_sts`（临时凭证） | 同上 | gRPC 进程 | 同上 |

保留键一行：每个新的第一方插件的键都要加进核心的保留键列表（`server/internal/plugin/install/reserved.go` 的 `reservedKeys`，这次加了 `azure_openai`、`bedrock`、`vertex`），否则 `TestReservedCoverRepo` 失败。所以新增插件并不是“核心零改动”。它只影响没有官方签名的手动上传；Vertex 和 Azure 的功能不依赖它，从市场安装到更早的核心（`hostCompat` 下限 0.1.290）上照样能用。

三个新插件都只进市场，不进内置列表（`deploy/docker/build-go.sh:165-166` 不改；市场列表按 `plugins/*` 自动发现，`tools/sub2api-plugin/scripts/build-demo.sh:105-118`）。

客户端用别的协议访问时由核心转换（所有账号类型都接受转换，CONVERT-MATRIX §0），插件只管自己的原生协议。

## 5. OpenAI：组织与项目

只改清单和表单，不动核心。

### 5.1 账号表单

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `api_key` | API Key | secret | 凭证（加密） | 无，必填 | 原样 |
| `base_url` | Base URL | url-presets | 设置（明文） | 空 = `https://api.openai.com` | 原样 |
| `organization` | 组织 ID | text | 设置（明文） | 空 | `^[!-~]{0,128}$`，占位 `org-...` |
| `project` | 项目 ID | text | 设置（明文） | 空 | `^[!-~]{0,128}$`，占位 `proj_...` |

两个新字段放在“高级（可选）”分组（`ui:section`，折叠），帮助文案写明：填了就以账号的为准，留空则沿用客户端自己带的头。

### 5.2 清单

```jsonc
"settingsFields": ["base_url", "organization", "project"],
"upstream": {
  "preset": "openai",
  "filterFields": [],
  "credentials": { "fields": ["organization", "project"] },
  "headers": {
    "openai-organization": { "set": "{credentials.organization}" },
    "openai-project":      { "set": "{credentials.project}" }
  }
}
```

行为：引擎先带上客户端的头，再执行清单的写入（`sdk/upstream/build.go:113-133`）。账号填了值，`set` 覆盖客户端的；没填，变量取不到值，这次写入整个跳过，客户端的头保持原样。所以留空时的行为和现在逐字节相同。

写在 openai 插件的清单里，不写进 `openai` 预设：预设还被 Azure 等别的类型引用，这两个头只属于 OpenAI 官方。

WebSocket 路由（`openai.responses_ws`）用同一套共用头，握手也会带上。

### 5.3 其他

- 版本 0.4.3 → 0.4.4。0.4.3 已经随核心 0.1.303 发布（`released.json`），所以这次是单独的 0.4.4。
- 错误分类、模型、映射都不变。组织或项目填错，上游回 401，按现有规则禁用账号。
- `manifest_test.go` 里钉住的 `declaration` 常量要跟着改。

## 6. Azure OpenAI

新插件 `plugins/azure-openai`（插件键 `azure_openai`：键不能带连字符，目录名保留连字符），只有清单（`runtime: "none"`），与 openai 插件互不依赖。两个账号类型，`creationGroup: "azure-openai"`。

为什么是两个类型：v1 和经典接口路径不同，而路由不能按账号设置分支（第 3 节第 1 条）。一个类型加 `api_mode` 字段做不到，除非改成代码插件。两个类型能保持只有清单。

### 6.1 账号表单

`azure_v1`（鉴权方式标签“v1 接口”，排第一，默认选中）：

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `base_url` | 资源地址 | url-presets（无预设） | 设置 | 无，必填 | `^https?://[^\s?#]+$`；占位 `https://<资源名>.openai.azure.com` |
| `api_key` | API Key | secret | 凭证（加密） | 无，必填 | 8–512 个可见字符（不要求前缀） |

`azure_deployment`（鉴权方式标签“按部署（经典）”）：上面两项，加

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `api_version` | API 版本 | text | 设置 | `2024-10-21`（schema `default`，必填） | `^\d{4}-\d{2}-\d{2}(-preview)?$` |

地址不设 `guardedSettings`：Azure 每个资源一个域名，没有“官方地址”可以钉。后果和 relay 一样：只有 own 级权限的用户建这类账号需要 `account:relay`（CONTRACTS §21.3.1 第 3 条）。这符合用户的要求“有自定义端点的，我们支持就行了”。

### 6.2 转发

共同部分：`preset: "openai"`；`baseURL: {default: null, required: true, stripSuffixes: ["/openai/v1", "/openai"]}`（用户连 `/openai/v1` 一起粘贴也行）；`auth: {type: "header", name: "api-key", value: "{api_key}"}`；`credentials.keyValidator: null`；`models: null`；`filterFields: []`（没有采用，见 16.5 第 2 条）；不写 `classify`，用预设的 `openai` 策略（6.3）。

| 类型 | 协议 | 上游路径 |
|---|---|---|
| `azure_v1` | `openai.chat` | `/openai/v1/chat/completions` |
| | `openai.responses` | `/openai/v1/responses` |
| | `openai.embeddings` | `/openai/v1/embeddings` |
| | `openai.responses_ws` | 不提供（`null`） |
| `azure_deployment` | `openai.chat` | `/openai/deployments/{model}/chat/completions?api-version={credentials.api_version}` |
| | `openai.embeddings` | `/openai/deployments/{model}/embeddings?api-version={credentials.api_version}` |
| | `openai.responses`、`openai.responses_ws` | 不提供（`null`） |

- 类型的 `platforms[].endpoints` 分别是 `["chat_completions","responses","embeddings"]` 和 `["chat_completions","embeddings"]`，与路由一致（否则清单检查不过）。
- 经典类型的账号遇到 `/v1/responses` 请求时，核心把它转成 chat 再发（CONVERT-MATRIX：responses → chat，good）。
- 经典路由设 `model: {required: true}`；`{model}` 是映射后的模型，按路径转义。请求体里的 `model` 保留，Azure 经典接口忽略它。
- 流式 chat 沿用预设的补丁 `stream_options.include_usage = true`。
- `services.ai.azure.com` 域名只是另一个 `base_url`，不需要特别处理。

### 6.3 令牌与错误分类

没有令牌。两个类型都用预设的 `openai` 策略，不新增策略（清单不写 `classify`）。实现中一度加过 `azure_openai` 策略让 404 `DeploymentNotFound` 换号，合并后撤掉了：这件事由管理员在失败处理设置里配重试规则完成（16.5 第 5 条）。插件的 `hostCompat` 是 `>=0.1.290 <0.2.0`。

| 上游回答 | 结果 | 依据 |
|---|---|---|
| 400 `content_filter` 及其他 400 | 原样回客户端，不换号、不动账号 | 没有规则命中 400 |
| 401、403 | 换号，禁用账号 | `Credentials` 规则 |
| 429 且是 `insufficient_quota` | 换号，禁用账号 | `openai` 策略原有的规则 |
| 429 | 换号，冷却到 `retry-after-ms` / `retry-after` 指的时间，没有就 60 秒 | `RateLimit` |
| 404（包括 `DeploymentNotFound`） | 原样回客户端，不动账号；要换号由失败策略的重试规则决定（16.5 第 5 条） | 没有规则命中 |
| 408、5xx | 换号，冷却 10 秒 | `ServerErrors` |

### 6.4 模型与映射

上游的“模型”是部署名。清单不预填 `defaultModels`（部署名是用户自己起的）；用账号的模型列表和模型映射把客户端模型名对到部署名（CONTRACTS §18）。测试默认模型 `gpt-4o-mini`，没有同名部署时在测试对话框里指定。

## 7. Vertex AI（Gemini）

新插件 `plugins/vertex`，gRPC 进程，平台 `gemini`，端点 `generate_content`、`stream_generate_content`、`count_tokens`。三个账号类型，`creationGroup: "vertex"`，`upstreamFields: []`（主机由区域决定，用户不能自己填地址），需要 `accounts.credentials` 授权（代码类型要读凭证）。

三个类型都由插件代码实现，共用一个 URL 构造函数。API Key 类型不做成声明式：基地址不能按区域拼主机，路由也不能按“有没有填项目”分支（第 3 节第 1 条）；拆成声明加代码两处反而重复。

### 7.1 账号表单

`vertex_sa`（服务账号，排第一）：

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `service_account_json` | 服务账号密钥 | secret + textarea（多行、等宽） | 凭证（加密） | 无，必填 | 插件解析：`type` 为 `service_account`，`client_email`、`private_key` 齐全且私钥能解析，`token_uri` 必须是 `https://oauth2.googleapis.com/token` |
| `project_id` | 项目 ID | text | 设置 | 空 = 取 JSON 里的 `project_id`（保存时由插件填进设置） | `^[a-z][a-z0-9-]{4,28}[a-z0-9]$` |
| `location` | 区域 | select（可搜索，可输入） | 设置 | `global` | `^[a-z0-9-]{2,32}$` |
| `access_token`、`expires_at`、`token_for` | — | hidden | 凭证（加密） | 由核心刷新写入 | 用户不可见 |

`vertex_authorized_user`（用户凭证）：同上，把第一项换成 `authorized_user_json`（校验：`type` 为 `authorized_user`，`client_id`、`client_secret`、`refresh_token` 齐全）；`project_id` 必填（这种 JSON 里的 `quota_project_id` 只作为默认值）。

`vertex_apikey`（API Key）：

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `api_key` | API Key | secret | 凭证（加密） | 无，必填 | 8–512 个可见字符 |
| `project_id` | 项目 ID（可选） | text | 设置 | 空 | 同上，允许空 |
| `location` | 区域（可选） | select | 设置 | 空 | 同上，允许空 |

`project_id` 和 `location` 都填了才走带项目的完整地址，否则走 express 地址。只填一个时保存报错，指出要么都填要么都不填。

隐藏字段的用处：编辑账号时表单把整个凭证对象带回来（`AccountEditor.vue:471`），敏感字段回传掩码、核心还原（`creds.go:118`），所以改别的字段不会把已有令牌冲掉。它们必须写进 schema（`additionalProperties: false`），用 `ui:widget: "hidden"` 不显示（`web/packages/ui/src/schema/SchemaObject.vue:42-45`）。

### 7.2 转发

主机：`location` 为 `global` 时 `aiplatform.googleapis.com`，否则 `{location}-aiplatform.googleapis.com`。

| 情况 | URL |
|---|---|
| 服务账号、用户凭证、填了项目和区域的 API Key | `https://{主机}/v1/projects/{project_id}/locations/{location}/publishers/google/models/{model}:{动作}` |
| 没填项目和区域的 API Key（express） | `https://aiplatform.googleapis.com/v1/publishers/google/models/{model}:{动作}` |

动作按 `meta.protocol`：`gemini.generate` → `generateContent`，`gemini.stream_generate` → `streamGenerateContent?alt=sse`，`gemini.count_tokens` → `countTokens`。模型取 `meta.model`，去掉 `models/` 前缀，按路径转义。

请求头：`content-type: application/json`；服务账号和用户凭证加 `authorization: Bearer {access_token}`，API Key 加 `x-goog-api-key`。请求体与 Gemini Developer API 相同，不打补丁；用量规则沿用 gemini 平台的（`usageMetadata.*`）。

账号还没有令牌（刚建，等第一次刷新）时，`BuildUpstreamRequest` 返回 `FAILED_PRECONDITION`，网关换下一个账号，不惩罚这个账号（`dispatch.go:634-636`）。

测试请求：`generateContent` + `gemini.GenerateContentPingBody(1)`，默认模型 `gemini-2.5-flash`。模型列表：返回 `UNIMPLEMENTED`，靠清单的 `defaultModels`。

### 7.3 令牌

两个带令牌的类型声明 `"refresh": {}`（默认字段 `expires_at`，提前 1800 秒）。令牌有效 1 小时，所以大约每半小时换一次，失败后每 5 分钟重试，有 6 次机会。

**请求**（`BuildRefreshRequest`，纯计算）：

- 服务账号：用私钥签 RS256 JWT（`iss` = `client_email`，`scope` = `https://www.googleapis.com/auth/cloud-platform`，`aud` = `https://oauth2.googleapis.com/token`，`exp` = `iat` + 3600，头里带 `kid`），`POST https://oauth2.googleapis.com/token`，表单体 `grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=…`，`Content-Type: application/x-www-form-urlencoded`。JWT 由 `github.com/golang-jwt/jwt/v5` 编码并签名，密钥文件由插件自己用 `encoding/json` 解析（16.10）；插件仍然不联网。
- 用户凭证：同一地址，`grant_type=refresh_token&client_id=…&client_secret=…&refresh_token=…`。

**回答**（`ParseRefreshResponse`）：200 → 补丁 `{access_token, expires_at: 现在 + expires_in, token_for: 指纹}`；400 `invalid_grant`、`invalid_client`、401、403 → `AUTH_REJECTED`（密钥被删、被停用、刷新令牌作废）；其他 → `TRANSIENT`。

`token_uri` 钉死为 Google 官方地址：这个请求由核心替插件发，不能让粘贴进来的 JSON 指到别处。

**第一枚令牌**。codex 的第一枚令牌是授权流程里插件自己换来的；Vertex 用 schema 表单，没有授权流程。做法：

1. `ValidateCredentials` 算出密钥指纹（`client_email` + `private_key_id` 的 SHA-256；用户凭证是 `client_id` + `refresh_token`）。提交的凭证里没有令牌，或者 `token_for` 和指纹不同（换了密钥），就在归一化结果里去掉旧令牌并写 `expires_at: 1`。
2. `expires_at: 1` 是一个读得出来的、已经过期的时间，下一轮扫描（最多 5 分钟）就会刷新。不依赖任何核心改动。
3. 等不及可以在账号详情点“立即刷新令牌”（`POST /accounts/:id/refresh-credentials`，现有功能）。
4. 核心 0.1.N 顺带加一个小改进（任务包 C2，Vertex 不依赖它）：保存账号后，类型声明了 `refresh` 就异步调一次 `refreshAccount(id, false)`。非强制调用只刷新已到期的，所以对 codex 没有影响，Vertex 账号则保存后几秒内就有令牌。

没有采用的做法：插件在校验时自己联网换令牌（要给插件 `net` 权限，而且走不了账号代理）。

### 7.4 错误分类

在 `gemini.Policy` 前面加一条规则（`Policy.With`），其余沿用：

| 上游回答 | 服务账号 / 用户凭证 | API Key |
|---|---|---|
| 401 UNAUTHENTICATED | 换号，冷却 60 秒，不禁用（令牌问题，等下次刷新；密钥真失效时刷新状态会显示 `auth_rejected`） | 换号，禁用 |
| 403 PERMISSION_DENIED | 换号，禁用 | 同左 |
| 400 INVALID_ARGUMENT | 原样回客户端 | 同左（key 无效的 400 按现有规则禁用） |
| 404 | 原样回客户端 | 同左 |
| 429 RESOURCE_EXHAUSTED | 换号，按 `retry-after` / `RetryInfo` 冷却 | 同左 |
| 500、503 | 换号，不动账号 | 同左 |

### 7.5 模型与映射

`defaultModels` 预填 Vertex 上的 Gemini 模型（与 gemini 插件的列表一致，实现时按 Vertex 文档核对哪些有 `global` 区域）。不预填映射：Vertex 的 Gemini 模型名与 Developer API 相同。

## 8. 核心扩展点

两处都是给 `BuildUpstreamRequestResponse` 加可选字段，插件不填时行为完全不变。它们不带任何厂商判断：核心只认“方案名”和“格式名”，按名字查表。

```protobuf
// common.proto
message RequestSigning {
  string scheme = 1;             // "aws-sigv4"
  string region = 2;
  string service = 3;
  string access_key_id = 4;
  string secret_access_key = 5;
  string session_token = 6;      // 可选
}

// platform.proto, BuildUpstreamRequestResponse
RequestSigning signing = 6;      // 请核心在发送前对最终请求签名
string response_stream = 7;      // 响应流的格式，如 "aws-eventstream-chunk"
```

清单不加字段。要不要签名、响应是什么分帧都取决于这一次请求（鉴权方式、是不是流式），放在构造结果里比放在清单里准确。

### 8.1 请求签名

**为什么在核心**：签名要对最终请求体取哈希，而插件拿不到请求体（第 3 节第 4 条）；请求体在插件返回之后还会被补丁和强制流式改写。

**库**：新包 `sdk/reqsign`。签名由 aws-sdk-go-v2 的 `aws/signer/v4` 完成（16.10；原设计是只用标准库手写）。

```go
type Directive struct{ Scheme, Region, Service, AccessKeyID, SecretAccessKey, SessionToken string }
func Sign(req *http.Request, payload []byte, d Directive, now time.Time) error   // 未知方案返回错误
```

内部是一张“方案名 → 签名函数”的表，现在只有 `aws-sigv4`。放在 SDK 而不是核心内部，是因为插件的测试请求也要用它（见下）。

**挂点**：`forwardBuilt` 里所有请求头写完之后、发送之前，即 `dispatch.go:833` 之后加一处调用（实现放在新文件 `gateway/upstream_ext.go`，`dispatch.go` 只多两行）。这时请求体、URL、所有请求头层都已定，重定向本来就不跟随。插件执行路径也经过 `forwardBuilt`，自动覆盖。

**SigV4 的细节**（都要有测试钉住）：

- 签名的头：`host`、`x-amz-date`，有的话加 `content-type`、`x-amz-security-token`。别的头不签，所以透传、参数覆盖加的头不影响签名。
- 规范路径：对 `req.URL.EscapedPath()` 的每一段再转义一次（S3 以外的服务都是双重转义）。模型 ID 里的 `:` 在线上是 `%3A`，在规范路径里是 `%253A`。
- 主机取 `req.Host`，为空取 `req.URL.Host`。
- 写入 `X-Amz-Date`、`Authorization`，有临时凭证时写 `X-Amz-Security-Token`；先删掉已有的 `Authorization`。
- 签名失败或方案名不认识：这次尝试按插件不可用换号，绝不发出未签名的请求。
- 签名指令带着密钥，不得进日志。

**黄金样例**（照 volcengine 的做法，两个来源）：

1. AWS 公布的 SigV4 测试套件样例。
2. 用旧版已经依赖的 aws-sdk-go-v2 签名器，对 Bedrock 形状的请求（模型 ID 带 `:`、ARN 带 `/`、带临时凭证）固定时间和密钥录下 `Authorization`，存进 `sdk/reqsign/testdata`。录下的结果仍然逐字节比对（现在 `reqsign` 自己就调用这个签名器，样例钉住的是“交给它签哪些头”和库升级后的输出，见 16.10）。

**测试请求**：账号测试的请求体是插件自己给的（`server/internal/account/testreq.go:211-245` 原样发送），插件在 `BuildTestRequest` 里直接调 `reqsign.Sign` 即可，账号测试的代码不用改。

**声明式扩展模式**：`declarative.merge`（`declarative.go:95-116`）要把这两个新字段从插件的修改结果里带过去，否则以后用 extend 的类型会丢掉它们。

volcengine 的素材库签名（插件自己发请求、算法细节不同）以后可以作为第二个方案并进这张表，这次不动。

### 8.2 响应流分帧

**库**：新包 `sdk/awseventstream`：帧解码器，外加给测试和模拟上游用的编码器，两处校验和都验。消息的解码和编码由 aws-sdk-go-v2 的 `aws/protocol/eventstream` 完成，包自己只读帧头并限制长度（16.10；原设计是只用标准库、从 `backend/internal/service/bedrock_stream.go:230-398` 移植）。

**适配器**（`awseventstream.NewChunkSSEReader`，格式名 `aws-eventstream-chunk`）：一个 `io.Reader`，读 eventstream 帧，吐 SSE 文本：

| 帧 | 输出 |
|---|---|
| `:message-type` = `event`，负载 `{"bytes":"<base64>"}` | 解出 base64 得到模型原生事件 JSON，输出 `event: <JSON 的 type>\ndata: <JSON>\n\n`（没有 `type` 就只有 `data:` 行） |
| `:message-type` = `exception` 或 `error` | 输出 `event: error\ndata: {"type":"error","error":{"type":"<异常类型>","message":"<消息>"}}\n\n`，然后结束 |
| 其他事件、没有 `bytes` 的负载 | 跳过 |
| 帧损坏（长度不合法、校验和不对、超过格式上限 16 MiB）或截断（适配器自己的 4 MiB 上限 `MaxChunkMessageSize` 已在转发 V2 收尾时删除，CONTRACTS §112.16） | 读取返回错误，按上游中断处理 |

**挂点**：`dispatch.go:923` 的嗅探之前。插件的构造结果里 `response_stream` 非空、上游状态是 2xx、并且响应的 `Content-Type` 就是该格式的媒体类型时，把 `resp.Body` 换成适配器，把 `Content-Type` 改成 `text/event-stream`。此后的一切（SSE 转发、用量规则、协议转换、首 token 计时、空闲超时、断开计费）都不用改，看到的就是普通 SSE。

格式名查不到：按插件不可用换号。内容类型对不上（上游回了 JSON 错误或者普通 SSE）：不处理，照原样走。

错误事件的形状选的是用量规则已经认识的那种（事件名 `error`、`error.message`，`server/internal/usagerules/usagerules.go:284-293`），所以流中途的异常会被记成上游流错误并交给故障策略。

### 8.3 兼容

- 旧插件不填新字段，不受影响；旧核心遇到新字段会直接忽略，结果是不签名就发出去、被 AWS 回 403、账号被禁用。所以 bedrock 插件的 `hostCompat` 必须写 `>=0.1.N`，装不到旧核心上。
- 不加能力 ID、不改 Host API 版本：两个字段是构造结果的可选部分，`hostCompat` 足够表达依赖。
- 网关二进制有变更，按核心发布流程走。

## 9. AWS Bedrock

新插件 `plugins/bedrock`，gRPC 进程。三个账号类型，`creationGroup: "bedrock"`，`upstreamFields: []`，需要 `accounts.credentials` 授权，`hostCompat: ">=0.1.N <0.2.0"`。

平台：

| 平台 | 端点 | 上游 |
|---|---|---|
| `anthropic` | `messages`（`passHeaders: ["anthropic-beta"]`，`requestFields: ["model", "anthropic_beta"]`） | InvokeModel / InvokeModelWithResponseStream |

只有 anthropic 平台。设计时还有 openai 平台（`chat_completions`、`responses`，发往 Bedrock 的 `/openai/v1/*`），实现后去掉了：没有类型声明它，Bedrock 的 OpenAI 兼容接口完全不用。客户端用 OpenAI 协议（chat completions、responses）访问时，核心先把请求转换成 Messages 请求，再走同一条 InvokeModel 路。原因见 16.7 第 11 条。

`count_tokens` 不声明，由核心本地估算（CONVERT-MATRIX §0 第 6 条）。

### 9.1 账号表单

所有类型共有：

| 字段 | 标题 | 控件 | 存储 | 默认 | 校验 |
|---|---|---|---|---|---|
| `region` | 区域 | select（可搜索，可输入） | 设置 | `us-east-1` | `^[a-z]{2}(-[a-z]+)+-\d$`（它会拼进主机名，必须严格）；`cn-*` 和隔离分区另外拒绝（16.7 第 14 条） |
| `inference_profile_prefix` | 跨区域推理前缀 | select：不加 / us / eu / apac / jp / au / in / global | 设置 | `global`（最初是“不加”，16.7 第 3 条） | 枚举 |

各类型自己的：

| 类型（标签） | 字段 | 控件 | 存储 | 默认 / 校验 |
|---|---|---|---|---|
| `bedrock_apikey`（API Key） | `api_key` | secret | 凭证 | 必填，8–2048 个可见字符；帮助写明短期 Key 最长 12 小时且绑定区域 |
| `bedrock_aksk`（Access Key） | `access_key_id` | text | 凭证 | 必填，`^[A-Z0-9]{16,128}$` |
| | `secret_access_key` | secret | 凭证 | 必填 |
| `bedrock_sts`（临时凭证） | `access_key_id`、`secret_access_key` | 同上 | 凭证 | 同上 |
| | `session_token` | secret + textarea | 凭证 | 必填 |
| | `session_expires_at` | date-time | 设置 | 可选，只作提示，不参与任何逻辑；帮助写明到期后要手动换凭证 |

后两个类型在代码里是同一条路径，区别只是有没有 `session_token`。分成两个类型是为了让表单把“会过期、不会自动续”说清楚。

### 9.2 转发

主机固定 `https://bedrock-runtime.{region}.amazonaws.com`。

**anthropic 平台**（`anthropic.messages`）：

- URL：`/model/{模型}/invoke`，`meta.stream` 为真时 `/model/{模型}/invoke-with-response-stream`。
- 模型：取 `meta.model`（已经过映射和账号参数覆盖）。设置了前缀、模型不是 ARN、也没有已知前缀（`us.` `eu.` `apac.` `jp.` `au.` `in.` `us-gov.` `global.`）时，在前面加 `<前缀>.`。整个 ID 作为一个路径段转义，`:` 再换成 `%3A`（同 `bedrock_request.go:167-180`）。
- 请求体补丁（`internal/bedrock/upstream.go`、`beta.go`，都只动顶层字段）：
  - 删 `model`、删 `stream`、设 `anthropic_version` 为 `"bedrock-2023-05-31"`。
  - 删 `metadata`、`provider`（Anthropic 自家接口的字段，InvokeModel 用不上）。
  - `anthropic_beta`：把客户端 `anthropic-beta` 头里的和请求体自带的 `anthropic_beta`（数组，或一个逗号分隔的字符串）里的特性合在一起，先头后体，别名改名、不在白名单里的丢弃、去重，带了 tool search 就补上 tool examples（白名单见 16.7 第 6 条）。有剩下的就写成数组；一个都不剩而请求体原来有这个字段时删掉它。头本身不转发。
  - `context_management`：结果里没有 `context-management-2025-06-27` 时删掉。
  - `output_config`、`output_format` **保留**（旧版会删，见 16.7 第 13 条）。
- 请求头：`content-type: application/json`、`accept: application/json`。
- 流式时构造结果带 `response_stream: "aws-eventstream-chunk"`（`awseventstream.FormatChunk`）。
- 删掉 `model` 不触发补丁守卫：守卫只比对声明的附加模型引用（`fallbacks`、advisor），不含主模型（`server/internal/gateway/model_references.go:326-342`）。

**openai 平台**：没有实现（16.7 第 11 条）。`BuildUpstreamRequest` 收到 `anthropic.messages` 以外的协议回 `INVALID_ARGUMENT`。

**鉴权**：

| 类型 | 做法 |
|---|---|
| `bedrock_apikey` | 请求头 `authorization: Bearer {api_key}` |
| `bedrock_aksk`、`bedrock_sts` | 构造结果带 `signing {scheme: "aws-sigv4", region, service: "bedrock", access_key_id, secret_access_key, session_token}`，核心签名 |

测试请求：非流式 InvokeModel，请求体是去掉 `model` 的 Anthropic ping 加 `anthropic_version`；AK/SK 类型由插件调 `reqsign.Sign` 签好。默认测试模型在清单里定。模型列表：`UNIMPLEMENTED`（列表在控制面 `bedrock.{region}`，这次不做）。

### 9.3 令牌

没有自动刷新。短期 API Key 和临时凭证到期后上游回 403，按 9.4 处理，管理员手动换。

### 9.4 错误分类

插件自带策略（`internal/bedrock/classify.go` 的 `policy`）。错误类型取响应头 `x-amzn-errortype`（去掉 `:` 之后的部分和 `#` 之前的命名空间），没有就取响应体的 `__type`；消息取 `message`（没有再取 `Message`）。比较错误类型时不看 `Exception` 后缀。回给客户端的错误类型用 Anthropic 的词表（只有 anthropic 平台）。

下表按规则的顺序写，先命中的生效；除“原样回客户端”外都换号：

| # | 上游回答 | 结果 |
|---|---|---|
| 1 | 403 且是边缘拦截页（`classify.EdgeBlock`） | 不动账号 |
| 2 | 429（任何错误类型；ThrottlingException、ModelNotReadyException） | 冷却到 `retry-after-ms` / `retry-after` 指的时间，没有就 60 秒 |
| 3 | 400 且是 ServiceQuotaExceeded(Exception) | 不动账号，原因 `upstream service quota exceeded (400)`：AWS 账号（某个模型、某个区域）的配额用完，别的账号可能还有，这个账号没坏 |
| 4 | RequestExpired；或 InvalidSignature 且消息含 `signature expired` / `not yet current`（不看大小写） | 不动账号，原因写“检查节点时钟” |
| 5 | IncompleteSignature | 不动账号（是核心签名的问题，不是账号的） |
| 6 | ExpiredToken | 禁用，原因写“临时凭证已过期” |
| 7 | 401（任何错误类型）；或 UnrecognizedClient、InvalidClientTokenId、InvalidSignature（第 4 条没命中的） | 禁用（密钥不对） |
| 8 | 其余所有 403（多数是 AccessDeniedException） | 冷却 60 秒，不禁用（见第 10 节偏差 6） |
| 9 | 408（ModelTimeoutException）、5xx（500、503 ServiceUnavailableException、529） | 不动账号 |
| — | 没有规则命中：400 ValidationException 及其他 400、404 ResourceNotFoundException、424 ModelErrorException | 原样回客户端 |
| — | 流中途的异常帧 | 响应已经开始，客户端收到 `error` 事件；记为上游流错误，交给故障策略 |

第 3–7 条除了写明的以外不看状态码，只看错误类型。

### 9.5 模型与映射

客户端用的是 Anthropic 的模型名，Bedrock 要的是 `anthropic.claude-…-v1:0` 这样的 ID、带区域前缀的推理配置 ID 或 ARN。做法：清单 `defaultModels` 列 Anthropic 模型名，`defaultModelMapping` 预填“Anthropic 名 → Bedrock ID”（不带区域前缀；实现里 Opus 4.1 例外，16.7 第 9 条），新建账号时带进表单，管理员可以改（CONTRACTS §41）。前缀由 `inference_profile_prefix` 统一加。映射表的具体 ID 实现时从 AWS 文档抄，逐个核对，本文不列（实际用的 ID 和出处见 16.7 第 9 条）。

插件不内置别名表：映射只有账号上那一份，看得见、改得了。

### 9.6 没有采用：`/anthropic/v1/messages`

bedrock-runtime 上还有一个 Anthropic 原生接口 `POST /anthropic/v1/messages`：请求体带 `model`、响应是 SSE、不需要分帧，用 Bedrock Key 时只要清单就能接。没有选它作主路径：文档只写了 `x-api-key` 加 Bedrock Key 的用法，SigV4 能不能用没有确认，AK/SK 和临时凭证两种账号就没着落；InvokeModel 是三种鉴权都确定可用的路径。等有真实账号验证后，可以给 `bedrock_apikey` 加这条路（第 15 节）。

同样没有采用：SigV4 的 `UNSIGNED-PAYLOAD`（只有 S3 有文档支持）；在插件里引 aws-sdk 的服务客户端（请求体要原样透传、请求要走账号代理、响应流由核心中继，服务客户端三样都接管不了；签名器和分帧库这两个不发请求的包后来用上了，见 16.10）。

## 10. 与初始决定的偏差

| # | 初始决定 | 改成 | 原因 |
|---|---|---|---|
| 1 | Azure 用一个 `api_mode` 选项，或两个类型 | 确定两个类型 | 路由不能按设置分支，一个类型就不是只有清单了 |
| 2 | 考虑新增 `azure` 错误策略 | 不新增，用 `openai`（6.3、16.5 第 5 条；实现中加过 `azure_openai`，已撤掉） | 400 `content_filter` 现在就是原样回客户端、不动账号；`retry-after-ms` 现在就读；新增策略还得改 SDK 的校验。404 `DeploymentNotFound` 换号交给失败策略的重试规则 |
| 3 | Azure 经典接口也提供 responses | 经典类型不提供 | 经典路径下 responses 只有预览版本；这类账号的 responses 请求由核心转成 chat |
| 4 | Vertex API Key 类型可能纯声明式 | 三个类型都用代码 | 主机随区域变、“填没填项目”要分支，声明式都表达不了 |
| 5 | Vertex 首枚令牌“设计一下” | `expires_at: 1` + 现有扫描，另加一个可选的核心小改进 | 现有扫描跳过读不出过期时间的账号，所以要写一个读得出的过期值；这样 Vertex 不依赖核心发布 |
| 6 | Bedrock 403 AccessDenied 算账号问题（禁用） | 冷却 60 秒，不禁用 | Bedrock 的模型访问是按模型开通的，一个模型没开通不该停掉整个账号。**这条需要用户定** |
| 7 | 400 IncompleteSignature / RequestExpired 算账号配置问题 | 换号，不动账号 | 签名和时间戳是核心节点生成的，出错是节点时钟或核心的问题，禁用账号会把所有 Bedrock 账号一起停掉 |
| 8 | 响应流格式可以写在平台端点声明里 | 只放在构造结果里 | 同一个类型同一个协议，只有流式请求才是 eventstream |
| 9 | 账号测试也由核心签名 | 插件在 `BuildTestRequest` 里自己签 | 测试请求体是插件给的，插件算得出哈希；签名库放在 SDK，账号测试的代码不用动 |
| 10 | Bedrock 临时凭证的 `expires_at` | 改名 `session_expires_at` | 避免和 §48 的默认字段名 `expires_at` 混淆 |
| 11 | 插件处理 Claude Code 的各种不兼容字段 | 首版不做深度清洗 | 插件看不到请求体，补丁只能按固定路径改，做不了“每个工具里删某字段”。见第 12 节第 7 条 |

另有一条不算偏差的发现：Azure 的 v1 接口文档示例用的是 OpenAI SDK，即 `Authorization: Bearer <key>`，按理现有 openai 账号把 Base URL 填成 `https://<资源>.openai.azure.com/openai` 就能用，但被 `sk-` 前缀校验挡住了。这次仍然做独立插件（`api-key` 头两种接口都有文档），不去放宽 openai 插件的校验。

## 11. 任务拆分

每个任务包只动自己列出的文件，可以分给不同的代理同时做。

| 包 | 内容 | 文件 | 验收 | 依赖 |
|---|---|---|---|---|
| **W0 脚手架** | 三个新插件目录的 `go.mod`（两条 `replace`）、`go.work` 三行 | `go.work`、`plugins/{azure-openai,vertex,bedrock}/go.mod` | `go work sync` 通过 | 无 |
| **W1 OpenAI** | 5 节 | `plugins/openai/manifest.json`、`forms/apikey.*`、`manifest_test.go` | 清单检查过；填了组织时覆盖客户端头、留空时与现有黄金样例输出相同（两个用例都要有） | 无 |
| **W2 Azure** | 6 节 | `plugins/azure-openai/**`（清单、两套表单、图标、`manifest_test.go`） | 清单检查过；两个类型每条路由的 URL、`api-key` 头、缺 `base_url` 报错、`/openai/v1` 后缀被去掉；`e2e/ac29_azure_test.go` 对模拟上游跑通 chat 流式与非流式、用量入账 | W0；e2e 依赖 W8 |
| **W3 Vertex** | 7 节 | `plugins/vertex/**` | 校验（三种 JSON 的好坏样例、指纹变了重置令牌、项目区域成对）；URL 表全覆盖；JWT 用测试密钥验签、声明字段正确；刷新回答的三类结果；分类表逐行；对假上游的往返测试 | W0 |
| **S1 签名库** | 8.1 的库 | `sdk/reqsign/**` | 两个来源的黄金样例逐字节相同；改一个字节的请求体签名就变；未知方案报错 | 无 |
| **S2 分帧库** | 8.2 的库 | `sdk/awseventstream/**` | 编码再解码还原；两处校验和错、截断帧、超长帧都报错；异常帧转成 `error` 事件；旧版测试里的帧样例移植过来 | 无 |
| **C1 核心接入** | proto 两个字段与生成代码；`gateway/upstream_ext.go` 与 `dispatch.go` 两处调用；`declarative.merge` 带上新字段 | `sdk/proto/**`、`sdk/gen/**`、`server/internal/gateway/upstream_ext.go`、`dispatch.go`、`server/internal/plugin/declarative/declarative.go`、`gateway/upstream_ext_test.go` | 假插件返回签名指令，假上游独立重算签名通过（含参数覆盖改了请求体、强制流式的情况）；假上游回 eventstream，客户端收到 SSE、用量入账、转换路由可用；未知方案和未知格式都换号且没有请求发出；不带新字段的全部现有测试不变；日志里没有密钥 | S1、S2 |
| **C2 保存后刷新** | 7.3 第 4 点 | `server/internal/account/handlers.go`、`refresh.go`、对应测试 | 保存到期的账号后触发一次刷新；未到期的不触发 | 无 |
| **W4 Bedrock** | 9 节 | `plugins/bedrock/**` | 校验；URL 与模型编码表（含前缀、ARN）；补丁与 beta 白名单；三种鉴权的构造结果；测试请求的签名能被独立重算；分类表逐行；对假上游的往返测试（测试里用 S1、S2 充当核心） | W0、S1、S2、C1（proto 字段） |
| **W8 模拟上游** | Azure 两套路径；供本地联调的 Bedrock invoke（eventstream）与 Vertex 路径 | `deploy/mock-upstream/**` | 各路径的单测 | S2 |
| **D1 文档** | CONTRACTS 新增一节（两个扩展点、三个插件、OpenAI 字段），§48 补“保存后刷新”；PLUGIN-DEVELOPMENT §3 表格加三行；UPSTREAM-DECLARATIVE 记 extend 带新字段 | `docs/CONTRACTS.md`、`docs/PLUGIN-DEVELOPMENT.md`、`docs/UPSTREAM-DECLARATIVE.md` | 与实现一致 | 全部完成后 |

依赖图：`W0 → W2、W3、W4`；`S1、S2 → C1 → W4`；`S2 → W8 → W2 的 e2e`；W1、C2、S1、S2 可以立刻开始。发布后改 `plugins/released.json`（集成时一次做完，不属于任何一个包）。

## 12. 测试方案

三层，都不需要真实云账号：

1. **单元与黄金样例**。签名、分帧、JWT、URL 与模型编码、补丁、错误分类表、清单检查。黄金样例的来源见 8.1。
2. **网关层**（`server/internal/gateway/*_test.go` 现有的环境：假插件 + `httptest` 假上游）。验证两个扩展点在真实的请求体改写、请求头分层、SSE 转发、用量、协议转换之下工作。假上游自己重算签名，不复用被测代码。
3. **插件对假上游**。Vertex、Bedrock 的主机是写死的，测试里用可替换的端点（codex 的 `NewWithEndpoints` 先例，`plugins/codex/internal/codex/plugin.go:76-126`）指向 `httptest`。Azure 的地址本来就是账号填的，走 docker e2e（`next/e2e`，同 `ac20_openai_gemini_test.go`）。

Vertex 和 Bedrock 首版没有 docker e2e：那需要一个在生产包里也生效的端点覆盖开关，为了测试开这个口子不值得。它们的全链路由第 2、3 层合起来覆盖。

### 没有验证过的事项

下面这些只有文档依据，或者文档也没说清，要等有真实凭证时逐条验证。验证之前不要对外说“已支持”。2026-11-17 实现后更新：第 1–8 条除第 2 条（已不适用）外仍然没有验证，第 9 条起是实现后新增的。

1. Bedrock：ARN 作模型 ID 时的路径编码（`/` → `%2F`、`:` → `%3A`）以及它的 SigV4 规范路径。签名本身只对黄金样例和模拟上游验证过，没有被真实的 AWS 验过。
2. Bedrock：`/openai/v1/*` 用 SigV4 签名、流式时是否接受 `stream_options`、支持哪些模型。**已不适用**：openai 平台已从插件里去掉，这条接口不用了（16.7 第 11 条）。
3. Bedrock：beta 白名单是否还是 2026-11 的现状（旧版的 8 项照抄，没有对照 AWS 文档；后加的 7 项取自 AWS 用户指南，见 16.7 第 6 条；都没有用真实账号试过）；`output_config`、`output_format` 留在请求体里是否被 InvokeModel 接受（依据是 AWS 文档，16.7 第 13 条）；流的最后一个事件里的 `amazon-bedrock-invocationMetrics` 字段原样透给客户端是否有客户端在意（首版不删）。
4. Vertex：绑定服务账号的 API Key 能不能用在带项目和区域的完整地址上；express 地址支持哪些模型。
5. Vertex：请求体与 Developer API 的细小差别（已知 Vertex 对 `contents[].role` 更严格）。首版不改请求体，客户端不带 `role` 时可能被上游拒绝。
6. Azure：`services.ai.azure.com` 域名下 v1 路径是否完全一致（表单接受 `/api/projects/<名>` 结尾的地址，请求会发到 `<该地址>/openai/v1/...`，这个路径没有验证）；经典接口 `2024-10-21` 是否接受 `stream_options`；v1 的 `/embeddings`。
7. Bedrock + Claude Code：Claude Code 会在工具定义里带 `custom`、在 `cache_control` 里带 `scope` / `ttl`，旧版为此做了深度清洗（`bedrock_request.go:197-265`）。首版插件没有做，三个表单的“区域与模型”分组说明里写了这条限制。可以在账号的参数覆盖（§84，带通配路径的删除和 `prune_objects`）里手工配，但这套配置没有实测过。**如果 Bedrock 账号主要给 Claude Code 用，这一条要先解决再上线**。
8. 所有错误分类表都来自文档里的错误码，没有用真实回答核对过。
9. Bedrock：默认模型映射（`defaultModelMapping`）的 ID 已经按 AWS 模型卡片改过（16.7 第 9 条），但只对照了文档，没有用真实账号调过；卡片上已知的出入见 16.7 第 16 条。按模型卡片，现行的 Claude 模型在 bedrock-runtime 上几乎都没有区域内（In-Region）可用性，只能经推理配置调用，所以前缀的默认值是 `global`、默认测试模型是 Haiku 4.5 的 `global.` 配置（16.7 第 3、8 条）；“新建账号用默认设置能调通预填的模型”同样只是按文档推出来的，没有实测。
10. Bedrock：短期 API Key 过期、临时凭证过期时上游实际回的状态码和错误类型（现在的规则：`ExpiredToken` 禁用，其余 403 冷却 60 秒）。
11. Vertex：用户凭证账号带 `x-goog-user-project` 是否足够、用户在该项目上还需要什么权限；令牌端点对时钟偏差的报错文字是否就是代码里匹配的那两句（`reasonable timeframe`、`short-lived token`）。
12. Vertex：`defaultModels` 与 gemini 插件的预设列表相同，没有按 Vertex 文档核对哪些模型在 Vertex 上存在、哪些有 `global` 区域。
13. Azure：404 `DeploymentNotFound` 的换号靠管理员配的失败策略重试规则（16.5 第 5 条）。没有验证的是：Azure 在部署不存在时回的错误码是否就是 `DeploymentNotFound`、v1 接口和经典接口是否相同（规则的关键词同时写 `DeploymentNotFound` 和 `model_not_found`）。
14. Azure 的 docker e2e（`e2e/ac29_azure_test.go`）已随整套 e2e（49 个用例）在与转发 V2 合并后的代码上运行并通过；CI 只编译 e2e，不运行。
15. **控制台里的新表单（Azure 两个、Vertex 三个、Bedrock 三个、OpenAI 的“高级”分组）没有做过界面走查**：分组折叠、鉴权方式切换（`creationGroup`）、隐藏字段在编辑时回传、date-time 控件、默认模型映射带进表单，都只有清单测试和单元测试覆盖。
16. 保存后立即刷新（7.3 第 4 点）只在单元测试里对假令牌端点验证过，没有对 Google 的令牌端点跑过。
17. 核心两个扩展点对真实上游的表现：真实 Bedrock 流的分帧（消息大小、异常帧的形状）只按旧版的样例和 aws-sdk-go-v2 录下的数据测试过。

## 13. 发布与回滚

顺序：

1. openai 0.4.4、vertex 0.1.0：不依赖核心改动，可以先发。openai 是内置插件，随核心同步；vertex 只进市场，管理员自己安装。
2. 核心 0.1.N（实际是 0.1.304；S1、S2、C1、C2，保留键）：改的是核心里的网关流水线（`server/internal/gateway`），节点外壳 `next/gateway` 没有改动，不用发新网关；按核心发布流程。没有迁移。
3. bedrock 0.1.0：核心 0.1.N 上线之后发，`hostCompat`（`>=0.1.304`）挡住旧核心。azure_openai 0.1.0 不依赖核心改动（`>=0.1.290`），可以与第 1 步一起发；它的键在 0.1.304 才进保留键列表。都只进市场。

回滚：

- 插件：卸载或回到上一版本。openai 回到 0.4.3 后，账号设置里多出的 `organization`、`project` 两个键没人读，不影响请求；重新升级后继续生效。
- 核心：两个扩展点只在插件填了新字段时才执行，回滚核心前先停用 bedrock 插件即可。C2 回滚后 Vertex 退回“最多等 5 分钟”。
- 没有数据迁移，没有要清理的数据。

## 14. 需要用户决定的事

1. Bedrock 的 403 AccessDenied：按本文冷却不禁用，还是按初始决定禁用（第 10 节偏差 6）。
2. Bedrock 给 Claude Code 用的优先级：高的话，深度清洗要在首版解决（第 12 节第 7 条，候选做法见第 15 节）。
3. Azure 经典类型要不要：用户的原话是“有自定义端点就行”，只留 `azure_v1` 也满足；经典类型只是多一份清单，本文保留了。
4. Bedrock 的 openai 平台要不要进首版：它依赖的行为没验证的最多（第 12 节第 2 条），去掉后插件只剩 anthropic 一条路，更小。**已定：不进，已从插件里去掉**（16.7 第 11 条）。

## 15. 后续

- Vertex 上的 Claude（rawPredict / streamRawPredict），放进 vertex 插件，复用同一套令牌。
- Azure 的 Entra ID 令牌：可以照 Vertex 的做法用 §48。
- Bedrock：Converse 接口；Claude 以外的 Bedrock 模型（OpenAI 兼容接口 `/openai/v1`，或 Converse）；`bedrock_apikey` 走 `/anthropic/v1/messages`；自定义端点；临时凭证用 AssumeRole 自动续期（刷新请求本身要签名，核心的刷新发送处要接上 8.1）。
- 让代码类型也能在清单里声明固定的参数覆盖（现在只有声明式类型可以），Bedrock 的深度清洗就能写成清单的一部分。
- 三家的模型列表拉取。
- volcengine 的素材库签名并入 `sdk/reqsign`。

## 16. 实现与设计的差异

2026-11-17 按分支 `feat/cloud-providers` 的代码逐条核对。正文与这里不一致时以这里为准。

### 16.1 总体

1. **插件键**：Azure 插件的键是 `azure_openai`，目录是 `plugins/azure-openai`，`creationGroup` 仍是 `azure-openai`。
2. **新插件需要一行核心改动**：三个新插件的键加进保留键列表（`install/reserved.go`），见第 4 节表下的说明。设计写的“需要核心改动：无”不成立。
3. **版本**：Vertex 和 Azure 的 `hostCompat` 是 `>=0.1.290 <0.2.0`；Bedrock 是 `>=0.1.304 <0.2.0`（第 8 节的两个扩展点）。核心 0.1.303 已发布、不含本分支，第一个带本分支的核心是 0.1.304。openai 是 0.4.4，清单的 `hostCompat` 仍是 `>=0.1.290 <0.2.0`；0.4.3 已随核心 0.1.303 发布。
4. **发布说明里的“网关二进制有变更”**指核心里的网关流水线，节点外壳没有改动（第 13 节已改）。

### 16.2 核心扩展点（第 8 节）

1. **格式名**：响应流格式叫 `aws-eventstream-chunk`（常量 `awseventstream.FormatChunk`，适配器 `awseventstream.NewChunkSSEReader`），不是 `aws-eventstream`。名字指“分帧 + 帧里事件的形状”：它只读 InvokeModelWithResponseStream 的 chunk 事件（负载 `{"bytes": base64}`），Converse 流这类别的形状要另起名字、另写适配器；`Decoder` 只管分帧，可以共用。
2. **单条消息上限**：只有格式本身的上限 16 MiB（`MaxMessageSize`），超过算帧损坏。原先 chunk 适配器另限 4 MiB（`MaxChunkMessageSize`），转发 V2 收尾时按所有者“超时、限制都去掉”的决定删除（CONTRACTS §112.16）。
3. **解码器缓冲**：帧头里声明的长度在读取和分配之前先检查（包自己读 12 字节帧头，官方解码器不检查长度）；负载缓冲随实际收到的字节成倍增长，声明的长度本身不预留内存。一条消息用过超过 256 KiB 的缓冲，下一条消息前释放。
4. **异常帧的输出**固定是 Anthropic 流错误的形状（`event: error`，`{"type":"error","error":{"type","message"}}`），类型为空时写 `upstream_error`。
5. **签名会改写请求**：签名器把查询串改写成规范形式（先编码，再按键、按值排序，空格写成 `%20`），发出去的就是签的那一份；解析不了的查询串（`;` 分隔、坏的转义）直接报错，不会只签一部分。主机去掉协议的默认端口（https 的 `:443`、http 的 `:80`）后写回 `req.Host`，所以发出的 Host 头就是被签名的主机。设计只写了“主机取 `req.Host`”。
6. **路径由插件编码**：带签名时，插件给的 URL 路径必须已经是线上的样子（`:` 写成 `%3A`，段内的 `/` 写成 `%2F`）；签名器不替插件编码，只把每一段再转义一次。
7. **GET / HEAD** 不带请求体，按空负载签名。
8. **签名前删掉已有的** `Authorization` 和 `X-Amz-Security-Token`，不只是 `Authorization`。
9. **检查在发送前一次做完**：未知的签名方案、未知的格式名都在 `forwardBuilt` 写完请求头之后、发送之前检查（`checkUpstreamExt`），这次尝试按插件不可用换号，没有请求发出。设计把未知格式名的处理写在响应阶段。
10. **WebSocket**：握手的构造结果带了 `signing` 或 `response_stream` 就按插件不可用换号（`unsupportedOnWebSocket`）。设计没有写 WebSocket。
11. **`RequestSigning` 的字段不是厂商无关的**：只有方案名通用，其余字段是“访问密钥 HMAC”这一族方案的输入；别的族（RSA 密钥、证书）要加新字段，不重新解释现有字段。`reqsign.Directive` 的 `String`、`GoString`、`LogValue` 都不输出密钥和会话令牌。
12. **适配后的响应**去掉 `Content-Length`。只有 2xx 且内容类型正好是 `application/vnd.amazon.eventstream` 时才适配。

### 16.3 保存后刷新（7.3 第 4 点，任务包 C2）

1. **先判断到期再启动**：`refreshSaved` 先解出刚保存的凭证，只有类型声明了 `refresh`、并且凭证已经在刷新窗口内（`refreshDue`）时才起后台协程。设计写的是“声明了 `refresh` 就异步调一次”，由 `refreshAccount(id, false)` 自己跳过没到期的。结果相同，区别是没到期的保存什么都不启动。
2. **只在保存了凭证时触发**：新建总会触发判断；修改只有这次请求带了凭证才触发。
3. **有 panic 恢复**（`runRecovered`，记 ERROR 日志）；失败记 WARN，并像扫描一样记入刷新状态。
4. **关停时不等它**：协程脱离请求的取消（`context.WithoutCancel`），不挂在任何 WaitGroup 上。被关停打断的账号仍然到期，由下一轮扫描补上。
5. Vertex 两个表单（服务账号、用户凭证）的帮助文字写的是“保存后自动获取访问令牌（旧版核心最多等待 5 分钟），之后自动续期。”：5 分钟是没有这项改进的核心上的上限；有它的核心上通常几秒。

### 16.4 OpenAI（第 5 节）

与设计一致。`organization`、`project` 在表单的“高级（可选）”折叠分组，校验 `^[!-~]{0,128}$`。

### 16.5 Azure OpenAI（第 6 节）

1. **`passHeaders` 显式列出**，是 openai 平台的列表去掉 `openai-organization`、`openai-project`：客户端带的这两个头不转给 Azure。设计没有写。
2. **预设的请求体过滤生效**：清单没有写 `filterFields: []`，所以 openai 预设的 `service_tier`、`safety_identifier` 会从请求体里删掉。设计 6.2 写的 `filterFields: []` 没有采用。
3. **资源地址的校验**比设计的 `^https?://[^\s?#]+$` 严格，两个表单相同：`^https?://[^\s/?#@]+(/|/openai/?|/openai/v1/?|/api/projects/[^\s/?#]+/?)?$`。
   - 接受：只有协议和主机（可带端口）；末尾可带 `/`、`/openai`、`/openai/v1`（保存时去掉）；`/api/projects/<名>`（Foundry 项目地址，保留）。
   - 拒绝：带 `deployments/...`、`chat/completions`、`/models` 等路径；带查询串或 `#`；带 `用户@`；大小写不同的 `/OpenAI/V1`；协议大写；前后有空格。报错文字是“只填资源地址，不要带 deployments、chat/completions 或 api-version”。
   - **`http://` 仍然接受**，因为 docker e2e 的模拟上游是 http。生产上应当只填 https。
4. `azure_deployment` 的 `api_version` 存在设置里，经 `credentials.fields` 进路径模板；`azure_v1` 没有这个字段。
5. **分类策略是 `openai`，与设计一致**。实现中加过 `azure_openai` 策略（`openai.AzurePolicy`，404 `DeploymentNotFound` 换号、不动账号，`hostCompat >=0.1.304`），合并后撤掉：失败策略发布后请求是否重试、换号只由失败策略决定（`server/internal/gateway/dispatch.go` 的 `applyClassification`、`failure_policy_runtime.go` 的 `classifyFailure`），插件分类的 action 不参与，这条规则只在没有发布失败策略时才起作用。现在由管理员在失败处理设置里加重试规则（上游 HTTP 响应、404、关键词 `DeploymentNotFound` / `model_not_found`，不配账号规则，见 [PLUGIN-CLOUD-PROVIDERS.md](PLUGIN-CLOUD-PROVIDERS.md) §3.4）。`classify` 可取的名字仍是 `openai`、`anthropic`、`gemini`，插件的 `hostCompat` 是 `>=0.1.290 <0.2.0`。

### 16.6 Vertex AI（第 7 节）

1. **密钥 JSON 用单行的 `secret` 控件**：没有“secret + 多行”的控件（设计写的 secret + textarea 不存在）。整个 JSON 粘贴进单行密码框，长度上限 32768。
2. **`location` 是文本框**，不是可搜索的下拉；占位 `global`。`project_id` 也是文本框。
3. **用户凭证账号发 `x-goog-user-project`**，值是账号的项目 ID：用户凭证不属于任何项目，不带配额项目时 aiplatform 回 403。服务账号不发。
4. `vertex_authorized_user` 的 `project_id` 在表单 schema 里不是必填：留空时取凭证文件的 `quota_project_id`，两处都没有才报必填。
5. **JWT 的 `iat` 往前拨 10 秒**（`assertionBackdate`；时间来自插件的时钟，以整数秒写进声明），`exp` = `iat` + 3600。设计写的是 `iat` = 现在。
6. **令牌过期也回 `FAILED_PRECONDITION`**：设计只写了“还没有令牌”。现在存着的令牌 `expires_at` 已过也不发请求，换下一个账号，不惩罚这个账号。读不出过期时间的令牌不算过期。
7. **时钟偏差的 `invalid_grant` 算暂时性错误**：400 `invalid_grant` 且 `error_description` 含 `reasonable timeframe` 或 `short-lived token` 时回 `TRANSIENT`（提示检查节点时钟），不是 `AUTH_REJECTED`；否则一个时钟不准的节点会把所有服务账号标成“密钥被拒绝”。
8. 回答里没有 `expires_in` 时按 3600 秒算。响应被截断、没有 `access_token` 都是 `TRANSIENT`。
9. **保存时重置令牌的条件**多一条：存着的令牌读不出过期时间也会被丢弃并写 `expires_at: 1`。服务账号、用户凭证账号保存时 `location` 留空会存成 `global`。
10. **`externalServices`** 列了三项：`oauth2.googleapis.com`、`aiplatform.googleapis.com`、`googleapis.com`。设计没有列。（2026-11-18 转发 V2 收尾时删除：核心只在取回和 `SendAccountRequest` 时读 `externalServices`，这个插件两者都不用，令牌和上游请求都由核心发送。）
11. 错误分类：除 API Key 类型以外的类型（包括未知类型）都按令牌账号处理，401 冷却 60 秒、不禁用；其余沿用 `gemini.Policy`（403 禁用，400 且 key 或区域无效禁用，429 冷却，408 / 5xx 换号不动账号）。

### 16.7 AWS Bedrock（第 9 节）

1. **`hostCompat` 下限写的是 0.1.304**（`>=0.1.304 <0.2.0`），清单测试钉住了这个值。
2. **`region` 是文本框**（默认 `us-east-1`，留空时保存成 `us-east-1`），不是下拉。校验 `^[a-z]{2}(-[a-z]+)+-\d$`。
3. **`inference_profile_prefix` 的“不加”存成字符串 `none`**（枚举 `none / us / eu / apac / jp / au / in / global`），不是空字符串。**默认值是 `global`**（常量 `DefaultProfilePrefix`；表单 schema 的 `default`，留空时也保存成它，已存的账号缺这个字段时同样按它处理），设计里是“不加”。改的原因：按 AWS 模型卡片，默认映射里的模型在 bedrock-runtime 上几乎都没有区域内可用性（只有 Opus 5.5、Opus 4.6、Sonnet 4.6 在 `eu-west-2`，Opus 5 在 `ap-northeast-2`，Sonnet 5 在 `eu-west-2`、`ap-northeast-2`、`ap-southeast-1`；Haiku 4.5 连裸模型 ID 都没有），默认“不加”时新建的账号一个预填模型都调不通；而除 Opus 4.1 之外每个预填模型的卡片都列了 `global.` 配置，除 GovCloud 之外的区域都可用。地域前缀做不了默认：各模型有的地域不一样（Fable 5.2、5.1、5 只有 `us.`；Opus 4.5 只有 `us.`、`eu.`；没有模型有 `apac.`）。Opus 4.1 只有 `us.` 配置，默认映射里直接写带前缀的 ID（第 9 条）。此前保存的账号存的是 `none`，不会自动变。表单的帮助文字写明了这些，清单测试检查。`in` 是印度地域的推理配置前缀（例如 `in.anthropic.claude-sonnet-5`，见 Opus 5、Sonnet 5、Haiku 4.5 的模型卡片），设计里没有。已经以这些前缀之一或 `us-gov.` 开头的 ID、以及 ARN 原样发送；`us-gov` 不在可选项里，这类 ID 由管理员在映射里写全。
4. **`session_expires_at` 只检查格式**（RFC 3339 的日期时间，只在提交表单时检查），不参与任何逻辑；已存的账号不管它写了什么都照常服务。
5. `session_token` 是单行 `secret` 控件（同 16.6 第 1 条）。`access_key_id` 存在凭证里但不是敏感字段，界面上明文显示。
6. **beta 处理**（`beta.go`）比设计多：除了 `anthropic-beta` 头，请求体自带的 `anthropic_beta` 也算进来（清单把它声明成请求字段，数组或逗号分隔的字符串都认），先头后体；`advanced-tool-use-2025-11-20` 改名为 `tool-search-tool-2025-10-19`；带了 tool search 就自动补上 `tool-examples-2025-10-29`；不在名单里的丢弃，同一个只写一次。有剩下的就写进 `anthropic_beta`，一个都不剩而请求体原来有这个字段时删掉它。结果里没有 `context-management-2025-06-27` 时删掉顶层的 `context_management`。白名单（`supportedBetas`）共 15 项，不分模型，某个模型没有的特性由上游拒绝：
   - 旧版名单的 8 项：`computer-use-2025-01-24`、`computer-use-2025-11-24`、`context-1m-2025-08-07`、`context-management-2025-06-27`、`compact-2026-01-12`、`fine-grained-tool-streaming-2025-05-14`、`tool-search-tool-2025-10-19`、`tool-examples-2025-10-29`。
   - `compact-2026-09-04`：Opus 4.6、Sonnet 4.6、Opus 4.7、Opus 4.8、Fable 5、Sonnet 5、Opus 5、Fable 5.1 的模型卡片“Compaction”一节（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-8.html> 及同类页面）。
   - `fallback-credit-2026-07-01`：Fable 5、Opus 5、Fable 5.1 的模型卡片（`model-card-anthropic-claude-fable-5.html`、`-opus-5.html`、`-fable-5-1.html`）。
   - `thinking-display-updates-2026-08-18`、`mid-conversation-output-config-2026-07-01`、`mid-conversation-effort-2026-08-01`、`per-turn-control-2026-07-01`、`per-message-effort-2026-07-01`：<https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-adaptive-thinking.html>。
7. **错误分类多出的行和实际规则**（按顺序，先命中的生效；9.4 的表已经按这里改过）：
   - 403 且是边缘拦截页（HTML、`cf-mitigated: challenge` 等，`classify.EdgeBlock`）：换号，不动账号。设计没有这一行。
   - 429（任何错误类型）：冷却到 `retry-after-ms` / `retry-after`，没有就 60 秒。
   - **400 且是 `ServiceQuotaExceeded`**：换号，不动账号。设计没有这一行。
   - `RequestExpired`，或 `InvalidSignature` 且消息含 `signature expired` / `not yet current`：换号，不动账号。
   - `IncompleteSignature`：换号，不动账号。
   - `ExpiredToken`：禁用。
   - **401（任何错误类型）**，或 `UnrecognizedClient`、`InvalidClientTokenId`、`InvalidSignature`：禁用。设计没有 401 这一行。
   - **其余所有 403**（不只是 `AccessDeniedException`）：冷却 60 秒，不禁用。
   - 408、5xx（含 529）：换号，不动账号。
   - 没有规则命中的（400 `ValidationException`、404、424）原样回客户端。
   - 错误类型比较时不看 `Exception` 后缀，先取响应头 `x-amzn-errortype`（去掉 `:` 之后的部分和 `#` 之前的命名空间），没有再取响应体的 `__type`。
8. **默认测试模型**是 `global.anthropic.claude-haiku-4-5-20251001-v1:0`（常量 `DefaultTestModel`），即默认映射里 `claude-haiku-4-5` 的目标加上默认前缀，清单测试钉住这个关系。Haiku 4.5 的模型卡片写明 “requires a geo or global inference profile ID. The bare model ID isn't supported for on-demand throughput”，并在 “Global inference ID” 一栏列出这个 ID（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-4-5.html>）。它自己带前缀，`modelID` 对带前缀的 ID 原样发送，所以前缀是“不加”、`us`、`global` 的账号测的都是同一个 ID（`TestBuildTestRequest`）。代价：选了地域前缀的账号，默认测试请求走的是全球配置；GovCloud 的账号要在测试对话框里指定模型。最初是不带前缀的裸 ID，前缀“不加”的账号在 AWS 上测不通。
9. **默认模型映射的 ID 取自 AWS 模型卡片**（索引 <https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards-anthropic.html>，各模型页 `https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-<名字>.html`，名字为 `fable-5-2`、`fable-5-1`、`fable-5`、`opus-5-5`、`opus-5`、`opus-4-8`、`opus-4-7`、`opus-4-6`、`opus-4-5`、`opus-4-1`、`sonnet-5-5`、`sonnet-5`、`sonnet-4-6`、`sonnet-4-5`、`haiku-5-5`、`haiku-4-5`，2026-11-17 读取）。最初是把旧版的 `DefaultBedrockModelMapping` 去掉 `us.` 前缀抄过来的，后来按卡片改正。三个类型相同，19 个客户端模型名各有一条，取卡片 “Programmatic Access” 表里 bedrock-runtime 一行的 Model ID，不带区域前缀（前缀由账号设置加，默认 `global`）。唯一的例外是 Opus 4.1：它的卡片只列了 `us.` 配置，没有 `global.`，映射里直接写 `us.` 开头的 ID，不受账号前缀影响。到 Opus 4.6 为止的 ID 带版本后缀，之后的不带：

   | 客户端模型 | Bedrock 模型 ID |
   |---|---|
   | `claude-fable-5-2` | `anthropic.claude-fable-5-2` |
   | `claude-fable-5-1` | `anthropic.claude-fable-5-1` |
   | `claude-fable-5` | `anthropic.claude-fable-5` |
   | `claude-opus-5-5` | `anthropic.claude-opus-5-5` |
   | `claude-opus-5` | `anthropic.claude-opus-5` |
   | `claude-opus-4-8` | `anthropic.claude-opus-4-8` |
   | `claude-opus-4-7` | `anthropic.claude-opus-4-7` |
   | `claude-opus-4-6` | `anthropic.claude-opus-4-6-v1` |
   | `claude-opus-4-5`、`claude-opus-4-5-20251101` | `anthropic.claude-opus-4-5-20251101-v1:0` |
   | `claude-opus-4-1` | `us.anthropic.claude-opus-4-1-20250805-v1:0` |
   | `claude-sonnet-5-5` | `anthropic.claude-sonnet-5-5` |
   | `claude-sonnet-5` | `anthropic.claude-sonnet-5` |
   | `claude-sonnet-4-6` | `anthropic.claude-sonnet-4-6` |
   | `claude-sonnet-4-5`、`claude-sonnet-4-5-20250929` | `anthropic.claude-sonnet-4-5-20250929-v1:0` |
   | `claude-haiku-5-5` | `anthropic.claude-haiku-5-5` |
   | `claude-haiku-4-5`、`claude-haiku-4-5-20251001` | `anthropic.claude-haiku-4-5-20251001-v1:0` |

   只对照了文档，没有用真实账号调过（第 12 节第 9 条）；2026-11-17 的核对结果见第 16 条。
10. **`externalServices`**：`amazonaws.com`（核心匹配等于它或在它之下的主机；请求的主机是 `bedrock-runtime.<区域>.amazonaws.com`）。（2026-11-18 转发 V2 收尾时删除：核心只在取回和 `SendAccountRequest` 时读 `externalServices`，这个插件两者都不用，令牌和上游请求都由核心发送。）
11. **openai 平台从插件里去掉了**（第 14 节第 4 条的决定）。没有账号类型声明 openai 平台，Bedrock 的 OpenAI 兼容接口（`/openai/v1`）完全不用；`BuildUpstreamRequest` 只接受 `anthropic.messages`。客户端用 OpenAI 协议（chat completions、responses）访问时，核心把请求转换成 Messages 请求，走同一条 InvokeModel 路。原因有两条：这条路依赖的行为没验证的最多（`/openai/v1` 上的 SigV4、`stream_options`、支持哪些模型，第 12 节第 2 条）；AWS 的模型卡片显示 Claude 模型在 bedrock-runtime 上根本不支持 Responses 和 Chat Completions 接口，这条路对 Claude 没有用。后果：Bedrock 账号没有 OpenAI 兼容的路由，Claude 以外的 Bedrock 模型这一版不支持。`response_stream` 只在流式请求上设置。
12. 表单的“区域与模型”分组说明里写明了 Claude Code 的限制（第 12 节第 7 条），清单测试检查这段文字存在。
13. **请求体顶层字段的处理**（`upstream.go` 的 `invokePatches`、`droppedFields`）：删 `model`、`stream`，设 `anthropic_version: bedrock-2023-05-31`，删 `metadata`、`provider`，再加第 6 条的 beta 处理。**`output_config`、`output_format` 特意保留**（旧版会删）：AWS 文档写明 InvokeModel 读 `output_config.format`（结构化输出）和 `output_config.effort`（<https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-adaptive-thinking.html>），`output_format` 是它已废弃的写法，插件不替客户端转换。插件仍然不做的（它只看得到清单声明的请求字段）：清理请求体深处的字段（工具定义里的 `custom`，`cache_control` 的 `scope` / `ttl`）；请求体需要某个 beta 而没有点名时替它补上（computer use、tool search）。
14. **不支持的区域在保存时拒绝**（`credentials.go` 的 `unsupportedPartition`）：`cn-*` 和隔离分区（`us-iso-*`、`us-isob-*`、`us-isof-*`、`eu-isoe-*`；代码判断的是第二段以 `iso` 开头）报字段错误 `region:unsupported`，一条中英双语的消息，因为它们的接入地址不在 `amazonaws.com` 下。已经存着这种区域的账号不服务任何请求（`FAILED_PRECONDITION`，网关换下一个账号）。`us-gov-*` 接受。（`eusc-*` 本来就过不了区域的格式校验。）
15. **账号的解码、校验用新的 SDK 包 `accountform`**，请求体补丁用 `pluginsdk.SetPatch` / `DeletePatch`（16.9）。
16. **2026-11-17 按 AWS 模型卡片核对默认映射，代码已经跟着改**：
    - Claude Sonnet 4 的卡片写 “Model lifecycle: EOL: 2026-10-14”（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4.html>），`claude-sonnet-4-20250514` 从默认映射里删掉。
    - Claude Opus 4 不在索引页里，`model-card-anthropic-claude-opus-4.html` 已经跳转到用户指南首页，`claude-opus-4-20250514` 从默认映射里删掉。清单测试不让这两个名字回来。
    - 新增 `claude-fable-5-2`（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-2.html>）、`claude-opus-5-5`（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5-5.html>）、`claude-sonnet-5-5`（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-5-5.html>）、`claude-haiku-5-5`（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-5-5.html>），ID 是各卡片 bedrock-runtime 一行的 Model ID。
    - Opus 4.1（Legacy，2027-01-08 停止服务）、Sonnet 4.5（Legacy，2027-04-08 停止服务）仍在默认映射里。
    - Mythos 5.2、5.1、5 在索引页里，但要通过 Anthropic 的验证项目才能用，不预填；Claude 3 Haiku 也不预填。
    - 各预填模型的推理配置（卡片的 “Geo inference ID” / “Global inference ID” 两栏）：都有 `global.`，只有 Opus 4.1 没有（只有 `us.`）。地域配置：Fable 5.2、5.1、5 只有 `us`；Opus 5.5 有 `us`、`eu`、`au`、`jp`；Sonnet 5.5 有 `us`、`ca`、`eu`、`au`；Haiku 5.5 有 `us`、`ca`、`eu`、`au`、`jp`；Opus 5、Sonnet 5 有 `us`、`eu`、`au`、`in`；Opus 4.8、4.7 有 `us`、`eu`、`jp`、`au`；Opus 4.6 有 `us`、`eu`、`au`；Sonnet 4.6、Sonnet 4.5 有 `us`、`eu`、`au`、`jp`；Opus 4.5 有 `us`、`eu`；Haiku 4.5 有 `us`、`eu`、`au`、`jp`、`in`。`ca.` 不在前缀的可选项里，也不算“已经带前缀的 ID”：要用它，前缀选“不加”并在映射里写全。`global.` 在 GovCloud 的两个区域不可用。

### 16.8 模拟上游与测试

1. 模拟上游（`deploy/mock-upstream/cloud.go`）新增路径：Azure 的 `/openai/v1/{chat/completions,responses,embeddings}`（v1 接口）和 `/openai/deployments/{部署}/{chat/completions,embeddings}`（经典接口）；Bedrock 的 `/model/{id}/invoke`、`/model/{id}/invoke-with-response-stream`；Vertex 的 `/v1/projects/{项目}/locations/{区域}/publishers/google/models/{模型}:{动作}`、`/v1/publishers/google/models/{模型}:{动作}`。`/openai/v1/*` 只是 Azure 的路径，模拟上游从来没有 Bedrock 的 `/openai/v1` 路径，去掉 Bedrock 的 openai 平台时这里没有删东西。Bedrock 路径上带 SigV4 的请求要用密钥 `mock-secret-<access key id>` 签名，模拟上游自己重算。上游 Key 的读取多了 `api-key` 头和 SigV4 `Authorization` 里的 access key id。
2. `e2e/ac29_azure_test.go` 已在 e2e 栈上运行并通过（第 12 节第 14 条）。Vertex、Bedrock 没有 docker e2e，与设计一致。

### 16.9 SDK 新增的辅助（设计里没有）

1. **包 `next/sdk/pluginsdk/accountform`**：自带账号表单的插件（字段不是 `api_key` + `base_url` 的类型：云凭证、密钥文件）解码、校验、归一化的公共部分，vertex 和 bedrock 都用它。`api_key` + `base_url` 的表单仍由 `apikey.Spec` 整个实现。导出的内容：
   - `Object`（`map[string]any`）：解码后的 JSON 对象，数字保持原样（`json.Number`），重新编码不会改动它们。方法 `Str(k)`（去掉首尾空白的字符串值，没有或不是字符串时为空）、`JSON()`。
   - `Decode(raw)`：解码一个 JSON 对象；空白和 `null` 得到空对象，对象后面还有内容报错。
   - `FieldError(field, code, msg)`、`Invalid(e)`：一条字段错误，以及用它拒绝表单的 `ValidateCredentials` 回答。
   - `Form{Creds, Settings}`、`DecodeForm(in)`：解出 `ValidateCredentials` 请求里的凭证和设置；有一个不是 JSON 对象时直接返回拒绝的回答。校验过程可以改写两个对象；`(*Form).Normalized()` 给出接受的回答，只带被改过的那个对象的 JSON，没改的留空（核心保留提交的内容）。
   - `DecodeAccount(acc)`：解出已存账号的凭证和设置，不是 JSON 对象时回 `INVALID_ARGUMENT`。
2. **`pluginsdk.SetPatch(path, value)`、`pluginsdk.DeletePatch(path)`**（`sdk/pluginsdk/request.go`）：构造 `BuildUpstreamRequest` 结果里的请求体补丁（`pluginv1.BodyPatch`）。`SetPatch` 把值编码成 JSON，编码不了时写 `null`；`DeletePatch` 删除路径，路径不存在时什么都不变。bedrock 的补丁都用它们构造。

### 16.10 签名、分帧改用 AWS 官方库，令牌断言用 golang-jwt（2026-11-17，分支 `feat/official-libs`）

所有者决定：签名、流分帧、令牌断言不用自己的实现。AWS 的签名和分帧用 AWS 的官方库；Vertex 的令牌断言用 `github.com/golang-jwt/jwt/v5` 签名，凭证文件由插件自己解析（Google 的库没有留下，原因见第 3 点）。网关仍然自己构造并发送 HTTP 请求：请求体原样透传、请求经账号的代理发出、响应流由核心中继，所以**不用 SDK 的服务客户端**，只换下面三处。对外接口都没有变。

1. **`sdk/reqsign`（SigV4）**：规范请求、待签字符串、密钥派生、`Authorization` 头由 `github.com/aws/aws-sdk-go-v2/aws/signer/v4`（`Signer.SignHTTP`）生成，手写的规范化代码已删除。包里留下的：
   - **只签四个头**。官方签名器会签请求上的所有头（少数几个除外）和 `content-length`；网关的请求还带着客户端透传的头、插件加的头，签名之后的各层（请求头覆盖、代理、net/http）还会改它们，签进去的头一变就是 403。所以交给签名器的是一份只带 `Content-Type` 的请求副本，签完把 `Authorization`、`X-Amz-Date`、`X-Amz-Security-Token` 抄回原请求。签名的头集合因此与原来相同，两个来源的黄金样例逐字节不变。
   - **查询串先解析**：解析不了就报错。官方签名器会把解析不了的参数悄悄丢掉再签、再发。
   - **每次签名新建一个签名器**：共用的签名器按“区域/服务”缓存派生密钥，只比对 Access Key ID；同一个 Access Key ID 改正了 Secret 之后，它当天仍用旧密钥签名。
   - 参数检查、出错时不动原请求、错误信息不带密钥。
   - 行为变化两处：查询串的排序改为**编码之后**再排（AWS 文档的要求，官方库 1.48 的做法；原来按解码后的值排，只有值里带需要编码的字符时顺序才不同）；IPv6 字面量主机去掉默认端口时官方库连方括号一起去掉（`[::1]:443` → `::1`，AWS 的端点都是域名，不受影响）。
2. **`sdk/awseventstream`（分帧）**：一条消息的校验和、头块与各种值类型的解码、编码由 `github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream`（`Decoder.Decode`、`Encoder.Encode`）完成。包里留下的：
   - **帧头自己读**（12 字节：两个长度和帧头校验和）并在读取、分配之前检查长度（总长 16..16 MiB、头块不超过 128 KiB 也不超过消息、解码器自己的上限）。官方解码器只验帧头校验和，不限制长度，头块长度大于消息时还会算出一个回绕的负载长度。检查过的帧头再连同后面的流交给官方解码器。
   - **错误分类**：流在消息中间结束是 `io.ErrUnexpectedEOF`，流自己的错误原样返回，其余（校验和不对、头块格式错、未知的值类型）都是 `ErrCorrupt`。
   - 只保留字符串头；编码前检查头名、头值、头块、消息的长度；负载缓冲超过 256 KiB 不留给下一条消息。
   - **同名的头算损坏**（`ErrCorrupt`，不论值的类型）。官方解码器对同名的头只留最后一个值，于是 `:message-type` 先是字符串 `event`、后面再来一个同名的非字符串头，这个头就没了、消息被跳过；先 `event` 后 `exception` 则被当成异常结束整条流。官方解码器解出来的结果里已经看不到重复，所以包把解出的头用官方编码器（`eventstream.EncodeHeaders`）重新写一遍只数字节：比帧头声明的头块短，就是有同名的头被合并了。顺带的效果：头值超过格式上限（32767 字节）的消息也算损坏，官方解码器读得进、官方编码器不肯写。
   - 行为变化一处：没有名字的头不再算损坏（官方解码器接受）。AWS 不会发这种头，也不会发同名的头。
3. **`plugins/vertex`（服务账号 JWT、凭证文件）**：JWT 由 `github.com/golang-jwt/jwt/v5` 编码并用 RS256 签名（`jwt.NewWithClaims` + `SignedString`），PEM 私钥由同一个库解析（`jwt.ParseRSAPrivateKeyFromPEM`，PKCS #8 和 PKCS #1 都读）。凭证文件由插件自己用 `encoding/json` 解析。插件仍然不联网：令牌请求照旧由 `BuildRefreshRequest` 构造、核心经账号代理发出。
   - **为什么不是 Google 的库**：这个分支先试过 `golang.org/x/oauth2`（提交 `07e58221d`），后来撤掉了。它公开、受支持的入口（`google.CredentialsFromJSONWithType`、`google.JWTConfigFromJSON` 得到的 TokenSource）都是签完断言就自己把令牌请求发出去，没有只签断言、不发请求的公开接口；能单独签名的只有 `golang.org/x/oauth2/jws`，它的包注释写着 Deprecated（仅供 x/oauth2 内部使用，将来可能移除）。而令牌请求必须由核心经账号的代理发出。剩下它能做的只是解析文件：对用户凭证只贡献了一次类型字符串的比较（同时建出一个装着 `client_secret`、`refresh_token` 却从不使用的 TokenSource），对服务账号只多给一个 `ProjectID`。所以整个依赖去掉。
   - **文件只解析一次**：一次 `json.Unmarshal` 进一个小结构体（服务账号：`type`、`project_id`、`private_key_id`、`private_key`、`client_email`、`token_uri`；用户凭证：`type`、`client_id`、`client_secret`、`refresh_token`、`quota_project_id`），检查和后面的使用读的是同一份值。`encoding/json` 匹配键名不区分大小写，重复的键取最后一个；`token_uri` 按这个**生效值**判断，必须正好等于 `https://oauth2.googleapis.com/token`，缺失、`null`、别的地址都拒绝。（用 Google 的库时这一项是另用 gjson 读的，gjson 取第一个键，于是 `{"token_uri":"<正确>",…,"token_uri":"https://evil.example/token"}` 能通过检查；现在是回归测试。）这个值只检查、不使用：请求永远发往常量地址，断言的 `aud` 也是常量。
   - **断言的内容不变**：`iss` = `client_email`，`scope` = cloud-platform，`aud` = 令牌地址常量，`iat` = 插件时钟的现在 − 10 秒，`exp` = `iat` + 3600，都是整数秒；头 `alg` = RS256、`typ` = JWT，文件有 `private_key_id` 时带 `kid`。golang-jwt 签名时不校验任何声明，数值由插件给定；测试用 `crypto/rsa` 独立验签并核对每个声明。
   - **错误信息各报各的字段**：不是 JSON 对象、`"type"` 不对（`external_account`、`impersonated_service_account`、`gdch_service_account` 等都落在这里）、某个字段缺失、某个字段不是字符串、`"token_uri"` 不对、`"private_key"` 不是 PEM 格式的 RSA 私钥。都不引用文件内容，也不带库的错误文本。
4. **依赖**：`next/sdk` 加 `github.com/aws/aws-sdk-go-v2` v1.48.0、`github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream` v1.7.27、`github.com/aws/smithy-go` v1.28.9（Apache-2.0）；`next/plugins/vertex` 加 `github.com/golang-jwt/jwt/v5` v5.3.1（MIT，没有间接依赖；`next/server` 的依赖图里已有同一版本）。`next/server`、`next/plugins/bedrock` 的 `go.mod` 多三行间接依赖。
5. **没有动的**：模拟上游（`deploy/mock-upstream`）的 SigV4 校验和 event stream 编码仍是独立的手写实现，这样测试里签名方和验签方不是同一份代码。volcengine 的素材库签名没有官方 Go 签名库可单独引用，不在这次范围内。
