# 平台插件转发 V2：一条请求路径、核心提供网络客户端、用量只走返回值（合并设计稿）

2026-11-18。分支 `feat/forwarding-v2`（从本地 develop `9361c0417` 开出）。**已实现**（与协议绑定合并后的分支头 `26950a969`）。实现期间所有者又做了几项决定（第 2 节 D24–D33），之后一轮审查修正中又定了 D34（写死的超时与限制全部去掉），收尾时又定了 D35–D38（核心代发请求的目标主机与 `Host`、`SendAccountRequest` 的主机与大小、空值的权威头），正文相关各节已按它们改写；改写处的旧说法不再成立（第 8.2、8.4 节保留了原设计，并注明 D34 怎样改了它）。现行契约以 CONTRACTS §111、§112 为准，插件写法见 PLUGIN-DEVELOPMENT.md；代码与本文不一致而所有者没有决定的地方列在第 17 节。

本文取代两份草稿。草稿在实现完成、CONTRACTS §112 写好后已从目录删除（第 11.6 节），原文在 `26950a969` 里（`git show 26950a969:next/docs/<文件名>`）；下文引用它们的章节号指那份原文，其中的结论凡与 CONTRACTS §112 不同的以 CONTRACTS 为准：

- `DESIGN-2026-11-18-EXECUTE-USAGE-RETURN.md`（下称“用量草稿”）
- `DESIGN-2026-11-18-PLUGIN-FORWARDING.md`（下称“转发草稿”）

约定：

- 路径相对 `next/`，行号对应 `9361c0417`。两份草稿是在 `123282faa` 上核对的；`git diff --stat 123282faa develop` 显示其后只改了 `sdk/reqsign`、`sdk/awseventstream`、`plugins/vertex` 的密钥文件、两个 `go.mod` 和几份文档，网关、运行时、用量、清单代码逐字节未变。本文沿用草稿里这些文件的行号，并重新读了下列部分：`gateway/dispatch.go` 的 `attempt` 与 `forwardBuilt`、`gateway/execute.go` 全文、`gateway/websocket.go` 的建连与逐轮补丁、`gateway/settings.go` 与 `settings_api.go`、`gateway/pipeline.go` 的请求体读取与 `submit`、`plugin/grpcruntime/execute.go`、`plugin/egress/egress.go`、`plugin/declarative/declarative.go`、`sdk/reqsign` 全部、`sdk/manifest/upstream.go`、三个 proto 文件。没有重读而直接沿用的引用在第 15 节说明。
- “核心 V”指带本设计的第一个核心版本。已发布的最新 tag 是 `core-v0.1.303`；`CHANGELOG.md:9-22` 的“未发布”一节（云厂商插件、`signing`、`response_stream`，原定 0.1.304）还没上线。本设计如果赶在它之前合入，V 就是 0.1.304，否则是下一个版本。
- “平台插件”指声明账号类型、为网关请求服务的插件，共十个：anthropic、openai、gemini、relay、azure_openai、vertex、bedrock、claude_oauth、codex、volcengine。
- 本设计实现之后，所有者决定删除 claude_oauth 插件（CONTRACTS §116，迁移 0111），平台插件现在是九个。下文写到它的地方（行号、清单、矩阵、一致性套件）记录的是设计与实现时的状态，不再一一改写。

## 1. 背景与目标

今天一次网关请求在选定账号、改写请求体之后分成两条路（`server/internal/gateway/dispatch.go:620-638`）：

- **普通转发**：核心把插件声明的几个请求字段交给 `BuildUpstreamRequest`，插件回答 URL、请求头和请求体补丁（`BodyPatch`），核心应用补丁后发送、中继、按规则读用量。只有清单的插件连进程都没有，由核心进程内的声明式引擎代答。
- **插件执行**：插件的 `Execute` 回调核心的 `ForwardUpstream` 发请求，再回调 `RecordUsage` / `ReserveAndWatch` 上报，核心在回调里记账。

两条路各有缺点：普通转发下插件看不到完整请求体，Bedrock 这类要深改请求体、要对整个请求体签名的厂商只能靠核心加扩展点（`signing`）；插件执行靠回调上报，核心还要为“插件没回调”准备兜底。

用户的决定是把它们合成一条：

1. 核心把**完整请求体**交给插件，插件自己构造上游请求，经**核心提供的网络客户端**发出；插件不开套接字。
2. 用量和任务事实**只放在核心这次调用的返回值里**，删掉上报回调，核心不兜底。
3. **完全替换，不考虑兼容**（用户原话：“目前我们不需要考虑兼容性，项目还在开发阶段，所以旧逻辑可以全部删掉”）：旧的“声明字段加补丁”模式、没有进程的插件、核心里的声明式引擎、上报回调、核心兜底、`signing`、旧的任务轮询接口，在同一次改动里全部删除；核心和全部第一方插件从同一个提交构建、一起发布；不设计新旧版本并存时的行为，不留识别或降级旧插件的代码（`AGENTS.md` 开发规则 2）。

目标是一份可以直接拆给多个实现者并行做的设计：一条路径怎么走、删什么、设置怎么变、怎么发布、风险在哪。

## 2. 决定一览

来源：“用量”指用量草稿及用户对它的答复，“转发”指转发草稿及用户对它的答复，“主控”指本次任务说明与中途更正，“最终答复”指用户对本文初稿第 16 节三个问题的回答（记在第 16 节）。

| # | 决定 | 来源 | 状态 |
|---|---|---|---|
| D1 | 用量、任务事实只在返回值里：`Execute` 返回 `usage`、明确的 `host_usage`、`task` 或什么都不返回；`Monitor` 返回进行中或终态 | 用量 | 已定 |
| D2 | 删除三个上报回调 `RecordUsage`、`ReserveAndWatch`、`ReportTaskProgress` | 用量 | 已定 |
| D3 | 什么都没返回时核心不兜底：零费用的“未上报”记录、释放预扣；后台恢复按核心读数补记的逻辑删除 | 用量 | 已定 |
| D4 | 不考虑兼容：没有兼容期，没有识别或降级旧插件的路径，没有新旧版本并存的设计，没有过渡代码。旧机制在同一次改动里删除 | 主控最后一次更正（覆盖两份草稿里所有兼容性安排） | 已定 |
| D5 | 用量草稿的 A1（旧插件加载但不执行）、A2（为区分新旧包引入 v2 能力 ID）、A8（核心 V 上允许切回旧版本插件）**全部作废** | 同上 | 已定 |
| D6 | 能力 ID 不为区分新旧而改名：保留 `platform.adapter.v1`、`platform.monitor.v1`、`platform.tasks.v1`、`platform.websocket.v1`；删除 `platform.execute.v1`、`platform.poll.v1`（理由见 4.1、4.4） | 同上，本文选定 | 已定 |
| D7 | 契约版本如实上调：插件线协议 `ProtocolVersion` 1 → 2，`HostAPIVersion` 9 → 10（12.1）。用量草稿的 A3（不升）作废 | 同上，本文选定 | 已定 |
| D8 | A4、A5：`EstimateUsage` 可选；没实现时按系统设置 `pre_consume_tokens`、`output_reserve_tokens` 预扣。预扣流程与今天完全相同 | 用量 | 已定 |
| D9 | A6：提交失败不落库重试；A7：插件拿不到任务 ID 和回执 | 用量 | 已定 |
| D10 | 主架构是“核心提供网络客户端”：插件不开套接字，核心的客户端默认套上**账号**的配置（代理、传输设置、SSRF、允许的主机（取回的主机规则见 D27）、限流与并发、运营方的请求头设置），插件的显式选项只能在账号配置允许的范围内，不能绕过代理（落地时插件没有任何选项：`UpstreamOptions` 在收尾时删除，超时只归核心，见 5.4 的实现记录、CONTRACTS §112.3） | 转发、主控 1 | 已定 |
| D11 | 客户端分两级：(a) HTTP，第一阶段，即本次改动；(b) 经核心、套账号代理的 TCP 连接（裸套接字、WebSocket、gRPC），以后的阶段，不在本次改动里。**UDP 不做**：没有需求，现有隧道只有 TCP（5.6） | 主控 2；最终答复 3 | 已定 |
| D12 | B7：插件不设大小上限。请求体上限**只有一处**：系统设置 → 网关设置里的 `max_request_body_mb`。同名的环境变量（全局层、分组层、进程环境变量）和清单的 `request.maxBodyBytes` 删除，这条路径上没有别的请求体大小限制。原设计让核心据它推出内部传输上限（8.1、8.2），D34 取消了这一步：它只管网关读客户端请求的地方 | 主控 3；最终答复 1（“系统设置-网关设置里面配置就行了，其他地方不要限制”） | 已定；D34 修订 |
| D13 | B8：不用厂商 SDK 客户端，不做 RoundTripper 适配器。只用 AWS 的 `aws/signer/v4`、`aws/protocol/eventstream` 和 Vertex 断言用的 golang-jwt（官方 SDK 只作 e2e 测试客户端，D29） | 主控 4 | 已定 |
| D14 | B10：“声明字段加 `BodyPatch`”模式删除，所有平台插件改完，不留双路径；`common.proto` 的“请求体从不整个发给插件”删除 | 主控 5 | 已定 |
| D15 | B9、B11：收完整请求体不需要权限；安装审查页加一句固定说明 | 主控 6 | 已定 |
| D16 | 十个平台插件保持各自独立，键不变（之后所有者决定删除 claude_oauth，现在是九个，CONTRACTS §116） | 主控 7 | 已定 |
| D17 | **没有进程的插件取消**：`runtime: "none"` 不再支持；openai、gemini、relay、azure_openai 各自改写成普通的 gRPC 代码插件；不做通用声明式运行器，核心里不留声明式执行器；公共逻辑做成各插件链接的 Go 库 | 主控中途更正 | 已定 |
| D18 | B3：协议转换和运营方的请求体改写（模型映射、`param_override`、强制流式）留在核心，在请求体交给插件**之前**完成（D32） | 转发 | 已定 |
| D19 | B4：现在不做“插件自己读上游的流”；第二级的 TCP 以后会带来这个能力 | 转发 | 已定 |
| D20 | B6：核心的 `signing` 指令删除，插件用 `sdk/reqsign` 自己签；`response_stream` 留在核心 | 转发 | 已定 |
| D21 | B5 重做：只有一条请求路径，不继承执行路径今天的代价（每请求三次执行记录写入、插件崩溃切断在途的流）。例外：等待形式下插件崩溃时切断上游，这个请求不计费（12.4） | 主控 10；最终答复 2 | 已定 |
| D22 | 旧的任务轮询 `Poll`（`platform.poll.v1`）和两阶段 `BuildReconcileRequest` / `ParseReconcileResponse` 删除，任务观察只剩 `Monitor` | 主控要求核对后决定，本文选定（4.4） | 已定 |
| D23 | 插件内存默认不限制，只有管理员手动设置时才有上限：插件自己的资源限制（控制台/接口）原样生效，节点封顶 `SUB2API_PLUGIN_MAX_MEMORY_MB` 只在设置了时生效（默认不再是 1024），清单里的 `memoryMB` 只是插件自述的需要、不是上限。不按请求体上限推内存下限，不抬高也不压低管理员的值，请求体上限改了不替换插件进程；在途请求体字节准入删除，网关设置里的内存警告删除（8.3） | 用户（“插件的内存下限是按请求体上限算出来的：为什么要自动算？”；“不要限制”；“默认插件内存上限不限制！！除非管理员手动限制！！”） | 已定 |
| D24 | 核心对插件交回的请求体**不做运行时守卫**：不比较模型引用、流式标志、可计量性（4.5 原来的“守卫”取消）；这些由一致性套件第 3 条检查。模型可用性只比较 `UpstreamRequest.upstream_model` 与核心算的上游模型（修复包 A 补了另一处检查：运营方的覆盖改动了**交给插件的**请求体时，核心对它再强制一次流式、再按用量规则检查一次可计量性，不能计量就换号；插件交回的请求体仍不检查，CONTRACTS §112.2） | 所有者，实现期间 | 已定、已实现 |
| D25 | 请求头分三层：插件 `default_headers` → 运营方设置（透传只补没有的；`header_override`、`param_override` 可设值或删除）→ 插件权威 `headers`。已发布过的代码插件（claude_oauth、codex、volcengine；claude_oauth 之后删除，§116）改写后保持全部头为权威头；原声明式的五个类型只有鉴权头是权威头。一致性第 4 条检查“声明的集合”（`Credentials` ∪ `Authoritative`） | 所有者，实现期间 | 已定、已实现 |
| D26 | 插件内存（D23 的细化）：默认不限；只有管理员给插件设 `memory_mb` 或设节点封顶 `SUB2API_PLUGIN_MAX_MEMORY_MB`（默认不封顶）时才有上限；清单 `resources.memoryMB` 只是插件的自述。没有在途字节准入，没有 `max_request_body_safe_mb` 警告 | 所有者，实现期间 | 已定、已实现 |
| D27 | 取回的主机：清单 `externalServices` 之一（或子域），**或者是本账号自己的上游地址字段（`upstreamFields`）里的主机**；照样过 SSRF 规则和账号代理 | 所有者，实现期间 | 已定、已实现 |
| D28 | SDK 里只有一个 JSON 改写工具 `sdk/pluginsdk/jsonedit`，插件不写自己的编辑器 | 所有者，实现期间 | 已定、已实现 |
| D29 | 插件和核心都不用厂商的 SDK 客户端（只用 AWS 的签名、event stream 库和 golang-jwt）；OpenAI、Anthropic 的官方 SDK 只允许用作 e2e 测试的客户端（D13 的补充；`26950a969` 的 e2e 还没有用它们，之后的 AC36 用 openai-go 和 anthropic-sdk-go 作客户端） | 所有者，实现期间 | 已定、已实现 |
| D30 | 不做“每个插件 10 分钟内 20 次未上报就通知运营方”（核心没有运营方告警渠道的设置），推迟；每次未上报写一条 ERROR 日志。TCP 级（5.5）是以后的阶段，UDP 不做 | 所有者，实现期间 | 已定：推迟 |
| D31 | 任务提交没有返回 `task`：走任务失败的路径（零费用记录、释放预扣、客户端报错），不算“未上报”（落地时任务仍不登记、回执记失败原因，但这次请求的零费用记录统一由 `core.UsageRecord.SetUnreported` 清零，带 `usage_extract = "unreported"`；`error_type` 随后仍按返回客户端的错误记为上游错误，见 CONTRACTS §112.7） | 所有者，实现期间 | 已定、已实现 |
| D32 | 协议转换留在核心网关（`internal/gateway/convert`），插件收到的是转换后的请求体（D18 的确认） | 所有者，实现期间 | 已定、已实现 |
| D33 | 跨插件规则（4.3）只在注册表绑定时执行一次：被跳过的平台从绑定的账号类型的平台里去掉（记 ERROR 日志），清单检查另报 `foreign_facts` | 所有者，实现期间 | 已定、已实现 |
| D34 | **写死的超时与限制全部去掉**：一次请求上留下的界限只有管理员配置的，加上安全、钱算对和防止进程崩溃的检查。删除：插件在 `Execute` 里的分段计时（`segment_timeout`，8.4）、等待形式的总时限（6 小时加整体超时）和网关在观察后约 44.5 分钟的切断、等待形式缓冲的 64 MiB、取回的次数（8）与大小（回答 8 MiB、URL、请求头、方法名单、请求体）和未设置整体超时时的 5 分钟默认、`Monitor` 结果与执行观察的 512 KiB、宿主小调用的 4 MiB 与中继请求体的界限（8.2）、WebSocket 的上游消息 64 MiB / 每 Key 64 个会话 / 握手 30 秒 / 写 1 分钟、写客户端 60 秒、代理拨号与 TLS 的 15 秒、每实例 1024 个并发 `Execute`、`path_params` / `query` 的截断、SSE 单行 16 MiB、协议转换与只流式上游的 32 MiB、读用量的 32 MiB 缓冲（超出按 0 计费）、读用量插件看到的默认预算、模型名长度。代价是以前带着明确错误被拒绝的东西，现在表现为内存占用、存储增长或一个一直等着的请求；逐项的代价和仍然有效的界限见 CONTRACTS §112.16 | 所有者（“超时、限制都去掉”），审查修正期间 | 已定、已实现 |
| D35 | **插件交回的 `Host` 不生效**：中继里的 `host` 与 `content-length` 一样归传输层、拒绝（`INVALID_ARGUMENT`），取回一直拒绝；请求的 `Host` 永远是 URL 的主机。运营方的透传和覆盖本来就挡住 `host`，没有任何设置能改它。九个平台插件、`standard`、`reqsign` 都没有设 `Host`，SigV4 签的就是 URL 的主机 | 所有者（“修”），收尾审查 | 已定、已实现（CONTRACTS §112.17） |
| D36 | **核心代发的主请求只发往声明过的或账号配置的主机**：清单 `externalServices`（含子域）或账号 `upstreamFields` 字段的主机（不含子域），一处规则 `manifest.UpstreamHostAllowed`，中继（HTTP、WebSocket 握手）、取回、`SendAccountRequest` 共用；中继被拒按插件不可用换号、不罚账号（`NeutralReason` `upstream_host_not_allowed`）。“运营方填的上游地址”沿用已有的 `upstreamFields`，不按“账号配置里所有 URL”识别（密钥文件里有不是上游的 URL）。bedrock 声明 `amazonaws.com`、vertex 声明 `googleapis.com`。配额查询、凭证续期、测试与模型列表请求不在范围内 | 所有者（“修”），收尾审查 | 已定、已实现（CONTRACTS §112.17） |
| D37 | `SendAccountRequest` 按 D36 的同一条规则放行账号自己的上游地址（此前只认 `externalServices`）；请求体、URL、方法不限大小，它与配额查询、凭证续期的回答不再截到 64 KiB（`truncated` 核心不再设置；之后连同其他 `truncated` 字段从 proto 删除并 `reserved`，CONTRACTS §112.17） | 收尾审查（火山素材库接口需要；按 D34） | 已定、已实现（CONTRACTS §91、§112.17） |
| D38 | 权威头的空值表示“这个头不发”：删掉前面各层设置的同名头；默认头的空值不变。SDK `Upstream.RemoveHeader`；bedrock 用它在签名没有会话令牌时去掉运营方加的 `x-amz-security-token`。proto 不变 | 收尾审查（bedrock 需要） | 已定、已实现（CONTRACTS §112.17） |

## 3. 现状

只列本设计要动的部分。

### 3.1 两条请求路径

- 分叉点：`attempt` 做完账号加载、协议转换结果读取、模型映射、强制流式、三层请求体改写之后（`gateway/dispatch.go:538-612`），取插件声明的字段组装 `BuildUpstreamRequestRequest`（`:616-619`）；`canExecute` 为真走 `executeAttempt`（`:620-624`），否则调 `BuildUpstreamRequest`，时限是“平台调用超时”（`:626-627`），再 `forwardBuilt(..., nil)`（`:638`）。
- `canExecute` 的条件：客户端实现 `core.ExecutePlugin`、结算器实现 `core.ExecutionRecords`、清单声明了 `platform.execute.v1`；端点由插件解析用量或是任务提交时，还要求平台插件与账号类型插件是同一个（`gateway/execute.go:52-76`）。
- `forwardBuilt`（`dispatch.go:744-941`）两条路共用：应用补丁（`:755`）、声明式 URL 重渲染（`:760`）、SSRF 检查（`:764`）、第二次强制流式（`:775-780`）、补丁守卫（`:781`）、按账号代理取 HTTP 客户端（`:784`）、请求头分层（`:824-833`）、签名（`:834`）、模型可用性守卫（`:837-851`）、不跟随重定向（`:852-854`）、响应头超时与整体超时（`:861-871`）、发送（`:879`）、错误读取与分类（`:911-924`）、响应流格式适配（`:925`）、中继与用量（`:934-940`）。
- 执行路径多出来的东西：每个请求写 `plugin_executions` 一行并更新两次（`execute.go:182`、`:275`、`:325`；存储在 `usage/executions.go:22-48`、`:50`）；非 SSE 回答先缓冲、记账提交后才发（`execute.go:241-245`、`:133-148`，上限 `gateway/spool.go:14-15`）；插件放弃 `ForwardUpstream` 调用会切断正在进行的上游交换（`dispatch.go:802-805`、`execute.go:47-50`）；`Execute` 返回后没有记录时核心自己按读数记一次（`execute.go:115-132`），后台还有一遍恢复扫描（`usage/executions.go:120-154`）。
- 上报回调只在上游观察完成后才被接受，一次执行只能转发一次（`plugin/grpcruntime/execute.go:81-89`）。
- 普通转发路径的用量：核心在 `forward()` 里按规则读，`submit()` 交给异步结算（`gateway/forward.go:49`、`gateway/pipeline.go:825-875`）。端点声明 `usageSource: "plugin"` 时事后调平台插件的 `ExtractUsage`（`gateway/usageplugin.go:159-194`）；仓库里只有 volcengine 的两个端点这样声明（`plugins/volcengine/manifest.json:139`、`:259`）。
- WebSocket：每一轮消息都调账号类型的 `BuildUpstreamRequest`，第一轮的结果用来建连（URL、请求头），每轮都取补丁（`gateway/websocket.go:33-37`、`:688-760`、`:821-848`）；核心用账号代理的 HTTP 客户端握手（`:754-782`）。握手带 `signing` 或 `response_stream` 时按插件不可用处理（`:746`、`gateway/upstream_ext.go:124`）。
- 本地 `count_tokens`：路由标了 `local` 时不调插件、不发上游（`dispatch.go:568-570`、`gateway/count_local.go`）。

### 3.2 十个平台插件今天怎么工作

| 插件（版本） | 运行时 | 账号类型怎么构造请求 | 路径 |
|---|---|---|---|
| openai 0.4.4、gemini 0.3.2、relay 0.3.2、azure_openai 0.1.0 | `none`，没有进程（各自 `manifest.json:12`） | 清单里的 `upstream` 声明加预设，核心进程内的 `declarative.Client` 代答 | 普通转发；openai 另有 WebSocket |
| anthropic 0.3.4 | `grpc` | **也是声明式的**：`apikey` 类型声明 `upstream: {preset: "anthropic"}`（`plugins/anthropic/manifest.json:63`），插件代码里没有 `BuildUpstreamRequest`、`ClassifyError`；进程只提供模型目录和管理路由 | 普通转发 |
| claude_oauth 0.2.5、vertex 0.1.0、bedrock 0.1.0 | `grpc` | 代码里的 `BuildUpstreamRequest`（`plugins/claude-oauth/main.go:93`、`plugins/vertex/internal/vertex/upstream.go:113`、`plugins/bedrock/internal/bedrock/upstream.go:58`）；bedrock 的结果带 `signing` 和 `response_stream` | 普通转发 |
| codex 0.1.11 | `grpc` | `Execute` 就是 `ExecuteDefault`（`plugins/codex/internal/codex/upstream.go:259`） | 插件执行、WebSocket |
| volcengine 0.12.8 | `grpc` | 同上（`plugins/volcengine/internal/volcengine/execute.go:12`）；`Monitor` 是 `Poll` 加上报（`:18`） | 插件执行（含异步任务） |

任务说明里把“只有清单的插件”列为 openai、gemini、relay、azure_openai 四个；按账号类型算，声明式的是五个插件的六个类型，anthropic 的 `apikey` 也在内。`docs/PLUGIN-DEVELOPMENT.md:13`、`:65` 把 anthropic 写成“实现了 `BuildUpstreamRequest` 的 gRPC 插件”，与代码不符。

### 3.3 声明式上游

- 引擎 `sdk/upstream`（`upstream.go`、`build.go`、`check.go`、`template.go`、`validate.go`，共约 960 行）：把清单的 `accountTypes[].upstream`（结构在 `sdk/manifest/upstream.go:25-`）和核心预设（`sdk/platforms/upstream/{openai,anthropic,gemini}.json`）合并成 `Profile`，算出凭证校验、上游请求、测试请求、模型列表请求、错误分类（`sdk/upstream/upstream.go:1-12`、`:44-`）。
- 核心侧 `server/internal/plugin/declarative`：把 `Profile` 包成 `core.PlatformPlugin`（`declarative.go:26-58`）；`extend: true` 时再调插件修正（`:76-118`）。注册表在 `bindDeclared` 里给每个声明了 `upstream` 的账号类型换上它（`server/internal/plugin/registry/registry.go:386-407`）。
- 网关里只为声明式类型存在的代码：`core.DeclaredUpstream` 接口（`server/internal/core/ports_plugin.go:72-95`）；请求体改写的第一层 `prepareDeclared`（声明的过滤字段与 `paramOverride`，`gateway/param_override.go:66-77`）；URL 重渲染 `declaredTarget`（`:122-140`）；请求头拆分 `splitHeaders`（`dispatch.go:643-648`）；鉴权头最后再写一遍 `writeDeclaredAuth`（`:688-707`）。
- 没有进程的包由 `StaticInstance` 承载（`server/internal/plugin/grpcruntime/static.go:14-48`），发布流程在 `rollout/types.go:53-54` 分流；清单检查的 `runtimeNone`（`sdk/manifest/check/upstream.go:80-92`、`check/validate.go:349-353`）；打包工具对它跳过编译（`tools/sub2api-plugin/build.go:59-63`、`dev.go:280`）；控制台改资源限制时拒绝（`server/internal/plugin/api/ops.go:442-446`）。
- 黄金样例：`sdk/upstream/testdata/golden/{openai,anthropic,gemini,relay}.json`，分别 441、343、371、343 条，是当年四个代码插件改成声明式之前录下的回答，引擎必须逐条一致（`sdk/upstream/golden_test.go:21-25`）。azure_openai 没有黄金文件，行为由 `plugins/azure-openai/manifest_test.go` 固定。
- `extend: true` 模式在仓库里没有使用者（十个清单里没有一个 `"extend"`）。

### 3.4 插件的网络

- 严格模式下 seccomp 只允许 `AF_UNIX` 的 `socket()`（`server/internal/plugin/sandbox/seccomp_linux.go:29-33`、`:53`），插件开不了 TCP 也开不了 UDP。
- 对外连接只有 `EgressService.Dial`：每连接一条双向流，帧最大 32 KiB（`sdk/proto/sub2api/plugin/v1/host.proto:690-709`、`server/internal/plugin/egress/egress.go:35`）。
- **隧道今天不转发 UDP**。三处都写死了 TCP：协议注释 `DialOpen.network` 是 `"tcp" (udp is not supported)`（`host.proto:707`）；核心侧 `Dial` 对 `tcp`、`tcp4`、`tcp6` 之外的网络直接回“unsupported network”并拒绝（`egress.go:236-240`）；SDK 一侧同样只接受这三个值（`sdk/pluginsdk/egress/conn.go:28-32`，测试 `egress_test.go:247-249` 断言 `udp` 被拒）。插件的 DNS 查询也不是 UDP：SDK 把解析器改成经 TCP 连 `dns.sub2api:53`，由核心进程内应答（`sdk/pluginsdk/egress/egress.go:67`、`egress.go:246-258`）。`docs/ARCHITECTURE.md:63` 把“插件 UDP 访问”列在不做的范围里。
- **隧道里没有账号，也不套代理**：`ServerFor(pluginKey, policy)` 只按插件区分（`egress.go:187-192`），`DialOpen` 只有网络和地址（`host.proto:706-709`），核心检查完地址后直接拨（`egress.go:270-285`）。检查顺序是数据库例外、`net` 授权、域名策略、解析出的地址必须是公网且不是核心服务地址，检查过的地址就是拨号的地址（`egress.go:430-465`）。账号代理只存在于 `proxy.Service.HTTPClient`（`server/internal/proxy/proxy.go:289`），它只给出 `*http.Client`，没有“经某个代理拨一条连接”的方法。
- 隧道只给有 `net` 授权或自带数据库的插件注册（`server/internal/plugin/grpcruntime/instance.go:452-456`）。平台插件里 claude_oauth、codex、volcengine 持有 `net`（各自清单的 `hostPermissions`）。

### 3.5 大小、时限、并发

- 请求体上限**已经有一个设置**：环境变量 `max_request_body_mb`，层级是全局和分组，范围 1 到 1024，进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB`，未设置时 128 MB（`server/internal/core/environment.go:77-80`、`gateway/settings.go:27`）。端点可以在清单里声明 `request.maxBodyBytes`，生效值取两者中较大的（`gateway/pipeline.go:383-395`、`sdk/manifest/manifest.go:304`、`docs/CONTRACTS.md:5123`）；仓库里没有任何清单或内置平台定义用到这个字段。它在控制台的“系统设置 → 环境变量”页（`web/src/views/settings/EnvironmentSettingsCard.vue`），不在“网关设置”卡片里。同一个上限也用于 WebSocket 的单条消息（`pipeline.go:383`）。
- 网关只接受 JSON 请求体（`pipeline.go:413-450`）。
- “网关设置”卡片现有的数值项：平台调用超时、钩子默认超时、平台热路径超时、两组排队设置、媒体下载上限（`web/src/views/settings/GatewaySettingsCard.vue:34-43`）；存储是 `settings` 表的 `gateway` 行，结构 `GatewaySettings`（`gateway/settings.go:31-60`），默认值 `:69-73`，范围 `:76-91`，接口校验 `gateway/settings_api.go:62-102`。
- “平台调用超时” `platform_call_timeout_ms` 默认 2000，范围 100 到 30000（`settings.go:70`、`:79-80`）。今天用在：`BuildUpstreamRequest`（`dispatch.go:626`）、WebSocket 的建连构造与逐轮补丁（`websocket.go:732`、`:828`）、`ClassifyError`（`dispatch.go:1021`）、断开时的一次调用（`gateway/disconnect.go:47`）、`Execute` 的准备时限（`execute.go:91`，从调用开始到插件调 `ForwardUpstream` 为止，`plugin/grpcruntime/execute.go:183-191`、`:86`）。界面提示写的是“请求路径上调用插件平台接口（构造上游请求、解析用量）的超时”（`web/src/i18n/locales/zh/settings.ts:119`）。
- `Execute` 的总时限是 6 小时加 `relay_timeout_seconds`（`execute.go:89`）；转发结束后有固定 15 秒的收尾时限（`execute.go:91`、`grpcruntime/execute.go:94-105`）。
- gRPC 单条消息：核心调插件，go-plugin 客户端收发都是 `math.MaxInt32`（`github.com/hashicorp/go-plugin@v1.8.0/grpc_client.go:40-41`，版本见 `server/go.mod:25`）；插件的服务端默认只收 4 MiB，要插件自己用 `WithMaxRecvMsgSize` 调大（`sdk/pluginsdk/serve.go:80-84`、`:110-113`）；插件回调核心，核心侧服务端是 `grpc.NewServer(opts...)`，没有调大（`grpcruntime/instance.go:459`），所以也是 4 MiB。
- 调用并发按类别分开：热路径 64、控制台 16、后台 8、执行 1024（`grpcruntime/runtime.go:112-142`）。
- 插件资源：`GOMEMLIMIT` 是上限的 90%，`GOMAXPROCS` 是 `ceil(cpu)`（`sandbox/launcher.go:93-94`、`:168-170`）；每 5 秒采样，连续 3 次超过上限 110% 重启（`launcher.go:20-28`）；全局内存封顶 `SUB2API_PLUGIN_MAX_MEMORY_MB` 默认 1024（`server/internal/config/config.go:121`、`grpcruntime/instance.go:219-221`）；启动超时 30 秒、10 分钟内重启超过 5 次标记不可用、排空 30 秒（`grpcruntime/runtime.go:98-105`）。有进程的平台插件申请的内存：anthropic、bedrock、codex、vertex、volcengine 各 64 MB，claude_oauth 128 MB（各自 `manifest.json` 的 `resources`）。

### 3.6 签名与官方库（develop 上已变）

- `sdk/reqsign.Sign(req, payload, d, now)` 对 `*http.Request` 原地签名（`sdk/reqsign/reqsign.go:75-84`），实际签名由 `aws-sdk-go-v2` 的 `aws/signer/v4` 完成（`sdk/reqsign/sigv4.go:49-50`、`:76`）。包自己决定的只有三件事：只签 `host`、`x-amz-date`，以及存在时的 `content-type`、`x-amz-security-token`，办法是交给签名器一个只带 Content-Type 的裸副本、再把三个结果头抄回去（`sigv4.go:10-21`、`:56`）；查询串解析不了就报错（`:22-23`、`:66-68`）；签名器规范化后的查询串和主机写回请求（`:24-25`）。
- `sdk/awseventstream` 的分帧用 `aws/protocol/eventstream`（`sdk/go.mod:8-10`）；核心在 `adaptResponseStream` 里把 eventstream 转成 SSE（`gateway/upstream_ext.go:45-47`、`:104-119`）。
- 核心今天替插件签名：`signUpstreamRequest`（`upstream_ext.go:74-93`）。bedrock 的账号测试请求已经是插件自己签（`plugins/bedrock/internal/bedrock/upstream.go:101-120`）。
- vertex 的服务账号断言用 `golang-jwt/jwt/v5`（`plugins/vertex/go.mod:7`）。
- 这些都还没发布（本文开头说的“未发布”一节），删 `signing` 没有线上兼容负担。

### 3.7 预扣

与用量草稿 6.1 节相同，本设计不改：调度前 `c.precharge`（`gateway/precharge.go:35`），声明平台的插件有进程时调 `EstimateUsage`（`:49-62`），没实现时非任务端点用核心本地的 tokenizer，输入不低于 `pre_consume_tokens`、输出取 `output_reserve_tokens`（`server/internal/billing/settings.go:30`、`:34`）。`EstimateUsage` 的入参是端点声明的 `usageRequestFields` 和截断的提示文本（`sdk/proto/sub2api/plugin/v1/platform.proto:189-195`、`sdk/manifest/manifest.go:237-253`），不是完整请求体。

## 4. 目标架构：一条请求路径

### 4.1 名字

唯一的请求路径叫**插件执行**（今天的名字保留，“普通转发”这个名字消失）。对应的调用是 `PlatformService.Execute`，每个平台插件都必须实现。

能力 ID：平台插件声明的仍是 `platform.adapter.v1`，它的含义改成“实现新的平台契约（含 `Execute`）”。`platform.execute.v1` 删除：今天它用来区分“执行型”和“只构造请求”的插件，合并之后没有后一种，这个 ID 不再区分任何东西。不为了区分新旧包而引入 v2（D6）；旧包靠协议版本拒绝（第 9 节）。

### 4.2 一次尝试的步骤

每次尝试（一个账号一次）从头走一遍，换号后重新开始。

| 步 | 谁 | 做什么 | 相对今天 |
|---|---|---|---|
| 1 | 核心 | 鉴权、Key 限额、请求级钩子、价格、余额、预扣（可调 `EstimateUsage`）、路由规划、调度选号、账号并发与限流 | 不变 |
| 2 | 核心 | 读选号时算好的协议转换结果；账号模型映射；引用模型映射；强制流式；内置 `claude_max_tokens`；账号 `param_override` 的请求体部分，同时算出请求头的决定（`dispatch.go:571-612`） | 少了第一层“声明的过滤字段与 `paramOverride`”，它搬进插件（7.3） |
| 3 | 核心 | 模型可用性预检（`guardAvailability`） | 不变 |
| 4 | 核心 → 插件 | `Execute`：请求元信息、账号、**完整请求体**、白名单内的客户端请求头、尝试序号、执行令牌，以及两个标志：这个端点是否由本插件解析用量、是否任务提交 | 取代 `BuildUpstreamRequest` 和旧 `Execute` |
| 5 | 插件 | 构造上游请求：方法、URL、请求头、最终请求体字节；需要时自己签名（`sdk/reqsign`）。可以先用客户端的“取回”方式发辅助请求（换令牌、上传） | 新 |
| 6 | 插件 → 核心 | 调客户端的“中继”方式，把最终请求交给核心（第 5 节）。普通端点用**不等待**的形式，核心校验后立即返回“已接受” | 取代 `ForwardUpstream` |
| 7 | 插件 → 核心 | `Execute` 返回 `host_usage`（普通端点） | 取代 `RecordUsage(nil)` |
| 8 | 核心 | `Execute` 返回后才真正发出请求：SSRF、请求头分层、账号代理、发送、三个超时、断开处理、错误体读取、`response_stream` 适配、嗅探、强制流式收集、中继、按规则读用量 | 与今天的 `forwardBuilt` 第 764 行之后相同 |
| 9 | 核心 → 插件 | 上游出错时调 `ClassifyError`（独立的一元调用，时限是平台调用超时，失败用默认分类），按故障策略决定重试、换号或返回 | 与今天的普通转发相同（`dispatch.go:998-1031`） |
| 10 | 核心 | `submit()`：使用记录交给异步结算，结清预扣 | 与今天的普通转发相同 |

第 6 步“不等待”的含义要说准确：核心在这次宿主调用里只做本地校验（URL 合法、SSRF 名字检查、请求体不超限、请求头合法、`response_stream` 认识、类型声明了模型可用性时 `upstream_model` 一致；不检查请求体内容，D24），把请求挂在这次执行上，然后返回。**请求在 `Execute` 带着 `host_usage` 返回之后才发出**。这样安排的结果：

- 插件只在“构造请求”的几毫秒里被占用，流式回答期间插件不在调用里。插件崩溃、内存超限重启、升级换代都不影响在途的流，影响范围和今天的普通转发一样：只有正在构造的那几个请求按“插件不可用”换号（今天是 `dispatch.go:635-637`）。
- 没有执行记录：这条路上不写 `plugin_executions`，用量走普通的 `submit()`。每请求的数据库写入与今天的普通转发相同。
- 非流式回答不缓冲，照旧分块透传。
- **这条路上不存在“未上报”**。插件在返回前出任何问题，请求都还没发出，直接换号，没有白送的内容，也没有要兜底的账。D3 的“未上报”只会出现在下面的等待形式里。

要坦率说明的是：不等待的中继加上返回 `host_usage`，实质上等同于“插件把构造好的请求作为返回值交回”（转发草稿的方案二），只多一次本机的宿主调用。保留调用形式的理由是 D10：三种发送（中继、取回、以后的连接）只有“调客户端”这一个入口和一个校验点，插件可以在中继之前先取回、可以选等待形式，不需要两套写法。

### 4.3 等待形式：插件自己解析用量的端点和任务提交

端点声明了 `usageSource: "plugin"`，或者是任务提交端点时，插件必须看到上游回答的有界观察才能给出事实。这时第 6 步用**等待**形式：

| 步 | 谁 | 做什么 |
|---|---|---|
| 6' | 插件 → 核心 | 中继（等待）。核心立即发送、中继，结束后把有界观察（或错误事实）作为这次宿主调用的结果交还插件。与今天的 `ForwardUpstream` 相同（`execute.go:169-206`、`:224-266`） |
| 7' | 插件 | 解析观察，`Execute` 返回 `usage` 或 `task`；上游出错时返回 `classification`；解析失败时返回 `host_usage`（核心记 `usage_extract=fallback`，用量草稿已确认保留） |
| 8' | 核心 | 校验返回的事实，定价，在一个事务里提交（使用记录、扣费或预扣加任务登记、执行行改 `committed`），然后发布缓冲的回答 |

这条支路保留今天执行路径的机制，因为它们正是为“事实由插件给出”服务的：执行记录三次写入（意图、观察、提交）、非 SSE 回答先缓冲后发布、任务登记的原子性。它与今天的差别只有 D1 到 D3：事实从返回值来，没有回调，没有兜底。

**实现（审查修正后）**：恢复按存活判断。网关从观察落库起每 15 秒续一次执行行（`ExecutionRecords.RenewExecution`），恢复扫描只关闭 2 分钟没有续期的 `observed` 行（`core.ExecutionRenewInterval`、`ExecutionStaleAfter`）：活着的执行没有时长上限，崩溃节点的预扣约 3 分钟内释放。缓冲的回答是否发布只看转发本身是否完成，不看插件在 2xx 回答里读出的业务错误；观察没能落库时请求自己的零费用记录照常写、预扣立即释放；任务提交报失败前先确认提交是否已落库；插件选了 `host_usage` 而核心读数完全为空时按未上报处理（不等待形式下客户端已有回答，同样记零费用）。细节见 CONTRACTS §112.7。

- 没有合法的返回（没填、填错种类、`Execute` 报错、插件崩溃）且观察已落库：**未上报**。同步请求写一条零费用记录（`success=false`、`error_type=usage_unreported`、`billing_status=free`、`anomalies.usage_extract="unreported"`），同一事务里释放预扣，执行行改 `unreported`；非流式回答不发、回 503，流式内容已发出、连接异常结束。任务提交没有 `task`：任务不登记，客户端报错，预扣释放。细节照用量草稿第 4 节。
- 提交失败：核心拿着返回的事实在收尾时限内重试（同一执行 ID、同一摘要，重复提交是无操作）；仍失败则同步请求按未上报处理，任务提交报错。不落库重试（D9）。（落地后：确定性的拒绝 `core.ErrExecutionRefused` 和冲突不重试，CONTRACTS §112.7。）
- 插件在等待期间崩溃或放弃调用：**切断上游交换，这个请求不计费**（今天的做法，`dispatch.go:802-805`；用户已确认）。同步请求按上一条的未上报处理，任务提交报错、不登记。理由和与 D21 的关系见 12.4。
- （落地后修复包 A 补充）客户端离开：等待形式的 `Execute` 在中继开始发送之前随客户端离开取消，之后不取消。插件在转发的观察之后挂住时，这次执行只会随插件进程退出而结束；客户端离开不会结束它，因为事实还要提交（CONTRACTS §112.2）。
- 今天走这条支路的只有 volcengine 的视频端点和任务提交。codex 的端点由核心按规则读用量（它服务的是内置 openai 平台，没有 `usageSource: "plugin"`），改完后走 4.2 的不等待形式，不再写执行记录、不再被插件崩溃切流。

跨插件的情况：端点由插件 A 解析用量或是 A 的任务提交，而账号类型属于插件 B。今天这种组合退回普通转发，事后调 A 的 `ExtractUsage` 或 `ParseTaskSubmission`（`execute.go:59-66`、`usageplugin.go:159-194`、`gateway/tasks.go:118-180`）。只有一条路径之后 B 的 `Execute` 没法替 A 解析。**实现（D33）**：注册表绑定时执行一次，别的插件的账号类型声明了这样的平台，这个平台从被绑定的类型的平台里去掉（记 ERROR 日志，`registry.withoutForeignFacts`）；清单检查在被引用的平台可见时报 `foreign_facts`；事后提取的那段代码删除。仓库里没有这种组合（volcengine 平台只有它自己的账号类型）；仓库外有没有查不到（第 15 节）。

### 4.4 `Monitor`，以及删除旧的 `Poll`

核心认领到期任务后调 `Monitor`（租约、调度不变）。插件用客户端的取回方式查一次上游，把 `ReconcileResult` 放进返回值；核心在一个事务里重排或结算。没返回结果按一次查询失败处理。终态结果必须伴随这次调用里一次完整、成功的 HTTP 观察（今天在回调入口检查，`grpcruntime/monitor.go:37-40`，改到返回处）。能力 ID 仍是 `platform.monitor.v1`。

**旧的任务观察接口删除。** 今天任务观察有三种协议：`Monitor` 加回调、`Poll`（`platform.poll.v1`，结果在返回值里，`platform.proto:101`）、两阶段 `BuildReconcileRequest` / `ParseReconcileResponse`（`platform.proto:124`、`:132`）；核心按清单的能力选，`Monitor` 优先（`server/internal/usage/reconcile.go:401-417`、`:419-444`、`:446-`）。`Monitor` 改成返回结果之后，它与 `Poll` 在语义上完全相同（核心发起一次调用、插件经受控的 HTTP 查一次、结果在返回值里），留两个没有意义；两阶段形式 CONTRACTS §31.3 自己写的是“仅作为未声明 Monitor 的兼容通路”。仓库里唯一有任务的插件是 volcengine，它同时声明了 `platform.poll.v1` 和 `platform.monitor.v1`，`Monitor` 的实现就是调自己的 `Poll` 再上报（`plugins/volcengine/internal/volcengine/execute.go:18-25`），两阶段的两个方法在它代码里标着“保留旧接口”（`video.go:388`、`:418`）。决定：**只留 `Monitor`**，volcengine 现在 `Poll` 的函数体成为它的 `Monitor`。

### 4.5 运营方设置的顺序

| 时机 | 内容 | 谁 |
|---|---|---|
| 交给插件之前 | 协议转换（客户端协议到上游协议）、账号模型映射、引用模型映射、强制流式、`claude_max_tokens`、账号 `param_override` 的请求体部分。插件拿到的是上游协议、已经改写完的请求体 | 核心 |
| 插件里 | 厂商特有的改造：过滤字段、路由补丁、信封（Bedrock）、深度清洗、签名 | 插件 |
| 发送时，请求头自下而上 | 插件的“默认头” → 透传（账号 `header_passthrough`、`header_override` 的透传键、粘性规则，只补没有的） → `header_override` 与 `param_override` 的设值和删除 → 插件的“权威头” → 没有 Content-Type 时补默认值 | 核心 |
| 发送之后 | 请求体不再改 | — |

两点说明：

- 插件交给客户端的请求体字节是最终的。今天插件之后的第二次强制流式（`dispatch.go:775-780`）不再执行，保持流式标志是插件的责任；核心仍然校验（下一条）。（落地后修复包 A 补了一次“插件之前”的再强制：参数覆盖改动了请求体时，核心在覆盖的结果上再强制流式，覆盖不能把它改回假；插件之后仍不强制，CONTRACTS §112.2。）
- 今天的补丁守卫（`validatePatches`）删除，**不**改成比较交给插件的与插件交回的请求体（D24）：核心不做这个守卫，不解析插件交回的字节；模型引用一致、流式标志仍为真、合法 JSON、没有重复键由一致性套件第 3 条检查。运行时只剩模型可用性：类型声明了它时，`upstream_model`（非空时）必须等于核心算的上游模型，不一致不发出。

请求头分成“默认头”和“权威头”是为了不改变今天的行为：今天声明式类型的头在透传和覆盖**之下**，运营方能改（`dispatch.go:824-828`）；代码插件的头在**之上**，运营方改不掉（`:829`）；鉴权头最后再写一遍（`:830`）。**实现（D25）**：声明式类型改成代码插件后，库把原来“声明的头”放进默认头，只有鉴权头是权威头；已发布过的代码插件（claude_oauth、codex、volcengine）保持全部头为权威头；没发布过的 vertex、bedrock 只有凭证头（bedrock 签名类型是签名头）是权威头。透传只补没有的头，不替换默认头；`header_override`、`param_override` 可以设值或删除默认头。

### 4.6 签名

插件对自己构造的 `http.Request` 调 `reqsign.Sign`，把签完的 URL、Host、请求头抄进交给客户端的请求里。（落地时改为 `reqsign.SignAWSSigV4` 与 `AWSSigV4Headers`，方案表删除；`Host` 不交给核心，永远取 URL 的主机，见 D35、CONTRACTS §109.1、§112.17。）之后会不会被核心改坏：

- 请求体：之后不变（4.5）。
- 被签名的四个头和 `Authorization` 放在权威头里，写在运营方的透传和覆盖之后，改不掉。`host` 另在覆盖的禁止名单里（`sdk/paramoverride/headers.go:22`）。
- 其余的头不在签名范围内（`sigv4.go:10-21`），核心照常加减。
- 插件必须给出它签名时用的 Content-Type，否则核心补的默认值不在签名里。
- URL：插件给的 URL 核心会重新解析再发（今天 `dispatch.go:764`、`:814`）。路径里的 `%3A` 这类转义往返后是否逐字节不变，要用黄金样例测试确认（第 15 节）。
- 时间：`x-amz-date` 在构造时生成，构造到发出之间只有本机的几步；每次尝试重新构造。

### 4.7 故障策略

每次 `Execute` 是一次尝试，核心裁决不变（`gateway/failure_policy_runtime.go:241-271`）：

- 插件在请求被接受之前失败（报错、超时、崩溃、交回的请求不过核心的发送前检查）：按插件不可用换号，账号不受罚。（落地后修复包 A 让发布了故障策略时也这样：插件不可用、被拒的中继、SSRF 拒绝的地址、不被允许的主机都只换号、不执行账号规则，尝试用尽时结果同没有策略，CONTRACTS §58。）
- 不等待形式下的上游错误：核心读错误体，调 `ClassifyError`，应用分类；故障策略下传输错误完全由策略决定、不问插件（`dispatch.go:1000-1003`）。“请求是否已写出”仍由核心的 `httptrace` 得到（`dispatch.go:875-884`）。
- 等待形式下的上游错误：错误事实在宿主调用的结果里，插件在 `Execute` 的返回值里给 `classification`，没给用默认分类（今天 `execute.go:96-107`）。
- 取回方式的请求失败：事实是核心看到的，插件可以在返回值里给 `classification`，核心按同样的规则裁决；这种失败发生在主请求发出之前，可以安全换号。
- 插件不重试、不换号。一次执行最多一个中继。

### 4.8 其他入口各自怎样

| 入口 | 今天 | 之后 |
|---|---|---|
| WebSocket（openai、codex 的 Responses WS） | 每轮调 `BuildUpstreamRequest`：第一轮建连并取补丁，以后每轮取补丁 | 每轮调 `Execute`，请求体是这一轮的完整消息，元信息里的传输是 `websocket`。插件调中继（不等待），给出 URL、握手头和最终消息，返回 `host_usage`。核心在会话还没有上游连接时用 URL 和握手头建连（账号代理、握手超时不变），之后每轮只用消息，URL 不同也不另开连接（今天的规则，`websocket.go:818-820`）。握手头只套 `header_override`，透传头不生效（今天的规则，`docs/PLUGIN-DEVELOPMENT.md:39`）。（落地时：握手的 30 秒时限按 D34 删除，握手只随客户端离开结束；握手头按 HTTP 的分层（`layerHeaders`）去掉透传：插件的默认头 → `header_override` 与 `param_override` 的请求头部分 → 插件的权威头（空值删除，D38）；首轮的请求头覆盖和请求体改写在 `Execute` 之前一起做，失败时这个账号换号、不握手。见 CONTRACTS §108.2、§112.4、§112.16。）WebSocket 端点不允许插件解析用量（清单检查已有），所以只有不等待形式；给了 `response_stream` 按插件不可用处理 |
| 本地 `count_tokens` 和其他本地路由 | 不调插件 | 不变 |
| 账号测试、模型列表 | `BuildTestRequest`、`BuildModelsRequest`，核心发送（`account/testreq.go:128`、`:246`，`account/models.go:186`） | RPC 不变。声明式类型原来由核心代答，改由插件用公共库实现 |
| 凭证续期、额度、余额 | 成对的 `Build…Request` / `Parse…Response`，核心发送（`account/refresh.go:304`、`:321`，`account/quota.go:282`、`:292`） | 不变 |
| 凭证校验、错误分类 | `ValidateCredentials`、`ClassifyError` | RPC 不变；声明式类型改由插件用公共库实现 |
| 任务轮询 | `Monitor` 加回调，或旧的 `Poll` | 4.4 |
| 预扣估算 | `EstimateUsage` | 不变（3.7） |
| 模型解析 | `ResolveModel`（平台热路径超时） | 不变 |

成对的“插件描述请求、核心发送、插件解析回答”这几组 RPC 不属于“声明字段加补丁”模式：请求体是插件自己写的，本来就完整。它们以后可以并到客户端的取回方式上，但那是独立的简化，不在这次范围内。

## 5. 核心网络客户端

### 5.1 两级

| 级 | 是什么 | 核心看得到明文吗 | 账号代理 | 阶段 |
|---|---|---|---|---|
| (a) HTTP 客户端 | 插件给出完整的 HTTP 请求（或 WebSocket 的握手加消息），核心完成交换 | 看得到；系统设置由核心强制，用量由核心直接从上游读 | 套 | 第一阶段，随核心 V |
| (b) TCP 连接 | 插件经核心的隧道拨一条 TCP 连接，凭这次执行的令牌；TLS 在插件与上游之间 | 看不到，只有主机、端口、字节数 | 套 | 以后的阶段，另行立项，不在本次改动里 |

本次改动只做 (a)。(b) 本文给出要补什么和约束，不排进这次的任务。UDP 不在范围内（5.6）。

### 5.2 (a) HTTP 客户端的两种方式

**中继**：回答交给客户端。核心从上游读回答、中继给客户端、按规则读用量。一次执行最多一次。分不等待（4.2）和等待（4.3）两种形式。等待形式下插件得到的只是有界观察（今天的 `ForwardUpstreamResponse`，`host.proto:339-347`），不是回答本身。

**取回**：回答交还插件，有界、非流式。用于多步流程（换令牌、上传、预检）和任务查询。就是今天的 `ExecuteHTTP`（`host.proto:226-231`、`:370-385`），适用范围从“轮询和监控期间”扩到“`Execute` 期间”。

消息草图（字段名是建议，最终以 W1 为准）：

```protobuf
message UpstreamRequest {
  string method = 1;                        // 默认 POST
  string url = 2;                           // 绝对 http(s) 地址；WebSocket 轮次是握手地址
  map<string, string> headers = 3;          // 权威头：写在运营方设置之后
  map<string, string> default_headers = 4;  // 默认头：写在运营方设置之前
  bytes body = 5;                           // 最终字节；WebSocket 轮次是这一轮的消息
  string upstream_model = 6;                // 记入使用记录
  string response_stream = 7;               // 如 "aws-eventstream-chunk"
  UpstreamOptions options = 8;
}
message UpstreamOptions {                   // 只能收紧，见 5.4
  int32 response_header_timeout_ms = 1;
  int32 total_timeout_ms = 2;
}
message RelayUpstreamRequest {
  string execution_token = 1;
  UpstreamRequest request = 2;
  bool wait = 3;                            // true：等中继结束，返回观察
}
message RelayUpstreamResponse {
  ExtractUsageRequest observation = 1;      // 只在 wait 时
  ClassifyErrorRequest error = 2;           // 只在 wait 时
}
```

`ExecuteRequest` 改成直接携带元信息、账号、完整请求体、客户端请求头、尝试序号、令牌和两个标志；`ExecuteResponse` 与 `MonitorResponse` 按用量草稿 3.2 节（`oneof report { usage | host_usage | task }`，`MonitorResponse.result`）。

### 5.3 默认值全部来自账号

插件不传任何选项时，客户端按这次执行绑定的账号行事，与今天核心自己发请求时相同：

| 项 | 默认 | 位置（今天的代码） |
|---|---|---|
| 代理 | 账号的代理；没有代理则直连，直连时拨号拒绝私网地址 | `dispatch.go:784`、`proxy.go:177-193`、`:289` |
| 传输 | 每个代理一个连接池，HTTP/2；拨号与 TLS 握手原有的 15 秒按 D34 删除，由操作系统的连接超时和网关的响应头计时约束 | `proxy.go:177-192` |
| SSRF | 直连检查解析出的地址；经代理检查名字（落地后修复包 A 改为经代理时本机不解析，只拒绝字面的私网地址和 `localhost`，`netguard.CheckHostVia`，CONTRACTS §43.4） | `dispatch.go:764`、`gateway/ssrf.go:15-20` |
| 重定向 | 不跟随 | `dispatch.go:852-853` |
| 超时 | 响应头超时、整体超时、流式空闲超时，取账号、分组、全局三层环境变量 | `dispatch.go:861-871`、`:900-902` |
| 限流与并发 | 这次执行已经占了账号的并发槽位和 RPM/TPM 额度；取回方式的请求算在同一次执行里，不另占 | `dispatch.go:496-535` |
| 运营方的请求头设置 | 中继：分层如 4.5。取回：**不套**透传和覆盖，这些设置定义的是“客户端的请求转给上游时”的行为，辅助请求不是那个请求 | `dispatch.go:824-833` |
| 断开处理 | 中继：按断开策略取消或读完计费 | `dispatch.go:798-801`、`gateway/disconnect.go` |
| 日志与轨迹 | 中继记入尝试链路；取回的每个请求记方法、主机、路径、状态码、字节数，不记请求头和内容 | — |

允许的主机：

- 中继：与今天相同，只有 SSRF 规则。账号的上游地址是运营方填的（relay、azure_openai 这类没有固定主机），所以不能再加一张主机名单。（落地时改了：收尾审查后中继与取回用同一条主机规则，`externalServices` 或本账号 `upstreamFields` 的主机，见 D36、CONTRACTS §112.17。）
- 取回：主机必须在清单 `externalServices` 之内（或其子域），**或者是本账号自己的上游地址的主机**（账号类型 `upstreamFields` 声明的字段，只比主机名、不含子域；实现，D27）。账号地址是运营方填的、可能指向私网，所以取回照样经账号代理、过 SSRF 规则（按账号所在范围的“允许私网”）。清单没有 `externalServices`、类型也没有地址字段的插件不能用取回。

### 5.4 插件能传的选项和边界

> 实现记录：SDK 设置超时的方法删除之后，`UpstreamOptions`（`UpstreamRequest.options` 字段 8、`ExecutionHTTPRequest.options` 字段 6）在转发 V2 收尾时从协议删除（`reserved`），下表第一行不再适用：超时只归核心（CONTRACTS §112.3、§112.6）。

| 选项 | 允许 | 不允许 |
|---|---|---|
| 响应头超时、整体超时 | 比账号的生效值更短 | 更长；设为不限 |
| 方法、URL、请求头、请求体 | 插件构造 | `content-length`、`accept-encoding`、`connection`、`transfer-encoding` 由传输层决定（今天 `dispatch.go:654-657`） |
| 代理 | — | 不能选、不能关。有代理的账号的请求一律经它 |
| TLS | — | 不能关校验、不能换根证书 |
| 重定向 | — | 不能打开 |
| 重试 | — | 客户端不重试；重试和换号归核心 |
| 取回的次数和大小 | 不限（D34；原设计是每次执行最多 8 个请求、请求体受请求体上限约束、回答最多 8 MiB 超出带 `truncated`） | — |

有一条现有行为要明说：权威头写在运营方的请求头覆盖之后，所以插件**能**盖掉运营方设置的头。这是今天代码插件的行为（`dispatch.go:829`，CONTRACTS §86.5），签名需要它。约束靠约定和一致性测试：新插件的权威头里只放鉴权头和被签名的头，其余放默认头；已发布过的代码插件保持原样（D25），一致性第 4 条只检查权威头是否在插件声明的集合里。

### 5.5 (b) 经核心的 TCP 连接：要补什么

隧道今天不知道账号、不套代理（3.4）。设计：

- **拨号凭证**：`DialOpen` 增加可选的 `execution_token`。就是这次 `Execute` 或 `Monitor` 的执行令牌：32 字节随机数，绑定插件进程、账号和这次调用，调用结束即失效（`grpcruntime/execute.go:33-61`）。SDK 的拨号器从调用的 context 里取，插件不经手。
- 带凭证的拨号，核心一侧依次：由令牌得到账号和代理；目标主机必须在清单 `externalServices` 之内；SSRF（直连检查解析出的地址，经代理检查名字，“允许私网”用账号所在范围的开关）；有代理时核心连到代理，HTTP 代理发 CONNECT、socks5 走握手，之后转发字节；出口日志带上账号。`proxy.Service` 要加一个“经某个代理拨一条 TCP 连接”的方法（今天只有 `HTTPClient`）。
- 不带凭证的拨号：行为不变（按插件的 `net` 授权和域名策略直连），留给与账号无关的后台工作。
- 需要的授权：带凭证的拨号不要求 `net`，权限来自令牌，主机受 `externalServices` 限制。
- TLS 在插件与上游之间。后果要写进契约：这条连接上的流量核心看不到也改不了，系统设置里请求头和请求体那部分核心无法强制；用量只能由插件给（返回 `usage`，不能用 `host_usage`）；回答要到客户端，还需要一条“插件写给客户端”的流式通道。后两件事就是 D19 说的“以后再做”的内容，第二阶段立项时一起设计。
- WebSocket、gRPC 都是这条 TCP 连接上的协议，由插件自己说。注意第一阶段的 WebSocket（4.8）是 (a) 级：核心握手、核心逐轮中继、核心读用量，与这里不是一回事。

### 5.6 UDP：不在范围内

现有隧道只转发 TCP（3.4）：协议、核心侧、SDK 三处都只接受 `tcp`、`tcp4`、`tcp6`，插件的 DNS 查询也走 TCP，沙箱不允许插件自己开 UDP 套接字。十个平台插件没有一个需要 UDP。用户的答复是现在不做、没有需求，所以本设计不为 UDP 做任何设计或预留：隧道、`host.proto:707` 的注释、`docs/ARCHITECTURE.md:63` 的“不做插件 UDP 访问”都保持原样。以后真有需求时另行立项。

## 6. 用量与预扣

- 事实只在返回值里（D1）。`Execute`：`usage`、`host_usage`、`task`、不填；`Monitor`：`result`、不填。proto、摘要与幂等、提交失败的重试、未上报的处理，都照用量草稿 3.2 到 3.5 节和第 4 节，不重复。
- 相对用量草稿的变化只有适用范围：它写的是“执行型插件只有 codex 和 volcengine，第二条路径不改”。现在所有平台插件都走 `Execute`，但其中由核心读用量的端点走不等待形式，`host_usage` 在请求发出之前就返回了，核心照旧在 `forward()` 里读、在 `submit()` 里结算。所以用量草稿里“未上报不计费”的钱的后果，只落在等待形式上，也就是今天 volcengine 的那两类端点，范围没有因为合并路径而扩大。
- SDK 的默认执行流程（`ExecuteDefault`）按请求里的两个标志选形式：端点由本插件解析用量或是任务提交时用等待形式并返回 `usage` 或 `task`，否则用不等待形式并返回 `host_usage`。用它的插件不会漏填。
- 预扣与今天完全相同（D8、3.7）。任务提交时 `task` 里的 `usage.reserve` 是第二次、更准的预估，核心登记任务时原子地替换请求前的预扣，入口从 `ReserveAndWatch` 改成返回值。
- `EstimateUsage` 的入参不改成完整请求体：它在调度之前、对每个请求调一次，改了就是每请求多一次完整请求体的进程间拷贝，而现有的唯一实现（volcengine 的视频估算）用声明的字段就够。
- 未上报的缓解措施：每次一条 ERROR 日志（请求 ID、执行 ID、插件、账号、原因）。控制台筛选与人工补记没有做；“按插件统计 10 分钟内未上报数，达到 20 发通知”推迟（D30：核心没有运营方告警渠道的设置）。不自动禁用插件。

## 7. 取消没有进程的插件（D17）

### 7.1 规则

`runtime` 只剩 `grpc`（`js` 仍是保留值）。清单里出现 `runtime: "none"` 或 `accountTypes[].upstream`，清单检查报错。核心里没有任何代码替插件构造上游请求。

### 7.2 四个插件的改写，加上 anthropic

openai、gemini、relay、azure_openai 各自变成普通的 gRPC 代码插件：`main.go` 加 `internal/<name>` 包，清单 `runtime: "grpc"`、有 `entry.grpc.binaries`、有 `resources`。anthropic 已经有进程，把 `apikey` 类型的平台实现加进现有的 `Plugin`。

插件键、账号类型 ID、表单、凭证字段都不变（`openai`/`apikey`，`gemini`/`apikey`，`relay`/`relay_key`，`azure_openai`/`azure_v1`、`azure_deployment`，`anthropic`/`apikey`），所以**账号数据不需要迁移**。变的只是包：从只有清单变成带二进制，版本号升，`hostCompat` 下限提到核心 V。清单里保留的声明：账号类型及其 `platforms`（端点、`passHeaders`、`usage` 规则、`streamOnly`）、表单、`externalServices`、能力。删掉的：`upstream` 整块、`requestFields`。

### 7.3 公共库：不写五份一样的代码

新包 `sdk/pluginsdk/standard`（暂名），是链接进每个插件自己进程的 Go 库，不是运行器。它实现“API Key 账号 + 基础地址 + 按协议的路由 + 请求头鉴权 + 错误分类策略”这一类账号类型的全部平台接口，各插件只用 Go 写配置。

API 草图：

```go
// 一个账号类型的全部行为。字段与今天清单 upstream 块一一对应
// （sdk/manifest/upstream.go:25-），只是从 JSON 模板变成 Go 值和函数。
type Type struct {
    ID              string
    Spec            apikey.Spec           // 密钥校验、基础地址的默认值/必填/去后缀、提示文案（现有）
    Auth            Auth                  // Header{Name, Format} | Query{Name} | None
    Headers         []Header              // Set / Default / Append，值是 func(*Request) string
    Routes          map[string]Route      // 键是上游协议 ID，如 "openai.chat"
    DefaultProtocol string
    FilterFields    []string              // 发送前从请求体删掉的路径
    Override        *paramoverride.Override // 原清单里声明的 paramOverride
    Classify        classify.Policy       // 现有
    Test            func(*apikey.Config) *TestRequest
    Models          *apikey.ModelList     // 现有
}
type Route struct {
    Method    string
    Path      func(*Request) string       // 可以含模型、部署名
    Transport string                      // "http" | "websocket"
    Headers   []Header
    Shape     func(body []byte, r *Request) ([]byte, error) // 路由补丁，按路径改写，不整份解析
}

// New 返回实现 pluginsdk.Platform 与 Executor 的值：
// ValidateCredentials、Execute（经 ExecuteDefault）、ClassifyError、
// BuildTestRequest、BuildModelsRequest。
func New(types ...Type) *Platform
```

三个厂商的预设变成 Go 函数，放在现有的 `sdk/pluginsdk/providers/{openai,anthropic,gemini}`，内容从 `sdk/platforms/upstream/*.json` 逐项搬过来：

```go
// plugins/openai/internal/openai
func New() *standard.Platform { return standard.New(openai.APIKey("apikey")) }

// plugins/relay/internal/relay：anthropic 预设加四处差异
// （基础地址必填、它自己的文案、两个额外的客户端请求头、没有默认协议）
func New() *standard.Platform {
    t := anthropic.APIKey("relay_key")
    t.Spec.BaseURLRequired = true
    ...
    return standard.New(t)
}

// plugins/azure-openai/internal/azureopenai：两个类型，都从 openai 预设出发，
// 鉴权头改成 api-key，路由按 v1 接口和按部署的经典接口各自改写
```

每个插件的 `main.go` 只有 `pluginsdk.Serve(x.New(), pluginsdk.WithManifest(manifestJSON))`；`internal` 包是一个构造函数加测试。

**实现**：上面是草图。实际的 `standard.Type` 没有 `Override`（三个预设和五份清单里都没有声明过清单固定改写），路由的改写是 `Route.Edit`（用 `jsonedit`，D28），`Headers` 的写法是 `standard.Set` / `Default` / `AppendTo`，另有 `PassHeaders`、`Model`、`WebSocket`；接口与例子见 `sdk/pluginsdk/standard/README.md`，原清单字段与库的对应见 PLUGIN-DEVELOPMENT.md 第 3 节。

**行为必须与今天逐字节相同**，验收办法：

1. 黄金样例是验收依据。四个文件共 1498 条（3.3），在删除之前转换成不依赖引擎、也不依赖 `BuildUpstreamRequest` 消息的样例，连同回放辅助放在 `sdk/pluginsdk/standard/goldentest`（W2，已完成；结构、用法、例外都写在该目录的 `README.md`）。旧样例的输入是声明的字段、期望是 `BuildUpstreamRequestResponse`（URL、请求头、补丁）；转换后的 `execute` 样例输入是账号、元信息、客户端请求头和完整请求体，期望是最终的上游请求：从字段合成请求体，把期望的补丁应用上去得到期望的最终请求体，再把请求头分成权威头（携带凭证的那一个）和默认头（其余）。`test`、`models`、`validate`、`classify` 四类的消息不变。转换程序是 `sdk/upstream/golden_convert_test.go`，输出已冻结，它与旧引擎一起删。1498 条全部转换、没有跳过；其中 49 条的期望值是今天的行为而不是当年的录制（relay 的 40 条账号测试；openai、gemini 对别的类型的账号照常作答的 9 条），逐条带说明。
2. 新旧对照：同一组输入分别过旧引擎和新库，比较“最终发给上游的方法、URL、请求头、请求体”。旧引擎这一半已经在 W2 做完（`TestConvertedGoldenReplay`：转换后的每一条按核心的用法经今天的引擎回放，全部一致；它与 `TestGoldenConversion` 一个出自引擎、一个出自录制，是防转换程序自己出错的第二道检查）。新库这一半是 W4 用同一批样例调 `goldentest.Run`，全部通过才允许进入删除。
3. azure_openai 没有黄金文件，把 `plugins/azure-openai/manifest_test.go` 里对路由、鉴权头、`api-version`、三种资源地址的断言移植成对新插件的断言。
4. anthropic、openai、gemini、relay 各自的 `manifest_test.go` 里依赖 `sdk/upstream` 的断言同样移植。这五份文件里要移植的断言已逐条导出在 `sdk/pluginsdk/standard/goldentest/MANIFEST-ASSERTIONS.md`。

有一处行为**做不到完全相同**，要事先说明：今天声明的过滤字段和声明的 `paramOverride` 在核心里先于 `claude_max_tokens` 和账号的 `param_override` 执行（`param_override.go:82-91`）。改成代码插件后，这一层在插件里、排在运营方的改写**之后**。运营方的参数覆盖恰好写了一个被声明过滤的字段时：今天这个值保留，以后被插件删掉。黄金样例不覆盖这种组合（它们只测构造），所以没有一条样例会因此不同。W2 已经列出三个预设和五份清单里实际声明的内容（goldentest 的 README）：没有任何一处声明 `paramOverride`；anthropic、openai、gemini、relay 的清单把过滤字段清空了；只有 azure_openai 的两个类型沿用 openai 预设的 `service_tier`、`safety_identifier`。所以这处变化今天只在 azure_openai 上可见：运营方在账号的参数覆盖里设置这两个字段之一时，今天发给上游，以后被插件删掉。写进 CONTRACTS 新章节和 CHANGELOG。

### 7.4 从声明式世界来的 SDK 库，哪些留下

| 包 | 去留 | 说明 |
|---|---|---|
| `sdk/pluginsdk/apikey`（`Spec`、`Validator`、`Config`、`ModelList`、`NormalizeBaseURL`、`ForwardHeaders`） | **留** | 今天 vertex、volcengine 直接在用（`plugins/vertex/internal/vertex/credentials.go:14`、`plugins/volcengine/internal/volcengine/platform.go:39`），新库也建在它上面 |
| `sdk/pluginsdk/classify`（`Policy`、`Rule` 和规则构件） | **留** | bedrock、codex、vertex 在用 |
| `sdk/pluginsdk/providers/{anthropic,openai,gemini,volcengine}` | **留**，并增加各自的预设构造函数 | bedrock、claude_oauth、vertex、volcengine 在用 |
| `sdk/pluginsdk/accountform` | 留 | vertex、bedrock 的表单 |
| `sdk/paramoverride` | 留 | 核心的账号参数覆盖用它；新库的 `Override` 也用它 |
| `sdk/reqsign`、`sdk/awseventstream` | 留 | 前者只剩插件用；后者核心（中继）用 |
| `sdk/upstream` | **删** | 引擎本身 |
| `sdk/platforms/upstream` | **删** | 预设 JSON，内容搬进 `providers/*` 的 Go 代码 |
| `sdk/manifest/upstream.go` 里的 `Upstream` 及其子结构 | **删**；`RuntimeGRPC` 常量搬到 `manifest.go` | |

### 7.5 `sdk/platforms` 下的平台定义

`sdk/platforms/{anthropic,openai,gemini}.json` 是核心内置平台的定义（端点、用量规则、`passHeaders`、默认粘性规则），不是插件的声明。**保留，位置不动**。理由就是它包注释里写的两条（`sdk/platforms/platforms.go:1-10`）：里面的协议 ID 是插件写 `accountTypes[].platforms` 时依据的契约；清单检查在核心、打包工具、插件自己的测试里都要用它判断端点冲突。搬回核心会让打包工具和插件测试够不着。

要改的只有一处：删掉平台级的 `requestFields`（`anthropic.json:110`、`openai.json:205`），它是“声明字段”模式的一部分。用量规则留在这里和清单里，因为读用量的是核心（D20）。

### 7.6 旧的只有清单的包

清单检查不再认识 `runtime: "none"` 和 `upstream` 块：这样的包上传时被拒绝，已经装着的在加载时被拒绝（第 9 节），不做别的处理。

### 7.7 进程数、内存、启动、开发环境

**进程数**。默认发布包里启用的内置插件是 anthropic、openai、gemini、moderation、codex（`deploy/docker/build-go.sh:165`）。今天其中有进程的是 anthropic、moderation、codex 三个；之后 openai、gemini 也各有一个，每个节点多 2 个进程。装齐十个平台插件的节点，平台插件进程从 6 个变成 10 个。进程数与账号数、请求量无关。

**内存**。实际占用没有测量。Go 写的 gRPC 插件空闲时的常驻内存按经验在一二十 MB 的量级（**未测量**）；按 20 MB 估，每节点默认多 40 MB，装齐多 80 MB。更大的变化来自完整请求体（第 10 节）：平台插件今天申请 64 MB，看门狗在连续 15 秒超过 110% 时重启，而请求体上限默认 128 MB，一个大请求就能超。按 D23，插件进程默认不限内存，清单里的数不再是上限，只有管理员设置的上限才生效（8.3）。

**启动**。每个进程启动超时 30 秒（`grpcruntime/runtime.go` 的 `StartTimeout`），实际启动时间没有测量，预计几百毫秒。核心启动和每次内置同步多启动两个到四个进程。

**`sub2api dev`**。开发环境的内置目录默认是空的（`server/internal/devenv/devenv.go:58-60`），插件由开发者自己装，以普通子进程运行、没有沙箱（`:201-205`）。影响：以前装 openai、gemini 只要打包清单，以后要先编译；`sub2api-plugin build` 和 `dev` 里跳过 `runtime none` 的分支删除（`tools/sub2api-plugin/build.go:59-63`、`dev.go:280`）；镜像构建多编译四个插件的各平台二进制（`deploy/docker/build-go.sh:76-84` 调的 `build-demo.sh` 按目录自动发现，不用改列表）。

## 8. 系统设置与界面

### 8.1 请求体上限：只在网关设置里

现状：上限是环境变量 `max_request_body_mb`（3.5），有全局层、分组层和进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB`，显示在“环境变量”页和分组编辑器里；端点还可以在清单里声明 `request.maxBodyBytes`。

用户的决定（原话：“系统设置-网关设置里面配置就行了，其他地方不要限制”）：**上限只有一处，在“系统设置 → 网关设置”里。** 环境变量和它的分组层删除，清单字段删除，插件不设上限，这条路径上不留别的请求体大小限制。

**新设置**

- `GatewaySettings.MaxRequestBodyMB`，JSON 名 `max_request_body_mb`，整数，单位 MB，**默认 128，范围 1 到 1024**。默认值是今天的生效默认值（2026-11-08 从 32 MB 调上来，`docs/CONTRACTS.md:5123`），降低它会让已有的大请求开始收到 413；范围是今天环境变量的范围，代码里没有要求改它的理由（gRPC 单条消息的硬上限约 2 GiB，1024 MB 在其内；与同一张卡片里“媒体下载上限”的范围相同）。任务说明起初建议的 64 MB 不采用。
- 它限制两样东西，都是今天 `bodyLimit()` 管的：HTTP 请求体（`readBody`，`pipeline.go:397-405`；超出回 413 `request_too_large`，不变）和客户端 WebSocket 连接上的单条消息（`websocket.go:115` 的 `SetReadLimit`；超出时连接以 1009 关闭，不变）。`bodyLimit()` 简化成读 `c.gw` 的这一个字段：`c.gw` 在鉴权和读请求体之前就已装好（`pipeline.go:211-216`），不增加任何读取。
- 没有分组层、账号层，没有进程环境变量。所有分组、所有端点用同一个值。
- 生效：与卡片里其他项相同，设置缓存 10 秒（`settingsTTL`），保存时经 `config:changed` 立即失效；对已经打开的 WebSocket 连接不追溯（上限在升级时设定）。

**服务端**（`server/internal/gateway`）

- `settings.go`：结构加字段（`:31-60`）；`defaultGatewaySettings()` 加 `MaxRequestBodyMB: 128`（`:69-73`）；范围常量 `minMaxRequestBodyMB = 1`、`maxMaxRequestBodyMB = 1024`（`:76-99`）；`normalized()` 里非正数回到默认值、其余夹到范围内（与 `MediaDownloadMaxMB` 同样写法）；加 `maxRequestBodyBytes()`；常量 `defaultMaxBodyBytes`（`:27`）删除。存储的行里没有这个字段时取默认值（`loadGatewaySettings` 是在默认值上反序列化）。
- `settings_api.go`：输入结构加 `*int` 字段，`validate` 里 `check("max_request_body_mb", ..., 1, 1024)`，超出范围报 `out_of_range`（与卡片里其他项相同），`applyTo` 写入。`GET/PUT /settings/gateway` 的路径和权限不变。
- `settings_plugin.go` 与 `sdk/protocol/settings.go` 的 `protocol.GatewaySettings`（插件经 `HostService.GetSettings` 读到的只读镜像，CONTRACTS §107）加同名字段。
- `pipeline.go`：`bodyLimit()` 如上；端点声明的分支随 `request.maxBodyBytes` 一起删。

**界面**（`web`）

- `views/settings/GatewaySettingsCard.vue`：`DEFAULTS` 和数值行各加一项 `{ key: 'max_request_body_mb', unit: 'MB' }`；`api/types.ts` 的 `GatewaySettings` 与 `GATEWAY_SETTINGS_RANGES` 加一项（后者的类型会强制要求）；`mock/billing.ts` 的网关设置和范围加一项。
- `i18n/locales/{zh,en}/settings.ts` 的 `settings.gateway.fields` 与 `hints` 各加一条。中文：“请求体上限”，提示“客户端请求体（以及 WebSocket 单条消息）的最大字节数，超过的请求返回 413。对所有分组和端点生效。”英文对应。
- “环境变量”页和分组编辑器不再有这一行：两处的行都由服务端的变量表生成，删掉变量即可，不改组件。

**删除环境变量**

- 定义：`sdk/protocol/environment.go:10` 的 `EnvVarMaxRequestBodyMB`；`server/internal/core/environment.go:26` 的别名和 `:77-80` 的表项（名称、说明的中英文就写在表项里，没有单独的 i18n）；`Environment.Bytes()`（`:177-178`，唯一的调用者是 `bodyLimit`）；单位 `mb` 随之没有使用者，`web/src/api/types.ts:307` 的 `'mb'`、`settings.environment.units.mb`（zh `:95`、en `:96`）、`web/mock/environment.ts:17-19` 一并删。
- 进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB`：`config.environmentEnv()` 只遍历已声明的变量，表项删掉后它不再被读取。部署里设过它的要改到网关设置里，写进 CHANGELOG；核心不为它保留识别或告警代码（D4）。
- 测试：`gateway/environment_test.go` 的 `TestEnvironmentRequestBodyLimit` 改写成对网关设置的测试；`gateway/pipeline_test.go:540` 的 `maxBodyBytes` 用例删除；`gateway/round4_test.go:289-293`、`:372-375` 整体比较设置结构的地方加新字段；`core/environment_test.go`、`environment/environment_test.go`、`environment/settings_plugin_test.go`、`group/environment_test.go`、`config/config_test.go:214-215` 里拿这个变量当例子的地方换成别的变量（它是唯一一个“只有全局层和分组层”的变量，换的时候注意层的断言）。

**数据：需要一个迁移**

环境变量的值存在两处：全局层是 `settings` 表 `key = 'environment'` 那一行的 JSON，分组层是 `groups.environment` 列（迁移 0065）；账号层从来不允许这个变量，`accounts.environment` 里不会有。变量删除后读取不受影响（解析只遍历已声明的变量，多余的键被忽略），**但保存会坏**：`GET /settings/environment` 和分组详情把存着的键原样返回，控制台把整份值原样回传，而 `ValidateEnvironmentValues` 对未声明的键报 `invalid`（`core/environment.go:117-118`），并且没有任何一行能显示这个错误。也就是说，设过这个变量的部署，升级后“环境变量”页和那些分组会保存不了。所以要迁移，新文件 `server/internal/migrations/0106_request_body_limit_setting.sql`（目前最新是 0105；`AGENTS.md` 写的“目前到 0104”已经过时）：

1. 全局层设过值的，把它写进 `gateway` 行的 `max_request_body_mb`（行不存在就建一行只含这个键的，其余字段读取时取默认值）。这样运营方调过的上限升级后不变。
2. 从 `environment` 行删掉这个键。
3. 从每个分组的 `groups.environment` 删掉这个键。**分组层的值丢弃**，没有地方可去；升级说明里要让运营方先查一遍哪些分组设过（`SELECT id, name, environment->'max_request_body_mb' FROM groups WHERE environment ? 'max_request_body_mb'`），确认全局值够用。

```sql
INSERT INTO settings (key, value)
SELECT 'gateway', jsonb_build_object('max_request_body_mb', (value->>'max_request_body_mb')::int)
FROM settings
WHERE key = 'environment' AND jsonb_typeof(value->'max_request_body_mb') = 'number'
  AND (value->>'max_request_body_mb')::numeric BETWEEN 1 AND 1024
ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value;
UPDATE settings SET value = value - 'max_request_body_mb' WHERE key = 'environment' AND value ? 'max_request_body_mb';
UPDATE groups SET environment = environment - 'max_request_body_mb' WHERE environment ? 'max_request_body_mb';
```

（草稿，没有运行过；只改数据不改表结构，不需要重新生成模型。先例：`0102_drop_ccgateway_and_legacy.sql:184-192` 用同样的写法从 `scheduling` 行删键。）只在进程环境变量里设过上限的部署迁移不到，升级后是默认的 128 MB。

**这条路径上与请求体大小有关的全部限制**

逐个核对了公网入口到上游发送之间的代码（节点外壳、核心的 HTTP 服务、`server/internal/gateway`、核心与插件之间的 gRPC、清单、插件）。结论：今天真正限制客户端请求体的只有 `bodyLimit()` 一处；其余要么由它推出，要么是别的种类的限制。

| 位置 | 今天 | 之后 |
|---|---|---|
| `gateway/pipeline.go:386-405`：HTTP 请求体 | `bodyLimit()`：环境变量与端点声明取大，否则 128 MB；超出 413 | **就是那一个设置** |
| `gateway/websocket.go:115`：客户端 WebSocket 单条消息 | 同一个 `bodyLimit()`；超出以 1009 关闭 | **同一个设置** |
| 环境变量 `max_request_body_mb`（全局层、分组层）与 `SUB2API_MAX_REQUEST_BODY_MB` | `bodyLimit()` 的输入 | **删除** |
| 清单 `endpoints[].request.maxBodyBytes`（`sdk/manifest/manifest.go:304`） | `bodyLimit()` 的输入；没有任何清单或内置平台用它，清单检查也不校验它 | **删除** |
| 节点外壳的公网入口与转发（`gateway/cmd/sub2api-gateway/main.go:652`、`gateway/internal/proxy/router.go:303-333`） | 不限制请求体，流式转发不缓冲；只有请求头的限制（Go 默认 1 MiB、读取超时 15 秒） | 不变：没有请求体限制；请求头的限制是另一类 |
| 核心的 HTTP 服务（`server/internal/app/phases.go:589`）与 Gin | 不限制请求体；请求头同上（读取超时 10 秒）。Gin 的 `MaxMultipartMemory` 没有设置，网关也不解析表单 | 不变 |
| 插件服务端的 gRPC 接收上限（`sdk/pluginsdk/serve.go:80-113`） | 默认 4 MiB；插件用 `WithMaxRecvMsgSize` 调大，只有 moderation 用了（64 MiB，`plugins/moderation/main.go:15`）。钩子收到的请求字段超过它时这次钩子调用失败，按钩子的 `failure` 处理：`closed` 的钩子就等于按大小拒绝了请求 | **由设置推出**（8.2）：SDK 把所有插件的接收上限固定为传输层的最大值，`WithMaxRecvMsgSize` 和 moderation 的 64 MiB 常量删除。核心在读请求体时已经保证不超上限 |
| `gateway/hooks.go:176-195` 的 `grpcDefaultMaxRecv`、`requestTooLarge` | 识别“请求超过插件接收上限”的失败，不计入熔断 | **删除**：上一行之后这种失败不再发生 |
| 核心侧宿主服务端的接收上限（`grpcruntime/instance.go:459`） | 4 MiB（插件回调核心） | 原设计“由设置推出”（8.2）；按 D34 是 gRPC 自己的 `MaxInt32`，不检查 |
| 核心发给上游的请求体（`dispatch.go:812-814`） | 不限制 | 不变 |
| 请求体的结构检查（`gateway/jsonscan.go:15` 嵌套深度 10000；合法 UTF-8、合法 JSON、没有重复键，`pipeline.go:433-451`） | 400 | 保留：是结构限制，不是大小限制 |
| 钩子的 `prompt_text` 截断（默认 32 KiB，清单最多 1 MiB，`hooks.go:35`）、`EstimateUsage` 的提示文本截断（1 MiB，`precharge.go:20`）、用量估算字段的大小（4 KiB/32 KiB，`sdk/manifest/check/usage.go:64-65`）、本地计数的文本上限（32 MiB，`tokenizer.go:19`） | 截断或省略交给插件、交给计数器的**派生文本**，请求照常进行 | 保留：不拒绝请求，也不改发给上游的请求体 |
| 媒体下载上限 `media_download_max_mb`（`convert_media.go:110-120`） | 协议转换时核心下载一个媒体文件的上限 | 保留：限制的是核心去下载的文件，不是客户端的请求体；同在网关设置卡片里 |
| moderation 的 `max_inflight_mb`（`plugins/moderation/internal/moderation/hook.go:135-140`、`:244-250`） | 插件里在途请求字节的并发预算；单个请求不论多大都接收 | 保留：是并发与内存的预算，不按大小拒绝单个请求 |
| 响应一侧：WebSocket 上游消息 64 MiB（`websocket.go:61`、`:807`）、SSE 单行 16 MiB、用量 JSON 32 MiB（`forward.go:25-26`）、等待形式的缓冲 64 MiB（`spool.go:14-15`）、错误体 64 KiB | 限制上游的回答 | 原设计保留；按 D34 除交给 `ClassifyError` 的错误体前 64 KiB 外都已删除 |
| 控制面：插件路由 1 MiB（`plugin/routes/routes.go:25`）、托管与升级接口 16/32 KiB、插件包上传 | 控制台和管理接口 | 保留：不在网关请求的路径上 |

有一点要说明：客户端的请求体通过上限之后，核心自己还可能把它变大——协议转换内联下载的媒体（每个文件最多“媒体下载上限”）、钩子的补丁、参数覆盖。今天这些之后不再检查大小，以后也不检查；8.2 里由设置推出的内部上限按“交给插件的那份请求体”算，不能反过来把核心自己变大的请求拒掉。

### 8.2 由它推出的内部上限

> **D34 取消了本节**：核心与插件之间不由请求体上限推出任何界限。宿主的 broker 对所有宿主调用的接收上限是 `MaxInt32`，中继和取回的请求体不检查大小，取回的回答不截断。下表是原设计，留作记录；SDK 一侧（插件服务端固定 `MaxInt32`、`WithMaxRecvMsgSize` 删除）照原设计实现。

| 内部上限 | 今天 | 之后 |
|---|---|---|
| 核心调插件的单条消息 | 不限（`MaxInt32`） | 不变；核心在调用前自己保证请求体不超上限 |
| 插件服务端的接收上限 | 4 MiB，插件用 `WithMaxRecvMsgSize` 调 | SDK 对所有插件固定设为 `MaxInt32`，`WithMaxRecvMsgSize` 删除（8.1 的清单：留着它，钩子插件的接收上限就是第二个请求体大小限制）。gRPC 单条消息的硬上限约 2 GiB，设置的最大值 1024 MB 在其内 |
| 核心侧宿主服务端的接收上限（`grpcruntime/instance.go:459`） | 4 MiB | 设为 `MaxInt32`；处理函数里检查 `len(body)`，超出回 `InvalidArgument`。中继的界限是“这次执行交给插件的请求体长度”与“当前的请求体上限”中较大的一个，再加 1 MiB 余量（插件加信封、补字段）：核心交出去的请求体可能已经因为协议转换、钩子补丁、参数覆盖而大于上限（8.1 末尾），插件原样交回必须能过。设置改了立即生效，不用重启插件 |
| 取回方式的请求体 | `ExecuteHTTP` 没有专门的上限 | 当前的请求体上限（辅助请求是插件自己写的，与客户端的请求体无关，用同一个数只是为了不另设一个上限） |
| 取回方式的回答 | — | 常量 8 MiB |
| 插件进程的内存上限 | 清单申请的值，可被运营方的资源限制覆盖 | 不由请求体上限推出：默认不限，只有管理员的设置才是上限（8.3） |

传输层的上限固定为最大值、由处理函数按当前设置检查，是因为 gRPC 服务端的接收上限在建服务端时定死，而设置可以随时改。

### 8.3 内存：默认不限，只有管理员设置的上限（D23）

一个请求体 N 字节的尝试，插件进程里的瞬时峰值约 3N 到 4N（接收缓冲、解码后的字节、改写后的副本、发给核心时的序列化缓冲，第 10 节）。初稿据此让核心按请求体上限推出平台插件的内存下限（64 MB + 3 × 上限），并按内存上限的三分之一做在途请求体字节准入。用户否决了这两项（D23）：内存不自动算，也不做准入。现在的规则：

| 来源 | 是否构成上限 |
|---|---|
| 什么都没设置 | 不限：不设 `GOMEMLIMIT`，看门狗不按内存重启 |
| 清单 `resources.memoryMB` | 不是上限，只是插件自述的需要（安装审查页、资源页里展示） |
| 管理员给这个插件设的内存上限（`plugins.resource_limits.memory_mb`，控制台或 `PUT /plugins/:key/resources`） | 原样生效，不被抬高或压低（下一行的封顶除外） |
| 节点封顶 `SUB2API_PLUGIN_MAX_MEMORY_MB` | 默认 0（不封顶）；管理员设了时照旧：没有插件上限的插件就用它，插件上限超过它时取它 |
| 请求体上限 `max_request_body_mb` | 与内存无关；改它不会让任何插件进程过期（`LimitsStale()` 只看上面几项） |

后果要说清楚：默认情况下没有任何东西阻止插件进程一直长到节点内存耗尽（比如并发的大请求体）。想要界限的管理员给插件设上限，或设节点封顶。给平台插件设上限时，每个并发的大请求按“3 倍请求体上限 + 64 MB”留量；默认请求体上限 128 MB 下，一个请求约 450 MB。网关设置卡片里不再有内存警告：卡片看不到每个插件的上限，默认又没有上限可比。

### 8.4 “平台调用超时”的新含义

> **D34 取消了分段计时**：实现过一版（`core.ExecutionClient.SegmentTimeout`，超时以 `plugin_unavailable` / `segment_timeout` 换号；为大请求体还加过按 8 MiB/s 的搬运余量），后来与 6 小时的总时限一起删除。现在 `Execute` 不计时，平台调用超时只管不等待形式下的 `ClassifyError` 和读断开策略（CONTRACTS §111.4）。挂住的插件由客户端离开、上游超时或进程结束收场。下面是原设计，留作记录。

今天它管的是一次短的 RPC。之后一次 `Execute` 可以跨越整个上游交换（等待形式），所以要改定义：

> **平台调用超时**（`platform_call_timeout_ms`，默认 2000，范围 100 到 30000，不变）：一次请求里，插件每一段**自己的处理时间**的上限，不含等待上游的时间。

具体是这几段，每段各自计时：

| 段 | 说明 |
|---|---|
| `Execute` 开始到第一次调客户端（或直接返回） | 构造请求 |
| 两次客户端调用之间 | 多步流程里处理上一个回答、构造下一个请求 |
| 最后一次客户端调用返回到 `Execute` 返回 | 解析用量或任务、给分类 |
| `ClassifyError` 整个调用 | 不变 |
| WebSocket 每一轮的 `Execute` | 同上 |

- 客户端调用内部的等待由上游的超时管（响应头超时、整体超时、空闲超时、取回的时限），不计入。
- 今天固定 15 秒的收尾时限（`execute.go:91`）取消，并入上表第三段。它原来留这么长是因为插件要在里面回调核心写库；现在提交在返回之后、由核心做，核心自己的提交重试仍用 15 秒。
- `Execute` 的总时限（6 小时加整体超时，`execute.go:89`）保留，只对等待形式有意义。
- “平台热路径超时”不变：`ResolveModel`、`EstimateUsage`（`pipeline.go:559`、`precharge.go:60`）。它今天还管事后的 `ExtractUsage` 和 `ParseTaskSubmission`（`usageplugin.go:182`、`tasks.go:142`），这两处随 4.3 的跨插件规则删除。
- 界面文案改为：“插件在一次请求中每段自身处理时间的上限（构造上游请求、解析用量、错误分类），不含等待上游的时间。”（`web/src/i18n/locales/{zh,en}/settings.ts`）
- 请求体很大时，序列化和拷贝本身要时间（第 10 节的估算是 64 MiB 一两百毫秒），默认 2 秒够用；机器繁忙时接近上限的请求体会逼近它。

### 8.5 其他界面变化

- 安装与升级的审查页（`server/internal/plugin/install/review.go` 的 `Review`，`:20-21`、`:339-`）：声明了账号类型的插件固定显示一句“此插件会收到经它的账号发出的每个请求的完整内容，并决定请求发往哪个地址”。不是权限，不能取消勾选（D15）。
- 插件列表：加载被拒绝的插件显示拒绝的原因（第 9 节的那一条信息）。
- 使用记录：详情与筛选显示“未上报”。**已实现**：`error_type = usage_unreported` 的记录在列表里叫“未上报用量（未计费）”，详情说明插件没有返回用量、这次请求没有计费；失败请求的错误类型筛选能选它（`web/src/views/usage/usageFilters.ts`）。
- 账号编辑器里的请求头覆盖、参数覆盖、透传：界面和存储不变。
- 数据库：表结构不变，只有一个改数据的迁移 `0107`（与协议绑定合并时由 0106 改号）（8.1：请求体上限从环境变量搬到网关设置）。`plugin_executions.state` 是没有 CHECK 的文本列，新状态 `unreported` 不需要改表（`server/internal/migrations/0019_plugin_executions.sql:8`）。

## 9. 升级方式

### 9.1 一起构建，一起发布

核心 V 和**全部**第一方插件（十个平台插件，以及因 SDK 变化需要重新构建的其余插件）从同一个提交构建，一起发布。发布包里的内置清单不需要为这次改动调整（`deploy/docker/build-go.sh:165-166`）；不在内置清单里的第一方插件（relay、claude_oauth、azure_openai、vertex、bedrock）的新包随同一次发布上架市场。

- 十个平台插件各升一个版本，`hostCompat` 下限提到 V；`TestReleasedPluginsBumped` 对照 `plugins/released.json` 检查，发布后重写该文件。
- azure_openai、vertex、bedrock 的 0.1.0 没有发布过，直接以新契约的版本首发。
- `CHANGELOG.md` “未发布”一节重写：云厂商那一节里关于核心 `signing` 的说法删除。

### 9.2 按旧契约构建的插件包：加载时拒绝，一条信息

插件线协议版本 `ProtocolVersion` 从 1 升到 2（`sdk/protocol/protocol.go:14`，握手配置 `:26-30`，`GetInfoResponse.protocol_version` 必须与核心相等，`sdk/proto/sub2api/plugin/v1/plugin.proto:33`）。Host API 版本从 9 升到 10（`sdk/protocol/protocol.go:19`、`runtime-contract/contract.go:14`，两者必须相等）。理由见 12.1。

核心 V 加载一个插件包时：

- 清单检查不过（`runtime: "none"`、带 `upstream` 块、声明了已删除的能力 `platform.execute.v1` 或 `platform.poll.v1`），或者
- 二进制的握手协议版本不是 2，

都以同一条信息拒绝：

> 插件 `<key>@<version>` 是按旧的插件协议构建的，本核心（<V>，插件协议 2）不能加载它。请安装为核心 <V> 及以上构建的版本。

实现的文字是英文（`protocol.OutdatedContractMessage`）：`plugin <key>@<version> was built for an older plugin protocol and cannot be loaded by this core (<当前核心>, plugin protocol 2); install a version built for core <V> or later`，其中 `<V>` 固定是本协议的第一个核心 `protocol.FirstCoreVersion`（0.1.304），不是当前核心；插件列表（`GET /plugins` 的 `refusal`）在控制台按语言显示。

上传和安装时做同样的检查、给同样的信息。没有降级服务，没有“加载但不执行”，没有切回旧版本的安排。

### 9.3 回退

没有专门的回退流程。要回到上一个状态，就把上一版的核心和上一版的整套插件一起重新部署。本次唯一的迁移（`0107`，8.1）只改数据：回到旧核心后表结构不需要处理，但请求体上限的环境变量已经被删掉，旧核心会按默认的 128 MB 运行，调过上限的要在“环境变量”页重填（`gateway` 行里多出来的 `max_request_body_mb` 旧核心不认识，读取时忽略，下次保存网关设置时被丢掉）；核心 V 写下的 `unreported` 状态的执行行，旧核心的恢复扫描只找 `observed`，不会碰它。

### 9.4 已部署实例

这一段只说明现状，不为它设计兼容。已经在运行的部署升级到核心 V 时，核心和全部平台插件必须一起换：升级后任何还是旧包的插件都会在加载时被拒绝，它的账号不可用，直到装上新版本。第三方的平台插件必须由作者用新 SDK 重新构建。Host API 版本变了，网关外壳不允许新旧核心滚动并存（`gateway/internal/control/store.go:454`），所以这次只能停服式升级。还要直说一点：现有的托管升级流程在这次升级上**走不通**——节点就绪检查要求每个已启用的插件都已加载（`server/internal/app/managed_checks.go:241-250`），而内置插件的升级要等升级计划完成之后才开始（CONTRACTS §37），旧包被拒绝加载会让新核心的节点一直不就绪。已有的部署需要停机，用核心 V 和新的插件集重新部署，再把非内置的插件逐个装上新版本；具体步骤本文没有设计也没有验证（第 15 节）。

## 10. 性能与风险

能从代码推出的给数字，其余标“未测量”。

### 10.1 请求体拷贝：现在每个请求都有

今天普通转发路径上请求体不离开核心。之后每次尝试：

| 位置 | 拷贝 |
|---|---|
| 核心序列化 `Execute` 请求 | 1 次（N 字节） |
| 传输（本机 unix socket，gRPC 分帧） | 写 1 次、读 1 次 |
| 插件解码 | 1 次 |
| 插件改写请求体 | 0 到 1 次。库按路径改写，不整份解析；什么都不改时原样引用 |
| 插件序列化中继请求、传输、核心解码 | 同上三行，反方向 |

合计每次尝试约 6 到 8 次 N 字节的内存拷贝，其中 4 次经过套接字。按转发草稿的估算（内存拷贝每毫秒约 1 MiB 的保守量级，**未测量**）：典型的几十到几百 KiB 的聊天请求增加不到 1 毫秒；1 MiB 增加 1 到 4 毫秒；20 MiB 几十毫秒；64 MiB 一两百毫秒；128 MiB 几百毫秒。换号重试时每次尝试都要再付一次。发布前必须在 1、8、32、128 MiB 四档实测延迟和两侧的内存峰值（第 15 节）。

瞬时内存：核心一侧每次尝试多约 2N（序列化缓冲、收到的最终请求体）；插件一侧约 3N 到 4N。不等待形式下持有时间是毫秒级。

另有一次本机 RPC 往返（中继调用）和一次 `Execute` 调用取代原来的一次 `BuildUpstreamRequest`，每请求净增一次往返，几十微秒的量级（未测量）。

### 10.2 数据库写入

- 不等待形式（绝大多数流量）：与今天的普通转发相同，没有执行记录。codex 的流量因此每请求**少**三次写入。
- 等待形式（今天 volcengine 的视频端点和任务提交）：与今天相同，三次。

### 10.3 进程数与并发类

- 进程数见 7.7。
- 并发类：原设计 `Execute` 用执行类，上限 1024（`grpcruntime/runtime.go:124-128`）；按 D34 删除，`Execute` 不限并发，只计入在途调用（排空时照旧等它们）。热路径类（64）不再被“构造请求”占用，只剩模型解析、估算、钩子、分类。
- CPU：`GOMAXPROCS` 是 `ceil(cpu)`，申请 0.1 到 0.25 的插件只有 1 个 P，进程 nice +10（`sandbox/exec.go:26-30`）。现在每个请求都要在这个 P 上做一次请求体的解码和编码。高流量的插件（anthropic、openai）在大请求体多的时候会成为瓶颈，申请值要按 10.1 的实测调。

### 10.4 崩溃的影响范围

| | 今天 | 之后 |
|---|---|---|
| 插件崩溃时正在构造的请求 | 普通转发：按插件不可用换号 | 相同 |
| 插件崩溃时在途的流 | 普通转发：不受影响。执行路径（codex、volcengine）：全部切断 | 不等待形式：不受影响（codex 因此变好）。等待形式：切断，不计费（已定，12.4），范围只有那个插件正在等待的请求 |
| 没有进程的插件 | 没有可崩溃的东西 | openai、gemini、relay、azure_openai、anthropic 的 `apikey` 现在依赖一个进程。进程 10 分钟内重启超过 5 次，这个节点上该插件的全部账号不可用（`grpcruntime/runtime.go:98-104`） |
| 插件升级换代 | 旧实例排空 30 秒；执行路径的长流跨不过去 | 不等待形式下流不在插件里，不受排空影响 |
| 内存超限重启 | 少见（插件不持有请求体） | 默认不限内存，看门狗不按内存重启；管理员设了上限时可能性上升，界限由管理员按 8.3 留量 |

### 10.5 失去的保证，和替代它们的东西

坦率列出：

1. **“平台插件看不到完整提示词”没有了。** 今天平台插件只看到声明的字段（`common.proto:11-12`）。之后每个平台插件都拿到经它账号的每个请求的全部内容。替代：安装审查页的固定说明；没有 `net` 授权的插件拿到内容也发不出去，它唯一的对外通道是核心的客户端（取回受 `externalServices` 限制，每个请求有记录）。但要说清楚中继的目标地址是插件定的，只受 SSRF 规则约束，这一点今天也是如此（今天请求体同样被发往插件给的 URL）。
2. **“声明式插件不可能有代码缺陷”没有了。** 五个插件的请求构造从核心里一份经过黄金样例固定的引擎，变成各自进程里的库调用。替代：公共库加移植过来的 1498 条黄金样例，加所有插件都要过的一致性测试（第 14 节）。
3. **核心在插件之后对请求体的强制没有了。** 今天核心在插件的补丁之后还会再强制一次流式标志；之后插件交回的字节是最终的。替代：一致性套件第 3 条（模型引用、流式标志、合法 JSON、没有重复键）；核心在运行时不再校验请求体（D24）。套件也校验不了“运营方的参数覆盖写进去的某个字段有没有被插件删掉”。也就是说，运营方的请求体设置由核心先执行，但插件在其后可以改掉结果。今天的代码插件用补丁也能做到同样的事，声明式的五个类型以前做不到，现在能了。
4. **等待形式下“插件没上报也照常计费”没有了**（D3）。范围是今天的 volcengine。替代：ERROR 日志（筛选、人工补记、阈值通知都还没有做，D30）。
5. **进程更多，故障点更多**（10.4 第三行）。替代：没有。这是 D17 的直接代价。
6. **一次调用不再有任何固定时限。** 等待形式的 `Execute` 可以任意长。原设计用 8.4 的分段计时替代，D34 把它也删了：替代只剩上游超时（管理员配置）、客户端离开和插件进程本身；挂住的插件占着账号和 API Key 的槽位直到其中之一发生。

### 10.6 没有采纳的进一步简化

等待形式和执行记录还可以整个去掉：插件解析用量走现有的 `ExtractUsage`、任务提交走现有的 `ParseTaskSubmission`，两者本来就是“核心调用、事实在返回值里”的一元调用，那样 `Execute` 永远是不等待的。没有采纳，因为用户已定的是“`Execute` 返回 `usage` 或 `task`”，而且任务登记“提交成功后才发布提交响应”的原子性今天由执行记录保证。记在这里，供以后考虑。

## 11. 删除清单

除特别说明外，都在核心 V 的同一组提交里删干净（开发规则 2）。

### 11.1 proto（`sdk/proto/sub2api/plugin/v1/`）与生成代码

| 文件 | 删除 |
|---|---|
| `platform.proto` | RPC `Poll`（`:101`）、`BuildReconcileRequest`（`:124`）、`ParseReconcileResponse`（`:132`）及消息 `BuildReconcileRequestRequest`、`BuildReconcileRequestResponse`、`ParseReconcileResponseRequest`（`:576-610`；`PollRequest`、`ReconcileEntry`、`ReconcileResult` 留给 `Monitor`）；RPC `BuildUpstreamRequest`（`:27-28`）；消息 `BuildUpstreamRequestRequest`（`:231-249`）、`BuildUpstreamRequestResponse`（`:251-291`，含字段 `patches`、`signing`、`base`）；RPC `ExtractUsage`（`:89`）和 `ParseTaskSubmission`（`:96`）作为核心调用的入口（消息 `ExtractUsageRequest` 留作观察的载体，`TaskSubmission`、`UsageReport` 留）；`:14-25` 注释里关于回调和 v1 的说法 |
| `host.proto` | RPC `ForwardUpstream`、`RecordUsage`、`ReserveAndWatch`、`ReportTaskProgress`（`:48-55`）；消息 `ForwardUpstreamRequest`、`ForwardUpstreamResponse`、`RecordUsageRequest`、`ReserveAndWatchRequest`、`ReportTaskProgressRequest`、`ExecutionReceipt`（`:334-368`）。`:707` 的 UDP 注释不动（5.6） |
| `common.proto` | 契约规则“请求体从不整个发给插件……用 `BodyPatch` 回答”（`:11-12`）；消息 `RequestSigning`（`:28-52`）。**`BodyPatch` 消息保留**：网关钩子的回答还在用它（`hook.proto:47`、`gateway/hooks.go:614`），钩子不在本次范围 |
| `sdk/gen/pluginv1` | 重新生成 |

删掉的字段号在 proto 里标 `reserved`。`plugin.proto:22-23` 里“旧 SDK 构建的插件对 `SettingsChanged` 回 UNIMPLEMENTED、核心忽略”的容忍随协议版本上调失去对象，核对核心侧的忽略逻辑后一并删。

### 11.2 清单与检查（`sdk/manifest`）

| 位置 | 删除 |
|---|---|
| `manifest.go:80-81` | `CapPlatformPoll`、`CapPlatformExecute` 两个常量和所有引用。`CapPlatformAdapter`、`CapPlatformMonitor`、`CapPlatformTasks`、`CapPlatformWebSocket` 保留原 ID |
| `manifest.go:166`、`:562` | `Platform.RequestFields`、`AccountPlatform.RequestFields` |
| `manifest.go:304` | `Endpoint.Request.MaxBodyBytes`（8.1） |
| `manifest.go:29` 的注释、`upstream.go` 全文 | `RuntimeNone`；`Upstream`、`UpstreamBaseURL`、`UpstreamCredentials`、`UpstreamKeyValidator`、`UpstreamTexts`、`UpstreamAuth`、`HeaderWrite`、`UpstreamRoute`、`UpstreamPatch`、`RouteModel`、`UpstreamTest`、`UpstreamModels` 及常量；`AccountType.Upstream` 字段。`RuntimeGRPC`、`TransportHTTP`、`TransportWebSocket` 搬到 `manifest.go` |
| `check/upstream.go` 全文 | `runtimeNone` 和对 `upstream` 块的全部校验 |
| `check/validate.go:349-353` | `runtime none` 分支；改成对它报“不再支持” |
| `check/account_check.go`、`check/platforms_check.go` | 依赖 `upstream` 声明和 `requestFields` 的检查 |
| `sdk/protocol/protocol.go:14`、`:19`，`runtime-contract/contract.go:14` | 不是删除：`ProtocolVersion` 改 2，两处 `HostAPIVersion` 改 10 |
| `sdk/platforms/anthropic.json:110`、`openai.json:205` | `requestFields` |
| `sdk/protocol/environment.go:10` | `EnvVarMaxRequestBodyMB`（8.1）。同文件 `protocol.GatewaySettings`（`settings.go:59-77`）不是删除：加 `max_request_body_mb` |

### 11.3 SDK

| 位置 | 删除 |
|---|---|
| `sdk/upstream/` 整个目录 | 引擎、黄金测试与 `testdata/golden`（先按 7.3 移植） |
| `sdk/platforms/upstream/` 整个目录 | 三个预设与 `presets.go` |
| `sdk/pluginsdk/execute.go` | `ForwardUpstream`（`:40-52`）、`RecordUsage`（`:54-67`）、`ReserveAndWatch`、`ReportTaskProgress`（到 `:97`）；`TaskMonitor` 的旧签名（`:21-27`）；`ExecuteDefault` 重写 |
| `sdk/pluginsdk/request.go:27`、`:37` | `SetPatch`、`DeletePatch`（钩子插件不用它们；核对后删） |
| `sdk/pluginsdk/interfaces.go` | `Platform` 接口里的 `BuildUpstreamRequest`；`UsageExtractor`、`TaskSubmissionParser` 从“对应一个 RPC”改成 `ExecuteDefault` 在进程内调用的接口 |
| `sdk/pluginsdk/serve.go:80-84`、`:110-113` | `WithMaxRecvMsgSize` 和 `maxRecv` 选项：接收上限固定为 `MaxInt32`（8.2） |
| `sdk/pluginsdk/serve.go:187-200`、`task.go:13-35` | 对 `platform.execute.v1`、`platform.poll.v1` 的推断和校验；`task.go:34-35` 里“或旧的 Poller/Reconciler”的分支 |
| `sdk/pluginsdk/poll.go`、`interfaces.go` | `Poller`、`Reconciler` 接口和 `Poll` 的服务端；受控 HTTP 的作用域留给 `Monitor` |
| `sdk/pluginsdk/pluginsdktest` | 假宿主里三个回调和 `ForwardUpstream` 的桩 |
| `sdk/pluginsdk/doc.go` | 相应的说明 |
| `sdk/reqsign/reqsign.go:6-8` | 包注释里“核心用它签名”的说法（包保留） |

### 11.4 核心

| 位置 | 删除 |
|---|---|
| `server/internal/plugin/declarative/` 整个目录 | 核心侧的声明式客户端 |
| `server/internal/plugin/registry/registry.go:386-407` | `bindDeclared` 及其调用、对 `sdk/upstream` 的引用（`:21`） |
| `server/internal/plugin/grpcruntime/static.go` 全文；`rollout/types.go:53-54` | `StaticInstance`、`LoadStatic` |
| `server/internal/plugin/api/ops.go:442-446` | 对 `runtime none` 的资源限制分支 |
| `server/internal/core/ports_plugin.go:72-95` | `DeclaredUpstream`；`PlatformPlugin` 里的 `BuildUpstreamRequest`、`ExtractUsage`、`ParseTaskSubmission`、`BuildReconcileRequest`、`ParseReconcileResponse`；`PollPlugin`（`:101-103`） |
| `server/internal/core/ports_execution.go:12-26`、`:34` | `ExecutionCallbacks` 的 `Record`、`Watch`、`Forward`（换成中继与取回的处理函数）；`MonitorPlugin.Monitor` 的上报参数；`CommitExecution` 返回的回执 |
| `gateway/dispatch.go` | `:616-638` 的分叉与 `BuildUpstreamRequest` 调用；`splitHeaders`（`:641-648`）；`writeDeclaredAuth`（`:688-707`）；`forwardBuilt` 里的补丁应用（`:755-758`）、`declaredTarget` 调用（`:760`）、第二次强制流式（`:775-780`）、声明头分层（`:824-826`、`:830`）、签名（`:834-836`）、`execution` 参数相关的分支按 4.2、4.3 重排 |
| `gateway/param_override.go:66-77`、`:122-140` | `prepareDeclared`、`declaredTarget`；`rewriteBody` 少一层 |
| `gateway/upstream_ext.go:55-93`、`:124-129` | `checkUpstreamExt` 的签名部分、`signUpstreamRequest`、`unsupportedOnWebSocket` 的签名部分；`:6-9` 的注释。`adaptResponseStream` 留 |
| `gateway/execute.go` | `canExecute`（`:52-76`）；回调装配（`:91`）；兜底（`:115-132` 里的 `:125-127`）；`forwardCallback`、`record`、`watch` 改成从返回值驱动 |
| `gateway/websocket.go` | `turnPatches`（`:815-848`）、`applyTurnPatches`（`:850-`）、`handshakePatches`（`:143-146`）、`dial` 里的 `BuildUpstreamRequest`（`:728-748`） |
| `gateway/usageplugin.go:159-194` 一带、`gateway/tasks.go:118-180` | 事后调 `ExtractUsage`、`ParseTaskSubmission` 的路径（4.3 的跨插件规则） |
| `gateway/util.go:154-`、`gateway/routing.go:35`、`:216`、`:234-235` | `requestFieldValues` 和路由上的 `requestFields`（`usageRequestFields` 留） |
| `gateway/hooks.go:614` 的 `applyPatches` | **保留**，钩子在用 |
| `gateway/pipeline.go:383-395`、`gateway/settings.go:27` | `bodyLimit` 里环境变量和端点声明两个分支，改成只读网关设置；常量 `defaultMaxBodyBytes`（8.1） |
| `server/internal/core/environment.go:26`、`:77-80`、`:177-178` | 环境变量 `max_request_body_mb` 的别名与表项（含进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB`）、`Environment.Bytes()`；`:48` 一带注释里的“请求体上限”（8.1） |
| `gateway/hooks.go:170-195`、`:534` | `grpcDefaultMaxRecv`、`grpcRecvTooLarge`、`requestTooLarge` 及其在熔断处的分支：插件的接收上限不再低于请求体上限（8.1） |
| `plugin/grpcruntime/execute.go` | `hostServer.RecordUsage`、`ReserveAndWatch`（`:131-178`）、`ForwardUpstream`（`:107-130`）；`online()` 里“观察完成后才接受回调”的分支（`:87-89`） |
| `plugin/grpcruntime/monitor.go:14-66`、`poll.go` | `hostServer.ReportTaskProgress` 及其状态；`platformAdapter.Poll`（`poll.go:117-143`），受控 HTTP 的作用域并入 `Monitor` |
| `usage/reconcile.go:401-417`、`:419-444`、`:446-` | `pluginObservationContract`、`askPoll`、`buildAndAsk`：任务观察只调 `Monitor` |
| `plugin/grpcruntime/adapters.go` | `BuildUpstreamRequest`、`ExtractUsage`、`ParseTaskSubmission`、`BuildReconcileRequest`、`ParseReconcileResponse` 的适配（`:239`、`:252` 一带） |
| `usage/executions.go:120-154` | 后台恢复按核心读数提交，改成标记未上报 |
| `usage/monitor.go:92-117` | 上报闭包的回调形式 |
| 测试 | `gateway/declared_test.go`、`plugin/declarative/declarative_test.go`、`gateway/patch_guard_test.go`（改写成守卫测试）、`gateway/upstream_ext_test.go` 的签名用例、`gateway/execute_test.go`、`grpcruntime/execution_audit_test.go`、`grpcruntime/monitor_test.go`、`usage/executions_test.go`、`plugin/pkg/pkgtest/pkgtest.go:188-222` 的 `runtime none` 夹具，以及各处用 `BuildUpstreamRequest` 假插件的测试 |

### 11.5 工具、构建、插件

| 位置 | 删除 |
|---|---|
| `tools/sub2api-plugin/build.go:59-63`、`dev.go:280` | 跳过 `runtime none` 的分支 |
| `deploy/docker/build-go.sh:152-155` | 注释里“openai 和 gemini 是 runtime none 包”的说明 |
| `plugins/{openai,gemini,relay,azure-openai,anthropic}/manifest.json` | `upstream` 块；`runtime: "none"` |
| `plugins/moderation/main.go:15`、`:19` | `maxRecvMsgSize` 常量和 `WithMaxRecvMsgSize` 选项（8.2）。它自己的并发预算 `max_inflight_mb` 保留 |
| `web/src/api/types.ts:307`、`web/src/i18n/locales/{zh,en}/settings.ts` 的 `units.mb`、`web/mock/environment.ts:17-19` | 环境变量单位 `mb` 和模拟数据里的变量（8.1） |
| `plugins/*/manifest_test.go` 里对 `sdk/upstream` 的引用 | 移植后删 |
| `plugins/bedrock/internal/bedrock/upstream.go:47` 一带 | `signing()` 和构造结果里的 `Signing`；`beta.go`、`upstream.go` 里的补丁 |
| `plugins/{codex,volcengine,vertex,claude-oauth}` | `BuildUpstreamRequest` 及补丁；volcengine `execute.go:23` 的上报、`video.go:388`、`:418` 的两阶段旧接口、清单里的 `platform.poll.v1` 与 `platform.execute.v1`（`manifest.json:27`、`:33`）；codex 清单里的 `platform.execute.v1`（`manifest.json:19`） |

### 11.6 文档

| 文件 | 处理 |
|---|---|
| `docs/UPSTREAM-DECLARATIVE.md` | 删除 |
| `docs/CONTRACTS.md` | §31 重写（插件执行与返回值）；§109 改写（`signing` 删除，`response_stream` 保留）；§30（单次 Poll）并入 `Monitor`；§83.6（WebSocket 逐轮构造）、§86.5（请求头分层）相应修改；§74.3 删掉 `max_request_body_mb` 一条和几处提到它的句子，§8、§14.4、§107.2 的网关设置表加 `max_request_body_mb`（8.1），§72.2 第 3 条（钩子请求的消息大小、`WithMaxRecvMsgSize`）按 8.2 改写；§14.4 与 §8 里平台调用超时的含义按 8.4 改；§101.1 关于 Host API 版本的说明更新为 10；描述声明式上游的章节标为已删除；新增一节写本设计（目前最后一节是 §109） |
| `docs/ARCHITECTURE.md` | §3、§5.3 与 `:1211`、`:1232-1237` 一带关于“插件看不到整个请求体”“声明式”的描述。`:63` 把插件 UDP 访问列为不做，不动（5.6） |
| `docs/PLUGIN-DEVELOPMENT.md` | 第 1 节“三条请求路径”改成一条路径两种传输；第 2 节各表的“普通 / 执行 / WebSocket”三列合并；第 3 节插件表；第 4 节自查清单（`:76` 一带）；第 6 节 `signing` 一行删除（`:112-122`）；第 7 节请求体补丁辅助删除（`:152`） |
| `docs/PLUGIN-EXECUTES-CORE-RECORDS.md`、`docs/PLUGIN-CODEX.md`、`docs/PLUGIN-VOLCENGINE-ARK.md`、`docs/PLUGIN-CLOUD-PROVIDERS.md`、`docs/OPENAI-RESPONSES-WEBSOCKET.md` | 涉及回调、补丁、`signing`、已知限制（`PLUGIN-CLOUD-PROVIDERS.md:235-242`）的段落；`OPENAI-RESPONSES-WEBSOCKET.md:42` 的“单条消息上限取端点 `maxBodyBytes`”改成网关设置 |
| `AGENTS.md` | “平台插件必须遵守全部系统设置：……在普通转发、插件执行、WebSocket 三条路径上”一句 |
| `CHANGELOG.md` | “未发布”一节 |
| 本目录两份草稿 | 实现完成、CONTRACTS 新章节写好后删除 |

### 11.7 核对过、决定保留的

| 项 | 位置 | 保留的理由 |
|---|---|---|
| `BodyPatch` 消息与 `applyPatches` | `common.proto:14-26`、`gateway/hooks.go:614` | 网关钩子（审核、拦截）的回答在用，钩子不在本次范围 |
| `usageRequestFields` | `sdk/manifest/manifest.go:237-253` | `EstimateUsage` 的入参，预扣不改（D8） |
| `EstimateUsage` 的 `Unimplemented` 回落 | `gateway/precharge.go:63-88`、`sdk/pluginsdk/estimate.go:30-37` | 不是兼容代码：估算是可选的（D8） |
| 提交失败时在收尾时限内重试同一份事实 | 今天 `gateway/execute.go:120-124` | 不是兜底，是同一份事实的重试（D9） |
| 成对的 `Build…Request` / `Parse…Response`（测试、模型列表、续期、额度、余额） | `platform.proto:32-36`、`:147-186` | 不属于字段加补丁模式（4.8） |
| `platform.tasks.v1`、`platform.websocket.v1` | `sdk/manifest/manifest.go:79`、`:83` | 标记端点种类，不是新旧之分 |
| `ExecuteHTTP` | `host.proto:226-231` | 就是客户端的取回方式，适用范围扩大 |
| `SendAccountRequest` | `host.proto:233-243` | 执行之外按账号发的有界请求，与本次无关 |
| `ClaudeMaxTokens` 取代的旧字段 `claude_default_max_tokens`“行里可能还有、读时忽略” | `gateway/settings.go:53-56` | 不在这条路径上；按 D4 的精神可以另行清理，不放进这次 |

## 12. 与决定的冲突与待定

逐条写明冲突在哪、建议怎么处理。没有一条是悄悄改掉的。

### 12.1 契约版本：升（D7）

用量草稿的 A3 是不升 Host API 版本，理由是升了就不能滚动发布。这个理由不再成立（D4）。本次删除了 `HostService` 的四个 RPC（11.1），也删除、改写了 `PlatformService` 的多个 RPC 和消息，是不兼容的接口变化。两个版本号都如实上调：

- `HostAPIVersion` 9 → 10：宿主接口变了。它今天的实际作用是让网关外壳判断两个核心版本能否滚动并存（`gateway/internal/control/store.go:454`），升了之后这次发布不能滚动，这正是事实。
- `ProtocolVersion` 1 → 2：插件线协议变了。`sdk/protocol/protocol.go:17-18` 的注释说“线协议保持为 1，哪些核心能跑由 `hostCompat` 决定”；但已发布的旧包 `hostCompat` 写的是 `<0.2.0`，包含核心 V，光靠它拒绝不了旧包。握手协议版本是现成的、在进程启动时就生效的拒绝点（`protocol.go:26-30`），用它给出 9.2 的那一条信息。那句注释随之改写。

### 12.2 D12（请求体上限只有一处）与现有代码：已定

用户的最终答复：上限只在“系统设置 → 网关设置”里配置，其他地方不要限制。现有代码与它不一致的地方和处理（细节与完整清单见 8.1）：

- 上限今天是环境变量，有全局层、分组层和进程环境变量，不在“网关设置”里。**搬进网关设置，环境变量连同分组层删除**，存着的值用迁移 0107 处理。本文初稿建议过保留环境变量、只在卡片里加一行，已被这条答复推翻。代价是失去按分组设置上限的能力，这是用户的选择。
- 清单允许端点声明 `request.maxBodyBytes`。删除（没有使用者）。
- 钩子插件的 gRPC 接收上限（默认 4 MiB，moderation 设 64 MiB）今天对 `failure: closed` 的钩子实际上是第二个请求体大小限制。改成由 SDK 固定为传输层最大值，`WithMaxRecvMsgSize` 删除。初稿把它列为“不在本次范围”，与“其他地方不要限制”不符，已改。
- 插件的内存上限：默认不限，只有管理员设置的上限生效（D23、8.3）。管理员设的上限不是“大小上限”，但它实际决定了插件能接多大的请求体，所以管理员要按请求体上限留量。

默认值保持 128 MB、范围保持 1 到 1024（任务说明起初建议 64 MB，不采用，理由见 8.1）。

### 12.3 D10（不能绕过代理）：UDP 不做；持有 `net` 的插件是已有的出入

UDP 不在范围内（5.6），所以本设计里没有不经账号代理的出口，D10 与 D11 之间没有冲突。

有一处已有的出入要指出：持有 `net` 授权的插件今天就能经隧道直连它域名策略允许的主机，不带账号、不经代理（`egress.go:270-285`）。claude_oauth、codex、volcengine 持有它，用于登录流程等账号还不存在的场合。也就是说“插件不能绕过代理”对持有 `net` 的插件今天就不是由核心强制的。本设计不扩大这个口子（网关请求只走客户端），但也没有关上它。建议另行评估这三个插件能否改用取回方式后放弃 `net`，不放进这次。

### 12.4 D21（插件崩溃不切流）在等待形式下的例外：已定，切断

D21 要求新的路径不继承“插件崩溃切断在途的流”。不等待形式做到了。等待形式（插件自己解析用量的端点、任务提交）下，插件一旦崩溃就给不出事实，按 D3 这个请求不计费。此时有两种做法：

- **切断（采用，今天的行为）**：上游停止生成，损失到此为止。
- 继续中继到结束：客户端拿到完整内容，但这个请求不计费，上游的费用照付。

用户的最终答复是切断、不计费：继续只会增加收不回来的上游费用。范围只有那个插件正在等待的请求（今天是 volcengine 的视频端点和任务提交，其中任务提交的交换本来就很短）。

### 12.5 D18（改写在交给插件之前）与声明式那一层

D18 说运营方的改写在插件之前。今天声明式类型自己的过滤字段和 `paramOverride` 排在运营方改写**之前**（`param_override.go:82-91`）；改成代码插件后它们在**之后**。行为差异和处理见 7.3 末尾。这不违反 D18，但它是 D17 带来的一处可见变化，不能说“逐字节相同”没有例外。

### 12.6 `AGENTS.md` 与 CONTRACTS

- **开发规则 2（不留过渡代码）**：发布的代码里没有任何为旧插件或旧核心保留的东西。
- **`AGENTS.md` “内置插件随核心升级（CONTRACTS §37）”与节点就绪检查**：这两条现有机制合在一起，使托管升级在本次改动上走不通（9.4）。按 D4 本文不为此设计；但它意味着“核心与插件一起发布”目前在托管部署上没有一个可用的升级流程，以后每一次不兼容的插件契约变化都会碰到同样的问题。记在这里，是否要另行解决由用户决定，不在本次范围。
- **`common.proto:7-12` 的契约规则**：“请求体从不整个发给插件”删除（D14）。“除 `EgressService.Dial` 外都是一元调用”在本次改动里仍然成立；(b) 级需要的“插件写给客户端”的通道会打破它，那一阶段立项时处理。
- **`docs/ARCHITECTURE.md:63`**：把插件 UDP 访问列为不做，与用户的答复一致，不改。
- **`AGENTS.md`“核心迁移……目前到 0104”**：实际已到 0105（`0105_account_header_override.sql`）。W8 加的迁移（合并协议绑定后为 0107） 时把这句改对。
- **CONTRACTS §31.3**：写“内置 anthropic/openai/gemini/relay 0.2.0 使用 Execute”，与代码不符（3.2）。§31 重写时改对。
- **`docs/CONTRACTS.md:5126`**：提到 `resource_http.go` 的资源请求上限，这个文件在仓库里已经不存在。改 §74.3 时一并删掉。
- **`docs/OPENAI-RESPONSES-WEBSOCKET.md:42`**：写“客户端单条消息上限取端点 `maxBodyBytes`（默认 32 MiB）”，与代码不符（取的是 `bodyLimit()`，默认 128 MB）。W8 改成网关设置。
- **`docs/PLUGIN-DEVELOPMENT.md:13`、`:65`**：把 anthropic 写成用代码实现 `BuildUpstreamRequest`，实际是声明式（3.2）。

### 12.7 任务说明里与代码不符的地方

- “只有清单的插件是 openai、gemini、relay、azure_openai”：按账号类型还有 anthropic 的 `apikey`（主控中途更正里已指出）。
- “请求体上限：除非网关已经有”：已经有，128 MB（3.5）；最终按用户的答复搬进网关设置（8.1）。
- “现有隧道是否转发 UDP”：不转发（3.4）；UDP 最终不做（5.6）。
- “`platform.poll.v1` 如果没有东西需要它就删”：核对的结果是没有（4.4）。

## 13. 任务拆分

不再要求每个包合入后整棵树都是绿的（D4）：契约包直接删旧加新，之后核心、SDK、插件各自改到能编译、测试通过为止。**要求只有两条：每个包有自己的验收测试；分支整体结束时全绿**（每个模块 `gofmt`、`vet`、`build`、`test`，控制台 `typecheck`、`test`、`lint`），然后才合入 develop。

大小：S 约一两天，M 约三到五天，L 一周以上（单人）。文件范围互不重叠，同一阶段的包可以交给不同的人并行。

| 包 | 内容 | 文件范围 | 验收 | 依赖 | 大小 |
|---|---|---|---|---|---|
| W1 | 契约：proto 删旧加新（11.1、5.2）；清单删 `upstream`、`runtime none`、`requestFields`、`maxBodyBytes`、两个能力 ID（11.2）；`ProtocolVersion` 2、`HostAPIVersion` 10；重新生成 | `sdk/proto/**`、`sdk/gen/**`、`sdk/manifest/**`、`sdk/protocol/**`、`runtime-contract/contract.go`、`sdk/platforms/*.json` | `buf generate` 后没有未提交的差异；`sdk/manifest` 的测试：`runtime none`、`upstream` 块、已删的能力各有一条拒绝用例；两个 `HostAPIVersion` 相等 | 无 | M |
| W2 | 黄金样例转换（**已完成**）：在 W1 **之前**的提交上把四个黄金文件转成不依赖引擎的样例并冻结，带回放辅助 `goldentest.Run`；同时导出 azure 与五份 `manifest_test.go` 的断言清单 | `sdk/pluginsdk/standard/goldentest/**`（新；样例在它的 `testdata/golden` 下，随包内嵌，这样插件模块 import 就能用）、`sdk/upstream/golden_convert_test.go`（新，随引擎删除） | 转换结果条数为 441、343、371、343；`TestGoldenConversion`（提交的文件就是录制的转换输出，输入转回去不丢东西）和 `TestConvertedGoldenReplay`（每一条经今天的引擎回放一致）通过（7.3 第 1、2 条） | 无（必须最先做，旧引擎一删就做不了） | S |
| W3 | SDK：`Relay`、`Fetch`、新的 `ExecuteDefault`、`Monitor` 返回值、能力推断、所有插件的接收上限固定为 `MaxInt32`、假宿主；删回调、`Poller`、`Reconciler`、补丁辅助、`WithMaxRecvMsgSize` | `sdk/pluginsdk/*.go`、`sdk/pluginsdk/pluginsdktest/**`、`sdk/reqsign`（注释） | 单测：三种返回各一例；标志决定等待与否；解析失败返回 `host_usage`；`Monitor` 返回结果；包里不再有三个回调函数 | W1 | M |
| W4 | 公共库与预设：`sdk/pluginsdk/standard`，`providers/{openai,anthropic,gemini}` 的预设构造函数；删 `sdk/upstream`（含 W2 的 `golden_convert_test.go`）、`sdk/platforms/upstream`。`standard/goldentest` 的样例是冻结的，不改 | `sdk/pluginsdk/standard/**`、`sdk/pluginsdk/providers/**`、`sdk/upstream/**`、`sdk/platforms/upstream/**` | W2 冻结的 1498 条样例全过；`sdk` 模块里搜不到 `sdk/upstream` | W2、W3 | L |
| W5 | 运行时：中继、取回的宿主服务端，执行作用域（不等待时挂起请求、返回后交给网关），`Monitor` 取返回值，宿主服务端接收上限，在途字节准入，内存下限（后两项按 D23 已删）；删回调服务端、`Poll` 适配、`StaticInstance`；握手协议不符时给出 9.2 的信息 | `server/internal/core/ports_execution.go`、`ports_plugin.go`、`server/internal/plugin/grpcruntime/**`、`server/internal/plugin/rollout/**` | 运行时测试：不等待的中继在 `Execute` 返回前不触发发送；等待的中继返回观察；取回第 9 次被拒、清单外主机被拒、回答超限带 `truncated`、令牌过期被拒；超过请求体上限的中继被拒；准入预算不被突破；旧协议的测试插件二进制被拒且信息正确 | W1 | M |
| W6 | 网关单一路径：`attempt` 调新 `Execute`；不等待与等待两种形式；请求头的默认头与权威头；守卫；WebSocket 逐轮；跨插件规则；删 11.4 里网关的全部条目 | `server/internal/gateway/{dispatch,execute,websocket,param_override,upstream_ext,routing,tasks,usageplugin,util}.go` 及同目录测试 | 第 14.2 节的网关测试矩阵；断开测试在新路径上通过；热路径基准前后对比写进交付说明 | W1、W5 | L |
| W7 | 用量：从返回值提交、未上报关闭、恢复扫描改标记、监控结算走返回值、任务观察只调 `Monitor` | `server/internal/usage/**`、`server/internal/core/ports_billing.go` | 用量草稿第 11 节的矩阵（同步、任务、监控各行）；数据库测试确认真的跑了 | W1 | M |
| W8 | 请求体上限搬进网关设置（8.1）：`GatewaySettings` 加 `max_request_body_mb`（默认 128、范围 1 到 1024、`normalized`、接口校验 `out_of_range`、`protocol.GatewaySettings` 镜像）；`bodyLimit` 只读它（HTTP 请求体与 WebSocket 单条消息）；删除环境变量 `max_request_body_mb`（全局层、分组层、进程环境变量）、`Environment.Bytes()`、单位 `mb`；迁移 `0107`（全局值搬进 `gateway` 行，`environment` 行和 `groups.environment` 删键）；`hooks.go` 的 `requestTooLarge` 一组；网关设置卡片加一行、中英文文案、类型与模拟数据；平台调用超时的文案（8.4）；保存过大值时的警告（8.3，按 D23 已删）。`request.maxBodyBytes` 字段本身由 W1 删，W8 先合入时 `bodyLimit` 已不读它 | `server/internal/gateway/{settings,settings_api,settings_plugin,hooks}.go`、`gateway/pipeline.go`（只动 `bodyLimit`）、`server/internal/core/environment.go`、`sdk/protocol/{environment,settings}.go`、`server/internal/migrations/0107_*.sql`、上述各处的测试（8.1 列出的 `gateway`、`core`、`environment`、`group`、`config` 测试）、`web/src/views/settings/**`、`web/src/i18n/locales/*/settings.ts`、`web/src/api/types.ts`、`web/mock/{billing,environment}.ts`、`AGENTS.md`（迁移编号一句） | 设置接口测试：默认 128、缺字段的行取默认、范围外 `out_of_range`、保存后立即生效；网关测试：超过设置的 HTTP 请求 413、WebSocket 消息以 1009 关闭、分组的 `environment` 不再影响上限；环境变量接口不再列出该变量，带着它保存报未知变量；迁移测试（数据库真的跑了）：全局值搬到 `gateway` 行且不覆盖行里其他字段、没有 `gateway` 行时新建、`environment` 行与分组的键被删、分组其余变量不动、迁移后“环境变量”页和分组能保存；`GatewaySettingsCard.spec.ts`；`npm run typecheck`、`test`、`lint` | 无 | M |
| W9 | 注册表、安装、审查：删 `bindDeclared` 与 `plugin/declarative`；上传与加载时的拒绝信息；审查页固定说明；插件列表显示拒绝原因；`api/ops.go` 的 `runtime none` 分支 | `server/internal/plugin/{registry,declarative,install,pkg,api}/**`、`web/src/views/plugins/**` | 旧契约的包上传被拒、信息正确；审查接口返回固定说明；`pkgtest` 夹具不再有 `runtime none` | W1 | M |
| W10a–W10j | 十个插件各一个包：清单、`hostCompat`、版本、实现或接入 `Execute` | 各自的 `plugins/<name>/**` | 一致性套件（14.1）；各自原有测试移植后通过；`TestReleasedPluginsBumped` | 见下 | 见下 |
| W11 | 构建与工具：打包工具删 `runtime none` 分支；构建脚本注释；其余第一方插件（moderation、guard、dynweight、mailer、growth、payment）按新 SDK 重新构建并升版本；moderation 去掉 `WithMaxRecvMsgSize`（8.2） | `tools/sub2api-plugin/**`、`deploy/docker/build-go.sh`、上述插件目录的 `manifest.json` | `tools/sub2api-plugin` 测试；镜像构建产出全部插件包 | W3 | S |
| W12 | 一致性套件与 e2e | `sdk/pluginsdk/pluginsdktest/conformance/**`（新）、`e2e/**` | 套件自测（一个故意违规的假插件被每条规则抓到）；e2e 在 e2e 栈上跑过一遍 | W3；e2e 部分等 W6、W10 | M |
| W13 | 文档：11.6 的全部 | `docs/**`、`AGENTS.md`、`CHANGELOG.md` | 文中不再把已删的接口写成现行 | 其余全部 | M |

插件包：

| 包 | 插件 | 依赖 | 大小 | 备注 |
|---|---|---|---|---|
| W10a | openai | W4 | S | 含 WebSocket |
| W10b | gemini | W4 | S | |
| W10c | relay | W4 | S | |
| W10d | azure_openai | W4 | S | 没有黄金文件，按 W2 导出的断言清单 |
| W10e | anthropic | W4 | S | 在现有进程里加 `apikey` 类型 |
| W10f | claude_oauth | W3 | M | |
| W10g | vertex | W3 | M | |
| W10h | bedrock | W3 | M | 自己签名；完整请求体下做深度清洗，解决 `PLUGIN-CLOUD-PROVIDERS.md:235-242` 的已知限制 |
| W10i | codex | W3 | M | 含 WebSocket；从执行路径变成不等待形式 |
| W10j | volcengine | W3 | M | 等待形式、任务；`Poll` 的函数体改成 `Monitor`，删两阶段旧接口 |

顺序：

1. W2（在删除之前留下验收依据），然后 W1。W8 与它们无关，随时可做；它动 `sdk/protocol` 的两个文件和 `gateway/hooks.go`，这些文件不在别的包的范围里。
2. W1 之后并行：W3、W5、W7、W9。这一阶段树是红的。
3. W3 之后：W4、W10f 到 W10j、W11、W12 的套件部分。W5 之后：W6。
4. W4 之后：W10a 到 W10e。
5. W6 和全部 W10 之后：W12 的 e2e。最后 W13。

需要注意的交叉点：`server/internal/core/ports_*.go` 归 W5（`ports_billing.go` 归 W7），W6、W7、W9 只读不改；需要改端口时找 W5 的负责人。

(b) 级不在上表里，是以后的阶段。到时的包：隧道的拨号凭证、`proxy.Service` 的拨号方法、插件写给客户端的通道，L。UDP 不做（5.6），没有对应的包。

## 14. 测试方案

### 14.1 一致性套件：每个转换后的插件都必须通过

放在 `sdk/pluginsdk/pluginsdktest/conformance`，一个函数加一张输入表，每个平台插件在自己的测试里调用。**实现**：套件做了第 1 到第 11 条（第 12、13 条不在套件里）；第 4 条检查的是“声明的集合”（D25）；十个平台插件都调用了它（第 17 节；claude_oauth 删除后是九个）；第 1 条的形式从端点定义读出，WebSocket 协议只归第 11 条。检查项：

1. 每个声明的上游协议、流式与非流式，`Execute` 都能完成，并且恰好发出一次中继。
2. 普通端点返回 `host_usage` 并用不等待形式；声明了插件解析用量的端点用等待形式并返回 `usage`；任务提交端点返回 `task`。什么都不返回的插件不过。
3. 交回的请求体：模型引用与交入的一致；交入时流式标志为真，交回时仍为真；是合法 JSON；没有重复键。
4. 请求头：鉴权头和被签名的头在权威头里，其余在默认头里；不回带 `content-length`、`transfer-encoding`、`connection`、`accept-encoding`；不把没在 `passHeaders` 里的客户端头带出去。
5. 不重试：假宿主让中继或取回失败时，插件不发第二次中继，返回错误或 `classification`。
6. 确定性：同样的输入（固定时钟）产生同样的请求。
7. 换号：尝试序号大于 0 时重新构造，不复用上一次的结果。
8. 不开套接字：测试进程里把默认传输换成一遇到拨号就失败的实现，整个套件期间不能触发。
9. 取消：context 取消后在套件自己的时限（`CallTimeout`，默认 2 秒）内返回。宿主不给插件计时（D34），这是套件的界限。（落地后这个时限和第 10 条的请求体大小成了套件内部的常量，`conformance.Plugin` 没有 `CallTimeout`、`BodyLimit` 字段。）
10. 大请求体：请求体取上限时能完成，插件一侧的额外分配不超过请求体的 4 倍（`testing.AllocsPerRun` 加内存统计）。
11. 声明了 WebSocket 能力的：一轮消息的 `Execute` 给出握手地址、握手头和最终消息。
12. `ValidateCredentials`、`ClassifyError`、`BuildTestRequest`、`BuildModelsRequest` 对套件给的标准输入有回答。
13. 签名的插件（bedrock）：交回的请求能通过模拟上游的 SigV4 校验，且 URL 经一次解析再输出后逐字节不变。

另外五个用公共库的插件要过移植的黄金样例（7.3）。

### 14.2 网关测试，用假插件

改写现有的假执行插件（`gateway/execute_test.go:23-93`），按开关返回不同内容。每行检查客户端收到什么、`usage_logs` 行数与费用、账本、`request_precharges`、`plugin_executions`。流式与非流式各一遍。

| 情形 | 期望 |
|---|---|
| 不等待，返回 `host_usage` | 请求在 `Execute` 返回后才到模拟上游；按规则计费；**没有**执行行；非流式回答不缓冲 |
| 不等待，插件在返回前报错、超时、进程退出 | 模拟上游**没有收到**请求；换号；账号不冷却；没有使用记录以外的痕迹 |
| 不等待，流式回答进行中插件进程退出 | 流不中断，照常计费 |
| 不等待，上游 4xx/5xx | 核心调 `ClassifyError`，按分类换号或返回；`ClassifyError` 失败用默认分类 |
| 等待，返回 `usage` / `host_usage` / `task` / 不返回 / 种类不对 / 提交先失败后成功 / 一直失败 / 重复相同事实 / 不同事实 | 用量草稿第 11 节的各行 |
| 等待，交换进行中插件进程退出 | 上游被取消，记未上报、不计费（12.4） |
| 插件给的 `upstream_model` 与核心算的不一致（类型声明了模型可用性） | 不发送，按插件不可用换号。请求体本身核心不检查（D24），由一致性套件第 3 条检查 |
| 请求头分层 | 默认头被透传和覆盖改写；权威头不被改写；模拟上游收到的逐项比对 |
| 请求体 | 交给插件的是转换、映射、强制流式、`claude_max_tokens`、参数覆盖之后的；模拟上游收到的与插件交回的逐字节相同 |
| 换号 | 第二次尝试拿到按新账号改写的请求体 |
| 取回 | 经账号代理发出；不套透传和覆盖；失败后可以安全换号 |
| 大小 | 超过网关设置里请求体上限的客户端请求 413，分组的环境变量不再影响它；钩子收到上限以内的任何请求都不因 gRPC 接收上限失败；插件交回的请求体超过 8.2 的界限被拒，原样交回核心自己变大的请求体不被拒（在途字节准入按 D23/D26 没有做） |
| 时限 | （原设计：插件在任一段自身处理超过平台调用超时，这次尝试失败。D34 删除，`TestExecuteWaitedHasNoTimeLimit` 固定“没有时限”） |
| WebSocket | 第一轮建连、之后每轮只发消息；每轮按规则计费；握手只套请求头覆盖 |
| 断开 | 三种断开策略在不等待形式下与今天的普通转发行为相同 |
| 故障策略 | 传输错误不问插件；“请求是否已写出”影响能否重试 |
| 跨插件 | 别的插件的账号类型不能服务插件解析用量的端点 |

### 14.3 用量与监控

用量草稿第 11 节的监控各行、预扣各行，原样采用。

### 14.4 旧包的拒绝

- 用旧 SDK 构建的测试插件二进制（握手协议版本 1）：加载被拒，信息是 9.2 的那一条。
- `runtime: "none"` 的包、带 `upstream` 块的包、声明 `platform.execute.v1` 或 `platform.poll.v1` 的包：上传和加载都被拒，信息相同。
- 新包装在旧核心上被 `hostCompat` 拒绝。

### 14.5 端到端

在 ec2pool 测试实例和 e2e 栈上：

- 每个平台插件流式、非流式各一次，核对 token 和费用与升级前同类请求一致；有真实账号的用真实上游，其余用模拟上游（azure、bedrock、vertex、volcengine 视频没有真实账号）。
- codex 和 openai 的 WebSocket 各一个多轮会话。
- bedrock：Claude Code 形态的请求体（带 `tools[].custom`、`cache_control.scope`）不再被模拟上游拒绝；SigV4 校验通过。
- 带代理的账号：模拟上游看到的来源是代理。
- 全新部署一遍（核心 V 加整套插件），确认内置插件都加载、十个平台插件都能服务（claude_oauth 删除后是九个）。
- 1、8、32、128 MiB 四档请求体的延迟和内存，写回本文第 10 节。

报告“测试通过”前确认数据库测试真的跑了，没有成片跳过。

**实现**：e2e 栈上的验收用例 AC30–AC36 覆盖了上面除性能实测外的各项：十个平台插件经 `Execute` 的流式与非流式（AC30，claude_oauth 删除后是九个；不能配地址的插件访问的厂商主机由 mock 以别名和自建 CA 的 TLS 扮演）、运营方的请求头设置与账号代理（AC31）、换号与未上报（AC32）、旧契约的包被拒（AC33）、Responses WebSocket 逐轮与请求体上限设置（AC34、AC35）、以官方 OpenAI 和 Anthropic Go SDK 为客户端（AC36）。运行方法见 `deploy/e2e/README.md`。1、8、32、128 MiB 的实测和 ec2pool 上的真实上游没有做。

## 15. 未验证事项

1. **沿用草稿而没有重读的引用**：`gateway/forward.go` 中继与用量的细节行号、`gateway/disconnect.go`、`gateway/failure_policy_runtime.go:241-271`、`gateway/ssrf.go:15-20`、`usage/monitor.go`、`usage/reconcile.go`、`usage/settler.go`、`billing/precharge.go`、`billing/settle.go`、`sandbox/exec.go:26-30`、`sdk/paramoverride/headers.go:22`、`gateway/internal/control/store.go:454`、`account/*.go` 除调用点以外的行。这些文件自 `123282faa` 以来没有变化，行号取自草稿。
2. 完整请求体往返的延迟和两侧内存峰值没有测量（10.1 是估算）。
3. 插件进程空闲时的常驻内存、启动时间没有测量（7.7）。
4. 不等待形式“请求在 `Execute` 返回后才发出”在运行时里的实现方式（作用域如何把挂起的请求交给网关）只做了纸面设计。
5. 声明式那一层从运营方改写之前变到之后（7.3、12.5）：W2 已逐个核对三个预设和五份清单，只涉及 azure_openai 的 `service_tier`、`safety_identifier`（7.3 末尾）。线上有没有账号的参数覆盖真的写了这两个字段，没有查。
6. 黄金样例的转换（W2 已做）：旧样例没有请求体，转换后的请求体是按字段和流式标志合成的最小请求体（只有 `model`、`stream`），补丁只出现过 `stream_options.include_usage` 一种。所以样例固定的是请求的构造，不能证明新库对真实的、带几十个字段的请求体逐字节原样交回；这一点要靠 W4 自己的测试和一致性套件（14.1 第 3 条）。
7. 插件给的 URL 经核心重新解析再发出后是否逐字节不变（路径里的 `%3A`、`%2F`，查询串顺序）。
8. 已部署实例的升级步骤（9.4）没有设计也没有验证：停机后用核心 V 和新插件集重新部署时，内置同步、节点就绪检查、非内置插件的重新安装各自的实际表现只读了 `managed_checks.go:236-254` 和 CONTRACTS §36、§37 的文字。
9. 握手协议版本不符时 go-plugin 给出的错误能否被运行时可靠地识别并换成 9.2 的那条信息，没有试过。
10. `plugin.proto:22-23` 说的“旧 SDK 对 `SettingsChanged` 回 UNIMPLEMENTED 时核心忽略”，核心侧的忽略逻辑在哪、删掉是否安全，没有核对。
11. 仓库外有没有第三方平台插件、有没有跨插件服务“插件解析用量”端点的组合，代码里查不到。
12. 等待形式下未上报的失败在启用故障策略时会不会触发管理员的 503 规则而冷却账号。
13. （按 D23 作废：内存不再由设置推出。）
14. `sdk/pluginsdk/request.go` 的 `SetPatch`、`DeletePatch` 是否有钩子插件在用（核对后才能删）。
15. volcengine 视频任务没有真实账号可测。
16. 新 proto 的生成代码差异没有跑过 `buf generate`。
17. (b) 级只有提纲：代理的 CONNECT 与 socks5 拨号没有验证。
18. （已验证）迁移 0107 的 SQL（8.1）起初是草稿；实现为 `server/internal/migrations/0107_request_body_limit_setting.sql`，由真实 PostgreSQL 上的 `TestMigration0107RequestBodyLimitSetting` 运行；`gateway` 行只含这一个键时其余字段取默认值，由 `gateway/request_body_limit_test.go` 覆盖。
19. 去掉 `WithMaxRecvMsgSize`、把钩子插件的接收上限固定为最大值之后，moderation 在请求体接近上限时的内存表现没有测量；它自己的 `max_inflight_mb` 预算按请求字段的字节数准入，单个请求总是接收，所以一个 1 GiB 的请求会原样进到它的进程里；插件默认不限内存（D23），管理员给它设了上限时要按 8.3 留量。

## 16. 需要用户确认

目前没有待确认的问题。初稿的三个问题用户都已回答，结论已写进正文：

| # | 问题 | 用户的答复 | 写在哪里 |
|---|---|---|---|
| 1 | 请求体上限放在哪里 | 只在“系统设置 → 网关设置”里配置，其他地方不要限制（原话：“系统设置-网关设置里面配置就行了，其他地方不要限制”）。环境变量 `max_request_body_mb` 连同分组层删除，没有清单字段，插件不设上限 | D12、8.1、8.2、12.2、W8 |
| 2 | 等待形式（插件自己解析用量的端点、任务提交）下插件在交换进行中崩溃 | 切断上游，这个请求不计费 | D21、4.3、12.4 |
| 3 | UDP | 现在不做，没有需求。经核心、套账号代理的 TCP 连接（(b) 级）仍是以后的阶段，不在本次改动里 | D11、5.1、5.6 |

## 17. 实现与本文的差异（没有所有者决定的）

按分支 `feat/forwarding-v2`（`26950a969`）的代码核对时列出的三条，在其后的审查修正（至 `dc5c306e9`）中处理如下。所有者已经决定的差异写在 D24–D34 和对应各节。

1. **一致性套件接到了全部十个平台插件上**（原差异：只有 anthropic、openai、gemini、relay、azure_openai 调用）。claude_oauth、codex、vertex、bedrock、volcengine 现在都调 `conformance.Run`；只有 bedrock 声明了两处例外（流式用例的 `03_body`、全部用例的 `04_headers`，都是 InvokeModel 本身的性质，见 PLUGIN-DEVELOPMENT.md 第 4 节）。接套件的过程中改了这些插件：vertex 把核心交来的 `user-agent`、`x-goog-api-client` 作为默认头转发；claude_oauth、codex 在清单里写 `passHeaders: []`（为此清单的 `passHeaders` 区分“没写”和“空”）；套件改为从端点定义读出每个协议的形式，WebSocket 协议只归第 11 条。
2. **使用记录有了“未上报”的显示和筛选**（8.5）。
3. **拒绝信息仍是英文**（9.2 写的是中文句子）：`sdk/protocol/protocol.go` 的 `outdatedContractFormat`；控制台按 `refusal` 的字段另行组织文字。信息里要求的版本从“当前核心”改成本插件协议的第一个核心 `protocol.FirstCoreVersion`（0.1.304），`refusal` 多了 `first_core_version`；节点按结构化的 `code` 记录拒绝，不再靠比较文字。首次启用就被拒、发布在准备阶段失败、节点已经删掉失败的实例时，拒绝也照样给出：发布结束时把每个节点的代码记在 `plugin_rollout_nodes.code`（迁移 0109），`refusal` 从该插件最近一次发布的节点结果取（CONTRACTS §112.10）。
