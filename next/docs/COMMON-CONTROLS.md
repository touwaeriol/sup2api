# 通用配置项与核心编辑器（第 2 期技术规格）

设计文档：https://claude.ai/artifact/Moff7JVEEQdkcgRAL4KK3k “通用配置项与编辑器”一节。实现完成后并入 CONTRACTS §87。

目标：限流、参数覆盖、请求头透传、代理、模型映射等账号通用配置只由核心实现一份界面和执行逻辑；插件只声明默认值、是否显示、是否可改、是否锁定，或在自己的页面引用核心编辑器。

## 1. commonControls 扩展

manifest `accountTypes[].commonControls`：键 = 通用项名，值 = 声明。

| 名称 | 账号字段 | 值类型 |
|---|---|---|
| `schedulable`、`auto_disable`（已有） | 同名 | bool |
| `priority` | `priority` | int |
| `weight` | `weight` | int |
| `max_concurrency` | `max_concurrency` | int |
| `rpm_limit`、`tpm_limit`、`spm_limit` | 同名 | int |
| `spm_window_seconds` | `spm_window_seconds`（会话周期，CONTRACTS §71.1） | int，60–86400，核心默认 60（核心 0.1.276 起） |
| `model_limits` | `model_limits` | 数组（同账号接口；各列表可带 `spm_window_seconds`，带了同样需下限 ≥0.1.276） |
| `usage_limit` | 账号用量限制（§73） | 对象（同账号接口） |
| `model_mapping` | `model_mapping` | 对象 |
| `param_override` | `param_override` | §84 对象 |
| `header_override` | `header_override`（请求头覆盖，CONTRACTS §108） | new-api 格式的对象（请求头名 → 值），清单检查用核心同一个编译器 |
| `header_passthrough` | `header_passthrough` | 字符串数组 |
| `environment` | `environment`（各项超时） | 对象 |
| `proxy` | `proxy_id` / `proxy_url` | 只支持 `required` |

每项声明（都可省略）：

| 字段 | 含义 | 缺省 |
|---|---|---|
| `visible` | 编辑页是否显示 | true |
| `configurable` | 能否修改（false = 只读，值固定为 `default`，已有账号保持原值） | true |
| `default` | 新账号的默认值 | 核心默认 |
| `locked` | true = 值永远是 `default`，新老账号都不能改；核心强制 | false |
| `required` | 只用于 `proxy`：必须配置代理 | false |

- 合并：插件默认值 → 账号自己的值 → 插件锁定值。
- `visible: false` 只影响界面；后端仍按 configurable/locked 校验（隐藏且可改的项，接口照样能设）。
- 与 `defaultLimits` 的关系：同一字段两处都写时以 `commonControls.<名>.default` 为准；check 报冲突（两处不同值）。`defaultLimits` 继续有效。
- 后端（`account/handlers.go applyCommonControls` 泛化为表驱动）：新建时缺省值取 `default`；`configurable: false` 或 `locked` 时，新建只接受 default、更新只接受原值（locked：只接受 default），否则字段错误 `read_only`；`proxy.required` 时新建 / 更新后没有代理报 `required`。锁行后校验（沿用现有做法），绕过界面直接调接口也改不了。
- `GET /account-types` 原样返回 `common_controls`（键为上表名称）。
- check：未知名称、值类型不符、`default` 越界（复用账号接口的范围校验）、`required` 用在 proxy 之外、`locked` 却没有 `default` 都报错。

## 2. 编辑页按声明渲染

- 账号编辑页（`web/src/views/accounts/AccountEditor.vue` 及各分区组件）对每个通用项读声明：`visible: false` 隐藏；不可改或锁定时控件置灰并显示原因（复用 `accounts.editorUi.commonControlFixed` 一类文案）；新建时预填 `default`；`proxy.required` 时代理必填并提示。
- 编辑页不认识任何具体插件；导航摘要跳过隐藏项。批量编辑：对不可改的项，所选账号所属类型有不可改的，该项置灰并提示。

## 3. 核心编辑器开放给插件（UI 3.7）

- `@sub2api/host`：`HOST_UI_VERSION` 3.6.0 → 3.7.0；`PluginHost.components`（只读对象）提供 `ParamOverrideEditor`、`RateLimitEditor`（rpm/tpm/spm，2026-11-11 起会话数一行带会话周期 `spm_window_seconds`，只在给过或改过时出现在 v-model 里）、`ModelLimitsEditor`、`HeaderPassthroughEditor`、`ProxyPicker`、`ModelMappingEditor`（3.7 另有账号上游编辑器 `UpstreamEditor`，UI 3.18 随内置插件 custom 删除，CONTRACTS §106），props 与 v-model 形状和账号接口字段一致；用到的插件声明 `hostUICompat: "^3.7"`（sdk/manifest check 按 3.7 要求核心下限为本期核心版本）。
- UI 3.11 起 `PluginHost.components` 另有 `EntityPicker`（按名称或 ID 选用户、分组、API Key、账号、模型、代理），见 §6。
- UI 3.19 起另有 `HeaderOverrideEditor`（v-model 为 `header_override` 对象或 `null`，接受 `disabled`），`ParamOverrideEditor` 另收可选的 `headerOverride`（试运行的初始请求头）；用到的插件声明 `hostUICompat: "^3.19"`，`hostCompat` 高于 0.1.302（CONTRACTS §108.3）。
- schema 表单控件（当时在 `web/src/components/schema/widgets.ts`）：`param-override`、`header-passthrough`、`rate-limits`、`model-limits`、`model-mapping`（另有 `proxy-select`；UI 3.19 另有 `header-override`）。UI 4.0 删除 `SchemaForm` 时随之删除（2026-11-19，插件界面只用代码写），原生表单直接用上面的 `host.components`。数据格式即核心执行格式，校验在插件自己的接口里用 SDK 同一套规则（`sdk/paramoverride` 等）。
- 规则：组件产出的格式就是核心执行的格式，插件不自己解释；凭证头、逐跳头仍在 §81 禁止列表。

## 4. 编辑页分区插槽

- 新插槽 `account.editor.section`：插件在 manifest `ui.slots` 声明，带 `label`、`icon`、可选 `accountTypes`（只对这些类型显示）。账号编辑页在导航最后追加这些分区，props `{ account, accountType, readonly }`。
- 按 §23.3 的决定，插件分区不进核心保存流程：分区内的配置由插件自己的接口保存（新建账号时分区显示“保存账号后可配置”）。

## 5. ccgateway

ccgateway 已在 0.1.290 整体删除（最后源码 `317b0c331`），核心里没有它的集成需要搬迁。

## 6. 实体选择器 EntityPicker（UI 3.11；表单用法 UI 3.12）

设计稿：`entity-picker-design`（“日志筛选：从手输 ID 改成可搜索的选择器”）。凡是“选一个用户 / 分组 / API Key / 账号 / 模型 / 代理”的地方都用它，不再手输 ID、不再各页自己拉列表拼下拉。组件 `web/src/components/EntityPicker.vue`；插件用 `host.components.EntityPicker`（`HOST_COMPONENT_NAMES`，声明 `hostUICompat: "^3.11"`）。数据来自查找接口 `GET /lookup/{kind}`（CONTRACTS §96）。

```vue
<EntityPicker v-model:id="filters.user_id" v-model:text="filters.user_email" kind="user" size="sm" @change="apply" />
<EntityPicker v-model:id="filters.api_key_id" v-model:text="filters.api_key_name" kind="api_key" :scope="{ user_id: filters.user_id }" />
<EntityPicker v-model:id="filters.group_id" kind="group" mine />
```

| prop | 说明 |
|---|---|
| `kind` | `'user' \| 'group' \| 'api_key' \| 'account' \| 'model' \| 'proxy'` |
| `v-model:id` | 选中的 id（字符串，`''` = 未选）；model 的 id 就是模型名 |
| `v-model:text` | 可选。绑定了才有“名称包含 “xxx””一行，值是用户输入的文本；与 id 互斥 |
| `placeholder` | 占位文字，缺省为实体名（“用户”）。同时是 aria-label；放在带标签的表单项（`SField` / `SSettingRow`）里时不设 aria-label，由表单项的标签命名（否则“探测代理”一栏会被读成“不使用代理”） |
| `size` | `'sm' \| 'md'` |
| `mine` | 只列调用者自己的（个人页面）：查找接口带 `mine=true`，按 id 反查名称时也带 |
| `scope` | 该 kind 的额外查找参数：api_key 用 `{ user_id }`（只列该用户的密钥），account 用 `{ prefer_group_id }`（该分组的账号排前面）；列表顶部会写明范围 |
| `exclude` | UI 3.12。`(string \| number)[]`：这些 id 不出现在候选里（多选里已选的、表单其它行已占用的），手输它也不算选中（没有“使用 ID #n”一行，文本框也不接受）；已选值照常显示。给了它时查找多要同样多行（上限 50），列表不会因为被排除的排在前面而变短；排除的超过 30 个时列表可能变短，查到的行全被排除而还有更多匹配时列表写“还有更多未列出，输入以缩小范围”，不写“没有匹配” |
| `extra` | `{ id, name, hint? }[]`：页面自己提供的候选（如所选时间范围内日志里出现过的模型），排在查找结果之前，同 id 以查找结果为准。可以带查找接口没有的 id（如“默认分组 0”）：这类 id 不反查名称，手输它也算选中。手输的文本与某个候选的 id 完全相同时，该候选就是第一行，搜索返回之前也是（回车选中什么不取决于按得早晚） |
| `disabled` | 禁用 |

事件：`update:id`、`update:text`（每次选中或清除两者都发，各带新值）；随后发 `change({ id, text })`，页面要“选中即查询”时监听它。

行为：

1. 点开即查前 20 条；输入停 250 毫秒后带 `q` 查；后发的查询先到时，先发的结果丢弃。
2. 每行：名称、`#id`（model 没有）、第二行辨认信息（查找接口的 `hint`）；`status` 不是 `active` 的行弱化并带“已停用”，仍可选。
3. 第一行就是回车选中的那一行：输入文字时是“名称包含 “xxx””（绑定了 `text` 才有）；输入纯数字时是该 id 的实体，列表里没有这个 id 时是“使用 ID #n”（查已删除对象的历史记录），其后才是“名称包含”。所以“文字回车 = 按名称，数字回车 = 按 ID”的习惯不变。model 只绑 `id` 时（精确筛选），输入的名称不在列表里就在**最后**多一行“使用 “xxx””（请求里的模型名不一定在价格表里）；匹配到的模型仍排前面，回车选的是它们。
4. 收起时显示“名称 #id”。`id` 有值而名称未知（链接带入、表格点入）时用 `ids=` 反查：同一 kind 同一时刻的反查合并成一次请求，结果在本次登录内缓存；查不到显示 `#id`。`text` 有值时显示“名称包含 “xxx””。
5. 没有该 kind 的查找权限（按权限预先得知，或接口返回 403 / 404——滚动更新时旧核心没有这条路由）时退化成文本框：纯数字 → `id`，其它 → `text`（没绑定 `text` 就只接受数字，其它输入会清空已选：发 `update:id ''` 和 `change`，框也清空——表单不会按一个已经被改掉的 id 提交；model 没绑定 `text` 时任何文本都是 id），回车或失焦时生效。403 / 404 记住，本次登录不再请求。文本框的占位写明要输入什么：“用户 ID”；页面给了 `placeholder` 时（它说的是字段，不是实体）写成“创建人（用户 ID）”“不使用代理（代理 ID）”。生效之后文本框显示的是当前的值：没被接受的文本、或页面另存到别处的 id（多选）不会留在框里。
6. 手机上是底部抽屉（`SSelect` 的可搜索形态）。

实体只是一段描述（`web/src/components/entity/entities.ts`）：查找路径、图标、名称文案键、id 是否是数字、所需权限、哪些 `scope` 参数指向别的实体。加一种实体 = 后端查找接口加一个 kind + 这里加一项，组件不改。

| kind | 查找路径 | 权限（任一） | `mine` 时 |
|---|---|---|---|
| user | `users` | `user:read` | 不支持 |
| group | `groups` | `group:read` | 任何登录用户 |
| api_key | `api-keys` | `usage:all:read` | `apikey:self:manage` |
| account | `accounts` | `account:read` / `account:own:read` | 同左 |
| model | `models` | `price:read` | `apikey:self:manage` |
| proxy | `proxies` | `proxy:read` / `proxy:own:read` | 同左 |

页面上要把 id 写成文字（筛选标签“分组：cc-低注入 #3”、表格单元格）时用同一份缓存，不要再请求一遍：

```ts
import { useEntityNames } from '@/components/entity/lookup'
const { name, label } = useEntityNames({ mine: () => !props.admin })
label('group', '3')   // 'cc-低注入 #3'；名称未到或查不到时 '#3'；model 直接是模型名
name('user', 7)       // 'zhangsan@example.com' | undefined
```

两个函数都是响应式读取（在模板或 computed 里用），没见过的 id 会自动排进下一次反查。选项里还可以给 `extra`（页面自己的候选，同 `EntityPicker` 的 `extra`）：查找接口不认识的 id（如“默认分组 0”）用它的名称。需要自己查候选时用同文件的 `lookupEntities(kind, { q?, ids?, limit?, mine?, scope? })`，例如 `lookupEntities('api_key', { ids: [keyId], scope: { user_id } })` 返回空即“这个密钥不属于该用户”。

### 6.1 多选：EntityMultiPicker（UI 3.12）

选多个同类实体（账号的分组、规则针对的用户）。组件 `web/src/components/EntityMultiPicker.vue`；插件用 `host.components.EntityMultiPicker`（声明 `hostUICompat: "^3.12"`、`hostCompat` 下限 `>=0.1.280`）。

```vue
<EntityMultiPicker v-model:ids="form.user_ids" kind="user" @change="dirty = true" />
<EntityMultiPicker v-model:ids="form.models" kind="model" :max="5" />
```

| prop | 说明 |
|---|---|
| `kind` | 同 `EntityPicker` |
| `v-model:ids` | 已选的 id，`string[]`（model 是模型名），顺序 = 添加顺序 |
| `placeholder` | “添加”选择器的 `placeholder`（占位和 aria-label，规则同上），缺省“添加{实体名}” |
| `max` | 选满后不再显示“添加”选择器 |
| `size`、`mine`、`scope`、`extra`、`disabled` | 同 `EntityPicker`，原样传给“添加”选择器；`extra` 还用来给查找接口不认识的 id 命名 |

事件：`update:ids`，随后 `change(ids)`；只在用户添加或移除之后发，外部改 `ids` 不发。

- 已选项是可关闭的标签（“名称 #id”，名称未知时“#id”，名称由组件用 `useEntityNames` 反查），后面是一个只绑 `id` 的 `EntityPicker`：选中即追加，自己保持为空，候选里排除已选（`exclude`）。没有已选时只有这个选择器。重复的 id 不追加。
- `disabled` 时标签不能关闭、没有选择器；此时一个都没选显示“—”。
- 没有查找权限时“添加”选择器是 `EntityPicker` 退化成的文本框：输入 id 后回车或离开即追加，框清空。这个回车不会提交外层的 `<form>`（框是空的时候回车照常交给表单）。
- 粘贴一串 id（逗号、空白或换行分隔，两个以上）一次全部追加：已选的、重复的、不是 id 的跳过，到 `max` 为止；两种形态都支持（它替换的标签输入框能这样批量维护名单）。只粘贴一个词时交给选择器，当作搜索词。
- 宽度随所在位置（最窄 12rem），标签和选择器放不下就换行；它不会把窄表单（手机）撑宽。**直接把单选的 `EntityPicker` 放进表单网格时**要自己保证这一点：它是带图标、`#id`、× 的文本框，选中后的自然宽度有三百多像素，放在没有 `min-w-0` / `grid-cols-1` 的网格格子里会把一列撑出手机屏幕。控制台的表单里优先用下面的 `EntityInput`，它已经处理了（原来的 schema 部件在 UI 4.0 删除，§6.3）。

### 6.2 表单里的值：EntityInput、GroupPicker、ProxyPicker

选择器自己用字符串 id；表单里存的往往是数字、`null` 或数组。`web/src/components/EntityInput.vue` 做这一层换算（只在控制台内部用），其余都是它的薄封装：

| 组件 | 值 | 说明 |
|---|---|---|
| `EntityInput` | `kind` + `v-model`；`multiple` 时是数组，否则是一个 id；`numeric` 时 id 存成数字、未选是 `null`，否则存字符串、未选是 `''` | 读的时候 `null`、`''`、`0` 都算未选；另有 `placeholder`、`mine`、`max`、`disabled` |
| `GroupPicker` | `number[]`（`multiple`）或 `number \| null` | `kind="group"`；对外用法与改造前相同 |
| `ProxyPicker`（也是 `host.components.ProxyPicker`） | `number \| null`，`null` = 不使用代理 | `kind="proxy"`，占位“不使用代理”（`common.noProxy`），点 × 回到不使用代理；调用者看不到的已选代理显示成 `#id` 并保持选中 |

2026-11（核心 0.1.280）起 `GroupPicker`、`ProxyPicker` 不再各自拉 `/groups`、`/proxies` 的前 200 条拼下拉，候选和名称都来自查找接口，所以分组、代理再多也能搜到。`composables/lookups.ts` 的 `useGroupsLookup` / `useProxiesLookup` 还在，给需要整行数据（分组的转换策略、代理的地址）的页面用；它们被强制刷新（页面改了分组或代理之后）时，会顺带丢掉选择器缓存的这类实体的名称（`forgetEntityNames(kind)`），改名后标签不会停在旧名字上。

弹窗（`SModal`）打开时会聚焦第一个控件，而可搜索选择器一聚焦就展开列表。弹窗的第一个控件是选择器时，在调用处给别的控件加 `data-autofocus`（用户的“分配分组”弹窗把它放在“取消”上；调整余额弹窗在已带用户时放在金额上）。

### 6.3 schema 部件：`entity-select`、`group-select`、`proxy-select`（已删除，UI 4.0）

UI 4.0（核心 0.1.304，插件界面只用代码写）删除了 `SchemaForm`，这三个部件和 `web/src/components/schema/widgets.ts` 一起删除。插件的原生表单直接用 `host.components` 的 `EntityPicker`（单选，选分组写 `kind="group"`）、`EntityMultiPicker`（多选）、`ProxyPicker`；控制台自己的表单用 `EntityInput`、`GroupPicker`、`ProxyPicker`（§6.1、§6.2）。本节原文见 `git show 99bc835a7:next/docs/COMMON-CONTROLS.md`。
