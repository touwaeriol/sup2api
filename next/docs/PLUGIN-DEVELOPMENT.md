# 插件开发：平台插件

本文写给开发平台插件（声明账号类型、为网关请求服务的插件）的人：一次网关请求怎样经过插件、插件怎么写和怎么测，以及控制台“系统设置”和账号编辑器里的每一项设置在插件的请求上由谁生效、插件要配合什么。规则本身以 [CONTRACTS.md](CONTRACTS.md) 为准（转发的契约在 §112），本文只做说明、索引和对照；两者不一致时以 CONTRACTS 为准，并把本文改对。插件的打包、签名、权限、迁移、界面写法见 CONTRACTS §2、§23.4、§26、§36 和 [ARCHITECTURE.md](ARCHITECTURE.md) §5；设计的来龙去脉见 [DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md](DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md)。

**总规则（2026-11-17 用户定）：每个平台插件都必须遵守全部系统设置，用户在控制台改的设置对任何插件的账号都要生效。** 新增系统设置或插件能力时，要同时核对 HTTP 与 WebSocket 两种传输并更新第 5 节的表。

## 1. 一条请求路径

每个平台插件都是有进程的代码插件（`runtime: "grpc"`），都服务 `PlatformService.Execute`（多数只写 `BuildUpstream`，由 SDK 跑 `ExecuteDefault`，2.1）。没有只有清单、没有进程的插件，也没有第二条请求路径：核心里不替插件构造上游请求。按旧契约构建的插件包（插件协议 1、`runtime: "none"`、清单里有 `accountTypes[].upstream`、`requestFields`、`request.maxBodyBytes`，或声明了 `platform.execute.v1`、`platform.poll.v1`）在上传、安装和加载时以同一条信息被拒绝（CONTRACTS §112.10）。

一次尝试（一个账号一次，换号后从头再来）：

| 步 | 谁 | 做什么 |
|---|---|---|
| 1 | 核心 | 鉴权、限额、请求级钩子、价格、余额、预扣（可调插件的 `EstimateUsage`）、调度选号、并发与限流 |
| 2 | 核心 | 协议转换（`internal/gateway/convert`）、账号模型映射、引用模型映射、强制流式、`claude_max_tokens`、账号参数覆盖的请求体部分；模型可用性预检 |
| 3 | 核心 → 插件 | `Execute`：元信息、账号（含凭证）、**完整请求体**（上游协议，核心的改写都已做完）、白名单内的客户端请求头、尝试序号，以及两个标志 `extract_usage`、`task_submission` |
| 4 | 插件 | 构造最终的上游请求：方法、URL、请求头、请求体字节；需要时自己签名。可以先用 `Fetch` 发辅助请求（换令牌、上传、预检） |
| 5 | 插件 → 核心 | `Relay`（不等待）或 `RelayAndWait`（等待），把请求交给核心的 HTTP 客户端 |
| 6 | 插件 → 核心 | `Execute` 返回事实：`host_usage`、`usage`、`task` 或 `classification` |
| 7 | 核心 | 发送（账号代理、SSRF、请求头分层、超时、断开处理）、中继、读用量、结算；上游出错时调 `ClassifyError` 并按故障策略裁决 |

两种形式由请求上的两个标志决定：

| 端点 | 标志 | 插件调用 | 返回 | 核心何时发请求 |
|---|---|---|---|---|
| 用量由核心按清单规则读（绝大多数） | 都不置 | `Relay` | `host_usage` | `Execute` **返回之后**。插件在返回前出任何问题（报错、崩溃），请求都没发出，核心按插件不可用换号，账号不受罚（发布了故障策略时也一样：只换号，不跑账号规则）。流式回答期间插件不在调用里，插件重启不影响在途的流 |
| 端点声明 `usageSource: "plugin"` | `extract_usage` | `RelayAndWait` | `usage`；读不出时 `host_usage`（核心记 `usage_extract=fallback`）；上游出错时 `classification` | 中继调用里立即发送；调用返回观察（2xx）或错误事实 |
| 任务提交（`endpoint.task.action: "submit"`） | `task_submission` | `RelayAndWait` | `task`；上游出错时 `classification` | 同上 |

- 等待形式下什么都没返回（或 `Execute` 报错、插件崩溃，或返回了 `host_usage` 而核心的读数完全为空）：同步请求记一条零费用的“未上报”记录（`error_type=usage_unreported`，预扣释放，ERROR 日志），已缓冲的非流式回答不发出、回 503，已开始的流异常结束；任务提交没有 `task` 时任务不登记、客户端报错、预扣释放（任务失败的路径，不是“未上报”）。等待期间插件崩溃或放弃调用，核心切断上游、这个请求不计费。核心不替插件补记用量。不等待形式下插件返回 `host_usage` 而核心的读数完全为空时，客户端已经拿到回答，记录同样是零费用的“未上报”（CONTRACTS §112.2）。
- WebSocket（`platform.websocket.v1`）：会话的每一轮消息调一次 `Execute`，请求体是这一轮的消息。插件照常 `Relay` + `host_usage`，给出握手地址（`ws(s)://` 或 `http(s)://`）、握手头和最终消息；核心在会话还没有上游连接时用第一轮的 URL 和握手头建连，以后每轮只发消息。WebSocket 端点没有等待形式，不能给 `response_stream`（CONTRACTS §83.6、[OPENAI-RESPONSES-WEBSOCKET.md](OPENAI-RESPONSES-WEBSOCKET.md)）。
- 插件只描述请求、不开套接字：上游请求、辅助请求都经核心的客户端发出，核心套上账号的代理、SSRF 规则、超时和运营方的请求头设置。插件不重试、不换号，一次执行最多一个中继。
- `Execute` 不计时：插件在 `Execute` 里自己的工作没有时限，等待形式的交换也没有总时限（所有者决定“超时、限制都去掉”，CONTRACTS §112.16）。挂住的插件由客户端离开、上游超时（管理员配置）或它的进程结束收场：客户端离开会取消 `Execute` 调用，但等待形式下只到中继开始发送为止，之后上游交换按断开策略处理，插件读观察之后的工作不再随客户端取消（它的事实还要入账）；期间这次尝试占着账号和 API Key 的槽位，所以插件要自己保证每一步都会结束、把 `ctx` 传到底。平台调用超时（网关设置 `platform_call_timeout_ms`）只管不等待形式下的 `ClassifyError` 和读断开策略（CONTRACTS §111.4）。

## 2. 写一个平台插件

### 2.1 接口

SDK 是 `sdk/pluginsdk`。`pluginsdk.Platform` 包含 `ClassifyError`、`ValidateCredentials`、`BuildTestRequest`；`Execute` 另由下面两者之一提供：实现 `UpstreamBuilder`（`BuildUpstream`），SDK 就替它跑 `ExecuteDefault`；或者实现 `Executor`（自己的 `Execute`），它优先。两者都没有时 `Serve` 拒绝启动（`a Platform must implement UpstreamBuilder or Executor`）。大多数插件只写构造请求的部分，不用写 `Execute`：

```go
func (p *Plugin) BuildUpstream(ctx context.Context, in *pluginv1.ExecuteRequest) (*pluginv1.UpstreamRequest, error) {
	cfg, err := spec.FromAccount(in.GetAccount()) // apikey.Spec
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return pluginsdk.NewUpstream(http.MethodPost, cfg.BaseURL+"/v1/messages").
		Header("x-api-key", cfg.APIKey).                                           // authoritative: the credential
		DefaultHeader("anthropic-version", "2023-06-01").                          // default: the operator may override
		DefaultHeader("anthropic-beta", in.GetInboundHeaders()["anthropic-beta"]). // an empty value writes nothing
		Body(in.GetBody()).
		Model(pluginsdk.RequestModel(in)).
		Build()
}
```

`ExecuteDefault` 按两个标志选形式（第 1 节的表）：普通端点 `Relay` 后返回 `ReportHostUsage()`；`extract_usage` 时 `RelayAndWait` 后调插件的 `UsageExtractor`（没有实现、出错或返回空时回落到 `host_usage`）；`task_submission` 时 `RelayAndWait` 后调 `TaskSubmissionParser`（清单有任务端点或 `platform.tasks.v1`、插件又没有自己的 `Executor` 时必须实现，否则 `Serve` 启动即失败）；等待形式的上游错误用插件的 `ClassifyError` 分类。`BuildUpstream` 返回 `pluginsdk.Classified(c)` 时（例如 `Fetch` 被上游拒绝），`ExecuteDefault` 回 `ReportClassification(c)`，核心按分类裁决而不是当成插件故障。

自己写 `Execute`（实现 `Executor`）也可以，其中仍可调 `pluginsdk.ExecuteDefault(ctx, p, in)` 做默认的部分；返回值用 `ReportHostUsage`、`ReportUsage`、`ReportTask`、`ReportClassification`。SDK 先拦住明显的错误并说明原因：第二次中继、没有标志却等待、`Monitor` 里中继、`host_usage` 却没有中继过、`usage` / `task` 却没有等待过、大写的头名和传输层的头。中继请求的方法和请求头由 `ValidateUpstreamRequest` 按宿主同一套规则（`httpguts`）判断，SDK 放行的宿主也放行。

可选接口：`UsageExtractor`、`TaskSubmissionParser`、`TaskMonitor`（`Monitor`）、`ModelLister`（`BuildModelsRequest`）、`ModelResolver`、`QuotaReader`、`CredentialRefresher`、`BalanceReader`（`BuildBalanceRequest` / `ParseBalanceResponse`，账号余额，CONTRACTS §52）、`ModelAvailabilityChecker`、`UsageEstimator`（`EstimateUsage`；见 `interfaces.go`、`estimate.go`）。能力由 `Serve` 推断：`Platform`（加上 `UpstreamBuilder` 或 `Executor`）就是 `platform.adapter.v1`，`TaskMonitor` 就是 `platform.monitor.v1`；`platform.tasks.v1`、`platform.websocket.v1` 在清单里声明，任务插件三个都要声明。

### 2.2 构造请求：`NewUpstream`

`pluginsdk.NewUpstream(method, url)` 返回构造器，`Build()` 得到 `*pluginv1.UpstreamRequest`，核心会拒绝的东西在 `Build` 时就报错（`INVALID_ARGUMENT`）：

- `method` 为空是 POST；URL 是绝对地址，路径写成线上的样子（每段已百分号编码），核心发送前会再解析一次。
- 头名一律小写（构造器替你转）；`host`、`content-length`、`accept-encoding`、`connection`、`transfer-encoding` 归传输层，不许出现：请求发往、并以之为 `Host` 的永远是 URL 的主机，插件和运营方都改不了（CONTRACTS §112.17）；`Header` / `DefaultHeader` 的空值不写入；同名时权威头生效。
- **目标主机**：URL 的主机必须是清单 `externalServices` 之一（含子域），或这次调用的账号自己的上游地址的主机（账号类型 `upstreamFields` 声明的字段，只比主机名、不含子域）。别的主机核心拒绝（`INVALID_ARGUMENT`，这次尝试按插件不可用换号）。代码里写死的默认地址（如 `api.openai.com`、各区域的 `bedrock-runtime.<region>.amazonaws.com`）要写进 `externalServices`，让运营方填地址的字段写进 `upstreamFields`。每个账号类型都必须写 `upstreamFields`，没有运营方填的地址（OAuth、云厂商按区域拼地址的类型）写 `[]`，没写的清单装不上（CONTRACTS §21.3）。规则是 `manifest.Manifest.UpstreamHostAllowed`，取回、`SendAccountRequest` 和假宿主用同一个（CONTRACTS §112.17）。声明过的主机也可能被管理员在“系统设置 → 插件联网”里屏蔽（或在“屏蔽所有域名”下没列进允许列表，CONTRACTS §113.3）：核心同样什么都不发，这次尝试换号，信息是 `plugin <key>: <host> is on the blocked list of plugin network access`（或 `is not on the allowed list`）。假宿主不模拟这项设置。
- `Body(b)` 是最终发出的字节，核心不再改；`Model(m)` 是记进使用记录的上游模型（类型声明了模型可用性时，核心核对它与自己算的上游模型一致，不一致按插件不可用换号）；`ResponseStream(f)` 见 2.5。超时、代理、TLS、重定向、重试不归插件（超时由管理员配置，SDK 不提供收紧它们的方法）。
- `pluginsdk.RequestModel(in)`：请求体的 `model`，没有则 `meta.model`（都已经过账号的模型映射）。`pluginsdk.RequestStream(in)`：`meta.stream` 或请求体的 `"stream": true` 任一为真。
- 请求体上限（网关设置 `max_request_body_mb`，CONTRACTS §111）只管网关读客户端的请求。核心交给插件的请求体可能因为自己的改写比它大，插件交回的中继请求体、取回的请求体也都不检查大小（CONTRACTS §112.16）。插件不设自己的大小上限。

核心不检查插件交回的请求体（不再比对模型、流式标志），这是插件的责任，由一致性套件（第 4 节）检查：模型引用与交入的一致，交入时流式为真交回仍为真，合法 JSON，没有重复键。

### 2.3 两类请求头

| | 权威头 `Header` / `Headers` / `HeaderMap` | 默认头 `DefaultHeader` / `DefaultHeaders` / `DefaultHeaderMap` |
|---|---|---|
| 写入时机 | 运营方的透传和覆盖**之后**，运营方改不掉 | 运营方设置**之前** |
| 运营方能做什么 | 不能改 | 客户端头透传**只补没有的**（不替换默认头）；请求头覆盖（`header_override`）和参数覆盖的请求头部分可以设值或删除 |

核心交给插件的客户端请求头（`inbound_headers`）已经按清单过滤过：账号类型在这个平台上写了 `passHeaders` 就用它，写成 `[]` 就一个都不交，没写才用平台的列表（`AccountPlatform.ClientHeaders`）。交来的头插件要作为默认头转发（一致性第 4 条），不想转发任何客户端头的类型在清单里写 `"passHeaders": []`（codex 就这样）。

发送时自下而上：插件的默认头 → 透传（账号 `header_passthrough`、`header_override` 的透传键、粘性规则，只补没有的）→ `header_override` 与 `param_override` 的设值和删除 → 插件的权威头 → 没有 Content-Type 时补 `application/json`（CONTRACTS §86.5）。WebSocket 握手同样分层（`websocket.go` `dial` 调同一个 `layerHeaders`）：插件的默认头 → `header_override` 与 `param_override` 的设值和删除（来自第一轮消息的改写）→ 插件的权威头（空值删除），**不套透传**，也不补 Content-Type；插件给的传输层的头（`upgrade`、`sec-websocket-*`、`content-type`、`content-length` 等，`handshakeOwned`）被跳过。

放哪一类：

- 鉴权头（`authorization`、`x-api-key`、`api-key` ……）和签名写入或覆盖的头放权威头；签名时用的 `content-type` 也算。
- 原来是声明式的五个类型（openai、gemini、relay、azure_openai、anthropic 的 `apikey`）改写后**只有鉴权头**是权威头，其余都是默认头，和以前的行为一样（`standard` 库就这样做）。
- 已经发布过的代码插件（codex；0.1.304 删除的 volcengine 也是）的头以前全都在运营方设置之上，改写后**保持全部权威**，运营方的透传和覆盖照旧改不了它们。没有发布过的 vertex、bedrock 按第一条写：只有凭证头（bedrock 的签名类型是签名头）是权威头，`content-type`、`accept` 是默认头。新插件按第一条写。
- 一致性套件第 4 条检查的是“声明的集合”：权威头的名字必须在插件声明的 `Credentials`（鉴权与签名头）与 `Authoritative`（其余要保留为权威的头，每个写理由）的并集里，`Credentials` 的头不能出现在默认头里。

签名的插件不要写 `Headers(req.Header)`（会把请求上每个头都变成权威头），用 `reqsign.AWSSigV4Headers(req)` 只取签名写入或覆盖的那几个。

**不发某个头**：`RemoveHeader(name)` 写入空值的权威头，核心把它当作“这个头不发”，删掉前面各层（默认头、透传、运营方的覆盖）设置的同名头，HTTP 与 WebSocket 握手相同（CONTRACTS §112.17）。用于签名不能看到的头：bedrock 在鉴权头里没有会话令牌时（Access Key 签名的类型，以及不签名的 API Key 类型）`RemoveHeader("x-amz-security-token")`，运营方加的会话令牌就不会发到 AWS、让请求失败。名字同样要在一致性第 4 条声明的集合里。

### 2.4 改请求体：`jsonedit`

所有插件用同一个工具 `sdk/pluginsdk/jsonedit`，不要各写一份，也不要整份 `json.Unmarshal` 再 `Marshal`（重排键、丢重复键、多拷贝一遍）或连串调用 `sjson.SetBytes` / `DeleteBytes`（每改一处整份拷贝一次，大请求体过不了一致性第 10 条）。打开请求体、记下改写、一次拼出结果：

```go
out, err := jsonedit.Edit(in.GetBody(), func(d *jsonedit.Doc) {
	for _, f := range []string{"metadata", "service_tier"} {
		d.Delete(f) // every occurrence; a missing field is no edit
	}
	d.Set("stream_options.include_usage", []byte("true")) // in place, or added at the end of its object
	d.Default("instructions", []byte(`""`))               // only where nothing is
	jsonedit.EachElement(d.Root().Get("messages"), func(m gjson.Result) {
		d.Object(m.Get("cache_control")).Delete("scope") // members of a nested object
		if role := m.Get("role"); role.Str == "system" {
			d.Replace(role, []byte(`"developer"`)) // a value read from the document
		}
	})
})
if err != nil {
	return nil, status.Error(codes.InvalidArgument, err.Error())
}
```

- 没有动到的字节原样保留（未知字段、键顺序、空白、数字写法、`\u` 转义）；什么都没改时返回原切片，不拷贝；改了就只分配一个最终大小的切片。
- 非法 JSON：`Parse` / `Edit` 返回 `jsonedit.ErrInvalid`；交给改写的值也必须是合法 JSON。
- 重复键：`Delete`、`DeleteFunc` 删掉每一个匹配的成员；`Set` 路径上的某个键在所在对象里出现两次是 `jsonedit.ErrDuplicateKey`，整批不出结果。改写之外的重复键不管（核心本来就以 400 拒绝带重复键的请求体）。
- 改写的字节重叠（删了一个成员又改它里面的值）是 `jsonedit.ErrConflict`。
- 同一个成员上的几次改写按调用顺序生效：先 `Set` 后 `Delete` 结果是删掉，先 `Delete` 后 `Set` 结果是设上。
- 路径是点分隔的普通键，没有下标和通配：数组元素先读（`EachElement`）再按值改（`Replace`、`Object`）。

### 2.5 签名与响应流格式

插件自己签名：对自己构造的 `*http.Request` 调 `sdk/reqsign` 的 `SignAWSSigV4`（凭证是 `reqsign.AWSCredentials`：区域、服务名、Access Key ID、Secret、可选的会话令牌），再把签过的 URL 和签名头交给核心：

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
req.Header.Set("Content-Type", "application/json")
creds := reqsign.AWSCredentials{Region: region, Service: "bedrock", AccessKeyID: ak, SecretAccessKey: sk}
if err := reqsign.SignAWSSigV4(req, body, creds, now); err != nil {
	return nil, err
}
return pluginsdk.NewUpstream(req.Method, req.URL.String()). // the URL as signed
	Headers(reqsign.AWSSigV4Headers(req)).                    // only what the signature wrote or covers
	DefaultHeader("accept", "application/vnd.amazon.eventstream").
	Body(body).ResponseStream(awseventstream.FormatChunk).Build()
```

- 请求体在签名之后不再改；签名头是权威头，运营方的覆盖改不了；`host` 在覆盖的禁止名单里。每次尝试重新构造、重新签，时间从可替换的时钟取（一致性第 6、7 条）。
- `AWSSigV4Headers` 返回的头（有才返回）：`Authorization`、`X-Amz-Date`、有会话令牌时的 `X-Amz-Security-Token`、被签名的 `Content-Type`。签名由 AWS 官方的 `aws/signer/v4` 完成，包只决定签哪些头。`AWSCredentials` 的 `String`、`GoString`、`LogValue` 不输出密钥，签名失败的错误也不带密钥。
- `response_stream`：回答不是 SSE 或 JSON 时说明格式，核心解码后照常中继和计量。现有 `aws-eventstream-chunk`（`sdk/awseventstream.FormatChunk`）：内容类型是 `application/vnd.amazon.eventstream` 的 2xx 回答，chunk 事件解成 SSE，异常帧变成 Anthropic 形状的 `error` 事件；别的内容类型原样不动。不认识的名字、WebSocket 轮次上给了名字，核心都拒绝这次中继（按插件不可用换号）。只在这一次请求的回答确实是该格式时设置（bedrock 只在流式 InvokeModel 上设）。
- 账号测试请求（`BuildTestRequest`）核心原样发送，插件同样自己签（bedrock 的 `sign`）。
- 要支持新的签名方案，在 `sdk/reqsign` 里 `SignAWSSigV4` 旁边加一个签名函数（签名只在插件里做，核心不认识签名方案）；新的事件形状在 `sdk/awseventstream` 里加一个格式名，并在 `server/internal/gateway/upstream_ext.go` 的表里登记。不在网关里写厂商判断。

不用厂商的 SDK 客户端（也不做 `RoundTripper` 适配）：插件和核心里只用 AWS 的签名与 event stream 库和 `golang-jwt`；OpenAI、Anthropic 的官方 SDK 只允许用作 e2e 测试的客户端。

### 2.6 取回：`Fetch`

`pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{...})` 发一个非流式的请求，回答整份交还插件。用于 `Execute` 里的辅助请求和 `Monitor` 里的任务查询。

- 主机：和中继同一条规则（2.2）：清单 `externalServices` 之一或其子域，**或者是这次调用的账号自己的上游地址的主机**（账号类型 `upstreamFields` 声明的字段，从账号的设置和凭证里读，只比主机名，不含子域）。别的主机 `PERMISSION_DENIED`。规则是 `manifest.Manifest.UpstreamHostAllowed`，核心和假宿主共用。`SendAccountRequest`（请求路径之外用账号凭证发请求，如查余额、codex 查重置次数）也按它判断，请求体和回答都不限大小。
- 核心经账号代理、过 SSRF（账号地址指向私网时按账号所在范围的“允许私网”判断；经代理的账号不在本机解析上游域名，只拒绝字面量的私网地址和 localhost，`netguard.CheckHostVia`）、不跟随重定向、不重试；**不套**运营方的透传和覆盖（它们描述的是客户端的请求）；拒绝 `Host`、hop-by-hop 和代理认证头。超时：`Execute` 里是账号的响应头超时和 `relay_timeout_seconds`（没设就没有整体超时），`Monitor` 里 20 秒；插件不能设超时（请求的 `options` 字段已从协议删除）。
- 不限次数、不限大小：一次调用取回多少次都行，回答整份交还（读不完整是传输错误；`truncated` 字段已删除）；URL、请求头、请求体也不限大小（CONTRACTS §112.16；`FetchLimit`、`FetchResponseLimit` 已删除）。没有了次数上限，循环取回的插件要自己保证会停。传输失败不是错误：回答的 `status` 为 0、`transport_error` 有值（文字固定，原因只进核心日志）。
- 不需要 `net` 授权。取回在主请求之前失败时可以安全换号：返回 `Classified(c)`（或自己 `ReportClassification(c)`）让核心按分类裁决。
- **建账号之前的授权**（设备码、OAuth 换令牌等，账号还不存在）也用 `Fetch`，在插件自己的管理路由里发，带上账号表单的代理票据（CONTRACTS §117）：
  - 原生账号表单收到 prop `proxyTicket`（`@sub2api/host` 的 `ProxyTicketSource`，UI 3.20 起），**每次**调用插件的授权路由（开始、每次轮询、换令牌、粘贴回调）之前都调用它现取一张票据，放进请求体（约定字段 `proxy_ticket`）；
  - 插件用 `pluginsdk.RouteProxyTicket(req)` 读这次调用的票据（缺了回 400），这次调用里的请求都 `Fetch(ctx, &pluginv1.ExecutionHTTPRequest{..., ProxyTicket: ticket})`，核心从表单**此刻**选的代理发出（没选就直连），插件看不到代理（建账号之前；账号建好以后代理随 `Account` 下发，2.8a）。票据不要存进授权会话沿用：授权开始后管理员改了代理，下一次调用就该走新代理；
  - 路由调用里的取回只能到 `externalServices`，必须带票据；票据只对发它的那个用户、这个插件有效，核心每次按该用户当时的权限复查。传输失败是 `transport_error`（可以重试），RPC 错误是核心拒绝发送（票据或代理不可用）：请求没有发出，不要因此结束授权会话，管理员把表单的代理改好后，下一次调用带着新票据就能发出；
  - 不需要 `net`。codex 是范例（`plugins/codex/internal/codex/oauth.go`）；测试里 `Harness.DoAs` 会给路由调用发执行令牌，假宿主要求路由取回带票据，用 `ExecuteHTTPFunc` 看到它。
- 插件自己的其他对外连接（Webhook、SMTP 等，不是转发、不是取回、也不是建账号前的授权）走出口隧道，需要 `net` 授权，还要通过管理员在“系统设置 → 插件联网”里的域名设置（默认放行，可以屏蔽某些域名，或改为只允许列出的域名，CONTRACTS §113）。被拒的拨号返回错误、在插件的外部访问日志里写明原因；插件要把这类错误当成配置问题报告，不要重试。这项设置同样管核心替插件发出的请求：中继、取回、`SendAccountRequest` 被它拒绝时与主机规则拒绝时一样（`Relay` 换号，取回和 `SendAccountRequest` 回 `PERMISSION_DENIED`，信息写明是插件联网的屏蔽或允许列表）；配额查询、凭证续期的请求被拒时插件拿到 `transport_error`（`status` 0），按传输失败处理；账号测试和拉取模型列表的失败显示给管理员。这类拒绝不要重试，也记在插件的外部访问日志里。

### 2.7 插件自己解析用量的端点

清单端点声明 `usageSource: "plugin"`（规则表达不了用量时）。用 `ExecuteDefault` 的插件只要再实现 `UsageExtractor`：

```go
func (p *Plugin) ExtractUsage(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.UsageReport, error) {
	n := gjson.GetBytes(in.GetBody(), "usage.total_tokens")
	if !n.Exists() {
		return nil, errors.New("no usage in the response") // -> host_usage fallback
	}
	return &pluginv1.UsageReport{Tokens: &pluginv1.UsageTokens{OutputTokens: n.Int()}}, nil
}
```

观察：非流式是整个回答体，流式是 `usageStreamEvents` 列出的每一条事件，都不限大小（清单的 `usageMaxBytes` 已删除）。`usageRequestFields` 声明的请求字段整个交给插件（`fields_omitted` 已从协议删除）。回答本身已经由核心中继给客户端。只有声明端点的插件自己的账号类型能服务这类端点：别的插件的账号类型声明了这样的平台，注册表绑定时把那个平台从它的平台里去掉（CONTRACTS §112.8）。WebSocket 端点不能声明 `usageSource: "plugin"`。

### 2.8 任务：提交与 `Monitor`

任务插件声明 `platform.adapter.v1`、`platform.monitor.v1`、`platform.tasks.v1`，实现 `TaskSubmissionParser` 和 `TaskMonitor`：

```go
func (p *Plugin) ParseTaskSubmission(_ context.Context, in *pluginv1.ExtractUsageRequest) (*pluginv1.TaskSubmission, error) {
	id := gjson.GetBytes(in.GetBody(), "id").String()
	if id == "" {
		return nil, status.Error(codes.Internal, "upstream accepted the task without an id")
	}
	return &pluginv1.TaskSubmission{UpstreamRefId: id, SnapshotJson: string(in.GetBody()), NextCheckAfterSec: 5}, nil
}

func (p *Plugin) Monitor(ctx context.Context, in *pluginv1.PollRequest) (*pluginv1.ReconcileResult, error) {
	answer, err := pluginsdk.Fetch(ctx, &pluginv1.ExecutionHTTPRequest{
		Url:     baseURL + "/tasks/" + in.GetEntry().GetRefId(),
		Headers: map[string]string{"authorization": "Bearer " + key(in.GetAccount())},
	})
	if err != nil {
		return nil, err
	}
	if early := pluginsdk.ClassifyPollResponse(answer.GetStatus(), answer.GetTransportError()); early != nil {
		return early, nil // transport error, 404/410, non-2xx
	}
	return parseTaskState(answer.GetBody()), nil // PENDING or a terminal state
}
```

- 提交：`ExecuteDefault` 做 `RelayAndWait` → `ParseTaskSubmission` → `ReportTask(task)`。解析失败时返回错误：任务不登记，客户端报错，预扣释放。`task.usage.reserve` 是更准的第二次预估，核心登记任务时原子地替换请求前的预扣。插件拿不到任务 ID 和回执。
- 观察：核心认领到期任务后调 `Monitor`（租约、调度、原账号都归核心）。`Monitor` 只能 `Fetch`、不能中继；结果在返回值里，返回 `nil` 或报错算一次查询失败。终态只在这次调用里有一次完整、成功的 `Fetch`（拿到 HTTP 状态、没有传输错误）时才被接受。结果不限大小。`PollFailure`、`TaskNotFound` 构造对应的结果。

### 2.8a 账号的全部信息与代理（CONTRACTS §124）

- 插件收到的每个 `Account`（`Execute`、`Monitor`、`ClassifyError`、测试、模型（已有账号的；建账号之前按表单拉模型时还没有账号，不给代理，请求经表单选的代理发出）、额度、续期、余额、用量观察）都是它自己账号类型的账号，带解密的凭证和这次调用出站用的代理（`Account.proxy`：id、协议、地址、端口、用户名、密码；直连时不设）。不需要任何宿主授权。WebSocket 会话里 `Account.proxy` 是上游连接走的代理，会话期间不变；同一轮里的取回按当时已保存的代理走，管理员中途改了代理时两者可以不同，新会话按新代理（CONTRACTS §124.2）。
- 请求之外要读账号（插件自己的管理路由），用 `Host.Account(ctx, id)`（`HostService.GetAccount`）：别人的账号和不存在的一样 `NOT_FOUND`，代理拿不到时 `UNAVAILABLE`；每次调用写一条审计，所以每个请求每个账号最多读一次，不缓存明文、不写进插件自己的表。列账号用 `Host.ListAccounts`。
- 代理只是数据：**不得用于自行联网**。账号的所有请求仍只经核心发出（`Relay`、`RelayAndWait`、`Fetch`、`SendAccountRequest`），核心强制这个代理；SDK 不提供把代理变成传输或拨号器的函数。
- 读代理用 `pluginsdk.AccountProxyOf(acc)`，它打印、`slog` 时不出现密码；不要打印 `Account` 或 `AccountProxy` 消息本身。
- 测试：假宿主 `pluginsdktest.FakeHost.Accounts` 回答 `GetAccount`，`AccountReads` 记录调用。

### 2.9 不要做的事

- 不开套接字、不用 `http.DefaultClient` 或厂商 SDK 客户端；对外只有 `Relay`、`RelayAndWait`、`Fetch`（请求之外是 `SendAccountRequest`）。`Account.proxy` 不用来自己联网（2.8a）。
- 不重试、不换号、不按请求缓存构造结果（`attempt > 0` 时从头构造）；按账号缓存令牌可以。
- 构造里不直接 `time.Now()`、不遍历 map 拼字符串（确定性）；把 `ctx` 传给 `Fetch`、`Relay`，不要用 `context.Background()`（取消）。
- 不在插件里实现限流、并发、粘性、价格、断开计费、自动禁用这类核心统一处理的逻辑；要让账号禁用到某个时刻，实现 `ClassifyError`。
- 不另设请求体大小上限，不另设插件设置让运营方重填系统设置（用第 6 节的接口读）。

## 3. `standard` 库与厂商预设

“账号 = API Key + 基础地址，请求 = 基础地址上按上游协议选的路由，Key 在请求头里”这一类账号类型，用 `sdk/pluginsdk/standard` 实现全部平台接口：openai、gemini、relay、azure_openai 四个插件和 anthropic 的 `apikey` 类型都是这样写的。它是链接进插件自己进程的 Go 库，不是运行器，也没有清单字段：插件用 Go 写一个 `standard.Type`（通常从 `sdk/pluginsdk/providers/{openai,anthropic,gemini}` 的预设 `APIKey(id)` 出发再改几处），交给 `standard.New`，得到的 `*standard.Platform` 实现 `Platform`、`UpstreamBuilder`、`ModelLister`、`BalanceReader`，SDK 用 `ExecuteDefault` 跑它的 `Execute`。接口、构造步骤和完整例子见 [`sdk/pluginsdk/standard/README.md`](../sdk/pluginsdk/standard/README.md)。

一个上游同时说几家协议（中转站：New API、Sub2API）时，用 `standard.Merge(id, spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""), …)` 把几份预设合成一个类型：每条路由保留自己那份预设的鉴权头（OpenAI `Authorization: Bearer`、Anthropic `x-api-key` 加默认 `anthropic-version`、Gemini `x-goog-api-key`）、请求头和失败分类（路由级的 `Route.Auth`、`Route.Classify`）；`spec` 换掉各预设的凭证规则（中转站的地址必填、没有默认）。账号测试按核心给的协议顺序选（`Type.Tests`，合并时收集各份的测试）：运营方用协议绑定只开 Messages 的账号就测 Messages。预设按协议带了最小的测试请求（CONTRACTS §122.3：openai 的 chat、Responses、Embeddings、生图，gemini 的 generate 与流式 generate）；只改路由的类型要同时去掉没有路由的协议的测试（azure-openai 的做法）。核心交给插件的协议已去掉清单端点 `test: never` 的协议，没点名模型时也去掉 `test: explicit` 的（生成一张图要花钱）；自己写 `BuildTestRequest` 的插件同样取 `protocols` 里第一个它会测的协议，在 `usage_protocol` 报实际用的，控制台会显示它；列表不空而一个都不会测时返回 `FailedPrecondition`，不要退回别的协议的测试（`standard` 就这样做），空列表才表示没有偏好。账号余额写 `Type.Balance`：基础地址上的一个或几个 GET（`BalanceRequest.Auth: &standard.Auth{}` 的请求不带 Key），`Read` 把按顺序的回答读成结果，`standard.BalanceFailure`（传输错误、429、5xx、其它非 2xx → TRANSIENT，401/403 → AUTH_REJECTED）和 `standard.Micros`（把上游答的十进制数换成 `amount_micros` 的百万分之一整数，同一个数换个写法）帮着读；**余额是上游的数字本身，单位（`currency` 字段）是上游给的原文、没给就留空，不做任何单位或汇率换算**（CONTRACTS §52）；清单里同时写 `"balance": {}`。TypeSafe 的平台定义与预设在 `sdk/pluginsdk/providers/typesafe`（`Platform()` 是平台的唯一来源，`APIKey(id)` 是预设）。

旧的清单 `upstream` 声明（已删除）的每一项在库里的对应：

| 原清单写法 | `standard.Type` |
|---|---|
| `preset` | 从 `openai.APIKey`、`anthropic.APIKey`、`gemini.APIKey` 出发 |
| `baseURL`（`default`、`required`、`stripSuffixes`）、`credentials.texts` | `Spec`（`apikey.Spec`：`DefaultBaseURL`、`RequireBaseURL`、`Versions`、`Texts`）。`stripSuffixes` 对应 `Versions`，但含义变了：地址按填写的保存，请求时才去掉末尾至多一个版本段（CONTRACTS §127）。`credentials.keyValidator` 没有对应：密钥只要求是非空字符串（2026-11-20 起，`KeyValidator` 已删除） |
| `auth`（header / none） | `Auth{Header, Prefix}`；`Header` 空 = 不带凭证。鉴权头是唯一的权威头。查询串里的 Key（`Auth.Query`）已删除：没有使用者 |
| `headers` 的 `set` / `default` / `append` | `Headers []Header`：`standard.Set`、`standard.Default`、`standard.AppendTo`；值是 `standard.Fixed`、`standard.Field(账号字段)` 或自己的函数，空串不写 |
| 模板 `{credentials.<k>}` | `standard.Field("k")`（先凭证后设置）；`Request.APIKey`、`Request.BaseURL` |
| `routes`（`path`、`transport`、`model`、路由 `headers`、`patches`） | `Routes map[string]Route`：`Path`（函数，路径已转义）、`WebSocket`、`Model{FromMeta, TrimPrefix, Required, Missing}`、`Headers`、`Edit func(*jsonedit.Doc, *Request)`（在删掉 `FilterFields` 的同一次 `jsonedit` 里接着改）。HTTP 路由一律 POST、WebSocket 路由一律 GET（`Route.Method` 已删除） |
| `defaultProtocol` | `DefaultProtocol` |
| `filterFields` | `FilterFields`（每个请求体删掉的路径）；预设不带，要过滤的类型自己写 |
| `paramOverride`（清单里的固定改写） | 没有对应：写进 `Edit`。三个预设和五份清单里都没有用过它 |
| `classify` | `Classify`（`classify.Policy`） |
| `test`、`models` | `Test`、`Models`（nil 回 `UNIMPLEMENTED`） |
| 类型的 `passHeaders` | 没有对应：只在清单里写。核心按清单过滤后才交给插件，`standard` 把交来的每个客户端头（小写、去空白、空值不写）都作为默认头写出（`Type.PassHeaders`、`PlatformPassHeaders` 已删除） |

两个例子：

- **azure_openai**（`plugins/azure-openai/internal/azureopenai`）：两个类型都从 openai 预设出发，改了这几处：`Spec` 去掉默认地址、必填、版本段是 `/openai/v1` 和 `/openai`（`Versions`，请求时从地址末尾去掉一段）；`Auth{Header: "api-key"}`；路由逐条给出（v1 类型 `/openai/v1/...`，经典类型 `/openai/deployments/<部署名>/...?api-version=<设置>`，模型必填）；`Models = nil`；自己写过滤字段 `service_tier`、`safety_identifier`（预设不带）。客户端请求头的名单在清单里：openai 平台的列表去掉 `openai-organization`、`openai-project`。两个类型是因为路由不能按账号设置分支。验收断言在插件自己的 `platform_test.go`、`manifest_test.go`（SDK 里原来那份抄写的测试已删除）。
- **openai** 的组织与项目：`standard.Set("openai-organization", standard.Field("organization"))`、`project` 同样。账号没填时值为空、不写，客户端经透传带来的头保持原样；填了就以默认头覆盖客户端的（运营方的请求头覆盖仍可改它）。

**顺序变化（与改写前不同的一处）**：过滤字段和路由改写现在在插件里，排在核心的改写（`claude_max_tokens`、账号参数覆盖）**之后**；以前声明式类型的过滤字段在核心里、排在账号参数覆盖之前。运营方在账号的参数覆盖里写了一个被过滤的字段时，以前发给上游，现在被插件删掉。目前只有 azure_openai 的两个类型写了过滤字段（`service_tier`、`safety_identifier`），所以只在 azure_openai 上可见（CONTRACTS §112.14）。

## 4. 测试

**假宿主**（`sdk/pluginsdk/pluginsdktest`）：

```go
h := pluginsdktest.Start(t, New(), pluginsdktest.Options{Manifest: manifestJSON})
ex := h.Execute(ctx, &pluginv1.ExecuteRequest{
	Meta:    &pluginv1.RequestMeta{Protocol: "anthropic.messages", Model: "claude-x", Stream: true},
	Account: &pluginv1.Account{Id: 1, Type: "apikey", CredentialsJson: `{"api_key":"sk-test"}`},
	Body:    []byte(`{"model":"claude-x","stream":true}`),
})
relay := ex.Relayed(t) // exactly one relay, the call succeeded
if relay.Wait || !proto.Equal(ex.Response, pluginsdk.ReportHostUsage()) { ... }
```

- `Harness.Execute` / `Harness.Monitor` 像核心一样发执行令牌、调用、调用返回后作废令牌；返回 `*Execution`（`Response`、`MonitorResponse`、`Err`、`Relays()`、`RelayErrors()`、`Fetches()`、`Relayed(t)`）。
- `h.Host.RelayFunc` 编排等待形式的回答（`pluginsdktest.Observed(200, body)`、`pluginsdktest.UpstreamFailed(429, body, "")`）；`h.Host.ExecuteHTTPFunc` 编排取回；`Options.Manifest` 是插件的 `manifest.json`：harness 用它起插件（等于 `pluginsdk.WithManifest`），假宿主也知道它；`h.Host.Manifest` 是宿主知道的清单（没有事先设置时取 `Options.Manifest`），中继和取回能到哪些主机按它和这次调用的账号决定（`manifest.UpstreamHostAllowed`；中继到别的主机 `INVALID_ARGUMENT`），没有清单时只有 `h.Host.ExternalServices` 放行的主机（测试指向 `127.0.0.1` 的假上游时在这里写上它）；`h.Host.ExpireExecutions()` 模拟核心中途放弃调用。假宿主自己执行核心的检查（令牌、一个中继、没有标志不许等待、`Monitor` 不许中继、方法和头名按 `httpguts`、HTTP 协议上不许 ws(s) 地址、`Monitor` 的终态要有一次完整成功的取回）；和宿主一样不限请求体、不数取回、不截回答。不经 `Harness` 时用 `h.Host.Begin(in)` / `BeginMonitor(in)` 发令牌。仍与宿主不同的地方写在 `FakeHost` 的注释里。
- 只测构造时直接调 `p.BuildUpstream(ctx, in)` 比较结果。
- `h.Balance(ctx, account, answer)` 像核心一样做一次余额查询：`BuildBalanceRequest` 的每个请求先按清单的主机规则判断（有一个不行就都不发，`PERMISSION_DENIED`），再逐个交给 `answer`（不跟随重定向，nil 算传输错误），回答按顺序交给 `ParseBalanceResponse`，返回结果（单位照插件给的原样，可以为空，与核心相同）。

**一致性套件**（`sdk/pluginsdk/pluginsdktest/conformance`）：平台插件在自己的测试里调一次 `conformance.Run(t, plugin, cases...)`，逐条检查 12 条规则：恰好一次中继、形式与返回种类、交回的请求体、请求头（声明的集合）、不重试、确定性、换号、不开套接字、取消、大请求体（额外分配不超过请求体的 4 倍）、WebSocket、余额（清单声明 `balance` 的类型要读得出余额，请求的主机过主机规则，传输错误和空回答给出错误结果；没声明的回 `UNIMPLEMENTED`）。账号类型服务别的插件声明的平台时（New API 服务 TypeSafe 平台），把那个平台放进 `Plugin.Platforms`（`typesafe.Platform()`），套件就认得它的协议和客户端请求头。输入表、例外的写法和套件证明不了的事见 [它的 README](../sdk/pluginsdk/pluginsdktest/conformance/README.md)。九个平台插件都调用它（`Plugin.Manifest` 必填，交给插件的客户端请求头从清单读）。大请求体的大小（8 MiB）和取消的等待时限（2 秒）是套件内部的常量，`Plugin` 没有对应字段。每个 HTTP 协议要在核心会发的每种形式下都有用例，形式由套件从协议的端点读出，插件不能声明自己不服务某种形式；WebSocket 协议不列在 `Protocols` 里，它的轮次只归第 11 条。只有 bedrock 声明了例外，都是 InvokeModel 本身的性质：流式用例的 `03_body`（InvokeModel 按 URL `invoke-with-response-stream` 选流式，请求体里带 `stream` 会被拒绝）和全部用例的 `04_headers`（客户端的 `anthropic-beta` 放进请求体的 `anthropic_beta`，Bedrock 不收这个请求头）。

**黄金样例**（`sdk/pluginsdk/standard/goldentest`）：转发 V2 之前（四个插件还由已删除的声明式引擎驱动时）录下的 1498 条样例（openai 441、anthropic 343、gemini 371、relay 343），转换成不依赖引擎的 `execute`、`test`、`models`、`validate`、`classify` 样例并冻结，用 `standardtest.Run(t, plugin, manifestPath, p)` 回放：它先核对插件清单交给样例账号类型的客户端请求头与文件的 `pass_headers` 完全一致，再像核心一样按 `pass_headers` 过滤样例里故意多给的客户端请求头，并检查每个请求的主机是核心会中继的（2.2）。用 `standard` 写的插件都要全部通过。样例不是本插件录的、但它也服务这些协议时（`Merge` 出的中转站类型），用选项只回放它必须答得一样的用例：`standardtest.Run(t, "openai", "../../manifest.json", p, goldentest.Select(goldentest.Forwarded("apikey", openai.ProtocolChat, …)))`（只取这些协议、账号自带基础地址的 `execute` 用例；凭证规则不同的 `validate` 等不取），清单交给类型的客户端请求头按样例协议所属的平台核对。结构和例外见 [它的 README](../sdk/pluginsdk/standard/goldentest/README.md)。

## 5. 设置对照表

“核心统一”表示插件什么都不用做；“插件配合”写明插件要实现或声明什么。表里说的都是网关请求上的生效方式；插件在请求之外（定时任务、事件处理、自己的 HTTP 路由）要按这些设置行事时，用第 6 节的接口读取当前值，不要让运营方在插件设置里再填一遍。“等待形式”指第 1 节表里的后两行。

### 5.1 计费（系统设置 → 计费）

| 设置 | 归属 | HTTP / WebSocket | 说明与位置 |
|---|---|---|---|
| 最低余额、高额费用警告（没有价格的模型一律拒绝，不是设置，CONTRACTS §17.3） | 核心统一 | 一致 / 每轮检查 | `pipeline_billing.go` `prepareBilling` |
| 请求预扣 Token 数、输出预留 | 核心统一；**插件配合**：可实现 `EstimateUsage` 给出更准的估算，返回 `Unimplemented` 时核心用本地 tokenizer（`task.submit` 端点必须实现） | 一致 / 每轮 | `precharge.go`，CONTRACTS §25.8 |
| 客户端中途断开时（读完再计费 / 按预扣计费 / 不计费） | 核心统一 | 一致（等待形式同样） / 不续读，按预扣（契约规定） | `disconnect.go`；CONTRACTS §25.8。终止用量事件送达客户端之后才断开的请求按成功计费（三种策略都一样）；等待形式下插件放弃中继调用（被终止、进程退出）记为上游中断，不算客户端断开 |
| 用量来源 | **插件配合**：在端点或账号类型上声明 `usage` 规则；规则表达不了时声明 `usageSource: "plugin"` 并实现 `UsageExtractor`（第 2.7 节） | 一致 / WebSocket 不支持 `usageSource: "plugin"`（清单检查拒绝） | `forward.go`、`waited.go`、`sdk/manifest/check/websocket.go` |

流式用量规则的写法会影响断开判定：**输出 token 数要映射在一个指名的终止事件上**（例如 Anthropic 的 `message_delta`、Responses 的 `response.completed`），核心据此判断“最终用量已经送达客户端”（`usagerules.Acc.Final`）。映射在任意事件（`"event": ""`）上的规则不会被当成终止，只能以读到流结束为准。

### 5.2 网关设置与账号的请求改写

| 设置 | 归属 | HTTP / WebSocket | 说明与位置 |
|---|---|---|---|
| 协议转换、账号参数覆盖（请求体）、Claude 默认 max_tokens 内置覆盖、强制流式 | 核心统一，在交给插件**之前**执行；插件拿到的是改写完的请求体 | 一致 / 每轮 | `upstreamBody`、`rewriteBody`；CONTRACTS §84、§84.1、§88。插件在其后的改写（过滤字段、厂商清洗）可以改掉覆盖的结果，见第 3 节的顺序变化 |
| 账号参数覆盖（请求头）、请求头覆盖 `header_override`、请求头透传 | 核心统一；**插件配合**：按 2.3 把头分成权威头和默认头 | 默认头可被覆盖设值或删除，透传只补没有的；权威头不受影响，空值的权威头删掉同名头 / 握手按同样的层次套默认头、`header_override` 与 `param_override` 的请求头部分、权威头（空值删除），透传不生效（有意如此，`websocket.go` `dial`）；请求头覆盖是参数覆盖的初始请求头状态，和第一轮消息的改写一起执行：改写失败（或已存的请求头覆盖编译不过）时这次尝试结束、不握手，`return_error` 按它的设置回给客户端或换号，其余换号，都不罚账号 | `upstreamRequest`；CONTRACTS §84、§86.5、§108 |
| 上游只接受流式（`streamOnly`） | **插件配合**：在账号类型路由上声明；核心在交给插件前把流式写进请求体，插件交回时保持 | 一致 / 不适用 | `streamonly.go`，CONTRACTS §83 |
| 流开头扣住（上游协议 `openai.responses` 的流式回答） | 核心统一，插件不用做什么：开头的 `response.created`、`response.in_progress` 扣到第一个别的事件再转发，之前的 `error` / `response.failed` 按 HTTP 500 交给插件的 `ClassifyError` 和故障策略 | 只管 HTTP / WebSocket 轮次不扣（照旧在轮次里结束），流内错误事件同样记 WARN | `streamhead.go`，CONTRACTS §138 |
| 请求体上限（网关设置 `max_request_body_mb`，默认 128 MB） | 核心统一，是唯一的请求大小上限，只管网关读客户端的请求；插件和清单都不能另设，核心与插件之间也不由它推出任何上限 | 一致 / 作为单条消息上限 | `bodyLimit`、`readBody`；CONTRACTS §111 |
| 平台调用超时（网关设置 `platform_call_timeout_ms`） | 核心统一，只管不等待形式的 `ClassifyError` 和读断开策略；`Execute` 不计时 | 一致 / 每轮的 `Execute` 同样不计时 | `gateway/settings.go` `platformTimeout`；CONTRACTS §111.4 |
| 上游整体超时、响应头超时、流式空闲超时（环境变量） | 核心统一，插件不能设超时；`Fetch` 在 `Execute` 里用同样的响应头超时和整体超时 | 一致（`Execute` 没有另外的总时限）/ 握手没有固定时限（随客户端离开结束），每轮受流式空闲超时约束，不读的一方由心跳 ping 结束 | `upstreamTimeouts`、`newIdleBody`；CONTRACTS §112.16 |

### 5.3 调度、限流与保护

| 设置 | 归属 | HTTP / WebSocket | 说明与位置 |
|---|---|---|---|
| 优先级、权重、调度策略、均衡负载 | 核心统一；插件可选实现 `RankAccounts` | 一致 / 首轮调度，后续轮复用连接 | `dispatch.go` |
| 账号的协议绑定：本平台协议（`protocols`）、模型的协议限定与显式协议转换（`model_protocols`） | 核心统一，在调度时过滤账号，早于转换和插件的 `Execute`；插件什么都不用做，清单里也没有对应的声明 | 一致 / 首轮调度时判断；后续轮换成该账号在这个协议下不提供的模型返回 400，要新建连接 | `sched_trace.go` `ineligibleReason`；CONTRACTS §110。账号能配哪些协议来自账号类型的 `platforms[].endpoints`：声明过的平台的协议是本平台协议（默认开放），其他平台的协议要经转换、默认关闭 |
| 粘性会话 | 核心统一（规则按协议匹配，内置规则不设模型和 UA） | 一致 / 首轮 | `stampSticky`；CONTRACTS §15.5 |
| 最大并发、RPM、TPM、会话数限制、Key 限额、用量限制 | 核心统一；`Fetch` 算在同一次执行里，不另占 | 一致 / 每轮重新准入和占槽 | `acquireAccount`、`admitRate`、`admitKey` |
| 账号代理、SSRF | 核心统一：中继和取回都经账号代理，插件不能选也不能关；经代理的账号由代理解析上游域名，核心不在本机解析，只拒绝字面量的私网地址和 localhost | 一致 / 握手经账号代理，检查相同 | `send`、`fetch.go`、`ssrf.go`、`netguard.CheckHostVia` |
| CPU 保护 | 核心之前，由节点网关处理 | 与传输无关 | `next/gateway` |
| 提示词审核 | 审核插件以钩子实现，平台插件不用做什么 | 一致 / 每轮 | `runHooks`、`runAttemptHooks` |

### 5.4 故障策略与订阅额度

| 设置 | 归属 | HTTP / WebSocket | 说明与位置 |
|---|---|---|---|
| 故障策略、冷却、自动禁用 | 核心统一；**插件配合**：实现 `ClassifyError`，故障策略下核心只采用其中的额度观测和限时禁用；传输错误完全由策略决定。插件不可用（`Execute` 报错或崩溃、中继被核心拒绝、目标主机被拒）不经账号规则：有没有故障策略都只换号、不罚账号；客户端已经离开时插件返回的分类不采用 | 不等待形式由核心调 `ClassifyError`；等待形式由插件在返回值里给 `classification` / 握手和流内错误事件都分类 | `applyClassification`、`waited.go`；CONTRACTS §69 |
| 订阅额度窗口（5 小时、周） | **插件配合**：在账号类型上声明 `quota.headers` | 一致 / 握手头和带头的错误事件 | `observeQuota`；CONTRACTS §44、§105 |

## 6. 读取系统设置与变更通知（核心 0.1.301 起）

插件可以只读地读取下表的系统设置分组，并在管理员保存后立即得知（CONTRACTS §107）：

| 分组 | SDK 类型 | 典型用途 |
|---|---|---|
| `billing` | `protocol.BillingSettings` | 自己结算或预估费用的插件按同样的最低余额、断开计费策略处理 |
| `gateway` | `protocol.GatewaySettings` | 自己调上游的后台任务参照重试次数、超时、媒体下载上限、请求体上限 |
| `scheduling` | `protocol.SchedulingSettings` | 排序插件（`RankAccounts`）参照均衡负载的指标 |
| `sticky` | `protocol.StickySettings` | 粘性会话关闭时放弃排队的绑定清理（dynweight 0.5.11） |
| `auto_disable`、`failure_policy` | `protocol.AutoDisableSettings`、`protocol.FailurePolicySettings` | 判断核心会不会自动禁用、冷却账号，避免插件重复处理；故障策略的开关按 `schema_version` 解读 |
| `environment` | `protocol.EnvironmentSettings` | 全局层的环境变量；分组层、账号层用 `GetEnvironment` |

```go
sticky, err := pluginsdk.Settings[protocol.StickySettings](ctx, p.host)
if err != nil {
	// 从未读成功过且宿主读不到：按插件自己的安全默认继续
}
if !sticky.Enabled { ... }
```

- 不用授权，不用在清单里声明能力；只读，不能改设置。
- SDK 在进程里缓存每个分组，核心在任一节点保存设置后会通知每个节点上的插件进程（`PluginService.SettingsChanged`，SDK 已实现），缓存随即作废；另外最多缓存 10 秒。缓存失效时并发调用只读宿主一次；读取失败时返回上次读成功的值，2 秒内不再读宿主，从未读成功过才返回错误（宿主的读取失败一律是 `UNAVAILABLE`）。所以在用到的地方直接调用即可，热路径（如 `RankAccounts`）也一样：命中时不访问宿主，只解码一次 JSON。
- 值与核心实际使用的一致（默认值、范围收敛都已算好），字段说明见 CONTRACTS §107.2。故障策略只给开关和 `schema_version`，规则不开放。
- 需要在变更后做一次性的工作（重算、通知）时，另外订阅 `settings.changed` 事件（`app.events.v1`，payload `{"sections": [...]}`），每个插件由一个节点接收，至少投递一次（按事件 id 幂等）。
- 测试：`pluginsdktest.FakeHost.SetSettings(v)` 设置分组（没设置的分组回 `UNAVAILABLE`），`Harness.SettingsChanged(sections...)` 模拟变更通知；读取失败后 SDK 2 秒内不再读宿主，测试里补设分组后要调一次 `Harness.SettingsChanged`。

## 6.1 插件自己的表：`Host.DB` 与 DSN

插件的表放在核心所用的模式里（线上是 `public`），和核心的表、别的插件的表并列；隔离靠名字前缀和数据库身份，不靠 schema（CONTRACTS §130）。

- **声明**：manifest 声明 `database`（`{"migrations": "migrations/"}`，没有 `schema` 字段）并获授 `db.schema`。
- **只能建表、索引、序列、视图和类型，名字带前缀**：迁移能建的对象只有关系（表、索引、序列、视图、物化视图）和类型（枚举、域、范围、复合类型），一律命名 `plg_<插件 id>__<本地名>`（`manifest.TablePrefix(id)`，例如 `plg_moderation__rules`；插件 id = 发布者 + key，第一方插件就是 key，其他发布者的是 `_<发布者>__<key>`，如 `plg__erio__codex__rules`，见第 10 节），名字不超过 63 字节（PostgreSQL 会不报错地截断更长的名字）。函数、过程、触发器、运算符、规则、策略等其他对象一律不允许，带前缀也不行；`DO` 块可以用（`db diff` 用它包新约束），它建的对象照样要守这条规则。PostgreSQL 自动起的名字也算：主键索引 `<表>_pkey`、唯一约束的索引 `<表>_<列>_key`、`serial`/`bigserial`/identity 列的序列 `<表>_<列>_seq`，表名带了前缀它们也就带上；自己起名的索引和约束（`CREATE INDEX`、`CONSTRAINT ... UNIQUE`）要自己写上前缀。GORM 模型里的索引必须写带前缀的名字（`index:<名字>`、`uniqueIndex:<名字>`）：不写名字的 `index`、`uniqueIndex` 由 GORM 起名 `idx_<表>_<列>`，没有前缀，`db check` 直接报错；不带名字的 `unique` 在迁移里已有该列的唯一约束时没问题，否则 GORM 起的 `uni_<表>_<列>` 也没有前缀，`db diff` 生成的迁移过不了检查，报错时提示改写成 `uniqueIndex:plg_<key>__<名字>`。插件键不含 `__`、不以 `_` 结尾、最多 30 字节，所以一个插件的前缀不会是另一个插件前缀的开头。
- **一个身份**：每个插件有一个自己的数据库身份（登录角色 `plg<库OID>_<key>`），密码由核心主密钥派生，只在核心进程里。插件的表归这个身份所有；核心的表、别的插件的表它没有任何权限，漏写前缀去改核心的表会被数据库拒绝。
- **连接**：`Host.DB(ctx)` 拿 GORM 句柄，每次调用重新取（CONTRACTS §2“插件数据库”）。句柄背后的 DSN 来自 `HostService.GetDSN`，不论是否开严格网络都是虚拟地址 `postgres://plugin@db.sub2api:5432/...?sslmode=disable`，没有密码；SDK 按地址选路，虚拟地址一律经出口隧道到核心的数据库代理，核心以插件的身份登录（TLS、密码认证都在核心），把会话的 `search_path` 固定为核心模式，之后原样转发协议；语句的 ctx 结束时 pgx 发的取消请求也由核心转给服务器。`GetDSN` 返回的 `schema` 是核心模式。插件进程里没有数据库凭据。
- **连接数**：由插件自己的池决定（默认每节点 4 个，`pluginsdk.WithDBPoolSize`），每条都是插件身份对数据库的一条连接，运维按它规划 `max_connections`。
- **迁移以插件身份执行**：核心在跑脚本前临时给身份核心模式上的 `CREATE`，以该身份另开连接登录，每个脚本一个事务，全部跑完或中途失败都立刻收回。每个脚本的事务提交前，核心检查这个身份在核心模式里拥有的全部对象（不只是本事务新建的）：有不带本插件前缀的关系或类型、或任何其他种类的对象，就回滚整个脚本。脚本必须幂等、对旧版本向后兼容（CONTRACTS §36）。迁移记录写在插件自己的表 `plg_<key>__schema_migrations`（`migration_id`、`checksum`、`applied_at`），由核心以插件身份建表、与每个脚本同一事务写入；脚本内容变了会重跑并更新记录。插件自己不要建、不要改这张表。
- **运行时没有建表权限**：插件进程连上来时身份没有 `CREATE`，只能对自己的表增删改查；建表、加列、加索引都写进迁移。
- **插件以外的表**：迁移或运行时要用核心的表，在清单 `database.access` 逐表申请，需要宿主权限 `db.core_read`（critical，只有超管能批）：

  ```json
  "database": {"migrations": "migrations/", "access": [
    {"table": "users", "columns": ["id", "email"], "privileges": ["select"]}
  ]},
  "hostPermissions": [{"id": "db.schema"}, {"id": "db.core_read"}]
  ```

  `privileges` 取 `select`、`insert`、`update`、`delete`、`references`；`columns` 省略时是整张表。管理员在安装确认页看到每一条（表、列、权限），同意即照申请授予（列级或表级 GRANT）；每次安装、升级、启动先收回再按当前清单重授。别的插件的表（`plg_` 开头）不能申请。直接写核心的表会绕过核心的缓存、权限版本和审计：有对应 Host API 的用 Host API，确实没有时才申请写权限。
- **卸载**：卸载并清除数据时删掉身份拥有的全部对象（含迁移记录表）和身份本身；保留数据时表留在库里，重装时交还给新身份。
- **单元测试**：`pluginsdktest.NewSchema` 建一个临时模式当作核心模式，`ApplyMigrations` 在里面跑真实的迁移（以测试库的用户），`FakeHost.SetDSN(dsn, schema)` 把它交给 `Host.DB`；测试库的地址不是虚拟地址，SDK 直接连。带前缀的表名在临时模式里照常使用。
- **`sub2api-plugin db check`**（`pack` 默认先跑它）按核心的规则逐个脚本检查：每个脚本跑完后，留下的关系或类型名字不带 `manifest.TablePrefix(key)`（含 PostgreSQL 自动起的名字）、有其他种类的对象、脚本里有超过 63 字节的名字，都列出来并注明脚本文件名，检查失败。迁移以一个只能在临时模式里建对象的临时角色执行（同宿主的插件身份），需要更多权限的语句（`ALTER … OWNER TO`、`CREATE SCHEMA`、`CREATE EXTENSION` 等）在检查时就失败；它在模式外拥有的对象（只在 PUBLIC 能建的地方）也列出来，`EXCEPTION` 块里建的也算。GORM 模型的表名或索引名不带前缀，直接报错。
- **db check 的限制**：它在一个空的临时模式里跑迁移，里面没有核心的表，所以迁移里用到 `database.access` 申请的核心表（例如建一个读 `users` 的视图）时检查跑不通，会报“relation does not exist”，并附上这条限制的说明。工具没有为核心表做替身。现在没有官方插件这样用。真有这种插件时只能用 `pack --skip-db-check` 打包，迁移要在装好核心的库上自己验证。

## 6.2 文件：默认没有，要用就申请 `fs.data`

- Linux 上插件跑在 Landlock 沙箱里（CONTRACTS §43.8，默认开启）：除了自己的二进制、go-plugin 的 socket 目录、`/dev/null` 和时区文件，插件**读写不了任何文件**，包括 `TMPDIR`（`os.CreateTemp("", …)`、`os.MkdirTemp("", …)` 得到 EACCES）、`/etc` 下的配置、其他插件的目录和 `/proc`。获授 `net` 的插件另外能读 CA 证书（经出口隧道做 TLS 用）。
- 要读写文件，在 manifest 申请宿主权限 `fs.data`（风险中，管理员在安装审查时同意）：`{"id": "fs.data", "reason": {"zh": "缓存下载的模型列表", "en": "Caches downloaded model lists"}}`。获授后插件从环境变量 `SUB2API_PLUGIN_DATA_DIR` 拿到数据目录（也是进程的工作目录），在里面读写文件和子目录、改名、截断；不能建 socket、fifo、符号链接。临时文件也放这里：`os.CreateTemp(os.Getenv("SUB2API_PLUGIN_DATA_DIR"), "…")`。没获授时这个变量不存在，代码要能在没有它时工作（或把它当成配置错误报告）。
- 数据目录只在本节点，不跨节点同步、不备份；要在节点之间共享或长期保存的数据放进自己的表（6.1 节）或 `Host.KV`。
- 插件必须静态链接（`sub2api-plugin build` 用 `CGO_ENABLED=0`）：沙箱不放行动态链接器和系统库。`time.LoadLocation` 能用（时区库放行），也可以内嵌 `time/tzdata`。
- 只在开发模式（`SUB2API_PLUGIN_DEV_MODE=true`，`sub2api dev`）或 `SUB2API_PLUGIN_LANDLOCK=false` 时没有这些限制；在本机测通的读写文件，打包前确认清单申请了 `fs.data`。

## 7. 现有平台插件

全部是 gRPC 代码插件，清单 `hostCompat` 下限 `>=0.1.304`。

| 插件 | 怎么构造请求 | 插件配合的部分 |
|---|---|---|
| openai、gemini、relay、anthropic | `standard` 库加预设；openai 另有 WebSocket（`openai.responses_ws`） | 全部在库和预设里；鉴权头之外都是默认头 |
| azure_openai（目录 `plugins/azure-openai`，市场） | `standard` 库，两个类型（v1 与按部署） | 同上；错误分类用 openai 预设的策略 |
| vertex（市场） | 自己的 `BuildUpstream`；把核心交来的 `user-agent`、`x-goog-api-client` 作为默认头转发；`externalServices` 是 `googleapis.com`（全局与各区域的 Vertex AI 主机都在它下面；不取回） | `ClassifyError`；凭证续期 `BuildRefreshRequest` / `ParseRefreshResponse`（CONTRACTS §48），第一枚令牌靠保存后续期（§48.3 第 5 条）；服务账号断言用 `golang-jwt/jwt/v5` 签名，密钥文件由插件自己解析（§109.6） |
| bedrock（市场；只服务 anthropic 平台的 messages，OpenAI 协议的请求由核心转换后走同一条路；带 CONTRACTS §110 的核心上这是跨平台转换，要在账号上为模型开启） | 自己的 `BuildUpstream`：把 Messages 请求装进 InvokeModel 信封，清洗 Bedrock 不接受的字段（顶层的 `model`、`stream`、`metadata`、`service_tier`、`inference_geo`、`speed`、`provider` 总是删除，以及 `tools[].custom`、`cache_control.scope`；`cache_control.ttl` 只在认出模型版本且它不支持缓存 TTL 时删除，认不出的保留），客户端点名的 beta 和工具需要的 beta（computer use、tool search、tool examples）写进请求体的 `anthropic_beta`，AK/SK 与临时凭证类型自己做 SigV4 签名，鉴权头里没有会话令牌（Access Key 与 API Key 类型）时 `RemoveHeader("x-amz-security-token")`；是否流式只看 `meta.stream`（不看请求体的 `stream`，运营方参数覆盖加的 `stream:true` 不会把非流式请求变成流式），流式请求给 `response_stream`；`externalServices` 是 `amazonaws.com`（各区域的运行时主机） | `ClassifyError`；账号测试请求同样自己签；一致性套件的两处例外见第 4 节 |
| codex | 自己的 `BuildUpstream`，HTTP 与 WebSocket | `ClassifyError`、额度头、`streamOnly`；`session_id` 由插件按 `prompt_cache_key` 派生（[PLUGIN-CODEX.md](PLUGIN-CODEX.md) §16）；全部头保持权威；两个类型 `passHeaders: []`，插件自己写出它发的每个头 |

## 8. 新插件自查

- 实现 `UpstreamBuilder`，不写 `Execute`（SDK 跑 `ExecuteDefault`）；API Key 加基础地址的类型直接用 `standard` 库。
- 权威头只放鉴权头和签名头，其余放默认头；改请求体只用 `jsonedit`。
- 用量规则：流式规则把输出数映射在指名的终止事件上；非流式规则映射响应体。规则表达不了时声明 `usageSource: "plugin"` 并实现 `UsageExtractor`。
- 账号类型声明的平台决定什么算“本平台协议”：只声明一个平台的类型，其他平台的客户端协议默认不调度到它的账号，管理员要在账号上按模型开启转换（CONTRACTS §110）。上游本身兼容多种协议的类型可以把这些平台都声明出来（sub2api 声明了 openai、anthropic、gemini）。
- 在一个平台上写了 `endpoints` 子集时，子集以外的端点**不服务**，除非在 `adapted` 里列出它：核心把它转换成子集内某个端点的协议再交给插件（同平台适配，CONTRACTS §83.3、§121.1）。例：只有 Responses 的上游写 `"endpoints": ["responses"], "adapted": {"chat_completions": {}}` 来服务 chat；核心没有对应转换器时清单能装上，但注册表不绑定这个类型。生图端点经 Responses 的 `image_generation` 工具服务时还要写驱动工具的对话模型：`"images_generations": {"model": "<对话模型>"}`（codex 的写法）；插件在 `meta.client_protocol` 是生图协议时不报上游模型（请求体顶层的 `model` 是这个对话模型，报了会覆盖核心从应答读到的回显生图模型）。没有隐式适配：旧插件依赖过它的要补声明。
- 上游同时说几家协议时用 `standard.Merge`，每个协议保留它官方的鉴权头和失败分类；有余额接口的写 `Type.Balance` 并在清单写 `balance`。
- 主请求和辅助请求（`Fetch`）的主机：写死的写进清单 `externalServices`，运营方填的地址字段写进 `upstreamFields`（账号自己的上游地址不用另写；每个账号类型都要写这个键，没有就写 `[]`）；不设 `Host` 头。
- 测试里调一致性套件；用 `standard` 写的插件另跑黄金样例。
- 不读写文件；确实需要时申请 `fs.data`，只用 `SUB2API_PLUGIN_DATA_DIR` 下的路径（6.2 节）。
- 第一方插件的清单写 `"publisher": "sub2api"`：这个发布者是保留的，只有官方级别的包能用（CONTRACTS §137.3），不用再登记插件键。插件声明的平台 ID 与通知渠道由 `server/internal/plugin/install/reserved.go` 按插件 id 保留（`TestReservedCoverRepo` 会检查）。
- 改了第 5 节表涉及的行为，同步本文和 CONTRACTS 对应章节，并在 HTTP 与 WebSocket 上补测试（参考 `gateway/disconnect_test.go`、`execute_test.go`）。

## 9. SDK 里的账号表单辅助

`api_key` + `base_url` 的表单由 `apikey.Spec` 整个实现（`Validate` 就是 `ValidateCredentials`，`FromAccount` 在请求时读出密钥和根地址），规则见 CONTRACTS §127：

- `api_key` 只要求是非空字符串，保存去首尾空白后的值；没有格式、长度检查（`KeyValidator` 已删除）。
- `base_url` 经 `NormalizeBaseURL` 规范化后按填写的保存（保留 `%2F` 等转义，不去后缀）；`Versions` 列出地址末尾可以带的版本段，`FromAccount` 在请求时去掉最长的一段（至多一段）得到根，路由路径接在根后面。类型声明了受限地址（`guardedSettings`）时，清单的 `versions` 写同样的值（CONTRACTS §21.3）。
- `Fields`（`fieldcheck.Fields`）列出 `api_key`、`base_url` 以外表单会发的键和各自的规则，**其它键一律按未知键拒绝**（`unknown`）。`Fields` 为 `nil` 时同样检查，只允许 `api_key`、`base_url`（2026-11-19 起；此前 `nil` 不检查）：直接用预设或 `Spec` 而不写 `Fields` 的插件，重新编译后保存账号时会拒绝其它键，已存账号带着其它键的下次保存被拒，要把它们列进 `Fields`。
- `NoBaseURL`：本类型没有地址字段（vertex 的 `vertex_apikey`），`base_url` 也按未知键拒绝。

自己实现 `ValidateCredentials` 的插件可以用包 `sdk/pluginsdk/accountform`，不用各写一份（vertex、bedrock 在用）。给字段是自己一套的账号类型用（云凭证、密钥文件）。

| 导出 | 用途 |
|---|---|
| `Object`（`map[string]any`），方法 `Str(k)`、`JSON()` | 解码后的 JSON 对象。数字保持原样（`json.Number`），重新编码不改动它们（令牌过期时间、大整数）。`Str` 返回去掉首尾空白的字符串值，没有或不是字符串时为空 |
| `Decode(raw) (Object, error)` | 解码一个 JSON 对象；空白和 `null` 得到空对象，对象后面还有内容报错 |
| `FieldError(field, code, msg)` | 一条字段错误（`*pluginv1.FieldError`） |
| `Invalid(e)` | 用一条字段错误拒绝表单的 `ValidateCredentials` 回答 |
| `DecodeForm(in) (*Form, *ValidateCredentialsResponse)`，`Form{Creds, Settings}` | 解出 `ValidateCredentials` 请求的凭证和设置。有一个不是 JSON 对象时第二个返回值是拒绝的回答（字段 `""`、`invalid_json`，与 `apikey.Spec` 相同），直接返回它 |
| `(*Form).Only(keys...)` | 凭证或设置里有 `keys` 以外的键时，返回拒绝的回答（`unknown`），否则 `nil`；拒绝表单不会发的键 |
| `(*Form).Normalized()` | 接受表单的回答。校验过程可以直接改写 `Creds`、`Settings`（填默认值、归一化）；回答里只带被改过的那个对象的 JSON，没改的留空，核心保留提交的内容 |
| `DecodeAccount(acc) (creds, settings Object, err error)` | 解出已存账号的凭证和设置（构造请求时用）；不是 JSON 对象时回 `INVALID_ARGUMENT` |

```go
form, refused := accountform.DecodeForm(in)
if refused != nil {
	return refused, nil
}
if bad := check(form.Creds, form.Settings); bad != nil { // 插件自己的校验，可以顺手填默认值
	return accountform.Invalid(bad), nil
}
return form.Normalized(), nil
```

## 10. 插件 id 与发布者（CONTRACTS §137）

- 插件由“发布者 + key”确定：`manifest.PluginID(publisher, key)`，第一方发布者 `sub2api` 的插件 id 就是 key，其他发布者的是 `_<publisher>__<key>`。核心的接口路径（`/plugins/:key`）、权限键（`plugin.<id>:…`）、任务名（`plugin.<id>.<job>`）、表前缀与数据库身份、插件进程的 `SUB2API_PLUGIN_KEY` 都用 id；界面显示 `erio/codex` 这样的形式（`manifest.DisplayID`）。插件自己的 `GetInfo` 仍报清单的 `key`。
- 发布者 id：小写字母、数字和单个下划线，以字母或数字开头和结尾（`^[a-z0-9](_?[a-z0-9])*$`），至多 25 字节；插件 id 不超过 30 字节，所以发布者与 key 合起来最多 27 字节（key 至少 2 字节，发布者因此至多 25）。`sub2api-plugin db check` 等工具的 `-key` 传插件 id。
- 同一个 key 可以属于不同发布者，它们是不同的插件，可以同时安装。发布者 `sub2api` 只给官方级别的包。
