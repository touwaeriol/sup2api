# standard：API Key 账号类型的公共库

“账号 = API Key + 基础地址，请求 = 基础地址上按上游协议选的路由，Key 放在请求头里”这一类账号类型，用这个包实现全部平台接口。openai、gemini、relay、azure_openai 四个插件和 anthropic 插件的 `apikey` 类型都是这一类（设计稿 `docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md` 7.3 节）。

它是链接进插件自己进程的 Go 库，不是运行器，也没有清单字段：插件用 Go 写一个 `Type`（通常从厂商预设出发再改几处），交给 `standard.New`，得到的 `*standard.Platform` 就是 `pluginsdk.Platform`。

库里只放每个协议都一样的东西。路径、路径里的模型、请求体的改写、测试请求体、错误分类策略这些协议差异，是 `Type` 里的 Go 值和函数，由 `sdk/pluginsdk/providers/{openai,anthropic,gemini,typesafe}` 的预设写好。**库里不按厂商分支**：需要 `if openai` 的东西放进预设或插件，不放进这里。

## 接口

`Type`、`Auth`、`Route`、`Model`、`Header`、`Test`、`Balance`、`BalanceRequest`、`Request` 和预设（`sdk/pluginsdk/providers/{openai,anthropic,gemini,typesafe}` 的 `APIKey(id)`）的定义和说明在 Go 文档里（`go doc github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard`），这里只说怎么用：

- 从预设出发得到一个 `standard.Type`（每次调用返回新的值，可以随意改），改 `ID`、`Spec`、`Routes`、`Headers` 等几处；
- 上游同时说几家协议（中转站）时，`standard.Merge(id, spec, parts...)` 把几份预设合成一个类型（例子四）；
- `standard.New(types...)` 得到 `*standard.Platform`：它实现 `pluginsdk.Platform`、`pluginsdk.UpstreamBuilder`、`pluginsdk.ModelLister`、`pluginsdk.BalanceReader`，SDK 用 `pluginsdk.ExecuteDefault` 跑它的 `Execute`；配置不一致时 `New` panic，插件测试里可以先调 `standard.Check(types...)`。

| 字段 / 函数 | 用途 |
|---|---|
| `Route.Auth *Auth` | 这条路由的鉴权头；nil 用 `Type.Auth`。`Check` 对它做与类型级相同的检查 |
| `Route.Classify *classify.Policy` | 这条路由所属协议的失败分类；nil 用 `Type.Classify`。`ClassifyError` 按 `meta.protocol` 找路由，没有协议或协议不认识时用类型的 |
| `Type.Tests []Test` | 按协议的测试请求（每个协议至多一个，要有 `Test`）：`BuildTestRequest` 取 `BuildTestRequestRequest.protocols` 里第一个在 `Test` 或 `Tests` 里有测试的协议；列表为空（没有偏好）用 `Test`，列表不空而都没有测试时回 `FailedPrecondition`，不退回 `Test`（CONTRACTS §122.3）；`usage_protocol` 回报用的协议 |
| `Type.Balance *Balance` | 账号余额：`Requests`（基础地址上的 GET，按顺序；`BalanceRequest.Auth` nil 用类型的鉴权，`&standard.Auth{}` 不带 Key）和 `Read`（把按顺序的回答读成 `BalanceResult`）。nil 时两个余额调用回 `UNIMPLEMENTED`；回答数与请求数不符时库直接给 TRANSIENT，`Read` 可以按下标取 |
| `Merge(id, spec, parts...) Type` | 一个类型服务几份预设的路由，见例子四 |
| `BalanceFailure(*BalanceAnswer) *BalanceResult` | 不是成功的回答的结果：传输错误、429、5xx、其它非 2xx → TRANSIENT，401/403 → AUTH_REJECTED，信息带状态码和回答体开头；2xx 返回 nil |
| `Micros(number string) (int64, bool)` | 上游答的十进制数（`"7.25"`、`"1250000"`、`"-1"`；gjson 对 JSON 数字的 `String()` 就是）换成百万分之一的整数（`BalanceResult.amount_micros`）：同一个数换个写法，**不是换算**。第六位小数之内精确，第七位四舍五入（远离零）；不是普通十进制数（含指数写法、空串）或超出 int64（约 9.2 万亿）时第二个值为 false。余额是上游的数字本身，单位是上游给的原文（没有就空），不做任何单位、汇率换算（CONTRACTS §52） |

## 一次请求怎么构造

`BuildUpstream`（SDK 的 `ExecuteDefault` 调它，然后不等待地中继、返回 `host_usage`）：

1. 按 `account.type` 找类型。类型不符报 `unsupported account type "x"`；账号没有类型时，只有一个类型的平台用那个类型，多个类型的拒绝。
2. `Spec.FromAccount`：取 Key 和基础地址（规范化、必填、默认值；存值末尾带类型声明的版本段 `Spec.Versions` 时去掉这一段，至多一段，得到“根”），错误原样返回。
3. 按 `meta.protocol` 找路由（空协议用 `DefaultProtocol`），没有就 `unsupported upstream protocol "x"`。
4. 按 `Route.Model` 取上游模型。
5. 请求体：一次 `jsonedit` 里先删 `FilterFields`，再 `Route.Edit`；两者都没有就不读请求体。
6. 请求头：
   - **默认头**（运营方的透传、覆盖能改）：核心交来的全部客户端请求头（核心已按清单的 `passHeaders` 过滤，库不再筛），然后类型的 `Headers`，然后路由的 `Headers`。WebSocket 路由去掉 `content-type`。
   - **权威头**（运营方改不掉）：只有鉴权头（`Auth.Header`）。
7. URL：基础地址 + `Path`，WebSocket 路由换成 ws/wss（`pluginsdk.WebSocketURL`，codex 用同一个函数）。

第 6 步的鉴权头是路由的 `Auth`，路由没有就是类型的。

账号测试走选中的测试（`Tests` 按核心给的协议顺序，核心没给协议时 `Test`）的路由，不带客户端请求头，鉴权头与其余头放在同一个 map 里（`BuildTestRequestResponse` 只有一个 map）；模型列表和余额请求只带类型的头和鉴权（余额请求可以不带），不带 `content-type`。

## 请求体

- **按路径改，不整份解析**：改写都用 SDK 的 `jsonedit`（`sdk/pluginsdk/jsonedit`）。没有改写的请求体原样交回（同一个切片）；改写只动它自己的字节，键顺序、空白、未知字段、`\u` 转义、超出 float64 的数字都不变，只解析一次、最多拷贝一次。`FilterFields` 是几个 `Delete`；`Edit` 在同一个 `Doc` 上接着改，例如 openai 的 `IncludeStreamUsage`（改写本身是 `openai.StreamUsage`，自己改请求体的插件如 volcengine 直接调它，流式按 `pluginsdk.RequestStream` 判断）：

  ```go
  func IncludeStreamUsage(d *jsonedit.Doc, r *standard.Request) {
  	StreamUsage(d, r.Stream)
  }

  func StreamUsage(d *jsonedit.Doc, stream bool) {
  	if stream {
  		d.Set("stream_options.include_usage", []byte("true"))
  	}
  }
  ```

  `Set` 原地替换已有的值，没有就加在所在对象的末尾（连同缺的上层对象）；`Delete` 连同一个分隔逗号删掉成员。
- **非法 JSON、重复键**：核心在调插件之前已经把这两种请求体以 400 拒绝（设计稿 8.1 的结构检查）。库在改写前仍然检查一遍（`jsonedit` 的规则）：要改写的请求体不是合法 JSON，或者 `Set` 路径上的某个键在它所在的对象里出现两次，就返回 `INVALID_ARGUMENT`，什么都不中继（核心按插件不可用换号），而不是发出一个改了一半的请求。路径之外的重复键不归改写管，原样保留；`Delete` 删掉一个路径的每一次出现。路由没有任何改写时，库根本不读请求体。
- **顺序变化（设计稿 12.5）**：`FilterFields` 和 `Edit` 在插件里，排在核心的改写（`claude_max_tokens`、账号的参数覆盖）之后。运营方在参数覆盖里写了一个被过滤的字段，以后会被删掉。预设都不带过滤字段；目前只有 azure_openai 的两个类型自己写了 `service_tier`、`safety_identifier`。
- **流式标志**：交入时为真，交回时仍为真。库不碰 `stream`。`Request.Stream` 是 `meta.stream` 或请求体的 `"stream"` 为 true（`pluginsdk.RequestStream`）：只流式的账号类型由核心把 `stream: true` 写进请求体，`meta.stream` 却保留客户端的值，openai 的 `IncludeStreamUsage` 看 `Request.Stream`，否则这种请求拿不到用量。

## 例子一：OpenAI 风格（openai 插件）

`plugins/openai/internal/openai/openai.go`：

```go
// Package openai is the OpenAI API key platform: the openai preset with the
// account's organization and project, forwarding every request field.
package openai

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(APIKey()) }

// APIKey is the apikey type: the preset, plus the organization and project
// an account may set, which replace the client's headers.
func APIKey() standard.Type {
	t := openai.APIKey("apikey")
	t.Headers = append(t.Headers,
		standard.Set("openai-organization", standard.Field("organization")),
		standard.Set("openai-project", standard.Field("project")))
	return t
}
```

`plugins/openai/main.go`：

```go
// Command openai is the sub2api-next OpenAI plugin.
package main

import (
	_ "embed"

	"github.com/Sub2API-Devs/sup2api/next/plugins/openai/internal/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	pluginsdk.Serve(openai.New(), pluginsdk.WithManifest(manifestJSON))
}
```

`plugins/openai/internal/openai/openai_test.go`：

```go
func TestGolden(t *testing.T) {
	standardtest.Run(t, "openai", "../../manifest.json", New())
}
```

### WebSocket 的一轮（`openai.responses_ws`，`platform.websocket.v1`）

清单声明 `platform.websocket.v1` 后，核心对 Responses WebSocket 会话的**每一轮**消息调一次 `Execute`（设计稿 4.8）。插件不用为此写任何代码：预设的 `openai.responses_ws` 路由是 `WebSocket: true`，同一个 `BuildUpstream` 给出

| `UpstreamRequest` | 值 |
|---|---|
| `method` | `GET` |
| `url` | 握手地址：`wss://api.openai.com/v1/responses`（基础地址是 http 时为 `ws://`） |
| `headers` | `authorization: Bearer <key>`（权威头） |
| `default_headers` | 转发的客户端请求头，加 `openai-beta`：客户端没给就是 `responses_websockets=2026-02-06`，给了别的值就追加，给了任何版本的 `responses_websockets` 就用客户端的；没有 `content-type` |
| `body` | 这一轮的消息（`FilterFields` 照样作用，没有路由改写） |

`ExecuteDefault` 照常不等待地中继、返回 `host_usage`。核心在会话还没有上游连接时用 URL 和握手头建连，之后每轮只发消息。例：

```go
in := &pluginv1.ExecuteRequest{
	Meta:    &pluginv1.RequestMeta{Protocol: "openai.responses_ws", Model: "gpt-4o-mini", Stream: true},
	Account: &pluginv1.Account{Type: "apikey", CredentialsJson: `{"api_key":"sk-…"}`},
	InboundHeaders: map[string]string{"openai-beta": "assistants=v2"},
	Body:    []byte(`{"type":"response.create","response":{"model":"gpt-4o-mini"}}`),
}
// BuildUpstream(ctx, in) =>
//   GET wss://api.openai.com/v1/responses
//   headers         {"authorization": "Bearer sk-…"}
//   default_headers {"openai-beta": "assistants=v2,responses_websockets=2026-02-06"}
//   body            {"type":"response.create","response":{"model":"gpt-4o-mini"}}
```

## 例子二：Gemini 风格（gemini 插件）

模型在路径里，取自 `meta.model`（去掉 `models/` 前缀，必填），Key 在 `x-goog-api-key`。预设原样就是插件要的：

```go
// Package gemini is the Gemini API key platform: the gemini preset as it is.
package gemini

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// New returns the platform of the apikey account type.
func New() *standard.Platform { return standard.New(gemini.APIKey("apikey")) }
```

`main.go` 与 openai 的相同，只换 import；测试是 `standardtest.Run(t, "gemini", "../../manifest.json", New())`。

预设怎么把模型写进路径，可以照着写别的“模型在路径里”的路由：

```go
standard.Route{
	Path:  func(r *standard.Request) string { return "/v1beta/models/" + url.PathEscape(r.Model) + ":generateContent" },
	Model: standard.Model{FromMeta: true, TrimPrefix: "models/", Required: true, Missing: "model is required (path /v1beta/models/{model}:...)"},
}
```

## 例子三：Anthropic 风格（anthropic 插件的 `apikey`，relay 插件）

anthropic 插件已经有进程（模型目录的路由），把平台嵌进现有的 `Plugin`：

```go
package anthropic

import (
	"context"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// Plugin is the anthropic plugin: the apikey platform (pluginsdk.Platform)
// and the model catalog route (pluginsdk.HTTP).
type Plugin struct {
	*pluginsdk.Router
	*standard.Platform
	host pluginsdk.Host
}

// New returns the plugin.
func New() *Plugin {
	p := &Plugin{Router: pluginsdk.NewRouter(), Platform: standard.New(APIKey())}
	p.Handle("GET", "/models", p.listModels)
	return p
}

// APIKey is the apikey type: the anthropic preset as it is (every request
// field is forwarded).
func APIKey() standard.Type { return anthropic.APIKey("apikey") }

// Init implements pluginsdk.Initializer.
func (p *Plugin) Init(_ context.Context, h pluginsdk.Host) error {
	p.host = h
	return nil
}
```

`main.go` 不变（`pluginsdk.Serve(anthropic.New(), pluginsdk.WithManifest(manifestJSON))`）。

relay 是同一个预设加三处差异（它收到哪两个客户端请求头由清单的 `passHeaders` 决定，核心过滤）：

```go
// Package relay is the platform of Anthropic-compatible relays.
package relay

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

// New returns the platform of the relay_key account type.
func New() *standard.Platform { return standard.New(RelayKey()) }

// RelayKey is the anthropic preset on the relay's own address: the base URL
// is required and has no default, a request must name its protocol, and the
// plugin forwards every field.
func RelayKey() standard.Type {
	t := anthropic.APIKey("relay_key")
	t.Spec.DefaultBaseURL, t.Spec.RequireBaseURL = "", true
	t.Spec.Texts.BaseURLRequired = "Base URL of the relay is required / 请填写中转站的 Base URL"
	t.DefaultProtocol = ""
	return t
}
```

测试：`standardtest.Run(t, "anthropic", "../../manifest.json", New().Platform)`、`standardtest.Run(t, "relay", "../../manifest.json", New())`。

azure_openai 的两个类型从 openai 预设出发（`api-key` 鉴权、必填的资源地址、版本段 `/openai/v1` 和 `/openai`（请求时去掉，再按路由拼接）、按部署名的经典路由带 `api-version`、没有模型列表、十个客户端请求头），配置在 `plugins/azure-openai/internal/azureopenai/azureopenai.go`，验收断言在同一插件的 `platform_test.go`。

## 例子四：中转站（多平台 + 余额，New API、Sub2API 的形状）

站点同时转发 OpenAI、Anthropic、Gemini（New API 再加 TypeSafe）协议，各协议用各自官方的鉴权头；余额是站点的接口。插件只写类型和余额的读法：

```go
// Package newapi is the platform of New API sites: one apikey type serving
// the OpenAI, Anthropic, Gemini and TypeSafe protocols, and the key's balance.
package newapi

import (
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/apikey"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/anthropic"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/gemini"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/openai"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/providers/typesafe"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard"
)

func New() *standard.Platform { return standard.New(APIKey()) }

func APIKey() standard.Type {
	spec := apikey.Spec{RequireBaseURL: true, Versions: []string{"/v1beta", "/v1"}, Texts: …}
	t := standard.Merge("apikey", spec, openai.APIKey(""), anthropic.APIKey(""), gemini.APIKey(""), typesafe.APIKey(""))
	t.Balance = &standard.Balance{
		Requests: []standard.BalanceRequest{
			{Path: "/api/usage/token/"}, // the key's quota, with the key
		},
		Read: readBalance, // standard.BalanceFailure, then the site's number as it is, standard.Micros
	}
	return t
}
```

`Merge` 做的事：

- 每份预设的类型级 `Headers` 前置到它每条路由的 `Headers`，类型级 `Auth`、`Classify` 写进它每条路由的 `Auth`、`Classify`。所以 `openai.chat` 带 `Authorization: Bearer` 和 `stream_options.include_usage` 的改写，`anthropic.messages` 带 `x-api-key` 和默认的 `anthropic-version`，`gemini.generate` 带 `x-goog-api-key`、模型在路径里，各自按自己的策略分类（Anthropic 的 529 冷却 30 秒、OpenAI 的 `insufficient_quota` 停用……）。
- 结果的类型级 `Headers` 为空、`DefaultProtocol` 为空（网关请求总带协议）；`Auth`、`Classify`、`Test`、`Models`、`Balance` 取第一份的（模型列表因此是 OpenAI 的 `GET /v1/models`、Bearer）；`Tests` 收集每一份的测试，账号测试按核心给的协议顺序选。
- `spec` 换掉各份的凭证规则。
- 两份有同一个协议、某份有 `FilterFields`（合并后会作用到所有协议）时 panic。

Sub2API 一样，只是余额读 `{Path: "/v1/usage"}`，单位取应答的 `unit`（没有就不带），不合 TypeSafe。New API 的额度点数不带单位。读法（站点的字段）留在各插件里，不进本库。

一个插件的类型服务别的插件声明的平台（New API 服务 TypeSafe 的 `typesafe` 平台）时，清单的 `platforms` 只列平台 id；平台定义只在 `providers/typesafe.Platform()`，TypeSafe 插件的清单测试断言自己清单里的平台与它相同，New API 的一致性测试把它交给 `conformance.Plugin.Platforms`。

TypeSafe 本身（`providers/typesafe.APIKey(id)`）：Bearer、`content-type: application/json`、`typesafe.systemone` → `/v1/systemone`（请求体原样）、默认地址 `https://api.typesafe.ai`（末尾的 `/v1` 当作版本段，请求时去掉）、`typesafe.Policy`、`jev-latest` 上的一题测试、模型列表 `GET /v1/models`（`models.#.name`）、没有余额。`typesafe.Policy` 不给客户端信息和错误类型，所以上游的错误体原样回给客户端；`detail` 只写进账号上的原因。

## 测试

- `standardtest.Run(t, plugin, manifestPath, p)` 先核对插件清单（`manifestPath`）交给样例账号类型的客户端请求头与文件的 `pass_headers` 完全一致（清单改了而样例没跟上就在这里失败），再用一个 `*standard.Platform` 回放 `plugin` 的全部样例（`goldentest.Run` 加 `standardtest.Golden(p, 文件的 PassHeaders)`）：`Execute` 先像核心那样只留下文件 `pass_headers` 里的客户端请求头（样例故意多给），再调 `BuildUpstream`（`ExecuteDefault` 中继的就是它）；其余四类是同名方法，分类用样例的时钟（`ClassifyAt`）。
- 不是样例文件那个插件的实现（例子四的合并类型）回放它也服务的协议：`standardtest.Run(t, "openai", "../../manifest.json", p, goldentest.Select(goldentest.Forwarded("apikey", openai.ProtocolChat, …)))`，只取这些协议上、账号自带基础地址的 `execute` 用例。客户端请求头按样例协议所属的平台（这里是 openai）核对。凭证规则有意不同的用例（例如中转站请求时也把末尾的 `/v1beta` 当作版本段去掉，openai 预设不认它）再用一个 `Select` 排除（`Case.BaseURL`）。本包 `standardtest/merged_test.go` 就是这样回放三份文件的（291 条）。
- 余额：`pluginsdktest.Start(t, p, …)` 后 `h.Balance(ctx, account, answer)`，`answer` 按 URL 给回答（本包 `balance_test.go` 有 New API、Sub2API 两种形状）。
- 想连同“恰好一次中继、不等待、返回 `host_usage`”一起验，用假宿主：`pluginsdktest.Start(t, p, …)` 后 `h.Execute(ctx, in).Relayed(t)`（本包 `platform_test.go` 的 `TestExecute`）。
- `standard.Check(types...)`：类型配置能通过检查（返回 error；`New` 遇到同样的问题会 panic）。

本包自己的验收只测库和预设本身：`platform_test.go`、`body_test.go`（请求体逐字节保持、改写只动自己的字节、非法 JSON 与重复键）、`merge_test.go`（路由级鉴权与分类、合并、按协议的测试）、`balance_test.go`（余额与两个辅助函数）、`providers/typesafe/typesafe_test.go`（请求字节、分类、平台定义过清单检查），`providers/gemini/golden_test.go` 用原样的 gemini 预设回放 gemini 的样例。插件的类型配置不在 SDK 里复制：1498 条冻结样例（openai 441、anthropic 343、gemini 371、relay 343）由各插件用自己的构造函数回放，azure_openai 的断言在它自己的 `platform_test.go`，CI 测试每个 `plugins/*` 模块。
