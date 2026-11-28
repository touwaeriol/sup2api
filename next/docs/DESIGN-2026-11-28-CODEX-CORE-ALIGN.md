# 核心随 codex 对齐官方 Codex CLI 的四处改动（2026-11-28）

所有者 2026-11-28 选“插件加核心一起做”。插件侧（codex 0.2.4）见 [PLUGIN-CODEX.md](PLUGIN-CODEX.md) 第 25 节；本文是同一批里要改核心的四件事。官方对照 openai/codex `codex-rs` 提交 065e18b0，下文路径相对 `codex-rs/`。原则：机制做成通用的（清单声明、核心执行），不在核心里写 codex 专用分支；无迁移，可滚动。

## 1. 401 立即续期并重发（HTTP 与 WebSocket 握手）

- 官方：401 → 重新加载凭证 → 刷新令牌 → 用新令牌重发同一请求；再 401 才报错（`login/src/auth/manager.rs` UnauthorizedRecovery、`core/src/client.rs` 401 处理）。刷新带 `refresh_reason: unauthorized_recovery`。
- 现状：插件把 401 分类为冷却 5 分钟；核心等每 5 分钟一轮的续期扫描（过期前 `beforeExpirySec` 内才续）；令牌提前失效时账号要冷却到扫描命中。
- 改为：账号类型声明了 `refresh` 时，一次请求里某账号收到上游 401（分类之前），核心对该账号做**一次**同步续期（复用 `account` 的续期流程与账号锁 `account:refresh:<id>`；锁被别处持有时等它结束后重读凭证，不重复刷新），成功后用新凭证在**同一账号**重建并重发这一次尝试（不计入换号次数）；续期失败或重发仍 401 时走原来的分类（冷却/禁用/换号）。每个请求每个账号最多恢复一次。
- 续期原因：`BuildRefreshRequestRequest` 增加 `reason`（枚举：定时扫描、管理员强制、保存后、401 恢复），codex 插件映射成官方的 `scheduled` / `forced` / `proactive_expiry` / `unauthorized_recovery`。其他插件忽略即可。
- WebSocket 握手 401 同样处理（握手前尚未向客户端写任何东西）。

## 2. 换号时不交账号相关的客户端头

- 官方：`x-codex-turn-state` 只在同一账号的同一轮内回放，账号变了就丢弃（`core/src/client.rs` `turn_state_with_auth_owner`）。
- 现状：插件按 `passHeaders` 把它作默认头转交；一致性要求每次尝试按同样输入构建，所以换号后的尝试也带着上一个账号的令牌。
- 改为：清单 `AccountPlatform` 增加 `accountBoundHeaders: [名字]`（必须是同一平台 `passHeaders` 的子集，清单校验）：核心交给插件的 `inbound_headers` 在换号后的尝试（attempt > 0 且账号与 attempt 0 不同）里去掉这些头。codex 两个类型声明 `["x-codex-turn-state"]`。插件不变（仍然只是把交来的头转交），一致性套件第 7 条照旧成立。

## 3. WebSocket 流内用量事件

- 官方：WebSocket 下解析流内事件 `codex.rate_limits`（`rate_limits.primary/secondary.{used_percent, window_minutes, reset_at}`，另有 credits、plan_type），`codex-api/src/rate_limits.rs`、`endpoint/responses_websocket.rs`。
- 现状：核心只在 HTTP 响应头和 WebSocket 握手（101）时按清单 `quota.headers` 采样；WebSocket 连着期间窗口不更新。
- 改为：清单 `quota` 增加 `events: [{type, windows: [{windowMinutes, utilization, utilizationUnit, reset}]}]`，字段值是事件 JSON 里的 gjson 路径，语义与 `quota.headers` 相同（窗口长度生成窗口名，`reset` 为 Unix 秒）。核心 WebSocket 中继收到 `type` 匹配的上游消息时采样（与响应头采样走同一个观察者、同一套窗口名），消息照常转发给客户端。只记录窗口，不据此禁用账号（禁用仍只看 429）。codex 两个类型声明 `codex.rate_limits` 的 primary、secondary。

## 4. WebSocket 上游连接可重连

- 官方：`websocket_connection_limit_reached`（单连接 60 分钟上限）、`previous_response_not_found` 当可重试；客户端新建连接重发（`codex-api/src/endpoint/responses_websocket.rs`）。
- 现状：需核实核心在上游连接关闭或发来这两个错误时的行为（是否连带关掉客户端连接）。
- 改为：上游连接因上述原因结束时，核心保留客户端连接，把这一轮的错误事件照常转给客户端（官方客户端据此重发），下一轮新建上游连接（重新调度、握手）。不在核心里替客户端重发请求体。426 回退 HTTP 是客户端的事，核心不做。

## 拆分

- A（HTTP 路径）：第 1、2 节；B（WebSocket 路径）：第 3、4 节，以及第 1 节的握手 401（复用 A 的续期恢复函数，合并时接线）。
- 新逻辑放新文件；`gateway/dispatch.go`（1126 行）、`gateway/websocket.go`（995 行）已超 500 行，只做接线，不再增大。函数不超过 30 行。
- 每项补测试，改 CONTRACTS（新节）与 PLUGIN-DEVELOPMENT，codex 清单与 PLUGIN-CODEX.md 第 25 节同步。
