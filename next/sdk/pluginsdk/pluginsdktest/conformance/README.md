# 平台插件一致性套件

依据 `docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md` §14.1。每个平台插件在自己的测试里调用一次 `conformance.Run`，套件用假宿主（`pluginsdktest`）逐条检查第 1 到第 11 条规则，外加余额设计（`docs/DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md` 3.1.7）加的 `12_balance`。转发设计 §14.1 的第 12、13 条（`ValidateCredentials` 等的标准输入、bedrock 的 SigV4 校验）不在本套件里。

## 一行调用

```go
func TestConformance(t *testing.T) { conformance.Run(t, conformancePlugin(), conformanceCases()...) }
```

`conformancePlugin()` 返回 `conformance.Plugin`，`conformanceCases()` 返回 `[]conformance.Case`，都写在插件自己的测试文件里。完整的例子是本目录 `conformance_test.go` 里的 `fakePlugin("")` 和 `fakeCases()`。

`Run` 运行期间把进程的 `http.DefaultTransport` 和 `net.DefaultResolver` 换成一拨号就失败的实现，所以不要和用网络的测试并行（`Run` 自己不调 `t.Parallel`，两个 `Run` 之间互斥）。

## 输入表

`Plugin`（插件级）：

| 字段 | 含义 |
|---|---|
| `Platform` | 被测实现。`TaskMonitor`、`UsageExtractor`、`TaskSubmissionParser` 由 SDK 照常按类型断言找到 |
| `Manifest` | 插件的 `manifest.json`，必填。套件用它起插件（`WithManifest`），清单里声明的能力决定第 11 条是否适用；第 4 条交给插件的客户端请求头、第 12 条哪些类型声明了余额也从它读 |
| `Platforms` | 别的插件声明、本插件的账号类型服务的平台（例如 New API 服务 TypeSafe 的平台：`[]manifest.Platform{typesafe.Platform()}`）。套件像对内置平台一样从它们读端点的形式和客户端请求头；不给时这些协议在 `Run` 开始时就被拒绝 |
| `SDK` | 传给 `pluginsdk.Register` 的其余选项，通常不用 |
| `Config` | `Configure` 的插件设置，nil 为 `{}` |
| `Protocols` | 插件声明的 HTTP 上游协议（不含 WebSocket 协议，它的轮次归第 11 条）。每个协议在核心会发的每种形式下都要有 HTTP 用例；有哪些形式由套件从协议的端点（内置平台的或清单里的）读出：有 `request.streamPath`（或模型由插件解析）的流式、非流式都有，`request.stream` 为真的只有流式，其余只有非流式。列了 WebSocket 协议或没有端点的协议，`Run` 在开始时就失败 |
| `Credentials` | 鉴权头和被签名的头（小写）。必须在权威头里，出现在默认头里就失败 |
| `Authoritative` | 其余要保留为权威头的头，名字 → 理由（例如改写前代码插件的头都在运营方设置之上，转换后要保持）。理由不能为空 |
| `Fetch` | 插件辅助请求（换令牌、上传）的回答。nil 时取回回 `UNIMPLEMENTED` |
| `FixClock` | 把插件的时钟设成给定时间；套件在每次 `Execute` 前调用。请求依赖时间（签名）时必填，否则第 6、7 条失败 |
| `Exceptions` | 有理由的例外，见下文 |

`Case`（一个样例请求）：

| 字段 | 含义 |
|---|---|
| `Name` | 失败信息和例外里用的名字；默认 `<协议> stream` / `<协议> non-stream`，WebSocket 轮次再加 ` websocket` |
| `Account` | 样例账号：类型、`credentials_json`、`settings_json`。每种类型第一个用例的账号也是第 12 条查余额的账号 |
| `Meta` | 请求元信息：协议、模型、流式、端点等 |
| `Body` | 核心交给插件的请求体（上游协议，改写已完成）；WebSocket 轮次是这一轮的消息 |
| `Endpoint` | `Ordinary`（默认，不等待 + `host_usage`）、`PluginUsage`（`usageSource: "plugin"`，置 `extract_usage`）、`TaskSubmission`（任务提交，置 `task_submission`） |
| `WebSocket` | 这是 WebSocket 端点的一轮 |
| `UpstreamModel` | 插件声明的模型映射结果；空表示不变 |
| `Observed` | 等待形式的上游回答，`PluginUsage`、`TaskSubmission` 必填，例如 `pluginsdktest.Observed(200, body)` |
| `Failover` | 换号那次尝试用的账号。nil 时由 `Account` 推出：换 id，凭证 JSON 顶层的每个字符串值加后缀 `.failover`。凭证有结构、加后缀会坏时自己给 |

客户端请求头不由用例给：套件像核心一样，按用例账号的类型在清单里找它对协议所属平台的那一项，取那一项的 `passHeaders`，没写就取平台的（`AccountPlatform.ClientHeaders`），每个头以 `conformance-client-<名字>` 的值交给插件。核心在调插件之前已经过滤，所以套件不再交白名单外的头。账号类型不在清单里、或者它不服务协议所属的平台，`Run` 在开始时就失败。

## 十二条检查

每条是一个子测试，子测试里每个用例一项。失败信息的格式是 `<用例>: <看到了什么，怎么改>`，下一行 `rule:` 写规则和设计里的章节。`08_no_sockets` 最后运行，因为它判断整个套件期间有没有拨号。

| 子测试 | 做法 |
|---|---|
| `01_one_relay` | 每个用例执行一次，`Execute` 成功且恰好一次中继；另有 `coverage: <协议> stream|non-stream` 项检查每个声明的协议在它的每种形式下都有用例；`coverage: <用例>` 项在用例有问题时出现：HTTP 用例的协议没声明，或者是核心不会发的形式（例如流式的 count_tokens），或者 WebSocket 轮次的协议没有 WebSocket 端点 |
| `02_form_and_report` | 按 `Endpoint` 检查等待与否和返回的种类；什么都不返回（未上报）失败 |
| `03_body` | 交回的请求体是合法 JSON、同一对象里没有重复键；`model` 等于交入的（或 `UpstreamModel`），请求体里没有 `model` 时 URL 里要有它；交入 `stream: true` 时交回仍为真 |
| `04_headers` | 权威头的名字都在 `Credentials` ∪ `Authoritative` 里；`Credentials` 的头不在默认头里；默认头的值里不带账号凭证；没有 `content-length`、`transfer-encoding`、`connection`、`accept-encoding`；核心交来的每个客户端请求头（清单的 `passHeaders`）都出现在默认头里（值可以被插件改，例如账号设置；声明在 `Authoritative` 里的也可以是权威头）。另有 `declared authoritative headers` 一项：声明的每个权威头至少在一个用例里作为权威头出现，否则是过期声明 |
| `05_no_retry` | 三种失败各执行一次：中继调用失败；等待形式下上游交换失败（503）；插件取回过的用例，取回失败。之后不许再中继、不许多取回，必须返回错误或 `classification`，不许返回 `host_usage`/`usage`/`task` |
| `06_deterministic` | 固定时钟下同一输入构造 5 次，逐字段比较 |
| `07_failover` | 先给换号账号做一次全新构造（尝试 0、另一个请求 ID）作参照；再按原账号尝试 0、换号账号尝试 1（同一请求 ID）。尝试 1 必须等于参照。两个账号构造出相同请求时无法区分复用，项失败并要求设置 `Failover` |
| `08_no_sockets` | 整个 `Run` 期间默认传输、默认解析器、假宿主的出口隧道都在拨号时失败并记下地址；这一项再把每个用例执行一次，然后要求一次拨号都没有 |
| `09_cancellation` | 假宿主让第一次中继或取回阻塞到它的 context 结束；插件进入后取消 `Execute` 的 context，用插件侧 gRPC 服务端的拦截器看 `Execute` 在插件一侧何时返回，必须在 2 秒内（套件自己的界限，宿主不给插件在 `Execute` 里的工作计时） |
| `10_large_body` | 在请求体里加顶层字段 `conformance_padding` 把它补到 8 MiB（只决定计量用多大的请求体，宿主不限制请求体）；`Execute` 要完成且恰好一次中继。用 `testing.AllocsPerRun` 驱动、`runtime.MemStats.TotalAlloc` 计量每次 `Execute` 的分配，减去同样输入下一个原样转交请求体的参照插件的分配，差值不超过请求体的 4 倍。`-race` 下跳过：竞态检测会改变分配，4 倍的界限在不带它的构建里测 |
| `11_websocket` | 声明了 `platform.websocket.v1` 就必须有 WebSocket 用例（反之亦然，`declaration` 项）。每一轮：恰好一次中继、不等待、返回 `host_usage`；URL 是 ws(s) 或 http(s) 的绝对地址；至少一个握手头；消息是合法 JSON；没有 `response_stream` |
| `12_balance` | 每个声明了 `balance` 或有用例的账号类型一项（`balance: <类型>`），用这种类型第一个用例的账号查。声明了的：用 `Harness.Balance` 查两次，请求的主机要过清单的主机规则，回答一次全是传输错误、一次全是空的 200，两次都必须是错误结果（`error_type` 不为 0），不许 panic、不许当成余额；插件不实现 `BalanceReader`（回 `UNIMPLEMENTED`）失败；没有用例的账号可查也失败。没声明的：`BuildBalanceRequest` 必须回 `UNIMPLEMENTED`（否则读出来的余额核心从不显示） |

## 例外

插件确实不能遵守某条规则时，在 `Plugin.Exceptions` 里声明：

```go
Exceptions: []conformance.Exception{{
	Check:  "03_body",
	Case:   "anthropic.messages stream",
	Reason: "Bedrock selects streaming by the URL (invoke-with-response-stream); the body carries no stream flag",
}},
```

- `Check` 是子测试名，`Case` 是失败信息开头的那个名字（用例名，或 `coverage: ...`、`declared authoritative headers`、`declaration`、`suite` 这几种项名）。
- 没有理由、或名字对不上任何规则和项，`Run` 在开始时就失败。
- 被例外的项照常运行，失败只记日志（带理由）。**它一旦通过，就以“例外已经不需要，删掉它”失败**，所以例外不会在修好之后留下。
- 第 4 条要保留的非鉴权权威头不走例外，写在 `Plugin.Authoritative` 里并给理由；声明了却没有任何用例用到也会失败。

## 套件证明不了什么

- 只看假宿主收到的东西：核心真实的发送、SSRF、代理、请求头分层、发送前检查、计费不在这里（网关测试 §14.2、e2e §14.5）。
- 只覆盖给出的用例。没有用例的端点、账号类型、请求形状没有被检查；第 1 条只核对 `Protocols` 列出的协议，不从清单推断插件服务哪些协议（每个协议有哪些形式是从端点定义读的）。
- 第 8 条拦得住经默认传输、默认解析器（主机名）和出口隧道的拨号；直接拨 IP 字面量、或用 cgo、`syscall` 开套接字的代码拦不住。
- 第 9 条只测“阻塞在宿主调用里时被取消”；插件在两次宿主调用之间自己算很久不在范围内。
- 第 10 条用 8 MiB（套件内部的常量 `largeBodySize`，`Plugin` 没有设置它的字段；取消的等待时限 `cancelWait` 同样）而不是网关请求体上限的默认值 128 MiB（分配随大小线性，比值相同），计量的是整个进程的分配减去参照插件，插件在后台另起的 goroutine 也算在内；填充字段被插件过滤掉时测到的就少。
- 第 6、7 条比较的是交回的请求，不比较取回（插件按账号缓存令牌是允许的）。第 7 条只能抓到请求随账号变化的复用。
- 第 11 条没有元信息里的“传输”字段可用（`RequestMeta` 没有这个字段），WebSocket 轮次由用例的 `WebSocket` 标出，插件怎么认出它是插件自己的事（例如按 `meta.endpoint`）。
- 不检查 `Monitor`，也不检查 `ValidateCredentials`、`ClassifyError`、`BuildTestRequest`、`BuildModelsRequest`（§14.1 第 12 条）和 SigV4（第 13 条）。
- 第 12 条只用无法读的回答检查失败的一面；能读的回答读成什么（金额、单位、`unlimited`）由插件自己的测试钉住。
