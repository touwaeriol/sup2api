# 提示词审核插件重设计方案（含平台侧栏子导航）

- 分支：`proto/moderation-redesign`，基于 develop `ed988819c`，插件版本 moderation 0.9.3。
- 依据：评估报告 `MODERATION_EVAL_REPORT.md`。该报告未提交，只在 worker 本地。本文引用其中的问题编号 P1–P15、建议编号 R1–R8 和"结论②"。
- 路径约定：`M/` 指 `next/plugins/moderation/internal/moderation/`，`UI/` 指 `next/plugins/moderation/ui/native/src/`，`WEB/` 指 `next/web/`。
- 行号约定：标"现状"的行号指 `ed988819c` 上的代码，原型没有改动这些 Go 文件；标"原型"的行号指本分支 HEAD。

## 0. 总览

本方案分三层：

1. **平台层：插件页侧栏子导航。** 插件只需在 manifest 的 `ui.pages.<page>.nav` 里声明分组和条目，控制台就会像"系统设置"那样，用左侧侧栏列出这些条目。点击条目打开同一页的 `?tab=<key>`。
   - 这一层复用宿主现有的"侧栏视图"机制（`WEB/src/layouts/sidebarViews.ts`，CONTRACTS §90），控制台 UI 升到 3.14。
   - 旧控制台会忽略这个声明，插件可以保留页内标签作为回退。因此 **不需要提高 `hostUICompat`**。
   - 审核插件是第一个接入者。
2. **审核插件界面：** 原来横排的 10 个标签改为 5 组、16 个侧栏条目。原来一整页的设置拆成 7 个短页，每页 1–2 张卡片，不再需要长滚动。名单、模式等命名改成"说出它实际做什么"。
3. **审核逻辑：** 按评估报告逐条落地，最重要的四项是：
   - 流程增加"审核范围"，新建流程默认只审 system + user；
   - 命中重放只重放命中部分，新内容照常审；
   - 记录写入"匹配来源"和"处理方式来源"，绑定失效时给出显式提示；
   - 失败策略统一由一个设置决定。

原型已在本分支实现了前端、平台声明、校验和过滤；审核插件的 Go 后端没有改动。界面用 web mock 运行，Playwright 截图在 `design/shots/`（§6）。

> **范围变更（2026-11-12，用户）：不再分期，全部功能一次发布，直接滚动升级。** 原第 1–4 期、D5 名单合并、D10 都在 0.10.0 实现；所有迁移按 expand/contract 设计，本版本只加列、加表、回填，破坏性步骤留到下一版本（§10）。§4 各项里“走 maintenance”“分两步发版”的说法以 §10 为准。

### 决策点（D1–D5、D9、D10 用户已确认；D6–D8 随功能删除作废）

| # | 决策 | 推荐 | 备选 |
|---|---|---|---|
| D1 | 已有流程的"审核范围"怎么处理——**用户已确认（按推荐）** | **保持 `all`（旧行为），不自动改**。新建流程默认 `input`（与第 2 期后端同时上线）；未设置范围的流程在编辑页显示提示，由管理员手动切换 | 升级时把所有已有流程改成 `input`：误报立刻下降，但会悄悄缩小审核面 |
| D2 | `mode` 是否拆成"开关"和"默认级别"两个字段——**用户已确认（按推荐）** | **不拆，只改文案**（关闭 / 开启·默认后台审核 / 开启·默认先审后发），不需要迁移 | 拆成 `enabled` + `default_level`：概念更干净，但要做设置迁移和兼容读取 |
| D3 | 缓存命中的重放方式——**用户已确认（按推荐）** | **`new_content`：只重放命中部分，新内容照常送审** | `whole`：旧行为，整个请求直接按命中处理 |
| D4 | 审核失败时的处理——**用户已确认（按推荐）** | **默认 `open`（放行并记为出错，与现状一致）**，可选 `close_pre`（先审的请求返回 503） | 默认 `close_pre`：更安全，但上游一抖动就会拒绝用户 |
| D5 | 白名单、黑名单是否并入观察名单——**用户已确认；范围变更后本版本就并入** | 改名为"只后台审核" / "总是先审"，并在 0.10.0 并入观察名单，作为"锁定条目"。滚动期间设置里的两个名单仍是判定依据（旧节点照读），新节点把它们同步成锁定条目（§10） | 立即合并：要迁移数据，而且自动观察的规则要一起改 |
| D6 | AI 的权限 | **作废（功能删除）**：AI 分析整体删除（§9），不再需要 | — |
| D7 | AI 复核的触发与数量限制 | **作废（功能删除）** | — |
| D8 | 由谁决定名单调整 | **作废（功能删除）** | — |
| D9 | 删除后，历史 AI 结论和分析记录是否还在界面上展示（§9.3）——**用户已确认**：“根据你的建议来” | **第一步就不再展示**：界面只认人工处理；数据在迁移那一期之前留在库里但不读取，不导出（有自动备份） | 只读保留到迁移那期：要保留一套只读界面和接口，与"功能已删除"相矛盾 |
| D10 | 是否用一条固定规则替代 AI 的自动升级（§9.3）——**用户已确认：要加** | **加“窗口内违规 N 次自动升为拦截级”**。用户原话：“加上为多少期限内违规多少次升级为拦截，比如……十分钟、十秒钟，或者一小时、十五小时这样。” 违规次数 N（0 = 关闭，默认 0）和时间窗口（数值 + 单位：秒 / 分钟 / 小时，存为秒）；规则见 §9.3 | 原推荐“暂不加”，用户未采纳 |

---

## 1. 信息架构

### 1.1 新菜单树

审核插件在控制台主菜单中的入口不变（安全 → 提示词审核）。进入后，侧栏换成下面这棵树，顶部有"返回控制台"。

```
← 返回控制台
运行（monitor）
  ├ 概览            ?tab=overview   读
  ├ 审核记录        ?tab=events     读      （?record=<id> 直接打开详情）
  └ 在线测试        ?tab=test       moderation:manage
策略（policy）
  ├ 审核流程        ?tab=flows      读      （?flow=<id>|new 打开编辑器）
  ├ 流程绑定        ?tab=bindings   读
  ├ 执行策略        ?tab=policy     设置权限 ← 旧 ?tab=settings 跳到这里
  └ 缓存与输入      ?tab=cache      设置权限
名单与处置（users）
  ├ 观察名单        ?tab=watch      读
  ├ 封禁用户        ?tab=blocks     读
  └ 名单规则        ?tab=lists      设置权限
资源（resources）
  ├ 审核器          ?tab=auditors   读
  ├ 渠道            ?tab=channels   读
  └ Jev 默认题集    ?tab=jev        设置权限
系统（system）
  ├ 拦截提示        ?tab=response   设置权限
  ├ 记录与保留      ?tab=records    设置权限
  └ 高级            ?tab=advanced   设置权限
```

权限说明：

- "读"：页面本身需要的 `moderation:read`，在菜单入口那一层已经检查过，条目上不再重复声明。
- "设置权限"：条目上声明 `permission: moderation:manage` 和 `corePermission: plugin:manage`，任一满足即可见。这与现在 `canManageSettings()` 的判断一致（现状 `UI/host.ts` canManageSettings）。
- 过滤分两处：
  - 核心在 `GET /ui/plugins` 里按条目权限过滤（原型 `next/server/internal/plugin/api/ui.go:152` `visibleNav`）；
  - 插件自己再用 `visibleGroups(may)` 过滤一遍（原型 `UI/nav.ts`），旧控制台走页内标签时同样生效。

链接兼容：

- 旧的 `?tab=settings`：由 `nav.ts` 的 `RENAMED` 映射到 `policy`。核心插件详情页的"设置"链接（`/plugins/moderation/settings` 会跳到 `?tab=settings`）因此不用改。
- `?record=` 链接：没有 `tab` 时打开审核记录页。
- `?tab=watch&user=<id>` 直接打开该用户的详情抽屉，打开的抽屉也会写进地址（与 `?record=` 一致，原型 `UI/WatchTab.vue`）。
- 未知的 `tab` 或无权限的 `tab`：显示第一个可见条目。
- 页面会把解析出的分节**写回 `?tab=`**（用 replace，原型 `UI/nav.ts` `useSection`）。这样有三个效果：侧栏高亮的条目与页面一致；旧链接会变成新地址；关闭由 `?record=` 打开的详情后，仍停在审核记录页。
- 在页内切换分节时：侧栏模式用 push（与点击侧栏一致，可以后退）；标签模式用 replace。

### 1.2 每组的概念

与评估报告 §5 的概念模型对应：

- **运行**：看结果。
- **策略**：两件事，"审什么、怎么判"归流程，"什么时候审、失败怎么办"归执行策略。
- **名单与处置**：处理"人"。
- **资源**：审核器和渠道，是流程引用的积木。
- **系统**：对外响应、留痕、性能这类很少改动的设置。

---

## 2. 平台能力：插件页侧栏子导航（UI 3.14）

### 2.1 宿主"系统设置"现在怎么做

- 系统设置的页面列表在 `WEB/src/views/settings/settingsNav.ts`（`LAYOUT` 分组，每个条目有 `tab`、`perm`、`label`、`icon`）。`SettingsView.vue` 只按 `?tab=` 切换卡片，本身没有导航。
- 侧栏是**同一个侧栏换一套菜单**，即"侧栏视图"：
  - `WEB/src/layouts/sidebarViews.ts` 的 `SIDEBAR_VIEWS` 列出所有视图；
  - 路由用 `meta.sidebar` 声明自己属于哪个视图（`WEB/src/router/index.ts:84`）；
  - `AppSidebar.vue` 在视图页渲染 `SidebarViewMenu.vue`，内容是"返回控制台"加上 `view.useGroups()` 给出的分组。
- 手机端是同一个抽屉，桌面端支持折叠成图标。

### 2.2 复用方式：插件页注册成"动态侧栏视图"

原型的改动点：

| 层 | 文件 | 改动 |
|---|---|---|
| manifest | `next/sdk/manifest/manifest.go`（`Page.Nav`；`type PageNav` 在第 970 行） | `Page` 增加 `nav: {groups:[{key,label,items:[{key,label,icon,permission,corePermission}]}]}` |
| 校验 | `next/sdk/manifest/check/ui.go:289` `pageNav` | 只允许 native 页使用；key 匹配 `^[a-z][a-z0-9_-]{0,31}$` 且在整页内唯一；label 不能为空；icon 必须在图标表里；permission 必须是已声明的 userPermission。另在 `uiFloors` 加 3.14 一行（:522），不带 `uses`，因为旧控制台忽略这个声明不会出错 |
| 核心 | `next/server/internal/plugin/api/ui.go:152` `visibleNav` | 按调用者权限过滤条目，删掉空分组；复制后再改，不修改共享的 manifest |
| 控制台 | `WEB/src/layouts/pluginNav.ts`（新） | `usePluginPageView()`：当前路由是 `/p/<plugin>/<page>`、且该页声明了 nav 时，返回一个 `SidebarView`（key 为 `plugin:<plugin>/<page>`），入口是主菜单里指向这个页的条目。各条目的链接是 `?tab=<key>`，`activeSection()` 决定哪个条目高亮 |
| 控制台 | `WEB/src/layouts/sidebarViews.ts` | `useSidebarView()` 在没有路由视图时改用插件页视图（新增 1 行）；`SidebarViewKey` 扩展为 `'settings' \| plugin:${string}`。`AppSidebar.vue` 和 `SidebarViewMenu.vue` **不用改** |
| 类型 | `WEB/src/api/types.ts` | `UIPluginPage.nav` 及 `UIPageNav*` 类型 |
| 版本 | `WEB/packages/{host,ui,vite-preset}` | `HOST_UI_VERSION` 改为 3.14.0；README 新增"插件页的侧栏子导航（UI SDK 3.14）"一节；`ui-contract.spec.ts` 同步修改 |
| 契约 | `next/docs/CONTRACTS.md` §90.7 | 新增该声明的说明 |

为什么选择"manifest 声明"，而不是"插件运行时调用 SDK 注册菜单"：

- 核心可以按权限过滤，菜单在插件模块加载前就能显示；
- 插件不会碰到宿主的 DOM 或布局，侵入最低；
- 和现有的 `menus` / `pages` 声明方式一致。

### 2.3 兼容性与版本

- **旧核心 + 新插件：** 旧核心解析 manifest 时容忍未知字段（`sdk/manifest/check/corefloor.go` 注释："plugin/pkg.ParseManifest tolerates unknown fields"），会直接忽略 `nav`。插件按 `hostShowsNav(host)`（`host.version >= 3.14.0`）判断，回退为页内 `STabs`。所以审核插件的 **`hostUICompat` 保持 `^3.12`，`hostCompat` 不用提高**。
- **新核心 + 旧插件：** 没有 `nav`，行为完全不变。
- **新核心 + 新插件：** 控制台 3.14 使用侧栏；插件不再渲染标签，标题改为当前条目名加一句说明。
  - 声明了 nav 的插件页**总会**得到侧栏视图，即使调用者的主菜单里没有这个页的入口（例如通过链接打开）。否则在 3.14 控制台上，插件已经去掉了标签，分节就没有入口了（原型 `sidebarViews.ts` `useSidebarView`）。
  - 旧控制台的回退：其它分节照常做成标签，所有设置页合并成一个"设置"标签，标签内用分段切换各设置页，避免十几个标签挤在一行。
- **只有离开侧栏就不能用的插件**，才需要声明 `hostUICompat: "^3.14"` 和 `hostCompat >= 0.1.284`。校验器的 `uiFloors` 会检查这一对是否一致。

### 2.4 其它插件怎么沿用

| 插件 | 现状 | 建议 |
|---|---|---|
| dynweight | 3 个标签：status / events / config（`next/plugins/dynweight/ui/native/src/DynweightDashboard.vue:20`），`hostUICompat ^3.11` | 不急。条目在 5 个以内时，页内标签更轻；等配置页变长再接入。接入方式与审核插件相同：manifest 写 `nav`，页面读 `?tab=` |
| codex | 页内标签在账号表单里（`CodexAuthForm.vue:172`），属于表单内的流程切换 | 不适用，不是页面级导航 |
| volcengine | 已有多个独立页（assets、asset_groups 等），各自有菜单 | 可以把多页合成一个 native 页加 nav。也可以保持现状，因为侧栏视图只对"同一页的多个分节"生效 |
| 新插件 | — | 页面超过约 5 个分节、或者有设置分页时，用 `nav`，并保留页内回退（可直接参考 `UI/nav.ts` 的写法） |

**还可以抽象的地方**（第 1 期可选）：

- 把审核插件 `nav.ts` 里的 `sectionOf`、`visibleGroups`、`hostShowsNav` 和"页内回退"包装组件，提到 `@sub2api/ui`，例如做成 `usePageSections(nav, may)` 加 `<SPageSections>`，其它插件直接复用。
- 原型先放在插件内，是为了不扩大 UI SDK 的导出面。是否提升到 SDK，在第 1 期评审时决定。

---

## 3. 设置页重构

### 3.1 拆页方式

- 每个 `ui:section` 头增加 `page` 字段（原型 `next/plugins/moderation/forms/settings.ui.json`）。
- `settingSections()` 读出 `page`，`SettingsTab` 增加 `page` 属性，只渲染这一页的分节（原型 `UI/settingsForm.ts` `sectionsOf`）。
- 保存逻辑（`settingsMerge.ts`）不变：每页只提交自己的键，其余键按存储值原样回写，所以各页互不覆盖。
- 0.1 旧 API key 的清除提示只出现在 `policy` 页（`LEGACY_KEY_PAGE`）。
- `settingsForm.spec.ts` 新增一条校验："每个分节都属于 nav 中某个设置页，且每个设置页至少有一个分节"。

结果是 7 个短页（AI 分析页随功能删除，§9）：

| 页（侧栏） | 分节 | 字段数 |
|---|---|---|
| 执行策略 policy | 执行方式 | 2 |
| 缓存与输入 cache | 判定缓存与重放 / 送审输入上限 | 3 + 5 |
| 名单规则 lists | 固定名单 / 自动观察与封禁 | 2 + 5 |
| Jev 默认题集 jev | Jev 默认题集 | 1 |
| 拦截提示 response | 拦截响应（带预览） | 2 |
| 记录与保留 records | 记录内容 / 保留期 | 6 + 5 |
| 高级 advanced | 性能 / 告警与通知 | 4 + 3 |

### 3.2 每个配置项的去向

处理方式的含义：

- **保留**：位置变了，语义不变；
- **改名**：文案改正，存储键不变；
- **新增**：0.10.0 新增的键（后端与界面一起上线）；
- **停止读取**：死配置，代码不再读取；键留在 schema 里作为隐藏的 legacy 键（根 `additionalProperties: false`，库里已存的值保存时仍要通过核心校验），下一版本 contract 时再删（§10）；
- **移到流程**：概念上属于流程。

| 键 | 原分组 | 新位置 | 处理 | 默认 | 新文案（中 / 英）与说明 |
|---|---|---|---|---|---|
| `mode` | 运行模式与用户名单 | 执行策略 / 执行方式 | **改名**（D2） | off | 审核开关与默认处理 / Moderation and default handling：关闭 · 开启·默认后台审核（只记录） · 开启·默认先审后发（命中拒绝）。说明里写明决定顺序 |
| `fail_policy` | — | 执行策略 / 执行方式 | **新增**（R6/P9） | open | 审核失败时 / When moderation fails：放行并记为出错 · 先审的请求拒绝（503）。覆盖审核器出错、总超时和繁忙三种情况 |
| `verdict_cache_days` | 性能与缓存（折叠） | 缓存与输入 / 判定缓存与重放 | 保留；**最小值改为 0 = 关闭**（P11，后端与界面同版本） | 7 | 与旧字段 `cache_ttl_seconds=0` 合并 |
| `hit_replay` | — | 缓存与输入 / 判定缓存与重放 | **新增**（R2/P2，D3） | new_content | 缓存命中的重放 / Replaying a cached hit：只重放命中部分，新内容照常审核 · 整个请求按命中处理 |
| `cache_entries` | 性能与缓存 | 缓存与输入 | 保留 | 100000 | — |
| `min_chars`、`input_max_chars`、`input_max_chunks`、`input_overflow`、`input_max_images` | 输入范围（折叠） | 缓存与输入 / 送审输入上限 | 保留，**不移到流程** | 2 / 12000 / 8 / sample / 8 | 理由：放进流程就要加 spec 字段，旧节点的 `ParseSpec` 会拒绝；而且它们是按节点的内存保护，与流程无关 |
| `allowlist_user_ids` | 运行模式与用户名单 | 观察名单（锁定条目） | **改名并并入观察名单**（D5） | [] | 只后台审核的用户（从不拦截） / Background-only users (never refused)。0.10.0 仍是判定依据（旧节点照读），新节点同步成观察级锁定条目；在观察名单页锁定 / 解锁会回写这个键 |
| `blocklist_user_ids` | 同上 | 观察名单（锁定条目） | **改名并并入观察名单**（D5），同上，同步成拦截级锁定条目 | [] | 总是先审的用户 / Always check first。说明里写明"这不是封禁" |
| `auto_watch`、`watch_days` | 观察名单与封禁 | 名单规则 / 自动观察与封禁 | 保留；后端修复 P8（跳过只后台审核的用户） | T / 30 | — |
| `escalate_violations`、`escalate_window_seconds` | — | 名单规则 / 自动观察与封禁（紧挨封禁阈值） | **新增**（D10） | 0 / 3600 | 自动升为拦截级 / Escalate to enforce：窗口内违规 N 次（0 = 关闭）；窗口用“数值 + 单位（秒 / 分钟 / 小时）”的可复用时长控件编辑，存为秒 |
| `ban_threshold`、`ban_window_hours`、`ban_duration_hours` | 同上 | 同上 | 保留。封禁窗口暂不改用时长控件：要新增秒级键并兼容旧键 `ban_window_hours`（旧节点只读旧键），列为后续 | 0 / 24 / 24 | — |
| `analysis_*`（10 项） | AI 分析 | — | **停止读取**（AI 分析整体删除）：隐藏的 legacy 键，代码不读；下一版本从 schema 删除（§9.4、§10） | — |
| `jev_default_set` | Jev 默认题集 | 资源 / Jev 默认题集 | 保留（独立成页，挂在资源下，靠近审核器） | null | — |
| `block_status`、`block_message` | 拦截响应 | 系统 / 拦截提示 | 保留（带预览） | 403 / 英文默认 | — |
| `record_pass`、`record_overturned`、`record_errors`、`store_text`、`record_pass_input_message`、`error_text_max_chars` | 记录与保留 | 记录与保留 / 记录内容 | 保留 | F/T/T/T/T/2000 | — |
| `*_retention_days`、`stats_retention_days` | 记录与保留 | 记录与保留 / 保留期 | 保留 | 180/30/7/3/90 | — |
| `max_concurrency` | 性能与缓存 | 高级 / 性能 | **改说明**（P10） | 16 | 写明"先审另有 max(64, 4×此值) 个名额"（`M/plugin.go:420`） |
| `queue_size`、`max_inflight_mb`、`ttft_timeout_ms` | 性能与缓存 | 高级 / 性能 | 保留；`max_inflight_mb` 超限的处理改由 `fail_policy` 决定 | 1000 / 128 / 10000 | — |
| `error_rate_alert_percent`、`notify_webhook_url`、`notify_webhook_secret` | 告警与通知 | 高级 / 告警与通知 | 保留；webhook 地址不合法时**在保存阶段报错**（P12） | 5 / "" / "" | — |
| `on_error` | 0.1（隐藏） | — | **停止读取**（P9；被 `fail_policy` 取代），键保留 | — | 被 `fail_policy` 取代 |
| `exempt_user_ids`、`cache_ttl_seconds` | 0.1（隐藏） | — | **保留读取**：没有重新保存过的设置仍靠它们回退（白名单、缓存天数）；下一版本先把存储值写进新键，再删 | — | 被 allowlist 和 `verdict_cache_days=0` 取代 |
| `base_url`、`api_key`、`model`、`system_prompt`、`categories`、`tool_choice`、`max_turns`、`temperature`、`max_tokens`、`timeout_ms`、`sample_rate`、`group_ids`、`model_patterns`、`retention_days` | 0.1（隐藏） | — | 只在 0.1 设置的一次性转换里读取（`legacy.go`），逐项评估后能停的停止读取；键保留到下一版本 | — | — |
| （流程）`sample_rate` | 流程配置 | 流程 | 保留；文案统一为"后台审核抽样比例"（P6） | 100 | 先审永远全量（`M/hook.go:251`） |
| （流程）`total_timeout_ms` | 流程配置 | 流程 | 保留；说明里写明"先审最多等 29 s"（P6，`M/hook.go:375`） | 0 = 自动 | — |
| （流程）`scope` | — | 流程 / 审核范围 | **新增**（R1/P1，D1）。存在 `flows.scope` 列而不是 spec JSON 里（旧节点的 `ParseSpec` 拒绝未知字段），像启用开关一样立即生效 | 新建为 input，已有流程（NULL）视为 all | 可选 input / tools / all；`all_but_own` 本版本不提供：核心没有响应结束钩子 |

---

## 4. 逻辑与配置优化：问题逐条映射

关于"能否 rolling 发布"：

- 插件是**每个节点各自升级**的。新旧版本混跑期间，旧节点读到新写入的数据可能出错。
- 凡是新增数据库列的改动都需要迁移，按约定**发布要走 maintenance**。

### 4.1 审核范围（R1 / P1 / 结论① / 结论② 第 3 点）

- **现状：** 抽取范围是"客户端可控的全部内容"，包括 assistant 历史、tool_use、tool_result，而且没有开关（`M/extract.go:169-222`、:289-292；CONTRACTS.md:5165）。
- **目标行为：** 流程新增 `scope`，取值如下：

  | 值 | 送审内容 |
  |---|---|
  | `input` | system、developer、user 文本，以及 user 的图片和文档 |
  | `tools` | `input` + tool_result |
  | `all_but_own` | 全部内容，但跳过网关自己返回过的 assistant 片段 |
  | `all` | 全部内容（现状） |

  工具定义（`tools`）只在 `all` 和 `all_but_own` 下送审。
- **后端改动点：**
  - `M/flow.go:18-23`：`Spec` 加 `Scope`；`ParseSpec` 把缺省值视为 `all`。
  - `M/hook.go:233`：`extractPieces` 之后，按 `flow.Spec.Scope` 用一个角色谓词过滤 `pieces`，抽取函数本身不动，实现上只是一张 `role → allowed` 表。
  - `M/snapshot.go` 的 `fingerprint` 要包含 scope，否则改了范围后旧缓存仍会生效。
  - `all_but_own` 需要新增"出站登记"：上游响应结束后，把 assistant 输出的规范化文本哈希写入 KV（TTL 与判定缓存一致），抽取时跳过命中的 assistant 片段。这需要网关在响应结束时提供一个钩子；如果核心没有，这一档推迟到后续期。
- **前端改动点（原型已实现）：**
  - `UI/types.ts` 增加 `SCOPES` 和 `Spec.scope`；
  - `UI/flowOps.ts` 的 `canonicalSpec` **只在范围被收窄（不是 `all`）时才发送 `scope`**。这样在现有后端上，没人改过范围的流程照常保存和发布。
    - 只有选了收窄的范围，现有后端才会返回 400，这一步要等第 2 期后端完成。
    - 新流程是否默认 `input`，与后端一起在 D1 决定，原型里新流程没有默认值；
  - `UI/flowValidate.ts` 增加 `invalid_scope`；
  - `UI/FlowConfigCard.vue` 增加"审核范围"下拉：
    - 未设置（沿用旧行为）时显示蓝色提示，建议切换；
    - 显式选择 `all` 或 `all_but_own` 时显示黄色警告。
- **迁移 / rolling：**
  - 不需要数据库迁移，spec 存在 JSON 里。
  - **不能直接 rolling。** 旧节点的 `ParseSpec` 使用 `DisallowUnknownFields`（`M/flow.go:57-62`），读到带 `scope` 的已发布 spec 会判为无效，并把这个流程丢掉（`M/snapshot.go:330-333`，这是现状代码），对应分组就会回退或不审。
  - 做法：第 2 期分两步发版。第一步让后端认识 `scope` 但界面不提供；全部节点升级后，第二步再开放界面。或者在界面上根据状态接口返回的"全部节点版本 ≥ X"来决定是否显示这个字段。
- **风险：** 缩小范围会减少对"伪造 assistant 历史"这类攻击的防护。所以 D1 推荐已有流程保持 `all`，由管理员显式切换。

### 4.2 命中重放（R2 / P2）

- **现状：** 请求里包含某个命中集合的全部片段时，整个请求直接按命中处理，新内容不再审（`M/hook.go:306-319`）。
- **目标行为：** `hit_replay = new_content`（默认）：
  - 命中集合照常记一条"重放命中"；
  - 先审时依然拒绝，但提示文案说明"历史中含已命中内容，请清理上下文"；
  - 后审时，集合之外的新片段照常进入 `makePlan` 送审。
- **后端改动点：** 在 `planUncached`（`M/hook.go:306-333`）里，`cached.hit != nil` 时不再立即返回：
  - 先审：维持拒绝，`deny` 的 message 可配置；
  - 后审：重放记录照写，同时把命中集合的 key 视为"已处理"，余下片段继续走 `makePlan`。

  需要补测试 `verdict_cache_*_test.go`。
- **前端：** 设置项 `hit_replay` 放在"缓存与输入"页（原型已有）。
- **迁移 / rolling：** 不需要迁移；设置键是新增的，旧节点会忽略（`DecodeSettings` 对未知键宽松）。可以 rolling。
- **风险：** 先审的语义不变，后审会多送审一些内容，成本略增。

### 4.3 先审 / 后审的决定收拢（R3 / P3 / P4 / 结论② 第 1 点）

- **现状：** 五个来源共同决定先审还是后审：
  - 黑名单 → 白名单 → 观察名单级别 → mode 推出的默认级别（`M/hook.go:173-194`、`M/settings.go:536-539`）；
  - 另外，审核管理员照常审核（`M/staff.go:9-12`）。

  记录上只显示全局 mode（`M/hook.go:256`），前端把它当作标签展示（现状 `UI/EventDetail.vue:173`）。
- **目标行为：**
  - 把"处理级别"作为唯一的概念：每个请求的级别只能是 observe 或 enforce，并附带**来源**（blocklist / allowlist / watch / default）。
  - 流程不再暗示时机。
  - 记录详情不再显示全局 mode 标签，改为"处理方式"一行，例如"先审 · 来自全局默认处理"。
- **后端改动点：**
  - 抽出纯函数 `decideHandling(cfg, watch, uid) → {level, source}`，替换 `preModerated` 和 `effectiveLevel`（`M/hook.go:173-194`）；
  - `eventRec` 加 `LevelSource`、`Level`；
  - `events` 表加列，见 §4.4 的迁移。
- **前端（原型已实现）：**
  - `UI/EventDetail.vue` 在记录带有 `level_source` 时，用"处理方式"一行代替 mode 标签；旧记录仍显示 mode 标签；三行新字段只在有值时出现；
  - `mode` 文案改为"默认后台审核 / 默认先审"（`UI/messages.ts` 的 `mode.*`）；
  - 流程卡片上的"审核时机"说明改用新名单名称。
- **迁移：** 需要（`events` 加列，与 §4.4 合并为同一个迁移）。
- **风险：** 低。

### 4.4 流程匹配来源与绑定失效提示（R4 / P5 / P14 / 结论② 第 2 点）

- **现状：** 被绑定的流程停用、未发布或已删除时，`bindGroup` 不写入，分组静默回退到默认流程（`M/snapshot.go:118-131`）。记录不保存匹配来源。
- **目标行为：**
  - 记录写入 `flow_source`（group / account / account_any / default / fallback），fallback 时带上被绑定流程的 ID 和原因；
  - "流程绑定"页把失效的绑定标红，并显示"实际生效：默认流程"；
  - 状态接口给出失效绑定的数量，概览页提示。
- **后端改动点：**
  - `snapshot` 在 `bindGroup` 和 `bindAccount` 时记下 `fallbackOf[group] = {flowID, reason}`；
  - `flowFor` 和 `flowForAccount` 返回 `(flow, source)`；
  - `hook.go:108` 和 `account_binding.go:52` 把 source 写进 `rec`；
  - `routes_bindings.go` 在列表里返回 `effective_flow_id` 和 `fallback_reason`。
- **前端：**
  - 原型已实现：`UI/EventDetail.vue` 的"匹配来源"一行，以及 fallback 时的黄色提示。
  - 待做：`BindingsTab.vue` 标红失效绑定。
- **迁移：** `events` 加列 `flow_source text`、`flow_fallback_id bigint`、`flow_fallback_reason text`、`level_source text`、`level text`，作为迁移 0011，全部是可为空的 `ADD COLUMN`。**按约定走 maintenance。**
- **风险：** 低。旧记录这几列为空，前端已处理为"不显示"。

### 4.5 多级"复核链"的语义与命名（R7 / P7 / P13 / 结论② 第 4 点）

- **现状：**
  - 普通级是复核链：第 1 级通过就结束，后续级只在前一级命中时才跑（`M/flow.go:412-458`）。
  - 级名按审核器类型推出（`UI/flowOps.ts:269-273`，后端在 `M/analysis_prompt.go:52` 有重复实现，这个文件随 AI 分析删除，见 §9）。
  - 第 1 级全部 skipped 时，补审级也不跑（`M/flow.go:436-438`）。
- **目标行为：**
  - 级名只按语义取：第 1 级叫"初审"，后续普通级叫"复核"，fallback 级叫"补审"，不再出现"行为审查"。
  - 界面画布上标注"通过即结束，命中才进入下一级"。
  - 第 1 级全部 skipped 时，改为继续运行补审级。这是行为变化，单独列为一项。
- **后端：**
  - `stageRole` 改为只看 index 和 fallback；
  - 最好由后端在 `flow` 接口里返回 `role`，前端不再自己推导，消除重复实现；
  - 修改 `flow.go:436` 的分支，并补测试。
- **前端：** `flowOps.ts` 的 `stageRole` 删掉按类型推导的分支；`messages.ts` 的 `stagesDesc` 补充说明。
- **迁移 / rolling：** 不需要迁移。改动 `flow.go:436` 会改变判定结果，各节点短时间内可能不一致，影响很小，可以 rolling。
- **风险：** 补审级比以前多跑一些，成本略增。

### 4.6 统一失败策略（R6 / P9）

- **现状：** 整体失败放行（`M/flow.go:417`、:592，`M/hook.go:418`，manifest 中 `failure: open`），唯独处理中字节超限时对先审用户返回 503（`M/hook.go:130-139`）。`on_error` 已失效（`M/legacy.go:52`）。
- **目标行为：** `fail_policy`：
  - `open`（默认）：所有失败都放行并记为出错，**包括繁忙**。这改变了现在繁忙时返回 503 的行为，需要在发布说明里写明。
  - `close_pre`：先审的请求在出错、超时、繁忙时都返回 503。
  - 后审始终放行。
- **后端：**
  - `hook.go` 的三处（busy、`runPre` 中 `ctx.Done`、最终 `verdict == error`）统一走 `failResponse(cfg, m)`；
  - 删除 `on_error` 的解码（`M/settings.go:483`）。
- **前端：** 原型已有 `fail_policy` 设置项。
- **迁移 / rolling：** 不需要迁移，可以 rolling。
- **风险：** 改默认的繁忙行为后，过载时会多放行一些请求。

### 4.7 名单概念精简（R3 / P8 / 结论② 第 5 点 / D5）

- **现状：** 有五个概念：白名单（只观察）、黑名单（总是先审）、观察名单、封禁、审核管理员。`auto_watch` 会把白名单用户加成拦截级（`M/store.go:297-301`），手动这样做却被禁止（`M/watch.go:237-245`）。
- **目标行为：**
  - 第 1 期只改名："只后台审核"和"总是先审"，原型已实现；
  - 修复 `auto_watch`，跳过只后台审核名单里的用户；
  - 第 4 期把两个名单并入观察名单，成为"锁定条目"（级别固定、不过期），设置里不再有这两个列表。
- **后端：**
  - 第 1 期：`store.go:297` 加判断 `!c.allowlisted(uid)`；
  - 第 4 期：`watchlist` 加 `locked bool`，迁移把两个列表转成锁定条目，`preModerated` 只看观察名单级别加默认级别。
- **迁移：** 第 4 期需要迁移（maintenance），而且旧节点读不到锁定条目，所以不能 rolling，需要一次性切换。

### 4.8 其余问题

| 问题 | 现状 | 目标 | 后端 | 前端 | 迁移 / rolling |
|---|---|---|---|---|---|
| P6 抽样只对后审生效、先审被截到 29 s | `M/hook.go:251`、:375 | 界面明确说明 | 无 | 原型已在流程卡片的说明里写明 29 s 截断；抽样说明已有 | 无 / 可以 |
| P10 `max_concurrency` 文案 | `M/plugin.go:420` | 说明写明先审另有名额 | 无 | 原型已改 | 无 / 可以 |
| P11 判定缓存在界面上关不掉 | `M/settings.go:333-344`（最小值 1） | 0 = 关闭 | `verdictCache` 的最小值改为 0；0 时不查、不写缓存 | 与后端同期修改：schema 最小值改为 0，`verdictCacheOff` 认 0。原型**没有**改，因为现有后端会把 0 夹到 1 天，界面会给出错误承诺（审查 H2） | 无 / 可以，但后端必须先于界面上线 |
| P12 设置被静默降级 | `M/settings.go:414-416`、:434-435 | 保存时直接报错 | schema 加 `maxLength`（guidelines）和 `pattern`（webhook，已有），解码时保留截断作为兜底 | 无 | 无 / 可以 |
| P15 代码结构 | 见评估报告 §6 | 决策集中成纯函数 | `decideHandling`（§4.3）、`failResponse`（§4.6）、`scopeFilter`（§4.1），都放在 hook 入口 | `nav.ts` 统一声明 | — |
| 记录三个标签 | `UI/EventDetail.vue:171-173`（现状） | 只保留"结论"和"时机"，mode 标签去掉，改为"处理方式"一行 | §4.3 | 原型已实现 | 同 §4.3 |

---

### 4.9 可选后续：审核本身的 `max_concurrency` 是否也默认不限（未改，待定）

AI 分析已删除（§9）。审核本身的并发设置 `max_concurrency` 这次**不改**，这里只列出风险，供以后决定。

**现状**（行号指 `ed988819c`）

- `max_concurrency`（默认 16，解码时夹到 1–256，`M/settings.go:365`）同时决定两件事：
  - 后台审核的工作协程数，即 post 池（`M/plugin.go:207`、:451-459）；
  - 每个节点同时进行的审核器调用名额（`M/plugin.go:166`）。
- 先审另有名额 `max(64, 4×max_concurrency)`（`M/plugin.go:167`、:420）。
- 每次审核器调用都要先拿到名额（`M/engine.go:278`）；在渠道超时内等不到，这个审核器就记为超时出错（:279-281）。
- 内存保护与此独立：
  - `max_inflight_mb`：处理中的请求超过这个字节数时，先审的请求返回 503，后台审核的请求直接放行（`M/hook.go:130-139`）；
  - `queue_size`：后台队列满时丢弃并放行（`M/hook.go:149-151`）。

**如果改为 0 = 不限**

- 改动点：
  - `acquire` 在不限时直接返回（`M/plugin.go:428-441`）；
  - post 池改为每个任务一个 goroutine，正在运行加排队的总数仍由 `queue_size` 封顶（`M/plugin.go:451-459`）；
  - 解码时允许 0（`M/settings.go:365`）；
  - 不限时先审也不再有单独的名额。
- 风险：
  - **上游限流**：审核器调用**没有任何重试**。429 会直接成为出错（`M/upstream.go:98` 的 `ErrType`）。这一级记为失败并跳过，结果是失败放行（§4.6）。不限之后，突发流量会把审核器渠道打满，**放行的请求随之增多**。这是最大的风险。
  - **内存**：`max_inflight_mb` 和 `queue_size` 仍然有效，必须保留，它们才是内存的真正保护。
  - **goroutine 数量**：
    - 先审的 goroutine 数等于网关的并发请求数，本来就由网关控制；
    - 后台审核的 goroutine 数由 `queue_size` 封顶；
    - 每个任务还会对每个审核器各开一个 goroutine（`M/flow.go:494-496`），这个数量不变。
- 建议：如果要改，先给审核器调用加上 429 重试（按 `Retry-After`，可参照原 AI 分析的单步重试 `M/analysis.go:247-280`），或者在渠道层面加限流，然后再把默认值改为不限。

## 5. 分期实施计划

每期都可以独立上线。

| 期 | 范围 | 主要文件 | 估算改动量 | 迁移 | rolling | 测试要点 |
|---|---|---|---|---|---|---|
| **第 1 期：平台侧栏加界面整理** | UI 3.14 插件页 nav（平台）；审核插件改为侧栏；设置拆成 7 页；改名与文案修正（P6/P7/P10，记录标签）；后端小修 P8（`auto_watch`）和 P11（缓存可设 0）；**AI 分析第一步：停用并隐藏**（§9.4：删除 Go 代码、路由和任务，`analysis_*` 改为隐藏的 legacy 键，界面移除） | sdk manifest/check、server plugin/api/ui.go、`WEB/src/layouts/pluginNav.ts`、`sidebarViews.ts`、packages 版本；`UI/nav.ts`、`ModerationDashboard.vue`、`SettingsTab.vue`、`settingsForm.ts`、`forms/*.json`、`messages.ts`、`EventDetail.vue`；`M/store.go`、`M/settings.go` | 平台约 +500/-20 行（含测试），插件 UI 约 +600/-250 行，Go 约 +20 行；删除 AI 分析约 −2 900（后端）、−600（测试）、−1 000（前端）行 | 无 | 可以（核心 0.1.284 + 插件 0.10.0；旧控制台自动回退到页内标签） | `nav.spec`（manifest 与 nav.ts 一致）、`pluginNav.spec`、`navigation.spec`、`ui-contract.spec`、`check/ui_test`、`api/unit_test`；`settingsForm.spec`（分页覆盖）；手动验证旧链接 `?tab=settings` 和 `?record=` |
| **第 2 期：审核范围加命中重放** | `Spec.scope` 与角色过滤、指纹包含 scope；`hit_replay=new_content`；`fail_policy` | `M/flow.go`、`hook.go`、`extract.go`（只加过滤表）、`snapshot.go`、`settings.go`；`UI/FlowConfigCard.vue` 等（原型已有） | Go 约 +250 行（含测试），前端少量 | 无 | **分两步发版**：先让后端认识 `scope`（界面不提供），全部节点升级后再开放界面 | 各协议抽取在不同 scope 下的矩阵测试；缓存指纹随 scope 变化；重放后新内容是否送审；先审拒绝的语义不变 |
| **第 3 期：可观测性** | `flow_source`、`level_source` 入库；绑定页标出失效绑定；概览提示 | `M/snapshot.go`、`hook.go`、`account_binding.go`、`store.go`、`routes_bindings.go`、`migrations/0011_*.sql`；`UI/BindingsTab.vue`、`EventDetail.vue`、overview | Go 约 +200 行，SQL 约 10 行，前端约 +150 行 | **有**（events 加 5 列，可为空） | 走 maintenance | 迁移前后记录的读取；fallback 的三种原因；account 与 group 的优先级 |
| **第 4 期：清理与合并** | 名单并入观察名单（锁定条目）；删除 0.1 旧字段和 `on_error`；`decideHandling` 收拢；可选把输入上限移到流程；**AI 分析第二步：迁移删除** `user_analyses`、名单行的分析列、记录的 `ai_*` 列和 `analysis_*` legacy 键（§9.4） | `M/settings.go`、`legacy.go`、`hook.go`、`watch*.go`、`store.go`、migrations；`forms/*`、`WatchTab.vue` | Go 约 +300/-400 行 | **有**（watchlist 加 `locked`，名单数据迁移） | 不能 rolling，一次性切换，走 maintenance | 迁移后先审 / 后审的判定与迁移前逐用户对比 |

---

## 6. 原型

运行方法：

```
cd next/plugins/moderation/ui/native && node node_modules/vite/bin/vite.js build
cd next/web && node node_modules/vite/bin/vite.js --mode mock
# 浏览器打开 /p/moderation/dashboard，用户 admin@example.com / admin
```

- mock 直接读取插件 manifest 的 `ui.pages`（含 nav）和真实的 `forms/*.json`；记录 mock 里带有 `flow_source`、`level_source` 和 `scope`。
- 截图在 `design/shots/`，共 28 张，脚本是 `design/shots.mjs`。重拍方法：在一个装有 `playwright-core@1.56.0` 的目录里运行 `mcr.microsoft.com/playwright:v1.56.0-noble` 容器，加 `--network host`，并把截图目录挂载到 `/out`。截图用 Playwright 1.56 加 Chromium 拍摄，视口如下：
  - 桌面 1440×900；
  - 手机 390×844 @3x，即 iPhone 14 视口；
  - 暗色模式。

截图清单：

| 内容 | 桌面 | 暗色 | 手机 |
|---|---|---|---|
| 左侧子菜单 | `desktop-overview-sidebar` | `dark-overview-sidebar` | `mobile-sidebar-drawer`、`mobile-dark-drawer` |
| 设置各页 | `desktop-settings-{policy,cache,lists,jev,response,records,advanced}` | `dark-settings-policy` | `mobile-settings-policy`、`mobile-settings-cache` |
| 旧链接 `?tab=settings` 跳转 | `desktop-legacy-tab-settings` | — | — |
| 流程编辑里的审核范围 | `desktop-flow-scope` | `dark-flow-scope` | `mobile-flow-scope` |
| 记录详情的匹配来源 | `desktop-record-match-fallback`、`desktop-record-match-fields`、`desktop-record-match-default` | `dark-record-match` | `mobile-record-match`、`mobile-record-match-fields` |
| 记录列表 | `desktop-events-list` | — | — |
| 观察名单用户详情（AI 分析删除后） | `desktop-watch-detail` | `dark-watch-detail` | `mobile-watch-detail` |

原型没有实现、或没有截图的部分：

- 旧控制台（UI 3.13 及以下）的回退界面：由 `nav.spec.ts` 中 `useSection` 和 `visibleGroups` 的测试覆盖，没有截图，因为 mock 控制台总是 3.14；

- 审核插件 Go 后端（`fail_policy`、`hit_replay`、`scope`、记录新列）；
- 绑定页标红失效绑定；
- 级名改为按语义命名；
- 把 `usePageSections` 提升到 UI SDK。

---

## 7. 编码规范自检

- **复用：** 侧栏没有在插件里复制一份。插件页视图直接复用 `SidebarView`、`SidebarViewMenu` 和 `AppSidebar`；宿主只新增 `pluginNav.ts` 一个文件，`sidebarViews.ts` 只加 1 行。
- **单一来源：**
  - 菜单只在 `UI/nav.ts` 声明一次，manifest 与它的一致性由 `nav.spec.ts` 保证；
  - 设置分页由 `settings.ui.json` 的 `page` 字段驱动，没有在代码里写死字段清单；
  - 一致性由 `settingsForm.spec.ts` 保证。
- **解耦与低侵入：**
  - 插件只声明 nav，不碰宿主布局；
  - 核心只做过滤；
  - 旧控制台和旧核心都能自动回退。
- **低嵌套与简洁：**
  - `ModerationDashboard` 原来的 `initialTab`、`isTab`、`watch` 加 `replace` 组合，改成了一个可写的 computed `tab`（`sectionOf`）；
  - 设置页从 1 个巨型表单变成"同一组件，按 page 过滤"。
- **待改进：**
  - `ModerationDashboard` 里各分节仍是逐个写出的 `v-if/v-show` 行，可以改成一张"分节 → 组件"表，但各分节的 props 和事件不同，原型保留显式写法以便阅读；
  - 后端的"决策纯函数化"属于第 2 期和第 4 期。

## 8. 独立审查

见 §8.1，由独立审查子代理只看 diff 给出意见，修复结果记在同一节。

### 8.1 审查发现与处理

独立审查子代理只读取 `git diff ed988819ca..HEAD`，共报告 22 条（3 高、8 中、11 低）。下表的修复都在第二个提交里。

| 级别 | 发现 | 处理 |
|---|---|---|
| 高 | `canonicalSpec` 总会带上 `scope`，现有后端 `ParseSpec`（`DisallowUnknownFields`）会让**所有流程的保存、发布、放弃修改**都返回 400 | 已修：只在范围被收窄时才发送；`flowValidate` 注明 `scope` 是原型字段；新流程不再默认 `input`（改由 D1 决定） |
| 高 | `verdict_cache_days` 最小值改成 0，但后端会夹到 1 天，界面给出了错误承诺 | 已修：恢复最小值 1 和原说明；P11 改为与后端同期上线（§4.8） |
| 高 | 关闭由 `?record=` 打开的详情后，页面跳回概览 | 已修：`useSection` 把解析出的分节写回 `?tab=`，并加了测试 |
| 中 | 侧栏与页面在 `?tab=settings` 和 `?record=` 时高亮不一致 | 已修：同上，地址统一写回 |
| 中 | 只按控制台版本判断，菜单里没有入口时侧栏不出现、页面又没有标签 | 已修：声明了 nav 的插件页总会得到侧栏视图（`sidebarViews.ts`） |
| 中 | 概览里"设置"链接总是跳到执行策略（AI 分析渠道的设置在 analysis 页） | 已修：去掉 `go-settings` 事件，`StatusLine` 直接发出 `go-tab` 并带上目标页 |
| 中 | 切换设置页时，未保存的修改会被悄悄留下 | 已修：`SettingsTab` 暴露 `dirty`，仪表盘用 `router.beforeEach` 在离开有未保存修改的页时先确认。各页共用一次加载暂未做，记为第 1 期正式实现时的待办 |
| 中 | 旧控制台上标签从 10 个变成 17 个 | 已修：设置页合并成一个"设置"标签，标签内用分段切换 |
| 中 | 记录详情在现有后端上丢掉 mode 信息，且显示空行 | 已修：没有 `level_source` 时显示 mode 标签；新的几行只在有值时出现 |
| 中 | 审核范围的默认值不一致，所有旧流程都显示警告 | 已修：未设置时显示蓝色提示，显式选择 `all` 时显示黄色警告 |
| 中 | 分节路由缺少测试 | 已补：`nav.spec.ts` 新增 `useSection` 的 4 个用例（record 关闭、旧链接、push 与 replace 及权限、离开页面）。仪表盘整体挂载测试未补，留到第 1 期 |
| 低 | 页内跳转用 replace、侧栏用 push，后退行为不一致 | 已修：侧栏模式下页内跳转也用 push |
| 低 | 离开页面时 `tab` 按下一个路由重新计算 | 已修：`useSection` 先检查 path |
| 低 | 非法 URL 让 `decodeURIComponent` 在 computed 里抛错 | 已修：加了 `decoded()` |
| 低 | `corePermission` 写错时条目对所有人隐藏 | 未改：记入第 1 期待办，安装时用核心权限目录校验 |
| 低 | 控制台类型缺少 `permission` 和 `corePermission` | 已修：加为可选字段 |
| 低 | `editableKeys` 默认用旧 key 所在的页 | 已修：`page` 改为必填 |
| 低 | `ui:section.page` 没有文档 | 已修：在 `packages/ui/src/schema/schema.ts` 的注释里说明；没有 page 的分节由 `settingsForm.spec` 拦住 |
| 低 | 名单改名只改了中文，英文仍用旧称 | 已修：英文文案同步 |
| 低 | 未知枚举值显示成 i18n 的键名 | 已修：改用 `enumLabel`，未知值显示原值 |
| 低 | mock 的菜单分区与 manifest 不一致，也不按权限过滤 | 分区已对齐为 security；mock 不做权限过滤，保持原样（只用于预览） |
| 低 | 残留的 `TabKey` 别名 | 已修：各处改用 `SectionKey`，删除别名 |

修复后的验证：

- 插件 UI：vitest 175 通过，vue-tsc 无错误；
- 控制台：vue-tsc 无错误，相关 vitest 52 个文件、410 个用例通过；
- Go：`sdk/manifest/...`、`server/internal/plugin/api/...`、审核插件的 manifest 测试均通过。

---

## 9. AI 分析：删除

### 9.1 结论

**整个 AI 分析（原型里叫"AI 复核"）功能删除。**

- 用户原话："这样直接去掉 AI 复合功能吧，没什么用。因为我们现在直接有 AI 二级复合了呀，根本就不用那个 AI 所谓的 AI 复合了。"
- 被删除的功能：按用户跑的 agent，复核名单用户的可疑记录，给出趋势和建议，并自动调整名单级别。
- 删除的理由：流程里已经有第 2 级 LLM 复核，按请求复核就够了。
- D6–D8 作废（见 §0）。之前为 AI 复核写的方案（谁来决定、授权闸门、触发、提示词等）不再保留，git 历史里可以查到。

### 9.2 删除清单

行号指 `ed988819c`。`M/` 指 `next/plugins/moderation/internal/moderation/`。

**后端**

原型里没有删除后端代码，这里列出改动点。删除时同一个提交里要一并改掉的有：manifest、CONTRACTS 和 Go 测试。

| 位置 | 删除内容 |
|---|---|
| `M/analysis.go`、`analysis_queue.go`、`analysis_tools.go`、`analysis_prompt.go`、`analysis_guidelines.go` | 整个文件，共 2 407 行。`agent.go` 是"大模型判定"审核器，保留；`llmproto.go` 是审核器与分析共用的协议层，保留 |
| `M/store.go:212-251`、:313-368 | 违规计数触发分析的部分：`analysis_every_violations` 判断、排队、`kickAnalyses`。保留违规计数本身，它用于显示和观察期顺延 |
| `M/watch.go:35-49`、:55-65、:88-98、:127、:149、:181-182、:295、:373、:377-387 | 名单行上的分析字段（扫描与 `fillRunning`）、名单历史的 `analysis` 列、`analysis_id` 参数及其校验、`deleteWatch` 的 `analysis_id` 查询参数 |
| `M/watch_list.go:21`、:156、:167-190、:285 起（`loadWatchAICounts`） | `trend` 过滤、`ai_counts`、`analysis_every_violations` |
| `M/user_detail.go:21-35`、:46、:69-73、:84-85、:98-106、:115-129、:162-170、:202-212（`loadLatestAnalysis`） | `AIVerdictCounts`、最近一次分析、分析进度 |
| `M/routes.go:39-43`、:140、:160、:201-207、:239、:268、:274、:305-319、:548-568 | 5 条分析路由（含手动分析），状态里的 `analysis` 运行信息，记录的 `ai_*` 字段、扫描与 `ai_verdict` / `verdict=needs_review\|disagree` 过滤，详情里查 `user_analyses` 的 `eventExtras` |
| `M/stats_api.go:243-249`、:314、:331、:372、:480、:497、:499-510（`addAnalysisCounts`） | 今日分析次数、`AIReview` 占比、健康统计与费用里的分析来源 |
| `M/routes_config.go:48-51`、:189-194、:419-421 | 渠道上的"分析渠道"标记，以及删除时的 409 保护 |
| `M/routes_misc.go:202-210`、:300-303、:317、:358-359（`case JobAnalysis`）、:389-393 | 导入导出里的 `analysis_guidelines`、`analysis` 任务的分派、每日清理时裁剪 `user_analyses` |
| `M/notify.go:111-135` | AI 调整名单时的通知 |
| `M/plugin.go:105-106` | 正在运行的分析计数 |
| `M/settings.go:106-117`、:180-182、:408-423 | `analysis_*` 设置的字段、默认值和解码 |
| `M/costs.go:25` | `SourceAnalysis`。历史费用行仍然以这个来源存在，保留常量和"AI 分析"文案，等统计保留期过后再删 |
| `M/marker.go:55` | 注释里分析开场消息的说明 |
| `next/plugins/moderation/manifest.json` | `analysis` 任务（:119-121）、4 条分析路由和 1 条手动分析路由（:406-432）、jobs 权限说明（:616-617）。描述里的 AI 分析一句已在原型中删掉 |
| `next/docs/CONTRACTS.md` | §72 对比表里的"AI 分析"一行（:5116）、§72.8 的计数（:5426）、§72.13.4 整节（:5629-5693）、§72.13.6 中观察名单的分析部分（:5709-5714）、用户详情抽屉的分析部分（:5851） |

**前端**（原型已全部删除）

- **文件**：`UserAnalyses.vue`、`AnalysisCard.vue`、`AnalysisModal.vue`、`AnalysisPromptModal.vue`、`overview/AiReviewCard.vue`，以及原型里新加的 `AiReviewTab.vue`、`UserAiReview.vue`、`ProposalBox.vue`、`useProposal.ts`（含测试）。
- **侧栏**："AI 复核""AI 复核设置"两个条目（`nav.ts` 和 manifest 的 `nav`）。
- **设置**："AI 复核设置"页，以及"高级 / AI 复核资源"一节。全部 `analysis_*` 改为隐藏的 legacy 键；原型里新加的 `analysis_upgrade`、`analysis_downgrade`、`analysis_remove`、`analysis_review_before_expiry` 直接删除。
- **记录**：AI 结论一节、"查看这次分析"，以及筛选里的"AI 复核""待复核""人与 AI 意见相反"。"结论"筛选改为只按人工处理状态；最终结论只看人工处理。
- **观察名单**："分析"列、"AI 结论"列、"趋势"筛选、"每 N 次违规自动分析"提示、"分析提示词"按钮。
- **用户抽屉**：AI 复核卡片、"分析记录"标签页、"AI 结论"统计，以及只为跟踪分析进度而做的轮询。
- **概览**：AI 复核卡片、状态行的"AI 分析"项、运行状态的分析队列行、今日费用的"AI 分析"来源。
- **渠道**："AI 分析渠道"标记和删除保护。
- **其它**：相关类型、接口函数、色调函数、CSS，以及 154 个文案键（中英各一份）。
- **mock**：分析数据和接口、记录上的 `ai_*` 字段。

**保留的部分**（这些只是为了能读懂历史数据，不再产生新数据）

- 名单历史里 AI 当年的动作文案（`auto_upgrade` 等）和原因格式；
- 名单来源"AI 分析"；
- 费用来源"AI 分析"。

**代码量估算**

| 部分 | 删除行数 |
|---|---|
| 后端非测试 | 约 2 900 行：`analysis*.go` 共 2 407 行，加上其它文件中的约 500 行（估算） |
| 后端测试 | 约 600 行：`analysis_test.go` 359 行，加上 `lists_test`、`stats_api_test` 等中的约 250 行（估算） |
| 前端（相对 `ed988819c`） | 约 1 000 行：删除的 5 个文件共 458 行，文案键 154 个（中英两份约 300 行），其余文件约 200 行（估算） |

本分支原型的实际 diff：前端非测试 −1 865 / +93 行（含原型里新加后又删掉的部分），测试 −108 / +13，mock −237 / +23，表单与 manifest −260 / +39。

### 9.3 影响

- **观察名单的级别今后只靠三种规则变化**，都不依赖分析模块：
  - `auto_watch`：首次出现可疑记录时，以默认级别加入（`M/store.go:297-306`）；观察级每次违规都会顺延观察期（:331-337）；
  - 封禁阈值：窗口内违规达到阈值即封禁（`M/store.go:375-401`），计数只排除人工误报（:443）；
  - 手动调整：设级别、移出、加备注（`M/watch.go`）。

  加上到期移出和两个固定名单，这几条与分析模块的交点只有 `store.go:239` 的 `kickAnalyses` 调用和 :349-368 的排队，删掉不影响它们。
- **这些规则够不够用**：
  - 默认级别为拦截时，所有没有名单条目的用户本来就先审，不需要自动升级。
  - 默认级别为观察时，失去了"持续违规自动升为拦截级"。但封禁阈值会处理严重的情况，其余由管理员在名单里手动升级。
  - 失去了"误报多时自动移出"。条目会按观察期自然到期，也可以手动移出。
  - 用户决定加一条不依赖 AI 的固定规则（D10），见下。
- **D10：自动升为拦截级**（0.10.0 新增，默认关闭，线上行为不变）
  - 设置：`escalate_violations`（N，0 = 关闭）和 `escalate_window_seconds`（窗口，存为秒，界面按秒 / 分钟 / 小时编辑）。
  - 计数口径与封禁阈值一致，复用同一个统计函数（`countViolations`）：同一请求只计一次，不含人工误报、重放命中和 0.1 的 flag，从最近一次解封 / 最近一次封禁开始。另外从该用户最近一次**人工**名单操作之后开始计，这样手动降级或移出后，不会被旧违规立刻再升上去。
  - 计数直接查记录表（`events (user_id, created_at)` 有索引），只在出现可疑记录时查一次，秒级窗口（例如 10 秒）也精确，不需要 Valkey。
  - 作用对象：观察级条目，以及**不在名单上的用户**（直接新建一条拦截级条目）。跳过“只后台审核”“总是先审”名单里的用户（锁定条目）和审核管理员；已是拦截级的条目不变。
  - 升级后的条目按 `watch_days` 到期；名单历史记一条“规则自动升级”（动作 `auto_escalate`），原因里带触发时的次数和窗口。条目来源列仍为 `auto`（该列有 CHECK 约束，加取值需要改约束，不符合本版本只做 expand 的要求）。
  - 可以手动撤销：降回观察级或移出名单，都写入历史。
- **三条自动规则的关系与执行顺序：封禁 > 升拦截 > 加观察**
  - 同一次违规同时满足时，只执行最高的一条：达到封禁阈值就封禁，不再升级（被封禁的用户请求直接被拒，再升级只会多一条无用的历史）；否则达到升级阈值就升为拦截级；否则才由 `auto_watch` 以默认级别加入。
  - 升级和 `auto_watch` 同时适用于不在名单上的用户时，以升级为准，只写一条历史。
  - 理由：三者是同一个用户违规程度的递进处理，按严重程度取最高一档，结果唯一，互不覆盖。
- **"AI 结论"等展示**：第一步就不再展示（D9）。数据在迁移之前仍留在数据库里，但界面不读。
- **第 2 级 LLM 复核能覆盖的**：对每个可疑请求做二次判断，减少单次误报。这正是用户看重的部分。
- **覆盖不到的**：跨请求的行为趋势（把违规拆散到多个请求、逐步升级），以及基于趋势自动调整名单。

### 9.4 数据与发布

**范围变更（用户）：“AI 复合不是第一期停用，是直接移除这个功能。”** 0.10.0 直接删除全部代码（后端、前端、manifest、文档）。库里的表和列本版本不删（滚动期间旧节点仍在读写），留到下一版本的 contract 迁移。

**本版本：删除代码**（不需要迁移，可以滚动）

- 后端删除上表中的 Go 代码、路由和任务，不再触发、不再读取分析。
- `analysis_*` 设置改为隐藏的 legacy 键，保证已存的值仍能通过核心校验。
- 界面移除（原型已完成）。
- **rolling 期间**，全部节点升级之前，旧节点照常排队、分析、自动调整名单并发送通知；新节点不读这些数据。若不希望升级窗口内还有 AI 调整名单，升级前先在旧版设置里清空分析渠道（`analysis_channel_id`），旧节点就不再分析。
- 当天已产生的分析费用仍计入今日费用合计，但不再单列来源，第二天起不再出现。

**下一版本：contract 迁移删除**（全部节点升到 0.10.0 之后才做；那时已没有节点读写这些表和列，可以滚动）

- 删除 `user_analyses` 表（`migrations/0004_analysis_costs.sql:25-47`）。
- 删除名单行上的分析列（0004:13-21）。
- 删除记录上的 `ai_verdict`、`ai_severity`、`ai_category`、`ai_evidence`、`ai_reason`、`ai_analysis_id`。
- 删除索引 `watchlist_analysis_requested_idx`（0004:22）和 `user_analyses_user_idx`（0004:48，随表删除）。
- `watch_events.analysis_id` 列保留，作为历史 AI 动作的审计信息，只是不再写入。
- 删除 schema 里的 `analysis_*` legacy 键。
- 名单历史 `watch_events` 里 AI 当年的动作属于审计记录，保留。

**已有数据是否导出**：不导出。数据库有自动备份；而且第一步之后界面不再展示这些数据（D9），导出也没有使用者。

### 9.5 原型截图

- 已删除 `design/shots/ai-*.png`。
- 重拍了受影响的截图：侧栏（`desktop-overview-sidebar`、`dark-overview-sidebar`、`mobile-sidebar-drawer`、`mobile-dark-drawer`）、观察名单详情（新增 `desktop-watch-detail`、`dark-watch-detail`、`mobile-watch-detail`）、设置页（`desktop-settings-*`、`dark-settings-policy`、`mobile-settings-*`）。
- 不再有 `desktop-settings-analysis`。

### 9.6 独立审查

独立审查子代理复核了 `d2cbd7b96f..38a8264a59`。没有高优先级问题，共报告中 4 条、低 10 条，均已处理。

- **§9.2 的行号**：按审查核对的结果改正并补全，例如 `watch.go`、`routes.go`、`user_detail.go`、`stats_api.go`、`routes_misc.go` 中漏列的位置；`analysis` 任务的分派在 `routes_misc.go:358-359`，不在 `plugin.go`；manifest 的 jobs 说明在 :616-617。
- **文档里残留的 AI 措辞**：D5 备选、§4.7、第 4 期的文件和测试要点中的已去掉。
- **mock 的今日费用合计**：改为审核加手动的和，与分项一致。§9.4 说明了当天已有的分析费用只计入合计。
- **rolling 期间旧节点的行为**：已如实写明，并给出做法：升级前先清空分析渠道。
- **数字**：删除的文件为 5 个、共 458 行；文案键 154 个；侧栏条目 16 个。
- **第二步迁移的范围**：补上两个索引；`watch_events.analysis_id` 列保留，作为审计信息。
- **代码清理**：
  - 删除不再使用的 CSS（`.mod-bar*`、`.mod-danger`、`.mod-verdict-badge .mod-sub`）；
  - 删除不再使用的文案键（`watch.notWatched`、`watch.loadMore`、`detail.verdictBy`）；
  - 改正过时的注释；
  - mock 新建名单条目时不再带分析字段；
  - `deleteWatch` 的参数改名为 `body`；
  - 概览里只剩一张卡片的网格去掉。
- **措辞与标题**：
  - 记录的"未复核"改为"待处理"，与"结论"筛选一致；
  - 隐藏的 `analysis_*` 键标题加上"（已停用）"。

## 10. 一次发布：迁移与滚动兼容（2026-11-12 范围变更）

用户要求：“AI 复合不是第一期停用，是直接移除这个功能，然后你完成剩下的功能。” 原第 1–4 期、D5、D10 全部在 0.10.0 一次发布，直接滚动升级（不在 new-api 停渠道）。本节取代 §5 的分期和 §4 各项里的“maintenance / 分两步发版”。

**版本**：核心 0.1.284（控制台 UI 3.14，插件页侧栏；核心无迁移）；moderation 0.10.0（`hostCompat` 与 `hostUICompat ^3.12` 不变，旧控制台回退到页内标签）。

**规则（expand / contract）**

- 本版本只加列、加表、回填；新旧插件在同一个库上同时运行。
- 加列前核对 0.9.3 对该表的 INSERT / SELECT 都显式列出列名，旧节点不受新列影响。
- 新设置键：旧节点解码时忽略未知键；核心按已安装的 schema 校验每次保存，所以新键都写进 schema。
- 删列、删表、删键、改约束都留到下一版本。

**本版本的迁移**（每个都有新旧版本兼容测试：在旧表结构和新表结构上执行 0.9.3 的原 SQL）

| 迁移 | 内容 | 为什么滚动安全 |
|---|---|---|
| `0011_flow_scope_source.sql` | `flows.scope`（可空，NULL = all）；`events.flow_source`、`flow_fallback_id`、`flow_fallback_reason`（可空） | 新列可空、旧 SQL 显式列名；scope 不写进 spec JSON，旧节点照常解析流程，只是在升级窗口内按 all 审核（更宽，不会漏审） |
| `0012_lists_level.sql` | `events.level_source`、`level`（可空）；`watchlist.locked`（默认 false） | 同上；锁定条目对旧节点是普通条目，先审 / 后审仍由设置里的名单决定，新旧节点判定一致 |

**滚动期间的行为差异**（都在升级完成后消失）

- 审核范围：旧节点对所有流程按 all 审核。
- 命中重放、失败策略、自动升级：只在新节点上生效。
- 名单：旧节点上仍能在旧界面改动锁定条目，新节点下一次同步时恢复。
- AI 分析：旧节点照常排队、分析并调整名单；若不希望升级窗口内还有 AI 调整名单，升级前在旧版设置里清空分析渠道。

**留到下一版本的 contract 步骤**

1. 删除 `user_analyses` 表、名单行的分析列、记录的 `ai_*` 列和相关索引（§9.4）。
2. 从 schema 删除 `analysis_*`、`on_error` 和不再读取的 0.1 旧键；`exempt_user_ids`、`cache_ttl_seconds` 先把存储值写进新键，再删。
3. 名单：观察名单的锁定条目成为唯一依据，删除设置里的 `allowlist_user_ids` / `blocklist_user_ids`。
4. 视需要给 `watchlist.source` 的 CHECK 加 `escalate`，把自动升级的条目来源单独标出。
5. 封禁窗口改用秒级键和时长控件（兼容旧键 `ban_window_hours`）。

具体列名、接口字段和测试结果以实现分支 `feat/moderation-redesign` 的 CONTRACTS §72 为准。
