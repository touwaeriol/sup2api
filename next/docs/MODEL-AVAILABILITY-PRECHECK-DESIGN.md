# 模型可用性预检优化设计（2026-11-02）

状态：设计稿（三人设计 + 一轮对抗审查后修订），待用户确认后实施。只改核心 `next/server`、控制台 `next/web` 和文档，插件不改，不加迁移。

## 1. 问题

线上使用记录里常见 `失败 503 model_availability`，没有账号、没有原因，无法排查。

- 每个请求在选号前调用 `filterAvailability`（`gateway/availability.go:70`）：先现查 PG 读所有候选账号的额度快照，再调插件 `CheckModelAvailability`。两步**共用一个 200 ms 时限**（`:88`，插件的 `:151` 从同一个 ctx 派生，第二个 200 ms 不起作用）。
- 任何读快照或插件调用的超时、出错、结果不合规，都把全部候选判为"无法确认"，返回 503（`:97-100`、`:147`、`:153`）。此时还没选号，所以 `account_id` 为空、`sched_decisions` 为空，也不写日志。
- 选号后、发上游前，`guardAvailability`（`dispatch.go:489/:639`、`websocket.go:405/:582`）再查一次 PG，同样 200 ms。
- sup2api-ccp 到 Supabase：热连接约 28 ms，新建连接约 430 ms，连接池 `max_conns=10`、`min_conns=0`。高峰期需要新建连接时，快照还没读完预算就用光了，插件根本没被调用。2026-11-02 高峰每小时 500–900 个请求中有 2–23 个这样失败，全部在 ccp。
- 插件升级（实例 draining）期间，`Instance.call` 直接返回 `ErrPluginUnavailable`（`plugin/grpcruntime/adapters.go:65-77`），预检同样全部 503。注意：本方案只消除预检这一处；同一请求随后的 `BuildUpstreamRequest`/`Execute` 用的仍是旧实例，同样会失败（`dispatch.go:504/568`、`execute.go:159`），最终变成 503 no available account（`dispatch.go:180`）。升级窗口的问题不在本方案范围内。
- ccgateway 0.1.112 起，托管账号的预检唯一会拦的情况是模型专属窗口（`7d_fable`/`7d_sonnet`）`rejected` 且重置时刻未到（`plugins/ccgateway/.../availability.go:58-76`）。为了这一个判断，每个请求要多两次跨洋查库。
- 已核对：没有自动禁用的账号被调度。禁用后没有任何请求落到这些账号上；几条带账号的 503 都发生在禁用之前。

## 2. 参照：sub2api

- 模型族限额作为账号状态（`model_rate_limits[claude-fable-5] = 重置时刻`）放在 Redis 调度快照和进程内副本里，判断是纯内存比较，请求路径不查库。
- 429 到达时写入限额，并同步刷新快照。
- RPM、会话数、窗口费用等附带检查读失败时放行；只有读不到候选列表才失败。
- 全部被筛掉时写日志 `select_account_no_available`，带各原因计数和最多 5 个样例账号；使用记录里同样没有账号 ID。

## 3. 方案总览

| # | 内容 | 效果 |
|---|---|---|
| A | 额度快照放进节点内存镜像，请求路径不查库 | 预检与 guard 变成内存读 |
| B | 预检只有插件明确说 `UNAVAILABLE` 才拦，其他一律放行并记 WARN | 预检不再产生 503 |
| C | 每个账号的预检结论写进使用记录 `anomalies.availability`，详情页展示；采样日志 | 可以排查 |
| D | 额度写入的总线消息只失效额度镜像，不再清空分组快照 | `Candidates` 不再频繁查库 |

## 4. A：额度快照内存镜像（`account/quota_mirror.go`，新文件）

### 4.1 结构

```go
type quotaMirror struct {
    mu      sync.Mutex
    wipe    uint64                      // 全部失效时加一
    entries map[int64]*quotaMirrorEntry // 键 account_id
    flight  singleflight.Group
    loaded  atomic.Bool                 // 首次全量加载完成
}
type quotaMirrorEntry struct {
    snap   *pluginv1.AccountQuotaSnapshot // nil = 没有快照行（负缓存）
    ver    uint64                         // 按 id 失效时加一
    stale  bool
    dbAt   time.Time                      // 读出时的 statement_timestamp
    filled time.Time
}
```

- 缓存转换好的 proto（转换从 `quota_refresh.go:356-368` 抽成 `toPluginQuota`）。镜像里的对象只读，调用方需要修改时自行 Clone（网关已在 `availability.go:142,186` Clone）。
- 负缓存：查过但没有快照行的 id 记为 `snap=nil`（例如新建、还没初始化快照的账号）。只有声明了 `modelAvailability` 的账号类型会被查询（`availability.go:76-83`），目前只有 ccgateway 声明（`plugins/ccgateway/manifest.json:33`）。
- 单行无效只跳过这一行并记日志，不再整批失败（现在 `quota_refresh.go:353-355` 一行坏数据就拦住所有账号）。
- 条目数不超过账号数；全量重载时删除 PG 里已不存在的 id。`quotaMirror` 可懒初始化（测试直接构造 `&Service{}`）。

### 4.2 失效

- 账号服务的总线回调由 `func([]byte){ s.invalidate() }`（`service.go:190`）改为 `s.onAccountChanged(payload)`：解析 `account_id`，大于 0 调 `invalidateQuota(id)`，否则调 `invalidateQuota(0)`（没有 id 表示全部，见 `core/ports_cluster.go:113-115`）。目录快照的处理见 D。
- `invalidateNode(id)`（`service.go:233`）同时调用 `invalidateQuota(id)`，写入节点立即读到自己的写入。
- 失效只标记 `stale=true`、`ver++`，不删除，保留旧值供数据库出错时使用；随后异步回填本机已缓存的 id，不阻塞总线接收 goroutine。
- 回填与失效竞争：
  - 回填开始前记下各 id 的 `ver` 和 `wipe`，写回时两者都没变且 `dbAt` 不早于原值才写入缓存。
  - singleflight 键里带上本次读取开始时的 `wipe` 和各 id 的 `ver`（如 `w12|5:3,9:1`）。这样失效之后发起的回填一定是一次新的读取，不会合并到失效前就开始的旧读取上；失效总在写事务提交之后，所以新读取一定看到这次写入。
  - 结果即使因版本变化不写入缓存，也照样交给正在等待的请求（它比缓存里的旧值新）。
- 兜底全量重载：每 60 s 重载一次。范围统一为"声明了 `modelAvailability` 的账号类型下所有未删除账号"（与 §4.4 预热相同）。重载结果按同样的版本规则写入：只覆盖 `ver` 和 `wipe` 都没变的条目；PG 里已不存在的 id 也只在其 `ver` 没变时删除，不误删重载期间新填入的条目。重载覆盖的情况：pub/sub 丢消息（`cluster/bus.go:13-15`）、插件表触发器换代次不发通知（`0053_account_quota_refresh.sql:34-43`）等。
- 总线重连：`cluster/bus.go:94-108` 重新订阅后调用 `invalidateQuota(0)`，因为断开期间的消息已丢。
- 插件代次切换：注册 `Registry.OnChange`，触发 `invalidateQuota(0)`。`OnChange` 可能早于 `plugins` 表的更新提交，所以 2 s 后再失效一次。
- 补发通知：
  - `store.EnsureAccountQuota`（`quota_refresh.go:36` 调用）改为返回 `(bool, error)`，换代次或新插入行时为 true，调用方收到 true 时调 `s.changed`；`handlers.go:1154`、`ccgateway.go:327` 在事务内调用，事务结束后本来就会 `changed`，忽略返回值。
  - `initializeQuota`/`initializeQuotaPage`（`quota_refresh.go:244-270`）新建快照行后调 `s.changed`，否则负缓存要等 60 s 重载。

### 4.3 请求路径上的读取

`AccountQuotaSnapshots(ctx, ids)` 改为读镜像，并多返回每个 id 是否为旧值。`core.AccountQuotaSource` 新增方法：

```go
// QuotaView: Snapshots 同原方法；Stale[id] 为 true 表示返回的是失效后尚未回填成功的旧值；
// Missing 为读不到（没有条目且回填未完成）的 id。
AccountQuotaView(ctx context.Context, ids []int64) (core.QuotaView, error)
```

原 `AccountQuotaSnapshots` 保留为它的包装，供其他调用方使用。

| 条目状态 | 处理 |
|---|---|
| 新鲜（未过期且 60 s 内回填过） | 直接返回，没有 I/O |
| 过期但有值 | 发起或加入回填，最多等 40 ms；超时返回旧值并标记 `Stale`，不报错 |
| 没有条目 | 发起或加入回填，等到 ctx 截止；超时把这些 id 放进 `Missing` |

- 回填在 `context.WithTimeout(context.Background(), 3s)` 里执行，请求只等 done channel，所以冷连接超过请求预算也会在后台完成并填好镜像。singleflight 键为排序后的缺失 id 列表；全量重载用固定键。
- 数据库出错时保留旧值，但旧值超过 10 分钟后按"没有条目"处理；同一批 id 失败后 1 s 内不重试。
- 直接读 PG，不经过 Redis 额度缓存：从 ccp 看两者都是二三十毫秒，每次写入都会删 Redis 键，多一层只多一个过期来源。`cachedAccountQuotas` 只留给控制台列表（`quota.go:172`）。

### 4.4 预热

启动时异步全量加载所有声明了 `modelAvailability` 的账号类型下未删除账号的快照，失败按 1→30 s 退避重试，之后每 60 s 按 §4.2 的规则重载。加载完成前的未命中按"没有条目"处理。

实施时的调整（2026-11-02）：
- 插件在 `account.Run` 之后才启动（`app/phases.go`），启动预热时还没有类型声明 `modelAvailability`，等于空加载。所以插件代次切换除了失效，还立即触发一次全量重载（2 s 后再一次），真正的预热在插件就绪时完成。
- "新鲜"期定为 75 s（大于 60 s 重载周期），避免条目在下次重载前刚好过期、请求路径频繁回填。超过新鲜期又没等到回填的条目同样标为 `Stale`。
- `AccountQuotaView` 的错误约定：err 非空当且仅当 `Missing` 非空，其余 id 的 `Snapshots` 照常填好；1 s 退避期内返回 `errQuotaMirrorBackoff`。
- 能否写入缓存的版本在回填真正开始时（与"进行中读取数"同一次加锁）记下，而不是发起时，防止发起到开始之间的失效被旧读取覆盖。
- `EnsureAccountQuota` 只要写了行就返回 true（含只改 `auto_refresh`），多发的通知无害；`executeQuotaQuery` 里的 Ensure 现在也发通知。
- 总线重连以"接收或 ping 失败之后收到的第一条消息"判定（go-redis 内部静默重连），经 `core.BusReconnects` 回调。

### 4.5 代次（generation）

- 镜像只镜像 PG 的 generation，不自己生成。
- 代次会在重新授权、凭据或设置变更、状态变更、插件状态或版本变更、插件 revision 变化时更换（迁移 0053 触发器、`store/account_quota.go:218`）。
- 镜像里的代次可能是旧的（其他节点刚换代次、本机还没收到通知）。此时观测写回的 `WHERE q.generation=$2`（`store/account_quota.go:176`）影响 0 行，观测被丢弃，不会写到新身份上，代价是这段时间（通常几十毫秒，丢消息时最多 60 s）该账号的观测写不进去。旧身份的专属窗口也可能多拦这一段时间，到 `resets_at` 后插件自己放行。
- 准入用的是旧值（`Stale`）时，准入记录的代次按"未知"处理，guard 改用自己读到的值（§5.3），不会因为"预检旧、guard 新"而误判为重新授权。

## 5. B：预检放行规则（`gateway/availability.go`）

### 5.1 判定表

| 结果 | 准入 | 日志 | 原因码 |
|---|---|---|---|
| 快照源为 nil | 放行，quota 为 nil 照常问插件 | WARN | `snapshot_unavailable` |
| 快照读取出错、超时或部分缺失（`Missing`） | 放行；有快照的账号带快照，缺的为 nil，照常问插件 | WARN | 缺快照的账号 `snapshot_unavailable` |
| 没有快照行（err 为 nil） | 正常，quota 为 nil | — | — |
| RPC 出错（含 PluginUnavailable、`codes.Unimplemented`） | 放行这一批及之后的批次 | WARN | `plugin_error` |
| RPC 超时 | 放行这一批及没来得及调用的批次 | WARN | `plugin_timeout` |
| 响应为 nil、条数不对、重复、越界 key、状态越界、`RetryAtUnix<0` | 整批丢弃并放行；已完成且合法的批次照常生效 | WARN | `plugin_bad_response` |
| `UNKNOWN`（`invalid_candidate`/`duplicate_candidate`/`unsupported_account_type`，或插件漏填 state，proto 里 `UNKNOWN=0`） | 放行 | WARN | `plugin_unknown`，附插件原因 |
| `UNAVAILABLE` | 拦截 | 见 §6.3 | `unavailable`，附插件原因（如 `quota_rejected:7d_fable`）和 retry_at |
| `AVAILABLE` | 放行 | — | — |

（gRPC 适配器总是实现了 checker 接口，插件真没实现时以 `codes.Unimplemented` 返回（`sdk/.../serve.go:556`），所以归入 `plugin_error`，不单设"未实现"。）

- 全部候选被拦只可能是全部 `UNAVAILABLE`，返回 429 `model_quota_exhausted`。所有被拦账号都有未来 retry_at 时带 `Retry-After`（沿用 `:128-131`）。预检本身不再产生 503。
- `UNKNOWN` 放行的理由：0.1.112 后它只表示核心与插件之间的契约不一致，不反映账号状态。拦下来会让一个 bug 把整个账号类型变成 503。插件要拦必须明确返回 `UNAVAILABLE`。已核对：没有其他插件声明 `modelAvailability`，不存在依赖 `UNKNOWN` 拦截的插件。
- 模型名校验前移到核心：现在核心对模型名只检查非空（`gateway/pipeline.go:427-434`，参数来源 TrimSpace，JSON 路径来源原样），ccgateway 的 `invalid_candidate` 实际上是唯一拦截空白和控制字符的地方。`UNKNOWN` 放行后，核心在解析请求时对含空白或控制字符、超过 256 字节或非法 UTF-8 的模型名直接回 400 `invalid_request_error`，不进入选号。（256 字节的长度限制在 2026-11-18 转发 V2 收尾时删除，模型名不限长度，CONTRACTS §15.2、§112.16；字符规则照旧。）
- 客户端断开（父 ctx 结束）时不放行，直接以 canceled 结束，不再走 `admitRate` 占用限速名额。

### 5.2 时限

```go
const availabilitySnapshotTimeout = 500 * time.Millisecond // 只管读快照
const availabilityPluginTimeout   = time.Second            // 读完快照后另起，本次所有分组与批次共用
const quotaObservationTimeout     = 2 * time.Second        // applyQuotaObservation 写回（现为 200 ms，冷连接下观测丢失）
// 删除 availabilityTimeout
```

- 两个预算并列，都从请求 ctx 派生。grpcruntime 自身的 2 s（`adapters.go:27`）仍是上限。
- 做成 `Gateway` 字段，默认取常量，测试里可改小。
- 镜像命中时读快照是内存读；插件是本机调用，正常不到 1 ms。
- 插件出问题时不能让每个 attempt 都再等 1 s（guard 每个 attempt 都执行，`CheckModelAvailability` 还和 `ClassifyError` 等热路径调用共用 classHot 并发槽（转发 V2 起构造请求的 `Execute` 走单独的 Execute 类），`adapters.go:205/209/263`）：
  - 同一个请求里插件已经失败放行过的，guard 不再调用插件；
  - 节点级熔断：某插件的可用性调用失败（出错、超时、坏结果）后 10 s 内，本节点对它的可用性检查直接按 `plugin_error` 放行，不发 RPC；10 s 后放一个请求试探，成功即恢复；
  - guard 读快照遇到缺失时不等待，直接沿用准入快照。
  - 因此最坏情况（镜像冷 + 插件第一次卡死）只有触发熔断的那个请求多约 1.5 s，之后的请求不再等待。
- 分批不变（128 一批，顺序调用）。`availabilityResults` 改为返回"已校验的批次结果 + 失败原因"，每批先校验到临时 map，整批合法才合并。

### 5.3 guardAvailability

保留每次 attempt 的 guard，它现在是内存读，用途是在换号或并发时看到别的请求刚写入的 `7d_fable rejected`。规则：

```go
func sameQuotaIdentity(old, cur *pluginv1.AccountQuotaSnapshot) bool {
    return old.GetGeneration() <= 0 || cur.GetGeneration() <= 0 || old.GetGeneration() == cur.GetGeneration()
}
```

1. 读快照；缺失时沿用准入时的快照（`cur = old.quota`），不等待回填。
2. 已准入的账号：
   - 准入快照是旧值（`Stale`）或为 nil 时，代次按"未知"处理，直接进入第 3 步；
   - 否则 `!sameQuotaIdentity` → 换号，原因 `guard_identity_changed`（真实的重新授权，仍返回 `availabilityFailure(false)`）；
   - 模型列表变化 → 换号，原因 `guard_models_changed`；
   - 同一秒且快照相同 → 直接放行。
3. 其余情况调用 `checkAvailability(ctx, [ref], {id: cur})`（本请求插件已失败放行或熔断中则不调用，直接放行），内部按判定表放行；放行时把 `c.availability[id]` 更新为 cur。HTTP 的 guard 紧挨着发送执行，用 cur 的代次写之后的观测是安全的。

模型不一致检查（`dispatch.go:642-652`、`websocket.go:585`）保留拦截：这是确定性的插件 bug，不会因数据库或缓存延迟触发。改动只是记录原因 `upstream_model_mismatch`、日志打 ERROR，状态码不变。

### 5.4 准入记录与额度观测

- 账号一旦放行（包括失败放行和 UNKNOWN 放行），都写 `c.availability[id] = availabilityAdmission{quota: clone(快照或 nil), models, now, reason}`。现在插件失败时根本不写（`:154-156`），WebSocket 的 `quotaGeneration` 会变成 0，必须改。
- 快照存在时，generation 来自核心自己的数据源，观测照常写回，符合 §44.7"使用核心捕获的 generation"。
- 快照为 nil 时 generation 为 0，`applyQuotaObservation` 跳过（`:277`）；HTTP 请求等 guard 补上后再写。
- WebSocket 不回写：连接是在代次未知时建立的，会话中途换代次后再采用新代次，会把旧身份的观测写到新身份上（§44.7 禁止）。所以 `quotaGeneration==0` 的会话整个连接期间都不写观测，`websocket.go:675-680` 只把比较改为 `sameQuotaIdentity`，不做 0→N 回写。（目前唯一声明 `modelAvailability` 的 ccgateway 没有 WebSocket 端点，这条只影响测试和以后的插件。）

### 5.5 调用点

| 位置 | 改动 |
|---|---|
| `dispatch.go:73`、`websocket.go:438` | 不改调用（预检只会返回 429） |
| `dispatch.go:489/:639`、`websocket.go:405/:582` | 不改调用，guard 内部按 5.3 |
| `dispatch.go:642-652`、`websocket.go:585` | 加原因码与 ERROR 日志 |
| `websocket.go:675-680` | 改用 `sameQuotaIdentity`，不回写（见 5.4） |
| `failure_policy_runtime.go:230-235`（`planFailure`） | `RecordType==model_availability` 的结果跳过 `decideFailure`，只排除该账号后换号；否则命中 429/503 规则会冷却或停用整个账号，违反"专属窗口只换号、不冷却整个账号" |
| `failure_policy_runtime.go:153` | 改注释（它刷新的是账号状态，不重跑可用性检查） |

## 6. C：可排查

### 6.1 记录：`anomalies.availability`（不加迁移）

不用 `sched_decisions`（语义是 scheduler.rank 改写链，§24.5，`rank.go:178` 会清空，接口不返回），不用 `plugin_detail`（插件原样写入，上限 4 KiB）。与 `failure_policy` 同构，值为 JSON 字符串：

```go
type AvailabilityTrace struct {
    V          int                  `json:"v"`       // 1
    Outcome    string               `json:"outcome"` // blocked_all | partial | fail_open
    Candidates int                  `json:"candidates"`
    Admitted   int                  `json:"admitted"`
    Counts     map[string]int       `json:"counts"`  // 原因 -> 账号数
    RetryAt    int64                `json:"retry_at,omitempty"`
    Samples    []AvailabilitySample `json:"samples"`
    Omitted    int                  `json:"omitted,omitempty"`
    Guard      []AvailabilitySample `json:"guard,omitempty"`
}
type AvailabilitySample struct {
    AccountID   int64  `json:"account_id,omitempty"`
    AccountName string `json:"account_name,omitempty"` // 只在读取时由 API 填，不落库
    Plugin      string `json:"plugin,omitempty"`
    Model       string `json:"model,omitempty"`
    Decision    string `json:"decision"` // block | fail_open
    Reason      string `json:"reason"`   // 原因码，插件原因用 "unavailable:quota_rejected:7d_fable" 的形式
    RetryAt     int64  `json:"retry_at,omitempty"`
    Attempt     int    `json:"attempt,omitempty"` // 仅 guard
}
```

- 放在 `core/ports_billing.go`，`UsageRecord.Availability *AvailabilityTrace`，`Anomalies()` 增加键 `availability`。
- 只要有拦截或失败放行就记，全部正常放行时为 nil（绝大多数记录不变）。
- 上限：`Samples` 最多 20 条（每个原因至少一条，其余按候选顺序，多出的计入 `Omitted`）；整批失败放行只记一条带插件、不带账号的样例；`Counts` 最多 16 个键；`Guard` 最多 5 条；插件原因截到 64 字节并去掉控制字符，模型截到 100 字节。整串约 3 KiB。
- WebSocket 每一轮都是新的 call 和新的记录（`websocket.go:309` `newCall`），每轮各记各的 trace；guard 追加到本轮记录的 `Guard`。
- 汇总统计的 `anomalous` 表达式（`usage/api.go:621`）排除 `availability`，与 `failure_policy` 一样不计为异常。
- 全部被拦时 `account_id` 保持 NULL（与 sub2api 一致）：它表示"由哪个账号服务或尝试"，账号用量、成功率都按它统计，不能算到从未发出请求的账号头上。账号 ID 在 `samples` 里。guard 拦截已选中账号的，仍按现有 `lastAccount` 记录。

### 6.2 脱敏（`usage/api.go` 的 `detail()`）

| 调用者 | 处理 |
|---|---|
| 自助 `/me/usage/:id` | 不变，整个 anomalies 为空（`:460`） |
| 管理员无 `account:read` | `redactAnomalies`：删去 `availability` 样例和 `failure_policy[]` 的 `account_id`；保留原因、模型、计数、retry_at |
| 管理员有 `account:read` | 一次查询回填 `account_name` |

顺带修复现有漏洞：无 `account:read` 的管理员现在能在详情的 `failure_policy[].account_id` 里看到账号（`api.go:469-474` 只隐藏了顶层字段），违反 §15.2。

`error_message` 继续用给客户端的通用文案，不拼原因（429 的消息自助用户可见）。

### 6.3 日志

新增 `gateway/logsample.go`：`warnSampler`，每个 key 每 60 s 最多放行一条，并带出期间被压掉的条数；超过 1024 个 key 整体清空；只加锁计数，没有 I/O。

| 事件 | 级别 | 采样 key | 字段 |
|---|---|---|---|
| 全部拦截 | WARN `gateway: no candidate passed model availability` | group+model+首要原因 | request_id, group, model, status, candidates, counts, samples（前 5 个，含剩余秒数）, suppressed |
| 失败放行 | WARN `gateway: model availability check failed open` | plugin+原因 | request_id, plugin, reason, accounts, err, suppressed |
| 部分拦截 | DEBUG | 不采样 | 同全部拦截 |
| guard 拦截 | INFO | plugin+原因 | request_id, account, model, reason, attempt, suppressed |

### 6.4 控制台

- 新组件 `views/usage/UsageAvailabilityTrace.vue`：`SCard variant="flat"` + `SDisclosure variant="section"`，与钩子、失败策略卡片一致；`blocked_all`/`fail_open` 默认展开；结论徽标、候选数/放行数/最早可重试、按原因计数、样例列表（账号用 `accountLabel()`，无权限时显示"无账号查看权限"）、guard 小节。
- `usage.ts` 新增防御式解析 `availabilityTrace(u)` 和原因文案 `availabilityReasonLabel`（`quota_rejected:7d_fable` → "额度窗口 7d_fable 已耗尽"，未知原因显示原文）。
- `UsageDetail.vue`：在失败策略卡片前插入该组件；错误提示框在 `model_availability` 且有 trace 时加一行"原因见下方"。
- i18n：`locales/{zh,en}/usage.ts` 新增 `availability.*`。
- 列表的"详情"列沿用已在工作区的 `UsageErrorText`（`status_code=…, 消息`），一起提交。

## 7. D：额度写入不再清空分组快照

现在每次被动写额度（每账号每节点最多 30 s 一次，`quota.go:53`；两节点约 30 个活跃账号）都调用 `s.changed` → `invalidate()`（`service.go:128,213-219`），清空本机和其他节点的分组快照和 ccgateway 请求缓存，下一个请求的 `Candidates`（`directory.go:82-98`）就要现查 PG，ccp 上同样会遇到冷连接。ccgateway 还会因此关闭该账号的隧道路由并清掉 blocked 标记（`ccgateway/request_cache.go:326-330`），下一个请求重建。

- 新增 `core.PublishAccountQuotaChanged(ctx, bus, id)`，发 `{"account_id": id, "kind": "quota"}`（现有 `PublishAccountChanged` 序列化 `map[string]int64`，放不下 `kind`，`ports_cluster.go:120`）。额度写入（`service.go:128`、`quota_refresh.go:186,403`）改用它，本机只调用 `invalidateQuota(id)`。
- 账号服务（`service.go:190`）和 ccgateway（订阅在 `ccgateway/accounts.go:556`，处理函数 `request_cache.go:428`）的回调遇到 `kind=quota` 时，只失效额度镜像，不清目录快照、不清 ccgateway 请求缓存、不关隧道。
- 已核对：分组快照 SQL（`directory.go:92-98`）、账号快照 `Load`（`:138-177`）、ccgateway 请求缓存都不含额度数据；订阅 `account:changed` 的只有这两处；状态变化（冷却、自动禁用）另外调用 `changed`（`automatic_state.go:232/319`、`ccgateway.go:292`），冷却存在 Redis、`Candidates` 每次现读（`directory.go:43-59`），所以额度写入不需要让它们失效。
- 兼容：旧节点不认识 `kind`，照旧整体失效，只是多查一次库。新旧节点混跑没有正确性问题。

## 8. 测试

**account（假加载函数）**：命中零调用；回填中按 id 失效时，失效后发起的读取不合并到旧读取、旧结果不写入缓存但交给等待者；全量重载不覆盖、不删除重载期间新填入的条目；无 id 载荷全部失效、带 id 只失效一个；`kind=quota` 不清目录快照；加载出错返回旧值并标 `Stale`；冷未命中按 ctx 截止返回 `Missing`、后台完成后条目已填；50 个并发未命中只加载一次；负缓存；单行无效只跳过一个；总线重连后全部失效。

**account（PG，sdk/testpg）**：写后立即可读（现有 `quota_refresh_test.go:398-412`）；触发器换代次后经 `changed` 读到新代次、旧代次观测影响 0 行；`Ensure` 换代次或新插入行会发通知；`initializeQuota` 新建行后能读到；两个 Service 共用库和内存总线，A 写 B 读到。

**会坏、要改写的现有测试**：
- `quota_refresh_test.go` 的 `TestQuotaSourceGenerationAndReadFailures`（`:507-536`）：它断言 ctx 取消时返回错误、坏行导致整批出错、直接调 `store.ResetAccountQuota`/`SaveAccountQuota`（不发通知）后立即读到新代次。镜像下三者都不成立，改为：ctx 取消返回 `Missing`；坏行只跳过一个；直接写库后需 `invalidateQuota` 或 `changed` 才读到。
- `TestQuotaUnknownScopedObservationRoundTrip`：直接调 `e.db.SaveAccountQuota` 后读，同样要先失效。
- `EnsureAccountQuota`、`initializeQuota` 改签名后，`quota_refresh_test.go`、`store/account_quota_test.go` 的调用要跟着改。
- `availability_test.go:379/418` 引用要删除的 `availabilityTimeout`，改用新的两个时限。

**gateway**：
- 改写 `TestAvailabilityFailuresDoNotFailOpen`（`availability_test.go:129`）为 `TestAvailabilityPluginFailuresFailOpen`：denied 仍 429，其余 12 种改为 200 且上游被调用，断言原因码。
- 改写 `TestAvailabilityBoundedBatches`（`:354`）：坏批次放行全部 140 个；新增"第 1 批 UNAVAILABLE、第 2 批坏掉，第 1 批拦截照常生效"；时间断言改用插件超时。
- `TestAvailabilityRetryAfterOnlyForCompleteDenial`（`:425`）的 `unknown_state` 断言 200。
- 两边 generation 都大于 0 的旧用例保持不变（`:202`、`:266`、`:468`、`:522`、`:534`）。
- 新增：失败放行保留快照 generation 并写回观测；nil 快照跳过观测；guard 在准入快照为旧值或 nil 时采用新值、不 503；guard 读快照缺失时沿用准入快照且不等待；本请求已失败放行时 guard 不调插件；熔断期间不发 RPC、10 s 后试探恢复；故障策略下 `model_availability` 结果不触发账号动作；WebSocket 连接时失败放行、后续轮次不写观测也不 503；模型名含空白或控制字符回 400；客户端断开时直接 canceled；快照读取 400 ms 时插件仍有完整 1 s；trace 的计数、上限、截断、guard 追加；WARN 合并与节流。

**usage**：PG 断言 `anomalies->>'availability'` 落库；自助无 anomalies；无 `account:read` 时 availability 与 failure_policy 都没有 account_id；有权限时回填 account_name；汇总异常计数不变。

**web**：`UsageAvailabilityTrace.spec.ts`（各结论徽标与展开、原因文案、未知原因原文、隐藏账号、损坏 JSON 不渲染）；`UsageDetail.spec.ts` 新增有无 trace 两种情况。

按规则所有 Go 模块都跑 `go test`，确认 PG 测试真的执行；Linux 专属代码在 ovh 容器里跑。

## 9. 文档

- CONTRACTS §44.7：
  - "已经声明而超时、返回空结果、缺少结果、重复或越界结果时不能放行" 改为 §5.1 的规则；
  - "没有首次成功快照时插件可返回 `UNKNOWN`" 改为"`UNKNOWN` 表示插件无法判断，核心放行；要拦截必须返回 `UNAVAILABLE`"；
  - "模型不可用是硬准入……不可被排序插件 fail-open 恢复" 保留，补一句"硬准入只针对插件明确的 `UNAVAILABLE`"；"新插件必须在不支持能力的旧核心上拒绝初始化，不能静默忽略准入限制" 保留，补一句"核心侧检查失败时放行，见上"；
  - 观测一条补充失败放行时的准入、guard 与 WebSocket 规则；
  - 新增"快照读取"（内存镜像、按 id 失效、10 分钟旧值上限、60 s 兜底、熔断）和"可观测性"（`anomalies.availability`、日志采样）；
  - 注明故障策略不对 `model_availability` 结果执行账号动作。
- CONTRACTS §15.2：模型名含空白、控制字符、超长或非法 UTF-8 时回 400。
- CONTRACTS §51.3：改为"Redis 额度缓存只服务控制台列表"，并按代码更正 DEL/fence 与微秒 mtime；代码注释里的"§44.9"改为"§51.3"。
- CONTRACTS §15.2：补充 anomalies 脱敏规则；§24.5：注明可用性预检不写 `sched_decisions`；`GATEWAY-FAILURE-POLICY-DESIGN.md` 同步 failure_policy 账号脱敏。

## 10. 发布

只发核心一个版本（含控制台）。插件、外壳不变，无迁移。两节点按现有流程（先 ovh 主节点）升级；新旧核心混跑期间 `kind=quota` 消息在旧节点上照旧整体失效，无正确性问题。

部署配置（可与核心发布同时做，不依赖代码）：ccp 的 DSN 加 `pool_min_conns=4`，`pool_max_conns` 从 10 调到 20，保持热连接。镜像只消除了额度读取，`Candidates`、账号 `Load`、计费等仍会查库，冷连接（约 430 ms）对它们同样有影响。

## 11. 代价与风险

- 窗口刚用完到其他节点镜像生效之间（通常不到 1 s，丢消息最多 60 s），这段时间里**每个**落到该账号的对应模型族请求都会多吃一次上游 429 再换号，不只一次。
- 镜像代次过期期间（同上，最长 60 s）该账号的观测写回被 `WHERE generation` 丢弃，期间新耗尽的专属窗口只能靠 429 发现。
- 数据库故障时旧值最多保留 10 分钟：已重新授权的账号，其旧身份的 `rejected` 专属窗口可能多拦这段时间（重新授权会清空窗口，但镜像读不到新值）；到 `resets_at` 后插件自己放行。
- 插件或镜像出错、熔断期间会放行本该被拦的请求（只限专属窗口刚耗尽的账号），后果同第一条。
- 模型名校验前移会让含空白或控制字符的模型名从"503"变成"400"，属于行为变化，但更准确。
- 插件升级窗口的 503 不在本方案范围内（见 §1）。
- 每节点多一份额度镜像，内存约每账号 1–2 KB。
