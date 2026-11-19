# 云厂商账号怎么填：Azure OpenAI、Vertex AI、AWS Bedrock，以及 OpenAI 的组织 / 项目

> 状态：2026-11-17 按分支 `feat/cloud-providers` 的代码写成，2026-11-18 按转发 V2（CONTRACTS §112）改写，**未发布**。三家都没有用真实云账号验证过，只对官方文档、黄金样例和模拟上游测试过；没验证的事项见 [DESIGN-2026-11-17-CLOUD-PROVIDERS.md](DESIGN-2026-11-17-CLOUD-PROVIDERS.md) 第 12 节。
> 给管理员看的填表说明。设计、与设计的差异见上面的设计文档（第 16 节），契约见 CONTRACTS §109。

## 1. 总览

| 上游 | 插件（键） | 版本 | 最低核心版本 | 服务的平台 | 来源 |
|---|---|---|---|---|---|
| OpenAI 组织 / 项目 | `openai` | 0.5.0 | 0.1.304 | openai | 内置 |
| Azure OpenAI | `azure_openai` | 0.1.0 | 0.1.304 | openai | 市场，自己安装 |
| Vertex AI（Gemini） | `vertex` | 0.1.0 | 0.1.304 | gemini | 市场，自己安装 |
| AWS Bedrock | `bedrock` | 0.1.0 | 0.1.304 | anthropic | 市场，自己安装 |

四个插件都是转发 V2 的代码插件（`hostCompat` `>=0.1.304 <0.2.0`），要和核心、其余第一方插件一起升级；按旧插件协议构建的包装不上新核心（CONTRACTS §112.10）。

- 三个新插件装好并启用后，在“新建账号”里各显示为一项，进去后用“鉴权方式”切换具体的账号类型。
- 客户端用别的平台的协议访问时由核心转换（CONVERT-MATRIX），但带账号协议绑定（CONTRACTS §110）的核心上，**跨平台转换默认关闭**：要在账号编辑页“协议与模型 › 模型的协议限定”里为模型勾上对应的协议，否则这类请求返回 404。同一平台内的转换（如 Azure 经典类型把 `/v1/responses` 转成 chat）不用开。
- 价格不随插件带来，按客户端请求的模型名在核心的模型价格里配置。
- “模型映射”在三家都很重要：账号的模型映射把**客户端请求的模型名**（键）换成**发给上游的名字**（值），第 3–5 节分别说明值该填什么。

## 2. OpenAI：组织 ID 与项目 ID

账号类型不变（OpenAI · API Key），表单最下面多了折叠的“高级（可选）”分组：

| 字段 | 含义 | 默认 | 例子 |
|---|---|---|---|
| 组织 ID（`organization`） | 以 `OpenAI-Organization` 请求头发送 | 空 | `org-AbCd1234` |
| 项目 ID（`project`） | 以 `OpenAI-Project` 请求头发送 | 空 | `proj_AbCd1234` |

- 填了：每个请求都带账号上的值，客户端自己带的同名头被覆盖。
- 留空：和以前一样，客户端带了就原样转给上游，没带就不发。
- 两个字段是明文设置，不加密，最长 128 个可见字符，不能有空格。
- 填错时上游回 401，账号按现有规则被禁用。
- WebSocket 模式（`/v1/responses`）的握手同样带上。

## 3. Azure OpenAI

需要核心 0.1.304 或更高（插件的 `hostCompat` 是 `>=0.1.304 <0.2.0`）。

### 3.1 选哪种类型

| 鉴权方式 | 账号类型 | 请求发到哪里 | 支持的接口 | 什么时候选 |
|---|---|---|---|---|
| v1 接口（默认） | `azure_v1` | `<资源地址>/openai/v1/chat/completions`、`/responses`、`/embeddings` | Chat Completions、Responses、Embeddings | 资源支持 v1 接口时优先选它 |
| 按部署（经典） | `azure_deployment` | `<资源地址>/openai/deployments/<部署名>/chat/completions?api-version=…`、`/embeddings?api-version=…` | Chat Completions、Embeddings | 只能用带 `api-version` 的旧接口时 |

经典类型**没有 Responses 接口**：客户端发来的 `/v1/responses` 请求由核心转换成 Chat Completions 再发，Responses 独有的能力（内置工具、服务端会话状态等）用不了。两种类型都不支持 Responses 的 WebSocket 模式。

### 3.2 表单字段

| 字段 | 含义 | 默认 | 例子 |
|---|---|---|---|
| 资源地址（`base_url`） | Azure OpenAI 资源的端点，必填 | 无 | `https://my-resource.openai.azure.com` |
| API Key（`api_key`） | 资源的 Key，以 `api-key` 请求头发送；加密保存。不限格式和长度，只要求非空（去掉首尾空白后保存） | 无 | Azure 门户“密钥和终结点”里的 KEY 1 |
| API 版本（`api_version`，仅经典类型） | 作为查询参数 `api-version` 发送 | `2024-10-21` | `2024-10-21`、`2025-04-01-preview` |

**资源地址贴什么**：Azure 门户里资源的“终结点”，只要协议和主机这一段。

- 可以填：`https://<资源名>.openai.azure.com`、`https://<资源名>.cognitiveservices.azure.com`；末尾带 `/`、`/openai`、`/openai/v1` 也行：地址按填写的保存（只去掉末尾的 `/`），末尾的 `/openai/v1` 或 `/openai` 在请求时按接口重新拼接，请求地址与不带时相同。Foundry 项目地址 `https://<资源名>.services.ai.azure.com/api/projects/<项目名>` 也接受（原样保留，项目名里的 `%2F` 也原样保留和发出；这个域名下的路径没有验证过）。项目名恰好是 `openai` 时，末尾的 `/openai` 会被当作后缀，请求里少了项目这一段，这种项目暂不支持。
- 会被拒绝（提示“只填资源地址，不要带 deployments、chat/completions 或 api-version”）：整条请求地址，例如 `…/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21`、`…/openai/v1/chat/completions`；带 `?` 查询串或 `#`；地址里带 `用户@`；`/OpenAI/V1` 这样大小写不同的后缀；前后有空格。
- `http://` 地址目前也能保存（测试环境的模拟上游需要）。正式账号请填 `https://`。

### 3.3 模型映射：客户端模型 → 部署名

Azure 上的“模型”是你自己起名的**部署**。

- 账号的模型列表里写客户端会请求的模型名（例如 `gpt-4o`）；插件不预填任何模型。
- 模型映射里把它对到部署名：`gpt-4o` → `my-gpt4o-prod`。部署名和模型名相同时不用映射。
- v1 类型：映射后的名字写进请求体的 `model`。经典类型：映射后的名字写进路径 `/openai/deployments/<部署名>/…`（请求体里的 `model` 保留，Azure 经典接口不看它）。
- 账号测试的默认模型是 `gpt-4o-mini`；没有同名部署时，在测试对话框里指定一个映射过的模型。
- 不支持“从上游获取模型列表”。

### 3.4 行为与限制

- 客户端带的 `OpenAI-Organization`、`OpenAI-Project` 头不转给 Azure；请求体顶层的 `service_tier`、`safety_identifier` 会被删掉。
- 流式 Chat Completions 会自动加 `stream_options.include_usage = true` 以便计量。
- 错误处理与 OpenAI 账号相同：401 / 403 禁用账号；429 冷却（按 `retry-after-ms` / `retry-after`，没有就 60 秒）；408、5xx 换号并冷却 10 秒；400（包括内容过滤 `content_filter`）原样回给客户端。
- **部署不存在（404，错误码 `DeploymentNotFound`，v1 接口也可能是 `model_not_found`）默认原样回给客户端**，插件不为它换号。别的账号的资源可能有这个部署，要让请求换到下一个账号，在系统设置的「调度策略」失败处理里加一条**自动重试**规则：
  - 来源：上游 HTTP 响应（新规则的默认值）；错误码：`404`；关键词：`DeploymentNotFound`、`model_not_found`，一行一个。错误码和关键词都填时要两者同时命中，所以其他 404 不受影响。
  - 动作是“按统一计划重试 / 换号”：打开“自动重试”和“切换账号”，重试次数大于 0；这条规则可以关掉“原账号重试”，在缺部署的账号上重试没有用。
  - **不要给它加自动禁用规则**：没有账号规则命中，账号既不冷却也不禁用。
  - 失败策略发布之后，请求是返回、重试还是换号只由失败策略决定，插件的错误分类对此不起作用（没有命中重试规则的错误一律返回给客户端）；没有发布失败策略时按上一条的插件分类执行，404 回给客户端。
  - 各账号映射到的部署最好都存在，否则每个请求都要在缺部署的账号上白试一次。
- **不支持 Entra ID（Azure AD）令牌**，只支持 API Key。
- 这类账号的地址由用户自己填，只有“自己的账号”权限的用户新建它需要 `account:relay` 权限。

## 4. Vertex AI（Gemini）

服务 gemini 平台的 `generateContent`、`streamGenerateContent`、`countTokens`。**只支持 Gemini，不支持 Vertex 上的 Claude**（Anthropic 模型的 rawPredict 接口没有接）。

### 4.1 选哪种类型

| 鉴权方式 | 账号类型 | 凭证 | 什么时候选 |
|---|---|---|---|
| 服务账号（默认） | `vertex_sa` | 服务账号的 JSON 密钥文件 | 正式使用，推荐 |
| 用户凭证 | `vertex_authorized_user` | `gcloud auth application-default login` 生成的文件 | 手头只有个人账号的凭证时 |
| API Key | `vertex_apikey` | Vertex AI 的 API Key | 用 express 模式，或绑定了服务账号的 Key |

前两种的访问令牌（1 小时有效）由核心向 `https://oauth2.googleapis.com/token` 换取并自动续期，经账号的代理发出；插件自身不联网。

### 4.2 表单字段

表单是插件自己的原生界面（`plugins/vertex/ui/native`：服务账号与用户凭证共用 `VertexKeyFileForm`，API Key 用 `createApiKeyForm` 生成的 `VertexApiKeyForm`）：填写时即时检查下表的格式，保存时以插件的 `ValidateCredentials` 为准（核心不再按 JSON Schema 检查）。核心续期时写回的 `access_token`、`expires_at`、`token_for` 不在表单上显示，保存时原样带回。表单没有的键保存时被拒绝（错误码 `unknown`，同原 schema 的 `additionalProperties:false`）：密钥文件类型允许密钥文件、`project_id`、`location` 和上面三个续期写回的键，`vertex_apikey` 允许 `api_key`、`project_id`、`location`。

服务账号、用户凭证：

| 字段 | 含义 | 默认 | 例子 |
|---|---|---|---|
| 服务账号密钥（`service_account_json`）/ 用户凭证文件（`authorized_user_json`） | 整个 JSON 文件的内容，必填；加密保存 | 无 | 见下 |
| 项目 ID（`project_id`） | 请求发往、计费的 Google Cloud 项目 | 服务账号：留空取密钥文件里的 `project_id`。用户凭证：留空取文件里的 `quota_project_id`，文件里也没有就必须填 | `my-project-123456` |
| 区域（`location`） | 请求发往的 Vertex AI 区域 | `global` | `global`、`us-central1`、`europe-west4`、`asia-northeast1` |

API Key：

| 字段 | 含义 | 默认 | 例子 |
|---|---|---|---|
| API Key（`api_key`） | 以 `x-goog-api-key` 发送；加密保存。不能为空，不限格式与长度，保存时去掉首尾空白 | 无 | Google Cloud 控制台 → Vertex AI → API 密钥 |
| 项目 ID（可选） | 见下 | 空 | `my-project-123456` |
| 区域（可选） | 见下 | 空 | `us-central1` |

API Key 类型的项目 ID 和区域**要么都填，要么都不填**：都填时用带项目的完整地址，都不填时用 express 地址 `https://aiplatform.googleapis.com/v1/publishers/google/models/…`。只填一个保存时报错。

**JSON 密钥贴什么**：

- 服务账号：Google Cloud 控制台 → IAM 和管理 → 服务账号 → 选中服务账号 → 密钥 → 添加密钥 → JSON，下载得到的文件。用文本编辑器打开，**全选、整个粘贴**（从 `{` 到 `}`）。服务账号要有调用 Vertex AI 的权限，例如 Vertex AI User 角色。文件必须满足：`"type": "service_account"`，有 `client_email` 和 PEM 格式的 RSA `private_key`，`token_uri` 是 `https://oauth2.googleapis.com/token`（别的地址会被拒绝）。
- 用户凭证：运行 `gcloud auth application-default login` 后生成的 `application_default_credentials.json`，同样整个粘贴。必须是 `"type": "authorized_user"`，有 `client_id`、`client_secret`、`refresh_token`。
- 输入框是单行的密码框（没有多行的密文控件），粘贴进去看不到内容和换行，这是正常的；长度上限 32768 个字符。保存后不再显示明文，编辑账号时不改它就留着不动。

**区域与主机**：`global` 发往 `aiplatform.googleapis.com`，其他区域发往 `<区域>-aiplatform.googleapis.com`。不是每个模型在每个区域都有。

### 4.3 保存之后

- 服务账号、用户凭证账号刚保存时还没有访问令牌。核心在保存后马上换一次（通常几秒）；没有这项改进的旧核心上要等下一轮扫描，最多 5 分钟。也可以在账号详情点“立即刷新令牌”。
- 没有可用令牌期间（还没换到，或令牌已过期），发到这个账号的请求会换到下一个账号，这个账号不受惩罚。
- 账号详情的“令牌”一行显示过期时间和上次刷新结果。显示“密钥被拒绝”（`auth_rejected`）说明密钥被删除、停用，或 refresh token 已作废，要换凭证。提示“检查节点时钟”说明是服务器时间不准，不是密钥的问题。
- 换了密钥文件后旧令牌自动作废，重新换取。

### 4.4 模型映射

Vertex 上的 Gemini 模型名和 Gemini Developer API 相同（例如 `gemini-2.5-flash`），一般不用映射。新建账号时预填的模型列表与 gemini 插件的相同（没有逐个核对在 Vertex 上是否可用），按实际开通的模型删改。客户端带的 `models/` 前缀会自动去掉。账号测试的默认模型是 `gemini-2.5-flash`。不支持“从上游获取模型列表”。

### 4.5 行为与限制

- 请求体与 Gemini Developer API 相同，原样转发，不做改写。Vertex 对请求体更严格（例如 `contents[].role`），在 Developer API 上能用的请求可能被拒绝。
- 用户凭证账号的请求会带 `x-goog-user-project: <项目 ID>`（用户凭证不属于任何项目，不带会被拒绝）。
- 客户端的 `user-agent`、`x-goog-api-client`（gemini 平台交给插件的两个客户端请求头）原样转发，是默认头，运营方的请求头覆盖可以改它们。
- 错误处理：
  - 服务账号、用户凭证：401 冷却 60 秒、不禁用（多半是令牌问题，等下一次刷新）；403 禁用。
  - API Key：401、403 都禁用。
  - 都一样：429 冷却；408、5xx 换号、不动账号；400 里“Key 无效”或“区域不支持”禁用，其余 400 和 404 原样回给客户端。
- **不支持 Vertex 上的 Claude**，也不支持 Vertex 的 OpenAI 兼容接口。

## 5. AWS Bedrock

需要核心 0.1.304 或更高（插件的 `hostCompat` 是 `>=0.1.304 <0.2.0`）。SigV4 签名由插件自己做，二进制响应流由核心解码成 SSE（CONTRACTS §109）。

只服务 anthropic 平台，只有一条路：

| 平台 | 客户端接口 | Bedrock 上的接口 |
|---|---|---|
| anthropic | `/v1/messages` | InvokeModel（`/model/<模型 ID>/invoke`），流式是 InvokeModelWithResponseStream |

- **没有 OpenAI 兼容的路由**：Bedrock 自己的 OpenAI 兼容接口（`/openai/v1`）没有接。客户端用 OpenAI 协议（`/v1/chat/completions`、`/v1/responses`）访问时，核心先把请求转换成 Messages 请求，再走上面这条 InvokeModel 路。这是跨平台转换：带账号协议绑定（CONTRACTS §110）的核心上默认关闭，要在 Bedrock 账号上为模型勾上 Chat Completions / Responses 才提供。
- **这一版只支持 Claude**，Bedrock 上 Claude 以外的模型不支持。

### 5.1 选哪种类型

| 鉴权方式 | 账号类型 | 凭证 | 会过期吗 |
|---|---|---|---|
| API Key（默认） | `bedrock_apikey` | Bedrock API Key，以 `Authorization: Bearer` 发送 | 短期 Key 最长 12 小时，且只能用在创建它的区域；长期 Key 按创建时在 AWS 上设定的有效期 |
| Access Key | `bedrock_aksk` | IAM 用户的长期 Access Key ID + Secret Access Key，插件做 SigV4 签名 | 不过期 |
| 临时凭证 | `bedrock_sts` | STS 签发的 Access Key ID + Secret Access Key + Session Token，插件做 SigV4 签名 | 会，按签发时的有效期 |

**三种都不会自动续期**。短期 Key 或临时凭证到期后上游拒绝请求，账号被禁用或冷却，要在账号上换新凭证并重新启用。

### 5.2 表单字段

表单是插件自己的原生界面（`plugins/bedrock/ui/native`，三种类型共用 `BedrockAccountForm`）：填写时即时检查下表的格式，保存时以插件的 `ValidateCredentials` 为准（核心不再按 JSON Schema 检查）。表单没有的键保存时被拒绝（错误码 `unknown`，同原 schema 的 `additionalProperties:false`）：三种类型都允许 `region`、`inference_profile_prefix`，另外 `bedrock_apikey` 允许 `api_key`，`bedrock_aksk` 允许 `access_key_id`、`secret_access_key`，`bedrock_sts` 再加 `session_token`、`session_expires_at`。

各类型自己的凭证字段：

| 类型 | 字段 | 含义 | 例子 |
|---|---|---|---|
| API Key | API Key（`api_key`） | Bedrock 控制台 → API keys 里生成的 Key；加密保存。不能为空，不限格式与长度，保存时去掉首尾空白 | — |
| Access Key、临时凭证 | Access Key ID（`access_key_id`） | 16–128 位大写字母和数字；明文显示 | 长期 `AKIA…`，临时 `ASIA…` |
| Access Key、临时凭证 | Secret Access Key（`secret_access_key`） | 加密保存，只用来签名，不发给上游 | — |
| 临时凭证 | Session Token（`session_token`） | 与临时凭证一起签发的令牌；加密保存；单行密码框，整段粘贴，不能有空格和换行 | — |
| 临时凭证 | 凭证到期时间（`session_expires_at`，可选） | **只是给管理员的备忘**，只检查是不是合法的日期时间，不参与任何逻辑：到期不会提醒，也不会提前停用账号 | `2026-11-18T12:00:00Z` |

三种类型共有（“区域与模型”分组）：

| 字段 | 含义 | 默认 | 例子 |
|---|---|---|---|
| 区域（`region`） | Bedrock 运行时所在的 AWS 区域，请求发往 `bedrock-runtime.<区域>.amazonaws.com`。文本框，手动输入 | `us-east-1`（留空也保存成它） | `us-east-1`、`us-west-2`、`eu-central-1`、`ap-northeast-1` |
| 跨区域推理前缀（`inference_profile_prefix`） | 加在 Claude 请求的模型 ID 前面，用来调用跨区域推理配置（inference profile） | `global`（留空也保存成它） | 不加 / `us` / `eu` / `apac` / `jp` / `au` / `in` / `global` |

**不支持的区域**：中国区（`cn-*`）和隔离分区（`us-iso-*`、`us-isob-*`、`us-isof-*`、`eu-isoe-*`）保存时会被拒绝（提示“不支持 AWS 中国区（cn-*）和隔离分区（us-iso-*、us-isob-* 等）：其接入地址不在 amazonaws.com 下”）。GovCloud（`us-gov-*`）可以填。

**前缀怎么选**：模型只能通过推理配置调用时就要选前缀。按 AWS 的模型卡片，现行的 Claude 模型在 bedrock-runtime 上几乎都没有区域内（In-Region）可用性，只能经地域或全球推理配置调用；Haiku 4.5 的卡片写明 “requires a geo or global inference profile ID. The bare model ID isn't supported for on-demand throughput”（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-4-5.html>）。所以默认是 `global`（全球推理配置）：预填的模型在各自的卡片上都有 `global.` 配置，除 GovCloud（`us-gov-*`）之外的区域都能调用，新建的账号不用改前缀就能用预填的模型。唯一的例外是 Opus 4.1，它只有 `us.` 配置（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-1.html>），预填的映射里直接写了 `us.anthropic.claude-opus-4-1-20250805-v1:0`，只有美国地域的区域能调。

- 要把请求留在某个地域内（数据驻留），改选地域前缀（`us`、`eu`、`apac`、`jp`、`au`、`in`）。不是每个模型都有每个地域的配置：例如 Fable 5.2、Fable 5.1、Fable 5 只有 `us.` 和 `global.`，Opus 4.5 只有 `us.`、`eu.` 和 `global.`，现行模型都没有 `apac.`（以各模型卡片的 “Geo inference ID” 一栏为准）。`in` 是印度地域的前缀（例如 `in.anthropic.claude-sonnet-5`）。
- “不加”按原样发送模型 ID，只适合映射里已经写了带前缀的完整 ID 或 ARN 的账号（GovCloud 的账号属于这种：`global.` 在那里不可用，要选“不加”并在映射里写 `us-gov.` 开头的 ID），或者模型在所选区域有区域内可用性（按卡片只有少数几个：Opus 5.5、Opus 4.6、Sonnet 4.6 在 `eu-west-2`，Opus 5 在 `ap-northeast-2`，Sonnet 5 在 `eu-west-2`、`ap-northeast-2`、`ap-southeast-1`）。
- 这次改动之前保存的账号，前缀存的是 `none`（“不加”），不会自动变成 `global`，要在账号上手动改。

IAM 权限：Access Key、临时凭证对应的身份要能调用 `bedrock:InvokeModel`、`bedrock:InvokeModelWithResponseStream`，并且在 AWS 账号里开通了要用的模型。

### 5.3 模型映射：客户端模型 → Bedrock 模型 ID / 推理配置

客户端用的是 Anthropic 的模型名（`claude-sonnet-4-5`），Bedrock 要的是它自己的 ID。分两步：

1. **模型映射**把客户端模型名换成 Bedrock 模型 ID。新建账号时已经预填了一份（19 个客户端模型名，16 个 Bedrock 模型），例如：

   | 客户端模型 | 映射到 |
   |---|---|
   | `claude-sonnet-4-5`、`claude-sonnet-4-5-20250929` | `anthropic.claude-sonnet-4-5-20250929-v1:0` |
   | `claude-haiku-4-5`、`claude-haiku-4-5-20251001` | `anthropic.claude-haiku-4-5-20251001-v1:0` |
   | `claude-opus-4-5` | `anthropic.claude-opus-4-5-20251101-v1:0` |
   | `claude-opus-4-6` | `anthropic.claude-opus-4-6-v1` |
   | `claude-opus-4-8` | `anthropic.claude-opus-4-8` |
   | `claude-sonnet-5` | `anthropic.claude-sonnet-5` |
   | `claude-fable-5-2` | `anthropic.claude-fable-5-2` |
   | `claude-opus-5-5`、`claude-sonnet-5-5`、`claude-haiku-5-5` | `anthropic.claude-opus-5-5`、`anthropic.claude-sonnet-5-5`、`anthropic.claude-haiku-5-5` |
   | `claude-opus-4-1` | `us.anthropic.claude-opus-4-1-20250805-v1:0`（唯一带前缀的一条，见 5.2） |

   预填的 ID 取自 AWS 的模型卡片（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards-anthropic.html>）：到 Opus 4.6 为止的 ID 带版本后缀，之后的不带。**只对照了文档，没有用真实账号调过**，启用前请到 Bedrock 控制台的模型目录核对你要用的模型 ID，不对就在账号上改。2026-11-17 按模型卡片核对过一遍：Claude Sonnet 4 在 AWS 上已于 2026-10-14 停止服务（<https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4.html>），Claude Opus 4 在索引页里已经没有模型卡片，`claude-sonnet-4-20250514`、`claude-opus-4-20250514` 不再预填；新增 Fable 5.2、Opus 5.5、Sonnet 5.5、Haiku 5.5。Opus 4.1（2027-01-08 停止服务）、Sonnet 4.5（2027-04-08 停止服务）处于 Legacy 阶段，仍在表里。Mythos 系列要通过 Anthropic 的验证项目才能用，不预填。已经建好的账号的映射不会跟着变，要用新模型就在账号上自己加。

2. **跨区域推理前缀**加在映射结果前面：默认的 `global` 把 `anthropic.claude-sonnet-4-5-20250929-v1:0` 变成 `global.anthropic.claude-sonnet-4-5-20250929-v1:0`，选了 `us` 就变成 `us.anthropic.claude-sonnet-4-5-20250929-v1:0`。现行的 Claude 模型几乎都只能通过推理配置调用（见 5.2），选“不加”而映射里又是裸 ID 的请求会被 AWS 拒绝。地域前缀要和区域相符（`us-east-1` 配 `us`，`eu-central-1` 配 `eu`，以此类推）。

下面两种映射值**原样发送，不再加前缀**：

- 已经带前缀的 ID：以 `us.`、`eu.`、`apac.`、`jp.`、`au.`、`in.`、`us-gov.`、`global.` 开头（`us-gov` 不在前缀的可选项里，GovCloud 的 ID 要在映射里写全）。想让个别模型用不同的前缀，就在映射里直接写带前缀的完整 ID。
- ARN（以 `arn:` 开头）：应用推理配置、预置吞吐、导入的模型。例如 `arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abcd1234`。

其他说明：

- 账号测试走非流式 InvokeModel，默认模型 `global.anthropic.claude-haiku-4-5-20251001-v1:0`，也就是 Haiku 4.5 的全球推理配置（Haiku 4.5 不支持用裸 ID 按需调用，见 5.2）。它自己带着前缀，所以不管账号的前缀选的是什么（包括“不加”）都原样发送。注意：选了地域前缀的账号，这一次默认测试请求（内容只有一个 `ping`）也是走全球配置的；不想这样，或者这个模型没开通、区域是 GovCloud 时，在测试对话框里指定别的模型（那里填的 ID 照常加账号的前缀）。
- 不支持“从上游获取模型列表”。

### 5.4 行为与限制

- **Claude Code 的请求体会被清洗**：插件拿到完整请求体，删掉 Bedrock 不接受、Claude Code 写在深处的内容：工具定义里的 `custom`，`cache_control` 里的 `scope`（总是删）和模型不接受的 `ttl`（认出模型版本时，只有支持缓存 TTL 的模型保留 `5m`、`1h`；模型 ID 认不出版本时原样保留，以免把一小时的缓存悄悄变成五分钟），工具调用 ID 里 `[a-zA-Z0-9_-]` 以外的字符（换成 `_`），模型不接受的 thinking 设置。这套清洗只对照过旧版的规则和 AWS 文档，**Bedrock 账号主要给 Claude Code 用的话，先在测试分组里验证再放量。**
- 除上一条的清洗外，请求体按客户端原样转发，顶层只处理这些字段：
  - 删除 `model`、`stream`（都移到地址里），写入 `anthropic_version: bedrock-2023-05-31`。走不走流式接口只看客户端请求本身是否流式（`meta.stream`），不看请求体里的 `stream`：运营方在参数覆盖里给非流式请求加 `stream: true` 不会把它变成流式。
  - 删除 `metadata`、`provider`、`service_tier`、`inference_geo`、`speed`：InvokeModel 的请求体没有这些字段；Bedrock 上的数据驻留由推理配置的前缀决定，fast mode 不可用。（此前删的是拼错的 `interface_geo`，真正的 `inference_geo` 会作为未知字段发给 Bedrock。）会悄悄改变回答的字段（`mcp_servers`、`container`、`fallbacks`）不删，交给 Bedrock 拒绝。
  - `anthropic_beta`：见下一条。
  - `context_management`：请求没有带 `context-management-2025-06-27` 这个 beta 时删除。
  - `output_config`、`output_format` **保留**（旧版会删除）：按 AWS 文档，InvokeModel 读 `output_config.format`（结构化输出）和 `output_config.effort`；`output_format` 是已废弃的写法，插件不替客户端转换。
- `anthropic-beta`：Bedrock 不认这个请求头，beta 要写在请求体的 `anthropic_beta` 里。插件把客户端请求头里的和请求体自带的 `anthropic_beta` 里的合在一起，Bedrock 支持的写进请求体的 `anthropic_beta`，不支持的丢弃、重复的只留一个，请求头本身不发给上游；一个都不剩时请求体里的 `anthropic_beta` 也删掉。支持的名单（15 项，不分模型，某个模型没有的特性由上游拒绝）：`computer-use-2025-01-24`、`computer-use-2025-11-24`、`context-1m-2025-08-07`、`context-management-2025-06-27`、`compact-2026-01-12`、`compact-2026-09-04`、`fallback-credit-2026-07-01`、`thinking-display-updates-2026-08-18`、`mid-conversation-output-config-2026-07-01`、`mid-conversation-effort-2026-08-01`、`per-turn-control-2026-07-01`、`per-message-effort-2026-07-01`、`fine-grained-tool-streaming-2025-05-14`、`tool-search-tool-2025-10-19`、`tool-examples-2025-10-29`（`advanced-tool-use-2025-11-20` 会换成 tool search，并自动带上 tool examples）。前 5 项和最后 3 项抄自旧版，其余 7 项取自 AWS 用户指南（模型卡片和 adaptive thinking 一页）；都没有用真实账号试过。
- 插件会按请求体里的工具自动补 beta（客户端没点名也补，做法同旧版）：`computer_20250124` 类型的工具补 `computer-use-2025-01-24`，其余 `computer_20…` 补 `computer-use-2025-11-24`；tool search 工具（`tool_search_tool_regex_20251119`、`tool_search_tool_bm25_20251119`，只在认出支持它的模型上）、允许代码执行调用的工具（`allowed_callers` 含 `code_execution_20250825`）和带 `input_examples` 的工具补 `tool-search-tool-2025-10-19`，有它就一并带上 `tool-examples-2025-10-29`（`beta.go` `toolBetas`）。其他特性仍要客户端自己在 `anthropic-beta` 头或 `anthropic_beta` 里点名。
- 请求头：`Content-Type`、`Accept` 是默认头，运营方的请求头覆盖可以改；鉴权头（API Key 的 `Authorization`，签名类型的签名头）是权威头，运营方改不了。API Key 与 Access Key 类型不带会话令牌，插件把 `X-Amz-Security-Token` 标成“不发”，运营方的请求头设置加的会话令牌不会发到 AWS。
- `count_tokens` 不发给 Bedrock，由核心本地估算。
- 错误处理：

  | 上游回答 | 结果 |
  |---|---|
  | 429（限流、模型未就绪） | 换号，冷却到 `retry-after-ms` / `retry-after` 指的时间，没有就 60 秒 |
  | 400 且是 `ServiceQuotaExceeded`（AWS 账号的配额用完） | 换号，不动账号 |
  | 401，或密钥不对（`UnrecognizedClient`、`InvalidClientTokenId`、签名不匹配） | 换号，禁用账号 |
  | 临时凭证过期（`ExpiredToken`） | 换号，禁用账号，原因写明凭证已过期 |
  | 其余 403（多数是 `AccessDenied`：这个模型没开通或没权限） | 换号，冷却 60 秒，不禁用 |
  | 签名时间被拒绝（`RequestExpired`、`Signature expired`）、签名格式被拒绝（`IncompleteSignature`） | 换号，不动账号；前者说明服务器时钟不准 |
  | 408、500、503、529 | 换号，不动账号 |
  | 其他 400（如 `ValidationException`）、404、424 | 原样回给客户端 |
  | 流式响应中途的异常 | 客户端收到 `error` 事件，记为上游流错误 |

- 不支持：Bedrock 的 OpenAI 兼容接口（`/openai/v1`）、Claude 以外的模型、Converse 接口、自定义端点（VPC 端点）、中国区和隔离分区、AssumeRole 自动续期、从上游拉模型列表。

## 6. 还没验证的事（上线前必读）

没有任何真实云账号参与过测试。完整清单在设计文档第 12 节，最要紧的几条：

1. 三家的请求地址、签名、令牌换取、错误码都只对照过文档和模拟上游，第一次接真实账号时要逐项看使用记录和账号状态。
2. Azure 的验收测试（`next/e2e/ac29_azure_test.go`）只对模拟上游跑过：它随整套 e2e 在 e2e 栈上运行并通过，没有接过真实的 Azure。
3. 这些新表单没有在控制台里做过界面走查（分组折叠、鉴权方式切换、编辑时隐藏字段的回传、日期时间控件、预填的模型映射）。
4. Bedrock 预填的模型映射和 beta 名单只对照过 AWS 文档（beta 名单里旧版的 8 项连文档也没对照），没有用真实账号调过；保留 `output_config` / `output_format` 也只有文档依据。
5. Bedrock 对 Claude Code 请求体的清洗没有用真实账号实测过。
