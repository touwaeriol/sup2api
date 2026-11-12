# 平台 UI 组件

控制台和原生插件通过 `@sub2api/ui` 复用同一套组件与样式。

## 表格列设置（UI SDK 3.1）

通用表格只需通过 `column-settings-key` 指定开发者定义的表名，SDK 自动显示“列设置”按钮：

```vue
<STable
  :columns="columns"
  :rows="rows"
  column-settings-key="usage-log"
  card
/>
```

`TableColumn` 支持 `hideable: false`（必须显示）和 `defaultHidden: true`（默认隐藏）。未启用列设置的旧表格保持原样。勾选即时生效，支持恢复默认，至少保留一列。选择、展开、单元格插槽和展开行跨度会跟随可见列变化。

桌面表格与手机卡片共用设置，或按钮需要放到筛选栏时，使用导出的状态和控件：

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { STable, STableColumnSettings, useTableColumns } from '@sub2api/ui'

const columns = computed(() => [
  { key: 'name', label: '名称', hideable: false },
  { key: 'client_ip', label: '调用 IP' }
])
const columnState = useTableColumns(columns, 'usage-log')
const { visibleColumns, isVisible } = columnState
</script>

<template>
  <STableColumnSettings :state="columnState" />
  <STable :columns="visibleColumns" :rows="rows" />
  <!-- 自定义布局使用同一 isVisible('client_ip')，无需重复保存状态。 -->
</template>
```

- 默认每个用户在 localStorage 中保存一个 JSON 对象，表名是对象中的 key；控制台通过 `TABLE_COLUMN_SCOPE_KEY` 注入当前用户，原生插件自动继承。独立预览可以自行提供响应式 scope；不传表名时只保留当前组件内状态。
- 表名由开发者指定，同一用户下应唯一，例如 `usage-log`、`account-list`；插件可加自己的名称前缀以避免冲突。管理端与个人端使用不同表名。调用方只传入有权限展示的列，SDK 不会根据保存的偏好恢复未声明的列。不要用列设置代替权限控制。
- 只保存显式选择，新增列遵循自身默认值；标签翻译不会改变按 key 保存的偏好。动态列、切换用户、跨标签页修改及恢复默认均重新计算可见列。
- localStorage 被禁用或数据损坏时表格仍可使用；不跨浏览器同步，也不保存行内容。
- 原生插件使用该新增导出时声明 `hostUICompat: "^3.1"`。宿主／构建 preset 的版本均为 `3.1.0`，原有 `^3.0` 插件仍兼容。

### 自定义／服务端保存

`columnSettingsKey` 是开发者指定的表名，不是调用模型的 API Key。默认浏览器存储键为 `s2a.table-columns.v1:<用户 scope>`（scope 做 URL 编码），保存内容使用导出的 `TableColumnPreferences` 结构，序列化为一个 JSON 对象：

```json
{
  "usage-log": { "client_ip": true, "tokens": false },
  "account-list": { "status": true }
}
```

每次保存读取最新对象，只合并当前表对应的项；恢复默认只移除当前表名，其余表配置保留。对象为空时才删除整个存储项。

业务方可实现导出的 `TableColumnStorage`，通过 `STable` 的 `columnStorage` 或 `useTableColumns` 第三个参数传入。下面的 `preferencesAPI` 由调用方实现，SDK 不注册或假设这些后端接口：

```ts
import type { TableColumnStorage } from '@sub2api/ui'

const columnStorage: TableColumnStorage = {
  getItem: key => preferencesAPI.read(key),          // string | null 或 Promise
  setItem: (key, value) => preferencesAPI.save(key, value),
  removeItem: key => preferencesAPI.remove(key)
}
// <STable :column-storage="columnStorage" column-settings-key="usage-log" ... />
// 或 useTableColumns(columns, 'usage-log', columnStorage)
```

适配器收到的是按用户隔离的文档存储 key；`value` 是上面整个对象的 JSON 字符串，`null` 表示没有保存项。方法支持同步返回或 Promise；失败应抛出／reject。SDK 等待首次读取再允许切换列；同一适配器实例下，各表共享按用户串行的读取、合并、写入队列，避免组件之间覆盖其他表。调用同一后端的表应复用适配器实例；服务端多客户端并发仍由后端实现处理。SDK 忽略旧读取结果，切换用户后不发送旧用户的排队写入。状态导出 `loading`、`saving`、`error`、`reload()`、`retry()`，默认控件展示同步失败和重试。使用服务端适配器时不再同时写 localStorage，也不监听本地偏好变动。

跨设备同步取决于调用方服务端实现；服务端必须用当前登录身份鉴权，不能把传来的 scope 或 key 当作授权。不要在偏好 key 或保存值里放 API 密钥。SDK 不负责保存行内容、获取凭据或创建偏好 API。

### 通用 IP 列

任意 `STable` 可通过列配置使用同一套 IP 展示，无需复制使用日志的单元格模板：

```ts
import type { TableColumn } from '@sub2api/ui'

const columns: TableColumn[] = [
  { key: 'source_ip', label: '来源 IP', format: 'ip', filterable: true }
]
// <STable :columns="columns" :rows="rows"
//   @filter="(column, value) => { filters[column.key] = value }" />
```

- `format: 'ip'` 默认显示地址及复制按钮；`copyable: false` 可关闭复制。`filterable: true` 使地址可点击，并发出 `filter(column, address, row)`；默认只显示，不自动发起筛选请求。
- 支持任意字段名和点分路径，保留 IPv4／IPv6 的原始表示；长地址截断时可通过悬停查看完整值，复制和筛选始终使用完整地址。空值不显示可操作按钮。
- 卡片、详情和自定义单元格可直接用 `<SIPAddress :value="address" filterable @filter="onFilter" />`，可选 `copyable`、`active`、`empty`。点击复制／筛选不触发行点击；`cell-<key>` 插槽仍可覆盖内置 IP 渲染。
- 组件只展示调用方传入的数据，不做 IP 归属地查询，也不请求业务接口。

## 标签页与标题归属（增强 STabs：核心 0.1.154 起）

系统设置、插件设置和原生插件内部的标签页统一使用增强的 `STabs`。它组合标签栏与内容面板，默认处理同名标题，并建立正确的 tab/tabpanel 关联。不保留 `STabLayout` 导出、包装或兼容别名；内部 `TabBar` 不对外导出。内容必须放入容器，不能把标签栏与内容作为兄弟节点拼装。

```vue
<STabs v-model="tab" :tabs="tabs">
  <template #default="{ active }">
    <BillingSettings v-if="active === 'billing'" />
    <OtherSettings v-else-if="active === 'other'" />
  </template>
</STabs>
```

- 当前标签可见时，面板内 `SPageHeader`、`SSettingsGroup`、`SSectionTitle` 的同名 `title` 默认不再显示；不同名称的分组保持可见。判断来自组件上下文，不扫描 DOM，也不使用模糊文字匹配。
- 说明、保存按钮、开关、筛选器等独立内容保留。只有标题且标题重复时不留下空标题栏；只读时调用方应使用 `v-if` 省略没有操作的 `#actions` 插槽。
- 控制台外壳（`provideTitleContext(title, sink)` 的第二个参数 `PageHeadSink`）下，页面里的 `SPageHeader` 把自己的 `title`、`description` 交给顶栏显示，页面内不再画标题行；标题文字不需要和菜单、路由标题相同。宽屏（≥768px）时 `#before`、`#title-extra`（版本号、状态徽章）也进顶栏、排在标题后；没有 `#filters`、只有 `#actions` 时按钮同样进顶栏，不再单独占一行。窄屏时顶栏只放标题，这些内容留在页面里，说明不显示。
- 标签页、`STitleScope`、弹窗和抽屉是嵌套范围，没有 `sink`：里面的 `SPageHeader` 留在原处，只省略与当前范围同名的标题。
- 只有一个标签时默认隐藏标签栏，恢复内容自身标题；需要显示单标签可用 `show-single`。`show-tabs=false` 同样恢复自身标题。
- 嵌套容器使用最近层的标题，弹窗和抽屉重置上下文。折叠分组必须保留折叠按钮及名称；自定义标题插槽保持原样，避免误删其中的状态或交互。
- `STabs` 不自动重建内容组件。需要保留草稿可继续用 `v-show` 或 `KeepAlive`，需要切换时重新创建则由调用方设置 `key`。
- 控制台插件设置宿主已提供该上下文，声明式 `SchemaForm` 自动继承；原生插件自建标签页应使用上述容器。普通 HTML 标题、iframe 内部内容不自动处理。
- 新插件使用增强 `STabs` 时，`hostCompat` 最低核心版本应为 `>=0.1.154`（并保留项目要求的上界）。`hostUICompat` 声明 UI 3.0；使用旧入口的插件必须迁移，不提供兼容别名。

验证至少覆盖：同名/不同名标题、单标签、语言切换、只读操作区、折叠分组、弹窗/抽屉、嵌套标签、声明式插件表单，以及保存功能。

## 表格手柄排序

```vue
<STable
  v-model:rows="rules"
  :columns="columns"
  row-key="id"
  reorderable
  :reorder-disabled="readOnly || saving"
/>
```

- `reorderable` 默认关闭。开启后自动在最左侧显示手柄，只有手柄能开始拖动，普通单元格仍可点击和选择文字。
- 支持鼠标、触屏和触控笔。拖动时显示插入位置，靠近滚动区域边缘自动滚动；松开后提交一次新顺序。
- 聚焦手柄后，上下方向键移动一行，Home/End 移至首尾。Esc、失焦、指针取消、数据刷新或组件卸载均取消进行中的拖动。
- `rowKey` 必须指向非空、唯一且稳定的字符串或有限数字。缺少有效标识、少于两行、`loading` 或 `reorderDisabled` 为真时，手柄禁用。
- 组件不修改传入数组，不调用保存接口。`update:rows` 返回新数组，通常通过 `v-model:rows` 接收；页面在用户点击“保存”后持久化。
- 可选监听 `reorder`，参数为导出的 `TableReorderEvent<T>`：`{ row, key, from, to, rows }`。`from/to` 为当前传入列表的零基索引。不要同时在两个事件中重复保存。
- 拖动只作用于当前表格，不支持跨表拖动。排序依据当前传入的 `rows`；业务若分页、筛选或另有列排序，须先明确如何映射到完整顺序，不应直接覆盖完整列表。
- 选中项、展开行和焦点按稳定标识保留。开启排序时的 `rowAttrs.draggable` 被覆盖为 `false`，避免整行原生拖动与手柄交互冲突。
- 普通表格不需要修改。`row-click`、选择、展开、单元格插槽及右侧固定操作列维持原有接口。

失败处理页的布局、规则语义和保存流程仍由业务页面负责，本次组件改进不接入这些改动。

## 批量选择与批量操作栏（UI SDK 3.2）

```vue
<SBulkBar
  :count="selected.length"
  :total="list.total"
  :page-selected="pageSelected"
  :all-selected="allSelected"
  :selecting-all="loadingAll"
  @select-page="selectPage"
  @select-all="selectAllResults"
  @clear="selected = []"
>
  <SButton size="sm" variant="primary" @click="bulkEdit">批量编辑</SButton>
  <SButton size="sm" variant="danger" @click="bulkDelete">删除</SButton>
</SBulkBar>
<STable v-model:selected="selected" :columns="columns" :rows="rows" selectable />
```

- `selectable` 的表格默认支持拖拽框选：从勾选列或单元格空白处按下拖动，经过的可选行全部选中；起点行已选中时改为取消。拖回时范围外的行恢复原状，靠近视口边缘自动滚动，滚轮继续扩展。`:swipe-select="false"` 关闭。
- 从单元格文字上按下仍是普通的文字选择；按钮、链接、输入框、开关等控件不受影响。拖动结束的那次点击被吞掉，不会再切换勾选框或触发 `row-click`。
- Shift + 点击勾选框：把上一次点击的行到当前行设为同一状态（跳过 `rowSelectable` 为假的行）。
- `SBulkBar` 只负责展示与事件，不保存选择、不请求接口。未选时默认隐藏，`persistent` 时显示提示（`#idle` 插槽可替换）。「选择全部 N 项」由调用方按当前筛选加载全部 key 后写回 `selected`；跨页保留选择时，业务方应在筛选条件变化后清空，避免对看不见的行执行操作。
- 批量操作的权限检查由服务端逐条执行，界面里按钮只用于提示适用数量。原生插件使用 `SBulkBar` 时声明 `hostUICompat: "^3.2"`。

## 通用页面组件（UI SDK 3.4）

控制台和原生插件共用下面这些组件，页面不再各自手写同类样板。原生插件使用时声明 `hostUICompat: "^3.4"`。

### 时间：`SDateTime`

```vue
<SDateTime :value="row.created_at" mode="absolute" muted />   <!-- 2026-10-31 15:04:05 -->
<SDateTime :value="row.created_at" mode="time" />             <!-- 今天 15:04:05，其他日期 10-30 15:04:05 -->
<SDateTime :value="row.last_heartbeat" mode="relative" live /><!-- 5s ago，每秒刷新 -->
```

默认 `auto`：7 天内显示相对时间，更早显示绝对时间。非 `absolute` 模式悬停显示完整时间；空值或无效值显示 `empty`（默认 `—`）。相对时间按间隔共用一个定时器（`live` 每秒，否则每分钟），绝对时间不计时。

自绘的相对时间可直接用 `useNow(intervalMs)`：返回当前时间（毫秒）的 ref，同一间隔的订阅者共用一个定时器，最后一个退订时清理；间隔可传 getter，变化时重新订阅，`0` 表示停表。

### 状态：`SStatusBadge` / `statusTone`

```vue
<SStatusBadge :status="row.status" />
<SStatusBadge :status="job.phase" :labels="{ paused: t('jobs.paused') }" :tones="{ paused: 'info' }" />
```

常见状态（active、disabled、running、failed、expired …）内置中英文文案和色调；未知状态显示原值、灰色。`statusTone(status, extra?)` 单独给出色调。

### 表单弹窗：`SFormModal`

```vue
<SFormModal v-model:open="editOpen" :title="t('users.editTitle')" :loading="saving" @submit="save">
  <SField :label="t('common.name')" :error="errors.name"><SInput v-model="form.name" /></SField>
</SFormModal>
```

- 字段内回车和“保存”按钮都触发 `submit`；`loading` 时不能关闭、按钮转圈。
- `:enter-submit="false"` 时回车不提交，只能点按钮，用于调余额、删除确认、多选确认这类不应误触的弹窗。
- `submitText`、`submitVariant="danger"`、`submitDisabled`、`submitAttrs` / `cancelAttrs`（如 `data-testid`）；其余属性落到 `<form>`。
- `#footer-start` 在页脚左侧放提示或次要操作，`#footer` 整体替换页脚。
- 组件只负责外壳，校验、请求和错误展示仍由页面处理。

### 列表加载失败：`STable` 的 `error`

```vue
<STable :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" @retry="list.reload" />
```

空表显示“加载失败”而不是“暂无数据”；已有旧数据时保留数据并在上方提示。绑定 `@retry` 才显示“重试”。`#error` 插槽可替换默认内容，`errorText` 改文案。

### 自动刷新：`useAutoRefresh` / `SAutoRefresh`

```ts
const seconds = ref(10)
useAutoRefresh(list.reload, () => seconds.value * 1000, { enabled: () => !editorOpen.value })
// <SAutoRefresh v-model="seconds" />   按钮加菜单：关闭 / 每 10、30、60 秒
```

上一次加载结束后才排下一次，慢接口不会堆积；页面隐藏时暂停，回到页面时补一次；间隔为 0 或 `enabled` 为 false 时暂停。组件内挂载后才开始，首次加载仍由调用方负责。

### 筛选条：`SFilterBar`

```vue
<SFilterBar :aria-label="t('stats.filters')" :dirty="changed" @reset="resetFilters">
  <SDateRange … />
  <SSelect … />
  <template #end><span class="text-xs text-fg-subtle">UTC+8</span></template>
</SFilterBar>
```

绑定 `@reset` 时显示“重置”，`dirty` 为假时禁用；`:card="false"` 去掉卡片外框，用于卡片内部。

不常用的筛选可收进“更多筛选”，已生效条件用 `SFilterChips` 显示，见下文“更多筛选与单行单元格（UI SDK 3.10）”。

手机上的形态见下文“手机形态”。

### 插件页懒加载：`lazyPage`

```ts
import { lazyPage } from '@sub2api/ui'

export function register(host: PluginHost) {
  host.registerComponent('MyDashboard', lazyPage(() => import('./MyDashboard.vue')))
}
```

控制台启动时会加载所有原生入口，页面组件用 `lazyPage` 注册后单独分包，首次打开页面时才下载；加载中显示占位，失败重试两次（间隔 0.5 秒、1 秒）后显示控制台的错误状态；之后点“重试”或重新打开页面会重新完整尝试。入口只保留 slot 组件、文案和 `register` 需要的代码。

## 手机形态 / Phone forms（UI SDK 3.5）

屏幕窄于 768px（Tailwind `md`）时组件自己换成手机形态，页面通常不用改；宽屏（含鼠标）渲染保持不变。判断统一用 `useViewport()`，返回 `Viewport`（两个只读 ref：`compact` 窄屏、`touch` 粗指针，控件加高到 40px），不要在页面里自己调用 `matchMedia`；CSS 用 `md:` / `max-md:`，触屏用 `touch:`。`provideViewport({ compact, touch })` 为子树固定取值（预览、测试）。原生插件用到下列新增属性时声明 `hostUICompat: "^3.5"`。

**表格变卡片。** `STable` 在手机上每行一张卡片：标题、状态、副标题、标签／值网格、“详情”折叠和操作行。列用 `TableColumn` 声明位置：

```ts
const columns: TableColumn[] = [
  { key: 'name', label: '名称', mobile: 'title' },
  { key: 'status', label: '状态', mobile: 'status' },
  { key: 'key_prefix', label: '密钥', mobile: 'subtitle' },
  { key: 'usage', label: '用量', mobile: 'meta', mobileSpan: 2 },   // 占满一行
  { key: 'quota', label: '额度', mobile: 'meta' },
  { key: 'created_at', label: '创建时间' },                          // 有声明时，未声明的列进“详情”
  { key: 'actions', label: '操作' }                                  // 操作列总在卡片底部
]
```

- `mobile`：`title` / `subtitle` / `status` / `meta` / `detail` / `hidden`。一列都没声明时自动推断：第一列为标题，`status` / `enabled` 为状态，其后四列进网格，其余进详情。用 `useTableColumns` 过滤列的页面应全部声明或全部不声明。
- 单元格插槽多一个 `layout`（`'table'` / `'card'`），同一插槽可按形态微调（例如开关从操作列移到状态位）；`#card` 插槽整体替换卡片内容（`{ row, index, selected, toggle, expanded, toggleExpand }`）。`STable` 的 `mobile: 'scroll'` 保留横向滚动的表格（默认 `'card'`）。
- 卡片里的 `SRowAction` 变成小号文字按钮；卡片底部的操作宜不超过三个，其余放进该行的“更多” `SDropdown`。选择、展开、拖动排序在卡片上照常可用，列设置变成“字段”底部抽屉。
- 卡片宽度跟着屏幕走，标签／值网格每格约半张卡片宽。单元格内容要能换行或收窄：为宽表格写的固定宽度（如 `max-width: 14rem`）要在窄屏下放开，写在 `@media (max-width: 767.98px)` 里改成 `max-width: 100%`（可同时 `flex-wrap: wrap`）；不要在宽屏也用百分比，表格单元格里的百分比上限不参与列宽计算，那一列会变宽。收不进卡片的内容在卡片边缘被裁掉，不会把页面撑出横向滚动。

**筛选与页头。** `SFilterBar` 的默认插槽收进“筛选”按钮（`active-count` 角标）打开的底部抽屉，里面还有 `#end` 和重置，筛选随 v-model 即时生效；按“搜索”才生效的草稿式筛选监听 `@done`（点“完成”或在抽屉字段里回车时触发并关闭抽屉）。`#primary`（如搜索框）留在按钮旁、宽屏时排在最前。`SPageHeader` 对应 `#filters-primary` 与 `filters-active-count`，手机上页头操作留在页面里。

**弹层。** `SDrawer` 新增 `side="bottom"`：底部抽屉（全宽、最高 92dvh、顶部拖动条，可下滑关闭，避开 Home 指示条），手机上的弹层都基于它：`SModal` 与 `SFormModal` 的 `mobile`（类型 `ModalMobile`）为 `auto`（默认，sm/md 宽度为抽屉、lg 以上为整页）、`sheet`、`fullscreen`、`center`；左右 `SDrawer` 变成整页；`SDropdown` 为底部操作表（`mobile="popover"` 保留浮动菜单）；可搜索的 `SSelect`、`SDateRange`、`STimeRange` 以抽屉选择。手机或触屏上浏览器“返回”关闭最上层弹层：`dismissTopLayer()` 关闭在手机或触屏上打开的最上层（持久层除外）并返回 `true`；没有弹层、宽屏鼠标下打开的弹层或持久层返回 `false`，照常导航。控制台路由在“返回／前进”时调用它。

**其他组件。** `SPagination` 为「共 N 条 + ‹ 3 / 12 ›」，点页码跳页、选每页条数；`SBulkBar` 有选择时固定在屏幕底部，原位置不占高度（根元素 `display: contents`，带 `data-bulk-dock`），改用父元素的 `::after` 在它末尾留出同样高度：所以要把它和它操作的列表放在同一个容器里（末尾几行和分页才能滚出停靠条），这个容器自己不要再用 `::after`，也不要是横向 flex 或多列 grid；停靠条里的操作按钮放不下时横向滚动（触屏上不显示滚动条），藏着按钮的一侧渐隐，只露出一部分的按钮点一下只滚进视野、不触发操作（键盘按下照常触发），危险操作照常放在最后；`STabs` 的标签放不下时标签栏自己横向滚动（藏着标签的一侧渐隐，选中的标签滚到中间）；`SAutoRefresh` 是带运行点的图标按钮，绑定 `@refresh` 时菜单里有“立即刷新”；`SButton fab`（需 `icon`）是右下角 56px 圆形按钮，带 `data-fab`，控制台据此在页面底部留出空间；`SBulkBar` 停靠期间 fab 不显示，控制台也不再为它留空间（只剩停靠条自己那一份；按页面里有没有 `data-bulk-dock` 判断，`v-show` 藏起来的停靠条也算）；`SGrid variant="stats"` 保持两列统计卡片；`SSegmented` 横向滚动；`STooltip` 在触屏上点按切换；`SSteps` 的 `direction` 默认改为 `'auto'`（手机竖排、宽屏横排，宽屏与以前一致）；`SChart` 默认 220px 高并使用手机图例；Toast 在顶部居中。

## 更多筛选与单行单元格（UI SDK 3.10）

筛选默认只露常用几项，其余收进“更多筛选”；表格每个单元格只占一行，次要信息进提示、列设置里的可选列或详情。原生插件用到本节任一项（`#advanced`、`advanced-count`、`v-model:advanced-open`、`SFilterChips`、`SChipList`、`TableColumn.grow`、图标 `image`）时声明 `hostUICompat: "^3.10"`，`hostCompat` 要求核心 `>=0.1.277`（0.1.276 是最后一个带 UI 3.9 的核心）。

**更多筛选：`SFilterBar` 的 `#advanced`。** 宽屏时默认插槽之后出现“更多筛选”按钮（角标 `advanced-count`，即 `#advanced` 里已生效的个数；收起且大于 0 时按钮高亮），展开后在筛选条下方整行网格显示；`v-model:advanced-open` 可由页面控制展开状态，不绑定时组件自己记、默认收起。不传 `#advanced` 时与 3.9 完全一样。

手机上 `#advanced` 接在默认插槽之后一起进筛选抽屉（不显示该按钮）。“筛选”按钮的角标数抽屉里已生效的条件，组件自动合计：`active-count` + `advanced-count`。两个数各管各的插槽：`active-count` 只数默认插槽里的筛选（`#primary` 留在按钮旁，不计），`advanced-count` 只数 `#advanced` 里的；不要把后者再加进前者。没有 `#advanced` 的页面照旧只传 `active-count`。

**已生效条件：`SFilterChips`。** `items: { key, label, value }[]` 渲染为一行可关闭的「label：value ×」标签和「清除全部」，`@remove(key)` / `@clear`；没有条目时不渲染。放在筛选条下方，列出收起的更多筛选里已生效的条件，以及表单里没有字段、只能由链接或详情带入的条件。

```vue
<SFilterBar :active-count="activeCount" :advanced-count="advancedCount" @reset="reset">
  <SSelect v-model="filters.status" … />
  <template #advanced><SInput v-model="filters.ip" … /></template>
</SFilterBar>
<SFilterChips :items="chips" @remove="clearFilter" @clear="reset" />
```

**多个标签：`SChipList`。** `items: { key, label, tone?, title? }[]`，`max` 默认 2，`size` 默认 `'xs'`：只显示前 `max` 个标签，其余收成「+N」，悬停或键盘聚焦时用卡片提示列出全部；整体不换行。

**吃剩余宽度的列：`TableColumn.grow`。** `grow: true` 让该列吃掉其余列剩下的宽度并单行省略，默认渲染时完整值进 `title`；单元格插槽内容同样被截断（块级或 flex 根元素被限制在列宽内，可自行 `truncate`）。手机卡片不受影响。其余列用 `nowrap` 或 `ellipsis`。

**其它。** 新图标 `image`；时间范围预设“全部”显示为“全部时间”（`ui.allTime`）。

## 可搜索下拉的扩展与时间范围（UI SDK 3.11）

`SSelect` 的 `searchable` 形态能当“实体选择器”的底座用了：每行带 ID 和副标题、可清除、可远程搜索。原生 `<select>` 形态不变；不传本节的 prop 和选项字段时，可搜索形态的渲染和交互与 3.10 相同。原生插件用到本节任一项时声明 `hostUICompat: "^3.11"`，`hostCompat` 要求核心 `>=0.1.279`（0.1.278 是最后一个带 UI 3.10 的核心）。

**选项字段（`SelectOption`，只在 `searchable` 时生效）。**

| 字段 | 作用 |
|---|---|
| `hint` | 行尾灰色等宽小字（如 `#7`）；选中后收起时显示在名称之后 |
| `description` | 第二行灰色小字，单行省略 |
| `tag` | 名称后的灰色小标签（如“已停用”） |
| `muted` | 名称弱化（已停用但仍可选） |
| `accent` | 不是列表里的值、而是一个动作的行（如“名称包含 “x””）：名称用主色，行下有虚线；`description` 可换行 |

带任一字段的行（以及 `remote` 列表的每一行）按两行排版；本地过滤同时匹配 `label`、`hint`、`description`。行的渲染留在 `SSelect` 自己的模板里、不拆成子组件：控制台的页面测试用 `shallowMount` 且不 stub `SSelect`，子组件会被 stub 掉（`helpers.spec.ts` 有守卫）。

**新 prop（都只在 `searchable` 时生效）。**

| prop | 作用 |
|---|---|
| `icon` | 输入框开头的 `SIcon` 图标 |
| `clearable` | 有值时显示 ×，点击发出 `null`；此时 `placeholder` 不再作为列表里的“空值”一行。用键盘在 × 上清除后焦点回到输入框（× 随值消失） |
| `tinted` | 有值时输入框带主色底（筛选已生效） |
| `remote` | `options` 就是搜索结果：不做本地过滤，打开时发出 `search('')`、输入时发出 `search(输入)`，防抖和请求由调用方做；列表固定 20rem 宽（不随结果变宽变窄），输入的文字在结果里高亮；结果到达后高亮第一项，回车选中；调用方重建了列表而各项的 `value` 没变时，高亮留在原处 |
| `loading` | 列表末尾显示“正在搜索…”（代替“没有匹配项”）；已给的 `options` 照常显示 |
| `selected` | `SelectOption`：`modelValue` 不在 `options` 里时（远程结果）收起后显示它的 `label` / `hint`；它的 `value` 与 `modelValue` 不同时忽略 |
| `note` / `footer` | 列表顶部的说明行、固定在列表底部的提示行（如“只显示前 20 个，输入以缩小范围”） |

事件新增 `search(query: string)`（仅 `remote`）。`icon`、`clearable`、`tinted` 任一给出时，输入框变成带边框的一组：图标、文字、选中项的 `hint`、×、箭头；点文字以外的位置同样打开列表。透传到输入框的原生监听（`@input`、`@focus`）照旧。手机上仍是底部抽屉，以上各项在抽屉里同样生效。

```vue
<SSelect
  :model-value="value" :options="results" :selected="current"
  searchable remote clearable tinted icon="user" placeholder="用户"
  :loading="loading" :footer="more ? '只显示前 20 个，输入以缩小范围' : undefined"
  @search="search" @update:model-value="pick"
/>
```

控制台和插件里选用户、分组、API Key、账号、模型、代理不要自己拼这一套：用核心的 `EntityPicker`（`host.components.EntityPicker`，见 docs/COMMON-CONTROLS.md §6）。

**时间范围：`SDateRange` 的 `time`。** 触发按钮在任何宽度都完整显示起止时间到秒，不截断、不省略：一行放不下时（手机）结束时间换到开始时间下面，按钮随之变高。不带 `time` 的日期范围不变（手机上仍只显示预设名）。

**其它。** 新文案 `ui.searching`、`ui.clearItem`。

## 表单里的实体选择器（UI SDK 3.12）

本包的组件没有变化；3.12 加的是核心交给插件的宿主组件和 schema 部件（都在控制台里实现，见 docs/COMMON-CONTROLS.md §6.1–6.3、docs/CONTRACTS.md §96.7）。原生插件用到任一项时声明 `hostUICompat: "^3.12"`，`hostCompat` 要求核心 `>=0.1.280`（0.1.279 是最后一个带 UI 3.11 的核心）。

- `host.components.EntityPicker` 新增 `exclude`：这些 id 不出现在候选里（多选里已选的、表单其它行已占用的）。
- `host.components.EntityMultiPicker`：选多个同类实体。已选项是 `SChip` 标签（“名称 #id”），后面是“添加”用的 `EntityPicker`；`v-model:ids` 是 `string[]`，`max` 限制个数。
- `SchemaForm` 的宿主部件 `entity-select`：`{ "ui:widget": "entity-select", "ui:options": { "kind": "user" } }`，数组 schema 是多选，其余单选，值的类型跟随 schema。与 `proxy-select`、`group-select` 一样由控制台注入（`SCHEMA_WIDGETS_KEY`），插件自己的 `SchemaForm` 也能用。

```vue
<script setup lang="ts">
const { EntityMultiPicker } = host.components
</script>
<template>
  <SField :label="t('rule.users')">
    <EntityMultiPicker v-model:ids="form.user_ids" kind="user" :max="20" />
  </SField>
</template>
```

## 从单元格展开行（UI SDK 3.13）

`STable` 的展开行除了行首的箭头，也可以由单元格里的按钮打开（例如操作列的“设置”），用于在行下方就地编辑。不传本节的 prop、不用新的插槽参数时，表格的渲染和交互与 3.12 相同。原生插件用到本节任一项时声明 `hostUICompat: "^3.13"`，`hostCompat` 要求核心 `>=0.1.281`（0.1.280 是最后一个带 UI 3.12 的核心）。

```vue
<STable :columns="columns" :rows="rows" row-key="model" expandable :expand-toggle="false" expand-single>
  <template #cell-actions="{ expanded, toggleExpand }">
    <SRowAction :label="expanded ? t('common.close') : t('common.edit')" :aria-expanded="expanded" @click="toggleExpand()" />
  </template>
  <template #expand="{ row }">…</template>
</STable>
```

- 每个 `cell-<key>` 插槽多两个参数：`expanded`（该行是否展开）和 `toggleExpand()`（展开或收起该行，照常触发 `expand` 事件），需要 `expandable`（不可展开的表格里 `expanded` 恒为 `false`，`toggleExpand()` 什么也不做：不改展开状态、不触发 `expand`）；手机卡片里的单元格插槽同样有，与 `#card` 插槽的同名参数一致。
- `:expand-toggle="false"` 去掉行首的箭头列，只由单元格打开；手机卡片仍保留“详情”折叠。默认 `true`，与以前一样。
- `expand-single`：展开一行时收起其它行。默认关闭。被它收起的每一行也触发 `expand`（`row, false`），在新展开那一行的 `expand`（`row, true`）之前；依赖 `expand` 事件记录展开状态的页面因此不会留下已收起的行。

## 插件页的侧栏子导航（UI SDK 3.14）

原生插件页可以在 manifest 的 `ui.pages.<page>.nav` 里声明分组和条目，控制台打开这个页时，左侧侧栏换成这些条目（与“系统设置”的侧栏视图同一机制，CONTRACTS §90.7），点条目打开同一页的 `?tab=<key>`。插件只声明，不渲染侧栏；页面读 `?tab=` 决定显示哪一节，没有或不认识时显示第一个可见条目。核心按条目的 `permission`（插件权限）或 `corePermission`（核心权限）过滤。旧控制台忽略这个声明，所以插件应保留页内标签作为回退（用 `satisfiesRange(host.version, '>=3.14.0')` 判断），这时 `hostUICompat` 仍可以是 `^3.12` 等旧范围；只有页面离不开侧栏时才声明 `hostUICompat: "^3.14"`，`hostCompat` 要求核心 `>=0.1.284`（0.1.283 是最后一个带 UI 3.13 的核心）。

```json
"pages": {
  "dashboard": {
    "type": "native",
    "component": "Dashboard",
    "nav": {
      "groups": [
        { "key": "monitor", "label": { "en": "Monitor", "zh": "运行" },
          "items": [{ "key": "overview", "label": { "en": "Overview", "zh": "概览" }, "icon": "dashboard" }] }
      ]
    }
  }
}
```
