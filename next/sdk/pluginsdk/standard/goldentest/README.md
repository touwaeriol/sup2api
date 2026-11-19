# goldentest：API Key 平台插件的冻结黄金样例

这里是 openai、anthropic、gemini、relay 四个插件的验收依据，共 1498 条（441、343、371、343），供 `sdk/pluginsdk/standard` 和用它写的插件回放。样例最初录自这四个插件早先的实现，转换成“完整请求进、最终上游请求出”的形式（`docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md` 7.3 节）。

**样例是冻结的**：不增、不删，`input`、名字、顺序不改。样例与实现不一致时只有两条路：

- 实现偏离了样例：改实现；确实合理的个别差异用 `Except` 逐条声明（见下）。
- 所有者决定改变行为：改这些样例的期望值，逐条写 `note`，并登记在“与录制不同的样例”表里。

## 文件

| 路径 | 内容 |
|---|---|
| `testdata/golden/<plugin>.json` | 转换后的样例，随包内嵌（`go:embed`），插件模块 import 这个包就能用，不需要相对路径 |
| `goldentest.go` | 样例的结构、读取、回放 |

位置与设计稿原文（`sdk/pluginsdk/standard/testdata/golden`）差一层目录，原因：样例要被别的模块（`plugins/*`）的测试读到，只能由一个非测试包内嵌后导出，而 `go:embed` 只能内嵌本包目录之下的文件；放进 `standard` 包本身则会把 1.2 MB 的样例链接进每个插件的二进制。

## 样例的结构

一个文件：

```json
{
  "plugin": "openai",
  "account_type": "apikey",
  "pass_headers": ["openai-organization", "..."],
  "cases": [ ... ]
}
```

`pass_headers` 是这个账号类型能收到的客户端请求头（清单的 `passHeaders`，没写就是平台的）。样例里的客户端请求头故意比它多（`authorization`、`cookie`、`x-api-key` 等），多出来的不允许出现在上游请求里。

一条样例：

| 字段 | 含义 |
|---|---|
| `name` | 文件内唯一（`execute` 样例的名字以 `build` 开头，沿用录制时的名字） |
| `kind` | `execute`、`test`、`models`、`validate`、`classify` |
| `now` | 只有 `classify` 有：分类必须使用的时钟（RFC 3339） |
| `input` | 输入，见下 |
| `want` | 期望的回答；与 `error` 恰好有一个 |
| `error` | 期望失败：`message` 参与比较，`code` 只作记录不比较 |
| `note` | 只在期望值不是当年录下的回答时出现，说明原因（共 88 条，见“与录制不同的样例”） |

### `execute`：一次网关请求的上游请求

插件拿到的是完整请求体，交出的是最终字节。

`input`：

```json
{
  "account": {"id": 42, "type": "apikey", "credentials_json": "{...}", "settings_json": "{...}"},
  "meta": {"protocol": "openai.chat", "model": "golden-model", "stream": true},
  "client_headers": {"user-agent": "..."},
  "body": "{\"model\":\"field-model\",\"stream\":true}"
}
```

- `account` 缺失就是“没有账号”的样例；`type` 为空表示核心没带类型。
- `meta` 只有样例矩阵变化的三项：上游协议、模型、是否流式。
- `body` 是完整请求体的原文（字符串，逐字节）。

`want`：

```json
{
  "method": "POST",
  "url": "https://upstream.example.com/v1/chat/completions",
  "headers": {"authorization": "Bearer sk-golden-key-0123456789"},
  "default_headers": {"content-type": "application/json", "user-agent": "..."},
  "body": "{\"model\":\"field-model\",\"stream\":true,\"stream_options\":{\"include_usage\":true}}",
  "upstream_model": "field-model"
}
```

- `headers` 是**权威头**（写在运营方的请求头设置之后）：只有携带凭证的那一个头（openai 是 `authorization`，anthropic 和 relay 是 `x-api-key`，gemini 是 `x-goog-api-key`）。
- `default_headers` 是**默认头**（写在运营方设置之前，运营方能改）：其余全部，包括转发的客户端请求头。划分见设计稿 4.5 节。
- `body` 是最终请求体的原文，**逐字节比较**。WebSocket 路由（`openai.responses_ws`）上它是这一轮的消息，方法是 `GET`，地址是 `ws(s)://`，没有 `content-type`。
- 请求头的名字不区分大小写；同一个名字以不同大小写出现两次算错。

### 请求体是怎么合成的

录制时只有核心从请求体里读出的字段（四个插件都只有 `model`），请求体按下面两条合成“核心能读出这份输入的最小请求体”：

1. 录下的每个字段写到它的路径上：`{"model":"field-model"}`；字段值原样保留（样例里有 `123`、`""`、`"models/field-model"`）；没有字段就是 `{}`。
2. `meta.stream` 为真、并且 `meta.protocol` 对应的内置端点声明了 `streamPath` 时，在那个路径上写 `true`（核心就是从这个标志得到 `meta.stream` 的）。实际涉及 `openai.chat`、`openai.responses`、`anthropic.messages` 三个协议，路径都是 `stream`。

然后把录下的补丁按顺序应用上去，得到期望的最终请求体。四个文件里出现过的补丁只有一种：`openai.chat` 流式时 `stream_options.include_usage = true`（28 条）。其余样例的期望请求体与输入逐字节相同，也就是插件必须原样交回。

两点要知道：

- 请求体里的 `model` 与 `meta.model` 故意不同（`field-model` 对 `golden-model`），样例借此固定“请求体里的模型优先，gemini 的路由用 `meta` 的”。真实请求里两者相同。
- 协议为空（走默认协议）且 `meta.stream` 为真的 openai 样例有 14 条：请求体里没有 `stream` 标志（规则 2 查不到协议），期望里仍有 `stream_options`。它们固定的是“流式补丁看的是 `meta.stream`，不是请求体”。

### 其余四类

`test`、`models`、`validate`、`classify` 的 `input` 和 `want` 就是请求、应答消息的 proto JSON（proto 字段名）。比较时把实现的回答编码成同样的 JSON 再逐字节比。

### 错误

`error.message` 是去掉插件自己包装之后的消息：开头的 `account N: ` 和嵌套的 `rpc error: code = X desc = ` 两侧都先去掉再比（`Unwrap`）。`error.code` 是录制时插件回答的 gRPC 码，不比较：核心对这些调用的失败一视同仁，而且今天的码已经与录制的不同（anthropic 插件一律回 `FailedPrecondition`，公共的账号检查回 `InvalidArgument`）。

## 与录制不同的样例（88 条）

1498 条全部转换，没有跳过。其中 88 条的期望值是**今天的行为**而不是当年录下的回答，每条带 `note`：

| 条数 | 哪些 | 原因 |
|---|---|---|
| 40 | relay 成功的 `test` | 录制时 `model`、`usage_protocol` 是空的；现在 relay 的账号测试与其他类型一样带上这两项 |
| 9 | 账号类型不是本插件类型的调用：openai 的 `models` 1 条；gemini 的 `execute` 3 条、`test` 4 条、`models` 1 条 | 录制时这两个插件对别的类型的账号照常作答；现在一律拒绝，消息是 `unsupported account type "oauth"`，与 anthropic、relay 相同。其余 23 条类型不符的样例按录制保留，这 9 条写成现在的回答。核心不会把别的类型的账号交给插件，这类输入在线上不出现 |
| 20 | 四个文件各 5 条 `validate` 的密钥用例：`inner space key`、`inner tab key`、`too short key`，以及 openai 的 `nine character key`、`wrong prefix key`，其余三个文件的 `too long key`、`non-ascii key` | 密钥只要求是非空字符串，保存去首尾空白后的值（`docs/DESIGN-2026-11-20-KEY-AND-BASE-URL-RULES.md` 5.1）。录制时它们被拒绝 |
| 2 | anthropic、relay 的 `validate non-string key` | 消息改成“必须是字符串”，不再提已删掉的 8-512 规则（同上 5.4） |
| 17 | `validate` 的 `base_url settings v1`、`base_url credentials`、`base_url both`、`base_url port prefix`（四个文件），以及 gemini 的 `base_url v1beta` | 基础地址按用户填写的保存，不再去掉版本段；请求时才去掉至多一段（同上 5.2、5.5）。同一设计下 `execute`、`test`、`models` 样例的请求地址都不变 |

## 怎么用

```go
import "github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/standard/goldentest"

func TestGolden(t *testing.T) {
	p := openai.New() // 被测的实现
	goldentest.Run(t, "openai", goldentest.Subject{
		Execute: func(ctx context.Context, in *goldentest.ExecuteInput) (*goldentest.UpstreamRequest, error) {
			// 用 in.Account.Proto()、in.Meta.Proto()、in.ClientHeaders、[]byte(in.Body)
			// 调插件的 Execute（假宿主），把它交给中继的请求填进 UpstreamRequest。
		},
		Test:     func(ctx context.Context, in *pluginv1.BuildTestRequestRequest) (*pluginv1.BuildTestRequestResponse, error) { ... },
		Models:   func(ctx context.Context, in *pluginv1.BuildModelsRequestRequest) (*pluginv1.BuildModelsRequestResponse, error) { ... },
		Validate: func(ctx context.Context, in *pluginv1.ValidateCredentialsRequest) (*pluginv1.ValidateCredentialsResponse, error) { ... },
		Classify: func(ctx context.Context, now time.Time, in *pluginv1.ClassifyErrorRequest) (*pluginv1.ClassifyErrorResponse, error) { ... },
	})
}
```

接口：

```go
func Run(t *testing.T, plugin string, s Subject, opts ...Option) // 回放一个文件的全部样例，逐条报告差异
func Except(name, reason string) Option                          // 声明一条已知的差异
func Select(keep func(*Case) bool) Option                         // 只回放 keep 接受的用例（几个 Select 取交集）
func Forwarded(accountType string, protocols ...string) func(*Case) bool // 这些协议上、该类型且自带基础地址的 execute 用例
func (c *Case) BaseURL() (string, bool)                          // execute 用例的账号自带的 base_url
func Load(plugin string) (*File, error)                          // 只读样例，自己遍历
func Plugins() []string
func (c *Case) Check(ctx context.Context, s Subject) []string    // 回放一条，返回差异
func (c *Case) Execute() (*ExecuteInput, *UpstreamRequest, error)
func Unwrap(message string) string
```

规则：

- `Subject` 的五个函数都要给。缺一个，那一类的每条样例都报错，不会悄悄跳过。
- `Classify` 收到样例的时钟 `now`，实现必须用它而不是系统时间（冷却到期时间在期望值里）。
- `Execute` 的适配器负责像核心那样过滤客户端请求头：只把 `client_headers` 里 `pass_headers`（`File.PassHeaders`）列出的交给被测实现。线上是核心按清单的 `passHeaders` 过滤之后才调插件，插件不再筛；样例故意多给的头（`authorization`、`cookie` 等）因此由适配器挡掉，不出现在上游请求里仍然是被比较的结果。`standardtest.Run(t, plugin, manifestPath, p)` 就是这样做的，并且先核对插件清单（`manifestPath`）交给文件账号类型的客户端请求头与 `pass_headers` 完全一致，清单改了而样例没跟上时直接失败。
- `Except(name, reason)` 声明的样例必须**真的不同**：声明了却与样例一致、或者名字不存在（或没被选中），测试失败。这样例外清单不会比它的理由活得久。
- `Select` 给不是这个文件的插件、但也服务这些协议的实现用（`standard.Merge` 出的中转站类型）：`Select(Forwarded("apikey", 协议...))` 只回放这些协议上、账号自带基础地址（`Case.BaseURL`：设置里先、凭证里后，非空字符串）的 `execute` 用例，不回放凭证规则、默认地址、默认协议、别的账号类型这些本来就该不同的用例。一个用例都没选中时测试失败。样例本身不变。
