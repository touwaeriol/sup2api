# sub2api 插件：另一个 Sub2API 站点的 API Key

> 状态：2026-11-18 按分支 `feat/up-p5` 的代码写成，**未发布**（插件 0.1.0，需核心 ≥0.1.304）。只用单元测试、合规套件和 openai / anthropic / gemini 三份冻结黄金样例验证过，**没有对真实 Sub2API 站点验证**；`/v1/usage` 的形状按本仓库旧版 `backend/`（`gateway_handler.go`）和 next 核心（`internal/apikey/report.go`、CONTRACTS §62）的代码写成。
> 设计见 [DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md](DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md) 3.5.1、3.5.2、3.6、3.7 与 D8、D11–D15、D20；写法是 SDK 的 `standard.Merge`（`sdk/pluginsdk/standard/README.md` 例子四）。

## 1. 它做什么

- 一个账号类型 `apikey`（控制台显示 "Sub2API"），不声明自己的平台：账号同时服务内置的 `openai`、`anthropic`、`gemini` 平台。上游可以是旧版 Sub2API，也可以是另一个 sub2api-next。
- **同协议原样转发**：客户端用哪个协议来，就按这个协议的官方路径发给站点，请求体不改（OpenAI Chat 流式照 openai 预设补 `stream_options.include_usage`）。插件不做任何协议转换；跨平台转换照协议绑定（CONTRACTS §110）默认关闭。
- 各协议用各自官方的鉴权头：OpenAI 系 `Authorization: Bearer`，Anthropic `x-api-key`（客户端没带 `anthropic-version` 时补默认值），Gemini `x-goog-api-key`。旧版对三种头都接受；新版作上游时更严（OpenAI 只认 Bearer、Gemini 只认 `x-goog-api-key`），正好对上。
- 能查 Key 的余额（第 4 节）。
- 插件只构造请求，由核心发送；插件不联网（不申请 `net`）。
- 内置、仅安装、默认不启用（D11）：管理员在插件页启用后才能建这类账号。不服务 TypeSafe（D15，清单加一行即可）。

## 2. 建账号

| 字段 | 必填 | 说明 |
|---|---|---|
| 站点地址（`base_url`） | 是 | 站点根地址，如 `https://your-sub2api.example.com`；按填写的保存。地址已以 `/v1` 或 `/v1beta` 结尾时不再重复添加：请求时先去掉这一段（只去一段），再拼各协议和余额查询的完整路径，所以站点本身在 `/v1` 之下时填 `…/v1/v1`；`%2F` 等转义原样保存、原样发出 |
| API Key（`api_key`） | 是 | 站点的 Key，任意非空字符串（保存时去掉首尾空白）；加密保存 |

- 地址由运营方填，插件不声明 `guardedSettings`、`externalServices`：转发和余额请求只能发往账号自己的 `base_url` 主机（§112.17）。这是"可自定义上游"的类型：**只有 own 级权限的用户**新建或改地址时需要 `account:relay`（CONTRACTS §21.3.1，与 relay 插件相同）。
- 默认模型：不写。用"拉取模型"读站点的 `GET /v1/models`（Bearer，取 `data[].id`；旧版对 Claude 分组返回 Anthropic 形状的 `data[]`，同样取得到 id）。旧版对 Gemini 分组的 Key 返回什么形状未核实，拉不到时手填。
- 账号测试：核心按账号开放的协议给出偏好顺序，插件取第一个有测试请求的协议：默认 OpenAI Chat（`gpt-4o-mini`）；绑定成只开 Messages 时测 `/v1/messages`（`claude-haiku-4-5`），只开 Gemini 时测 `generateContent`（`gemini-2.5-flash-lite`）。

## 3. 协议

| 平台 | 端点 |
|---|---|
| openai | 全部：`/v1/chat/completions`、`/v1/responses`（含 WebSocket）、`/v1/embeddings`、`/v1/images/generations`、`/v1/images/edits`（只收 JSON 请求体） |
| anthropic | 全部：`/v1/messages`、`/v1/messages/count_tokens` |
| gemini | 全部：`generateContent`、`streamGenerateContent`、`countTokens` |

- **上游 Key 能用哪些协议取决于它在站点上所属的分组**，插件看不出来。旧版的规则：Messages / Chat / Responses 对多数分组开放（站内转换）；Embeddings 只给 openai 分组；生图只给 openai、grok 分组（其它 404）；Gemini 原生接口只给 gemini 分组（其它回 400）。
- 所以按分组收窄：账号编辑器"协议与模型"卡片一选"指定"，只勾这个 Key 真正能用的协议。例：anthropic 分组的 Key 只勾 Messages（计数跟随）；同一站点 gemini 分组的 Key 另建一个账号只勾 Gemini。被绑定排除的协议调度时就不会选到这个账号（§110.4）。
- 上游不支持某个协议时，后果按该协议预设的失败分类（D20）：400 / 404 原样返回客户端、账号不受影响；5xx 换号并短暂冷却；401 / 403 停用账号。

## 4. 余额

一个请求 `GET /v1/usage`，带 `Authorization: Bearer <Key>`，在账号的 `base_url` 上（每 600 秒定时刷新，控制台可随时手动刷新）。Key 用尽或过期也能查。

**余额就是站点返回的数字和单位本身，不做任何换算**（2026-11-18 所有者："上游返回多少我们就是多少，我们不要做任何折算，就算数字不对也不管，汇率不对也不管"，CONTRACTS §52）。金额依次读 `remaining`、`balance`、`quota.remaining`，取第一个存在的，原样；单位是应答的 `unit` 原文（`USD` 就显示 `7.5 USD`），**没有 `unit` 就不带单位**（只显示数字），也不校验它是不是币种代码。

| 应答 | 显示 |
|---|---|
| 旧版，带额度的 Key（`quota{…}`、顶层 `remaining` = 额度剩余） | 额度剩余 |
| 旧版，订阅分组（`remaining` = 日 / 周 / 月剩余的最小值） | 该值 |
| 旧版，订阅分组一项限额都没配（`remaining: -1`） | **不限** |
| 旧版，余额计费（`remaining` = `balance` = 用户钱包） | 钱包余额 |
| 新版（`remaining` 总有：余额与 Key 额度剩余取小） | `remaining` |
| `isValid: false` | 认证被拒"上游报告此 Key 已停用或过期"（D8） |
| 有 `unit`（`USD`、`CNY` 或任何文字） | 数字 + `unit` 原文，不折算 |
| 没有 `unit` | 只有数字 |
| 金额不是数字（例如字符串 `"plenty"`），或大到百万分之一放不进 int64（约 9.2 万亿） | 暂时失败"余额不是可读的数字" |
| 三个金额字段都没有 | 暂时失败 |
| 401 / 403 | 认证被拒 |
| 传输错误、429、5xx、其它非 2xx | 暂时失败 |

- 金额为负（透支）时显示负数。余额只显示，不停用账号、不影响调度、不告警（D1）。

## 5. 权限

- 宿主权限：`platform.register`（账号随调用下发，不需要授权，CONTRACTS §124）、`ui.native`（账号表单 `Sub2APIKeyForm`，插件自带的原生界面 `ui/native`，用 `@sub2api/ui` 的 `createApiKeyForm`；保存时的校验在插件的 `ValidateCredentials`）；没有 `net`、没有文件权限、没有 `gateway.endpoint`。
- 能力：`platform.adapter.v1`、`platform.websocket.v1`（Responses 的 WebSocket 模式）。
- 不在清单里写价格：模型价格由管理员在核心录入。

## 6. 已知限制

- Gemini 原生接口只对上游 gemini 分组的 Key 可用（旧版），其它分组的 Key 要在协议绑定里去掉 Gemini。
- 生图只收 JSON 请求体，且旧版只对 openai、grok 分组开放。
- 旧版"只配了速率限制、没配总额度"的 Key，`/v1/usage` 里三个金额字段都没有，余额显示为暂时失败（设计没有规定这种情况，没有替它猜成"不限"）。
- `rate_limits`（5 小时 / 1 天 / 7 天窗口或 rpm / tpm）不读、不显示。
