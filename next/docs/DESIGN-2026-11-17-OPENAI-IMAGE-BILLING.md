# OpenAI 生图模型的接入与计费（设计稿，待用户确认）

> 2026-11-18：3.1、3.2 已由 [DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md](DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md) 3.4 取代并实现（CONTRACTS §118：edits 只收 JSON 形式，multipart 以后做）；3.3、3.4 也已落地（CONTRACTS §17.2、§118.10），与下文的差别写在这两节末尾：3.3 没有按设想另计，生图 token 计入请求本身。下文是当时的快照。

2026-11-17，用户要求：“我们应该支持 openai 生图模型的计费，检查我们现在是否支持 gpt 的生图模型，codex 账号是否有接入生成图形的功能”。本文先写现状（ec2pool 测试实例实测），再给方案；**方案未实现，等用户确认第 4 节的决定后再开发**。

## 1. 现状（实测）

| 检查项 | 结果 |
|---|---|
| `POST /v1/images/generations` | 404 `not_found`。内置 `openai` 平台只有 `chat_completions`、`responses`、`responses_websocket`、`embeddings` 四个端点（`sdk/platforms/openai.json`），没有图片端点，任何 OpenAI 账号都不能直接调生图接口 |
| Sign in with ChatGPT（`chatgpt_siwc`）账号 + Responses `image_generation` 工具 | 上游 400 `subscription_sharing_unsupported_capability`：“User subscription sharing currently supports only 'web_search' and developer-defined 'function' and 'custom' tools … tool 'image_generation' is not supported. Remove it or use an API key instead.” **这类账号不能生图**，是上游政策，不是我们没接 |
| 设备码（`codex_device`，ChatGPT Codex 后端） | 测试实例上没有这类账号，未验证。需要一个真实设备码账号发一次带 `image_generation` 工具的请求确认 |
| SIWC 的 `GET /v1/models` | 只有 8 个对话模型（gpt-5.6 / 6 / 6.1 系列），没有生图模型 |
| 价格表 | 已同步 `gpt-image-1.5`、`chatgpt-image-latest`，都是 `per_token` `{p:5, c:10, cr:1.25}`：只有文字 token 的价，没有图片输入、图片输出 token 的价（官方图片输出 token 比文字贵数倍），即使接上端点也会算少 |
| Responses 用量里的生图部分 | 每个 Responses 应答都带 `tool_usage.image_gen`：`input_tokens`、`output_tokens`、`input_tokens_details.{text_tokens,image_tokens}`、`output_tokens_details.{text_tokens,image_tokens}`。目前用量规则不读它：API Key 账号用 `image_generation` 工具生图时，生图部分**完全不计费** |
| 价格表达式 | 文档预留了变量 `img`、`img_o`（ARCHITECTURE 7.3），还没有实现 |

## 2. 目标

1. API Key 账号可以调 `POST /v1/images/generations`（JSON，含流式）和 `POST /v1/images/edits`（multipart），按图片 token 正确计费。
2. 任何 OpenAI 账号经 Responses `image_generation` 工具生图时，生图部分按生图模型的价格另计。
3. codex 账号：SIWC 不支持（上游拒绝，控制台不宣称支持）；设备码账号若实测支持，按目标 2 计费。

## 3. 方案

### 3.1 计费变量：实现预留的 `img`、`img_o`

- 用量规则新增两个标准字段 `image_input_tokens`、`image_output_tokens`，对应表达式变量 `img`（图片输入）、`img_o`（图片输出）。与缓存类似：表达式计了 `img` / `img_o` 时，`p` / `c` 扣掉对应部分；没计时留在 `p` / `c` 里按文字价算（与 §104.3 缓存写入的做法一致，不会变成免费）。
- 价格示例（`gpt-image-1.5`，具体数以价格源为准）：`p*5 + cr*1.25 + img*8 + c*10 + img_o*32`。
- 改动面：`billing/expr`（变量、Normalize）、`core.UsageTokens` 与 `usage_logs` 两列（**需要迁移**）、价格页编辑器、价格同步。备选是不加列，只用插件计量值 `u("image_output_tokens")`（无迁移，但每个生图模型的表达式都要手写，统计页看不到图片 token）。

### 3.2 `openai` 平台新增端点

| id | 路径 | 协议 | 用量 |
|---|---|---|---|
| `images_generations` | `POST /v1/images/generations` | `openai.images` | JSON：`usage.input_tokens`、`output_tokens`、`input_tokens_details.image_tokens`→`image_input_tokens`、`output_tokens_details.image_tokens`→`image_output_tokens`；流式（`stream:true`）读 `image_generation.completed` 事件的 `usage` |
| `images_edits` | `POST /v1/images/edits` | `openai.images_edit` | 同上；请求是 multipart，模型取表单字段 `model`（需确认核心对 multipart 取模型、审核取 prompt 的支持，不支持时这一步单独排期） |

- API Key 账号类型（openai 插件）默认支持全部端点，加端点后自动可用；默认模型补 `gpt-image-1.5`、`chatgpt-image-latest`。
- codex 两种账号类型的 `endpoints` 只列 `responses`、`responses_websocket`，不会被调度到图片端点。

### 3.3 Responses 里的 `image_generation` 工具

- 生图部分按**生图模型**的价格计，而不是对话模型：作为一条附加用量（`usage.additional`），模型取请求 `tools[type=image_generation].model`，没写时用上游默认（目前 `gpt-image-1.5`，以文档为准），token 取 `response.tool_usage.image_gen`。
- 现有 `AdditionalUsageRule` 只接受数组快照、模型从应答里取；这里是对象、模型从请求里取，需要扩展规则（契约改动，CONTRACTS 新一节）。
- 结果：一条使用记录里对话部分按对话模型计、生图部分按生图模型计，明细里分开显示。
- **落地（2026-11-18，CONTRACTS §17.2、§118.10）**：所有者否掉了“生图部分按生图模型另计”：一条请求只有一个计价模型，即客户请求的模型，绝不按上游返回取价，没有默认模型名。`tool_usage.image_gen` 的 token 计入这条请求自己的用量（图片 token 记为 `img` / `img_o`），按客户请求的模型的价格算。同日曾实现过的附加用量对象形式已删除（CONTRACTS §120.2）。

### 3.4 codex

- SIWC：保持不支持；请求带 `image_generation` 时上游 400 原样返回（不在插件里删工具，免得客户端以为生成了图）。
- 设备码：先用真实账号验证；支持则无需插件改动，计费由 3.3 覆盖；不支持则同 SIWC。
- **落地（2026-11-18）**：SIWC 类型已删除（PLUGIN-CODEX 第 14.3 节）。设备码账号实测可以生图（上游模型 `gpt-image-2-codex`），插件未改，计费由 3.3 的落地覆盖（PLUGIN-CODEX 第 12 节、CONTRACTS §118.10）。

## 4. 需要用户决定

1. 计费变量用 3.1 的 `img` / `img_o`（有迁移，统计可见），还是只用插件计量值（无迁移）？建议前者。
2. 先做哪一步：建议顺序 3.1 → 3.2（generations）→ 3.3 → 3.2（edits）。
3. 是否提供一个设备码账号用于验证 3.4。
4. 生图价格：沿用价格同步源，还是在默认价里手写官方图片 token 价？
