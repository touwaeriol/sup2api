# 设计：插件按归属收到账号的全部信息（含代理），删除 `accounts.credentials` / `accounts.read` 与 `GetAccountCredentials`

日期 2026-11-19。对照 `develop` `4d1dcfeb8`（核心 0.1.304 批次，尚未发布）。状态：已实现（分支 `feat/plugin-account-context`，契约见 CONTRACTS §124）；实现与本文不同的地方见第 9 节。

取代 `.claude/worktrees/NOTES-design-plugin-ui-account-context.md` 的第二部分（第 9–14 节）。原稿是对着转发 V2（CONTRACTS §112）、删除 Claude OAuth（§116）、代理票据（§117）和 §123 之前的代码写的，它的 E9、`pluginsdk.ProxyTransport`、`GetProxy`、"废弃但保留"等内容全部作废，本文不再复述。

## 1. 结论

- **规则只有一条**：核心把哪个账号的请求交给插件执行，就把这个账号的全部信息给这个插件。全部信息包括属性、设置、解密的凭证，以及代理的地址、用户名和密码。收到的插件就是声明该账号类型的插件。插件在请求之外主动取账号，用新增的 `HostService.GetAccount(account_id)`，只能取到自己账号类型的账号，每次都写审计。
- **前一半已经是现状**：
  - 所有带 `Account` 的平台调用都只发给账号类型的声明者，而且带解密的凭证（第 3.1 节）。
  - 这次真正的改动只有四项：
    - 删除两个宿主授权；
    - 删除 `GetAccountCredentials`，换成 `GetAccount`；
    - 在 `Account` 上加代理详情；
    - 顺手统一用量观察里的账号。
- **插件严禁联网**：代理信息随 `Account` 下发只是数据，**不得用于自行联网**。所有出站请求仍然只经核心的客户端发出：
  - `RelayUpstream`；
  - `Execute`/`Monitor` 里的 `Fetch`，以及路由调用里带代理票据的 `Fetch`；
  - `SendAccountRequest`；
  - 核心代发的测试、模型、额度、续期、余额请求。
  
  账号代理由核心强制，任何平台插件都不加 `net`。
- **同一个版本删干净**：`accounts.credentials`、`accounts.read`、`GetAccountCredentials` 随 0.1.304 一起删除。清单里还写着这两个授权的包按旧契约拒绝（§112.10 的同一条信息），迁移 0116 删除它们的授予行。
- **版本号**：
  - 插件线协议仍是 2，Host API 仍是 10：两者都是 0.1.304 批次里定的，还没有发布（第 4.2 节）。
  - 第一方插件都已在这一批里升过版本号（相对 `plugins/released.json`），继续累计，不再升，`hostCompat` 也不动（第 4.9 节）。

## 2. 所有者的决定（原话）

- "允许插件获得账号的全部信息，包括直接获取代理、直接获取授权凭证，用插件授信的方式来保证"。
- "声明了账号类型以及对应协议的插件。核心在转发相关请求给插件、由插件来完成的时候，把对应的账号信息转发给它"。
- "调度的那个账号 让插件执行，就给哪个账号 ok？"
- "还是要通过核心的 http /socks 客户端去连接，谢谢，插件严禁联网！！！！"
- "旧的两个权限和旧接口：移除"。
- 信任级别："这些先不要限制了"。
- 相关的既有规则：
  - "代理配置了就走，没配置就不走，配置了就不要泄露 IP"（§123）；
  - "插件本身是不允许任何直连的，它的所有网络都需要通过核心发出"（§113.3）。

## 3. 现状（证据）

### 3.1 账号信息现在怎么给

- **`Account` 消息**（`sdk/proto/sub2api/plugin/v1/common.proto:31-64`）：
  - 字段：id、name、platform、type、`credentials_json`、`settings_json`、`properties`。
  - `properties` 里有 `proxy_id`（:53），这是插件现在拿到的唯一代理信息。没有任何插件读它（`GetProxyId` 在插件和 SDK 里没有调用）。
- **带 `Account` 的平台调用共 11 个**（`platform.proto`）：
  - `Execute`（:159）、`Monitor`（`PollRequest` :217）；
  - `ClassifyError`（:239）；
  - `BuildTestRequest`（:299）、`BuildModelsRequest`（:329）；
  - `BuildQuotaRequest`/`ParseQuotaResponse`（:581、:597）；
  - `BuildRefreshRequest`/`ParseRefreshResponse`（:692、:710）；
  - `BuildBalanceRequest`/`ParseBalanceResponse`（:750、:781）。
  
  另外，`ExtractUsageRequest`（:384-389）是 `RelayUpstreamResponse.observation`，交回给正在执行 `Execute` 的同一个插件。
- **这些调用都只发给账号类型的声明者**：
  - `gateway/execute.go:127`、`gateway/dispatch.go:915`；
  - `account/testreq.go:143,492`、`account/models.go:162`、`account/quota.go:282,292`、`account/refresh.go:304,321`、`account/balance.go:157,180`；
  - `usage/monitor.go:74`。任务的插件和账号的插件不一致时，核心当作账号已不存在（`usage/task_reconcile.go:83-84`）。
- **凭证无条件放进 `Account`**（`core/ports_identity.go:989-995`）。授权检查前移到了两处：
  - 清单检查要求声明账号类型就必须申请 `accounts.credentials`（`sdk/manifest/check/account_check.go:69`）；
  - 没有这个授权，账号类型不注册（`server/internal/plugin/registry/registry.go:373-381`）。
  
  所以"声明了账号类型"和"持有 `accounts.credentials`"今天是一回事。
- **构造 `Account` 的地方**：
  - 主函数 `core.(*Account).PluginAccount`（`core/ports_identity.go:989`），调用方：
    - `gateway/dispatch.go:559`、`gateway/websocket.go:668`；
    - `usage/task_reconcile.go:101`；
    - `account/handlers.go:211-214`（`row.pluginAccount`，给测试、模型、额度、续期、余额用）。
  - 特例一：`account/models.go:80-81`，建号前拉模型，只有 type、凭证、settings。
  - 特例二：`gateway/usageplugin.go:99-107` 的 `small`，只有 id、name、platform、type；settings/properties 只在平台和账号类型同属一个插件时才给。
  - 不一致：`gateway/waited.go:203`（没有用量捕获的分支）直接放进带凭证的 `x.acct`，和 `platform.proto:386-388` 的注释"without the credentials"矛盾。
- **HostService 里的账号接口**：
  - `ListAccounts`（`host.proto:238-242`；`grpcruntime/host.go:555-588`）：需要 `accounts.read`，返回 `AccountSummary`{id, name, type, status, enabled, settings_json}（`host.proto:512-527`）。
  - `GetAccountCredentials`（`host.proto:244-246`；`host.go:537-551,593-613`）：需要 `accounts.credentials` 且 scope 为 own。返回 `AccountSummary` 加凭证。审计 `plugin.account.credentials.read` 先写，写失败则调用失败（`account/plugins.go:45-47,100-145`）。
  - `SendAccountRequest`（`host.proto:278-290`；`host.go:615-660` 一带，函数从 :626 起）：检查与 `GetAccountCredentials` 相同。用这一行的 `proxy_id` 经代理发出，发出后写审计 `plugin.account.request`（`account/plugins.go:161-199`）。
  - `AccountScopeCheck`（`host.proto:128-135`；`host.go:403-425`）：需要 `accounts.read`，`user_id` 必须是正在处理的请求的调用者。
- **使用者**：
  - `GetAccountCredentials`：volcengine `plugins/volcengine/internal/volcengine/routes.go:153`、codex `plugins/codex/internal/codex/resetcredits.go:222`。
  - `ListAccounts`：只有 volcengine（routes.go:192,293）。
  - `AccountScopeCheck`、`pluginsdk.RequireAccounts`、`ResolveAccountScope`：**没有任何第一方插件调用**（在 `plugins/`、`server/`、`tools/` 里搜过）。
- **12 个声明账号类型的插件**：anthropic、azure-openai、bedrock、codex、gemini、newapi、openai、relay、sub2api、typesafe、vertex、volcengine 共 12 个，全部持有 `accounts.credentials {"types":"own"}`；只有 volcengine 另持有 `accounts.read`。持有 `net` 的只有 guard、mailer、moderation（逐个读过各 `manifest.json`）。

### 3.2 代理

- **`proxies` 表**（`store/model/proxies.gen.go`）：protocol、host、port、username、`password_enc`（加密）、status、allow_private、real_ip 等。
- **密码解密**：在 `proxy/proxy.go:222-231`。`buildClient`（:233-248）用 `proxyURL`（:209-219）把明文密码放进 `url.UserPassword`，交给传输层。所以核心内存里本来就常驻代理明文，在每个缓存的客户端里。
- **缓存**：`proxy.Service` 按代理 id 缓存 `entry{client, status, updatedAt, checkedAt}`（:73-78），按 `updated_at` 复查（:290-345）。代理停用时 `entry.result` 报错，绝不退回直连（:347-353）。
- **对外的端口**：`core.ProxyDirectory`（`core/ports_identity.go:1090-1098`）只有 `HTTPClient` 和 `HTTPClientFor`。`HTTPClient` 唯一的外部调用方是 `account.proxyEgress`（`account/directory.go:315-318`）；另外 `proxy/location.go:35` 在包内调用。`core.ProxySpec`（:1103-1109）已经有 Protocol、Host、Port、Username、Password。
- **§123 的取法**：账号以外只有一个入口 `AccountDirectory.Egress(ctx, id)`（`account/directory.go:304-310`），返回 `core.Egress{Client, Proxied}`（`core/ports_identity.go:1083-1086`）。
  - 网关每次尝试取一次（`gateway/execute.go:77-80`），WebSocket 每轮取一次（`websocket.go:668-673`）。
  - `Monitor` 的每次取回各取一次（`usage/poll_http.go:21`）。
  - 账号包里握着实时行的路径直接用这一行的 `proxy_id`：
    - `account/testreq.go:153,318`、`account/models.go:141`；
    - `account/quota.go:290,429`、`account/balance.go:177`、`account/refresh.go:320`；
    - `account/plugins.go:167-191`。
- **账号快照**（`account/directory.go:275-296`）里存着解密的凭证，但代理只有 `ProxyID`。代理本身的改动不会让账号快照失效（§123）。
- **建号前**：拉模型（`models.go:55-82`）和授权（§117 票据，`account/proxyticket.go:80-84`）用的是表单当前的代理选择。§117 明确写着"插件看不到代理地址和密码"。

### 3.3 授信手段与边界

- **风险表** `HostPermissionRisk`：`sdk/manifest/manifest.go:1207`（`accounts.read` 为 Medium）、`:1222`（`accounts.credentials` 为 Critical）。控制台另有一份：`web/src/views/plugins/pluginUtil.ts:24,41`。文案在 `web/src/i18n/locales/{en,zh}/plugins.ts:96,113,129`。
- **安装审核**里，声明账号类型的包已经固定显示一句（`server/internal/plugin/install/review.go:73-88`，控制台 `web/src/views/plugins/ConsentView.vue:270`；§112.11）："会收到经它的账号发出的每个请求的完整内容……不是权限，不能取消"。
- **信任级别**：official、verified、community、unsigned（`server/internal/plugin/pkg/trust.go:20-25`）。按信任级别加限制的只有一处：非官方包申请 `db.schema` 时，管理员要另有 `plugin:grant:db_schema_unconfined`（`review.go:70-72,691-695`）。
- **`db.schema` 的边界**：持有它的插件用核心的数据库用户，能读写全部表（§115"隔离"）。`accounts`、`proxies` 的非加密列（含代理 host、port、username）它都读得到，加密列读不出明文。
- **`GetEnvironment`**（`host.go:705-717`）：不检查授权，`host.proto:25-28` 写的是"always granted"，属于有意为之。它返回任意分组和账号的数值环境变量；不存在的账号返回 `{}`（`environment/environment.go:205-224`）。
- **授予的清理**：插件升级时，`pruneGrants`（`install/defaults.go:60-71`）删除新清单不再申请的授予，但撤销状态（`revoked`）的行除外。风险表里没有的授权在审核里按 Critical 显示（`review.go:279-282`）。
- **清单检查删除授权的先例**：`removedAffinityPermission`（`sdk/manifest/check/platforms_check.go:359`、`validate.go:416-417`）只在上传和安装时报错。加载时的检查只有 `check.OutdatedPackage`（`grpcruntime/runtime.go:237-245`、`sdk/manifest/check/retired.go:37-42`）。

### 3.4 codex 的 `settingsFields: ["proxy_id"]`

- 位置：`plugins/codex/manifest.json:36,87`，`plugins/codex/manifest_test.go:92-93` 断言它。
- 账号的代理是 `accounts.proxy_id` 列（`account/handlers.go:815,939`），不在凭证 JSON 里。
- codex 的 Go 代码不读它。界面的 `mergeCredentials` 只是原样保留已有的键（`ui/native/src/flow.ts:52-53`，`flow.spec.ts:41-43` 的样例里放了 `proxy_id: 3`）。
- 首个提交 `9a45b0d4b` 起就是这样。`git log -S proxy_id` 在 codex 的界面和 Go 代码里只找到这个样例和一条错误提示的测试，没有任何写入方。

## 4. 设计

### 4.1 两个授权删除后各处按"归属"判断

"归属"指账号的 `plugin_key` 等于调用的插件。所有账号查询在 SQL 里按 `plugin_key` 过滤（`account/plugins.go` 的 `pluginAccountByID`、`pluginAccountsPage`），凭证用插件键作 AAD 加密（§26.6），所以归属检查不另写代码。

| 位置 | 现在 | 改成 |
|---|---|---|
| `registry/registry.go:373-381` `addAccountTypes` | 没有 `accounts.credentials` 就不注册账号类型 | 删掉授权判断和参数 `grants`；注释改为"由实现了 platform.adapter.v1 的声明者服务" |
| `check/account_check.go:69` | `needPerm(... "accounts.credentials" ...)` | 删掉这一行，保留 `needCap`、`needPerm("platform.register")` |
| `check/validate.go:416-428` | 两个授权都在风险表里；对 `accounts.credentials` 检查 scope 是否为 own | 遇到 `accounts.credentials` 或 `accounts.read` 时报 `CodeOutdatedContract`："host permission %q was removed: a plugin receives the accounts of the account types it declares"（第 4.1.1 节）。scope 判断只留 `accounts.auto_state` |
| `manifest/manifest.go:1207,1222` `HostPermissionRisk` | 两行 | 删掉 |
| `grpcruntime/host.go:537-551` `requireOwnCredentials` 与 :593-613 `GetAccountCredentials` | — | 删掉，换成 `GetAccount`（第 4.2 节） |
| `grpcruntime/host.go:626-629` `SendAccountRequest` | 先调 `requireOwnCredentials` | 删掉这次调用。归属已由 `SendPluginAccountRequest` 的 SQL 保证（`account/plugins.go:171`，别人的账号是 NOT_FOUND） |
| `grpcruntime/host.go:555-557` `ListAccounts` | 需要 `accounts.read` | 不检查授权。只返回自己账号类型的账号；不声明账号类型的插件得到空列表 |
| `grpcruntime/host.go:403-405` `AccountScopeCheck` | 需要 `accounts.read` | 不检查授权（所有者选了方案甲） |
| `grpcruntime/runtime.go:61` | 注释提到两个接口 | 跟着改 |
| 控制台 `pluginUtil.ts:24,41`、`i18n/locales/{en,zh}/plugins.ts:96,113,129` | 风险等级和文案 | 删掉这两项 |
| 测试夹具 `registry/registrytest/registrytest.go:140`、`plugin/pkg/pkgtest/pkgtest.go:144,171`、`tools/sub2api-plugin/cli_test.go`、`sdk/manifest/check/*_test.go`（quota、balance、usage、usagesource、common_controls）、`sdk/pluginsdk/providers/typesafe/typesafe_test.go`、e2e `ac03_install_test.go:65-66` | 写着这两个授权 | 去掉，改成断言没有这两个授予；ac03 改为断言 `platform.register` |

**`AccountScopeCheck`（所有者已确认方案甲）**

- 它回答的是"这次请求的调用者能对哪些账号做某个动作"，按核心的账号范围规则判断（§21.0）。
- 它不交出账号的任何字段，只在调用者有请求在途时可用，而且按访问密钥收窄。
- 性质和 `AuthzCheck` 问本插件自己的权限一样，后者不需要授权（`host.go:378-386`）。插件路由在清单里声明 `accounts:` 时，核心替它做同样的检查，这也不需要授权。
- 现在没有插件调用它。

推荐方案（甲）：

- 不检查授权，任何有登录路由的插件都能问。
- 插件能多知道的只是"调用者的范围里有哪些账号 id 是存在的"，而调用者本人在控制台里本来就看得到这些。

备选方案（乙）：

- 只给声明了账号类型的插件，即注册表里有它的账号类型。
- 不声明账号类型的插件得到 `PERMISSION_DENIED`，`RequireAccounts` 回 403。

两种方案都不改 SDK 接口，`sdk/pluginsdk/accounts_scope.go:40,74` 和 `fakehost.go:60` 里关于 `accounts.read` 的注释都要改。

#### 4.1.1 旧包怎么拒绝

- 用 §112.10 的"旧契约"机制，不用 `removedAffinityPermission` 的普通校验错误。原因是后者只在上传和安装时生效，而 `CodeOutdatedContract` 由 `check.OutdatedPackage` 在上传、安装、加载三处用同一条信息拒绝（`protocol.OutdatedContractMessage`：`... built for an older plugin protocol ...; install a version built for core 0.1.304 or later`）。
- 这条信息是准确的：带这两个授权、按协议 2 构建的包只可能是 0.1.304 发布之前的开发或测试构建。
- 升级须知（写进 CHANGELOG 0.1.304 一节）：
  - 0.1.304 本来就是停机、核心和全部插件一起换（§112.11）。
  - 0.1.303 时代的内置包是协议 1，在这批里已经按旧契约被拒绝。所以删除这两个授权不会让任何此前可用的内置包在升级窗口里（§37）多一次不可用。
  - 内置插件随核心导入的新版本清单不带这两个授权，安装或升级时 `pruneGrants` 会删掉旧授予。
  - 第三方的平台插件本来就要用新 SDK 重新构建（§112.11）。

#### 4.1.2 迁移 0116

文件 `server/internal/migrations/0116_drop_account_grants.sql`：

```sql
DELETE FROM plugin_permission_grants WHERE permission IN ('accounts.credentials', 'accounts.read');
DELETE FROM plugin_staged_grants    WHERE permission IN ('accounts.credentials', 'accounts.read');
```

- 为什么需要：`pruneGrants` 不删撤销状态的行，没升级的插件和暂存的版本也会留着这些授予；它们在审核里会显示成"未知权限、按 Critical"。
- 这两张表是普通表，删除可重复执行。
- 不需要重新生成模型：表结构没变。
- 这一批本来就是单向的（0106–0115），回退照旧连同数据库回到备份。

### 4.2 `HostService.GetAccount`

**proto（`host.proto`）**

- 删除 `rpc GetAccountCredentials` 和消息 `GetAccountCredentialsRequest`/`Response`（:244-246、:529-538）。
- 新增：

```proto
// The account of this plugin's own account types, as PlatformService calls
// receive it: properties, settings, decrypted credentials and the proxy
// (CONTRACTS §124). Another plugin's account, a deleted one or none at all
// is NOT_FOUND. Audited on every call. The proxy is data: the plugin never
// connects through it itself.
rpc GetAccount(GetAccountRequest) returns (GetAccountResponse);
message GetAccountRequest { int64 account_id = 1; }
message GetAccountResponse { Account account = 1; }
```

- 改注释：:222-237（"they do not widen……"一段）、`ListAccounts` 的 :238-241（去掉 `accounts.read`）、`SendAccountRequest` 的 :278（去掉 `accounts.credentials`）、`AccountSummary` 的 :512-515。
- 重新生成：`cd next/sdk && buf generate`。

**core 端口**（`core/ports_plugin_infra.go:236-260`）

- 删掉 `PluginAccountCredentials` 和 `PluginAccountReader.ReadPluginAccountCredentials`。
- 换成：

```go
// ReadPluginAccount returns one account of pluginKey's own account types as
// the plugin sees it on PlatformService calls (credentials, settings,
// properties, the saved proxy), after writing the audit row.
ReadPluginAccount(ctx context.Context, pluginKey string, id int64) (*pluginv1.Account, error)
```

**账号模块**（`account/plugins.go:100-145`）

- 改写成 `ReadPluginAccount`：用 `loadRow` 按 `plugin_key` 读一行，这样字段和网关路径同一来源。
- 用这一行的 `proxy_id` 调 `proxyEgress` 拿代理详情（第 4.3 节），然后 `row.pluginAccount(plain, eg)`。
- 代理停用或拿不到时返回错误：`Unavailable`，信息同 `entry.result`，不交出不带代理的账号。
- 审计沿用"先写、写失败则调用失败"的顺序：
  - action 改名为 `plugin.account.read`（常量 `AuditPluginAccountRead`，取代 :45-47）；
  - detail 为 `{plugin_key, account_type, account_name, via: "host.GetAccount"}`，不记任何值。
  - 历史行的 `plugin.account.credentials.read` 留在 `audit_logs` 里不动；控制台没有这个 action 的专门文案（`web/src` 里搜不到）。

**宿主**（`grpcruntime/host.go`）

- `GetAccount`：参数非正返回 `InvalidArgument`。
- 调 `h.i.rt.o.Accounts.ReadPluginAccount(ctx, h.key(), id)`，错误经 `toStatus` 映射：别人的账号返回 NOT_FOUND。
- 不检查任何授权。

**SDK**

- `sdk/pluginsdk/host.go:103-114,198-205,402-407`：`Host.AccountCredentials` 和类型 `AccountCredentials` 换成 `Host.Account(ctx, id) (*pluginv1.Account, error)`。
- `host.go:94-102` 的 `ListAccounts` 注释去掉 `accounts.read`；`hostcore.go:93-99` 的 `SendAccountRequest` 注释去掉授权。
- 假核心 `pluginsdktest.FakeHost` 加上 `Accounts map[int64]*pluginv1.Account`，实现 `GetAccount`（不在表里返回 NOT_FOUND），并记录调用（`AccountReads []int64`）供插件测试断言。

**迁移两个调用方**

- volcengine：
  - `routes.go:150-160` 的 `creds` 改用 `host.Account`；
  - `assets.go:149` 的 `AssetConfigOf(*pluginsdk.AccountCredentials)` 改成接收 `*pluginv1.Account`（读 `GetCredentialsJson`、`GetSettingsJson`）；
  - 文件头 `routes.go:1-15` 改为"审计的是 `GetAccount`"；
  - 测试 `asset_endpoints_test.go`、`assets_test.go`、`routes_db_test.go` 跟着改。
- codex：
  - `resetcredits.go:216-233` 的 `resetAccount` 改用 `host.Account`，读 `GetCredentialsJson`；
  - `resetcredits_test.go` 跟着改。

**版本**

- 插件线协议不升（仍是 2）。删掉一个 RPC 是不兼容的改动，但协议 2 属于 0.1.304 批次，还没有发布（CHANGELOG"未发布"一节的标题与 :13）：
  - 按协议 2 构建的包都在这一批里；
  - 带旧授权的会被 4.1.1 拒绝；
  - 不带旧授权、但调用了 `GetAccountCredentials` 的，只有 volcengine 和 codex，和本改动同批重建。
  - 如果有测试实例装了按当前协议 2 构建的这两个包，换上新核心后它们调用旧接口会得到 `UNIMPLEMENTED`，重新装这一批的包即可。这种测试实例是否存在没能确认（第 8 节）。
- Host API 不升（仍是 10）：同样是这一批里从 9 升上来的、未发布的值。

### 4.3 `Account.proxy`

**proto（`common.proto`）**

```proto
message Account {
  ...
  // The proxy this call's requests go out through, password included; unset
  // when the account has none (direct). Data only: every request of the
  // account is sent by the host, which enforces this proxy; a plugin never
  // connects through it itself (CONTRACTS §124).
  AccountProxy proxy = 8;
}
message AccountProxy {
  int64 id = 1;
  string protocol = 2;   // http | https | socks5
  string host = 3;
  int32 port = 4;
  string username = 5;
  string password = 6;
}
```

同时删掉 `AccountProperties.proxy_id`（:53），改成 `reserved 9; reserved "proxy_id";`。理由：

- 没有任何读者；
- 两个字段并存时，可能出现快照里的 id 和本次实际代理的 id 不一致（§123 写过的"选号与执行之间改了代理"）；
- 唯一的来源只能是本次出站用的代理。

**代理明文从哪来**

选择让 `ProxyDirectory` 一并返回，而不是在 `Egress` 外面再加一个方法。理由：

- 客户端和明文出自代理服务的同一个缓存条目，天然同源；
- 不多一次查询，也不多一次解密：`buildClient` 本来就解密了密码；
- 模块边界不变：账号模块只经 `core.ProxyDirectory` 用代理服务，网关、对账只经 `core.AccountDirectory.Egress`。

具体改动：

- `core/ports_identity.go:1083-1098`：
  - `Egress` 加 `Proxy *ProxyRef`（nil 表示直连），`ProxyRef{ID int64; ProxySpec}`，复用 :1103 的 `ProxySpec`。
  - `ProxyDirectory.HTTPClient(ctx, proxyID)` 改成返回 `(ProxyClient, error)`，`ProxyClient{Client *http.Client; Proxy *ProxyRef}`。唯一的外部调用方是 `account/directory.go:316`，包内调用方是 `proxy/location.go:35`。
  - `ProxySpec` 加 `String`、`GoString`、`LogValue`，遮蔽 Password（做法同 `sdk/reqsign/reqsign.go:23-50`），核心自己的日志不会漏出密码。
- `proxy/proxy.go`：
  - `entry` 加 `ref core.ProxyRef`；
  - `buildClient`（:233-248）在构造 URL 时一并填好 `ref`；
  - `entry.result` 返回 `ProxyClient`。
  - 代理停用时照旧报错（:347-353），不给出明文。
- `account/directory.go:315-318` 的 `proxyEgress`：返回 `core.Egress{Client, Proxied: proxyID != nil, Proxy}`。拿不到时 `Proxy` 为 nil，并且一定带错误。

**插件看到的代理与本次出站同源**

`row.pluginAccount`（`account/handlers.go:211-214`）和 `core.(*Account).PluginAccount`（`core/ports_identity.go:989`）都改成接收一个 `*core.ProxyRef`（nil 表示没有代理），由 `pluginProxy(ref)` 转成 `*pluginv1.AccountProxy`。转换只有这一处，放在 core。各条路径：

- **网关每次尝试**（`dispatch.go:559`、`execute.go:77-80`）：
  - 先取 `Egress`，再构造 `Account`：`newExecution` 改为先 `Egress`，再用 `acc.PluginAccount(platform, eg.Proxy)`。
  - 取不到出口（`egressErr`）时不调 `Execute`，直接按 `dispatch.go:741-743` 的同一个结果失败转移。这一次请求本来就发不出去；而且插件不能拿到一个"代理缺失"的账号。
  - 同一次尝试里 `ClassifyError`（`dispatch.go:915`）用的是同一个 `Account`。
- **WebSocket 每一轮**（`websocket.go:668-673`）：同上。每轮开始取一次，下一轮重新取。已建立的上游连接照 §123 不断开，所以同一会话中连接的出口可能比这一轮 `Account.proxy` 旧。这和 §123 已经写明的"连接和取回的出口可能不同"是同一种情况，文档照写。
- **Monitor**（`usage/task_reconcile.go:101`）：
  - `acquireTaskAccount` 取一次 `Egress` 放进 `Account`；取不到时按 `errTaskDeferred` 推迟（同冷却）。
  - 这次调用里的每次取回仍按 §123 各取一次（`usage/poll_http.go:21`）。两者之间改了代理的话，插件看到的是调用开始时的代理，请求按新代理发出，不会经账号已经不用的代理发出。
- **账号包里握着实时行的路径**：
  - 测试 `testreq.go:140,153,318`；
  - 编辑页拉模型 `models.go:141,146`；
  - 额度 `quota.go:281,290,429`；
  - 余额 `balance.go:156,177`；
  - 续期 `refresh.go:303,320`；
  - `GetAccount`。
  
  这些路径改为先用这一行的 `proxy_id` 调 `proxyEgress`，再构造 `Account`，再把同一个 `Egress` 交给发送函数：`sendPluginCall`（`quota.go:417-429`）、`runTest`（`testreq.go:301-318`）的参数从 `proxyID` 改成 `core.Egress`。取不到出口时：
  - 测试：结果失败，信息写明原因，什么都没发出（同现在）；
  - 拉模型：返回错误；
  - 额度、续期：不调 Build，也不调 `Parse*`（`Parse*` 同样带 `Account`，而出口拿不到时不调用插件）：额度记失败，原因写进快照；续期按 transient 失败（第 9 节）；
  - 余额：快照记 `transient: <原因>`（同 §52.3 被拒的处理）。
- **不进账号快照**：`account/directory.go:275-296` 的快照照旧只有 `ProxyID`。代理明文只在代理服务的缓存里。

**两个特例给不给**

- `account/models.go:80-81`（建号前，按表单的选择拉模型）：**给**，所有者决定“代理这时候已经填进去了就用 没填进去就不用”（第 7 节第 5 条）。表单的选择经 `egressClient` 取出口，`Egress.Proxy` 是这次出站用的代理（粘贴的代理串照样只解析、不入库、不进日志和审计，`id` 为 0），经 `core.PluginProxy` 放进 `Account.proxy`；没选为空、直连；选了的代理取不到时报错，不调插件。测试 `account/models_test.go` `TestTypeModelsFetchCarriesTheFormProxy`。

  建好账号之后的所有调用都带代理。
- `gateway/usageplugin.go:99-107` 的 `small`：删掉，见第 4.4 节。

**热路径**

- 每次尝试多带一个 `AccountProxy`，约 60–150 字节，对比请求体可以忽略。
- 缓存命中时不查数据库、不解密（明文和客户端同时建好）。
- 网关路径照旧不逐条审计，和凭证一样（§26.6：随请求下发的不审计，主动读取的审计）。

**日志遮蔽**

- 生成的 `pluginv1.AccountProxy` 不能加方法。本机 protobuf-go v1.36.12 的 `String()` 也不认 `debug_redact`：查过 `encoding/prototext` 和 `internal/impl`，没有实现。所以遮蔽分两层：
  - 核心侧：`core.ProxySpec`/`ProxyRef` 遮蔽；核心不打印 `pluginv1.Account`，这和今天不打印带凭证的 `Account` 是同一条约定，CONTRACTS 写明。
  - SDK 侧：新增 `pluginsdk.AccountProxyOf(acc *pluginv1.Account) *AccountProxy`，返回 SDK 自己的结构体，带 `String`、`GoString`、`Format`、`LogValue`，遮蔽 Password，做法同 `reqsign.AWSCredentials`。PLUGIN-DEVELOPMENT.md 写明：读代理用它，不要打印 `Account` 或 `AccountProxy` 消息本身。
- SDK 不提供任何把代理变成 `http.RoundTripper` 或拨号器的函数。

### 4.4 用量观察里的账号统一

- 观察结果只交回正在执行 `Execute` 的插件，也就是账号类型的声明者；它手里已经有完整的 `ExecuteRequest.account`。
- `small` 里"平台与账号类型同属一个插件才给 settings"的条件在这里没有意义：比如 codex 账号挂在 openai 平台上时，收到观察结果的仍是 codex。
- 改动：
  - 删掉 `usageplugin.go:99-107` 的 `small`，`pendingExtract.account` 直接用 `acct`，也就是本次尝试的 `Account`，含凭证和代理；
  - `waited.go:197-204` 两个分支已经一致，不用改；
  - `platform.proto:386-388` 的注释改成 "as in ExecuteRequest.account"；
  - `pluginsdk/pluginsdktest/execution.go:225`（假核心把凭证清空）跟着改成原样。

### 4.5 安装提示

`install/review.go:81-88` 的 `requestAccessStatement` 改为：

- en: "This plugin receives everything about the accounts of the account types it declares — their settings, decrypted credentials and proxy (address, username and password) — and the full content of every request sent through them and the data relevant to their responses, and it decides where each request is sent. The core sends every request; the plugin never connects to the network itself. This is how every platform plugin works; it is not a permission and cannot be turned off."
- zh: "此插件会收到它声明的账号类型下账号的全部信息（设置、解密的凭证、代理的地址、用户名和密码），以及经这些账号发出的每个请求的完整内容和与响应有关的数据，并决定请求发往哪个地址。请求都由核心发出，插件自己不联网。所有平台插件都是这样工作的：这不是一项权限，不能取消。"

其他改动：

- 控制台 `ConsentView.vue:270` 不用改，它只显示这段文字。`ConsentView.spec.ts:306` 用的是测试自带的文字，也不用改。
- 审核页已经显示 `trust` 和 `signature_status`（`review.go:28-29`），不新增字段。
- 不按信任级别加限制（所有者："这些先不要限制了"）。

### 4.6 如实写明的边界（只写文档，不改行为）

CONTRACTS §124 写明以下三点：

- **账号信息的边界**：
  - "只有声明者拿得到"只对凭证明文和代理密码成立；
  - 持有 `db.schema` 的插件能读 `accounts`、`proxies` 的全部非加密列，包括代理 host、port、username（§115）；
  - 安装提示不暗示"其他插件看不到账号信息"。
- **`GetEnvironment`**：不检查授权（§74.3"always granted"），返回任意分组和账号的数值环境变量，不存在的返回 `{}`。
- **代理信息不得用于联网**：插件没有 `net`，出口隧道拒绝（`server/internal/plugin/egress/egress.go:490-491`）。账号的请求只经核心的客户端发出，核心强制代理，配了代理绝不直连。

### 4.7 清理 codex 的 `settingsFields: ["proxy_id"]`

- 删掉 `manifest.json:36,87` 两处，删掉 `manifest_test.go:92-93` 对它的断言（只断言 `sensitiveFields`），把 `flow.spec.ts:41-43` 样例里的 `proxy_id: 3` 换成别的普通键。
- 依据：没有写入方（第 3.4 节）；即使某个账号的凭证里手工放过 `proxy_id`，删掉之后它只会在下一次保存时被当作凭证加密，不影响代理（代理是 `accounts.proxy_id` 列）。
- 线上是否有这样的账号没能确认（第 8 节）。PLUGIN-CODEX.md :56 的清单描述跟着改。

### 4.8 测试与 e2e

**核心**

- `grpcruntime/testdata/testplugin`：每个带 `Account` 的平台调用把收到的 `credentials_json`、`proxy` 回写给测试。对每个调用各有一条用例，断言：
  - 账号有代理时 `proxy` 与代理行一致（含密码）；
  - 没有代理时为空；
  - 凭证都在。
- `gateway/egress_test.go`（§123 的用例）加断言：连发三次、中间改代理或去掉代理时，每次 `Execute` 看到的 `Account.proxy` 依次是 7、8、空，和中继实际走的代理一致。出口取不到时不调用 `Execute`。
- `usage/poll_test.go`：`Monitor` 的 `Account.proxy` 是调用开始时的代理。
- `account` 包：
  - `ReadPluginAccount` 的归属：别人的账号、已删除的账号都是 NOT_FOUND；
  - 审计行的内容，审计写失败则调用失败；
  - 代理停用时返回 `Unavailable`；
  - 测试、额度、余额、续期在代理停用时不调 Build，各自的失败形式如 4.3 所述。
- `proxy` 包：`ProxySpec`/`ProxyRef` 用 `%v`、`%+v`、`%#v` 打印和 `slog` 输出时都不出现密码。
- `install`：审核文案；带旧授权的包上传、安装、加载都以 `outdated_contract` 被拒；迁移 0116 删掉授予行（`migrations_test.go`）。
- `registry`：没有任何授权也能注册账号类型。
- `ListAccounts`、`SendAccountRequest`、`AccountScopeCheck` 不再需要授权（`host_accounts_internal_test.go`）。

**SDK**

- `check`：两个授权报 `outdated_contract`；声明账号类型不再需要它们。
- `pluginsdk`：`AccountProxyOf` 的遮蔽；`FakeHost.GetAccount`。

**插件**

- volcengine、codex 的路由测试改用 `FakeHost.Accounts`。
- `tools/sub2api-plugin` 的 `TestReleasedPluginsBumped`、`TestDBCheck` 照常通过。

**e2e（新增 `next/e2e/ac43_plugin_account_context_test.go`）**

- 给一个 volcengine 账号绑定 e2e 的 `CONNECT` 代理（§117.5 已有）。
- 调素材库路由，断言：
  - `audit_logs` 有 `plugin.account.read` 行，且不含任何值；
  - 素材库请求经该代理到达 mock（核心代发）。
- 上传一个清单带 `accounts.credentials` 的测试包，断言 422 或 `outdated_contract` 的拒绝信息。
- 现有 AC03（`ac03_install_test.go:65-66`）改为断言不再出现这两个授予。

### 4.9 插件版本

- 删掉清单里的 `accounts.credentials`：anthropic、azure-openai、bedrock、codex、gemini、newapi、openai、relay、sub2api、typesafe、vertex、volcengine，共 12 个。volcengine 另删 `accounts.read`。
- 版本号不再升：
  - 它们相对 `plugins/released.json` 都已在这一批里升过（anthropic 0.3.4→0.4.0、openai 0.4.3→0.5.0、gemini 0.3.2→0.4.0、relay 0.3.2→0.4.0、codex 0.1.11→0.2.0、volcengine 0.12.8→0.13.0）；
  - azure-openai、bedrock、vertex、newapi、sub2api、typesafe 是首发（0.1.0），`released.json` 里没有它们；
  - `TestReleasedPluginsBumped` 只比较 `released.json`，内容变了而版本已不同就通过，所以在未发布的版本上继续累计，这是现有做法（CHANGELOG"未发布"一节 :77 列的就是这些版本）。
- `hostCompat` 不动（`>=0.1.304 <0.2.0`）：本改动属于 0.1.304。
- 只因 SDK 重新构建的插件（dynweight、growth、guard、mailer、moderation、payment）同样已在这一批里升过版本。
- 发布后照 AGENTS.md 重写 `released.json`。

## 5. 契约改动清单

| 类别 | 改动 |
|---|---|
| proto `common.proto` | `Account.proxy = 8`、`AccountProxy`；`AccountProperties.proxy_id` 改为 reserved 9 |
| proto `host.proto` | 删除 `GetAccountCredentials` 及其两个消息；新增 `GetAccount`；改 `ListAccounts`、`SendAccountRequest`、`AccountScopeCheck`、`AccountSummary` 的注释 |
| proto `platform.proto` | :386-388 注释 |
| `sdk/manifest` | `HostPermissionRisk` 删两项；`check` 对两个授权报 `outdated_contract`；声明账号类型不再需要授权 |
| `sdk/pluginsdk` | `Host.AccountCredentials` 换成 `Host.Account`；新增 `AccountProxyOf`/`AccountProxy`；`FakeHost.GetAccount`；注释 |
| `server/internal/core` | `Egress.Proxy`、`ProxyRef`、`ProxyClient`、`ProxyDirectory.HTTPClient` 的签名、`ProxySpec` 遮蔽；`PluginAccount` 接收代理；`PluginAccountReader.ReadPluginAccount` |
| 迁移 | 0116 删除两个授权的授予行 |
| 版本 | 插件协议 2、Host API 10 不变（未发布） |
| 审计 | 新 action `plugin.account.read`；不再写 `plugin.account.credentials.read` |
| CONTRACTS | 新增 §124；改 §21.1（:287 的 `AccountScopeCheck` 授权）、§21.x（:462"凭证授权"）、§26（:1782 的表）、§26.6（:1873-1886 第 2 条）、§91（:6186 授权一行）、§112.11（审核提示）、§112.12（:8223"声明了账号类型就必须有……"）、§113.3（加一句代理数据不用于联网）、§115（边界）、§117（"插件看不到代理"限定为建账号之前）、§119（:8647）、§123（`Egress` 带代理详情） |
| 其他文档 | ARCHITECTURE.md（:547、:665、:667）、PLUGIN-DEVELOPMENT.md（读账号与代理、不得联网、不打印 `Account`）、PLUGIN-CODEX.md（:56、:74、:192）、PLUGIN-VOLCENGINE-ARK.md（:99、:208-234、:534、:583）、PLUGIN-NEWAPI.md:60、PLUGIN-SUB2API.md:63、CHANGELOG"未发布"一节 |

## 6. 任务拆分

| 包 | 内容 | 文件范围 | 验收 | 依赖 |
|---|---|---|---|---|
| P1 契约 | 改 proto，`buf generate`；`sdk/manifest` 风险表与 `check`；改 SDK 的 `host.go`/`hostcore.go`/`accounts_scope.go` 的接口与注释；`AccountProxyOf`；`FakeHost.GetAccount` | `sdk/proto`、`sdk/gen`、`sdk/manifest`、`sdk/pluginsdk` | `cd next/sdk && go test ./...`；check 用例：两个授权报 `outdated_contract`；遮蔽用例 | 无 |
| P2 代理端口 | `core.Egress.Proxy`/`ProxyRef`/`ProxyClient`、`ProxySpec` 遮蔽；`proxy` 包的 entry 与 `HTTPClient`；`account.proxyEgress` | `server/internal/core/ports_identity.go`、`server/internal/proxy`、`server/internal/account/directory.go` | `proxy`、`account` 包测试；`gateway/egress_test.go` 不变而通过 | 无（可与 P1 并行） |
| P3 构造账号 | `PluginAccount`/`row.pluginAccount` 接收代理；网关每次尝试和 WebSocket 每轮（出口取不到时不调 `Execute`）；`Monitor`；账号包里六条路径改成先取出口；删掉 `small`；改 `waited.go` 与 proto 注释 | `server/internal/core`、`gateway`（execute、dispatch、websocket、usageplugin）、`usage`（task_reconcile）、`account`（testreq、models、quota、balance、refresh、handlers） | 4.8 的核心用例：每个带 `Account` 的调用、改代理后下一次尝试、`Monitor`、出口失败 | P1、P2 |
| P4 宿主与授权 | `GetAccount`；`ReadPluginAccount` 与审计；删掉 `requireOwnCredentials` 和 `GetAccountCredentials`；`ListAccounts`/`SendAccountRequest`/`AccountScopeCheck`（按所有者的确认）；registry；迁移 0116；`requestAccessStatement`；改测试夹具 | `server/internal/plugin/grpcruntime`、`plugin/registry`、`plugin/install`、`plugin/pkg/pkgtest`、`account/plugins.go`、`core/ports_plugin_infra.go`、`migrations/0116_*.sql` | `go test -race ./internal/plugin/... ./internal/account/...`；迁移测试；`GOOS=linux go build ./...` | P1、P2 |
| P5 插件 | 12 个清单删授权；volcengine、codex 迁移到 `GetAccount`；codex 删 `settingsFields` 与断言、改 `flow.spec.ts` | `next/plugins/*/manifest.json`、`plugins/volcengine/internal/volcengine`、`plugins/codex/internal/codex`、`plugins/codex/ui/native/src/flow.spec.ts` | 每个插件 `go test ./...`；codex 的 `npm test`；`tools/sub2api-plugin` 的 `TestReleasedPluginsBumped` | P1 |
| P6 控制台 | 删 `pluginUtil.ts` 的两项和 i18n 的两项 | `next/web/src` | `npm run typecheck && npm test && npm run lint` | 无 |
| P7 e2e | 新增 AC43；改 AC03；`tools/sub2api-plugin/cli_test.go` | `next/e2e`、`next/tools/sub2api-plugin` | `go vet ./e2e/...` 编译通过；在 e2e 栈上跑 `-run '^TestAC(03|43)'` | P3、P4、P5 |
| P8 文档 | 第 5 节列的所有文档 | `next/docs`、`CHANGELOG.md` | 与代码同一组提交 | 随各包 |

- **并行关系**：P1、P2、P6 可以同时开始；P3、P4、P5 等 P1 生成的代码（P3、P4 还要等 P2）；P7 最后。
- **粗略工作量**：
  - P1：1 天；
  - P2：0.5 天；
  - P3：1.5 天（路径多，测试是大头）；
  - P4：1 天；
  - P5：0.5 天；
  - P6：0.2 天；
  - P7：0.5 天；
  - P8：0.5 天；
  - 合计约 5–6 个工作日。

## 7. 替所有者做的决定（可否决）

1. "全部信息"这次只在现有 `Account` 上加代理详情，不扩其它字段（最近测试、额度窗口、余额、协议绑定、分组、tpd_limit 等），等有插件真要用再加。（主控代定）
2. `AccountScopeCheck` 改为不需要授权（方案甲，第 4.1 节）。所有者已确认。
3. 旧包按 §112.10 的旧契约信息拒绝，不新造错误码；并加迁移 0116 删掉残留的授予行。
4. 删掉 `AccountProperties.proxy_id`，`Account.proxy.id` 是唯一来源。
5. 建账号之前的拉模型（`models.go:80`）：所有者已定（2026-11-19）：“代理这时候已经填进去了就用 没填进去就不用”。表单填了代理（已有的或粘贴的）就经它发出，插件收到的 `Account.proxy` 就是这条代理（粘贴的 id 为 0）；没填就直连，`Account.proxy` 为空；填了的代理不可用时报错，不直连、不调用插件。原来代定的“不给代理详情”作废。授权票据（§117）的路由调用不交 `Account`，不受影响。
6. 网关出口取不到时不调用 `Execute`，直接失败转移；账号包里取不到出口时不调 Build。
7. 审计 action 改名为 `plugin.account.read`。
8. 插件协议、Host API 不升，理由是协议 2、Host API 10 都还没发布。

## 8. 没能确认的事

- 线上或测试实例上是否装有持有 `accounts.credentials`/`accounts.read` 的第三方包，或按当前协议 2 构建的 volcengine、codex 测试包：不联网，看不到实例上的安装。
- 线上是否有账号的凭证或设置里留着 `proxy_id` 键：代码里没有写入方，但没有查线上数据。即使有，也只影响这个键在下一次保存时是否加密，不影响代理。
- protobuf-go 将来是否会在 `String()` 里支持 `debug_redact`：本机 v1.36.12 没有实现，所以本文不依赖它。

## 9. 实现记录（与上文不同的地方）

- 额度、续期在出口拿不到时不调用 `Parse*`（上文原写“按传输错误交给 `Parse*`”）：`Parse*` 也带 `Account`，所有者的规则是出口拿不到时不调用插件。额度记失败（原因进快照），续期按 transient 失败。余额记 `transient: <原因>`，测试记失败，与上文相同。
- `ReadPluginAccount` 用与网关同一套列的 `selectRows` + `scanRow`，条件是 `id`、`plugin_key`、未删除（`loadRow` 带的是调用者的 DataScope，不适用于插件调用）。
- `grpcruntime/testdata/testplugin` 没有加逐个调用回写 `Account` 的用例：`Account` 经 gRPC 原样传递，回写只测 protobuf 序列化。改为在构造 `Account` 的地方测：`gateway/egress_test.go`（每次 `Execute` 的 `Account.proxy` 依次是 7、8、空；出口取不到时不调用 `Execute`）、`usage/poll_test.go`（`Monitor`）、`account/testreq_test.go`、`account/refresh_test.go`、`account/balance_test.go`、`account/plugins_test.go`（`GetAccount` 的代理、审计、审计失败、代理停用）。
- `ProxySpec`/`ProxyRef` 的遮蔽测试在 `core/proxyspec_test.go`（类型在 core）。
- volcengine、codex 的路由测试仍用各自的本地假宿主（`routes_db_test.go` 头部写明 `pluginsdktest.Options.Host` 只接受 `*FakeHost`，不能替换），只把方法换成 `Account`；`FakeHost.Accounts` 供其他插件测试用。
- e2e AC43 需要 mock 回答方舟素材库：mock 加了 `POST /__ark/asset`（`deploy/mock-upstream/ark.go`），AC43 的账号是 volcengine 中转类型、`asset_endpoint` 指向它。
- `host.proto` 要 `import "sub2api/plugin/v1/common.proto"` 才能用 `Account`。

