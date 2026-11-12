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
2. **审核插件界面：** 原来横排的 10 个标签改为 5 组、17 个侧栏条目。原来一整页的设置拆成 8 个短页，每页 1–2 张卡片，不再需要长滚动。名单、模式等命名改成"说出它实际做什么"。
3. **审核逻辑：** 按评估报告逐条落地，最重要的四项是：
   - 流程增加"审核范围"，新建流程默认只审 system + user；
   - 命中重放只重放命中部分，新内容照常审；
   - 记录写入"匹配来源"和"处理方式来源"，绑定失效时给出显式提示；
   - 失败策略统一由一个设置决定。

原型已在本分支实现了前端、平台声明、校验和过滤；审核插件的 Go 后端没有改动。界面用 web mock 运行，Playwright 截图在 `design/shots/`（§6）。

### 待用户确认的决策点（推荐值写在前面）

| # | 决策 | 推荐 | 备选 |
|---|---|---|---|
| D1 | 已有流程的"审核范围"怎么处理 | **保持 `all`（旧行为），不自动改**。新建流程默认 `input`（与第 2 期后端同时上线）；未设置范围的流程在编辑页显示提示，由管理员手动切换 | 升级时把所有已有流程改成 `input`：误报立刻下降，但会悄悄缩小审核面 |
| D2 | `mode` 是否拆成"开关"和"默认级别"两个字段 | **不拆，只改文案**（关闭 / 开启·默认后台审核 / 开启·默认先审后发），不需要迁移 | 拆成 `enabled` + `default_level`：概念更干净，但要做设置迁移和兼容读取 |
| D3 | 缓存命中的重放方式 | **`new_content`：只重放命中部分，新内容照常送审** | `whole`：旧行为，整个请求直接按命中处理 |
| D4 | 审核失败时的处理 | **默认 `open`（放行并记为出错，与现状一致）**，可选 `close_pre`（先审的请求返回 503） | 默认 `close_pre`：更安全，但上游一抖动就会拒绝用户 |
| D5 | 白名单、黑名单是否并入观察名单 | **本期只改名**（"只后台审核" / "总是先审"），第 4 期再并入观察名单，作为"锁定条目" | 立即合并：要迁移数据，而且 AI 分析和自动观察的规则都要一起改 |
| D6 | AI 复核的结论默认如何生效（§9） | **升为拦截级：自动执行；降为观察级、移出名单：只给建议，等人确认**。所有自动执行都可以一键撤销 | 全部自动（相当于现状 `allow_downgrade` 开）；或全部只给建议（最稳，但人工负担最大） |
| D7 | AI 复核的触发时机（§9） | **每 N 次违规（observe、enforce 都计）+ 名单到期前一天 + 手动**；同一用户最短间隔 60 分钟，期间的触发顺延而不是丢掉；全站每天上限 200 次 | 维持现状：只按违规次数触发，且只有 observe 级计数（默认级别为拦截时 enforce 级也计）；分析运行期间的触发会丢 |
| D8 | 模型输出的 `trend` 和 `suggestion` 两个枚举如何处理（§9） | **改为"风险等级 + 建议动作"**：模型只给风险（低 / 中 / 高）和一段总结，建议动作由服务端规则按风险、级别和复核为违规的条数推出；`trend` 保留为说明性标签 | 维持两个枚举，由矩阵组合（现状，很多组合什么也不做、不留痕） |

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
  ├ AI 复核         ?tab=ai         读      （待确认的建议 + 最近的复核，§9）
  ├ 名单规则        ?tab=lists      设置权限
  └ AI 复核设置     ?tab=analysis   设置权限
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
  - 旧控制台的回退：其它分节照常做成标签，所有设置页合并成一个"设置"标签，标签内用分段切换各设置页，避免 17 个标签挤在一行。
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

结果是 8 个短页：

| 页（侧栏） | 分节 | 字段数 |
|---|---|---|
| 执行策略 policy | 执行方式 | 2 |
| 缓存与输入 cache | 判定缓存与重放 / 送审输入上限 | 3 + 5 |
| 名单规则 lists | 固定名单 / 自动观察与封禁 | 2 + 5 |
| AI 分析 analysis | AI 分析 / 分析资源（默认折叠） | 6 + 4 |
| Jev 默认题集 jev | Jev 默认题集 | 1 |
| 拦截提示 response | 拦截响应（带预览） | 2 |
| 记录与保留 records | 记录内容 / 保留期 | 6 + 5 |
| 高级 advanced | 性能 / 告警与通知 | 4 + 3 |

### 3.2 每个配置项的去向

处理方式的含义：

- **保留**：位置变了，语义不变；
- **改名**：文案改正，存储键不变；
- **新增**：原型里已有界面，后端尚未实现；
- **删除**：死配置，按 §5 第 4 期清理；
- **移到流程**：概念上属于流程。

| 键 | 原分组 | 新位置 | 处理 | 默认 | 新文案（中 / 英）与说明 |
|---|---|---|---|---|---|
| `mode` | 运行模式与用户名单 | 执行策略 / 执行方式 | **改名**（D2） | off | 审核开关与默认处理 / Moderation and default handling：关闭 · 开启·默认后台审核（只记录） · 开启·默认先审后发（命中拒绝）。说明里写明决定顺序 |
| `fail_policy` | — | 执行策略 / 执行方式 | **新增**（R6/P9） | open | 审核失败时 / When moderation fails：放行并记为出错 · 先审的请求拒绝（503）。覆盖审核器出错、总超时和繁忙三种情况 |
| `verdict_cache_days` | 性能与缓存（折叠） | 缓存与输入 / 判定缓存与重放 | 保留；第 1 期后端完成后，**最小值改为 0 = 关闭**（P11） | 7 | 与旧字段 `cache_ttl_seconds=0` 合并 |
| `hit_replay` | — | 缓存与输入 / 判定缓存与重放 | **新增**（R2/P2，D3） | new_content | 缓存命中的重放 / Replaying a cached hit：只重放命中部分，新内容照常审核 · 整个请求按命中处理 |
| `cache_entries` | 性能与缓存 | 缓存与输入 | 保留 | 100000 | — |
| `min_chars`、`input_max_chars`、`input_max_chunks`、`input_overflow`、`input_max_images` | 输入范围（折叠） | 缓存与输入 / 送审输入上限 | 保留（**候选：第 4 期移到流程**） | 2 / 12000 / 8 / sample / 8 | 属于"审什么"，和审核范围同类 |
| `allowlist_user_ids` | 运行模式与用户名单 | 名单规则 / 固定名单 | **改名**（D5） | [] | 只后台审核的用户（从不拦截） / Background-only users (never refused) |
| `blocklist_user_ids` | 同上 | 名单规则 / 固定名单 | **改名** | [] | 总是先审的用户 / Always check first。说明里写明"这不是封禁" |
| `auto_watch`、`watch_days` | 观察名单与封禁 | 名单规则 / 自动观察与封禁 | 保留；后端修复 P8（跳过只后台审核的用户） | T / 30 | — |
| `ban_threshold`、`ban_window_hours`、`ban_duration_hours` | 同上 | 同上 | 保留 | 0 / 24 / 24 | — |
| `analysis_*`（10 项） | AI 分析 | AI 复核设置、高级 / AI 复核资源 | 见 §9.4 的逐项去向表 | — | — |
| `jev_default_set` | Jev 默认题集 | 资源 / Jev 默认题集 | 保留（独立成页，挂在资源下，靠近审核器） | null | — |
| `block_status`、`block_message` | 拦截响应 | 系统 / 拦截提示 | 保留（带预览） | 403 / 英文默认 | — |
| `record_pass`、`record_overturned`、`record_errors`、`store_text`、`record_pass_input_message`、`error_text_max_chars` | 记录与保留 | 记录与保留 / 记录内容 | 保留 | F/T/T/T/T/2000 | — |
| `*_retention_days`、`stats_retention_days` | 记录与保留 | 记录与保留 / 保留期 | 保留 | 180/30/7/3/90 | — |
| `max_concurrency` | 性能与缓存 | 高级 / 性能 | **改说明**（P10） | 16 | 写明"先审另有 max(64, 4×此值) 个名额"（`M/plugin.go:420`） |
| `queue_size`、`max_inflight_mb`、`ttft_timeout_ms` | 性能与缓存 | 高级 / 性能 | 保留；`max_inflight_mb` 超限的处理改由 `fail_policy` 决定 | 1000 / 128 / 10000 | — |
| `error_rate_alert_percent`、`notify_webhook_url`、`notify_webhook_secret` | 告警与通知 | 高级 / 告警与通知 | 保留；webhook 地址不合法时**在保存阶段报错**（P12） | 5 / "" / "" | — |
| `on_error` | 0.1（隐藏） | — | **删除**（P9；`M/settings.go:142`、:483 只用于转换） | — | 被 `fail_policy` 取代 |
| `exempt_user_ids`、`cache_ttl_seconds` | 0.1（隐藏） | — | **删除**（第 4 期，先迁移存储值） | — | 被 allowlist 和 `verdict_cache_days=0` 取代 |
| `base_url`、`api_key`、`model`、`system_prompt`、`categories`、`tool_choice`、`max_turns`、`temperature`、`max_tokens`、`timeout_ms`、`sample_rate`、`group_ids`、`model_patterns`、`retention_days` | 0.1（隐藏） | — | **删除**（第 4 期）；转换逻辑移到 `legacy.go` 的私有结构 | — | 现在只在 `legacy.go:52-104` 读取 |
| （流程）`sample_rate` | 流程配置 | 流程 | 保留；文案统一为"后台审核抽样比例"（P6） | 100 | 先审永远全量（`M/hook.go:251`） |
| （流程）`total_timeout_ms` | 流程配置 | 流程 | 保留；说明里写明"先审最多等 29 s"（P6，`M/hook.go:375`） | 0 = 自动 | — |
| （流程）`scope` | — | 流程 / 审核范围 | **新增**（R1/P1，D1） | 新建为 input，已有流程视为 all | — |

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
  - 级名按审核器类型推出（`UI/flowOps.ts:269-273`，后端在 `M/analysis_prompt.go:52` 有重复实现）。
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
  - 第 4 期把两个名单并入观察名单，成为"锁定条目"（级别固定、不过期、AI 不能修改），设置里不再有这两个列表。
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

## 5. 分期实施计划

每期都可以独立上线。

| 期 | 范围 | 主要文件 | 估算改动量 | 迁移 | rolling | 测试要点 |
|---|---|---|---|---|---|---|
| **第 1 期：平台侧栏加界面整理** | UI 3.14 插件页 nav（平台）；审核插件改为侧栏；设置拆成 8 页；改名与文案修正（P6/P7/P10，记录标签）；后端小修 P8（`auto_watch`）和 P11（缓存可设 0）；**AI 复核的界面部分**（§9.4 归期：只读的复核视图、抽屉卡片、设置拆分、`?user=`、去掉准则里的后果段） | sdk manifest/check、server plugin/api/ui.go、`WEB/src/layouts/pluginNav.ts`、`sidebarViews.ts`、packages 版本；`UI/nav.ts`、`ModerationDashboard.vue`、`SettingsTab.vue`、`settingsForm.ts`、`forms/*.json`、`messages.ts`、`EventDetail.vue`；`M/store.go`、`M/settings.go` | 平台约 +500/-20 行（含测试），插件 UI 约 +600/-250 行，Go 约 +20 行 | 无 | 可以（核心 0.1.284 + 插件 0.10.0；旧控制台自动回退到页内标签） | `nav.spec`（manifest 与 nav.ts 一致）、`pluginNav.spec`、`navigation.spec`、`ui-contract.spec`、`check/ui_test`、`api/unit_test`；`settingsForm.spec`（分页覆盖）；手动验证旧链接 `?tab=settings` 和 `?record=` |
| **第 2 期：审核范围加命中重放** | `Spec.scope` 与角色过滤、指纹包含 scope；`hit_replay=new_content`；`fail_policy` | `M/flow.go`、`hook.go`、`extract.go`（只加过滤表）、`snapshot.go`、`settings.go`；`UI/FlowConfigCard.vue` 等（原型已有） | Go 约 +250 行（含测试），前端少量 | 无 | **分两步发版**：先让后端认识 `scope`（界面不提供），全部节点升级后再开放界面 | 各协议抽取在不同 scope 下的矩阵测试；缓存指纹随 scope 变化；重放后新内容是否送审；先审拒绝的语义不变 |
| **第 3 期：可观测性** | `flow_source`、`level_source` 入库；绑定页标出失效绑定；概览提示；**AI 复核的建议模型**（§9.4：`proposal_*` 列、决策接口与撤销、新触发与上限、`allow_*` 退役） | `M/snapshot.go`、`hook.go`、`account_binding.go`、`store.go`、`routes_bindings.go`、`migrations/0011_*.sql`；`UI/BindingsTab.vue`、`EventDetail.vue`、overview | Go 约 +200 行，SQL 约 10 行，前端约 +150 行 | **有**（events 加 5 列，可为空） | 走 maintenance | 迁移前后记录的读取；fallback 的三种原因；account 与 group 的优先级 |
| **第 4 期：清理与合并** | 名单并入观察名单（锁定条目）；删除 0.1 旧字段和 `on_error`；`decideHandling` 收拢；可选把输入上限移到流程 | `M/settings.go`、`legacy.go`、`hook.go`、`watch*.go`、`store.go`、`analysis_queue.go`、migrations；`forms/*`、`WatchTab.vue` | Go 约 +300/-400 行 | **有**（watchlist 加 `locked`，名单数据迁移） | 不能 rolling，一次性切换，走 maintenance | 迁移后先审 / 后审的判定与迁移前逐用户对比；AI 分析不能修改锁定条目 |

---

## 6. 原型

运行方法：

```
cd next/plugins/moderation/ui/native && node node_modules/vite/bin/vite.js build
cd next/web && node node_modules/vite/bin/vite.js --mode mock
# 浏览器打开 /p/moderation/dashboard，用户 admin@example.com / admin
```

- mock 直接读取插件 manifest 的 `ui.pages`（含 nav）和真实的 `forms/*.json`；记录 mock 里带有 `flow_source`、`level_source` 和 `scope`。
- 截图在 `design/shots/`，共 42 张（其中 `ai-*` 16 张见 §9.5），脚本是 `design/shots.mjs`。重拍方法：在一个装有 `playwright-core@1.56.0` 的目录里运行 `mcr.microsoft.com/playwright:v1.56.0-noble` 容器，加 `--network host`，并把截图目录挂载到 `/out`。截图用 Playwright 1.56 加 Chromium 拍摄，视口如下：
  - 桌面 1440×900；
  - 手机 390×844 @3x，即 iPhone 14 视口；
  - 暗色模式。

截图清单：

| 内容 | 桌面 | 暗色 | 手机 |
|---|---|---|---|
| 左侧子菜单 | `desktop-overview-sidebar` | `dark-overview-sidebar` | `mobile-sidebar-drawer`、`mobile-dark-drawer` |
| 设置各页 | `desktop-settings-{policy,cache,lists,analysis,jev,response,records,advanced}` | `dark-settings-policy` | `mobile-settings-policy`、`mobile-settings-cache` |
| 旧链接 `?tab=settings` 跳转 | `desktop-legacy-tab-settings` | — | — |
| 流程编辑里的审核范围 | `desktop-flow-scope` | `dark-flow-scope` | `mobile-flow-scope` |
| 记录详情的匹配来源 | `desktop-record-match-fallback`、`desktop-record-match-fields`、`desktop-record-match-default` | `dark-record-match` | `mobile-record-match`、`mobile-record-match-fields` |
| 记录列表 | `desktop-events-list` | — | — |

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

## 9. AI 分析重设计

### 9.0 结论

- **"AI 趋势分析"指的是插件里的"AI 分析"。** 它对观察名单上的用户运行一个 agent：
  - 逐条复核该用户的可疑记录；
  - 给出趋势（`trend`）和建议（`suggestion`）；
  - 按一张矩阵自动调整名单级别。

  插件里另有两处叫"趋势"的东西，都不是 AI（§9.1）。
- **设计的主要问题：一个功能同时做四件事，但对外只露出一个模糊的"趋势"：**
  - 逐条复核；
  - 用户趋势判断；
  - 自动处置；
  - 手动"按 AI 建议执行"。

  处置规则写在服务端矩阵里，并且**与给模型的准则互相矛盾**。触发会丢；结论没有"待确认、驳回、撤销"的状态；界面上的信息分散在五处，侧栏里只有一个设置页。
- **方案：把它重新定位为"AI 复核"。**
  - 每次复核只给出**一个风险判断**（低 / 中 / 高）和**最多一条名单调整建议**。
  - 每种调整可以单独设为"自动执行 / 只给建议 / 不提出"，所有自动执行都能撤销，并且在名单历史里可追溯。
  - 侧栏"名单与处置"下新增"AI 复核"视图（待确认的建议 + 最近的复核）；用户详情抽屉的第一张卡片就是该用户的复核结论；设置拆成"AI 复核设置"和"高级 / AI 复核资源"。
- **原型已经实现上述界面**，后端用 mock；截图是 `design/shots/ai-*.png`。
- **待确认的决策点是 D6–D8**，见 §0 的表。D1–D5 还在等回复，所以本节只做原型。

### 9.1 "AI 趋势分析"具体指什么

| 功能 | 是否 AI | 位置 | 说明 |
|---|---|---|---|
| **AI 分析**（本节对象） | 是 | 后端：`M/analysis*.go`；界面：用户抽屉中的 `UserAnalysisBar.vue`、`UserAnalyses.vue`、`AnalysisCard.vue`，名单页的提示词预览 `AnalysisPromptModal.vue`，记录详情的 AI 结论，概览的 `overview/AiReviewCard.vue` | 输出里最显眼的是"趋势"（偶发 / 持续 / 越来越重 / 多半是误报），用户说的"趋势分析"指的应该就是它（推测） |
| 概览的"趋势"卡 | 否 | `UI/messages.ts` `trendCard`（"各流程的可疑率与各审核节点"） | 统计图表 |
| 用户每日图表 | 否 | `UI/UserDailyChart.vue` | 近 30 天的审核、可疑、高危、出错 |

**当初的设计意图：** CONTRACTS §72.13.4（`next/docs/CONTRACTS.md:5629` 起）和 §72.13.6（:5706 起），2026-10-31，插件 0.3.0。

- 当时照搬 new-api 的 `analysis.go`、`analysis_tools.go`、`escalate.go`，目标是"带 5 个工具的 agent，结论自动调整名单"（CONTRACTS.md:5116）。
- 0.6.0 加了防注入：移出名单默认关闭，enforce 级用户只降级不移出（:5662 起）。
- 0.8.0 加了白名单、黑名单的限制。
- 界面原定为：名单详情抽屉里放"重新分析"、分析状态、分析卡片、时间线、名单历史（:5709-5714）。

### 9.2 现状

以下由子代理逐行核对、本人抽查关键行；行号指 `ed988819c`。

**触发**

- 违规计数在 `countWatchHit` 里（`M/store.go:313-358`）。
  - **只有 observe 级条目计数**；默认级别为拦截时，enforce 级条目也计（`M/store.go:318`）。
  - 计数达到 `analysis_every_violations`（默认 30）时清零并排队（`M/store.go:351-364`）。
- 手动触发：`POST /watchlist/:id/analyze`，需要 manage（`M/analysis_queue.go:463-510`）。
- 每分钟的 `analysis` 任务只负责调度。
- 新进名单、名单即将到期都**不会**触发。
- 白名单用户照样会被分析、产生费用。

**上下文**

- 系统提示词由分析准则（`analysis_guidelines`，为空时用内置准则 `M/analysis_guidelines.go`）、该用户当前分组绑定的流程、类别字典组成（`M/analysis_prompt.go:340-370`）。
- 不预先塞入记录，由模型调用 5 个工具获取（`M/analysis_tools.go:38-87`）。
- 原文放在 `<user_text>` 里（:208-223）。
- `analysis_context_kb` 用来截断和替换早期的工具结果（`M/analysis.go:298`、:428-453）。
- **没有轮数上限**，只受超时约束（`M/analysis.go:300`）。

**输出**

- `trend` 有 4 个取值，`suggestion` 有 3 个取值，另有总结、主要问题和逐条结论（`M/analysis.go:53-72`）。
- **没有总体的风险等级。**

**作用于名单**

- `escalationOf` 矩阵（`M/analysis_queue.go:219-252`）加上三个 `allow_*` 开关和两个名单（:258-273），共有十余种组合。其中不少组合"什么也不做、不留痕"，例如 enforce 级加 `block`、observe 级加 `observe`。
- AI 升级后 `expires_at` 置空，即**永久 enforce**（`M/analysis_queue.go:396-406`，只有降级会设有效期）。
- **没有"待确认"状态，也不能驳回、撤销。**
  - 现在界面上的"按 AI 建议执行"只是前端拿最近一次分析，再调普通的名单接口（现状 `UI/UserAnalysisBar.vue`，原型已删除）。
  - 撤销就是再手动改一次。
- AI 不能封禁。AI 判为误报的结论**不会**减少封禁计数（`M/store.go:443` 只排除人工误报）。

**保存**

- `user_analyses` 每个用户保留 `analysis_keep_per_user` 条（`M/analysis_queue.go:329`）。
- 记录上的 `ai_*` 会被覆盖；分析被裁剪后，记录里的 `ai_analysis_id` 会悬空。

**失败**

- 只有 ok 和 failed 两种状态；失败不重试，排队直接清掉（`M/analysis_queue.go:351`）。
- 渠道不可用时，排队一直挂着。

**费用**

- 每一步都按核心价格计价，记在来源"分析"下。概览只显示次数和 token。

**界面**

现状的分析信息分散在五处：

1. 用户抽屉中部的一行"分析状态 + 趋势 + 建议 + 执行按钮"（现状 `UI/UserAnalysisBar.vue`，放在概况之后）；
2. 抽屉下方的"分析"标签页（`UserAnalyses.vue`，卡片里有 trace）；
3. 记录详情里的 AI 结论；
4. 概览里的 AI 复核占比卡；
5. 名单历史里的 `auto_*` 动作。

此外，**看不到跨用户的结论**，例如"哪些用户在等我处理"。

### 9.3 哪里"设计不对"

| # | 问题 | 证据 |
|---|---|---|
| A1 | **定位不清**：同一次运行既是"逐条二次判断"，又是"用户趋势"，又是"处置"。界面主打"趋势"，真正影响用户的却是矩阵推出的处置，而处置规则不在界面上 | `M/analysis_queue.go:219-273`；现状 `UI/UserAnalysisBar.vue` |
| A2 | **模型被告知的后果与代码不一致**：准则说"解除时观察级、拦截级都一样直接移出"，代码对 enforce 级只降级，另有 high 严重度拦截，`allow_remove` 默认关闭。而且把后果写给模型，本身就会诱导它调整结论 | `M/analysis_guidelines.go:46` 对比 `M/analysis_queue.go:233-246`、:379-391 |
| A3 | **触发会丢、会偏**：分析运行期间到达的触发被合并后清掉；失败不重试，计数却已清零；手动排队也清零自动计数；enforce 级条目的误报撤不回计数；新进名单、到期前都不复核 | `M/store.go:362-364`、`M/analysis_queue.go:351`、:454-455、`M/routes.go:707` |
| A4 | **结论不可执行、不可追溯**：没有"待确认"状态和驳回动作；自动动作无法一键撤销；跳过的动作（`*_skipped`、`auto_not_allowed`）只在名单历史里留一行 | `M/analysis_queue.go:201-208`；CONTRACTS.md:5659 |
| A5 | **与名单、封禁规则重叠**：AI 升级会变成永久 enforce，与人工有期限的规则不同；AI 判误报不影响封禁和违规计数；白名单用户被分析却只能降级 | `M/analysis_queue.go:396-406`、`M/store.go:443` |
| A6 | **成本没有上限**：没有轮数上限，没有每日上限，没有同一用户的最短间隔 | `M/analysis.go:300` |
| A7 | **界面信息层级混乱**：结论分散在五处；抽屉里最重要的"建议"夹在概况和标签页之间；侧栏的"名单与处置 / AI 分析"只是一个设置页，没有可查看结论的视图 | §9.2 界面 |
| A8 | **与新信息架构不契合**：新侧栏把"名单与处置"作为处理人的地方，AI 分析却只占一个设置页；三个 `allow_*` 布尔开关与两个名单的交互，在"名单规则"页和"AI 分析"页两处都要理解 | §1.1、§3 |
| A9 | **小问题**：手动接口在"已排队"时返回 202 但什么也不做，CONTRACTS 写的是 409；分析被裁剪后记录上的 `ai_analysis_id` 悬空；不再拥有认领的节点也会写入分析行 | `M/analysis_queue.go:455`、:499；CONTRACTS.md:5633；`M/analysis_queue.go:304`、:329 |

### 9.4 方案

**定位：AI 复核 = 对名单用户的二次判断 + 一条可执行、可撤销的名单调整建议。** 逐条结论（`ai_*`）照旧写进记录，作为人工复核的参考。

**输出模型**（D8）

- 模型只提交 `risk`（low / medium / high）、`summary`、`main_problems` 和逐条结论；`trend` 保留为说明性标签。
- **建议动作由服务端按固定规则推出**，规则写在一处，界面上可以看到。原则是：
  - 风险高且有复核为违规的记录：升为拦截级；
  - 风险低、没有复核为违规的记录、近期没有高严重度记录：enforce 级降为观察级，observe 级移出名单；
  - 其余情况：不调整。
- 系统提示词里**不再写后果**（解决 A2）。

**生效方式**（D6）

- 每种调整（升级、降级、移出）各有一个设置：`auto`（自动执行）、`suggest`（只给建议）、`off`（不提出）。
  - 推荐值：升级 `auto`，降级和移出 `suggest`。
  - 两个名单的限制保留：只后台审核的用户不升级，总是先审的用户不降级、不移出。
- 建议的状态：`pending`、`applied`、`auto_applied`、`rejected`、`expired`（名单在此期间被人工改过，即原来的 stale）、`undone`。
- `pending` 可以采纳或驳回；`applied` 和 `auto_applied` 可以撤销。撤销恢复原级别和有效期。
- 每个动作写 `watch_events`，带上 `analysis_id` 和操作人，名单历史里可以追溯。
- AI 升级不再设为永久：沿用 `watch_days`，与人工规则一致（A5）。

**触发**（D7）

- 每 N 次违规触发，observe 和 enforce 级都计数。
- 名单到期前一天复核一次，决定是到期移出还是延长。
- 手动触发；在"AI 复核"页上可以批量触发（后续）。
- 同一用户最短间隔 `analysis_cooldown_minutes`（默认 60）。**间隔期内到达的触发记为"待补跑"，不清零、不丢**（A3）。
- 失败后按退避重试 1 次。
- 全站每天自动复核上限 `analysis_daily_limit`（默认 200）。
- 运行时设轮数上限（固定 12 轮），解决 A6。

**界面**（原型已实现）

- **侧栏"名单与处置 / AI 复核"**（`?tab=ai`，读权限）：
  - 顶部 3 个统计：待确认的建议数、今日复核次数（含失败数）、今日分析 token；
  - "待确认的建议"表：用户和当前级别、风险和趋势、总结（点开看分析过程）、建议框（采纳 / 驳回）；
  - "最近的复核"表：所有用户的结果、费用和时间。
  - 手机上建议框直接显示在卡片里，不收进"详情"。
- **用户抽屉**：
  - 第一张卡片是"AI 复核"：风险、趋势、总结、建议框（采纳 / 驳回 / 撤销，以及依据的记录链接）、下次触发条件、"重新复核"按钮；
  - 下方标签页改为"可疑记录"优先，其次是"分析记录"和"名单历史"；
  - 原来的 `UserAnalysisBar.vue`（"按 AI 建议执行"）已删除，由建议框取代。
- **设置**：
  - "AI 复核设置"页分三节：何时复核 / 结论如何生效 / 分析准则（折叠）；
  - 资源类设置移到"高级 / AI 复核资源"（折叠）。
- 新增 `?tab=watch&user=<id>` 深链。
- 组件复用：建议框 `ProposalBox.vue` 和决策逻辑 `useProposal.ts` 由"AI 复核"页和抽屉共用；风险和建议状态的颜色在 `tones.ts` 中统一定义。

**`analysis_*` 设置项去向**

| 键 | 现在 | 新位置 | 处理 | 默认 |
|---|---|---|---|---|
| `analysis_channel_id` | AI 分析 | AI 复核设置 / 何时复核 | 保留（文案"AI 分析渠道"） | 0 |
| `analysis_every_violations` | AI 分析 | AI 复核设置 / 何时复核 | 保留，改名"每多少次违规复核一次"；observe 和 enforce 都计数（后端） | 30 |
| `analysis_review_before_expiry` | — | AI 复核设置 / 何时复核 | **新增** | true |
| `analysis_cooldown_minutes` | — | AI 复核设置 / 何时复核 | **新增** | 60 |
| `analysis_daily_limit` | — | AI 复核设置 / 何时复核 | **新增**（0 = 不限） | 200 |
| `analysis_upgrade` | — | AI 复核设置 / 结论如何生效 | **新增**，取代 `analysis_allow_upgrade` | auto |
| `analysis_downgrade` | — | 同上 | **新增**，取代 `analysis_allow_downgrade` | auto（与现状一致；D6 推荐第 3 期改为 suggest） |
| `analysis_remove` | — | 同上 | **新增**，取代 `analysis_allow_remove` | suggest |
| `analysis_allow_upgrade`、`analysis_allow_downgrade`、`analysis_allow_remove` | AI 分析 | 隐藏 | **退役**，第 4 期删除（原型 `UI/settingsForm.ts` 的 `EFFECT_KEYS`）：<br>· 新键没保存过时按旧值显示（true 为 auto，false 为 suggest）；<br>· 保存新键时同步写回旧键（auto 为 true，否则 false），供混跑期间的旧节点读取；<br>· 若旧键在此期间被别人改过，保存前先确认（`retiredConflicts`）；<br>· 旧节点上"不提出"按"只给建议"处理，设置页已说明 | — |
| `analysis_guidelines` | AI 分析 | AI 复核设置 / 分析准则（折叠） | 保留；内置准则删去"后果"一段（A2） | "" |
| `analysis_timeout_ms`、`analysis_concurrency`、`analysis_context_kb`、`analysis_keep_per_user` | AI 分析 | 高级 / AI 复核资源（折叠） | 保留（资源类，很少改动） | 60000 / 2 / 64 / 20 |

**后端改动点与迁移**

- 以下改动**都不需要数据库迁移**，因为设置 JSON 的解码本来就宽松，可以 rolling：
  - 设置：`M/settings.go` 解码新键，并兼容旧的 `allow_*`；
  - 触发：`M/store.go:313-368` 去掉"只有 observe 级计数"；运行期间的触发改为设置"待补跑"标记；
  - `M/analysis_queue.go:351` 在有"待补跑"时不清掉排队；
  - 准则：`M/analysis_guidelines.go`；
  - 轮数上限：`M/analysis.go:300`。
- `user_analyses` 加列 `risk`、`proposal_action`、`proposal_from`、`proposal_to`、`proposal_status`、`proposal_evidence`、`decided_by`、`decided_at`；`watchlist` 加"待补跑"的时间列。这些**需要迁移**，属于加列，按约定走 maintenance。
- 新接口：
  - `GET /analyses?proposal=pending`（read）；
  - `POST /analyses/:id/decision {apply|reject|undo}`（manage），在 `block_subjects` 锁内校验状态和当前级别，写 `watch_events`。
- `escalateWatch` 改为"建议生成加按设置执行"：同一份规则产出 `proposal`，`auto` 直接执行，`suggest` 写入 `pending`（`M/analysis_queue.go:376-427`）。
- 发版顺序（rolling 时的注意）：新节点写 `proposal_*` 时，旧节点只是读不到，没有坏处。
- **能力开关：** 状态接口的 `runtime.features` 列出全部节点都已实现的能力（原型 `UI/types.ts` `Runtime.features`）：
  - `analyses_list`：跨用户的复核列表；
  - `proposals`：建议模型、决策接口和撤销。

  界面按能力开放功能：
  - 没有 `analyses_list` 时，"AI 复核"页显示说明，而不是报错；
  - 没有 `proposals` 时，"撤销"不出现。
- **与当前后端兼容：** 当前后端的分析没有 `proposal` 字段，`proposalOf()`（原型 `UI/useProposal.ts`）会把它的 `suggestion` 转成一条"待确认"的建议。
  - 这类建议仍通过名单接口执行，带上 `analysis_id`，与原来的"按 AI 建议执行"相同；
  - 不提供驳回和撤销；
  - 对审核管理员或"只后台审核"名单里的用户，不显示会被后端拒绝的操作，而是说明原因。

**归期**

- **第 1 期（无迁移）：**
  - 新增只读接口 `GET /analyses`（read，跨用户、可分页），并加上能力 `analyses_list`；
  - 侧栏"AI 复核"视图先只读，展示最近的复核，用现有的 `user_analyses` 数据；
  - 抽屉里的建议框兼容旧的 `suggestion`（见上文"与当前后端兼容"）；
  - 抽屉卡片调整顺序；
  - 设置拆分并改文案；
  - `?user=` 深链；
  - 准则去掉"后果"一段；
  - 修复手动接口的 202 / 409 不一致。
- **第 3 期（与 events 加列放在同一个 maintenance 窗口）：** 建议模型（`proposal_*` 列、决策接口、撤销）、新的触发方式与上限、`allow_*` 退役。

### 9.5 原型与截图

- 新增文件：
  - `UI/AiReviewTab.vue`：AI 复核页；
  - `UI/UserAiReview.vue`：抽屉卡片；
  - `UI/ProposalBox.vue`：建议框；
  - `UI/useProposal.ts`：决策逻辑。
- 修改的文件：
  - `UI/types.ts`：`Risk`、`Proposal`、`AnalysisRow`；
  - `UI/host.ts`：`fetchAnalyses`、`decideProposal`；
  - `UI/nav.ts` 和 manifest：增加 `ai` 条目；
  - `UI/WatchDetail.vue`、`UI/WatchTab.vue`：`?user=` 深链；
  - `forms/*.json`、`UI/settingsForm.ts`：`EFFECT_KEYS` 的读取与回写；
  - `UI/messages.ts`；
  - web mock：分析带 `risk` 和 `proposal`，新增两个接口；
  - `design/shots.mjs`：增加 ai 截图。
- 删除 `UI/UserAnalysisBar.vue`，以及它独用的 9 个文案键。
- 截图（`design/shots/`，可用 `ONLY=ai-` 只重拍这一组）：

| 内容 | 桌面 1440 | 暗色 | 手机 |
|---|---|---|---|
| 复核结果与待确认建议 | `ai-desktop-review` | `ai-dark-review` | `ai-mobile-review` |
| 采纳的确认框 | `ai-desktop-apply-confirm` | — | — |
| 用户详情里的复核卡片（待确认） | `ai-desktop-user-pending` | `ai-dark-user-pending` | `ai-mobile-user-pending` |
| 用户详情（已自动执行，可撤销） | `ai-desktop-user-auto-applied` | — | — |
| 用户详情（排队中 + 重新复核入口） | `ai-desktop-user-queued` | — | — |
| AI 复核设置 | `ai-desktop-settings` | `ai-dark-settings` | `ai-mobile-settings` |
| 高级 / AI 复核资源 | `ai-desktop-advanced-resources` | — | — |
| 侧栏中的入口 | — | — | `ai-mobile-sidebar` |
| 当前后端：没有跨用户列表时的说明 | `ai-desktop-unsupported` | — | — |
| 当前后端：抽屉里的旧建议（只能采纳，经名单接口执行） | `ai-desktop-user-legacy` | — | — |

### 9.6 独立审查

独立审查子代理只读取本部分的 diff（`bcdc8fc74e..eca9e64819`），共报告 20 条（4 高、8 中、8 低）。修复都在后续提交里。

| 级别 | 发现 | 处理 |
|---|---|---|
| 高 | 三个 `analysis_allow_*` 设为隐藏后，Go 测试 `TestSettingsForms` 会失败 | 已修：`manifest_test.go` 的 `legacy` 列表加上这三个键；`go test .` 通过 |
| 高 | 新增的 `?user=` 写回会与切换分节的 push 竞争，导致导航被取消（例如抽屉里"查看记录"、从 AI 复核页打开用户时） | 已修：抽屉完全由地址驱动（`?tab=watch&user=`），打开和关闭都只改地址；`useSection` 在真正切换分节时去掉 `user` 和 `record`，`?user=` 链接默认打开观察名单；删除 `preset` 通道；补了测试 |
| 高 | 当前后端的分析没有 `proposal`，抽屉对每次分析都显示"无需调整"，原来的"按 AI 建议执行"也没了 | 已修：`proposalOf()` 把旧的 `suggestion` 转成可采纳的建议，经名单接口执行（截图 `ai-desktop-user-legacy`） |
| 高 | 当前后端没有 `GET /analyses`，AI 复核页对所有人报错 | 已修：按能力 `analyses_list` 开放，没有时显示说明（`ai-desktop-unsupported`）；设计在第 1 期加入该接口 |
| 中 | 没保存过旧开关时，降级显示为"只给建议"，实际节点却会自动执行 | 已修：`analysis_downgrade` 的 schema 默认值改为 auto，与 Go 默认一致；D6 的改动推迟到第 3 期 |
| 中 | 写回旧开关时，会无提示地覆盖别人同时做的修改；"不提出"在旧节点上会退化 | 已修：新增 `retiredConflicts` 进入确认流程；设置页说明了退化 |
| 中 | 新的触发设置在当前后端上不生效，抽屉却总显示"到期前复核" | 已修：设置页注明需要新版本；抽屉只显示后端给出的 `review_at` |
| 中 | 在一处做出决定后，另一处的列表（观察名单行、AI 复核页）不刷新 | 已修：共享的 `proposalsVersion`，各视图收到后重载 |
| 中 | 对审核管理员、"只后台审核"名单里的用户，也显示了会被拒绝的采纳按钮 | 已修：`blockedReason()` 改为显示原因 |
| 中 | 撤销没有按设计等全部节点升级后再开放 | 已修：用能力 `proposals` 控制 |
| 中 | mock 的决策接口状态有误（重新加入时缺 `user_id`、没有 expired 路径、撤销不恢复有效期、未知决定也会生效） | 已修：校验决定；级别已被改动时返回 409 并标为 expired；保存被替换的条目，撤销时恢复；采纳后让同一用户的其它待确认建议失效 |
| 中 | mock 的"重新复核"复用了 #21 的建议对象 | 已修：新的复核结果 `proposal: null`、`risk: medium` |
| 低 | 待确认列表只取 50 条，没有提示 | 已修：超出时显示"只显示前 50 条"。分页留到正式实现 |
| 低 | 决定后返回的分析对象没有使用；判断逻辑在两处重复 | 已修：`decided` 不再带数据；`proposalOf` 统一放在 `useProposal.ts` |
| 低 | AI 页没有按过时（stale）规则处理 | 已修：同由 `proposalOf` 处理 |
| 低 | 抽屉文案："还剩 0 次"、英文首字母小写、不在名单的用户也显示卡片、丢了排队时间 | 已修 |
| 低 | "分析"与"复核"用词混用；总结为空时显示"—" | 总结已改用 `ai.noSummary`；"AI 分析渠道"等现有文案保留（渠道的名字），其余名称留到第 1 期统一 |
| 低 | AI 复核设置与"高级"用了同一个图标；`WatchDetail` 多了一个空行 | 已修：改用 `role` 图标 |
| 低 | 手机布局：待确认表的时间被收进"详情" | 已修：时间放到卡片正文，最近复核的时间收进详情 |
| 低 | 多处没有测试 | 已补：`useProposal.spec.ts`（旧建议的转换、过时、拦截原因）、`settingsForm.spec`（默认值、冲突、写回）、`nav.spec`（`?user=`）。决策接口的 mock 状态转换和组件交互没有单测，以截图人工核对 |

修复后的验证：

- 插件 UI：vitest 18 个文件、185 个用例全部通过，vue-tsc 无错误；
- Go：`next/plugins/moderation` 的 manifest 测试通过；
- 截图共 42 张，全部重拍。
