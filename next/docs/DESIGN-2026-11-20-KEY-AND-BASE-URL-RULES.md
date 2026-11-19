# 密钥与基础地址的规则（设计与任务，2026-11-20）

状态：已按方案 B 实现（任务 T1–T7，核心 0.1.304，未发布）。现行规则以 CONTRACTS §127 为准，本文保留为设计记录。对照的代码是 `feat/plugin-ui-code-only` 的 `ef1413472`。文中 `文件:行号` 都以这个提交为准，路径省略开头的 `next/`。

实现与本文的出入（以实现为准）：
- 第 11 节各问题的取舍：Q0 选方案 B；Q2 bedrock 的 `api_key` 一起放开；Q3 用 7.1 做法一（manifest 的 `guardedSettings[].versions`），这是按建议先做的，所有者尚未明确确认；Q1、Q6、Q7 按建议写定；Q4、Q5、Q8、Q9、Q10 在方案 B 下不存在；Q11 按建议接受，已写进 CONTRACTS §127。
- 5.2 第 3 步：scheme 和 host 由 `url.URL{Scheme, Host}.String()` 输出，再接 `EscapedPath()` 去掉末尾 `/` 的结果（`url.Parse` 已把 scheme 转成小写，host 保持用户写的样子）。结果与本文“小写 scheme + `://` + `u.Host`”相同，只是写法不同。
- 5.5 第 3 步：去掉版本段之后，还要再去一次剩下的末尾 `/`（旧存值 `https://x//v1` 的等价性需要）。核心的受限地址比较（`server/internal/account/creds.go` 的 `guardNorm`）同样再去一次，与 SDK 相同；两边都调 `manifest.TrimVersion`，只在路径上找版本段，不碰 scheme 和主机（`http://v1`、`http://api/v3` 的根是它们本身，6.5 的论证依赖这一点），见 CONTRACTS §21.3。
- 7.1 做法一之外：核心判断地址是否改动（`creds.go` 的 `addressValue`，决定是否要重新输入密钥、是否需要 `account:relay`）不认版本段，只差一个版本段也算改了地址（CONTRACTS §21.3）。
- 第 8 节：`@sub2api/ui` 没有为 `createApiKeyForm` 的默认规则另升小版本，因为同一批改动里界面包已升到未发布的 4.0.0。
- 第 9 节：gemini 的 `execute` 类 golden 不变（改成 `…/v1/v1beta` 是方案 A 的结论）；方案 B 只改了 validate 类。第 9 节说 anthropic、relay、typesafe、gemini、newapi、sub2api 的 `credentials_test.go` 有 8–512 用例要改，实际这些测试跟随公共助手 `apikeytest` 后都通过。
- 第 12 节：“含去后缀”那句实际在 CONTRACTS §75.1，不在 §112。

## 1. 结论

- **密钥（决定 A）**：`apikey.Spec` 管的 `api_key` 只要求是非空字符串。去掉首尾空白后为空报"必填"，不是字符串报类型错误，此外不检查前缀、字符集和长度。前端规则同步改为 `required` 加 `whitespace`。不保留任何格式检查（3.2）：密钥里带 CR、LF 等控制字符时，SDK、核心和 Go `net/http` 都会拒绝发出，不存在请求头注入。
- **地址的转义（决定 B）**：`NormalizeBaseURL` 不再清空 `RawPath`，路径按用户填写的转义形式保存。`%2F` 原样保留，请求时也原样发出。两个方案都这样做。
- **版本后缀（决定 C）有两个方案，请所有者选。推荐方案 B。**
  - 两个方案的存储相同：保存用户填的地址，只做去首尾空白、scheme 小写、去末尾 `/`；保存时**不剥**后缀。
  - 两个方案都认同一组版本段：每个账号类型原来 `StripSuffixes` 里的那几个（改名为 `Versions`），整段匹配、区分大小写，按转义形式比较。
  - **方案 A（字面规则，5.3）**：只有基础地址末尾正好是**这条路由**的版本段时，才不重复加；否则把完整路径接上去。
  - **方案 B（5.5）**：基础地址末尾是本类型声明的任一版本段时，请求时把这一段看作"版本"、去掉（至多一段），剩下的部分是"根"，再把路由的完整路径接到根上。也就是说：已经带了版本就不重复；带的是另一个版本（例如 gemini 填了 `/v1`，路由要 `/v1beta`），就按路由的来；没带就加上。
- **方案 B 的要点**：
  - 已存账号的请求地址**全部**不变，不分类型（6.5 有证明），所以不再需要上线前的线上核查。
  - 地址本身以 `/v1` 结尾的站点可以配通：填 `https://x/v1/v1`，请求发往 `https://x/v1/v1/messages`。这是所有者要修的问题。
  - 现在能用的常见填法都继续能用：newapi、sub2api、gemini 填 `/v1`，volcengine 填 `/api/v3`，azure_deployment 填 `/openai/v1`。
  - 实现上，只是把现在保存时那一次剥后缀删掉，请求时那一次保留；`Join` 和路由拼接代码都不用改。
  - 代价有一处契约改动：核心的受限地址比较（guardedSettings）要学会先去掉至多一个版本段，否则带版本的官方地址会被拒（7.1、Q3）。
- **方案 A 的问题**：上面那些常见填法会变得配不通（Q4、Q5、Q8、Q9）。另外 gemini、newapi、sub2api、volcengine apikey 有一部分已存值的请求地址会改变，所以需要上线前做线上核查（6.2、6.3）。
- **线上现状（所有者授权的一次只读核对，只看了键名与数量，没看地址的值）**：
  - 密钥类账号共有 anthropic 15、gemini 2、openai 2、relay 2、volcengine apikey 2、volcengine relay 2；
  - 其中设置了 `base_url` 的：anthropic 9、gemini 2、openai 2、relay 2，volcengine 两类各 2；
  - 没有 azure、newapi、sub2api、typesafe、bedrock、vertex 的账号。
- **与所有者说法的出入（第 4 节）**：
  - new-api 的转发不做"已有就不加"，只有前端提示不要带 `/v1`，唯一的例外是豆包视频的旧接口。
  - 旧版 Sub2API 只在 OpenAI 系做了这件事，而且认的是"任何版本段、不区分大小写"。
  - 两个方案都只认类型声明的版本段（Q7）。

## 2. 所有者的决定（原话）与理解

### A. 密钥不做格式限制

> "密钥不要做任何限制，因为用户可以自定义 base url 和密钥，不要对格式有任何要求。密钥的长度放宽到二零四八字符，或者说直接就字符串吧，因为其他的密钥可能会很长。"

协调者的理解：密钥只要求是非空字符串，不限制前缀、字符集和长度（"直接就字符串"优先于"放宽到 2048"）。范围是所有使用 `apikey.Spec` 的密钥类账号的 `api_key`，前端和 Go 两侧都改。

本文结论：照此执行。去首尾空白保留，理由见 5.1。

### B. 地址里的 `%2F` 不再被还原成 `/`

> "第四个你也规划一下，一起改了"

目标是两点：存下的地址与用户填写的等价，保留转义形式；拼接请求时也保持这个形式。

### C. 版本后缀"没有就加上，有就不管"

> "我们应该对唯一这种做处理，我们的插件自己处理兼容，不要去多也不要少。没有就加上，有就不管，应该是这样的，这个[new-api]和[Sub2API]原来的代码都是这样的"

协调者的理解：
- 不再剥后缀，保存用户填的地址，只做去首尾空白、scheme 转小写这类不改变含义的规范化。
- 拼接请求地址时，基础地址已以这条路由需要的版本段结尾就不重复加，没有就加上，不多剥也不少加。

本文结论：
- 照此执行，另外补充三点：
  - 去掉末尾的 `/` 仍然算"不改变含义"的规范化，保留，理由见 5.2；
  - "版本段"的定义见 5.3；
  - 已存数据的等价性边界见第 6 节。
- 原话里"new-api 和 Sub2API 原来都是这样"与代码不完全相符，见第 4 节。

## 3. 现状调查

### 3.1 密钥的全部限制

| 位置 | 规则 | 作用于 |
|---|---|---|
| `sdk/pluginsdk/apikey/apikey.go:194-209` | 去首尾空白；缺省、空串报 `required`；不是字符串报 `type`，或报 `Texts.KeyNotString` 的文字（`pattern`） | 所有 `Spec` |
| `apikey.go:214-215`、`:462-472` `validAPIKey` | 没设 `KeyValidator` 时要求 8–512 字节，每个字符在 `!`–`~` 之间（可见 ASCII，不含空格） | anthropic、relay、gemini、typesafe、newapi、sub2api、azure 两类、volcengine 两类、vertex_apikey |
| `apikey.go:40-64` `Validator.Check` | 前缀、最短长度、不含空白 | 只有设了 `KeyValidator` 的类型 |
| `apikey.go:71-75` 变量 `apikey.OpenAI` | `sk-`/`sess-` 前缀，至少 10 位 | 只有测试引用（`apikey_test.go`） |
| `sdk/pluginsdk/providers/openai/preset.go:34` | `KeyValidator{Prefixes: sk-, sess-; MinLength: 10}` | openai 插件；azure 在 `azureopenai.go:123` 把它置空 |
| `plugins/openai/internal/openai/openai.go:19,28` | `Spec.Fields["api_key"] = visible(512)`：最多 512 个可见 ASCII 字符 | openai |
| `providers/anthropic/preset.go:21`、`providers/typesafe/preset.go:22`、`plugins/newapi/internal/newapi/newapi.go:42`、`plugins/sub2api/internal/sub2api/sub2api.go:41` | `KeyNotString` 文案写着"8-512 个不含空格的可见字符" | 只是文字 |
| `web/packages/ui/src/form/apiKeyForm.ts:30,85,92` | 默认 `required`、`min 8`、`max 512`、`pattern /^[!-~]+$/` | 所有用 `createApiKeyForm` 的表单：anthropic、openai、gemini、relay、typesafe、newapi、sub2api、azure、volcengine apikey、vertex_apikey |
| `plugins/volcengine/ui/native/src/RelayForm.vue:17` | 同上，自己写的一份 | volcengine relay |
| `plugins/vertex/ui/native/src/form.cases.ts:9,98` | 用例按 8–512 写 | 测试 |

现状里没有任何一个预设或插件要求 2048 位。2048 只出现在 bedrock，它不走 `apikey.Spec`，见 3.3。

### 3.2 去掉限制后，密钥写进请求头会怎样

所有使用 `apikey.Spec` 的类型都把密钥放在请求头里，没有放进查询串的：
- `x-api-key`：anthropic、relay
- `Authorization: Bearer`：openai、typesafe
- `x-goog-api-key`：gemini、vertex_apikey（`plugins/vertex/internal/vertex/upstream.go:73`）
- `api-key`：azure
- volcengine 用自己的 `upstreamHeaders`

newapi、sub2api 按协议沿用各家的请求头。

我在本机用 Go 1.27 实测了 `net/http` 客户端，用的是临时测试，跑完已删除：

| 密钥 | 结果 |
|---|---|
| `abc\r\nX-Evil: 1`、`abc\ndef`、`abc\x00`、`abc\x7f` | 发送前报错 `net/http: invalid header field value for "X-Api-Key"`，请求没有发出 |
| `a b\tc`（内部空格、制表符） | 正常发出，上游收到原值 |
| `ключ`（非 ASCII） | 正常发出（按 obs-text 原样发送字节） |
| `" padded "` | 发出时被去掉首尾空白（HTTP/1 写头时 `textproto.TrimString`），上游收到 `padded` |

我们自己的代码在发送之前还有两道检查，用的是同一个判断（`httpguts.ValidHeaderFieldValue`）：
- **SDK**：`pluginsdk.NewUpstream(...).Build()` 检查所有请求头，含控制字符就返回错误（`sdk/pluginsdk/upstream.go:220`）。
- **核心**：`gateway/relay_check.go:131-145` 再查一遍，不合格按插件故障处理（`pluginUnavailable`）。这种情况会换号重试，但不罚账号。

账号测试、模型列表、余额请求由核心的 HTTP 客户端发出，会在 `net/http` 那一层报同样的错。

结论：**不存在请求头注入**，不需要为安全保留任何检查。去掉限制后，唯一可见的差别是含控制字符的密钥也能保存，之后每次请求都以"插件故障/请求头无效"失败，账号测试也会直接显示这个错误。是否保留一条"不含控制字符"的检查，列为第 11 节问题 Q1，我的建议是不保留。

### 3.3 不走 `apikey.Spec` 的密钥字段

| 字段 | 现状 | 建议 |
|---|---|---|
| bedrock `bedrock_apikey.api_key` | Go：`plugins/bedrock/internal/bedrock/credentials.go:52,130`，8–2048 个可见 ASCII 字符。前端：`plugins/bedrock/ui/native/src/fields.ts:29` | 默认不动，等所有者确认（Q2）。它同样只放在请求头里，按决定 A 的理由可以一起放开 |
| bedrock `access_key_id`、`secret_access_key`、`session_token` | 分别要求 `^[A-Z0-9]{16,128}$`、1–1024 位可见字符、1–8192 位可见字符（`credentials.go:47-56`）。它们参与 SigV4 签名，格式是 AWS 定的 | 不动。这些不是"自定义地址配自定义密钥"的场景 |
| vertex 服务账号密钥文件（JSON） | 由 vertex 自己的 `credentials.go` 解析检查 | 不动。它是结构化凭证，不是一串密钥 |
| vertex `vertex_apikey.api_key` | **走** `apikey.Spec`（`plugins/vertex/internal/vertex/credentials.go:48`），现在受 8–512 限制 | 在范围内，随 SDK 一起放开 |
| volcengine `access_key`、`secret_key` | ≤256 位可见字符（`ui/native/src/rules.ts:7`、Go 的 `validateAssetFields`） | 不动。它们是素材库签名用的 AK/SK，不是 `api_key` |

### 3.4 基础地址：保存与请求的现状

`apikey.NormalizeBaseURL`（`apikey.go:346-376`）在两处使用：
- 保存时：`Spec.Validate` → `validateBaseURL`，`:222-248`；
- 每次请求时：`Spec.FromAccount`，`:313-325`。

它按顺序做这些事：
1. 去掉首尾空白。
2. 拒绝内部空白、`?`、`#`、userinfo，以及非 http(s) 的地址。
3. 对路径去掉末尾的 `/`。
4. 去掉 `StripSuffixes` 里**第一个**匹配的后缀，只去一个，用 `HasSuffix` 判断，区分大小写。
5. 再去一次末尾的 `/`。
6. 把 `RawPath` 置空。
7. 返回 `u.String()`。

第 6 步导致路径里转义过的字符被解码后再重新转义：`%2F` 变成 `/`，`%7E` 变成 `~`。

请求地址是 `基础地址 + 路由路径` 的字符串拼接（`sdk/pluginsdk/standard/request.go:61-67`）。调用点有四处：
- `standard/platform.go:73`（转发）
- `:131`（测试）
- `:151`（模型列表）
- `standard/balance.go:31`（余额）

volcengine 用自己的 `upstreamURL(base, path)`（`plugins/volcengine/internal/volcengine/prefix.go:293`）。

各类型的后缀与路由：

| 类型 | `StripSuffixes` | 路由路径 |
|---|---|---|
| openai（`providers/openai/preset.go:33`） | `/v1` | `/v1/chat/completions`、`/v1/responses`（含 WebSocket）、`/v1/embeddings`、`/v1/images/generations`、`/v1/images/edits`，模型列表 `/v1/models` |
| anthropic、relay（`providers/anthropic/preset.go:18`，relay 在 `plugins/relay/internal/relay/relay.go:27` 用同一预设） | `/v1` | `/v1/messages`、`/v1/messages/count_tokens`，模型列表 `/v1/models?limit=1000` |
| typesafe（`providers/typesafe/preset.go:19`） | `/v1` | `/v1/systemone`，模型列表 `/v1/models` |
| gemini（`providers/gemini/preset.go:26`） | `/v1beta`、`/v1` | `/v1beta/models/{m}:generateContent`、`:streamGenerateContent?alt=sse`、`:countTokens`，模型列表 `/v1beta/models?pageSize=1000` |
| newapi（`plugins/newapi/internal/newapi/newapi.go:38`） | `/v1beta`、`/v1` | openai、anthropic、typesafe 的 `/v1/...` 和 gemini 的 `/v1beta/...`；余额 `/api/usage/token/`（`:49`）；模型列表取第一个部分 openai 的 `/v1/models` |
| sub2api（`plugins/sub2api/internal/sub2api/sub2api.go:37`） | `/v1beta`、`/v1` | 同上，少 typesafe；余额 `/v1/usage`（`:48`） |
| azure_v1、azure_deployment（`plugins/azure-openai/internal/azureopenai/azureopenai.go:122`） | `/openai/v1`、`/openai` | v1：`/openai/v1/chat/completions`、`/responses`、`/embeddings`（`:98-100`）；deployment：`/openai/deployments/{d}/chat/completions?api-version=…`、`/embeddings`（`:139-146`） |
| volcengine apikey（`plugins/volcengine/internal/volcengine/platform.go:177`） | `/api/v3` | `/api/v3/chat/completions`（bot 是 `/api/v3/bots/chat/completions`）、`/responses`、`/embeddings`、`/images/generations`、视频 `/api/v3/contents/generations/tasks`；Anthropic `/api/compatible/v1/messages`（`prefix.go:52`、`platform.go:304-342`） |
| volcengine relay（`platform.go:207`） | 无 | `{api_prefix}`（默认 `/v1`）+ 同样的相对路径；`video_api_prefix`、`video_endpoint`、`asset_endpoint` 另配。基础地址以 `/v1` 或 `/api/v3` 结尾时直接拒绝保存（`prefix.go:125-158`） |
| vertex_apikey | 没有 `base_url` | — |

azure 另外在 `ValidateCredentials` 里，用 `EndpointPattern`（`azureopenai.go:37`）检查 `NormalizeBaseURL(base, nil)` 的输出，规则是 `^https?://[^\s/?#@]+(/|/openai/?|/openai/v1/?|/api/projects/[^\s/?#]+/?)?$`。因为第 6 步已经把 `%2F` 解码，`/api/projects/a%2Fb` 会变成 `/api/projects/a/b`，于是被拒。这就是决定 B 提到的那个例子。

guardedSettings（CONTRACTS §21.3）在插件规范化**之后**比较地址，比较前只去尾部 `/`、并把 scheme 和 host 转小写，不去版本后缀。官方地址的允许值都是不带版本的根地址：openai `https://api.openai.com`、gemini `https://generativelanguage.googleapis.com`、volcengine 的两个主机等。

### 3.5 现状实测：输入 → 存值 → 请求地址

在本机对 `ef1413472` 的 `NormalizeBaseURL` 跑临时测试，输出见第 6 节"改前"一列。几个要点：

- **`https://x/v1/v1`**：openai 系存成 `https://x/v1`，请求时再剥一次，实际请求 `https://x/v1/chat/completions`。真实地址以 `/v1` 结尾的站点因此配不通，这正是所有者说的问题。
- **gemini 的 `https://x/v1/v1beta`**：存成 `https://x/v1`，请求 `https://x/v1beta/...`，与用户填写的不同。CONTRACTS §86.1 已把它记为"例外"。
- **azure 的 `https://x/api/projects/openai`**：项目名恰好是 `openai`，后缀被当作 `/openai` 剥掉，存成 `https://x/api/projects`，请求 `https://x/api/projects/openai/v1/...`，少了项目自己那一段 `/openai`。
- **`HTTPS://X/a%7Eb%2Fc`**：存成 `https://X/a~b/c`。scheme 转了小写，host 的大小写保持原样，路径的转义被解开。

## 4. 两个行为参考

只描述行为。`/home/cvp/rc/new-api` 是 AGPL 项目，没有复制它的代码。

### 4.1 new-api（`/home/cvp/rc/new-api`）

**保存渠道时**：后端只用 TrimSpace 判断地址是否为空，不改写 base_url。前端提交时会去掉首尾空白和末尾所有的 `/`。地址以 `/v1` 结尾时，前端只弹警告"不需要在末尾加 /v1"，确认后照样保存原值（`web/default/src/features/channels/lib/channel-form.ts:1424-1428`、`web/classic/src/components/table/channels/modals/EditChannelModal.jsx:938-947`）。

**请求时**：base_url 原样与固定路径拼接。转发路径上没有"已有就不加"的判断，所有比较都区分大小写。例外有三类：
- 豆包视频渠道开启旧接口时，`/v1` 已存在就不再加；
- 自定义渠道（类型 8）把 base_url 当作完整地址；
- Cloudflare AI Gateway 前缀会去掉请求路径开头的 `/v1`。

| 渠道 | base_url 结尾 → 请求 |
|---|---|
| OpenAI 及默认走 OpenAI 适配器的类型（`relay/channel/openai/adaptor.go:114-190`、`relay/common/relay_utils.go:18-30`），以客户端请求 `/v1/chat/completions` 为例 | `x` → `x/v1/chat/completions`；`x/v1` → `x/v1/v1/chat/completions`；`x/v1/v1` → `x/v1/v1/v1/…`；`x/proxy/v1` → `x/proxy/v1/v1/…`；末尾带 `/` 的值（只能经 API 写入）会出现 `//` |
| Anthropic（`relay/channel/claude/adaptor.go:76-100`） | `x` → `x/v1/messages`；`x/v1` → `x/v1/v1/messages` |
| Gemini（`relay/channel/gemini/adaptor.go:137-163`）。版本取自模型设置，默认 `v1beta`，与 base_url 无关 | `x` → `x/v1beta/models/m:generateContent`；`x/v1beta` → `x/v1beta/v1beta/…`；`x/v1` → `x/v1/v1beta/…` |
| Azure（`openai/adaptor.go:127-175`） | `x` → `x/openai/deployments/d/chat/completions?api-version=…`；`x/openai` → `x/openai/openai/deployments/…` |
| 火山（`relay/channel/volcengine/adaptor.go:239-285`） | `x` → `x/api/v3/chat/completions`；`x/api/v3` → `x/api/v3/api/v3/…` |
| 高级自定义（`relay/channel/advancedcustom/adaptor.go:396-465`） | 每条路由配一个 `upstream_path`，用"去尾 `/` + `/` + 去头 `/`"的方式拼接；不去重，`%2F` 会被解码 |
| 豆包视频旧接口（`model/channel_doubao_video.go:15-27`） | 去掉末尾所有 `/`，已以 `/v1` 结尾（区分大小写）就不加，否则加 `/v1` |

### 4.2 旧版 Sub2API（仓库根 `backend/`）

**保存账号时**：后端不校验，也不规范化 base_url（`backend/internal/handler/admin/admin_account.go:1206,1222`）。前端只做 `.trim()`。

**请求时**：先经 `urlvalidator` 处理（`backend/internal/util/urlvalidator/validator.go`）。默认模式只做 TrimSpace 和去尾 `/`，`%2F` 原样保留。启用 allowlist 时会清空 `RawPath`（`:67-69`），`%2F` 被解码。

| 类型 | 规则 | base_url 结尾 → 请求 |
|---|---|---|
| OpenAI apikey 及兼容平台（`backend/internal/service/openai_endpoint_url.go:8-87`） | 去尾 `/`；路径已以完整端点结尾就原样用。否则看末段：是**任意**版本段（`v<数字>`，可带 `.x`、`alpha*`、`beta*`、`preview*`，**不区分大小写**），就只加去掉 `/v1` 的相对路径；不是，就加完整路径。`RawPath` 清空 | `x`、`x/` → `x/v1/chat/completions`；`x/v1` → `x/v1/chat/completions`；`x/v1/v1` → `x/v1/v1/chat/completions`；`x/proxy/v1` → `x/proxy/v1/chat/completions`；`x/V1` → `x/V1/chat/completions`；`x/api/paas/v4` → `x/api/paas/v4/chat/completions` |
| Anthropic apikey（`gateway_upstream_request.go:31-37`） | 去尾 `/` 后直接加 `/v1/messages?beta=true` | `x/v1` → `x/v1/v1/messages` |
| Gemini apikey（`gemini_upstream_url.go:23-44`） | 去尾 `/` 后直接加 `/v1beta/models/{m}:{action}` | `x/v1beta` → `x/v1beta/v1beta/…`；`x/v1` → `x/v1/v1beta/…` |
| Grok（`xai/oauth.go:637-655`） | 默认地址自带 `/v1`，直接加 `/responses`，不补 `/v1` | `x` → `x/responses` |

### 4.3 一致与不一致

- **一致的地方**：
  - 两者都不在保存时剥后缀；
  - 两者都在请求时去掉末尾的 `/`（new-api 是前端在提交时去）；
  - 都不会把 `x/v1/v1` 合并成 `x/v1`；
  - 都没有本仓库现在这种"存值剥一次、请求再剥一次"的做法。
- **不一致的地方**：
  - "已有就不加"在 new-api 的转发里**不存在**（只有前端提示和豆包视频旧接口）。旧版 Sub2API **只在 OpenAI 系**实现了它，Anthropic 和 Gemini 都会重复。
  - 旧版 Sub2API 认的是"任何版本段、不区分大小写"，例如 `/v4`、`/V1` 也算。new-api 豆包视频认的是 `/v1`，区分大小写。
  - `%2F`：new-api 的字符串拼接会保留；旧版 Sub2API 在 OpenAI 系和启用 allowlist 时解码，其余情况保留。
- **本仓库的先例**：moderation 插件的 `joinURL`（`plugins/moderation/internal/moderation/upstream.go:156-162`，CONTRACTS 第 1015 行）正是所有者说的规则：去尾 `/` 后，基础地址以 `/v1` 结尾且路径以 `/v1/` 开头时只加后半段。整段匹配，区分大小写。新规则与它一致。

## 5. 新规则

### 5.1 密钥

`Spec.validateKey` 的处理：
- 读 `api_key`，先凭证后设置。
- 缺省、`null`、或去首尾空白后为空 → `required`。
- 不是字符串 → `type`，或报类型自己的 `Texts.KeyNotString`。
- 其余一律接受，保存去首尾空白后的值。

删掉的东西：`Validator`、`Spec.KeyValidator`、`validAPIKey`、变量 `apikey.OpenAI`、openai 插件 `Spec.Fields` 里的 `api_key: visible(512)`。`FromAccount` 读取时同样去首尾空白，空值报 `missing api_key`，与现在相同。

保留去首尾空白有两个理由：
- 它不改变含义，HTTP/1 发送时本来就会去掉；
- 已存的值都是去过空白的，保留它才能等价。

前端 `createApiKeyForm` 的默认规则改为 `{ required: true, whitespace: true }`。`ApiKeyFormSpec.apiKey` 的 `min`、`max`、`pattern` 选项保留，但不再有默认值，第一方插件都不传。volcengine `RelayForm.vue:17` 同样改。

### 5.2 基础地址的规范化（取代 `NormalizeBaseURL(base, strip)`）

新签名是 `NormalizeBaseURL(base string) (string, error)`，去掉第二个参数。

1. 去掉首尾空白，空串报 `is required`。
2. 拒绝的情况与现在相同：
   - 内部含空白（`unicode.IsSpace`）；
   - 含 `?`、`#`；
   - 解析失败，或没有 host；
   - scheme 不是 http/https（不区分大小写）；
   - 含 userinfo。

   这些不是新加的限制，是现有规则，见 CONTRACTS §86.1。
3. 输出由三部分拼成：`小写 scheme + "://" + u.Host + 路径`。
   - `u.Host` 保持用户写的样子，与现在相同。
   - 路径取 `u.EscapedPath()` 并去掉末尾所有的 `/`。`EscapedPath` 在用户写的转义合法时就返回它，所以 `%2F`、`%7E` 原样保留。用户写的原样非 ASCII 字符会按 UTF-8 百分号编码，这与原值等价。
4. 不剥任何后缀。

（实现：scheme 与 host 由 `url.URL{Scheme, Host}.String()` 输出，见文首。）

去掉末尾的 `/` 也保留，理由有三点：
- 它不改变拼接后的含义；
- 不去的话，`https://x/` 会拼出 `https://x//v1/...`；
- 已存的值都没有末尾 `/`。

**幂等**：新函数的输出再规范化一次，结果不变。理由是输出里没有空白、`?`、`#`，路径是合法的转义形式，再解析时 `EscapedPath` 返回同一个字符串。

**已存的值**是旧函数的输出，即 `u.String()`，路径已是规范转义。新函数对它们原样返回，因为规范转义下 `RawPath` 为空，`EscapedPath` 等于原路径。所以在决定 B 上，已存账号逐字等价。

### 5.3 方案 A：版本段与拼接

**声明**：`apikey.Spec.StripSuffixes` 改名为 `Versions []string`，值不变：

| 类型 | `Versions` |
|---|---|
| openai、anthropic、relay、typesafe | `/v1` |
| gemini、newapi、sub2api | `/v1beta`、`/v1` |
| azure_v1、azure_deployment | `/openai/v1`、`/openai` |
| volcengine apikey | `/api/v3` |
| volcengine relay | 无 |

每一项以 `/` 开头，不以 `/` 结尾，可以有多段。

**拼接** `apikey.Join(base, path string, versions []string) string`：

1. `base` 是 5.2 的输出（没有末尾 `/`）；`path` 是路由路径，以 `/` 开头，可能带查询串，例如 `/openai/deployments/d/chat/completions?api-version=…`。
2. 候选版本段 v 必须同时满足三个条件：
   - v 在 `versions` 里；
   - `path` 以 v 开头，并且 v 后面紧跟 `/`、`?`，或者 v 就是整个路径（整段边界）；
   - `base` 的路径以 v 结尾。v 以 `/` 开头，所以这个结尾判断本身就在段边界上。
3. 有候选时取最长的 v，结果是 `base + path[len(v):]`。没有候选时，结果是 `base + path`。
4. 比较按转义后的字符串逐字进行，区分大小写。`/V1` 不算 `/v1`，`/%76%31` 也不算。

用到这个函数的地方：`standard.upstreamURL`，以及 `apikey.ModelListRequest`（它现在直接拼 `cfg.BaseURL + list.Path`，`apikey.go:405`）。为此 `Config` 带上 `Versions`，新增方法 `Config.URL(path)`，`standard` 和 volcengine 都调它。WebSocket 仍在拼好之后换 scheme。

**"已有"的几个具体判断**：
- 只认整段。`/xv1` 不以 `/v1` 结尾；路径 `/v1beta/...` 不以版本段 `/v1` 开头，因为 `/v1` 后面跟的是 `b`。
- 区分大小写。
- gemini 类的路由都以 `/v1beta` 开头。基础地址以 `/v1beta` 结尾时不重复加。以 `/v1` 结尾时，`/v1` 不是这条路由的版本段，于是完整加上 `/v1beta/...`。`/v1` 留在 `Versions` 里不影响 gemini 自己的路由，因为 gemini 路由永远匹配不到它。它影响的是 newapi、sub2api 里与 gemini 合并的 `/v1` 路由。
- 多协议类型（newapi、sub2api）按每条路由自己的版本段分别判断。同一个基础地址 `https://x/v1`：openai 路由得到 `https://x/v1/chat/completions`，gemini 路由得到 `https://x/v1/v1beta/models/...`。
- 不带版本段的路径照常完整加上。例如 newapi 的余额 `/api/usage/token/`、volcengine 的 `/api/compatible/v1/messages`。
- azure 声明了两段。azure_v1 的路由同时以 `/openai/v1` 和 `/openai` 开头，基础地址以 `/openai` 结尾时只加 `/v1/...`；azure_deployment 的路由只以 `/openai` 开头。"取最长"就是为这种情况准备的。

### 5.4 Spec 的其他变化

- `Spec.Fields` 里 `base_url` 规则看到的值，改为 5.2 的输出。CONTRACTS §112 第 5271 行"含去后缀"那句要改。
- `standard/check.go:46` 校验默认地址时，改调新签名。
- `Texts` 的几句旧文案（"8-512 个不含空格的可见字符"）改成"必须是字符串"，具体在 `KeyNotString` 字段。

### 5.5 方案 B：请求时至多去掉一个版本段

5.1 的密钥规则、5.2 的存储规范化，两个方案相同。版本段的处理不同：

1. `Versions`（原 `StripSuffixes`）的含义改为"可以出现在基础地址末尾的版本段"，各类型的值与 5.3 的表相同。volcengine relay 仍然没有。
2. 保存时（`Spec.Validate`）不去任何版本段，存 5.2 的输出。
3. 请求时（`Spec.FromAccount`），从存值算出"根"，放进 `Config.BaseURL`：
   - 在存值的**转义形式**路径（已去掉末尾 `/`）上找 `Versions` 里能作为后缀的项，取最长的那个。因为每项都以 `/` 开头，后缀判断本身就落在段边界上。
   - 找到就去掉这一段，只去一段；找不到就原样用。
   - （实现：去掉之后再去掉剩下的末尾 `/`，见文首。）
   - 比较区分大小写。
4. 路由、模型列表、余额的路径照旧完整接到根上：`standard.upstreamURL`、`ModelListRequest`、volcengine 的 `upstreamURL` 都不改。

用转义形式判断，是为了保证 `…/a%2Fv1` 不会被当成以 `/v1` 结尾。它的转义路径末尾是 `%2Fv1`，不是 `/v1`。如果先解码再判断，就会变成 `/a/v1` 而误判。`…/a%2Fv1` 在方案 B 下请求 `…/a%2Fv1/v1/chat/completions`，与方案 A 相同。

同一类型的各项版本段不会同时成为同一个地址的后缀：
- `/v1beta` 与 `/v1`：以 `/v1beta` 结尾的地址末尾是 `beta`，不是 `/v1`；
- `/openai/v1` 与 `/openai`：两者末尾分别是 `v1` 和 `openai`。

所以"取最长"与现在"按顺序取第一个匹配"结果相同（6.5 要用到这一点）。

方案 B 中，基础地址上带的另一个版本会在请求时被换成路由要的版本，例如 gemini 的 `…/v1` → `…/v1beta/…`，newapi 余额从根上拼。这正是"插件自己处理兼容"。它不是保存时的剥离：存值始终是用户填的，控制台显示的也是用户填的。

**`Spec.Fields`、azure `EndpointPattern` 看到的值**：
- 都是存值（5.2 的输出，没去版本段），与方案 A 相同；
- azure 的 `EndpointPattern` 本来就把 `/openai`、`/openai/v1` 列为可选后缀，不用改；
- `/api/projects/a%2Fb` 因为保留了转义，可以通过。

## 6. 改前与改后（方案 A）

本节是方案 A 的对照与论证；方案 B 见 6.4、6.5。

下面四张表都是实测与推导的结果。"改前"一列来自 3.5 的实测，"改后"一列按第 5 节的规则推导。路径中的 `x` 代表 `https://x`，`chat` 代表 `/chat/completions`，`gen` 代表 `/models/m:generateContent`。

### 6.1 对照表

**openai、anthropic、relay、typesafe**（`Versions = /v1`，下表以 openai 的 chat 为例，其他路由同理）

| 输入 | 改前存值 | 改前请求 | 改后存值 | 改后请求 |
|---|---|---|---|---|
| `x` | `x` | `x/v1/chat` | `x` | `x/v1/chat` |
| `x/` | `x` | `x/v1/chat` | `x` | `x/v1/chat` |
| `x/v1` | `x` | `x/v1/chat` | `x/v1` | `x/v1/chat` |
| `x/v1/` | `x` | `x/v1/chat` | `x/v1` | `x/v1/chat` |
| `x/v1/v1` | `x/v1` | `x/v1/chat` | `x/v1/v1` | **`x/v1/v1/chat`** |
| `x/proxy` | `x/proxy` | `x/proxy/v1/chat` | 同左 | 同左 |
| `x/proxy/v1` | `x/proxy` | `x/proxy/v1/chat` | `x/proxy/v1` | `x/proxy/v1/chat` |
| `x/v1beta` | `x/v1beta` | `x/v1beta/v1/chat` | 同左 | 同左 |
| `x/V1` | `x/V1` | `x/V1/v1/chat` | 同左 | 同左 |
| `x/p/a%2Fb` | `x/p/a/b` | `x/p/a/b/v1/chat` | `x/p/a%2Fb` | **`x/p/a%2Fb/v1/chat`** |

**gemini**（`Versions = /v1beta, /v1`）

| 输入 | 改前存值 | 改前请求 | 改后存值 | 改后请求 |
|---|---|---|---|---|
| `x`、`x/` | `x` | `x/v1beta/gen` | `x` | `x/v1beta/gen` |
| `x/v1beta` | `x` | `x/v1beta/gen` | `x/v1beta` | `x/v1beta/gen` |
| `x/v1`、`x/v1/` | `x` | `x/v1beta/gen` | `x/v1` | **`x/v1/v1beta/gen`** |
| `x/v1/v1` | `x/v1` | `x/v1beta/gen` | `x/v1/v1` | **`x/v1/v1/v1beta/gen`** |
| `x/v1/v1beta` | `x/v1` | `x/v1beta/gen` | `x/v1/v1beta` | **`x/v1/v1beta/gen`** |
| `x/proxy` | `x/proxy` | `x/proxy/v1beta/gen` | 同左 | 同左 |
| `x/proxy/v1` | `x/proxy` | `x/proxy/v1beta/gen` | `x/proxy/v1` | **`x/proxy/v1/v1beta/gen`** |

**newapi、sub2api**（`Versions = /v1beta, /v1`；列出 openai 路由、gemini 路由、newapi 余额、sub2api 余额）

| 输入 | 改前存值 | 改前请求（openai / gemini / newapi 余额 / sub2api 余额） | 改后存值 | 改后请求 |
|---|---|---|---|---|
| `x`、`x/` | `x` | `x/v1/chat` / `x/v1beta/gen` / `x/api/usage/token/` / `x/v1/usage` | `x` | 全部相同 |
| `x/v1` | `x` | 同上 | `x/v1` | `x/v1/chat` / **`x/v1/v1beta/gen`** / **`x/v1/api/usage/token/`** / `x/v1/usage` |
| `x/v1beta` | `x` | 同上 | `x/v1beta` | **`x/v1beta/v1/chat`** / `x/v1beta/gen` / **`x/v1beta/api/usage/token/`** / **`x/v1beta/v1/usage`** |
| `x/v1/v1` | `x/v1` | 同上 | `x/v1/v1` | **`x/v1/v1/chat`** / **`x/v1/v1/v1beta/gen`** / … |
| `x/proxy` | `x/proxy` | 各路由在 `x/proxy` 之后 | 同左 | 同左 |
| `x/proxy/v1` | `x/proxy` | 同上 | `x/proxy/v1` | 规律同 `x/v1` 行 |

**azure**（两类都是 `Versions = /openai/v1, /openai`）。表中 `…/chat` 指 `/openai/v1/chat/completions`，`…/dep` 指 `/openai/deployments/d/chat/completions?api-version=V`。

| 输入 | 改前存值 | azure_v1 改前 → 改后 | azure_deployment 改前 → 改后 |
|---|---|---|---|
| `x`、`x/` | `x` | `x/openai/v1/chat` → 相同 | `x/openai/deployments/…` → 相同 |
| `x/openai` | `x` | `x/openai/v1/chat` → 相同（存 `x/openai`，按 `/openai` 段只加 `/v1/chat`） | `x/openai/deployments/…` → 相同 |
| `x/openai/v1` | `x` | `x/openai/v1/chat` → 相同 | `x/openai/deployments/…` → **`x/openai/v1/openai/deployments/…`**（见 Q5） |
| `x/api/projects/p` | 同左 | `x/api/projects/p/openai/v1/chat` → 相同 | 相同 |
| `x/api/projects/a%2Fb` | **拒绝** | → `x/api/projects/a%2Fb/openai/v1/chat` | → `x/api/projects/a%2Fb/openai/deployments/…` |
| `x/api/projects/openai` | `x/api/projects` | `x/api/projects/openai/v1/chat` → **`x/api/projects/openai/openai/v1/chat`**（修正：项目名不再被吃掉） | `x/api/projects/openai/deployments/…` → 相同 |

**volcengine apikey**（`Versions = /api/v3`）

| 输入 | 改前存值 | 改前请求（OpenAI 面 / Anthropic 面） | 改后存值 | 改后请求 |
|---|---|---|---|---|
| `x`（官方主机） | `x` | `x/api/v3/chat` / `x/api/compatible/v1/messages` | `x` | 相同 |
| `x/api/v3` | `x` | 同上 | `x/api/v3` | `x/api/v3/chat` / **`x/api/v3/api/compatible/v1/messages`**（见 Q4） |
| `x/proxy` | `x/proxy` | `x/proxy/api/v3/…` | 同左 | 相同 |

视频任务（`/api/v3/contents/generations/tasks`）、图片、responses、embeddings 的结果与 chat 相同。素材库的 `asset_base_url` 走 `NormalizeBaseURL(base)`（`assets.go:234,265,309`），只受决定 B 影响。

**volcengine relay**：不声明版本段，基础地址必须是根地址（`prefix.go:125-158`），保持现状。理由见第 7 节。

### 6.2 已存账号的等价性论证

记已存值为 S（它可能是旧规范化的输出，也可能是更早没规范化的值），路由路径为 P，旧后缀表为 L（等于新的 `Versions`）。

- 改前：`strip1(S) + P`。`strip1` 去掉 S 末尾 L 中**第一个**匹配的后缀，否则原样返回。另有两点：S 末尾的 `/` 两边都会去掉；转义形式两边一致（5.2）。
- 改后：`Join(S, P, L)`。

**情况一：S 不以 L 中任何一项结尾。** 改前 `strip1(S) = S`，结果 `S + P`。改后没有候选，结果也是 `S + P`。**相等。** 已存值绝大多数属于这种情况，因为保存时已经剥过一次。

**情况二：S 以 L 中某项 v 结尾。** 改前结果是 `S[:-|v|] + P`。分两种：
- P 以 v 开头（整段边界）：改后 `S + P[|v|:]`，与改前相等。
- P 不以 v 开头：改后 `S + P`，比改前多出 v。**这时不等价。**

（L 中两项互为后缀的情况，只有 azure 的 `/openai/v1` 与 `/openai`，下面单独论证。）

逐类看：

| 类型 | 是否存在"S 以 v 结尾而 P 不以 v 开头" | 结论 |
|---|---|---|
| openai、anthropic、relay、typesafe | L 只有 `/v1`，所有路由（含模型列表）都以 `/v1/` 开头 | **任何已存值都等价** |
| azure_v1 | P 都以 `/openai/v1` 开头，也就以 `/openai` 开头。S 以 `/openai/v1` 结尾时，改前剥 `/openai/v1`、改后去掉同一段，相等；S 只以 `/openai` 结尾时，改前剥 `/openai`、改后按最长候选 `/openai` 去掉同一段，相等 | **任何已存值都等价** |
| azure_deployment | P 只以 `/openai` 开头。S 以 `/openai` 结尾（不以 `/openai/v1` 结尾）时相等；S 以 `/openai/v1` 结尾时不等价 | 后者存不下来：保存时原始输入要先过端点规则，以 `/openai/v1` 结尾的输入被剥成根地址。端点规则在首发当天（`2c84c7a28`，2026-11-17，当时写在表单 schema 里）就加上了；只有它之前的 0.1.0（`01d7a18da`）期间，输入 `…/openai/v1/openai/v1` 才可能存下 `…/openai/v1`。6.3 的查询把它一并查掉 |
| gemini | P 都以 `/v1beta` 开头。S 以 `/v1` 结尾时不等价 | 见 6.3 |
| newapi、sub2api | S 以 `/v1` 结尾时，gemini 路由和 newapi 余额不等价；S 以 `/v1beta` 结尾时，`/v1` 路由和两种余额不等价 | 见 6.3 |
| volcengine apikey | S 以 `/api/v3` 结尾时，Anthropic 面不等价，其余等价 | 见 6.3 |

在不等价的几类里，S 以 v 结尾只有三个来源：
- 用户填了两个连续的后缀，保存时只剥掉一个。例如 `…/v1/v1beta`、`…/v1/v1`、`…/v1beta/v1beta`、`…/api/v3/api/v3`。
- 绕过保存时规范化写入的数据，例如早于规范化的旧数据、直接改库、导入。
- golden 样例里的 `base_url=v1`、`v1beta` 这类"请求时遇到未规范化存值"的用例。它们是录制下来的鲁棒性用例，不代表线上数据。

第一种来源下，新结果恰好就是用户填写的地址。例如 `…/v1/v1beta` 会发往 `…/v1/v1beta/models/...`。也就是说，改变的正是所有者要修的那类错误。

### 6.3 发布前的只读核查（只在选方案 A 时需要）

上线前请所有者在线上执行一次（只读）：

```sql
SELECT id, plugin_key, type, settings->>'base_url' AS base_url
FROM accounts
WHERE deleted_at IS NULL
  AND ((plugin_key IN ('gemini','newapi','sub2api') AND type = 'apikey'
        AND rtrim(settings->>'base_url', '/') ~ '/(v1|v1beta)$')
    OR (plugin_key = 'volcengine' AND type = 'apikey'
        AND rtrim(settings->>'base_url', '/') ~ '/api/v3$')
    OR (plugin_key = 'azure_openai' AND type = 'azure_deployment'
        AND rtrim(settings->>'base_url', '/') ~ '/openai/v1$'));
```

各表单把 `base_url` 存在 `settings`（明文 jsonb）。如果有账号把 `base_url` 放在凭证里（`credentials_enc`，加密），这条查询看不到。需要的话，由所有者另行授权核心侧解密核查。查询结果为空，就说明所有已存账号都等价。结果不为空时，逐个由所有者决定：
- 改成用户本意的地址；
- 或者接受新的请求地址。

不写"旧值走旧逻辑"的代码。

### 6.4 现在、方案 A、方案 B 对照

记号说明：
- `x` 代表 `https://x`；
- `chat` 代表 `/v1/chat/completions`，anthropic 的 `/v1/messages` 结果相同；
- `gen` 代表 `/v1beta/models/m:generateContent`；
- `tok` 代表 newapi 余额 `/api/usage/token/`；
- `use` 代表 sub2api 余额 `/v1/usage`；
- `ark` 代表 `/api/v3/chat/completions`，`msg` 代表 volcengine 的 `/api/compatible/v1/messages`；
- `v1c` 代表 `/openai/v1/chat/completions`，`dep` 代表 `/openai/deployments/d/chat/completions?api-version=V`。

"现在"一列是对 `ef1413472` 实测的结果（3.5）。方案 B 一列逐行按 5.5 推导，并与"现在"核对过。粗体表示与"现在"不同。

| 类型 / 输入 | 现在 | 方案 A | 方案 B |
|---|---|---|---|
| openai 系 `x`、`x/` | `x/chat` | `x/chat` | `x/chat` |
| openai 系 `x/v1`、`x/v1/` | `x/chat` | `x/v1` + `/chat/completions`，即 `x/chat` | 根 `x`，`x/chat` |
| openai 系 `x/v1/v1` | `x/chat` | **`x/v1/v1/chat/completions`** | 根 `x/v1`，**`x/v1/v1/chat/completions`** |
| openai 系 `x/proxy/v1` | `x/proxy/chat` | 同现在 | 同现在 |
| openai 系 `x/v1beta`、`x/V1` | `x/v1beta/chat`、`x/V1/chat` | 同现在 | 同现在 |
| openai 系 `x/p/a%2Fb` | `x/p/a/b/chat` | **`x/p/a%2Fb/chat`** | **`x/p/a%2Fb/chat`** |
| openai 系 `x/a%2Fv1` | `x/a/v1/chat/completions`（解码后被当作 `/v1` 剥掉） | **`x/a%2Fv1/chat`** | **`x/a%2Fv1/chat`** |
| gemini `x/v1beta` | `x/gen` | `x/gen` | `x/gen` |
| gemini `x/v1`、`x/proxy/v1` | `x/gen`、`x/proxy/gen` | **`x/v1/gen`、`x/proxy/v1/gen`** | 同现在 |
| gemini `x/v1/v1beta` | `x/gen` | **`x/v1/gen`** | 根 `x/v1`，**`x/v1/gen`**（即用户填的地址） |
| gemini `x/v1/v1` | `x/gen` | **`x/v1/v1/gen`** | 根 `x/v1`，**`x/v1/gen`** |
| gemini `x/v1beta/v1` | `x/gen` | **`x/v1beta/v1/gen`** | 根 `x/v1beta`，**`x/v1beta/gen`**，即 `x/v1beta/v1beta/models/…` |
| newapi、sub2api `x/v1` | `x/chat`、`x/gen`、`x/tok`、`x/use` | `x/chat`、**`x/v1/gen`**、**`x/v1/tok`**、`x/use` | 同现在 |
| newapi、sub2api `x/v1beta` | 同上 | **`x/v1beta/chat`**、`x/gen`、**`x/v1beta/tok`**、**`x/v1beta/use`** | 同现在 |
| newapi、sub2api `x/v1/v1` | 同上 | **全部在 `x/v1/v1` 之后** | 根 `x/v1`：**`x/v1/chat`**、**`x/v1/gen`**、**`x/v1/tok`**、**`x/v1/use`**（即站点在 `x/v1` 下） |
| volcengine apikey `x`、`x/api/v3` | `x/ark`、`x/msg` | `x/api/v3`：`x/ark`、**`x/api/v3/msg`** | 同现在 |
| volcengine apikey `x/api/v3/api/v3` | `x/ark`、`x/msg` | **在 `x/api/v3/api/v3` 之后** | 根 `x/api/v3`：**`x/api/v3/ark`**、**`x/api/v3/msg`** |
| azure_v1 `x`、`x/openai`、`x/openai/v1` | `x/v1c` | `x/v1c` | `x/v1c` |
| azure_deployment `x/openai/v1` | `x/dep` | **`x/openai/v1/dep`**（404） | 同现在 |
| azure `x/api/projects/a%2Fb` | 拒绝保存 | **接受**，`x/api/projects/a%2Fb/v1c` | **接受**，同 A |
| azure_v1 `x/api/projects/openai`（项目名叫 openai） | `x/api/projects/v1c`，少了项目自己那段 | **`x/api/projects/openai/v1c`**（修正） | 同现在，仍然少一段（见 Q11） |
| 无自定义权限者提交 `https://api.openai.com/v1` | 通过 | **拒绝**（Q3） | 通过（核心按根比较，7.1） |

协调者列出的预期，逐条核对如下：
- `https://x` → 加上：成立。
- `https://x/v1` + `/v1/messages` → `https://x/v1/messages`：成立。
- `https://x/v1/v1` → `https://x/v1/v1/messages`：成立。
- gemini、newapi、sub2api 填 `/v1` 或 `/v1beta` 全部可用（含余额）：成立。
- volcengine apikey 填 `/api/v3`、azure_deployment 填 `/openai/v1` 可用：成立。

和"现在"不同的输入只有三类：
- 末尾连续带两个版本段：B 只去一段，现在会去两段（保存一次、请求一次）；
- 带 `%2F` 等转义；
- azure 项目名恰好是 `openai`，A 能修正，B 不能。

前两类正是本次要修的问题。

### 6.5 方案 B 的已存账号等价性

**结论：对所有类型成立，不需要 6.3 的线上核查。**

论证。记已存值为 S，路由路径为 P，旧后缀表为 L（等于新的 `Versions`）。
- 现在的请求地址是 `strip1_L(N旧(S)) + P`：`N旧` 去掉末尾的 `/` 并把转义还原成规范形式，`strip1_L` 去掉 L 中按顺序第一个匹配的后缀。
- 方案 B 的请求地址是 `root_L(N新(S)) + P`：`N新` 是 5.2 的规范化，`root_L` 按转义形式取最长的一个匹配后缀去掉。

逐项看：
1. 已存值都是旧规范化的输出 `u.String()`（CONTRACTS §86.1："已存的地址都是它的输出"）：scheme 是小写，路径是规范转义，`RawPath` 为空，没有末尾 `/`。对这样的 S，`N旧(S) = S` 和 `N新(S) = S` 都成立：`EscapedPath` 返回规范形式，也就是 S 自己的路径。
2. L 中任意两项不会同时是同一个字符串的后缀（5.5），所以"按顺序第一个匹配"和"最长匹配"是同一项。加上 S 中没有非规范的转义，"按解码形式判断"和"按转义形式判断"结果也相同。所以 `strip1_L(S) = root_L(S)`。
3. 两边都把 P 原样接上，WebSocket 换 scheme 的方式也相同。所以对任意 P 结果都相等，包括多协议类型的每条路由、模型列表、余额路径，以及 volcengine 的 `upstreamURL`。

这里不需要区分 S 是否以版本段结尾，所以 6.2 里方案 A 的那些例外，在方案 B 下都不存在。

唯一的前提是第 1 条："已存值都是旧规范化的输出"。不满足的只有绕过 `Spec.Validate` 写入的值（直接改库、导入）。即使是这种值：
- 首尾空白、末尾 `/`、大写 scheme，两边处理相同；
- 只有路径里带非规范转义（例如 `%2F`、`%7E`）时，B 会照原样发出，而现在会解码后发出。

线上核对显示只有 15 个账号设置了 `base_url`。核心保存账号时，无论走控制台还是 API，都要先经插件的 `ValidateCredentials` 规范化（CONTRACTS §21.3 的 `prepare`），只有直接改库或导入的值会绕过这一步。所以我认为不需要核查。所有者如果仍想确认，可以查 `settings->>'base_url' LIKE '%\%%'`，这一条同样是只读的。

**golden 回放**：execute、test、models 三类用例的结果在方案 B 下全部不变，因为这些用例的存值本身带 `/v1`、`/v1beta`，正好落在第 2 条上。只有 validate 类里，密钥的几个拒绝用例和 base_url 去后缀的保存值会变。

## 7. azure 与 volcengine 怎么纳入（方案 A）

**azure**：
- 纳入新规则，`Versions = /openai/v1, /openai`，与原来的 `StripSuffixes` 相同。
- `EndpointPattern` 不变，继续匹配新 `NormalizeBaseURL` 的输出。因为保留了转义，`/api/projects/a%2Fb` 能通过，`[^\s/?#]+` 匹配到 `a%2Fb`。
- 变化有三处：
  - azure_v1 填 `…/openai` 时，按最长候选只加 `/v1/...`，结果正确；
  - 项目名为 `openai` 的 Foundry 地址不再被吃掉一段；
  - azure_deployment 填 `…/openai/v1` 时，改前被悄悄修正，改后会发往 `…/openai/v1/openai/deployments/...`，得到 404。

  第三处是否要在该类型上拒绝 `/openai/v1`，列为 Q5。我的建议是不加拒绝，改帮助文案。
- 帮助文案（`plugins/azure-openai/ui/native/src/messages.ts:8-9,25-26`）改成："地址已以 /openai/v1（或 /openai）结尾时不再重复添加"。前端 pattern（`forms.ts:16`）不变。

**volcengine apikey**：
- 纳入新规则，`Versions = /api/v3`，`upstreamURL`（`prefix.go:293`）里的拼接改用 `Config.URL`（或 `apikey.Join`）。同源检查保持不变。
- 官方主机的允许值是不带版本的根地址，没有 `account:settings:custom` 的人只能从预设里选，所以对他们没有变化。
- 有自定义权限的人填 `…/api/v3` 时，OpenAI、图片、视频照常工作，Anthropic Messages 会拼到 `/api/v3` 后面。是否接受，列为 Q4。

**volcengine relay**：不纳入，保持现状：
- 它的基础地址是中转站根地址；
- 路径由 `api_prefix`、`video_api_prefix` 表达；
- 基础地址以 `/v1`、`/api/v3` 结尾时，保存时就明确拒绝，并提示用户去填 `api_prefix`（`prefix.go:109-158`）。

这是"拒绝并告诉用户该填哪里"，不是剥后缀；它防的是视频轮询 404 导致按预估计费，原因写在该处注释里。按"有就不管"把它改成去重，会让文本和视频前缀不同的中转站重新踩坑。列为 Q6，建议不动。它的 `api_prefix` 与端点覆盖（`endpoint.go`）都不受本次改动影响。

### 7.1 方案 B 下的 azure、volcengine 与受限地址

- **azure**：
  - `Versions = /openai/v1, /openai`；
  - `EndpointPattern` 不改，仍匹配存值（保留转义）；
  - 两个类型填 `/openai`、`/openai/v1` 都和现在一样能用；
  - 帮助文案改成"地址末尾的 /openai/v1 或 /openai 在请求时按接口重新拼接"；
  - 项目名恰好是 `openai` 的 Foundry 地址，仍和现在一样少一段（Q11）。
- **volcengine apikey**：`Versions = /api/v3`，根由 `FromAccount` 算出，`prefix.go:293` 的 `upstreamURL` 和同源检查都不改。
- **volcengine relay**：不动（Q6）。
- **受限地址（guardedSettings）**：
  - 比较点在核心 `server/internal/account/creds.go:434-469` 的 `checkGuarded`。它在插件规范化之后，把每个受限字段经 `guardNorm`（`:474-482`：去首尾空白、去末尾 `/`、scheme 与 host 小写）与 `allowed` 及账号原值比较。
  - 核心不知道插件 Go 代码里的 `Versions`，所以要么核心学会这件事，要么扩大允许列表。两种做法：
  - **做法一（推荐）**：manifest 的 `GuardedSetting` 增加可选的 `versions: []string`，即该字段末尾可以带的版本段。`guardNorm` 在比较前对两边去掉至多一个（转义形式、最长、区分大小写）。
    - 改动位置：`sdk/manifest/manifest.go:544-547` 加字段；`sdk/manifest/check/account_check.go:287` 起校验每项以 `/` 开头、不以 `/` 结尾；`creds.go` 的 `guardNorm` 多一个参数，约 15 行；五个插件的 `manifest.json`（anthropic、openai、gemini、typesafe、volcengine apikey）写上与 `Versions` 相同的值；CONTRACTS §21.3。
    - 每个插件加一个测试，断言 manifest 里的 `versions` 等于 Go 里的 `Spec.Versions`，防止两边写得不一致。
    - 界面不变：允许值仍只有一项，表单照旧锁定。
    - 旧核心不认识这个字段时会忽略它，manifest 解码不严格。这时只是带版本的官方地址被拒，所以插件的 `hostCompat` 下限要随核心版本提高。
  - **做法二**：把带版本的官方地址加进 `allowed`，例如 openai 加 `https://api.openai.com/v1`。只改 manifest，不改契约。但允许值变成两项后，`guarded.ts:36` 的锁定条件（只有一项时锁定）不再成立，没有自定义权限的人会看到一个两项的下拉框，界面变了。

## 8. 前端

- **`createApiKeyForm`**（`web/packages/ui/src/form/apiKeyForm.ts`）：
  - `api_key` 的默认规则改为 `{ required: true, whitespace: true }`，删掉 `API_KEY_PATTERN`；
  - 注释和 `ApiKeyFormSpec.apiKey` 的说明改为"默认只要求非空"；
  - 这是 `@sub2api/ui` 公开行为的变化，按 AGENTS.md 升小版本，并同步 `web/packages/ui/README.md:453,506`。（实现：未另升，界面包同批升到 4.0.0，见文首。）
- **base_url 的前端 pattern** `^https?://[^\s?#]+$` 不变，与 Go 侧的拒绝条件一致，`%2F` 能通过。
- **`SUrlPresetInput` 的预设值**不变。预设都是不带版本的根地址，在新规则下请求地址不变。
- **帮助文案**：把"末尾的 /v1（/v1beta、/openai/v1、/openai）会被去掉"统一改成"地址已以 /v1（…）结尾时不再重复添加"。涉及：
  - `plugins/openai/ui/native/src/messages.ts:7,26`
  - `anthropic …:7,17`
  - `typesafe …:7,17`
  - `gemini …:7,17`
  - `relay …:5,14`
  - `azure-openai …:8-9,25-26`
- **newapi、sub2api**（`messages.ts:6,16`）：改成"填站点根地址，各协议按官方路径拼在它后面；地址已以 /v1 或 /v1beta 结尾时，对应协议不再重复添加"。另外写明：带 `/v1` 时，Gemini 协议和余额查询也会拼在 `/v1` 之后。
- **volcengine apikey** 的 `baseUrlHelp`（`messages.ts:7,90`）：补一句"填根地址；已以 /api/v3 结尾时 OpenAI、图片、视频不再重复添加，Anthropic Messages 仍拼 /api/compatible/v1"。
- **volcengine relay**：只改 `RelayForm.vue:17` 的 `api_key` 规则。

## 9. 涉及的代码与测试

**SDK（`sdk/pluginsdk`）**
- `apikey/apikey.go`：
  - 删 `Validator`、`OpenAI`、`KeyValidator`、`validAPIKey`；
  - `StripSuffixes` 改为 `Versions`；
  - `NormalizeBaseURL` 改签名、改实现（5.2）；
  - 新增 `Join`，`Config` 带上 `Versions` 和 `URL(path)`；
  - `ModelListRequest` 用 `cfg.URL`；
  - 包注释和 `Texts` 注释同步改。
- `apikey/apikey_test.go`：
  - `TestValidator`、`TestBuiltinValidators` 删除；
  - `TestNormalizeBaseURL`、`TestNormalizeBaseURLIsStable` 改为新规则，加 `%2F`、`%7E`、非 ASCII、末尾 `/`、大写 scheme 的用例；
  - 新增 `TestJoin`：覆盖 5.3 的每条判断，以及第 6 节各表；
  - 新增"旧输出在新规则下请求地址不变"的性质测试：对一组输入算出旧函数的存值，比较 `strip1(S)+P` 与 `Join(S,P)`，只有 6.2 列出的那几类可以不同。
- `apikey/apikeytest/apikeytest.go`：
  - 去掉 `MinKey` 和"短于最短""长于 512""带空格""非 ASCII"这几个拒绝用例；
  - 改成接受：带空格、非 ASCII、4096 位的密钥；
  - 加两个用例："base_url 带 /v1 原样保存""%2F 原样保存"；
  - `Form` 的说明同步改。

  这是公共测试助手，所有用它的插件测试都要跟着改。
- `standard/request.go`、`platform.go`、`balance.go`：`upstreamURL` 改用 `Config.URL`。
- `standard/check.go:46`：改调新签名。
- `standard/README.md:32,250,273`：同步。
- `providers/{openai,anthropic,gemini,typesafe}/preset.go`：`StripSuffixes` 改为 `Versions`；openai 删 `KeyValidator`；`Texts` 文案按 5.4 改。
- **golden 回放**（`standard/goldentest/testdata/golden/*.json`）会变：
  - `validate` 类：`inner space key`、`inner tab key`、`too short key`、`wrong prefix key`、`too long key`、`non-ascii key`、`nine character key` 由拒绝改为接受；`base_url settings v1`、`base_url v1beta`、`base_url port prefix` 的保存值不再去后缀。
  - `execute`、`test`、`models` 类中，存值本身带版本段的用例：
    - anthropic、relay、openai 的 `base_url=v1`、`http port prefix v1 slash`：结果不变，这正好验证了 6.2 情况二的第一种；
    - `base_url=v1beta`：结果不变；
    - gemini 的 `base_url=v1`、`http port prefix v1 slash`、`base_url in credentials`（值为 `…/v1`）：改为 `…/v1/v1beta/...`。（这一条是方案 A 的结论；按方案 B 实现后不变。）

  按 goldentest README 的约定，每条改动都写 `note`，说明是本设计改的。

**插件**（每个都要在 `released.json` 体系下升版本）
- `plugins/openai`：`openai.go:28` 去掉 `api_key: visible(512)`，注释同步；`credentials_test.go`；UI 文案。
- `plugins/anthropic`、`relay`、`typesafe`、`gemini`、`newapi`、`sub2api`：测试（`credentials_test.go` 里的 8-512 用例）、UI 文案。newapi 和 sub2api 的 `Spec` 改用 `Versions`。
- `plugins/azure-openai`：
  - `azureopenai.go`：`StripSuffixes` 改为 `Versions`，删掉 `KeyValidator = nil` 这一行；
  - `EndpointPattern` 的注释改为"新规范化输出"；
  - `credentials_test.go` 加 `%2F` 项目路径的用例；
  - UI 文案。
- `plugins/volcengine`：
  - `platform.go:177` 改为 `Versions`，`:172-176`、`:197-206` 的注释同步；
  - `prefix.go:293` 的 `upstreamURL` 改用 `Join`；
  - `assets.go` 改调新签名；
  - `form_constraints_test.go`、`RelayForm.vue`、UI 文案；
  - `docs/PLUGIN-VOLCENGINE-ARK.md:819,969,983`。
- `plugins/vertex`：`credentials.go:45-48` 的注释；`ui/native/src/form.cases.ts` 的用例。
- `plugins/bedrock`：只在 Q2 同意时改 `credentials.go:52,130` 和 `ui/native/src/fields.ts:29`。

**前端包**：`web/packages/ui/src/form/apiKeyForm.ts`；`composite.spec.ts:277` 的注释；`useForm.spec.ts` 不受影响（它测的是规则引擎本身）；`ui-contract.spec.ts` 按需改。

**核心**：方案 A 不改核心，影响见 Q3。方案 B 按 7.1 做法一改 `checkGuarded` 和 manifest 的 `GuardedSetting`（契约改动）。

**方案 B 下 SDK 的改动更小**：
- `NormalizeBaseURL` 去掉 strip 参数并保留转义；
- `FromAccount` 用 `Versions` 算根；
- `Validate` 不再剥后缀；
- 不需要 `Join`，也不改 `standard` 的拼接；
- golden 只改 validate 类。

## 10. 任务拆分

按推荐的方案 B 拆分；选方案 A 时的差异写在最后。

T1、T2 先做，可以并行。T3–T6 在 T1 合入功能分支后并行。T7 最后做。

| 任务 | 输入 | 产出 | 验证 |
|---|---|---|---|
| **T1 SDK** | 5.1、5.2、5.5 | 见下 | `gotest.sh <wt> sdk go test -count=1 ./pluginsdk/...`；`gofmt`、`go vet` |
| **T2 核心受限地址** | 7.1 做法一 | 见下 | `gotest.sh <wt> sdk go test ./manifest/...`；`gotest.sh <wt> server go test -race ./internal/account/...`（PG 测试必须真的跑了） |
| **T3 简单插件** | T1、T2 | 见下 | 各插件 `go test ./...`（含 golden、conformance）；UI 用 `next-check-plugin-ui.sh` 检查 |
| **T4 azure** | T1 | `Versions`，删掉 `KeyValidator = nil` 那一行；测试覆盖 6.4 azure 各行（`%2F` 项目路径、两个类型 × `/openai`、`/openai/v1`）；UI 文案 | `go test ./...` |
| **T5 volcengine** | T1、T2 | apikey 的 `Versions` 和 manifest 的 `versions`；relay 不动；测试；两个表单的密钥规则；文案；PLUGIN-VOLCENGINE-ARK.md | `go test ./...`，原有的同源检查和视频用例全过 |
| **T6 前端包与 vertex、bedrock** | 第 8 节、Q2 | `createApiKeyForm` 的默认规则、README、`@sub2api/ui` 升版本；vertex 用例；bedrock（Q2 同意后才做） | `next/web` 下 `npm run typecheck && npm test && npm run lint`；插件 UI 检查脚本 |
| **T7 文档、版本、收尾** | T1–T6 | 第 12 节的文档（含 CONTRACTS §21.3）；各插件升版本、提高 `hostCompat`；CHANGELOG | `tools/sub2api-plugin` 的 `TestReleasedPluginsBumped` |

各任务的产出：
- **T1 SDK**：
  - 密钥规则；`NormalizeBaseURL(base)` 保留转义、不剥后缀；`StripSuffixes` 改名 `Versions`；`FromAccount` 算根；四个 `providers` 跟着改；
  - 测试：`apikey_test`、`apikeytest`；
  - "旧存值在新规则下请求地址不变"的性质测试：用一组输入算出旧存值，对所有类型和路由比较 `strip1(S)+P` 与新结果，要求全部相等；
  - golden 的 validate 用例和说明。
- **T2 核心受限地址**：manifest 的 `GuardedSetting.versions` 及其检查；`checkGuarded`、`guardNorm` 按根比较；核心测试：带版本的官方地址通过、`…/a%2Fv1` 不当作版本、PATCH 与原值比较。
- **T3 简单插件**：openai、anthropic、relay、typesafe、gemini、newapi、sub2api 的 Go 改动、测试、UI 文案；四个受限插件的 manifest 写 `versions`，并加"manifest 与 `Spec.Versions` 一致"的测试。

选方案 A 时：
- T1 改为实现 5.3 的 `Join` 和 `standard` 拼接，golden 的 execute、test、models 类也要改；
- 取消 T2；
- T4、T5 的输入加上 Q4、Q5；
- T7 加上把 6.3 的核查交给所有者。

所有任务都只用本地测试和 mock 上游，不向任何真实上游发请求。

## 11. 需要所有者确认的问题

- **Q0 选方案 A 还是方案 B**（**已采用 B**）：**建议选 B**。B 下已存账号的请求地址全部不变，现在能用的常见填法都继续能用，所有者要修的"地址本身以 /v1 结尾"也能修。A 会让 Q4、Q5、Q8、Q9 那些填法配不通，而且需要线上核查。
- **Q1 控制字符**（按建议写定）：密钥不加"不含控制字符"的检查。SDK、核心、Go `net/http` 都会拒绝发送，不存在注入（3.2）。代价是这种密钥能保存，但每次请求都以"请求头无效"失败。
- **Q2 bedrock**（**已采用：放开**）：bedrock 的 `api_key`（现在要求 8–2048 位可见字符）不走 `apikey.Spec`。建议一起放开，它同样只放在请求头里。AK/SK/会话令牌、vertex 密钥文件、volcengine AK/SK 不动。
- **Q3 受限地址**（**已采用做法一**，按建议先做，待所有者确认）：
  - 选 B：建议用 7.1 做法一（manifest 增加 `versions`，核心按根比较）。这是契约改动，请确认；也可以选做法二（扩大允许列表，但界面会从锁定变成下拉）。
  - 选 A：带版本的官方地址会被拒，建议接受。
- **Q4 volcengine apikey 填 `/api/v3`**（方案 B 下不存在）：选 B 时这个问题不存在。选 A 时，Anthropic Messages 会拼到 `/api/v3` 后面，建议接受并改文案。
- **Q5 azure_deployment 填 `/openai/v1`**（方案 B 下不存在）：选 B 时这个问题不存在。选 A 时请求会 404，建议不在保存时拒绝，只改文案。
- **Q6 volcengine relay**（按建议写定）：不动。基础地址带 `/v1`、`/api/v3` 时继续在保存时拒绝，并提示去填 `api_prefix`。
- **Q7 版本段的认法**（按建议写定）：只认类型声明的版本段，整段匹配，区分大小写，按转义形式判断。"任意版本段"的认法（旧版 Sub2API 的 OpenAI 系）会改变 `…/v1beta`、`…/V1`、`…/v4` 这类已存值的请求地址。代价是智谱 `…/api/paas/v4` 这类站点仍然配不通，现在也配不通。
- **Q8 newapi、sub2api 填 `/v1`** 和 **Q9 gemini 填 `/v1`**（方案 B 下不存在）：选 B 时这两个问题不存在。选 A 时，Gemini 路由和余额会多出 `/v1`。
- **Q10 线上核查**（方案 B 下不需要）：选 B 时不需要（6.5）。选 A 时请执行 6.3 的只读查询。
- **Q11 azure 项目名恰好是 `openai`**（**已接受**，写进 CONTRACTS §127）：只在选 B 时存在。地址 `…/api/projects/openai` 的末尾被当作版本段 `/openai`，请求少了项目自己那一段，和现在一样。线上没有 azure 账号。建议接受，并在文档里注明；要修就得给 azure 单独加例外，我不建议这样做。

## 12. 要改的文档与 CHANGELOG

- `docs/CONTRACTS.md`：
  - §86.1 第 5925 行"基础地址的规则"整条重写为第 5 节，删掉"例外"那句。
  - §112 第 5271 行"含去后缀"改为"经规范化（不去后缀）"。
  - 第 8627、8629 行插件表的 `api_key` 和 `base_url` 说明改掉。
  - 新增一节"密钥与基础地址（2026-11-20）"，写 5.1–5.3 的约定，以及"已存账号的等价边界"。
- `docs/PLUGIN-DEVELOPMENT.md:243,257`：`stripSuffixes`、`keyValidator` 的对应行改为 `Versions`，删掉 keyValidator。
- `sdk/pluginsdk/standard/README.md:32,250,273`；`sdk/pluginsdk/standard/goldentest/README.md`（改动用例的说明）。
- `docs/PLUGIN-NEWAPI.md:20`、`PLUGIN-SUB2API.md:20`、`PLUGIN-TYPESAFE.md:18`、`PLUGIN-CLOUD-PROVIDERS.md:55,118`：去掉"8–512 个……可见字符"，并改写地址说明。
- `docs/PLUGIN-VOLCENGINE-ARK.md:819,969,983`：`StripSuffixes` 改为 `Versions`，说明 relay 不变。
- `web/packages/ui/README.md:453,506`。
- `CHANGELOG.md` 的"未发布"，写用户可见的三点：
  - 密钥不再限制格式和长度；
  - 地址里的转义原样保存；
  - 不再剥版本后缀，保存用户填的地址；按所选方案说明请求时怎么处理版本段，并附上 6.4 里会变的输入。
- 选方案 B 时另改 CONTRACTS §21.3（`guardedSettings[].versions` 与按根比较）。
