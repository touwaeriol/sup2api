# 协议转换矩阵（第 3 期技术规格）

设计文档：https://claude.ai/artifact/Moff7JVEEQdkcgRAL4KK3k （“sup2api 核心通用协议层设计”，“协议转换矩阵”一节）。本文件是第 3 期的实现规格，实现完成后并入 CONTRACTS §88（§86 为第 1 期，§87 留给第 2 期），本文件保留为转换器开发说明。迁移号 0093（develop 已有 0092_usage_hourly）。代码依据：develop b9b654ac5（含 §86 声明式上游）。

## 0. 定案

设计已定（用户 2026-11-09 确认）：

1. 四种文本协议两两互转：OpenAI chat（`openai.chat`）、OpenAI responses（`openai.responses`）、Anthropic messages（`anthropic.messages`）、Gemini generateContent（`gemini.generate` / `gemini.stream_generate`）。
2. 质量等级（行 = 客户端协议，列 = 上游协议）：

   | 客户端 \ 上游 | chat | responses | anthropic | gemini |
   |---|---|---|---|---|
   | chat | — | good（已有） | fair（已有，重写） | fair |
   | responses | good | — | fair（已有，重写） | fair |
   | anthropic | fair | fair | — | discouraged（两跳） |
   | gemini | fair | fair | discouraged（两跳） | — |

3. 调度：优先级（priority）优先；同一优先级内原生优先，再按等级（good → fair → discouraged）。原生与转换的先后由管理员用优先级控制。等级和实际步骤记入使用记录。
4. 丢失策略（账号或分组上选）：`allow`（默认，丢掉并记录）、`safe`（会丢工具定义或工具调用时拒绝）、`strict`（任何有损都拒绝）。拒绝 = 跳过该账号，不计失败切换次数。优先级：账号 > 分组 > 默认 allow。
5. 每对覆盖：请求、非流式响应、SSE；文本、图片、工具定义与调用、推理内容、结束原因、用量。用量按上游协议规则读，计费不受转换影响。
6. count_tokens：上游没有对应端点时由核心 tokenizer 本地估算，不占限流和额度。
7. 不做：WebSocket 仍只走原生；异步任务端点不转换；Anthropic prompt 缓存标记不与 OpenAI 的缓存参数互转。
8. relaykit（AGPL）只参考行为，不拷代码；sup2api 保持 LGPL-3.0。
9. 2026-11-09 用户对第 13 节各项的答复（§13“已定决策”）：严格转换改宽松；优先级优先；所有账号类型都接受转换、没有退出机制；chat 客户端输出 `reasoning_content`；D5–D9 参考 new-api。

> **补充（2026-11-19，CONTRACTS §121）**：另有两对同平台的生图转换 `openai.images → openai.responses`、`openai.images_edit → openai.responses`（good，独立实现，不经 IR，不参与组合），把生图接口的请求转成带 `image_generation` 工具的 Responses 请求、把应答转回生图接口的形状（9.9）。同平台适配（含 chat ↔ Responses）改为只对账号类型在 `adapted` 里显式列出的端点生效（5.1）。

> **修订（2026-11-17，用户要求）**：第 9 条里的“所有账号类型都接受转换、没有退出机制”（D3）不再成立。跨平台协议转换默认关闭，要在账号上为具体的模型开启（账号字段 `model_protocols`）；账号还可以用 `protocols` 关掉本平台协议。没开转换的账号在转换之前就被调度过滤，候选账号全部如此时请求返回 404。转换器、等级、调度顺序、丢失策略（第 1–8 条）都不变；同平台转换（chat ↔ Responses）不算协议转换，默认照常。规则见 CONTRACTS §110、[DESIGN-2026-11-17-PROTOCOL-BINDING.md](DESIGN-2026-11-17-PROTOCOL-BINDING.md)。本文以下各节保留当时的决定，受影响处另加修订说明。

本规格定下的实现方式：

- 响应侧统一用一个内部表示（IR）：每个协议一个 reader（上游 → IR）和一个 writer（IR → 客户端），请求侧逐对实现。两跳只在注册表里做通用组合。
- 转换器不判断策略：只把每个字段归为“映射 / 有损（带类别）/ 不可能”，策略由网关按账号判断。
- 转换留在核心（`server/internal/gateway/convert`），在交给插件之前完成：转发 V2 起平台插件的 `Execute` 拿到的是已经转换成上游协议、改写完的完整请求体，响应的转换也由核心在中继时做（CONTRACTS §112.2）。
- 推理内容跨协议只传文本，签名和加密推理状态不跨协议携带（参考 new-api，§3.2）。
- 现有两个严格转换（chat/responses → anthropic）重写为宽松转换；`protocol-codec`（含 `strict`）在本期末删除。现状（2026-11-16）：`a853227ff` 已删掉 `protocol-codec/strict` 和 `convert/openai_anthropic.go`，`server/go.mod` 不再引用 `protocol-codec`，`next/` 里也没有别的模块 import 它；模块本身和 `go.work` 里的条目仍保留，因为根目录旧版 `backend/` 经 `replace` import 它的根包和 `modelpolicy`（`backend/internal/pkg/apicompat`、`claude`、`openai`），根目录 `Dockerfile`、`deploy/Dockerfile`、`backend/Dockerfile` 也复制它，`backend-ci.yml` 每次推送都会构建。要删它，得先让旧代码不再依赖它（需要用户决定是否动旧代码）。

## 1. 现状

### 1.1 代码

- `server/internal/gateway/convert`：
  - `convert.go`：`Converter{From, To, Request, Response, NewStream}`、`StreamConverter{Event, Flush}`、`Registry`（按 (from,to) 精确查找，`Lookup` / `CanConvert` / `Pairs`），`builtins` 三项。
  - `prepared.go`：`PreparedConverter.Prepare(body) (Converter, []byte, error)` 返回绑定本请求的转换器；`RequestError`（“无法表达”，网关返回 400 `unsupported_conversion`）；`DropReporter.Dropped() []string`（§83.2，只打 debug 日志）。
  - `openai_anthropic.go`：包一层 `protocol-codec/strict`（chat、responses → anthropic，严格：`reasoning_effort`、`developer` 角色、缺 `max_tokens`、`stop` 以外的大量字段一律拒绝；响应里的 thinking 对 chat 客户端直接失败；对 responses 客户端把 Anthropic thinking 包成 `sup2api-anthropic-thinking-v1:` 信封放进 `encrypted_content`）。
  - `openai_chat_responses.go`：chat → responses，宽松（§83.2），1900 行，手写 SSE 转换。
- `protocol-codec`（包 `apicompat`，2026-10 为 ccgateway 引入）：`AnthropicToResponses`、`AnthropicToChatCompletionsRequest`、`ResponsesToChatCompletionsRequest`、`ChatCompletionsToResponses`、各自的响应与流状态机。除 `strict` 引用 `AnthropicToResponsesResponse` / `ResponsesToChatCompletions` 外**没有任何调用方**。带 codex/DeepSeek 特化（强制 `parallel_tool_calls`、`verbosity: medium`、按模型名丢 `temperature`、namespace/tool_search、推理缓存），并且静默丢字段——§83.2 已因此决定不复用。
- 路由 `gateway/routing.go`：`planRoutes` 先加原生路由，再对每个账号类型 `addConvertedRoute`：按 `b.Type.Platforms` 顺序、平台端点顺序找**第一个**能转换的端点（“first match”），不看质量。转换在 `attempt` 里懒执行（`typeRoute.upstreamBody`），`RequestError` 直接 `attemptReturn`（已计 1 次尝试，不再试别的类型，见 `strict_conversion_admission_test.go`）；其他转换错误 `attemptFailover`，之后 `usable()` 跳过同类型账号。
- 调度 `dispatch.go` `pick`：所有路由类型的账号混在一起按 priority → 加权随机，原生和转换不分先后（§12 写的“原生优先”只体现在列表顺序）。
- 使用记录：`usage_logs.protocol`（客户端）、`upstream_protocol`、`anomalies`（扁平字符串 JSON，含 `failure_policy`、`availability` 两个 trace）。没有转换相关字段。
- count_tokens：`anthropic.count_tokens`、`gemini.count_tokens` 两个端点（billing free）；`tokenCounting()` 时不占并发、RPM/TPM、额度，不写粘性。没有本地估算路径；不支持计数端点的类型不服务它。`internal/tokenizer`（tiktoken o200k/cl100k，`Count(ctx, text, enc)`）已用于预扣估算（`prompt_count.go`、`extractPromptText`）。
- 设置：网关设置行 `gateway`（`gateway/settings.go` `GatewaySettings`，`GET/PUT /settings/gateway`，控制台 `GatewaySettingsCard.vue`）。SSRF 检查在 `internal/netguard`（`gateway/ssrf.go` 调用）。
- 账号/分组：`accounts` 已有 `header_passthrough`（0089）、`param_override`（0091）等列；`groups` 有 `model_filter_mode`、`environment`。调度快照 `core.AccountRef`（`account/directory.go` `groupSnapshot`），分组随 API Key 认证带入 `core.GroupInfo`（`apikey/apikey.go`）。迁移只要求编号唯一。
- 账号类型接口 `account/types.go`：`endpoints[].native=false` 表示经转换，靠 `CanConvert`。`sdk/manifest/check/corefloor.go` 的 `SamePlatformConversions` 只有 `chat → responses`。
- 插件声明（develop）：codex 只声明 openai `responses`/`responses_websocket`（streamOnly）；volcengine 声明 openai+anthropic+volcengine；custom 三类各声明一家全部端点。

### 1.2 每对的复用与改造

| 对 | 现状 | 做法 |
|---|---|---|
| chat → responses | 已接入，宽松 | 保留；`drop(path)` 改成 `drop(category, path)`；流式与 JSON 响应保留现有手写实现，增加 `reasoning_content` 输出（推理摘要文本）；其余以后可迁到 IR，本期不动 |
| responses → chat | codec 里有，未接入 | 新写请求侧；响应走 chat reader + responses writer；codec 的测试用例移植为样例 |
| chat → anthropic、responses → anthropic | strict 已接入 | 重写请求侧为宽松（一个 anthropic 构造器，两个入口）；响应走 anthropic reader + chat/responses writer；strict 的测试输入移植为样例：strict 接受的输入（推理信封相关的除外），新转换在 `strict` 策略下必须零损失、请求语义等价；`sup2api-anthropic-thinking-v1:` 信封不再生成，请求里遇到按普通“别家推理”丢弃 |
| anthropic → chat、anthropic → responses | codec 里有，未接入 | 新写，codec 只作行为参考 |
| chat/responses → gemini、gemini → chat/responses | 无 | 新写，行为参考 relaykit `gemini_chat`、`oai_chat/to_gemini_*`（只看行为） |
| anthropic ↔ gemini | 无 | 注册表通用组合（§4），无专门代码 |

## 2. 转换器接口

### 2.1 类型（`convert/convert.go`）

```go
type Grade int8 // 越小越好
const (
	GradeNative      Grade = 0 // 只用于路由，转换器不返回
	GradeGood        Grade = 1
	GradeFair        Grade = 2
	GradeDiscouraged Grade = 3
	GradeLocal       Grade = 9 // 只用于 count_tokens 本地估算路由
)
func (g Grade) String() string // native | good | fair | discouraged | local

type LossCategory string
const (LossTool = "tool"; LossReasoning = "reasoning"; LossImage = "image"; LossOther = "other")

type Loss struct {
	Category LossCategory
	Path     string // 本跳输入协议里的路径：请求 "tools[2]"、"messages[3].content[1]"；响应 "response.content[2]"
	Hop      int    // 第几跳（单跳 0）
}

// MediaFetcher 下载 http(s) 媒体，返回 MIME 与 base64；同一请求内按 URL 缓存。只给到 Gemini 的转换用（D6）。
type MediaFetcher func(ctx context.Context, url string) (mime, b64 string, err error)

type Options struct {
	Ctx    context.Context
	Model  string       // 客户端模型（c.model，Gemini 取自路径）
	Stream bool         // 客户端是否流式（c.stream）
	Fetch  MediaFetcher // 网关提供；nil 时遇到需要下载的 URL 返回 RequestError
	ClaudeDefaultMaxTokens func(model string) int // 网关设置（D5）
}

type Converter interface {
	From() string
	To() string
	Grade() Grade
	// Prepare 转换一个请求，返回绑定本请求的转换器（注册表里的实例无状态、可并发）。
	Prepare(body []byte, opt Options) (Converter, []byte, error)
	Response(body []byte) ([]byte, error) // 只在绑定后的转换器上调用
	NewStream() StreamConverter           // 同上；每次调用独立状态
}

// LossReporter：绑定后的转换器实现。Prepare 之后返回请求侧损失；
// Response / 流 Flush 之后再追加响应侧损失（Path 以 "response." 开头）。排序、去重。
type LossReporter interface{ Losses() []Loss }

// HopReporter：组合转换器实现；默认 [From, To]。
type HopReporter interface{ Hops() []string }
```

- 去掉 `Converter.Request` 和 `DropReporter`：`Prepare` 必须实现（`convert.Prepare` 辅助函数删除；测试桩 `fakeConv` 跟着改）。`Losses(c)`、`Hops(c)` 两个辅助函数对未实现接口的转换器返回 nil / `[From, To]`。
- `RequestError` 保留，只表示“不可能”（§2.2 第 3 类），与策略无关。
- `Registry`：`Register` 校验 `Grade()` ∈ {good, fair, discouraged}；新增 `Resolve(from, to) (Converter, bool)`：有直接转换器就返回它，否则 from→`openai.responses` 和 `openai.responses`→to 都存在时返回组合转换器（§4，缓存）；`CanConvert` 改用 `Resolve`；`Pairs()` 返回 `[]PairInfo{From, To, Grade, Hops}`（含组合）。`Lookup` 删除。

### 2.2 字段的三类处理

每个转换器对客户端请求的每个成员只做三件事之一，写进代码注释和本文 §9：

1. **映射**：有确定等价，或映射到对方文档里最接近的取值（`budget_tokens` → effort 档位、角色改名、schema 格式互转、`developer` → `system`、URL 图片下载后内联）。不算损失。
2. **有损**：丢掉、位置改变、取值被截断或改写，但请求仍然合法、主要意图仍在。照常转换，记一条 `Loss`。
3. **不可能**：丢掉后请求意义全变或上游必然拒绝（`n > 1`、`previous_response_id`、`background: true`、Gemini `cachedContent`、要求图片/音频输出、空对话、JSON 无效、字段类型错误、到 Gemini 的媒体下载失败或 MIME 不受支持）。返回 `RequestError`。

显式写成默认值的字段（`n: 1`、`logprobs: false`、罚分 0、`null`）等于没写，既不转换也不记录（沿用 §83.2 规则 4）。

### 2.3 损失类别（判定规则）

| 类别 | 何时记 |
|---|---|
| `tool` | 上游看到的**可调用工具集合**、**强制选择**或**工具调用历史**与客户端不同：工具定义（函数、custom、托管工具）被丢；`tool_choice` 的指定工具、`required`、`allowed_tools` 限制被放宽；历史里的工具调用、工具结果、托管工具调用/结果被丢或参数/输出被改写（参数 JSON 无效时被替换为 `{}`） |
| `reasoning` | 推理配置被忽略（例如因 `max_tokens` 太小或工具循环里缺签名而关掉 thinking）；历史里的推理内容（thinking 块、reasoning 项、thought 部件、签名、加密推理）被丢；响应侧上游的签名/加密推理状态无法带给客户端 |
| `image` | 输入图片（含工具结果里的图片）被丢或换成文字；`detail` 被丢 |
| `other` | 其他：采样参数、`stop`、`seed`、元数据、缓存标记、system 消息位置、文件/音频部件、`response_format` 细节、custom 工具的 grammar、`parallel_tool_calls: false` 无法表达等 |

工具定义细节（`strict` 标志、描述里的 `cache_control`、schema 中目标不支持的关键字）属于 `other`，不影响 `safe`。

### 2.4 策略判定点

转换器不知道策略。网关在**挑选账号时**（`pick` / 粘性绑定检查里的 `usable(ref)`，不在 `attempt` 里）：

1. 该账号路由需要转换且本请求还没转换过 → `Prepare(c.body, Options{...})`，结果缓存在 `typeRoute`（按 `c.bodyVersion` 失效）。只在挑选时真正考察到该账号时才转换（按 §5.2 的排序惰性进行），没被考察到的类型不转换、不下载媒体。
2. `Prepare` 返回错误 → 整个账号类型不可用（`RequestError` 记为“不可能”，其他错误记 warn）。
3. 否则取账号的生效策略 `effective(ref.ConversionLossPolicy, group.ConversionLossPolicy)`，对请求侧损失判定：`allow` 总通过；`safe` 有 `tool` 损失则拒绝；`strict` 有任何损失则拒绝。结果按 (路由, 策略) 缓存（只有 3 种）。
4. 被拒绝的账号不进候选池，不占并发槽、不计 `attempts`、不触发失败切换。拒绝原因记入 `c.convTrace.Rejected`。
5. 所有候选都因转换不可用（不可能或被策略拒绝）时，请求以 400 `unsupported_conversion` 结束（记录类型 `invalid_request`，`attempts = 0`），消息带第一条原因：`request cannot be converted to anthropic.messages: tools[1] would be lost (tool, policy safe)` 或 `RequestError` 的原文。有其他原因（忙、限流）时按现有规则返回那个原因。

与现状不同：`RequestError` 不再直接终止请求，而是跳过该类型继续试其他类型（与 §12“同类型其他账号跳过、不计失败切换次数”一致）；`strict_conversion_admission_test.go` 的期望改为“有原生账号时由原生账号服务，只有转换类型时 400、attempts 0”。

响应侧损失**从不拒绝**（上游已产生用量），只记录。

## 3. 响应侧中间表示（IR）

文件：`convert/ir.go`（类型）、`convert/read.go`（reader 共用部分）与 `convert/read_{chat,responses,anthropic,gemini}.go`、`convert/write.go`（writer 共用部分）与 `convert/write_{chat,responses,anthropic,gemini}.go`、`convert/pair.go`（通用对转换器）。

### 3.1 类型

```go
type Msg struct {
	ID, Model string
	Created   int64
	Blocks    []Block
	Finish    Finish
	Usage     Usage
}
type BlockKind uint8 // Text | Reasoning | ToolCall | Refusal | Image | Opaque（服务端工具等无法表达的块）
type Block struct {
	Kind        BlockKind
	Text        string     // 文本 / 推理摘要 / 拒绝
	HasState    bool       // Reasoning：上游带了签名或加密推理（不跨协议携带，writer 记 reasoning 损失）
	ToolID      string
	ToolName    string
	ToolKind    string     // function | custom
	Args        string     // JSON 文本（custom 为原始输入）
	Annotations []Citation // url_citation 统一形状 {url, title, start, end}
	MIME, Data  string     // Image：base64
	Raw         json.RawMessage // Opaque：原样保留，writer 决定丢弃并记损失
	Src         string          // 块在上游回复里的位置（不带 "response." 前缀，如 "output[2]"、"content[1]"），响应侧损失路径用它
}
type FinishReason uint8 // Stop | ToolUse | Length | Filter | Pause | Error
type Finish struct{ Reason FinishReason; Raw, StopSequence string }
type Usage struct {
	Set                                   bool
	Input                                 int64 // 含缓存（OpenAI/Gemini 口径）
	CacheRead, CacheWrite5m, CacheWrite1h int64
	Output                                int64 // 含推理
	Reasoning                             int64
}

// 流式增量
type Delta struct {
	Op     DeltaOp // Start | BlockStart | Text | Reasoning | ReasoningState | Args | Citation | BlockStop | Usage | Finish | Error
	Index  int     // 块序号（按出现顺序从 0 起）
	Block  *Block  // BlockStart：Kind、ToolID、ToolName、ToolKind
	Text   string  // Text / Reasoning / Args 的增量
	Cite   *Citation
	Usage  *Usage  // 累计值，后到的覆盖先到的
	Finish *Finish
	Err    *UpstreamError // {Status, Type, Message}
	Start  *Msg    // Start：ID、Model、Created
}

type Reader interface {
	Event(ev Event) ([]Delta, error) // 上游一条 SSE 事件
	End() ([]Delta, error)           // 上游流结束
	JSON(body []byte) (*Msg, error)  // 非流式
}
type Writer interface {
	Write(ds []Delta) ([]Event, error)
	End() ([]Event, error)
	JSON(m *Msg) ([]byte, error)
	Losses() []Loss // 响应侧损失
}
```

规则：reader 在知道用量时就发 `Usage`，只在协议的终止点发 `Finish`（chat：`[DONE]` 或流结束且见过 `finish_reason`；responses：`response.completed/incomplete`；anthropic：`message_delta` 带 `stop_reason`；gemini：带 `finishReason` 的块）。writer 在 `Finish` 时写终止事件，之后的增量丢弃；`End` 时没收到 `Finish` → 报转换失败（客户端看到截断，同 §83.2）。`Error` 立即按客户端协议写错误事件并结束。`ReasoningState` 只表示“这个推理块带了签名/加密状态”，内容不传。

增量顺序（reader 保证，writer 依赖）：`Start` 最先且只有一次；块按出现顺序从 0 编号，同一时刻最多一个打开的块，开始新块前先发上一块的 `BlockStop`；`Usage` 可出现在任何位置（累计值）；`Finish` 或 `Error` 之后不再有增量。reader 也可以实现 `LossReporter`，报告 IR 装不下的内容（无法映射的结束原因、没有 url 的引用）。

`pair.go`：`pairConverter{from, to, grade, req func(body, opt, *losses) ([]byte, respOpts, error), reader func() Reader, writer func(respOpts) Writer}`。`Prepare` 调 `req`，绑定 `respOpts`（客户端模型、`include_usage`、旧式 functions 模式、custom 工具名表等）；`Response` = `reader.JSON` → `writer.JSON`；`NewStream` = reader → writer 串接。新增 8 对都用它声明，不再各写状态机。

### 3.2 推理跨协议（参考 new-api，D8）

- **响应侧只传文本**：上游的推理文本按客户端协议输出（chat `reasoning_content`、responses reasoning 项的 `summary`、anthropic thinking 块、gemini `thought: true` 部件）。上游的签名（Anthropic `signature`、`redacted_thinking.data`、Gemini `thoughtSignature`）和 OpenAI `encrypted_content` **不**带给客户端，记一条响应侧 `reasoning` 损失。anthropic writer 写 thinking 块时不带 `signature` 字段（与 new-api 一致）；responses writer 的 reasoning 项不带 `encrypted_content`。
- **请求侧丢弃别家推理**：客户端历史里的推理（chat `reasoning_content`、responses reasoning 项、anthropic thinking/redacted_thinking、gemini thought 部件与 `thoughtSignature`）到另一家上游时一律丢弃，记 `reasoning` 损失。
- **不做信封、不做原生清理**：sup2api 不生成任何 `sup2api-` 推理信封（现有 `sup2api-anthropic-thinking-v1:` 停止生成）；原生路由不扫描、不改写请求。对话从转换路由换到原生账号时，客户端带回的无签名 thinking 等由原生上游自行处理（可能 400），靠粘性会话把对话留在同一账号（new-api 靠渠道亲和）。
- **Anthropic 工具循环**：开了 thinking 且最后一条带 tool_use 的 assistant 消息里没有本家签名的 thinking 块（转换后必然没有）→ 本次不开 thinking，记 `reasoning` 损失（否则上游 400）。
- **Gemini thoughtSignature**：转换得到的每个 model 轮次，第一个 `functionCall` 部件（没有就第一个文本部件）写 `thoughtSignature` 旁路值 `context_engineering_is_the_way_to_go`（Google 文档公开的值；new-api 默认开启同一行为），不记损失。

## 4. 多跳组合

`convert/compose.go`：`compose(a, b Converter) Converter`，要求 `a.To() == b.From() == "openai.responses"`（唯一中转协议，最多两跳）。

- `Grade()` = discouraged；`Hops()` = `[a.From, "openai.responses", b.To]`。
- `Prepare(body, opt)`：`a.Prepare(body, opt)` → `bodyR`；`b.Prepare(bodyR, opt)` → `bodyB`（`opt` 原样传：模型、流式在两跳间不变）。任一步错误即返回该错误（`RequestError` 保持类型）。损失 = a 的（Hop 0）+ b 的（Hop 1）。
- `Response(x)` = `a.Response(b.Response(x))`。
- 流：`Event(ev)`：`rs := bs.Event(ev)`，对每个 `r` 依次 `out += as.Event(r)`；`Flush`：`bs.Flush()` 的事件喂给 `as.Event`，再加 `as.Flush()`。中间事件的 `Data` 只在本次调用内使用，不需复制。
- 前提：每个 writer 产出的事件对同协议的 reader 必须是合法输入（responses writer → responses reader）。黄金测试里每个组合对都有专门样例，并断言“组合结果 = 手动两步结果”。

组合在注册时为每个缺直接转换器的对生成（两端属于同一平台时不组合，如 `gemini.generate` ↔ `gemini.stream_generate`；生图两对实现 `terminal`，不作第一跳：`image_generation` 工具只有 Responses 有），不注册；`Resolve` 返回它，`Pairs()` 列出。

## 5. 路由与调度

### 5.1 选目标端点（`planRoutes` / `addConvertedRoute`）

对每个启用的账号类型 b（客户端端点平台 P、协议 X）。所有账号类型都参与转换，没有插件、账号类型或分组层面的退出机制（D3）：

> **修订（2026-11-17）**：选路规则（下面 1–7）不变，每个账号类型仍得到一条路由；路由上多记一个 `cross`（b 不支持 P，即第 2 步的“看 b 其他平台”）。退出机制在账号层：`cross` 的路由只对账号在 `model_protocols` 里开启了协议 X 的模型可用，b 支持 P 的路由（原生、同平台转换）可以被账号的 `protocols`、`model_protocols` 关闭。判断在 `ineligibleReason` 里，早于转换和 §5.2 的挑选（CONTRACTS §110、[DESIGN-2026-11-17-PROTOCOL-BINDING.md](DESIGN-2026-11-17-PROTOCOL-BINDING.md)）。

1. 原生：b 支持 P 且子集含该端点 → grade native（不变）。
2. 否则收集候选端点 e：b 支持 P 但子集不含该端点时，只有 b 在 P 的 `adapted` 里列出该端点（CONTRACTS §83.3、§121.1，2026-11-19 起必须显式声明）才看 P 上子集内的端点，否则不服务；b 不支持 P 时看 b 其他平台子集内的端点。跳过 WebSocket、非 proxy 端点、计数端点。判断在 `core.BestConvertTarget` 一处，账号模块的可服务端点与协议用同一个函数。
3. 对每个 e：`conv, ok := Resolve(X, e.Protocol)`；要求流式相容：
   - 客户端流式：`e.Response.Stream == "sse"`；
   - 客户端非流式：`e.Request.Stream != true` 且 `e.Response.NonStream == "json"`，或 b 对 `e.Protocol` 声明了 streamOnly（§83.1 照旧）。
   所以 chat 流式请求到 Gemini 选 `gemini.stream_generate`，非流式选 `gemini.generate`；Gemini 流式客户端到 chat 上游带 `stream: true`。
4. 在候选里取 (grade, 跳数, 声明顺序) 最小者；声明顺序 = b 的 `platforms` 顺序，其次平台的 `endpoints` 顺序。完全确定，等级相同时就是今天的“first match”。
5. `typeRoute` 新增 `grade Grade`、`hops []string`；`rt.upstream` 仍是最后一跳协议（使用记录 `upstream_protocol`、用量规则、`meta.protocol` 都按它）。转换路由另取：转换器的 `ModelLocator.ModelPath()`（有就代替上游端点的 `modelPath`，§121.4）、`ResponseModelLocator.ResponseModelPaths()`（有就把用量规则读 `model` 的路径换成它，读到的模型覆盖账号映射记为 `upstream_model`，§121.4）、本平台路由上 `adapted` 选项的 `model`（`adaptModel`，交给 `Options.AdaptModel`，§121.3）。
6. WebSocket、异步任务提交端点的过滤不变（只要原生）。
7. count_tokens 端点另见 §8。

### 5.2 挑选账号（`pick`，D2：优先级优先）

- 粘性绑定优先，规则不变；绑定账号被策略拒绝或类型转换不可能时按“类型不可用”处理（保留绑定，同现状）。
- 其余：`rankOverrides` 之后按 **priority 降序**分层（不变）；同一 priority 内先按路由等级升序（native → good → fair → discouraged → local）分组，每组内照旧加权随机（`weightedOrder`）。按此顺序逐个 `usable(ref)`（惰性转换与策略判定，§2.4）并立即获取并发槽；都拿不到时 `queueTarget` 照旧在整个可用池上选排队目标。
- 被限流（exhausted）、权重 0 的账号照旧排除。
- 失败切换（`excluded`）与故障策略（`policyCandidates`）共用这套排序。
- 结果：等级只影响同一优先级内的先后；管理员要让转换账号先于原生账号，就把它们的优先级设得更高。

### 5.3 其他

- 账号类型接口 `GET /account-types`：`endpoints[]` 新增 `grade`（native/good/fair/discouraged/local）和 `via`（经过的协议，组合时 3 项）。
- `sdk/manifest/check/corefloor.go`：`SamePlatformConversions` 加 `{"openai.responses", "openai.chat"}`，新常量 `ConvertMatrixFirstCore`（本期核心版本）用于该项的 hostCompat 下限；`convert` 包测试保证两边一致。
- `forward.go` / `resource_response.go` / `streamonly.go` 只换成调用绑定后的转换器（字段名不变，`rt.conv`）。响应完成后把 `Losses(rt.conv)` 的响应侧部分并入使用记录。

## 6. 丢失策略与转换设置：存储、接口、控制台

### 6.1 存储（迁移 `0093_conversion_loss_policy.sql`）

```sql
ALTER TABLE accounts  ADD COLUMN IF NOT EXISTS conversion_loss_policy varchar(10)
  CHECK (conversion_loss_policy IN ('allow','safe','strict'));
ALTER TABLE groups    ADD COLUMN IF NOT EXISTS conversion_loss_policy varchar(10)
  CHECK (conversion_loss_policy IN ('allow','safe','strict'));
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS conversion jsonb;   -- NULL = 原生
```

NULL 表示继承：账号 NULL → 分组；分组 NULL → `allow`。三列都是可空、无默认值，PG 只改元数据，不重写表。合并时若 0093 已被占用，取下一个空号。

- `core.AccountRef.ConversionLossPolicy string`（`groupSnapshot` 查询加列，快照随账号变更失效，现有机制）；`core.GroupInfo.ConversionLossPolicy string`（`apikey` 认证时加载，随分组变更失效，现有机制）。
- 备选（不选）：做成 §74.3 环境变量——环境变量只支持数字，且账号层取 attempt 时重新加载的账号，而策略要在挑选时判定。

网关设置行 `gateway`（`GatewaySettings`，`GET/PUT /settings/gateway`）新增两项（参考 new-api 的全局设置，D5、D6）：

| 字段 | 类型与默认 | 含义 |
|---|---|---|
| `claude_max_tokens`（2026-11-14 取代 `claude_default_max_tokens`） | `{"enabled": false, "models": {"default": 8192}}`，`models` 键为模型名 | 可开关的内置参数覆盖（CONTRACTS §84.1）：开启时给每个发往 Anthropic 且没有 `max_tokens` 的请求补上，按客户端模型精确匹配，否则 `default`；转换请求客户端没给输出上限时用它的值，关闭或没有匹配时用转换器兜底 8192 |
| `media_download_max_mb` | 64，范围 1–1024 | 转换到 Gemini 时下载 http(s) 媒体的单个文件上限 |

### 6.2 接口

| 接口 | 字段 |
|---|---|
| `GET/POST/PATCH /accounts`、`/accounts/:id` | `conversion_loss_policy`: `"allow"\|"safe"\|"strict"\|null`；非法值 400 `invalid_argument`，`details.fields[{field:"conversion_loss_policy", code:"invalid"}]`；权限同账号其他字段（含 own 级） |
| `GET/POST/PATCH /groups`、`/groups/:id` | 同上（`group:update`） |
| `GET/PUT /settings/gateway` | `claude_max_tokens`、`media_download_max_mb`；校验错误 `details.fields`，字段名 `claude_max_tokens`（超范围、非法模型名、超过 100 项、总长度超限） |
| `GET /account-types` | `endpoints[].grade`、`endpoints[].via` |
| 使用记录列表与详情 | `conversion`（§7）；`self` 范围不返回（与 `upstream_protocol` 一致） |
| 使用记录筛选 | `conversion=native\|converted\|good\|fair\|discouraged\|local\|lossy`（`lossy` = 有任何损失） |

第 2 期的 commonControls 以后可以把 `conversionLossPolicy` 列入插件可声明默认/锁定的项，本期不做。

### 6.3 控制台

- 账号编辑器“高级”分区（`AccountEditor.vue`，转发设置组）：下拉“协议转换丢失策略”：继承分组（默认）/ 允许 / 安全 / 严格；提示显示当前生效值和来源。
- 分组表单（`GroupsView.vue`）：同样的下拉（默认 = 允许）；详情弹窗显示。
- 系统设置网关卡片（`GatewaySettingsCard.vue`）：Claude 默认 max_tokens（键值表，`default` 行不可删）、媒体下载上限。
- 使用记录：`UsageTable.vue` 上游协议列在转换时加徽标“转换·fair”（悬停显示步骤），有损时加图标；`UsageDetail.vue` 新增“协议转换”块：等级、步骤链、生效策略、按类别列出损失路径、被拒绝的账号类型；`UsageFilters.vue` 加筛选项。
- 账号类型/新建账号的端点列表：`native=false` 的徽标换成等级徽标。
- i18n zh/en；`web/src/api/types.ts` 加字段。

## 7. 使用记录

`core.UsageRecord.Conversion *ConversionTrace`，settler 写 `usage_logs.conversion`：

```json
{"v":1,"grade":"fair","hops":["anthropic.messages","openai.responses"],"policy":"allow",
 "lost":{"tool":["tools[3]"],"reasoning":["messages[4].content[0]"],"other":["top_k"]},
 "response_lost":{"reasoning":["response.output[0].encrypted_content"]},
 "omitted":0,
 "rejected":[{"type":"anthropic/apikey","policy":"safe","category":"tool","path":"tools[1]"}]}
```

- 写入条件：最终路由是转换或本地估算，或有 `rejected`。原生且无事发生时为 NULL。
- 上限：路径合计 40 条、每条 160 字节，超出计入 `omitted`；`rejected` 最多 5 条。组合时路径前加跳标记 `"1:tools[0]"`（第 2 跳的路径是 Responses 请求里的路径）。
- `policy` 是最终服务账号的生效策略；本地估算为 `{"v":1,"grade":"local","hops":["anthropic.count_tokens"]}`。
- 用量与计费：照旧由上游端点的用量规则从**上游**响应读取（`routing.go` `usagerules.For` 按最后一跳），转换后给客户端看的 usage 只是换算展示，不参与计费。生图两对也是：Responses 端点的规则本来就把生图工具的用量加进来（CONTRACTS §118.10、§121.5），只有 `model` 的路径换成转换器 `ResponseModelLocator` 给的（回显的生图模型，§121.4）。
- 生图转换的记录：`hops` 为 `["openai.images", "openai.responses"]`（edits 为 `openai.images_edit`），`grade` good；应答里 assistant 的文字等非图片输出项记 `response_lost.other`（`response.output[i]`）。
- `dispatch.go` 原来的 debug 日志 `request conversion dropped fields` 换成带类别的同名日志。

## 8. count_tokens（参考 new-api，D9）

适用：客户端端点 `anthropic.count_tokens`、`gemini.count_tokens`。

1. **路由**：原生计数路由不变。账号类型没有原生计数端点、但能服务“同族生成端点”（`anthropic.count_tokens` → `anthropic.messages`，`gemini.count_tokens` → `gemini.generate`，原生或经 `Resolve`）时，加一条 `grade = local` 的路由。计数端点之间不做协议转换。
2. **调度**：和普通请求一样挑账号（§5.2：priority 优先，同 priority 内原生先于 local），`tokenCounting()` 的现有豁免不变（不占并发、RPM/TPM、额度，不写粘性）。挑中的账号走原生路由 → 照旧转发到它的计数端点（失败处理不变）；挑中的账号走 local 路由 → 不发上游，本地估算并返回。即“选中的账号决定怎么计数”，与 new-api 一致（new-api 由选中渠道的设置决定转发或本地估算）。原生计数账号不可用（冷却、禁用）时自然会选到 local 路由的账号。
3. **估算**（`gateway/count_local.go`，行为对照 new-api `service.CountRequestToken` 与 `dto.ClaudeRequest.GetTokenCountMeta`）：
   - 文本按顺序收集、以换行拼接后用 `tokenizer.Count(o200k_base)` 计数：
     - anthropic：`system` 文本；每条消息的 `role` 与文本块；`tool_use` 的 name 与 input 的 JSON；`tool_result` 的文本（字符串或文本块，图片块按媒体计）；工具定义的 name、description、input_schema 的 JSON；web_search 工具的 name 与 user_location 的 JSON。
     - gemini（请求体为 `{contents, systemInstruction, tools}` 或 `{generateContentRequest: {...}}`，后者取内层）：`systemInstruction` 文本、每个 content 的 role 与 `text` 部件、`functionCall` 的 name 与 args JSON、`functionResponse` 的 JSON、`functionDeclarations` 的 JSON。
   - 不加消息格式开销（new-api 只对 OpenAI 格式加）。
   - 媒体按固定值（new-api 对非流式请求默认不下载、不测尺寸，按 1024×1024 计）：图片 anthropic 1373（resize 到 1568 内，28 像素块数 + 4）、gemini 1290（768 切块规则）；PDF/文件 4096；音频 256；视频 8192。
4. **响应**：anthropic `{"input_tokens": N}`；gemini `{"totalTokens": N}`。不加任何额外响应头。
5. **使用记录**：`account_id` 为挑中的账号、`upstream_protocol = "local"`、`conversion.grade = "local"`、成功、计费 free（端点本来就是 free）。

与 new-api 的差别：new-api 选中非 Anthropic 渠道时回 404、不估算，Gemini `:countTokens` 一律 404；sup2api 按已批准的设计对没有计数端点的类型本地估算，并保留原生 Gemini 计数。new-api 的“Claude 渠道可设为本地估算或转发到指定渠道”（`claude_count_tokens_mode`）本期不做。

## 9. 映射表

约定：→ 表示客户端到上游请求方向；“丢（类别）”= 有损；“不可能”= `RequestError`。Gemini 客户端请求同时接受 camelCase 和 snake_case 成员名（`systemInstruction` / `system_instruction`、`generationConfig` / `generation_config` 等，与 Google proto JSON 一致）。

### 9.1 请求顶层

| 概念 | chat | responses | anthropic | gemini |
|---|---|---|---|---|
| 模型 | `model` | `model` | `model` | 路径 `:model`（`Options.Model`；Gemini 上游请求体不带 model） |
| 系统 | 开头连续 `system`/`developer` 消息 | `instructions`；input 里 `system`/`developer` 消息 | `system`（字符串或文本块） | `systemInstruction.parts[].text` |
| 中途的系统消息 | 原位 | 原位（developer） | 并入 `system` 末尾，丢（other，位置） | 并入 `systemInstruction`，丢（other，位置） |
| 输出上限 | `max_completion_tokens` / `max_tokens` | `max_output_tokens`（最小 16） | `max_tokens`（必填；客户端没给时见 9.4 末行与 D5） | `generationConfig.maxOutputTokens` |
| 温度 | `temperature` 0–2 | 同 | 0–1：超出截到 1，丢（other）；开 thinking 时删除，丢（other） | `temperature` |
| top_p / top_k | `top_p` / — | `top_p` / — | `top_p`（开 thinking 时删除，丢 other）/ `top_k` | `topP` / `topK` |
| 停止序列 | `stop`（≤4） | —（丢 other） | `stop_sequences` | `stopSequences`（≤5，超出丢 other） |
| seed、罚分、logit_bias | 有 | —（丢 other） | —（丢 other） | `seed`、`presencePenalty`、`frequencyPenalty`；logit_bias 丢 other |
| logprobs | `logprobs`/`top_logprobs` | `top_logprobs` | 丢 other | 丢 other（writer 不输出） |
| n | `n` > 1 不可能 | — | — | `candidateCount` > 1 不可能 |
| 结构化输出 | `response_format` | `text.format` | `output_config.format`（只有 json_schema；`json_object` 丢 other；`strict:false`/`description` 丢 other） | `responseMimeType: application/json` + `responseJsonSchema`；客户端 `responseSchema`（OpenAPI 子集）转 JSON Schema（类型名小写） |
| 冗长度 | `verbosity` | `text.verbosity` | 丢 other | 丢 other |
| 推理 | 见 9.4 | | | |
| 流式 | `stream` + `stream_options.include_usage` | `stream` | `stream` | 端点决定（§5.1） |
| 用户标识 | `user` / `safety_identifier` | 同 | `metadata.user_id` | 丢 other |
| 并行工具 | `parallel_tool_calls` | 同 | `tool_choice.disable_parallel_tool_use` | false 丢 other |
| 服务层级 | `service_tier` | 同 | `service_tier`（`auto`↔`auto`，`default`↔`standard_only`，其他丢 other） | 丢 other |
| 缓存（D7） | `prompt_cache_key`、`prompt_cache_retention`、部件 `prompt_cache_breakpoint` | 同 | 块上 `cache_control` | `cachedContent` 不可能 |
| 存储与状态 | `store` | `store`；`previous_response_id`、`conversation`、`background:true`、`prompt` 不可能；`truncation` 丢 other | — | — |
| 安全 | — | — | — | `safetySettings` 丢 other；转到 Gemini 不加任何 safetySettings |
| 其他未知成员 | 丢 other | 丢 other | 丢 other（`container`、`context_management` 等） | 丢 other；`responseModalities` 含 IMAGE/AUDIO 不可能 |

缓存参数（D7，参考 new-api）：`prompt_cache_key` 在 chat ↔ responses 间原样，`prompt_cache_retention` 在 responses → chat 原样；anthropic → chat 时消息文本块的 `cache_control` 原样写到对应 chat 文本部件的 `cache_control` 上（OpenRouter 一类 chat 上游认它，其他上游忽略），`system` 块上的 `cache_control` 丢 other；其余方向（chat/responses → anthropic、anthropic → responses、到/从 gemini）缓存参数与标记丢 other，`prompt_cache_breakpoint` 与 `cache_control` 不互转，也不自动插入断点。

到 chat 上游的流式请求总是写 `stream_options.include_usage: true`（计费需要），chat reader 按客户端是否要求决定是否转出用量。到 responses 上游总是 `store: false`。

### 9.2 内容部件

| 内容 | chat | responses | anthropic | gemini |
|---|---|---|---|---|
| 文本 | `{type:text}` / 字符串 | `input_text` / `output_text` | `{type:text}`（空文本删除，不算损失） | `{text}`；user 文本里的 markdown 内联 data URL 图片 `![…](data:…)` 拆成文本 + `inlineData` 部件（new-api 同样只对 Gemini 这样做） |
| 图片 base64 | `image_url.url` = data URL | `input_image.image_url` = data URL | `image.source{type:base64, media_type, data}` | `inlineData{mimeType, data}` |
| 图片 URL | `image_url.url` = http(s) | 同 | `source{type:url}` | 核心下载后 `inlineData`（D6，见下） |
| 图片 file_id | — | `input_image.file_id` | 丢 image | 丢 image |
| detail | `detail` | `detail` | 丢 image（非 auto 时） | 丢 image（非 auto 时） |
| PDF | `file{file_data, filename}` | `input_file{file_data, filename}`；`file_url` | `document.source{base64, application/pdf}`；文本源 → 文本；url 源丢 other | `inlineData application/pdf`；`file_url` 下载后内联（D6）；`file_id` 丢 other |
| 音频输入 | `input_audio{data, format}` | —（丢 other） | —（丢 other） | `inlineData audio/wav\|mp3` |
| 助手拒绝 | `refusal` | `refusal` 部件 | 文本 | 文本 |
| 引用 | `annotations[url_citation]` | `output_text.annotations` | `text.citations`（web_search_result_location 等，转 url_citation；其他丢 other） | `groundingMetadata` → url_citation（无法定位偏移时只给 url，丢 other） |
| 工具结果里的图片 | 不支持：图片移到紧随其后的 user 消息，加说明文字，丢 other（位置） | `function_call_output.output` 数组里的 `input_image` | `tool_result.content` 里的 image 块 | `functionResponse.parts[].inlineData`（Gemini 3+；更早模型丢 image）；URL 同样下载 |

**到 Gemini 的媒体下载**（D6，参考 new-api `service/file_service.go` `loadFromURL`）：`gateway/convert_media.go` 实现 `MediaFetcher`：只允许 http(s)，经 `internal/netguard` 的 SSRF 检查下载：不走账号代理，核心进程环境配了 `HTTP_PROXY` / `HTTPS_PROXY`（`NO_PROXY` 除外）时经该代理，否则直连（规则见 CONTRACTS §88.5）；`SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM` 同样放行私有地址，单个文件不超过 `media_download_max_mb`（默认 64），单个文件 60 秒超时且受请求 ctx 约束；MIME 取响应 `Content-Type`，不可信时按内容嗅探与扩展名；同一请求内按 URL 缓存（多个类型、多次重试只下载一次）。下载失败、超限或 MIME 不在 Gemini 支持的列表（图片 png/jpeg/webp/heic/heif、`application/pdf`、`text/plain`、audio/*、video/*）→ `RequestError`（该类型不可用；没有其他类型时 400 `unsupported_conversion`，消息带 URL 与原因）。到 Anthropic 的 URL 图片保持 `source{type:url}`（Anthropic 原生支持，不下载）。

### 9.3 工具

| 概念 | chat | responses | anthropic | gemini |
|---|---|---|---|---|
| 函数定义 | `tools[{type:function, function:{name, description, parameters, strict}}]` | `{type:function, name, description, parameters, strict}` | `{name, description, input_schema}`（无 parameters 时 `{type:object}`；`strict` 丢 other） | `functionDeclarations[{name, description, parametersJsonSchema}]`（直接放完整 JSON Schema；客户端 `parameters` 为 OpenAPI 子集时转 JSON Schema） |
| custom（自由文本） | `{type:custom}` | `{type:custom}` | 函数 `{input_schema:{type:object, properties:{input:{type:string}}, required:[input]}}`，调用的 `input.input` 还原为 custom 输入；grammar 丢 other | 同 anthropic 的包装 |
| 网页搜索 | `web_search_options` | `{type:web_search}` | `web_search_20250305`（`user_location` 映射，`search_context_size` 丢 other） | `{googleSearch:{}}`（参数丢 other） |
| 其他托管工具 | — | file_search、code_interpreter、computer、mcp、image_generation、shell 等：丢 tool | bash、text_editor、code_execution、computer、web_fetch、memory、`mcp_servers`：丢 tool | codeExecution、urlContext、fileSearch：丢 tool |
| `tool_choice` auto/none/required | `auto`/`none`/`required` | 同 | `{type:auto}`/`{type:none}`/`{type:any}` | `functionCallingConfig.mode` AUTO/NONE/ANY |
| 指定工具 | `{type:function, function:{name}}` | `{type:function, name}` | `{type:tool, name}` | ANY + `allowedFunctionNames:[name]` |
| allowed_tools | `{type:allowed_tools, allowed_tools:{mode, tools}}` | `{type:allowed_tools, mode, tools}` | 丢 tool（限制被放宽） | AUTO/ANY + `allowedFunctionNames`；Gemini 客户端 ANY 且多个名字 → chat/responses 的 allowed_tools(required)；VALIDATED → auto，丢 other |
| 调用 | assistant `tool_calls[{id, type:function, function:{name, arguments(字符串)}}]` | `function_call{call_id, name, arguments}` / `custom_tool_call{call_id, name, input}` | `tool_use{id, name, input(对象)}`；arguments 不是合法 JSON 对象 → `{}`，丢 tool | model 部件 `functionCall{id?, name, args}`；没有 id 时合成 `call_<sha1(轮次,序号)[:16]>` |
| 结果 | `tool{tool_call_id, content}` | `function_call_output{call_id, output}` | user 消息里的 `tool_result{tool_use_id, content, is_error}`，紧跟对应调用；`is_error` 到 chat/responses 丢 other | user 部件 `functionResponse{id?, name, response}`：name 按 id 回查调用；输出是 JSON 对象时原样，否则 `{"output": 文本}`；`is_error` → `{"error": 文本}` |
| 旧式 functions | `functions` / `function_call` | — | 同 tools | 同 tools |

工具 id：anthropic 要求 `^[A-Za-z0-9_-]+$`，不符的 id 确定性改写（非法字符换 `_`，后缀 8 位 sha1），同一请求内调用与结果一致；响应里的 id 原样。Gemini model 轮次的 `thoughtSignature` 见 §3.2。

### 9.4 推理

| 方向 | 映射 |
|---|---|
| chat `reasoning_effort` ↔ responses `reasoning.effort` | 同值 |
| effort → anthropic | `thinking:{type:enabled, budget_tokens}`：minimal/low 2048、medium 8192、high 24576、xhigh 32768；客户端给了输出上限时截到 `max_tokens-1`，结果 < 1024 则不开 thinking，丢 reasoning；`none` 不开 |
| anthropic → effort | `output_config.effort` 原值（`max` → `xhigh`）；否则 `budget_tokens` ≤ 2048 low、≤ 8192 medium、其余 high；`type:adaptive` → medium；`disabled` → 不写 |
| effort → gemini | 模型名 `gemini-2.` 开头：`thinkingBudget` minimal 0（pro 128）、low 1024、medium 8192、high 24576、xhigh -1（动态）；其他：`thinkingLevel` 同名（Gemini 不支持的档位就近取）；客户端要推理时 `includeThoughts: true` |
| gemini → effort | `thinkingLevel` 原值；`thinkingBudget` 0 → 不写、-1 → medium、≤1024 low、≤8192 medium、其余 high |
| responses `reasoning.summary` | 到 anthropic/gemini：有值即 thinking 可见 / `includeThoughts`；到 chat 丢 other |
| 历史里的推理 | 跨协议一律丢弃，记 reasoning（§3.2） |
| anthropic 工具循环 | 开 thinking 且最后一条带 tool_use 的 assistant 消息没有本家签名 thinking 块 → 本次不开 thinking，丢 reasoning（§3.2） |
| 到 anthropic 的 `max_tokens` 缺省（D5） | 客户端没给输出上限：网关设置 `claude_max_tokens` 开启时取 `models[客户端模型]`，没有该键用 `default`；关闭或都没有时用转换器兜底 8192（Anthropic 必填）；开了 thinking 时再取 `max(该值, budget_tokens + 1)`，且不低于 1280（new-api 规则）。客户端给了就原样，不改。转换器只管转换请求；原生 Anthropic 请求由 `claude_max_tokens` 开启后的内置参数覆盖补（CONTRACTS §84.1） |

### 9.5 结束原因

| IR | chat | responses | anthropic | gemini |
|---|---|---|---|---|
| Stop | `stop` | `completed` | `end_turn`、`stop_sequence`（带 `stop_sequence`） | `STOP` |
| ToolUse | `tool_calls`（旧式 `function_call`） | `completed` 且有调用项 | `tool_use` | `STOP` 且有 functionCall 部件 |
| Length | `length` | `incomplete` / `max_output_tokens` | `max_tokens`、`model_context_window_exceeded` | `MAX_TOKENS` |
| Filter | `content_filter` | `incomplete` / `content_filter` | `refusal` | `SAFETY`、`RECITATION`、`BLOCKLIST`、`PROHIBITED_CONTENT`、`SPII`、`IMAGE_SAFETY`、`LANGUAGE`；`promptFeedback.blockReason`（无候选） |
| Pause | `stop` | `completed` | `pause_turn` | — |
| Stop（其他） | `stop` | `completed` | `end_turn` | `OTHER`、`MALFORMED_FUNCTION_CALL`、`UNEXPECTED_TOOL_CALL` 等，响应侧丢 other |

写出时：IR Stop 但有调用块 → ToolUse；anthropic writer 的 Filter 写 `refusal`；`stop_sequence` 在 chat/responses 客户端没有对应字段，不记。

### 9.6 用量

| IR | chat | responses | anthropic | gemini |
|---|---|---|---|---|
| Input（含缓存） | `prompt_tokens` | `input_tokens` | `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` | `promptTokenCount`（+`toolUsePromptTokenCount`） |
| CacheRead | `prompt_tokens_details.cached_tokens` | `input_tokens_details.cached_tokens` | `cache_read_input_tokens` | `cachedContentTokenCount` |
| CacheWrite | — | — | `cache_creation_input_tokens`、`cache_creation.ephemeral_{5m,1h}_input_tokens` | — |
| Output（含推理） | `completion_tokens` | `output_tokens` | `output_tokens` | `candidatesTokenCount + thoughtsTokenCount` |
| Reasoning | `completion_tokens_details.reasoning_tokens` | `output_tokens_details.reasoning_tokens` | — | `thoughtsTokenCount` |

写出：anthropic `input_tokens = Input − CacheRead − CacheWrite`；gemini `candidatesTokenCount = Output − Reasoning`；`total` = Input + Output。

### 9.7 SSE：reader（上游 → IR）

| 上游 | 事件 → Delta |
|---|---|
| chat | 首块 → Start；`delta.content` → Text（首次先 BlockStart text）；`delta.reasoning_content`/`delta.reasoning` → Reasoning；`delta.refusal` → 拒绝块；`delta.tool_calls[i]` 带 id/name → BlockStart tool，`function.arguments` → Args；`finish_reason` 记下；`choices:[]`+`usage` → Usage；`[DONE]`/流结束 → 关块 + Finish；`{"error":...}` → Error |
| responses | `response.created` → Start；`output_item.added` reasoning/function_call/custom_tool_call → BlockStart；`content_part.added` output_text/refusal → BlockStart；`output_text.delta` → Text；`refusal.delta` → Text（拒绝块）；`reasoning_summary_text.delta`、`reasoning_text.delta` → Reasoning；`output_item.done`（reasoning，带 `encrypted_content`）→ ReasoningState + BlockStop；`function_call_arguments.delta`、`custom_tool_call_input.delta` → Args（done 只补后缀，同 §83.2）；`output_text.annotation.added` → Citation；`response.completed`/`incomplete` → Usage + Finish；`response.failed`/`error` → Error；服务端工具项 → Opaque；其余忽略 |
| anthropic | `message_start` → Start + Usage；`content_block_start` text/thinking/tool_use → BlockStart（redacted_thinking → Reasoning 块 + ReasoningState，无文本；server_tool_use、*_tool_result → Opaque）；`text_delta` → Text；`thinking_delta` → Reasoning；`signature_delta` → ReasoningState；`input_json_delta` → Args；`citations_delta` → Citation；`content_block_stop` → BlockStop；`message_delta` → Usage + Finish；`error` → Error；`ping` 忽略 |
| gemini | 每个 `data:` 是一个完整 GenerateContentResponse：首块 → Start（`responseId`、`modelVersion`）；`parts[]` 中 `thought:true` → Reasoning，`text` → Text，`functionCall` → BlockStart + 整段 Args + BlockStop（一次给全），`inlineData` image → Image 块，`executableCode`/`codeExecutionResult` → Opaque；部件上的 `thoughtSignature` → ReasoningState；`usageMetadata` → Usage（累计，取最后）；`finishReason` → Finish；`promptFeedback.blockReason` → Finish(Filter)；`{"error":...}` → Error |

### 9.8 SSE：writer（IR → 客户端）

| 客户端 | 输出 |
|---|---|
| chat | 首个内容前一块 `delta:{role:"assistant", content:""}`；Text → `delta.content`；Reasoning → `delta.reasoning_content`（D4；非流式为 `message.reasoning_content`）；拒绝 → `delta.refusal`；tool BlockStart → `tool_calls[{index, id, type:function, function:{name, arguments:""}}]`（custom → `type:custom`）；Args → `tool_calls[{index, function:{arguments}}]`；Citation → `delta.annotations`；Finish → `delta:{}` + `finish_reason`；`include_usage` 时再发 `choices:[]` + `usage`（之前每块 `usage:null`）；`data: [DONE]`；Error → 一块 `{"error":{...}}` 后结束，不发 `[DONE]`；Image/Opaque/ReasoningState 丢（image/tool/reasoning） |
| responses | Start → `response.created` + `response.in_progress`；文本块 → `output_item.added`(message) + `content_part.added`(output_text) + `output_text.delta`… + `output_text.done` + `content_part.done` + `output_item.done`；推理块 → `output_item.added`(reasoning) + `reasoning_summary_part.added` + `reasoning_summary_text.delta`… + done 系列（不带 `encrypted_content`）；工具块 → `output_item.added`(function_call/custom_tool_call, arguments "") + `function_call_arguments.delta`… + `.done` + `output_item.done`；拒绝 → `refusal.delta/done`；Citation → `output_text.annotation.added`；Finish → `response.completed`（Length/Filter 为 `response.incomplete` + `incomplete_details`），带完整 `output` 与 `usage`；Error → `response.failed`；每个事件带递增 `sequence_number`、`output_index`、`item_id`、`content_index`；Image/Opaque/ReasoningState 丢 |
| anthropic | Start → `message_start`（`id` 为上游 id，没有 `msg_` 前缀则加；usage 只有已知部分，未知为 0）；文本 → `content_block_start{type:text, text:""}` + `text_delta` + `content_block_stop`；推理 → `{type:thinking, thinking:""}` + `thinking_delta`（不发 `signature_delta`）；工具 → `{type:tool_use, id, name, input:{}}` + `input_json_delta{partial_json}`；custom 工具 Args 包成 `{"input": "..."}`；Citation → `citations_delta`；Finish → `message_delta{delta:{stop_reason, stop_sequence}, usage:{全部}}` + `message_stop`；Error → `event: error {type:error, error:{type, message}}`；Image/Opaque/ReasoningState 丢 |
| gemini | 每批增量 → 一块 `{candidates:[{content:{role:"model", parts}, index:0}], modelVersion, responseId}`；Text → `{text}`；Reasoning → `{text, thought:true}`；工具调用缓冲到 BlockStop 再发 `{functionCall:{id, name, args(对象)}}`；Finish → 最后一块带 `finishReason` 与 `usageMetadata`；Error → `data: {"error":{code, message, status}}`；JSON 数组流（无 `alt=sse`）由网关现有逻辑处理；Image 原样 `inlineData`；Opaque/ReasoningState 丢 |

非流式：reader.JSON / writer.JSON 用同一套块与映射；chat writer 只出一个 choice。

### 9.9 逐对要点

- **chat → responses（good，已有）**：映射见 CONTRACTS §83.2，不变。已有丢弃路径的类别：`tools`、`functions`、`function_call`、`tool_choice`、`web_search_options` 下的 → tool；`image_url.detail` → image；其余 other。新增：assistant 消息上的 `reasoning_content` → 丢 reasoning（原来按未知成员记 other）；响应侧推理摘要输出为 `reasoning_content`。
- **responses → chat（good）**：`instructions` → 第一条 system 消息；input 项：message → 同角色消息；连续的 `function_call`/`custom_tool_call` 合入前一条 assistant 消息的 `tool_calls`（没有就新建）；`function_call_output`/`custom_tool_call_output` → tool 消息（数组输出：文本拼接，图片按 9.2 移到 user 消息）；`reasoning` → 丢 reasoning；托管工具调用项 → 丢 tool；`item_reference` 不可能；`include` 丢 other。`text.format` → `response_format`，`reasoning.effort` → `reasoning_effort`。
- **chat / responses → anthropic（fair，重写）**：消息必须 user/assistant 交替：同角色合并；第一条不是 user 时前插 `{"role":"user","content":"."}`，丢 other；tool 结果合入同一 user 消息且排在其他内容前；空 assistant 消息删除（不记）。refusal → 文本。`max_tokens` 缺省按 9.4 末行。
- **anthropic → chat（fair）**：`system` 文本块拼接为一条 system 消息（`cache_control` 丢 other）；消息文本块的 `cache_control` 原样带到 chat 部件（D7）；thinking/redacted_thinking → 丢 reasoning；`tool_result` → tool 消息；`search_result` 块 → 文本，丢 other；`server_tool_use`/`*_tool_result` 历史 → 丢 tool；`top_k` 丢 other。
- **anthropic → responses（fair）**：`system` → `instructions`；thinking → 丢 reasoning；`tool_result` → `function_call_output`（含图片时输出为 `input_text`/`input_image` 数组）；`stop_sequences`、`top_k`、`cache_control` 丢 other。上游是 codex 时 streamOnly 照旧由 §83.1 处理。
- **chat / responses → gemini（fair）**：assistant → role `model`；连续 tool 结果合并为一条 user 内容的多个 `functionResponse`；所有系统消息进 `systemInstruction`；`response_format.json_object` → `responseMimeType` 无 schema；不加 safetySettings；URL 媒体下载内联（9.2）；model 轮次写 thoughtSignature 旁路值（§3.2）。
- **gemini → chat / responses（fair）**：`contents[].role` `model` → assistant、`user`/`function` → user；`thought:true` 历史部件与 `thoughtSignature` → 丢 reasoning；`fileData` http(s) 图片 → URL 图片，`files/…` URI → 丢 image（非图片丢 other）；`functionResponse` → tool 消息 / `function_call_output`（`tool_call_id` 按 id，没有 id 按同名调用的出现顺序配对）；`toolConfig` 见 9.3。
- **anthropic ↔ gemini（discouraged）**：组合，损失为两跳之和；无专门映射。
- **images / images_edit → responses（good，2026-11-19）**：请求、应答、流式事件与错误的映射表见 CONTRACTS §121.2、§121.7、§121.8。要点：顶层 `model` 是账号类型 `adapted` 选项的驱动模型（`Options.AdaptModel`，没有时转换器报配置错误），客户端模型与其余参数原样进 `tools[0]`（`ModelLocator` 指向 `tools.0.model`），`size`、`quality`、`background` 另写进一句 `instructions`；`n` ≠ 1、`response_format` 不是 `b64_json` 为 `RequestError`；`user`、`safety_identifier`、`style` 丢 other。应答以上游的结果为准：有几张图给几张，没有图时 `data` 为空、流里没有 `completed` 事件，失败的生图项与文字只记应答损失；只有上游自己的 `response.failed`、`error` 事件转成客户端的 `error` 事件。`ResponseModelLocator` 把用量规则读 `model` 的路径换成回显的生图模型（`tools.0.model`），记进 `upstream_model`。

## 10. 测试

### 10.1 黄金样例（`server/internal/gateway/convert/testdata`）

```
testdata/recorded/<protocol>/<case>.{request.json,response.json,stream.sse}   真实上游录制（原生），脱敏
testdata/golden/<from>~<to>/<case>/
    case.json            {options:{model, stream}, upstream:"recorded/gemini.stream_generate/tools.sse" 或路径列表（相对本目录或 testdata）,
                          media:{"<url>":"fixtures/x.png"}, claude_default_max_tokens:{"default":8192,"<model>":n}（夹具键沿用旧名，代表 claude_max_tokens 的 models）, note}
    in.request.json      客户端请求
    want.request.json    期望的上游请求；转换应失败时改为 want.request_error.txt（RequestError 原文）
    want.losses.json     {"tool":[...],"reasoning":[...],"image":[...],"other":[...]}（请求侧）
    upstream.response.json / upstream.stream.sse   （不引用录制时；本目录的上游文件总是使用）
    want.response.json / want.stream.sse           客户端看到的输出；响应转换应失败时为 want.response_error.txt / want.stream_error.txt
    want.response_losses.json                      非流式与流式两边的响应侧损失合并
testdata/ir/<upstream protocol>/<case>.{json,sse}.txt   reader 输出的 IR 快照（read_test.go），-update 重写
```

- 驱动 `golden_test.go`：遍历目录，跑 `Prepare` / `Response` / `NewStream`（逐事件喂），比对；`-update` 重写 `want.*`（人工审阅后提交）。规范化：32 位以上十六进制 id、`created`/`created_at` 等时间戳。媒体下载用 `case.json` 的 `media` 映射到本地文件的假 `MediaFetcher`，不联网。
- 生图两对的必备样例是另一张表（`goldenImagesRequired`）：`text_basic`、`no_instructions`、`two_images`、`partial_image`、`no_image`、`image_failed`、`image_and_failed`、`no_usage`、`error_midstream`、`error_event`、`lossy`（只要求 other）、`impossible`；`case.json` 的 `options.adapt_model` 给驱动模型。
- 每对必备样例（缺一项测试失败，由 `golden_coverage_test.go` 按清单检查）：`text_basic`、`image_input`（到 gemini 的对含 URL 图片下载）、`tools_call`（两个并行调用、参数分 ≥3 个增量）、`tool_roundtrip`（历史里有调用和结果）、`reasoning`（请求推理 + 响应推理文本 + 响应侧签名丢失记录 + 历史推理丢弃）、`finish_length`、`usage_cache`、`error_midstream`、`lossy`（每个类别至少一条）、`impossible`。到 anthropic 的对另有 `max_tokens_default`（含 thinking 时抬高）与 `thinking_tool_loop`；组合对另有 `compose_equals_chain`。
- 录制工具 `next/server/cmd/convert-record`（Go；放在 server 模块里才能引用 `internal/` 包）：按 `recorded/<protocol>/<case>.request.json` 用测试环境账号（环境变量给 base_url/key）请求真实上游，写 `response.json`/`stream.sse`；脱敏 id、签名换成固定假值。手动运行，不进 CI。`-live <from>~<to>` 模式把转换后的请求发给真实上游，只断言 2xx 与流完整：发布前在测试环境跑一遍（尤其 anthropic 工具循环 + thinking、Gemini 3 functionCall 的旁路签名）。
- strict 等价：`protocol-codec/strict/*_test.go` 里 strict 接受的输入（推理信封相关的除外）全部移植，新 chat/responses → anthropic 在 `strict` 策略下零损失、`want.request.json` 与 strict 输出语义等价（差异逐条写进样例的 `note`）。
- 模糊测试：每个 reader `FuzzRead<P>`（任意事件序列不 panic、不死循环）。

### 10.2 网关测试

- 路由（`gateway/convert_routing_test.go`）：等级选择与确定性（同类型多个可转端点）、流式相容（chat 流式选 stream_generate）、§83.3 同平台规则不变、WebSocket/任务不转换、所有类型都参与转换。
- 调度（同上）：priority 优先、同 priority 内原生先于转换先于 local、同 (priority, 等级) 内权重不变、粘性不变。
- 策略（`gateway/convert_policy_test.go`）：allow/safe/strict × 有无 tool/other 损失；账号 > 分组 > 默认；被拒绝不占槽、`attempts` 不增、不切换计数；全部被拒绝 400 `unsupported_conversion` 且 attempts 0；`RequestError` 跳过类型后原生账号成功；只考察到的类型才转换。
- 媒体（`gateway/convert_media_test.go`）：下载成功内联、超限、超时、SSRF 拒绝、MIME 不支持、同请求缓存。
- 使用记录：`conversion` 字段各情形（原生 NULL、转换、组合、rejected、截断）。
- count_tokens（`gateway/count_local_test.go`）：挑中原生账号转发、挑中 local 账号估算（不占限流额度、记录形状、无额外响应头）、priority 决定、gemini 两种请求体、估算数值（文本、工具、各类媒体常数）。
- 设置：`claude_max_tokens`（2026-11-14 前为 `claude_default_max_tokens`）、`media_download_max_mb` 读写与校验；账号/分组字段读写与校验、`/account-types` 的 grade/via、使用记录筛选；迁移测试。
- `go test -race` 全量（容器内）；web 单测与截图（账号编辑器、分组表单、网关设置、使用记录详情）。

## 11. 分工（4 个可并行子任务）

**第 0 步（T1 负责人，半天，单独提交）**：只放类型与签名，其余子任务以它为基准并行：`convert/convert.go`（Grade、Loss、Options、MediaFetcher、Converter、LossReporter、HopReporter、Registry.Resolve 签名与 PairInfo）、`convert/ir.go`（§3.1 全部类型与 Reader/Writer 接口）、`convert/pair.go`（pairConverter 骨架）。T2 在第 0 步之后独占 `ir.go`、`pair.go`。

| 子任务 | 内容 | 拥有的文件（只有拥有者改；别人需要时提给拥有者） |
|---|---|---|
| **T1 框架、调度、存储、设置、控制台、文档** | 接口与注册表实现、组合、路由等级与流式相容、priority 内按等级排序、惰性转换与策略判定、拒绝错误、使用记录、迁移、账号/分组/账号类型接口、网关设置两项、corefloor、控制台、CONTRACTS §88、ARCHITECTURE 6.6；最后把注册表切到新转换器并删除 `openai_anthropic.go` 的接入 | `server/internal/gateway/convert/{convert,prepared,compose,registry_test,compose_test}.go`；`server/internal/gateway/{routing,dispatch,forward,resource_response,streamonly,settings,sticky_api}.go`、新文件 `gateway/{convert_policy,convert_routing_test,convert_policy_test}.go`、`gateway/accounttypes_test.go`（fakeConv）、`gateway/strict_conversion_admission_test.go`；`server/internal/core/{ports_identity,ports_billing}.go`；`server/internal/account/{directory,handlers,types}.go`；`server/internal/group/group.go`；`server/internal/apikey/apikey.go`；`server/internal/usage/{settler,api,reconcile}.go`；`server/internal/migrations/0093_conversion_loss_policy.sql`；`sdk/manifest/check/corefloor.go`；`web/src/views/accounts/AccountEditor.vue`、`web/src/views/groups/GroupsView.vue`、`web/src/views/settings/GatewaySettingsCard.vue`、`web/src/views/usage/{UsageTable,UsageDetail,UsageFilters,usageFilters,usage}.*`、`web/src/api/types.ts`、`web/src/i18n/locales/{zh,en}/{accounts,groups,settings,usage}.ts`；`next/docs/{CONTRACTS,ARCHITECTURE}.md` |
| **T2 响应侧 IR 与测试框架** | 4 个 reader、4 个 writer（JSON + SSE）、通用对转换器、黄金样例驱动与覆盖检查、fuzz、录制工具 | `convert/{ir,pair,read,write}.go`（第 0 步之后）、`convert/read_{chat,responses,anthropic,gemini}.go`、`convert/write_{chat,responses,anthropic,gemini}.go` 及其 `_test.go`、`convert/{pair_test,read_test,write_test,golden_test,golden_coverage_test,fuzz_test}.go`、`convert/testdata/{recorded,fixtures,ir}/`、`next/server/cmd/convert-record/` |
| **T3 OpenAI/Anthropic 请求侧** | chat/responses → anthropic 重写（含 max_tokens 缺省、thinking 规则）、anthropic → chat、anthropic → responses、responses → chat、chat → responses 的损失类别与 `reasoning_content` 输出；内置注册表清单；对应黄金样例；本期末删除 `convert/openai_anthropic.go` 与 `next/protocol-codec`（含 `go.work`、`server/go.mod` 引用；先把 strict 用例移植成样例） | `convert/req_openai_anthropic.go`、`convert/req_anthropic_chat.go`、`convert/req_anthropic_responses.go`、`convert/req_responses_chat.go`、`convert/openai_chat_responses.go`、`convert/openai_chat_responses_test.go`、`convert/builtins.go`（内置清单，T4 往里加 gemini 项时提给 T3）、`convert/testdata/golden/{openai.chat~anthropic.messages,openai.responses~anthropic.messages,anthropic.messages~openai.chat,anthropic.messages~openai.responses,openai.responses~openai.chat,openai.chat~openai.responses}/`；删除 `convert/openai_anthropic.go`、`next/protocol-codec/`、`next/go.work` 与 `server/go.mod` 中的引用 |
| **T4 Gemini 请求侧、媒体下载、count_tokens** | chat/responses → gemini（两个上游协议）、gemini（两个客户端协议）→ chat/responses、推理档位、schema 互转、thoughtSignature 旁路值、markdown 内联图片拆分；`MediaFetcher` 实现；count_tokens local 路由的估算与响应（T1 留的接缝：`gateway/routing.go` 的包变量 `localCount func(c *call, ctx context.Context, rt *typeRoute) attemptResult`，为 nil 时不规划 local 路由；`gateway/convert_policy.go` 的 `mediaFetcher func(c *call) convert.MediaFetcher`，每个请求调用一次、结果供该请求所有转换共用，为 nil 时 `Options.Fetch` 为 nil；下载上限取 `c.gw.mediaDownloadMaxBytes()`；两个包变量由 T4 的文件在 `init()` 里赋值，或日后改为直接调用）；Gemini 与组合对黄金样例 | `convert/req_chat_gemini.go`、`convert/req_responses_gemini.go`、`convert/req_gemini_chat.go`、`convert/req_gemini_responses.go`、`convert/gemini_{thinking,schema}.go` 及测试、`gateway/convert_media.go`、`gateway/convert_media_test.go`、`gateway/count_local.go`、`gateway/count_local_test.go`、`convert/testdata/golden/` 下所有含 `gemini.` 的目录与 `anthropic.messages~gemini.*`、`gemini.*~anthropic.messages` |

依赖与顺序：第 0 步后四路并行。T3/T4 的请求侧与请求样例不依赖 T2；响应样例在 T2 的 reader/writer 合入后补。T1 的“注册表切换”在 T3 完成 anthropic 重写后做；count_tokens 的端到端在 T1 路由钩子与 T4 估算都合入后测。每个子任务末尾按编码规范自审，合入前 `go test -race` 全量与 web 单测。

## 12. 风险

1. **端点暴露面扩大（已定，D3）**：几乎每个分组都会服务四家协议的全部文本端点（只有 codex 账号的分组开始回答 `/v1/messages` 和 Gemini 端点，Claude 账号开始回答 Gemini 端点），没有退出机制；费用与上游风控（当时的 ccgateway 等订阅类上游，后来都已删除）都要观察。**修订（2026-11-17）**：这条风险由协议绑定收回，跨平台转换默认关闭，分组只回答成员账号开放的协议，转换要在账号上按模型开启（CONTRACTS §110、[DESIGN-2026-11-17-PROTOCOL-BINDING.md](DESIGN-2026-11-17-PROTOCOL-BINDING.md)）。升级到带协议绑定的版本后，存量的转换流量在配置之前返回 404。
2. **同 priority 下的分布变化**：同一优先级内原生账号先被用满，转换账号才接流量；需要转换账号分担时管理员要调优先级或权重（D2）。
3. **推理与签名**：推理状态不跨协议（§3.2），推理模型跨轮次会丢失上下文、质量下降；Anthropic 工具循环靠关 thinking 兜住；对话从转换路由换到原生账号时，客户端带回的无签名 thinking 可能被原生上游 400，只能靠粘性会话避免（与 new-api 相同）。必须用 `-live` 实测 Gemini 3 旁路签名。
4. **严格转为宽松（已定，D1）**：原来 400 的 chat → anthropic 请求会以有损方式成功；需要原行为的账号/分组设成 strict。
5. **max_tokens 缺省 8192**：没给上限的 OpenAI 客户端长输出会被截断（`length`），由管理员在网关设置按模型调大（D5）。
6. **媒体下载**：核心对外发起下载，有带宽、延迟与 SSRF 面；下载在挑选阶段进行，大文件会推迟首个上游请求（D6）。
7. **推理模型参数**：OpenAI 推理模型拒绝 `temperature`/`top_p`；本规格按原样透传，由上游报错（codec 曾按模型名丢弃，不沿用）。若线上常见，可在 responses 上游方向按 `reasoning` 存在时丢弃并记 other。
8. **Gemini schema**：直接用 `parametersJsonSchema`/`responseJsonSchema`，Gemini 1.5/2.0 等旧模型可能不支持（备选：清洗为 OpenAPI 子集 `parameters`，有损 other）。
9. **性能**：转换提前到挑选阶段，按顺序惰性进行；组合对做两次。大请求（多 MB base64 图片）在多次回退时 CPU 增加。
10. **缓存与费用（D7）**：OpenAI 客户端走 Anthropic 上游没有提示缓存，输入成本明显上升。
11. **只有 chat 的 OpenAI 兼容站点**：openai 的 API Key 类型声明全部 openai 端点为原生，responses 请求不会走 responses → chat（设计已知，账号级关协议不在本期）。**修订（2026-11-17）**：账号级关协议已实现。这类账号把“本平台协议”设成只开放 `openai.chat`（`protocols: ["openai.chat"]`），responses 请求就不会再调度到它（跳过原因 `protocol_closed`），由组内其他账号服务，都不提供时返回 404；它不会因此改走 responses → chat，要这种转换仍得用子集不含 responses 的账号类型（CONTRACTS §110、[DESIGN-2026-11-17-PROTOCOL-BINDING.md](DESIGN-2026-11-17-PROTOCOL-BINDING.md)）。
12. **第 2 期并行**：`AccountEditor.vue`、`GroupsView.vue`、`GatewaySettingsCard.vue` 可能与第 2 期冲突，合并时手工合并；迁移号与节号按 0093、§88。

## 13. 已定决策（用户 2026-11-09）

| # | 事项 | 决定 | new-api 的做法（只读行为，文件为 /home/erio/rc/new-api 内路径） | sup2api 的实现 |
|---|---|---|---|---|
| D1 | 现有严格转换（chat/responses → anthropic） | 改为宽松，默认 allow；账号/分组可选 safe、strict | — | §1.2、§9.9 重写；strict 用例移植为等价样例 |
| D2 | 调度顺序 | priority 优先，等级只在同一 priority 内排序；粘性不变 | — | §5.2 |
| D3 | 账号类型能否拒绝转换 | 全部自然接受转换，没有任何退出机制（同 new-api）。**2026-11-17 修订，见表后** | 渠道按格式自动转换 | §5.1 |
| D4 | chat 客户端的推理输出 | 输出 `reasoning_content` | 同（`relaykit/relayconvert/internal/claude_messages/to_oai_chat_resp.go` 等） | §9.8 chat writer；现有 chat → responses 一并输出 |
| D5 | 到 Anthropic 的 `max_tokens` 缺省 | 参考 new-api | 全局设置 `claude.default_max_tokens`：模型名 → 值的映射，必须有 `default`，默认 `{"default": 8192}`（`setting/model_setting/claude.go`）；转换时客户端没给就按模型取值（`relaykit/relayconvert/internal/oai_chat/to_claude_messages_req.go` 108–113、`oai_responses/to_claude_messages_req.go` 71）；有推理时至少 1280、手动 thinking 抬到 `budget + 1`（`internal/shared/claude/reasoning.go` 92–115）；原生 Claude 请求也补（`relay/channel/claude/adaptor.go` 50–53） | 网关设置 `claude_max_tokens`（2026-11-14 起：可开关的内置参数覆盖，默认关闭，`default` 不必填，CONTRACTS §84.1）：开启时原生请求也补（同 new-api），转换请求客户端没给时用它的值，关闭时转换器兜底 8192；thinking 时抬高规则同 new-api（§6.1、§9.4） |
| D6 | 到 Gemini 的 http(s) 图片 URL | 参考 new-api | 转 Gemini 时所有媒体都解析为 base64 `inlineData`（`internal/oai_chat/to_gemini_chat_req.go` 330–345、`internal/oai_responses/to_gemini_chat_req.go` 236、365）；URL 由 `service/file_service.go` `loadFromURL` 下载：SSRF 防护客户端（`service/download.go` 52–68），单文件上限 `MAX_FILE_DOWNLOAD_MB` 默认 64（`common/init.go` 193），失败或 MIME 不受 Gemini 支持时整个请求报错；user 文本里的 markdown data URL 图片拆成 `inlineData`（同文件 280–320） | `gateway/convert_media.go`：netguard SSRF 检查、直连或环境 HTTP 代理（不走账号代理）、单文件 `media_download_max_mb`（默认 64）、60 秒超时、同请求缓存；失败/超限/MIME 不支持 → `RequestError`（该类型跳过，没有其他类型则 400）；markdown 内联图片同样拆分；到 Anthropic 的 URL 保持 url 源（§9.2） |
| D7 | 提示缓存参数与标记 | 参考 new-api | 不做 OpenAI `prompt_cache_*` 与 Anthropic `cache_control` 的互转；`prompt_cache_key` chat ↔ responses 原样（`internal/oai_chat/to_oai_responses_req.go` 405、`internal/oai_responses/to_oai_chat_req.go` 81、112），Claude → chat 把消息文本块的 `cache_control` 原样带到 chat 文本部件（`internal/claude_messages/to_oai_chat_req.go` 184–189；system 块只对 OpenRouter 的 Claude 带，133–144）；chat → Claude 不带缓存标记 | 同 new-api：§9.1“缓存”行与说明；sup2api 没有 OpenRouter 方言，system 块的 `cache_control` 一律丢 other |
| D8 | 推理签名跨协议、换到原生上游 | 参考 new-api | 不做任何信封或清理：OpenAI 推理 → Claude thinking 块不带 signature（`internal/oai_chat/to_claude_messages_resp.go` 342–363、461–466）；Claude signature 不传给 OpenAI 客户端（`internal/claude_messages/to_oai_chat_resp.go` 88–90），`redacted_thinking` 记“continuation_state_lost”诊断（`internal/toolconv/response.go` 214–220）；请求转换不搬运别家推理历史；原生 Claude 渠道不清理请求；对话靠渠道亲和留在同一渠道（`service/channel_affinity.go`）；Gemini 转换的 model 轮次第一个 functionCall（或文本）部件写旁路签名 `context_engineering_is_the_way_to_go`，默认开启（`internal/shared/gemini/request.go` 64–95、`setting/model_setting/gemini.go` 53） | §3.2：推理只传文本；签名与加密推理记响应侧 reasoning 损失；请求侧丢弃别家推理；不生成信封（现有 `sup2api-anthropic-thinking-v1:` 停用）；原生路由不清理，靠粘性；Gemini 旁路签名同值；另加 Anthropic 工具循环关 thinking 规则防 400 |
| D9 | count_tokens 兜底与响应头 | 参考 new-api；不加任意响应头 | `/v1/messages/count_tokens` 照常选渠道（`router/relay-router.go` 106）；选中非 Anthropic 渠道回 404 不估算，Anthropic 渠道按 `claude_count_tokens_mode` 转发上游 / 转发指定渠道 / 本地估算，不占速率、不记用量、失败不重试（`relay/claude_count_tokens.go` 35–60）；本地估算 `service.CountRequestToken`（`service/token_counter.go` 59–170）：tokenizer 计 system、role、文本、tool_use 名与入参、tool_result 文本、工具定义（`relaykit/dto/claude.go` 288–418），图片按 `tokenkit/image.go` 规则（非流式默认不下载，按 1024×1024），文件 4096、音频 256、视频 8192；响应只有 `{"input_tokens": N}`，无额外头；Gemini `:countTokens` 一律 404（`router/relay-router.go` 222–229） | §8：照常挑账号，选中账号的路由决定转发还是本地估算（原生计数账号不可用时自然落到 local 路由的账号）；估算口径与媒体常数照 new-api；响应只有 `input_tokens` / `totalTokens`，不加响应头；与 new-api 的差别：按已批准的设计对没有计数端点的类型估算而不是 404，保留原生 Gemini 计数，不做按账号的计数模式 |

**D3 的修订（2026-11-17，用户要求“协议转换默认不支持，需要显式开启”）**：跨平台协议转换改为默认关闭、在账号上按模型显式开启；“同 new-api”不再适用（new-api 仍是隐式转换）。同平台转换（chat ↔ Responses）不算协议转换，默认照常，可以在账号上关闭。账号类型、插件、分组层面仍然没有开关。存量账号不回填，升级后依赖转换的请求返回 404，直到在账号上配置。规则见 CONTRACTS §110、[DESIGN-2026-11-17-PROTOCOL-BINDING.md](DESIGN-2026-11-17-PROTOCOL-BINDING.md)；D1、D2、D4–D9 不变。

与现有代码行为不同、发布说明里要写明的：转换“不可能”（`RequestError`）时不再立即 400，而是跳过该类型试其他类型，都不行才 400 且 attempts 为 0；`/account-types` 多出 Gemini/Anthropic/Responses 端点的转换项与等级；chat → anthropic 等原来被拒的请求改为有损成功；codex 的 chat 路径响应多出 `reasoning_content`；responses 客户端经 Anthropic 上游不再收到 `sup2api-anthropic-thinking-v1:` 推理信封。
