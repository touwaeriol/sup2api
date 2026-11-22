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
- 控制台插件设置宿主已提供该上下文；原生插件自建标签页应使用上述容器。普通 HTML 标题、iframe 内部内容不自动处理。
- 新插件使用增强 `STabs` 时，`hostCompat` 最低核心版本应为 `>=0.1.154`（并保留项目要求的上界）。`hostUICompat` 声明 UI 3.0；使用旧入口的插件必须迁移，不提供兼容别名。

验证至少覆盖：同名/不同名标题、单标签、语言切换、只读操作区、折叠分组、弹窗/抽屉、嵌套标签，以及保存功能。

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

本包的组件没有变化；3.12 加的是核心交给插件的宿主组件和 schema 部件（schema 部件在 4.0 随 `SchemaForm` 删除；都在控制台里实现，见 docs/COMMON-CONTROLS.md §6.1–6.3、docs/CONTRACTS.md §96.7）。原生插件用到任一项时声明 `hostUICompat: "^3.12"`，`hostCompat` 要求核心 `>=0.1.280`（0.1.279 是最后一个带 UI 3.11 的核心）。

- `host.components.EntityPicker` 新增 `exclude`：这些 id 不出现在候选里（多选里已选的、表单其它行已占用的）。
- `host.components.EntityMultiPicker`：选多个同类实体。已选项是 `SChip` 标签（“名称 #id”），后面是“添加”用的 `EntityPicker`；`v-model:ids` 是 `string[]`，`max` 限制个数。
- `SchemaForm` 的宿主部件 `entity-select`（4.0 删除）：`{ "ui:widget": "entity-select", "ui:options": { "kind": "user" } }`，数组 schema 是多选，其余单选，值的类型跟随 schema。与 `proxy-select`、`group-select` 一样由控制台注入（`SCHEMA_WIDGETS_KEY`），插件自己的 `SchemaForm` 也能用。

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

## 宿主权限的范围查询（UI SDK 3.15）

资源范围（CONTRACTS §21）之后，`host.permissions` 增加两个可选方法：`hasAction(action)`（任意范围持有该动作即为真，如 `hasAction('account:read')` 对只持有 `account:read:own` 或 `account:read:group/12` 的人也为真）与 `scope(action)`（返回持有的规范 key，全部范围为 `["account:read"]`，未持有为 `[]`）。`has(key)` 不变，按蕴含判断（`has('account:read')` 只在全部范围时为真）。UI 3.14 及更早的控制台没有这两个方法：用到它们的插件声明 `hostUICompat: "^3.15"`，`hostCompat` 要求核心 `>=0.1.286`（0.1.285 是最后一个带 UI 3.14 的核心）。0.1.286 起核心与控制台都只认规范写法，不再有 `account:own:read` 这类旧写法可以回退。moderation 0.12.0、dynweight 0.5.9、codex 0.1.4 起声明 `^3.15`。

## 插件权限的范围（UI SDK 3.16）

插件的用户权限可以在清单里声明 `scope`（`{resource, own, options}` 或枚举 `{values}`），授权 key 写作 `plugin.<key>:<权限>:own`、`…:<resource>/<值>`，枚举的资源名固定为 `value`（`…:value/input`）。3.16 的控制台按语法解析这些 key，`host.permissions` 的三个方法对插件 key 与核心 key 同一口径：`has('plugin.p:flow:manage')` 只在持有全部范围时为真，`hasAction('plugin.p:flow:manage')` 任意范围即为真（决定“编辑”按钮是否出现），`scope('plugin.p:flow:manage')` 返回持有的规范 key，如 `["plugin.p:flow:manage:own", "plugin.p:flow:manage:flow/2"]`。行级能否编辑仍建议由插件后端在列表行里返回。方法签名与 3.15 相同；UI 3.15 的控制台不解析插件范围，依赖这些语义的页面声明 `hostUICompat: "^3.16"`，`hostCompat` 要求核心 `>=0.1.290`（UI 3.16 随核心 0.1.290 发布，0.1.288 是之前最后一个核心，带 UI 3.15；0.1.289 没有发布）。声明了 `scope`、`group`、`risk` 等新字段的清单本来就要求这一版本。

同一版本导出 `useScrollClip(scroller, content)`：横向滚动容器的内容被裁到右侧之外时返回 `true`（随尺寸变化与滚动更新），用来在可横向滚动的表格右缘画渐隐或提示“还有列”。核心的权限矩阵与 `STable` 用的就是它；`scroller` 是 `overflow-x: auto` 的元素，`content` 是其中的表格，两个都是 `Ref<HTMLElement | undefined>`，挂载前为 `false`。

## 带会话的原始请求（UI SDK 3.17）

`@sub2api/host` 新导出 `fetchWithSession(path, init?)`：JSON 客户端带不了的请求（原始字节上传、流、文件下载）用它发。`path` 与 `api` 的写法相同（相对 `/api/v1`，如 `/p/demo/export?x=1`），`init` 是 `fetch` 的参数，`headers` 写成普通对象。它带上会话令牌，401 按 `api` 的规则处理（其他标签页换了令牌就用新令牌重发一次，否则结束会话回登录页；5xx 不登出），返回的 `Response` 不论成败都交给调用方读。不要自己读 `session` 拼 `Authorization`，也不要为了“续期”先发一个 JSON 请求：会话由服务端续期（CONTRACTS §100）。用到它的插件声明 `hostUICompat: "^3.17"`，`hostCompat` 要求核心 `>=0.1.290`（UI 3.16、3.17 都随核心 0.1.290 发布）。

同一版本起 `Session` 只有 `access_token`，`@sub2api/host` 也不再导出 JWT 控制台自己用的续期函数 `startKeepAlive`、`renewalDue`、`RENEW_BEFORE_MS`（控制台内部的导出，插件文档从未提供，内置插件都没有用到）。

## 去掉账号上游编辑器（UI SDK 3.18）

内置插件 custom（自定义上游）删除后，账号上游层（账号设置字段 `upstream`）也一起删除：`host.components` 不再有账号上游编辑器，控制台的 SchemaForm（4.0 删除）也不再有对应的部件。本包的组件没有变化，其他宿主组件和部件与 3.17 相同；没用到这两项的插件不用改，`hostUICompat` 照旧。UI 3.18 随核心 0.1.301 发布，0.1.298 是最后一个带 UI 3.17 的核心。

## 账号请求头覆盖编辑器（UI SDK 3.19）

`host.components` 增加 `HeaderOverrideEditor`，控制台的 SchemaForm（4.0 删除）增加部件 `header-override`：编辑账号的请求头覆盖 `header_override`（CONTRACTS §108，与 new-api 渠道的请求头覆盖同一格式），v-model 是字符串到字符串的对象或 `null`，接受 `disabled`。`ParamOverrideEditor` 另收可选的 `headerOverride`（账号的 `header_override`），试运行时请求头从它的结果开始。本包的组件没有变化；用到新编辑器或部件的插件声明 `hostUICompat: "^3.19"`，`hostCompat` 要求高于 0.1.302（0.1.302 是最后一个带 UI 3.18 的核心）。

## 原生账号表单的代理票据（UI SDK 3.20）

原生账号表单（CONTRACTS §23.5）多一个 prop `proxyTicket`，类型 `ProxyTicketSource`（`() => Promise<string>`，从 `@sub2api/host` 导出）。调用时控制台按表单**当前**的代理选择（已有代理、粘贴的代理串、不用代理）向核心要一张代理票据（`POST /account-types/:plugin_key/:type/proxy-ticket`，CONTRACTS §117）：核心按调用者的权限检查这个选择，把它加密成只有核心能解开的不透明字符串。在账号建好之前要访问外部服务的授权流程（设备码、OAuth 换令牌）在开始时把票据交给自己插件的路由，插件用 `pluginsdk.Fetch` 带上它，核心就从这个代理把请求发出去；插件界面和插件都看不到代理地址和密码。要不到票据时 Promise 以核心的 `ApiError` 拒绝，控制台同时把字段错误显示在代理输入下。本包的组件没有变化；用到它的插件声明 `hostUICompat: "^3.20"`，`hostCompat` 要求高于 0.1.303（0.1.303 是最后一个带 UI 3.19 的核心）。

## 插件界面只用代码写（UI SDK 4.0）

插件界面只用代码写（DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY）：账号表单、设置和页面都是原生组件（本包与 `@sub2api/host`），或者照旧用 iframe。4.0 删除了声明式的写法：
- 本包删除 JSON Schema 表单：`SchemaForm`、`SchemaField`、`SCHEMA_WIDGETS_KEY`、`BUILTIN_WIDGETS`、`uiGet`、`childUI`、`schemaType`、`isSecret`、`enumOptions`、`resolveWidget`、`fieldLabel`、`fieldHelp`、`orderedKeys`、`isVisible`、`schemaWithDefaults`、`validateSchema`，类型 `JSONSchema`、`UISchema`、`EnumOption`、`ValidateMessages`，以及文案 `ui.schema.*`（必填等校验文案在 `ui.form.v.*`）。`localizedText` 保留。
- 控制台不再渲染账号表单的 `form.mode: "schema"`（编辑账号时凭证卡片提示升级插件，其它分区照常编辑，凭证原样送回）、`schema` 模式的设置，以及 `ui.pages.*.type` 为 `table`、`form` 的页面（显示"不支持的页面类型"）。宿主部件 `proxy-select`、`group-select`、`entity-select`、`header-override` 随之删除；表单里改用 `host.components` 的 `ProxyPicker`、`EntityPicker`（`kind="group"` 等）、`EntityMultiPicker`、`HeaderOverrideEditor`。
- `@sub2api/host` 新增 `definePluginHost(name)`，返回一对 `{ setHost, useHost }`：插件的 `host.ts` 写 `export const { setHost, useHost: useShopHost } = definePluginHost('shop')`，`register(host)` 里 `setHost(host)`，`unregister` 里 `setHost(null)`；`register` 之前调 `useShopHost()` 抛错（CONTRACTS §23.1）。
- `@sub2api/host` 新增 `belowRange(version, range)`：`version` 低于 `range` 每个分支的下限时为真（`belowRange('4.0.0', '^4.1')`），控制台据此把“插件的界面契约比控制台新”提示成升级核心，而不是升级插件（CONTRACTS §23.1）。
- 带原生界面的插件声明 `hostUICompat: "^4.0"`，`hostCompat` 要求高于 0.1.303（0.1.303 是最后一个带 UI 3.x 的核心）。3.x 的控制台不加载 `^4.0` 的插件；声明 `^3.N` 的插件 4.0 的控制台也不加载。

## 表单校验框架（UI SDK 4.0）

插件界面只用代码写（DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY），表单的即时校验用本包的 `useForm` + `SForm`，规则写法与 Element Plus、Ant Design Vue、Naive UI 共用的 async-validator 风格相同，由本包自己实现。后端（插件的 `ValidateCredentials` / `ValidateSettings`）仍是最终校验。

```vue
<script setup lang="ts">
import { SForm, SField, SInput, useForm } from '@sub2api/ui'
const props = defineProps<{ modelValue: Record<string, unknown>; errors?: Record<string, string> }>()
const emit = defineEmits<{ 'update:modelValue': [Record<string, unknown>] }>()
const form = useForm({
  model: () => props.modelValue,
  update: (v) => emit('update:modelValue', v),
  serverErrors: () => props.errors,
  defaults: { region: 'us-east-1' },
  rules: {
    api_key: { required: true, whitespace: true },
    'items.*.url': { type: 'url' },
    webhook_url: { required: true, when: (m) => !!m.notify }
  }
})
defineExpose({ validate: form.validate })
</script>
<template>
  <SForm :form="form">
    <SField prop="api_key" label="API key">
      <SInput v-bind="form.bind('api_key')" />
    </SField>
  </SForm>
</template>
```

- **规则**（`Rule`）：`required`（`undefined`、`null`、`''`、`[]` 不通过）、`whitespace`（只有空格不通过）、`type`（`string` `number` `integer` `boolean` `array` `object` `url` `email` `date-time`）、`min` / `max` / `len`（字符串按字符数、数组按项数、数字按数值；有 `len` 时不看 `min` / `max`）、`pattern`（只对字符串）、`enum`、`validator(value, ctx)`（同步或异步，返回出错文案，`true` / 不返回为通过；抛错或 reject 也算不通过，文案取错误信息）、`message`（字符串或函数，覆盖内置文案）、`trigger`（`'blur'` / `'change'` 或数组）、`when(model)`（条件成立才校验，不成立时清掉错误）。规则表的键是点分路径，`*` 表示数组的每一项（`items.*.url`）。
- **顺序**：同一字段按规则的先后，每条规则先 `required`、`whitespace`，空值且不必填时跳过其余内置检查，再 `type`、`len`、`min`、`max`、`pattern`、`enum`，最后 `validator`（空值时它也运行）。只显示第一条失败。
- **已存密钥**：值是 `SECRET_MASK`（`******`）时 `required` 通过，其余检查和 `validator` 都跳过；原样送回，核心换回已存的值。
- **触发**：先惰性后积极。字段第一次失焦（控件经 `SField` / `SSettingRow` 的 `prop` 上报）或 `validate()` 之后算 touched，之后每次经 `form.set` 改值都校验。写了 `trigger` 的规则只在列出的时机运行；`validate()` 运行全部规则。
- **异步**：同一字段新一轮开始时中止上一轮（`ctx.signal`），迟到的结果丢弃；`validate()` 等全部字段跑完，跑的过程中又开始的新一轮以新一轮为准。不另设超时。
- **`useForm` 返回**：`value()`（整个当前值，含本拍还没交回的写入）、`get(path)`、`set(path, value)`（只在当前值上打补丁，规则和界面都没有的键原样保留；`undefined` 删掉这个键）、`bind(path)`（`{ modelValue, 'onUpdate:modelValue' }`，写成 `<SInput v-bind="form.bind('x')" />`；值原样传递，不做类型转换）、`bindNumber(path)`（数字框用：`<SInput type="number" v-bind="form.bindNumber('port')" />`，`SInput` 清空时发的 `null` 变成删掉这个键，即不发送，插件用自己的默认值；`bind` 会把 `null` 原样存下，插件 Go 收到后报 `type`）、`validate()`（`Promise<boolean>`，不 reject）、`validateField(paths)`、`clearValidate(paths?)`、`error(path)`、`errors`、`unclaimed`（没有 `SField` / `SSettingRow` 认领的错误）、`required(path)`。
- **默认值**：`defaults` 只填父组件交来的值里缺失的键（新建时、换账号类型时），不覆盖已有的键，用户清空的也不再填回。"交来的新值"按对象身份判断：**父组件必须把 `update` 收到的值原样存回**（`v => (model.value = v)`、`v-model`），存成拷贝（`{...v}`）的话用户清空的默认键会被填回。`usePluginSettings` 不靠这个判断，它在每次读到设置时自己填。
- **服务端错误**：`serverErrors` 的键可以是 `a.b`、`credentials.a.b`、`values/a/b`、`values.a.b`（`@sub2api/host` 的 `toFieldPath` 转成 `a.b`）。同一路径本地错误优先；改了某个路径就清掉它（以及它里面、外面的路径）的服务端错误；父组件换一份新的错误后重新全部显示。
- **`SForm`**：`form`、`disabled`（`<fieldset disabled>`，禁用里面的全部控件）；`submit` 只在 `validate()` 通过后触发。已经在 `<form>` 里（`SFormModal`、`SCredentialForm`）时不再渲染第二个 `<form>`，由外层提交。
- **`SField` / `SSettingRow` 的 `prop`**：在 `SForm` 里显示该路径的错误和必填星号（显式传入的 `error`、`required` 优先），控件失焦时校验它。自己写的控件用 `useFieldControl` 返回的 `blur` 接到失焦事件即可。
- 内置文案在 `ui.form.v.*`（中英），占位符 `{n}`、`{label}`（`SField` 的 label）。

### 组合组件（UI SDK 4.0）

- **`SSecretInput`**：密钥输入（`modelValue`、`placeholder`、`disabled`、`size`、`revealable`（默认 true）、`clearable`、`plain`）。值是 `SECRET_MASK` 时输入框为空、占位符显示 `******`，下方提示"已保存值，留空表示不修改"；输入即替换，清空后发回 `SECRET_MASK`（核心保留已存的值）；`autocomplete="new-password"`。可选的密钥加 `clearable`：有已存的值时显示"清除已保存的值"按钮，点了发 `''`（核心清掉这个密钥），之后不再当作已存。`createApiKeyForm` 的 `secret` 字段规则里没有 `required` 时自动带上它。敏感地址（`ui.settings.secrets` 里 `sensitive: true` 的字段，如 webhook 地址）加 `plain`：已存时同样遮蔽、留空沿用，但用普通文本框输入，没有显示/隐藏按钮。
- **`SUrlPresetInput`**：Base URL（`modelValue`、`presets`、`allowed`、`placeholder`、`disabled`、`readonly`）。没有 `allowed` 时是 URL 输入框加 datalist 和"预设"菜单；有 `allowed`（`useGuardedSettings`）时是选择框，已存的、不在列表里的值也保留为可选项，只有一个允许值时只读并提示"由管理员设置"。清空发 `undefined`，即不发送这个键。
- **`useCredentialForm(props, emit, { rules, defaults? })`** + **`SCredentialForm`**：原生账号表单（CONTRACTS §23.5）。`props.errors` 作为服务端错误，`defaults` 只在新建时填；组件写 `defineExpose({ validate: form.validate })`。`SCredentialForm`（`form`、`disabled`）在顶部用 `SAlert` 列出没有字段认领的错误，不再渲染 `<form>`（账号编辑器本身就是表单）。类型 `NativeAccountFormProps`；props 用运行时对象 **`nativeAccountFormProps`** 声明：插件工程里 `defineProps<NativeAccountFormProps>()` 编译不了（SFC 编译器解析不了从别的包导入的类型），`defineProps(nativeAccountFormProps)` 可以：

  ```vue
  <script setup lang="ts">
  import { nativeAccountFormProps, SCredentialForm, SDateTimeInput, SField, SFormSection, SInput, useCredentialForm } from '@sub2api/ui'
  const props = defineProps(nativeAccountFormProps)
  const emit = defineEmits<{ 'update:modelValue': [Record<string, unknown>] }>()
  const form = useCredentialForm(props, emit, { rules: { region: { pattern: /^[a-z]{2}(-[a-z]+)+-\d$/ } } })
  defineExpose({ validate: form.validate })
  </script>
  <template>
    <SCredentialForm :form="form">
      <SField prop="session_expires_at" label="Expires at"><SDateTimeInput v-bind="form.bind('session_expires_at')" /></SField>
      <SFormSection title="Region" help="Where requests go." collapsed :fields="['region']">
        <SField prop="region" label="Region"><SInput v-bind="form.bind('region')" /></SField>
      </SFormSection>
    </SCredentialForm>
  </template>
  ```
- **`SFormSection`**：表单里的一节（`title`、`help?`、`collapsed?`、`fields?`，默认插槽放字段）：上边线、`h4` 标题、下面一行小字说明，再是字段。`collapsed` 时标题可点开收起（`SDisclosure variant="section"`）：一开始收起，`fields`（外层 `SForm` 的路径）里有值或有错误时自动展开（`validate()` 报出节里的错误也会展开），用户点过之后按用户的选择。手拼表单写 `collapsed` 时**必须同时传 `:fields`**（节里各字段的路径），否则永远不会自动展开，节里的错误也看不到；`createApiKeyForm` 自己会传。
- **`SDateTimeInput`**：日期时间输入（`modelValue?: string`，RFC 3339）：输入框按本地时间显示，选好的时间发 RFC 3339（UTC），清空发 `undefined`（不发这个键）；其余属性（`placeholder`、`disabled`、`size` 等）落到里面的 `SInput`。只读的时间显示用 `SDateTime`。
- **`createApiKeyForm(spec)`**：返回整个账号表单组件，`host.registerComponent(name, createApiKeyForm({...}))`。`spec.apiKey`（默认只要求非空：去掉首尾空白后不能为空，与插件后端的规则一致；`min`/`max`/`pattern` 没有默认值，写了才检查；`placeholder`、`help`）、`spec.baseUrl`（`false` 表示没有；默认规则与插件后端 `apikey.NormalizeBaseURL` 一致：去掉首尾空白后为空算没填，否则要是有主机的 http(s) 地址（scheme 不分大小写），不含 userinfo、空白、`?`、`#`；`required`、`presets`、`pattern`（替换默认规则）、`message`（不符合 `pattern` 时的提示）、`label`（默认本包的“Base URL”）、`placeholder`、`help`、`first`（放在 API Key 前面）；没有 `account:settings:custom` 时按 `guarded_settings` 只给官方值）、`spec.extra`（`key`、`kind`：`text` `secret` `url` `select` `textarea` `date-time` `switch`，`label`、`help`、`placeholder`、`options`、`presets`、`rules`、`section`、`when`、`mono`（`text`、`textarea` 用等宽字体，同 `SInput` 的 `mono`，其它控件忽略）；`section` 写标题函数，或 `{ title, help?, collapsed? }`（类型 `FieldSection`），从这个字段起是一个 `SFormSection`，之后标题相同或没写 `section` 的字段都在这一节里）、`spec.defaults`；`url` 字段若在类型的 `guarded_settings` 里，和 Base URL 一样只给官方值。文字都写成函数（`() => host.t('…')`），随语言切换；API Key、Base URL 的标签用本包的文案，Base URL 的标签可用 `baseUrl.label` 替换。`date-time` 是 `SDateTimeInput`。
- **`usePluginSettings({ pluginKey, api, rules, defaults? })`** + **`SSettingsPage`**：插件设置（`GET/PUT /plugins/:key/settings`）：每次读到设置都填一次默认值，填好后的值是"未修改"的基准；保存先 `validate()`，再整份 PUT，成功后重新读一遍（刚填的密钥变回 `******`，重置回到保存后的值），重读期间不进入 `loading`，表单不会被卸掉；失败的文案用 `@sub2api/host` 的 `errorMessage`，会话结束、取消验证、请求中止不提示；字段错误显示在对应行，整体的错误用提示条；只有插件设置权限的人改了别的设置又保留着密钥时，那个密钥显示"要修改其他设置，需要重新填写这个密钥"。返回 `useForm` 的全部方法，外加 `loading`、`saving`、`loadError`、`dirty`、`canEdit`（`plugin:manage` 或清单 `ui.settings.permission`）、`reload()`、`save()`、`reset()`。`SSettingsPage`（`settings`、`title?`、`description?`，默认插槽放 `SSettingsGroup`）自带加载与重试、底部保存栏（未保存提示、重置、保存）、离开确认（路由离开、同一路由换查询参数如 `?tab=` 切换、关闭页面）；在输入框里按 Enter 也会保存，但没有改动时什么都不做；不能编辑时控件全部禁用、不显示保存栏。
- **`SResourcePage`**：插件的列表页（`client`（`host.pluginApi`）、`path`、`columns`、`search?`（查询参数名，有它才有搜索框）、`rowKey?`、`createLabel?`、`canCreate?`、`submit?`）。内部用 `useList` 和 `STable`（卡片形式）加分页，工具栏有刷新按钮；`cell-<key>` 插槽照传给 `STable`，`toolbar` 插槽放在搜索框后。`canCreate` 且有 `create` 插槽时显示新建按钮，打开 `SFormModal`，插槽参数 `{ close, done }`；弹窗的保存调用 `submit()`（在里面校验并提交，返回 `false` 则不关；抛错时按 `errorMessage` 提示，弹窗不关），成功后回到第一页刷新并发出 `created`。弹窗关闭时插槽内容会卸载：表单状态写在插槽里的组件内部时，下次打开自然是空的；状态放在页面上（比如页面上的一个 `ref`）的，要作者自己清空。
- `@sub2api/host` 新导出 `useGuardedSettings(accountType)`（`allowed(field)`、`locked(field)`、`guarded`）、类型 `AccountTypeView`，以及 `settingsFieldErrors`、`toFieldPath`（服务端字段名转表单路径；本包不再另起名字导出）、`errorMessage(e, t)` 和 `isSilentError(e)`（控制台的报错文案与"哪些失败不提示"，控制台自己也用这两个）。

### 测试辅助：`@sub2api/ui/testing`（UI SDK 4.0）

**只能在 spec（`*.spec.ts` 及只被 spec 引用的测试辅助文件）里引用**：它不在控制台的 import map 里，插件的运行时代码引用它时构建不会报错（预设把 `@sub2api/ui` 的所有子路径都当作外部模块），要到控制台里加载插件时才失败，依赖 `vitest` 和 `@vue/test-utils`。插件工程的 vitest 配置继承控制台 `web/vitest.config.ts` 的别名，自动解析到 `web/packages/ui/src/testing/index.ts`；类型检查时本仓库的插件工程继承的 `web/packages/vite-preset/tsconfig.plugin.json` 已经映射了它；自带 tsconfig 的工程在 `paths` 里加一行（写在 `@sub2api/ui` 之前）：`"@sub2api/ui/testing": ["../../../../web/packages/ui/src/testing/index.ts"]`。

- `fakeHost({ granted?, messages?, plugin?, api? })`：**在 `beforeEach` 里调用**（`beforeEach(() => fakeHost())`），挂载账号表单之前必须调过：表单经 `useHost()` 读控制台（受限设置、权限），没调会抛 “host context not initialised”。它给接下来挂载的组件提供控制台（`provideHost`：只授予 `granted` 里的权限，控制台的翻译返回键本身），返回插件的 host（交给插件自己的 `setHost`：`t` 按 `messages.en` 查，没有 `messages` 时返回键本身；`plugin.key`、`api`）。`messagesT(messages)` 是其中的 `t`（点分键，填 `{name}` 参数）。
- `mountAccountForm(form, { accountType, stored?, errors? })`：像账号编辑器那样挂载原生账号表单（`<form>` 里，v-model 凭证与 §23.5 的 props；有 `stored` 是编辑这个账号，否则是新建），返回 `{ w, credentials, validate(), saved() }`（`saved()` 是编辑器会保存的 JSON）。
- `fillControls(w, labels, input)`：按 `labels`（字段 → 标签文字）找到每个字段的控件，依次填入 `input`（字段 → 文字）：选择框按选项文字选，日期框填本地时间，`''` 清空；每个控件填完失焦。`control(w, label)` 是按标签找控件的那一步。
- `keyAndUrlCases(key, url, required, fill?)`：API Key 类表单共有的 api_key、base_url 用例（密钥按默认规则：只有空白的拒绝，1 位、4096 位、带空格、非 ASCII 的接受；`AccountFormCase`：`name`、`stored?`、`input?`、`out`、`ok`）；与 `mountAccountForm`、`fillControls` 合用：
  ```ts
  beforeEach(() => fakeHost())
  afterEach(cleanup)
  for (const c of keyAndUrlCases('sk-ant-api03-abcdef', 'https://x.example.com/anthropic', false)) {
    it(c.name, async () => {
      const { w, saved, validate } = mountAccountForm(AnthropicKeyForm, { accountType, stored: c.stored })
      await fillControls(w, { api_key: 'API key', base_url: 'Base URL' }, c.input ?? {})
      expect({ value: saved(), ok: await validate() }).toEqual({ value: c.out, ok: c.ok })
    })
  }
  ```
- `fakeCore(plugin, values, secrets?)`：`GET/PUT /plugins/<plugin>/settings` 的假核心（`secrets` 里的顶层键读回 `******`，PUT `******` 保留原值），返回 `{ api, puts, stored(), refuse(e) }`。`mountSettings(Settings, { plugin, setHost, values?, granted?, secrets? })`：挂载插件的设置组件并等它读完（默认授予 `plugin:manage`），返回 `{ w, core, settings }`（`settings` 是 `SSettingsPage` 拿到的表单）。
- `checkMessages(messages, { sources, dynamic? })`：在 `describe` 里加三条用例：中英文键相同；文案里没有 vue-i18n 特殊字符（`@`、`|`、`{name}` 占位符以外的花括号）；每个键都在 `sources`（`import.meta.glob(['./*.vue', './*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true })`）里以 `'a.b'` 出现，或在 `dynamic` 列出的前缀下（拼出来的键）。
- `cleanup()`：卸载上面挂载的组件、撤销 `mountSettings` 的 `setHost`、撤销 `fakeHost` 提供的控制台（之后 `useHost()` 再次抛错，上一条用例授予的权限不会带到下一条）、清空 `<body>`：`afterEach(cleanup)`。
- `mountPinned(component, options?, pin?)`：在固定的视口形态（默认手机）下挂载（控制台自己的 spec 用）。

## 一次请求保存的标签表单（UI SDK 4.1）

编辑弹窗的各个标签页都先把修改存成前端草稿，点“新建 / 保存”时一次请求、一个接口提交，不再有“先保存再设置”：新建时每个标签页都能直接编辑。新增 `STabbedForm`（外壳）和 `useTabbedForm`（保存逻辑），只新增导出，4.0 的接口不变。用到它们的插件声明 `hostUICompat: "^4.1"`。

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { STabbedForm, useTabbedForm, type FormTab } from '@sub2api/ui'
const tabs: FormTab[] = [{ key: 'basic', label: '基本信息' }, { key: 'prices', label: '价格' }]
const tab = ref('basic')
const form = useTabbedForm(tab, () => tabs.map((t) => t.key))
form.register({ tab: 'basic', check: () => !!name.value.trim(), body: () => ({ name: name.value }), dirty: () => name.value !== saved, fields: ['name'], applyErrors: (e) => (errors.value = e) })
async function save() {
  const done = await form.submit((body) => host.api.post('/things', body))
  if (done) close()
}
</script>
<template>
  <STabbedForm v-model="tab" :tabs="tabs" :form="form" layout="top">
    <template #default="{ tab: key }">
      <BasicPane v-if="key === 'basic'" />
      <PricesPane v-else />
    </template>
  </STabbedForm>
</template>
```

- **`STabbedForm`**：`tabs: FormTab[]`（`TabItem` 加 `summary?`、`icon?`）、`v-model`（当前标签）、`layout: 'top' | 'side'`（默认 `top`）、`form?`（`useTabbedForm` 的返回值，取未保存与出错的标签）、`idPrefix?`、`ariaLabel?`。`top` 是 `STabs` 的标签栏；`side` 是左侧导航：序号、标题、一行 `summary`，带 `icon`（`SIcon` 名）的标签在前面多一个图标块、序号移到标题前，导航栏在桌面宽度随内容滚动时贴顶，手机宽度变成横向滚动的一行（这时 `aria-orientation` 为 `horizontal`，当前标签——包括保存失败时自动打开的——滚到中间）。具名插槽 `before-nav` 放在标签前面（`side` 在左侧导航上方、和它一起贴顶，`top` 在标签栏上方），例如控制台账号编辑器的账号类型卡片。每个标签标题后有标记：出错为红点，否则有未保存的修改为黄点（读屏读出 `ui.tabHasErrors` / `ui.tabUnsaved`）。默认插槽按每个标签各渲染一次（参数 `{ tab }`，标签的 key），各在自己的 `tabpanel` 里；所有面板都保持挂载，只显示当前的（`v-show`），切换标签不丢草稿。面板与 `STabs` 一样是标题范围：与标签同名的 `SSettingsGroup` 等标题不再显示。
- **`useTabbedForm(current, tabs)`**：`current` 是当前标签的 `Ref`（即 `v-model`），`tabs` 是按顺序的标签 key（数组、`Ref` 或函数）。它把表单 provide 给调用者下面的组件。返回 `{ current, saving, dirtyTabs, errorTabs, anyDirty, register, submit, clearErrors }`。
- **部分**（`TabbedFormPart`）：`{ tab, check?(): boolean | Promise<boolean>, body(): Record<string, unknown>, dirty?: boolean | Ref | () => boolean, fields?: string[], applyErrors?(errors), invalid?: boolean | Ref | () => boolean }`。`check` 可以等待（例如插件表单的校验）。给了 `invalid`（这个部分现在是否显示着错误：输入时就发现的，或服务端返回、还没被改掉的）时，它的标签是否标红只看 `invalid`，错误随改随消；没给时看上一次保存的结果。一个标签可以有几个部分。调用 `useTabbedForm` 的组件用 `form.register(part)` 登记（返回取消登记的函数）；标签页里的子组件用 `useTabbedFormPart(part, active?)`，组件卸载时自动取消；`active`（布尔值、`Ref` 或函数，默认 `true`）为假时不登记，变真时登记、再变假时取消，组件不必重新挂载（例如控制台定价抽屉里把用户转成专享后，同一个倍率表变成可编辑）；不在这样的表单里时什么也不做。`fields` 是这个部分发的请求体字段前缀：`"model_prices"` 认领字段错误 `model_prices`、`model_prices.0.rule` 和 `model_prices[0]`，不认领 `model_prices_x`。
- **`submit(send)`**：先跑所有部分的 `check`（全部都跑，每个标签自己显示原因），有失败的就标红这些标签、打开其中第一个（按 `tabs` 的顺序），返回 `false`，不发请求；否则把各部分的 `body()` 合并成一个对象（各部分的字段不要重叠），调 `send` 一次并返回它的结果。`send` 以 `ApiError` 拒绝且带字段错误（`ApiError.fields`，§3.1）时，按字段前缀找到对应的部分，把它认领的错误（键是完整路径）交给它的 `applyErrors`，标红并打开第一个标签，返回 `false`；有部分认领、但也有没被任何部分认领的字段错误时，后者拼成一条错误 toast（`字段: 信息`，用 `; ` 分隔），不会静默丢掉；一条也没被认领的失败原样拒绝，由调用方提示。`saving` 从检查开始到请求结束都为真；`errorTabs` 在下一次 `submit` 或 `clearErrors()` 时清空。
