# 变更记录

记录 next 平台（`next/`）发布到线上的版本，从 2026-11-04 开始，更早的版本见部署记录。日期是北京时间。部署细节、核验和回滚方式见 [OVH 部署记录](next/deploy/gateway/ovh/README.md) 和 [clicodeplus 部署记录](next/deploy/gateway/clicodeplus/README.md)。

每次发布在最前面加一节，写清版本、提交和用户能看到的变化。版本有三条线：核心、网关（外壳）、插件（如 moderation）。核心与网关由 tag `core-v<版本>`、`gateway-v<版本>` 触发 GitHub Actions 发布，Release 说明取标题里写着“核心 <版本>”或“网关 <版本>”的那个 `###` 小节，所以打 tag 前先写好这一节（格式见 `next/deploy/gateway/github/README.md`）。

## 未发布

开发完成、尚未上线的改动。发布时改成发布日期的一节，补上提交、实际版本号和发布过程。

### 侧栏“账号/代理”目录（核心 + 控制台；无迁移，可滚动）

- 管理员侧栏把账号、分组、代理合成一个可展开的二级菜单“账号/代理”，当前页在其中时自动展开；分组从系统设置视图移出，系统设置视图的“分组与定价”组改名“定价”，只剩模型价格。只有 `group:read` 的调用者不再看到“系统设置”，改在“账号/代理”里看到分组。动态权重等声明 `section:"gateway"` 的插件菜单也在这个目录里。地址都不变（CONTRACTS §132）。

### 分组编辑去掉“账号”“用户组”标签页（控制台；无迁移，接口不变）

- 分组编辑窗口只剩“基本信息”“模型名单”“价格”“高级”四个标签页。账号属于哪些分组改在账号编辑里设；各用户组在分组上的开通和专属倍率改在用户组编辑的“分组倍率”里设，两边原来都能设，现在各只在一处。
- “价格”页的实付价按分组倍率显示，不再能选某个用户组看它的实付价。新建分组还没有账号，价格表起初是空的，可以先“添加模型”定价；账号加入分组后它们提供的模型会自动列出。
- 没有分组管理权限时打开的只读价格窗口只显示价格表，标题和按钮改为“价格”“查看价格”。（CONTRACTS §92.5）

## 2026-11-22

### 核心 0.1.307：计费方式只留可视化与表达式、阶梯条件、模型价格只属于分组（`483346b48`；迁移 0122、0123，单向；网关仍 0.1.139）

- **构建**：照 0.1.306 的做法在 ovh 上从 touwaeriol/sup2api develop `483346b48` 构建（`stage/own-0.1.307/`，内置包用 `sup2api-plugins-2026` 重签）。清单 sha256 `1f0943da2a4d90f6a55ce913b670979982588b23596f46dcceeefcbce7c782a0`（`release_id` `core-v0.1.307`，amd64 bundle `f6244058…`、arm64 `7ea19c55…`），strategy maintenance，schema `0b8698cf…748d` → `6c3ac553…cf2e`。内置插件版本与线上相同（anthropic 0.4.0、codex 0.2.2、dynweight 0.6.0、gemini 0.4.0、mailer 0.1.5、moderation 0.13.0、newapi 0.1.1、openai 0.5.0、sub2api 0.1.1、typesafe 0.1.1），插件包与 0.1.306 逐字节相同，只有 `sub2api`、`sub2api-plugin` 两个二进制变了；`next/plugins/released.json` 不用改。
- **发布前**：代码按“代码块 ≤30 行、文件 ≤500 行”做了规范修复并经两轮审查；后端全量 `go test -race`（带 PostgreSQL，64 个包）与前端 vue-tsc、eslint、vitest（2729 个用例）通过。对线上库只读：新核心 `sub2api admin check-prices` 通过；升级后会开始生效的自定义价（§131.4）0 行；0123 将删除的用户组专属价（§97.10）0 行；`price_rules` 本来就是空的。
- **ovh 集群**（北京时间 2026-11-22 19:39–19:40，用户原话“更新ovh”）：import 后预检 primary-first-v1、无阻断（expected_revision 577），计划 `cf82f8992186332f6459f080788867cf` 19:39:21→19:40:12，迁移 0122、0123 于 19:39:43 应用；ovh 3130 与 api.prophey.ai `/readyz` 503 约 9 秒（19:39:39→19:39:48），没有连接错误。核验：两节点核心 0.1.307、网关 0.1.139，7 个启用插件 active、ready、restarts 0；`price_rules.segment_id` 已删；迁移后 `check-prices` 通过（含每条可视化价格能否生成所存表达式）；`model_prices` 由 expression 25、per_token 183 变为 expression 25、visual 183；经 api.prophey.ai 的非流式、流式 `/v1/messages` 各一个（临时密钥，已删）200、`billed`、费用与价格一致；发布后 15 分钟 61 个请求，7 个 529 都是分组 9 的 `claude-haiku-4-5-20251001` 没有可用账号（发布前全天如此，与发布无关）；新核心日志无 ERROR（旧核心关停时 ccp 有 1 条 `context canceled` ERROR）。没有关 new-api 渠道（392 由管理员关着，未动），没有另做数据库备份。
- **ec2pool**（单节点，北京时间 2026-11-22 20:21，用户原话“ec2pool更新也”）：在那台机器上从 `483346b48` 构建（159 秒，核心二进制与 ovh 逐字节相同，内置包用 `sup2api-ec2pool-2026` 签名，清单 `4bf06a6b09bb10fcddb874c9cec8e3e081b4cc109c37fe0f51e154e8f315ba62`）；升级前只读检查同样全部通过（`check-prices` 通过，§131.4、§97.10 都是 0 行，`price_rules` 为空）。预检 primary-first-v1、无阻断，计划 `e76fb0afa2c978ca58a0a44b4d192973` 20:21:19.6→20:21:26.6，迁移于 20:21:25.6 应用，入口 503 约 4 秒；切换那 20 秒里有 8 个 499、1 个流式请求被切断。核验：核心 0.1.307、网关 0.1.139，`segment_id` 已删，5 个启用插件正常（codex 0.2.2），迁移后 `check-prices` 通过，`model_prices` 变为 visual 105、expression 9（总数不变）；`chatGPT` 分组 chat、responses 的非流式与流式都 200、`billed`，粘性会话落同一账号并命中缓存；升级后 15 分钟 791 个请求成功率 98.0%（升级前 15 分钟 736 个、90.1%），5xx 只有上游 502；日志无 ERROR。
- 回滚：两个迁移单向，0123 删了 `segment_id` 列，回退要连同数据库回到升级前（依靠库的自动备份）。两个环境都升级完后，单向发布手册已删除（`git show 3e61df8e8:next/deploy/rollback/0.1.307-billing-release.md`）。

本次发布包含的改动：

#### 模型价格只属于分组，用户组只设倍率（核心；迁移 0123，单向，不能滚动）

- 用户组（含个人专属）只能定制倍率：全场折扣、各分组的授权和专属倍率。分组可以定制倍率，也可以给模型定价（按倍数，或可视化 / 表达式的自定义价）。实付 = 模型价格（分组内定价 → 全局价）× 倍率（专属倍率 → 分组默认倍率 × 全场折扣 → 分组默认倍率）。见 CONTRACTS §97.10。
- **迁移 0123 删除全部用户组专属价**（`price_rules` 里 `segment_id` 非空的行），删掉 `segment_id` 列，规则按（分组, 模型）唯一。个人专属复制设置不再复制规则。命中过专属价的历史使用记录照常显示、重试和对账（价格已随记录冻结）。
- 接口：定价表、试算、`/me/prices`、`/key/prices` 里不再有用户组这一层（`segment_rule`、`segment_rule_count`、`price_levels.segment`、`?segment_id=`、来源 `segment`）；用户组和分组设置不再带 `price_rule_count`，`GET /groups` 不再带 `segment_rule_count`；用户组的 `rate_override_count` 换成 `rate_overrides`（`[{id, name, rate}]`，各分组的专属倍率）。`PUT /groups/:id/model-prices` 带 `?segment_id=` 或项里有未知字段（如旧控制台的 `segment_id`）回 400，不会被当成分组内定价写入。
- 分组定价可以先给分组还不提供的模型定价（模型要有启用的全局价）：定价表列出分组提供的模型和有规则的模型，每行带 `offered`；`GET /groups` 的规则条数不再只算提供的模型。新增 `GET /pricing/catalog`（`group:read`），给定价页列全部启用的全局价。`GET /segments?kind=all` 一次列出命名用户组和个人专属。
- 定价表、`/me/prices?group_id=`、`/key/prices` 改读本节点计费用的价格快照，与实际扣费同源；价格试算对停用的分组显示“不可用”（`access_source: disabled`）；价格快照的并发载入合并成一次读库；网关鉴权读出的倍率直接是数值，不再有解析失败时按 ×1 计费的路径。
- 发布前对线上库只读跑 §97.10 的查询，列出将被删除的专属价报给所有者。迁移后旧二进制读不了 `price_rules`，按停机维护一次升级全部节点；回退要连同数据库回到升级前的备份。发布步骤（含 0122 的检查）写在当时的单向发布手册里（已删除，见上方发布记录）。
- 控制台：分组编辑把分组倍率和模型价格放进“价格”页，模型价格按“原价 / 原价 × N / 自定义价格”设，可从全部原价里添加模型；用户组列表同时列出命名用户组和专享用户（原“个人专属”，界面改称“专享”，与用户组仍二选一），用户组只设折扣、可用的私有分组和各分组倍率；分组编辑的“用户组”页只列开通了或单独设了倍率的用户组和专享用户，用“添加用户组”“添加用户”加入、“移除”清掉；新建分组、用户组时各页都能先填，点“新建”按顺序保存。界面用词统一为原价、分组价、模型价格、分组倍率、倍率 / 实付倍率、折扣、可用。

#### 按 token 的阶梯条件照 new-api（核心；迁移 0122，单向）

- 计费方式只剩**可视化**和**表达式**两种（所有者 2026-11-22 定）：原“按 token”改名可视化（`mode: visual`），原“按次”删掉，按次价就是可视化里一个按次计的档位（可以照样带加价规则）。写旧方式名 `per_token`、`per_request` 或旧形状的配置直接报错，不做兼容。见 CONTRACTS §131。
- 档位不再只按输入长度上限（`max_len`）分：每档一个条件（`when`），可比较输入长度 `len`、输入 token `p`、输出 token `c` 和小时、分钟、星期、月、日（各带时区，缺省 UTC），比较符 `<`、`<=`、`>`、`>=`、`==`、`!=`，可以用“且 / 或 / 非”嵌套（每档最多 5 层、20 个比较，最多 20 档）。按配置里的顺序第一个条件成立的档位生效，最后一档兜底、不带条件。生成的表达式写成 `c1 ? 档1 : (c2 ? 档2 : 档3)`，原来按长度分档的价格迁移后表达式逐字节不变。
- 迁移 0122 只改配置：按 token 的价格改成可视化，档位按原来的上限从小到大排、上限换成 `len <= 上限` 的条件；按次价改成单个按次档、保留原规则；分组的自定义价同样改（用户组的随后由 0123 删除），`price_rules.mode` 只允许 `visual`、`expression`。表达式和哈希都不变（测试逐行核对）；重跑什么也不写。
- 分组的自定义基础价格不再受全局价限制（所有者 2026-11-22 定“自定义基础价格、自定义倍率是两个概念”）：和全局价一样写，可视化任意档位、条件、加价规则，或直接写表达式，校验与全局价相同；全局价之后怎么改，自定义价都照样生效，原来的“规则未生效”（`inactive`）删掉。命中自定义价的请求按自定义价判断是否按次收费、是否只走视频端点；结算时算原价要用的全局价入参也一起捕获；原价只用于展示，全局价算不出时原价记成标价费用、照常扣费，不再让整条记录进 `failed`。控制台的分组自定义价用与模型价格页相同的编辑器（可视化或表达式）。见 CONTRACTS §97、§131.4。0.1.306 上原来失效的自定义价升级后会开始生效：发布前用 §131.4 的只读查询列出，有结果先报给所有者决定去留。原价算不出而记成标价费用的记录带 `billing_detail.base_fallback`；带替换项或附加项的记录，只要任一分项的原价回退，顶层也标 `base_fallback`，分项明细里各自也带这个标记。
- 没有用量也要收费的“按次价”现在指“可视化、表达式不读任何 token 数”的价格（`PerRequest`）：原按次价都是；原按 token 里只有一个按次档的价格现在也算，上游完成了但没报用量时照样收这一次的钱。插件清单的 `billingTypes` 词表不变，可视化价格按是否读 token 数对照 `per_request` 或 `per_token`。
- LiteLLM、models.dev 同步来的 200K 两档写成 `standard`（`len <= 200000`）+ `long_context`；从还没升级的上游 sup2api 导入时，它的按 token、按次价格和 `max_len` 分档在导入时转成新形状。发布前对线上库只读跑 `sub2api admin check-prices`：未跑 0122 的库按迁移的转换检查配置能否生成所存表达式。
- 回退要连同数据库回到升级前的备份。e2e 的价格夹具（`RunPriceConfig`）和 AC08 已改成可视化。

### 核心 0.1.306 + codex 0.2.2：计费方式统一、价格编辑页、用量不参与调度、插件 logo（`cc6e51699`；迁移 0121，单向；网关仍 0.1.139）

- **构建**：没有打 tag、没有走 GitHub Actions，照 0.1.305 的做法在 ovh 上从 touwaeriol/sup2api develop `cc6e51699` 构建（`build-ui.sh`、`build-go.sh`、`package-core.sh`，内置包用 `sup2api-plugins-2026` 重签，`stage/own-0.1.306/`，148 秒）。清单 sha256 `e3015a17c130872773b2587ae4c1b55b30c87fe0f4308a79f1bd133348b356a7`（`release_id` `core-v0.1.306`，amd64 bundle `8bddcc9d…1977`、arm64 `a2fd5fd5…9d1f`），strategy maintenance，schema `6c0c081d…ae59` → `0b8698cf…748d`。内置包：codex 0.2.1→0.2.2，newapi、sub2api、typesafe 0.1.0→0.1.1；anthropic 0.4.0、dynweight 0.6.0、gemini 0.4.0、mailer 0.1.5、moderation 0.13.0、openai 0.5.0 版本不变，但二进制随 SDK 删掉 `AdditionalUsageRule.RequiredBy` 而不同，核心保留库里原来那份（每个一条 `version already stored with other content, keeping the stored one` WARN），这六个插件仍跑线上的包，行为不受影响。`next/plugins/released.json` 已按本次改写。
- **发布前**：后端全量 `go test -race`（带 PostgreSQL）与前端 eslint、vue-tsc、vitest（2637 个用例）通过；新核心对线上库只读运行 `sub2api admin check-prices` 全部通过；线上没有含 `|||` 的表达式，没有要删的自定义价，`pending`、`reserved` 为 0。
- **ovh 集群**（北京时间 2026-11-22 03:01–03:02，用户原话“同意发布 0.1.306 到 ovh 集群”）：03:01:14 import，预检 primary-first-v1、无阻断（expected_revision 575），计划 `7c8596e80938c138dcd22444f0f5ee7a` 03:01:34→03:02:28，迁移 0121 于 03:01:55 应用；ovh 3130 与 api.prophey.ai `/readyz` 503 约 9 秒（03:01:51→03:02:00）。核验：两节点核心 0.1.306、网关 0.1.139，7 个启用插件 active、ready、restarts 0，codex 内置同步到 0.2.2；迁移后 `check-prices` 通过（含配置能否生成所存表达式）；`model_prices` expression 32→25、per_token 176→183（7 条可视化分档价格转成按 token）；经 api.prophey.ai 的非流式、流式 `/v1/messages` 各一个（临时密钥，已删）200、`billed`、费用与价格一致；发布后 15 分钟 12 个请求，唯一的 5xx 是没有账号服务的模型返回的 529；两节点日志无 ERROR。没有关 new-api 渠道（392 由管理员关着，未动），没有另做数据库备份。
- **ec2pool**（单节点，北京时间 2026-11-22 03:23，用户原话“帮我更新ec2pool”）：在那台机器上从 `cc6e51699` 构建（159 秒，核心二进制与 ovh 逐字节相同，内置包用 `sup2api-ec2pool-2026` 签名，清单 `50edeb94387bf39f38293bc9c05df6b61ff52b8803229da78987cf570fa6052f`），升级前只读 `check-prices` 通过；预检 primary-first-v1，计划 `5291cde85c8a4ead8856cc511faadd24` 03:23:30.8→03:23:37.4，入口 503 约 4 秒；停机那一刻 6 个进行中的流式请求（responses 转 chat）被切断，记为 200 + `upstream ended without response.completed`，另有 1 个 499。核验：0121 已应用，codex 0.2.2，5 个启用插件正常，迁移后 `check-prices` 通过；`chatGPT` 分组 chat、responses 的非流式与流式都 200、`billed`，同一 `prompt_cache_key` 的请求落同一账号并命中缓存；升级后 15 分钟 383 个请求成功率 99.5%（升级前 15 分钟 248 个、100%），5xx 只有 1 个上游 502；日志无 ERROR。
- 回滚：迁移 0121 单向，回退要连同数据库回到升级前（依靠库的自动备份）。
- 遗留：线上 3 条 10 月 8 日的 `failed` 使用记录（helper 用量不完整，重试已满 10 次）不会被重试，升级没有动它们。

本次发布包含的改动：

#### 扣费金额进一到 8 位小数（核心；无迁移，可滚动）

- 每次请求的扣费金额（表达式结果 × 分组倍率）原来四舍五入到 8 位，现在进一到 8 位，只会多扣不会少扣；先四舍五入到 12 位去掉浮点误差，免得整数价被多加 0.00000001（`billing.FinalCost`，CONTRACTS §97 原则段）。

#### 计费方式只留按次、按 token、表达式（核心；迁移 0121，单向；不改插件契约）

- 价格的计费方式只剩三种：按次、按 token、表达式。按 token 改成分档编辑：每档按 token（可单独填缓存写 1 小时、图片输入输出的价）或按次计，加价规则可以有多个条件同时满足才生效，条件可读请求参数、请求头、插件报的用量字段和时段。表达式模式只写源码，不再有可视化分档和视频计费模板。见 CONTRACTS §131。
- 加价规则直接写成乘法：`(基础) * (条件 ? 倍数 : 1)`，原来的 `|||` 分隔符删掉。表达式里任何“读了请求信息、命中时是一个数字、否则为 1”的三元式都算一条规则，试算和使用记录照旧显示每条规则是否命中。
- 迁移 0121 把已有价格转成新形状：原按 token 的五个单价变成单档；能用界面表达的可视化表达式价格变成按 token；其余表达式价格保留表达式。配置不合法（缺值、缺倍数、档名不合法、没有“其余”档、小时或时区不对等）的可视化价格也保留表达式，照旧计费。没有加价规则的价格表达式和哈希不变；含 `|||` 的表达式改写成乘法、换哈希，价格历史和还会重新定价的使用记录（未结算、预扣中，以及按预估价扣费、还能“重新核对”的）一起改，其他已结算的记录不动；使用记录按主键查，不扫整张表。写成字符串的价格（`"0.15"`）改成数字。全局价是表达式的模型上原有的自定义价规则（本来就不生效）被删除，免得全局价转成按 token 后它们突然生效。
- 分组 / 用户组的自定义价只能设在按次、或只有一档、没有规则的按 token 全局价上，按 token 的自定义价本身也只能是一档、带输入和输出价；全局价改成分档或加了规则后，原来的自定义价失效（按全局价往下一层找），与改了计费方式相同。
- 按 token 的配置里写错或旧的字段（如 `prices`、`flat`）现在直接报错，不再静默按 0 价保存。
- LiteLLM、models.dev 同步来的价格一律是按 token（有 200K 档的是两档）；从上游 sup2api 按倍率导入表达式价格时写成 `(原表达式) * 倍率`，写成字符串的价格也按倍率缩放（`GET /key/prices` 同样）。上游还是旧版本时，它的五个单价、可视化配置和 `|||` 规则在导入时转成新形状。
- **发布前**对线上库运行 `sub2api admin check-prices`：它用新引擎编译库里所有价格表达式（`|||` 按迁移的写法改写后），列出编译不过的（例如表达式里写了 `$env`，或读请求信息的三元式命中时是负数）；有输出就先改价格再升级；升级后再跑一次，它还会列出配置生成不出所存表达式的按次、按 token 价格（CONTRACTS §131.8）。
- 价格编辑页照 new-api 的模型定价页重排：左侧模型列表（搜索、按方式筛选，切换模型时保留筛选和页码），右侧分方式编辑（按次排第一，按次也能加倍数规则），档位卡、请求规则组、Token 估算器和请求模拟、底部检查栏；表达式模式有行号、着色，错误标出行和列（位置按输入的原文计），报错是中文，说明放在抽屉里。离开或切换模型前有未保存的改动会先询问；只读时也能展开档位卡查看、能返回列表。列表和摘要里的金额显示到 8 位有效数字，小额不再显示成 $0，摘要带“共 N 档 · M 条加价规则”。
- 回退要连同数据库回到升级前的备份。e2e 的价格夹具（`RunPriceConfig`）和 AC08 已改成新形状与乘积写法。

#### 用量不参与调度，算不了按请求的模型计费（核心；无迁移，不改插件契约，可滚动更新）

- 以前请求带了要单独计量的字段（`/v1/messages` 的 `fallbacks`、`compaction`、`context_management`，以及引用的模型），调度只把它交给能分项计量的账号，发送前账号参数覆盖加了这类字段也要换号；转换过去的账号（例如 Codex 账号接 `/v1/messages`）因此接不了这类请求。现在发出前不再检查，能服务这个模型（和它引用的模型）、这个协议的账号都能接，请求原样发出（转换过去的请求不带这些字段）。
- 应答回来后，附加用量和 fallback 各次尝试照旧分项计价；某一项报的模型没有准入的价格（应答报了没准入的模型等），这一项照它的用量按请求的模型的价格计费，记在计费明细的 `inputs.requested_model_segments`；只有分项的读数本身不能用时才丢弃分项、按顶层用量和请求的模型计费，原因记在 `inputs.segments_dropped`，并记一条警告日志。都照常结算。不再有“计费错误、整条不结算”的记录。之前因计费错误还没结算、仍在重试次数内的记录，下次重试时会按这个规则结算。
- 调度也因此快了：原来这项检查对每个候选账号都扫一遍请求体，1 MB 的请求体在 500 个账号上约 0.84 秒，现在没有这一步。见 CONTRACTS §120.4。

#### 插件列表显示厂商 logo（核心；newapi 0.1.1、sub2api 0.1.1、typesafe 0.1.1；无迁移，不改插件契约）

- 插件列表以前一律画首字母色块：`GET /plugins` 没有返回清单里的图标，列表拿不到。现在列表和详情接口带 `icon`、`asset_base`，列表按清单的图标画 logo（Anthropic、OpenAI、Gemini 等插件本来就带图标文件）。
- 已安装但没有启用的插件也显示 logo：`/plugin-ui` 对这类插件只提供清单顶层 `icon` 指向的那一个文件，界面和其他包内文件仍要等启用后才提供（CONTRACTS §5.8）。每个节点对每个插件只在内存里留一份图标（读到别的版本的图标时替换），同一插件的图标读取一次一个，是否允许提供仍每次查库。
- New API、Sub2API、TypeSafe 三个插件加上各自的 logo（取自 @lobehub/icons-static-svg 1.104.0，MIT，许可文本随包），各升一个补丁版本。动态权重、邮件、提示词审核等没有对应厂商的插件仍是首字母色块。

#### codex 0.2.2：授权处的个人使用提示（只改插件界面；无迁移，不改契约，可滚动更新）

- 新建 ChatGPT（Codex）账号和重新授权时，授权步骤上方多一条提示：这里接入的是你自己的 ChatGPT 订阅，仅限本人使用，不得共享、对外分发或包装成 API 转售，否则账号可能被 OpenAI 限制或封停。提示下方有“OpenAI 使用条款”和“OpenAI 账号共享政策”两个官方页面的链接。只提示，不需要勾选，授权步骤不变。说明见 [PLUGIN-CODEX.md](next/docs/PLUGIN-CODEX.md) 第 23 节。

## 2026-11-21

### 核心 0.1.305 + codex 0.2.1：codex 迁移一致性审核后的修复（`70424716f`；无迁移，不改契约，滚动更新；网关仍 0.1.139）

- **构建**：没有打 tag、没有走 GitHub Actions。在 ovh 上从 touwaeriol/sup2api develop `c2cc87ceb`（代码即 `70424716f`，加本节说明）按 `next-release.yml` 的做法构建：`build-ui.sh`、`build-go.sh`（amd64 全部插件，打包时连临时 PostgreSQL 做模型检查；arm64 只出两个二进制）、`package-core.sh` 用 ovh 上的插件密钥 `sup2api-plugins-2026` 重签内置包（`stage/own-0.1.305/`，149 秒）。清单 sha256 `54bbc659bc0bec472cdfdfc4b1afba3f5ffd8e574503d963e08ad15187b28765`（`release_id` `core-v0.1.305`，amd64 bundle `76e8b790…2d77`、arm64 `b6603ebc…81d7`），strategy rolling，schema 仍是 `6c0c081d…ae59`。与线上的上游 0.1.304 包逐文件比对：9 个没有改动的内置包（anthropic 0.4.0、dynweight 0.6.0、gemini 0.4.0、mailer 0.1.5、moderation 0.13.0、newapi、openai 0.5.0、sub2api、typesafe）和 `trust.pub` 逐字节相同，核心与插件 CLI 两个二进制不同（代码和版本号变了），codex 0.2.0 换成 0.2.1（包里的界面是这次构建的）。从同一提交重建的网关 0.1.139 二进制与线上相同（`c02f39bf…9870`），网关没有换。`next/plugins/released.json` 的 codex 改为 0.2.1。
- **发布前**：该代码提交的 next CI（run 44808330592）全过；e2e AC30–AC43 共 14 个用例在开发机的 e2e 栈上全过（AC34 Responses WebSocket、AC38 代理票据、AC40–AC43 在升级前跑完）。
- **ovh 集群**（北京时间 2026-11-21 04:21–04:22）：清单与 bundle 放进 `publish/sup2api/`，04:21:27 import，预检 rolling-v1、无阻断（expected_revision 573），计划 `6994b874dfa8c8a6ca4175b26dc1effd` 04:21:38→04:22:28（先 sup2api-ccp 后 sup2api-1），ovh 3130 与 api.prophey.ai `/readyz` 全程 200。内置同步 04:22:44 把 codex 升到 0.2.1。核验：两节点核心 0.1.305、网关 0.1.139，7 个启用的插件 active、ready、restarts 0；经 api.prophey.ai 的非流式、流式 `/v1/messages` 各一个（临时密钥，已删）都是 200、`billed`；04:15–04:40 只有这两个请求，5xx 0；两节点日志无 ERROR（WARN 只有计划让出时的 `offload started`、`local core unreachable; recovering from the persisted route` 和 Landlock 的几条）。线上没有 codex 账号，Responses WebSocket 会话没有在线上实测（由 AC34、AC36 覆盖）；线上两个启用账号的并发上限是 0（不限），并发位是否释放在线上看不出来，只确认了两节点在途请求为 0。
- **更新源**：03:49:57 起线上的更新源是 `touwaeriol/sup2api`（经 sup2api-1 的本机管理 socket 改，`update_source_revision` 0→1）；这个仓库目前只有 `gateway-v0.1.139` 一个 Release，核心线没有 Release，所以检测不到核心新版本，核心照本节的做法在 ovh 构建。
- 回滚：schema 与 0.1.304 相同，可用 0.1.304 清单 `af321be48f5cbb024456a3a2defca1aedc31e713d55af40b3a075925b9d81548` 建计划回退（rolling）；codex 0.2.1 不降级。
- **ec2pool**（单节点，北京时间 2026-11-21 04:59–05:00，从核心 0.1.303、网关 0.1.138 直接到 0.1.305、0.1.139）：同样在那台机器上从 develop `375f829a7` 构建（160 秒；核心二进制与 ovh 的构建逐字节相同，内置包用只留在那台机器上的密钥 `sup2api-ec2pool-2026` 签名，清单 `8054723a5c1b35fd01f40d78a5429a403c544faf9fe29512364bb89117e0e5bd`，strategy maintenance）。照网关手册“停机重新部署”：停网关后先 `pg_dump` 备份（26.8 MB，`pg_restore --list` 可读），`redeploy -manifest`，`serve -bootstrap`，再去掉 `-bootstrap` 重建；入口 `/readyz` 04:59:29.4→05:00:13.7 约 44 秒、05:00:30.4→05:00:34.6 约 4 秒不可用。迁移 0106–0120 应用，volcengine 卸载，近 30 天没有跨平台转换，不用配置。升级后经公网入口调 `chatGPT` 分组（73 个 codex 账号）：`/v1/chat/completions`、`/v1/responses` 的非流式与流式都是 200、`billed`；同一个 `prompt_cache_key` 的请求都落在同一个账号并从第二次起命中 `openai-prompt-cache-key`，不同会话落在不同账号，`cache_read` 正常。细节见 [ec2pool 部署记录](next/deploy/gateway/ec2pool/README.md)。

把 codex 0.2.0 + 核心 0.1.304 逐模块对照上一版线上的 codex 0.1.11 + 核心 0.1.303（请求构造、WebSocket 与计费收尾、登录与刷新、用量窗口与 429、重置次数、清单与设置）：发给 OpenAI 的请求、用量窗口与 429 规则、重置次数、令牌刷新与线上等价，差别只有本文件已经记录的那些。查出并修掉的：

- **Responses WebSocket 会话可能永久占住账号并发位**（核心；2026-11-18 去掉写超时和握手超时之后才有：0.1.303 没有，已发布的 0.1.304 有）：客户端在一轮中途不再读取时，会话循环阻塞在写给它的那一步，而保活 ping 正是由这个循环发起的，所以发不出去，会话和它占的账号并发位、密钥会话数、预扣一直留着。现在每条连接的 ping 由自己的协程发出，30 秒内发不出去或没有回应就关闭连接，这一轮记为客户端取消；上游不再读取时同样处理。ping 要等正在写的那条消息写完才发得出去，所以仍在读、但读一条消息的余下部分超过 30 秒的对端也会被断开（一条消息实际可以写 30 到 60 秒；0.1.303 是每次写 1 分钟，0.1.304 没有上限，代价就是上面的卡死）。上游握手迟迟不回、客户端又已离开时，握手现在随客户端离开而结束（记 499，不算账号失败），以前会一直等。没有新增超时常量，用的仍是原来的 30 秒 ping、30 秒无响应（OPENAI-RESPONSES-WEBSOCKET.md 第 4 节）。
- **授权时表单选的代理被停用，被报成“连不上 OpenAI”**（核心）：票据发出后代理被停用，取回以前按传输失败交还，codex 的开始回 502、轮询一直提示连不上直到设备码过期。现在与其他拒绝一样以 `UNAVAILABLE` 交给插件，界面显示核心给出的原因（代理已停用）并保留登录（CONTRACTS §117.3）。
- **设备码批准后换令牌被核心拒发**（codex）：这时会话已经结束，提示却是“检查代理后重试”；改为“检查账号表单里的代理后重新开始”。
- **插件设置保存的是去掉首尾空白后的值**（codex）：经接口存入 `"UA/1\n"` 以前原样入库（生效值本来就是裁剪后的），之后界面会拒绝这个已存值。
- **账号表单**（codex 界面）：服务端对 User-Agent、originator 的报错显示在对应输入框下（以前只在底部列表里）；没有对应输入框的报错显示在表单顶部，与 vertex、bedrock 的表单一致；本地格式错误改为失焦或保存时提示。
- 清理（行为不变）：删无调用者的 `loadCached`、不会发生的随机数错误分支、界面里多余的 `alive` 标志与没人用的 `defineExpose`；客户端标识写请求头、“请求没发出”的分类各收敛到一处；`oauth.go` 里与登录无关的 KV、路由应答、凭证校验搬到各自的文件；注释和 PLUGIN-CODEX.md 改成与代码一致。
- 补测试：被动采样用量窗口（HTTP 的 200 与 429、WebSocket 握手）、缓存的重置次数解不开、`input` 不是数组时不改角色、两种登录方式的默认模型相同。

### 审核插件删掉更早版本的升级代码（moderation 0.13.0；无迁移，随 0.1.304 发布）

- 删除 0.8.0 的观察名单转换、0.11.0 从设置导入名单（连同只给它读的 `allowlist_user_ids`、`exempt_user_ids`、`blocklist_user_ids`）和 0.1 单模型设置的错误日志；`GET /status` 不再有 `lists_imported`，设置页也不再因“名单导入尚未完成”而不能保存。线上的 0.12.1 早已做完这些一次性步骤（两个部署都以 0.12.1 运行过，代码与删除前相同）。
- 设置接口不再接受 0.1 的 `api_key`（`ValidateSettings` 回 `unknown`；控制台保存时本来就去掉它）。
- 保留为过渡代码：已存设置里的 `cache_ttl_seconds`、`ban_window_hours`、`retention_days` 仍在新键不存在时读（控制台保存时折算进新键），已存的 `api_key` 仍由核心遮蔽。所有部署都在 0.13.0 下保存过一次审核设置之后的下一个版本删除。规则见 CONTRACTS §72.10。
- **升级须知**：只能从 moderation 0.12.1 升级（更早的版本先升到 0.12.1）。升级后在审核设置的任一页点一次“保存”（不用改值：没有改动时页面照样写回去掉、折算后的设置），已存设置里的旧键与 `api_key` 就被去掉；之后 `GET /api/v1/plugins/moderation/settings` 的 `values` 里应没有上面四个键。

### 兑换码只有余额一种，兑换不了的码不再被用掉（payment 0.2.0；无迁移）

- 生成兑换码（`POST /redeem-codes`）时 `type` 不写即为 `balance`，写了别的值 400 `unsupported code type`；不再接受 `group_id`、`validity_days`（从没生效过），管理员列表不再返回 `validity_days`。此前不写 `type` 存成空串，`subscription`、`invitation` 或任意字符串也照收。
- 兑换时先查类型：不是 `balance` 的码返回 400 `unsupported code type`，码保持未使用。此前这样的码被标成已用、接口返回成功，余额却没有变化。new-api 的兑换码同样只给额度。规则见 CONTRACTS §46.6。
- **升级须知**：库里已有类型不是 `balance` 的未用兑换码（直接调接口生成时没带 `type`，或带了别的类型）的，升级后兑换会被拒、码不再被消耗；需要时作废它们，按 `balance` 重新生成。查询：`SELECT id, type, value FROM plg_payment__redeem_codes WHERE status = 'unused' AND type <> 'balance'`。

### 审核 OpenAI 生图的提示词（moderation 0.13.0；无迁移，随 0.1.304 发布）

- 审核钩子（调度前的 `moderation` 与按账号的 `moderation_attempt`）加上 `openai.images`、`openai.images_edit`：审 `prompt`，JSON 形式的 edits 另审 `images[].image_url` 的图片（`file_id` 引用和 `mask` 不审）。此前删除 volcengine 时一并删掉了生图提示词的审核，而本批新开放的 OpenAI 生图（OpenAI API Key 账号、codex 账号经转换）只经过封禁钩子，拦截级用户发到 `/v1/images/generations`、`/v1/images/edits` 的违规提示词不审、也不留记录。new-api 对生图请求同样检查提示词。规则见 CONTRACTS §72.3、72.4。
- 钩子 `needs` 与 `gateway.hook` 的 `scope.fields` 加 `prompt|@this`、`images|@this`。moderation 是内置插件，随镜像升级时由系统同意，不需要管理员重新批准。
- 生图请求的审核记录 `input_format` 为 `openai.images` / `openai.images_edit`；审核按流程的范围照常执行，提示词和图片都算用户内容，三种范围都审。

### 一个事务里多次升授权版本时，节点不再先等自己还没提交的版本（核心；无迁移，随 0.1.304 发布）

插件升级的激活（`ApplyDefaults` 的 `SyncPlugin` 加 `SetPluginActive`）、带角色授予的同意（`CanGrantToRoles` 加 `SyncPlugin`）、复核授予多个键等，在同一个事务里升两次以上版本。第二次升版本时节点把第一次（同一事务、尚未提交）的版本当成已提交，enforcer 读不到它，反复记 `casbinx: … the database is at authz version N, below M heard of`，本节点的权限判断等到提交才恢复；事务回滚时这个版本永远不会出现，本节点所有判断返回 503，直到另有授权写入提交到该版本或重启核心。现在只把别的事务提交的版本当成已知（CONTRACTS §99.4 原本的约定）。与数据库是否从备份恢复无关。

同时修正：调用方事务里的授权写入（插件目录、`ClearScopeGrants`）由 `committedLater` 轮询到版本可见后广播，以前带上本节点登记的范围；事务回滚、而别的节点又提交了同一个版本号时，会以错误的范围广播（例如把一次用户写入说成策略写入），各节点据此保留了本该重读的用户，真正的广播又因版本不新被丢掉。现在轮询广播一律不带范围，各节点整体重载。

### 插件数据库隔离：插件的表放进核心的模式、带前缀，每个插件一个数据库身份（核心；没有核心迁移，已有数据在启动时搬迁；随 0.1.304 发布，单向）

所有者定稿“同一个模式，加前缀，一个身份”。规则见 CONTRACTS §130。2026-11-18 定下的“插件只用核心的数据库用户”（CONTRACTS §115）在发布前被它取代，没有上线，本节只写最终的做法。

- **布局**：插件的表、索引、序列在核心的模式（线上 `public`）里，名字一律以 `plg_<key>__` 开头；不再有每插件一个的 schema `plg_<key>`。
- **身份**：每个用数据库的插件一个登录角色 `plg<库OID>_<key>`，密码由主密钥派生、不落库（只发 SCRAM 校验值），插件的表归它所有；核心的表和别的插件的表它没有权限，漏写前缀改到核心的表由数据库拒绝。
- **迁移**以这个身份另开连接执行（临时给模式的 CREATE，跑完或失败立即收回），每个脚本一个事务；只能建带前缀的表、索引、序列、视图和类型，建了别的（不带前缀的对象，任何函数、运算符、collation、触发器等）就回滚该脚本，提前 `COMMIT` 逃出事务的也在迁移结束时删掉；记录写在插件自己的表 `plg_<key>__schema_migrations`，核心只以插件的身份读写它，核心的 `plugin_migrations` 只记数据迁移。
- **插件以外的表**：清单 `database.access` 逐表申请列和权限，要宿主权限 `db.core_read`（极高风险，只有超管能批）；上传时检查表和列存在；授予照申请做列级或表级 GRANT；升级多要任何东西都要重新同意。审查新增 `database_access` 和 `database_migrations`（每个脚本的 SQL 与 `new`/`changed`/`applied`），新接口 `GET /api/v1/plugins/:key/database`。
- **连接**：插件一律拿到虚拟地址 `db.sub2api:5432`，核心的 pgproxy 以插件的身份登录（`pg_stat_activity` 里 `application_name` 是 `sub2api plugin <key>`），插件拿不到数据库的地址和密码；出口策略里“有数据库的插件可以拨真实数据库地址”的例外删除。
- **卸载**：“清除数据”删插件的表和身份；不清除时只留数据：表（连同索引、序列）和类型留给核心，插件的视图、物化视图、外部表和表上的触发器、规则、策略删掉，插件授出的权限（包括给 PUBLIC 的）收回，免得它们以核心的权限替别的插件读写核心的表；重装时交还新身份，已跑的脚本不重跑（删掉的视图要由之后版本的迁移重建）。插件在运行时把自己的表授权给别人，每次 Ensure 收回。停用的键“还有数据”改为看有没有带前缀的表。
- **授予 `db.schema`**：非官方插件一律要求超管专属的 `plugin:grant:db_schema_unconfined`（0.1.303 只在关闭角色隔离时要求；所有者决定保留），官方插件不变。理由改为：插件因此在核心的数据库里有自己的身份、自己的表和自己的连接；插件以外的表另由 `db.core_read` 把关。权限说明随之更新。停用插件不改变它身份的数据库权限（`db.core_read` 的授权留到卸载）。
- **迁移 0110**（`0110_drop_plugin_migration_funcs.sql`，单向）删除 0.1.303 的插件角色记录迁移用的两个 SECURITY DEFINER 函数 `plugin_migrations_applied`、`plugin_migration_record`；之后旧核心无法再给插件建角色，持有 `db.schema` 的插件在旧核心上起不来。
- **官方插件**：dynweight 0.6.0、guard 0.3.0、moderation 0.13.0、payment 0.2.0，表名、索引名和迁移脚本加前缀，模型常量 `model.Prefix`；`pgdb.TryXactLock` 的键改按插件身份（`current_user`）区分。
- **控制台**：插件详情的“数据库”页只显示最后一次读取的结果：插件升级时 `desired_version`、`active_version` 先后变化引发的几次读取，先发的晚到时不再盖掉新版本的脚本和执行状态。
- **打包工具**：`sub2api-plugin db check` 每个脚本跑完后检查一次，拒绝不带前缀的对象和表、类型以外的任何对象，也检查模型的表名和索引名带前缀、不超过 63 字节。迁移又以临时的 `NOLOGIN` 角色执行（只有临时 schema 的 `CREATE`，同宿主的插件身份），需要更多权限的语句（`ALTER … OWNER TO`、`CREATE SCHEMA`、`CREATE EXTENSION` 等）在工具里就失败；模式外的对象按属主查，`EXCEPTION` 块里建的也查得到。比对库的用户要能建角色（testpg 和 CI 的超级用户可以）。
- **多节点**：各节点记着本进程做过 Ensure 的插件，之后每次先确认插件的身份还在；经一个节点保留数据卸载、再重新上传时，别的节点上的上传、审查和 `GET /plugins/:key/database` 不再因身份已被删而报 `password authentication failed`，而是重建身份、把留下的表交还给它。
- **搬迁**（过渡代码，所有库都在 0.1.304 或更新的核心下启动过后的下一个核心版本删除）：新核心启动时逐个插件把旧 schema `plg_<key>` 的表、索引、序列、约束改名加前缀、移进核心的模式，迁移记录搬进插件的记录表（脚本不重跑）；0.1.303 为插件建的登录角色 `plg_<key>`（含已卸载插件留下的）逐个收回：`REASSIGN OWNED` 给核心的用户、`DROP OWNED`、`DROP ROLE`，日志 `converted legacy plugin database role`，角色还有会话（旧插件进程未退出）时该插件整个留到下次 Ensure 或启动，记一次警告 `legacy plugin database role still has sessions`；搬不走（schema 里有函数、类型）的插件搬迁中止并记错误日志，不影响别的插件。结果与全新安装逐项相同（含约束名，例如 `fk_payment_orders_provider_instance` 变成 `fk_plg_payment__payment_orders_provider_instance`）。

**升级须知**

- **核心的数据库用户需要 `CREATEROLE`**（或是超级用户；以及 PostgreSQL 16 及以上）。0.1.303 设了 `SUB2API_PLUGIN_DB_ROLE_ISOLATION=false` 时不需要，现在没有这个开关。没有时用数据库的插件（dynweight、guard、moderation、payment）迁移失败、不启动，报 `plugin_db_isolation_unavailable`，不降级。升级前按 0.1.304 一节“升级前要做的”里的只读查询检查。
- **插件身份要能以密码登录本库**：迁移和 pgproxy 都以 `plg<库OID>_<key>` 另开连接，核心不给它授 `CONNECT`。`pg_hba.conf` 只放行核心的用户的，要加上允许 `plg<库OID>_*` 角色以密码（`scram-sha-256`）连本库的规则；PUBLIC 在本库上的 `CONNECT`（PostgreSQL 默认有）不能收回，收回过的要还给 PUBLIC 或逐个授给插件身份。否则 Ensure 成功，但迁移和插件的数据库连接登录被拒，带数据库的插件起不来。
- 新核心第一次启动会搬迁上面四个插件的表（线上 35 张表），日志里每个插件一行 `moved the plugin's tables into the core's schema`；出现 `convert the legacy database layout of a plugin` 错误时该插件起不来，按错误处理后重启。
- PostgreSQL 14 及更早建的库，核心启动时收回 PUBLIC 在核心模式上的 CREATE，收不回启动失败（线上已确认没有开放）。
- **核心启动时收回 PUBLIC 在库上的 `TEMPORARY`**（`REVOKE TEMPORARY ON DATABASE <库> FROM PUBLIC`，库级改动，PostgreSQL 默认开放）：插件身份不能再建临时表和 `pg_temp` 里的函数（否则插件表上的触发器可以借核心的权限执行它）。所以 PUBLIC 还有 `TEMPORARY` 时，核心的数据库用户须是库的属主或超级用户（属主自己仍可建临时表），否则**核心整个启动失败**（不只是插件），日志给出要执行的语句；升级前用 0.1.304 一节“升级前要做的”里的查询确认，不满足的先由库的属主执行 `REVOKE TEMPORARY ON DATABASE <库> FROM PUBLIC`，或把库的属主改成核心的用户。用同一个库、靠 PUBLIC 建临时表的其他数据库用户（报表、运维账号）升级后要单独 `GRANT TEMPORARY ON DATABASE <库> TO <用户>`。
- 插件的每条数据库连接是它自己身份的一个会话，计入 `max_connections`，规划同前（节点数 ×（核心池 + 每个用数据库的插件 4 个））。
- **单向**：搬迁后旧核心找不到 `plg_<key>` schema，回退要连同数据库回到升级前的备份（与 0.1.304 一节相同）。
- **设过 `SUB2API_PLUGIN_DB_ROLE_ISOLATION=false` 的部署先别删它**：网关外壳与核心分开换，先换上这一版外壳时它启动的仍是 0.1.303 核心，旧核心没有这个值就重新打开角色隔离，数据库用户没有 `CREATEROLE` 时持有 `db.schema` 的插件起不来。所以外壳继续把它传给核心（`supervisor.legacyCoreEnv`），全部集群换上 0.1.304 之后的下一个外壳版本删除；新核心设了也不读。

### 插件界面只用代码写：删掉声明式表单与页面，界面契约 4.0（核心、SDK、控制台、全部带界面的第一方插件；无迁移，随 0.1.304 发布；设计见 [DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY.md](next/docs/DESIGN-2026-11-19-PLUGIN-UI-CODE-ONLY.md)）

账号表单、插件设置和插件页面只能用代码写：插件自带的原生界面（`ui/native`，受信任发布者，宿主权限 `ui.native`）或 iframe。声明式的 JSON Schema 表单（`form.mode: "schema"`）和表格、表单页面（`type: "table" | "form"`）整个删除，核心不再读、不再校验 JSON Schema 表单。规则见 CONTRACTS §23、§75.1、§75.5。

**核心与清单**

- 删掉 `GET /account-types/:plugin_key/:type/form`；`GET /plugins/:key/settings` 不再返回 `schema`、`ui_schema`；插件包不再打包、核心不再提供 `forms/`（`/plugin-ui/.../forms/...` 返回 404）。保存账号凭证只由插件的 `ValidateCredentials` 把关，保存插件设置只由插件的 `ValidateSettings`（可选）把关。
- 插件设置的密钥改在清单 `ui.settings.secrets` 里声明（`[{path, sensitive?}]`）。native、iframe 设置必须写这个键（上传时缺了报 `ui.settings.secrets` `required`）：写了列表只掩码列出的字段，写成 `[]` 不掩码；已装的包没有这个键时，和此前一样每个顶层键都被掩码。路径中途遇到数组时整个数组按密钥掩码和保留（CONTRACTS §75.1、§75.5）。
- 新增可选的 `PluginService.ValidateSettings`：保存插件设置前由插件校验，可以返回字段错误或归一化后的设置；插件没实现时照常保存，插件已启用但运行不起来时保存失败（`plugin_unavailable`，先停用再改）。校验在保存的事务之外，插件回答期间不锁插件行；校验期间设置被别的保存改了，这次保存返回 409，重读后再存。请求带插件当前的授权（与 `Configure` 相同）。插件线协议（2）和 Host API（10）不变。
- `GET /plugins` 的 `has_settings` 只对当前版本是 native 设置的插件为 true（iframe 设置控制台不打开，仍为 false）。
- 清单检查：账号表单或设置的 `form.mode: "schema"`、页面的 `type: "table" | "form"` 上传时报 `unsupported`；native 设置要有 `component` 或 `page`。`manifest.Form` 删掉 `schema`、`uiSchema`，`manifest.Page` 删掉 `source`、`columns`、`schema`、`submit`、`search`、`create`，`check.Warnings` 删除。`sub2api-plugin` 严格读清单，旧字段报 unknown field。

**SDK**

- 新增 `pluginsdk.SettingsValidator` 和校验辅助包 `pluginsdk/fieldcheck`（`OneOf`、`Pattern` 只能用于 `String()`，用错在构造时 panic；必填字段收到 `null` 报 `required`）、`fieldcheck.DecodeSettings`（设置的“解码 → 检查 → 解码进带默认值的结构体”；`Check` 接受而结构体的 Go 类型装不下的值，例如写成 `60.0`、`6e1` 的整数或超出类型范围的数，报在该字段上、错误码 `type`，不再报成根上的“设置必须是 JSON 对象”，guard 的 `alert_cooldown_sec`、mailer 的 `port`、`timeout_seconds` 随之变化）；`pluginsdktest.Harness.ValidateSettings` 多一个 `grants` 参数，新增 `pluginsdktest.SettingsSecrets`（清单写出 `ui.settings.secrets` 且正好声明预期的密钥）。
- `apikey.Spec.Fields` 改为 `fieldcheck.Fields`：一处声明既是允许的键又带规则（也可以给 `api_key`、`base_url` 加规则，只在 Spec 自己的检查通过后、对将要保存的值即去空白的 key 和规范化后的地址运行），插件不必再包一层 `Platform` 改写 `ValidateCredentials`。`Fields` 为 `nil` 时也拒绝 `api_key`、`base_url` 以外的键（此前 `nil` 不检查），代替 schema 的 `additionalProperties:false`；`accountform.Form.Only` 同理。新增 `Spec.NoBaseURL`（vertex_apikey 用它拒绝 `base_url`）。新增测试辅助 `apikey/apikeytest`（api_key + base_url 类型共有的 `ValidateCredentials` 用例）。
- `apikey.NormalizeBaseURL` 拒绝去掉首尾空白后仍含空白字符（任何 Unicode 空白）、`?` 或 `#` 的基础地址（与原表单 schema 一致；此前路径里的空格被转成 `%20` 保存）。已存的地址都是它的输出，空格与非 ASCII 字节都已百分号转义，请求地址不变。

**界面包与控制台**

- 界面包 `@sub2api/ui`、`@sub2api/host`、`@sub2api/vite-preset` 升到 4.0.0（`HOST_UI_VERSION`），删掉 `SchemaForm`、`SchemaField` 等 schema 导出；控制台删掉声明式表单、表格、表单页的渲染。全部带原生界面的第一方插件（15 个，payment 没有界面）`hostUICompat` 都是 `^4.0`。
- `@sub2api/ui` 新增：`SFormSection`（表单分节：标题、说明、可折叠，有值或有错误时自动展开；`createApiKeyForm` 的 `section` 可写 `{ title, help, collapsed }`）、`SDateTimeInput`（RFC 3339 的日期时间输入，清空不发该键；`createApiKeyForm` 的 `date-time` 改用它）、`nativeAccountFormProps`（§23.5 props 的运行时对象，插件写 `defineProps(nativeAccountFormProps)`）、`useForm` 的 `bindNumber(path)`（数字框清空时不发该键）、`SSecretInput` 的 `plain`（敏感地址用文本框输入）；`createApiKeyForm` 的额外字段可写 `mono`（`text`、`textarea` 用等宽字体）。`useForm` 的 `get()` 在写入后的同一拍里渲染时不再丢掉对值的依赖（此前 `v-if` 绑在 `get()` 上的字段第二次切换后不再更新）。
- `SSettingsPage` 在同一路由换查询参数时也先询问未保存的修改：插件详情页切换 `?tab=` 标签（mailer、codex、guard 的设置就在这个标签里）、插件页面用 `?tab=` 切分区，此前只在离开路由时询问，切走设置标签修改直接丢失。
- `@sub2api/host` 新增 `definePluginHost(name)`：插件模块级 host 的 `setHost`、`useHost`（`register` 之前调用抛错）；bedrock、vertex、mailer、guard、codex、moderation、dynweight 的 `host.ts` 改用它，不再各写一份（CONTRACTS §23.1）。
- 插件的界面契约比控制台新（例如插件只把 `hostUICompat` 抬到 `^4.1` 而没抬 `hostCompat`，装在 UI 4.0.0 的核心上）时，账号表单、插件设置和插件页面提示“此插件的界面需要更新的控制台，请升级核心”，不再误报成“请升级插件”；`@sub2api/host` 新增 `belowRange` 判断这个方向。
- `@sub2api/ui/testing`：账号表单（`fakeHost`、`mountAccountForm`、`fillControls`、`keyAndUrlCases`）、设置表单（`fakeCore`、`mountSettings`）、文案（`checkMessages`）的测试辅助和 `cleanup`；只能在 spec 里引用，不进 import map。
- 仓库里的插件界面工程用控制台的 vite、vue-tsc、vitest 构建和测试（`@sub2api/vite-preset` 提供基础 tsconfig 与构建配置），CI 的插件界面检查任何一步失败都报错；工程有自己的 lockfile 时才 `npm ci`。

**各插件**

- API Key 类账号（anthropic、gemini、typesafe、openai、relay、newapi、sub2api、azure-openai）：表单改为插件的 `ui/native`（`createApiKeyForm`），字段名、默认值、文案与原表单相同，编辑时不改密钥仍保留已存的值，Base URL 仍按受限设置只给官方地址；清单改为 `form.mode: "native"`，加 `ui.native`。relay、newapi、sub2api、azure-openai 的表单把站点地址放在 API Key 前面；openai 的组织 ID、项目 ID 放在“高级（可选）”一节下，默认收起，有值或有错误时自动展开。`ValidateCredentials` 拒绝表单不会发的键（`unknown`；openai 只另外允许 `organization`、`project`，`azure_deployment` 只另外允许 `api_version`），基础地址路径里的空格由转成 `%20` 改为拒绝。插件补上原来只写在 schema 里的校验：openai 的组织 ID、项目 ID 不超过 128 个可见 ASCII 字符（错误码随 `fieldcheck`：不是字符串报 `type`、超长报 `maxLength`、字符不对报 `pattern`；为 `null` 时由忽略改为报 `type`）；azure-openai 的资源地址只能是资源根地址（可带 `/openai`、`/openai/v1` 或 Foundry 项目路径；表单与插件一样按规范化后的地址判断：去首尾空白、协议名不分大小写、去掉末尾斜杠），`azure_deployment` 的 `api_version` 必填、须是 `2024-10-21` 或 `2025-04-01-preview` 这样的日期，新建时表单仍预填 `2024-10-21`，编辑已有账号不再补默认值。密钥与地址的规则见下一节。
- bedrock：三种账号类型的表单改为原生组件 `BedrockAccountForm`，字段、默认值（区域 `us-east-1`、前缀 `global`）和文案不变；编辑已有账号时不再自动补区域和前缀（保存时插件照旧补上）；原 schema 的约束都由 `ValidateCredentials` 检查，拒绝表单没有的键（三种类型各按原 schema 的键）；不认识的账号类型不再当作 `bedrock_apikey` 显示；区域填中国区（`cn-*`）或隔离分区（`us-iso-*`、`eu-isoe-*` 等）时表单当场报错（插件保存时同样拒绝）。
- vertex：表单改为原生组件 `VertexKeyFileForm`（服务账号、用户凭证）和 `VertexApiKeyForm`（直接用 `createApiKeyForm`），字段、默认值（密钥文件类型的区域 `global`）和文案不变；核心续期写回的 `access_token`、`expires_at`、`token_for` 不显示、保存时原样带回；编辑已有账号时不再自动补区域；密钥文件 32768 个字符的上限从 schema 搬进插件；表单当场报出插件会拒的两种缺项：API Key 账号只填了项目 ID 和区域中的一个，密钥文件里没有项目（服务账号的 `project_id`、用户凭证的 `quota_project_id`）而项目 ID 留空。拒绝表单没有声明的键（`unknown`）：密钥文件类型允许密钥文件、`project_id`、`location` 和 `access_token`、`expires_at`、`token_for`，`vertex_apikey` 允许 `api_key`、`project_id`、`location`。
- 原生账号表单（codex、bedrock、vertex）只按编辑器传的 `accountType` 认账号类型：删掉为不传它的旧控制台留的退路（codex 类型未知时两种登录都显示成标签页，bedrock、vertex 退回 `account.type`），能加载 `^4.0` 插件的控制台总会传它；CONTRACTS §23.5 删掉教插件这样写的“兼容”一段，改为写明 `accountType` 总会传。codex 账号表单随之去掉只为两种登录并列而有的标签页和文案（`tabs.device`、`tabs.oauth`、`tabsLabel`），界面不变。
- moderation 0.13.0：设置页不再用表单 JSON，改用 `useForm`、`SForm`、`SSecretInput` 写成代码，分区、顺序、文案和保存方式不变；清单 `ui.settings` 声明 `secrets`（`api_key`、`notify_webhook_secret`，`notify_webhook_url` 为敏感地址）；插件实现 `ValidateSettings`，按原 schema 的每条约束校验并拒绝未知键。删掉只为 UI 3.13 及更早的控制台留的页内标签页回退（`hostUICompat ^4.0` 下不会走到）和用不到的文案；删除回滚 0.10.0 的脚本 `tools/settings_cleanup.py`：核心 0.1.304 起设置接口不返回 `schema`，它在新核心上不能用（旧核心上需要时的取法见 CONTRACTS §72 的 0.11.0“回滚到 0.10.0”）。
- 插件设置（mailer、codex、guard）改为原生组件，保存前由插件 Go 校验（`ValidateSettings`，与 `Configure` 共用一套规则，拒绝未知键）：mailer 新建界面工程，`timeout_seconds` 超出 5–25 秒改为报错（原来加载时夹到范围内，控制台本就按 schema 拒绝），可以清除已存的 SMTP 密码，填了 SMTP 服务器而发件地址为空或不像地址时设置页当场在发件地址一行报错（插件的检查仍是最终的）；codex 的设置（`user_agent`、`originator`）由 `CodexSettings` 显示，校验（只拒绝控制字符）搬到插件，账号表单里账号自己的这两项也按同一规则当场在对应一行报错；guard 的设置由 `GuardSettings` 显示（webhook 用 `SUrlPresetInput`），webhook 地址格式、冷却时间 0–300 秒和未知键由插件拒绝（冷却时间越界的错误码由 `range` 改为 `minimum`/`maximum`）。四个插件的 `ui.settings.secrets` 都显式写出：mailer 为 `[{path: "password"}]`，moderation 见上，codex、guard 为 `[]`。

#### 升级须知

- **非内置的带界面插件要管理员装上新版本**：relay、guard、azure-openai、vertex、bedrock（payment 没有界面，本节不涉及它，但仍要按 0.1.304 的升级须知重装）。内置插件随核心升级。重装审查多一项“在控制台运行插件界面”（`ui.native`）。
- **装着旧包也照常加载**（加载时只检查契约版本）：schema 模式的账号类型照常服务，控制台编辑页的凭证部分显示“此插件版本的界面已不受支持，请升级插件”，其它部分照常保存；按 3.x 界面契约构建的原生界面（`hostUICompat` 不含 4.0）不加载，账号表单、插件设置和插件页面同样显示这句（此前显示版本不匹配的技术原因和一个无用的“重试”）；保存时插件拒绝的凭证字段，在没有插件表单可显示的这些情况下由编辑页自己列出（此前只在左侧分区标红，没有任何文字）；schema 设置、带 schema 却没写 `secrets` 的 native 设置，值全部遮蔽，控制台不显示设置页；声明式页面显示“不支持的页面类型：{type}”（`table` 或 `form`）。
- **第三方插件作者**：表单与页面只能用 native（受信任发布者）或 iframe 写，设置的密钥在 `ui.settings.secrets` 里声明。schema 模式的第三方插件升级后没有界面（见上一条），新包上传时报 `unsupported`。直接用 SDK 预设或 `apikey.Spec` 而没写 `Fields` 的插件，用新 SDK 重新编译后保存账号时拒绝 `api_key`、`base_url` 以外的键（`unknown`）；已存账号带其它键的，下次保存会被拒，要在 `Fields` 里列出这些键。
- **已存的越界设置值让升级后的第一次保存失败**：插件设置的校验从控制台搬到了插件（`ValidateSettings`），库里已存着越界或不合规则的值时（例如 mailer 的 `timeout_seconds`、guard 的冷却时间、moderation 的数值项，以及下面“审核题集”里那几种题集），之后在任何设置页保存都会被拒，直到把那个值改对；审核题集也可以改回用内置题集。请求时的行为不变。

### 密钥与基础地址规则（SDK、核心、带 API Key 的十个插件、界面包；无迁移，随 0.1.304 发布；设计见 [DESIGN-2026-11-20-KEY-AND-BASE-URL-RULES.md](next/docs/DESIGN-2026-11-20-KEY-AND-BASE-URL-RULES.md) 方案 B）

所有者要求“密钥不要做任何限制”、地址里的 `%2F` 不再被还原、版本后缀“没有就加上，有就不管”。规则见 CONTRACTS §127。

- **密钥不再限制格式和长度**：`apikey.Spec` 管的 `api_key`（anthropic、openai、gemini、typesafe、relay、newapi、sub2api、azure-openai、vertex_apikey）以及 bedrock 的 `api_key` 只要求是非空字符串，保存去掉首尾空白后的值；不再检查前缀、字符集和长度（原来 8–512 个可见 ASCII 字符，openai 另有 512 上限，bedrock 8–2048）。带 CR、LF 等控制字符的密钥能保存，但每次请求都以“请求头无效”失败，不会注入请求头。`createApiKeyForm` 的 `api_key` 默认规则改为只要求非空（`min`、`max`、`pattern` 仍可按需写）；`base_url` 的默认规则改为与插件后端一致：首尾的空白不再报格式错误（地址框本就会去掉首尾的 ASCII 空格，不换行空格等其它空白也一样），只有空白算没填，scheme 不分大小写，带 userinfo（`https://user:pass@host`）、没有主机（`https:///v1`）的地址当场报错，不再等保存时由插件拒绝。newapi、sub2api 的密钥不是字符串时错误码由 `pattern` 改为 `type`。bedrock 的 AK/SK/会话令牌、vertex 密钥文件不变。
- **基础地址按填写的保存**：只去首尾空白、scheme 转小写、去掉末尾的 `/`；路径的转义原样保留（`%2F`、`%7E` 不再还原成 `/`、`~`），保存时不再剥掉版本后缀（`/v1`、`/v1beta`、`/openai`、`/openai/v1`、`/api/v3`）。控制台显示的就是填写的地址。
- **请求时至多去掉一个版本段**：插件按账号类型声明的版本段（`apikey.Spec.Versions`，原 `StripSuffixes`），在地址转义形式的路径末尾整段、区分大小写地去掉最长的一个（scheme 和主机不参与），再把各接口的完整路径接上。所以 `https://x/v1/v1` 现在请求 `https://x/v1/v1/...`（以前少一段 `/v1`，配不通）；`…/a%2Fv1` 不算版本。gemini、newapi、sub2api 的地址带另一个版本时按接口换成它要的版本（如 gemini 填 `…/v1` 请求 `…/v1beta/...`）。
- **受限地址比较认版本段**：清单 `accountTypes[].guardedSettings[]` 新增可选的 `versions`（如 `["/v1"]`）：核心比较受限设置时，提交值、`allowed` 各项、账号原值都先按同一规则去掉至多一个版本段，所以没有 `account:settings:custom` 的人填带版本的官方地址（如 `https://api.openai.com/v1`）也能保存。没写 `versions` 的受限设置行为不变。清单检查：每项非空、以 `/` 开头、不以 `/` 结尾、不重复。`GET /account-types` 的 `guarded_settings` 随之带出 `versions`（没写时不出现）。写了它的清单：anthropic、openai、typesafe、gemini（CONTRACTS §21.3）。
- azure-openai：资源地址按填写的保存，`/openai`、`/openai/v1` 只在请求时去掉，请求地址不变；Foundry 项目名里转义的斜杠（`/api/projects/a%2Fb`）由拒绝改为接受，作为一段保存和发出。项目名恰好是 `openai` 时，请求仍少掉项目自己那一段（与此前相同）。
- SDK：删掉 `apikey.Validator`、`Spec.KeyValidator`、`apikey.OpenAI`；`Spec.StripSuffixes` 改名 `Versions`；`NormalizeBaseURL` 去掉第二个参数；`apikeytest` 删掉 `MinKey`，改为接受带空格、非 ASCII、4096 位的密钥，并检查 `/v1`、`%2F` 原样保存。
- 各插件表单的帮助文案改为“地址末尾的版本段在请求时按接口重新拼接”一类说法。
- `apikey.Spec` 解析凭证与设置改用 `accountform`（数字按原文保留）：凭证或设置里的其他数字字段保存时不再经 float64 改写（大于 2^53 的整数不再丢精度，`1.0` 不再变成 `1`）。SDK 删掉 `apikey.DecodeObject`、`apikey.Lookup`，改用 `accountform.Decode`、`accountform.Lookup`；`standard.Request.Field` 读数字时给出原文。

#### 升级须知

- **已存账号的请求地址全部不变**（SDK 性质测试 `apikey.TestStoredBaseURLsKeepTheirRequests` 逐一比较），不用核查线上数据；只有绕过插件校验直接写进库里、路径带非规范转义的值，会从解码后发出变为原样发出（CONTRACTS §127.5）。
- 写了 `versions` 的插件要求核心 0.1.304 起（旧核心忽略这个字段，只是带版本的官方地址被拒）。
- 第三方插件用到 `StripSuffixes`、`KeyValidator`、`apikey.OpenAI`、`NormalizeBaseURL` 的第二个参数或 `apikeytest.MinKey` 的，按上面改名或删掉后重新编译。

### 审核题集：保存时拒绝运行时会丢弃的题集（moderation 0.13.0；无迁移，随 0.1.304 发布）

- moderation 0.13.0：Jev 默认题集（`jev_default_set`）保存时也按运行时的 `JevQuestions.Validate` 拒绝三种表，此前它们能存进去，运行时却被整套丢弃、静默改用内置题集：类别重复（`items/<i>/category` `duplicate`）、自定义类别（不是 OpenAI 的类别）没填名称（`items/<i>/label` `required`）、问题只有空白（`items/<i>/question` `required`）；名称与问题两条与运行时共用 `engine.go` 的函数，三条的等价由测试保证。设置页把错误逐格显示在出错那一题的对应格子里（类别、名称、问题、回答、阈值，开头说明也一样；题数不对、题里有编辑器没有的字段仍显示在题集那一行），服务端拒绝的指针也落到对应格子，改动题集后消失。`Configure` 照旧宽松（已存的这类题集仍改用内置题集，只记一条不含值的警告）。**后果**：库里已存着这类题集时，之后在任何设置页保存都会被拒，直到把题集改对（与已存越界数值的处理一致）；这时别的设置页的提示会点名是哪项已存设置不符合现在的规则、要先到它所在的设置页改对，不再只弹一条 `invalid argument`。只有 `moderation:manage`、没有核心插件管理权限的人存着 Webhook 签名密钥时，核心要求修改设置时重新填写它（`reenter`），而它只在“高级”页：在别的设置页保存时，提示如实说明本页因此无法保存，不再说成那项设置不符合规则（到“高级”页重填也不能让别的页保存，这是原有的限制）。

### 删除 growth 插件（核心；迁移 0117，随 0.1.304 发布）

所有者同意整个删除（原话“同意”），不留兼容。仓库记录显示线上从未安装过它（0.1.288 时没有装，之后没有再核实，**发布前要确认**）。规则见 CONTRACTS §125。

- 插件 `growth`（“用户增长”：邀请返利与每日签到，`next/plugins/growth`）删除，市场里不再有它；它的菜单“我的邀请”“每日签到”“增长管理”随之消失。
- 插件键 `growth` 列入已删除插件键：任何来源、任何签名的包都不能再安装，返回 403 `plugin_key_reserved`（已签名的 0.2.x 包同样被拒）。
- **迁移 0117**（`0117_drop_growth_plugin.sql`）：装过它的库像带 purge 的卸载那样删除插件行（连同版本、发布状态、宿主权限授予、事件游标）、它的权限（`growth:use`、`growth:stats:read` 与已标删除的 `growth:read`、`growth:manage`）与授予、出网域名、任务、插件迁移记录和 schema `plg_growth`（邀请码、返利记录、签到记录），键记入 `plugin_key_retirements`；没有它的库什么都不变，可以重跑。它记过的返利和签到奖励作为 `plugin_credit` 留在账本里，余额不变。

**升级须知**：装过 growth 的库升级时会被卸载（同 purge），schema `plg_growth` 连同邀请、返利、签到记录删除；要留这些数据，升级前先备份这个 schema。没装过的库什么都不变。

### 删除 volcengine 插件（核心、moderation 0.13.0；迁移 0119，随 0.1.304 发布）

所有者决定整个删除，不留兼容。规则见 CONTRACTS §128。**线上装着它，有账号**（OVH 已启用）。

- 插件 `volcengine`（火山引擎方舟：账号类型“官方通用”`apikey` 与“豆包视频”`relay`，方舟生图、Seedance 视频任务和素材库，`next/plugins/volcengine`）删除，镜像不再内置它；说明书 `PLUGIN-VOLCENGINE-ARK.md` 一并删除。用方舟模型的 API Key 升级后失去这些模型。
- 插件键 `volcengine` 列入已删除插件键：任何来源、任何签名的包都不能再安装，返回 403 `plugin_key_reserved`（已签名的 0.12.x 包同样被拒）；平台 `volcengine` 不再保留给它。
- **迁移 0119**（`0119_drop_volcengine_plugin.sql`）：装过它的库像带 purge 的卸载那样删除插件行（连同版本、发布状态、宿主权限授予、事件游标）、它的权限（`assets:read`、`assets:manage`）与授予、出网域名、任务、插件迁移记录和 schema `plg_volcengine`（素材库的本地索引；素材本身还在火山引擎），键记入 `plugin_key_retirements`；可以重跑。它的账号、使用记录、账本、审计日志保留。
- 原来随这一批发布的迁移 0118（清掉方舟 API Key 账号设置里的前缀键）一并撤回，原 0119 改号为 0118。
- moderation 0.13.0：审核钩子不再匹配 `volcengine.images`、`volcengine.video_submit`，不再读 `content`、`image` 两个字段（`gateway.hook` 的字段范围随之变小）；`prompt` 改为审 OpenAI 生图时读取（见上面“审核 OpenAI 生图的提示词”）。
- SDK：`providers/volcengine` 删除；`providers/openai.StreamUsage` 并入 `IncludeStreamUsage`。e2e 的异步视频任务用例 AC24 删除，AC43 改用 codex 账号。

**升级须知**：

- 它的账号在控制台显示为插件已卸载，不再服务请求。逐个删除，或在已退役插件键里释放 `volcengine` 并勾选同时删除账号（`POST /api/v1/plugins/retired-keys/volcengine/release?purge_accounts=true`）一次删掉。
- **升级时还在生成的视频任务失败并退款**：核心再也问不到这个插件，每次查询算一次失败，连续 20 次（`max_poll_failures`）后以 `task_poll_failed` 结束、退还预扣（失败上限关掉时到任务截止时间以 `task_timeout` 结束），客户端查到失败。想让它们正常完成，升级前等待 `SELECT count(*) FROM async_tasks WHERE plugin_key = 'volcengine' AND observation_status = 'pending'` 归零。已经成功的任务照常扣费。
- 素材库的本地索引随 `plg_volcengine` 删除，要留的话升级前备份这个 schema。

### 删除 anthropic 的模型目录（核心、anthropic 0.4.0；迁移 0120，随 0.1.304 发布）

所有者决定删除，测试换别的探针。规则见 CONTRACTS §129。**线上装着 anthropic，有 `plg_anthropic.model_catalog` 表。**

- anthropic 0.4.0 不再有模型目录：插件接口 `GET /api/v1/p/anthropic/models`、用户权限 `model_catalog:read`、表 `plg_anthropic.model_catalog` 删除；清单去掉 `database`、`routes`、`userPermissions`、能力 `http.routes.v1` 和宿主权限 `db.schema`、`routes.admin`，审查里少了它们。插件只剩 API Key 账号类型，不再用数据库。0.4.0 还没有发布，版本号不再另升。
- **迁移 0120**（`0120_drop_anthropic_model_catalog.sql`）：删除权限 `plugin.anthropic:model_catalog:read` 与它的授予、schema `plg_anthropic` 和 anthropic 的插件迁移记录（以及带核心标记的旧角色 `plg_anthropic`）；插件本身、账号不动，可以重跑。
- 测试夹具：anthropic 的升级夹具 `0.4.1-test` 只改版本号；带迁移的升级夹具改为 dynweight 的 `0.6.1-test`（`0004_config_rule_count.sql`，给 `plg_dynweight__config` 加字段并回填），市场里两个都有，真实核心升级测试（`deploy/ci/check.sh`）改由它演示内置插件随核心升级并跑迁移。e2e AC03 改为经 anthropic 账号分别请求两个节点；AC05 在流量下先后升级 anthropic 与 dynweight，查 dynweight 的迁移只跑一次、升级前保存的配置仍在并已回填；AC06 的权限与插件接口随停用变化移到 dynweight（`TestAC06_DisableGreysOutPermissionsAndRoutes`）。

**升级须知**：角色里的“查看模型目录”消失；目录是插件自带的种子数据，删除不影响调度、价格和账号的模型列表，要留的话升级前备份 schema `plg_anthropic`。

### 网关 0.1.139：停机重新部署命令 `redeploy`、插件包不再依赖主节点（与核心版本 0.1.304 同时发布，该核心要求这一版网关）

- **Release 与上线**：tag `gateway-v0.1.139`（`05638760e`，run 44798617353，北京时间 03:24:12 发布）。网关清单 sha256 `19b7181429f49f3cbdf9c16adffb7601709a0673d796b39370d9cf4314a94f67`，amd64 二进制 `c02f39bfdeccafa6f2f75cb62c91cdc12887e5e22a5c35606236b75131ec9870`。ovh 集群随核心 0.1.304 的停机重新部署一起换上（见下一节），两节点 `updater.nodes` 0.1.139；ec2pool 没有动。
- 一次性命令失败时日志写“<命令> failed”，不再写“gateway stopped”（只有 `serve` 退出才这样写）。
- **`sub2api-gateway redeploy -manifest <核心清单 URL>`（或 `-digest <摘要>`，版本已由 `import` 或更新源导入）**：计划带不过去的核心版本（Host API 变了，例如 0.1.304）停机换用。全部网关停掉后，在任一节点上以同一份配置执行一次：导入清单（同 `import`），任一节点心跳还新鲜（20 秒内，读 Redis 的实时心跳和数据库里的节点登记）、任一核心还在 Redis 核心登记里或有 running/paused 计划时拒绝（因此必须连得上 Redis）；否则把集群基线换成这个版本、删掉本集群的 `updater.node_admissions`，接着以 `serve -bootstrap` 启动主节点（按首次启动处理：允许迁移、内置插件随包导入），主节点就绪后以 `serve` 启动其余节点（主节点就绪前重启仍带 `-bootstrap`；从节点抢先启动的话要全部停掉重新 `redeploy`）。计划、步骤和事件照旧留作记录；它不停任何进程，也不碰核心的库（CONTRACTS §34“停机重新部署”）。此前这一步只能手工改 `updater.clusters.baseline` 和 `updater.node_admissions`。
- **插件包不再依赖主节点**：每个节点留一份自己存过、用过的包，缺包时向任一启用节点取，心跳新鲜的先问；只把网络上连不上的节点记为“连不上”，本机拒绝对方身份（节点目录还不认识它）记为认证失败（CONTRACTS §114）。删除节点间上传 `PUT /internal/plugin-blobs/` 与范围 `plugin-upload`。
- **`plugin_landlock_disabled`** 写 `SUB2API_PLUGIN_LANDLOCK=false` 给核心（核心 0.1.304 默认开 Landlock）。
- **仍把 `SUB2API_PLUGIN_DB_ROLE_ISOLATION` 传给 0.1.303 核心**（`supervisor.legacyCoreEnv`），先换网关、核心还是 0.1.303 时行为不变；全部集群换上 0.1.304 之后的下一个网关版本删除。
- **升级**：可以先单独换网关（`sub2api-shell yield` 后换镜像，核心 0.1.303 照常运行）；核心 0.1.304 只能按它的升级须知停机重新部署，网关 0.1.138 过不了它的预检。网关不能回退到 0.1.135 及更早。

### 核心 0.1.304 + 全部插件：平台插件转发 V2、账号协议绑定、插件数据库隔离（每插件一个身份）、删除 Claude OAuth、growth 与 volcengine、三个云厂商插件、三个上游站点插件、账号余额、OpenAI 生图（插件协议 2、Host API 10；迁移 0106–0120；**停机重新部署，核心和全部插件一起换；单向，回退要连同数据库回到升级前的备份**）

- **Release**：tag `core-v0.1.304`（`05638760e`；`92af2ee0a` 加一处 CHANGELOG 标题修正：网关 0.1.139 的标题原来两次写着“核心 0.1.304”，`changelog-notes.sh` 会把它当成核心 Release 的说明，改掉字面后两条线各取各的小节）。上游 develop 从 `af7927bd5` 快进到 `05638760e`。run 44798616699 门禁全过，北京时间 03:24:52 发布（`package` 任务的 `release` 环境审批何时通过没有观察到，两次轮询之间流水线已经完成）。清单 sha256 `af321be48f5cbb024456a3a2defca1aedc31e713d55af40b3a075925b9d81548`（amd64 bundle `0d4dde09…81e9`、arm64 `66c4164a…d4d1`，下载后核对摘要一致），strategy maintenance，Host API 10，schema `19cabc85…c100` → `6c0c081d…ae59`。内置包的 `trust.pub` 仍是 `sup2api-plugins-2026`，与线上 `SUB2API_BUILTIN_TRUST_KEY` 相同。`next/plugins/released.json` 按 `05638760e` 重写（16 个插件）。
- **发布前**：只读预检（数据库用户权限四项为真、近 30 天没有跨平台转换、没有分组设过请求体上限、`missing_price_policy` 为 reject、没有非内置插件、volcengine 没有在途任务），并在生产库副本上演练两次：第二次用生产主密钥、从网关 0.1.138 + 核心 0.1.303 的两节点集群起步、完全照网关手册“停机重新部署”，停机到主节点就绪 36.9 秒。所有者决定不做 `pg_dump`，回退依靠 Supabase 的自动备份。
- **ovh 集群**（北京时间 2026-11-21 03:32–03:34，照网关手册“停机重新部署”）：`status` 无在途计划；03:32:09.8 停两个网关（sup2api-1 03:32:12.8、sup2api-ccp 03:32:13.5 退出，`Exited (0)`），`.env` 的 `GATEWAY_IMAGE` 换成 0.1.139；03:32:35.0 在 sup2api-1 执行 `redeploy -manifest https://releases/sup2api/af321be4….json`，0.9 秒返回，基线换成 0.1.304、`revision` 572→573、准入记录清空；03:32:42.2 主节点以 `serve -bootstrap` 启动，迁移 0106–0120 于 03:32:45 应用，03:33:08.3 `/readyz` 200；03:33:15.0 启动 sup2api-ccp，03:33:38.0 就绪；03:33:48.3 去掉 override 重建主节点，03:33:57.2 就绪。**停服**：api.prophey.ai `/readyz` 03:32:10.5→03:33:08.4 约 58 秒 503/502；重建主节点时 03:33:48.8→03:33:56.9 又有约 8 秒：clicodeplus 的 Caddy `fail_duration 30s` 这时还把刚就绪的本机节点记为失败、请求落到 ovh。从节点就绪后等满 30 秒再重建主节点可以避免这一段。
- **核验**：两节点核心 0.1.304、网关 0.1.139，anthropic 0.4.0、codex 0.2.0、dynweight 0.6.0、gemini 0.4.0、mailer 0.1.5、moderation 0.13.0、openai 0.5.0 active、ready、restarts 0，newapi、sub2api、typesafe 0.1.0 只安装；7 次 rollout 都是 active。`schema_migrations` 120 条；`plg_*` schema 与旧角色（含 `plg_guard`）全部消失，新身份 `plg713988_dynweight`、`plg713988_moderation` 各自拥有 7、17 张表并以自己的身份连库，已卸载的 guard 的 6 张表归核心用户；抽查的 9 张插件表和 users、groups、accounts 等核心表，搬迁前后行数相同；PUBLIC 在库上的 `TEMPORARY` 已收回；volcengine 记入已退役键。审核与邮件设置可读（键与升级前相同），插件“数据库”页 200。经 api.prophey.ai 用临时密钥发了一个非流式、一个流式 `/v1/messages`（分组 9），都是 200、`billed`，密钥随后删除。强制检测核心 0.1.304、网关 0.1.139 无更新、无错误。主节点重建后和 sup2api-ccp 的日志无 ERROR；03:25–03:50 除这两个核验请求外无请求（392 关着，没有动）。主节点 bootstrap 那一次启动的日志随容器重建丢失，没有留存。clicodeplus 内核 6.1，Landlock 只有基础的文件层生效（`fs_truncate`、`tcp`、`scope`、`unix`、`udp` 各记一条 WARN）；ovh 内核 6.12，缺 `unix`、`udp` 两层。
- **ec2pool**：这次没有动，也没有查看（上一次记录是核心 0.1.303）；升级时照同一手册停机重新部署。`next/deploy/rollback/protocol-binding-*` 因此保留。
- 回滚：单向。核心不回退，出问题修复前进；必须回退时用网关 0.1.138、核心 0.1.303 和升级前的数据库备份一起恢复（Supabase 的备份按整个项目恢复，同一项目里的其他库会一起回到备份时刻）。

这一批在分支 `feat/forwarding-v2` 上一起发布：核心 0.1.304、全部第一方插件的新版本（`hostCompat` 都是 `>=0.1.304 <0.2.0`）、`@sub2api/ui`、`@sub2api/host`、`@sub2api/vite-preset` 4.0.0，以及一处网关改动。插件线协议从 1 升到 2，Host API 从 9 升到 10；核心迁移 0106–0120（0106 账号的协议绑定，0107 请求体上限搬进网关设置，0108 模型名的列改成 `text`，0109 发布的节点结果加拒绝代码，0110 删除插件迁移记录函数，0111 删除 Claude OAuth 插件，0112 使用记录加图片 token 两列，0113 账号余额表改形，0114 余额单位列改为 `text`，0115 账号记最近一次测试的协议，0116 删除两个账号授权的授予行，0117 删除 growth 插件，0118 删除没有读者的 `plugin_monitor_receipts`，0119 删除 volcengine 插件，0120 删除 anthropic 的模型目录），没有新的插件迁移（已有脚本随插件数据库隔离加了前缀，搬迁后不重跑）。

内容：

- **平台插件转发 V2**：每个平台插件都是有进程的代码插件，每个网关请求只有 `Execute` 一条路径，插件拿完整请求体、构造最终请求，由核心发出；请求体上限改到网关设置；插件内存默认不限；请求路径上写死的超时与限制去掉（CONTRACTS §111、§112）。
- **账号协议绑定**：账号可以限定开放的本平台协议、模型的协议；**跨平台协议转换改为默认关闭**，要在账号上为模型开启（CONTRACTS §110）。
- **插件数据库隔离**：插件的表放进核心的模式、带前缀，每个插件一个数据库身份，核心代理插件的数据库连接并以插件的身份登录（CONTRACTS §130，见上面“插件数据库隔离”）。
- **插件联网**：管理员可以指定插件能访问、不能访问的域名，管插件经核心发出的全部网络（插件自己的连接和平台插件发往上游的请求），默认行为不变（CONTRACTS §113）。
- **插件包不再依赖主节点**（网关；CONTRACTS §114）。
- **授权请求走账号表单选的代理**（代理票据，codex 0.2.0；CONTRACTS §117）。
- **代理始终按最新**：已有账号的所有出站请求（转发、取回、Monitor、测试、获取模型、额度、余额、续期、`SendAccountRequest`）都在发出时按账号**当前保存**的代理取客户端，改代理后下一次请求就走新代理（CONTRACTS §123）；建账号前的授权每次调用都取一张新票据，授权中途改代理不用重新授权，后续请求即走新代理（codex 0.2.0；CONTRACTS §117.2）。设备码授权时核心拒绝经表单代理发送、或表单代理暂不可用，界面显示原因并继续轮询，代理改好后同一个设备码接着完成，不用重新开始。
- **删除 Claude OAuth 插件**及两种账号类型（CONTRACTS §116）。
- **删除 volcengine 插件**（火山引擎方舟，CONTRACTS §128；见上面“删除 volcengine 插件”）。
- **云厂商上游**：azure_openai、vertex、bedrock 三个新插件（首发 0.1.0），openai 账号可填组织与项目。
- **上游站点插件**：newapi、sub2api、typesafe 三个新插件（首发 0.1.0，内置、只安装、默认不启用；CONTRACTS §119）。
- **账号余额接通**：声明了余额的账号类型（目前只有 newapi、sub2api）在账号列表显示上游返回的余额数字和单位本身（不做任何换算），手动刷新加定时刷新，只显示不联动（CONTRACTS §52）。
- **OpenAI 生图**：内置 openai 平台新增 `/v1/images/generations`、`/v1/images/edits`（JSON），图片 token 单独计价（CONTRACTS §118）。
- **插件按归属收到账号的全部信息**：插件收到它声明的账号类型下账号的凭证和代理详情（地址、用户名、密码），宿主授权 `accounts.credentials`、`accounts.read` 和 `GetAccountCredentials` 删除，换成 `GetAccount`；代理只是数据，请求仍全部由核心发出，插件不联网（CONTRACTS §124）。
- **插件界面只用代码写**：声明式 JSON Schema 表单与表格、表单页面删除，界面只能是插件自带的原生代码或 iframe，界面契约 4.0（见上面“插件界面只用代码写”）。
- **密钥与基础地址规则**：密钥不限格式和长度，地址按填写的保存，请求时至多去掉一个版本段（见上面“密钥与基础地址规则”）。
- 协议清理、控制台与若干修正。

#### 升级须知

**部署方式**

- **只能停机重新部署，核心和全部插件一起换。** Host API 变了，节点外壳不允许新旧核心滚动并存；托管升级也走不通（节点就绪要求每个已启用的插件都已加载，而内置插件在升级计划完成后才升级）。做法（网关 0.1.139，详见 next/deploy/gateway/README.md“停机重新部署”）：事先把 core-v0.1.304 的清单和 bundle 放进发布源、把网关 0.1.139 镜像拉到每台机器；先用 `sub2api-gateway status` 确认没有 running/paused 计划；停掉全部网关，确认各节点停止命令已返回（容器已退出），换成 0.1.139 的镜像；再等 20 秒，在任一节点执行 `sub2api-gateway redeploy`（`-manifest` 给 core-v0.1.304 的清单 URL，或 `-digest` 给已由更新源导入的摘要）；以 `serve -bootstrap` 启动主节点，`/readyz` 200 后启动其余节点；再把非内置插件逐个装上新版本。在生产库副本上按这套步骤演练过两次（第二次用生产主密钥、照网关 0.1.139 的手册走）：停网关到主节点 `/readyz` 200 约 37 秒（其中等心跳过期 20 秒，主节点启动约 13 秒），从节点再约 3 秒就绪；数据库备份另计。主节点启动时有约 30 行预期的 ERROR（旧内置插件协议不符、随后被停），看 `/readyz` 判断成败。
- **旧插件包装不上。** 按旧契约构建的包以同一条信息被拒绝（`plugin <key>@<version> was built for an older plugin protocol ...; install a version built for core 0.1.304 or later`，要求的版本是这个插件协议的第一个核心，不是当前核心；控制台文字以“请安装为核心 0.1.304 及以上构建的版本”结尾），插件列表和插件详情显示拒绝原因（`refusal`）。拒绝发生在两个时点：
  - 清单里带旧契约标志的包（`runtime: "none"`、`accountTypes[].upstream`、`requestFields`、`request.maxBodyBytes`、能力 `platform.execute.v1` / `platform.poll.v1`、宿主授权 `accounts.credentials` / `accounts.read`）在**上传、安装时**就被拒（`pkg.CheckContract`），也不留版本行；
  - 清单没有这些标志、只是二进制是旧协议的包（例如旧的 guard、payment），上传时不被发现，到**启用、加载时**握手才被拒（`grpcruntime` 的 `checkContract` 与握手）。已经装着的旧版本就是这种情况。
- **没声明 `upstreamFields` 的第三方包按新契约被拒**：每个账号类型都必须写 `upstreamFields`（没有运营方填的地址写 `[]`），没写的包上传时报字段错误 `accountTypes[i].upstreamFields` `required`（不是上面那条旧契约信息），补上这个键重新打包即可（见“插件作者须知”）。
- **非内置插件升级后停用，直到装上新版本。** 内置插件随核心镜像升级（anthropic、openai、gemini、moderation、codex 启用；mailer、dynweight、newapi、sub2api、typesafe 只安装，见 `deploy/docker/build-go.sh` 的 `BUILTIN_PLUGINS`）。其余插件——relay、guard、payment，以及装过的 azure_openai、vertex、bedrock——的旧版本在新核心上加载被拒，它们的账号不可用、功能不运行，直到逐个装上新版本。**guard 停着期间它的请求检查静默不运行**：没加载的插件不注册钩子，请求照常放行，只有插件列表上能看到拒绝原因。
- **审查里不再有账号授权**：`accounts.credentials`、`accounts.read` 已删除，声明账号类型只需 `platform.register`（CONTRACTS §124）；清单还写着它们的包按旧契约被拒（见上一条）。codex 的清单去掉了这两项和 `net`，anthropic 的清单去掉了没有代码用到的 `kv`，审查里少了它们。
- **网关也要换。** 插件包不再依赖主节点是网关二进制的改动，单发核心不会生效（核心只改了 `package_gone` 的文案）。网关滚动更新期间不要上传插件：旧网关节点上的上传仍要推主节点，主节点换新后会被拒；旧网关的主节点取不到在新网关节点上传的包。全部换完再上传、启用。

**回退**

- **单向**：迁移 0110 删除了旧插件角色记录迁移用的两个 SECURITY DEFINER 函数，之后旧核心无法再给插件建角色，持有 `db.schema` 的插件在旧核心上起不来；新核心第一次启动还会收回旧的插件数据库角色，并把插件的表从 `plg_<key>` schema 搬进核心的模式（CONTRACTS §130.8）。所以回退只能把上一版核心和上一版整套插件一起重新部署，**并连同数据库回到升级前的备份**。
- 其它迁移各自的说明（原文）：迁移 0107 只改数据，回到旧核心后请求体上限按默认 128 MB，调过的要在“环境变量”页重填；迁移 0108 不用撤，旧核心读写 `text` 列照常（旧核心自己会把模型名截到 200）；迁移 0106 不回填；迁移 0111 只在装过 Claude OAuth 的库上删东西，可以重跑。

**升级前要做的**

- **确认核心的数据库用户的权限**（插件数据库隔离，见上面“插件数据库隔离”）：以线上 `DATABASE_URL` 的用户连上线上的库，执行只读查询
  ```sql
  SELECT r.rolsuper OR r.rolcreaterole AS createrole,
         r.rolsuper OR d.datdba = r.oid OR NOT has_database_privilege('public', d.oid, 'TEMPORARY') AS temp_ok,
         has_database_privilege('public', d.oid, 'CONNECT') AS public_connect,
         current_setting('server_version_num')::int >= 160000 AS pg16
  FROM pg_database d JOIN pg_roles r ON r.rolname = current_user
  WHERE d.datname = current_database();
  ```
  四项都要为真。`createrole` 为假：给这个用户 `CREATEROLE`，否则用数据库的四个插件起不来。`temp_ok` 为假（它既不是库的属主也不是超级用户，PUBLIC 又还有 `TEMPORARY`）：新核心收不回 PUBLIC 的 `TEMPORARY`，**整个启动失败**；先由库的属主执行 `REVOKE TEMPORARY ON DATABASE <库> FROM PUBLIC`（或 `ALTER DATABASE <库> OWNER TO <核心的用户>`），再查一次。`public_connect` 为假：插件身份连不上本库，把 `CONNECT` 还给 PUBLIC。`pg16` 为假：先升级 PostgreSQL。另外确认 `pg_hba.conf` 允许 `plg<库OID>_*` 角色以密码（`scram-sha-256`）连本库（查询看不出来）。
- **导出存量的跨平台协议转换**：升级后跨平台转换默认关闭，迁移不回填，依赖它的客户端从升级完成起收到 404 `no account in this group serves this model on this endpoint`，直到管理员在账号上为模型开启；新字段升级后才存在，没法提前配置。升级前按 [升级手册](next/deploy/rollback/protocol-binding-release.md) 跑只读查询 `next/deploy/rollback/protocol-binding-conversions.sql`，导出近 30 天实际发生过转换的账号、入站协议、模型和请求数，升级后照着在控制台配置。受影响的例子：Anthropic 客户端（Claude Code 等）打到只有 OpenAI、codex 账号的分组；OpenAI 协议的客户端打到只有 Claude 账号或 Bedrock 账号的分组；Gemini 客户端打到非 Gemini 账号。原生请求、同一平台内的转换、声明了多个平台的账号类型（newapi、sub2api）不受影响。
- **请求体上限改到“系统设置 → 网关设置”**（`max_request_body_mb`，默认 128 MB，范围 1–1024）。迁移 0107 把全局层的值搬进网关设置，**分组层的值丢弃**；升级前先查哪些分组设过并确认全局值够用：`SELECT id, name, environment->'max_request_body_mb' FROM groups WHERE environment ? 'max_request_body_mb'`。只靠进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB` 设上限的实例迁移不到，升级后是 128 MB，要在网关设置里填；带着这个变量启动不报错，但它不再起作用。
- **规划数据库连接数**：插件的数据库连接（每节点每个用数据库的插件默认 4 个；现有 4 个官方插件用数据库：dynweight、guard、moderation、payment）由核心以插件自己的数据库身份代为打开，计入数据库的连接上限，不在核心的连接池里，也不再受插件角色的 `CONNECTION LIMIT 20` 约束；`max_connections` 按“节点数 ×（核心池 + 每个用数据库的插件 4 个）”规划。
- **原来把“模型没有配置价格时”设成“免费放行”的实例，升级前先给要服务的模型补上价格**：这个设置（`missing_price_policy`）已删除，没有价格的模型一律拒绝。升级后这些模型的请求在调度前收到 403 `model_price_not_configured`（请求里声明的引用模型同样）。查当时的设置：`SELECT value->>'missing_price_policy' FROM settings WHERE key = 'billing'`，为 `free` 的就是（CONTRACTS §17.3）。
- **检查旧策略下留下的未结算记录**（只读）：`SELECT count(*) FROM usage_logs WHERE billing_status IN ('pending','failed') AND expr_hash = '';` 结果不为 0，表示有“免费放行”下留下、还没结算完的记录。升级后它们不会被当作免费：结算报错，计入重试次数，用完重试次数后停在 `failed`，不会自动处理，需要管理员处理。
- **进行中的 Codex 授权要重来**：codex 0.2.0 的开始路由要 `proxy_ticket`，升级前开始、没完成的设备码或 Sign in with Codex 授权在升级后重新开始。

**升级后要做的、要知道的**

- **在账号上为模型开启需要的协议转换**（照升级前导出的结果）。升级后分组详情和密钥页列出的端点会变少，直到成员账号里有模型开了转换。
- **插件内存默认不限**：只有管理员设置时才有上限。节点封顶 `SUB2API_PLUGIN_MAX_MEMORY_MB` 的默认值从 1024 改为 0（不封顶），清单里的 `memoryMB` 只作展示；之前靠默认封顶或清单的值限住的插件升级后不再受限，要界限的在插件资源页设上限或设置这个变量。给平台插件设上限时，每个同时处理的大请求按“3 × 请求体上限 + 64 MB”留量（默认约 450 MB）。单机栈 `deploy/single/compose.yml` 不再默认传 1024。
- **严格控制出网的部署要改“插件联网”设置**：新设置默认“允许所有域名”、列表为空，升级后每个插件能访问的主机、每个平台能发往的上游都与升级前相同。这项设置管插件经核心发出的全部网络，包括平台插件发往上游的请求，所以改为“屏蔽所有域名”之前，**先把各平台的上游域名和账号上自定义的上游地址的域名都列进允许列表**（例如 `api.anthropic.com`、`api.openai.com`、`chatgpt.com`、`auth.openai.com`、`googleapis.com`、`amazonaws.com`、中转账号 `base_url` 的域名），否则这些平台的请求会全部被拒；再列出插件自己的连接要去的域名：mailer 的 SMTP 服务器，guard 的告警地址，moderation 的审核渠道和通知地址。codex 0.2.0 的授权请求由核心发出，同样要列出它的域名。改之前先在各插件的“外部访问”里看它们实际访问过哪些域名（平台插件的上游请求被放行时不记在那里，看账号的上游地址和插件清单的外部服务）。
- **旧清单里的 `usageMaxBytes`**：核心忽略这个未知字段（上传、安装、加载照常）；`sub2api-plugin` 严格解码，`build`、`pack`、`sign`、`verify`、`index`、`dev` 报 `unknown field "usageMaxBytes"`，删掉这一行即可。
- **azure_openai 和 bedrock 的字段删除在账号参数覆盖之后**：运营方在参数覆盖里写这些字段会被删掉，见下面“云厂商插件”。
- **发布了失败策略时，插件和核心自己的失败换号、不罚账号**：插件不可用（`Execute` 出错、崩溃）、核心拒绝插件交回的请求、地址被 SSRF 检查拒绝、目标主机不在清单和账号地址之内，都换下一个账号，不匹配账号规则，账号不冷却也不禁用；以前作为普通的核心 503 交给策略，没有规则匹配时第一次尝试后就回客户端 503 `plugin_unavailable`，运营方给核心来源写的规则还可能冷却或禁用账号。尝试次数用尽时结果与没有策略时一致：请求自己的上游失败照常回给客户端，其余回 503 `no_available_account`（Anthropic 格式 529）。
- **“未上报”的请求不续粘性会话、不刷新账号的最后使用时间**：未上报的记录是失败记录（`success = false`），粘性绑定只在成功时续期，`TouchLastUsed` 也只在成功时调用。
- **WebSocket 首轮的参数覆盖失败改为换号**：以前握手时忽略参数覆盖的失败、照常在这个账号上建立连接，失败留给这一轮报告；现在首轮与 HTTP 一样在发出前改写，失败时换下一个账号（`return_error` 照常回给客户端）。
- **经代理的账号不在本机解析上游域名**：只拒字面的私网、回环、链路本地地址和 `localhost`，其余由代理检查。以前本机 DNS 失败会拒绝所有经代理的账号，只有代理能解析的名字能通过账号测试、之后每个请求都失败；账号测试、拉取模型、额度查询以前经代理时完全不检查，现在按同一条规则（`netguard.CheckHostVia`，CONTRACTS §43.4）。
- **迁移 0112–0115**：0112 给 `usage_logs` 加 `image_input_tokens`、`image_output_tokens` 两列（常量默认值，只改目录；`lock_timeout` 10 秒，拿不到锁迁移失败、重试即可）；0113 改 `account_balances`（`updated_at` 可空、加 `unlimited`、删 `source`；线上这张表是空的）；0114 把 `account_balances.currency` 从 `VARCHAR(3)` 改为 `text`（只改目录）；0115 给 `accounts` 加可空列 `last_test_protocol`（只改目录）。四条都不妨碍旧核心读写，回退仍按上面“单向”的做法。
- **三个新内置插件默认不启用**：newapi、sub2api、typesafe 随镜像安装、批准，但不启用，升级后什么都不发生；要用时在插件页启用，再建账号（typesafe 启用后各节点多一个 `POST /v1/systemone`）。key `newapi`、`sub2api`、`typesafe` 和平台 id `typesafe` 从此保留给官方插件，没有官方签名的手动上传不能占用。
- **清单的 `accountTypes[].balance.currency` 删除**：币种由插件每次读余额时给出。核心忽略这个未知字段；`sub2api-plugin` 严格解码，旧清单带它时 `build`、`pack` 等报未知字段，删掉这一行即可。
- **生图模型要先录价格**：没有价格的生图模型与其他模型一样回 403 `model_price_not_configured`（缺价一律报错，见上面）。Responses 里用生图工具的请求按客户请求的模型计价，生图工具的 token 计入它（CONTRACTS §118.10），应答回显的 `gpt-image-2-codex` 等生图模型不需要价格。要按图片 token 正确计费，给生图模型录表达式价格（变量 `img`、`img_o`，参考价见 CONTRACTS §118.6）；models.dev 同步来的生图价只有文字价，会少收，要改成表达式价格。
- **协议绑定“指定”过的 OpenAI 账号要手动勾选生图协议**：`protocols` 为空（默认）的 OpenAI API Key 账号升级后自动服务两个生图端点；指定过协议的账号不会自动开放，要的话在“协议与模型”里勾上 `openai.images`、`openai.images_edit`（CONTRACTS §118.5）。
- **界面、密钥与地址、审核题集、growth、volcengine 的升级须知**见上面各自一节：带界面的非内置插件要重装，已存的越界设置值让第一次保存失败，装过的 growth 被卸载并删除 `plg_growth`，volcengine 被卸载、账号留下待删、在跑的视频任务失败退款。
- 配置 `SUB2API_PLUGIN_DB_ROLE_ISOLATION` 删除（新核心设了也不读；网关外壳仍把它传给 0.1.303 核心，见上面“插件数据库隔离”的升级须知）；环境变量 `max_request_body_mb`（全局层、分组层）删除。

#### 插件版本

全部 `hostCompat >=0.1.304 <0.2.0`（以各插件 `manifest.json` 为准；`next/plugins/released.json` 发布后按测试说明重写）：anthropic 0.4.0、openai 0.5.0、gemini 0.4.0、relay 0.4.0、codex 0.2.0、azure_openai / vertex / bedrock 0.1.0（首发），newapi / sub2api / typesafe 0.1.0（首发，内置只安装），dynweight 0.6.0、guard 0.3.0、moderation 0.13.0、payment 0.2.0（表名随插件数据库隔离加前缀，界面与设置的改动见上面各节），mailer 0.1.5（设置页改为原生界面）。codex 发给上游和授权服务器的 `User-Agent` 是 `sup2api-codex/<版本>`，随之从 `sup2api-codex/0.1.11` 变成 `sup2api-codex/0.2.0`。

### 插件按归属收到账号的全部信息，删除两个账号授权（核心、SDK、11 个平台插件、控制台；迁移 0116）

契约见 CONTRACTS §124，设计见 [设计文档](next/docs/DESIGN-2026-11-19-PLUGIN-ACCOUNT-CONTEXT.md)。

- 平台调用（`Execute`、`Monitor`、`ClassifyError`、测试、模型、额度、续期、余额）里的 `Account` 带上这次出站用的代理（`Account.proxy`：id、协议、地址、端口、用户名、密码）；`AccountProperties.proxy_id` 删除。插件看到的代理就是核心这次发请求用的代理；取不到代理（停用、不存在）时核心不调用插件：网关换号，`Monitor` 推迟，测试、额度、余额、续期记失败。
- 建账号之前在表单里拉模型：表单里填了代理（选已有的或粘贴代理串）就经它发出，插件收到的 `Account.proxy` 就是这条代理（粘贴的 id 为 0）；没填就直连，`Account.proxy` 为空；填了的代理不可用时报错，不直连、不调用插件。授权票据的路由调用不带 `Account`，插件仍看不到票据里的代理。
- `HostService.GetAccount` 取代 `GetAccountCredentials`：返回与平台调用相同的完整账号，审计 action 改为 `plugin.account.read`（只记插件、类型、账号名）。用量观察里的账号就是 `Execute` 收到的那个（含凭证）。
- 宿主授权 `accounts.credentials`、`accounts.read` 删除：声明账号类型只需 `platform.register`；`ListAccounts`、`SendAccountRequest`、`AccountScopeCheck` 不再检查授权。清单里还写着这两个授权的包按旧契约拒绝（与协议 1 的包同一条信息）。迁移 0116 删除这两个授权的全部授予行（含撤销的、暂存的）。
- 安装审核的固定提示改为写明插件收到账号的全部信息（含代理密码）、请求都由核心发出。控制台去掉这两个授权的风险等级与文案。
- 平台插件不能申请联网：声明了账号类型的清单申请宿主权限 `net` 时，清单检查报 `hostPermissions[i].id` 的 `platform_net`（上传、安装、加载同一处，此前只靠第一方清单不写），所以审核提示里“插件自己不联网”对每个能装上的包都成立。第一方插件都不受影响。
- 11 个平台插件的清单去掉这两个授权，codex 重置次数改用 `GetAccount`；codex 去掉没有写入方的 `settingsFields: ["proxy_id"]`。版本在这一批未发布的版本上继续累计。
- 升级须知：按协议 2 构建、调用过 `GetAccountCredentials` 的测试包（codex）在新核心上会得到 `UNIMPLEMENTED`，重新装这一批的包即可；第三方平台插件本来就要用新 SDK 重新构建，清单里删掉这两个授权。

### 平台插件转发 V2：每个平台插件都是代码插件，只有 `Execute` 一条请求路径

分支 `feat/forwarding-v2`。契约见 CONTRACTS §111、§112（去掉的限制与仍然有效的界限在 §112.16），插件写法见 [PLUGIN-DEVELOPMENT.md](next/docs/PLUGIN-DEVELOPMENT.md)，设计与所有者在实现期间的决定见 [设计文档](next/docs/DESIGN-2026-11-18-PLUGIN-FORWARDING-V2.md)。

- 所有平台插件都有进程：openai、gemini、relay、azure_openai 原来只有清单，现在是普通的 gRPC 插件，默认发布包里每个节点多两个进程（openai、gemini）。账号类型、表单、凭证字段不变，账号不用迁移。
- 插件构造完请求后由核心发送，插件不在流式回答里：插件崩溃、内存超限重启、升级换代不再切断在途的流（以前 codex 会）。codex 请求不再写执行记录（以前每请求三次写入）。
- 用量由插件解析的端点和任务提交：插件在交换结束后给出用量或任务；插件什么都没给（报错、崩溃、给错种类）时，同步请求记一条零费用的使用记录（`error_type = usage_unreported`，每次一条 ERROR 日志），核心不再按自己的读数补记；任务提交没有给出任务时，任务不登记、客户端报错、预扣释放。等待期间插件崩溃会切断上游，这个请求不计费。插件选了核心的读数（`host_usage`）而核心一个用量都没读到时，同样记零费用的“未上报”，不按 0 计费（不等待形式下客户端照常拿到回答）。“10 分钟内 20 次未上报就通知”没有做。
- 使用记录里“未上报”的请求显示为“未上报用量（未计费）”，详情说明插件没有返回用量；失败请求的错误类型筛选能选它。
- 运营方的请求头设置：插件的头分成默认头和权威头。透传的客户端头只补没有的；请求头覆盖和参数覆盖可以改、删默认头，改不了权威头。openai、gemini、relay、azure_openai、anthropic 的 `apikey` 只有鉴权头是权威头（与以前一样）；codex 的头全部是权威头（与以前一样）。
- “平台调用超时”只管上游出错后的错误分类（`ClassifyError`）和读断开策略，默认值和范围不变；`Execute` 不计时（见下一节）。插件出错、崩溃时请求没有发出，按插件不可用换号，不惩罚账号。设置页的说明已按这个含义改写；“平台热路径超时”的说明改成它实际管的模型解析和预扣估算。
- 安装与升级的审查页对平台插件固定显示一句：插件会收到经它的账号发出的每个请求的完整内容，并决定请求发往哪个地址（不是权限，不能取消）。
- 钩子调用因请求超过插件 gRPC 接收上限而失败的情况不再发生（插件的接收上限固定为传输层最大值），`requestTooLarge` 的熔断例外删除。
- vertex：把客户端的 `user-agent`、`x-goog-api-client` 作为默认头转发。
- 中继请求的方法与请求头只有一套规则（`sdk/protocol.CheckRelayHeaders`），核心的 `RelayUpstream`、网关发出前的检查和 SDK 都用它：头名必须小写、不能是传输层的头（含 `host`）、名和值要合法。核心以前只把头名转成小写后查传输头，现在大写头名在核心也被拒绝（用 SDK 构造器的插件本来就是小写，不受影响）。
- **请求体上限**（`max_request_body_mb`，CONTRACTS §111）：每个网关请求的请求体、WebSocket 的单条消息都按网关设置里的值限制，超出回 413（WebSocket 以 1009 关闭），保存后各节点对新请求生效。环境变量 `max_request_body_mb`（全局层、分组层）和进程环境变量 `SUB2API_MAX_REQUEST_BODY_MB` 删除，端点清单的 `request.maxBodyBytes` 删除（没有清单用过）。交给插件的请求体不按字节排队，网关设置里没有内存警告。
- **插件内存默认不限**（CONTRACTS §111.5）：插件资源页给插件设的内存上限原样生效；节点封顶默认不封顶（见升级须知）。
- **执行的恢复按存活判断**：插件解析用量的端点和任务提交在观察之后由网关每 15 秒续期，节点崩溃时恢复扫描在 2 分钟没有续期后把它关成零费用的“未上报”、释放预扣（约 3 分钟内），活着的执行不会因为时间长被关掉。
- **迁移 0107**（`0107_request_body_limit_setting.sql`）：把全局层存着的 `max_request_body_mb` 搬进网关设置，分组层的值丢弃。
- **迁移 0108**（`0108_model_name_text.sql`）：`usage_logs`、`usage_hourly` 的 `model` / `upstream_model` 和 `price_rules.model`、`model_prices.model` 改成 `text`。类型转换不重写表、不重建索引，每张表只拿一次短暂的排他锁；`lock_timeout` 10 秒，拿不到锁迁移失败，避开业务高峰执行。触发器 `usage_hourly_mark_update` 在同一事务里重建；价格模型 ID 的检查只留字符集。
- **迁移 0109**（`0109_plugin_rollout_node_code.sql`）：发布的节点结果加 `plugin_rollout_nodes.code`，记每个节点的拒绝代码；旧契约的插件首次启用就被拒、发布在准备阶段失败时，插件列表和详情的 `refusal` 从这里给出。

### 去掉写死的超时与限制

所有者决定“超时、限制都去掉”（CONTRACTS §112.16）：一次请求上留下的界限只有管理员配置的（请求体上限、上游的响应头 / 整体 / 流式空闲超时、钩子和热路径超时、平台调用超时），加上安全、钱算对和防止进程崩溃的检查。以前带着明确错误被拒绝的东西，现在表现为内存占用、存储增长或一个一直等着的请求。删掉的和没有它的代价：

- 插件在 `Execute` 里自己的工作不再计时（以前按段超过平台调用超时就以 `segment_timeout` 换号）：挂住的插件让这次尝试一直等，直到客户端离开、上游超时或插件进程结束，期间占着账号和 API Key 的槽位。等待形式（插件解析用量的端点、任务提交）的 `Execute` 在开始转发之前也随客户端离开取消（见“计费与正确性修正”）。
- 插件解析用量的端点和任务提交（等待形式）没有总时长（以前 6 小时加 `relay_timeout_seconds`，中间版本还在观察后约 44.5 分钟切断）：没配 `relay_timeout_seconds` 的部署里，一个不停的上游流会一直进行。
- 等待形式缓冲的非流式回答不限大小（以前 64 MiB 以上回 503）：大回答落临时文件占磁盘，请求结束即删。
- 插件的辅助请求（取回）不限次数和大小（以前每次调用 8 个、回答截到 8 MiB，URL、请求头、方法也有限制），没设 `relay_timeout_seconds` 时也没有整体超时（以前默认 5 分钟）：有缺陷的插件可以不停地取回，回答整份进内存。
- 任务观察的结果、等待形式存下的读数和观察不限大小（以前 512 KiB，后者超出时请求 503 且白送）：`plugin_executions` 表会随大回答变大。
- 插件发给核心的宿主调用不再有 4 MiB 上限，插件交回的请求体也不再有界：有缺陷的插件可以让核心为一次调用分配到约 2 GiB。
- WebSocket：上游单条消息不限大小（以前 64 MiB）、每个 API Key 的会话数不限（以前 64 个，第 65 个 429）、上游握手和读写没有固定时限（以前 30 秒和 1 分钟）：超大消息整条进内存，一个 Key 能开任意多会话，不读的一方由心跳 ping 结束。
- 写客户端没有时限（以前每次 60 秒）：客户端连着但不读时，请求一直占着账号和 API Key 的槽位，直到连接断开。
- 代理的拨号与 TLS 握手没有固定的 15 秒：由操作系统的连接超时和网关的响应头超时管住。
- 每个插件实例同时进行的 `Execute` 不再限 1024 个（以前满了 503）：并发只由账号和 API Key 的槽位限制。
- 交给插件的路径参数和查询参数不再截断（以前 32 个、名 64 字节、值 512 字节）；SSE 单行不限长（以前 16 MiB 断流）；Bedrock 回答流的单条 event-stream 消息不再限 4 MiB（以前超过就断流，只剩格式自己的 16 MiB）；协议转换读的回答和只流式上游攒下的内容不限大小（以前 32 MiB 回 502）：都整份进内存。
- 非流式 JSON 回答按整个回答读用量（以前只读前 32 MiB，超过的回答**按 0 计费**）：这一条是钱的修正，代价是回答在读完用量前整份留在内存里一次。
- 读用量的插件看到整份回答、全部列出的事件和请求字段的完整值，`detail_json` 不再截断（以前默认 256 KiB、64 条事件、字段 4 KiB / 32 KiB、`detail_json` 4 KiB）：插件内存和消息变大。清单原有的读用量字节预算 `usageMaxBytes` 已删除（见“插件作者须知”）。
- 模型名不限长度（以前客户端的超过 256 字节回 400，使用记录截到 200，价格模型 ID 和分组的模型名单限 200 字符）：超长的模型名整份进使用记录和日志；价格表上的唯一索引仍在，超过约 2.7 KB 的模型 ID 写价格时报错。
- 控制台的账号测试和拉取模型列表不再限 30 秒（所有者：“去掉超时，但是用户可以提前结束”；以前生图这类慢测试超时失败），控制台请求也不再限 120 秒：一直等到上游应答，或用户结束。模型测试弹窗里测试中的那一行显示“停止”，弹窗底部的“停止”结束全部进行中的测试，关闭弹窗、离开页面同样中止；被中止的显示“已取消”而不是失败。账号列表批量测试的“停止”现在会中止进行中的测试（以前只是不再开始新的），编辑页拉取模型时有“停止获取”。客户端结束的测试取消发往上游的请求，不写最近测试（CONTRACTS §15.9、§19、§112.16）。
- 对账查询把单条 30 秒预算用光（这次查询 30 秒内没有结果）算作一次失败查询，计入连续失败次数（所有者：“算”；以前当作核心取消、不计，一个每次都超过 30 秒的上游永远耗不掉失败预算，只能等到截止时间）。节点停止等核心自己取消的查询仍不计（CONTRACTS §32）。

### 安全

收尾审查发现，所有者决定“修”（CONTRACTS §112.17）。

- 核心代发的请求只发往插件清单里声明过的主机（`externalServices`，含子域，安装时管理员审查过）或这个账号自己配置的上游地址（`base_url` 这类在清单 `upstreamFields` 里声明的字段）。以前插件能把带着账号凭证的主请求发到任意公网主机，只受 SSRF 检查约束。主请求（HTTP、WebSocket 握手）发往别的主机时什么都不发出，这次尝试换号、不冷却也不禁用账号，日志 `gateway: upstream host not allowed` 写明插件和主机；插件的辅助请求、`SendAccountRequest` 回 `PERMISSION_DENIED`。九个官方平台插件的正常请求都在范围内。vertex 声明 `externalServices: ["googleapis.com"]`、bedrock 声明 `["amazonaws.com"]`，这是它们的上游所在的域，安装审查时列给管理员看。
- 插件交回的 `Host` 请求头不再生效（以前会写进请求的 `Host`，把请求指向 URL 之外的虚拟主机，而 SSRF 检查和代理选择只看 URL）：中继里出现即拒绝，请求的 `Host` 永远是 URL 的主机。运营方的请求头设置本来就改不了 `Host`。
- 配额查询、凭证续期、账号测试与模型列表请求不在这条规则里，只受 SSRF 约束（经代理时的检查见升级须知）和管理员的“插件联网”设置（见“插件联网”一节）。
- 插件取回在 `Execute` 和任务观察里用同一份实现：以前 `Execute` 里的取回允许插件改写 `Host` 头、把含代理地址的传输错误原文交给插件。

### 计费与正确性修正

审查轮发现，都在发布前修好。

- 核心代发（`SendAccountRequest`，以及共用发送路径的配额查询、凭证续期）读应答体失败（例如连接中途断开）时按传输错误交给插件，不再把半截应答体连同状态码当成完整应答交出，和取回一致。
- 插件解析用量的端点上，插件在 2xx 回答里读出业务错误时，以前已经计费却回客户端 503；现在照常发出上游的回答。缓冲回答的临时文件真的写不进时，在计费之前就按未上报关闭。
- 执行的观察没能落库时，以前什么记录都不写、预扣挂到 24 小时后才释放；现在写零费用记录、立即释放。已经有使用记录的请求再被恢复扫描关闭时不再报错。
- 任务提交的提交在重试窗口末尾才落库时，以前客户端拿到 503 而任务已登记、预留已扣；现在报失败前先确认，落了库就照常交付任务。
- 记录确定拒绝的提交（提交本身无效、身份不符、没有落库的观察，包装成 `core.ErrExecutionRefused`）不再重试满 15 秒，立即按失败处理；只有冲突以外、非这类拒绝的错误才重试。
- 任务的原账号已删除或属于别的插件时，以前一直延后到期限、按估算扣费；现在算查询失败，用完失败次数后退款。
- 零费用的“未上报”记录只有一种写法（`UsageRecord.SetUnreported`）：成功为假、没有用量和价格、不计费、不带预扣，`anomalies.usage_extract = unreported`。任务提交没能登记时的记录现在也是这样（错误类型仍是上游错误），以前只清了用量、价格和预扣，没有这个标记。
- **参数覆盖改写后的请求体也检查“账号的用量规则能否计量”**：运营方的参数覆盖（或内置覆盖）可能加进账号用量规则计量不了的操作（例如经 `context_management` 的 Anthropic compaction），以前调度只检查客户端的请求体，这样的请求不计量地发往上游；现在改写改变了请求体时，对交给插件的请求体做同样的检查，不通过就换号，和调度时的拒绝一样。插件交回的请求体仍不检查。
- **强制流式在参数覆盖之后再设一次**：以前强制流式在参数覆盖之前设置，覆盖写 `stream: false` 会把只流式的上游又改回非流式（上游回 400）；现在覆盖改变了请求体时，在结果上重新强制流式。
- **客户端离开在转发开始前取消等待形式的 `Execute`**：以前等待形式（插件解析用量的端点、任务提交）的 `Execute` 不随客户端取消，在转发前挂住的插件会一直占着账号和 API Key 的槽位直到进程退出。现在核心还没为这次执行发出任何东西时，客户端离开就取消调用；开始转发后不再随客户端取消，上游交换只由断开策略决定，插件在观察之后的工作照常提交它的结果。没有加计时。
- **插件给了分类而客户端已离开时，先按客户端离开处理**：以前先采用插件不带中继返回的分类，被客户端离开切断、又被插件分类的取回可能让账号冷却；现在先检查客户端是否离开（与 develop 一致）。
- WebSocket 首轮插件给出可重试的错误回答时，以前客户端收到 503 `no_available_account`；现在和 HTTP 一样收到这个错误。
- 插件列表的“旧契约被拒”以前靠比较错误文字识别，错误被包装或截断就会丢；现在节点记结构化的代码。插件资源页显示的生效内存上限改用运行时的同一个函数（`EffectiveMemoryMB`），不再各算一份。
- 旧契约的插件首次启用就被拒、发布在准备阶段失败时，以前插件列表的 `refusal` 是 null（节点删掉了失败的实例，只剩 `status_reason` 的文字）；现在发布结束时把每个节点的拒绝代码记在 `plugin_rollout_nodes.code`（迁移 0109），列表和详情从那里给出 `refusal`。
- 插件解析用量的端点上，插件选了核心的读数时不再每个请求记一条 WARN（只在规则一个用量都没读到时记）。
- 启用 install-only 的内置插件（mailer、dynweight 等）、重新启用被禁用的内置插件时，以前所有节点的 `/healthz` 同时变成 503，负载均衡在这段时间（发布期间加最多 5 秒；抢到内置插件安装锁的节点约 20 秒）把全部请求答成空的 503（develop 上同样存在）。内置插件的就绪检查现在只决定节点启动后何时第一次就绪，之后不再撤回（CONTRACTS §29.1）。同一个误判还让一个节点在发布期间每 5 秒跑一整轮内置插件安装，每次上传都占集群变更锁，管理员这时提交插件变更会收到 409；发布进行中的内置插件现在算已装好，不再触发安装。
- **核心读用量的端点上，上游成功应答却一个用量都没读到时记“未上报”**：以前这样的记录静默地不计费，没有标记也没有日志；现在和插件选了核心读数而读数为空时一样，记零费用的 `usage_unreported` 记录（`anomalies.usage_extract = unreported`）并记一条 ERROR，漏计费能在使用记录里查到；和其他未上报的请求一样不续粘性会话、不刷新账号的最后使用时间（WebSocket 的每一轮也是）。按次计价、`billing: "free"` 的端点、任务提交和带计费错误的记录不变，插件选了核心读数时按同一个判定（CONTRACTS §25.3）。
- **终止用量事件写出之前客户端已经断开的，不算已服务**（三种客户端断开策略相同）：判定看客户端在写这个事件之前是否还在，写之后才断开的照常算已服务。以前 `precharge`、`free` 下写之前不检查，客户端离开、上游已被取消后，网关从缓冲里读出的终止用量事件写进已关闭的连接也算送达，请求按成功全额计费，`free` 策略本该免费（CONTRACTS §25.8）。
- **发布了失败策略时 WebSocket 首轮握手失败照样重试、换号**：以前协议升级之后 gin 认为响应已经写出，策略把第一个账号的握手失败（上游 HTTP 错误、插件失败、目标被拒）一律判成 `response_committed`，直接把错误发给客户端，分组里其他账号都不试（`core-v0.1.303` 就是这样，本节和下面几处写的“握手换号”此前在线上不成立）。现在握手那一轮按 HTTP 尝试处理：策略的原账号重试、换号、出口约束都生效，核心拒绝的握手不罚账号就换号。没有策略时握手换号也解除会话粘在失败账号上的绑定（以前只放会话名额，绑定留在失败的账号上，同一会话的新连接每次先试它）。见 CONTRACTS §35。
- **流式专用上游上，非流式客户端在核心收流期间离开照样计费**：codex 账号（含 chat 适配和经 Responses 生图）这类只流式的上游，核心要先把 SSE 读完再拼 JSON 交给非流式客户端；以前客户端在这段时间离开，上游立刻被取消、记录不计费（`continue`、`precharge` 都不生效，§83 起就有）。现在已开始读 2xx 的流就算上游已应答：`continue` 读完、按上游报告的用量计费，`precharge` 按预估计费（CONTRACTS §25.8）。
- 发布了失败策略时插件和核心自己的失败换号、未上报的请求不续粘性会话、WebSocket 首轮参数覆盖失败换号、经代理的账号不在本机解析域名：见升级须知。

### 计费：一条请求一个计价模型，Responses 生图工具的 token 计入本条请求（核心、SDK 平台规则；无迁移）

- **一条请求只按客户请求的模型计价**，预扣和最终扣费都是它；绝不按上游返回的模型名或工具信息取价（CONTRACTS §17.2）。
- **Responses 里生图工具的 token 是这条请求自己的用量**：上游在 `tool_usage.image_gen` 里报的输入、输出加进这条请求的输入、输出，其中的图片 token 记为图片输入 / 图片输出（`img` / `img_o`），按客户请求的模型的价格算（CONTRACTS §118.10）。请求所用模型的价格表达式要按图片价计时，写上 `img` / `img_o`。应答回显的生图模型（例如 Codex 后端的 `gpt-image-2-codex`）不需要价格。
- 同一批里曾加入的“生图部分按另一个模型另计”（附加用量对象形式、按应答回显或默认名 `gpt-image-2` 取价、`additional_unpriced` 异常与控制台警示）在发布前删除（CONTRACTS §120.2）。

### 计费：删除“模型没有配置价格时”设置，没有价格一律拒绝（核心、SDK 设置结构、控制台；无迁移）

- 所有者决定：“删除。不要免费提供。”没有价格的模型一律报错，403 `model_price_not_configured`，没有放行途径：请求的主模型、请求里声明的引用模型都在调度前拒绝（CONTRACTS §17.3）。账号测试不查价格、不计费（所有者：“测试账号还是不要计费吧，放着就行了。”），被测模型有没有价格都可以测。
- `/settings/billing` 的 `missing_price_policy` 整项删除：接口不再返回，`PUT` 时忽略这个字段；插件读到的 `protocol.BillingSettings` 去掉 `MissingPricePolicy`；控制台“系统设置 → 计费”去掉这一项。数据库里已存的值不迁移，读取时忽略，下次保存计费设置时消失。
- 价格为 0 不受影响：照常转发，按 0 计费。
- 结算里“主价格为空即免费”的分支随之删除：带附加用量的记录，主用量的价格表达式在价格历史里找不到时结算失败，不再把主用量当免费。
- 升级须知见上文“升级前要做的”。

### Codex 账号服务 OpenAI 生图接口；同平台适配改为显式声明（核心、SDK 清单与 openai 平台、codex 0.2.0、azure-openai 0.1.0；无迁移）

- **codex 账号能服务 `/v1/images/generations`、`/v1/images/edits`**：核心把生图请求转成带 `image_generation` 工具的 Responses 请求（顶层是驱动工具的对话模型 `gpt-6-luna`，客户端的生图模型在工具里；`size`、`quality`、`background` 另写进一句 `instructions`，Codex 后端只按它定尺寸），应答转回生图接口的 JSON 或事件流（`image_generation.*` / `image_edit.*`），非流式与流式都支持（CONTRACTS §121）。不支持：`n` 大于 1、`response_format: "url"`（400 `unsupported_conversion`），edits 的 multipart（§118.1）。
- **计费与记录**：整条请求（对话部分约 2300 输入 token 加生图工具的 token）都按客户端请求的生图模型（`gpt-image-2`）的价格算；使用记录的上游模型记上游回显的生图模型 `gpt-image-2-codex`，它和驱动模型都不参与计价、不需要价格。成败以上游为准：上游成功完成就是成功，没出图时返回 200 和空的 `data`（流式没有 `completed` 事件），审核拦截的生图项也一样，记录成功、用量照扣、不算账号失败；上游自己报的错误照常处理。
- **管理员要做的**：codex 账号的模型列表加 `gpt-image-2`（新账号默认模型已含它；列表为空的不用加），给 `gpt-image-2` 录表达式价格（CONTRACTS §118.6），建议在“模型的协议限定”里把它限定到两个生图协议。`protocols` 没指定过的 codex 账号升级后自动开放两个生图协议，不想出图的账号在“本平台协议”里取消。账号测试带模型名测 `gpt-image-2` 时，若它已限定到生图协议，会真实出一张图。
- **同平台适配改为显式声明**：账号类型在平台上写了端点子集时，子集以外的端点只有在清单 `adapted` 里列出才经核心转换服务（此前对所有类型自动生效）。第一方插件已补声明：codex 列 chat 与两个生图端点，azure-openai 的“按部署”类型列 `/v1/responses`；行为与之前相同。**第三方插件**若依赖过隐式适配（子集里没有 chat 却服务 chat 之类），升级后那些端点不再服务，要在清单里补 `adapted`。
- 清单新字段：`accountTypes[].platforms[].adapted`；插件协议仍是 2。用量照 Responses 端点自己的规则读（它已经把生图工具的 token 加进来）。
- **模拟上游与 e2e**：mock 的 Responses 对 `image_generation` 工具按 Codex 后端实测的事件序列应答（生图项在前，`image_generation_call.in_progress` / `.generating` / `.completed`，图项带 `action`、`output_format`、`size`、`quality`、`revised_prompt`），提示词带 `MOCK-NO-IMAGE` 时只回文字；新增 AC42（Codex 账号经转换生图，见 `next/deploy/e2e/README.md`）；AC40 的“不服务生图”改为 Azure 部署账号。mock 的 Codex 后端（`/backend-api/codex/responses`）像真实上游一样以 400 `{"detail": ...}` 拒绝没有 `store: false` 或不是流式的请求（此前照收，回归只能靠用例自己查请求体）；AC41 的 codex 用例也检查到达 Codex 后端的请求是 `stream: true`、`store: false`。

### 账号测试按绑定的协议测（核心、SDK 清单与预设、控制台；迁移 0115）

- **测试用模型绑定的协议的真实请求**：OpenAI 预设补了 Responses、Embeddings、生图的最小测试请求，Gemini 预设补了流式生成；只绑了 Responses 或 Embeddings 的模型不再被拿去测 chat 而误报不可用。用这些预设的 openai、gemini、newapi、sub2api、azure-openai 插件随之生效（CONTRACTS §122）。对话测试请求对 OpenAI 拒收 `max_tokens` 的模型（o 系列、gpt-5 及以后，按模型名判断，与 new-api 的渠道测试同法）改写 `max_completion_tokens` 并取 16（推理占用这个上限，与 new-api 相同），其余模型照旧 `max_tokens`。
- **生图只在带了模型名时测**：清单端点新字段 `test`，`openai.json` 的生成图片标 `explicit`（会真的出一张图、花钱），编辑图片标 `never`（要输入图片）。“测试模型”里的单独测试、“测试所选”“测试全部”都带模型名，绑了生图协议的模型照常按生图协议测、真实出图；不带模型名的默认测试（账号列表的“批量测试”、“测试账号”）不会选生图协议。
- 模型在“模型的协议限定”里有条目时按条目里写的协议顺序测；不点名模型时跳过只剩生图协议的模型。
- **没有可测协议的模型不测**：被测模型在这个账号上没有可测的协议（例如只开了编辑图片）、或不点名时所有模型都没有，回 400 `no_testable_protocol` 并记为失败，不再拿默认的对话请求去测。`standard` 库收到协议列表而一个都没有测试请求时同样报错，不退回默认测试。
- 测试结果和账号“最近测试”显示实际测的协议（`protocol`；迁移 0115 加列 `accounts.last_test_protocol`，只加可空列）。

### 账号协议绑定与模型的协议限定，跨平台协议转换改为默认关闭（迁移 0106）

分支 `feat/protocol-binding`，已合并进 `feat/forwarding-v2`（合并提交 `6bef447cf`）。规则见 CONTRACTS §110，设计与实现的差异见 [设计文档](next/docs/DESIGN-2026-11-17-PROTOCOL-BINDING.md) 的“实现记录”。这一项本身只改了核心和控制台。升级影响见升级须知。

- **本平台协议**（账号字段 `protocols`）：账号编辑页“协议与模型”里可以把账号开放的本平台协议从“全部”改成“指定”，只勾需要的。例如只有 Chat 没有 Responses 的 OpenAI 兼容站点只勾 Chat Completions；不想让 codex 账号接 chat 请求就去掉 Chat Completions；去掉 Responses WebSocket 就关掉 WebSocket。默认“全部”，旧账号行为不变。指定之后，账号类型以后新增的协议不会自动开放。
- **模型的协议限定**（账号字段 `model_protocols`）：选中模型，指定它在哪些协议下提供。被列出的模型只在勾选的协议下提供；没列出的模型在账号开放的全部本平台协议下提供、不做转换。**协议转换也在这里开**：给模型勾上其他平台的协议，这个账号才对这个模型做转换。“其余模型”一行代表所有没单独列出的模型；给它开转换等于恢复升级前的行为，不建议（上游不认识的模型名可能触发故障策略冷却账号）。count_tokens 不单独配置，跟随同族的生成协议（Messages、generateContent）。
- 不受影响：原生请求；同一平台内的转换（codex 账号回答 `/v1/chat/completions`、只有 chat 的 OpenAI 类型回答 `/v1/responses`）；声明了多个平台的账号类型（newapi、sub2api）在这些平台上的请求。
- **全部账号都不提供时返回 404**：分组里提供给请求的账号全部因为“账号未开放该协议”“模型未开启协议转换”“模型在该协议下不提供”被过滤时，请求返回 404（客户端不会当成过载重试），不转换、不发上游、尝试次数为 0。组内冷却中或限时停用的账号按同样的规则判断：其中有一个开了这个协议（或为这个模型开了转换）时，它回来后会服务这个请求，仍按原来的规则返回 503（Anthropic 格式 529）；它们也都没开时照样是 404，不会因为有账号在冷却而变成可重试的错误。这个判断只读调度快照和冷却状态，不查数据库。WebSocket 首轮同样处理；已建立的连接上换成该账号在这个协议下不提供的模型，返回 400，要新建连接。
- **调度记录**：使用记录详情的调度记录多三个原因：`protocol_closed`（账号未开放该协议）、`conversion_not_enabled`（模型未开启协议转换）、`model_protocol_closed`（模型在该协议下不提供），能看到每个账号为什么没被选。这一判断排在模型白名单之前。
- **控制台**：
  - 账号编辑页：“模型配置”改名“协议与模型”，新增“本平台协议”和“模型的协议限定”两张卡；后者有矩阵、列表、JSON 三种视图，可以多选模型一次指定协议。矩阵里没有单独指定的模型，在“其余模型”一行指定了协议之后按那一行显示（标“跟随其余模型”），与网关的判定一致。
  - 账号列表：平台图标按账号实际开放的协议显示，开了转换的带“转”角标，本平台协议被限定的标“部分”。账号详情的“可服务的端点”按实际开放的显示，没开放的置灰。
  - 批量编辑加“本平台协议”“模型的协议限定”两项（替换），只对同一账号类型的账号可用。
  - 分组详情的“端点”改为按成员账号实际开放的协议显示，每个端点带提供它的账号数；原来“经协议转换还可调用”的推算删除。
  - 新建账号选类型时，跨平台的端点标“需在账号上为模型开启”。
- **接口**：账号的 GET / POST / PATCH 和列表带 `protocols`（`string[]` 或 `null`）、`model_protocols`（`[{models, protocols}]`），PATCH 各自整体替换；`GET /accounts/:id` 另带只读的 `served_protocols`（账号实际开放的协议、方式和模型；账号有模型白名单时，白名单里一个模型都不提供的协议不列出），`GET /groups/:id` 另带只读的 `served_protocols`（协议和提供它的账号数）。没有新增批量接口，也没有按协议筛选账号的参数。`GET /me/groups` 的 `served_protocols` 见“控制台与小改动”。
- **迁移 0106**：`accounts` 加 `protocols`（`text[]`，可空，空 = 本平台协议全部开放）和 `model_protocols`（`jsonb`，默认 `[]`）。不回填。
- **性能**：判断只是调度时的内存比较，不增加数据库和 Redis 访问；没有配置的账号只多一次空指针判断。没开转换的账号在转换请求体、下载媒体之前就被过滤，跨平台请求比以前便宜。
- **已知限制**：`/v1/models` 不按协议过滤，客户端可能看到当前协议下调不通的模型；账号列表没有“开放协议”筛选。验收用例 `e2e/ac19_builtin_platforms_test.go` 改成先断言默认 404、在账号上为模型开启转换后断言 200，运行情况见“验证状态”。

### 插件沙箱默认开启 Landlock：插件默认读写不了文件，也连不到不属于它的 unix socket（核心、SDK 清单、网关、控制台；无迁移）

分支 `feat/plugin-sandbox-fs`。契约见 CONTRACTS §43.8，设计见 [设计文档](next/docs/DESIGN-2026-11-18-PLUGIN-SANDBOX-FS.md)。

- **`SUB2API_PLUGIN_LANDLOCK` 在 Linux 上默认开启**（此前默认关闭，只有网关托管的核心由网关打开）。规则集按内核的 Landlock 版本处理它支持的全部限制：文件（含建 socket、fifo、链接、设备节点、跨目录移动、截断、设备 ioctl），内核 6.12 起抽象 unix socket 与给沙箱外进程发信号，7.1 起路径型 unix socket（只放行插件自己的 socket 目录），严格网络下 6.7 起 TCP、7.2 起 UDP（seccomp 之下的第二道）。
- **插件默认没有文件访问**：只放行自己的二进制、go-plugin 的 socket 目录、`/dev/null`、时区文件，获授 `net` 的插件另外能读 CA 证书，关了严格网络（`SUB2API_PLUGIN_STRICT_NETWORK=false`）时还能读 `/etc/resolv.conf`、`/etc/hosts`、`/etc/nsswitch.conf`（此时插件自己解析域名，读不到就解析不了）。去掉了 `/etc/services`、严格网络下的这三个解析文件、`/etc/ca-certificates`、`/dev/urandom`，以及以前放行的工作目录和整个私有运行目录（插件的 `TMPDIR` 不再可写）。同用户的网关管理令牌、其他插件的目录、`/proc` 都读不到（e2e 栈里用探针核对过）。
- **新宿主权限 `fs.data`**（风险中，审查页“读写本插件的数据目录”）：只有获授它的插件才有数据目录（`<SUB2API_PLUGIN_DIR>/<key>/work`，环境变量 `SUB2API_PLUGIN_DATA_DIR`，也是进程的工作目录）；授予或撤销它会重启插件进程。现有插件都不读写文件，没有插件申请它，插件版本都不变。
- **内核不支持某一层时照常启动插件**：核心启动时每层记一条 WARN（缺什么、后果、需要的内核），全部生效时记 INFO；控制台节点页新增“插件沙箱”一列（全部生效 / 缺几层 / 未开启），`GET /nodes` 带 `plugin_sandbox`。
- 网关的 `plugin_landlock_disabled: true` 现在写 `SUB2API_PLUGIN_LANDLOCK=false`（以前只是不写，核心默认开启以后就关不掉了）。
- `next/deploy/ci/sandbox-kernel.sh`：在 QEMU 里用发行版内核包跑沙箱测试，验证本机内核没有的层。

#### 升级须知

- 升级后 Landlock 默认开启。各层需要的内核：文件 5.13，截断 6.2，TCP 6.7，抽象 socket 与信号 6.12，路径型 unix socket 7.1，UDP 7.2；内核还要在 LSM 列表里启用 landlock（`cat /sys/kernel/security/lsm`），容器的 seccomp 配置要放行 `landlock_*` 系统调用（Docker 默认配置放行）。看核心启动日志里的 `plugin sandbox:` 几行或节点页确认实际生效到哪一层。
- 网关托管的节点（线上两节点）2026-10-29 起已经开着 Landlock；这次规则收得更紧（去掉工作目录、`TMPDIR`、DNS 配置文件），插件启动失败或行为异常时，先用 `SUB2API_PLUGIN_LANDLOCK=false`（网关：`plugin_landlock_disabled: true` 或 `core_env`）回到原来的不限制，再报告日志。
- 第三方插件如果读写工作目录、`TMPDIR` 或 `/etc` 下的文件：在清单的 `hostPermissions` 加 `fs.data`、只用 `SUB2API_PLUGIN_DATA_DIR` 下的路径，升版本后重新上传，由管理员同意（中风险，不会自动授予）。插件必须静态链接（`CGO_ENABLED=0`，`sub2api-plugin build` 默认如此）。

### 插件联网：管理员可以指定插件能访问、不能访问的域名（无迁移；默认行为不变）

分支 `feat/fv2-egress`。契约见 CONTRACTS §113，设计见 [设计文档](next/docs/DESIGN-2026-11-18-PLUGIN-NETWORK-ACCESS.md)。

- **新设置“系统设置 → 运行环境 → 插件联网”**：所有域名的默认设置二选一。“允许所有域名”（默认）下维护一张“已屏蔽的域名”，“屏蔽所有域名”下维护一张“已允许的域名”；列出的域名连同它的子域名生效，两张表分开保存，切换模式不会丢。添加对话框可以一次填多个，或批量粘贴（每行一个），格式不对的行逐行提示。保存后各节点对新连接立即生效（漏收广播的节点最多 10 秒），已建立的连接不断开。接口 `GET/PUT /settings/plugin-egress`（`settings:read` / `settings:manage`），每次保存记审计 `settings.plugin_egress.update`。
- **只收紧，不放宽**：插件仍须持有 `net` 授权；插件详情“外部访问”里设为“仅白名单”的插件仍只能访问它获批的域名，管理员的允许列表不会把别的域名加给它。被拒的连接和 DNS 查询照常记在插件的外部访问日志里，结果“已拒绝”，原因写明是被屏蔽还是不在允许列表里。
- **管插件经核心发出的全部网络**（所有者决定“插件的所有网络都通过核心发出，这时候来进行域名限制”，CONTRACTS §113.3）：插件自己的连接（出口隧道：Webhook、SMTP、审核渠道等），以及核心替插件发出的请求——转发（含 WebSocket 握手）、取回（含任务轮询和 codex 带代理票据的授权请求）、`SendAccountRequest`、配额查询、凭证续期、账号测试、拉取模型列表。目标主机先过清单与账号地址的规则（§112.17），再过这项设置，两道都过才发。被拒的转发什么都不发出、换下一个账号、不罚账号（原因 `upstream_host_blocked`），所有账号都被拒时客户端得到 `no_available_account`（不含主机名）；取回和 `SendAccountRequest` 回 `PERMISSION_DENIED`；配额与续期的插件看到传输错误；账号测试、模型列表显示原因。每个被拒的请求记在该插件“外部访问”的明细里（结果“已拒绝”，协议 `https`/`http`）。判断与隧道同一份缓存，请求路径上不读数据库。
- 列表按规范写法匹配：请求的主机先按保存列表时的规则规范化再比较，国际化域名的 Unicode 写法、IPv6 的非规范写法（如 `[2001:db8:0:0::1]`）、IPv4 映射的 IPv6（如 `::ffff:93.184.216.34`，列表里也按它的 IPv4 保存）不再绕过屏蔽列表，也不再在“屏蔽所有域名”下被误拒（CONTRACTS §113.2）。
- 控制台：说明文字改为覆盖两条路；切换到“屏蔽所有域名”前要确认，选中时显示警告：允许列表里没有的上游域名对应的平台请求会全部被拒。
- 以前没有全局的插件联网设置，按插件的出口策略（`allow_all` / `allowlist`）保持原样，没有要迁移的旧选项。升级要做的见升级须知。
- e2e AC37 覆盖屏蔽列表、允许列表对 guard webhook 的效果与出口日志里的拒绝原因，以及对经网关的上游请求（anthropic 账号的 `base_url`）的效果（CONTRACTS §113.5）。

### 插件包不再依赖主节点：每个节点留一份，缺包时向任一持有者取（网关；核心只改错误文案；无迁移）

见 CONTRACTS §114、[设计](next/docs/DESIGN-2026-11-18-PLUGIN-PACKAGE-ANY-NODE.md)。

- 上传、市场安装、内置插件导入只把包存在接到请求的那个节点，不再推给主节点；主节点宕机时，在其他节点上传、安装、升级插件照常进行。
- 节点缺包（新节点、丢了缓存的节点、主节点自己）时，逐个问其他启用的节点，心跳新鲜的先问、不新鲜的后问（心跳只排顺序，不排除节点：集群冷启动、从节点加入时持有包的节点心跳可能还没写或已过期），第一个给出摘要正确的包的节点胜出；摘要不符的字节被拒，继续问下一个。主节点不再特殊，`set-primary` 与插件包无关。
- 持有某个包的节点都连不上时装不了：控制台 `package_gone` 的文案改为“没有能连上的节点持有这个安装包”，节点加载失败的错误里逐个写明每个节点的结果（没有这个包，或连不上及原因）。
- 本机自己失败的取包（本机节点目录还不认识对方而拒绝、取不到本机节点凭据、请求已取消）不算“连不上”，管理 socket 回 502、核心按一般错误处理，不再报成包不存在、把人引去重新上传。
- 删除节点间插件包上传（`PUT /internal/plugin-blobs/`、范围 `plugin-upload`、每节点 2 个并发上传的限制）。`plugin_store_max_bytes` 总量上限保留。
- 升级要换网关二进制，滚动更新期间不要上传插件（见升级须知）。

### 授权请求走账号表单选的代理：代理票据（核心、SDK、UI 3.20、codex 0.2.0；无迁移）

所有者要求“账号授权时的第一次换令牌不走代理：修复bug”，粘贴的代理串也要支持。规则见 CONTRACTS §117，设计见 [DESIGN-2026-11-18-ACCOUNT-PROXY-TICKET.md](next/docs/DESIGN-2026-11-18-ACCOUNT-PROXY-TICKET.md)。

- **修复**：codex 的设备码申请、轮询和两种登录的换令牌以前由插件直连 OpenAI，从服务器本机 IP 出去，账号表单上选的代理不起作用。现在由核心经表单选的代理发出（已有代理、粘贴的代理串、不用代理三种都支持），开始、每次轮询和换令牌都按表单当时的代理选择发出；发给 OpenAI 的方法、路径、请求体不变，请求头除 `User-Agent` 里的插件版本（`sup2api-codex/0.2.0`）外不变。账号建好后的上游、刷新和重置次数本来就走账号代理（codex、vertex 已核对），不变。
- **核心**：新接口 `POST /account-types/:plugin_key/:type/proxy-ticket`（`account:create` 或 `account:update`）：与“拉取模型列表”同一段代码检查代理选择，加密成只有核心能解开的票据；粘贴的代理串只解析使用，不建代理记录，不进日志和审计。插件路由调用（有登录用户的）带执行令牌，插件在路由里可以 `Fetch`，但必须带票据、只能到清单 `externalServices`；票据只对发它的管理员和这个插件有效，每次使用按该管理员当时的权限复查。
- **@sub2api/ui、@sub2api/host、@sub2api/vite-preset 3.20**：原生账号表单多一个 prop `proxyTicket`（类型 `ProxyTicketSource`），按表单当前的代理选择要一张票据。只加不改；用到的插件声明 `hostUICompat: "^3.20"`。
- **codex 0.2.0**：清单去掉 `net`，插件不再联网；开始路由要 `proxy_ticket`；请求没有发出时会话保留（开始、换令牌回 400 `proxy_refused`，轮询回带原因的 `pending`），代理改好后接着完成。进行中的授权在升级后要重新开始。
- **e2e AC38**：三种代理选择下 codex 设备码登录的全部请求经代理（或直连）到达 mock 扮演的 `auth.openai.com`，粘贴的代理串不留代理记录，令牌建成的账号能经网关用并计费；Sign in with Codex 的换令牌经代理。mock 为此新增授权服务器的三个接口和 HTTPS 的 `CONNECT` 代理（以前 https 地址经代理到不了 mock），e2e 网络给 mock 加别名 `auth.openai.com`。

### codex：上游会话 id 以账号 id 加盐；User-Agent 与 originator 可自定义（codex 0.2.0；无迁移）

审计结论，所有者决定会话 id“加上盐进行适配”、“直接根据账号的 id 来做盐”，以及 User-Agent / originator“账号上可以自定义，然后插件的设置中也可以自定义”。规则见 [PLUGIN-CODEX.md](next/docs/PLUGIN-CODEX.md) 第 20 节。

- **会话 id 以账号 id 加盐**：发给上游的 `session_id` / `conversation_id` 改为 `sha256("<账号 id>:<api_key_id>:<prompt_cache_key>")` 排成的 UUID，账号 id 是核心里的账号编号，不发给上游。**升级后每个会话的上游 session 变一次**，上游提示缓存的落点重来一次；标识随账号不同，同一会话换到另一个 ChatGPT 账号时也会变。
- **User-Agent 与 originator 可自定义**：账号编辑页 codex 表单新增“客户端标识”两个输入框，插件设置新增同名两项；取值为账号 → 插件设置 → 内置（`sup2api-codex/<版本>`、`sup2api`），留空沿用上一级。用于这个账号发往 OpenAI 的全部请求：转发（HTTP、WebSocket）、账号测试、令牌刷新、重置次数，以及授权（设备码申请、轮询、换令牌）——授权时账号还没保存，每次调用都带上表单此刻填的值。默认值不变，不设就与以前一样。
- **凭证的 `expires_at` 写成数字时也要是非负整数**：手工粘贴的凭证里 `"expires_at": -1`、`1.5` 这类数字以前直接保存（写成字符串 `"-1"` 却会被拒），现在同样以 `expires_at`/`pattern` 拒绝。
- **`instructions: null` 按缺省处理**：Responses 请求体里显式写 `"instructions": null` 时，与没写一样补成空串再发给 Codex 后端（以前原样发 `null`），与旧版后端一致。

### 删除 Claude OAuth 插件及两种账号类型（迁移 0111）

所有者决定整个删除（原话“删除掉！”），不留兼容。线上没有安装这个插件，也没有这两种类型的账号。规则见 CONTRACTS §116。

- 插件 `claude_oauth`（`next/plugins/claude-oauth`）与账号类型 `claude_oauth`（Claude OAuth）、`claude_setup_token`（Claude Setup Token）删除，市场里不再有它；核心里写死的 Claude OAuth 余额查询一并删除。平台插件从十个变成九个。
- 插件键 `claude_oauth` 列入已删除插件键：任何来源、任何签名的包都不能再安装，返回 403 `plugin_key_reserved`（已签名的 0.3.0 包同样被拒）。
- **迁移 0111**（`0111_drop_claude_oauth_plugin.sql`）：装过它的库（例如测试实例）像带 purge 的卸载那样删除插件行（连同版本、发布状态、宿主权限授予）、它的权限 `claude_oauth:manage` 与授予、出网域名、任务，键记入 `plugin_key_retirements`；没有它的库什么都不变，可以重跑。这两种类型的账号、使用记录、账本保留，控制台显示为插件已卸载，由管理员删除。
- 删掉它之后没有使用者、暂时保留的通用能力（账号余额、额度主动查询）见 CONTRACTS §116.3。
- e2e：AC30 的平台插件矩阵变成九个插件。删除后没有重跑 e2e。

### 云厂商上游：Azure OpenAI、Vertex AI、AWS Bedrock 三个新插件，OpenAI 账号可填组织与项目（核心加响应流格式；无迁移）

分支 `feat/cloud-providers`，已在 develop 上，三个插件都已按转发 V2 改写，签名由插件自己做。**没有真实云账号，三家的行为都只对文档、黄金样例和模拟上游验证过**；没验证的事项见 [设计文档](next/docs/DESIGN-2026-11-17-CLOUD-PROVIDERS.md) 第 12 节，验证之前不要对外说“已支持”。填法见 [PLUGIN-CLOUD-PROVIDERS.md](next/docs/PLUGIN-CLOUD-PROVIDERS.md)。三个插件都是市场插件，不内置，线上从未发布过。

- **openai**：API Key 账号“高级（可选）”里新增组织 ID、项目 ID，填了就以 `OpenAI-Organization`、`OpenAI-Project` 发送并覆盖客户端自己带的；留空时行为不变。
- **azure_openai**（用 `standard` 库的代码插件）：Azure OpenAI 资源地址加 API Key（`api-key` 头）。两种类型：v1 接口（chat、responses、embeddings）和按部署的经典接口（chat、embeddings，带 `api-version`；`/v1/responses` 请求由核心转成 chat）。错误分类与 OpenAI 账号相同（openai 预设的策略）。部署不存在（404 `DeploymentNotFound`）默认原样回给客户端；要换到下一个账号，由管理员在「调度策略」的失败处理里加一条自动重试规则（来源：上游 HTTP 响应；错误码 `404`；关键词 `DeploymentNotFound`、`model_not_found`，一行一个），不配账号规则，账号不受惩罚。上游模型就是部署名，用账号的模型映射对应。客户端的 `OpenAI-Organization` / `OpenAI-Project` 和请求体里的 `service_tier`、`safety_identifier` 不转给 Azure。粘贴带 deployments、chat/completions 或 api-version 的请求地址会在保存时报错。不支持 Entra ID 令牌。
  - **顺序变化**：这两个过滤字段现在在插件里、排在账号参数覆盖之后删除。运营方在 Azure 账号的参数覆盖里写这两个字段之一时，以前发给上游，现在被删掉。
- **vertex**：Vertex AI 上的 Gemini（generateContent、streamGenerateContent、countTokens）。三种鉴权：服务账号 JSON、用户凭证 JSON（`gcloud auth application-default login`）、API Key。访问令牌由核心换取并自动续期（CONTRACTS §48），插件自身不联网。服务账号 JWT 用 `github.com/golang-jwt/jwt/v5` 签名，凭证文件由插件自己解析（Google 的库没有只签断言、不发请求的公开接口，能单独签名的 `golang.org/x/oauth2/jws` 标着 Deprecated）；`token_uri` 必须是 Google 的令牌地址，按解析后生效的值判断。不支持 Vertex 上的 Claude。
  区域填多区域 `us`、`eu` 时请求发往 `aiplatform.us.rep.googleapis.com`、`aiplatform.eu.rep.googleapis.com`（Google 的多区域端点），其余区域发往 `<区域>-aiplatform.googleapis.com`（此前多区域也按后者拼，这样的主机不存在，请求连不上）。
- **bedrock**：只服务 anthropic 平台，Claude 走 InvokeModel / InvokeModelWithResponseStream。客户端用 OpenAI 协议（chat、responses）访问时由核心先转换成 Messages 请求，走同一条路（这是跨平台转换，要在 Bedrock 账号上为模型开启）；Bedrock 自己的 OpenAI 兼容接口（`/openai/v1`）没有接，Claude 以外的 Bedrock 模型这一版不支持。三种鉴权：Bedrock API Key、长期 Access Key、临时凭证（后两种由插件用 `sdk/reqsign` 做 SigV4 签名；都不自动续期）。新建账号时预填“Anthropic 模型名 → Bedrock 模型 ID”的映射（ID 按 2026-11-17 的 AWS 模型卡片，含 Fable 5.2、Opus / Sonnet / Haiku 5.5，不含 AWS 已停止服务的 Sonnet 4 和 Opus 4），可选跨区域推理前缀（us、eu、apac、jp、au、in、global）；按 AWS 模型卡片，现行的 Claude 模型几乎都只能经推理配置调用，所以前缀默认是 `global`（预填模型里只有 Opus 4.1 没有全球配置，它的映射直接写 `us.` 开头的 ID），要把请求留在某个地域内就改选地域前缀。账号测试的默认模型是 Haiku 4.5 的全球推理配置。中国区（`cn-*`）和隔离分区的区域在保存时被拒绝。请求体的处理（`internal/bedrock` 的 `body.go`、`beta.go`、`sanitize.go`、`upstream.go`）：
  - 顶层总是删除 `model`、`stream`、`metadata`、`provider`、`service_tier`、`inference_geo`、`speed`（后三项选的是 Anthropic 自己的服务等级、数据驻留和快速模式，Bedrock 没有，地域由推理配置决定），设上 Bedrock 要求的 `anthropic_version`。顶层的 `output_config`、`output_format` 保留（旧版会删）。
  - 这些删除都在插件里、在运营方的参数覆盖之后：运营方在参数覆盖里写这些字段会被删掉。develop 上的 bedrock 0.1.0 也是由插件的补丁在参数覆盖之后删（当时只删 `model`、`stream`、`metadata`、`provider`），顺序没有变，删除的字段多了 `service_tier`、`inference_geo`、`speed`。
  - `anthropic_beta`：取客户端 `anthropic-beta` 头和请求体里要的功能，白名单 15 项（Bedrock 另有名字的改名，不支持的丢掉），再按工具自动加：computer use 工具加对应的 `computer-use-2025-01-24` / `computer-use-2025-11-24`，tool search 工具（在有 tool search 的模型上）、带 `input_examples` 或允许 code execution 调用的工具加 `tool-search-tool-2025-10-19`，有它时同时带 `tool-examples-2025-10-29`。没有对应功能的 `context_management`、`fallback_credit_token` 删除；`fallbacks` 一律删除（Bedrock 没有服务端拒答后备，Claude Code、SDK 默认带的 `"fallbacks":"default"` 不删会让每个请求 400，与旧版相同）。
  - 清洗 Claude Code 写在请求体深处、Bedrock 不接受的字段：工具定义的 `custom`；`cache_control` 的 `scope` 总是删，`ttl` 只在认出模型版本时按模型删（低于 4.5 的删，4.5 及以上只删 `5m`、`1h` 以外的值），**认不出模型版本时 `ttl` 原样保留**（删掉会把一小时缓存静默变成五分钟）；工具调用 ID 的非法字符；模型不接受的 thinking 设置。不用再在参数覆盖里手工删。
  - 是否流式只看请求的 `meta.stream`（客户端的选择），不看请求体里的 `stream`：运营方的参数覆盖给非流式请求加 `stream: true` 不会把它变成 InvokeModelWithResponseStream。
  - Access Key、API Key 账号上去掉 `x-amz-security-token`（`RemoveHeader`）：运营方经请求头覆盖加的这个头不再发给 AWS（否则签名校验失败）；临时凭证账号照常带。
  - 以上都没有用真实账号验证过。
- **核心：响应流格式**（CONTRACTS §109）：插件构造的上游请求可以声明响应流的格式（`response_stream`，现有 `aws-eventstream-chunk`，核心解码成 SSE 后照常中继、计量、转换）。核心不认识的格式、以及 WebSocket 轮次上给了格式时，这次尝试换号、不发请求。签名由插件用新包 `next/sdk/reqsign` 自己做；event stream 的编解码在新包 `next/sdk/awseventstream`。两个包用 AWS 的官方库（`aws-sdk-go-v2` 的 `aws/signer/v4`、`aws/protocol/eventstream`；只用这两个包，不用服务客户端，请求仍经核心、经账号代理发出）。event stream 里带同名头的消息按帧损坏处理。
- **核心：保存账号后立即续期到期的凭证**（CONTRACTS §48.3）：新建账号或修改凭证后，类型支持自动续期且凭证已到期时，后台马上续一次，不用等 5 分钟一轮的扫描。Vertex 账号保存后几秒内可用；其他账号类型（凭证没到期）不受影响。
- **核心**：`azure_openai`、`bedrock`、`vertex` 加入保留的第一方插件键（没有官方签名的手动上传不能占用）。
- **SDK**：新包 `next/sdk/pluginsdk/accountform`（自带账号表单的插件解码、校验、归一化的公共部分，vertex、bedrock 在用）。
- **模拟上游**新增 Azure（v1 与按部署）、Bedrock InvokeModel（含 event stream 和 SigV4 校验；凭证范围的服务名必须是 `bedrock`、区域必须是主机名 `bedrock-runtime.<区域>.amazonaws.com` 里的区域，与真实上游一样回 403 `InvalidSignatureException`）、Vertex 的路径；新增验收 `e2e/ac29_azure_test.go`（运行情况见“验证状态”）。Vertex、Bedrock 没有 docker e2e。新表单没有做过控制台界面走查。

### 上游站点插件：newapi、sub2api、typesafe（首发 0.1.0，内置、只安装；无核心迁移）

分支 `feat/upstream-plugins`。三个插件都**只对文档、单元测试、合规套件、黄金样例和模拟上游验证过，没有接过真实站点或真实 TypeSafe 账号**。契约见 CONTRACTS §119，填法见 [PLUGIN-NEWAPI.md](next/docs/PLUGIN-NEWAPI.md)、[PLUGIN-SUB2API.md](next/docs/PLUGIN-SUB2API.md)、[PLUGIN-TYPESAFE.md](next/docs/PLUGIN-TYPESAFE.md)。

- **newapi**：New API 站点的 Key（站点地址加 Key），一个账号同时服务 openai、anthropic、gemini，以及 typesafe 插件启用时的 typesafe 平台；同协议原样转发、各协议用官方鉴权头（Gemini 只用 `x-goog-api-key`）；Gemini `countTokens` 不转发，由核心本地估算；Responses WebSocket 和 Anthropic `count_tokens` 照转（以 touwaeriol/new-api 的 `codingplus` 分支为准，上游 New API 没有这两条，接那类站点要用协议绑定关掉）。余额读 `/api/usage/token/`，显示站点的额度点数本身，没有单位（令牌限额、取用户额度、不限三种）。
- **sub2api**：另一个 Sub2API 站点（旧版或 sub2api-next）的 Key，服务 openai、anthropic、gemini，同协议原样转发；余额读站点的 `/v1/usage`（`remaining`、`balance`、`quota.remaining` 依次取，单位是 `unit` 原样，旧版订阅分组没配限额（`subscription` 存在且 `remaining` 为 `-1`）时为不限（钱包余额恰好是 `-1` 照数字显示），`isValid: false` 为认证被拒）。上游 Key 能用哪些协议看它在站点上的分组，用协议绑定收窄。
- **typesafe**：新平台 `typesafe` 与端点 `POST /v1/systemone`（TypeSafe 的 Jev 接口，只有非流式），请求原样转给官方地址（可在有权限时改地址），危险区两个请求头透传，用量读 `usage.input_tokens`。价格由管理员录（参考输入 $0.042 / 百万 token，输出 0）；没有余额接口。
- 三个插件都不申请 `net`、`fs.data`：请求由核心发出，在默认开启的文件沙箱下运行。
- **模拟上游与 e2e**：mock 新增两个站点的余额接口、OpenAI 生图接口、TypeSafe 主机名下的模型列表（`api.typesafe.ai` 加进 e2e 网络别名）；AC30 加 newapi、sub2api、typesafe 三行和 New API 账号转发 Jev，新增 AC39（账号余额）、AC40（OpenAI 生图）、AC41（Responses 生图工具的 token 计入请求模型，一条记录、一笔扣费、没有另计分项）；mock 的 Responses 对 `image_generation` 工具回 `tools`（模型 `gpt-image-2-codex`）与 `tool_usage.image_gen`。

### 账号余额接通（核心；迁移 0113、0114）

- 账号余额显示上游返回的数字和单位原文，不做任何单位或汇率换算（2026-11-18 所有者决定，CONTRACTS §52）；上游没给单位时只显示数字，核心不再要求单位是三位大写币种代码。New API 插件余额只请求 `/api/usage/token/`，显示站点的额度点数。Sub2API 插件余额的单位取上游 `unit` 原文，没有时不带单位。
- 声明了余额的账号类型由插件描述查询（`BuildBalanceRequest` / `ParseBalanceResponse`）、核心经账号代理发出：每个目标先过清单主机规则与插件联网（有一个被拒就一个都不发），不跟随重定向。控制台手动刷新（`GET /accounts/:id/balance?force=true`），集群任务 `core.account.balance_sweep` 每分钟按各类型的 `updateInterval` 读到期的账号（启用且可调度的和从没读过的；停用的（不论调度开关）和关了调度的只在手动刷新时读）。额度的定时刷新同样只刷新启用且可调度的账号：以前两者都只看调度开关，停用而调度开关仍开着的账号（`PATCH status` 不动调度开关）照样每个周期向上游查询。**只显示，不停用账号、不影响调度、不告警。**
- 改账号的 Key、设置或代理时正在进行的余额查询（定时任务或另一个节点的手动刷新）用的是旧凭证，结果不再写库；查询结果提交后只删缓存键，由下次读取从库回填，账号行锁住期间不再等 Redis：以前它在修改删掉快照之后写回，旧 Key 的余额当成新 Key 的显示到下一个周期（CONTRACTS §52.3）。
- 控制台余额格显示未查询、金额、不限、查询失败（带上次的金额）；余额显示为“数字 + 单位”（如 `7.5 USD`、`1,250,000`），不再显示货币符号；前端余额刷新不再有 2 秒冷却。
- 迁移 0113：`account_balances.updated_at` 可空、加 `unlimited`、删 `source`。清单 `accountTypes[].balance.currency` 删除（见升级须知）。迁移 0114：`account_balances.currency` 从 `VARCHAR(3)` 改为 `text`。
- 账号测试按账号的协议绑定把开放的协议按顺序传给插件（`BuildTestRequestRequest.protocols`），只开放了某些协议的账号测的是它真正服务的协议。

### OpenAI 生图（核心、openai 0.5.0；迁移 0112）

- 内置 openai 平台新增 `POST /v1/images/generations`、`POST /v1/images/edits`（协议 `openai.images`、`openai.images_edit`）。**只收 JSON 请求体**，表单上传（multipart）形式在网关回 400 `request body is not valid JSON`、留一条免费记录，暂不支持。OpenAI API Key 账号可转发；azure_openai 的账号不服务这两个端点（codex 账号经核心转换服务，见上面“Codex 账号服务 OpenAI 生图接口”一节）。
- 用量记录图片输入 / 输出 token（迁移 0112，`usage_logs` 两列，`/usage` 列表与详情返回），价格表达式变量 `img`、`img_o` 生效：用到时从 `p`、`c` 里扣出，没用到时按文字价算，不会免费也不会重复计。控制台用量详情显示图片 token 行，价格试算可填 `img`、`img_o`。
- 没有价格的生图模型回 403 `model_price_not_configured`（缺价一律报错，CONTRACTS §17.3）；参考价见 CONTRACTS §118.6，models.dev 同步的生图价只有文字价，会少收。
- openai 插件默认模型加 gpt-image-2、gpt-image-1.5、gpt-image-1-mini。已经手工指定过协议的 OpenAI 账号升级后要手动勾选两个生图协议。
- Responses（含 WebSocket）请求使用 `image_generation` 工具时，工具的 token（`tool_usage.image_gen`）计入这条请求的用量，按客户请求的模型计价（见上文“计费：一条请求一个计价模型”，CONTRACTS §118.10）。ChatGPT（Codex 登录）账号经 Responses 的生图工具可以生图（2026-11-18 在设备码账号上实测）。

### 控制台与小改动（无迁移）

- 提交插件变更时，若另一项集群变更（插件变更或核心升级计划）正在提交，返回 409 的错误码从 `conflict` 改为专用的 `cluster_change_busy`（信息不变），控制台显示本地化的“请稍后重试”；e2e 上传插件按这个错误码重试，不再看信息里有没有 retry。上传时同一插件的首次上传恰好与另一处（某节点首次安装内置插件，它不取集群变更锁）同时建插件行的 409（`the plugin is being uploaded concurrently; retry`）也改用 `cluster_change_busy`，同样稍后重试即可。“核心升级进行中或已暂停”仍是 `conflict`。
- `GET /me/groups` 每行带 `served_protocols`（`[{protocol}]`，与分组详情同一计算，不带账号数，整页一次查询）。密钥页的手动配置说明和密钥编辑窗口里的分组说明改按它列端点，与分组详情一致，不再列出账号没开放、实际会 404 的端点。
- **网关设置读出来原样保存不再 409**（分支 `feat/fv2-rdfix`，真实数据测试发现）：发布了失败策略后，`PUT /settings/gateway` 带着与当前值相同的 `max_attempts`（例如把 GET 的结果原样写回）照常保存；改成别的值仍返回 409 `managed by settings/failure-policy`（CONTRACTS §58、§14.4）。控制台的网关设置页本来就不提交这个字段，不受影响。
- **账号测试不指定模型时用账号自己的模型**：以前一律交给插件选默认模型（Anthropic API Key 是 `claude-haiku-4-5`），账号的模型列表里没有它时测试必然失败。现在账号有模型列表时测列表里第一个，没有列表但有模型映射时测映射里按字典序最小的请求模型（再经映射），两者都没有才由插件选（CONTRACTS §15.9）。账号列表的“测试”、批量测试和模型测试对话框里的“用默认模型测试”都按这个规则；对话框里这个按钮改名“用默认模型测试”。
- **使用记录详情重新逐条显示另算的分项**：计费详情里列出 `billing_detail.additional`（如请求里声明的 advisor 顾问模型）和 `billing_detail.replacement` 的每一项：种类（替代计费项按尝试轮次显示“第 N 轮”）、模型、该项的 token 用量、它自己的计算公式和金额；免费项标“免费”并显示原因。主用量公式以主用量自己的金额结尾，下面一行“主用量小计 + 各项 = 合计”；为此核心在有附加用量的记录明细里另存主用量的 `primary_cost`、`primary_list_cost`、`primary_total_cost`（只是把算出来的数存下，金额不变；之前结算的记录没有，详情里主用量公式只显示算式，CONTRACTS §120.3）。主用量被替代计费用量替换时不再画出“$0 × 0”的主用量公式。数据一直在记录里，之前随“生图另计价”一起删掉了这块显示；没有分项的记录不显示。
- **`/me/usage` 不再露出分项背后的全局价**：分项按分组自定义价格计价时，它明细里的 `detail.inputs.pricing.base`（全局价表达式与哈希）现在和顶层的一样在普通用户的使用记录详情里删去。
- **用 http 打开控制台时“系统 → 升级”页不再空白**：页面用了只在 HTTPS / localhost 下才有的 `crypto.randomUUID`。升级页和余额调整的幂等键改用 `@sub2api/ui` 已有的 `createItemId`（`crypto.getRandomValues`，非安全上下文可用），eslint 禁止再直接用 `crypto.randomUUID`。
- **本地构建镜像不再带进旧的界面产物**：`next/Dockerfile.dockerignore` 原来排除的 `web/dist` 不是任何产物目录，开发机上 `npm run build` 留下的 `server/web/dist/assets/` 和插件 `ui/native/dist/` 旧文件会随构建上下文进镜像和插件包；现在排除这两处，由镜像的 ui 阶段重新构建。CI 与发布用干净检出，不受影响。

### 插件作者须知：SDK、清单与 proto 的变化

插件线协议 2、Host API 10。删除的字段编号和名字都写进 `reserved`，不复用（照 §91.3、§117.4 的先例）。

- **删除**：`PlatformService.BuildUpstreamRequest`（声明字段加补丁）、`ExtractUsage`、`ParseTaskSubmission` 作为核心调用、`Poll`、`BuildReconcileRequest` / `ParseReconcileResponse`；`HostService.ForwardUpstream`、`RecordUsage`、`ReserveAndWatch`、`ReportTaskProgress`；`RequestSigning`（核心签名）；清单的 `runtime: "none"`、`accountTypes[].upstream`、`requestFields`、`request.maxBodyBytes`，能力 `platform.execute.v1`、`platform.poll.v1`；SDK 的 `SetPatch` / `DeletePatch`、`WithMaxRecvMsgSize`；`sdk/upstream`、`sdk/platforms/upstream`。
- **新的写法**：实现 `UpstreamBuilder.BuildUpstream`，让 `Execute` 是 `pluginsdk.ExecuteDefault`。插件拿到完整请求体（转换、模型映射、参数覆盖之后），用 `NewUpstream` 构造最终请求（`Header` 权威、`DefaultHeader` 默认），经 `Relay` / `RelayAndWait` 交给核心（主机必须是清单 `externalServices` 或本账号的上游地址，不许设 `Host`；`RemoveHeader` 表示不发某个头，CONTRACTS §112.17）；辅助请求用 `Fetch`（主机规则同上，`manifest.UpstreamHostAllowed`；不限次数和大小，`FetchLimit`、`FetchResponseLimit` 已删除）；用量与任务只在返回值里（`ReportHostUsage`、`ReportUsage`、`ReportTask`、`ReportClassification`）；任务观察实现 `TaskMonitor.Monitor`，结果是返回值。签名用 `sdk/reqsign` 自己签；改请求体只用 `sdk/pluginsdk/jsonedit`；不开套接字、不用厂商 SDK 客户端、不重试。
- API Key 加基础地址的账号类型用新库 `sdk/pluginsdk/standard` 和预设 `providers/{openai,anthropic,gemini}.APIKey`；原声明式引擎的 1498 条黄金样例冻结在 `standard/goldentest`。
- **余额与多平台组合**（上游插件一批）：新增可选接口 `pluginsdk.BalanceReader`；`standard` 库加 `Merge`（几份预设合成一个账号类型）、路由级 `Auth` / `Classify`、`Type.Tests`（按账号的协议选测试请求）、`Type.Balance`（余额请求与读法，带 `BalanceFailure` 与 `Micros(number string) (int64, bool)` 两个辅助）；`pluginsdktest.Harness.Balance` 不要求币种；新预设 `providers/typesafe`（平台定义的唯一来源 `Platform()`）。合规套件新增规则 `12_balance` 和 `Plugin.Platforms`（别的插件声明的平台）。
- 一致性套件 `sdk/pluginsdk/pluginsdktest/conformance`（11 条检查，上游插件一批加第 12 条余额），十一个平台插件都调用它；`Plugin.Manifest` 必填（交给插件的客户端请求头从清单读），`Plugin.PassHeaders` 删除；每个协议的流式 / 非流式形式由套件从端点定义读出，插件不能声明自己不服务某种形式；WebSocket 协议只归第 11 条。只有 bedrock 有两处例外（InvokeModel 按地址选流式、不收 `anthropic-beta` 头）。
- 假宿主 `pluginsdktest`：`Harness.Execute` / `Harness.Monitor`，`FakeHost.Begin` / `BeginMonitor`；取回的主机按 `FakeHost.Manifest`（`Start` 时取 `Options.Manifest`，同一份清单也经 `pluginsdk.WithManifest` 交给插件）判断，不再默认放行所有主机；和宿主一样不限请求体、不数取回、不截回答，`Monitor` 的终态要有一次完整成功的取回。`MaxBodyBytes` 删除。假宿主对中继也执行主机规则（以前只管取回），测试里没给清单或指向 `127.0.0.1` 的假上游要设 `FakeHost.Manifest` / `ExternalServices`；`standardtest.Run` 回放黄金样例时同样检查主机。
- 清单：账号类型写 `"passHeaders": []` 表示不交给插件任何客户端请求头（以前空列表会回落到平台的列表）；没写仍取平台的。跨插件事实的判断统一为 `AccountPlatform.ServesPluginFacts`，内置平台不参与。没有使用者的 `TransportHTTP`、`TransportWebSocket` 删除。声明账号类型只需宿主授权 `platform.register`（§124 删除了 `accounts.credentials`、`accounts.read`，清单写着它们的包按旧契约被拒）；请求能发往的账号地址只认清单的 `upstreamFields`（原来声明式类型自动得到 `base_url`）。**每个账号类型都必须写 `upstreamFields`**（没有运营方填的地址写 `[]`），没写的清单检查报 `accountTypes[i].upstreamFields` 的 `required`；没写时把 `base_url` 和 `guardedSettings` 的字段当作地址、没有 `guardedSettings` 就算可自定义上游的旧规则删除（`manifest.AccountType.AddressFields` 一并删除，判断地址是否改动、能否自定义上游、请求能发往哪里都只看 `upstreamFields`）。缺这个键是普通的字段错误，不算旧契约标志，不报 `outdated_contract`（CONTRACTS §21.3、§112.12）。全部第一方插件本来就声明了，不受影响。`manifest.AccountType.PassHeaders`（为已删的上游模板 `{client_header.<名>}` 服务）删除，客户端请求头按平台逐个读 `AccountPlatform.ClientHeaders`。
- **清单的 `usageMaxBytes` 删除**（端点读用量的观察的字节预算，没有插件声明过它）：观察总是整份。旧清单写着它时的表现见升级须知。
- `standard` 库：`Type.PassHeaders`、`PlatformPassHeaders`、`Auth.Query`、`Route.Method` 删除（核心已按清单过滤客户端头，库把交来的都写成默认头；HTTP 路由一律 POST）；`Route.Edit` 改为 `func(*jsonedit.Doc, *Request)`，和过滤字段在同一次改写里；预设不再带过滤字段（azure_openai 自己写）；流式判断用 `Request.Stream`（`pluginsdk.RequestStream`：`meta.stream` 或请求体的 `stream` 为真），只流式类型的非流式请求也会加 `include_usage`；黄金样例用 `standardtest.Run(t, plugin, manifestPath, p)` 回放。`pluginsdk.WebSocketURL` 是 http 地址换成 ws 地址的唯一实现（codex 和 `standard` 库共用）。
- 账号表单辅助：`accountform.DecodeForm` 拒绝不是 JSON 对象的凭证或设置时错误码由 `invalid` 改为 `invalid_json`，与 `apikey.Spec` 相同（bedrock、vertex 随之变化）。`apikey.Texts.KeyNotStringCode` 删除：设了 `KeyNotString` 就报 `pattern`（只有 openai、anthropic 预设设它，冻结的黄金样例钉住了这个码），没设报 `type`。`providers/anthropic` 无人调用的 `Path`、`Headers` 删除。清单检查包的 `check.ParseRouteRef`（转调 `manifest.ParseRouteRef`）删除，`check.OutdatedContract` 不再导出（包级判断用 `check.OutdatedPackage`）。
- `jsonedit`：同一成员上的改写按调用顺序生效（以前先 `Set` 后 `Delete` 会留下这个键）。`ValidateUpstreamRequest` 与宿主用同一套方法和请求头检查。
- `SendAccountRequest` 能发往账号自己的上游地址（以前只认 `externalServices`），请求体、URL、方法不限大小，回答整份交还（以前截到 64 KiB）；配额查询、凭证续期的回答同样不再截断。
- **proto 清理**（转发 V2 收尾，删除没有使用者的字段）：
  - `EstimateUsageRequest.fields_omitted`（3）、`ExtractUsageRequest.fields_omitted`（9）删除。核心自转发 V2 收尾起就不再填它，唯一读过它的火山插件也已不读。
  - `UpstreamOptions` 消息及 `UpstreamRequest.options`（8）、`ExecutionHTTPRequest.options`（6）删除。它只能收紧核心的响应头超时和整体超时，现在超时只由核心（账号与管理员的设置）决定。核心里对应的 `execfetch.Tighten` 一并删除。
  - `GetDSNResponse.role_isolated`（3）与 SDK 的 `DSNInfo.RoleIsolated` 删除：插件一律经虚拟地址、以自己的数据库身份连接（CONTRACTS §130），这个字段没有意义了。`sub2api-plugin db diff|check`（`pgdbtool.Drift`）以临时的 `NOLOGIN` 角色在临时 schema 里执行迁移（只有这个 schema 的 `USAGE`、`CREATE`，`search_path` 指向它，代替宿主以插件身份执行时的核心模式），它在 schema 之外拥有的对象按属主列为差异，每个脚本之后再列出宿主会拒绝的对象（不带前缀的对象，表、索引、序列、视图、类型以外的任何对象；见上面“插件数据库隔离”的“打包工具”）。
  - `truncated` 全部删除：`ExecutionHTTPResponse.truncated`（5）、`ParseQuotaResponseRequest.truncated`（6）、`ParseRefreshResponseRequest.truncated`（6）、从没有核心调用方的 `ParseBalanceResponseRequest.truncated`（6）、随 `usageMaxBytes` 删除的 `ExtractUsageRequest.truncated`（7）。回答不再截断，codex、vertex 已不读它们。`ExecuteHTTP` 的回答读不完整时照旧是 `transport_error`。
  - 代理票据新增 `HTTPRequest.execution_token`（10）、`ExecutionHTTPRequest.proxy_ticket`（7）。SDK：路由处理函数的 context 绑定执行令牌，`pluginsdk.Fetch` 在路由里可用并传 `ProxyTicket`；假宿主的 `Harness.DoAs` 发令牌（`FakeHost.BeginRoute`）。

### 验证状态

- 测试结果以各工作包的交付说明为准。
- e2e 新增 AC30–AC36（平台插件经 `Execute`、运营方设置、换号与未上报、旧契约的包被拒、Responses WebSocket 逐轮、请求体上限设置、以官方 OpenAI 与 Anthropic Go SDK 为客户端）、AC37（插件联网）、AC38（codex 授权走表单代理）；e2e 栈的部署也按现在的核心和插件改过（两节点共用插件包卷、默认镜像版本 0.1.999、厂商主机由 mock 以别名和自建 CA 扮演），运行方法见 `next/deploy/e2e/README.md`：整套约 27 分钟，`test.sh` 默认 `-timeout=150m`（直接 `go test` 时自己带上）。
- **已经跑过的**：整套 e2e 当时的全部 49 个顶层用例（`next/e2e` 里的全部 `func Test`，含 AC19、AC29、AC30–AC36；当时 AC30 还是十个平台插件）在协议绑定与转发 V2 合并后的代码上运行，全部通过（记录于 `732587ea9`）。
- **之后没有重跑整套 e2e**：此后的修正（上面“计费与正确性修正”里参数覆盖计量检查、强制流式、客户端离开、确定拒绝不重试、未上报记录、经代理不本机解析、失败策略下换号、客户端离开优先等）、删除 Claude OAuth（AC30 改成九个平台插件；删除后没有重跑 e2e）、新增的 AC37、AC38 都在那次运行之后，没有整套运行的记录，AC37、AC38 也没有单独运行的记录。现在共 51 个顶层用例（其中 43 个 `TestAC*`，编号到 AC38）。
- 三家云厂商只对文档、黄金样例和模拟上游验证过，没有接过真实账号；新表单没有做过控制台界面走查。
- 1、8、32、128 MiB 请求体的延迟和内存没有测量，已部署实例的停机升级步骤没有演练过（设计文档第 15 节）。

### 到 Gemini 的媒体下载可经环境 HTTP 代理（核心；无迁移；默认行为不变）

所有者决定“图片直连下载（或者环境http代理）”。规则见 CONTRACTS §88.5。

- 协议转换到 Gemini 时核心下载客户给的媒体链接，以前只直连。现在核心进程环境里配了 `HTTP_PROXY` / `HTTPS_PROXY`（或小写写法）时经这个代理下载，`NO_PROXY` 里的目标仍直连，规则同 Go `http.ProxyFromEnvironment`；没配就和以前一样直连。仍不走账号代理。
- SSRF 防护：直连照旧；经环境代理时和经账号代理同一条规则——本机不解析目标，字面私网地址与 `localhost` 拒绝，每跳重定向都查；代理自己的地址可以是内网地址。域名（含非规范的 IPv4 写法）由代理解析：环境代理应当自行拒绝访问内网地址；这一层核心无法检查。
- 经环境代理下载失败时，返回给客户的原因不含代理的地址（原始错误只记在服务端日志）。
- 这些变量外壳本来就传给核心（进程基本变量），不用改部署；设了它们，插件市场与插件包下载本来也经它。账号上游请求、账号测试、额度查询、价格同步等不读环境代理，插件进程也拿不到这些变量。

## 2026-11-18

### 核心 0.1.303：账号请求头覆盖（兼容 new-api）与参数覆盖补齐、通知测试同步发送、默认模型加上 gpt-6.2 / Claude Fable 5.2 / Gemini 3.5–4（`6d6b2caa3`，codex 0.1.11、openai 0.4.3、anthropic 0.3.4、gemini 0.3.2；迁移 0105，主节点优先）

- **Release**：tag `core-v0.1.303`（`4414334c8`，即 `6d6b2caa3` 加本节说明）。第一次运行（run 44172908784）在门禁 `update coordination race checks` 失败：网关测试 `TestUpgradeWakeupsReconnectAfterRedisRestart`（-race 下 5 秒内没等到订阅重连）偶发失败，网关代码自 0.1.302 没有改动，同一提交 develop 的 next CI（run 44172888097）通过；jkjk02 没有重跑权限，于是删掉 tag 后在同一提交重推，第二次运行（run 44174717893）全过，`release` 环境已没有审批规则，北京时间 05:12:32 发布。清单 sha256 `d97809231015c5269addc248f406e8db3851bba4f2f222c4c8c97564a92100a8`（amd64 bundle `cf96d8c6…44c0`、arm64 `56eeb4b1…2b08`，与 GitHub 资产摘要一致），strategy maintenance，schema `50a07a21…2437` → `19cabc85…c100`。`next/plugins/released.json` 按 `4414334c8` 重写（anthropic 0.3.4、claude-oauth 0.2.5、codex 0.1.11、gemini 0.3.2、openai 0.4.3、relay 0.3.2）。
- **ovh 集群**：Release 清单与 bundle 核对摘要后导入，预检 primary-first-v1、无阻断（expected_revision 570），计划 `3cb837e48d67124fdaff65c5641336d2` 05:13:20→05:14:23（ccp 先转发主节点、停止，主节点停机迁移启动，再启动 ccp 并恢复 local）；**停服**：ovh 3130 与 api.prophey.ai `/readyz` 503 05:13:34.0→05:13:43.0 约 9 秒；迁移 0105 于 05:13:38 应用（`accounts.header_override` jsonb 可空，129 个账号都为空）。内置同步 05:14:33–05:14:37 把 anthropic 升到 0.3.4、codex 0.1.11、gemini 0.3.2、openai 0.4.3。两节点核心 0.1.303，8 个插件 active、ready、restarts 0；强制检测核心 0.1.303 无更新、网关 0.1.138 无更新、无错误。日志：sup2api-ccp 无 ERROR；sup2api-1 只有 05:13:34.4 旧核心排空退出时的一条 river `PeriodicJobEnqueuer: Error starting transaction`（`context canceled`），新核心起来后无 ERROR；05:00–05:30 无请求（392 关着）。
- **ec2pool**（单节点）：预检 primary-first-v1、无阻断（expected_revision 9），计划 `4e649809f536490fe8b9dc0f9da79522` 05:15:28→05:15:34，`/readyz` 503 05:15:31.3→05:15:35.3 约 4 秒；迁移 0105 于 05:15:33 应用。核心 0.1.303，anthropic 0.3.4、codex 0.1.11、gemini 0.3.2、moderation 0.12.1、openai 0.4.3 active、ready、restarts 0（05:15:37 内置同步；dynweight、mailer、volcengine 仍只安装未启用）；两个 codex 账号（`codex_device`，id 2、3）active、可调度；强制检测无更新；日志无 ERROR。
- 回滚：schema 已变（迁移 0105），核心不回退到 0.1.302，出问题修复前进；插件只升不降。
- 内置集合里 anthropic 0.3.3→0.3.4、codex 0.1.10→0.1.11、gemini 0.3.1→0.3.2、openai 0.4.2→0.4.3，其余（dynweight 0.5.11、mailer 0.1.4、moderation 0.12.1、volcengine 0.12.8）不变。claude-oauth 0.2.5、relay 0.3.2 不在内置集合，随源码记入 `released.json`，线上没有安装。网关、runtime-contract 没有改动，不发新网关（仍 0.1.138）。
- **控制台版本徽标**：打开徽标时重新读取版本和更新检测，两者都返回后才提供一键更新；点更新前先复查，这一版本已不可用时不发请求，只提示当前状态。此前跨过一次服务端升级仍开着的页面，会继续提供已经装上的更新。

#### 账号请求头覆盖（兼容 new-api）与参数覆盖补齐（迁移 0105）

- **请求头覆盖**（CONTRACTS §108）：账号新增“请求头覆盖”（接口字段 `header_override`），格式与 new-api 渠道的请求头覆盖相同，new-api 的配置可以直接粘贴。键是请求头名，值是固定文本或占位符：`{client_header:名}` 取客户端的这个头（没带就不设），`{client_header:名:追加值}` 在客户端的值后面追加（没带时只发追加值），`{unset}` 删除这个头，空值忽略；键 `*` 透传全部客户端请求头，键 `re:正则` / `regex:正则` 透传名称匹配的头。它在请求头透传之后、参数覆盖之前生效，参数覆盖从它的结果开始（条件可读 `header_override.名`，`keep_origin` 不覆盖它设的头），插件的头和鉴权头仍在最后。WebSocket 握手同样生效（透传键除外）。
- 与 new-api 不同：凭证（Authorization、x-api-key、Cookie 等）、Host 等逐跳头、客户端地址和网关控制头不能设置、删除，也不能经 `client_header` 读取，保存时报“不能覆盖”；不支持 `{api_key}`；客户端同名头有多个值时全部取出、以 `, ` 连接。请求头覆盖的值和参数覆盖一样，有账号查看权限的人都能看到，不要放密钥。
- **控制台**：账号编辑页“高级 › 请求转发”，在“请求头透传”和“请求参数覆盖”之间新增一行“请求头覆盖”，显示“未设置 / N 项”和“编辑”按钮；编辑弹窗是“请求头 / 值”表格，值旁可快捷插入占位符（客户端头、客户端头 + 追加、删除），也可切到 JSON 模式粘贴。导航摘要显示“请求头覆盖 N 项”。请求参数覆盖的试运行从请求头覆盖的结果开始。插件也可以在通用配置项里给它默认值、隐藏或锁定。
- **参数覆盖补齐**（CONTRACTS §108.4）：此前报“暂不支持”的 new-api 写法现在可以用：
  - `sync_fields`：在请求体字段（`json:` / `body:`）和请求头（`header:`）之间同步，一边有值另一边没有时复制过去；
  - `prune_objects`：删除路径下（可用 `*`）满足条件的对象，例如按 `type` 删掉某类工具；
  - 条件模式 `regex`（Go 正则，保存时校验）；
  - `pass_headers` 的值可以写成对象 `{headers | names | header}` 或 JSON 字符串；
  - 条件上下文 `using_group`（同 `group`）。
- **请求头里的控制字符**：参数覆盖把含换行等控制字符的值写进请求头时（例如 `sync_fields` 从请求体同步），这次尝试按参数覆盖失败换号，不再把会被上游传输层拒绝的请求发出去导致账号被冷却；固定值里含控制字符在保存时报错。
- **@sub2api/ui、@sub2api/host、@sub2api/vite-preset 3.19**：`@sub2api/host` 新增宿主组件 `HeaderOverrideEditor`，schema 表单新增控件 `header-override`，`ParamOverrideEditor` 可传 `headerOverride` 作为试运行的初始请求头。只加不改；用到的插件声明 `hostUICompat: "^3.19"`。
- **迁移 0105**：`accounts` 加可空列 `header_override`（jsonb），旧账号为空、行为不变。

#### 通知测试发送改为同步，投递记录不再一直显示“发送中”

- 系统设置 → 通知 → 发送测试：接口等这条测试通知第一次投递完成（最多 35 秒）再返回结果，控制台在点击时和返回后各刷新一次投递记录，并提示已发送、发送失败（带完整错误）、将重试或仍在发送。不轮询、不自动刷新、没有服务端推送（CONTRACTS §74.4）。

#### codex 0.1.11、openai 0.4.3：默认模型加上 gpt-6.2

- **codex 0.1.11**：两种账号类型（设备码、Sign in with Codex）的默认模型最前面加 `gpt-6.2-astra`、`gpt-6.2-sol`（ec2pool 设备码账号实测上游可用，docs/PLUGIN-CODEX.md §17）。
- **openai 0.4.3**：API Key 账号默认模型同样加上这两个模型。
- 只改默认模型列表，不改价格；已有账号的模型列表不变，需要时在账号上手动添加。

#### 预设模型补上 Claude Fable 5.2 与 Gemini 3.5 / 3.6 / 3.8 / 4（anthropic 0.3.4、claude-oauth 0.2.5、relay 0.3.2、gemini 0.3.2）

- **Claude**：Anthropic（API Key）、Claude OAuth、Claude Setup Token、中转 Key 四种账号类型的“填入预设模型”在最前面加上 `claude-fable-5-2`。
- **Gemini**：API Key 账号的“填入预设模型”加上 `gemini-4-carbon`、`gemini-4-argon`、`gemini-4-hydrogen`、`gemini-3.8-flash`、`gemini-3.6-flash`、`gemini-3.5-flash`、`gemini-3.5-flash-lite`（Google 模型页与弃用表列出的 ID，3.6 只有 flash 一个正式型号；`gemini-3.5-flash` 上游已自动转到 `gemini-3.6-flash`，但端点仍可用）。
- 只改预设列表，不含价格；已有账号的模型列表不变，要用新列表请在账号上重新填入或从上游获取。
- anthropic 的升级测试夹具随之由 0.3.4-test 改为 0.3.5-test（`deploy/ci/check.sh`、e2e AC05、`tools/sub2api-plugin` 测试和 `build-demo.sh`）。anthropic 插件自带的模型目录（`GET /models`）没有加 Fable 5.2，那需要新迁移。

### 核心 0.1.302：通知投递错误看得懂、复制 API Key 不再二次验证（`0295123ea`，mailer 0.1.4；无迁移，滚动）

- **Release**：tag `core-v0.1.302`（`92ca8c469`，即 `0295123ea` 加本节说明）自动构建（run 44155847574），北京时间 03:35:40 发布。清单 sha256 `188dc02f01e331299f100dcccc4226a08c32e784415c515637e5f69d88c8ec78`（amd64 bundle `ba765c82…d5d9`、arm64 `38519cf8…900e`，与 GitHub 资产摘要一致），strategy rolling，schema 仍 `50a07a21…2437`；内置集合只有 mailer 由 0.1.3 升到 0.1.4。网关没有改动，仍 0.1.138。`next/plugins/released.json` 按 `92ca8c469` 重写（mailer 0.1.4）。
- **ovh 集群**：Release 清单与 bundle 核对摘要后导入，预检 rolling-v1、无阻断（expected_revision 568），计划 `d218afa5ca5672abb9ba55a24f0c3889` 03:40:38→03:41:43（先 ccp 后 ovh），ovh 3130 与 api.prophey.ai `/readyz` 全程 200。两节点核心 0.1.302，8 个插件 active、ready、restarts 0；mailer 在节点就绪后由内置同步升到 0.1.4（03:41:58，`plugin.upgrade` 0.1.3→0.1.4，原有 net 授权沿用）。通知记录 3（03:41:37 控制台“发送测试”创建，经 sup2api-ccp）重试后 last_error 为“SMTP host mail.codingplus.ai does not exist, correct it in the mailer settings / 找不到 SMTP 服务器 mail.codingplus.ai（域名不存在），请在邮件插件设置里改正: dial tcp …: no such host”，不再带 `internal: … plugin mailer: Unknown:` 前缀；旧记录 1、2 保留旧文字。SMTP 设置没有动。强制检测核心 0.1.302 无更新、无错误；两节点日志无 ERROR；03:30–03:50 无请求（392 关着）。
- **ec2pool**（单节点）：计划 `ee9a061b7c1c6546f64cef3aba926e19` primary-first-v1 03:44:02→03:44:09，`/readyz` 503 03:44:05.8→03:44:09.8 约 4 秒。核心 0.1.302，anthropic、codex 0.1.10、gemini、moderation、openai active、ready、restarts 0（dynweight、mailer、volcengine 仍只安装未启用，mailer 0.1.4 包已存入）；两个 codex 账号（`codex_device`，id 2、3）active、可调度；强制检测无更新；日志无 ERROR。
- 回滚：核心回 0.1.301（清单 `8fb8ddbd…b0b6`），ovh 集群 rolling、ec2pool 主节点优先；插件只升不降，mailer 留在 0.1.4。

- **mailer 0.1.4：投递失败说明是哪一步、该查什么**（CONTRACTS §74.4）：SMTP 服务器域名不存在时，通知记录显示“找不到 SMTP 服务器 <host>（域名不存在），请在邮件插件设置里改正”；连不上、服务器不应答（含 TLS 握手失败）、STARTTLS 失败、登录失败、发件地址被拒也各有中英文说明，后面接底层错误。端口 465 没选“全程 TLS”、端口 587 选了“全程 TLS”时附上提示（这种组合以前只会等到超时）。
- **核心**：插件自己返回的投递错误原样记入通知记录，不再带 `internal: internal error: plugin mailer: Unknown:` 前缀；错误截断到 1000 字节时在字符边界截断，中文错误不会写坏。
- **控制台**：系统设置 → 通知的投递记录完整显示错误并换行，不再截断成一行（以前只看得到 `internal: internal error: plugin mailer: Unknown: dial t…`）。
- **复制 API Key 不再二次验证**：在“我的 API Key”里复制、查看自己的 Key（`POST /me/api-keys/:id/reveal`）不再要求二次验证，仍只能操作自己的 Key，每次照常记 `apikey.reveal` 审计。权限 `apikey:self:manage` 不再标记高危和二次验证，二次验证范围里去掉 `apikey.reveal`（CONTRACTS §80.2）。

### 核心 0.1.301：删除 custom 插件、答完后才断开的请求按成功计费、插件可读取系统设置并感知变更、最大并发移入限流（`e0a985c81`，codex 0.1.10、dynweight 0.5.11；迁移 0104，主节点优先）

- **Release**：上游 PR #46 合并为 `e0a985c81`（next/ CI 全过；旧 backend 的 backend-security、shell 两项与 #44、#45 一样失败，不涉及 next/），tag `core-v0.1.301` 自动构建（run 44132030346，用户在项目线程原话同意提上游同步 PR、推 tag、批准 release 环境，release 环境由我代批），北京时间 01:17:03 发布。清单 sha256 `8fb8ddbd75afb13eceaa6041f10bfc145249ed2aa7021df4a9add3369611b0b6`（amd64 bundle `3e632f88…8684`、arm64 `7c27ff05…6e57`），strategy maintenance，schema `ab35f2f6…187d` → `50a07a21…2437`；内置集合 anthropic 0.3.3、codex 0.1.10、dynweight 0.5.11、gemini 0.3.1、mailer 0.1.3、moderation 0.12.1、openai 0.4.2、volcengine 0.12.8，不再有 custom。网关、runtime-contract 没有改动，不发新网关（仍 0.1.138）。`next/plugins/released.json` 按 `e0a985c81` 重写（codex 0.1.10、dynweight 0.5.11）。原计划的核心 0.1.299、0.1.300 没有发布，版本号跳过。
- **发布前**：两个集群 custom 账号都是 0（`plugin_key = 'custom' AND deleted_at IS NULL`，连已删除的也没有）。new-api 渠道 392 自 11-17 15:43:47 起由管理员手动关闭（用户定不动、不恢复），发布前后都没有动；不备份数据库。
- **ovh 集群**：Release 清单与 bundle 核对摘要后导入，预检 primary-first-v1、无阻断（expected_revision 566），计划 `f6f50d8a3fa6cd5ad048d3462099583e` 01:18:22→01:19:27（ccp 先转发主节点，主节点停机迁移启动，再恢复 ccp）；**停服**：ovh 3130 与 api.prophey.ai `/readyz` 503 01:18:37.0→01:18:46.0 约 9 秒；迁移 0104 于 01:18:41 应用（`plugin_key_retirements` 记入 custom，plugins 无 custom 行）。两节点核心 0.1.301，8 个插件 active、ready、restarts 0（codex 0.1.10、dynweight 0.5.11）；`/account-types` 没有 custom 类型；强制检测无更新、无错误；日志无 ERROR；392 关着，期间没有请求。
- **ec2pool**（单节点）：计划 `bf6597af4f50491e9567509e33d397d8` primary-first-v1 01:20:07→01:20:13，`/readyz` 503 01:20:09.7→01:20:13.7 约 4 秒；迁移 0104 于 01:20:12 应用。核心 0.1.301，codex 0.1.10 active（dynweight、mailer、volcengine 仍只安装未启用）；两个 codex 账号（`codex_device`）active、可调度；无 custom 类型；日志无 ERROR。上线后 codex-test 还没有新请求，缓存比例待复查（上线前 11-17 21:56–22:13 共 22 次，cache_read/input 26.6%）。
- 回滚：迁移 0104 单向，不回退核心，出问题修复前进（插件只升不降）。

- **升级前**：每个集群先在控制台删除 custom 类型的账号（“自定义上游（OpenAI/Anthropic/Gemini 兼容）”，账号类型 `custom_*`）。新版不再认识这些类型，迁移 0104 卸载插件后它们只显示为插件已卸载。升级前只读核对：`SELECT count(*) FROM accounts WHERE plugin_key = 'custom' AND deleted_at IS NULL;` 应为 0。
- **删除内置插件 custom**（CONTRACTS §106）：接任意 OpenAI、Anthropic、Gemini 兼容接口，改用内置 openai、anthropic、gemini 插件的 API Key 账号并填 Base URL，协议转换由平台做。新建账号不再有“自定义上游”一组。`custom` 键不能再安装（任何来源、任何签名都返回 403 `plugin_key_reserved`）。
- **账号上游层一并删除**：自定义上游账号的“Key 发送方式”“追加请求头”“修改路径”“放开过滤字段”这些设置（账号 settings 的 `upstream`）没有了，核心不再读取；声明式上游的其余功能不变。`GET /account-types` 不再返回 `upstream`、`pass_headers`。
- **插件 SDK**：清单 `accountTypes[].upstream` 删除账号层开关，`upstream.Resolve` 去掉账号参数（CONTRACTS §106.2）。
- **@sub2api/ui、@sub2api/host、@sub2api/vite-preset 3.18**：`@sub2api/host` 不再导出 `UpstreamEditor`，schema 表单控件 `upstream-editor` 删除。内置插件都没有用它。
- **迁移 0104**：删除 custom 的插件行（连同版本与授权）并记入已退役的插件键；它的账号、使用记录、账本与审计保留。单向，回退用升级前的备份。

#### 断开计费与插件读取系统设置

- **答完后才断开的请求记为成功**（CONTRACTS §25.8）：流式应答的终止用量事件（Anthropic `message_delta`，OpenAI Responses `response.completed` / `response.incomplete`）已经转发给客户端之后，客户端在上游关闭连接前断开的请求，记为成功（状态码取上游的，通常是 200），按上游报告的真实用量（含缓存读）计费。此前 Codex 等客户端收到完成事件就断开连接时，网关先读到上游结束就记 200，客户端先断开就记 499 `client canceled`，并且输出按预扣保留量（默认约 4000 token）计费，同一个客户端两种结果随机出现，多收了钱；插件执行的请求（例如 codex）同样如此。账号统计也不再把这类请求计为取消。终止用量事件之前断开的请求，行为不变。`continue` 策略下这类请求也不再继续读上游。
- 判定依据是平台 manifest 的 usage 规则：指名事件且映射了输出 token 的规则命中，就算终止用量事件。OpenAI Chat Completions、Gemini 的用量在不具名的数据块里，没有可以判定的终止事件，仍以读到上游结束为准。WebSocket 不受影响：轮次本来就在完成事件发给客户端后结束。
- **“读完再计费”现在对插件执行的请求也生效**（CONTRACTS §25.8）：系统设置 → 计费 → “客户端中途断开时”选“读完再计费”（`continue`，默认）时，插件执行（`platform.execute.v1`）、由核心代为转发的 HTTP 请求（codex，以及 volcengine 的非任务请求）在客户端断开后也会把上游读完，按实际用量计费。此前这类请求无论怎么设置都立即中止上游、按预扣估算计费，这个设置对 codex 不生效。任务提交仍不续读。插件放弃转发（被终止、超时）时记为上游中断，不再算作客户端断开。
- **发布故障策略后，插件执行的请求也执行插件给出的限时禁用与额度观测**（CONTRACTS §69）：此前发布了故障策略时，codex、volcengine 等插件执行的请求遇到上游 HTTP 错误不会调用插件的错误分类，codex 在 429 且用量窗口已满时“禁用到重置时刻”不执行，额度用满的账号每个请求仍会被先试一次。现在与普通转发一致：插件分类，重试、换号、冷却仍由故障策略决定。
- **插件开发文档**：新增 `next/docs/PLUGIN-DEVELOPMENT.md`，逐项列出系统设置在普通转发、插件执行、WebSocket 三条路径上由谁生效、插件要配合什么。
- **插件可读取系统设置并感知变更**（CONTRACTS §107）：插件经新接口 `HostService.GetSettings`（SDK `pluginsdk.Settings[T]`）只读地读取计费、网关、均衡负载、粘性会话、自动禁用、故障策略开关和环境变量全局层七组设置，值与核心实际使用的一致，不需要授权，不含密钥，故障策略的规则不开放。管理员在任一节点保存这些设置后，每个节点上的插件进程立即收到 `PluginService.SettingsChanged` 并作废缓存（SDK 已实现，缓存最多 10 秒兜底；并发读取合并成一次，读取失败时沿用上次的值、2 秒内不再读）；同时在保存设置的事务里写 `settings.changed` 事件，插件可经 `app.events.v1` 订阅。Host API 仍是 9；用到它的插件把 `hostCompat` 下限提到 `>=0.1.301`。环境变量设置的节点间广播改为 `{"key":"settings.environment"}`，滚动期间新旧节点之间全局层缓存最多晚 10 秒生效。用法见插件开发文档第 5 节。
- **dynweight 0.5.11：粘性会话关闭时放弃排队的粘性清理**（需要核心 0.1.301 及以上，CONTRACTS §74.5）：系统设置里关闭粘性会话后，降权排队的“清理粘性会话”直接放弃，各记一条清理事件（`detail.skipped = "sticky_disabled"`），不再扫描绑定。关闭期间新请求既不绑定也不读取绑定，留下的绑定按 TTL 过期；推迟到重新开启后再清，会误清开启后新建、指向已恢复账号的绑定。读不到系统设置时照常清理。
- **codex 0.1.10：修复 Codex 会话缓存时有时无**（需要核心 0.1.297 及以上）：请求体带 `prompt_cache_key` 时（Codex CLI 每个会话都带），插件按 API Key 隔离派生一个会话 ID，作为 `session_id` / `conversation_id` 头发给 ChatGPT 后端，同一会话的每一轮落到同一份提示缓存。此前插件不发会话头，ec2pool 上同一会话、同一账号的请求缓存读只有 0、5632 或停在早先某轮的长度，命中率约 27%。客户端原值不发给上游，`originator`、`user-agent` 仍如实写 sup2api（docs/PLUGIN-CODEX.md §16）。

#### 控制台

- 新建/编辑账号的“调度与限流”页，“最大并发”从“调度”卡片移到“限流”卡片，和 RPM、TPM、会话数限制放在一起（排在最前，带“个请求”单位）；“调度”卡片只留优先级、权重、参与调度、允许自动禁用，两张卡片的说明文字相应调整。左侧导航摘要不变。

#### codex 0.1.9 的界面改动（原计划核心 0.1.299，未单独发布，内置 codex 直接是 0.1.10）

- **设备码授权引导**：设备码登录表单顶部说明去哪里开启设备代码登录：ChatGPT 设置 → 账户安全与登录 → 应用安全 → “为 Codex、Excel、PowerPoint 和 Word 启用设备代码登录”，附“打开 ChatGPT 安全设置”链接（`https://chatgpt.com/settings/security`），授权前就能看到，不再只在授权失败时提示（旧提示的“设置 → 安全 → 设备码授权”路径也已过时）。
- **验证页面链接旁加复制按钮**，在别的设备上打开验证页时不用手动选中链接。
- 中文界面的账号类型名称改为“ChatGPT（Codex） · 设备码”“ChatGPT（Codex） · Codex”：0.1.8 的中文名在“·”前少一个空格，控制台没识别出插件名前缀，新建账号标题显示成“ChatGPT（Codex） · ChatGPT（Codex）· 设备码”。

## 2026-11-17

### 网关 0.1.138：停用的节点不进升级计划，删节点不再要求停止确认（`0bbda1192`）

- **Release 与上线**：上游 PR #45 合并为 `0bbda1192`（next/ CI 全过；旧版 backend 的 backend-security、shell 两项与 PR #44 一样失败，不涉及 next/），tag `gateway-v0.1.138` 与 `core-v0.1.298` 一起构建（run 44101318342、44101327224），release 环境由我代批（用户在决策卡上同意这次发布），北京时间 22:35:11 发布。网关清单 sha256 `0761e99c…104c`，amd64 二进制 `82baa5c2…51f4`；ovh 从同一提交构建的镜像（`sha256:f2951276…a2f`）里的二进制与之相同。逐个节点手动换：ccp 22:36:26 让出、22:37:03 就绪（`.env.bak-gw137`），ovh 22:37:19 让出、22:37:27 就绪，ec2pool（单节点）22:37:44 重建、22:37:49 就绪。之后 `updater.nodes` 三个节点 0.1.138。

- **预检跳过停用节点**：手动停用的节点（例如两节点集群改成单节点后留下的那一台）不再列入核心升级计划，此前它会进入计划并让计划在它的第一步暂停（`planned node is disabled`）。没有任何启用节点时预检报 `no_nodes`。
- **`remove-node` 不再要求 `stopped`**：只要节点已停用且 5 分钟内没有心跳就能删除。`stopped` 只由升级计划的停止步骤写入，手动停容器或停用都不写，所以 ec2pool 的 node-b 这类节点以前删不掉。

### 核心 0.1.298：codex 登录方式改名为“设备码”和“Codex”、网关托管的核心自动信任内置插件签名密钥（`0bbda1192`，codex 0.1.8；无迁移，滚动）

- **Release**：22:35:59 发布，清单 sha256 `108d2c7d0115cccdb9f83839be85045ea8ca26fedf6a4fbd9e415e8cff2ff9d7`（amd64 bundle `698a2d21…07a4`、arm64 `09059b7a…d1b0`），strategy rolling，schema 仍 `ab35f2f6…187d`。`next/plugins/released.json` 按 `0bbda1192` 重写（codex 0.1.8）。
- **ovh 集群**：Release 清单与 bundle 核对摘要后导入，预检 rolling-v1、无阻断（expected_revision 564），计划 `1fc58f57c4c85ed5a179e53a4cc10468` 22:38:23→22:39:32（先 ccp 后 ovh），`/readyz` 全程 200。两节点 9 个插件 active、ready，codex 0.1.8；codex 账号类型的认证方式为“设备码”“Codex”（英文 Device Code、Codex）。5xx 0，日志无 ERROR；392 仍是关闭状态，没有动。
- **ec2pool**（单节点）：计划 `5598fc2899a37cbd5f7212e118ad618d` primary-first-v1 22:40:05→22:40:11，`/readyz` 503 约 4 秒；codex 0.1.8。随后删掉 0.1.297 时在 compose 手动补的 `SUB2API_BUILTIN_TRUST_KEY`，22:40:36 重建 node-a（约 6 秒），日志没有 `unknown signing key`，内置插件照常（这次各内置插件版本都已存在，没有触发新的上传验签）。
- 回滚：核心回 0.1.297（清单 `64bc544f…c431`），ovh 集群 rolling、ec2pool 主节点优先；插件只升不降，codex 留在 0.1.8；网关回 0.1.137 用各节点 `.env` 备份再让出、重建（ec2pool 回 0.1.297 核心时要把 `SUB2API_BUILTIN_TRUST_KEY` 加回 compose，备份 `compose.yml.bak-with-trustkey`）。

- **codex 0.1.8：登录方式改名**：新建账号表单的“认证方式”选项卡与账号类型的认证方式改为中文“设备码”“Codex”，英文 “Device Code”、“Codex”（此前是“设备码登录”“Sign in with Codex”）。账号类型名称随之改为“ChatGPT（Codex）· 设备码”和“ChatGPT（Codex）· Codex”（英文 “ChatGPT (Codex) · Device Code”、“ChatGPT (Codex) · Codex”），账号列表、筛选和详情里能分清两种登录；此前两者都显示“ChatGPT（Codex）”。只改文字，不改行为。
- **网关托管的核心自动信任内置插件签名密钥**：未设 `SUB2API_BUILTIN_TRUST_KEY` 时，核心从内置插件目录的 `builtin/trust.pub` + `trust.keyid`（镜像和 Release 包都带）读取签内置包的密钥并始终信任，镜像入口脚本里同样的推导删除。此前网关托管的核心不走镜像入口脚本，compose 不手动配这个变量就会拒绝 Release 内置包（`unknown signing key sup2api-plugins-2026`），内置插件停在旧版本：ec2pool 升到 0.1.297 时 codex 停在 0.1.6，当时是手动在 compose 里补的变量，升级到本版后可以去掉。

### 核心 0.1.297：codex 用量窗口以响应头为准、新增 Sign in with Codex、删除 Sign in with ChatGPT（`f40c7831d`，codex 0.1.7；无迁移，滚动）

- **Release**：上游 PR #44 合并为 `f40c7831d`，tag `core-v0.1.297` 自动构建（run 44085237135，用户提前批准，release 环境由我代批），北京时间 20:59:56 发布。清单 sha256 `64bc544f5b13d683d4565cb4fb5a0fefc60369ed6f0e434457285e857710c431`（amd64 bundle `7222fad2…bfa5`、arm64 `f3f4441a…a09b`），strategy rolling，schema 仍 `ab35f2f6…187d`。`next/plugins/released.json` 按 `f40c7831d` 的插件重写（codex 0.1.7 等）。
- **ovh 集群**：Release 清单与 bundle 核对摘要后放进 ovh 发布目录导入，预检 rolling-v1、无阻断（expected_revision 562），计划 `d3602f6e87e5df4a901786b57ab988e0` 21:02:05→21:03:14（先 ccp 后 ovh），`/readyz` 全程 200；两节点 9 个插件 active、ready，codex 0.1.7，账号类型只剩 `codex_device`、`codex_oauth`；集群上没有 codex 账号，被动用量快照未实测。392 仍是关闭状态，没有动。
- **ec2pool**（单节点）：20:40:17 经控制台接口软删除 `chatgpt_siwc` 账号 id 1（`gpt-plus-1117-01`，用户要求不保留）。预检发现计划仍含已停用的 node-b（网关 0.1.137 会把停用节点放进计划，见网关 0.1.138），经用户同意把 `updater.nodes` 里 node-b 一行改为 `stopped=true` 后 `remove-node`，再预检只剩 node-a；计划 `64d9594ce44007181b36ac7dafa1749c` primary-first-v1 21:16:24→21:16:30，`/readyz` 503 约 4 秒。内置插件随后因 `unknown signing key "sup2api-plugins-2026"` 没有升级（网关托管时没人设 `SUB2API_BUILTIN_TRUST_KEY`，见核心 0.1.298），在 `/opt/sup2api-gw/compose.yml` 补上该变量重建 node-a（21:17:45，约 5 秒）后 codex 升到 0.1.7、`chatgpt_siwc` 类型消失。详见 [ec2pool 部署记录](next/deploy/gateway/ec2pool/README.md)。
- 回滚：核心回 0.1.295（ovh 集群，清单 `b62f6c8b…0721`）或 0.1.296（ec2pool，`9fbbdae1…cf90`），rolling / 主节点优先计划；插件只升不降，codex 留在 0.1.7。

- **升级前**：每个集群先在控制台删除已有的 Sign in with ChatGPT 账号（类型 `chatgpt_siwc`），新版不再认识这个类型。
- **额度响应头可按窗口时长命名**（CONTRACTS §105）：manifest `quota.headers[].windowMinutes` 指向携带窗口分钟数的响应头，窗口名按时长生成（300 → `5h`、10080 → `7d`），供 codex 每次响应按 `x-codex-*` 头更新用量窗口。
- **WebSocket 错误事件的响应头**（CONTRACTS §105）：上游 WebSocket 会话里的错误事件带顶层 `headers` 对象时（Codex 后端的用量超限就是这样），核心把它当作这次错误的响应头，交给插件判断错误并按声明采样用量窗口；此前只采样握手的响应头，会话中途用满不会禁用账号。
- **codex 0.1.7：用量窗口以响应头为准**（PLUGIN-CODEX.md 第 13 节，需要核心 0.1.297）：两种账号类型的 5 小时 / 周窗口只取自每个 HTTP 响应、WebSocket 握手和带响应头的 WebSocket 错误事件里的 `x-codex-*` 头，删除 `wham/usage` 主动查询（控制台点击用量只重新读取快照，用量随账号的下一次请求更新）；只有 429 且头里有窗口用满并给出重置时刻时才自动禁用账号，到最晚的重置时刻由核心自动恢复；其他 429（包括只有 `usage_limit_reached` 错误体、窗口满但没有重置时刻）一律原样返回客户端，不冷却不禁用，不再按错误体、`retry-after` 或 5 分钟默认值禁用。重置次数缓存只由查看重置明细刷新；使用重置后，重置页把用量标为“用量会在账号下一次请求后更新”。
- **codex 0.1.7：新增 Sign in with Codex，删除 Sign in with ChatGPT**（docs/PLUGIN-CODEX.md §14）：ChatGPT（Codex）账号的登录方式改为“Device Code”（设备码，原有）和新增的“Sign in with Codex”（Codex CLI 的浏览器登录，授权后把 `localhost:1455` 回调网址粘回表单），两者令牌、上游、刷新、用量窗口与官方重置次数完全相同。Sign in with ChatGPT（`chatgpt_siwc`）账号类型整体删除，没有兼容处理：**升级前必须手动删除已有的 Sign in with ChatGPT 账号**。

### 核心 0.1.296：codex/OpenAI 流式应答正确计费、OpenAI 端点可不带 `/v1`、新模型列表（`cf4718dcb`，codex 0.1.6、openai 0.4.2；无迁移，滚动）

- **Release**：上游 PR #43 合并为 `cf4718dcb`（代码在 `179142ae6` 已同步），tag `core-v0.1.296` 推送后自动构建（run 44070757390，用户提前批准，release 环境由我代批），北京时间 19:25:10 发布。清单 sha256 `9fbbdae1f86286fa0b72498f74fb61352403805bc9d7ce4aa46d2f13bc7bcf90`（amd64 bundle `5a59c505…2777`、arm64 `b9a5aea7…cac4`），strategy rolling，schema 仍 `ab35f2f6…187d`。
- **ec2pool 上线**（用户要求把 ec2pool 改成网关托管的 docker compose）：原来是非托管的 `deploy/single`（`/opt/sup2api`，app、app-2 两个核心，已跑 `179142ae6`），改为 `/opt/sup2api-gw` 的网关集群（网关 0.1.137，沿用原来的 PostgreSQL、Valkey 与开发插件市场；先按两节点同机运行，随后按用户要求改为单节点 node-a），以 Release 0.1.296 为基线 `init`。19:25:47 停旧 app、app-2，复制 11 个插件包到 node-a 的 `plugin-blobs/`，19:26:05 node-a 就绪、19:26:09 node-b 就绪；入口 `127.0.0.1:18080` 不变，宿主机 Caddy 没改。6 个启用插件两节点 active、ready。详见 [ec2pool 部署记录](next/deploy/gateway/ec2pool/README.md)。
- **ovh 集群（ovh + clicodeplus）仍是核心 0.1.295、网关 0.1.137，没有装 0.1.296**（要装时可在控制台一键更新或按部署记录手动）。

- **修复：Sign in with ChatGPT 账号的流式请求不计费**（用户 11-17 报告，ec2pool 实测）：上游对 `stream:true` 返回 SSE 正文却标 `Content-Type: application/json`，网关按 JSON 处理，token、首字时间都记 0、计费状态 `free`，而且整段缓冲到结束才发给客户端。现在流式请求的 2xx 应答若正文以 SSE 字段开头就按事件流转发与计量（CONTRACTS §104.1）。已有的 0 token 记录不会补算。
- **OpenAI Responses 缓存写入**：`input_tokens_details.cache_write_tokens` 计为缓存写入，按价格的 `cc` 计；价格没有 `cc` 的模型仍按输入价计（`inclusive` 语义下未计价的缓存写入留在输入里，CONTRACTS §104.3）。TPM 与实时 token 统计不含缓存写入，OpenAI 请求的这两个数会比以前略小。
- **OpenAI 端点不带 `/v1`**：`POST /responses`、`/chat/completions`、`/embeddings` 与 WebSocket `GET /responses` 自动按 `/v1/...` 处理，适配 base URL 没写 `/v1` 的客户端；只对 openai 平台（CONTRACTS §104.2）。
- **codex 0.1.6**：两种账号类型的默认模型改为上游实际可用的 `gpt-6.1-sol`、`gpt-6.1-terra`、`gpt-6-astra`、`gpt-6-sol`、`gpt-6-luna`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`（Sign in with ChatGPT 账号 `/v1/models` 的结果），测试模型改为 `gpt-6-luna`。已有账号的模型列表不变，要用新列表请在账号上重新获取模型。生图：Sign in with ChatGPT 上游明确拒绝 `image_generation` 工具（`subscription_sharing_unsupported_capability`），所以不加生图模型。
- **openai 0.4.2**：API Key 账号默认模型补上上述 8 个新模型。

### 网关 0.1.137：删除只给网关 0.1.135 留的 `signed_manifest` 列（首个由 GitHub Actions 发布的网关，`3c8cbbda9`）

- **Release**：上游 PR #41（`eec4bb0d3`）、#42（`3c8cbbda9`，修两个门禁测试）合并后，在 `3c8cbbda9` 打 tag：`gateway-v0.1.137`（run 44054676233）与 `core-v0.1.295`（手动 run 44054717060，`schema_before` = 线上 0.1.294 的 `ab35f2f6…187d`），release 环境由用户批准，北京时间 17:44:50、17:45:15 发布。网关清单 sha256 `383be985…08e7`（amd64 二进制 `fc74f2f2…2618`），核心清单 sha256 `b62f6c8b04628ff00a155506737ce5103a7ff8b3d5f555222b224636a3720721`（amd64、arm64 两个 bundle）。第一次门禁失败（growth `TestManifestValid`、网关 handoff 测试用了 runner 上改名后拒跑的 `echo`），修好后删掉两个 tag 重打。
- **上线（用户要求逐个节点手动处理，没有用一键更新）**：网关镜像 0.1.137 在 ovh 从 `3c8cbbda9` 构建，二进制 sha256 与 Release 的 `fc74f2f2…2618` 一致；ccp 18:23:27 让出、18:24:05 就绪，ovh 18:24:21 让出、18:24:30 就绪（`.env.bak-gw136`），`signed_manifest` 列随后删除。核心用 Release 的清单与 bundle（摘要核对）放到 ovh 的发布目录导入，预检 rolling-v1、无阻断（expected_revision 560），计划 `4d9d574cffbb3aa86a3f7238c1f80b2e` 18:25:31→18:26:37 completed（先 ccp 后 ovh），`/readyz` 全程 200；9 个插件两节点 active、ready、restarts 0；之后在线检测两条线都没有更新。392 仍是管理员关闭状态，没有动；期间没有请求，5xx 0。
- 回滚：核心用 0.1.294 清单 `013a7736…` 建 rolling 计划；网关把 `.env` 换回 `.env.bak-gw136` 后让出、重建（0.1.136 不读 `signed_manifest`）。ec2pool（非托管单机）没有动。

- 两个节点都已运行不验签的网关 0.1.136，`updater.releases.signed_manifest` 不再有人读，网关启动时删除它，同时删掉 0.1.136 里把签名发布记录转成清单原文的一次性转换（线上已转换过）。还在 0.1.135 或更早的网关要先手动换到 0.1.136，再一键更新。
- 这是第一个由 GitHub Actions 按 tag `gateway-v0.1.137` 构建、发在 Sub2API-Devs/sup2api Release 上的网关，线上可以在控制台“系统设置→在线更新”一键滚动更新。

### 核心 0.1.295：首个由 GitHub Actions 发布的核心（`3c8cbbda9`，代码与 0.1.294 相同，无迁移，滚动）

- 第一个由 GitHub Actions 按 tag `core-v0.1.295` 构建、发在 Sub2API-Devs/sup2api Release 上的核心，清单不签名、摘要即清单原文的 sha256；内置插件版本与 0.1.294 相同，仍用插件密钥 sup2api-plugins-2026 签名（集群里已存的同版本插件包保持不变）。代码与线上 0.1.294 相同，用来验证从 GitHub 一键更新核心的整条流程。

### 核心 0.1.294：角色复制改为“合并”和“覆盖”（`1ca4d32de`，滚动，无迁移；只改控制台，网关保持 0.1.136，内置插件与 0.1.293 相同）

- **上线**：北京时间 16:22:48–16:23:54，计划 `8c9ca53ba5fb7a415d37bc913003ed64` rolling-v1 completed（清单 `013a7736…b918`，不签名，由网关 0.1.136 的 sub2api-release 生成），`/readyz`（ovh 3130 与 api.prophey.ai）全程 200。new-api 渠道 392 维持管理员 15:43:47 手动关闭的状态，没有动；不备份数据库。392 关着，15:52 之后没有请求，5xx 为 0；两节点日志 0 个 ERROR。核验：两节点核心 0.1.294；控制台 `/roles` 与其静态资源 200。**回滚**：用 0.1.293 清单 `cd41acb0…096b` 建 rolling 计划（schema 相同）。
- 用户 11-17 要求：“从角色复制”只保留两种方式，**合并**（保留现在的勾选，再把对方有的加进来）和**覆盖**（勾选变成跟对方一模一样），去掉交集；文案改得口语一些。覆盖时当前角色已经蕴含的来源范围直接取，可以收窄；来源里操作者不能授出的、关键权限和已移除的权限照旧不勾、在编辑器里列出，当前被它们蕴含的较窄范围保留。
- 角色页保存（权限或可分配清单）被“上级会管不了下级”（`owner_cannot_manage`，0.1.292）拒绝时，原因留在编辑器提示里并写出是哪个用户（#id），不再只弹一条很快消失的提示（CONTRACTS §103.3）。规则见 CONTRACTS“角色页从角色复制”。

### 网关 0.1.136（核心随 0.1.293，`eaf21e48`）：发布清单不再签名，只校验摘要（无核心迁移，滚动；网关手动换镜像）

- **上线**：北京时间 16:07:58–16:10:42。先手动换网关 0.1.136（镜像 `0e1cc8f9…`）：ccp 16:07:58 让出、16:08:01 排空、16:08:36 就绪；ovh 16:08:49 让出、16:08:58 就绪。新网关启动时 `updater.releases.manifest` 转成 `bytea`，已有发布的摘要与原文 sha256 一致，基线 0.1.292（`54bc00a9…`）照常。两节点都是 0.1.136 后用新网关导入不签名清单 `cd41acb0068c0588bf566c470a0775d1d5def9b8be6e85627fdf5855dae8096b`（预检 rolling-v1、无阻断，expected_revision 556），计划 `c72a712f4433b33b07698fc4aadc42f7` rolling-v1 completed（16:09:34→16:10:42，先 ccp 后 ovh），`/readyz`（ovh 3130 与 api.prophey.ai）全程 200。内置插件与 0.1.292 相同，9 个插件两节点 active、ready、restarts 0。
- 渠道与备份：没有关 392，也没有备份数据库。392 在发布前（15:43:47）已被人工关闭（`manual operation`），之前 15:10 起账号 126、127 有成批 502（upstream request failed），与本次发布无关；发布期间 sup2api 没有请求，5xx 0。
- 核验：`GET /system/update-check` 两条线带 `reason_code: no_release`，控制台“设置 → 在线更新”显示中文原因；节点页两节点核心 0.1.293、网关 0.1.136；两节点日志无 ERROR。
- 回滚：核心用 0.1.292 清单 `54bc00a9…a682` 建 rolling 计划（schema 相同）。基线已换成不签名的 0.1.293，网关不能再回到 0.1.135；网关出问题修复前进（之后可一键更新）。部署细节见 [OVH 部署记录](next/deploy/gateway/ovh/README.md)。

- **发布清单不再签名，只校验摘要**（用户决定；网关二进制、发布工具与流水线变更，随下一个网关版本生效；CONTRACTS §33、§102.10）：核心与网关清单以普通 JSON 发布，发布摘要是清单原文的 sha256，离线导入（`import -manifest`）用同一格式。网关信任配置的更新源（HTTPS 访问的 GitHub 仓库或配置的 HTTPS 地址；离线导入是 `release_origin`），清单和每个安装物按摘要与大小校验：防得住下载途中或镜像上的损坏与替换，防不住仓库、账号或来源本身被攻破。删除签名信封 `rc.SignedManifest` 与验签、网关配置 `trusted_keys`（写了也不读）、内置公钥目录 `release/trusted/`（含 `sup2api-ovh-2026`）、`sub2api-release keygen` 与 `--key`/`--key-id`；流水线的 `sign` 任务改名 `package`，不再需要 `SUB2API_RELEASE_SIGNING_KEY`、`SUB2API_RELEASE_KEY_ID`（可从环境 `release` 删除）。内置插件包仍用插件密钥签名，核心线仍要 `SUB2API_PLUGIN_SIGNING_KEY`、`SUB2API_PLUGIN_KEY_ID`：内置包导入集群插件库后，各节点（包括回滚到的旧核心）加载时重新验签，信任级别也来自签名。控制台的更新源说明改为“只填信任的仓库或地址”，不再提签名。
- **网关 schema**：`updater.releases.manifest` 由 `jsonb` 改成清单原文（`bytea`），只转换一次：已登记的签名发布取信封里的 payload（当初算摘要的那份字节），所有摘要不变，线上基线核心 0.1.292（`54bc00a9…a682`）照常作为基线。`signed_manifest` 改为可空、不再读写，只留给 0.1.135 在换网关期间或回滚后启动以前登记的发布；全部节点换上新网关后，下一个网关版本删除它。
- **从网关 0.1.135 过渡**：0.1.135 只认签名清单，装不了不签名的发布（核心一键更新也由网关校验，同样装不了），所以第一个不验签的网关要手动换镜像：先从节点 ccp、再主节点 ovh，每个节点先 `sub2api-gateway yield`，等 `/readyz` 503 且在途请求为 0 再重建容器（与 0.1.135 上线时相同）。新网关启动时转换 schema；之后核心与网关都能一键更新。新旧网关混跑期间两边都不要导入或应用更新：新网关导入的发布不带签名，0.1.135 读到这样的行会报错（发布列表整体 500）；0.1.135 导入的行按它重新序列化的字节存，与摘要对不上，新网关装不了，只能手动删行。网关回滚到 0.1.135 只在基线还是签名发布（如 0.1.292）时可行，回滚后发布列表因不签名的行报错；基线换成不签名的核心（0.1.293）之后就不能再回到 0.1.135，只能修复前进。节点本地 `manifests/<digest>` 里旧的签名信封会被新网关的清理当成空清单，上一版的 bundle 会被删掉，回滚核心时重新下载。
- 网关 0.1.136 同时带上 0.1.292 已写的检测原因代码 `reason_code`（控制台按代码显示中文/英文原因）。

### 核心 0.1.293：角色从其他角色复制授权（`eaf21e48`，只改控制台，无迁移，滚动；与网关 0.1.136 同批上线）

- **从角色复制**：角色与权限页编辑一个角色时，新按钮“从角色复制”选一个来源角色（不含超级管理员角色），按“并集”（把来源的权限加到当前勾选上，已勾选的保留）或“交集”（只保留两边都有的，其余取消勾选）改编辑器里的勾选，点“保存”后才写入，保存时照常检查不能超出操作者（§43.6）。按动作比较范围：`account:read:own` 与 `account:read:team` 的并集是 `team`，`account:read` 与 `account:read:own` 的交集是 `own`。操作者不能授出的、关键（与整组勾选一样只能单独勾）和已移除的权限不复制，分三类列在编辑器提示里；交集里分组与单个账号相交保留为“分组内的该账号”。规则见 CONTRACTS“角色页从角色复制”。代理供应商等角色仍是独立角色，之后加给 user、supplier 的权限不会自动跟到代理，需要时在代理角色上用它复制（用户 11-17 定）。

### 核心 0.1.292：代理供应商、team 范围、账号所属（`e3f5d36aa`，迁移 0103，primary-first 停服；网关保持 0.1.135，内置插件与 0.1.291 相同）

- **上线**：北京时间 15:09:58–15:11:03，计划 `09029830db9caf041b0e90f3e5917920` primary-first-v1 completed（清单 `54bc00a9…a682`，maintenance）；**停服约 10 秒**（`/readyz` 503：api.prophey.ai 15:10:12.9→15:10:23.0，ovh 3130 15:10:12.7→15:10:22.7）；迁移 0103 于 15:10:17 应用，schema `d78ba831…333c` → `ab35f2f6…187d`。new-api 渠道 392 没有关（用户在决策卡上选“发布，不关 392”），不备份数据库。升级前预检 ①②③ 都干净：没有 key 为 `supplier_agent` 的自定义角色，没有能分配供应商的自定义角色，user、supplier 的核心授予与默认值相同（supplier 由升级补上 `usage:account:read:own`）。5xx：发布前 14:40–15:09:58 共 2496 次 0 个，计划期间 42 次 0 个（15 个 499 是停服时客户端断开），之后 0 个。核验：两节点核心 0.1.292；`schema_migrations` 有 `0103_user_owner.sql`；内置角色 `supplier_agent` 已建（20 条核心授予，含 `account:read:team`、`usage:account:read:team`、`user:read/create/update:team`）；129 个账号的 `owner_id` 全部回填（空值 0，`created_by` 本来就没有空的）；9 个内置插件不变。**回滚**：不能回退（网关拒绝跨 schema，0.1.291 也拒绝带 0103 记录的库），线上出问题修复前进。
契约见 CONTRACTS §103。

- **账号所属**：账号新增“所属”（`accounts.owner_id`），表示账号属于哪个用户。新建时就是创建者，管理员可以用 `PUT /accounts/:id/owner` 改成别的用户（新权限 `account:owner:update`，admin 自动获得，operator、供应商没有；审计 `account.owner`）。“创建者”只记录谁建的，不再决定归属：供应商的“自己的账号”、账号列表“只看我的”、管理员停用锁定、插件账号路由都改按所属判断，管理员把账号改给别人后，原创建者就看不到、改不了它。账号 JSON 加 `owner_id`、`owner_email`，列表可按 `owner_id=` 筛选。代理不变，仍按创建者。
- **新角色“代理供应商”（`supplier_agent`）**：普通用户和供应商的全部能力（只给这一个角色就够；插件权限：升级时取普通用户、供应商两个角色当时实际持有的插件授予，同意页去掉的、待复核的不给，之后新装的插件按 manifest 给普通用户、供应商的默认值一并给它），加上管理挂在自己名下的用户（下级，最多 8 层，含已删除的用户）：读、建、改下级用户并给他们分配供应商或代理供应商角色，只读下级的账号（不能改、测、看凭证，看不到下级的代理）和这些账号的使用记录。不能删除用户、重置密码。admin 和超管能分配它，operator 不能（也就不能修改、停用持有它的用户）。
- **新权限范围 `team`**（“自己及下级”）：`account:*`、`proxy:*`、`usage:account:read`、`user:read/create/update` 可带 `:team`（`account:create` 不行）；`user:read/create/update` 只接受全部与 team。下级是上级关系下的整棵子树（链最多 8 层）、含已删除的用户：每个用户存着自己的上级路径（`users.owner_path`），“A 的下级”就是路径含 A 的用户，由每条查询自己用一条走 GIN 索引的子查询读出，不缓存、不预先展开，改上级提交后各节点的权限判断、列表与详情立即生效（账号侧概览与汇总除外，见下文“账号侧使用记录”）。只有 team 范围用户权限的人只能管理（改资料、分配角色）自己的下级，不能对自己或别人操作。
- **新权限 `usage:account:read`**（敏感，看范围内账号服务过的请求，只给白名单字段）：供应商默认 `:own`（`rev` 2，有人持有的只补缺），operator 默认全部。侧栏新增“账号用量概览”“账号日志”，持有全站使用记录权限的人（admin、operator、超管）只看到全站的两项。
- **账号侧使用记录**：新接口 `/account-usage`（列表、`/summary`、`/overview`、`/realtime`、`/export`、`/:id`），给持有 `usage:account:read` 的人看自己范围内的账号（含已删除的账号）服务过的请求。只返回白名单字段：没有用户、API 密钥、分组、IP、收费金额（只有原价 `standard_cost`）、错误详情；调用链里范围外的账号只显示编号；用户、密钥、分组、IP 等筛选和分组一律 400 `not_allowed`。概览和汇总按调用者的范围在每个节点的进程里缓存，改用户上级或账号所属不让缓存失效，这两项在各节点上最多滞后 30 秒；列表、详情、导出与权限判断立即生效。`/usage` 不变。
- **用户的上级**（`users.owner_id`）：用户 JSON 加 `owner_id`、`owner_email`（上级已删除时仍显示邮箱）。新建、修改用户可传 `owner_id`（`0` 为无）：管理员可以指定任何人或无；代理供应商只能挂在自己名下，不传时也是自己。用户列表、详情、修改和实体查找（`/lookup/users`）都按调用者的范围：代理供应商只看得到、改得了下级，别的用户（包括自己）一律 404；列表新增筛选 `owner_id=<id>|none` 和 `direct=true`（只看直属下级）。上级不能是自己或自己的下级，链最多 8 层（含已删除的用户），上级必须按角色管得了该用户（否则 400 `owner_cannot_manage`，之后给该用户加上级管不了的角色，或改角色的权限、可分配清单、恢复默认使某个上级管不了它的下级，也一样拒绝，字段是 `permission_keys`、`role_keys` 或插件矩阵的 `roles.<角色>`）；改上级时操作者要管得了新旧上级以及它们之上的每一级（自己和自己之上的除外），所以 operator 不能借供应商把用户挪进或挪出代理供应商的下级；改上级时连同该用户下面整棵子树的路径一起在同一事务里改写，并发的两次改动不会成环；有下级的用户不能被硬删除（外键，用户本来只软删除）；代理供应商指定别人当上级一律 403，不会因用户是否存在而得到不同的回答；改上级审计 `user.owner`。
- **控制台**：权限选择器加“自己及下级的”（只在权限接受 team 时出现；用户权限只有“全部”和它）。用户列表加“所属”列和所属筛选，team 范围的人多一个“仅直属”；能管全部用户的人新建、编辑用户时可以指定所属，team 范围的人新建的用户归自己；新建用户总是明确提交角色：权限覆盖“用户”角色时默认勾选它（服务端允许任何创建者给无角色的新用户这个角色），team 范围的人再默认勾选可分配的“供应商”，勾选“供应商”时带上“用户”，不会退而默认勾选别的角色；只有 team 范围的人看不到删除和重置密码。账号列表加“所属”列（全部与 team 范围的人可见）和所属筛选，team 范围的人多一个“只看下级的”；有 `account:owner:update` 的人在账号操作里“更改所属”；“创建者”列与筛选照旧只给全部范围的人；持有 team 范围操作权限（如自定义角色的 `account:update:team`）的人在列表里下级的账号上也能看到对应操作，由服务端判断。新菜单“账号用量概览”“账号日志”（`/account-usage`，有 `usage:account:read`、没有 `usage:all:read` 的人可见）：按账号统计，账号名取自记录（已删除的账号也有名字），只显示白名单字段，费用按标准价（日志的统计条只有一项“原价”），没有用户、密钥、分组、IP 与计费信息。`/account-usage` 是新的控制台页面根，插件的 GET / HEAD 网关端点不能以它开头（`manifest.ConsoleSegments`）。
- **插件 SDK 新包 `sdk/hierarchy`**：在插件自己的表上存树（层级）：表按约定有父列和 `bigint[]` 路径列（GIN 索引），`Tree.Move` 在一个事务里检查自己、成环、找不到与最大深度并改写整棵子树的路径（锁住所涉两棵树的根行，同一棵树的移动依次进行），`Ancestors` 读路径，`Below` 是“某些节点之下的行”的子查询。核心的用户上级是它的第一个使用者；它不读核心的用户树，Host API 不变（CONTRACTS §2“插件数据库”、§103.2）。
- **`sub2api dev` 的内存 Redis 按时过期键**：进程内 miniredis 原来从不推进时钟，带 TTL 的键（冷却、会话、并发槽位、锁）永远不到期，账号冷却后一直不被调度。现在每 100ms 按墙钟推进一次（CONTRACTS §47）。单机 e2e 的 `TestGatewayFailoverAndCooldown` 因此能等到冷却结束；它对 401 之后状态的旧断言（`disabled`/`error`）一并改为 `auto_disabled`（§42）。e2e AC23 去掉供应商建账号时传的分组（供应商没有 `account:group:bind`），own 范围下 `created_by` 的断言改为“在范围内收窄”（§21.0）。
- **修复**：不是全部范围的人建、改、测账号带 `proxy_id` 时，读不到它可引用的代理范围（授权读库失败）原来当作没有代理，回 400 `proxy_id` `not_found`，现在回 503（CONTRACTS §103.2）。
- 更新检测结果的原因按代码显示中文/英文：`GET /system/update-check` 每条线新增 `reason_code`（`no_release`、`version_conflict`、`incompatible`，契约 §102），控制台不再直接显示“the update source has no stable … release”之类的英文原文。网关二进制变更，随下一个网关版本生效；网关 0.1.135 不带该字段，控制台照旧显示它的英文原因。

**迁移 0103**（`0103_user_owner.sql`，可重跑，只加列、约束和索引）：`users.owner_id`（外键、不能指向自己、部分索引）、`users.owner_path`（`bigint[] NOT NULL DEFAULT '{}'`，按 `owner_id` 递归算出已有行的路径，只改不同的行，重跑结果相同；最后一个必须等于 `owner_id` 的约束；GIN 索引）与 `accounts.owner_id`（外键、部分索引），`accounts.owner_id` 按 `created_by` 回填，重跑只补仍为空的行，不改已改过的所属。

**发布**：核心 0.1.292 带迁移 0103，走 `primary-first-v1`：旧核心建账号不写 `owner_id`，迁移之后不能再有旧核心建账号，所以不用滚动升级。0103 只加列、约束、索引，算出用户的上级路径（线上还没有上级，全是空路径）并回填 `accounts.owner_id = created_by`（只填空的，重跑不改已改过的所属，也不改账号的其他列和额度快照）；5 万用户、2 万账号的合成库上整个迁移约 0.36 秒（几乎都是回填），用户路径的计算、约束与 GIN 索引在 5 万用户上另加约 0.06 秒，已迁移的库重跑约 10 毫秒。**升级前预检**（在主库上执行，任何一项有结果先处理再建升级计划）：① `SELECT id, key FROM roles WHERE key = 'supplier_agent' AND NOT builtin;` 有行时，新核心在迁移之后启动同步失败（`builtinKeyConflict`，日志给出改名 SQL），主节点起不来、集群停在维护：先在线上改名，`BEGIN; UPDATE authz_meta SET version = version + 1, policy_version = policy_version + 1 WHERE id = 1; UPDATE casbin_rule SET v0 = 'r:supplier_agent_custom' WHERE ptype = 'p' AND v0 = 'r:supplier_agent'; UPDATE casbin_rule SET v1 = 'r:supplier_agent_custom' WHERE ptype = 'g' AND v1 = 'r:supplier_agent'; UPDATE roles SET key = 'supplier_agent_custom' WHERE id = <id>; COMMIT;`（授予与持有者跟着走；已经卡在维护时同样改名后 `sub2api-gateway resume`）；② `SELECT r.key FROM roles r JOIN role_assignable_roles ra ON ra.role_id = r.id JOIN roles s ON s.id = ra.assignable_role_id WHERE s.key = 'supplier' AND NOT r.builtin;` 列出能分配供应商的自定义角色：升级后 supplier 多了 `usage:account:read:own`（`rev` 2），持有这些角色的人自己没有它就不能再分配、管理供应商（`within`），要给该角色加 `usage:account:read:own`（admin、operator 已自带）；③ user、supplier 角色的核心授予与默认值不同时：supplier 有人持有，升级时补回缺少的全部默认权限（包括管理员删掉的，审计 `authz.sync.grant` `defaults_rev = 2`），多出的授予保留；代理供应商的核心权限按代码默认值，持有者的授予超出它（user、supplier 多出的权限或别的角色）时代理管不了这些用户（403 `missing`），需要时给 supplier_agent 加上同样的权限。**回退**：托管部署不能经升级计划回到 0.1.291（网关拒绝跨 schema），0.1.291 核心也拒绝带 0103 记录的库（`database includes migrations unknown to this core`），线上出问题修复前进。非托管部署实测 0.1.291 能在迁移过 0103 的库上照常运行（新列可空或有默认值，旧核心建用户不碰上级，按 `created_by` 判断，改过所属的账号回到创建者名下）；这期间旧核心新建的账号 `owner_id` 为空，再升到 0.1.292 前删掉 `schema_migrations` 里的 `0103_user_owner.sql`，启动时重跑 0103 补上。

### 核心 0.1.291 + 网关 0.1.135：删除为网关 0.1.133 与回退到 0.1.288 保留的代码，修正升级预检误报（`a0eac3c38`，滚动；无核心迁移，内置插件与 0.1.290 相同）

0.1.290 已在所有节点上线（核心 0.1.290、网关外壳 0.1.134），升级前的备份已删除，不能再回到 0.1.288。按“迁移完成后不留兼容代码”，删除只为外壳 0.1.133、核心 0.1.288 和这次回退保留的部分（CONTRACTS §101.3）。

- **上线**：北京时间 03:55:33–03:58:27。核心：计划 `8041bf318cb8884029391dee3061d87f` rolling-v1 completed（清单 `5a0fff61…3824`，03:55:33→03:56:41，先 ccp 后 ovh），`/readyz`（ovh 3130 与 api.prophey.ai）全程 200。网关：镜像 `sup2api-gateway:0.1.135` 逐个节点手动更换（先从节点 ccp 03:57:15–03:57:53，再主节点 ovh 03:58:10–03:58:27），每个节点先 `sub2api-gateway yield` 等 `/readyz` 503 且在途请求为 0 再重建容器；两节点 `updater.nodes.gateway_version` = 0.1.135、`self_update` = true。
- new-api 渠道 392 没有关（滚动发布，用户 11-12 要求）；不备份数据库。03:30–04:08 的请求 5xx 为 0（发布前 243 次、滚动与换网关期间 17 次、之后到 04:08 共 45 次）。
- 首次更新检测（03:59:27，默认来源 GitHub `Sub2API-Devs/sup2api`）两条线都返回“the update source has no stable core/gateway release”：还没有 `core-v*`、`gateway-v*` Release，节点也没有发布流水线的公钥，属预期。
- 回滚：核心用 0.1.290 清单 `8823ac9e…bb32` 建 rolling 计划（schema 相同）；网关把节点 `.env` 的 `GATEWAY_IMAGE` 改回 `sup2api-gateway:0.1.134` 后同样先让出再重建（0.1.134 启动时把空的 `cpu_percent` 列加回来，不影响服务）。部署细节见 [OVH 部署记录](next/deploy/gateway/ovh/README.md)。

- **托管核心必须有管理 socket 令牌**：`SUB2API_MANAGED=true` 时没有 `SUB2API_UPDATER_TOKEN_FILE`、文件读不到或内容不是 64 位十六进制，核心启动失败；转发升级接口与插件包读写总是带令牌，不再有“没有令牌就不带”的分支。非托管核心既不读这个变量也不读 `UPDATER_SOCKET`：升级接口返回 503 `updater_unavailable`，插件包存本地目录。线上的外壳 0.1.134（以及之前的 0.1.133）总是传这个变量并在启动核心前写好令牌文件，不需要改配置。
- **核心不再声明 `drain.graceful`**：`runtimecontract.Hello` 去掉 `capabilities` 与 `CapabilityGracefulDrain`，只有外壳 0.1.133 看它。`DrainRequest.graceful` 照旧：外壳 0.1.134 起按需要发送，核心照样让在途请求跑完。
- **网关外壳 schema 删除 `updater.nodes.cpu_percent`**（`DROP COLUMN IF EXISTS`）：只有外壳 0.1.133 读写这一列，0.1.134 不读。外壳启动核心时总是传令牌文件路径，不再判断它是否为空。
- **删除 0.1.290 的发布与回退手册**：`next/deploy/rollback/0.1.290-release.md`、`0.1.290-casbin-down.md/.sql`、`0.1.290-session-down.md`、`0.1.290-effective-permissions.sql` 只服务那次已完成的单向升级，删除（原文用 `git show 91e0c26bc:next/deploy/rollback/<文件名>` 读取）；只为它们存在的测试 `TestEffectivePermissionsScript`、`TestCasbinRuleOneWay` 与从 0.1.288 升级的 `TestManagedUpgradeFromProductionResumesWithTheSamePermit` 一并删除，后者固定的线上 schema 契约改为 0.1.290 的（迁移到 0102，`d78ba831…333c`，`TestReleasedMigrationsKeepTheProductionSchema`）。回到 0.1.285 的 `0.1.286-authz-down.md/.sql` 与 `TestAuthzHardeningRollback` 同样不再适用，一并删除，`next/deploy/rollback/` 现在为空（下一次单向发布再建）。已应用的迁移 0097、0098、0100–0102 的注释里仍写着这些文件名（迁移文件不能再改）。
- `next/deploy/gateway/.env.example` 去掉外壳 0.1.134 起不再读取的 `SUB2API_ALLOW_TOKENLESS_MANAGEMENT`。
- **rolling-v1 不再检查节点的 `rolling` 标记**（网关）：外壳 0.1.133 起都登记 `rolling=true`，资格只看签名策略、schema 不变与至少两个节点在服务。0.1.135 照常写 true、不再读；列保留，换外壳期间的 0.1.134 协调者还读它。
- **升级预检不再把刚启动的托管核心当成未托管**（网关）：预检、回退到基线和迁移屏障共用的“未托管核心”检查用心跳缓存的节点目录核对 Redis 里的核心登记，而这份快照早于本节点这次心跳写入的新 `core_boot_id`，核心重启后可能要再过一个心跳才不误报 `unmanaged or unrecognized active core`。改为从 PG 读启用节点的 `core_boot_id`；核心在 Redis 登记后、外壳下一次心跳前仍会被报出，误报不超过一个心跳（外壳 0.1.132 起有这个问题）。
- **真实核心测试 `TestRealCore*` 恢复可跑**：harness 给核心传它实际读取的 `DATABASE_URL`/`REDIS_URL`（原来是核心不读的 `SUB2API_` 前缀名）；清理测试库时一并删除核心为它建的插件数据库角色（角色属于整个 PostgreSQL 集群，留下来下一个测试装内置插件会 `plugin_db_role_conflict`）；滚动升级测试等内置插件 rollout 结束再预检；测试 API 客户端遇到 `verification_required` 用密码完成二次验证后重发；Responses WebSocket 测试按“优先级越大越先用”（2026-11-09 起）让被拒的账号先被选中；核心监听端口选在内核临时端口范围之下，避免被测试自己的出站连接占用；`next/deploy/ci/check.sh` 构建的两个测试核心改为 0.1.900/0.1.901，落在内置插件 `hostCompat`（`>=0.1.290 <0.2.0`）里。
- 发布流水线：核心与网关改由 GitHub Actions（`.github/workflows/next-release.yml`）按 `core-v*`、`gateway-v*` tag 构建 linux/amd64 与 linux/arm64、在受保护环境 `release` 里签名、先草稿后发布 GitHub Release；网关另推多架构镜像 `ghcr.io/<owner>/sup2api-gateway`。`sub2api-release` 新增 `pack --platform`（多平台一个清单）、`pack-gateway`、`mirror`、`pubkey`；删除 `deploy/gateway/github/prepare-assets.py`。
- 版本检测与一键更新（网关与核心同版发布；契约 CONTRACTS §102）：网关从更新源检测核心与网关两条线的新稳定版，控制台“版本与更新”面板分两条线显示，管理员点“一键更新”后导入验签过的发布、预检并建升级计划，从不自动升级。更新源在“设置 → 在线更新”：GitHub 仓库（默认 `Sub2API-Devs/sup2api`，私有仓库可填只读令牌）或带 `releases.json` 索引的 HTTPS 目录（可选 Bearer 密钥）；密钥只在把来源标为私有后填写（GitHub 来源旁有“在 GitHub 创建令牌”链接，打开预填好只读 Contents 权限、不过期的细粒度令牌页面），只写不读，用 `SUB2API_MASTER_KEY` 加密保存，只发给 `api.github.com` 或该目录的源站。网关一键更新用新计划策略 `gateway-rolling-v1`（逐个节点换网关二进制，核心不变；已是目标版本的节点不进计划、照常服务；单节点约 10 秒中断，需确认），可回滚（`gateway-rollback-v1`）。预检阻断项与确认原因按代码显示中文或英文说明。
  - 接口：新增 `POST /system/updates/apply`（`system:update:execute` + 二次验证）；`PUT /system/update-source` 改为整体替换 `{enabled, kind, repository, url, secret?}`；`GET /system/update-check` 返回 `core`、`gateway` 两条线。`PUT` 响应带 `changed`、`secret_change`，什么都没改时不写审计；预检与一键更新的阻断项、确认原因另带代码（`blocker_codes`、`reason_codes`）。核心转发 `update-check` 与一键更新时最多等 4 分钟（其他升级接口 60 秒）。删除 `POST /system/releases/import` 与审计 `system.release.import`，新增审计 `system.update.apply`。GitHub 来源翻页读最新 500 个 Release。只认 `core-v*`、`gateway-v*` tag 的 Release，不再读 `v*` tag 的 `releases/latest`。
  - 网关 schema（启动时幂等追加，无核心迁移）：`updater.clusters` 加检测开关（默认打开）、来源类型、URL、加密密钥列，原来表示“关闭”的空 `update_repository` 改成默认仓库，所以换上本版网关后所有集群都开始检测，不想检测要在设置里关掉；新表 `updater.update_checks`；`updater.nodes` 加 `gateway_version`、`self_update`；`updater.releases` 加 `line`、`source_assets`、`source_locator`：下载时按导入时的来源定位校验，开关检测、重存令牌不影响进行中的计划，改了仓库或 URL 后之前导入的发布要重新一键更新。
  - 网关：新子命令 `sub2api-gateway version`；新配置 `allow_private_update_sources`（`url` 来源允许内网地址）；已安装的网关放在 `<root>/gateway/<版本>/`，`<root>/gateway/current` 指定启动哪一版（换了更新的镜像时镜像自带的版本优先，只记 INFO）。第一次换上本版网关仍按镜像手动换；之后各节点都报告版本并能自更新时才出现网关的一键更新。`release_origin` 与 `import -manifest` 保留给离线发布。

**发布**：两条线互不依赖，顺序不限。核心 0.1.291 没有迁移，可以在外壳 0.1.134 下走 `rolling-v1`；外壳 0.1.134 照常发 `graceful`、传令牌文件。网关 0.1.135 按部署记录逐个节点更换，它照常驱动核心 0.1.290（外壳不检查 `capabilities`，多出的字段忽略）；换外壳期间 0.1.134 重启时会把空的 `cpu_percent` 加回来，之后的 0.1.135 启动时再删掉，都不影响服务。

### 核心 0.1.290 + 插件 + 网关 0.1.134：插件权限声明、Casbin 权限存储、Valkey 控制台会话、GORM、删除 CCGateway（`e3906cc68`，迁移 0099–0102，单向，停服发布，所有人重新登录）

- **上线**：北京时间 00:25:42–00:26:44，计划 `c50106034b2ed8086cdecf03deb4e6a4` primary-first-v1 completed（清单 `8823ac9e…bb32`）；**停服约 9 秒**（`/readyz` 503：api.prophey.ai 00:25:58.3→00:26:07.3，ovh 3130 00:25:58.2→00:26:08.2）；迁移 0099–0102 于 00:26:02 应用（连接池打开到 schema 校验通过约 0.35 秒）。内置插件 00:27:18 全部升到新版本（见下文“插件版本”）。之后两节点换上网关外壳 0.1.134（ccp 00:28:06–00:28:42，ovh 00:29:00–00:29:09），compose 去掉 `SUB2API_JWT_SECRET`。
- **渠道与备份**：用户选择“先关 392，切完恢复”：new-api 渠道 392 00:18:04 在控制台关闭，00:30:10 恢复；关闭期间 sup2api 无流量。用户选择“备份一次，确认后删”：00:24:20–00:25:27 在 ovh 全库 `pg_dump`（含 events，497 MB），确认发布无误后删除。
- **核验**：有效权限升级前后逐行相同（82 行）；casbin `p` 103、`g` 10，超管 1 个；JWT 三张表、CCGateway 的表和插件行已删除（ccgateway 记入 `plugin_key_retirements`，104 个已软删除账号与 46388 条使用记录保留）；失败策略 revision 6→7，去掉 8 个只有 CCGateway 会回的关键词；超管登录拿到 `s2s_` 令牌、没有 refresh token，`POST /auth/refresh` 404，登出后 401；00:30:10 起 5xx 只有 1 次上游 529。
- 发布前在 11-16 22:10 的生产副本上完整演练（迁移 0.8 秒、0102 重跑不变、全套冒烟通过）。部署细节见 OVH 部署记录。


一个发布单元，从线上 0.1.288（schema 0098）直接升级，四个迁移在同一次升级里依次执行。插件权限这部分开发时按 0.1.289 规划，**0.1.289 没有单独发布**。全部内置插件的 `hostCompat` 都是 `>=0.1.290 <0.2.0`；清单检查不再按用到的功能核对 `hostCompat`/`hostUICompat` 的核心下限，SDK 也不再为旧核心降级或报 `Unimplemented`（0.1.290 之前的核心不会与它同时运行）。步骤见当时的发布手册（已删除，`git show 91e0c26bc:next/deploy/rollback/0.1.290-release.md`）。

**发布**：单向，停服。发布前在生产副本上演练，用当时的 `0.1.290-effective-permissions.sql`（已删除，`git show 91e0c26bc:next/deploy/rollback/0.1.290-effective-permissions.sql`）逐个用户比较升级前后的有效权限，结果必须完全相同。托管部署在建升级计划**之前**备份整个数据库，然后建 `primary-first-v1` 计划：各从节点先把入口转发到主节点再停掉核心，主节点最后进入维护（停服从这里开始）、停掉旧核心，带迁移许可启动新版本，执行 0099–0102 并恢复服务（停服到这里结束），最后从节点启动新版本。计划从停从节点到迁移之间不停顿，备份之后、维护开始之前的写入在回退时会丢失。非托管部署先停掉全部节点（顺序不限），再备份，然后启动一个节点执行迁移，再启动其余节点。上一版签发的登录全部作废，所有人重新登录。事务级 advisory 锁的 id 全部换了（见下文“数据库访问改用 GORM”），新旧核心同时运行时彼此不互斥，所以只能停服升级：0.1.288 与 0.1.290 的核心任何时候都不能同时连着同一个库。

**升级停在中途**（例如 0100 因为多个超管被拒，此时 0099 已提交）：托管部署里 0.1.288 和 0.1.290 都不在这个库上提供服务，集群停在维护状态；按报错修复数据后，用同一个计划继续（`sub2api-gateway resume`），或者从备份恢复。

**回退**：目标是 0.1.288，只能从升级前的备份恢复，升级后的写入全部丢失；Valkey 不用清理。网关外壳也一起回到 0.1.288 时的版本（镜像、compose 和 `.env`，和数据库一起备份）；本次的外壳不能运行 0.1.288，升级计划在原来的外壳下执行，完成后再换外壳。没有反向脚本，也没有运行时开关；网关不允许跨 schema 回退，线上出问题首选修复前进。

#### 插件权限声明与角色分配（迁移 0099）

设计见 `next/docs/plugin-permissions/`，契约见 CONTRACTS §4“插件权限”、§5.2、§5.7、§5.8、§21.0“插件资源范围”。

- **插件权限按“权限 × 角色”矩阵分配**：安装、升级确认页和插件详情新的“权限”标签用同一个矩阵。插件详情的“授权”标签改名“权限”（旧链接 `?tab=grants` 自动跳到 `?tab=permissions`），分两部分：插件申请的访问权限（原“授权”，新增“收窄”和“重新授予”），以及插件提供给用户的权限 × 角色，可以直接勾选保存（只提交改动的角色；有未保存的修改时切换标签或离开页面会先确认）。角色编辑页的插件权限按插件声明的分组显示，标出低 / 敏感 / 关键三级风险，内置角色上标出与插件建议不同的项，可以“恢复插件建议”（只改编辑框，保存才生效）。
- **宿主权限收窄、重新授予**：`PUT /plugins/:key/grants/:permission`。收窄（列表型范围，每个列表至少留一项）只要 `plugin:manage`；已撤销、被拒的可以重新授予，要 `plugin:install`（与安装同意相同），并按风险要 `plugin:grant:high`/`critical`。两者都要二次验证，超出插件申请的范围被拒（`scope_too_wide`）。
- **插件资源范围**：插件可以给权限声明范围，角色可以只拿“全部”“自己创建的”或指定的几项（如某几个审核流程）；角色编辑页从 `GET /plugins/:key/scope-options` 选候选。核心转发插件路由时按范围放行、判 `scopeFrom` 指向的值，并把调用者的范围交给插件（`action_scope`）。本次的内置插件都还没有声明范围。
- **风险等级取代敏感标记**：权限分 low / sensitive / critical 三级；critical 等同核心的超管专属，安装时只有超管能授给角色、不进任何默认值。内置插件：`moderation:exempt` 改为 critical（新部署只有超管持有，线上已有的持有者保留）；`claude_oauth:manage`、`codex:manage`、`moderation:read` 提为 sensitive（只影响以后新建 key 的自动授予与升级是否要确认，已有授权不变）；默认授予的角色不变。
- 控制台 UI 3.16。

**行为变化**（外部脚本、插件作者需要留意）：

- `GET /permissions`、安装审查 `review`、`RoleGrant` 的 JSON 去掉 `sensitive`，改为 `risk`。同意请求的 `new_permission_roles` 改为 `{本地 key: {角色: true | [范围]}}`，旧的数组写法返回 400，`role_keys_for_new_permissions` 不再读取。
- 默认或继承的授予给了同意者不能指定的角色时，不再 403，改为挂起待复核（插件详情顶部的待确认授予），首次安装也一样；显式指定不能指定的角色仍 403。
- 插件新版本不再声明的权限标为“已移除”（以前直接删除）：授予保留但不生效，再次声明时原样恢复，不重新套默认值。标记和恢复各写一条审计（`authz.sync.remove` / `authz.sync.revive`，列出持有的角色）。插件升级收窄了权限范围（不再允许“自己创建的”、删掉某个可选值）时，不再接受的范围授予在新版本激活时删除；新版本只是同意、还没激活时，正在运行的旧版本的授予不受影响。角色编辑页可以取消勾选已移除的权限；通过 API 保存角色（`PUT /roles/:id/permissions`、`PUT /permissions/plugins/:plugin`）时不带上已移除的 key，会删掉这些授予。
- 角色编辑页禁用编辑者自己没有的权限，核心权限也一样（以前能勾选，保存时才被拒）。插件停用期间它的权限不生效，但授予照常算持有：持有者仍可在矩阵、角色页授出和修改。
- 插件菜单、页面、slot 按“任意范围持有”显示；iframe 桥 `permissions.check(本地 key)` 对不带范围的 key 只要持有任一范围即为 true，带范围的 key 按蕴含判断（持有全部范围也为 true，与服务端一致）。
- 安装审查按查看者计算：同一个 URL，不同的人看到的“能否指定”“会挂起”不同。没有 `role:read` 的查看者看不到旧 key 由哪些角色持有（`removed_user_permissions[].holders`、复活和改名行的格子初值与“会挂起”标记、继承来的 `new_role_grants`、插件列表与详情里继承来的待复核授予都省略）。
- **并发保存不再互相覆盖**：角色对象和插件权限矩阵带每个角色的修订号 `revision`；保存时带上读到的修订号，期间别人改过这个角色就返回 409 `version_conflict`，不写入；不带不检查。控制台收到后自动重读，只把自己改过的部分（名称、描述、权限、可分配清单逐项）合并到别人的版本上；别人也改了同一项时不覆盖，提示核对后再保存。细节见下文 Casbin 一节的“并发保存角色”。
- 通过角色编辑（新建角色、改角色权限、插件权限矩阵）**新授出关键（critical）插件权限**要二次验证，与安装同意、复核确认一致；只保留或撤销不需要。
- 角色权限保存的审计（`role.permissions`）记下删掉的权限（`removed`，含已移除的插件权限），插件矩阵保存另记该插件上的 `before`/`after`。
- 同意请求的 `new_permission_roles` 列出的权限，对改名继承同样生效：列出的角色以覆盖为准，没列的角色不继承（以前继承一律照常）；确认页上继承来的格子可以逐格修改。
- 暂存升级（另一个版本还在运行）的同意不再提前生效：新版本才有的权限建成“暂存”（`status = 'staged'`，不生效，停用再启用运行版本也不会打开；权限列表、插件权限矩阵、角色页都不显示，角色保存也不能授予或删除它的授予，只在安装审查页显示），重新声明的已移除权限不恢复、范围授予不裁剪，都等激活时再处理；新旧都声明的权限风险取两者较高的，范围和默认值仍按运行版本。之后再同意别的版本时，上一次暂存的权限连同授予作废（审计 `authz.sync.discard`），按新版本的建议重新结算。同意之后、激活之前旧 key 新增的持有者，在停用旧 key 的那次激活时列为待复核授予（审计 `plugin.role_grants.carry`），以后的升级不再重提。
- 插件路由的 `scopeFrom` 与账号路由的 `accounts.from`：query 参数名不分大小写匹配，每个值都校验；JSON body 里同名字段（不分大小写）出现多次返回 400 `duplicate`。
- 重新授予宿主权限（`PUT /plugins/:key/grants/:permission`）需要 `plugin:install`；请求体总是读取，不再依赖 `Content-Length`（以前分块传输的收窄请求会被当成“授予到清单申请的范围”）。
- 清单的 `defaults` 省略时不授予任何角色（以前 `defaultRoles` 省略时给 `admin`）。

**迁移 0099**（`0099_plugin_permission_meta.sql`，可重跑）：`permissions` 加 `risk`、`requires`、`group_key`、`group_label`、`group_sort`、`scope`、`defaults` 七列与约束 `permissions_risk_check`，`scope_type` 加宽到 `varchar(40)`，`risk` 按 `sensitive` 回填（`sensitive` 列由 0100 删除）。

**第三方插件不兼容**（不做旧字段兼容）：清单里还有 `userPermissions[].sensitive` 或 `defaultRoles` 的，打包和上传都被拒（`unsupported`），要改用 `risk`、`defaults`；用了新字段（`group`、`risk`、`requires`、`scope`、`defaults`、`permissionGroups`、`routes[].scopeFrom`）的插件只能装在 0.1.290 及以后的核心上。清单检查另要求：权限声明了 `scope` 的非 GET 路由必须写 `scopeFrom`（`required`）；`inheritFrom` 只能指向本清单已不再声明的 key（`invalid_value`）。已安装（含已上传、待同意）的旧版本照常运行和同意，但旧字段不再读取：同步时风险按 low 写入，新建的权限不授予任何角色。

**内置插件**：9 个声明了用户权限的插件（anthropic、claude-oauth、codex、dynweight、growth、guard、moderation、payment、volcengine）的清单已改用 `risk`/`defaults`，`hostCompat` 升到 `>=0.1.290 <0.2.0`，需要核心 ≥0.1.290；版本见下文“插件版本”。被管理员停用的内置插件不会自动升级（只升级已启用的），重新启用之前目录停在迁移回填的状态（风险只按旧的 `sensitive` 回填，没有 `defaults` 和分组，`moderation:exempt` 仍是 low）；已安装的旧格式版本再次同意或激活时风险按 low 写入，内置插件也一样（暂存同意取新旧较高的风险，不受影响）。

#### 权限改由 Casbin 判断，授予只存 `casbin_rule`（迁移 0100）

契约见 CONTRACTS §99。用户可见的权限不变：同样的角色、授予得到同样的结果。

- **存储**：迁移 0100 把 `role_permissions`、`role_permission_scopes`、`user_roles` 与超管标志转换成新表 `casbin_rule`（Casbin gorm-adapter 的列布局）的行，然后删除这三张表和 `permissions.sensitive`；之后所有授予与角色指派只写 `casbin_rule`（核心在业务事务里写，不经 Casbin 的管理 API）。已移除、已停用的权限的授予与软删除用户的角色照常保留。
- **判断**：每个节点一个 Casbin enforcer（`SyncedCachedEnforcer`，Casbin 自带的角色管理器），装入 active 权限的授予与超管行，用户的角色在第一次被问到时按需读入（最多记住 5 万个用户，10 万用户的库重载一次只要几毫秒）；每个权限判断都是 Casbin 的 `Enforce`（经它的决策缓存，命中约 0.2–0.4 µs），读库出错时返回 503 并记日志，不当作无权限，`/me`、菜单、访问密钥收窄用到的授予列表来自 Casbin（`GetImplicitPermissionsForUser`）。改授予、权限状态、角色定义（`authz_meta.policy_version` 随之变化）时各节点原地换掉授予、保留已读入的用户（删除角色或版本缺失时换一个新的 enforcer）；给用户指派角色、改用户状态时各节点只在内存里换掉这个用户的角色、作废这个用户的决策缓存。`authz:changed` 的 payload 改为 JSON `{"v","p"?,"u"?}`。
- **约束**：只有 `super_admin` 能是超管角色，持有者最多一人（`casbin_rule_one_superuser`，取代 0097 的触发器）；角色 key 不能是 `_self`，权限 key 不能是 `*`。库里已有这类数据时迁移报错停止，提示怎样改。
- **并发保存角色**：角色与插件角色矩阵带每个角色自己的修订号 `revision`（`roles.revision`），PATCH、PUT permissions、PUT assignable、reset、插件矩阵都可带回读到的修订号，不一致 409 `version_conflict`（`details.revision`）；写入返回的是本次写入的修订号；启动同步、删除角色（从别的角色的可分配清单移除）对角色的改动同样推进修订号；别的角色或用户的写入不会造成 409（CONTRACTS §5.2）。
- **范围授予只能点名存在的行**：给角色新增 `group/G`、`account/X`、`proxy/X` 范围时，对应的分组、账号（未删除）、代理须存在，否则 400 `invalid_argument`（字段错误码 `unknown_scope`）；删除它们时的授予清理先加授权锁，不会漏掉同时提交的授予，并由权限服务自己广播。
- **行为变化**：启动时发现自定义角色占用内置角色 key，报错给出的改名 SQL 改为一条同时改 `casbin_rule` 的语句（直接 `UPDATE roles SET key` 会让该角色的授予和指派失效）。`sub2api admin` 只在已迁移到本版本 schema 的库上执行；0100 因多个超管停止时，用 0.1.288 的 `sub2api admin recover-superadmin` 保留一个。

迁移前的自检、迁移后的核对 SQL 见当时的 `0.1.290-casbin-down.md`（已删除，`git show 91e0c26bc:next/deploy/rollback/0.1.290-casbin-down.md`）。应用过任何发布前构建的 0099、0100 或 0101 的开发、预发库都要重建，或恢复到 0098 的备份后再升级。

#### 控制台会话改为 Valkey 令牌，删除 JWT（迁移 0101）

契约见 CONTRACTS §100。

- **会话令牌**：控制台登录只签发 `s2s_` 令牌，存在 Valkey（只存哈希），一个令牌、服务端滑动续期（空闲 7 天、登录后最长 30 天，`SUB2API_SESSION_IDLE_TTL` / `SUB2API_SESSION_MAX_TTL`），没有 refresh token。撤销单个会话、登出立即失效；改密码、禁用、删除用户、撤销其他会话时批量删除，`token_version` 兜底。每个用户最多 50 个会话，再登录时结束最久未用的那个。二次验证后换发同一会话的新令牌（`POST /me/verify` 响应的 `access_token`）。
- **删除**：JWT 签发与校验、`POST /auth/refresh`（现在 404）、refresh token 与重放检测；核心不再读取环境变量 `SUB2API_JWT_SECRET`、`SUB2API_ACCESS_TOKEN_TTL`、`SUB2API_REFRESH_TOKEN_TTL`。网关外壳不再把它们传给核心，compose 模板、`.env.example` 去掉了 `SUB2API_JWT_SECRET`；回退时 `.env` 随备份恢复原值（CONTRACTS §100.7）。
- **迁移 0101**：删除 `refresh_tokens`、`login_sessions`、`auth_flows` 三张表。两步登录、通行密钥、TOTP 设置、二次验证的中间流程只存 Valkey。
- **Valkey 故障**：会话鉴权、登录、二次验证返回 503 并记 ERROR `tokenstore: valkey unavailable`，不回退；会话鉴权的 503 不会被改写成 401（控制台不会被登出）。访问密钥和网关 Key 不受影响；管理员禁用、删除用户、重置密码不依赖 Valkey。
- **行为变化**：重置用户的安全设置（`DELETE /users/:id/security`）现在会结束该用户的会话（重置自己时保留当前会话）。`POST /auth/logout` 不带请求体，只结束调用者自己的会话（以前不带 refresh token 时结束全部）。结束全部会话时 `token_version` 一次加 2。
- **API 调用方注意**：用会话调 `POST /me/verify` 的客户端（脚本、自动化）必须改用响应里的 `access_token`，旧令牌 30 秒后失效。
- **控制台**：401 直接回登录页（不再刷新令牌，删除跨标签页刷新锁与 keep-alive）；一个标签页登出或会话失效，其他标签页也回登录页。控制台 UI 3.17：`@sub2api/host` 新增 `fetchWithSession`（原始字节、流、下载类请求带会话发送），用到它的插件声明 `hostUICompat ^3.17`、`hostCompat >=0.1.290`；`Session` 只剩 `access_token`，控制台内部用的 `startKeepAlive`、`renewalDue`、`RENEW_BEFORE_MS` 删除（插件文档从未提供，内置插件没有用到，UI 不升主版本）。
- **被盗令牌的遏制**（取代 JWT 时期的重放检测，CONTRACTS §100.4）：“结束其他会话”同时给当前会话换令牌（`POST /me/sessions/revoke-others` 回 200 `{access_token}`），复制走的当前令牌 30 秒后失效；没有其他会话时这个按钮也可用。会话列表的 IP 与浏览器随续期更新为最近一次使用的（以前是登录时的）。两个标签页同时二次验证不再互相顶掉令牌：从宽限期里的旧令牌验证不再轮换，控制台只替换发请求时用的令牌（`@sub2api/host` 的 `session.adoptRotated`，UI 3.17；401 后改用别的标签页的令牌重发时，以实际发出的那个为准，`RequestOptions.onSentWith`）。
- **单机部署的 Valkey 开启 AOF**（`deploy/single`，`--appendonly yes --appendfsync everysec`，新数据卷 `redisdata`）：会话只在 Valkey，没有持久化时 Valkey 一重启所有人被登出且没有告警。从 valkey 换到 redis 镜像时先删掉这个卷。
- **可用性修正**：Valkey 内存满拒绝写入时，续期失败不再让已经通过鉴权的请求 503；请求恰好落在“撤销其他会话/改自己的密码”中间时，不再把刚保留的当前会话删掉；节点漏收“重新启用用户”的广播时，不再删掉重新启用后的新登录。

会话部分的说明见当时的 `0.1.290-session-down.md`（已删除，`git show 91e0c26bc:next/deploy/rollback/0.1.290-session-down.md`）。

#### 数据库访问改用 GORM

核心、网关和插件的数据库访问从 pgx 改为 GORM，驱动仍是 pgx，经 `database/sql` 连接池。语句的条件、锁、`ON CONFLICT`、`RETURNING` 不变，用户看到的功能不变。对外可见的变化：

- **插件 SDK 0.2.0 不兼容**：`Host.DB(ctx)` 返回 `*gorm.DB`（以前是 `*pgxpool.Pool`），第三方插件把 SDK 升到这一版后，用到它的代码和自实现的 `Host` 测试替身要改写。返回的句柄绑定这次调用的 ctx，ctx 结束后再用会报错，不要跨调用缓存（CONTRACTS §11.2）。已编译的旧插件二进制不受影响（gRPC 协议没变）。SDK 0.2.0 依赖 `runtime-contract`：插件的 `go.mod` 要加 `replace github.com/Sub2API-Devs/sup2api/next/runtime-contract => ../../runtime-contract`（CONTRACTS §2），内置插件已加。除 `runtime-contract/gormpg` 外不再有代码 import pgx（核心读 PostgreSQL 错误码也经它），驱动配置、数组类型与错误读取都来自这里（`TestNoPgxImports`、gormguard `[pgx-import]` 检查）。
- **`sub2api-plugin pack` 默认执行 `db check`**：有 `internal/model` 和 `database` 声明的插件，打包前会在临时 schema 上核对 GORM 模型与迁移是否一致（按系统目录比对列类型、NOT NULL、默认值、主键、索引和约束定义；迁移以受限角色执行；迁移里的函数、视图、序列等要在模型包的 `Unmanaged()` 里声明），这一步需要 PostgreSQL（`TEST_DATABASE_URL`；没设置时启动本机 testpg）。`--skip-db-check` 可以关闭。`sub2api-plugin db diff` 可以从模型生成下一个迁移（CONTRACTS §2）。
- **River 只轮询**：集群任务改由 `database/sql` 驱动的 River 执行，没有 LISTEN。事务里投递的运行、其他节点投递的运行，最多晚 1 秒被取走（CONTRACTS §77.1）。
- **连接池只由环境变量设置**：核心、网关、插件 SDK 用同一份 `runtime-contract/gormpg` 打开数据库，DSN 由 pgx 解析（取代手写的 `runtime-contract/pgdsn`）。连接池大小只由核心的 `SUB2API_PG_MAX_CONNS`（默认 20）、网关的 `SUB2API_GATEWAY_POOL_MAX_CONNS`（默认 8）和插件 SDK 的选项（`pluginsdk.WithDBPoolSize`，默认 4）决定；**`DATABASE_URL` 不能再带 pgxpool 的 `pool_*` 参数**（`pool_max_conns` 等），带了核心、网关外壳和插件启动即失败，报错只列参数名（线上各节点的 DSN 都没有）。所有连接固定 `DateStyle=ISO`、`IntervalStyle=postgres`（数组列按这两种格式解析）。
- **出口日志**：插件出口连接的日志行先按条数预留 id（`gormpg.NextIDs`，升序，取到的个数不对即报错），写进各自的行再插入，id 与行一一对应；以前依赖多行 INSERT 的 RETURNING 顺序，PostgreSQL 不保证这个顺序，连接结束时可能把结果写到别的连接的行上。日志行每条 INSERT 至多 100 行（`store.InsertChunk`，多于一条时在一个事务里，全成或全不成），列固定，不再读回默认值列；域名计数的 upsert 同样分条，一次待写的域名再多也不会超过 PostgreSQL 65535 个参数的上限（以前超过约 13000 个域名就一直写不进去）。插件管理、任务、事件、控制台资源等模块的语句全部改由 GORM 构造（插件迁移记录调用 `plugin_migration_record` 的函数调用除外），DDL、advisory 锁、系统函数只经 `store` 的小助手（`store.DDL`、`XactLock`、`CurrentSnapshot` 等），`EXISTS`、`INSERT ... SELECT`、`nextval`、`to_regclass` 的组合器由核心、网关、SDK 共用（`runtime-contract/gormpg`）。
- **网关**：升级协调的一步（Coordinate）和节点准入各在一个事务里完成，进度在提交后才计。
- **网关控制器与网关包不再手写整条 SQL**：改用 GORM 模型与查询构造器，生成的谓词、行锁（含 `SKIP LOCKED`）、`RETURNING` 与往返次数不变。事务级 advisory 锁全部改由 `store.XactLock` / 新增的 `store.TryXactLock` 按名字取（id 是名字的 `hashtextextended`）：以前 32 位 `hashtext` 的键（故障策略锁等）和网页资源、插件 schema、迁移函数 ACL 的固定数字 id 都换了，锁 id 与 0.1.288 不同，所以新旧核心不能同时运行（停服发布，见上文“发布”）；只有插件迁移锁仍按 id（`store.XactLockID`），与迁移执行器的会话锁同一个 id。网关新增 `internal/sqlguard` 测试（与核心、SDK 同一个检查器 `runtime-contract/gormpg/sqlcheck`）：允许列表之外的 `Raw`/`Exec`、交给 GORM 的非常量 SQL 文本都会失败（CONTRACTS §39）。
- **核心不再手写整条 SQL**：账号、计费、使用记录、用户、API Key、用户组、代理等模块的语句改由 GORM 模型与查询构造器生成，谓词、锁、`ON CONFLICT`、`RETURNING` 不变；使用记录的多行批量写入改为先预留 id（`gormpg.NextIDs`，多一次往返）再按每条 100 行 `Create`（`store.InsertChunk`：满 100 行的语句文本相同，一个连接预编译的文本至多 100 种；200 行的批次两条 INSERT）。分组价格规则的写入（PUT `/groups/:id/model-prices`）不再是一条数据修改 CTE，改为同一事务里的几条语句：表达式历史、按键序 `FOR UPDATE` 锁住改动的已有规则、删除、两级各一次 upsert（锁序不变，并发请求仍不会互相死锁）；outbox 事件改用类型化的行模型批量插入，不再每次格式化整批事件。整条手写的只剩账本的 data-modifying CTE 与 `store` 里锁、DDL、系统函数的小助手；gormguard 新增 `[raw-sql]`、`[sql-const]`：允许列表之外的 `Raw`/`Exec`、交给 GORM 的非常量 SQL 文本（含 `Select` 的全部参数、`Find`/`First`/`Delete` 等的内联条件、`Raw: true` 的列名与表名、经接口或方法值的调用）都会失败，允许列表里没有命中的条目也会失败（CONTRACTS §2“核心的 SQL”）。
- **权限（authz）**：角色、授予、指派、插件权限目录、核心同步、超管移交、范围授予清理与授权版本号的读写不再有手写的整条 SQL，全部改用 GORM 模型与查询构造器（`unnest` 批量写改为一次 `Create`，数据修改 CTE 拆成同一事务里先读后写的几条语句，准确性依靠已持有的 authz 锁）。行为不变；`authz.scope.clear` 审计现在和其他授权审计一样记下客户端 IP。
- **开发约束**（核心）：需要事务的函数（行锁、事务级 advisory 锁）在连接池上调用直接返回错误（`store.RequireTx`，gormguard `[tx-lock]` 检查加锁的那个句柄，跟到结构体字段和包变量）；`store.ForUpdate()` 改为函数，返回不可变的行锁（`.Of`、`.SkipLocked`），共享锁用 `store.Lock`；禁止 `Debug()` 与覆盖会话配置的 `Session`；`core` 不再 import `store`（`[core-store]`）。
- **开发约束**（插件、SDK）：插件和 SDK 的语句全部由 GORM 按模型生成（`Model`/`Table`、`Where`、`Create`、`Updates`、`clause.OnConflict`、`clause.Returning`），不再手写整条 SQL；`Raw`/`Exec` 只用来组合已生成的查询（`(?) UNION ALL (?)`、`pgdb.Exists`、`pgdb.InsertSelect`），非数据语句走 `pgdb.NextIDs`、`pgdb.LockTable`、`pgdb.TryXactLock`。`Raw`、`Exec`、`Where`、`Select`、`Order`、`Joins`、`Table`、`gorm.Expr`、`clause.Expr` 等的 SQL 文本必须是常量，值只走 `?` 参数，请求选的列经白名单进 `clause.Column`/`clause.OrderByColumn`；`pgdb.List` 的列与排序参数改为 `pgdb.SQL`，`pgdb.InsertSelect` 的尾部子句改为可变参数 `clauses ...clause.Expression`（`pgdb.InsertSelect(db, t, cols, sub, &clause.OnConflict{...})` 的写法不变）。不能用 GORM 的泛型 API `gorm.G`，也不能经 gorm 的 `ConnPool` 或 `DB()` 拿连接、`*sql.DB` 执行语句。SDK 的 `TestNoRawSQL` 检查 SDK、插件和工具，规则与核心相同（例外只有 `pgdbtool` 的系统目录读取、迁移执行与 DDL、`pgdb.LockTable`，以及连接池上的 `pgdb.Close` 和 testpg 的探测，按函数写明原因）。审核插件去掉了模仿 pgx 的 `queryRow`/`collect` 等封装。

#### 删除 CCGateway（迁移 0102）

用户决定：CCGateway 插件不再使用，整体删除，代码只留在 Git 历史里（最后的源码 `317b0c331`，例如 `git show 317b0c331:next/plugins/ccgateway/manifest.json`）。契约见 CONTRACTS §101.5。

- **删除的代码**：插件 `plugins/ccgateway`（含 companions：engine、worker、controller-go、guard、contracts、cli-mods、xray-egress，以及 `cli-reverse`）、核心模块 `internal/ccgateway`、`remotedocker`，以及只为它服务的 `helperhistory`（辅助历史）、`providerresources`（文件资源与技能）、`fallbackcredits`（回退额度凭证）和网关里对应的资源引用、辅助历史与回退额度处理；控制台的 CCGateway 页面，账号编辑器里的 Claude Code 授权与重新授权面板、容器语言与时区、特性策略和“自动配置模型”开关，账号列表的“容器已停止”提示，插件账号操作对 CCGateway 回包（`key`、`refreshed`）与原因码的专门处理（现在统一提示“已完成”或错误信息）；镜像构建脚本 `build-ccgateway-images.sh`、工作流 `ccgateway-images.yml`、构建参数 `REQUIRE_CCGATEWAY_IMAGES`、`sub2api-plugin pack --images`；`deploy/gateway/ovh` 的 `verify-ccgateway.py`、`verify-account-runtimes.py`；`docs/ccgateway-*`、`docs/CCGATEWAY-*.md` 与它专用的审计记录；只给它的 `-infinity` 时间列用的 `store.InfTime`。上游响应头的转发白名单是通用行为，保留为 `core.ProviderHeaders`。
- **账号生命周期契约一并删除**（CCGateway 是唯一的实现者，CONTRACTS §59、§101.5）：插件 RPC `OnAccountLifecycle` 与 `AccountPatch` 等消息、`pluginsdk.AccountLifecycle`、清单的 `accountTypes[].lifecycle`（账号预备、生命周期动作、删除时保留资源）与 `defaultModelsBySubscription`；账号订阅类型、“自动配置模型”（`auto_models`）、保存前插件改账号与插件只读扩展；额度查询的运行时目标（`runtime_operation`）。清单 `ui.accountActions` 的插件账号操作不受影响。没有插件声明过 `lifecycle`，内置插件不用改。
- **接口**：`/api/v1/system/ccgateway/*` 全部 404；`POST /accounts` 不再接受 `ccgateway_runtime`、`cc_feature_policy`；账号视图去掉 `runtime_parked`、`cc_feature_policy`、`rate_limit_tier`；使用记录详情去掉 `request_log_account_id`；二次验证去掉 `host.install` 范围、二次验证会话与错误码 `verification_expired`。生命周期部分：`POST /account-types/:plugin_key/:type/preparations`、`GET|POST /account-preparations/:key/:action`、`PUT|DELETE /account-preparations/:key`、`GET /accounts/:id/actions`、`POST /accounts/:id/actions/:plugin_key/:action_id` 404；`POST /accounts` 不再接受 `preparation_key`，`POST`/`PATCH` 不再接受 `auto_models`，删除账号（单个与批量）不再接受 `retain_resources`；账号视图去掉 `subscription_type`、`auto_models`、`lifecycle_sync`、`actions`，账号类型去掉 `lifecycle`、`default_models_by_subscription`；额度查询不再回 409 `runtime_not_ready`；错误码 `too_many_drafts` 删除。
- **内置插件**：`ccgateway` 不再随镜像内置（线上 0.1.133，已启用）。核心不会再安装或运行它；迁移 0102 删除插件行，所以不会留下从包存储启动的旧版本。
- **迁移 0102**（`0102_drop_ccgateway_and_legacy.sql`，可重跑）删除：
  - 只有 CCGateway 写的表：`ccgateway_runtimes`、`ccgateway_account_feature_policies`、`ccgateway_account_request_logs`、`provider_resources`、`provider_resource_contexts`、`provider_skill_versions`、`provider_fallback_credits`、`provider_diagnostic_messages`、`provider_helper_attempts`、`provider_helper_records`、`provider_helper_usage_outbox`、`provider_helper_usage_receipts`；
  - `accounts.status_changed_at`（连同触发器 `accounts_status_changed_at` 与同名函数，只给停容器用）和 `accounts.rate_limit_tier`（只由 CCGateway 写）；
  - 账号生命周期：`account_preparations`、`account_lifecycle_sync`、`accounts.subscription_type`、`accounts.auto_models`，定时任务 `core.account.lifecycle_status`、`core.account.preparation_cleanup`；
  - 设置 `ccgateway_remote`（控制器地址、密钥、镜像、分组的 CC 策略）；定时任务 `core.ccgateway.*`、`core.helper.*` 与 `plugin_key = 'ccgateway'` 的任务；
  - 失败策略里只有 CCGateway 会回的关键词：CLI/Worker 的错误码 `oauth_refresh_dead`、`oauth_account_on_hold`、`stored_login_refused_restart_needed`、`stored_login_refused_admin`，引擎的 `not_logged_in`、`has cleared its stored login`、`gateway concurrency limit reached`，Claude Code 客户端自己的 `oauth refresh token is no longer valid`。只在 `login-invalid`、`account-on-hold` 的关键词与迁移 0072/0074 写入的完全相同（含或不含 0075 追加的那个）、重试规则 `rate-limit` 的关键词仍是原默认值时去掉（`rate-limit` 仍叫原默认名时改名为 `Account quota (429)`），管理员改过的规则不动；有改动时旧策略进历史、revision +1。代码的默认规则同样去掉这些关键词，上游 API 的原文（如 `oauth access token has been revoked`、`invalid x-api-key`、`exceed your account's rate limit`）保留；
  - 插件本身，与 `install.Uninstall` 带 purge 相同：它的权限与这些权限的授予（`casbin_rule`，相关角色的 `revision` 与 `authz_meta` 的版本随之加一）、`plugins` 行（级联版本与暂存授予、宿主权限授予、事件游标与死信、任务运行、发布记录、节点、卸载屏障与粘性规则）、出口域名、插件迁移记录、`plg_ccgateway` schema 与带标记的同名角色（线上没有）；插件键记入 `plugin_key_retirements`。
  - **保留**：`plugin_key = 'ccgateway'` 的账号（线上 104 个，都已软删除）及它们的分组关系、使用记录、余额流水和审计日志；插件的执行记录、历史与出口日志（卸载任何插件都保留）。账号在控制台显示为插件已卸载，需要时由管理员删除；使用记录照常读出。
- **不能再安装**：`ccgateway` 列入已删除插件键，任何来源、任何签名的包都装不上（403 `plugin_key_reserved`）；官方签名的 0.1.133 包的 `hostCompat` 覆盖 0.1.290，没有这一条就能装回来，但核心一侧已经没有它需要的部分。
- **插件包上限**：从 1 GiB（压缩）/ 2 GiB（解包）降到 64 MiB / 256 MiB（另限 2000 个文件），约为现在最大的内置插件包（moderation，压缩约 16 MiB、解包约 45 MiB）的 4 倍；核心（`SUB2API_PLUGIN_MAX_PACKAGE_BYTES` 的默认值）、网关外壳（`plugin_max_bytes` 的默认值）与打包工具共用 `runtime-contract` 里的同一组常量。包下载、经外壳的传输与外壳从其他节点拉包的超时从 30 分钟降到 10 分钟（`runtime-contract` 的 `PluginPackageTransferTimeout`）。
- **发布前**：在生产副本上执行发布手册（已删除，`git show 91e0c26bc:next/deploy/rollback/0.1.290-release.md`）的只读核对，记下各项行数。**发布后**（需要用户同意）：停止并删除 ovh 上的 `ccg-controller`、`ccg-guard`、`ccg-state`、`ccg-shared-egress` 容器，步骤同一文件。

#### 删除旧版本兼容

0.1.290 之前的核心不会与它同时运行，只为混跑保留的代码删除（CONTRACTS §101）：插件 SDK 不再检查 Host API 版本、不再为旧核心回退或报 `Unimplemented`，清单检查不再核对功能的核心下限（`below_feature_core`、`below_ui_core`、`below_node_jobs_core` 不再出现）；网关外壳不再识别旧策略的升级计划、不再为没有遥测的旧外壳刷新 PG 心跳；账号状态不再把旧的 `error` 当作自动禁用；任务只按 `s2task_` 公开 ID 查询，任务框架之前的裸上游 ID 不再导入，迁移 0102 删除只为它记标记的 `async_tasks.legacy_imported`。均衡负载设置（`/settings/scheduling`）去掉只给旧核心看的 `mode`，迁移 0102 按原来读入时的规则归一库里的行后删掉这个键。控制台与核心同版本，去掉只为更早核心保留的分支：均衡负载表单的 `mode`；用量限制关闭时改发上次金额、终身限额改发周期长度 1、按 `UTC` 猜“从未保存”；侧栏在菜单缺设置入口时退回控制台菜单；API 密钥“选择全部结果”在前端复核筛选；卸载插件兼容 204；市场版本缺 `compatible`。仍为 0.1.288 时的外壳（网关 0.1.133）保留的只有核心声明 `drain.graceful` 和没有更新令牌时照常访问管理 socket，等所有节点换上新外壳之后的第一个核心版本删除。

#### 插件版本

相对线上（0.1.288 的内置集合）：

| 插件 | 线上 | 本次 | 变化 |
|---|---|---|---|
| anthropic（内置） | 0.3.2 | 0.3.3 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；模型目录改用 GORM，功能不变 |
| codex（内置） | 0.1.4 | 0.1.5 | 清单改用 `risk`/`defaults`（`codex:manage` 提为 sensitive），`hostCompat >=0.1.290`；版本号随清单升级（同一版本号、内容不同时线上会保留旧清单） |
| dynweight（内置） | 0.5.9 | 0.5.10 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；存储改用 GORM，功能不变 |
| moderation（内置） | 0.12.0 | 0.12.1 | 清单改用 `risk`/`defaults`（`moderation:exempt` 改为 critical，`moderation:read` 提为 sensitive），`hostCompat >=0.1.290`；存储改用 GORM，功能不变 |
| volcengine（内置） | 0.12.7 | 0.12.8 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；素材索引与视频账本改用 GORM，功能不变 |
| mailer、openai、gemini、custom（内置） | 0.1.2、0.4.0、0.3.0、0.1.0 | 0.1.3、0.4.1、0.3.1、0.1.1 | `hostCompat >=0.1.290`，用 SDK 0.2.0 重新编译，功能不变 |
| claude-oauth | — | 0.2.4 | 清单改用 `risk`/`defaults`（`claude_oauth:manage` 提为 sensitive），`hostCompat >=0.1.290` |
| growth | — | 0.2.1 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；存储改用 GORM，功能不变 |
| guard | — | 0.2.10 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；规则、统计、事件改用 GORM，功能不变 |
| payment | — | 0.1.2 | 清单改用 `risk`/`defaults`，`hostCompat >=0.1.290`；订单与通知改用 GORM，功能不变 |
| relay | 0.3.0 | 0.3.1 | `hostCompat >=0.1.290`，用 SDK 0.2.0 重新编译，功能不变 |

`tools/sub2api-plugin` 的 `TestReleasedPluginsBumped` 对照 `next/plugins/released.json`（线上各插件的版本和源码摘要，取自 0.1.286 打包的 `951525943`），发现内容变了而版本没升的插件就失败；每次发布后按该测试的说明重写这个文件。

上表的新版本都要求核心 ≥0.1.290，不能在线上的 0.1.288 上单独升级，要随核心一起发布。线上 0.1.288 没有安装 claude-oauth、growth、guard、payment。测试夹具：anthropic 的 0.3.4-test（`deploy/ci/check.sh`）和 guard 的 0.2.10-test（`e2e/guard.go`）。

## 2026-11-14

### 核心 0.1.285（`b12c9aede`，滚动；无迁移，内置插件保持线上版本）

- **客户端断开后的续读不再单独设上限**：计费设置删除“断开后最长继续读取”（`client_disconnect_wait_seconds`）。断开策略为“读完再计费”时，续读只受这次请求本身的超时约束（`relay_timeout_seconds`、流式空闲超时），与正常请求一致；服务停止时仍会中止续读。线上设置行里已存的 120 秒保留但不再读取（回滚到 0.1.284 时仍按它生效）。
- **Claude 默认 max_tokens 改为可开关的内置参数覆盖**：网关设置的“Claude 默认 max_tokens”从必填、强制生效的 `claude_default_max_tokens` 改为 `claude_max_tokens {enabled, models}`，默认关闭。开启后相当于给每个 Anthropic 账号的调用加一条参数覆盖：请求体没有 `max_tokens` 时按客户端模型取值，否则用 `default`；客户端给了的值不改；账号自己的参数覆盖在它之后执行，可以再改（CONTRACTS §84.1）。关闭时原生 Anthropic 请求原样转发；转换到 Anthropic 的请求因为 `max_tokens` 是协议必填字段，仍按协议补 8192。线上原来没有存过自定义值（一直是默认 8192），所以关闭状态下行为不变。
- 滚动期间如果在旧核心（0.1.284）的控制台保存网关设置，会丢掉新开关（旧核心不认识 `claude_max_tokens`），属于正常现象；滚动完成后再改即可。
- 内置插件集合 = 线上正在运行的版本（moderation 0.11.1、dynweight 0.5.7 等），develop 上未发布的 moderation 0.11.2、dynweight 0.5.8 不随本次发布。
- 北京时间 05:21:39–05:23:39 滚动升级（计划 `db8e5317`，约 120 秒，先 ccp 后 ovh），不停服、没关 new-api 渠道、不备份数据库。
- 回滚：滚动回 0.1.284（同一 schema），见 OVH 部署记录。

## 2026-11-13

### moderation 0.11.1（`790208acb`，只升级插件，核心 0.1.284 不动；无迁移）

- **AI 审核理由完整显示**：记录详情“逐级审核”的理由不再作为表格列（单行省略，表格比弹窗宽时整列被挤掉），改为在表格下方按“第 N 级 · 审核器”逐条完整显示，保留换行、长词断行；桌面、弹窗、手机、暗色一致。测试页、流程模拟复用同一组件。
- **理由上限 500 → 2000 字**：LLM 问题集与 agent 审核员共用 `clipReason`，超出时末尾加“…”。线上近 3 天理由最长 173 字，正常不会截断。
- **审核范围为空 = 只审输入**：`flows.scope` 为 NULL 或无效值时按 input（system / developer / user 文本、用户图片和文档）执行，缓存指纹随之变化。线上两个流程已在发布前通过管理接口显式设为 input。
- 封禁列表的原因点击展开全文；兼容下限提到 `hostCompat >=0.1.281`、`hostUICompat ^3.13`（用到 UI 3.13 的单元格展开行）。
- 北京时间 15:09:32 单独升级插件（rollout 268，约 5 秒，两节点同时切换），不停服、没关渠道、不备份数据库。
- 回滚：在 `stage/moderation-0.11.1/` 执行 `rollout.py upload-prev` 再 `rollout.py rollback`（回到 0.11.0，无数据变更）。

### moderation 0.11.0（`509655d10`，只升级插件，核心 0.1.284 不动；插件迁移 0013 删表删列）

- **清理 0.10.0 留下的旧结构**：删除 AI 分析的表 `user_analyses`、观察名单和审核记录上的分析列；设置里删除 `analysis_*`、`on_error` 和 0.1 时期的旧键。
- **固定名单只看观察名单**：“只后台审核 / 总是先审”的用户就是观察名单里的锁定条目，直接在观察名单里加锁、解锁；设置里的 `allowlist_user_ids` / `blocklist_user_ids` 删除（首次启动时逐条导入，线上两个名单为空）。
- **封禁窗口改为秒级**：新键 `ban_window_seconds`，用“数值 + 单位”控件；旧的小时值自动折算。
- 审核范围“除网关自己的输出外全部”一档不做（需要核心新增响应钩子，改动面大），CONTRACTS 写明理由。
- 北京时间 08:26:55 单独升级插件（rollout 267，约 5 秒，两节点同时切换），不停服、没关渠道；随后用管理接口保存一次设置，删除 13 个已存旧键，其余 15 个值不变。
- 回滚：先导出锁定条目，再把 moderation 退回 0.10.0（0.10.0 不读被删的表和列），最后把名单与封禁窗口写回旧设置键，见 OVH 部署记录。

### 核心 0.1.284 + moderation 0.10.0（`75780e65a`，滚动；核心无迁移，插件迁移 0011/0012 只加列）

- **提示词审核界面重做**：插件内的 10 个横排标签改成和“系统设置”一样的左侧子菜单（运行 / 策略 / 名单与处置 / 资源 / 系统 5 组）；一整页的设置拆成执行策略、缓存与输入、名单规则、Jev 默认题集、拦截提示、记录与保留、高级 7 个短页；旧链接 `?tab=settings` 跳到“执行策略”。“白名单 / 黑名单”改名为“只后台审核的用户 / 总是先审的用户”，并在观察名单里显示为锁定条目（只能通过名单修改）。
- **平台能力**：插件在 manifest 的 `ui.pages.<page>.nav` 声明分组和条目，控制台就用左侧子菜单展示（控制台 UI 3.14，CONTRACTS §90.7）。旧控制台忽略该声明，插件退回页内标签。
- **删除 AI 分析（AI 复核）**：按用户的 AI 分析、自动调整名单、相关设置与页面全部删除；流程里的第 2 级 LLM 复核是唯一的 AI 判断。旧的 `analysis_*` 设置保留为隐藏键（不读），表 `user_analyses` 下个版本删除。
- **审核范围**：流程新增“审核范围”，新建流程默认只审 system + user（不审 AI 回复和工具结果）；已有流程保持“全部内容”，行为不变。
- **命中重放**：缓存命中只重放命中的部分，新内容照常送审（以前整个请求按命中处理）。
- **失败策略**：新增“审核失败时”：默认放行并记为出错，可选先审请求返回 503；处理中字节超限也按它处理（以前对先审用户固定返回 503）。
- **匹配来源**：审核记录写入流程是怎么匹配到的（账号 / 任意分组账号 / 分组 / 默认 / 回退及原因）和处理方式来源；绑定的流程停用或未发布时，绑定页和概览给出提示，不再静默回退。
- **自动升为拦截级**：新增规则“在指定时间窗口（秒 / 分 / 时）内违规 N 次，自动升为拦截级”，默认关闭；按 watch_days 到期，名单历史记为 auto_escalate。
- 修复：自动观察不再把“只后台审核”的用户加入观察名单；判定缓存天数可以设为 0（关闭缓存）。
- 北京时间 06:42:50–06:44:03 滚动升级核心（计划 `27d93d22`），内置同步 06:44:14–06:44:20 把 moderation 从 0.9.3 升到 0.10.0（rollout 266，迁移 0011/0012 随之执行）；不停服、没关渠道，前后都没有 5xx。
- 回滚：核心滚动回 0.1.283；moderation 重新上传 0.9.3 后回滚（迁移只加列，0.9.3 可直接运行），细节见 OVH 部署记录。

### moderation 0.9.3（`ed988819c`，无迁移，核心 0.1.283 不动）

- **默认拦截提示改为英文**：请求被拦截时返回给客户端的错误信息（错误码 `moderation_blocked`）默认改为 “Your request was blocked by content moderation. Please revise your input and try again.”（原来是“提示词审核未通过，请调整输入后重试”）。设置页“拦截提示”的默认值和拦截响应预览同步变化。
- 已保存的值原样使用，不做迁移：线上如果存的是旧中文，需要在设置页手动改掉或清空（清空即用新默认）。
- 能力、授权、迁移、其它配置都不变。北京时间 00:07:50 单独升级插件（rollout 265，约 4 秒），不停服、没关渠道。
- 回滚：moderation 回 0.9.2（先重新上传 0.9.2 的包）。

## 2026-11-12

### 核心 0.1.283（`beb06edbb`，无迁移，滚动）

- **均衡负载去掉“默认”策略**：控制台的“调度策略”一行（默认 / 流量）去掉了，流量均衡始终开着。没保存过设置的部署，默认按“会话数”做流量均衡（以前默认什么都不做）。以前存成“默认”且没开上限负载的设置，指标按会话数、模型限额关；开了上限负载的保留原来的选择。设置行仍写 `mode: "traffic"`，旧核心也能读（CONTRACTS §89，设计 `next/docs/DESIGN-2026-11-12-BALANCE-DROP-DEFAULT.md`）。只有一个候选账号时不再读用量。
- 线上设置不变：仍是“流量 + RPM”，上限负载关。内置插件不变。
- 北京时间 22:16:13–22:25:12 滚动升级，约 9 分钟，两节点轮流让出，不停服、没关渠道，发布中和发布后都没有 5xx。
- 回滚：滚动回 0.1.282（同一 schema，细节见 OVH 部署记录）。

### 核心 0.1.282（`8c9b189a2`，无迁移，滚动）

- **分组定价第二轮复审修复**：价格试算接口要 `price:read` 加 `group:read`，按用户试算再要 `user:read`、按用户组试算再要 `segment:read`；试算用量限制在 0 到 1e12；试算的价格取自同一份价格快照。结算的待结算输入合并成一处；自定义价格缺历史表达式又带附加分项时记为 failed，不再按错的价格扣费。用户组的“专属价数”只算分组在售的模型。网关鉴权复用同一段 SQL。
- 控制台修复一批（表格不可展开时“展开”不做任何事等），UI 仍是 3.13。内置插件不变。
- 北京时间 21:04:31–21:14:51 滚动升级，约 10 分 20 秒，两节点轮流让出，不停服、没关渠道，前后都没有 5xx。
- 回滚：滚动回 0.1.281（同一 schema，细节见 OVH 部署记录）。

### 核心 0.1.281（`5b660cce6`，迁移 0095/0096，停服约 15 秒）

- **分组定价**：新增“用户组”。每个用户只属于一个用户组（默认、命名用户组，或只有自己的“个人专属”）；用户组决定能用哪些私有分组（授权）、全场折扣、在某个分组里的专属倍率。分组可以给模型单独定价（按全局价的倍数或自定价格），用户组还可以在分组内有自己的专属价；实付 = 标价 × 倍率。控制台新增用户组页，分组详情有“授权与定价”，用户有“分组与定价”抽屉，模型价格页和用户抽屉里都能做价格试算；`/me/models` 和 `GET /key/prices` 按用户组给出价格和倍率（CONTRACTS §97）。
- 用量记录冻结计价方式，新增 `base_cost`（按全局价、倍率 1 的金额），使用明细显示倍率和标价来自哪一层。
- **客户端中途断开**按策略计费，提示词不会被计两次（`bd584a377`）。调度均衡新增“会话数”指标（`108f00927`，CONTRACTS §89）。
- 控制台 UI SDK 3.13；插件用到新组件时声明 `hostCompat >=0.1.281`。内置插件不变。
- 北京时间 19:02:32–19:03:40 停服升级（primary-first-v1），两入口约 14–15 秒返回 503；new-api 渠道 392 在 18:59:21–19:08:48 关闭。
- 回滚：先清空 `provider_helper_usage_outbox`，再按 0.1.280 的清单建反向计划；0095/0096 新增的表和列 0.1.280 会忽略，不用删（细节见 OVH 部署记录）。

### 核心 0.1.280 + moderation 0.9.2（`531e6f4e3`，无迁移，滚动）

- **表单里选用户、分组、账号、代理也改成可搜索的选择器**（0.1.279 只改了筛选）：点开列出候选，可以按名称搜、输 ID，每行带 `#ID`；多选的已选项是可以关掉的标签，粘贴一串 ID 会一次全部加上。不再只列前 200 个分组或代理。
- **控制台**：账号编辑器的分组（标签 + “添加分组”）和代理、批量编辑账号的分组和代理、用户“分配分组”弹窗、删除带 API Key 的分组时选目标分组，都换成选择器。账号编辑器的转换策略提示按已选分组取信息。价格试算的示例按钮在窄屏换行。
- **提示词审核（moderation 0.9.2）**：手动封禁、添加观察的用户，绑定和命中预览的分组与账号（分组候选含默认分组 #0；账号不在所选分组时给出警告，这样的绑定不会命中请求），分析提示词、流程筛选、性能卡片的分组，都改用选择器；设置页的白名单、黑名单显示为“邮箱 #id”标签加“添加用户”选择器。绑定列表的账号名称按 ID 反查，不再拉全部账号。接口、能力、权限、迁移不变，设置表单只改了白名单、黑名单两个字段的控件，升级不需要重新批准。
- 控制台 UI SDK 3.12：新增 `EntityMultiPicker`（核心组件，也是插件的 `host.components.EntityMultiPicker`），`EntityPicker` 加 `exclude`，设置表单部件 `entity-select`（数组多选、标量单选）；插件用到时声明 `hostUICompat: "^3.12"`、`hostCompat >=0.1.280`（COMMON-CONTROLS §6、CONTRACTS §96.7）。
- 北京时间 17:48:26–17:58:11 滚动升级，约 9 分 45 秒，两节点轮流让出，不停服、没关渠道；完成后内置同步把 moderation 从 0.9.1 升到 0.9.2（17:58:23–17:58:27）。
- 回滚：先把 moderation 退回 0.9.1（要先重新上传旧包），再回 0.1.279（无迁移，按滚动方式，细节见 OVH 部署记录）。

### 核心 0.1.279 + moderation 0.9.1 + dynweight 0.5.7（`a01c79d51`，无迁移，滚动）

- **筛选里的用户、分组、API Key、账号、模型改成可搜索的选择器**：点开直接列出候选，可以按名称搜，也可以输 ID；每行带 `#ID`，用户带昵称、API Key 带所属用户、账号带类型，已停用的有标记。选中后显示名称和 ID，不用再记 ID 手输。已删除的对象仍可以按 ID 筛它的历史记录。
- **全站日志、使用日志**：筛选栏的模型、用户、API Key 和“更多筛选”里的分组、账号都是选择器。选了用户以后，API Key 只列这个用户的密钥；选了分组以后，这个分组的账号排在前面。筛选标签显示名称和 ID，不再只是一个数字。用户、API Key、账号、模型也可以不选具体对象、按“名称包含”搜；模型可以填价格表里没有的名字。使用日志（自己的记录）里只列自己的密钥、自己可用的模型和分组。
- **其他页面**：账号列表的分组、模型、创建人筛选，代理列表的创建人筛选，余额流水的用户筛选和调整余额弹窗里的用户，ccgateway 请求日志的模型筛选，都换成同一个选择器。
- **手机上时间范围显示完整**：全站日志、使用日志的时间按钮在手机宽度下分两行显示起止时间到秒，不再只显示“今天”。
- **提示词审核（moderation 0.9.1）**：审核记录的用户、分组、API Key 筛选和观察名单的用户筛选改用同一个选择器。**动态权重（dynweight 0.5.7）**：有效权重、权重事件页的账号、分组、模型筛选和配置弹窗里选账号、分组的地方改用同一个选择器。两个插件都只改界面，接口、能力、权限、迁移和设置不变，升级不需要重新批准。
- 新增只读接口 `GET /api/v1/lookup/{kind}`（users、groups、api-keys、accounts、models、proxies；CONTRACTS §96），按名称或 ID 查找、按 ID 取名称，权限沿用各自列表的读权限。控制台 UI SDK 3.11（`EntityPicker`、可搜索 `SSelect` 的扩展）；插件用到时声明 `hostUICompat: "^3.11"`、`hostCompat >=0.1.279`。
- 北京时间 05:12:55–05:14:07 滚动升级，约 72 秒，两节点轮流让出，不停服、没关渠道；完成后内置同步把 dynweight 从 0.5.6 升到 0.5.7、moderation 从 0.9.0 升到 0.9.1（05:14:18–05:14:25）。
- 回滚：先把 moderation 退回 0.9.0、dynweight 退回 0.5.6（两个新版本都要求核心 0.1.279，要先重新上传旧包），再回 0.1.278（无迁移，按滚动方式，细节见 OVH 部署记录）。

### 核心 0.1.278（`978316fea`，无迁移，滚动）

- **“无可用账号”能看到原因了**：请求选不出账号（529 “no available account”、429 “账号繁忙”）时，使用记录详情页多一块“账号调度记录”，列出分组里每个账号没被选用的原因（已停用、冷却中、模型列表不含请求的模型或请求引用的模型、限流、并发满等）；请求成功但途中因限流、并发满或失败切换跳过了账号时也会记。节点日志同时打一行带 request_id 的 WARN。以前这类请求只有一句话，查不到原因。
- **使用日志、全站日志的时间选择到秒**：默认打开为“今天 00:00:00 ~ 当前时间加 1 小时”（和 new-api 的使用日志一样），按钮上直接显示完整起止时间，自定义可以选到时分秒。“使用日志”原来默认最近 7 天，现在也是今天。页面开着超过一小时后新请求会落在范围外，需要重新选时间。仪表盘、用量概览、钱包流水仍按天选。
- 任务运行的推迟在一次拉取轮询内摊开，避免不该执行的节点一直占着一次运行。
- 内置插件集合里的 dynweight 换成已上线的 0.5.6，没有重建任何插件。北京时间 01:15–01:17 滚动升级，约 75 秒，不停服、没关渠道。
- 回滚：回 0.1.277（无迁移，按滚动方式，细节见 OVH 部署记录）。

### dynweight 0.5.6（`09d27fb16`，无迁移，核心 0.1.277 不动）

- **弹窗直接保存**：动态权重配置页里调整项、分组、账号覆盖弹窗的主按钮改为直接保存（先校验，有警告时确认），保存成功才关闭；以前只改页面草稿，还要再点底部保存栏的“保存”，漏点就没存，看起来像“改了不生效”。有未保存的改动时离开页面会先确认。
- **事件看得出按哪版配置、做了什么**：权重事件的说明里列出执行了哪些动作（`detail.steps`）和当时的配置版本（`detail.config_version`）；配置页显示当前版本和保存时间，回升过快抵消降权时给出提醒。`GET /config` 多返回 `updated_at`。
- 能力、授权、迁移、配置格式都不变。北京时间 01:04:45 单独升级插件（rollout 261，约 4 秒），不停服、没关渠道。
- 回滚：dynweight 回 0.5.5（先重新上传 0.5.5 的包）。

### 核心 0.1.277 + moderation 0.9.0（`777a57a61`，无迁移）

- **日志筛选精简**：全站日志的筛选从 28 项减到 13 项，使用日志从 18 项减到 10 项。常用的几项留在筛选栏，其余收进“更多筛选”，展开后在筛选栏下方整行显示，按钮角标是其中已生效的条件个数。用户、API Key、账号可以填 ID 或名称。
- **已生效条件以标签显示**：收起的“更多筛选”里已生效的条件，以及从链接或详情带进来、表单里没有对应字段的条件，在筛选栏下方显示为可关闭的“名称：值 ×”标签，另有“清除全部”。
- **请求 ID 也能查客户端请求 ID**：“请求 ID”一栏填客户端自己的 `X-Request-Id` 也能查到那条记录（选了时间范围时生效；不选时间只按我们的请求 ID 查，避免全表扫描）。CONTRACTS §95。
- **提示词审核插件（moderation 0.9.0）**：各页表格每个单元格只占一行，次要信息放进提示、列设置里的可选列或详情；行操作收进“更多”菜单；每个表格都有列设置（手机上也有）；审核记录的筛选从 17 项改为 4 项常用 + 9 项更多筛选；模式徽章显示在顶栏。只改界面，能力、权限、迁移和设置都没变，不需要重新批准。
- 控制台 UI SDK 3.10（`SFilterBar` 的 `#advanced`、`SFilterChips`、`SChipList`、`TableColumn.grow`、图标 `image`）。插件用到这些时声明 `hostUICompat: "^3.10"`、`hostCompat >=0.1.277`。
- 北京时间 00:51–00:54 滚动升级，两节点轮流让出，不停服、没关渠道；完成后内置同步把 moderation 从 0.8.2 升到 0.9.0。
- 回滚：先把 moderation 退回 0.8.2（先重新上传 0.8.2 的包；它对 0.1.276 也兼容），再回 0.1.276（无迁移，按滚动方式，细节见 OVH 部署记录）。0.9.0 要求核心 0.1.277，所以必须先退插件再退核心。

### 核心 0.1.276（`7892c7665`，迁移 0094）

- **限流改为 20 段滚动计数**：账号和模型列表的 RPM、TPM、会话数，每个统计周期分成 20 段计数（60 秒的周期每段 3 秒），按最近 20 段判断是否超限，准入判断的开销不再随流量增长。新的 Redis 键是 `rl:account:<id>:rpmb`、`tpmb`、`spmb:<秒>`、`spms:<秒>`，都是 HASH。同一账号内的同一请求或同一连接只算一个会话（没有粘性键的 WebSocket 连接按连接计）。
- **会话数可设统计周期**：账号编辑页“调度与限流”里的“会话数限制”一行，除了上限还可以设周期，按分钟或小时填写，范围 1 分钟到 24 小时，默认 1 分钟（与原来一致）。模型列表卡片和批量编辑也可以设。账号列表的限流格显示成 `4/8·5分` 的形式（已用/上限·周期）。CONTRACTS §71.1。
- 迁移 0094：`accounts.spm_window_seconds`（int，非空，默认 60，约束 60–86400）。现有账号都是 60，行为不变。插件清单的公共配置项可以声明 `spm_window_seconds` 的默认值和锁定（SDK 校验）；内置插件没有改。
- 北京时间 00:23:07–00:24:16 停服升级（primary-first-v1，有迁移不能滚动）：入口 api.prophey.ai 约 16 秒返回 503 或超时，ovh 3130 约 13 秒 503。new-api 渠道 392 这次没有关：00:05 关过一次，00:10 有人在控制台手动重新打开，用户回复“直接发布”。停服期间 392 有 4 次 503，new-api 重试到了其他渠道。
- 回滚：反向导入 0.1.275 的清单（`bc618812…0316`）之前，要先删掉 0094 加的列和约束，以及 `schema_migrations` 里 0094 那一行。删掉后 schema 回到 0.1.275 的契约，就可以滚动回退了。详见 OVH 部署记录。

## 2026-11-11

### 核心 0.1.275 + moderation 0.8.2（`b33650368`，无迁移）

- 0.8.1 上线后对这批改动做的一轮代码质量整理，缓存的规则不变。对使用者可见的只有提示词审核"概览"页的两行：新增"未写缓存的审核（期间有人工处理或缓存被换）"，原来的"正在写入共享缓存的结论"改叫"等待写入共享缓存的审核（当前）"。
- moderation 三处小的行为变化：片段特别多的审核写到一半失败时，已写进 KV 的键从本节点内存里删掉并通知其他节点；流程异常率告警的 KV 失败按缓存的口径计数；片段键在 KV 里的有效期向上取整到整秒。
- 核心：插件 KV 的校验规则只写一处（`sdk/protocol`），单键和批量调用、SDK 报同样的错；单键 `KVSet` 的有效期有了上界（过大的值原来会溢出成"不过期"，现在报错）；批量读超限时提前返回。SDK 拒绝负的有效期（原来不到 1 秒的负值会写成"不过期"）。现有插件没有传这类值的。
- 内部：判定缓存的代码统一用词、合并重复的写入逻辑、注释每件事只讲一处；测试拆成五个文件，运行时间减半；文档去掉从未发布的中间版本。详见 `RELEASE-MODERATION-0.8.0.md` 第 8 节、CONTRACTS §72 的 0.8.2 条目和 §93。
- 北京时间 23:16–23:25 滚动升级，两节点轮流让出，不停服、没关渠道；完成后内置同步把 moderation 从 0.8.1 升到 0.8.2。
- 回滚：moderation 装回 0.8.1 即可（先重新上传 0.8.1 的包，内置同步只升不降）；核心回 0.1.274（无迁移）。

### 核心 0.1.274 + moderation 0.8.1（`ba75109f3`，无迁移）

- **每个页面的标题和说明都显示在顶栏**，页面里不再另占一行。原来只有标题文字和菜单完全相同的页面才这样；个人设置、全站概览、全站日志、用量概览、使用日志，以及提示词审核、动态权重等所有插件页面，现在都一样。菜单名不变，顶栏显示页面自己的标题（菜单「个人设置」对应顶栏「个人中心」）。
- 详情页的版本号、状态徽章在宽屏下跟在顶栏标题后面（插件详情、价格编辑、价格同步、插件授权确认、发布进度）；手机上顶栏只放标题，这些内容留在页面里。
- 价格来源、价格编辑、价格同步页去掉了页头的「返回」按钮，插件详情去掉了「← 插件」一行：顶栏面包屑已有上级页面。
- UI 契约 3.9.0（`provideTitleContext` 的第二个参数改为 `PageHeadSink`，CONTRACTS §94）；现有插件不用重新构建。
- 带上此前已构建、未发布的 0.1.273：插件批量 KV 调用（`KVGetMany` / `KVSetMany` / `KVDeleteMany`，CONTRACTS §93，Host API 仍是 9）和 moderation 0.8.1（判定缓存改用批量调用，见 `RELEASE-MODERATION-0.8.0.md` 第 7 节）。0.1.273 没有单独上线。
- 北京时间 21:20–21:28 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：核心回 0.1.272（无迁移）；moderation 回 0.8.0 要先重新上传 0.8.0 的包再升级（内置同步只升不降）。

### moderation 0.7.4（`824a860a9`，无迁移，核心仍 0.1.272）

- 提示词审核“概览”“测试”页在 360 像素及更窄的手机上不再横向滚动：并排卡片的最小列宽原来写死 360 / 340 / 260 像素，比窄屏的内容区还宽，现在不超过容器宽度，窄屏自动变成单列。宽屏不变。
- 只改插件界面；接口、配置、迁移都不变。
- 北京时间 03:42 单独升级插件（约 5 秒），核心没动，不停服、没关渠道。
- 回滚：moderation 回 0.7.3。

### 核心 0.1.272 + moderation 0.7.3（`75f06b778`，无迁移）

- **手机上页面不再被撑出横向滚动**：列表卡片里放不下的内容在卡片边缘裁掉。0.1.271 核验时提示词审核“审核记录”页宽 427 px，原因是插件给分数标签写了比卡片格子还宽的固定宽度。
- **手机批量条的按钮放不下时**：可以横向滑动，被遮住的一侧渐隐提示；只露出一部分的按钮点一下只滚进视野、不触发操作（最后一个通常是删除），再点才执行。
- 会话绑定规则页、发布者密钥在手机卡片里不再伸出格子。
- moderation 0.7.3（只改界面样式）：手机上分数/分类标签、流程页的绑定分组在格子里换行显示完整，筛选栏按钮放不下时换行（英文界面观察名单页原来伸出屏幕）。宽屏不变。
- moderation 0.7.2（模型拒绝 `temperature` 时去掉参数重发）此前从分支单独构建上线，这次并入 develop；0.7.3 在它之上只多了样式。
- 只改控制台和 moderation 的样式；接口、Host API、UI 契约版本都不变，其余 9 个内置插件与 0.1.271 完全相同。
- 北京时间 02:34–02:37 滚动升级，两节点轮流让出，不停服、没关渠道；moderation 02:36:50 两节点切到 0.7.3。
- 回滚：核心回 0.1.271（无迁移），moderation 回 0.7.2。

### 核心 0.1.271（`4ec74abc7`，无迁移）

- **密钥页接入地址包含协议转换可达的地址**（CONTRACTS §92.5）：手动配置说明和编辑窗口的分组说明里，除分组自己平台的地址外，还列出经协议转换可调用的其它协议地址（分组说明里默认折叠）。快捷配置的客户端清单不变，仍只给分组自己平台的客户端。
- Magpie 手动填写说明的字段名改成 Magpie 界面上的叫法（API 密钥、Anthropic 地址、OpenAI 地址、Responses 地址、Gemini 地址、余额查询地址、余额字段），并说明各项在它的供应商表单里的位置。
- **手机上的批量条**：勾选后列表上方不再多出一段空白，占位改到列表末尾；批量条出现时右下角的“新建”按钮隐藏，页面底部只留一份空白。密钥页、账号页和用到批量条的插件页（动态权重、提示词审核）都生效，插件不用重建。
- 英文界面密钥页的单复数（1 key / 2 keys）。
- `/LOBE-ICONS-LICENSE.txt` 登记为控制台保留路径（0.1.270 漏登记，插件接口不能再占用这个路径）。
- 只改控制台和 SDK 的一行路径名单；接口、Host API、UI 契约版本都不变，内置插件与 0.1.270 完全相同。
- 北京时间 00:10–00:15 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：回核心 0.1.270（无迁移）。

## 2026-11-10

### 核心 0.1.270（`9fd26b12b`，无迁移）

- **API 密钥页重做**（CONTRACTS §92，参考 new-api 令牌页）：列表可按名称或密钥前缀搜索，按状态（已启用 / 已禁用 / 已过期）和分组筛选；可多选或跨页全选后批量复制、启用、禁用、删除。密钥列显示掩码，可就地显示和复制；状态列区分已启用、已禁用、已过期、额度用尽；新增“可用模型”“IP 限制”列，“限速”“创建时间”默认隐藏（列设置里可打开）；额度列改为显示剩余。行内启用开关改为“启用 / 禁用”文字按钮，“重新生成”移到编辑窗口。搜索框里粘贴的完整密钥只按前 12 个字符查找，明文不进请求地址。
- **快捷配置**：行操作“快捷配置”可一键导入 CC Switch（按客户端）和 Magpie（一个提供商带上该 Key 可达的全部协议地址和余额查询），也可打开手动配置说明。只保存了哈希的旧密钥不能一键导入。
- **平台品牌色**：平台、账号、模型名按厂商配色并带厂商标记（账号、分组、使用日志、用量概览、模型价格、我的模型各页）；分组徽章不带平台图标。分组页不再显示“可服务的平台”，没有账号的分组在账号数前给出警告图标；分组详情的接入地址包含经协议转换可达的端点。表格里的问题说明改为警告图标加悬浮提示。
- 接口：`GET /me/api-keys` 新增可选参数 `q`、`status`（`active`|`disabled`|`expired`）、`group_id`；不带参数时结果与之前相同，值非法返回 400。其余接口、Host API、UI 契约版本都不变，内置插件与 0.1.269 完全相同。
- 滚动升级期间，旧核心会忽略新的筛选参数、返回全部密钥（不报错）；控制台对跨页全选的结果按当前筛选再过一遍，批量操作不会越出筛选范围。
- 北京时间 22:22–22:33 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：回核心 0.1.269（无迁移）。

### 核心 0.1.269 + codex 0.1.3（`4f8ab5b44`，无迁移）

- **ChatGPT（Codex）官方用量重置次数**（docs/PLUGIN-CODEX.md §10）：Codex 设备码账号可查看 OpenAI 发放的官方重置次数（账号列表“余额”列的徽标、账号详情“官方重置”标签页），并用“使用官方重置”行操作消耗一次来清零已用满的用量窗口（需确认，不能撤回；没有可重置窗口时不扣次数）。新管理权限 `codex:reset`；codex 新增宿主权限 `accounts.auto_state`（重置成功后清除本插件设置的自动停用与冷却）。
- 核心：宿主接口 `SendAccountRequest` 经账号代理发送插件请求，`CLEAR` 自动状态时顺带清冷却（CONTRACTS §91）；控制台 UI 3.8：插件可声明账号详情标签页、账号行操作和 `account.list.quota` 额度装饰，均可按 `accountTypes` 只对指定账号类型显示（§91b）。Host API 仍 9。
- 其余 9 个内置插件与 0.1.268 完全相同。codex 0.1.3 要求核心 ≥0.1.269，内置同步等核心计划完成后才升级，两节点同时切换。
- 北京时间 19:38–19:40 滚动升级，两节点轮流让出，不停服、没关渠道；codex 19:40:40 切到 0.1.3。
- 回滚：回核心 0.1.268（无迁移），codex 退回 0.1.2。

### 核心 0.1.268（`b4b2caf0b`，无迁移）

- **插件设置只从已安装插件进入**（CONTRACTS §90）：系统设置视图去掉“插件设置”组，剩网关、分组与定价、计费与通知、运行环境四组。已安装插件列表 CCGateway 一行的“设置”打开 `/system/ccgateway`、邮件等插件打开 `/plugins/<key>?tab=settings`，侧栏停在控制台视图并高亮“已安装插件”，面包屑“已安装插件 > …”。旧地址 `/settings?tab=plugin:<key>` 跳到 `/plugins/<key>/settings`，`/plugins/ccgateway/settings` 跳到 `/system/ccgateway`。
- 只改前端与文档；内置插件与 0.1.267 完全相同（anthropic 仍 0.3.1）。
- 北京时间 15:57–16:09 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：回核心 0.1.267（无迁移）。

### 核心 0.1.267 + anthropic 0.3.1（`b278765d3`，无迁移）

- **侧栏第四版**（CONTRACTS §90）：分组、模型价格搬进“系统设置”的新组“分组与定价”（`/groups`、`/prices`、`/prices/sources` 打开时侧栏是设置视图，面包屑以“系统设置”开头）；CCGateway 不再占系统菜单，只在系统设置“插件设置”组和已安装插件列表的“设置”按钮进入（`/system/ccgateway`）。管理员组平铺只剩账号、代理、动态权重、提示词审核，然后是用户、插件、火山方舟、系统四个子菜单和系统设置。
- 去掉“平台”菜单（`/platforms` 跳到 `/accounts`，平台标识一律取 `/me/platforms`）、“余额流水”菜单（用户列表行菜单的“余额流水”仍可打开 `/ledger?user_id=…`）和 anthropic 插件的“模型目录”菜单。
- anthropic 0.3.1：manifest 去掉 `ui` 与 `ui.menu` 宿主权限（只减不增，升级不需要管理员确认）；`GET /models` 管理接口保留。其余 9 个内置插件与 0.1.266 完全相同。
- 北京时间 14:55–15:06 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：回核心 0.1.266（无迁移），anthropic 退回 0.3.0。

### 核心 0.1.266（`4a34c1e8b`，无迁移）

- **内置会话绑定规则只按协议匹配**（CONTRACTS §15.5）：`claude-code-session`（`anthropic.messages`）去掉 `models:["claude-*"]`，与 `openai-prompt-cache-key`（`openai.chat`、`openai.responses`、`openai.responses_ws`）一样不限模型、不限 User-Agent。无迁移：核心启动时内置同步把未改过（`customized=false`）的内置规则改写为新定义；管理员改过的保持原样，点“恢复默认”后同样不限模型。
- 规则编辑框的模型、User-Agent 为空时占位显示“任意”（原先是 `claude-*`、`claude-cli` 示例，看着像已填值）。
- 内置插件与 0.1.265 完全相同，插件都不重建。
- 北京时间 13:37–13:45 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：回核心 0.1.265（无迁移）。

### 核心 0.1.265 + dynweight 0.5.5（`83ddf09f4`，无迁移）

- **侧栏第三版：设置归设置，功能归功能**（CONTRACTS §90 重写）：系统设置里的功能页搬回控制台侧栏。管理员组平铺账号、分组、平台、代理、模型价格、CCGateway、模型目录、动态权重、提示词审核、余额流水；“用户”（用户、角色与权限）、“插件”（已安装插件、插件市场、发布者）、火山方舟、“系统”（集群节点、定时任务、核心升级、审计日志）是可展开的子菜单；最后一项“系统设置”。
- **系统设置换侧栏**：点“系统设置”后整个侧栏换成设置菜单（网关、计费与通知、插件设置、运行环境四组），顶部“返回控制台”回到进来前的页面；设置页里的第二列导航和手机下拉框去掉。设置页地址仍是 `/settings?tab=`；插件设置在 `/plugins/<key>/settings`（`/settings?tab=plugin:<key>` 跳过去）。
- **CCGateway 独立页** `/system/ccgateway`，只在 ccgateway 插件启用时出现在菜单里；`/plugins/ccgateway/settings` 跳过去。`GET /me/menus` 形状和区 id 不变，SDK 不变。
- 内置插件 dynweight 0.5.5：清单只把菜单 `section` 从 `system` 改回 `gateway`，其余内置插件与 0.1.264 完全相同。
- 北京时间 05:22–05:26 滚动升级，两节点轮流让出，不停服、没关渠道；计划完成后内置同步把 dynweight 热切换到 0.5.5。
- 回滚：回核心 0.1.264（无迁移）；dynweight 0.5.5 装在旧核心上菜单进网关区，可不降级。

### 核心 0.1.264 + dynweight 0.5.4（`d2af79927`，无迁移）

- **侧栏三组**（CONTRACTS §90）：控制台（仪表盘、API 密钥、模型与价格、用量概览、使用日志，有全站权限再加全站概览、全站日志）、个人中心（钱包、个人设置，接插件的“我的”菜单）、管理员（账号、分组、模型价格、模型目录、提示词审核、余额流水、用户、插件子菜单，最后一项“系统设置”）。`GET /me/menus` 的形状和区 id 不变。
- **系统设置**：平台、代理、角色与权限、已安装插件、插件市场、发布者、集群节点、定时任务、核心升级、审计日志并入 `/settings` 的外壳，左侧二级导航分 网关 / 计费与通知 / 用户与权限 / 插件 / 插件设置 / 系统 六组，各页地址不变，顶栏面包屑“系统设置 › 页面”。
- 内置插件 dynweight 0.5.4：清单只把菜单 `section` 从 `gateway` 改成 `system`（动态权重进“插件设置”组），其余内置插件与 0.1.263 完全相同。
- 北京时间 03:34–03:39 滚动升级，两节点轮流让出，不停服、没关渠道；计划完成后内置同步把 dynweight 热切换到 0.5.4。
- 回滚：回核心 0.1.263（无迁移）；dynweight 0.5.4 装在旧核心上菜单进旧“系统”区，可不降级。

### 核心 0.1.263（`3c89d9f12`，无迁移）

- **同优先级均衡负载**（CONTRACTS §89）：系统设置 → 调度策略 分为“故障策略 / 均衡负载”两个标签（`/settings?tab=failure&sub=balance`）。均衡负载四项：均衡策略（默认 / 流量）、基于上限负载调度、均衡指标（RPM / 并发 / TPM）、根据模型限速。只影响同一优先级内新会话和换号的挑选，已绑定的会话不迁移。
- 新接口 `GET/PUT /settings/scheduling`，设置存 `settings` 表的 `scheduling` 行，缺行即默认值（全部关闭），默认值下调度结果与之前完全一致、不读 Redis。核心改变了层内占比时，`usage_logs.sched_decisions` 多一条 `"plugin":"core"`。
- 内置插件与 0.1.262 完全相同，插件都不重建。
- 北京时间 01:42–01:44 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：直接回核心 0.1.262（无迁移；`scheduling` 行旧核心不读）。

## 2026-11-09

### 核心 0.1.262（停服发布，迁移 0092、0093）

协议层三期（设计见“sup2api 核心通用协议层设计”）与同期各线程的改动一起发布。

- **声明式上游与自定义上游**（CONTRACTS §86）：账号类型在清单里用 `upstream` 声明上游（预设 openai/anthropic/gemini，可覆盖路径、鉴权、请求头、过滤字段、固定改写），核心本地构造请求，插件进程拿不到 API Key；只有清单的插件（`runtime: "none"`）；插件可用 `extend` 在声明结果上追加。openai 0.4.0、gemini 0.3.0、relay 0.3.0 改为只有清单，anthropic 0.3.0 只保留模型目录；新内置插件 custom 0.1.0：新建账号里的“自定义上游”（OpenAI / Anthropic / Gemini 兼容三类），账号上可改鉴权、请求头、各协议路径。
- **参数覆盖与插件的顺序**（§84）：账号参数覆盖先执行，插件在覆盖结果上再改；请求头同样插件的头盖过覆盖的头。鉴权头只由上游声明的 auth 模板写。影响：codex 这类老插件自己构造的请求头不能再被账号参数覆盖改掉。
- **通用配置项**（§87）：插件可按账号类型声明调度、限流、用量限制、模型映射、参数覆盖、请求头透传、超时、代理各项的显示 / 可改 / 默认 / 锁定（代理可声明必填），核心强制；锁定值在账号下次保存时写入。核心编辑器开放给插件（UI 3.7）；新插槽 `account.editor.section`。
- **四种协议互转**（§88）：OpenAI chat、OpenAI responses、Anthropic messages、Gemini 两两互转（含流式、工具调用、推理、图片），质量分 good / fair / discouraged（Anthropic ↔ Gemini 经 Responses 两跳）。调度先按优先级，同优先级内原生优先、质量高的先用。转换默认宽松（allow），账号或分组可设 safe / strict；转不了或策略拒绝时跳过该账号类型，都不行才回 400 `unsupported_conversion`。转去 Anthropic 时缺省 `max_tokens` 由网关设置 `claude_default_max_tokens`（默认 8192）给出；发给 Gemini 的图片链接由核心下载（`media_download_max_mb`，默认 64）；没有计数端点的账号类型 count_tokens 本地估算。使用记录新增转换明细（等级、步骤、丢失），可按转换筛选。
- **调度优先级反转**：`priority` 改为数值越大越先用（与 new-api 一致）。
- **账号界面**：操作改为文字按钮（全平台按钮统一为文字），限额数字四色显示，新建账号默认并发 0（不限）；账号编辑窗口各步骤固定同一尺寸。
- **仪表盘**：统计改读小时汇总表 `usage_hourly`（迁移 0092，含 usage_logs 触发器），运营统计查询放宽超时。
- 内置插件：openai 0.4.0、gemini 0.3.0、anthropic 0.3.0、custom 0.1.0（新，启用）、dynweight 0.5.3、moderation 0.7.1；ccgateway 0.1.132、codex 0.1.2、mailer 0.1.1、volcengine 0.12.6 不变。
- 回滚：回 0.1.261 前要先把插件退回旧版、停用 custom，再撤掉 0092、0093 新增的表、列、触发器（见 OVH 部署记录）。

### 核心 0.1.261（`56efabffd`，无迁移）

- 账号限额悬浮卡片的按模型明细全部从 Redis 读取，`GET /accounts/:id/rate-models` 不再查使用记录：网关每次结算时把本次 token（与 TPM 同口径）加到账号按 UTC 整点的哈希 `rl:account:<id>:tpd:<整点 unix 秒>`（field = 客户端模型，TTL 26 小时，仿 new-api 渠道的 TPD 哈希）；TPD 按请求时区的 0 点起累加各小时，模型名单取这些小时和最近一小时出现过的模型（按 TPD 最多 100 个）（CONTRACTS §68.7）。
- 升级当天（11-09）的 TPD 从各节点换上新版本起算（北京时间 ccp 16:25:33、ovh 16:26:54），之前的用量不计入；次日起完整。
- 卡片说明文字（中英文各一条）随口径更新。内置插件与 0.1.260 完全相同（codex 0.1.2）。
- 北京时间 16:23–16:27 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：直接回核心 0.1.260（无迁移）。

### 核心 0.1.260（`3a9c661ff`，无迁移）

- 账号列表“并发/RPM/TPM/会话”列的悬浮卡片按模型显示明细（仿 new-api 渠道的 RPM / TPM / TPD / SPM 明细）：表头 模型 / 并发 / RPM / TPM / TPD / 会话，第一行是全部模型，其余按并发、RPM、TPM、TPD 倒序；卡片打开时每 2 秒刷新。TPD 是浏览器所在时区当天的非缓存输入加输出（来自使用记录）。
- 网关每次尝试多登记一个不设上限的按模型并发槽位（`account-md:<model>`）和按模型的 60 秒窗口计数（`md:<model>`），只用于展示，不参与限流和调度；新接口 `GET /accounts/:id/rate-models?tz=`（CONTRACTS §68.7）。
- 内置包里的 codex 换成线上已在用的 0.1.2，其他插件不变。
- 北京时间 15:21–15:24 滚动升级，两节点轮流让出，不停服、没关渠道。
- 回滚：直接回核心 0.1.259（无迁移）。

### codex 0.1.2（`7f1f0b733`，插件单独升级，核心不变）

- 设备码登录（`codex_device`）账号显示 5 小时与周用量两个额度窗口，自动刷新；查询由核心发送，插件只构造请求（manifest `quota`）。`chatgpt_siwc` 账号仍不提供额度窗口。无新增宿主权限。
- 北京时间 14:52:55 上传升级，两节点约 3 秒切到 0.1.2，不停服、没关渠道。
- 回滚：升级回 codex 0.1.1（包仍在插件存储）。

### 核心 0.1.259（`84f6121ed`，无迁移）

- 账号列表改为按添加时间倒序（`created_at DESC, id DESC`），新账号排在最前；改优先级或权重不再让行换位置，控制台自动刷新时行保持不动。调度顺序与列表顺序无关，不受影响（CONTRACTS §18.2，`1439c016f`、`028b7e919`）。
- 账号列表布局压缩（`84f6121ed`）：紧凑模式下额度单元格每个窗口一行，查询报错改成第一个窗口旁的警告图标，观测时间和被动标记移到各窗口的悬浮提示（详情页不变）；登录邮箱与账号名重复时不再多显示一行；整页只有一个创建人时隐藏创建人列；测试、编辑改为图标按钮；优先级、权重、最近测试列收窄，紧凑行左右边距 0.5rem。
- 插件不变（moderation 0.7.0、dynweight 0.5.2、codex 0.1.1 等照旧）。
- 回滚：直接回核心 0.1.258（无迁移）。

### 核心 0.1.258 + moderation 0.7.0 + dynweight 0.5.2（无核心迁移；moderation 插件迁移 0009）

- 修复：新建 ChatGPT（Codex）账号时提示“无法加载插件表单”。`GET /ui/plugins` 以前会漏掉只有原生入口（native 账号表单）的插件，现在照常返回；插件列表或插件模块还在加载时，账号编辑页显示加载中，不再显示“表单不可用”（`ba0eb813f`）。
- 账号列表状态列前加了启停开关（手机卡片在标题旁）：只有已启用的账号显示为开。打开就是手动启用，会清除自动禁用；关闭就是手动禁用。批量启用也包括自动禁用的账号；行菜单给自动禁用的账号加了“手动禁用”（`5c5fa32dc`）。
- 新钩子点 `gateway.attempt`（CONTRACTS §85）：选定账号后、发往该账号之前，每次尝试都执行一次（含换账号重试和 WebSocket 每一轮），插件能拿到账号 ID（`RequestMeta.account_id`），只能放行或拒绝。`gateway.request` 钩子可以回 `skip_attempt_hooks`，让核心跳过本插件这次请求的 attempt 钩子（`787bd4bdd`）。
- moderation 0.7.0（内置包自动升级，插件迁移 0009 新表 `flow_accounts`）：审核流程除了绑定分组，还可以绑定账号（分组内某个账号，或任意分组的某个账号）。按最具体的绑定生效：分组+账号 → 账号 → 分组 → 默认分组 0。只按分组绑定的请求照旧在请求入口审核，按账号绑定的在选定账号后审核（`96edf882c`）。需要核心 0.1.258。
- dynweight 0.5.2（内置包自动升级）：评估窗口最短 3 秒（与 new-api 一致），评估任务每 5 秒运行一次；关闭插件时立即重置所有已降权的权重；已不再调度的账号不再评估；基准权重变化后不丢弃新流量的第一个窗口；状态颜色改用组件色调，账号名称批量读取（`f80f85a01`、`a2792fd9a`、`0b24a961f`）。
- codex 仍是 0.1.1，其他插件不变。
- 回滚顺序：先把 moderation 回退到 0.6.2（0.7.0 要求核心 0.1.258，账号绑定存放在 `flow_accounts`，0.6.2 不读它），再回退核心到 0.1.257（核心无迁移）；dynweight 可回 0.5.1。

### 核心 0.1.257 + dynweight 0.5.1 + codex 0.1.1（`dca8628ef`，迁移 0091）

- 账号新增“请求参数覆盖”（CONTRACTS §84，迁移 0091 `accounts.param_override`）：格式兼容 new-api 渠道的 `param_override`（旧的平铺对象和 `operations` 操作列表），可改请求体（set、delete、追加、替换、正则替换、copy/move 等）和请求头（set_header、delete_header、pass_headers 等），支持条件和 `return_error`。每次尝试在模型映射之后、插件构造上游请求之前执行。账号编辑页“高级 › 请求转发”里编辑，弹窗有模板、JSON 视图和试运行（`POST /accounts/param-override/preview`，试运行会标出执行失败的操作）。
- 新增内置插件 codex 0.1.1（随核心内置包安装并启用）：ChatGPT 订阅账号，支持 Codex 设备码登录和 Sign in with ChatGPT，提供 /v1/responses（含 WebSocket 模式），经核心转换也能接 /v1/chat/completions。仅供账号所有者自用，请求如实标明来源为 sup2api，不伪装 Codex CLI。
- 核心配合 codex 的改动（CONTRACTS §83）：账号类型可声明 `streamOnly`，非流式请求在上游强制走流式再合成 JSON 响应；同平台 chat → Responses 转换，只支持 Responses 的账号类型也能接 /v1/chat/completions（旧式 functions、自定义工具、web_search_options 等会转换，没有对应项的参数直接丢弃，只拒绝 n>1 和音频）；WebSocket 每轮都调用插件构造请求并应用补丁（§83.6）。
- Responses WebSocket 会话不再有空闲超时（去掉首条消息 30 秒、两轮之间 300 秒的限制），客户端断开、节点让出或上游关闭时才结束；两侧每 30 秒 ping 保活，30 秒没有回应就关闭那一侧（`dca8628ef`）。
- 安全修复（`20419123f`）：用户通过 `PATCH /users/:id` 改自己的密码或邮箱、通过 `DELETE /users/:id/security` 重置自己的二次验证时，必须提供当前密码（开了二次验证还要验证码）；对已禁用用户的权限比较按其角色计算，重新启用也要检查，堵住拿 user:update 接管已禁用管理员的路子；流式响应内容已返回、用量事件前客户端断开时，按预扣的估算计费（`usage_extract=precharge`），不再记成免费。
- dynweight 0.5.1（内置包自动升级）：配置页仿 new-api 重做，规则和策略合成“调整项”编辑，分组配置和账号覆盖改成表格加弹窗，统一底部保存栏；账号页权重徽标显示升降箭头或“仅统计”，悬浮卡片显示状态和最近变化。`GET /weights` 返回 state、changed_at、adjust_off。
- 账号列表权重列的插件徽标不再换行到数字下面（`f7a944a37`）。

## 2026-11-08

### 网关 0.1.133 + 核心 0.1.256 + dynweight 0.5.0 + ccgateway 0.1.132（`e8ef5124d`，无迁移）

- 网关 0.1.133：核心升级改为逐个节点滚动（rolling-v1），升级中的节点先让出流量，另一节点继续服务，无迁移的版本不再停机；去掉准入看门狗（`07f9db0f2`）。
- 核心 0.1.256：默认请求体上限提高到 128 MB，由核心统一限制（ccgateway 0.1.132 的引擎同步放到 128 MiB）；计费在余额锁内批量结算，预扣失败更快返回（`87b79cc76`）；账号页插件扩展点（UI 3.6：账号操作、工具栏插槽、表单挂件保存）。
- dynweight 0.5.0：动态权重从账号编辑页移出，改用账号页插件扩展点展示和操作。

## 2026-11-07

### 核心 0.1.254（`428d457aa`，无迁移）

控制台适配手机（屏幕宽度小于 768px），宽屏布局不变（UI SDK 3.5.0，只有前端改动）：

- 顶栏更窄：语言和深色模式挪进头像菜单，避开刘海和底部手势区；侧边菜单切换页面后自动收起。
- 列表页（账号、API 密钥、用户、分组、代理、余额明细、使用记录等）改成卡片：标题、状态和关键字段在卡片上，其余字段点“详情”展开，行操作放在卡片底部。“列设置”变成底部弹出的“字段”面板。
- 筛选收成一行：保留搜索框，其余条件放进“筛选”按钮（带已选数量）打开的底部面板，里面有重置和完成。使用记录的导出 CSV 和列设置也放进这个面板。
- 新建按钮（新建账号、用户、代理等）变成右下角的悬浮圆形按钮，页面底部会留出空间，不遮挡分页和卡片按钮。分页改成“< 3 / 12 >”简洁样式，批量操作栏固定在底部。
- 弹窗、下拉菜单、可搜索的选择框和时间范围改成底部面板或整页；手机和触屏上按返回键或手势先关闭弹层，不离开页面。
- 按钮和输入框在触屏上加大，输入框用 16px 字号避免 iOS 自动放大；统计卡片每行两张，图表、标签页、步骤条、长 IP 和长文本都按手机宽度排版。

### 核心 0.1.253（`cd505b30d`，无迁移）

- 修复：由插件执行上游请求的账号（如 Anthropic API Key 账号）成功请求的使用记录里，粘性规则和是否命中一直为空；现在会正确记录。粘性统计（Redis）本来就正常，这次只修记录。

### 核心 0.1.252 + anthropic 0.2.9（`80cde9581`，迁移 0090）

- 粘性规则可以配置“透传请求头”（写法同账号请求头透传：`*`、`regex:正则` 或请求头名称）：命中规则的请求把这些客户端请求头转发到上游，不覆盖插件和账号已设置的头，账号透传的禁止列表照样生效。粘性规则弹窗新增该项。
- 内置 claude-code-session 规则默认透传 x-claude-code-session-id、agent-id、parent-agent-id、request-class、agent-type 五个头；内置 anthropic 平台 passHeaders 增加 request-class、agent-type。
- anthropic 插件 0.2.9（随核心内置包发布并自动升级）：默认转发上述五个 x-claude-code-* 头，未配置请求头透传的 Anthropic API Key 账号也会带上，下游网关可据此区分子代理会话。

## 2026-11-06

### 核心 0.1.251（`01009036d`，无迁移）

- 账号编辑页新增第 4 个分区“高级”，里面是“请求转发”组：请求头透传和各项上游超时（从“调度与限流”移来）。导航摘要如“透传 * · 超时跟随”，都没配时显示“全部跟随默认”（`d16ba3b03`、`01009036d`）。
- 账号列表状态列不再显示灰色的“不自动禁用”标签和说明（`97062578b`）。

### 核心 0.1.250（`0570a9451`，迁移 0089）

- 账号新增“请求头透传”（CONTRACTS §81）：规则可写 `*`、`regex:正则` 或请求头名称，最多 32 条，插件自己设置的头优先。凭证、逐跳头、客户端地址（X-Forwarded-For、X-Real-IP 等）和网关控制头（x-sub2api-*、x-kin-*）任何规则都不透传。账号编辑页“调度”分区可配置。
- 内置 anthropic 平台的 passHeaders 增加 x-claude-code-session-id、x-claude-code-agent-id、x-claude-code-parent-agent-id（`2fdd291a9`）；anthropic 插件仍是 0.2.8，插件里的同名改动未发布。

### 核心 0.1.249（`f0206570c`，无迁移）

- 只有“刷新”“新建”等按钮、没有筛选的页面，宽屏时按钮放进顶栏标题旁，不再单独占一行（仪表盘、分组、API 密钥、平台、发布者、角色、两个概览、节点、定时任务、核心升级）。手机上不变。
- 使用记录：筛选栏一行多放一项，“列设置”挪到“导出 CSV”旁边；概览的“重置/搜索”接在筛选行末尾。
- 插件页标题和菜单一致，不再重复显示标题和说明。

### 核心 0.1.248（`b15cd0f4b`，无迁移）

- 账号页工具栏并成一行：紧凑显示、自动刷新、刷新、新建账号和搜索、筛选放在同一行，不再单独占一行。自动刷新改成按钮加菜单；2xl 以下筛选框收窄，中文界面 1366px 起、英文 1440px 起都是一行。

### ccgateway 0.1.131（`a4396d717`）

- 修复：账号原地升级 Worker + CLI 之后，如果容器被删掉需要重建，会因为找不到旧版本 CLI 而建不起来。

### ovh 控制器宿主启用会话记忆 Valkey

- 新建的 CCGateway 账号把会话记忆存进宿主上的 Valkey：每个账号只能访问自己的数据，24 小时过期，过期后按历史重新计算；Worker 或容器重启不再丢记忆。现有账号不受影响。

## 2026-11-05

### ccgateway 0.1.130 + 核心 0.1.247（`45297934c`，无迁移）

- 新账号的 CLI 换成 Claude Code 2.1.318，请求带上和官方一致的工具清单头；修复三方 WebSearch 的辅助请求被当成主线程、工具循环后压缩导致请求链断开的问题。老账号保持原版本。
- 核心加会话记忆相关设置。
- ovh 插件包存储超出新上限，删除了 ccgateway 0.1.0～0.1.125 的旧版本（已备份）。

### 核心 0.1.246（`03f641100`，迁移 0088）

- 新增访问密钥：脚本和工具可以用 `sak_` 密钥以用户身份调用控制台 API，入口在个人中心。权限只能是用户自己权限的子集，用户失去权限时密钥立刻跟着失去；密钥不能改密码、二次验证、通行密钥和会话，也不能创建密钥，插件接口暂不接受（CONTRACTS §80）。
- 修复权限勾选框：真实点击会触发两次，等于没点。角色页用的是同一个权限树。

### 核心 0.1.245（`4364f3abf`，迁移 0087）

- 新增通行密钥（Passkey）登录和二次验证（验证器应用 + 备用码）。开了二次验证的账号，密码登录后还要输验证码（CONTRACTS §79）。
- 普通用户新增三个菜单：个人中心（资料、语言、密码、二次验证、通行密钥、登录会话）、钱包（余额、消费、流水，右上角余额链接到这里）、模型与价格（按分组显示可用模型和乘过倍率的价格，`GET /me/prices`）。
- 管理员可以在用户列表重置用户的二次验证和通行密钥。

### 核心 0.1.244（`43060bee5`）

- 使用记录页的标签页（用量明细、错误请求、调用 IP、用户排行、余额变动）改成筛选栏第一项“日志类型”下拉框，`?tab=` 链接照常可用（CONTRACTS §66.7）。
- 使用记录页第一行新增“请求类型”下拉框，选项来自当前搜索下出现过的协议；`GET /usage/summary`、`GET /me/usage/summary` 支持 `group_by=protocol`。刷新按钮移到导出旁边。

### 核心 0.1.243（`cb31afd22`）

- 修复权限提升：创建角色和给用户分配角色时，操作者必须持有被授予的全部权限（CONTRACTS §43.6）。

### 核心 0.1.242（`6fd65922c`）

- 控制台把使用记录拆成“概览”和“使用记录”两个菜单，管理员和普通用户都一样。概览是合计卡片和图表，使用记录是请求列表和各个标签页。
- 使用记录页显示六项统计：消耗、基础额度、缓存命中跟随时间范围，RPM、TPM、成功率只看近 1 分钟。新增 `GET /usage/realtime`、`GET /me/usage/realtime`（CONTRACTS §66.7）。
- 管理员的统计接口加进程内缓存：概览卡片 15 秒内直接返回，到 30 秒前返回旧值并在后台刷新；统计查询按查询串缓存 30 秒（CONTRACTS §66.3）。

### 网关 0.1.132（`d51ac8f0d`）

- Redis 里只保存节点密钥的哈希，peer 协议升到 2，两个节点要一起换。
- `REDIS_URL` 走 TCP 且没有密码时，网关拒绝启动。
- 私有转发入口收窄转发头。
- 主节点的插件包上传限制并发和总量。

## 2026-11-04

### 核心 0.1.241 + ccgateway 0.1.129（`ed8f932a1`）

- RPM、TPM 按精确的近 60 秒窗口计算，TPM 在准入时预占，响应结束后换成实际用量。
- 账号满并发时排队等待，进行中的请求不再因为并发槽位被切断。
- 同一会话的请求留在同一账号，按周负载降低账号的选中概率，并显示账号的限额等级（迁移 0086）。
- 重试规则可以关闭原账号重试；默认只在换账号有用时才重试 429。

### 核心 0.1.240 + ccgateway 0.1.128（`b41fad7fb`、`841ce6b16`）

- CCGateway 全链路支持 `/v1/messages/count_tokens`。计数不占并发、RPM、TPM 和额度，失败不影响账号，也和粘性无关（CONTRACTS §11.6）。
- Worker 和 CLI 没有全量升级，未升级账号的计数回 503 并换账号。

### ccgateway 0.1.127（`c8f63b05e`）

- 托管账号的上游 429 只有额度真的用满才自动禁用，其他情况原样返回客户端（CONTRACTS §69）。

### 核心 0.1.239（`f71ee5f77`）

- 修复多个插件同时建 schema 时，共享迁移函数授权的并发竞态。

### 核心 0.1.238（`88ad5db99`）

- 插件上传时检查任务上限，任务可以分多次续跑（CONTRACTS §77.8）。
- 修复运行时任务不论超时设多长都被 60 秒切断，以及插件“任务”页签刷新后表格变空。

### 核心 0.1.237 + 网关 0.1.131（`2701738f6`）

- 插件定时任务全部走任务框架，manifest `jobs[]` 增加 `scope` 和 `title`。
- Host API 从 8 升到 9，要先升网关再升核心。

### 核心 0.1.236（`7d6b6e51b`）

- 新的定时任务框架和控制台“系统 → 定时任务”页面，原有的 20 个循环任务迁入（CONTRACTS §77，迁移 0076–0085）。0.1.235 没有单独发布，内容合在这个版本里。

### ccgateway 0.1.126 + guard 0.1.126（`4c5a519d6`）

- 新 Worker 的模型请求不再占槽位，并发只由核心的账号并发限制决定。
- guard 创建账号容器时不限制 swap。
- 只对新账号生效，现有账号容器没有变化。
