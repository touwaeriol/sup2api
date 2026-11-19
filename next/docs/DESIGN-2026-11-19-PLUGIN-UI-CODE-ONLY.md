# 插件界面只用代码写（设计与任务，2026-11-19）

状态：已实现（集成分支 `integ/plugin-ui`，随核心 0.1.304 批次发布，尚未发布）；与实现的出入在文中就地注明，契约以 CONTRACTS §23、§75 为准。下文是设计时的记录，对照的代码是 `develop` 的 `4d1dcfeb8`。文中 `文件:行号` 都以这个提交为准，路径省略开头的 `next/`。

## 1. 结论

- 插件界面只保留一种写法：插件 `ui/native` 里的 Vue 3 + TypeScript 代码，使用 `@sub2api/ui` 和 `@sub2api/host` 的组件。
- 以下内容全部删除：
  - 声明式表单（`form.mode: "schema"`、`forms/*.schema.json`、`forms/*.ui.json`）
  - 声明式页面（`ui.pages.*.type: "table" | "form"`）
  - 控制台里按 JSON 渲染表单和表格的代码
  - `@sub2api/ui` 的 `SchemaForm` 和 `SchemaField`
- 仓库里不再保留任何表单 JSON，也不保留"只给后端用的极简 schema"。
- `@sub2api/ui` 新增一套通用的表单校验框架（第 5 节）。它的规则写法参照 Element Plus 和 Ant Design Vue 的 `rules`，由我们自己实现，不引入依赖。
- 前端校验只负责即时反馈，**后端仍是最终校验**：
  - 账号凭证由各插件已有的 `ValidateCredentials` 校验。
  - 插件设置由新增的、可选实现的 `PluginService.ValidateSettings` 校验。
  - 原 schema 的约束都要搬进插件的 Go 代码：必填、长度、pattern、上下限、枚举、`additionalProperties:false`（拒绝未知键）。
- **一步做完。** 核心、SDK、界面包（4.0.0）、17 个第一方插件（设计时 18 个，growth 后来删除）在 `feat/plugin-ui-code-only` 分支上逐包合入，全部完成后一次合回 `develop`，随同一个核心版本发布。不发"新旧写法都支持"的过渡核心。
- **装着旧包也不拒绝加载。** 新核心上，清单仍是 `mode: "schema"` 的已装旧包照常加载，网关、路由、定时任务都照常运行。表单处显示"此插件版本的界面已不受支持，请升级插件"，设置的值全部遮蔽。新上传的 schema 包会以 `unsupported` 被拒（不是 `outdated_contract`，见 7.1）。
- 插件线协议 `ProtocolVersion`（2）和 Host API（10）都不变。
- 未验证（community 或 unsigned）的第三方插件没有 native 界面，这一点接受。iframe 模式保持现状，不删也不扩。

## 2. 所有者决定（原话）

- 最初的要求："我们就直接不用声明式 UI 了，直接只用 SDK 提供的相关组件来做。"
- 校验："不保留json 字段校验前端封装一个通用的校验框架出来 参考主流ui框架的校验逻辑"
- 版本："一步做完"
- 未验证的第三方插件没有界面："接受"（iframe 保持现状）

## 3. 现状（带证据）

### 3.1 三种写法

| 写法 | 清单 | 渲染位置 |
|---|---|---|
| 声明式表单 | `form.mode: "schema"`，加 `forms/x.schema.json`、`forms/x.ui.json` | `@sub2api/ui` 的 `SchemaForm`（`web/packages/ui/src/schema/`，`index.ts:111-134` 导出），控件在 `schema/controls/index.ts`，控制台部件在 `web/src/components/schema/widgets.ts:47-57`（`main.ts:20-21` 注入） |
| 声明式页面 | `ui.pages.x.type: "table" / "form"` | `web/src/components/plugin/DeclarativeTable.vue`、`DeclarativeForm.vue`、`declarative.ts`，入口在 `views/plugin-host/PluginPageView.vue:8-9,68-69` |
| native | `form.mode: "native"` 加 `component`；`pages.x.type: "native"`；`ui.settings.mode: "native"` | 插件在 `register(host)` 里注册的组件。账号表单的挂载点是 `views/accounts/AccountEditor.vue:660-664`，传入 `v-model`、`mode`、`account`、`accountType`、`errors`、`proxyTicket`（CONTRACTS §23.5） |

另外两种写法的现状：
- iframe：没有插件使用，代码还在（`components/plugin/PluginIframe.vue`、`AccountEditor.vue:644-658`）。
- `runtime: none`：已经取消，`sdk/manifest/check/retired.go:45`、`check/validate.go:349-350` 把它当作旧契约拒绝。
- claude-oauth：已在 0.1.304 删除（CONTRACTS §116）。
- growth：设计之后也在 0.1.304 删除（CONTRACTS §125），现在是 17 个第一方插件。下文第 3、8、10 节里关于 growth 的行是设计时的盘点，保留作记录。

### 3.2 真实的迁移范围（`plugins/*/manifest.json`）

18 个插件的 runtime 全部是 `grpc`。下表中"—"表示没有这一项。

| 插件 | 账号类型（表单模式） | 设置表单 | 声明式页面 | 已有 ui/native | hostUICompat | forms 文件数 / 字节 |
|---|---|---|---|---|---|---|
| anthropic | apikey（schema） | — | — | 否 | — | 2 / 1428 |
| azure-openai | azure_v1、azure_deployment（schema） | — | — | 否 | — | 4 / 4271 |
| bedrock | bedrock_apikey、bedrock_aksk、bedrock_sts（schema） | — | — | 否 | — | 6 / 21736 |
| codex | codex_device、codex_oauth（native） | **schema** | — | 是 | ^3.20 | 2 / 1685 |
| dynweight | — | — | —（1 个 native 页面） | 是 | ^3.15 | 0 |
| gemini | apikey（schema） | — | — | 否 | — | 2 / 1650 |
| growth | — | schema | —（3 个 native 页面） | 是 | ^3.4 | 2 / 4849 |
| guard | — | schema | —（1 个 native 页面） | 是 | ^3.4 | 2 / 2454 |
| mailer | — | schema | — | 否 | — | 2 / 3235 |
| moderation | — | native，但带 schema 和 uiSchema，运行时用 `SchemaField` 渲染 | —（1 个 native 页面） | 是 | ^3.15 | 2 / 31083 |
| newapi | apikey（schema） | — | — | 否 | — | 2 / 1488 |
| openai | apikey（schema） | — | — | 否 | — | 2 / 2787 |
| payment | — | — | — | 否 | — | 0 |
| relay | relay_key（schema） | — | — | 否 | — | 2 / 1326 |
| sub2api | apikey（schema） | — | — | 否 | — | 2 / 1517 |
| typesafe | apikey（schema） | — | — | 否 | — | 2 / 1425 |
| vertex | vertex_sa、vertex_authorized_user、vertex_apikey（schema） | — | — | 否 | — | 6 / 9067 |
| volcengine | apikey、relay（schema） | — | 2 个 table、2 个 form | 否 | — | 8 / 18085 |

合计：

| 项目 | 数量 |
|---|---|
| 账号表单 | 17 个（分布在 11 个插件） |
| schema 设置表单 | 4 个（codex、growth、guard、mailer） |
| 要重写的设置页 | 1 个（moderation） |
| 声明式页面 | 4 个（volcengine） |
| 要新建 `ui/native` 的插件 | 12 个：anthropic、azure-openai、bedrock、gemini、mailer、newapi、openai、relay、sub2api、typesafe、vertex、volcengine |
| 要删的 `forms/` | 48 个文件，共 108,086 字节 |

### 3.3 schema 现在承担的职责，以及去掉后的去处

| # | 职责 | 现在的实现 | 去掉后 |
|---|---|---|---|
| 1 | 保存凭证时校验（新建、更新、两个模型拉取接口） | `account/creds.go:218-245` 的 `validateSchema`，由 `prepare` 在 `:353` 调用；`prepare` 的调用方是 `handlers.go:1575,1712`、`models.go:75,130` | 删除，只靠插件的 `ValidateCredentials`（`creds.go:359-393` 已经在调用） |
| 2 | 拒绝未知键（17 个 schema 都写了 `additionalProperties:false`） | 同上 | SDK 的 `apikey.Spec` 和 `accountform` 增加已知键列表（6.4）。`apikey.Spec` 现在会把其它键原样放过（`sdk/pluginsdk/apikey/apikey.go:162-167`） |
| 3 | 账号凭证读回遮蔽、保留已存密钥、reenter | **已经不依赖 schema**：`accountTypes[].sensitiveFields`（`sdk/manifest/manifest.go:340`），由 `handlers.go:567`、`creds.go:339` 使用 | 不变 |
| 4 | 受限设置（§21.3） | 后端硬校验 `creds.go:409-413,434-472`（`checkGuarded`），测试在 `account/ownership_test.go:519 TestGuardedSettings`；界面提示靠 `types.go:342-422` 的 `typeForm` 和 `guardForm` 改写 schema | 硬校验不变；删掉 `typeForm`、`guardForm` 和路由 `service.go:169`；界面改用 `accountType.guarded_settings`（`types.go:31-39`），见 5.8 |
| 5 | 插件设置的校验 | `plugin/api/settings.go:332-333,366-414` | 新增 `ValidateSettings`（6.2） |
| 6 | 设置的密钥遮蔽 | `settings.go:60-149` 用 schema 的 `writeOnly`、`format: password`、`x-sensitive`，经 `sdk/manifest/settings_secrets.go:86 SettingsSecrets` 读出；没有 schema 时 `maskAll` 全部遮蔽 | 改为清单 `ui.settings.secrets`（6.1） |
| 7 | 装包时检查密钥能否逐个遮蔽 | `check/validate.go:495-512` 的 `maskableSecrets` | 改为检查 `secrets` 的路径 |
| 8 | 默认值 | 控制台 `SchemaForm` 在前端填好后连同保存（`packages/ui/src/schema/SchemaForm.vue:59`、`views/plugins/detail/SettingsTab.vue:46`）。核心不填默认值 | 前端在新建账号或设置缺键时预填同样的默认值（5.6）；插件 Go 的运行时默认值保持现状 |
| 9 | "插件有设置"的判断 | `plugin/api/plugins.go:115` 的 SQL 写死了 `mode = 'schema'`；控制台 `views/plugins/pluginUtil.ts:453-466` 只认 schema，或 native 且有 page | 改为 `ui.settings` 存在即可（6.3） |
| 10 | `upstreamFields` 漏声明时提示 | `check/account_check.go:331-358` 的 `Warnings` 读 schema 的属性名 | 删除；不再加新的强制要求 |
| 11 | 打包与提供文件 | `tools/sub2api-plugin/pack.go:30`（打包目录含 `forms`）、`:199-212`；`plugin/routes/routes.go:428` 公开提供 `forms/` | 删掉 `forms` |
| 12 | 插件 Go 测试把 schema 当作约束来源 | `plugins/moderation/internal/moderation/settings_schema_test.go:13-17`、`plugins/guard/internal/guard/throttle_test.go:324` | 改为测试插件 Go 的设置校验函数 |
| 13 | e2e | AC04（`e2e/ac04_account_test.go:32-33`）、AC18（`ac18_mixed_account_types_test.go:68`）、AC20（`ac20_openai_gemini_test.go:56`）、AC23（`ac23_ownership_test.go:217-228`、`e2e/ownership.go:20`）断言 `GET .../form` | 删掉这些断言；AC23 改为断言 `GET /account-types` 返回的 `guarded_settings`，以及 POST 提交非官方值被拒 |
| 14 | 测试夹具 | `plugin/pkg/pkgtest/pkgtest.go:74,137,165,176,205-215`、`plugin/registry/registrytest/registrytest.go:92,160-161`、`sdk/pluginsdk/providers/typesafe/typesafe_test.go:24,43`、`tools/sub2api-plugin/cli_test.go`、`dev_test.go` | 改成 native 表单 |
| 15 | 控制台部件契约（proxy-select、group-select、entity-select、param-override 等） | `components/schema/widgets.ts`，COMMON-CONTROLS §6.3；目前没有插件表单使用 | 删除；native 里用 `host.components`（`packages/host/src/index.ts:112-122`） |

不受影响的部分：
- `commonControls`：来自清单，`views/accounts/commonControls.ts` 不读 schema。
- `settingsFields`：明文设置和加密部分的拆分在 `creds.go:63-74`，不依赖 schema。
- 账号的导入导出：也走 `prepare`，没有单独读 schema 的地方。
- `accountTypes[].extensionsSchema`：它不是表单，见 9.2。

### 3.4 已经现成、可以直接复用的部分

- **native 账号表单的接口、遮蔽和校验链路已经完整。** codex 就是现成的参照：`plugins/codex/ui/native/src/CodexAuthForm.vue:135-146` 暴露 `validate`；`internal/codex/plugin.go:205` 实现 `ValidateCredentials`；清单写了 `sensitiveFields`。
- **每个平台插件都有 Go 校验：**
  - `standard`：anthropic、azure-openai、gemini、openai、relay、typesafe
  - `standard` 加 `apikey`：newapi、sub2api
  - `accountform`：bedrock、vertex
  - 自己实现：codex、volcengine
- **插件的设置在 `Configure` 里已经有 Go 侧的解码、默认值和字段错误。** 例如 `mailer/internal/mailer/mailer.go:78-104`、`growth/internal/growth/plugin.go:72-83`、`moderation/internal/moderation/settings.go:162-424`。
- **字段错误路径的转换已经有现成函数：** `views/plugins/pluginUtil.ts:189-200` 的 `settingsFieldErrors`。
- **`ui.native` 只允许受信任的发布者：** 它是 RiskCritical（`manifest.go:1225`），community 和 unsigned 的包上传时就被拒（`plugin/pkg/trust.go:244-246`）。审核页的说明文案是"插件代码将以你的登录身份在控制台中运行"（`web/src/i18n/locales/zh/plugins.ts:128`）。所以不需要再加"含前端代码"的提示。

## 4. 主流 UI 框架的校验做法与取舍

下面按各框架的官方文档和源码整理，2026-11-19 实现 U1 时联网核对过（Element Plus `form.vue` 源码与 Form 文档、async-validator README、Ant Design Form 文档（Ant Design Vue 沿用同一套 rc-field-form 语义）、Naive UI Form 文档、VeeValidate v4 文档）。

| 框架 | 规则写法 | 触发 | 方法 | 路径 | 文案 |
|---|---|---|---|---|---|
| Element Plus（`el-form`） | `rules: { [prop]: FormItemRule[] }`，底层是 async-validator：`required`、`type`、`min`/`max`/`len`、`pattern`、`enum`、`whitespace`、`validator(rule, value, callback)`、`message` | 每条规则带 `trigger: 'blur' \| 'change'`（可写数组）；控件在失焦和值变化时通知表单项 | `validate()` 类型是 `Promise<boolean>`，但**不传回调时校验失败会 reject 出 invalidFields**（源码 `shouldThrow && Promise.reject(invalidFields)`），通过时 resolve `true`；`validateField(props)`、`clearValidate(props)`、`resetFields()`、`scrollToField()`；表单项的 `error` prop "设了就立即显示"，`required` 不写时由规则决定 | `prop="a.b.0.c"` 点分路径 | 写在每条规则的 `message` 上；内置文案来自 async-validator |
| Ant Design Vue（`a-form`） | 同样基于 async-validator 的规则字段，另有表单项的 `validateFirst`（默认 false，即全部规则都跑、全部文案都显示）；`validator` 返回 Promise，reject 即不通过 | `validateTrigger`（默认 change），规则上也可写 | `validate()` 成功时 resolve 值对象，失败时 reject 出 `{ values, errorFields, outOfDate }`（`outOfDate`：校验期间值又变了）；`validateFields`、`clearValidate` | `name` 写成数组路径 `['a', 0, 'b']` | 表单级 `validateMessages` 模板（`${label}`），可整体替换做 i18n |
| Naive UI（`n-form`） | `FormItemRule` 继承 async-validator，多了 `renderMessage`、`key` 和 `level: 'warning'` | `trigger`；`validate()` 默认不看 trigger、运行全部规则 | `validate(cb?, filter?)` resolve `{ warnings }`，失败时 reject 出错误数组；`restoreValidation()`；表单项的 `required` 只管星号 | `path` 点分路径 | 写在规则上 |
| VeeValidate + zod/yup | 表单级 schema，或字段级函数规则 | v4 默认 `validateOnBlur`、`validateOnChange`、`validateOnModelUpdate` 都开，`validateOnInput` 关；失焦时置 `meta.touched`。"先惰性、出错后积极"（eager）是 v3 的交互模式 | `useForm().validate()` resolve `{ valid, errors }`，不 reject；`validateField(path)` 同理；`useField` | 点分或方括号路径 | 由 schema 库提供，或自定义 |

我们的取舍：
- **规则采用 async-validator 风格的声明式子集**（`required`、`type`、`min`/`max`/`len`、`pattern`、`enum`、`whitespace`、`validator`、`message`、`trigger`）。这是 Element Plus、Ant Design Vue、Naive UI 的共同写法，插件作者最熟悉，写法也最短。
- **触发采用"惰性转积极"**（VeeValidate v3 的 eager 模式一类做法，v4 默认是每次改值都校验）。第一次失焦或提交前不报错；之后（字段已 touched）每次改值都重新校验（5.3）。这比固定的 blur 或 change 更少打扰用户，也能及时消除报错。
- **方法名取 Element Plus 的（`validate`、`validateField`、`clearValidate`），返回值取 VeeValidate 的做法：失败时 resolve，不 reject。** Element Plus、Ant Design Vue、Naive UI 不传回调时校验失败都会 reject；我们的 `validate()` 总是 resolve `boolean`，因为它要直接作为 native 账号表单 expose 的 `validate(): boolean | Promise<boolean>`（CONTRACTS §23.5），控制台按返回 `false` 不保存，reject 会变成保存时的未处理异常。
- **文案取 Ant Design Vue 的做法：** 内置规则的默认文案集中放一处、带占位符、走 i18n，规则上的 `message` 可以覆盖。
- **一个字段只显示第一条失败**（相当于 Ant Design Vue 的 `validateFirst: true`；Element Plus 的表单项也只显示第一条）。
- **不引入 async-validator，也不引入 zod、yup、VeeValidate。** 要用到的规则只有十来种，现有 `packages/ui/src/schema/schema.ts:278-327` 的 `validate` 才五十行，自己实现更小。也避免第二套 i18n 文案和回调式 API。插件包本来就从 import map 共享 `@sub2api/ui`（`packages/vite-preset/index.js:15`），校验框架放在 `@sub2api/ui` 里，插件不增加打包体积。
- **不做表单级的 schema 校验**（zod 那类）。那等于换一种形式保留 schema，和所有者"不保留 json 字段校验"的方向不一致。

## 5. 前端设计

### 5.1 校验框架：文件与导出

新目录 `web/packages/ui/src/form/`，从 `index.ts` 导出：

| 文件 | 内容 |
|---|---|
| `rules.ts` | `Rule`、`Rules`、内置规则的求值函数（从 `schema/schema.ts:278-327` 改写而来，原文件随后删除） |
| `path.ts` | 点分路径 `a.b.0.c` 的 `getPath`、`setPath`（不可变写法，返回新对象；值为 `undefined` 时删掉这个键）、`expandPath`/`matchPath`/`overlaps`（`*`；`set`、`touch`、`clearValidate` 判断"相关路径"都用 `overlaps`：相等、祖先或后代）、`fillMissing`（默认值）。服务端字段名转路径（`credentials.a.b`、`values/a/b`、`values.a.b` → `a.b`）只有 `@sub2api/host` 的 `toFieldPath` 一个名字，与 `settingsFieldErrors` 共用 |
| `useForm.ts` | 表单状态：值、规则、本地错误、服务端错误、touched 状态、`validate` 等方法 |
| `SForm.vue` | `<form novalidate>` 容器：provide 表单上下文，拦截提交；`disabled` 用 `<fieldset disabled>` |
| `context.ts` | `FORM_KEY`、`IN_FORM_KEY`、`useFormItem`（`SField`、`SSettingRow` 共用的 `prop` 接线） |
| `messages.ts` 新增 `ui.form.v.*` | 内置文案（中英），从现有 `ui.schema.v.*`（`messages.ts:146-158`、`:305-317`）搬过来 |

### 5.2 规则类型

```ts
// web/packages/ui/src/form/rules.ts
export type Trigger = 'change' | 'blur'
export type RuleType = 'string' | 'number' | 'integer' | 'boolean' | 'array' | 'object' | 'url' | 'email' | 'date-time'
/** A message: literal text, or a function so it follows the current locale (host.t). */
export type RuleMessage = string | ((ctx: RuleContext) => string)

export interface RuleContext<M = Record<string, unknown>> {
  path: string                // 'a.b.0.c'
  model: M                    // the whole form value
  label?: string              // the SField label, for messages
  signal: AbortSignal         // aborted when a newer run of the same field starts
}

export interface Rule<V = unknown, M = Record<string, unknown>> {
  required?: boolean          // '', null, undefined, [] fail; SECRET_MASK passes (a stored secret)
  whitespace?: boolean        // a string of spaces only fails (async-validator: not tied to required)
  type?: RuleType
  /** string / array: length; number / integer: value. */
  min?: number
  max?: number
  len?: number
  pattern?: RegExp            // strings only; SECRET_MASK is never matched against it
  enum?: readonly unknown[]
  /** Custom check: return an error message, or true / undefined when valid. May be async. */
  validator?: (value: V, ctx: RuleContext<M>) => string | true | void | Promise<string | true | void>
  message?: RuleMessage       // overrides the built-in message of this rule
  trigger?: Trigger | Trigger[] // default: lazy-then-eager (5.3)
  /** The rule applies only while this holds (conditionally shown fields). */
  when?: (model: M) => boolean
}

export type Rules<M = Record<string, unknown>> = Record<string, Rule<any, M> | Rule<any, M>[]>
```

内置规则的求值顺序（同一字段按规则的先后逐条求值）：
- 值是 `SECRET_MASK` 时整个字段通过，`validator` 也不跑（值未知）。
- 每条规则先 `required`、`whitespace`。
- 值为空且不是必填时，跳过其余**内置**检查；`validator` 仍然运行（async-validator 的做法，Element Plus 常用它写"两项至少填一项"之类的检查）。
- 然后 `type`、`len`（有它时不看 `min`/`max`，同 async-validator）、`min`、`max`、`pattern`、`enum`，最后 `validator`。
- `validator` 抛错或 reject 也算不通过（Ant Design 的写法），文案取规则的 `message`，没有就取错误信息；被新一轮中止的不算。
- 同一字段只报第一条失败的规则（Ant Design Vue 的 `validateFirst`）。
- 字符串长度按字符（code point）数，与 JSON Schema 和 Go 的 `utf8.RuneCountInString` 一致。

内置默认文案（`ui.form.v.*`，占位符写法 `{n}`、`{label}`）：
- `required`
- `whitespace`
- `type.integer`、`type.number`、`type.url`、`type.email`、`type.dateTime`、`type.invalid`（string、boolean、array、object 不符时）
- `minLength`、`maxLength`、`lenString`、`lenItems`、`lenNumber`（`len` 按值的种类分三条文案）
- `minimum`、`maximum`
- `minItems`、`maxItems`
- `pattern`
- `enum`

### 5.3 触发时机

- 默认是惰性转积极：
  - 字段第一次失焦、或表单调用了 `validate()` 之后，这个字段才算 touched。
  - touched 之前只在失焦时校验，之后每次改值都校验。
- 规则写了 `trigger` 时，只在列出的时机运行（失焦、改值），`validate()` 运行全部规则（Naive UI 同样如此）。
- "改值"就是经 `form.set(path)` 的写入（Element Plus 的控件也是在值变化时通知表单项，包括程序改值）。写入会校验与这个路径重叠的字段（它自己、它里面的、包含它的）。
- 失焦事件的来源：
  - `FieldContext`（`packages/ui/src/field.ts`）增加可选方法 `touch?(kind: Trigger): void`。
  - `useFieldControl` 多返回一个 `blur`，控件把它接到自己的失焦事件上，经 `SField`/`SSettingRow` 的 `prop` 调到 `form.touch(path, 'blur')`。改值不经它上报（见上一条）。
  - 失焦校验与这个路径重叠的全部字段：`<SField prop="tags">` 里的 `STagInput` 失焦时也校验 `tags.*`。判断 touched 时，路径本身或它的任一祖先被 touch 过都算。
  - 写入某个路径时，与它重叠的路径上在途的一轮立即中止并丢弃（结果是旧值的），与 `clearValidate` 相同；组件卸载时中止全部在途校验。
  - SInput、STextarea、SSelect（原生和可搜索两种）、SSwitch、STagInput 接了 `blur`，**组件本身不改 props**。SSecretInput、SUrlPresetInput 包着 SInput/SSelect，自然接入。（原稿把 SDateTime 列为控件，它是只读的时间显示，不是输入。）

### 5.4 嵌套路径、数组、条件显示

- 规则表的键就是点分路径，可以写 `'notify.webhook_url'`、`'items.0.url'`。
- 数组的每一项可以写通配 `'items.*.url'`。求值时按当前值展开成 `items.0.url`、`items.1.url` 等。
- 条件显示的字段写 `when`。条件不成立时这个字段的规则不运行，已有错误清掉。界面上用 `v-if` 隐藏控件。它取代 growth 用的 `ui:visibleWhen`。

### 5.5 异步规则与竞态

- 每个字段维护一个运行序号和一个 `AbortController`。新一轮开始时中止上一轮，迟到的结果按序号丢弃（和 `packages/host/src/useList.ts` 丢弃过期响应的做法一样）。
- `validate()` 等全部字段（包括异步规则）跑完才返回；跑的过程中某个字段又开始了新一轮（用户在改值），以新一轮的结果为准（相当于 Ant Design 的 `outOfDate` 不再出现）。
- 不另设超时：异步校验通常是请求插件路由，超时由请求本身决定。

### 5.6 `useForm`：值、默认值、密钥、hidden 键

```ts
// web/packages/ui/src/form/useForm.ts
export interface UseFormOptions<M extends Record<string, unknown>> {
  /** The value (v-model of the form): read on every access. */
  model: () => M
  /** Writes a whole new value (emit('update:modelValue', v)). */
  update: (next: M) => void
  rules: Rules<M> | (() => Rules<M>)
  /** Server field errors (path -> message), e.g. the native form's `errors` prop. */
  serverErrors?: () => Record<string, string> | undefined
  /** Filled into absent keys once (create mode / unset settings); never overwrites a present key. */
  defaults?: Partial<M>
}

export interface FormApi<M> {
  /** The whole current value (with this tick's writes). */
  value(): M
  /** Value at path. */
  get<T = unknown>(path: string): T
  /** Writes one path: a shallow patch of the current value, every other key kept as it is (hidden keys too). */
  set(path: string, value: unknown): void
  /** Runs every rule; true when nothing failed. Marks every field touched. Never rejects. */
  validate(): Promise<boolean>
  validateField(paths: string | string[]): Promise<boolean>
  clearValidate(paths?: string | string[]): void
  /** The message shown at path: the local error, else the server's. */
  error(path: string): string | undefined
  /** Errors whose path no SField claims (shown above the form). */
  unclaimed: ComputedRef<string[]>
  /** Whether a rule makes path required (SField's star). */
  required(path: string): boolean
  readonly errors: Readonly<Record<string, string>>
  /** v-model of the control at path: <SInput v-bind="form.bind('x')" /> (values pass unconverted). */
  bind(path: string): { modelValue: any; 'onUpdate:modelValue': (v: unknown) => void }
  /** @internal A control at path lost focus (SField / SSettingRow `prop`). */
  touch(path: string, kind: Trigger): void
  /** @internal Shows path's errors at a frame labelled `label` (SField / SSettingRow `prop`); returns the release. */
  claim(path: string, label?: () => string | undefined): () => void
}
export function useForm<M extends Record<string, unknown>>(opts: UseFormOptions<M>): FormApi<M>
```

- **只做补丁写入，从不重建整个对象。** `set` 永远基于当前值做浅拷贝补丁，校验框架也从不按规则表重组整个对象。所以规则表、界面上都没有的键（vertex 的 `access_token`、`expires_at`、`token_for`，moderation 留着用于遮蔽的 `api_key`）原样回到 `v-model`。这条写进 spec。
- **密钥。** 编辑时控制台给的凭证里，密钥值是 `SECRET_MASK`（`******`）。规则把它当作"有值、未修改"：`required` 通过，其余规则跳过（沿用 `schema.ts:298`）。核心看到 `******` 会换回已存的值（`creds.go:118-141 unmask`）。所以"编辑时保留已存密钥"只要求界面在用户不碰时把 `******` 原样送回，`SSecretInput` 负责这一点（5.8）。
- **同一拍里的多次写入。** 父组件经 `v-model` 交回新值要等下一次渲染，`useForm` 记着最后一次写出、还没交回的值，同一拍里连续的 `set` 都在它上面打补丁，不丢前一次。
- **默认值。** `defaults` 只填缺失的键（嵌套对象逐层填）：父组件每交来一份新值（新建账号、切换账号类型）填一次；`useForm` 自己写出的值不再填，所以用户清空的键不会被填回。"新值"按对象身份判断，父组件必须把收到的值原样存回（`v-model`），存成拷贝会让清空的默认键被填回。`usePluginSettings` 不靠这个判断，读到设置时自己填。它取代 `SchemaForm` 填默认值的行为，值必须和原 schema 的 `default` 一致，这是"同输入同输出"的验收项。

### 5.7 和后端字段错误的合并

- 账号凭证的服务端错误，由控制台去掉 `credentials.` 前缀后作为 `errors` 传入（`views/accounts/editorErrors.ts`）。插件设置的错误是 `values/a/b` 或 `values.a.b`（`plugin/api/settings.go:405-410`、`pluginUtil.ts:189-200`）。两者都由 `@sub2api/host` 的 `toFieldPath` 转成 `a.b`。
- **显示规则：** 同一路径上，本地错误优先于服务端错误。用户改了某个路径的值，就清掉这个路径的服务端错误（控制台在任何改动时都会清空 `credErrors`，见 `AccountEditor.vue:528-530`，这里再细到单个路径）。
- 没有任何 `SField` 认领的错误进 `unclaimed`，由 `SCredentialForm` 或 `SSettingsPage` 显示在表单顶部（和 codex 的 `errorList` 做法一样，见 `CodexAuthForm.vue:86`）。
- `settingsFieldErrors`（`pluginUtil.ts:189-200`）搬进 `@sub2api/host`（`fields.ts`，另导出它用的 `toFieldPath`），控制台和插件共用；控制台改用它、删掉 `pluginUtil.ts` 里的那份由 K2 做。

### 5.8 表单容器与表单项（复用现有组件）

- `SForm`
  - props：`form: FormApi`、`disabled?: boolean`
  - 事件：`submit`，只在 `validate()` 通过后触发
  - 插槽：默认插槽
  - 它 provide 一个 `FORM_KEY`，不画任何样式。`disabled` 用 `<fieldset disabled>` 实现，里面的原生控件全部禁用。
  - 已经在 `<form>` 里时（`SFormModal` 和 `SCredentialForm` provide `IN_FORM_KEY`；账号编辑器本身就是 `<form>`）不渲染第二个 `<form>`，避免表单嵌套，提交交给外层。
- `SField` 和 `SSettingRow` 各加一个 prop：`prop?: string`。在 `SForm` 里写了 `prop` 时：
  - `error` 取 `form.error(prop)`；显式传入的 `error` prop 优先。
  - 必填星号取 `form.required(prop)`；显式传入的 `required` 优先。
  - 控件通过 `FieldContext.touch` 报告失焦和改值，`SField` 据此调用 `form.validateField(prop)`。
  - 改动范围是 `SField.vue:14-33` 和 `SSettingRow.vue:20-29` 的 props 与 `provideField` 调用，各十来行。
- `SFormModal`：只加一行 `provide(IN_FORM_KEY, true)`（它本身是 `<form>`），在默认插槽里放 `SForm`；提交写 `@submit="form.validate().then(ok => ok && save())"`。
- 分节：设置页用现有的 `SSettingsGroup`；账号表单等表单里的分节用 `SFormSection`（`title`、`help?`、`collapsed?`、`fields?`：上边线、`h4` 标题、`SHint size="xs"` 说明；`collapsed` 时是 `SDisclosure variant="section"`，默认收起，`fields` 里有值或有错误时自动展开，用户点过后按用户的选择，规则同原 `SchemaObject`）。`createApiKeyForm` 的 `section` 也渲染成它（U3a；原稿说不新增组件，三个插件各手写了一份带说明的分节、volcengine 手写了一份折叠）。

### 5.9 组合组件

| 组件或函数 | 包 | 取代的旧能力 |
|---|---|---|
| `SSecretInput` | ui | `ui:widget: secret`（22 处） |
| `SUrlPresetInput` | ui | `ui:widget: url-presets`（12 处），以及 `guardForm` 的 enum 和只读 |
| `useCredentialForm` 和 `SCredentialForm` | ui | 账号编辑器里的 `SchemaForm` |
| `createApiKeyForm` | ui | 约 10 个 api_key + base_url 类表单 |
| `useGuardedSettings` | host | `guardForm` |
| `usePluginSettings` 和 `SSettingsPage` | ui（用 host 的 api） | 控制台 `views/plugins/detail/SettingsTab.vue` 加 `SchemaForm` |
| `SResourcePage` | ui | `DeclarativeTable` 加 `DeclarativeForm` |

**`SSecretInput`**
- props：
  - `modelValue?: string`
  - `placeholder?: string`
  - `disabled?: boolean`
  - `size?: ControlSize`
  - `revealable?: boolean`，默认 true
- 事件：`update:modelValue`
- 行为：
  - 值是 `SECRET_MASK` 时输入框显示为空，占位符显示 `******`，提示"已存，留空则保留"（`ui.form.secretKeep`）。
  - 输入就替换；清空后发出 `SECRET_MASK`。
  - `autocomplete="new-password"`。
- 照搬 `schema/controls/InputControl.vue:15-40` 的逻辑；另外记住"有已存的值"：输入过再清空仍发回 `SECRET_MASK`。父组件把值换成 `undefined`/`null`（换了账号）时忘掉。
- 可选的密钥（`mailer.password`、`moderation.notify_webhook_secret`、volcengine 的 `secret_key`）原来清空就能删掉，所以加 `clearable`：有已存值时显示带文字的按钮"清除已保存的值"，点了发 `''`、不再当作已存。`createApiKeyForm` 的 `secret` 字段在规则没有 `required` 时自动带上。
- "已存，留空则保留"由组件自己用 `SHint` 显示在输入框下（外层 `SField` 的 hint 它碰不到）。

**`SUrlPresetInput`**
- props：
  - `modelValue?: string`
  - `presets?: string[]`
  - `allowed?: string[] | null`
  - `placeholder?: string`
  - `disabled?: boolean`
  - `readonly?: boolean`
- 事件：`update:modelValue`，空串发 `undefined`，即不发送这个键（§21.3"清空即不发该键"）。
- 行为：
  - 有 `allowed` 时变成选择框；已存值不在列表里时也保留为可选项。
  - 只有一个允许值时只读，显示"由管理员设置"。
  - 没有 `allowed` 时是 URL 输入框，带 datalist 和"预设"选择。
- 照搬 `schema/controls/UrlPresetsControl.vue`；"预设"从原生 `<select class="input">`（控制台全局类）改成带文字按钮的 `SDropdown` 菜单。

**`useCredentialForm`**

```ts
export interface NativeAccountFormProps {      // CONTRACTS §23.5, unchanged
  modelValue: Record<string, unknown>
  mode: 'create' | 'edit'
  account?: { id: number; type: string; credentials: Record<string, unknown> } | null
  accountType?: AccountTypeView | null       // includes guarded_settings, custom_upstream, sensitive_fields
  errors?: Record<string, string>
  proxyTicket?: ProxyTicketSource
}
export function useCredentialForm<M>(
  props: NativeAccountFormProps,
  emit: (e: 'update:modelValue', v: M) => void,
  opts: { rules: Rules<M> | (() => Rules<M>); defaults?: Partial<M> }
): FormApi<M> & { creating: ComputedRef<boolean> }
```

- 它把 `props.modelValue` 和 `emit` 接到 `useForm`，`serverErrors` 接 `props.errors`，`defaults` 只在 `mode === 'create'` 时生效。
- 组件写 `defineExpose({ validate: form.validate })` 就满足 §23.5。
- `proxyTicket` 原样留在 props 上，需要授权流程的表单自己用（codex）。

**`SCredentialForm`**
- props：`form: FormApi`、`disabled?: boolean`
- 插槽：默认插槽
- 渲染：`SForm`，顶部用 `SAlert` 列出 `form.unclaimed`，下面是插槽内容。

**`createApiKeyForm(spec): Component`**

它是工厂函数，返回一个满足 §23.5 的组件，插件只要 `host.registerComponent(name, createApiKeyForm({...}))`。

```ts
export interface ApiKeyFormSpec {
  apiKey?: { min?: number; max?: number; pattern?: RegExp; placeholder?: string; help?: () => string } // default 8-512, /^[!-~]+$/
  /** false: the type has no base_url. */
  baseUrl?: false | {
    required?: boolean
    presets?: string[]
    pattern?: RegExp          // default /^(https?:\/\/[^\s?#]+)?$/, required: /^https?:\/\/[^\s?#]+$/
    placeholder?: () => string
    help?: () => string
  }
  /** Further fields, in order; each is one SField. */
  extra?: ExtraField[]
  defaults?: Record<string, unknown>
}
export interface ExtraField {
  key: string
  kind: 'text' | 'secret' | 'url' | 'select' | 'textarea' | 'date-time' | 'switch'
  label: () => string
  help?: () => string
  placeholder?: () => string
  options?: { value: string; label: () => string }[]   // select: replaces ui:enumNames
  presets?: string[]                                     // url
  rules?: Rule | Rule[]
  section?: (() => string) | { title: () => string; help?: () => string; collapsed?: boolean } // starts an SFormSection (U3a: help, collapsed)
  when?: (m: Record<string, unknown>) => boolean         // shown and validated only while true
  mono?: boolean                                          // text, textarea: monospaced, as SInput's mono (U3b)
}
```

- `base_url` 自动接 `useGuardedSettings`：没有 `account:settings:custom` 时传 `allowed`。
- 不要 `t` 参数：插件的文字都写成函数（`() => host.t('…')`），api_key、base_url 的标签用 `@sub2api/ui` 自己的文案（`ui.form.apiKey`、`ui.form.baseUrl`）。（原稿的 `t` 没有用处。）
- `date-time` 用 `SDateTimeInput`（`SInput type="datetime-local"` 与 RFC 3339 互转，和原 `InputControl` 的转换一致；清空时不发这个键），手拼的表单同样用它（U3a）。原稿写的 `SDateTime` 是只读的时间显示组件，不能输入。
- `select` 选回占位项时删掉这个键（不发 `null`）。
- 能覆盖的类型：
  - anthropic、gemini、typesafe：base_url 选填，带预设
  - openai：多 organization、project
  - relay、newapi、sub2api：base_url 必填
  - azure_v1；azure_deployment 多 api_version，默认 `2024-10-21`
  - volcengine apikey：多 `access_key`、`secret_key`、`asset_base_url`、`asset_region`，分节
  - vertex_apikey：`baseUrl: false`，多 project_id、location
  - bedrock_apikey：`baseUrl: false`，多 region、inference_profile_prefix（选择框）
- 字段差异大的类型（bedrock_aksk、bedrock_sts、vertex_sa、vertex_authorized_user、volcengine relay）自己用 `useCredentialForm`、`SCredentialForm`、`SField` 拼。

**`useGuardedSettings`**（`@sub2api/host`）

```ts
export function useGuardedSettings(accountType: () => AccountTypeView | null | undefined): {
  /** null: not restricted for this caller; else the allowed values. */
  allowed(field: string): string[] | null
  locked(field: string): boolean        // exactly one allowed value
  guarded: ComputedRef<boolean>         // any field restricted (the editor's hint, AccountEditor.vue:74)
}
```

- 依据是 `accountType.guarded_settings` 和 `!useHost().permissions.has('account:settings:custom')`，没有新的数据来源。
- `AccountTypeView` 原稿没有定义，`@sub2api/host` 里新加：`plugin_key`、`type`、`guarded_settings?`、`custom_upstream?`、`sensitive_fields?`，其余字段放开（`[key: string]: unknown`）。
- 它只管控件怎么显示，**后端的 `checkGuarded` 不变**。

**`usePluginSettings`**

```ts
export function usePluginSettings<M>(opts: {
  pluginKey: string
  api: ApiClient                      // host.api (GET/PUT /plugins/:key/settings)
  rules: Rules<M> | (() => Rules<M>)
  defaults?: Partial<M>
}): FormApi<M> & {
  loading: Ref<boolean>; saving: Ref<boolean>; loadError: Ref<unknown>
  dirty: ComputedRef<boolean>         // differs from the loaded value (after defaults)
  canEdit: ComputedRef<boolean>       // plugin:manage or ui.settings.permission
  reload(): Promise<void>
  save(): Promise<boolean>            // validate(), then PUT; server errors via settingsFieldErrors
  reset(): void
}
```

- 逻辑从控制台 `views/plugins/detail/SettingsTab.vue` 提出来：读值、以填好默认值后的值作为"未修改"的基准、保存、错误回显。
- `reenter` 错误码的行为沿用（`settings.go:228-235`）：不是管理员时，改了设置又带着保留的密钥，会要求重新输入。

**`SSettingsPage`**
- props：
  - `settings: ReturnType<typeof usePluginSettings>`
  - `title?: string`
  - `description?: string`
- 插槽：默认插槽，放各个 `SSettingsGroup`
- 自带两样东西：
  - 底部保存栏：未保存提示、重置、保存。照搬控制台 `SettingsTab.vue` + `StickyActionBar.vue` + `SettingsSaveBar.vue` 的行为（保存在没有改动时禁用，重置只在有改动时出现），样式写在组件里、颜色只用 CSS 变量，不依赖控制台全局类（CONTRACTS §23.4）。
  - 离开确认：用 `vue-router` 的 `onBeforeRouteLeave`，加上 `beforeunload`；文案照控制台 `common.unsaved` 搬进 `ui.form.leave`。现在的设置标签没有离开确认，这是新增的。
  - 另外：加载中、加载失败（带重试）；`title` 用 `SPageHeader`；不能编辑时 `SForm disabled`、不显示保存栏；Enter 也会保存（经 `SForm` 的 `submit`）。
- 保存成功后重新读一遍设置（控制台也是保存后重读）：新填的密钥变回 `******`，重置回到保存后的值。重读不置 `loading`，`SSettingsPage` 不会换成转圈、表单不卸载。`SForm` 的提交（Enter）在没有改动时什么都不做，已经校验过时 `save({ validated: true })` 不再校验第二次。
- 默认值由 `usePluginSettings` 在每次读到设置时自己填（不交给 `useForm` 的身份判断）。
- 失败的文案用 `@sub2api/host` 新导出的 `errorMessage(e, t)` 和 `isSilentError(e)`：从控制台 `utils/errors.ts` 搬过去，控制台改为引用它们，不留两份。
- `usePluginSettings` 的返回类型导出为 `PluginSettingsForm`；`canEdit` 用 `useHost().permissions.has`；保存成功、失败的提示用 `@sub2api/ui` 的 `toast`；reenter 文案搬进 `ui.form.reenter`。

**`SResourcePage`**
- props：
  - `client: ApiClient`（`host.pluginApi`）
  - `path: string`
  - `columns: TableColumn[]`（`STable` 的列）
  - `search?: string`：查询参数名，有它才显示搜索框，与原 `Page.search` 语义相同（`manifest.go:1045-1061`）
  - `rowKey?: string`
  - `createLabel?: string`
  - `canCreate?: boolean`
- 事件：`created`
- 插槽：
  - `cell-<key>`
  - `toolbar`
  - `create`：放在 `SFormModal` 里，插槽参数是 `{ close, done }`
- 多一个 prop `submit?: () => Promise<boolean | void>`：`SFormModal` 的保存按钮在弹窗的页脚、插槽内容碰不到，所以由页面传入保存函数（在里面 `form.validate()` 再提交；返回 `false` 不关弹窗），成功后等同 `done()`（Ant Design 的 Modal `onOk` 是同一做法）；抛错时按 `errorMessage` 提示（`isSilentError` 的不提示），弹窗不关。插槽内容自己保存时仍可调用 `done()`。弹窗关闭时插槽内容卸载，写在插槽组件内部的表单状态下次打开就是空的；放在页面上的状态由作者清空。
- 内部用 `useList` 和 `STable`（卡片形式，搜索框、`toolbar` 插槽和新建按钮在表格的工具栏里，分页在页脚）。新建成功后回到第一页并刷新，发出 `created`。

### 5.10 示例：anthropic 的账号表单（整个 `ui/native/src/entry.ts`）

```ts
import { createApiKeyForm } from '@sub2api/ui'
import type { PluginHost } from '@sub2api/host'
import messages from './messages'

export function register(host: PluginHost) {
  host.addMessages(messages)
  host.registerComponent('AnthropicKeyForm', createApiKeyForm({
    baseUrl: { presets: ['https://api.anthropic.com'], placeholder: () => host.t('baseUrl.placeholder') },
    apiKey: { placeholder: () => 'sk-ant-…' }
  }))
}
```

自己拼的表单（bedrock_aksk 的一部分）：

```vue
<script setup lang="ts">
// The runtime props object: a plugin's SFC compiler cannot resolve a props type imported from @sub2api/ui.
const props = defineProps(nativeAccountFormProps)
const emit = defineEmits<{ 'update:modelValue': [Record<string, unknown>] }>()
const t = useHost().i18n.t
const form = useCredentialForm(props, emit, {
  defaults: { region: 'us-east-1', inference_profile_prefix: 'global' },
  rules: {
    access_key_id: { required: true, pattern: /^[A-Z0-9]{16,128}$/ },
    secret_access_key: { required: true, max: 1024, pattern: /^[!-~]+$/ },
    region: { pattern: /^([a-z]{2}(-[a-z]+)+-\d)?$/ },
    inference_profile_prefix: { enum: PREFIXES }
  }
})
defineExpose({ validate: form.validate })
</script>
<template>
  <SCredentialForm :form="form">
    <SField prop="access_key_id" :label="t('plugin.bedrock.akid')"><SInput :model-value="form.get('access_key_id')" @update:model-value="form.set('access_key_id', $event)" /></SField>
    <SField prop="secret_access_key" :label="t('plugin.bedrock.secret')"><SSecretInput :model-value="form.get('secret_access_key')" @update:model-value="form.set('secret_access_key', $event)" /></SField>
    <!-- region, inference_profile_prefix (SSelect) ... -->
  </SCredentialForm>
</template>
```

### 5.11 控制台的改动

- `views/accounts/AccountEditor.vue`：
  - 删掉 schema 分支（`:14,17,102-104,110,496-525,540,639-643`）。
  - `mode` 不是 `native` 或 `iframe` 时，显示 `accounts.formUnavailableOld`："此插件版本的界面已不受支持，请升级插件"。保存仍然可以（凭证原样送回），因为基本信息、分组、模型等其它分区不依赖插件的 JS。
- `views/plugins/detail/SettingsTab.vue`：改为挂载插件的设置组件。条件是 `ui.settings.mode === 'native'`、有 `component`、没有 `page`。加载中和加载失败的提示照 `AccountEditor.vue:662-663`。
- `views/plugins/pluginUtil.ts:453-466`：`consoleSettings` 改为"native、有 component、没有 page"；`nativeSettingsPath` 不变。
- `views/plugins/PluginSettingsView.vue`：同上。
- `views/plugin-host/PluginPageView.vue:8-9,68-69`：删掉 table 和 form 分支。
- `main.ts:10,20-21`：删掉 `SCHEMA_WIDGETS_KEY` 的注入。

## 6. 后端设计

### 6.1 设置的密钥列表：`ui.settings.secrets`

```json
"ui": { "settings": { "mode": "native", "component": "MailerSettings",
  "secrets": [ {"path": "password"} ] } }
"ui": { "settings": { "mode": "native", "page": "dashboard", "permission": "manage",
  "secrets": [ {"path": "notify_webhook_secret"}, {"path": "api_key"}, {"path": "notify_webhook_url", "sensitive": true} ] } }
```

- **类型。** `sdk/manifest/manifest.go` 的 `Form` 增加 `Secrets []SettingsSecret`，结构是 `{Path string; Sensitive bool}`。
  - `Path` 是值下面的 JSON 指针：嵌套对象用 `a/b`，`~0`、`~1` 转义。它和 `SecretField.Pointer` 写法相同（`settings_secrets.go:21-28`），也和 `GET /settings` 的 `secret_fields` 相同。
  - `sensitive: true` 对应原来的 `x-sensitive`：只遮蔽，不当作凭证。
- **范围。** 沿用现有能逐个遮蔽的范围：嵌套对象的属性，最深 `MaxSecretDepth`（8）层。数组里的元素不能单独声明：路径中途遇到数组（或任何不是对象的值）时，那个值整体遮蔽、回传掩码时整体保留（K1 审查 S1，2026-11-19），建议直接声明数组本身。
- **构造。** 新增 `func (f Form) SecretFields() []manifest.SecretField`，返回类型不变。所以 `settings.go` 下游的 `maskValues`、`mergeSettings`、reenter 都不用改。
- **删除。** `SettingsSecrets(schema)` 的整个 schema 遍历、`MaxSecretRefDepth`、`MaxSecretRefNodes`、`UnmaskableFields`（`settings_secrets.go:40-…`），以及 `settings.go:103-120 schemaSecrets`。
- **检查**（替换 `check/validate.go:495-512`）：
  - 只有 `ui.settings` 可以带 `secrets`，账号类型带了报 `unsupported`，账号的密钥用 `sensitiveFields`。
  - `path` 非空，每段非空，不超过 8 层。
  - 不重复，也不能一个是另一个的前缀。
- **`settings.go` 的规则改为**（K1 审查 M1 修订，2026-11-19）：
  - `ui.settings` 是 native 或 iframe 时，按 `secrets` 遮蔽：写了列表按列表，写成 `[]` 就什么都不遮蔽。
  - **没有 `secrets` 这个键**表示"不知道哪些是密钥"，`maskAll` 全部遮蔽。清单在库里存的是原始 JSON（jsonb），`json.Unmarshal` 后键不存在是 `nil`、`[]` 是空切片，二者可以区分；`Form.Secrets` 用 `omitzero`，Go 里构造的清单 `[]` 也不会丢。
  - 没有 `ui.settings`，或者模式是新核心不认识的值（已装的旧 schema 包）时，`maskAll` 全部遮蔽。这是核心对未知模式的安全默认，不是兼容分支。
  - 清单检查：native、iframe 的 `ui.settings`（不带 schema 时）必须写 `secrets`，缺了报 `ui.settings.secrets` `required`。它和 `component` 必填一样只在上传时检查，已装的包照常加载，按上一条全部遮蔽。
  - `secrets` 不读包里的任何文件，所以 `settingsForm` 不再调用 `a.d.Install.Package`（`settings.go:87-90`）。

### 6.2 设置校验：新增 `PluginService.ValidateSettings`

**结论：新增 RPC，不复用 `Configure` 的 `errors`。** 原因有两条：
- `Configure` 会让配置在进程里生效（例如 `growth/internal/growth/plugin.go:81` 的 `p.cfg.Store`）。如果在落库前调用，会让还没提交、可能回滚的配置先生效。
- `Configure` 只在加载时、以及配置变更事件之后调用（`grpcruntime/instance.go:493-506`），语义是"应用"，不是"检查"。

```proto
// plugin.proto, PluginService
// ValidateSettings checks plugin settings before the host stores them
// (PUT /plugins/:key/settings). Optional: UNIMPLEMENTED or no errors = accepted.
rpc ValidateSettings(ValidateSettingsRequest) returns (ValidateSettingsResponse);

message ValidateSettingsRequest { string config_json = 1; }   // secrets already restored from the stored values
message ValidateSettingsResponse {
  repeated FieldError errors = 1;           // field: JSON pointer below the values ("a/b"), "" = the whole object
  string normalized_config_json = 2;        // optional: stored instead of the input
}
```

- **它是可选 RPC，和 `BuildModelsRequest` 一样。**
  - 插件没实现时，SDK 回 `UNIMPLEMENTED`（`sdk/pluginsdk/serve.go:220` 嵌入了 `UnimplementedPluginServiceServer`），核心视为接受（参照 `account/models.go:164`）。
  - 这是可选能力的约定，不是过渡代码。没有设置的插件（dynweight、payment）本来就不用实现。
- **SDK。** `sdk/pluginsdk` 增加接口 `SettingsValidator { ValidateSettings(ctx, Config) (normalized json.RawMessage, err error) }`，错误用现有的 `pluginsdk.FieldErrors`。
  - 插件实现时，把 `Configure` 里现有的"解码、默认值、检查"拆成纯函数，`Configure` 和 `ValidateSettings` 共用，保证"存进去的值一定能 `Configure` 成功"。
- **核心。**
  - `core.Generation` 增加 `SettingsValidator(pluginKey string) (SettingsValidator, bool)`，与 `Scheduler`、`JobRunner` 同一模式（`core/ports_plugin.go:252-261`）。
  - `grpcruntime.Instance` 增加 `ValidateSettings`，照 `grpcruntime/settings.go:63` 的 `SettingsChanged` 的写法。
  - `plugin/api/settings.go:330-334` 的校验换成：先 `mergeSettings` 换回保留的密钥，再调用插件，有 `normalized` 就用它，字段错误加上 `values/` 前缀返回。
  - 插件不在当前代里（没有启用）时，不调用插件校验，直接保存；之后加载时 `Configure` 照常检查（`instance.go:502-504`）。这和账号凭证的规则一致：账号类型只属于已启用的插件。
  - 插件已启用（在当前代里）但进程运行不起来时，调用失败，保存返回 503 `plugin_unavailable`：要改设置先停用插件（K1 审查 S3，不当成接受）。
  - 请求带上插件当前的授权（`grants`，与 `Configure` 下发的同源，K1 审查 S7）。
- **契约版本。** 这是新增、可选的 RPC：`ProtocolVersion` 不升（仍是 2），Host API 不变（`HostService` 没改）。重新生成 proto（`cd sdk && buf generate`）。

### 6.3 "插件有设置"和没有 page 的 native 设置

- `plugin/api/plugins.go:115`：`manifest->'ui'->'settings'->>'mode' = 'schema'` 改为 `manifest->'ui'->'settings'->>'mode' IN ('schema', 'native')`。语义从"控制台渲染设置"改为"插件有控制台能打开的设置"；iframe 设置控制台不打开，保持现状仍为 false。控制台用它决定显示设置标签，插件页里编辑的设置（moderation）由 `nativeSettingsPath` 转过去，`pluginUtil.ts:430-432` 的 `consoleSettingsPath` 不变。
- 清单检查（`check/validate.go:525-570 form`）的 native 分支：`ui.settings` 必须有 `component` 或 `page`，二者至少一个。账号表单仍然要求 `component`。

### 6.4 删除与收紧

账号侧：
- 删除 `account/creds.go:190-290`：`compiled`、`validateSchema`、`collect`、`credField`。同时删掉 `Service.schemas` 缓存，以及 `creds.go:353` 的调用。
- 删除 `account/types.go:335-422`：`rawOrNull`、`typeForm`、`guardForm`。删除路由 `account/service.go:169`，以及 `TestGuardForm`（`ownership_test.go:694`）。`TestGuardedSettings` 和 `TestCheckGuarded` 保留。
- 删除 `core/ports_plugin.go:165-166` 的 `FormSchema`、`FormUI`，以及 `plugin/registry/registry.go:390-393`。

插件设置侧：
- 删除 `plugin/api/settings.go` 的 `validateSettings`、`schemaErrors`、`denyLoader`（`:360-414`），以及 `SettingsView` 的 `Schema`、`UISchema` 字段（`:37-38`）。

清单类型与检查：
- `sdk/manifest/manifest.go:659-669` 的 `Form`：删掉 `Schema`、`UISchema`，`Mode` 的注释改为 `iframe | native`。
- `manifest.go:1036-1072` 的 `Page`：删掉 `Source`、`Columns`、`Schema`、`Submit`、`Search`、`Create`。删掉 `PageLink`（`:1102-1108`）、`Column`（`:1110-1114`）。
- 同步删除 `visible_ui.go:60-90` 处理 `Create` 的部分，以及 `routes.go:136-150` 解析页面路由引用的部分。
- `check/ui.go`：删掉 `pageSearch`（`:75-116`）、`pageCreate`（`:326-343`）、table 和 form 分支（`:221-241`）。`check/validate.go:546-570` 的 `case "schema"` 改为报 `unsupported`："form mode schema is no longer supported: write the form as a native component (DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY)"。页面的 `table`、`form` 同理。
- 删除 `check/account_check.go:331-358 Warnings`，以及 CLI 打印它的地方。

打包与资源路由：
- `tools/sub2api-plugin/pack.go:30`：`packDirs` 删掉 `forms`；删掉 `:198-212` 的 `need` 检查。
- `plugin/routes/routes.go:428`：去掉 `"forms/"`。

SDK 拒绝未知键：
- `apikey.Spec` 增加 `Fields []string`：除 `api_key`、`base_url` 外允许出现的键。凭证与设置两边的合集里出现其它键，就报 `<key>: unknown`（"不认识的字段"）。
- `accountform.Form` 增加 `Only(keys ...string) *pluginv1.ValidateCredentialsResponse`，作用相同。
- 这与原 schema 的 `additionalProperties:false` 等价。已存的账号原来每次保存都过 schema，不会因为这条变得无法编辑。
- 核心在调用插件前已经去掉了扩展字段（`creds.go:345-351`），所以插件看不到扩展键。

默认值（做到同输入同输出）：
- 原来的默认值由 `SchemaForm` 在前端填入后保存，核心和插件都不填。
- 新做法由前端的 `defaults` 填同样的值，插件 Go 的归一化逻辑不加新的默认值。这样通过 API 直接建账号的结果和今天也一样：没有填的键仍然不存。
- 例外是 azure `api_version`：schema 是 `required` 加 `default`，所以 Go 侧补"必填加 pattern"，前端预填 `2024-10-21`。

## 7. 一步做完的落法

### 7.1 旧包在新核心上

- 加载时只拒绝 `CodeOutdatedContract`（`plugin/grpcruntime/runtime.go:236-249`），其它清单问题只在上传时检查（`plugin/install/upload.go:117,141`）。所以 schema 模式报 `unsupported`：新上传的包被拒，已经装着的旧包照常加载。
- **不能用 `outdated_contract`。** 用了的话，§37 那段窗口里（升级计划完成后、内置插件换新之前）旧的内置平台插件会被拒绝加载，账号短暂不可用。
- 旧包在新核心上的具体表现：

| 方面 | 表现 |
|---|---|
| 账号 | 遮蔽来自 `sensitiveFields`，不受影响；校验来自它自己的 `ValidateCredentials`；编辑页的凭证卡片显示"请升级插件"，其它分区可以编辑 |
| 设置 | schema 模式的旧包：模式不认识，于是全部遮蔽（6.1）。native 带 schema 的旧包（moderation 现在的清单）：收尾包删掉 `Form.Schema` 之前仍按 schema 遮蔽；删掉之后它被读成"native、没有 `secrets` 键"，同样全部遮蔽（6.1 的 M1 规则），不会明文读回。控制台不显示设置表单；没有 `ValidateSettings`，于是只能靠 `Configure` |
| 页面 | volcengine 的 table 和 form 页面显示"不支持的页面类型：table"（或 `form`），这是 `PluginPageView` 现有的兜底（`pluginHost.unsupported`；设计时误写成"页面不可用"） |
| 网关、路由、任务、钩子 | 不受影响 |

- 内置插件随核心镜像在计划完成后几秒内换成新包（§37）。带界面的非内置插件（relay、guard、azure-openai、vertex、bedrock）要管理员重新安装新版本（payment 没有界面，这一批没有改它；设计时误列了它）。

### 7.2 版本

- **界面包**：`@sub2api/ui`、`@sub2api/host`、`@sub2api/vite-preset` 一起升到 **4.0.0**（`web/packages/*/package.json`），`HOST_UI_VERSION = '4.0.0'`（`packages/host/src/index.ts:15`）。`ui-contract.spec.ts` 同步改。
- **hostUICompat**：所有带 `ui.native` 的插件写 `^4.0`。包括已有的 5 个（codex `^3.20`、dynweight `^3.15`、growth `^3.4`、guard `^3.4`、moderation `^3.15`）和新建的 12 个。旧控制台（3.x）不会加载它们（`stores/plugins.ts:51-57`），但新插件包的 hostCompat 本来就要求新核心。
- **hostCompat**：所有改动的插件把下限提到发布这批改动的核心版本。如果随 0.1.304 一起发，就保持现在的 `>=0.1.304 <0.2.0`。
- **插件版本号**：`TestReleasedPluginsBumped` 对照的是 `plugins/released.json`。现在 17 个插件的版本都已经比 released.json 高（0.1.304 那批还没发布）。如果这批改动随 0.1.304 一起发，就不用再升；如果 0.1.304 先发，每个改动过的插件再升一个 minor。发布后按测试说明重写 `released.json`。
- **核心**：没有迁移。`ProtocolVersion`、Host API 都不变。

### 7.3 升级须知（写进 CHANGELOG 的"未发布"）

- 插件表单全部改成插件自己的代码。装着旧版本的插件，在升级前编辑账号凭证会提示"请升级插件"，设置会全部遮蔽。
- 内置插件随核心自动升级。非内置插件要逐个装上新版本，列出 relay、guard、azure-openai、vertex、bedrock。
- 第三方插件作者：`form.mode: "schema"`、`ui.pages.*.type: "table" | "form"`、`forms/` 都不再支持，上传会报 `unsupported`。设置的密钥改为在 `ui.settings.secrets` 里声明。community 和 unsigned 的发布者不能带 native 界面（这是现有规则），只能用 iframe。

## 8. 构建与 CI

- **`deploy/docker/build-go.sh:100-102`：** 去掉自动加 `--allow-missing-ui`。清单声明了 native 却没有 `ui/native/dist` 时直接失败。没有 schema 表单之后，缺 dist 就等于发出一个"没有账号表单"的包。
  - `tools/sub2api-plugin/scripts/build-demo.sh:28-29,90-96` 的 `REQUIRE_UI` 改为默认 1，或者删掉这个开关。演示脚本仍保留 `--allow-missing-ui` 参数供本地开发使用。
  - T1 的做法：`REQUIRE_UI` 删掉（发布工作流里的 `REQUIRE_UI: '1'` 随之删除），`build-demo.sh` 与 `build-go.sh` 的通用分支缺 `ui/native/dist/entry.js` 时都报出插件名并退出。`sub2api-plugin pack` 和 `dev` 的 `--allow-missing-ui` 保留，只供本地手工打包和 `dev` 调试用（`pack` 不带它时本来就拒绝缺界面的包）；没有脚本再自动加它。
  - 调用 `pack` 的路径：镜像（`Dockerfile` 的 ui 阶段先跑 `build-ui.sh`）、e2e 栈（同一个镜像）、发布工作流（先跑 `build-ui.sh`）都先构建了全部插件界面；`deploy/ci/check.sh` 的 `plugins` 阶段在 golang 容器里没有 node，注释写明要先在检出上跑 `build-ui.sh`（它打的 guard，以及迁移后的 anthropic、openai、volcengine、relay 都带 native 界面）；`build-go.sh` 的 `GO_BINARIES_ONLY=1` 不打包，不受影响。
- **`deploy/docker/build-ui.sh` 已经遍历 `plugins/*/ui/native`，新工程自动纳入。** 结论是不改成 npm workspace，原因有三：
  - 插件界面对外是独立工程，第三方也这样写。
  - 现有 5 个工程只有 4 个 devDependency，约 87 个包，lockfile 约 45KB；`vue` 和 `@sub2api/*` 走 import map（`packages/vite-preset/index.js:15,77`）。
  - Docker 构建有 npm 缓存挂载（`next/Dockerfile:38`），下载只发生一次。
  - T1 实测（2026-11-19，开发机，npm 缓存已热，Node 26 / npm 11）：按 `build-ui.sh` 的方式逐个 `npm ci && npm run build`。

    | 工程 | `npm ci` | `build` | `node_modules` | `dist` |
    |---|---|---|---|---|
    | codex | 2.4 s | 1.8 s | 39 MB | 56 KB |
    | dynweight | 2.1 s | 2.8 s | 40 MB | 248 KB |
    | growth | 1.9 s | 1.7 s | 37 MB | 44 KB |
    | guard | 2.2 s | 1.4 s | 38 MB | 32 KB |
    | moderation | 1.9 s | 4.5 s | 38 MB | 544 KB |

    - 5 个工程的 lockfile 都是 86–87 个包、约 45KB，依赖完全相同；冷缓存时一次 `npm ci` 2.8 s，下载进缓存 12 MB，之后的工程全部命中缓存。
    - 镜像 ui 阶段里插件界面合计约 23 s、`node_modules` 约 200 MB（只在 ui 阶段，不进最终镜像）。
    - 17 个工程时按平均每个 4.6 s、39 MB 估算：约 80 s、约 660 MB。CI 的 `plugin UIs` 任务（含 typecheck、spec）5 个工程实测 146 s，17 个约 8 分钟，与 Go 的 server 任务并行跑。
    - 改成 npm workspace 只能省掉每个工程约 2 s 的 `npm ci` 和大部分重复的 `node_modules`（磁盘），省不掉 typecheck、spec、build 的时间。上面这个量级可以接受，结论仍是不改；工程数再翻倍、或 ui 阶段的磁盘成为问题时再议。
    - 没有实测整个镜像改动前后的构建时间：T1 只改脚本里的检查，不改构建步骤，镜像构建时间不变。
- **新工程统一照 codex 的模板**（`plugins/codex/ui/native/package.json`、`tsconfig.json`）：`build`、`typecheck`、`test` 三个脚本；typecheck 和 test 借用控制台的 `node_modules`；提交 `package-lock.json`。growth、guard 补上 typecheck 和 test 脚本，以及 tsconfig。
  - 收尾（D1b）改为：build 也用控制台的 vite，工程不再有依赖、lockfile、`.npmrc`、`vite.config.ts`、`vitest.config.ts`；`tsconfig.json` 继承 `web/packages/vite-preset/tsconfig.plugin.json`，构建配置是 `web/packages/vite-preset/vite.plugin.config.js`，spec 用 `web/vitest.config.ts`（CONTRACTS §23）。17 个工程中 16 个的 `dist` 与改动前逐字节相同；codex 的 `proxyTicket` prop 多了 `type: Function`（插件自己的 vue 没有 typescript，解析不了从 `@sub2api/host` 导入的类型，原来输出 `{}`），其余是压缩后的变量名。
- **`.github/workflows/next-ci.yml`：** 新增 `plugin-ui` 任务。
  - 先在 `next/web` 跑 `npm ci`。
  - 再对每个 `plugins/*/ui/native` 依次跑 `npm ci`（只在工程有 `package-lock.json` 时）、`npm run typecheck`、`npm test`、`npm run build`。
  - 任务列表由 `ls next/plugins/*/ui/native/package.json` 生成，和 Go 模块的列表一样（`next-ci.yml:115-124`）。

## 9. 契约改动清单与删除清单

### 9.1 契约

| 位置 | 改动 |
|---|---|
| `sdk/manifest` | `Form` 删 `Schema`、`UISchema`，加 `Secrets`；`Page` 删 table 和 form 的字段；删 `Column`、`PageLink`；`SettingsSecrets` 改为 `Form.SecretFields`；check 把 schema、table、form 报 `unsupported`，新增 `secrets` 的检查，删 `Warnings` |
| `sdk/proto/.../plugin.proto` | 新增可选的 `ValidateSettings`；更新 `ConfigureRequest` 注释（`plugin.proto:52-53`，"validated against manifest.ui.settings schema by the host"改为"checked by ValidateSettings when implemented"） |
| `sdk/pluginsdk` | `SettingsValidator` 接口；`apikey.Spec.Fields`；`accountform.Form.Only` |
| `server/internal/core` | 删 `AccountTypeBinding.FormSchema`、`FormUI`；`Generation.SettingsValidator` |
| HTTP | 删 `GET /account-types/:plugin_key/:type/form`；`GET /plugins/:key/settings` 删 `schema`、`ui_schema` 字段；`has_settings` 语义变化（6.3） |
| 界面包 4.0.0 | 删 `SchemaForm`、`SchemaField`、`SCHEMA_WIDGETS_KEY` 及 `index.ts:111-134` 的 schema 导出；新增第 5 节的全部内容 |

### 9.2 删除清单

- **控制台：**
  - `web/src/components/plugin/DeclarativeForm.vue`、`DeclarativeTable.vue`、`declarative.ts` 及其 spec
  - `web/src/components/schema/widgets.ts`、`widgets.spec.ts`
  - `web/src/i18n/locales/{en,zh}/schema.ts`：先确认使用者，`ProxyPicker.vue:20` 用了 `schema.noProxy`，把这个键搬到 `common` 再删
  - `main.ts:10,20-21`
  - `AccountEditor.vue` 的 schema 分支
  - `api/types.ts` 里 FormView 的 schema 字段，以及设置的 `schema`、`ui_schema`
  - `pluginUtil.ts:434-441` 的 `PluginSettings.schema`、`ui_schema`
- **`@sub2api/ui`：** `src/schema/` 整个目录，以及 `messages.ts` 的 `schema` 段（保留的文案搬到 `form`）。
- **核心、SDK、工具：** 6.4 列出的全部内容；随 `Form.Schema` 一起删掉"有 schema 时掩码按 schema、`secrets` 不必写"的分支：`plugin/api/settings.go` `settingsForm` 里 `f.Schema == ""` 的条件（之后 native、iframe 一律按 `secrets` 键在不在决定）和 `check/validate.go` `formSecrets` 里 `f.Schema` 的两处判断（K1 审查 M1）；`apikey.Spec.Fields` 改成总是检查（K1 审查 S5）；`pkgtest.go:74,137,165,176,205-215`；`registrytest.go:92,160-161`；`typesafe_test.go:24,43`；`cli_test.go`、`dev_test.go` 里 schema 模式的用例。
- **e2e：** AC04、AC18、AC20 对 `.../form` 的断言；AC23 改成 3.3 第 13 行所写的断言。
- **插件：** 16 个插件的 `forms/` 目录（48 个文件）；各插件 `manifest_test.go` 里读 forms 的部分（azure-openai、bedrock、vertex、volcengine、moderation）。
- **文档：**
  - CONTRACTS §21.3（1215-1226 行，删掉 `/form` 改写那一段，以及"表单不设 default"的说法，改成写 native 表单和 `useGuardedSettings`）
  - §23（1325 行起：版本 4.0.0、校验框架、组合组件）、§23.5（`validate` 用 `useCredentialForm`）
  - §75.1、§75.5（5312 行，改成 `secrets` 和 `ValidateSettings`；同时更正"不带 schema 不掩码"这条和现行代码不符的说法）
  - §5.4、§12、§15.8、§20.2、§45.6、§72.10、§96.7 里提到 `forms/`、`/form` 的地方
  - COMMON-CONTROLS.md §6.3（162-179 行）和第 55 行
  - PLUGIN-DEVELOPMENT.md（6 处）
  - ARCHITECTURE.md 里有 37 处提到 schema，逐条核对，只改讲声明式 UI 的部分
  - AGENTS.md 第 80 行的"`forms/`……`ui/`（schema、iframe 或原生微前端）"和第 97 行的"目前 3.20"
  - 新增 CONTRACTS 一节"§120 插件界面只用代码写"
- **`accountTypes[].extensionsSchema` 留还是删：** 建议这次不动。它不是界面表单：控制台从来不渲染它（`web/src` 只有类型声明，见 `PLUGIN-CODEX.md:327`），用途是核心校验账号扩展字段（`account/extensions.go`，CONTRACTS 3526 行）。目前没有插件使用，删掉它要连带改 `POST/PATCH /accounts` 的 `settings` 参数，属于另一项契约。是否另起一项删除，列在第 13 节"需要所有者确认"。

## 10. 逐插件迁移表

每个插件的通用步骤：
1. 没有 `ui/native` 的建工程（照 codex 模板）。
2. 写组件，在 `entry.ts` 注册，文案走 `host.addMessages`。
3. 清单改 `mode: "native"` 加 `component`，补 `hostUICompat: "^4.0"` 和 `ui.native` 权限（审核时这是一项 critical 权限）；设置还要加 `secrets`。
4. Go 侧补齐约束。
5. 删 `forms/`。
6. 改版本号（7.2）。

通用验收：
- 同一组输入，新表单和旧表单产出的键、值逐个相同，包括新建时的默认值。
- 编辑已存账号或设置时，不碰的密钥原样保留。
- hidden 键原样保留。
- 中英文文案齐全。
- 每个插件有**永久的 Go 测试**："原 schema 拒绝的输入，现在 `ValidateCredentials` 或 `ValidateSettings` 仍然拒绝"，下表的约束逐条成一个用例。
- 平台插件的 `conformance.Run` 照常通过。

账号表单（"Go 已有"：已核对的列出位置，没有逐条核对的写"逐条核对"）：

| 插件 / 账号类型 | 组件 | schema 约束（都带"拒绝未知键"） | Go 已有 | Go 要补 |
|---|---|---|---|---|
| anthropic / apikey | `createApiKeyForm`，带预设 `https://api.anthropic.com` | api_key 必填，8–512 个可见字符；base_url 选填 `^(https?://[^\s?#]+)?$`；guarded | `apikey.Spec`（key、base_url） | `Fields` 为空列表，即拒绝未知键 |
| gemini / apikey | 同上，带预设 generativelanguage | 同上；guarded | 同上 | 同上 |
| typesafe / apikey | 同上，带预设 `https://api.typesafe.ai` | 同上；guarded | 同上 | 同上 |
| openai / apikey | 同上，extra 加 organization、project | 同上；organization 和 project 是 `^[!-~]{0,128}$`；guarded | key、base_url | organization 和 project 的格式；`Fields: organization, project` |
| relay / relay_key | base_url 必填 | api_key 同上；base_url 必填 `^https?://[^\s?#]+$` | `RequireBaseURL` | 拒绝未知键 |
| newapi / apikey | 同 relay | 同 relay | 同上 | 同上 |
| sub2api / apikey | 同 relay | 同 relay | 同上 | 同上 |
| azure-openai / azure_v1 | base_url 必填，带 `ui:errorMessages` 文案 | base_url 必填，`^https?://[^\s/?#@]+(/\|/openai/?\|/openai/v1/?\|/api/projects/[^\s/?#]+/?)?$`；api_key 同上 | 只有 key 和 base_url（`azureopenai.go:47-53`） | base_url 的这条 pattern；拒绝未知键 |
| azure-openai / azure_deployment | 同上，extra 加 api_version，默认 `2024-10-21` | 同上；api_version 必填 `^\d{4}-\d{2}-\d{2}(-preview)?$` | 没有 | api_version 必填加 pattern；`Fields: api_version` |
| bedrock / bedrock_apikey | `createApiKeyForm`，`baseUrl: false`，加 region、inference_profile_prefix（选择框） | api_key 必填，8–2048 个可见字符；region `^([a-z]{2}(-[a-z]+)+-\d)?$`，默认 us-east-1；prefix 枚举 none/us/eu/apac/jp/au/in/global，默认 global | region pattern（`bedrock/internal/bedrock/credentials.go:40`） | 逐条核对 key 长度、prefix 枚举；`Only(...)` |
| bedrock / bedrock_aksk | 自己拼 | access_key_id 必填 `^[A-Z0-9]{16,128}$`；secret 必填 1–1024 个可见字符；region、prefix 同上 | `accountform` | 逐条核对；`Only` |
| bedrock / bedrock_sts | 自己拼，用 `SDateTimeInput`（原稿写成只读显示的 `SDateTime`，U3a 改正） | 在 aksk 的基础上：session_token 必填 1–8192；session_expires_at 是 date-time | `accountform` | 逐条核对；`Only` |
| vertex / vertex_apikey | `createApiKeyForm`，`baseUrl: false`，加 project_id、location | api_key 同上；project_id `^([a-z][a-z0-9-]{4,28}[a-z0-9])?$`；location `^([a-z0-9-]{2,32})?$` | `accountform` 加 `apikey` | 逐条核对；`Only` |
| vertex / vertex_sa | 自己拼，JSON 用 `SSecretInput` 多行，或 `STextarea` 加遮蔽 | service_account_json 必填 2–32768；project_id、location 同上，location 默认 global；**hidden 键 access_token、expires_at、token_for 原样保留** | `accountform` | 逐条核对；`Only` 里包含这 3 个 hidden 键 |
| vertex / vertex_authorized_user | 同上 | authorized_user_json 必填 2–32768；其余同 sa | 同上 | 同上 |
| volcengine / apikey | `createApiKeyForm`，带两个预设，extra 加 access_key、secret_key（secret）、asset_base_url（url，带预设）、asset_region，分节 | api_key 同上；base_url 和 asset_base_url 选填 URL，guarded；access_key、secret_key ≤256 `^[!-~]*$`；asset_region ≤64 `^[a-z0-9-]*$` | `platform.go:273`、`assets.go:239-370`（成对填写等） | 逐条核对长度和 pattern；`Fields` |
| volcengine / relay | 自己拼，分节 | api_key 同上；base_url 必填，至少 8 个字符的 URL；api_prefix、video_api_prefix ≤128，`^(/\|/[^/\s?#][^\s?#]*)?$`；access_key、secret_key、asset_base_url、asset_region 同上；video_endpoint、asset_endpoint ≤2048，取 URL 或路径 | `prefix.go:137`、`assets.go` | 逐条核对；`Fields` |
| codex（已是 native） | 不改表单 | 没有 schema | `plugin.go:205` | 只改 `hostUICompat` |

设置表单与页面：

| 插件 | 组件 | schema 约束 | Go 要做的 |
|---|---|---|---|
| mailer（新建 ui/native） | `usePluginSettings` 加 `SSettingsPage`，挂在插件详情的设置标签（没有 page） | host ≤253；port 1–65535，默认 587；security 枚举 starttls/tls/none，默认 starttls；username ≤320；password ≤1000，是密钥；from_address ≤320；from_name ≤100，默认 Sub2API；timeout_seconds 5–25，默认 20 | `Configure`（`mailer.go:78-104`）已有 port、security、from_address 检查。拆出纯函数实现 `ValidateSettings`，补长度检查和未知键检查。`timeout_seconds` 现在是夹到范围内，改成越界即报错（与 schema 相同）。`secrets: [password]` |
| codex 设置 | 同上（ui/native 已有） | user_agent、originator 不能含控制字符（`^[^\x00-\x08\x0A-\x1F\x7F]*$`），默认 "" | `agent.go:48 Configure` 逐条核对后拆出；未知键检查 |
| growth | 同上；条件显示 `when` 取代 `ui:visibleWhen` | 两个开关默认 true；比例 0–100，默认 10；其余数值 ≥0，各有默认值；checkin_timezone 默认 UTC | `compileSettings`（`growth/internal/growth/plugin.go:77`）已有。实现 `ValidateSettings`，逐条核对上下限 |
| guard | 同上；webhook_url 用 `SUrlPresetInput` | webhook_url `^(https?://\S+)?$`；record_snippets 默认 false；alert_cooldown_sec 0–300，默认 60 | `Configure`（`guard/internal/guard/plugin.go:129`）拆出；`throttle_test.go:324` 改测 Go |
| moderation 设置页 | **重写** `ui/native/src/SettingsTab.vue`（316 行）和 `settingsForm.ts`（129 行），不再用 `SchemaField`、`uiGet`、`durationWidget`，改用 `usePluginSettings`、`SSettingsGroup`、`SSegmented`、`SSwitch`、`SInput`，以及现有的 `DurationInput.vue` | 39 个属性：4 个枚举（mode、fail_policy、hit_replay、input_overflow）、26 个整数上下限（例如 block_status 400–599、pass_retention_days 1–3）、block_message ≤500、notify_webhook_url ≤1000（sensitive）、notify_webhook_secret ≤200（密钥）、api_key（hidden，是密钥）、jev_default_set（JSON） | `settings.go:162-424` 已有解码和 `clampInt`。实现 `ValidateSettings`，越界报错（目前是夹到范围内，需逐项核对后改）。`settings_schema_test.go` 改测 Go。清单删掉 `schema` 和 `uiSchema`，加 `secrets: [notify_webhook_secret, api_key, {notify_webhook_url, sensitive}]` |
| volcengine 4 个页面 | `assets`、`asset_groups` 用 `SResourcePage`（列与原清单 columns 相同，`search: 'q'`，新建要 `assets:manage` 权限）；`new_asset`、`new_asset_group` 改成 `SResourcePage` 的 `create` 插槽里的表单，清单删掉这两个页面 | asset：group_id 必填 ≥1；name ≤128；asset_type ≤64；url 必填 1–2048 `^(https?://\|tos://)[^\s]+$`。asset-group：account_id 必填 ≥1；name 必填 1–128；title ≤256；description ≤1024；group_type ≤64，默认 AIGC | 插件路由的服务端校验**未确认**是否独立于 schema（DeclarativeForm 只在前端用 schema 校验）。逐条核对，缺的补到路由处理里 |
| dynweight、payment | 只改 `hostUICompat`（dynweight） | — | — |

## 11. 任务拆分

所有包在 `feat/plugin-ui-code-only` 上逐个合入，全部完成后一次合回 `develop`。每包结束时 `go test`、控制台和插件的 typecheck、test 都要通过。"依赖"一列写必须先合入的包。

| 包 | 内容 | 文件范围 | 验收 | 依赖 | 量级（人日） |
|---|---|---|---|---|---|
| **U1** 校验框架 | 5.1–5.8：`form/`、`SForm`、`SField` 和 `SSettingRow` 的 `prop`、`FieldContext.touch`、`ui.form.v.*` 文案 | `web/packages/ui/src/form/`、`field.ts`、`SField.vue`、`SSettingRow.vue`、各控件的 `useFieldControl` 接入、`messages.ts`、`index.ts` | spec 覆盖：每条内置规则；惰性转积极；异步竞态（迟到丢弃）；`*` 路径；`when`；`SECRET_MASK`；hidden 键保留；服务端错误合并与按路径清除；`validate()` 返回布尔值 | — | 3 |
| **U2** 组合组件 | 5.9：SSecretInput、SUrlPresetInput、useCredentialForm、SCredentialForm、createApiKeyForm、usePluginSettings、SSettingsPage、SResourcePage；host 的 `useGuardedSettings` 和 `settingsFieldErrors` | `web/packages/ui/src/`、`web/packages/host/src/` | 用 `createApiKeyForm` 写出 anthropic 表单的 spec，与旧 schema 表单逐键相同；usePluginSettings 的未保存、reenter、错误回显与现在的 SettingsTab 一致；SResourcePage 的分页、搜索、新建后刷新 | U1 | 4 |
| **K1** 核心与 SDK | 6.1–6.4：`secrets`、`SecretFields`、`ValidateSettings`（proto、gen、pluginsdk、grpcruntime、core.Generation、settings.go）、`has_settings`、check（unsupported、secrets、删 Warnings）、`apikey.Spec.Fields`、`accountform.Only`；删除 validateSchema、typeForm、guardForm、FormSchema、页面字段；pack、routes 去掉 forms；pkgtest、registrytest、typesafe_test、cli_test、dev_test 改成 native | `sdk/manifest`、`sdk/proto`、`sdk/gen`、`sdk/pluginsdk`、`server/internal/{account,plugin,core}`、`tools/sub2api-plugin` | 现有遮蔽、保留旧值、reenter 的测试改用 `secrets` 后全部通过；新增"旧 schema 包已安装时仍能加载、设置全部遮蔽、上传新的 schema 包报 unsupported"的测试；`TestGuardedSettings` 通过；`buf generate` 无差异 | — | 5 |
| **K2** 控制台 | 5.11；`api/types.ts` | `web/src/` | 旧模式的提示文案；没有 page 的 native 设置能挂载；声明式分支全部删掉后控制台的 typecheck 和 test 通过 | U2（设置标签挂载）、K1（接口字段） | 2 |
| **T1** 构建与 CI | 第 8 节；growth、guard 补脚本；记录构建耗时 | `deploy/docker/`、`tools/sub2api-plugin/scripts/`、`.github/workflows/next-ci.yml`、`plugins/{growth,guard}/ui/native/` | CI 的新任务能跑完已有的 5 个工程；缺 dist 时 build-go.sh 失败 | — | 1 |
| **G1** apikey 批 | anthropic、gemini、typesafe、openai、relay、newapi、sub2api：新建 ui/native、清单、Go 补约束、删 forms | 这 7 个 `plugins/<name>/` | 第 10 节的通用验收 | U2、K1 | 3 |
| **G2** azure | azure-openai 的两个类型 | `plugins/azure-openai/` | 同上 | U2、K1 | 1 |
| **G3** 云厂商 | bedrock（3 个）、vertex（3 个，hidden 键） | `plugins/{bedrock,vertex}/` | 同上，另加 hidden 键保留的 spec | U2、K1 | 3 |
| **G4** volcengine | 2 个账号表单，4 个页面改成 2 个 SResourcePage 加 create 插槽；路由的服务端校验 | `plugins/volcengine/` | 同上，另加列表、搜索、新建的 spec | U2、K1 | 3 |
| **G5** 设置 | mailer（新建 ui/native）、codex 设置、growth、guard；`ValidateSettings` | `plugins/{mailer,codex,growth,guard}/` | 同上；`Configure` 与 `ValidateSettings` 共用一个纯函数 | U2、K1、K2（设置标签挂载） | 3 |
| **G6** moderation | 设置页重写，`ValidateSettings` | `plugins/moderation/` | 同上；39 个键逐个对照；上下限越界报错的 Go 用例 | U2、K1 | 3 |
| **D1** 删除与版本 | 9.2 中前端剩余部分：`@sub2api/ui` 的 `schema/`、控制台的 schema i18n；e2e；界面包 4.0.0；5 个已有 native 插件的 `hostUICompat`；dynweight；插件版本号与 hostCompat | 跨目录（只做删除和版本号） | 全仓搜索 `SchemaForm`、`uiSchema`、`forms/`、`mode: "schema"`、`"table"`、`"form"`、`/form` 没有残留（文档和 CHANGELOG 历史除外）；`TestReleasedPluginsBumped` 通过；e2e 能编译 | G1–G6、K2 | 1.5 |
| **D2** 文档 | 9.2 的文档部分；CONTRACTS 新增 §120；CHANGELOG 的升级须知（7.3）；AGENTS.md | `docs/`、`CHANGELOG.md`、`AGENTS.md` | 文档与代码一致 | D1 | 1.5 |

- **合计约 34 人日。** 一个人做约 7 周。
- 三四个人并行时：
  - U1 和 K1、T1 同时开始。
  - U2 接着 U1。
  - 然后 G1–G6 按插件目录分给不同的人，目录互不重叠。
  - 最后 D1、D2。
  - 这样约 3 周。
- 等 U2 和 G1 完成后再重估一次工作量。

## 12. 风险

| 风险 | 对策 |
|---|---|
| 新表单写出的键或默认值和原来不一样，已存账号编辑后被改坏 | 每个表单都有"同输入同输出"的 spec；`set` 只做补丁写入；hidden 键有专门的 spec |
| Go 侧漏掉原 schema 的某条约束 | 第 10 节逐条列出；每条一个永久 Go 用例；SDK 的 `Fields`/`Only` 统一拒绝未知键 |
| 设置的 `secrets` 漏写一个字段，导致密钥明文回显 | `secrets` 对照原 schema 的 writeOnly、x-sensitive 逐个写（mailer 1 个，moderation 3 个；codex、growth、guard 没有），K1 和 G5、G6 的测试断言 `GET /settings` 的 `secret_fields` |
| 插件 JS 加载失败时，账号凭证编辑不了（原来 schema 表单不依赖插件 JS） | 其它分区仍可编辑；显示加载失败原因（`AccountEditor.vue:663`）；构建时缺 dist 直接失败（第 8 节） |
| moderation 的 `Configure` 现在把越界值夹到范围内，改成报错后，已存的越界值会让插件起不来 | 已存的值原来每次保存都经 schema 校验，不会越界。旧版本（0.1.222 之前）没有 schema 时存下的值，`Configure` 继续夹到范围内；只有 `ValidateSettings` 对新提交的值报错。这样启动不受影响 |
| 这批改动和 0.1.304 混在一起，发布风险集中 | 第 13 节由所有者决定是否随 0.1.304 一起发 |

## 13. 需要所有者确认

1. **后端校验保留。** 我把"不保留 json 字段校验"理解为不保留 JSON 形式的校验，而不是取消后端校验：账号凭证和插件设置仍由插件的 Go 代码做最终校验，前端校验只做即时反馈。如果所有者的意思是后端也不再校验，请明确告知，这会让绕过界面的 API 调用可以写入任意值。
2. **是否随 0.1.304 一起发。** 一起发：非内置插件只需要重装一次，插件版本号不必再升。分开发：0.1.304 的风险面更小，但非内置插件要再重装一次。建议看完成时间：0.1.304 还没发就一起发，已经发了就作为下一个核心版本单独发。
3. **`extensionsSchema`。** 建议另起一项删除（没有使用者，控制台也不渲染），这次不动。
4. **插件没启用时保存设置。** 不调用插件校验，直接保存，加载时由 `Configure` 拒绝无效值（与账号凭证一致）。另一种做法是插件没运行时拒绝保存，但那是新增限制，没有采用。

## 14. 替所有者做的决定

- 校验框架自己实现，不引 async-validator、zod、VeeValidate；规则写法取 async-validator 风格的子集，触发取惰性转积极（第 4 节）。
- 设置校验新增可选 RPC `ValidateSettings`，不复用 `Configure`；`ProtocolVersion` 和 Host API 不升（6.2）。
- 设置密钥写在 `ui.settings.secrets`；账号密钥继续用现有的 `sensitiveFields`，不新增字段（6.1）。
- schema 模式在 check 里报 `unsupported`，不报 `outdated_contract`；已装的旧包照常加载（7.1）。
- 默认值只在前端预填，插件 Go 不新增归一化默认值（azure 的 `api_version` 改为后端必填）（6.4）。
- volcengine 的两个 form 页面并进 SResourcePage 的新建弹窗，清单里删掉这两个页面（第 10 节）。
- 不改 npm workspace，构建耗时在 T1 实测（第 8 节）。
- `has_settings` 改为"插件有设置"（6.3）。
- U1、U2 实现时补充的（2026-11-19）：
  - `validate()` 失败时 resolve `false`，不 reject（第 4 节）。
  - `validator` 在空值时也运行，抛错或 reject 算不通过；`whitespace` 不要求同时写 `required`（5.2）。
  - "改值"以 `form.set` 为准，控件只上报失焦（5.3）。
  - 默认值按"父组件交来的每一份新值"填一次，用户清空的不再填回（5.6）。
  - `SForm` 在已有 `<form>` 里不再渲染 `<form>`，`disabled` 用 `<fieldset>`；`SFormModal` 加一行 provide（5.8）。
  - `createApiKeyForm` 去掉 `t` 参数，`date-time` 用 `datetime-local` 输入框；`SResourcePage` 加 `submit` prop；`SSettingsPage` 新增离开确认（5.9）。
  - 审查后补的：`FormApi.bind`；`SSecretInput` 的 `clearable`；保存后重读设置；控制台的报错文案搬进 `@sub2api/host`（5.6–5.9）。

## 15. 没能确认的事

- volcengine 素材路由（`POST /assets`、`/asset-groups`）的服务端校验是否覆盖原 schema：只看到账号凭证里的素材字段有校验。
- bedrock、vertex、volcengine、codex 的 Go 校验对第 10 节每条约束的覆盖程度，只抽查了 bedrock 的 region 和 `apikey.Spec`。G 系列任务第一步就是逐条核对。
- `plugin:events` 的 `config` 事件在哪里触发重新 `Configure`：只看到发布端（`plugin/install/service.go:325`）。
- 镜像构建时间：没有数据（T1 实测）。
- 生产环境有没有装非第一方插件：没有查。
