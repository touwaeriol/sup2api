# 核心失败处理设计与实现

> 2026-10-27 清理：文中引用的本机 `artifacts/` 与 ovh 上 `~/sup2api/checks/`、旧 `release-*`、`~/sup2api-managed/{stage,backups,publish,ccgateway-images}/` 下的旧版本原始记录已删除，路径仅作出处；当时的现存位置记在 ccgateway 插件的 `docs/CLI-TEST-PLAN.md`（插件已在 0.1.290 删除，原文 `git show 317b0c331:next/plugins/ccgateway/docs/CLI-TEST-PLAN.md`）。

日期：2026-10-15。状态：核心 0.1.154 已部署到生产四节点，UI 合约 3.0.0，只保留增强 STabs 公共组件；STabLayout 无导出、别名或实现。线上 moderation 0.1.11 已完成迁移，管理员既存策略（revision=1）保持原值。

## 增强 STabs 主入口

从核心 0.1.154 起，公共主入口恢复为 `STabs`，实现仍包含内容面板与标题上下文。重复标题由内容标题组件按上下文处理，不删标签文字。删除 `STabLayout`，不保留兼容别名、包装或重复实现；内部 TabBar 只承担导航渲染，继续不公开。

控制台所有标签页统一使用 STabs。使用增强入口的插件声明最低核心 0.1.154、UI 3.0；growth 0.1.4 和 moderation 0.1.11 已迁移。guard 0.2.6 不使用标签接口，兼容声明覆盖 UI 3.0。

## UI 2.0 标签接口收敛

- 移除旧 `STabs` 导出及源码入口，底层收进 `internal/TabBar.vue`，只由 `STabLayout` 使用。迁移账号详情、我的用量、价格同步及 growth/moderation 内部标签，连同内容放入容器，保留分页、筛选、选中项及已访问页面状态。
- 控制台及 Vite preset 的 `HOST_UI_VERSION` 改为 `2.0.0`。moderation 0.1.10、growth 0.1.3、guard 0.2.5 声明 `>=1.0.0 <3.0.0`；前两者最低核心版本 0.1.151（首次提供新容器）。新版插件能先在 UI 1.0 上部署，再切换核心，旧插件只声明 UI 1 时由加载器明确拒绝。
- 原审计日志和历史文档中出现的旧组件名称保留作为历史证据。运行源码、公共组件列表和当前使用说明以新接口为准。

## 平台标题去重

公共 `STabLayout` 统一标签栏与内容上下文。系统设置全部标签、插件详情各标签及 CCGateway 设置已接入；`SPageHeader`、`SSettingsGroup`、`SSectionTitle` 复用同一判定，默认隐藏与当前可见标签相同的标题。不同分组、操作和说明保留；单标签栏隐藏后恢复标题；折叠、自定义插槽和弹窗/抽屉按独立交互处理。

完整组件使用约定见 `next/web/packages/ui/README.md`。本次保持现有插件包不变；宿主中的声明式插件设置自动继承，插件自己编排的新标签页按公共容器约定接入，不能依赖平台修改原生 HTML 标题。

## 调度策略界面与分区保存

- 系统设置标签及相关提示统一命名为“调度策略”，保留原 `/settings/failure-policy` 接口和 `tab=failure` 地址兼容。
- 去掉标签下重复的页面标题，以及规则表与所属卡片重复的标题。插件设置通用宿主 `SettingsTab` 没有额外页面标题；CCGateway 在插件详情内使用 `embedded` 隐藏自身页头，原生插件页宿主也已避免重复渲染页头。
- 自动重试、自动禁用各自在卡片底部提供保存按钮。保存时用已保存的另一分区与当前分区组合请求，成功后保留另一分区未保存的草稿；校验不会被另一分区的无效草稿阻止。沿用服务端版本冲突保护，保存过程中阻止并行提交。
- 对旧 Schema 的首次保存仍按既有迁移规则升级完整策略格式；升级发布本身不会自动保存或改写业务配置。

### 插件系统配置资源前缀检查

当前没有插件可声明“系统配置键前缀”的 manifest 字段，也没有对应前缀授权/冲突检查机制。这与既有插件配置隔离是两件事：

| 配置 | 声明 / 隔离 | 实际入口及权限 |
| --- | --- | --- |
| 通用插件设置 | `ui.settings` 声明表单；`plugins.config_enc` 按插件 key 保存，AES-GCM AAD 为 `plugin-config:<key>`；运行时仅加载该 key 配置后发送 `ConfigureRequest` | `/api/v1/plugins/:key/settings`，读 `plugin:read`、写 `plugin:manage` |
| CCGateway 系统配置 | manifest 没有 `ui.settings` 或系统配置前缀声明；插件详情页由宿主特殊嵌入核心 CCGateway 设置组件 | 核心注册 `/api/v1/system/ccgateway/*`，配置键 `ccgateway_remote`，AAD `system:ccgateway:v1`，读取 `settings:read`、保存 `settings:manage` |
| 调度策略 | 核心 `failure_policy` 设置键，无插件声明依赖 | 核心 `/api/v1/settings/failure-policy` |

证据：`next/sdk/manifest/manifest.go`、`next/plugins/ccgateway/manifest.json`、`next/server/internal/plugin/api/{api,settings}.go`、`next/server/internal/plugin/grpcruntime/{runtime,instance}.go`、`next/server/internal/ccgateway/{config,http}.go`。核心 API 路由前缀由 `next/sdk/manifest/routes.go` 预留，插件自有 API 经 `/api/v1/p/<key>` 隔离；这不能代替系统配置键的声明。本次按“检查”范围记录现状，不引入新的插件权限或迁移插件配置。

## Schema 4：固定条件关系与 Go 正则

> 2026-11-01 修订（用户要求）：状态码与关键词改为固定 **AND**：两者都填时，状态码命中**且**某个关键词（正则）命中才算匹配；只填其一时按那一项匹配。原来是 OR，带关键词的自动禁用规则只凭 401 就会禁用账号。Schema 3 及以上都按此执行（加载时 `mode` 统一改为 `all`，存量 Schema 3 策略里迁移生成的 `any` 不再生效），Schema 1/2 仍按存的 `mode`。自动禁用写入账号的原因带上命中的关键词，如 `failure policy <规则 ID>: upstream HTTP 401, keyword "permission denied"`；决策记录与规则预览新增 `account_keyword`。未发布失败策略时使用的旧自动禁用设置（`auto_disable`，new-api 语义）不变，仍为状态码或关键词任一命中。
>
> 同时拆分迁移来的自动禁用规则（迁移 0072）：已发布策略的账号规则仍只有 `migrated-disable`（401 + new-api 关键词、未改过）时，拆成三条，依次为 `login-invalid`（401 + 登录失效关键词：`oauth access token has been revoked`、`oauth refresh token is no longer valid`、`oauth_refresh_dead`、`not_logged_in`、`has cleared its stored login`、`invalid x-api-key` 等）、`account-on-hold`（403 + `oauth_account_on_hold` 等）、`migrated-disable`（原关键词，不限状态码，与原先关键词单独命中即禁用一致）。旧策略进历史，revision +1。迁移草稿（`migrationDraft`）按同样方式生成。关键词来自线上近 30 天真实 401 错误与 CLI/Worker 的登录错误码（`loginAccountRules`，测试校验与迁移一致）。迁移 0074 补上 0072 跳过的情况：管理员另加过账号规则时，在账号规则数组里按 id 找到仍为 401 的 `migrated-disable`，原位换成同样的三条（策略里已有同 id 的规则则不重复添加），其余规则顺序不变。迁移 0075（2026-11-02）给 `login-invalid` 追加 `stored_login_refused_restart_needed`、给 `account-on-hold` 追加 `stored_login_refused_admin`（CLI v44 对 2.1.316 `StoredLoginHeldBackError` 的 401/403 错误码），条件与理由见 CONTRACTS §58。0.1.290 删除 CCGateway 后，代码默认规则不再带只有 CCGateway 会回的关键词（`oauth refresh token is no longer valid`、`oauth_refresh_dead`、`not_logged_in`、`has cleared its stored login`、`oauth_account_on_hold` 与 0075 的两个），迁移 0102 从已发布的策略里去掉（只改关键词仍与 0072/0074/0075 写入的完全相同的规则），见 CONTRACTS §101.5。

- 移除“条件关系”控件。新版保存为 Schema 4，状态码与文本条件的关系固定（原为 OR，现为 AND，见上）；关键词或正则均一行一个，多行之间是 OR，页面不再展示条件关系提示。
- 表格内复用公共 `SCheckbox` 提供“Go 正则”选项，默认为关闭。关闭时保留忽略大小写的普通子串匹配；开启时以 Go `regexp` 编译各行，区分大小写，支持 `(?i)` 显式忽略大小写。不混用 JavaScript 正则校验，不添加 `/.../` 分隔符。
- `match.keyword_regex` 只在 Schema 4 支持；旧 Schema 1–3 继续按原模式运行，页面编辑生成新版草稿，保存才生效。升级程序不修改管理员现有策略。旧核心拒绝 Schema 4，避免把正则误当普通关键词执行。
- 编译在保存、预览及加载策略时进行，实际错误判定复用已编译表达式。保留语法大小写及有效空格，跳过空白行并去重；每条最多 200 字符、每规则最多 100 条，错误文本仍限前 4 KiB。无效表达式返回字段错误和原始行号，拒绝保存，不回退为文本匹配。
- 重试、自动禁用、规则预览共用匹配函数；原来源和范围约束继续作为前置条件。服务端错误按规则 ID 对应到关键词单元格，拖动后仍指向原规则，修改内容后清除该行旧提示。

## Schema 3 简化设置（本次更新）

当前实现以本节为准；下文 Schema 1/2 的动作、出口和等待设置仅用于兼容历史配置。

- 页面只有“保存”，没有撤销或历史版本入口。后台仍保留原有历史 API/记录以兼容旧客户端，不删除既存数据。
- 自动重试、自动禁用为两张独立卡片，保留各自总开关。重试只配置总次数、切换账号、单账号次数和首次退避间隔；只有开启换号才显示单账号次数。
- 规则表接入公共 `STable` 手柄排序，左侧拖动，单元格直接编辑，右侧复制、删除；去掉逐条启用开关、动作列和列表条件关系提示。组件只返回顺序，点击页面保存才持久化。
- 新增及编辑均直接在表格内完成，无弹窗；关键词使用多行文本域；错误码允许多个和区间，支持中文逗号、分号与换行的规范化。只填错误码或只填关键词均有效；Schema 3 旧版两者都填时曾显示 AND/OR 编辑项；当前界面以 Schema 4 为准。移除高级匹配范围编辑入口，新规则默认匹配上游错误、不限分组/模型/协议；编辑已有规则保留原来源与范围，有范围限制时提供只读查看。保存和预览统一检查规则，错误提示跟随规则 ID，不随排序错位。
- Schema 3 所有列出的规则均启用：请求规则固定重试，账号规则固定自动禁用。服务端校验也固定这些语义，不依赖隐藏的旧动作值。
- 换号固定清除粘性绑定，使用候选账号自身配置的出口，不再限制相同代理记录。已有代理故障处理不变，不创建直连兜底。
- 不再遵守 Retry-After，也没有独立累计等待预算。保留指数退避、单次基础间隔 30 秒上限、请求取消/期限和总重试次数限制。Schema 1/2 的既存等待逻辑保持兼容。
- 新规则 ID 使用公共 `createItemId`，不依赖仅安全上下文提供的 `crypto.randomUUID()`，解决 HTTP 环境下规则弹窗初始化失败的问题；标识仅用于 UI 记录，不作安全凭据。
- 旧配置不会被升级程序自动改写。页面先生成 Schema 3 待保存配置，保留启用的重试/账号条件，排除关闭、返回错误和不处理规则，冷却条件转换为禁用；展示具体迁移语义，保存后才切换。首次未配置时由核心生成 Schema 3 草稿。

验证：本次前端 115 项相关测试、类型检查、生产构建和 ESLint 通过。真实浏览器在 `randomUUID` 不可用环境下验证行内添加、编辑、复制、删除、拖拽、统一保存和条件输入，移动端页面无横向溢出。后端未修改；0.1.147 的 gateway/account/usage/core 回归记录保留于发布验收。

本功能由核心提供，适用于核心管理的通用网关请求，与具体插件无关。管理员配置失败条件和处理动作；核心读取自己收到的状态码、错误文本并执行。新策略不调用插件错误分类，不添加插件接口，不修改任何插件、CLI 或代理组件。

## 职责与界面设计

| 层次 | 职责 | 实现位置 |
| --- | --- | --- |
| 纯规则模型 | 条件匹配、两表首条命中、默认动作 | `server/internal/gateway/failure_policy.go` |
| 校验与规范化 | 字段范围、空关键词、状态码区间、规则数量 | `failure_policy_validate.go` |
| 配置 API | 发布版本、并发控制、历史、预览 | `failure_policy_api.go` |
| 请求执行 | 次数、退避、账号动作、换号和出口限制 | `failure_policy_runtime.go` |
| 粘性存储 | 带代数的读取、重置和成功写回 | `failure_policy_sticky.go` |
| 前端页面 | 组合设置卡片、规则表和预览 | `web/src/views/settings/FailurePolicySettings.vue` |
| 前端状态 | 加载、保存、冲突处理与迁移草稿 | `useFailurePolicy.ts` |

界面入口为“系统设置 → 失败处理”。保留网关页的通用超时设置；重试和自动禁用在新入口统一管理。

> 2026-11-02：使用记录详情里的决策记录（`anomalies.failure_policy`）对没有 `account:read` 的管理员去掉 `account_id`，此前会漏给这类管理员（CONTRACTS §15.2）。模型可用性的执行前检查拦下账号时，结论不交给失败策略，不冷却、不停用账号，只换号（CONTRACTS §44.7）。

> 2026-11-18（转发 V2 收尾）：插件不可用（`Execute` 报错或崩溃、中继被核心拒绝）、SSRF 拒绝的目标地址、不被允许的目标主机和模型可用性一样走“只换号、不执行账号规则”的路径；尝试次数用尽时结果与没有发布策略时相同（请求自己的失败原样返回，其余 503 `no_available_account`，Anthropic 格式 529）。此前它们在策略下按核心的 503 处理，一次尝试就回 503 `plugin_unavailable`，命中核心来源的账号规则还会冷却或禁用账号（CONTRACTS §58）。

页面分为“自动重试”和“自动禁用”两张独立设置卡片：前者包含重试开关、统一计划和重试规则；后者包含自动禁用开关和禁用规则。两块复用现有公共设置卡片、行和规则表，页面统一保存并检查版本冲突。

0.1.146 的界面：先显示“达到次数后切换账号”开关，仅开启时显示每账号重试次数和换号出口限制。换号自动清除原粘性绑定，不再提供切换开关；页面预览、发布统一提交 `reset_sticky=true`。后端保留历史字段兼容，已保存策略不因打开页面而改变；历史保留绑定策略显示迁移草稿，发布后才采用固定重置规则。关闭换号时，未完成填写的隐藏次数不阻止发布。

| 界面区域 | 公共组件 | 行为规范 |
| --- | --- | --- |
| 设置与发布 | `SSettingsGroup`、`SSettingRow`、`SettingsSaveBar`、`SInput`、`SSwitch` | 沿用设置页卡片、间距、按钮和错误样式；保存条仅增加可选按钮文案 |
| 两类规则表 | `SSectionTitle`、`STable`、`SSwitch`、`SDropdown` | 与粘性规则页一致；菜单支持编辑、复制、上下移动和删除，表格行也可拖动排序 |
| 规则编辑 | `SModal`、`SField`、`SGrid`、`SSelect`、`STagInput`、`STextarea` | 编辑独立副本；应用后进入页面草稿，取消不改原规则 |
| 规则预览 | `SSettingsGroup`、`SDescriptions`、`SDescItem`、`SHint` | 显示规则名称、动作和出口限制，不以 JSON 替代结果界面 |
| 历史与状态 | `SDisclosure`、`SSelect`、`SButton`、`SAlert` | 恢复版本只填入草稿，发布才生效 |

没有新增局部 CSS、写死颜色或重写基础控件。页面只负责组合；规则表、弹窗、共用字段编辑器、预览与结果展示分别封装。两类规则复用同一个条件编辑器；只读用户不能编辑、排序或发布。

## 配置与匹配

策略包含重试开关、最多额外重试次数、每账号承接次数、是否换号、换号是否重置粘性绑定、出口限制、首次退避间隔、累计等待预算、是否遵守 Retry-After、自动冷却/禁用开关、重试规则表和账号规则表。

- 最多额外重试为 0–9 次，总尝试上限为 1–10 次。关闭自动重试或设为 0，只进行首次尝试。
- 两张表各自按数组顺序取首条匹配规则；互不触发对方。没有命中时返回错误且不改变账号。
- 规则范围支持分组、模型和协议。不同维度同时满足，同一维度多个值满足任一；模型支持现有通配匹配。
- 来源分为上游 HTTP 响应、连接/传输错误、核心准备/调度错误。范围与来源始终是前置限制。
- HTTP 状态码支持 400–599 与闭区间，例如 `429,500-599`。匹配核心收到的原始错误状态，不用最后包装给客户端的状态码替代。
- 传输失败没有 HTTP 状态码，不能填写状态码；条件均为空时表示所有传输错误。
- 关键词为忽略大小写的包含匹配，多个词命中任一。规范化时去掉空行、首尾空白和重复项。
- 匹配错误文本的前 4 KiB，预览使用同样的截断规则；不扫描正常聊天历史或工具输出。

| 状态码 | 关键词 | 结果 |
| --- | --- | --- |
| 填写 | 留空 | 仅按状态码匹配，允许保存与执行 |
| 留空 | 填写 | 仅按错误关键词匹配 |
| 填写 | 填写，AND | 两者都命中 |
| 填写 | 填写，OR | 任一命中 |
| 留空 | 留空，上游/核心来源 | 拒绝发布，不隐式匹配所有错误 |

每表最多 100 条规则；每条最多 100 个关键词、每词 200 字符。关键词为空保存为 `[]`，不会重新补入默认词。

## 请求动作

| 动作 | 当前请求 | 粘性绑定 |
| --- | --- | --- |
| 返回错误 | 不再尝试 | 保留，独立禁用清理规则除外 |
| 原账号重试 | 同账号、同出口配置再次尝试 | 保留 |
| 换号，保留原绑定 | 排除失败账号，临时选其他合格账号 | 临时账号成功也不覆盖原绑定 |
| 换号，重置绑定 | 解除当前绑定，排除失败账号后重新选择 | 新账号成功后绑定；全部失败保持未绑定 |

以上是核心执行动作。Schema 2 的请求规则仅选择“返回错误”或“按统一计划重试/换号”，不再分别配置重试次数和换号动作。全局设置共同决定执行计划；离开一个账号后本次请求不再返回，避免 A→B→A。

| 全局字段 | 含义 |
| --- | --- |
| `max_retries` | 首次请求之外的总重试预算，0–9 |
| `retries_per_account` | 每账号承接多少次重试后换号，0–9；0 表示每次重试换号 |
| `switch_account` | 关闭则全部重试留在原账号，忽略每账号阈值 |
| `reset_sticky` | 换号时解除旧粘性绑定；关闭则只临时换号、保留旧绑定 |
| `egress` | 整个请求共用的换号出口约束 |

例如总重试 5 次、每账号 2 次：首次请求 A0，随后 A1、A2、B3、B4、C5。完整请求序列为 **A → A → A → B → B → C**，最多 6 次请求。B、C 上的首次请求也占用总预算和该账号的重试名额。每次失败重新匹配错误条件，但不重置计数；成功立即结束。账号在处理后被冷却或禁用时，若允许换号则提前切换，否则结束。没有可用候选账号时直接返回错误。

界面实时展示该计划在连续失败且候选充足时的请求顺序。重试和自动账号处理均支持只填状态码、只填关键词，以及两者 AND/OR；共用条件编辑器与校验器。

新策略的明确换号动作覆盖旧软粘性 `OnFailure=stick`。粘性页面在新策略发布后隐藏旧失败动作，提示到失败处理页配置。硬资源归属约束仍然有效。

## 自动账号处理

账号规则的动作是“不改变”“冷却”“自动禁用”：

- “自动冷却 / 禁用账号”关闭时不执行冷却或禁用，不影响独立的自动重试与换号。
- 自动禁用还需账号自身允许；账号拒绝禁用时不暗中改为冷却。
- 禁用或冷却本身不强制本次请求换号。是否继续由重试表决定。
- 冷却时长为 1–86400 秒。禁用使用现有 `auto_disabled` 状态，需人工恢复。
- 账号不可用后不能继续原账号重试；Schema 2 仅在开启全局换号时才能继续。
- 状态写入失败记录为 `write_failed`，不假称成功、不换成其他账号动作。
- 禁用和冷却均使用请求开始时的策略快照。禁用通过核心专用 `AutoDisableByPolicy` 端口执行，仍检查账号自身许可，不再读取较新版本的全局开关；旧接口保持原全局检查语义。
- 显式“换号保留绑定”优先保留；其余情况下禁用后清理遵守原有 `keep_on_account_disabled`。显式“换号重置”不受该保留开关阻挡。

已有账号诊断请求不自动换号或修改账号状态；额度查询、后台任务不自动加入本业务请求规则引擎。

## 粘性并发与重放边界

绑定值包含随机代数和账号 ID。读取时为不存在的绑定创建一个未绑定代数；重置和成功写回均比较原代数后原子更新。旧并发请求不能覆盖新绑定；绑定过期或手工清除后，迟到结果也不能复活它。

读取旧的数字账号值仍然兼容。升级后的旧策略写入也增加比较操作，防止新旧请求交叠时覆盖新格式。旧版本进程不具备此保护，首次启用前须完成所有核心节点升级。

清理只是解除路由粘性，不删除历史、凭据、文件或工具内容。依赖核心已知账号资源或 `previous_response_id` 的请求不能用清理绕过归属。核心不会迁移插件私有会话或改写工具历史。

以下情况禁止自动重放，即使命中重试规则：

- 响应已经对客户端提交，或 WebSocket 已完成客户端升级。
- 任务、辅助操作或计费提交已进入不可安全重复的阶段。
- 传输失败发生在请求已写出之后，结果不确定。
- 请求取消、到达重试次数或等待预算上限。
- 现有网关路径明确返回不可重放结果。

SSE 首次响应提交前的 HTTP 失败可按规则处理；流内错误只执行账号处理和记录，不从头重放。现有 WebSocket 实现先升级客户端再选择上游，因此其升级后失败不自动重试，仍可按规则处理账号状态。该限制在 HTTP 与 WebSocket 入口共用执行检查。

## 出口与等待

重试始终使用账号已配置出口，不修改代理、不绕过代理组件。

- `same_proxy`：只选与最初账号相同代理记录的账号；最初显式直连则只允许显式直连。
- `proxy_only`：只选绑定代理的账号，可换不同代理。
- `account_configured`：允许候选账号各自配置，包括显式直连；界面明确显示此含义。

默认换号限制为 `same_proxy`。Schema 2 使用固定的全局限制；兼容的 Schema 1 将后续规则与已经生效的限制取交集，不能逐步放宽。重复使用同一账号时若代理配置改变，停止该账号的重试；加载账号与选中快照不一致时直接返回错误。没有合格账号即结束。

退避采用指数增长和抖动，单次基础间隔上限 30 秒。遵守 Retry-After 时取其要求与退避的较大值；超过累计等待预算或现有请求期限则停止，不提前重试。等待响应取消，不占用已释放的账号并发槽位。

这些次数只约束核心发起的尝试。插件、SDK、CLI 内部调用不属于本功能的计数范围，也不通过本功能改写。

## API 与配置版本

| 方法与路径 | 权限 | 功能 |
| --- | --- | --- |
| GET `/api/v1/settings/failure-policy` | `settings:read` | 返回 `configured` 与当前策略，未发布时返回迁移草稿 |
| PUT 同路径 | `settings:manage` | 接收 `expected_revision` 和完整 `policy`，校验后发布 |
| POST `/api/v1/settings/failure-policy/preview` | `settings:read` | 用草稿与输入错误做纯匹配，不发请求或改状态 |
| GET `/api/v1/settings/failure-policy/history` | `settings:read` | 最近 10 个被替换的版本 |

配置保存在现有 settings 表，无新增数据库迁移。发布与旧重试/禁用设置更新共用事务锁；版本冲突返回 409。历史快照与新配置同事务提交。

Schema 2 新增全局计划，仍兼容执行已保存的 Schema 1。升级程序不会自动改写业务配置。页面将 Schema 1 转为可审阅的 Schema 2 草稿：按首条启用的旧重试规则填入全局次数、换号和出口，保留每条错误条件和直接返回例外。页面明确提示旧版每条规则的独立计划将合并，管理员发布后才生效。从历史恢复 Schema 1 同样先生成迁移草稿。所有节点升级完成前不要发布 Schema 2；旧核心不支持读取它。

各节点复用配置变更广播与现有缓存 TTL，请求开始固定一份策略直到结束。无效已发布配置或读取失败会拒绝业务请求，不退回隐藏默认策略。广播与缓存不保证所有节点同一时刻切换；严格切换须结合现有摘流发布流程。

发布后旧 `/settings/gateway` 的 `max_attempts` 改动、旧 `/settings/auto-disable` 写入返回 409，避免两个入口互相覆盖；`/settings/gateway` 的写入带着与当前值相同的 `max_attempts`（例如把 GET 的结果原样 PUT 回去）不算改动，照常保存（2026-11-18 起，此前也返回 409）。网关超时设置仍可单独修改。账号编辑器读取的新全局禁用状态也来自核心失败策略。

## 迁移与启用

1. 升级所有核心节点；不需要升级插件。
2. 打开失败处理页，检查迁移草稿。旧 `max_attempts=3` 转为额外重试 2 次；旧禁用条件保留 OR 语义。
3. 草稿提供常见 HTTP 失败重试规则，默认限制同一代理。旧插件的隐含动作、禁用失败转冷却等行为不会自动复制。
4. 编辑、测试匹配结果后发布。首次发布前继续旧行为，首次发布后由核心通用规则接管。
5. 要回滚策略，从历史填入草稿并重新发布；不会自动退回旧隐藏行为。

预览只证明规则匹配，不模拟实时账号池或副作用。实际执行还会检查预算、账号状态、资源归属和响应提交状态。

## 日志与验证

每次失败记录策略版本、尝试序号、账号、来源、状态码、两个命中规则、计划动作、执行/停止原因和账号处理结果。信息保存到现有用量记录 `anomalies.failure_policy`，用量详情以公共描述组件显示；它是过程记录，不计入“异常请求”统计，不保存完整请求或凭据。

本地验证：

- Schema 2 回归覆盖 5 次重试按 2 次分配到账号、0 阈值即时换号、关闭换号、9 次上限、禁用/冷却提前换号或停止、跨账号粘性保留/重置，以及两张规则表的仅状态码/仅关键词/AND/OR。前端 31 项相关测试通过，覆盖实时顺序、旧策略迁移草稿、只填关键词发布和统一动作选择。

- 核心 gateway、account、usage、core 四个模块测试通过，含状态码单独匹配、AND/OR、关闭重试、共享预算、独立禁用、并发绑定、禁止放宽出口、传输结果不确定、API 版本与历史。
- 前端类型检查、生产构建、27 项相关测试通过，覆盖空白/小数预算拒绝、只读完整详情；后端四个相关模块测试和全项目编译检查通过，增加当时插件执行路径不调用插件分类的用例（转发 V2 起只有传输错误不问插件，等待形式用 `Execute` 返回的分类，CONTRACTS §69、§112.2）、模型准入刷新、策略快照授权和 null 预算拒绝。
- 新前端组件 ESLint 检查通过；在隔离浏览器预览中检查了公共卡片、表格与规则编辑弹窗。

生产发布记录：

- 源码提交 `54afc35df64663f6fccf4a47025cf783faa3f020`；核心 `0.1.143`。
- Manifest `2268d89afb4f40cef3f827d82206bea5da4c9f26b530ee3c00e6117d7ee1a52d`；bundle `394dfd9d6d41ecb1b223dd255b2c1a856b507fea89ea7d06e9958a71fd8219ac`。
- 升级计划 `99fe4001fbe981791c087abcebbf1ba7` 完成 27 步；四节点 readyz 均 200，核心二进制 SHA-256 均为 `29c9fb9881ceb90a58de203e718209d83f5812ef1b57c65be1fffa126af3e0a6`。
- 六个内置插件包哈希未变；8 个 Worker、控制器、共享 Xray 共 10 个容器的 ID、镜像和启动时间未变。
- 新规则接口与状态码单独匹配预览正常；前端资源 `SettingsView-BHzO1qz9.js` 已发布；账号 21 的 `claude-haiku-4-5-20251001` 请求返回 200。
- 既有 gateway、auto-disable、sticky 设置保持原值；四节点均 `policy_configured=false`，首次发布规则由管理员完成。
- 私有备份与验收记录：`/home/debian/sup2api-managed/backups/failure-policy-0.1.143/`。数据库备份 19,572,749 字节，SHA-256 `c7a840ee50aefe1f7d5ef0d78103aef66ff17194cfa33c9868da2730721de921`，恢复目录可读取。本次未执行数据库恢复。

## 参考

### 0.1.144 统一重试计划发布验收

- 源码提交 `8b801e20ed708b766cebbcd322f61886312db005`；四节点核心版本均为 `0.1.144`，readyz 均为 200。
- Manifest `380ea3e403c8a1042f4bdf6afe7e214357551b8d1a2fb5c6a979133834dfb1eb`；bundle `446e581ca155e31362f52db099f6af8ef4e09d67a142772200d5a7b0a02b4f42`。
- 升级计划 `0ae0732f3f97538e2f7bbb2fd63e9edc` 完成 27 步；四节点二进制 SHA-256 一致：`60278fd83991d991131034d01a25601a3137a9ac51f4a66539d657acbe3d664f`。
- 六个内置插件包不变；10 个 Worker/控制器/Xray 容器的 ID、镜像、启动时间不变；原 gateway、auto-disable、sticky 设置不变。
- 线上只读预览确认重试和账号规则都能只按 429 或只按关键词命中；返回统一阈值 2 和换号重置动作。四节点 `configured=false`，返回 Schema 2 草稿，没有自动发布业务配置。
- 新前端资源为 `SettingsView-Bntxuk6M.js`。私有备份、预检和验收记录位于 `/home/debian/sup2api-managed/backups/failure-policy-0.1.144/`。数据库备份 20,086,076 字节，SHA-256 `71802b9825f4d0de781a2a503cce3be6eb30c0fd6caa1e4c2288c03a243dc3bf`；恢复目录可读取，未执行恢复。

### 0.1.146 界面调整发布验收

- 源码提交 `8271b139cec1cd76247cdfca64ebece98100c971`。本地前端构建、ESLint 与 20 项设置/语言相关测试通过。
- 四节点均为 `0.1.146`，readyz 均为 200，二进制 SHA-256 均为 `4222fbe8da342b7b77c74024da74098e79aee83e90c23eb3f885ee9a2db45aec`。
- Manifest `cea2276849bb7681e19498dbf1b02e5ebd88c12e810f21fc67e36dc11a9315fe`；bundle `297aadf5362c1d7496efb2ea94555b26c71fd386e4514a409409d8c36f77feca`。升级计划 `589ddb0ee993fcb1a3093d9176459018` 完成 27 步，直接由 0.1.144 升级；0.1.145 仅准备制品、未上线。
- 新资源 `SettingsView-9hXQld63.js` 已包含两张设置卡片，不含粘性重置配置开关。六个内置插件包、10 个运行容器和现有业务策略配置均未改变，仍为 `configured=false`。
- 私有备份与验收记录：`/home/debian/sup2api-managed/backups/failure-policy-0.1.146/`。数据库备份 20,525,165 字节，SHA-256 `a0710360d4e9fdba455177c0646f953349f65257e65edfb294720097ee60e2c5`，恢复目录可读取，未执行恢复。

### 0.1.147 简化设置发布验收

- 公共表格组件提交 `4c9f399fb`；业务源码提交 `2a5056c632c55cdea208ae600f7b0835407e7f2f`。前端 114 项相关测试、后端四模块回归、类型检查、生产构建和 ESLint 通过。
- 四节点均为 `0.1.147`，readyz 均为 200，二进制 SHA-256 均为 `5980fab845b4cf730ab6e1fb265f4d3de9ea518be9aba8d80e516bc4cbedbd7b`。
- Manifest `8ef958a07c3a418636f62582e5a6fae6b45d09a2db830a2916efe219d8681f03`；bundle `58e923a3a485dcae3d2f09fca784ba12de17bcafd5d28c8bf874a9ad12a5bbb1`。升级计划 `b1b67797240e350fea25980182cc5143` 完成 27 步。
- 六个内置插件包、10 个运行容器以及既有 gateway/auto-disable/sticky 配置不变。四节点 `configured=false`，接口返回 Schema 3 待保存配置，没有自动启用新业务策略。
- 线上纯预览验证多错误码（含中文逗号与区间）、仅关键词均匹配重试和自动禁用；固定换号重置绑定、各账号自身出口语义正确。新资源 `SettingsView-EbzEeQKr.js` 已发布。
- 私有备份与验收记录：`/home/debian/sup2api-managed/backups/failure-policy-0.1.147/`。数据库备份 20,969,815 字节，SHA-256 `df0aca086a11859e2f9c9bb365c7319c243499779dd69bd526b0ff46477a0af1`，恢复目录可读取，未执行恢复。

### 0.1.148 规则行内编辑发布验收

- 源码提交 `ddde97e6f72e494f6c4eb49b94c91ba00f2395f0`。规则标题改为“自动重试”，新增及编辑直接在表格中完成，移除弹窗和高级匹配范围编辑入口；只读展示已有范围，保存仍保留旧范围数据。
- 前端 115 项相关测试、类型检查、生产构建、ESLint 和实际浏览器操作通过；测试覆盖新增、复制隔离、排序后错误对应、仅关键词、多个状态码、保存及预览校验、旧范围保留和只读权限。
- 四节点均为 `0.1.148`，readyz 均为 200，二进制 SHA-256 均为 `f333a8fcd3b06a2392d49be8fb41b6123373d7d39fc4901f351ec80420306ca5`。
- Manifest `37798fdb916a37415ec1fd1680c400b82c958cbdf0f20340982600510caea6d9`；bundle `5b01edec00d5c5426b8f0ee43beade3a2f407f1d176425acdc9b548c90ae9fa7`。升级计划 `ce7703c66609e93eab8620a23b440e64` 完成 27 步。
- 六个内置插件包及 10 个 Worker/控制器/Xray 容器未变。gateway、auto-disable、sticky、failure-policy 配置与备份逐项一致；四节点均 `configured=true`、Schema 3、revision=1，保留管理员已保存的规则。
- 新资源 `SettingsView-hHkyf8KN.js` 已发布。私有备份、预检和验收记录：`/home/debian/sup2api-managed/backups/failure-policy-0.1.148/`。数据库备份 21,505,823 字节，SHA-256 `ea7294699b8e7237f0c5891afe806c85b5207827f4b0c6f9bb2e200cdce2e9bf`，恢复目录 593 行可读取，未执行恢复。

### 0.1.149 逐行 Go 正则发布验收

- 源码提交 `ec8a4f5b9b7d1998cd774398d92d6977f8be6462`。移除条件关系控件，新版固定 OR；逐条规则可选择 Go 正则，每行独立表达式，支持原始行号错误提示。
- 前端 117 项相关测试、类型检查、生产构建和 ESLint 通过；gateway/account/usage/core 回归通过，新增测试覆盖 Go 语法与大小写、跨行选择、前 4 KiB 限制、加载后重编译、非法表达式拒绝、旧策略兼容及正则驱动真实模拟上游重试和禁用。
- 四节点版本 `0.1.149`、readyz 200，核心 SHA-256 均为 `0da458cb32e1efe2cffc16fae13e3da11f80906779e9346039a7c5a39987c3d1`。
- Manifest `e82d265b7e19d446d30483a822688e886446dc9692bf29d386d2b1701591d282`；bundle `3c923247f495fc0af2e17f7109241fc2e2660380e96ae250f5bcb5e1e491d4c7`。升级计划 `c31f40f5b6d1ac5113291d1c5d794d8e` 完成 27 步。
- 四节点只读预览确认第二条正则可独立命中重试及禁用，状态码也可独立命中；非法正则被 400 校验错误拒绝。新资源 `SettingsView-5L7YxEA8.js` 包含正则选项、不含条件关系控件。
- 六个内置插件包和 10 个运行容器保持不变；gateway、auto-disable、sticky、failure-policy 与备份一致。四节点仍为管理员既存策略 revision=1，本次未发布业务规则。
- 私有记录 `/home/debian/sup2api-managed/backups/failure-policy-0.1.149/`。数据库备份 21,946,258 字节，SHA-256 `2685cc5c4d9646df700e26a4f1e4066b3a27438c1ac25ac533d71d967d447061`，恢复目录 593 行可读取，未执行恢复。

### 0.1.150 调度策略界面发布验收

- 源码提交 `b694858c5119eceff31687573cedbbe8e7ee68fa`。系统设置标签更名“调度策略”，删除下方重复标题及规则区重复标题；重试和禁用各自提供底部保存按钮，互不提交或丢弃另一分区的草稿。
- 前端 118 项相关测试、类型检查、生产构建、ESLint 通过。实际浏览器验证两次分区保存、版本递进、无效草稿隔离、标题去重及移动端无横向溢出；后端本次未改动。
- 四节点 `0.1.150`、readyz 200，核心 SHA-256 均为 `e4499c4d8492324df8d48877d7ddbd43476bbb336da8d54effa4d4ac6f77c726`。
- Manifest `f8f4a7af0f6bc67a9a5ba4c64159e52a0bf147665c2c9f4b8faeae458ed3961c`；bundle `d3dbdf5d9abf9de9a1562323a66f1b785804fa6c96c6f0daae55168ecaa23dd0`。升级计划 `45863fb2ee6cc5a42b4e4076ebc7c1e9` 完成 27 步。
- 新资源 `SettingsView-DPSEXm8S.js` 已含独立保存按钮；入口资源包含“调度策略”。六个内置插件包、10 个运行容器及既存业务配置均不变，四节点规则 revision=1。
- 私有备份与验收记录 `/home/debian/sup2api-managed/backups/failure-policy-0.1.150/`。数据库备份 21,985,248 字节，SHA-256 `c2cb380d256448f5c076ed2485f74aee9436b733b695bddb660ba4295eda5460`，恢复目录 593 行可读取，未执行恢复。

### 0.1.151 平台标题去重发布验收

- 源码提交 `fd01060802236bdf9c600398279823e72ab0480d`。新增共享 `STabLayout` 及标题上下文；系统设置、插件详情和 CCGateway 设置接入。同名标题默认隐藏，不同分组、说明和操作保留；单标签、折叠、自定义标题、弹窗及抽屉保持对应语义。
- 前端 184 项相关测试、类型检查、生产构建通过；ESLint 无错误，已有可选属性默认值警告保留。实际浏览器核对六个系统设置标签均不重复标题，计费保存、中英文切换、移动端宽度正常。
- 四节点均为 `0.1.151`，readyz 均 200，核心 SHA-256 `00773a600247c43cbeb5fb779bfed90da83efe39994692068665fa43d5a48d98`。
- Manifest `3b537a70ada38d7d5c73bd45794db30f248d6b9c9806b21324606333b994405e`；bundle `fc411b89157a96d5169866772f51846712e5e3ebe4092115fc92d73b1ac4bb67`。升级计划 `6829490092b3738dc95126db08defb64` 完成 27 步。
- 新资源 `SettingsView-CXZeTkTA.js` 已引用 `STabLayout.vue_vue_type_script_setup_true_lang-YJBnDF7B.js`。六个内置插件包、10 个运行容器及既有配置保持不变；四节点调度策略 revision=1。
- 私有备份及验收记录 `/home/debian/sup2api-managed/backups/failure-policy-0.1.151/`。数据库备份 22,436,156 字节，SHA-256 `a12d798312834d4791b553ce0f1a4e8adf010a08f1921ea1389ee9353e7b0062`，恢复目录 593 行可读取，未执行恢复。

### 0.1.153 移除旧标签接口发布验收

- 主改动提交 `c1a5870f1dbde218d26e053a1f2bd4b5ad252065`，最终构建源码 `18878f057ab271d34ccd3a70370bb7cd30096efb`。移除 STabs 公共导出，底层改为内部 TabBar；账号详情、我的用量、价格同步、growth 和 moderation 的全部实际调用迁入 STabLayout。
- UI/host/Vite preset 合约版本 2.0.0。moderation 0.1.10、growth 0.1.3、guard 0.2.5 的兼容声明更新；其中后两者线上未安装，不额外启用插件。
- 全部前端 513 项测试、类型检查、生产构建、目标代码 ESLint 通过；三个原生插件前端构建及 Go 测试通过。源码扫描不再有旧组件调用，负向合约测试明确保证旧公共导出不存在。
- 初次 0.1.152 构建被 growth 新生成的错误 npm 锁文件阻止，未导入或上线。移除该新锁文件、恢复本地 file 依赖安装后，以新版本 0.1.153 重新构建；失败目录留作证据。
- 先上传并升级 moderation（插件发布 138，四节点 serving=0.1.10），再升级核心。插件权限 diff 的 added/widened/removed 均为空，配置与既有授权保持原值。
- 四节点核心 0.1.153、readyz 200，二进制 SHA-256 均 `b520728b489cc4f663f3781cfcd590080661797a1fa6eeb81526f3990ece4e39`。核心升级计划 `6f54171673c6da85162eb0216de1ec81` 完成 27 步。
- Manifest `6cf4af38249a9acc72e32aba535ab241bb564bcb666a3af6f67a18596b14aead`，bundle `c22760abf65ce9416a1af4cc26718a3f13766582c0d24758c9a846c9acc8a3a2`。其他五个内置插件包不变；10 个 CCGateway Worker/控制器/Xray 容器不变。
- 线上资源 `index-F6FkxA5Y.js`，公共 UI `shared/sub2api-ui-DzQbaxuj.js` 已无 STabs 导出；公共 host 及依赖确认为 UI 2.0.0；当前原生插件资源无旧接口引用。既有 gateway/auto-disable/sticky/failure-policy 设置不变，策略 revision=1。
- 私有升级与验收记录 `/home/debian/sup2api-managed/backups/failure-policy-0.1.153/`；沿用未切换服务前的 `/home/debian/sup2api-managed/backups/failure-policy-0.1.152/database.dump`，22,944,414 字节，SHA-256 `c32c1b88ab74c778db8250915ac6087e80a1fe3fd6f04bb6f1571df8a6c278e6`，恢复目录 593 行可读取，未执行恢复。

### 0.1.154 单一增强 STabs 发布验收

- 源码 `9793321516b966e82b966b6bf1143d9eeaeb71ed`。将统一容器命名为 STabs；STabLayout 实现、公共导出及实际引用全部删除，不留兼容别名。内部 TabBar 只负责导航渲染，公共 STabs 负责标签内容及标题上下文，两者职责不同。
- UI 合约 3.0.0；moderation 0.1.11、growth 0.1.4 使用增强 STabs，最低核心 0.1.154；guard 0.2.6 更新兼容范围。没有额外安装或启用 growth、guard。
- 全部前端 513 项测试、生产构建、类型检查和目标 ESLint 通过；三个插件 Go 测试和前端构建通过。
- 四节点核心 0.1.154、readyz 200；核心 SHA-256 `16f2fd8b4628b84d0637b23740c5b97754cef13fa6cb486d55c3907d0cbfa2e9`。升级计划 `1a4aec57c56883963497c4d80ccdbb92` 完成 27 步；内置 moderation 随核心更新，四节点 serving=0.1.11，状态 ready。
- Manifest `f2bee0d1a4c90a2aa74cf5d23bbb5909d92991608a8903a375b50242367d78aa`，bundle `4fe4130521eca6f29782a0777416a72066b6e1f8324988af8d3143371b3a4139`。
- 公共资源 `shared/sub2api-ui-BIKQILl_.js` 实证只导出 STabs、不含 STabLayout；线上原生插件资源也不引用 STabLayout。其余五个内置插件包、moderation 配置与授权、既有业务配置及 10 个 Worker/控制器/Xray 容器均未变。
- 私有备份和验收记录 `/home/debian/sup2api-managed/backups/failure-policy-0.1.154/`。数据库备份 23,497,877 字节，SHA-256 `9e69bc78a6a8b22b5249d5f58cbd5347caaef1aeb6ca790aa06fb8db71cf6507`，恢复目录 593 行可读取，未执行恢复。

## 外部参考

- [New API 自动禁用](https://github.com/QuantumNous/new-api/blob/main/service/channel.go)
- [New API 请求决策记录](https://github.com/QuantumNous/new-api/blob/main/service/request_policy.go)
- 本项目出口故障审计 `CCGATEWAY-EGRESS-FAIL-CLOSED-AUDIT.md`（随 ccgateway 在 `b7ed61128` 删除，原文 `git show b7ed61128~1:next/docs/CCGATEWAY-EGRESS-FAIL-CLOSED-AUDIT.md`）
