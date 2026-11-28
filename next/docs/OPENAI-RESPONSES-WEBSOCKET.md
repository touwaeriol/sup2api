# OpenAI Responses WebSocket 模式

日期：2026-10-02。状态：设计与实现同轮完成，验收见 [验证记录](audits/2026-10-02/MULTINODE-VALIDATION.md) 第 14 节。

旧版 sub2api 支持 Codex 等客户端使用的 Responses WebSocket 模式（`GET /v1/responses` 升级为 WebSocket，客户端逐轮发送 `response.create`，服务端逐条推送 Responses 事件）。next 之前没有任何 WebSocket 接口，核心与插件路由都会丢弃 `Upgrade` 头。本文把这一模式移植到 next：核心提供通用的 WebSocket 端点类型，openai 插件为其 API Key 账号构造上游 WebSocket 请求。

## 1. 范围

移植的是协议本身和计费、调度语义，不照搬旧版实现。旧版约 3.5 万行（含测试）大量处理 ChatGPT OAuth/Codex 账号、连接池与预热、`store` 恢复、HTTP 桥接回退、会话抢占等。next 的 openai 插件只有 API Key 账号，这些机制不在本次范围：

| 旧版机制 | 本次 |
|---|---|
| 客户端 `GET /v1/responses` 升级，逐轮 `response.create` | 支持 |
| 上游 `wss://<base>/v1/responses`，`OpenAI-Beta: responses_websockets=2026-02-06` | 支持（API Key 账号） |
| 每个客户端连接固定一条上游连接、一个账号（`previous_response_id` 依赖同一连接） | 支持 |
| 每轮独立计费、使用记录、限额 | 支持 |
| 首条消息超时 30 s、轮间空闲 300 s、每个 API Key 最多 64 条连接 | 2026-11-18 起每个 API Key 的连接数不设上限（此前 64）；2026-11-08 起不设空闲超时，会话保持到客户端断开，双向每 30 s 发 ping 保活，ping 30 s 无响应视为断开 |
| 建连失败按插件分类换号 | 支持 |
| ChatGPT OAuth / Codex 账号、`x-codex-*` 头处理 | 不移植：next 尚无此账号类型 |
| 上游连接池、预热、`generate=false` | 不移植 |
| 连接中断后的 `previous_response_id` 恢复、HTTP 桥接回退 | 不移植：上游断线时核心保留客户端连接，下一轮新建上游连接（2026-11-28 起，CONTRACTS §142；此前关闭客户端连接），由客户端决定是否重发 |
| 不带 `/v1` 的 `/responses` 别名 | 不移植：next 的内置端点都带 `/v1` |

## 2. 职责划分

沿用“插件陈述事实，核心决定并落账”：

- **核心（内置 openai 平台）** 声明端点 `GET /v1/responses`，`kind: "websocket"`，协议 `openai.responses_ws`。核心负责鉴权、按轮的模型白名单、钩子、价格与余额、账号调度与粘性、并发与限额、建立上游连接（账号代理、SSRF 检查）、双向转发、按声明式规则读取用量、写使用记录与扣费、节点排空。
- **openai 插件**（0.3.0 起）声明能力 `platform.websocket.v1`，表示它的 `Execute` 能为本平台 WebSocket 协议的每一轮给出握手和这一轮的消息（转发 V2，CONTRACTS §83.6、§112）。对 `openai.responses_ws` 给出 `GET ws(s)://<base_url>/v1/responses`、鉴权头（权威头）和 `OpenAI-Beta`（默认头）；建连失败时照常由 `ClassifyError` 分类。插件不参与逐条事件转发，也不读用量：Responses 的事件与 SSE 的 `data` 完全相同，内置平台已有的 `response.completed` / `response.incomplete` 规则直接适用。
- 只有声明了 `platform.websocket.v1` 的插件的账号类型才会被调度到 WebSocket 端点。旧版本插件或其他平台插件的账号不受影响，也不会收到它们不认识的协议。
- WebSocket 端点只走原生路由，不做协议转换。

## 3. 清单约定（SDK）

- `Endpoint.Kind` 增加 `"websocket"`（原先只允许 `"proxy"`）。WebSocket 端点必须是 `GET`；`response.stream` 必须为 `"websocket"`，不能有 `response.nonStream`、`request.streamPath`、`request.stream`、`usageSource: "plugin"`、`task`；`request.modelPath` 必填，表示每条客户端消息里模型的位置；按轮计费，`billing: "usage"` 时必须有非空的 `usage.sse` 规则（规则按消息的 `type` 匹配，与 SSE 无 `event:` 行时相同）。
- 新能力 `platform.websocket.v1`，需要同时声明 `platform.adapter.v1`。
- 内置 openai 平台的粘性规则 `openai-prompt-cache-key` 加入 `openai.responses_ws`，按首轮的 `prompt_cache_key` 选择账号。

## 4. 会话流程

1. **升级前**：节点健康检查、API Key 鉴权、每个 API Key 的连接数（集群范围，Redis 并发位 `ws:<key id>`，随连接释放；不设上限，2026-11-18 前上限 64，第 65 条回 429；仍然计数，Redis 出错时照样拒绝升级，不放进没计数的连接）。失败直接返回 HTTP 错误（OpenAI 错误格式），不升级。
2. **升级**：接受 WebSocket（支持 permessage-deflate），客户端单条消息上限取网关设置的请求体上限 `max_request_body_mb`（默认 128 MB，CONTRACTS §111）。不限制首条消息的等待时间（2026-11-08 前为 30 s）。
3. **每一轮**：客户端发送 `{"type":"response.create", ...}`。其他类型、非法 JSON 或缺少模型时回一条错误事件，连接保持。一轮进行中再次发送 `response.create` 时回错误事件（上游同一连接一次只处理一个响应）。每轮依次执行：
   - 新的请求 ID（使用记录与账本的幂等键），读模型，校验分组模型白名单；
   - 占 API Key 并发位并检查 RPM/TPM（CONTRACTS §60）；
   - 运行网关钩子（可拒绝或改写本轮消息）；
   - 解析价格、检查余额；
   - **首轮**：按 HTTP 请求相同的规则选账号（候选、粘性、排序插件、限额、账号并发位），调用插件的 `Execute`（这一轮的消息、握手地址与握手头），建立上游连接（握手没有固定时限，2026-11-18 前是 30 s；握手随客户端离开而结束）；建连失败交给插件 `ClassifyError`，按结果冷却/禁用账号并在 `max_attempts` 内换号（换号时解除会话粘在这个账号上的绑定，与 HTTP 相同）；发布了失败策略时由策略决定原账号重试、换号和出口约束，与 HTTP 尝试相同（客户端只收到了协议升级，不算已提交的响应）；插件给出可重试的 `return_error`（每个账号都不跳过重试）时和 HTTP 一样把这个错误转给客户端（此前变成 503 `no_available_account`）；
   - **后续轮次**：沿用首轮的账号与上游连接；账号并发位或限额暂时不可用时等待最多 10 s，仍不可用则回 429 错误事件；账号已不能服务本轮模型时回错误事件；
   - 套用账号的模型映射和与 HTTP 相同的改写，交给插件的 `Execute`，把插件交回的消息发往上游（首轮用握手那次调用交回的消息；后续轮每轮调用一次，只用交回的消息，忽略 URL 与头；调用失败时回错误事件、连接保持。见 CONTRACTS §83.6）；
   - 逐条转发上游事件，按 `usage.sse` 规则累计；上游单条消息不限大小（2026-11-18 前超过 64 MiB 关闭），写客户端、写上游都没有时限（此前 1 分钟），不读的一方由下面的心跳 ping 结束；遇到 `response.completed`、`response.incomplete`、`response.failed` 或 `error` 事件即结束本轮；
   - 结束时释放 API Key 与账号并发位，结算账号和 Key 的 token 预占（CONTRACTS §78.3），写一条使用记录（`protocol=openai.responses_ws`、`stream=true`），由结算服务扣费。
4. **上游错误事件**：带 HTTP 状态的 `error` 事件（例如限流）交给插件 `ClassifyError`，冷却或禁用账号；本轮记为失败，连接保持，下一轮仍用该连接，由上游决定。
5. **结束**：不因空闲关闭（2026-11-08 前轮间空闲 300 s 以 1000 关闭），会话保持到客户端断开；核心每 30 s 向客户端和上游各发一次 ping，30 s 内没收到 pong 就关闭该连接（客户端侧按断开处理，上游侧按上游断开处理）；ping 由每条连接自己的协程发出，不经会话循环，所以会话循环正阻塞在写给一个不再读取的对端时照样生效；ping 要等正在写的那条消息写完才发得出去，所以这 30 s 也是对端读完一条消息余下部分的最长时间（ping 每 30 s 一次，一条消息实际可以写 30 到 60 s；2026-11-18 前是每次写 1 分钟）；客户端断开时取消进行中的一轮并记为客户端取消；上游连接断开时只丢掉上游连接（2026-11-28 起，CONTRACTS §142；此前以 1011 关闭客户端连接）：进行中的一轮记为上游失败并向客户端发 502 错误事件，下一轮像首轮一样重新选账号、建连。

## 5. 多节点与排空

- 会话固定在建立它的节点上。外壳转发、节点密钥轮换、Redis 短断、CPU 保护对已建立会话的影响已由第 13 节的外壳测试覆盖：已建立的会话不迁移、不中断，新会话按当前路由分配。核心层同样不因 Redis 短断关闭连接：并发位续期出错或丢失都不影响进行中的一轮（CONTRACTS §78.5）。一轮内上游静默超过流式空闲超时（默认 180 秒）时以 1013 关闭。首轮账号全满时不排队，直接返回错误。
- 节点排空（升级、准入撤销、关停）时，核心停止接受新会话；空闲会话立即以 1012（Service Restart）关闭；进行中的一轮最多再给 30 s，之后关闭。客户端重连后由外壳路由到仍在服务的节点。`http.Server.Shutdown` 不跟踪已升级的连接，所以会话自己观察排空信号，处理器返回后核心的请求屏障才能完成。

## 6. 计费与记录

每轮一条使用记录与一次扣费，与 HTTP 流式请求相同：价格按本轮模型，`inclusive` 语义，缓存读取 token 单独计价。客户端在上游开始应答之后中途断开的一轮记为客户端取消（499），按预扣的估算计费（WebSocket 不续读，按 `precharge` 处理，CONTRACTS §25.8）；上游应答完却没给出用量的一轮记零费用的“未上报”记录（`usage_unreported`）并记 ERROR。插件的 `Execute` 每轮只构造请求、不等待地中继并返回 `host_usage`；WebSocket 端点不能由插件解析用量，用量按核心的声明式规则读取，由结算服务扣费。

## 7. 验收

- SDK：清单校验接受合法的 WebSocket 端点并拒绝各类非法组合；内置平台通过同一校验。
- 插件：`openai.responses_ws` 生成 `wss://api.openai.com/v1/responses`，自定义 `base_url`（含 http）转换为 ws(s)，头部带鉴权与 `OpenAI-Beta`。
- 网关单元测试（真实 WebSocket，模拟上游）：多轮共用一条上游连接且逐轮记录用量；首轮建连 401 换号并禁用账号；白名单外模型、余额不足按轮报错不断开；轮内重复 `response.create` 被拒；上游限流事件冷却账号；客户端断开记为取消；空闲超时；排空时空闲会话 1012 关闭、进行中一轮完成后关闭；每 Key 连接上限。
- 真实三节点：经从节点入口建立会话，多轮对话由该节点核心经模拟上游完成，PG 中逐轮有使用记录与扣费；首个账号建连被拒后换号并被禁用；经转发节点建立的会话由主节点核心处理；排空节点核心时会话以 1012 关闭，客户端经其他节点重连后继续。
