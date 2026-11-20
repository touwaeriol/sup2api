# codex 插件：ChatGPT 账号（Codex 设备码 / Sign in with Codex）

日期：2026-11-08（额度窗口 2026-11-09，官方用量重置次数 2026-11-10）。状态：已实现（插件 0.1.1 已上线；0.1.2 增加 `codex_device` 的 5h / 周额度窗口，见第 9 节，需核心 ≥0.1.257；0.1.3 接入官方用量重置次数，见第 10 节，需核心 ≥0.1.269；0.1.4 随权限加固发布，只改授权相关行为，见第 11 节，需核心 ≥0.1.286；0.1.7 的用量窗口只取自响应头，见第 13 节，同版新增 Sign in with Codex（`codex_oauth`）并删除 Sign in with ChatGPT（`chatgpt_siwc`），见第 14 节，需核心 ≥0.1.297）。0.1.11 是线上最后发布的版本。下一个发布版本是 0.2.0（未发布，需核心 ≥0.1.304、UI 3.20）：按转发 V2 改写（第 18 节；开发中曾编号 0.1.12，从未发布，因为是不兼容的契约变化，与其他平台插件一样升次版本号），并且授权请求由核心经账号表单选的代理发出，插件不再联网（第 19 节，CONTRACTS §117），以及经核心转换服务 OpenAI 生图接口（第 21 节，CONTRACTS §121）。核心契约见 CONTRACTS §83、§112（WebSocket 每轮见 §83.6），额度见 §44、§69。第 4 节按现状写；第 6–17 节是各版本当时的记录，其中的 `BuildUpstreamRequest`、补丁、`requestFields` 都已被第 18 节取代。

## 1. 目标与边界

- 新增内置并默认启用的插件 `codex`，让运营者用**自己的** ChatGPT 订阅为**自己的**应用提供 OpenAI 兼容接口：`/v1/chat/completions`、`/v1/responses`、`/v1/responses`（WebSocket 模式）；0.2.0 起另有 `/v1/images/generations`、`/v1/images/edits`（第 21 节）。
- 两种建号方式（同一 `creationGroup: "codex"` 下的两个账号类型）：
  1. `codex_device`：Codex 设备码登录（与 Codex CLI `--device-auth`、OpenCode 等第三方工具相同的公开流程）。
  2. `codex_oauth`：Sign in with Codex，Codex CLI 自己的浏览器登录（授权码 + PKCE，2026-11-17 新增，见第 14 节）。
  - ~~`chatgpt_siwc`：OpenAI 官方第三方入口 Sign in with ChatGPT · ChatGPT plan usage~~（2026-11-17 删除，见第 14 节；下文涉及 `chatgpt_siwc` 的内容只作历史记录）。
- 守线（写进插件说明，不做技术强制）：
  - 请求默认如实标明来源：`originator: sup2api`、`User-Agent: sup2api-codex/<版本>`；两者可以在账号上或插件设置里由运营方自定义（第 20 节，2026-11-18 所有者决定）。插件**不**发 `x-codex-*` 头、不仿 TLS 指纹，**不**注入 Codex 官方系统提示词。
  - 仅供账号所有者自用；把多人 ChatGPT 账号做成号池对外出售违反 OpenAI 条款（付费/托管应用需先通过 OpenAI 的 Sign in with ChatGPT 候补审批）。
- 不做：连接池/预热、`previous_response_id` 跨连接恢复。

## 2. 官方协议事实

### 2.1 Codex 设备码（`codex_device`）

来源：openai/codex `codex-rs/login/src/device_code_auth.rs`。issuer = `https://auth.openai.com`，client_id = `app_EMoamEEZ73f0CkXaXp7hrann`（Codex 公开客户端）。

1. `POST https://auth.openai.com/api/accounts/deviceauth/usercode`，JSON `{"client_id": "..."}` → `{"device_auth_id", "user_code"（或 "usercode"）, "interval"（字符串秒数）}`；404 = 该账号/工作区未开启设备码登录（ChatGPT 设置 → 安全 → Device code authorization；企业工作区需管理员开启）。
2. 展示验证地址 `https://auth.openai.com/codex/device` 和 `user_code`，15 分钟有效。
3. 轮询 `POST https://auth.openai.com/api/accounts/deviceauth/token`，JSON `{"device_auth_id", "user_code"}`：403/404 = 尚未授权，按 interval 继续；2xx → `{"authorization_code", "code_challenge", "code_verifier"}`；其他状态 = 失败。
4. 换令牌：`POST https://auth.openai.com/oauth/token`，form：`grant_type=authorization_code`、`code`、`redirect_uri=https://auth.openai.com/deviceauth/callback`、`client_id`、`code_verifier` → `{id_token, access_token, refresh_token, expires_in}`。
5. 刷新：`POST https://auth.openai.com/oauth/token`，JSON `{"client_id", "grant_type":"refresh_token", "refresh_token", "scope":"openid profile email"}`（与旧版 `backend/internal/pkg/openai/oauth.go` 一致）；refresh token 会轮换。
6. 身份：id_token 的 `https://api.openai.com/auth` 声明里有 `chatgpt_account_id`、`chatgpt_plan_type`；`email` 在顶层。
7. 上游：`POST https://chatgpt.com/backend-api/codex/responses`，头 `authorization: Bearer <access_token>`、`chatgpt-account-id`、`originator`、`user-agent`（取值见第 20 节，默认 `sup2api` 与 `sup2api-codex/<版本>`）、`accept: text/event-stream`，请求体带 `prompt_cache_key` 时另加 `session_id` / `conversation_id`（§16）；请求体须 `store:false`、`stream:true`、带 `instructions` 字段（缺省时补空串）。WebSocket：`wss://chatgpt.com/backend-api/codex/responses` + `openai-beta: responses_websockets=2026-02-06`。

### 2.2 Sign in with ChatGPT（`chatgpt_siwc`，已删除，见第 14 节）

来源：developers.openai.com/siwc/token-sharing-open-source/*。

1. 宿主 ID：每个账号生成一次 `ext_agent_host_id = urn:uuid:<UUIDv4>`，存进凭证，以后不变（两节点共用同一账号即同一 host）。
2. 授权地址 `https://auth.openai.com/api/accounts/authorize`，参数：`client_id=dynamic_agent_client`（首次；重新授权用已发放的 client_id 且不带 agent_name_hint）、`agent_name_hint=sup2api`、`ext_agent_host_id`、`response_type=code`、`redirect_uri=http://127.0.0.1:1455/auth/callback`、`scope=openid profile email offline_access resource.invoke chatgpt.tokens.use.direct`、`resource=https://api.openai.com/v1`、`state`、`nonce`、`code_challenge_method=S256`、`code_challenge`。
3. 服务器不在本地：浏览器跳到 `127.0.0.1:1455` 会打不开，运营者把地址栏完整网址粘回后台。插件解析 `code`、`state`、`client_id`、`scope`、`error`；校验 state；`error=access_denied` 直接失败；新注册缺 `client_id` 视为未完成；重新授权时返回的 client_id 与已存的不同要拒绝。
4. 换令牌：`POST https://auth.openai.com/api/accounts/oauth/token`，form：`grant_type=authorization_code`、`client_id`（已发放的）、`code`、`code_verifier`、`redirect_uri`（同上）、`resource=https://api.openai.com/v1`。必须确认授予的 scope 含 `chatgpt.tokens.use.direct`。
5. 刷新：同一 token 端点，form：`grant_type=refresh_token`、`client_id`（已发放的）、`refresh_token`、`resource`；不带 scope。终止性错误：`invalid_grant`、`invalid_refresh_token`、`token_expired`、`refresh_token_expired`、`refresh_token_invalidated`、`refresh_token_reused`（需重新授权）；`invalid_client`（配置错误）。其他错误保留凭证、稍后重试。
6. 上游：`POST https://api.openai.com/v1/responses`，`authorization: Bearer <access_token>`；必须 `store:false`、`stream:true`，`input` 必须是数组，不支持 `temperature`、`top_p`、`max_output_tokens`、`metadata`、`user`、`truncation`，HTTP 下不能用 `previous_response_id`，不能有 system 角色的 input 项（改用 `instructions`）。禁止调用 `backend-api`。WebSocket：`wss://api.openai.com/v1/responses`，连接内可续接。
7. 模型：`GET https://api.openai.com/v1/models` → `{"models":[{"slug","display_name","visibility"}]}`，取 `visibility=="list"` 的 `slug`。
8. 错误码（结构化 Responses 错误）：`subscription_sharing_usage_limit_exceeded`（429，冷却账号，不推断重置时间）、`subscription_sharing_usage_unavailable`/`subscription_sharing_user_unavailable`（503，短暂退避）、`subscription_sharing_user_not_eligible`（403，禁用账号）、`subscription_sharing_unsupported_capability`（400，原样返回客户端）、`subscription_sharing_route_not_supported`（403）、`subscription_sharing_invalid_user`（401）、`chatpass_v2_scope_not_authorized`/`chatpass_v2_invalid_authorization_context`（403，禁用）。

## 3. 核心改动：上游只接受流式（`streamOnly`）

两类账号的上游都**只接受 `stream:true`**。核心目前按客户端的 `stream` 原样转发，非流式客户端（以及被转换成 Responses 的非流式 chat 请求）会失败。新增：

- `manifest.AccountPlatform.StreamOnly []string`（JSON `streamOnly`）：列出该账号类型上游只接受流式的上游协议（本插件填 `["openai.responses"]`）。
- 请求选中此类账号、上游协议在列表里、且本次为非流式时，核心：把上游请求体的 `streamPath` 置 `true`；按 SSE 读上游；从 `response.completed`（或 `response.incomplete`）事件取 `response` 对象作为非流式 JSON 响应体；若其 `output` 为空，用沿途 `response.output_item.done` 的 `item` 按 `output_index` 补齐；`response.failed`/`error` 事件按上游错误处理（交给插件 `ClassifyError`，状态码取事件里的值，缺省 502）。之后走原有非流式路径（JSON 用量规则、协议转换成 chat JSON、记录、计费）。
- 使用记录的 `stream` 字段仍记客户端视角（false）。
- 只对声明了 `streamOnly` 的账号类型生效，其他插件行为不变。CONTRACTS 增加一节。

## 4. 插件 `codex`

- 目录 `next/plugins/codex`，key `codex`，runtime grpc，能力 `platform.adapter.v1`、`platform.websocket.v1`、`http.routes.v1`。
- 两个账号类型都挂内置 `openai` 平台，`endpoints: ["responses", "responses_websocket"]`（chat 由核心转换成 Responses；0.2.0 起要在 `adapted` 里显式列出 `chat_completions` 和两个生图端点，第 21 节），`streamOnly: ["openai.responses"]`，`refresh: {}`，`sensitiveFields: ["access_token","refresh_token","id_token"]`（没有 `settingsFields`：账号的代理是账号的 `proxy_id` 列，原来写的 `settingsFields: ["proxy_id"]` 没有写入方，已删，第 22 节），表单 `mode: "native"`、组件 `CodexAuthForm`。
- 插件只实现 `BuildUpstream`，`Execute` 由 SDK 的 `ExecuteDefault` 提供（不等待地中继，返回 `host_usage`，用量由核心按 `openai.responses` 规则读）；`BuildUpstream` 两种类型完全相同，上游是 `chatgpt.com/backend-api/codex/responses`（§2.1 第 7 条的 URL 与头）；请求体用 `jsonedit` 一次改完：`store=false`；字符串 `input` 转数组（空白字符串转 `[]`，其余包成一条 user 消息）；`role:"system"` 的 input 项改成 `developer`；缺 `instructions` 或为 `null` 时补空串（与旧版后端一致；其他值原样转发）；删除不支持字段；WebSocket 协议换成 `wss` + `GET` + `openai-beta`，每轮 `response.create` 消息同样改写（不碰 stream 相关字段；核心每轮调一次 `Execute`，CONTRACTS §83.6）。插件的头全部是权威头（与转发 V2 之前一样，运营方的透传和覆盖改不了），不转发任何客户端头；请求体里有非空字符串 `prompt_cache_key` 时加 `session_id` 和 `conversation_id`（第 16 节）。
- `ClassifyError`：reason 一律纯英文（与 SDK `classify` 一致）；401 → 冷却 5 分钟（`authCooldown`）并提示重新授权（刷新由核心按 `refresh` 声明处理）；408、5xx 与传输错误 → 冷却 10 秒（`serverErrorCooldown`）；429 且响应头显示某个用量窗口 ≥100% 并给出重置时刻 → 自动禁用到最晚的重置时刻（0.1.7 起只看响应头，见第 13 节；0.1.2–0.1.6 见第 9 节）；其他 429 原样返回客户端、不冷却不禁用（0.1.2 起；0.1.1 为冷却）；其他 403 → 禁用；400 原样返回。
- 刷新：`BuildRefreshRequest` / `ParseRefreshResponse`，两种类型相同（Codex 客户端的 `POST /oauth/token` JSON，§2.1 第 5 条）；轮换后的 refresh token 一并保存。
- 管理路由（`scope: admin`，权限 `codex:manage`）。响应按 CONTRACTS §3.1：成功为 `{"data": ...}`（`pluginsdk.DataResponse`，下面写的是 `data` 的内容，控制台 `pluginApi` 自动解包）；请求本身不合法（缺 `session_id`、请求体不是合法 JSON）为 400 `invalid_argument`，`details.fields = [{field, code, message}]`（`pluginsdk.BadRequest`；field 为 `session_id` / `body`，code 为 `required` / `invalid_json`）；授权流程本身的拒绝保留各自的错误码（`device_auth_disabled`、`invalid_callback`、`state_mismatch`、`access_denied`、`authorization_failed` 为 400，`session_expired` 为 404，`upstream_unavailable` / `upstream_error` / `token_exchange_failed` 为 502）。所有 message 为“English / 中文”双语（CONTRACTS §10）。
  - `POST /auth/device/start` `{proxy_ticket}` → `{session_id, verification_url, user_code, interval, expires_in}`；会话存 KV（含 device_auth_id，15 分钟过期）。
  - `POST /auth/device/poll` `{session_id, proxy_ticket}` → `{status: "pending"|"done"|"expired"|"error", credentials?, message?, interval?}`；`interval` 为 pending 时的轮询间隔（秒，OpenAI 返回 429 时加长）；done 时返回完整凭证对象（见下），同时删除会话。同一会话的“读会话 → 问 OpenAI → 写回/删除”在集群锁（`Host.Locks`，锁名为会话 id 的哈希，ttl 50 秒）下进行：两个标签页或两个节点并发轮询时，拿不到锁的一方直接回 `pending`，不会两边都去用一次性授权码换令牌；锁服务不可用时回 503 `unavailable`。
  - `POST /auth/codex/start` `{proxy_ticket}` → `{session_id, authorize_url}`；PKCE verifier、state 存 KV（30 分钟）。2026-11-17 起取代 `/auth/siwc/start`，见第 14 节。
  - 开始、轮询、换令牌路由都要 `proxy_ticket`（账号表单**当前**的代理选择，界面每次调用现取一张，第 19 节）：缺了 400 `invalid_argument`（field `proxy_ticket`）；核心拒绝发送（票据或代理不再可用）时开始、换令牌回 400 `proxy_refused`，轮询回 `pending` 并在 `message` 里带拒绝原因。请求没有到 OpenAI，设备码、授权码都没用掉，所以会话保留：代理改好后，下一次轮询或换令牌带着新票据就能继续。例外是设备码登录批准之后的换令牌：用户批准后插件先删会话（授权码只能用一次）再换令牌，这一步被核心拒绝时轮询回 `error`，`message` 要求检查账号表单里的代理后重新开始（会话已删，再轮询只会得到 `expired`）。
  - `POST /auth/codex/exchange` `{session_id, callback_url, proxy_ticket}` → `{credentials}` 或错误。
  - 四个路由的请求体另带可选的 `user_agent`、`originator`：账号表单此刻填的客户端标识（第 20.2 节），界面每次调用都带上，插件只用于这次调用的请求、不存进会话。校验与 `ValidateCredentials` 对这两个字段的相同（`credMap.agentError`：必须是字符串，去掉首尾空白后不能含控制字符），不合格时 400 `invalid_argument`（field `user_agent` / `originator`，code `pattern`），什么也不发。
  - 两种会话都绑定发起它的登录用户（`pluginsdk.SaveCallerSession`，2026-11-12）：其他用户拿到 `session_id` 也无法继续，得到的回答与会话不存在时相同（轮询 `expired`，exchange 404 `session_expired`）；没有登录用户时 start 回 401。
- 凭证对象：
  - 共有：`access_token`、`refresh_token`、`id_token`、`expires_at`（Unix 秒）、`email`、`plan_type`（从令牌读出，两种类型都可能有）；除 `access_token` 外缺值时省略。
  - `chatgpt_account_id`（两种类型都有）。
  - `user_agent`、`originator`：账号自己的客户端标识（第 20 节），授权流程不写，表单里填（授权时每次调用也带上表单此刻的值）；空或没有时用插件设置的值。
  - ~~`chatgpt_siwc`：`client_id`、`ext_agent_host_id`、`scopes`~~（已删除）。
- 网络权限（与 manifest 一致）：0.2.0 起没有 `net`，插件不开任何对外连接。`auth.openai.com`、`chatgpt.com` 列在 `externalServices`：授权请求是带代理票据的取回（第 19 节），上游请求、令牌刷新与重置次数请求都由核心经账号代理发出。（0.2.0 之前 `net` 只有 `auth.openai.com`，授权请求由插件直连。）不访问 `api.openai.com`，没有模型列表（控制台用 manifest 默认模型），也没有用量查询（窗口由核心从响应头采样，第 13 节）。
- 其他宿主权限：`platform.register`、`kv`（授权会话）、`lock`（设备码轮询的会话锁，见上）、`routes.admin`、`ui.native`；0.1.3 起另有 `accounts.auto_state`（`types: own`），见第 10 节。
- 日志：`Init` 取 `Host.Logger()`；授权失败、令牌换取失败、删除会话失败、锁获取/释放失败记 warn，只记状态码、OAuth 错误码、错误文本；绝不记录令牌、授权码、PKCE verifier、state 或会话 id。
- `ValidateCredentials` 会解析调用方提交的 `id_token`（不验签），用来补缺的 `email`、`plan_type` 与 `chatgpt_account_id`，后者会作为上游头 `chatgpt-account-id` 发送。凭证是运营者自己的（只有有账号权限的管理员能提交），上游会用 access token 校验账号 id，填错只会让运营者自己的请求失败，sup2api 也不据此做任何授权判断，所以不验签（`identity.go` `jwtClaims` 注释）。
- 原生表单 `CodexAuthForm`：设备码流程（`codex_device`；开始 → 显示链接与验证码、复制按钮、倒计时、自动轮询 → 成功后填入凭证并显示邮箱/套餐）；Sign in with Codex 流程（`codex_oauth`；2026-11-17 起取代 Sign in with ChatGPT；生成链接 → 新窗口打开 → 粘贴回调网址输入框 → 换取 → 填入凭证）；编辑已有账号时显示当前邮箱与“重新授权”。只显示编辑器传的 `accountType` 对应的那一种（编辑器总会传，CONTRACTS §23.5；0.2.0 起不再有类型未知时两种并列的页签）。中英文文案。回调网址的格式提示与插件 `parseCallback` 一致（完整网址、不带 scheme 的地址、只有查询串 `?code=…` / `code=…`、片段里的参数都接受；只有插件也解析不了的——非法 `%` 转义或分号——才禁用“完成授权”）。失败时优先显示 `details.fields` 里的字段消息，否则显示错误的 message。

## 5. 内置

`deploy/docker/build-go.sh` 的 `BUILTIN_PLUGINS` 加 `codex`（安装即启用）。web mock 加 codex 插件与两个账号类型。

## 6. 实现记录（2026-11-08）

- 核心 §83：`streamOnly`（非流式请求强制上游流式、合成 JSON）；同平台转换 `openai.chat → openai.responses`（2026-11-08 改为宽松：旧式 functions、custom 工具、allowed_tools、web_search_options 等能等价的都转换；`stop`、`seed`、罚分、logprobs、消息 `name`、未知字段等静默丢弃并在网关 debug 日志记下字段路径；只有 `n>1`、音频输出/输入、空 `messages` 等无法保留含义的返回 400 `unsupported_conversion`；对话中间的 system/developer 消息转成 `developer` 角色。详见 CONTRACTS §83.2）；manifest 校验对用到这两项的插件要求 hostCompat ≥ `StreamOnlyFirstCore`（占位 0.1.257，发布时定号）。
- 插件：`codex_device` 也删除 Codex 后端已知不接受的字段（`max_output_tokens`、`max_completion_tokens`、`temperature`、`top_p`、`frequency_penalty`、`presence_penalty`、`user`、`metadata`、`safety_identifier`、`stream_options`、`truncation`、`prompt_cache_retention`、`stop_sequences`；WebSocket 消息保留 `stream_options`，见 `upstream.go` `unsupportedDevice`）；`chatgpt_siwc` 必须有 `client_id`。net 权限只给 `auth.openai.com`（上游与刷新由核心发出）。
- 已知限制：~~WebSocket 消息核心不做 patch，客户端需自带 `store:false`（device 还需 `instructions`）；SIWC 下对话中间的 system/developer 消息会被上游拒绝；~~（0.1.1 已解决，见下）device 账号无在线模型列表；授权请求不走账号代理；路由要求 `codex:manage` 权限。

## 7. 0.1.1（2026-11-08，用户要求 sup2api 兜住客户端差异）

- 核心 CONTRACTS §83.6：WebSocket 每轮消息都调用插件 `BuildUpstreamRequest` 取 body patch（首轮复用握手那次调用，后续轮只取 patch、忽略 URL/头），在钩子与模型映射之后应用；空 patch 时消息逐字节不变；patch 失败回该轮错误事件、不断开。属于同一未发布核心（占位 0.1.257），`hostCompat` 不变。
- 插件：WebSocket 返回与 HTTP 相同的 patch（`store=false`、删除不支持字段、device 补 `instructions`、字符串 input 转数组），不含 stream 相关字段，SIWC 保留 `previous_response_id`。客户端不再需要自带 `store:false`/`instructions`。
- 插件：两种账号类型、HTTP 与 WebSocket 都把 `role:"system"` 的 input 项改成 `role:"developer"`；`requestFields` 新增 `input.#.[role].#(=="system")#`（按项序 `["system"]`/`[]`，每项约 3 字节），据此生成 `input.<i>.role` 的 `OP_SET`，每请求最多 256 个。
- 仍有限制：字符串 input 字段回显整段字符串，超过插件 gRPC 接收上限（4 MiB）会使调用失败；device 账号无在线模型列表；授权请求不走账号代理；路由要求 `codex:manage` 权限。

## 8. 编码规范审核修复（2026-11-08，插件版本仍为 0.1.1）

- 用户可见文案一律“English / 中文”：路由错误、`readToken`、`internalError`、刷新结果（令牌端点状态、响应过大、无法解析、缺 `access_token`、连接失败）。`ClassifyError` 的 reason 改为纯英文。
- 路由：成功统一 `{"data": ...}`；请求参数错误统一 400 `invalid_argument` + `details.fields`（替换原自定义 `invalid_request`、`invalid_client_id`、`invalid_host_id`）；流程业务码不变（见 §4）。web mock（`web/mock/codex.ts`）同步为相同的响应格式与错误码（为便于试用，回调缺 `client_id` 时 mock 仍代发一个，不报 `registration_incomplete`）。
- 设备码轮询加会话锁（新增宿主权限 `lock`），授权失败等记 warn 日志（不含敏感值），见 §4。

## 9. 0.1.2：`codex_device` 的 5 小时 / 周额度窗口（2026-11-09，用户要求）

目标：和 Claude 账号一样在控制台显示 5 小时窗口（key `5h`）和周窗口（key `7d`）的用量与重置时间；窗口真满时按项目规则自动禁用到重置，其他上游 429 原路返回（CONTRACTS §69 末两条，CCGateway 0.1.127 同一规则）。

> **0.1.7 起本节 9.2–9.4 已被第 13 节取代**（9.1 里按 ±5% 容差定 key、`reset-after-seconds`、`wham/usage` 的内容和 9.5 的测试清单同样作废）：窗口只来自响应头，`wham/usage` 查询已删除，判满只看 429 的响应头。下面保留的是 0.1.2–0.1.6 的历史设计。

### 9.1 协议事实

- 响应头：`x-codex-primary-used-percent`（百分比）、`x-codex-primary-window-minutes`、`x-codex-primary-reset-at`（Unix 秒，新后端）或 `x-codex-primary-reset-after-seconds`（旧后端），`secondary` 同名三项。
- 用量接口：`GET https://chatgpt.com/backend-api/wham/usage` → `{"plan_type", "rate_limit": {"allowed", "limit_reached", "primary_window": {"used_percent", "limit_window_seconds", "reset_after_seconds", "reset_at"} | null, "secondary_window": … | null} | null, "credits", "additional_rate_limits"}`。未使用的槽位为 `null`。
- **primary / secondary 与 5h / 周的对应关系不固定**（旧版 sub2api 注释写 primary = 周、secondary = 5h，Codex CLI 里 primary 通常是 300 分钟），所以插件一律按窗口时长定 key（`windowKey`）：300 分钟 ±5% → `5h`，10080 分钟 ±5% → `7d`；其他时长按实际长度给 key（整天 `<n>d`、整小时 `<n>h`，否则 `<n>m`，控制台按 key 原样显示）；没有时长或时长不可信（≤0 或超过一年）的窗口忽略。两个窗口时长相同时保留用得多的那个。
- `reset-at` / `reset_at` 优先（大于 1e11 视为毫秒），没有时用 `reset-after-seconds` / `reset_after_seconds` 加当前时间；都没有时重置时间未知（0）。没有用量百分比的窗口记为 `utilization_unknown`。用量 ≥100% 的窗口状态记 `rejected`，其他不写状态（后端不报）。
- `additional_rate_limits`（按功能的额外限额，如 Codex Spark）与 SSE 里的 `codex.rate_limits` 事件不读：前者不是账号级窗口，后者核心不交给插件。

### 9.2 manifest（只给 `codex_device`）（已被第 13 节取代）

```json
"quota": {
  "query": true,
  "autoRefresh": true,
  "windows": [
    { "key": "5h", "label": { "en": "5-hour usage", "zh": "5 小时用量" }, "shortLabel": { "en": "5h", "zh": "5 小时" } },
    { "key": "7d", "label": { "en": "Weekly usage", "zh": "周用量" }, "shortLabel": { "en": "7d", "zh": "7 天" } }
  ]
}
```

- 不声明 `quota.headers`：声明式被动采样把固定的头映射到固定的 key（CONTRACTS §44.2），表达不了“按 window-minutes 选 key”，而核心目前没有让插件在成功响应上解析额度头的钩子（只有 `ClassifyError` 的 `quota`，见 9.4）。所以数据来自主动查询：`autoRefresh` 让核心后台每 30 分钟查一次（新账号录入后立即查一次，升级后已有账号由核心的补建扫描建立查询计划），点击控制台用量单元格强制刷新。窗口重置时间在 30 分钟内时，插件建议在重置后 1 分钟复查（`next_check_at_unix`）。
- `chatgpt_siwc` 不声明额度：Sign in with ChatGPT 官方文档没有额度头，并且禁止调用 `backend-api`，只靠 `subscription_sharing_usage_limit_exceeded` 冷却。`BuildQuotaRequest` 对它回 `Unimplemented`。
- 不声明 `modelAvailability`：额度窗口不参与调度准入（同 CCGateway 0.1.112 起的做法），真满时由下面的自动禁用把账号拿出调度。
- `autoRefresh` 需要 Host API 7（SDK 在旧核心上拒绝初始化）；`hostCompat` 仍为 `>=0.1.257 <0.2.0`，所用能力（`quota.query/autoRefresh/windows`、`automatic_state`、Host API 7）0.1.257 都有。控制台已认识 `5h` / `7d`，前端不用改。

### 9.3 查询（插件描述、核心发送）（已被第 13 节取代，查询已删除）

- `BuildQuotaRequest`：`GET <chatgpt>/backend-api/wham/usage`，头 `authorization: Bearer <access_token>`、`chatgpt-account-id`（凭证里有时）、`originator: sup2api`、`user-agent: sup2api-codex/<版本>`、`accept: application/json`。不发任何伪装 Codex 客户端的头（`openai-beta`、`oai-language`、`sec-fetch-*` 等旧版用过的都不发）。凭证无 `access_token` → `FailedPrecondition`，凭证不是 JSON → `InvalidArgument`。
- 核心经账号代理发送，所以插件 `net` 权限不变（仍只有 `auth.openai.com`），`chatgpt.com` 已在 `externalServices`。
- `ParseQuotaResponse`：401/403 → `auth_rejected`；连接失败、其他非 2xx、响应过大、无法解析 → `transient`（只记录在快照上，不冷却、不禁用账号）；`rate_limit` 为 `null` 或两个窗口都为 `null` → 成功、没有窗口。

### 9.4 `ClassifyError`：窗口用满才禁用（已被第 13 节取代）

- **窗口用满**（`windowFull`）：上游错误 type/code 为 `usage_limit_reached`（任何状态码），或 429 且 `x-codex-*` 头里 `5h` / `7d` 窗口用量 ≥100%。其他时长的窗口满、只有重置时间或 `retry-after`，都不算。
- 用满时：`action = FAILOVER`，`automatic_state = DISABLE`、`until_unix` = 重置时刻，reason `Codex usage limit reached until <时间> (<来源>)`。重置时刻依次取：错误体 `resets_at`（秒或毫秒）/ `resets_in_seconds`（错误对象内或顶层）→ 头里已满的 5h/周窗口中最晚的重置时刻 → `retry-after(-ms)` → 5 分钟；限定在 [现在+1 秒, 现在+7 天]。核心不论是否发布了故障策略都执行这个临时禁用，到点自动恢复（CONTRACTS §69）；账号不允许自动禁用时，核心退回冷却到同一时刻（`cooldown_until_unix`，仅无故障策略时）。
- **其他 429**（包括带窗口头但都没满的）：`action = RETURN_TO_CLIENT`，不冷却、不禁用，reason `upstream 429 without a used-up usage window`，上游错误码照常作为 `client_error_code` 返回。0.1.1 对所有 429 冷却（默认 5 分钟），这是行为变化。
- 错误响应带 `x-codex-*` 窗口头时，`ClassifyErrorResponse.quota` 附上这些窗口（已满的标 `rejected`）。注意：核心只在声明了 `modelAvailability` 的类型上保存这份观测（准入时要有快照 generation，CONTRACTS §44.7），本插件不声明，所以目前不会写入快照，控制台的用量仍以查询为准。
- `subscription_sharing_usage_limit_exceeded`（`chatgpt_siwc`）保持冷却，不推断重置时间、不自动禁用。发布了故障策略时核心不采用插件给的冷却（§69），这类账号的处理由策略决定。

### 9.5 上线

- 只需在线升级插件到 0.1.2；不需要升级核心（线上 0.1.258 ≥ 0.1.257）。
- 测试：`quota_test.go`（时长 → key、两种窗口顺序、reset-at 与 reset-after、毫秒、缺字段、null 窗口、错误映射、查询请求构造、经 SDK 服务端调用）、`classify_test.go`（`usage_limit_reached` 与头部判满的自动禁用和观测、其他 429 原样返回、SIWC 仍为冷却）、`manifest_test.go`（只有 `codex_device` 有额度声明、窗口标签中英文、`net` 不变）。

## 10. 0.1.3：官方用量重置次数（2026-11-10，用户要求）

目标：ChatGPT 套餐会发放少量“用量重置次数”（rate-limit reset credits），每次可把账号已用满的 Codex 用量窗口立即重置一次。控制台上能查看某个 `codex_device` 账号（0.1.7 起两种类型都适用）剩余的重置次数，并由管理员手动对该账号使用一次；成功后账号立即恢复调度。仍只用于账号主人自己的订阅。

### 10.1 官方接口（openai/codex `codex-rs/backend-client` 的 `rate_limit_resets.rs`、`types.rs`）

- 根为 `https://chatgpt.com/backend-api`，鉴权头与 `wham/usage` 完全相同。~~只有 `codex_device` 能用：Sign in with ChatGPT 禁止调用 `backend-api`。~~（0.1.7 起两种类型都适用：`codex_oauth` 与设备码令牌相同。）
- `GET /backend-api/wham/rate-limit-reset-credits` → `{"available_count": int, "credits": [{"id", "reset_type", "status": "available"|"redeeming"|"redeemed"|…, "granted_at": RFC3339, "expires_at": RFC3339|null, "title": str|null, "description": str|null}]}`。
- `POST /backend-api/wham/rate-limit-reset-credits/consume`，body `{"redeem_request_id": "<uuid>", "credit_id"?: "<id>"}`（不带 `credit_id` 时后端挑下一个）→ `{"code": "reset"|"nothing_to_reset"|"no_credit"|"already_redeemed", "windows_reset": int}`。`redeem_request_id` 是幂等键：同一个 id 重复提交只扣一次（第二次答 `already_redeemed`）。
- `wham/usage` 里也有 `rate_limit_reset_credits: {available_count}`（可能缺失或为 `null`）：0.1.3–0.1.6 用它缓存次数；0.1.7 删除了 `wham/usage` 查询，不再读（见 10.6）。

### 10.2 发送方式

- 插件不直连 `chatgpt.com`（`net` 仍只有 `auth.openai.com`）：两个请求都经 `pluginsdk.SendAccountRequest` 由核心通过账号代理发出（CONTRACTS §91，URL 主机须在 `externalServices`，`chatgpt.com` 已在；核心 0.1.269 起提供，旧核心答 `Unimplemented`，路由回 503 并提示需要的核心版本）。
- 头为 `backendHeaders`（0.1.2–0.1.6 额度查询用的同一组）：`authorization: Bearer <access_token>`、`chatgpt-account-id`（有时）、`originator: sup2api`、`user-agent: sup2api-codex/0.1.3`、`accept: application/json`，consume 另加 `content-type: application/json`。不发 `x-codex-*` 或任何伪装 Codex CLI 的头。

### 10.3 路由（`scope: admin`，新权限 `codex:reset`）

成功为 `{"data": ...}`，错误为 `{"error": {"code", "message"}}`（message “English / 中文”），请求参数错误为 400 `invalid_argument` + `details.fields`，与授权路由一致（§4）。

- `POST /reset-credits/list`，body `{"account_id": int}` → `{"available_count": n, "credits": [{"id", "title", "description", "status", "granted_at": Unix 秒, "expires_at": Unix 秒|null}]}`。
  - `credits` 按 `expires_at` 升序，`null`（不过期或时间无法解析）排最后；顺序稳定。
  - 解析宽松：未知 `status` / `reset_type` 原样透传（`reset_type` 不输出）；`title` / `description` 为 `null` 时输出空串；`granted_at` 解析失败为 0，`expires_at` 解析失败为 `null`。响应不是 JSON → 502 `upstream_error`。
- `POST /reset-credits/consume`，body `{"account_ids": [int], "credit_id"?: str}`。`account_ids` 是控制台声明式行操作的固定形状，本接口**只接受恰好 1 个**正整数 id，否则 400（field `account_ids`，code `count`）。`credit_id` 去空白，空时不发给后端。
  - `reset` / `already_redeemed` → 200 `{"outcome": "reset"|"already_redeemed", "windows_reset": n, "account_restored": bool}`。随后调用 `SetAccountAutomaticState(CLEAR)` 清除账号的自动停用与冷却（§9.4 窗口用满时设置的，核心本版起 CLEAR 也清冷却）；CLEAR 失败只记 warn，请求仍为 200，`account_restored: false`（状态到窗口重置时刻自然解除）。
  - `nothing_to_reset` → 409 `nothing_to_reset`（未消耗次数）；`no_credit` → 409 `no_credit`。
- `POST /reset-credits/summary`，body `{"account_ids": [int, ...]}` → `{"accounts": {"29": {"available_count": 2, "updated_at": 1793491200}}}`（键为账号 id 字符串，`updated_at` 为写入缓存的 Unix 秒）。只读插件 KV、不访问 ChatGPT、不读凭证；没有缓存的账号不出现在结果里（结果可以是 `{}`）。`account_ids` 先只留正整数并去重，之后超过 `manifest.MaxAccountBatch`（500）→ 400（field `account_ids`，code `max`）。供账号列表的次数装饰器使用（列表每 10 秒刷新）。
- list / consume 共同的错误：账号不存在或不属于本插件 → 404 `account_not_found`；~~账号类型不是 `codex_device` → 400 `unsupported_account_type`~~（0.1.7 起两种类型都适用，不再有这个错误）；凭证无 `access_token` 或不是 JSON → 409 `not_authorized`（提示重新授权）；上游 401/403 → 409 `auth_rejected`（提示重新授权）；连接失败、其他非 2xx、响应过大、未知 `code` → 502 `upstream_error`（带状态码和截断到 200 字节的响应片段；片段中出现的 access token 替换为 `[redacted]`，绝不回显令牌）；读账号或发请求的宿主调用失败 → 503 `unavailable`（只有 `Unimplemented` 才提示需要核心 0.1.269）。

### 10.4 锁与幂等

- consume 在每账号一把集群锁下进行（`Host.Locks`，锁名 `codex-reset:<account_id>`，ttl 90 秒（按两次发送各自的 30 秒上限再加余量；实际上核心对整个路由调用的上限也是 30 秒，两次发送都在其中结束，所以重试进行时锁不会过期），结束即释放，释放失败等 ttl 过期）：双击或两个节点同时提交时，拿不到锁的一方回 409 `busy`，不发请求。锁服务不可用 → 503 `unavailable`。
- 每次 consume 新生成一个 UUIDv4 作 `redeem_request_id`；传输失败（status 0）时用**同一个** id 重试一次，所以即使第一次其实已到达后端也只扣一次（后端答 `already_redeemed`，按成功处理）。再失败回 502，不再重试；非 0 状态码不重试。
- list 只读，不加锁、不重试。

### 10.5 manifest 与守线

- `version` 0.1.3，`hostCompat` `>=0.1.269 <0.2.0`（`SendAccountRequest`）。
- 新增 userPermission `codex:reset`（说明写明会消耗账号由 OpenAI 发放的重置次数、不可撤回），三条路由（list、consume、summary）都用它，与授权用的 `codex:manage` 分开。
- 新增宿主权限 `accounts.auto_state`（`types: own`，用于成功后 CLEAR）；`accounts.credentials`、`kv`（次数缓存）、`lock`、`routes.admin` 的 reason 补上本功能。控制台入口（UI 3.8，CONTRACTS §91b，`hostUICompat` `^3.8`）：`account.list.quota` 次数装饰 `CodexResetBadge`、`account.detail.tabs` 标签页 `CodexResetTab`、行操作 `reset-credit`（`POST /reset-credits/consume`，`danger`、带确认、不设 `bulk`），都要 `codex:reset` 且只对 `codex_device` 显示（0.1.7 起两种类型都适用）。
- 守线：只由管理员对单个账号手动触发，插件从不自动消耗重置次数；请求如实标 sup2api；只用于账号主人自己的订阅。
- 测试：`resetcredits_test.go`（假 chatgpt.com + 包一层 harness 宿主 fake `AccountCredentials` / `SendAccountRequest` / `SetAccountAutomaticState`）：list 解析与排序、各 `code` 与状态码的映射、传输失败重试复用同一 `redeem_request_id` 且最多两次、锁被占回 409 且不发请求、SIWC / 无令牌 / 未知账号、`account_ids` 数量不对、reset 后 CLEAR 及 CLEAR 失败仍 200、请求头含 `originator: sup2api` 且无 `x-codex-*`、错误不回显令牌、旧核心回 503；`manifest_test.go`（新权限、路由与权限对应、`accounts.auto_state` 范围）。

### 10.6 次数缓存（账号列表装饰器）

- 位置：插件 KV，namespace `reset_count`，key 为账号 id，值 `{"available_count", "updated_at"}`，TTL 7 天（键名与读写封装都在 `resetcache.go`）。
- 写入：`/reset-credits/list` 成功时用应答的 `available_count` 写入。0.1.3–0.1.6 还在 `ParseQuotaResponse` 解析 `wham/usage` 时写入，0.1.7 随查询一起删除（第 13 节），所以次数只由 list 刷新、由 consume 删除。
- 失效：consume 得到 `reset` / `already_redeemed` 或未知 `code`（可能已扣）后**删除**该账号的缓存键，而不是写“原数 − 1”（原数可能已过时，`already_redeemed` 也未必是这次扣的），也不为此再调一次 list（多一次上游请求）。装饰器在下次打开明细（list）后恢复显示。`nothing_to_reset` / `no_credit` 不动缓存。
- KV 读写失败一律只记 warn：不让 list / consume 失败；summary 中读失败的账号视为没有缓存（一次请求只记一条 warn，带失败个数）。
- summary 一次批量读（`pluginsdk.KVGetMany`，CONTRACTS §93）取全部账号的缓存；一次至多 500 个账号，在批量读 512 个键的上限内。批量读失败时全部视为没有缓存，单个值解不开的只略过该账号。
- 测试：`resetcache_test.go`：summary（去重与非正整数过滤、缺失账号不出现、500 个通过、501 个 400、坏 JSON、一次 summary 只有一次批量读、KV 失败仍 200、不发上游请求）、list 写入与 consume 后删除（拒绝的 consume 不删，KV 失败不影响 consume）。

## 11. 0.1.4：权限加固（2026-11-13，随核心权限加固发布）

功能与上游协议不变，只改授权相关行为；版本在期 4 T84 统一升级（[DESIGN-2026-11-12-AUTHZ-HARDENING.md](DESIGN-2026-11-12-AUTHZ-HARDENING.md)“期 4 T84”）。

- **`hostCompat` `>=0.1.286 <0.2.0`**：`defaultRoles` 显式写出（旧核心把省略与 `[]` 混淆）和路由的 `accounts` 声明都需要权限加固后的核心（0.1.286）。
- **路由的账号范围（T25）**：`POST /reset-credits/list` 声明 `{"action":"update","from":["body:account_id"]}`，`POST /reset-credits/summary` 声明 `{"action":"read","from":["body:account_ids"]}`，核心按调用者对这些账号的范围放行（只有自己创建的账号的角色操作他人账号回 404，CONTRACTS §21）；`/reset-credits/consume` 不声明，由 `ui.accountActions` 推断为 `update` + `body:account_ids`。持有 `codex:reset` 但没有任何范围的 `account:update` 的角色调用 list / consume 由 200 变为 403。
- **默认角色（T58）**：`codex:manage`、`codex:reset` 声明 `defaultRoles: ["admin", "operator"]`。新安装时授给这两个内置角色；已安装的库不回填（D9），升级时若新授予需要同意（敏感权限或授给 user、自定义角色），codex 作为内置插件会暂缓这些授予，由超管在插件页确认（P4C；授给内置角色只有超管能确认）。
- **授权会话绑定发起人（T42）**：设备码与 Sign in with ChatGPT 的会话改用 `pluginsdk.SaveCallerSession` / `LoadCallerSession` / `UpdateCallerSession`，存 `{"uid", "v"}`。他人拿到 `session_id`：设备码轮询回 200 `expired`（与会话不存在相同），SIWC `exchange` 回 404 `session_expired`。升级或回滚时进行中的授权会话（不超过 15 分钟 / SIWC 会话有效期）需重新发起。重置次数缓存不是会话，改名 `loadCached` / `saveCached`，行为不变。
- **账号类型（T26）**：`codex_device`、`chatgpt_siwc` 声明 `upstreamFields: []`（没有可改的上游地址），控制台据 `custom_upstream=false` 不显示上游地址字段。
- **访问密钥（S4）**：`plugin.codex:codex:manage` 与 `plugin.codex:codex:reset` 可以加入访问密钥的权限；`consume` 会花掉 OpenAI 发放的次数，不可撤销，授给密钥前确认用途。
- 回滚：装回 0.1.3（核心回滚时随之回滚）；会话格式不兼容，进行中的授权需重新发起。

## 12. 0.1.6：默认模型换成上游现有模型（2026-11-17，用户要求）

- 用户反映内置模型和上游对不上，`gpt-6-astra` 请求在账号模型里没有它时被调度拒绝（`model_not_served`，503）。在 ec2pool 测试实例上对 Sign in with ChatGPT 账号获取模型（`GET /v1/models`，`visibility=="list"`）得到：`gpt-5.6-luna`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-astra`、`gpt-6-luna`、`gpt-6-sol`、`gpt-6.1-sol`、`gpt-6.1-terra`。两种账号类型的 `defaultModels` 都换成这 8 个（新到旧），`DefaultTestModel` 由已不在列表里的 `gpt-5.1-codex-mini` 改为最便宜的 `gpt-6-luna`。`codex_device` 没有在线模型列表，沿用同一份；测试实例上没有设备码账号，未单独核实。
- 生图（2026-11-18 改写）：上面这条 400 是已删除的 Sign in with ChatGPT（`chatgpt_siwc`，经 api.openai.com，第 14.3 节）账号的结果，不适用于现在的两种账号类型。Codex 后端的账号（设备码 `codex_device`、授权码 `codex_oauth`）**可以**经 Responses 的 `image_generation` 工具生图：2026-11-18 在设备码账号上实测流式、非流式都拿到图（授权码账号走同一上游，未实测）。上游的生图模型是 `gpt-image-2-codex`（应答 `tools` 回显里给出，请求没写 `model` 时也有）；Free 套餐不能用；生图消耗订阅额度比普通对话快（官方说法 3–5 倍）。生图工具的 token 计入这条请求自己的用量，按客户请求的模型的价格计（CONTRACTS §17.2、§118.10），插件不用改；`gpt-image-2-codex` 只是上游实际用的模型名，不参与计价，不需要价格。已知限制：官方 Codex 客户端 2026-07 起改为调用 Codex 后端的 `/images/generations` 专用接口生图，网关没有这个端点，用这条路径的客户端生图不经过我们。两种账号类型的 `endpoints` 仍只有 `responses`、`responses_websocket`；0.2.0 起经 `adapted` 由核心转换服务 `/v1/images/*`（第 21 节，CONTRACTS §121）。
- 计费为 0 的根因不在插件：上游 SSE 标成 `application/json`，由核心按正文嗅探修复（CONTRACTS §104.1）。

## 13. 0.1.7：用量窗口以响应头为准（2026-11-17，用户要求）

用户要求“以响应头为准，不要乱加兜底”“429 + 响应头判断”。本节取代 §9.2–9.4（以及 §9.1 中按 ±5% 容差定 key、`reset-after-seconds`、`wham/usage` 的内容）。

- 协议事实（openai/codex `codex-rs/codex-api/src/rate_limits.rs`）：`x-codex-{primary,secondary}-used-percent`（百分比）、`-window-minutes`、`-reset-at`（Unix 秒），没有别的。哪个槽位是 5 小时、哪个是周不固定。
- manifest（两种类型，声明相同，`manifest_test` 断言）：`quota` 改为声明这 6 个头，每个槽位一条，用 `windowMinutes` 按窗口时长命名（CONTRACTS §105：300 → `5h`，10080 → `7d`，其他时长为 `<n>d` / `<n>h` / `<n>m`，没有容差；同一响应两个槽位同名时保留用得多的）；`windows` 保留 5 小时 / 周的标签；删掉 `query`、`autoRefresh`。核心的被动采样（CONTRACTS §44.3）在每个 2xx、429 HTTP 响应、每次 WebSocket 握手（101 或被拒的状态）和每个带 `headers` 对象的 WebSocket 错误事件（CONTRACTS §105）上读这些头，所以窗口随每个响应更新；WebSocket 会话里的 `codex.rate_limits` 事件不读。没有了主动查询，控制台点击用量只重新读取快照（用量随账号的下一次请求更新）；核心按旧声明建的查询计划因声明里没有 `query` 而跳过。
- 删除 `wham/usage` 查询：`BuildQuotaRequest`、`ParseQuotaResponse` 及其解析、按时长定 key 的容差、窗口合并、`reset-after-seconds` 处理全部删除，插件不再实现 `pluginsdk.QuotaReader`（SDK 对查询调用回 `Unimplemented`）。`backendHeaders` 只剩重置次数接口在用，移到 `resetcredits.go`。
- 头名只写在 manifest：`main.go` 把嵌入的 manifest 传给 `codex.New(manifestJSON)`，插件读出 `codex_device` 的 `quota.headers`，`ClassifyError` 用这份声明调 `manifest.ReadQuotaHeaders`（与核心采样同一个函数）；manifest 里没有这份声明时插件启动失败。
- `ClassifyError`：
  - 窗口用满：状态 429，且响应头里某个窗口（任何时长都算，不只 5 小时 / 周）用量 ≥100% 并有晚于现在的重置时刻。此时 `FAILOVER`，`automatic_state = DISABLE`、`until_unix` 为这些已满窗口中最晚的重置时刻（限定在 7 天内），冷却到同一时刻作为核心不能自动禁用时的后备，reason `Codex usage limit reached until <时间> (x-codex window reset)`。到点由核心自动恢复（CONTRACTS §69）。
  - 只看响应头：不再读错误体的 `resets_at` / `resets_in_seconds`，不用 `retry-after`，没有 5 分钟默认值。窗口满但没有重置时刻（或重置时刻已过）→ 当作普通 429，原样返回客户端。
  - 删除“错误 type/code 为 `usage_limit_reached`（任何状态码）即算用满”：只有这个错误体、没有满窗口的头 → 原样返回客户端。上游错误码仍作为 `client_error_code` 返回（type `usage_limit_reached` 照旧当作 code）。
  - 不再在 `ClassifyErrorResponse.quota` 里附窗口：429 的头由核心自己采样。
  - 其他 429 → `RETURN_TO_CLIENT`，不冷却、不禁用；其他状态码从不因窗口头禁用。401、403、5xx、传输错误的规则不变。
  - Sign in with ChatGPT（`chatgpt_siwc`）整体删除（同期另一项改动），它的错误规则（`subscription_sharing_*`、`chatpass_v2_*`）随之从 `ClassifyError` 删除。
- 重置次数缓存（§10.6）不再由用量查询写入，只由 `/reset-credits/list` 成功时写入、consume 后删除。
- 重置页（`CodexResetTab`）使用重置后不再请求 `GET /accounts/:id/quota?force=true`（核心不再查询，只会返回仍是 100% 的快照），只重新读取重置列表；`windows_reset > 0` 时在本页把用量标为待更新（不再显示“已用满”），提示“用量会在账号下一次请求后更新”。
- `version` 0.1.7，`hostCompat` `>=0.1.297 <0.2.0`（`windowMinutes` 随核心 0.1.297 发布，旧核心因缺 `key` 拒绝安装）。宿主权限 `accounts.credentials`、`net` 的说明去掉“用量查询”。
- 测试：`quota_test.go`（manifest 的声明经 `ReadQuotaHeaders` 在两种槽位顺序下都得到 `5h` / `7d`；判满：任一窗口满且有未来重置时刻、取最晚、无重置时刻或已过、未满、没有时长头；manifest 缺声明时启动失败）、`classify_test.go`（429 满窗口 → 禁用到最晚重置并冷却到同一时刻；满但无 `reset-at`、未满、没有头、只有 `usage_limit_reached` 错误体 → 原样返回；200 / 503 / 401 带满窗口头不禁用；不附 `quota`）、`manifest_test.go`（两种类型声明相同的 `windowMinutes` 头、没有查询）；核心 `websocket_test.go`（WebSocket 错误事件的 `headers` 交给分类与采样）。

## 14. Sign in with Codex（2026-11-17，用户要求）

用户要三种 ChatGPT 登录方式，随后改为只保留两种：“Device Code”（`codex_device`）与新增的“Sign in with Codex”（`codex_oauth`）；Sign in with ChatGPT（`chatgpt_siwc`）整体删除。插件版本号不在本改动里升（同期另一项改动统一升到 0.1.7）。

### 14.1 协议（与旧版 Sub2API `backend/internal/pkg/openai/oauth.go` 及其服务代码一致）

- Codex CLI 的浏览器登录：公开客户端 `client_id = app_EMoamEEZ73f0CkXaXp7hrann`（与设备码相同），授权码 + PKCE（S256），没有动态客户端注册。
- 授权地址 `https://auth.openai.com/oauth/authorize`，参数：`client_id`、`response_type=code`、`redirect_uri=http://localhost:1455/auth/callback`（Codex 客户端登记的回调，必须是 `localhost`）、`scope=openid profile email offline_access`、`state`、`code_challenge_method=S256`、`code_challenge`，以及 Codex CLI 同样带的 `id_token_add_organizations=true`（id_token 带上工作区信息）、`codex_cli_simplified_flow=true`（只走 ChatGPT 授权页，不进入 API 组织设置）。旧版还会发的 `originator` 等 Codex 标识不发。
- 服务器不在本地：浏览器最后跳到 `localhost:1455/auth/callback?...` 打不开，运营者把地址栏完整网址粘回表单（解析与原 SIWC 相同：完整网址、无 scheme、只有查询串、片段都接受）。
- 换令牌：`POST https://auth.openai.com/oauth/token`，form：`grant_type=authorization_code`、`code`、`redirect_uri`（同上）、`client_id`、`code_verifier`。与设备码登录的最后一步完全相同（只有 `redirect_uri` 不同），代码共用 `redeemCode`。
- 得到的令牌与设备码登录相同：上游 `chatgpt.com/backend-api/codex/responses`、`chatgpt-account-id` 取自 id_token、刷新用同一个 Codex 客户端（`POST /oauth/token` JSON，scope `openid profile email`，`refresh.go`）、`x-codex-*` 用量头、429 处理、官方重置次数都一样。插件里两种类型没有任何分支，只在 `knownType` 里一起列出。

### 14.2 插件改动

- manifest：新账号类型 `codex_oauth`（`authMethodLabel` “Codex”（0.1.8 起，原 “Sign in with Codex”），表单、敏感字段、设置字段、`refresh`、默认模型、`requestFields`、`streamOnly` 与 `codex_device` 相同）；额度用 `quota.headers` 按 `windowMinutes` 从每次应答的 `x-codex-primary-*` / `x-codex-secondary-*` 头命名窗口（CONTRACTS §105），窗口标签与 `codex_device` 相同；`codex_device` 的 `authMethodLabel` 改为 “Device Code”（中文 0.1.8 起为“设备码”）。路由 `POST /auth/codex/start`、`POST /auth/codex/exchange`（`scope: admin`，`codex:manage`）。重置次数的两个 UI 插槽与行操作的 `accountTypes` 加上 `codex_oauth`。宿主权限说明改为设备码与 Sign in with Codex。
- 会话：KV namespace `codex_session`，`{state, code_verifier}`，30 分钟，绑定发起人（`SaveCallerSession`，他人或过期都回 404 `session_expired`）。exchange 的错误码沿用原 SIWC：`invalid_callback`、`state_mismatch`、`access_denied`（结束会话）、`authorization_failed`（400），`session_expired`（404），`upstream_unavailable`、`token_exchange_failed`（502）。
- 表单：`SiwcFlow.vue` 改为 `OAuthFlow.vue`（生成链接 → 新窗口打开 → 粘贴 `localhost:1455` 回调网址 → 换取），重新授权走同一流程；文案 `oauth.*`。

### 14.3 删除 Sign in with ChatGPT（`chatgpt_siwc`）

- 删掉账号类型、`/auth/siwc/*` 路由、动态客户端注册（`dynamic_agent_client`、`ext_agent_host_id`、`resource`、`nonce`、`chatgpt.tokens.use.direct` scope 检查）、`api.openai.com` 上游与模型列表（`BuildModelsRequest` 不再实现，两种类型都用 manifest 默认模型）、SIWC 的刷新分支、凭证字段 `client_id` / `ext_agent_host_id` / `scopes`、`externalServices` 里的 `api.openai.com`。上文 §2.2、§4 中 SIWC 的路由与凭证、§6–§9 中 SIWC 的行为、§12 中 SIWC 的模型核实都只是历史记录。`classify.go` 里 SIWC 的错误码规则由同期另一项改动删除。
- 不留兼容代码：已有的 `chatgpt_siwc` 账号必须在升级前手动删除；升级后插件不认识该类型（`ValidateCredentials` 回 `account_type unsupported`，上游与刷新回 `FailedPrecondition` / `InvalidArgument`）。

### 14.4 测试

- `oauth_test.go`：start 的授权地址参数与 KV 会话（verifier 对得上 challenge、绑定用户 1）、exchange 成功（换令牌表单、凭证与设备码相同）、会话用后即删、只粘查询串、state 不符 / access_denied / 其他错误 / 无 code / 空、错误后会话保留与拒绝后删除、令牌端点拒绝 / 无 access_token / 连不上；他人会话。`upstream_test.go`、`refresh_test.go`、`resetcredits_test.go`：`codex_oauth` 与 `codex_device` 的上游请求、刷新、重置次数相同；无模型列表。`manifest_test.go`：两个类型、标签、`codex_oauth` 的额度头、路由、插槽与行操作覆盖两种类型、`externalServices`。UI：`CodexAuthForm.spec.ts`（Sign in with Codex 流程、编辑与重新授权）、`flow.spec.ts`、`messages.spec.ts`。

## 15. 0.1.9：设备码授权引导与复制验证页链接（2026-11-17，用户要求）

- 设备码流程（`DeviceFlow.vue`）在说明下方常驻一条提示：设备码要先在 ChatGPT 设置 → 账户安全与登录（Security and login）→ 应用安全（App security）里打开“为 Codex、Excel、PowerPoint 和 Word 启用设备代码登录”（Enable device code sign-in for Codex, Excel, PowerPoint, and Word），企业工作区需管理员允许；链接 `CHATGPT_SECURITY_URL`（`flow.ts`，`https://chatgpt.com/settings/security`）只是页面上的外链，插件不请求它，不进 `externalServices`。原先只在失败时出现的 `device.notEnabled` 提示删除。
- 验证页面链接旁加图标复制按钮（`SCopyButton`，与 Sign in with Codex 的授权链接同样式）。
- 中文账号类型名称在“·”前补空格，与控制台去重插件名前缀的规则（`插件名 + ' · '`）一致。

## 16. 0.1.10：按会话固定上游提示缓存（2026-11-17，用户要求）

- 现象：ec2pool 上 Codex CLI 的同一会话 22 条请求全走同一账号、粘性全部命中，缓存读却只有 0、5632（系统提示与工具）或停在约 15.6 万（早先某轮的长度），账号当日命中率 26.7%。判断是 Codex 后端按会话把提示缓存放在某台机器上，请求不带会话时每轮随机落点。插件原先不转发任何客户端头、也不发会话头；旧 sub2api 对 Codex 账号用 `prompt_cache_key` 按 API Key 隔离后生成 `session_id` / `conversation_id`。这是从数据推断的，上线后以命中率对比确认。
- 做法：manifest 两个账号类型的 `requestFields` 加 `prompt_cache_key`。`BuildUpstreamRequest`（HTTP 与 WebSocket 握手）在它是非空字符串时加头 `session_id` 和 `conversation_id`，值相同：`sha256("<api_key_id>:<prompt_cache_key>")` 取前 16 字节排成 UUID（version 8）。同一 Key 的同一会话每轮相同；不同 Key 即使 `prompt_cache_key` 相同也不同；不回显客户端原值。没有该字段、空串或非字符串时不发。客户端自己的 `session_id` 等头仍不转发。
- 会话键与核心内置粘性规则 `openai-prompt-cache-key` 同源，所以同一会话既固定账号，也固定上游缓存。这两个头只是缓存路由，不是客户端标识：`originator`、`user-agent` 仍如实写 sup2api，不发 `x-codex-*`。
- 测试：`upstream_test.go` 的 `TestBuildUpstreamRequestSession`（HTTP/WS 同值、同 Key 同会话稳定、换 Key 或换会话不同、不含原值、不用客户端头、缺失/空串/非字符串不发）；`manifest_test.go` 的 `requestFields` 与 gjson 查询。

## 17. 0.1.11：默认模型加上 gpt-6.2（2026-11-18，用户要求）

- 两种账号类型的 `defaultModels` 最前面加 `gpt-6.2-astra`、`gpt-6.2-sol`（新到旧，排在 `gpt-6.1-sol` 前），其余不变；openai 0.4.3 的 API Key 账号默认模型同样加在最前。价格不在插件里，未改动。
- 依据：ec2pool 测试实例上两个 `codex_device` 账号（id 2、3）已由管理员手动加上这两个模型；经临时 Key 各发一次流式 `/v1/responses`，上游都正常完成（`response.completed`，`model` 分别为 `gpt-6.2-astra`、`gpt-6.2-sol`）。设备码账号没有在线模型列表，`gpt-6.2` 其他后缀（如 luna、terra）在测试实例与线上都没有价格、也没有账号用到，未加。
- 已有账号的模型列表不变，要用新模型请在账号上手动添加。`DefaultTestModel` 仍为 `gpt-6-luna`。

## 18. 0.2.0：转发 V2（2026-11-18，需核心 ≥0.1.304；开发中编号 0.1.12，未发布）

核心与全部平台插件改成一条请求路径（CONTRACTS §112），codex 随之改写，行为对客户端不变：

- 插件拿到完整请求体，在 `BuildUpstream` 里用 `sdk/pluginsdk/jsonedit` 改写（第 4 节列的几项），交回最终字节；插件不写 `Execute`，SDK 用 `ExecuteDefault` 跑它。原来的“清单声明请求字段 + 补丁”、每请求最多 256 个 system → developer 改写的上限、字符串 input 超过插件 gRPC 接收上限（4 MiB）就失败的限制都没有了。
- 清单删掉 `platform.execute.v1` 和两个账号类型的 `requestFields`；`session_id` / `conversation_id` 改从请求体的 `prompt_cache_key` 算（算法不变）。
- 不再写执行记录：以前每个 codex 请求在 `plugin_executions` 写三次、插件崩溃会切断在途的流；现在核心在 `Execute` 返回后才发请求，插件崩溃或重启不影响在途的流。
- 插件的头全部保持权威头，运营方的请求头透传和覆盖改不了它们（与改写前相同）。
- 两个账号类型在清单里写 `"passHeaders": []`：核心不交给插件任何客户端请求头（插件本来就丢弃它们、自己写出发送的每个头）。以前没写这个字段，核心把 openai 平台的整份列表交给插件；中间版本只列 `user-agent`。上游收到的请求没有变化。
- 测试：`internal/codex/execute_test.go`（两种类型、HTTP 与 WebSocket 的改写，经假宿主的一次中继），`upstream_test.go`（会话头），`TestConformance` 调一致性套件（两种类型的 HTTP 请求、WebSocket 一轮，以及核心对只流式类型发出的非流式形式：`meta.stream` 为假、请求体的 `stream` 为真）。`authorization`、`chatgpt-account-id` 是凭证头，其余头按已发布插件的约定声明为权威头；11 条全部通过，没有例外。

## 19. 0.2.0：授权请求走账号表单选的代理（2026-11-18，所有者要求“账号授权时的第一次换令牌不走代理：修复bug”，需核心 ≥0.1.304、UI 3.20）

通用机制见 CONTRACTS §117 与 [DESIGN-2026-11-18-ACCOUNT-PROXY-TICKET.md](DESIGN-2026-11-18-ACCOUNT-PROXY-TICKET.md)。

- 现象：账号配了代理（本机 IP 到 OpenAI 不通或会被风控），但设备码申请、轮询和两种登录的换令牌是插件用自己的 `http.Client` 直连 `auth.openai.com`，从服务器本机 IP 出去。账号建好后的上游、刷新（`BuildRefreshRequest`，核心按账号 `proxy_id` 发）、重置次数（`SendAccountRequest`）一直走账号代理，不受影响；vertex 的刷新同样由核心经账号代理发。
- 现在：`CodexAuthForm` 收到编辑器的 `proxyTicket`，两种登录的每次调用（开始、每次轮询、换令牌）之前都调它现取一张票据（表单此刻的代理选择：已有代理、粘贴的代理串、不用代理）放进请求体；插件用这次调用的票据（`pluginsdk.RouteProxyTicket`），授权会话里不存票据。四类请求（`/api/accounts/deviceauth/usercode`、`/api/accounts/deviceauth/token`、两种登录的 `/oauth/token`）都改成 `pluginsdk.Fetch` 带这次调用的票据，核心解开票据、确认是同一个管理员、复查他能用这个代理后从代理发出。授权开始后改了代理，不中断、不用重新授权，下一次轮询或换令牌就走新代理（2026-11-19 所有者：“代理修改了，后面的请求都用新的代理走就行了呀，重新授权干嘛呢？”；最初的做法是开始时取一张存进会话，见 CONTRACTS §117.2）。
- 发给 OpenAI 的内容不变：方法 POST、路径、请求头（`Content-Type`、`Accept: application/json`、`User-Agent: sup2api-codex/<版本>`、`Originator: sup2api`）和请求体逐字节相同（`oauth_proxy_test.go` 钉住），变的只有出口。`User-Agent` 里的版本取自代码常量 `Version`（与清单一致），所以相对线上的 0.1.11，0.2.0 发出的授权、上游、刷新、重置次数请求都带 `User-Agent: sup2api-codex/0.2.0`（原为 `sup2api-codex/0.1.11`），这是版本号本身的变化。
- 错误：传输失败（核心发了但连不上）与以前一样：开始、换令牌回 502 `upstream_unavailable`，轮询回 `pending` 下次再问。核心拒绝发送（票据不是这个管理员的、权限被收回、代理被删）是新的 400 `proxy_refused`（开始、换令牌），轮询回 `pending`，`message` 是拒绝原因（会话照常按 `expires_at` 过期）。请求没有到 OpenAI，设备码、授权码都没用掉，所以**会话保留**：代理改好后，下一次轮询或再提交一次回调就带着新票据发出；只有设备码登录批准之后的换令牌例外（会话已随批准删除），轮询回 `error` 并提示检查代理后重新开始（§4）。界面：换令牌的 `proxy_refused` 显示为普通错误（不提示重新生成链接），再点“完成”即可；设备码界面把 `pending` 带的 `message`（核心拒绝的原因、暂时连不上 OpenAI）显示在等待区的提示里并照常轮询，下一次没有 `message` 的 `pending` 清掉提示；每次轮询前取票据时表单代理不可用（取票据抛错），同样把原因显示为提示、按原节奏继续轮询，不计入连续失败次数（这是表单代理的问题，不是网络抖动），代理改好后同一个设备码继续完成；轮询照旧在设备码到期时停止（2026-11-19 审查）。OpenAI 的回答（设备码过期、授权码无效等）照旧处理。
- 删除：插件自己的 `http.Client`、20 秒 `authTimeout`、1 MiB 读上限；清单的 `net`（沙箱不再让插件联网）。轮询锁的 ttl 原来写成 `2*authTimeout+10s`，改成同值的 50 秒。
- 清单：`hostUICompat` `^3.20`（`proxyTicket`；同一批里界面契约升到 4.0，现为 `^4.0`），`hostCompat` 仍是 `>=0.1.304 <0.2.0`（同批核心），版本是未发布的 0.2.0（与第 18 节同一个版本）。
- 测试：`oauth_proxy_test.go`（四类请求都是带票据的取回、方法 URL 请求头请求体与以前相同、到达 OpenAI 的头没有代理的东西；开始、轮询、换令牌各用不同的票据，每个请求带的是自己那次调用的；缺票据、没有调用者、核心拒绝时开始/换令牌的 `proxy_refused` 与轮询带原因的 `pending`，以及拒绝后会话仍在、改好后同一会话完成）、`oauth_test.go`（原有流程改为经假宿主的取回）、`manifest_test.go`（没有 `net`）；界面 `CodexAuthForm.spec.ts`（开始时要票据并放进请求体、要不到时显示原因且不发请求；开始后换一张票据，轮询和换令牌的请求体带新的；换令牌的 `proxy_refused` 不提示重新生成链接；轮询的 `pending` 带消息时显示提示并继续轮询、之后完成；轮询前取票据抛错时显示提示、不进 `error`、之后成功）。

## 20. 0.2.0：上游会话 id 以账号 id 加盐；User-Agent 与 originator 可自定义（2026-11-18/19，审计结论，所有者决定；无迁移，版本仍是未发布的 0.2.0）

审计发现两点，所有者的决定：会话 id“加上盐进行适配”，盐“直接根据账号的 id 来做”；User-Agent / originator“这个不管，但是账号上可以自定义，然后插件的设置中也可以自定义”。

### 20.1 会话 id 以账号 id 加盐

- 问题：`session_id` / `conversation_id` 原来是 `sha256("<api_key_id>:<prompt_cache_key>")`，而 `prompt_cache_key` 原样在请求体里发给上游；上游枚举小整数就能反推出我们内部的 API Key 编号。
- 所有者决定“直接根据账号的 id 来做盐”（2026-11-19；此前的集群随机盐与以邮箱做盐两种方案都没有采用）。现在：`sha256("<账号 id>:<api_key_id>:<prompt_cache_key>")`，取前 16 字节排成 UUID（version 8）。账号 id 是核心里这个账号的编号（`ExecuteRequest.account.id`，HTTP 与 WebSocket 都由 `BuildUpstream` 从同一处读），不是发给上游的 `chatgpt_account_id`；两个编号都是十进制整数，用 `:` 隔开，各部分不会混淆。账号 id 只作哈希输入，不出现在请求里。没有可用的 `prompt_cache_key` 时照旧不发。
- 结果：同一账号上同一 Key 的同一会话每轮相同；**标识随账号不同**，同一个客户 Key 的同一会话换到另一个 ChatGPT 账号时（换号、粘性失效）标识会变，新账号上的上游提示缓存从头开始；升级后每个会话的标识变一次。
- 局限（照实写）：账号 id 和 API Key 编号都是小整数，上游若知道算法，同时枚举两者仍能对上，盐只是把要枚举的范围扩大到两个编号的组合。

### 20.2 User-Agent 与 originator

- 取值（每个头各自）：账号凭证里的 `user_agent` / `originator` → 插件设置的 `user_agent` / `originator` → 内置 `sup2api-codex/<版本>` / `sup2api`。空字符串（去掉首尾空白后）表示不自定义，沿用上一级。只在 `agent.go` 一处解析（`Plugin.agent`），用在账号建好后发往 OpenAI 的全部请求：转发（HTTP 与 WebSocket 握手）、账号测试（`BuildTestRequest`）、令牌刷新（`BuildRefreshRequest`）、重置次数（`SendAccountRequest`）。
- 授权请求（设备码申请与轮询、两种登录的换令牌）用账号表单此刻填的值（2026-11-19 审查判定必须改，原先只用插件设置的值）：授权时账号还没保存，值只在表单里，所以和代理票据一样，界面每次调用授权路由都把表单当前的 `user_agent`、`originator` 放进请求体（`host.ts`，`flow.ts` 的 `agentOf`），插件在这次调用里按 `Plugin.agent(credentials{UserAgent, Originator})` 解析（`oauth.go` 的 `readCall` / `routeCall`），不存进会话；授权中途改了这两个值，下一次轮询或换令牌就用新值。表单留空时照常落到插件设置和内置值。新建和编辑已有账号时的重新授权相同。路由里的校验复用 `ValidateCredentials` 对这两个字段的检查（`credMap.agentError`），没有另加限制。
- 账号上：凭证的两个普通字段（不在 `sensitiveFields`，编辑时原样显示；不在 `settingsFields`，所以和令牌一起进 `credentials_json`，刷新、测试、重置读到的都是它）。`ValidateCredentials` 只做格式校验：必须是字符串、不能含控制字符（水平制表符除外，与发送前的检查相同）。原生表单 `CodexAuthForm` 多一块“客户端标识”，两个输入框，新建和编辑都有；重新授权保留它们（`mergeCredentials` 只替换令牌字段，`withAgent` 在“重新授权”撤回时保留表单里改过的值）。原生表单的凭证控制台不会自动渲染，所以这两个输入框写在插件的表单里；账号的 `extensionsSchema` 控制台没有渲染（没有组件读它），值存进账号设置、不进 `credentials_json`（`account/creds.go` 把扩展键从凭证里拿掉），所以没有用它。
- 插件设置：清单 `ui.settings`（`mode: "native"`，组件 `CodexSettings`，在插件详情页的“设置”标签里显示；没有密钥，`secrets` 写 `[]`（不写则核心全部遮蔽））；两个字符串，默认空，界面规则只拒绝控制字符（水平制表符除外）。保存前核心调用插件的 `ValidateSettings`，与 `Configure` 共用 `checkSettings`（`agent.go`）：只接受这两个键（其他键报 `unknown`），值必须是字符串，去掉首尾空白后按账号的同名字段检查（`credMap.agentError`）。去掉空白改变了某个值时 `ValidateSettings` 交回裁剪后的设置，核心存的就是它（例如提交 `"UA/1\n"` 存 `"UA/1"`），与 `ValidateCredentials` 交回规范化的凭证一致。`Configure` 同样去掉首尾空白后保存在进程里，各节点各自收到。（2026-11-19 前是 schema 表单 `forms/settings.*.json`。）
- 现有机制查过，不能直接满足：账号请求头覆盖（CONTRACTS §108）和参数覆盖（§84）只作用在转发上，而 codex 的头全部是权威头（§112.4，所有者定的例外），覆盖改不了；刷新、测试、重置次数、授权这些请求核心按插件交回的头原样发送，不套覆盖。
- 内置默认值、请求头黑名单没有改。

### 20.3 测试

`execute_test.go`（会话头按账号 id 计算：同一账号同输入稳定、另一个账号 id 结果不同、与不加盐的旧值不同、请求的头、体、URL 都不含账号 id）、`agent_test.go`（六种组合下转发 HTTP 与 WebSocket、测试、刷新、重置次数的两个头；授权的设备码申请、轮询、两种登录的换令牌用路由调用带的表单值，六种组合同样成立；表单值不是字符串或含控制字符时四个路由都回 400 且不发请求；设置不是对象时拒绝）、`upstream_test.go`（两个字段的格式校验）；界面 `CodexAuthForm.spec.ts`（编辑时显示并写回凭证、新建时就有；两种登录的每次调用都带表单当前的值，中途改了下一次调用带新值；重新授权带账号已有的值）、`flow.spec.ts`（`withAgent`、`agentOf`、重新授权保留）。

## 21. 0.2.0：经核心转换服务 OpenAI 生图接口（2026-11-19，所有者要求“Codex 要兼容 OpenAI 的生图协议去转成 response 生图”；需核心 ≥0.1.304；版本仍是未发布的 0.2.0；设计见 [DESIGN-2026-11-18-CODEX-IMAGES.md](DESIGN-2026-11-18-CODEX-IMAGES.md)，契约见 CONTRACTS §121）

### 21.1 清单

- 两种类型的 `openai` 平台加 `adapted`：`chat_completions: {}`（chat 适配从核心的隐式规则改为显式声明，CONTRACTS §83.3）、`images_generations` 与 `images_edits` 都是 `{"model": "gpt-6-luna"}`（驱动 `image_generation` 工具的对话模型，插件常量 `ImageChatModel`，与清单同值，清单测试钉住）。
- `defaultModels` 末尾只加 `gpt-image-2`：Codex 后端只有一个生图模型 `gpt-image-2-codex`，工具里写什么名字都按它出图；不要把 2.5、2.6 等名字加进 codex 账号。
- 描述里写明经核心转换提供 `/v1/images/generations`、`/v1/images/edits`，生图模型写 `gpt-image-2`。

### 21.2 请求与上报的模型

核心把生图请求转成 Responses（顶层 `model` 是驱动模型，客户端模型在 `tools[0].model`，CONTRACTS §121.2）后交给插件，`BuildUpstream` 照常改写（`store: false`、补 `instructions`、删不支持字段）。`meta.client_protocol` 是 `openai.images` 或 `openai.images_edit` 时插件不上报上游模型（`upstreamModel` 返回空串）：请求体顶层的 `model` 是驱动模型，上报了会覆盖核心从应答读到的回显生图模型（`gpt-image-2-codex`）；使用记录的上游模型按所有者“上游返回什么就是什么”记回显（CONTRACTS §121.4）。

### 21.3 账号测试（CONTRACTS §122.3）

`BuildTestRequest` 取 `protocols` 里第一个 `openai.responses`、`openai.chat`、`openai.images`：前两者发原来的流式 `ping`；`openai.images`（核心只在测试带了模型名时才给，排在原生协议之后，CONTRACTS §122.2）发转换后的最小生图请求——顶层 `ImageChatModel`，`tools: [{"type":"image_generation","model":<被测模型>,"output_format":"png","quality":"low"}]`，`tool_choice` 强制工具，一条用户消息“a small white dot”，`store: false`、`stream: true`；`usage_protocol` 报 `openai.responses`，`model` 报被测模型。会真实出一张图、消耗订阅额度。空列表发 `ping`；列表不空而没有这三个协议时回 `FailedPrecondition`（与 `standard` 库相同）。

### 21.4 测试

`manifest_test.go`（`adapted` 与唯一的生图默认模型）、`execute_test.go` `TestExecuteImagesReportNoModel`（两个生图客户端协议不上报模型、请求体改写照旧）、`upstream_test.go` `TestBuildTestRequestByProtocol`（chat 测 Responses、生图测试的请求体、没有可测协议）。

## 22. 账号的全部信息随调用下发，删掉账号授权（2026-11-19，所有者要求“调度的那个账号 让插件执行，就给哪个账号”“旧的两个权限和旧接口：移除”；需核心 ≥0.1.304；版本仍是未发布的 0.2.0；契约见 CONTRACTS §124）

- 清单删掉 `accounts.credentials`：插件收到自己账号类型的账号（凭证、代理）不需要授权。
- 重置次数的路由读账号改用 `Host.Account`（`HostService.GetAccount`，每次一条审计 `plugin.account.read`），取代 `GetAccountCredentials`；请求照旧经 `SendAccountRequest` 由核心经账号代理发出，插件不联网。
- 删掉两个账号类型的 `settingsFields: ["proxy_id"]`：代理是账号的 `proxy_id` 列，Go 代码和界面都没有写入它；`flow.spec.ts` 的样例改用别的普通键。
- 测试：`resetcredits_test.go` 的假宿主改实现 `Account`；`manifest_test.go` 断言没有 `accounts.credentials`、没有 `settingsFields`。

