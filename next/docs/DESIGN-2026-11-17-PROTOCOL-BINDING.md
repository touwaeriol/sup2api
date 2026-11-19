# 账号协议绑定、模型协议限定、显式协议转换

2026-11-17，用户要求："在账号上可以进行协议绑定；某个模型只在某个协议上提供服务；协议转换默认不支持，需要显式开启"。补充口径（同日）：

- "OpenAI 的 Chat 转 Responses 这种不算协议转换，协议转换一般是跨服务的"。
- "模型默认是不支持协议转换的，默认是客户什么协议访问什么模型，我们就转发"。
- 配置方式是"选中模型，指定协议"。

状态：**已实现**（2026-11-17，分支 `feat/protocol-binding`；2026-11-18 合并进 `feat/forwarding-v2`（合并提交 `6bef447cf`），随转发 V2 一起发布，未发布）。契约在 CONTRACTS §110，迁移 0106（`0106_account_protocols.sql`），升级手册 `next/deploy/rollback/protocol-binding-release.md`。实现与本文的差异见 §10“实现记录”；本文 §1–§9 保留定稿时的原样，两者不一致时以 CONTRACTS §110 和代码为准。

本文修订 CONVERT-MATRIX 的 D3（2026-11-09 定的"所有账号类型自然接受转换，没有任何退出机制"）和风险第 11 条（"账号级关协议不在本期"）。

## 1. 现状

### 1.1 new-api 的做法（只读调研，分支 develop）

- **转换默认隐式开启**。选渠道只按分组 + 模型（`model/ability.go` 的 Ability 表没有协议维度），选中后由渠道适配器的 `Convert*Request` 自动转成渠道原生格式。
- **转不了的组合到转换阶段才报错，且不换渠道重试**（`relay/convert_request_error.go`，SkipRetry）。例如 `/v1/messages` 可以选中 Codex 渠道，然后直接失败。
- **没有"渠道只接受某协议"的设置**。相关开关只有：透传（不转换、原样发）、Chat → Responses 策略（全局，可按渠道和模型正则限定）、`tool_loss_policy`（只拦有损转换）。
- **唯一的显式绑定在 AdvancedCustom 渠道**：每条路由配 `incoming_path` + `models` + `converter`，选渠道时就按入站路径和模型过滤，`converter` 为空表示不转换。本设计的形态与它最接近，但做成所有账号通用。
- **"模型支持的端点类型"只用于展示**（价格页、`/v1/models`、渠道测试），请求时不校验。

### 1.2 sup2api 现状

- 协议 id 是 `<平台>.<名称>`（`openai.chat`、`openai.responses`、`openai.responses_ws`、`openai.embeddings`、`anthropic.messages`、`anthropic.count_tokens`、`gemini.generate`、`gemini.stream_generate`、`gemini.count_tokens`，以及插件自己声明的，如 `volcengine.images`）。入站协议就是命中端点的 `ep.Protocol`。
- 账号能服务哪些协议**只由账号类型决定**（manifest 的 `platforms[]` 和每个平台的端点子集 `endpoints[]`）。账号、分组上没有协议字段。
- `planRoutes`（`gateway/routing.go`）对每个账号类型给出一条路由，共三种：
  1. **原生**：类型声明了入站端点的平台，且子集含该端点。
  2. **同平台适配**：类型声明了该平台，但子集不含该端点，转到同平台子集内的另一个端点（codex 账号服务 `openai.chat` 就是这样转到 `openai.responses`，CONTRACTS §83.3）。
  3. **跨平台转换**：类型没有声明该平台，转到它其他平台的端点（OpenAI 账号服务 `/v1/messages`）。
- 三种路由对所有账号、所有模型**一律开启**，没有任何一级的开关。账号/分组上的"丢失策略"（allow / safe / strict）只管转换丢字段能不能接受。
- 账号的 `models`（白名单）、`model_mapping`、`model_limits` 都只看模型，不分协议。
- "OpenAI 生图"目前不是端点：`/v1/images/generations` 返回 404，`openai.images` 只在 DESIGN-2026-11-17-OPENAI-IMAGE-BILLING 里提出，尚未实现。

## 2. 定义

| 名称 | 定义 | 例子 |
|---|---|---|
| 本平台协议 | 入站端点所属的平台，是账号类型声明过的平台。包括原生和同平台适配两种路由 | OpenAI 账号的 chat、responses；codex 账号的 chat（适配到 responses） |
| 跨平台协议转换（下称"转换"） | 入站端点所属的平台，账号类型**没有声明** | OpenAI 账号服务 `anthropic.messages`、`gemini.generate`；Claude 账号服务 `openai.chat` |

- 判定只看账号类型的声明，与具体请求无关。代码里就是 `planRoutes` 中 `b.Supports(p)` 的结果，不新增分类表。
- volcengine 这类同时声明 openai、anthropic、volcengine 三个平台的类型，这三家的协议都算本平台协议；Gemini 请求打到它才算转换。
- 同平台适配不算转换，默认照常工作，但可以用协议绑定关掉（§3.1）。

## 3. 设计

默认行为：**客户用什么协议访问什么模型，就按这个协议转发**，只走本平台协议；任何模型都不做跨平台转换。要转换，必须在账号上选中模型、勾上转换协议。

账号上加两个字段。

### 3.1 协议绑定：`protocols`

账号对哪些**本平台协议**提供服务。

- `null`（默认）：账号类型能服务的本平台协议全部开放。旧账号行为不变。
- 数组：只开放列出的协议。可以是空数组，表示本平台协议全部关闭（此时 `model_protocols` 里必须至少开了一个转换协议，否则保存报错"账号没有开放任何协议"；要停用账号用"可调度"开关）。
- 用途：只有 Chat 没有 Responses 的 OpenAI 兼容站点，只勾 `openai.chat`；不想让 codex 账号接 chat 请求，去掉 `openai.chat`；关掉 WebSocket，去掉 `openai.responses_ws`。
- 指定后，账号类型以后新增的端点不会自动开放（界面提示"有 N 个协议未开放"）。

### 3.2 模型的协议限定与显式转换：`model_protocols`

选中模型，指定它在哪些入站协议下提供。转换也在这里开：给模型勾上跨平台协议，才对这个模型做转换。

```json
[
  {"models": ["claude-sonnet-4-5"],      "protocols": ["anthropic.messages"]},
  {"models": ["gpt-5.6"],                "protocols": ["openai.chat", "openai.responses", "anthropic.messages"]},
  {"models": ["gpt-image-1"],            "protocols": ["openai.images"]},
  {"models": ["text-embedding-3-large"], "protocols": ["openai.embeddings"]},
  {"models": ["*"],                      "protocols": ["openai.chat", "openai.responses"]}
]
```

（上例是一个 OpenAI 账号：`claude-sonnet-4-5` 是经模型映射指到 GPT 的名字，只在 Messages 协议下经转换提供；`gpt-5.6` 原生加 Messages 转换；生图、向量模型各只走自己的协议；其余模型只走 chat 和 responses。）

- **被列到的模型**：只在这条列出的协议下提供。协议可以是本平台的，也可以是跨平台的。
- **没被列到的模型**：在账号开放的全部**本平台协议**下提供，**不做转换**。默认 `[]`，即所有模型都是这样。
- `"*"` 是唯一的通配，表示"其余模型"，只能单独成一条。用它可以表达"某协议只提供某些模型"（上例中 `openai.images` 只提供 `gpt-image-1`），也可以给全部模型开转换（不建议，见 §6 第 3 条）。
- 模型按**映射前的客户端模型**匹配，协议按**入站协议**匹配（与 `models`、`model_limits`、计费一致）。一个模型只能出现在一条里。
- 本平台协议实际生效的是它与 `protocols` 的交集；列了账号没开放的本平台协议不报错，界面给警告。
- 丢失策略不变：模型开了转换之后，照旧按账号 > 分组 > allow 判定有损转换。

### 3.3 count_tokens

`anthropic.count_tokens`、`gemini.count_tokens` 不单独列，跟随同族的生成协议（`anthropic.messages`、`gemini.generate`）：该模型在生成协议下提供，计数就提供（原生转发或本地估算，规则同 CONVERT-MATRIX §8）。

### 3.4 生效位置

| 环节 | 改动 |
|---|---|
| `planRoutes`（`gateway/routing.go`） | 路由上记下 `cross`（是否跨平台），不改选路逻辑 |
| 调度快照 `core.AccountRef` | 加 `Protocols`、`ModelProtocols`（`account/directory.go` 的 `readGroupSnap` 加列），一个判定方法"模型在该协议下是否提供" |
| `ineligibleReason`（`gateway/sched_trace.go`） | 取到路由后调用上面的方法；引用模型同样判断 |
| 调度记录 | 三个新跳过原因（见下），使用记录详情里能看到账号为什么没被选 |

判定顺序与跳过原因：

1. 模型命中 `model_protocols` 的某条（精确，其次 `"*"`）：入站协议不在这条里 → `model_protocol_closed`。
2. 模型没命中任何一条，且路由是跨平台的 → `conversion_not_enabled`。
3. 路由是本平台的，`protocols` 非 null 且不含入站协议 → `protocol_closed`。

- 只做内存比较，在可用性预检、粘性、转换、取并发槽之前。被过滤的账号不转换请求、不下载媒体、不计尝试次数。
- HTTP、WebSocket、粘性绑定走同一个入口，不需要分别处理。
- **错误**：组内候选账号全部因这三个原因被过滤 → 404 `no account in this group serves this model on this endpoint`（客户端不会当成过载重试）。只要有一个账号是因别的原因（冷却、限流、忙）没选上，就按现有规则返回那个原因。
- 优先级、等级排序、丢失策略、粘性、故障策略都不变。

### 3.5 性能

现有热路径：分组快照缓存在每个节点的内存里（30 秒，账号变更经 `account:changed` 立即失效，未命中时 singleflight 读一次库）；每个请求从快照取候选、用一次 Redis 管道查冷却，再对每个候选跑 `ineligibleReason`。本设计不给这条路径加任何数据库或 Redis 访问，约束如下：

1. **解析只在建快照时做一次**。`readGroupSnap` 多读两列；`model_protocols` 为 `[]` 时在 SQL 里取成 NULL，不传输、不解析。绝大多数账号走这条路。
2. **编译成只读索引**。建快照时把两个字段编译成一个不可变结构，`AccountRef` 上只多一个指针（没有配置的账号为 nil）：
   - `protocols`：协议数量是个位数，存小切片，顺序比较；
   - `model_protocols`：`map[模型] → 协议切片`，加一个"其余模型"的协议切片。
   每个请求按值拷贝 `AccountRef` 时只多拷 8 字节，索引本身在快照内共享、不拷贝。
3. **每个候选的判断是 O(1)**：nil 判断 → 一次 map 查找 → 在个位数的协议切片里比较。不随规则条数或模型数增长。没有配置且路由是本平台的账号，只有一次 nil 判断。
4. **跨平台请求比现在便宜**。现在跨平台的候选要走到挑选阶段才转换请求体（多 MB 图片时很重，转 Gemini 还要下载媒体）；改后没开转换的账号在转换之前就被内存比较过滤掉。全部被过滤时的 404 直接由跳过原因得出，不查库。
5. **内存上限**：单账号最多 50 条 × 500 个模型，索引最多 2.5 万项；账号在多个分组时每个分组快照各持一份（与现有 `models`、`model_mapping` 相同）。

验收：按 `store/gormtest/BASELINE.md` 的命令前后各跑一次 `HotGroupSnapshot`，无配置时 allocs/op 不增加；新增一个 `ineligibleReason` 的基准（500 个候选，分别为无配置、有 50 条规则），有规则时每候选不分配内存。

## 4. 存储与接口

迁移 0106（`0106_account_protocols.sql`）：

```sql
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS protocols       text[];                      -- NULL = 本平台全部
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS model_protocols jsonb NOT NULL DEFAULT '[]';
```

不回填。升级后所有账号都不做跨平台转换（§6 第 1 条）。

| 接口 | 字段 |
|---|---|
| `GET/POST/PATCH /accounts`、`/accounts/:id` | `protocols`: `string[] \| null`；`model_protocols`: `[{models, protocols}]`。PATCH 整体替换 |
| `GET /accounts/:id` | 另返回只读的 `served_protocols`: `[{protocol, via: native\|adapted\|converted, grade, hops, models}]`，即账号实际开放的协议；`models` 为 `null` 表示不限模型，界面不再自己推算 |
| `GET /groups/:id` | 另返回只读的 `served_protocols`: `[{protocol, accounts}]`，成员账号开放协议的并集 |
| 批量编辑 | 加"本平台协议""模型的协议限定"两项（替换） |

校验（字段错误定位到对应字段）：

- `protocols[i]`：必须是该账号类型的本平台协议且能服务（原生或同平台适配）；不收 count_tokens。
- `model_protocols`：最多 50 条、每条最多 500 个模型；模型重复报 `duplicate`；`"*"` 只能单独成条且最多一条；`protocols` 不能为空。
- `model_protocols[i].protocols[j]`：必须是该账号类型能服务的协议（原生、同平台适配或经转换）；不收 count_tokens；跨平台的不收 WebSocket、异步任务提交（这些只走原生）。
- `protocols` 是空数组且 `model_protocols` 里没有任何跨平台协议：`protocols: no_protocol`。

权限同账号其他字段。变更经现有的 `account:changed` 广播失效各节点快照。

## 5. 控制台

可点击的示意图：[mockups/protocol-binding.html](mockups/protocol-binding.html)（账号编辑器三种账号类型、账号列表、分组详情、使用记录；数据为虚构）。

### 5.1 账号编辑器

"模型配置"分区改名为"**协议与模型**"，卡片顺序：**本平台协议**（新）→ 模型列表 → 模型映射 → **模型的协议限定**（新）。理由：先定账号开放哪些协议，再定模型，最后选中模型指定协议。

**卡片一：本平台协议**

```
┌ 本平台协议（OpenAI）──────────────────────── (●) 全部   ( ) 指定 ┐
│   ☑ Chat Completions    POST /v1/chat/completions     原生        │
│   ☑ Responses           POST /v1/responses            原生        │
│   ☑ Responses WebSocket GET /v1/responses             原生        │
│   ☑ Embeddings          POST /v1/embeddings           原生        │
│                                                                   │
│ ⓘ 其他平台的协议（Anthropic、Gemini）默认不提供。需要协议转换时，    │
│   在下面"模型的协议限定"里选中模型、勾上对应协议。                   │
└───────────────────────────────────────────────────────────────────┘
```

- 选"全部"时复选框全勾且置灰（对应 `null`）；选"指定"后可逐个取消。
- codex 账号这里的 Chat Completions 行徽标显示"适配 → responses"。
- 左侧导航摘要：`协议 4 · 限定 3 个模型 · 转换 2 个模型`。

**卡片二：模型的协议限定**

列 = 卡片一开放的本平台协议 + 该账号类型经转换能到达的协议（列头带"转换"徽标和等级）。行 = 模型。两种视图，按模型列表自动选择，可手动切换，也可切到 JSON。

- **矩阵视图**（账号模型列表非空时默认）：

```
┌ 模型的协议限定 ─────────────────────────────────── [矩阵 | 列表 | JSON] ┐
│                     ├──── 本平台 ────────────┤├───── 转换 ──────────┤  │
│ 模型 \ 协议          Chat  Responses  Embed.   Messages·一般  Gemini·一般 │
│ claude-sonnet-4-5    ☐      ☐         ☐          ☑             ☐       │
│ gpt-5.6              ☑      ☑         ☐          ☑             ☐       │
│ text-embedding-3-l.  ☐      ☐         ☑          ☐             ☐       │
│ gpt-5.6-mini         ▣      ▣         ▣          ☐             ☐  默认 │
│ 其余模型              ▣      ▣         ▣          ☐             ☐  默认 │
│                                                                        │
│ ▣ = 默认（不限定，按客户的协议原样转发，不转换）  [全部恢复默认]         │
└────────────────────────────────────────────────────────────────────────┘
```

  - 行来自账号的模型列表，末尾固定一行"其余模型"（对应 `"*"`）。
  - 每行初始是"默认"：本平台列灰色勾选，转换列不勾。点任一格后该行变成显式限定；行尾有"恢复默认"。
  - 可以多选几行后一次指定协议（"选中模型，指定协议"）。
  - 勾转换列时，等级为"不推荐"（两跳）的提示一句；给"其余模型"行勾转换时提示"所有未单独列出的模型都会接受转换"。
  - 保存时把勾选相同的行合并成一条 `model_protocols`，默认行不写。复选矩阵的交互参考 `PermissionMatrix.vue`。
- **列表视图**（账号模型列表为空 = 全部模型时默认；或模型很多时）：照搬"指定模型限速"的卡片式（`ModelLimitsEditor.vue`），每张卡 = 一组模型（`ModelTagSelect`）+ 一组协议（`StickyProtocolPicker`，候选按本平台 / 转换分组），外加一张可选的"其余模型"卡。
- 某条里的本平台协议账号没有开放时，在该处显示警告"账号未开放该协议，这一项不生效"。
- Gemini 的 `generate` 和 `stream_generate` 在界面上合成一列，一起勾选（存储仍是两个协议 id）。

**"高级 → 请求转发"**：现有"协议转换丢失策略"一行保留原位；账号没有任何模型开转换、也没有同平台适配时，下拉置灰并提示"未开启协议转换"。

### 5.2 其他页面

- **账号列表**：不加列。名称下方已有的平台图标改为按账号实际开放的协议显示：本平台图标照旧，有模型开了转换的平台图标带"转"角标，悬停列出协议和模型数；本平台协议被限定时图标旁加"部分"标记。高级筛选加"开放协议"。
- **账号详情**："可服务的端点"按 `served_protocols` 显示，转换项列出开了转换的模型；账号类型能服务但账号没开放的，置灰标"未开放"。
- **分组详情**："可调用端点"改用接口返回的 `served_protocols`，每个协议显示有几个成员账号提供。删掉前端按"所有类型都能转换"推算的 `convertedEndpoints.ts`。
- **新建账号的类型选择**：端点列表里跨平台的项加说明"需在账号上为模型开启"。
- **使用记录详情**：调度记录里出现新的跳过原因"协议未开放""模型未开启协议转换""模型在该协议下不提供"。
- i18n zh/en；`web/src/api/types.ts` 加字段。

## 6. 已知影响与注意事项

1. **升级后存量转换流量直接 404**（用户 2026-11-17 定：存量默认 404 即可）。字段在升级后才存在，没法提前配好，所以从升级完成到配好之间，依赖转换的客户端会 404。发布手册里放一条只读查询：列出近 30 天实际服务过跨平台转换的账号、入站协议和模型，升级前导出，升级后照着在批量编辑里配。codex 账号接 chat 请求属于同平台适配，不受影响。
2. **模型列表接口不分协议**。`/v1/models` 和分组的模型列表仍按分组和账号的模型返回，客户端可能看到一个在当前协议下调不通（404）的模型。
3. **给"其余模型"开转换要谨慎**。账号模型列表为空（= 全部模型）时，Anthropic 客户端发来的 `claude-*` 模型名会原样（或经映射）发给 OpenAI 上游，上游报模型不存在，按故障策略可能冷却账号。建议转换只开给明确列出的、有映射的模型。
4. **粘性会话**：升级后，已绑定到某账号的转换会话因该账号不再提供转换而失效，按现有"绑定账号不可用"的规则处理。
5. **分组展示变少**：分组详情的"可调用端点"不再包含"经转换也可调用"的部分，直到成员账号里有模型开了转换。

## 7. 不做

- **分组级、全局级的转换开关**：只在账号上设，批量编辑用于成批配置。
- **插件通过通用配置项声明默认值或锁定**（`commonControls`）：等有插件需要时再加。
- **`/v1/models` 按入站协议过滤模型**（§6 第 2 条）。
- **生图协议本身**：`openai.images` 端点由生图计费那份设计落地；落地后它自动成为 OpenAI 账号的一个本平台协议，`gpt-image-*` 只走生图协议用 §3.2 配置，本设计不需要再改。
- 协议 id 不支持通配（`openai.*`）。

## 8. 已定决策（用户 2026-11-17）

| # | 事项 | 决定 |
|---|---|---|
| 1 | 存量账号 | 不回填，升级后跨平台转换默认 404 |
| 2 | 配置方向 | 选中模型，指定协议 |
| 3 | 全部未开放时的错误 | 404；模型没开转换、模型在该协议下不提供也一样（§3.4） |
| 4 | 账号类型新增端点 | 已"指定"本平台协议的账号不自动开放；转换默认不开放 |
| 5 | 模型默认行为 | 不判断模型支持哪种协议：客户用什么协议访问就按什么协议原样转发，不转换；只有配置了绑定或转换才生效 |
| 6 | 转换开在哪 | 只在模型上开，账号上没有单独的转换开关（§3.2） |
| 7 | 同平台适配（Chat ↔ Responses） | 不算协议转换，默认对所有模型生效 |

## 9. 任务拆分（定稿后细化）

| # | 任务 | 范围 |
|---|---|---|
| T1 | 迁移 0106、模型生成、账号输入/输出/校验/审计字段、`AccountRef` 两个字段与判定方法、`served_protocols` | server（account、core） |
| T2 | 路由记 `cross`、`ineligibleReason` 判断、三个跳过原因、全部未开放时的 404、count_tokens 跟随 | server（gateway） |
| T3 | 分组 `served_protocols`、批量编辑 | server（group、account） |
| T4 | 控制台：两张卡片、分区改名与摘要、错误定位、列表图标、详情、分组详情、筛选、i18n、类型、测试 | web |
| T5 | 发布手册：升级前的存量转换查询 | deploy |
| T6 | CONTRACTS 新一节、CONVERT-MATRIX D3 与风险 11 的修订说明、CHANGELOG | docs |

## 10. 实现记录（2026-11-17）

| 项 | 实际 |
|---|---|
| 迁移 | 0106，`server/internal/migrations/0106_account_protocols.sql`（与暂记的号相同） |
| 契约 | CONTRACTS §110（与暂记的号相同）；§11、§12、§13、§18.2、§23.3、§24.6、§83.3、§86、§88、§92.4、§92.5 加了“§110 起”的交叉引用 |
| 其他文档 | CONVERT-MATRIX §0、§5.1、§12 风险 1 与 11、§13 D3 的修订说明；ARCHITECTURE 6.2、6.4 的“协议转换”一行、6.6“请求如何调度”；PLUGIN-DEVELOPMENT 2.3 加一行、第 3、4 节；PLUGIN-CLOUD-PROVIDERS、UPSTREAM-DECLARATIVE 各一处；CHANGELOG“未发布” |
| 升级手册 | `next/deploy/rollback/protocol-binding-release.md` 和只读查询 `protocol-binding-conversions.sql`（T5）；`next/deploy/README.md`、`next/deploy/gateway/README.md` 各加一处指引 |
| 代码提交 | `1c00559be` 迁移与模型、`f06ae4677` core、`fcd78d84d` account、`37e352336` group 与 app、`c929d188e` gateway、`e7e8dfb2f` `bc55f7a3c` `54828f147` `ba5b06891` `6247ec118` web |
| 没有改动的 | 节点外壳（`next/gateway`）、SDK、proto、插件、`@sub2api/ui` 与 `@sub2api/host` 的公开接口 |

### 10.1 与本文的差异

**存储、core、账号接口**

1. §3.4 的表格写 `AccountRef` 加 `Protocols`、`ModelProtocols` 两个字段，§3.5 写只多一个指针。按 §3.5 实现：一个指针字段 `ProtocolRules`，判定方法是 `(*core.ProtocolRules).Serves(model, protocol, cross)`。
2. §3.2 的例子里有 `{"models": ["gpt-image-1"], "protocols": ["openai.images"]}`。`openai.images` 端点还没有实现（§7），现在这样写保存时会被拒（`unsupported`）；契约里的例子去掉了这一条。
3. §4 没有规定校验的错误码，实现定为：`protocols[i]` 用 `invalid`（count_tokens）、`unsupported`（类型不能服务）、`cross_platform`（其他平台的协议）；`model_protocols` 用 `too_many`、`required`、`invalid`、`duplicate`、`unsupported`；另有 `protocols: no_protocol`。没有模型的条目报 `required`，不像 `model_limits` 那样静默丢弃。
4. 账号类型的插件未启用时，PATCH 带这两个字段返回 503 `plugin_unavailable`（不知道该类型能服务哪些协议）。本文没写这种情况。
5. `served_protocols`（§4）的细节本文没定：`models` 为 `null` 的精确含义是“`model_protocols` 没有另行限定的所有模型”，表达不了排除；`via` 只有三种值，count_tokens 本地估算时类型声明了该平台记 `adapted`、没声明记 `converted`，`grade` 仍是 `local`；POST、PATCH 的返回也带它。
6. §4 的“批量编辑”在服务端没有对应的接口：批量编辑一直是控制台对每个账号调 `PATCH /accounts/:id`（CONTRACTS §23.3），服务端没有新增接口。
7. 异步任务的查询端点（如 `volcengine.video_query`）不经过调度，协议绑定对它不生效，但校验和 `served_protocols` 把它当普通本平台协议：账号“指定”协议时没勾它，界面显示未开放，实际查询仍可用。

**网关**

8. §3.4 写“只要有一个账号是因别的原因（冷却、限流、忙）没选上，就按现有规则返回”，§3.5 第 4 条又写“404 直接由跳过原因得出，不查库”。冷却中的账号不在候选里，所以第一版实现是读一次组内未提供的账号（查库），有冷却中或限时停用的就不返回 404，不看那个账号自己的协议绑定；审查指出这样在有冷却的号池里 404 基本落空（没开转换的账号一冷却，请求就变成可重试的 503/529），且那次查库只读前 200 个账号。**审查后改为**（10.3 第 1 条）：账号模块加 `AccountDirectory.Resting`，从分组快照里给出冷却中和限时停用未到期的账号，网关对它们跑同一个 `ineligibleReason`；其中有一个不是被协议绑定过滤的才不返回 404。这次读取只用快照和一次 Redis 管道，不查库，两条都满足；读取失败时不返回 404。
9. 永久停用、关闭调度的账号不算“会回来”，不妨碍 404。现有的“端点无账号”404（`GroupHasType`）把停用账号也算在内，两处口径不同；按本文列举的“冷却、限流、忙”实现。
10. 协议判断排在模型白名单之前（本文只说“取到路由后调用”）。带模型白名单的 OpenAI 账号收到 `/v1/messages`，得到 `conversion_not_enabled` 和 404，而不是 `model_not_served` 和 503。
11. WebSocket 已建立的连接上，后续轮次换成该账号在这个协议下不提供的模型，返回现有的 400（要新建连接），不是 404。本文没写这种情况。
12. `protocol_closed` 的详情填入站协议；count_tokens 请求填的是计数协议本身，实际被关的是它跟随的生成协议。
13. §3.4 说 HTTP、WebSocket、粘性“走同一个入口，不需要分别处理”，改动前并非如此（WebSocket 首轮先用 `servesAllModels` 判断）。实现时把 `servesAllModels` 删掉，`candidateEligible` 改为调用 `ineligibleReason`，现在才是一个入口。这改动了现有代码，对没有协议配置的账号行为不变。
14. §3.5 的验收：
    - “按 BASELINE.md 的命令跑 `HotGroupSnapshot`”跑不了：这个基准已在 `fcc847b62` 删除，BASELINE.md 现在没有这条路径的基线。实现时按旧基准临时重写测了前后（10 个无配置账号，424 → 399 allocs/op），基准文件没有提交，BASELINE.md 没有改。为了多读两列不增加分配，`readGroupSnap` 把逐行的扫描临时变量改成整次读取共用一份。
    - “有规则时每候选不分配内存”：`Serves` 本身是 0 次分配（有测试断言）；但 `ineligibleReason` 里原有的用量检查对每个能服务的候选分配 3 次、约 1 KB，所以基准里能服务的情况是 1500 allocs/op（500 个候选），有无规则相同。被协议绑定过滤的账号是 0。
15. 原有的网关测试里有 17 个依赖默认转换（16 个失败、1 个空过），夹具改成显式开启转换，断言没有改。

**控制台**

16. 列表视图和矩阵多选栏的协议选择用的是 `SChip` 的开关（示意图的交互），没有用 §5.1 提到的 `StickyProtocolPicker`（它是“已选标签 + 下拉添加”）。
17. 示意图里保存按钮置灰、底部显示“有 n 处需要修正”；实现按编辑器现有做法：按钮不置灰，点保存时不发请求，跳到“协议与模型”分区显示错误。
18. 矩阵里显式勾选的转换格没有用琥珀色区分，靠“协议转换”列分组表头和等级徽标区分；图例简化成两句文字。
19. 导航摘要是 `协议 4/4 · 限定 n 个模型 · 转换 n 个`（同示意图），不是 §5.1 写的 `协议 4 · 限定 3 个模型 · 转换 2 个模型`；“限定 n 个模型”把“其余模型”也算一个；后面保留原有的“n 个模型 · n 条映射”。
20. 分组详情（§5.2）：接口只有 `{protocol, accounts}`，做不出示意图里的“原生 / 经转换”分行、“限 1 个模型”和“count_tokens 跟随 Messages”。现在按平台列端点和账号数，底部一行说明“没有列出的端点请求返回 404”。
21. §5.2 的“高级筛选加开放协议”没有做：§4 没有定义列表筛选参数，后端也没有实现。
22. 删掉 `convertedEndpoints.ts` 后，API Key 页的分组端点预览（`KeyGroupDetails`、`KeyUseGuide`）只按分组的 `platforms` 列端点。这两处拿不到 `served_protocols`，不反映账号关掉的协议和开启的转换（与 §6 第 2 条同类）。
23. 模型规则里的本平台协议被账号关闭后，矩阵不显示该列（同示意图），只给警告；要取消勾选得切到列表视图或重新开放该协议。
24. “两跳转换”按端点 `via` 的协议链长度大于 2 判断，没有按等级是否为 `discouraged` 判断。
25. 账号类型已不存在的账号：编辑器不显示两张协议卡，保存请求不带这两个字段（与第 4 条一致）。账号详情在 `served_protocols` 返回前先按类型端点原样显示。

**发布手册**

26. §6 第 1 条说“升级后照着在批量编辑里配”。批量编辑是把同一份设置替换到所选账号上，而且只对同一账号类型的账号可用，所以手册以逐个账号在编辑器里配置为主，批量编辑只用于要配成一样的账号。
27. 导出查询除了跨平台转换的请求，还列出核心本地估算的 count_tokens 请求（`route = local_count`）：账号类型没有声明该平台时，这类请求升级后同样关闭（跟随生成协议），而它们的 `upstream_protocol` 是 `local`，按“平台前缀不同”筛不出来。
28. 手册放在 `next/deploy/rollback/`（AGENTS.md 规定的单向发布手册位置）。版本号还没定，文件名暂时没有版本前缀，发布时改名。

### 10.2 没做完的

（审查后已补的在 10.3；下面只留仍然没做的。）

- `store/gormtest/BASELINE.md` 没有记录这次的读数（第 14 条），也没有恢复分组快照的基准。
- 账号列表按“开放协议”筛选：要先定参数（如 `?protocol=openai.chat`，语义是账号对至少一个模型开放该协议）并在后端实现。
- API Key 页的端点预览要准确，需要后端在用户可见的分组信息里也返回 `served_protocols`。
- `GET /account-types` 的 `endpoints` 没有标出“只走原生”的端点（WebSocket、任务提交）。控制台的候选列是从它推出来的；如果某个类型的 `endpoints` 里出现经转换或适配的这类端点，界面会列出来，保存时被服务端拒绝。
- `ineligibleReason` 里原有的用量检查每个候选分配 3 次（第 14 条），500 个候选就是每个请求 1500 次、约 536 KB。这是改动前就有的，值得单独优化。
- 故障策略刷新快照这条路径只有单元测试（`candidateEligible` 与 `ineligibleReason` 结果一致），没有端到端用例。
- 控制台的批量编辑弹窗和深色模式没有做视觉检查。

### 10.3 审查后的修改（2026-11-17）

三位审查者（后端、规范、控制台）的发现，逐条核对后的处理。没有改变 §8 的任何决策。

**后端**

1. **404 与冷却中的账号**（10.1 第 8 条）：`core.AccountDirectory` 加 `Resting(ctx, groupID, types)`，账号模块从 `Candidates` 用的同一份快照里给出冷却中、限时停用未到期的账号；`gateway/sched_trace.go` 的 `protocolNotServed` 对它们跑 `ineligibleReason`，只有其中有账号不是被协议绑定过滤时才不返回 404。不查库，没有 200 个账号的上限。调度记录的那次查库（`Unschedulable`）恢复成只在写记录时读，与本功能之前相同。这改了 core 接口，属于契约变更（CONTRACTS §110.5）。
2. **`served_protocols` 与调度不一致**：账号有模型白名单、白名单里的模型全被条目点名到别的协议时，旧实现仍把本平台协议列为“对所有模型开放”。`modelsOn` 不再自己解释规则：被点名的模型调 `Serves`，其余模型调新增的 `(*ProtocolRules).ServesOthers`，白名单里没有未被点名的模型时不用 `null`。加了随机对拍测试。
3. **“协议规则只有一处定义”**：同上；`Serves` 拆出内部的 `serves`，`Serves` 与 `ServesOthers` 共用，仍不分配内存。
4. **`cross` 只有一种算法**：`routing.go` 加 `crossPlatform`（`!b.Supports(入站平台)`），`addRoute` 与 `addLocalCountRoute` 都用它，不再有 `q != c.platform` 这第二种写法。
5. **模型列表归一化不再复制**：`model_limits.go` 的 `entryModels` 给 `normalizeModelLimits` 和 `normalizeModelProtocols` 共用；两个 jsonb 列的解析与序列化用泛型的 `parseList`、`listJSON`。错误码、路径、文案都没有变。

**验收用例与文档**

6. `e2e/ac19_builtin_platforms_test.go`：先断言默认 404（OpenAI 错误格式、没有调用上游），给租户账号的 `model_protocols` 为该模型开 `openai.chat`（同时保留 `anthropic.messages`）后断言 200。已随整套 e2e（49 个用例）在与转发 V2 合并后的代码上运行并通过。
7. AGENTS.md：迁移号改为 0106，`rollback/` 一行指向协议绑定的手册。CONTRACTS §110.1、110.3、110.5、110.7、110.9–110.11 和 CHANGELOG 同步。

**控制台**

8. **矩阵里没有规则的行**：按 `rules[模型] ?? rules["*"] ?? 默认` 显示（`protocolBinding.protocolsOf`），与网关的判定顺序一致；“其余模型”有指定时这些行行尾写“跟随其余模型”，图例相应换一句；在这样的行上点一格，从它显示的协议开始改。示意图画的是“没有规则就显示默认”，与 §3.4 的判定顺序不一致，以 §3.4 为准。**文案“跟随其余模型”是实现时定的，用户可以改。**
9. **列表视图的卡片**：编辑期间每张卡有固定的 id，两张卡的协议改成一样时不合并（保存的数据里才合成一条，符合 §5.1“保存时合并”）；卡片的 key 不再是模型列表拼的字符串，加减模型不重建卡片、输入框不丢焦点。
10. **JSON 视图**：文本解析失败期间，“本平台协议”卡的改动不再覆盖用户正在改的文本（与原注释一致）。
11. **`protocols` 里类型已不服务的 id**：“本平台协议”卡里显示成只能移除的警告项，问题列表里加一条警告（`unknown_own_protocol`）。
12. **开发 mock**：插件未启用时返回 503（原来 409）；字段错误的路径和错误码按 CONTRACTS §110.6（`invalid` / `unsupported` / `cross_platform` 等）；`served_protocols` 的模型判定按第 2 条。

**没有处理的**

- BASELINE.md 的读数、账号列表按协议筛选、API Key 页端点预览、`GET /account-types` 标出“只走原生”的端点、`ineligibleReason` 里原有用量检查的分配、故障策略刷新路径的端到端用例、批量编辑弹窗与深色模式的视觉检查：仍如 10.2。
- 审查没有发现需要改变产品行为的问题。
