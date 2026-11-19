# 分组定价：用户组、私有分组授权、分组内模型定价（设计文档）

> 日期：2026-11-12。状态：已实现，实现后的审查发现正在修复（第 13 节）；尚未发布。设计经过独立审核并按结论修改（第 12 节）。分支：`claude/project-thread-27oe9k`。迁移 0095、0096。契约在 CONTRACTS §97，本文与它不一致时以它为准。
> 界面先按模拟数据做完并经用户确认，后端照 `next/web/src/api/pricing.ts`（类型）和 `next/web/mock/pricing.ts`、`next/web/mock/pricingStore.ts`（行为）实现。实现之后的口径以后端为准，mock、前端和文档跟随（第 13 节列出了与 mock 原来不同的地方）。
> 路径省略前缀 `next/`；Go 文件省略 `server/internal/`。
> 2026-11-19 修订（所有者决定）：`missing_price_policy` 删除，没有价格的模型一律拒绝（403 `model_price_not_configured`），没有免费放行；下文提到它的地方以 CONTRACTS §17.3 为准。

---

## 1. 模型（已确认）

实付 = 标价 × 倍率。两条链各自解析，先命中者生效，互不遮蔽。

用词固定：**全局价**是价格表里按模型设置的那条价格；**原价**是按全局价算的费用（全局价 × 用量，不含规则和倍率；英文 base price）；**标价**是规则之后、倍率之前的价格（英文 list price），没有规则时就是全局价；**实付**是标价费用 × 倍率。

- **标价**：用户组专属价（用户组 × 分组 × 模型）→ 分组内定价（分组 × 模型）→ 全局价。规则两种：
  - `multiplier`：全局价 × N，全局价变化时跟着变。N 可以是 0（免费）和 1（用户组级的 ×1 表示“本组按全局价，不吃分组内定价”）。
  - `custom`：固定价，沿用全局价的计费方式（按 token：`p / c / cr / cc / cc1h`；按次：`price`）。全局价是表达式计费的模型只能用 `multiplier`。
  - 模型没有启用的全局价时不能计费，规则不生效（照旧走 `missing_price_policy`）。
  - `custom` 规则的计费方式与全局价现在的计费方式不同（全局价后来被改成了别的方式）时，这条规则失效：解析时跳过它，往下一层找；接口里这条规则带 `inactive: true`。
- **倍率**（替换，不连乘）：用户组 × 分组专属倍率 → 分组默认倍率 × 用户组全场折扣（折扣 ≠ 1 时）→ 分组默认倍率。实际倍率保留 4 位小数（四舍五入），上限 999999.9999，与 `usage_logs.rate_multiplier numeric(10,4)` 一致：首次结算、重试、对账用的是同一个数。折扣必须 > 0（免费用专属倍率 0 表达）。
- 每个用户只属于一个用户组。内置“默认”用户组：新用户进它，不能删。
- **个人专属** = 系统建的、只有这一个用户的用户组（`kind = personal`）。设为个人专属时可复制原用户组的设置或从空白开始；移回命名用户组时个人设置删除。
- 分组可见性：`public` 所有人可用；`restricted`（界面叫“私有”）只有被授权的用户组可用。
- 分组提供的模型 = 分组内未删除账号声明的模型；有账号没声明模型（不限）时再加上全部已启用全局价的模型；最后经分组模型名单过滤。

代码里用户组叫 **segment**：`user_groups` 是旧的“用户 ↔ 分组”授权表，`tier` 是价格档位，都不能再用。

## 2. 数据模型（迁移 `0095_segments.sql`、`0096_usage_base_cost.sql`）

全部是新增，不删不改旧对象；旧二进制在新库上照常工作（见 §9）。DDL 以迁移文件为准，这里只列要点。两个迁移都带 10 秒的 `lock_timeout`，拿不到锁就失败退出，重跑即可（都幂等）。

| 对象 | 要点 |
|---|---|
| `segments` | `kind`（`named \| personal`）、`name`（命名用户组不区分大小写唯一；个人专属为空）、`description`、`discount numeric(10,4)`（> 0，默认 1）、`is_default`（至多一行）、`user_id`（个人专属的唯一成员，每个用户至多一个）、`origin_id`（个人专属原来所在的用户组） |
| `segment_groups` | 主键 `(segment_id, group_id)`；`granted`、`rate_override numeric(10,4)`（NULL = 跟随）。既没授权也没专属倍率的行不保留 |
| `price_rules` | `group_id`、`segment_id`（NULL = 分组内定价）、`model`、`kind`（`multiplier \| custom`）、`multiplier`、`mode / config / expression / expr_hash`（custom）；唯一索引 `(group_id, model) WHERE segment_id IS NULL` 和 `(group_id, segment_id, model)`（后者不带条件，兼作按分组查规则的索引） |
| `users.segment_id` | 非空，默认值 `default_segment_id()`：任何不写这一列的 `INSERT INTO users`（旧二进制、插件、测试）都进默认组 |
| `usage_logs.base_cost` | `numeric(20,8)`，原价；旧行为 NULL。单独放在 0096：0095 在事务里持有 `users` 的排他锁，不能同时去等 `usage_logs` 的锁（鉴权每次请求都连 `users`） |

回填（0095，幂等：只动 `segment_id IS NULL` 的用户）：

1. 建“默认”用户组，所有用户（含已软删除的）进默认组。
2. 旧授权迁成个人专属：未删除、且在 `user_groups` 里至少有一个 `restricted` 分组的用户，各建一个个人专属（`origin_id` = 默认组），这些私有分组记 `granted = true`。指向公开分组的旧授权没有意义，不迁。

0096：加 `base_cost`，重建 `usage_hourly_mark_update` 触发器（列清单加 `base_cost`）。

`user_groups` 表本次保留、不再读写，下一个版本再删。用户软删除时（`iam`）：个人专属删除，`segment_id` 改回默认组。命名用户组删除时成员回默认组；默认组不能删（应用层的规则；数据库层只有 `users.segment_id` 的外键挡着，成员全部移走后手工删得掉，见 §9.3）。

## 3. 计费链路

### 3.1 一次请求里“标价”和“倍率”各自怎么来

现状（调研见 `group-pricing-design/research/research-sup2api-next.md` §B6）：倍率只有 `groups.rate_multiplier`，在 `apikey.Service.lookup` 的一条 SQL 里读出（每次请求查库，没有缓存），经 `APIKeyPrincipal.Group.RateMultiplier` → `gateway/pipeline.go newRecord` → `UsageRecord.RateMultiplier` 进入预扣和结算；价格由 `Pricer.Resolve(ctx, model)` 给出一条全局 `core.PriceRule`，它的表达式被冻结在使用记录上（`expr_hash` + `model_price_history`）。`billing.Service.Quote` 是唯一的算钱入口：`Eval(expression) × RateMultiplier`，四舍五入到 8 位。

改动的原则：**标价 = 一条表达式 × 一个可选的常数，倍率 = 一个数**。`Quote` 和 `QuoteInputs` 不改，表达式文本不拼接、不改写（只有给下游同步的 `GET /key/prices` 把倍数折进导出的价格，§5.2；计费不读它）。

- **倍率**：`lookup` 的 SQL 多连两张表（`segments`、`segment_groups`，都按主键），读出 `segment_id`、`segments.discount`、`segment_groups.rate_override`、`segment_groups.granted`；实际倍率由 `core.SegmentRate(groupDefault, discount, override)` 算出（全仓唯一一处写这条规则，含 4 位小数和上限）。`APIKeyPrincipal.Group.RateMultiplier` 直接放**实际倍率**，下游（`newRecord`、`/key/prices`）不用知道它是怎么来的；另加 `APIKeyPrincipal.SegmentID`、`RateSource` 供记录来源。
- **能否使用私有分组**：`GroupAvailable = visibility = 'public' OR segment_groups.granted`，同一条 SQL 里算。
- **标价**：`core.Pricer.Resolve(ctx, core.PriceQuery{Model, GroupID, SegmentID})`。`billing` 的价格快照同时载入 `price_rules`（同一个 TTL、同一个 `config:changed` 失效）。解析顺序：全局价不存在 → 照旧（报错或免费）；否则取 `(group, segment, model)` 规则，没有或失效再取 `(group, NULL, model)`，都没有就是全局价。返回的 `core.PriceRule` 多三个字段（都 `omitempty`，旧的已持久化 JSON 照常读）：
  - `Source`：`group | segment`；全局价时留空（冻结到使用记录时省略）。给人看的接口（定价表的 `source`、试算的 `price_source`、`/me/prices` 的 `source`）把全局价写成 `global`。
  - `Scale *decimal.Decimal`：`multiplier` 规则的 N；表达式、`Mode`、`ExprHash` 都还是全局价的。nil = 没有倍数。
  - `Base *PriceBase`：`custom` 规则命中时的全局价（`Expression`、`ExprHash`），用来算原价。此时 `Expression / ExprHash / Mode` 是规则自己的（保存规则时由 `expr.Generate(mode, config)` 生成并存下，同时写进 `model_price_history`）；`ID` 和 `VideoOnly` 始终取全局价的（`usage_logs.price_id` 一直指全局价）。
- **乘数只在一处合成**：`func (r *PriceRule) Multiplier(rate decimal.Decimal) decimal.Decimal`（`rate × Scale`，nil 安全）。传给 `Quote` 的 `RateMultiplier` 都经过它：`billing/additional.go quoteRecord`（预扣）和 `usage/settler.go priceOne`（结算、预留、对账）。
- 网关：`prepareBilling` 和 `prepareReferencedPrices` 两处 `Resolve` 传 `PriceQuery`；`newRecord` 把 `SegmentID`、`RateSource` 记到 `UsageRecord`。`clonePriceRule` 连同 `Scale`、`Base` 深拷贝。其余（计费方式准入、价格入参捕获）不变，因为它们只看 `rule.Mode`、`rule.VideoOnly` 和 `rule.Expression`。`custom` 规则只存在于按 token / 按次的全局价之上，全局价的表达式不读请求参数，`Base` 不需要另外捕获入参。
- 插件只读报价 `PriceModelUsage`（`billing/usage_quote.go`）仍按全局价、倍率 1。
- WebSocket 连接整个生命周期复用升级时的 principal（今天分组倍率也是这样）：连接期间用户组变化，倍率要到重连才变，标价每轮重新解析。

### 3.2 冻结与重试

使用记录写入时，主项的来源和规则冻结在 `billing_detail.inputs.pricing`：

```json
{ "segment_id": 12, "rate_source": "discount", "price_source": "segment",
  "scale": "0.8",                         // multiplier 规则
  "base": { "expression": "…", "expr_hash": "…" } }   // custom 规则：全局价
```

`price_source` 只有 `segment`、`group` 两个值，标价是全局价时省略。倍率来源是分组默认且标价是全局价时整个 `pricing` 省略：这样的行 `billing_detail` 与今天逐字节相同。附加和替换分项（`PricedUsage`）本来就整条持久化 `Price`，新字段随它一起冻结。

结算重试（`usage/settler.go RetryPending`）和对账（`usage/reconcile.go loadReserved`）从库里重建 `pending` 时：表达式照旧按 `expr_hash` 从 `model_price_history` 取（`custom` 规则的表达式在保存规则时已写进历史表，任何版本的节点都取得到），`scale` 和 `base` 从 `inputs.pricing` 取。重试不重新解析规则，规则在请求之后被改不影响这笔账。显式构造 `pendingInputs` 的两处（`settler.go` 写入和 `fromRecord`）要带上新字段。

读回冻结价格的两条 SQL（重试、对账）共用同一段连接（`settler.go` 的 `frozenPriceJoins`）。它原来在 `model_price_history` 里找不到 `expr_hash` 时回退到 `price_id` 指向的全局价现在的表达式；这个回退现在多一个条件：这行不是自定义价（`billing_detail` 的 `inputs.pricing.base` 为空）。自定义价的行 `price_id` 一直指全局价，照旧回退就是静默按全局价扣费。现在自定义价的行在历史表里缺行时：重试进 `failed`（`price expression not found`）；对账保持 `reserved` 的估算值，并在 `pending_settlements.last_error` 留痕。

### 3.3 三个金额

| 名称 | 含义 | 存在哪 |
|---|---|---|
| 原价 `base_cost` | 全局价 × 用量 | `usage_logs.base_cost`（新列）；`billing_detail.cost`（含义不变：倍率前、全局价） |
| 标价费用 `list_cost` | 标价 × 用量 | `billing_detail.list_cost`，只在命中规则时写 |
| 实付 `total_cost` | 标价费用 × 倍率 | `usage_logs.total_cost`（不变） |

`priceOne`（两个版本合成一个实现）对一个 `pending` 返回这三个数：

- 没有规则：原价 = 标价费用 = `res.Cost`。
- `scale`：原价 = `res.Cost`，标价费用 = `res.Cost × scale`。明细里的档位、分项、加价规则都是全局价的，不混入倍数。
- `base`：标价费用 = `res.Cost`（规则表达式），原价 = 全局表达式再求值一次（用它自己的 `Uses` 做 `Normalize`，两条表达式计价的缓存类别可能不同）。

`priceComponents` 对主项和每个附加、替换分项三个数都累加（分项的 `scale / base` 取自 `item.Price`）。`billing_detail.cost` 保持“全局价口径”是有意的：

- 账号的原始费用（`0064` 的 `account_usage_raw_cost`，优先读 `billing_detail.cost`）度量的是账号消耗了多少上游额度，不应随某个分组的售价变化。函数不用改，也不用重算历史。
- 统计里的“原价”（`standard_cost`）：`usage/stats.go standardCost` 改成 `COALESCE(u.base_cost, <旧公式>)`；旧行 `base_cost` 为 NULL，结果与今天相同，小时汇总表不用重算（升级窗口里旧节点封存的小时例外，§9.1）。导出（`usage/export.go`）的原价一列直接用 `standardCost` 这一条公式，不在 Go 里另写一遍；这一列的英文表头改为 `Base price`（原来写作 `List price`，内容一直是原价）。免费分组（倍率 0）的新记录从此有真实的原价（旧公式下是 0）。对普通用户，“原价”与今天的含义一致：今天分组倍率 1.2 的用户看到“原价 10、实付 12”，以后规则 ×1.2 的用户看到的也是这样。
- `usage/rollup_test.go` 里读 `0092` 触发器列清单的断言改成读最新定义该触发器的迁移。

写 `total_cost` 的五处都同步写 `base_cost`：`settler.go` 结算和预留、`reconcile.go` 对账写实际值，`reconcile.go` 关闭和 `settlements_api.go` 置免费时写 0。

`usage_logs.rate_multiplier` 的含义变成“这次实际生效的倍率”（不含规则倍数），类型不变。

## 4. 包与职责

| 包 | 改动 |
|---|---|
| `core` | 新文件 `segment.go`：`SegmentRef`、`GroupRef`、来源常量、`SegmentRate()`、`ParseRate()`、`PriceQuery`、`PricingTrace`。`ports_billing.go`：`PriceRule` 加 `Source`、`Scale`、`Base` 和 `Multiplier()`；`Pricer.Resolve` 换参数；`UsageRecord` 加 `SegmentID`、`RateSource`。`ports_identity.go`：`APIKeyPrincipal` 加 `SegmentID`、`RateSource`。 |
| `segment`（新） | 用户组的读写：CRUD、移入成员、个人专属、用户组 × 分组设置（两个方向）、供别的包用的访问条件 SQL 片段、`Terms` 读取、用户组引用（`Ref / Masked / ShowUsers`）和两个事务内的助手（`LockNamed`、`DropPersonal`）。只依赖 `core / httpapi / store`；分组的平台（`SegmentGroupSetting.group.platforms`）和价格快照失效由 `app` 注入函数。 |
| `billing` | `price_rules` 的存取与校验、快照与 `Resolve`、`GET/PUT /groups/:id/model-prices`、`POST /pricing/quote`、`GET /me/prices`、`GET /key/prices`。 |
| `group` | `/me/groups` 和 `/groups` 的新字段、`?model=` 筛选、导出 `ProvidedModels` 和 `OfferedModels`、删掉 `/users/:id/groups` 两个接口。 |
| `apikey` | `lookup`、`groupAvailable`、Key 列表的 `group_status` 三处 SQL 改按用户组判断。 |
| `iam` | 用户带 `segment`、按用户组筛选、新建时可选用户组、软删除时清个人专属（提交后通知价格快照）；去掉 `group_ids`；成员列表 `GET /segments/:id/members`（它就是带用户组条件的用户列表，行同 `GET /users`，放在 `iam` 才不成环）。 |
| `usage` | `pendingInputs` 加 `Pricing`；`priceOne` 返回三个金额、`priceComponents` 累加；写 `base_cost`；统计和导出口径。 |
| `gateway` | `Resolve` 的两处调用和 `newRecord`。 |
| `lookup` | `groupOpenTo` 改用 `segment` 的访问条件。 |
| `authz` | 权限 `segment:read`、`segment:manage`；菜单“用户组”。 |

依赖方向：`billing → group, segment`；`apikey / group / iam / lookup → segment`；`segment` 不依赖它们。没有环。

共用的规则各只写一处（完整的表在 CONTRACTS §97.9）：

- 倍率规则：`core.SegmentRate(groupDefault, discount decimal.Decimal, override *decimal.Decimal) (rate decimal.Decimal, source string)`。倍率、折扣、规则倍数的写法校验：`core.ParseRate`。
- 用户组在分组里的条件（能否使用、实际倍率与来源）：`segment.UserTerms / SegmentTerms(ctx, q, id, groupIDs) (map[int64]segment.Terms, error)` 和反方向的 `segment.GroupTerms(ctx, q, groupID, segmentIDs)`，批量读取时用它们；`apikey.lookup` 为了一条 SQL 读完自己连表，但倍率同样只经 `core.SegmentRate`。
- 访问条件：`segment.OpenToUserSQL(groupAlias, userArg string) string`，展开为 `(g.visibility = 'public' OR EXISTS (SELECT 1 FROM segment_groups sg JOIN users su ON su.segment_id = sg.segment_id WHERE sg.group_id = g.id AND sg.granted AND su.id = <arg>))`。`apikey`（3 处）、`group`（`/me/groups`）、`billing`（`/me/prices`）、`lookup`（1 处）共 6 处旧的 `user_groups` 子查询都换成它。
- 用户组的引用与名称屏蔽：`segment.Ref`（读一个用户组的引用）、`segment.Masked`（没有 `user:read` 时个人专属给 `#<user_id>`）、`segment.ShowUsers`（“调用者有没有 `user:read`”的判定）。分组的引用是 `core.GroupRef`。
- 事务内的两个助手：`segment.LockNamed`（锁定并校验命名用户组，`iam` 新建用户时也用）、`segment.DropPersonal`（删个人专属，`iam` 软删除用户时也用）。
- 分组提供的模型：规则写在 `group.Models.Offers(model, priced, policy)`（单个模型的判断）；`Models.Offered` 和 `GET /groups?model=` 复用它，对外的入口是 `group.OfferedModels(ctx, q, groups []core.GroupInfo, priced []string)`，定价表、试算、`/me/prices` 都用它。账号声明的模型由 `group.ProvidedModels(ctx, q, groupIDs []int64) (map[int64]group.Models, error)` 读出，`Models` 带“不限模型的账号数”，`GET /groups/:id/models` 返回它。
- 规则到展示用标价（接口里的 `list_price`）：`billing` 的 `listPrice(base, rule) PriceDef`。无规则返回全局价；自定义价返回规则自己的价格；按倍数的规则返回全局价原样（`mode / config / expression` 都不动）加 `multiplier`。不缩放配置，不改写表达式；定价表、试算、`/me/prices?group_id=` 三处同形。页面先取出要显示的价格格子再乘 `multiplier`，取不出格子的表达式显示“按表达式计费 ×N”；草稿规则的预览也走这条路径。
- 规则到导出用价格：`billing` 的 `exportPrice`，只给 `GET /key/prices` 用。它在 `listPrice` 之后，有 `multiplier` 且不等于 1 时调用同步价格的 `scalePrice` 把倍数折进价格（§5.2）。

## 5. 接口

类型见 `web/src/api/pricing.ts`；行为以后端和 CONTRACTS §97.6 为准。金额和倍率都是十进制字符串（请求里也必须是 JSON 字符串）；列表接口走现有分页约定；创建类接口返回 201；错误走 `core.InvalidFields` / `core.ErrNotFound` / 409 `conflict`。

### 5.1 `segment` 包（新）

| 接口 | 权限 | 说明 |
|---|---|---|
| `GET /segments?q=&kind=named\|personal` | `segment:read` | 默认 `named`；`Segment[]`，含 `member_count`、`granted_groups`、`rate_override_count`、`price_rule_count`；`personal` 带 `user`、`origin`。`q` 匹配名称和说明，调用者有 `user:read` 时个人专属还匹配成员的邮箱和显示名 |
| `GET /segments/:id` | `segment:read` | |
| `POST /segments` `{name, description, discount}` | `segment:manage` | 201。名称必填、≤100、不区分大小写唯一；说明 ≤2000；折扣 > 0、< 1000000、最多 4 位小数 |
| `PATCH /segments/:id` | `segment:manage` | 个人专属只能改 `discount` |
| `DELETE /segments/:id` | `segment:manage` | 默认组 409；个人专属 409（走下面的移回接口），例外是唯一成员已被软删除的个人专属可以删（升级窗口里旧节点软删除用户留下的行，§9）；成员回默认组 |
| `GET /segments/:id/members?q=` | `segment:read` 且 `user:read` | 分页，行同 `GET /users`；在 `iam` 包 |
| `POST /segments/:id/members` `{user_ids}` | `segment:manage` | 只能移入命名用户组；原来是个人专属的，个人专属随之删除；返回 `{moved}` |
| `GET /segments/:id/groups` | `segment:read` | `SegmentGroupSetting[]`，每个分组一行 |
| `PUT /segments/:id/groups` `{items: [{group_id, granted?, rate_override?}]}` | `segment:manage` | 整批一个事务；`rate_override` 区分“没传（不动）/ null（跟随）/ 值”；专属倍率的校验同分组倍率；字段错误的 `items.<i>` 是请求里的原始位置；同一个分组出现多条时按请求顺序应用，后面的生效；返回最新列表 |
| `GET /groups/:id/segments?include=` | `group:read` | 全部命名用户组，后跟在本分组有授权、专属倍率或专属价的个人专属，以及 `include` 点名的个人专属 |
| `PUT /groups/:id/segments?include=` `{items: [{segment_id, granted?, rate_override?}]}` | `group:manage` | 同上，另一个方向 |
| `POST /users/:id/personal-segment` `{copy}` | `segment:manage` | 201，返回 `Segment`；用户不存在 404；已是个人专属 409；`copy` 时复制原用户组的折扣、`segment_groups`、`price_rules` |
| `DELETE /users/:id/personal-segment?to=` | `segment:manage` | 移回命名用户组（缺省默认组），个人专属及其设置删除；204。用户不是个人专属时 404，包括请求处理期间被别的请求移走的情况（不会再移一次） |

倍率和授权每次请求由 `lookup` 直接读库，不经缓存，改完立即生效。只有 `price_rules` 在价格快照里：`billing` 写入后广播 `config:changed`；个人专属“复制设置”和删除、用户组删除会改 `price_rules`，由 `segment` 在**事务提交后**调用注入的 `PriceRulesChanged(ctx)`（`app` 接线时指向 `billing.Service` 的失效方法；提交前调用会让节点把旧规则再缓存一个 TTL）。`iam` 软删除带个人专属的用户时也删了 `price_rules`，同样在事务提交后调用它。删除分组时规则随外键级联删除，这条路径不通知（见 §13 的已知取舍）。

个人专属的名称是成员的邮箱。调用者没有 `user:read` 时，接口里个人专属的 `name` 给 `#<user_id>`、不带 `user`，`GET /segments?q=` 也不匹配成员的邮箱和显示名（`segment` 包一处处理：`ShowUsers` 判定、`Masked` 替换），免得只有 `segment:read`、`group:read` 或 `price:read` 的角色借此列出用户。

并发：删除用户组与并发移入成员靠行锁和外键兜底，冲突回 409；一个用户同时被两个请求设为个人专属，由 `segments_user_uq` 兜底回 409。成员移动、删除用户组等写操作遇到并发冲突（包括数据库判定的死锁）都回 409，可以重试，不会是 500。

### 5.2 `billing` 包

| 接口 | 权限 | 说明 |
|---|---|---|
| `GET /groups/:id/model-prices?segment_id=` | `group:read` | `GroupModelPrices`：分组提供的模型（不并入分组已不提供的模型上的旧规则），每行 `base`、`group_rule`、`segment_rule`（给了 `segment_id` 时）或 `segment_rule_count`、`list_price`、`source`；失效的规则带 `inactive: true`。`list_price` 是 `PriceDef`（`{mode, config, expression, multiplier?}`）：按倍数的规则返回全局价原样加 `multiplier`，自定义价返回规则自己的价格，无规则返回全局价。没有全局价时 `list_price` 为 null、`source` 为 `global`。`segment_id` 不是正整数 400 |
| `PUT /groups/:id/model-prices?segment_id=` `{items: [{segment_id, model, rule}]}` | `group:manage` | `rule: null` 删除；校验见 mock 的 `ruleError`、`rulePatchError`，另加倍数 < 1000000、最多 4 位小数；整批一个事务；返回最新 `GroupModelPrices` |
| `POST /pricing/quote` | `price:read` 且 `group:read`；带 `user_id` 还要 `user:read`，带 `segment_id` 还要 `segment:read`（与控制台入口相同，缺权限 403） | `user_id` 和 `segment_id` 必须恰好给一个，都给或都不给 400（字段 `user_id`）；`usage` 是 `{p, c, cr?, cc?, cc1h?}`；分组不提供该模型 400（字段 `model`）；价格无法用给定用量求值 400 `invalid_argument`（没有 `details.fields`）；无全局价时 `base / list_price` 为 null、`price_source` 为 `global`、费用 "0"。`list_price` 与定价表同形；`price_levels` 里失效的规则带 `inactive: true`。`usage` 每项 0 到 1e12（越界 400，字段 `usage.<k>`）。金额用与真实计费同一条路径算：`Resolve` + `core.SegmentRate` + `Quote`；展示的价格与费用出自同一份价格快照 |
| `GET /me/prices` | `apikey:self:manage` | 不带 `group_id`：全部已启用全局价 + 每行 `groups`（调用者可用且提供该模型的分组），`rate_multiplier: "1"`；带 `group_id`：该分组提供且能计费的模型，每行是规则后的标价，与定价表的 `list_price` 同形（按倍数的规则带 `multiplier`，表达式和配置不动，不追加规则），另带 `source`；响应带实际倍率和 `rate_source`；不可用的分组 404，`group_id` 不是正整数 400 |
| `GET /key/prices` | API Key | 仍是分组模型名单允许的全部已启用模型；价格换成该 Key 所有者在该分组的标价，`rate_multiplier` 是实际倍率。响应形状不变，下游读不到规则，所以这是唯一把倍数折进价格的地方（`exportPrice`）：按次、按 token、视频模板的价格缩放配置并重新生成表达式，其余的表达式追加常数规则 `\|\|\| true ? N : 1`；倍数为 1 不追加；追加后重新编译校验（规则条数必须多 1），不通过的模型跳过并记日志。用的是同步价格的 `scalePrice`，“从上游同步并应用倍率”的结果不因此改变 |

`POST /prices/preview` 的 `group_id` 保持原意（分组默认倍率），不改。

### 5.3 现有接口的变化

| 接口 | 变化 |
|---|---|
| `GET /groups` | 每行加 `access: {segments: SegmentRef[], personal_count}`（私有分组被授权的命名用户组和个人专属数；公开分组恒为 `{segments: [], personal_count: 0}`）、`price_rule_count`、`segment_rule_count`（只算分组现在提供的模型上的规则，与定价表一致）、`model_count`；新筛选 `?model=`（只留提供该模型的分组） |
| `GET /me/groups` | 可见性按调用者的用户组；每行只比原来多 `effective_rate`、`rate_source`、`exclusive`（私有分组）。`rate_multiplier` 仍是分组默认倍率。读不到调用者在某个分组里的条件时（两次查询之间分组被删等）略去这个分组 |
| `GET /users`、`GET /users/:id` | 加 `segment: SegmentRef`（个人专属的 `name` 是用户邮箱）；去掉 `group_ids`；新筛选 `segment=<id>` 或 `segment=personal`。登录、刷新返回的 `user` 不带 `segment` |
| `POST /users` | 可带 `segment_id`（必须是命名用户组；需要 `segment:manage`，没带就是默认组） |
| `GET/PUT /users/:id/groups` | 删除 |
| `GET /usage/:id` 等详情 | `billing_detail` 里多 `list_cost`、`inputs.pricing`（见 §3） |
| `GET /lookup/groups?mine=`、`/lookup/models?mine=` | 可见性按用户组；`models?mine=true` 是各分组 `/me/prices?group_id=` 并集的超集 |

## 6. 权限与菜单

- `authz/catalog.go`：新模块 `segment`（“用户组”），`segment:read`、`segment:manage`。`admin` 自动拥有；`operator` 拥有（它只排除 `settings / sticky / plugin / publisher` 模块）；`user` 没有。
- `authz/menus.go`：`users` 段加 `{"segments", "User groups" / "用户组", "publisher", "/segments", ["segment:read"]}`，排在“用户”之后、“角色与权限”之前。
- 分组侧的授权、倍率、模型定价沿用 `group:read / group:manage`；价格试算接口和控制台的试算入口都要 `price:read` 和 `group:read`（按用户试算还要 `user:read`，按用户组试算还要 `segment:read`）；成员列表要 `segment:read` 和 `user:read`。

## 7. 前端接入

页面已做完，这次只动：

1. 使用记录详情：倍率旁显示来源（专属倍率 / 分组默认 × 全场折扣 / 分组默认），命中规则时显示标价来源、规则倍数或自定义价、“标价费用”。读 `billing_detail.inputs.pricing`、`list_cost`。
2. 清理：`User.group_ids`、mock 里的 `PUT /users/:id/groups`。
3. UI SDK：`STable` 的 `expanded / toggleExpand`、`expandToggle`、`expandSingle` 计入 **UI 3.13**（`HOST_UI_VERSION`、README 小节标题、`ui-contract.spec.ts`、vite-preset、服务端声明处一起改）。UI 3.13.0 对应核心 >= 0.1.281（3.12 已由表单实体选择器随核心 0.1.280 占用）。
4. 与后端对齐的四处小改：实际倍率按 4 位小数（mock `rateOf`、`pricingFormat.inheritedRate` 现在是 6 位）；专属倍率和规则倍数加 < 1000000 的校验；mock 里自定义价在没有全局价时 `list_price` 应为 null；失效规则（`inactive`）在模型定价表里标出“计费方式已变，规则未生效”。
5. 分组编辑弹窗的“授权与倍率”在新建分组时调 `GET /segments`：没有 `segment:read` 的角色不显示这个标签页。
6. 以真实后端联调。联调阶段发现的契约出入先按 mock 修后端；实现后的审查改为以后端为准，mock 和页面跟随（§13）。
7. `PriceDef` 类型加 `multiplier?: string`；一个函数负责把 `PriceDef` 变成要显示的价格格子（先取格子，再乘 `multiplier`），草稿规则的预览 `applyRule(base, 草稿)` 对按倍数的规则返回 `{...base, multiplier}`，与保存后是同一条显示路径。任何地方都不再改写表达式文本。

## 8. 文档

- `docs/CONTRACTS.md`：新 §97“分组定价”（本文 §1–§6 的契约部分）；§4 权限表加 `segment`；§5 REST 表增删；§12 改写（价格解析带分组和用户组；“表达式结果 × 倍率”中的倍率是实际倍率）；§15.1 分组可用性按用户组；§17 `/key/prices`；§66 `standard_cost` 口径；§73 原始费用说明 `billing_detail.cost` 是全局价口径；§79 `/me/prices`；§90 菜单；§23.1 `HOST_UI_VERSION`；§96 查找接口 `mine` 的范围。
- `docs/ARCHITECTURE.md`：§7.1 分组（可见性与授权）、§7.3 定价范围（不再是“只按模型全局设置”）、§7.4 计费流程图加标价解析。
- `web/packages/ui/README.md`：UI 3.13 小节。

## 9. 兼容与回滚

契约在 CONTRACTS §97.8，那里有四段 SQL：A 迁移前统计、B 回滚前同步撤销的授权、C 小时汇总标脏重算、D 遗留个人专属检查。下面是要点和发布检查清单。

### 9.1 升级

- 迁移只新增对象。升级过程中旧节点照常读 `user_groups` 和 `groups.rate_multiplier`，行为与升级前一致；新节点读新表。
- 升级瞬间每个用户的可用分组和倍率与升级前相同：公开分组照旧；私有分组的授权逐条迁成个人专属；没有任何专属倍率、折扣和规则，实际倍率 = 分组默认倍率，标价 = 全局价。
- **所有节点升级完之前，不要改授权，也不要建规则、设折扣或专属倍率。** 旧节点不认识它们：它按分组默认倍率和全局价给新请求计费，重试时也不认 `inputs.pricing` 里的规则倍数。
- 使用记录：新记录多 `base_cost` 和可选的 `billing_detail` 字段，旧二进制忽略它们。
- 新旧节点并存的窗口里有四处不对称，都不丢数据：
  1. helper history 的用量信封（`core/helper_history_usage.go`）按“不认识的字段一律拒收”解码。`UsageRecord` 新增的 `RateSource`、`SegmentID` 每次都会写进信封，新节点写的信封旧二进制一律拒收：每分钟推迟一次，行留在 `provider_helper_usage_outbox`。滚动升级时只是延迟，新节点会把它们入账。
  2. 旧节点对账新节点预留的行时只改 `total_cost`，`base_cost` 停在预留时的估算值。
  3. 旧节点刷新小时汇总按旧公式算 `standard_cost`，不读 `base_cost`：倍率为 0 的新记录计 0。窗口内被它封存或重算的小时，升级完成后要标脏重算。做法（`usage/rollup.go` 的标脏机制）：往 `usage_hourly_dirty` 插入这些小时（UTC 整点），统计立即改为实时读这些小时的 `usage_logs`，刷新任务 `core.usage.rollup` 随后按现在的公式重算并清掉标记。范围从第一个新节点启动的那个小时到最后一个旧节点停止的那个小时，SQL 是 CONTRACTS §97.8 的 C。
  4. 旧节点软删除用户时不删他的个人专属，留下唯一成员已软删除的个人专属。它不影响鉴权和计费；升级完成后查出来（CONTRACTS §97.8 的 D），经 `DELETE /segments/:id` 删除。

### 9.2 回滚

回滚到旧二进制不需要改库，但要知道下面几件事：

1. 新功能整体失效：旧二进制读 `user_groups`（冻结在迁移时刻）。升级后新做的授权它看不到，升级后撤销的授权会复活；折扣、专属倍率、规则对新请求不生效。回滚前把这期间撤销过的授权在 `user_groups` 里同步删掉（CONTRACTS §97.8 的 B）。
2. 回滚时还没结算的记录（`pending / failed / reserved`）由旧二进制结算：倍率和自定义价是对的（倍率在列里、自定义价的表达式在历史表里），按倍数的规则会被忽略，按全局价 × 倍率结算。回滚前先让待结算清空，或先删掉按倍数的规则。旧二进制读回冻结价格时没有 §3.2 那个“不是自定义价才回退到全局价”的条件：自定义价的行在历史表里缺行时，它仍会按全局价结算。正常路径下不会缺行，保存规则与写历史表在同一个事务里。
3. 旧二进制退款、关闭记录或对账时只改 `total_cost`、不改 `base_cost`，这些记录在统计里的“原价”会停在改之前的值。
4. **回滚前先让 `provider_helper_usage_outbox` 清空。** 新节点留在里面的信封旧二进制一直拒收，这部分用量要到再次升级才入账。
5. 回滚期间旧二进制封存的小时汇总按旧公式算，再次升级完成后一并标脏重算（范围取到最后一个旧二进制节点停止）。

### 9.3 发布检查清单

发版前：

1. 在生产备份库上跑迁移前统计（CONTRACTS §97.8 的 A），数出会生成多少个个人专属、有多少种授权集合。旧授权一人迁成一个个人专属；数量很大（上百）时，改为按“授权集合相同”归并成命名用户组再发布。
2. 确认没有长事务占着 `users` 和 `usage_logs`。迁移 0095、0096 带 10 秒的 `lock_timeout`：拿不到锁会快速失败，不会排在长事务后面把读这两张表的请求一起堵住；失败后处理掉长事务再重跑，两个迁移都幂等。可以这样看有没有开了很久的事务：

   ```sql
   SELECT pid, now() - xact_start AS age, state, left(query, 80) AS query
   FROM pg_stat_activity
   WHERE datname = current_database() AND xact_start < now() - interval '1 minute'
   ORDER BY xact_start;
   ```

3. 记下第一个新节点启动的时间（升级后标脏重算要用）。

升级过程中：

4. **所有节点升级完之前不要改授权，不要建规则、设折扣或专属倍率。**

全部节点升级完之后：

5. 记下最后一个旧节点停止的时间，对这段时间里的小时做标脏重算（CONTRACTS §97.8 的 C）。
6. 检查并清理遗留的个人专属（CONTRACTS §97.8 的 D；窗口内没有旧节点软删除过用户时结果为空）。
7. 核对升级前后每个 Key 的可用性和倍率逐个相等、统计与账号用量不变，再演练新功能（§10）。

任何时候：

8. 默认用户组在数据库层只靠 `users.segment_id` 的外键保护：成员全部移走之后，手工 `DELETE` 是删得掉的（应用层才拦），之后新用户插不进去。手工改库不要删它。

回滚前：

9. 让待结算清空（或先删掉按倍数的规则）；让 `provider_helper_usage_outbox` 清空；把升级后撤销的授权从 `user_groups` 里删掉（CONTRACTS §97.8 的 B）。

### 9.4 生产副本验证（第一阶段）的结论

在生产库的一份副本上（PostgreSQL 17，约 4.6 GB，`usage_logs` 约 31 万行；副本上没有并发）执行了两个迁移并做了前后对比：

- 耗时：0095 首次执行 171 ms，其中持有 `users` 排他锁约 31 ms；0096 用 10.6 ms，持有 `usage_logs` 排他锁约 11 ms，不重写表。线上还要加上等锁的时间，上限是 `lock_timeout`。
- 前后对比差异为 0：未删除 Key 的可用性和倍率、用户 × 分组的访问、按天 × 分组的统计（59 格）、账号原始费用（120 个账号）；小时汇总表不变。迁移重跑不改变任何东西，约束都生效。
- 线上当前没有逐用户授权（`user_groups` 是空的，分组都是公开的），所以升级不会生成个人专属，迁移后只有“默认”一个用户组。
- 另用一份合成数据副本（5 个分组、60 个用户、101 条旧授权、430 多把 Key）验证回填：生成 35 个个人专属，等于未删除且有私有分组授权的用户数，授权集合 5 种；未删除用户的 381 把 Key 可用性和倍率差异 0；10 把 Key 从可用变成不可用，全部属于已软删除的用户，鉴权本来就拒绝他们。

新功能的演练（授权、倍率、规则生效后的扣费）在后续阶段做。

## 10. 测试

- 单元：`core.SegmentRate` 表驱动（含 4 位小数和上限）；规则解析（三层 × 两种规则 × 无全局价 × 失效规则 × 倍数 0 和 1）；`priceOne` 的三个金额（倍数、自定义价、无规则；主项 + 引用模型命中另一条规则 + replacement 的累加）；规则校验；展示用标价的三种形状（无规则、自定义价、按倍数带 `multiplier`）；导出用价格的缩放与追加（含档位价、倍数 0 和 1、追加后校验不通过的表达式）。
- 数据库测试（`TEST_DATABASE_URL`）：迁移回填（默认组、旧授权 → 个人专属、公开分组的授权不迁、软删除用户）；`segment` 包各接口；`lookup` 的可用性与倍率；重试路径用冻结的规则（请求后改规则不影响）；`standard_cost` 新旧行；账号原始费用不随规则变化。
- 现有测试和夹具里用旧授权的地方同步改成用户组授权：`e2e/resources.go` 的 `SetUserGroups` 及其使用者、`gateway/internal/control/realcore_*_linux_test.go`、直接插 `user_groups` 的 `apikey / lookup / billing` 测试。
- 网关端到端（`e2e/ac08_billing_test.go` 的做法）：同一请求在三种用户组设置下的扣费。
- 前端：现有 1806 个用例 + 使用记录详情的新展示。
- 生产备份库：核对升级前后每个 Key 的可用性和倍率逐个相等、统计与账号用量不变，再演练新功能。第一阶段的结论见 §9.4，发布检查清单见 §9.3。

## 11. 实现分块

每块自带测试；写完自审（抽象、复用、解耦、低嵌套、简洁、低侵入），合并前再独立审查一遍。

| 内容 | 主要文件 | 依赖 |
|---|---|---|
| 迁移 0095、0096 与回填；迁移测试 | `migrations/0095_segments.sql`、`0096_usage_base_cost.sql`、`migrations/*_db_test.go` | 无 |
| `core` 类型与规则函数；`Pricer` 接口换参（先让全仓编译通过，调用处传只有 `Model` 的查询） | `core/segment.go`、`core/ports_billing.go`、`core/ports_identity.go`、各调用处 | 无 |
| `segment` 包：CRUD、成员、个人专属、两个方向的设置、`OpenToUserSQL`、`Terms` | `segment/*.go`、`app/phases.go` | 迁移、`core` |
| `billing`：`price_rules` 存取校验、快照与 `Resolve`、`model-prices`、`quote`、`/me/prices`、`/key/prices` | `billing/rules.go`、`pricer.go`、`group_prices.go`、`quote_api.go`、`my_prices.go`、`list_price.go`、`sync.go`、`usage_quote.go` | 迁移、`core`；`group` 的模型范围和 `segment` 的 `Terms` |
| 鉴权与网关：`apikey` 三处 SQL、`gateway` 两处 `Resolve` 和 `newRecord`、`lookup` | `apikey/apikey.go`、`gateway/pipeline.go`、`gateway/model_references.go`、`lookup/entities.go` | 迁移、`core`；`OpenToUserSQL` |
| `group`：列表新字段、`?model=`、`/me/groups`、分组提供的模型、删旧接口；`iam`：`segment` 字段、筛选、新建、软删除、成员列表 | `group/group.go`、`group/models.go`、`group/summary.go`、`iam/users.go`、`iam/http.go` | 迁移、`core`、`segment` |
| `usage`：冻结、三个金额、`base_cost`、统计与导出 | `usage/settler.go`、`additional.go`、`reconcile.go`、`settlements_api.go`、`stats.go`、`export.go`、`api.go` | 迁移、`core` |
| `authz`：权限与菜单；控制台保留路径 | `authz/catalog.go`、`authz/menus.go`、`sdk/manifest/routes.go` | 无 |
| 前端接入（§7） | `web/src/views/usage/**`、`web/src/api/types.ts`、`web/src/api/pricing.ts`、`web/mock/**`、`web/packages/ui/**` | `usage` 的字段名 |
| CONTRACTS、ARCHITECTURE、README | `docs/*.md` | 全部定稿后 |
| 网关端到端与联调 | `e2e/*`、mock 对照 | 后端各块 |

顺序：迁移、`core`、`authz` 先行（`core` 的接口一次定准）；后端各包和前端并行；随后端到端、文档；最后独立审查、修复、生产备份库验证。

## 12. 独立审核后的修改

设计初稿由独立审核对照代码核查（5 处必须改、13 处应该改）。采纳的结论：

| 审核结论 | 修改 |
|---|---|
| 把倍数规则拼进表达式文本不可靠：表达式带含引号的注释时追加的规则会被当成字符串，静默按全局价扣费；明细里还会多一条假的加价规则 | 不改写表达式。规则倍数作为数字放在 `PriceRule.Scale`，经 `Multiplier()` 进入 `Quote`（§3.1） |
| ×1 的规则有意义（用户组不吃分组内定价），不能拒绝 | 接受 0 和 1（§1） |
| 分组倍率 × 折扣可到 8 位小数，超出 `usage_logs.rate_multiplier numeric(10,4)`：首次结算和重试用的倍率不同，超范围时写入失败 | 实际倍率取 4 位小数并封顶，mock 和前端同步（§1、§7） |
| 冻结的规则在旧节点和回滚后会丢 | 自定义价的表达式在保存规则时写历史表，任何版本都取得到；倍数规则的限制写进 §9 |
| 附加、替换分项没有原价，汇总口径混在一起 | `priceOne` 返回三个金额，`priceComponents` 都累加（§3.3） |
| 迁移在一个事务里先后拿 `users` 和 `usage_logs` 的排他锁 | 拆成 0095、0096（§2） |
| 自定义价与全局价的计费方式后来不一致时没有定义 | 规则失效并标出（§1） |
| 删旧授权接口会打断端到端夹具 | 同步改夹具（§10） |
| 展示用标价对分档模型会退化成“按规则计费” | 当时的做法是单独的配置缩放函数；实现后改为全局价原样加 `multiplier`，由页面乘上去（§13） |
| `segment` 包的两个接口会让依赖成环 | 成员列表放 `iam`，平台由 `app` 注入（§4） |
| 只有 `segment:read / group:read` 的角色能借个人专属的名称列出用户邮箱 | 没有 `user:read` 时不给邮箱（§5.1） |
| 折扣为 0 的校验与页面不一致 | 统一为 > 0（§1） |
| 回滚说明不完整 | §9（实现后又补了三点，§13） |

没有采纳的：`default_segment_id()` 函数体写死 `public.` 模式（部署可能不在 `public` 模式）；给新增写接口加审计日志（同类的分组、价格写接口都没有，保持一致）。

## 13. 实现后审查的调整与已知取舍

实现完成后，对后端各包、前端和契约做了一轮独立审查。下面是因此改变的口径，前文已按它们改过。

**标价的展示口径。** 审查发现同一对（全局价，倍数）有三种表达：定价表和试算里手写表达式原样返回（页面把没缩放的价格当标价显示），`/me/prices` 在表达式后追加常数规则，前端和 mock 又各自缩放一遍。统一为：

- `PriceDef = {mode, config, expression, multiplier?}`。`multiplier` 只在标价来自按倍数的规则时出现（0 和 1 也出现），含义是“标价 = 这个价格 × `multiplier`”。全局价和自定义价不带。
- `listPrice` 不再缩放配置：无规则返回全局价，自定义价返回规则自己的价格，按倍数返回全局价原样加 `multiplier`。定价表、试算、`/me/prices?group_id=` 三处同形。
- `GET /key/prices` 是唯一把倍数折进价格的地方，用同步价格的 `scalePrice`，行为与引入本功能之前“从上游同步并应用倍率”一致；追加常数规则后要重新编译校验，倍数为 1 不追加。
- 前端一个函数负责把 `PriceDef` 变成显示的价格格子；mock 与后端同形，不再用正则改写表达式。

**以后端为准的其它口径。**

- 创建类接口返回 201。
- 试算：`user_id`、`segment_id` 都给或都不给 400；`usage` 可带 `cc1h`；价格无法求值 400；没有全局价时 `price_source` 为 `global`。
- 全局价的来源：使用记录里省略，给人看的接口输出 `global`。
- 失效规则带 `inactive: true`（定价表的两条规则、试算的 `price_levels`）。
- 没有 `user:read` 的调用者：`GET /segments?q=` 不匹配成员邮箱和显示名；个人专属名称是 `#<user_id>`，不带 `user`。
- 登录、刷新返回的 `user` 不带 `segment`；`/me/groups` 只比原来多三个字段。
- `GET /groups` 的 `access` 只统计私有分组。
- `lookup models?mine=true` 是各分组 `/me/prices?group_id=` 并集的超集。
- 导出的原价一列英文表头改为 `Base price`。

**金额路径。** 读回冻结价格时，自定义价的行不再回退到全局价现在的表达式（§3.2）。

**已知取舍（接受的行为）。**

- 价格规则快照在删除分组之后可能短暂留着已删分组的规则：规则随外键级联删除，这条路径不通知快照，最多留到 TTL 过期。广播没送到某个节点、滚动窗口里旧节点软删除用户时也是这样。id 不复用，已删的分组和用户组不会再有请求解析到，这些规则不影响计费，所以不加通知。（`segment` 删用户组、删个人专属、复制设置，以及 `iam` 软删除带个人专属的用户，都在事务提交后通知。）
- 旧节点或回滚后的旧二进制改过 `total_cost` 的记录，`base_cost` 停在旧值（§9）；不回补。
- 升级窗口和回滚期间由旧二进制封存的小时汇总，要人工标脏重算（§9.3），没有做成自动。
- “默认用户组不能删”只在应用层拦，数据库层没有单独的约束（§9.3）。
- 没有采纳的两条审核意见见 §12 末尾。
