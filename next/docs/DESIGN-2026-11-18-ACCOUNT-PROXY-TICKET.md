# 授权流程走账号表单选的代理：代理票据

2026-11-18，所有者要求：“账号授权时的第一次换令牌不走代理：修复bug”，“插件应该有能力在创建账号的时候获取配置的代理是吧？codex 插件就需要这个功能，是在创建账号的时候通过代理获取授权”，“如果是粘贴的代理串呢？”。契约写进 CONTRACTS §117。

## 1. 现状（改之前）

- 账号建好以后，发往上游的请求、令牌刷新、额度、重置次数都由核心发出，走账号的代理（`account/quota.go` 的 `sendPluginCall`、网关的中继与取回）。codex、vertex 的刷新都是 `BuildRefreshRequest` → 核心按账号的 `proxy_id` 发送，已经走代理。
- 但**建账号之前的授权**（codex 的设备码申请、设备码轮询、两种登录的换令牌）是插件自己在管理路由里用 `http.Client` 经出口隧道直连 `auth.openai.com`，清单为此申请了 `net`（域 `auth.openai.com`）。账号表单上选的代理根本没有传给插件，插件也没有办法用它。
- 结果：运营方给账号配了代理（本机 IP 到 OpenAI 不通，或会被风控），授权这一步却从服务器本机 IP 直连 OpenAI。

## 2. 思路

“核心替插件做对外的事、凭据不进插件”（CONTRACTS §115 的先例）：插件不拿代理地址和密码，把要发的请求交给核心，核心按**运营方在账号表单上选的代理**发出。

要解决三件事：

1. 账号表单当前的代理选择（已有代理的 id、粘贴的代理串、不用代理）怎么交给核心，且由核心按当前管理员的权限校验；
2. 一次授权有好几个请求（设备码申请、多次轮询、换令牌），跨了好几次路由调用，每次调用都要带上表单此刻的代理选择，插件改不了、别的管理员借不走；
3. 插件怎么让核心代发，发到哪些主机。

## 3. 数据流

```
账号表单（AccountEditor）                       核心                                   插件（codex）
  代理：选已有 #12 / 粘贴 socks5://u:p@h:1 / 不用
  点“开始授权”
  ── POST /account-types/codex/codex_device/proxy-ticket {proxy_id | proxy_url | 都不给} ──▶
                                  校验（与“拉取模型列表”同一段代码）：
                                  proxy_id 调用者看得见、proxy_url 能解析且调用者有 proxy:manage
                                  票据 = AES-GCM(主密钥, {用户, 选择}, AAD="proxy-ticket:codex")
  ◀──────────────────────────────── {ticket} ──
  ── POST /p/codex/auth/device/start {proxy_ticket} ──▶  路由调用带 execution_token ──▶  插件用这次调用的票据（不存进会话）
                                                                                 ◀── ExecuteHTTP{url, proxy_ticket}
                                  解开票据：插件对得上（AAD）、用户 = 这次路由调用的用户
                                  按调用者此刻的权限重新校验选择，取代理客户端
                                  经代理发出（直连时过 SSRF），回答整份交还 ──▶
  ── POST /p/codex/auth/device/poll {session_id, proxy_ticket} ──▶ ……每次轮询、换令牌之前界面都现取一张新票据，插件同样交给核心……
```

2026-11-19 起票据不再存进授权会话（最初是开始时取一张、存进会话、后续请求都用它）：所有者说“代理修改了，后面的请求都用新的代理走就行了呀，重新授权干嘛呢？”，所以界面每次调用都现取一张，契约见 CONTRACTS §117.2。本文已就地改成现在的做法。

一段话的说法：管理员在账号表单上选了代理（或粘贴了一串代理地址）以后点“开始授权”，控制台先把这个选择交给核心，核心检查他有没有权限用这个代理，然后把选择加密成一张只有核心能解开的“票据”交回来；授权界面每次调用插件（开始、每次轮询、换令牌）之前都这样现取一张交给插件。插件每次要访问 OpenAI，都把请求和这次调用的票据一起交给核心，由核心解开票据、确认还是这位管理员在操作、他仍然有权使用这个代理，再从这个代理把请求发出去。插件始终看不到代理地址和密码，也换不了别的代理；没选代理时核心直接发出，和以前一样能用。

## 4. 决定

### 4.1 代理选择的入口：核心接口发票据

- `POST /api/v1/account-types/:plugin_key/:type/proxy-ticket`，请求体就是“拉取模型列表”（`POST /account-types/:p/:t/models/fetch`）里的代理部分：`proxy_id`、`proxy_url`、都不给（直连）。权限：`account:create` 或 `account:update` 的任意范围（新建和重新授权都要用）。
- 校验**复用同一段代码**：从 `fetchTypeModels` 抽出 `proxyChoice`（`proxy_id` / `proxy_url`、两者冲突、`proxy_url` 去空白后为空视同未给）和它的 `check`（`proxy_id` 走 `checkProxyRef` 的可见性规则，`proxy_url` 走 `parseProxyURL`：要 `proxy:manage` 的任意范围，解析错误不回显原串）、`client`（`HTTPClient(proxy_id)` / `HTTPClientFor(spec)`）。拉取模型列表、发票据、用票据三处调同一组函数。
- 粘贴的代理串只解析使用，不查找也不建代理记录（与 §21.2 的模型列表一致）。

### 4.2 绑定放在哪：核心加密的票据，不存在核心

- 票据 = `base64url(secret.Cipher.Encrypt(json{u: 用户, id?: 代理 id, url?: 代理串}, "proxy-ticket:<插件键>"))`，与账号凭据、代理密码同一套 AES-256-GCM 主密钥加密（`runtime-contract/secret`）。插件读不出代理串里的密码，也改不了内容（改了解不开）；AAD 带插件键，别的插件拿去也解不开。
- 核心不存票据，所以没有新的存储、没有过期清理，也没有新的时长：票据只在一次路由调用里用（界面每次现取），插件不存它。
- 票据本身不授予任何东西：每次使用时核心按**这次路由调用的用户此刻的权限**重新校验（权限被收回、代理被删以后票据随即失效；用户被禁用后没有会话，也就没有能用它的路由调用），所以不需要过期时间。
- 别的管理员借不走：使用时票据里的用户必须等于这次路由调用的用户（核心从调用的上下文读，不信插件）。插件自己的会话还按发起人绑定（`SaveCallerSession`），两层都挡。

### 4.3 核心代发：扩展取回（`ExecuteHTTP`），不新加 RPC

对比过两种做法：

| | 给 `SendAccountRequest` 加“按票据”形式 | 给 `ExecuteHTTP`（取回）加“路由调用”形式（采用） |
|---|---|---|
| 授权 | 账号凭据授权 + 账号 id；票据形式没有账号，用户绑定只能靠“这个用户此刻有请求在途”（`callerSet`），插件在别的管理员的调用里也能用 | 执行令牌：只在这次路由调用期间有效，调用的用户就是令牌的用户，严格到每次调用 |
| 限制 | 30 秒、回答截到 64 KiB、每次写审计（这些是为账号请求设的，换到授权上等于新加了超时和上限） | 不限次数、不限大小，没有写死的超时，受路由调用本身的期限约束 |
| proto | 1 个字段 | 2 个字段 |

取回本来就是“核心替插件发一个请求、回答整份交还”，规则（主机、SSRF、请求头、不跟随重定向、不重试）都在 `execfetch`。所以：

- `HTTPRequest.execution_token`（新字段 10）：核心把每个**有登录用户**的路由调用登记成一个调用范围（与 `Execute`、`Monitor` 同一套 `callScopes`），令牌只在这次调用期间有效。SDK 把它绑在路由处理函数的 context 上，`pluginsdk.Fetch` 在路由里就能用。
- `ExecutionHTTPRequest.proxy_ticket`（新字段 7）：路由调用里的取回**必须**带票据（运营方没有选择就没有代理决定，核心不替他决定直连）；`Execute`、`Monitor` 里的取回**不能**带（它们走账号自己的代理）。
- 主机规则不变，复用 `requireFetchHost`（`manifest.UpstreamHostAllowed`，原名 `FetchHostAllowed`）：路由调用没有账号，只能到清单 `externalServices` 之一或其子域。codex 的 `auth.openai.com` 本来就在。另一个任务给核心代发的请求加“清单声明或账号配置里的主机”的统一规则时，这条路径已经走在取回的主机判断上，不用另接。
- 发送：`execfetch.Do`，`CheckURL` 按“经代理与否”过 SSRF，`Client` 是票据的代理客户端；`HeaderTimeout`、`TotalTimeout` 都是 0（不新加超时），由路由调用的 context 约束。粘贴的代理串用完 `CloseIdleConnections`。
- 不写审计行：路由调用本身已有 `plugin.route.write` 审计，取回也从来不审计；`execfetch` 失败时只记主机。

### 4.4 控制台与 `@sub2api/host`

- 原生账号表单（§23.5）多一个 prop `proxyTicket: () => Promise<string>`（类型 `ProxyTicketSource`，从 `@sub2api/host` 导出）：调用时按表单**当前**的代理选择向核心要一张票据。插件界面不看代理地址，只拿不透明的票据。对所有原生表单都一样传，不分插件。
- `AccountEditor.vue`：把拉取模型列表里拼代理参数的那段抽成 `proxyChoice`，模型列表和票据共用；票据失败时把字段错误显示在代理输入下，再抛给插件界面。
- `@sub2api/ui`、`@sub2api/host`、`@sub2api/vite-preset` 升到 3.20.0（只加不改），codex 声明 `hostUICompat ^3.20`。

### 4.5 codex

- 两个开始路由（`/auth/device/start`、`/auth/codex/start`）和轮询、换令牌路由的请求体都必须带 `proxy_ticket`（缺了 400 `invalid_argument`，field `proxy_ticket`），插件用这次调用的票据，不存进授权会话。四类请求（申请设备码、轮询、两种换令牌）都改成 `pluginsdk.Fetch` 带这次调用的票据。核心拒绝发送（`proxy_refused`）时请求没有到 OpenAI，会话保留，代理改好后下一次调用就能继续。方法、路径、请求头（`Content-Type`、`Accept`、`User-Agent`、`Originator`）和请求体逐字节不变，测试钉住。
- 删除插件自己的 `http.Client`、20 秒 `authTimeout`、1 MiB 读上限；轮询锁的 ttl 原来写成 `2*authTimeout+10s`，改写成同值的 50 秒（不是新上限）。
- 清单去掉 `net` 授权：插件不再自己联网，沙箱不让它开任何对外连接；`hostCompat` 仍是 `>=0.1.304`（本批核心未发布），版本 0.2.0 未发布，不再升。

## 5. 不做的

- 不给票据加有效期：每次使用都按当前权限重新校验，会话本身有寿命；加时长违背“不要擅自加限制”。
- 不改 `SendAccountRequest`：它是账号建好以后的账号请求，语义和限制都不一样。
- e2e：交付时 mock 上游没有 `auth.openai.com` 的路由，没有用例；之后补了 AC38（`next/e2e/ac38_codex_auth_proxy_test.go`，mock 扮演授权服务器并支持 HTTPS 的 `CONNECT` 代理）。
