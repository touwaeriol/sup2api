# 客户端断开时的计费策略（2026-11-12）

状态：设计与实现在同一分支 `feat/client-disconnect-policy`。契约落在 CONTRACTS §8 和 §25.8。

> 2026-11-14 修订（用户决定）：`client_disconnect_wait_seconds` 删除，`continue` 的续读不另设上限，只受请求本身的超时（`relay_timeout_seconds`、流式空闲超时）约束，与客户端还在时一致。下文关于等待上限的内容已作废，以 CONTRACTS §25.8 为准。
> 2026-11-19 修订（所有者决定）：下文对照的「缺价策略」`missing_price_policy` 已删除，没有价格的模型一律拒绝（CONTRACTS §17.3）。

## 1. 要解决的问题

客户端在流式响应中途断开时，核心拿不到上游最后才发的用量，现行做法是按预扣估算计费（CONTRACTS §25.8，2026-11-08）。这条路径有一个多收的缺陷：

- 预扣估算把整个提示算作普通输入（`Input`），不含缓存。
- 断开前上游已经在 `message_start` 里报了真实的输入侧用量：很小的 `Input` 加上很大的 `CacheRead` / `CacheCreation` / `CacheCreation1h`。
- 现行代码对两者**逐字段取最大值**（`gateway/pipeline.go` 的 `maxTokens`）。Anthropic 的输入不含缓存（exclusive 语义），于是提示被算了两遍。

例：10 万 token 的提示几乎全部写入 1 小时缓存，客户端在 `message_start` 之后断开。

| | 输入 | 1h 缓存写 | 约合输入价的倍数 |
|---|---|---|---|
| 应收 | 10 | 99,990 | 2× |
| 现在收 | 100,000 | 99,990 | 3× |

以缓存读为主的请求差得更多（约 1.1× 对 0.1×）。上下文长度 `len` 也被翻倍，超过 10 万的提示会被误推入 20 万以上的长上下文档。

## 2. 方案

两件事，互相独立：

1. **修正按预扣计费时的合并方式**（缺陷修复，对所有策略、所有转发路径生效）。
2. **新增「客户端断开时」策略开关**，三选一，默认 `continue`（2026-11-12 用户决定）。

### 2.1 合并方式

断开时记录里的用量分两侧处理：

- **输入侧**（`Input`、`CacheRead`、`CacheCreation`、`CacheCreation1h`）：响应已经报告过任意一项，就整体采用响应的数；一项都没报告，才用估算。两者不再混合。
- **输出侧**（`Output`）：取响应已报告的数与估算（即 `output_reserve_tokens`）的较大值，和现在一样。

依据：Anthropic 的输入侧用量在 `message_start` 里一次给全，之后不再变大；OpenAI 系协议的用量只在流末尾出现，断开时输入侧全为 0，自然落到估算上。

### 2.2 策略开关

| 取值 | 含义 |
|---|---|
| `continue`（默认） | 断开后不再向客户端写，但继续把上游响应读完，按上游报告的真实用量计费，然后结束。 |
| `precharge` | 断开后立即中止上游，按预扣估算计费。即改动前的行为，加上 2.1 的修正。 |
| `free` | 断开后立即中止上游，这次请求不计费。 |

`continue` 读不完时退回 `precharge`。读不完指：

- 等待超过 `client_disconnect_wait_seconds`（默认 120，范围 5–1800）；
- 上游自己中途出错（连接中断，或以流内错误事件结束）、空闲超时或触发 `relay_timeout_seconds`；
- 进程关停不再等待在途请求（见第 3 节）；
- 请求走的是不支持续读的路径（见 2.4）。

`free` 的风险写在界面说明里：流式协议的用量在最后才发，客户端收完正文再断开就能白拿整个输出，只适合内部或可信环境。

三种策略都不改变这几种情况（与 §25.8 现有规定一致）：排队时就离开（上游没应答，预扣照旧释放）、端点 `billing=free`、插件自带结算（`Replacement` 或 `Reservation`）、上游错误。

### 2.3 开关放在哪里

**界面**：系统设置 → 计费（`web/src/views/settings/BillingSettingsCard.vue`），紧跟在「请求预扣 Token 数」之后。

理由：

- 它和「缺价策略」是同一类设置：都回答「遇到某种情况时这次请求怎么收费」，取值里也都有「免费」。
- `precharge` 策略，以及 `continue` 读不完时的退路，扣的是上一行「请求预扣 Token 数」所代表的那笔预扣（输入估算加 `billing` 行里的输出预留 `output_reserve_tokens`），放在一起才看得懂扣的是什么。
- 网关卡片里是重试次数、超时、排队这类转发参数，没有一项涉及收费。

「继续读取的最长等待」只在选了 `continue` 时显示。

**存储**：`settings` 表的 `billing` 行，新增两个字段。旧行没有这两个字段，读出来就是默认值，不需要迁移。

| 字段 | 取值 | 默认 |
|---|---|---|
| `client_disconnect_policy` | `continue` / `precharge` / `free` | `continue` |
| `client_disconnect_wait_seconds` | 5–1800 | 120 |

**API**：沿用 `GET/PUT /api/v1/settings/billing`，权限不变。

### 2.4 续读（`continue`）怎么实现

现在上游请求的 context 派生自客户端请求的 context，客户端一断，上游请求随之取消。改为：

- 上游请求的 context 与客户端 context 脱钩（`context.WithoutCancel`），另挂一个监听：
  - 客户端断开时，如果策略不是 `continue`，或上游还没开始应答，或当前路径不支持续读：立即取消上游（与现在一致）。
  - 否则不取消，启动等待计时器，到点以 `errDisconnectWaitTimeout` 取消上游。
- 转发循环里向客户端写失败后，不再返回，改为丢弃后续输出、继续读上游并提取用量。
- 「客户端已离开」以有输出被丢弃为准：客户端收完最后一个字节才关连接的请求没有丢任何输出，仍记为成功。
- 上游读到正常结束：记录仍是 `client_canceled` / 499（客户端确实没收到），但用量是完整的，按实际计费，不经过预扣合并。`usage_logs.anomalies.usage_extract` 不写 `precharge`。
- 没读完：按 `precharge` 处理。

续读期间请求处理协程不退出，账号并发槽位、API Key 并发、流空闲超时、`relay_timeout_seconds` 都照常生效，不需要额外的占位逻辑。

**支持续读的路径**：核心直接转发的普通响应，即 `forwardSSE`、`forwardJSON`、`forwardJSONConverted`。

**不支持续读的路径**（策略为 `continue` 时按 `precharge` 处理；`precharge`、`free` 两种策略对它们照常生效）：

- 插件 `Execute` 执行的请求（`executeAttempt`，如 codex、volcengine）：上游调用由插件驱动，有自己的 context 和结算；
- 任务提交（`task.submit`）、资源与额度流（`forwardResourceResponse`、`forwardCreditSSE`）；
- WebSocket。

这些路径仍然得到 2.1 的修正，所以多收的缺陷在所有路径上都消失；它们只是暂时用不上「按实际用量」。

## 3. 代价与取舍

- `continue` 下，客户端断开后请求最多再占用账号并发槽位 `client_disconnect_wait_seconds` 秒，用户也会为没收到的输出付费，所以带上限。
- **默认值是 `continue`，升级后所有部署的行为随之改变**：核心直接转发的请求在客户端断开后不再立即中止上游。要保持原行为，把策略设为 `precharge`。
- 设置读取失败时网关按 `precharge` 处理，不续读。
- 进程退出：关停等待期满时核心调用 `Gateway.AbandonReads()`，所有只为拿用量而继续读的上游立即中止，按 `precharge` 处理，不会拖住关停。等待期内（非托管 30 秒，托管按网关给的宽限）续读和其他在途请求一样可以收尾。
- 升级后运维会看到的其他变化（策略为 `continue` 时）：
  - 断开请求的 499 记录，`latency_ms` 包含续读的时间；
  - 这些记录不再带 `usage_extract=precharge`，统计里的「估算行」相应减少；
  - 客户端断开后立即重试，会同时占用两个账号并发名额，直到前一个读完；
  - 被放弃的请求在上游侧仍然跑完整个生成，上游账号照常消耗额度；
  - 客户端离开后上游才发出的流内错误，现在也会计入该账号的流失败记录。
- 半关闭的客户端（发完请求就关闭写方向、仍在读）会被 net/http 视为断开：`continue` 下它不再收到后续输出，并按实际用量付费。

## 4. 任务拆分

| # | 任务 | 位置 | 验证 |
|---|---|---|---|
| T1 | 用测试复现多收：exclusive 语义、带缓存、`message_start` 后断开 | `gateway/precharge_cancel_test.go` | 修复前失败 |
| T2 | 修正合并方式（2.1） | `gateway/pipeline.go` | T1 通过，原有用例不变 |
| T3 | `billing` 设置新增两个字段：默认值、校验、归一化、读取接口 | `billing/settings.go` | 设置读写测试 |
| T4 | 计费判定按策略分支：`free` 不计费、续读完成按实际计费 | `gateway/pipeline.go` | 单测覆盖三种策略 |
| T5 | 上游 context 脱钩与等待计时器 | `gateway/dispatch.go`、新文件 `gateway/disconnect.go` | T6 |
| T6 | 转发循环续读；端到端测试：断开后读完、等待超时、`precharge` 立即中止、免费策略、续读中遇到关停 | `gateway/forward.go`、`gateway/disconnect_test.go` | `go test ./internal/gateway/...` |
| T7 | 控制台：计费卡片新增两行，中英文案 | `web/src/views/settings/BillingSettingsCard.vue`、`i18n` | `npm run typecheck`、组件测试 |
| T8 | 契约：CONTRACTS §8 字段表、§25.8 末尾 | `docs/CONTRACTS.md` | — |
| T9 | 规范审查并修复（见第 5 节） | 全部改动 | 审查项全部通过 |
| T10 | 整机实测（见第 6 节） | 一次性测试环境 | 六个场景符合预期 |
| T11 | 合并到 `develop` | — | CI 的四项检查 |

## 5. 规范审查清单

### 5.1 代码规范（项目约定，2026-11-12 用户明确）

| 原则 | 在本次改动里的含义 |
|---|---|
| 抽象 | 「客户端离开后上游怎么办」是一个概念，集中在 `gateway/disconnect.go`，不散落在转发循环里。 |
| 复用 | 先用仓库已有的东西：设置读写与校验、写入包装、超时计时、测试环境、界面的表单与文案键。 |
| 解耦 | 策略归计费设置所有，网关只通过一个小接口读取；核心包只放双方共用的类型。 |
| 低嵌套 | 用提前返回代替层层 `if`；一个函数只做一件事。 |
| 简洁 | 能推导的状态不另存；没有只用一次的抽象；注释只写代码看不出的原因。 |
| 低侵入 | 已有文件的改动尽量小；功能没启用的路径行为不变。 |

### 5.2 门禁与契约

依据 CONTRACTS §2、§3、§10 和 `next-ci` 的门禁：

1. `gofmt -l` 无输出；`go vet ./...`、`GOOS=linux go build ./...`、`go test ./...` 通过，数据库测试没有被跳过。
2. 代码注释和标识符用英文；用户可见文案同时有 `en` 和 `zh`。
3. JSON 字段 snake_case；校验失败返回 `invalid_argument` 和 `details.fields`。
4. 设置项有默认值，旧数据不需要迁移；契约文档同步更新。
5. 测试不假设某个端口空闲，起自己的监听。
6. `web/`：`npm run typecheck` 通过；提交前还原 `server/web/dist/index.html`。

### 5.3 审查方式

三名审查员并行，各看一个角度：抽象复用与解耦；低嵌套与简洁，并复核修复的正确性；低侵入与门禁契约。发现的问题修完后重跑门禁，再合并。

## 6. 实测

单元测试和网关内的端到端测试用的是进程内的假上游。合并前另做一次整机实测：用本分支构建核心，接 `deploy/mock-upstream` 的模拟 Anthropic 上游，走真实的 HTTP、数据库和结算。

| 场景 | 做法 | 预期 |
|---|---|---|
| `continue`，断开后读完 | 流式请求，上游每个分片延迟；收到首个事件后断开客户端 | 上游收到完整请求并正常结束；使用记录为 `client_canceled`，token 等于上游报告值，余额按实际用量扣减 |
| `continue`，等待超时 | 同上，上游延迟超过 `client_disconnect_wait_seconds` | 到时上游连接被关闭；按预扣估算扣费，`usage_extract=precharge` |
| `precharge` | 切换策略后同样断开 | 上游连接立即关闭；按预扣估算扣费，输入侧取上游已报告的数 |
| `free` | 切换策略后同样断开 | 上游连接立即关闭；余额不变 |
| 带缓存的提示 | 上游用量设为缓存读与 1 小时缓存写为主 | 输入侧不重复计费，金额与手算一致 |
| 正常完成 | 不断开 | 与改动前一致 |

这六个场景写成了验收用例 `e2e/ac26_client_disconnect_test.go`（AC 26），以后可以重复跑。

### 结果（2026-11-12）

环境：ovh 上的一次性 Compose 项目 `sub2api-next-e2e-cdp`，本分支构建的核心双节点、PostgreSQL、Redis、Caddy、`mock-upstream`。模型价格（每百万 token）：输入 3、输出 15、缓存读 0.3、5 分钟缓存写 3.75、1 小时缓存写 6。上游报告的用量：输入 7、输出 33、缓存读 40、5 分钟缓存写 60、1 小时缓存写 900。输出预留 4000。

| 场景 | 记录 | 输出 token | 扣费（USD） | `usage_extract` | 耗时 |
|---|---|---|---|---|---|
| `continue`，断开后读完 | `client_canceled`，已计费 | 33（上游报告） | 0.006153 | 无 | 3.6 秒，上游读到结束 |
| `continue`，等待 5 秒超时 | `client_canceled`，已计费 | 4000（预留） | 0.065658 | `precharge` | 5.0 秒，到时中止 |
| `precharge` | `client_canceled`，已计费 | 4000（预留） | 0.065658 | `precharge` | 立即中止 |
| `free` | `client_canceled`，免费 | — | 0 | 无 | 立即中止 |
| 正常完成 | 成功，已计费 | 33 | 0.006153 | 无 | — |

- 五种情况的输入侧都是上游报告的 7 / 40 / 60 / 900，没有叠加估算，「带缓存的提示」场景随之覆盖。
- 每个场景的余额减少额与使用记录的金额一致。
- 手算核对：(7×3 + 33×15 + 40×0.3 + 60×3.75 + 900×6) ÷ 1,000,000 = 0.006153；输出换成 4000 时为 0.065658。

搭这套环境时发现 `develop` 上三处与本次改动无关的既有问题，只在测试副本里绕过，未在本分支修改：

1. `deploy/docker/build-go.sh` 只从构建用的 `go.work` 里去掉 `companions` 和 `companions/worker`，后来加入的 `companions/controller-go`、`companions/guard` 没去掉，而 `Dockerfile.dockerignore` 排除了它们，镜像构建直接失败。
2. `deploy/e2e/compose.yml` 只给节点设了 `SUB2API_DATABASE_URL` / `SUB2API_REDIS_URL`，核心现在要求 `DATABASE_URL`，节点启动即退出。
3. 同一文件里 `market-init` 的内存上限 128 MB 不够复制 176 MB 的插件市场（被 OOM 杀掉）；默认的 `E2E_VERSION=0.1.0-e2e` 低于内建插件要求的核心版本，全部内建插件安装失败。

## 7. 不在本次范围

- 插件 `Execute` 路径、WebSocket 的续读。
- 预扣估算本身不含缓存的问题（缓存读为主的请求在响应开始前断开时，仍按整段提示的输入价估算）。
- 本次改动不包含发布；上线走核心发布流程。
