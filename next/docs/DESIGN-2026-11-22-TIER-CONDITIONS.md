# 计费方式只留可视化与表达式，阶梯条件照 new-api（2026-11-22）

所有者 2026-11-22 定“完全照 new-api”：按 token 价格的档位不再只按 `len <= N` 分，而是每档一个条件树，按档位顺序第一个满足条件的档位决定价格，最后一档兜底。参考 touwaeriol/new-api codingplus 分支的 `web/classic/src/pages/Setting/Ratio/components/VisualConditionTree.jsx`、`TieredPricingEditor.jsx`、`web/classic/src/helpers/tier-expr.js`、`web/shared/src/pricing/billing-expr.ts`（AGPL：只参考行为与布局，不抄代码）。契约改 CONTRACTS §131.2，迁移 0122，核心 0.1.307。

## 0. 计费方式只留“可视化”和“表达式”（所有者 2026-11-22 追加）

> “模型计费仅仅保留：可视化/表达式。可视化会被转换为表达式去参与计费，可视化切换到表达式时会转换为表达式填充，表达式切换到可视化，如果不是可视化转换到表达式的转换，不能识别就不识别。删除按次，按 token 改成可视化。表达式使用说明：说明后端用的表达式库。”

- `mode` 只剩 `visual`、`expression`。`per_token` 改名 `visual`（配置就是下面第 2 节的形状）；`per_request` 删除：原按次价 `{price, rules?}` 迁移成可视化的单档 `{"tiers":[{"name":"base","unit":"request","price":P}],"rules":<原规则>}`，生成的表达式与原按次逐字节相同（`tier("base", flat(P))` 加同样的规则），`expr_hash` 不变。`price_rules` 的 `mode` CHECK 约束（0095 里是 `per_token`、`per_request`）一并改成只允许 `visual`。
- 计费只认表达式：可视化保存时由生成器把配置转成表达式，存 `expression` + `expr_hash`，计费按表达式算（与现在相同）。
- 界面只有两个方式标签：可视化、表达式。
  - 可视化 → 表达式：用当前配置生成的表达式填进表达式编辑器。
  - 表达式 → 可视化：把表达式解析回配置，再用生成器重新生成，与原文（忽略首尾空白）逐字节相同才算识别成功，填进可视化；否则提示“这条表达式不是可视化生成的，无法转成可视化”，留在表达式模式。只在前端做（TS 解析器 + 生成器回环校验）。
- 自定义价（分组 / 用户组）：只能设在“可视化、只有一档（按 token 或按次）、没有规则”的全局价上，自定义价本身也是这种形状。
- 表达式使用说明（抽屉）开头写明：后端用 Go 的表达式库 expr-lang/expr（v1.17.8，https://expr-lang.org ），语法、运算符与内置函数以它为准；本平台在它之上加了 `tier()`、`flat()`、`param()`、`header()`、`u()`、时间函数和变量 `p`、`c`…，并禁用了 `__rule`、`$env`。
- 价格同步、导出 `/key/prices`、e2e 夹具、mock、预设里的 `per_token`、`per_request` 全部改为 `visual`；旧上游（还有 `per_token`/`per_request`/五个单价）导入时转换。
- 迁移 0122 同时做：`per_token` → `visual`（并把 `max_len` 换成 `when`），`per_request` → `visual` 单个按次档，`price_rules` 同样处理并改 CHECK。

## 1. new-api 的行为（对照依据）

- 档位条件可比较的量：`len`（输入长度）、`p`（输入 token）、`c`（输出 token）；时间函数 `hour`、`minute`、`weekday`、`month`、`day`，各带时区（缺省 UTC）。
- 比较符：`<`、`<=`、`>`、`>=`、`==`、`!=`；值是数字（星期用下拉选 0–6）。
- 条件可以用“且 / 或 / 非”嵌套成树。
- 档位按顺序判断，第一个满足的生效，最后一档兜底（没有条件）。

## 2. 配置形状（`visual` 的 `config.tiers[]`）

`max_len` 删掉，换成 `when`：

```json
{
  "tiers": [
    {"name": "long_night", "when": {"and": [
        {"var": "len", "op": ">", "value": 200000},
        {"or": [{"var": "hour", "tz": "Asia/Shanghai", "op": ">=", "value": 22},
                {"var": "hour", "tz": "Asia/Shanghai", "op": "<", "value": 6}]}]},
     "unit": "token", "p": 4, "c": 18},
    {"name": "long", "when": {"var": "len", "op": ">", "value": 200000}, "unit": "token", "p": 6, "c": 22.5},
    {"name": "base", "unit": "token", "p": 3, "c": 15}
  ],
  "rules": []
}
```

- 条件节点恰好是下面一种，键多了少了都 400：
  - 叶子 `{"var", "op", "value", "tz"?}`：`var` ∈ `len`、`p`、`c`、`hour`、`minute`、`weekday`、`month`、`day`；`op` ∈ `<`、`<=`、`>`、`>=`、`==`、`!=`；`value` 是有限数字（可为数字字符串，`expr.Num`）；`tz` 只有时间量可带，缺省或空为 `UTC`，必须是能加载的 IANA 时区。时间量的值是整数：hour 0–23、minute 0–59、weekday 0–6（0 是星期日）、month 1–12、day 1–31。
  - `{"and": [节点…]}`、`{"or": [节点…]}`：至少 1 个子节点。
  - `{"not": 节点}`。
- 上限：一档的条件树深度不超过 5，叶子不超过 20；档位不超过 20。
- 档位：至少一档；名字规则与不重复同现在；**除最后一档外每档都必须有 `when`，最后一档不能有**（兜底）；只有一档时它没有 `when`。顺序就是配置里的顺序，不再排序。

## 3. 生成的表达式（Go `expr.Generate` 与 TS 生成器逐字节相同）

- 叶子：`len <= 200000`、`p > 100`、`hour("Asia/Shanghai") >= 22`（数字用 `fmtNum`；时区原样加引号，`strconv.Quote` / `JSON.stringify` 同口径；空时区写 `"UTC"`）。
- `and`、`or`：子节点用 ` && `、` || ` 连接；只有一个子节点时就是那个子节点本身；子节点剥掉层层单子节点组之后，是有 ≥2 个子节点的 `and`/`or` 时加括号（例如 `and[or[or[a,b]], c]` 生成 `(a || b) && c`）。
- `not`：`!(内层)`。
- 档位链：一档时 `tier("n", 价格)`；多档时从右往左拼，`c1 ? T1 : c2 ? T2 : T3` 的嵌套写法沿用现在的生成器：最后两档 `cN-1 ? TN-1 : TN`，再往左每层把右边整体加括号，`c1 ? T1 : (c2 ? T2 : T3)`。所以现有 `len <= N` 的配置迁移后生成的表达式与现在**逐字节相同**，`expr_hash` 不变。
- 规则照旧由 `withRules` 乘在外面。

## 4. 迁移 0122（单向，只改配置）

- `model_prices` 的 `per_token` 行、`price_rules` 的 `custom` + `per_token` 行：档位按 `max_len` 从小到大排（`null` 最后，与现在生成器的排序相同），`max_len` 不为 null 的档加 `"when": {"var": "len", "op": "<=", "value": N}`，然后去掉每档的 `max_len` 键。
- 不改 `expression`、`expr_hash`；测试断言迁移后每行 `Generate(config)` 的哈希等于所存 `expr_hash`。
- `SET LOCAL lock_timeout = '10s'`；临时函数末尾删掉；重跑什么也不写。

## 5. 其他要跟着改的

- `customMode`（自定义价准入）：单档 = 一档、没有 `when`、没有规则。
- 价格同步（LiteLLM、models.dev 的 200K 两档、旧上游的五个单价和可视化配置）产出新形状：两档写成 `[{name, when: len <= 200000, …}, {name, …}]`。
- `check-prices`、导出、列表摘要、估算器与试算显示命中的档位不变（`tier()` 名字）。
- 前端档位卡：
  - 去掉“上限”输入，换成条件树编辑器（照 new-api 的 VisualConditionTree 布局：一行一个条件，量下拉 len/p/c/小时/分钟/星期/月/日，时间量带时区下拉，比较符下拉，值输入；星期用下拉；“且 / 或”组可以加条件、加子组、取反；最后一档显示“兜底：前面的档位都没命中时”）。
  - 档位可以上移、下移；新增档插在兜底档之前。
  - 卡头摘要用中文写出条件（如“len > 200,000 且 小时(Asia/Shanghai) ≥ 22”）。
  - 预设模板（Claude 200K 两档等）改成新形状。
- e2e 价格夹具、mock 数据、`generate_golden.json`（加嵌套、取反、时间量、单子节点组、三档以上的用例）。
- CONTRACTS §131.2 改写，CHANGELOG “未发布”加一节。

## 6. 任务拆分

| 任务 | 范围 | 负责 |
|---|---|---|
| T11 后端 | `billing/expr/generate.go`（类型、校验、渲染）、`generate_test.go`、golden 用例、`billing` 的 customMode/同步/导出、迁移 0122 + 数据库测试、e2e 夹具、CONTRACTS §131.2 | 子代理 A |
| T12 前端 | TS 生成器与校验（`priceExpr.ts`、`tokenPrice.ts`）、档位卡条件树编辑器、上移下移、摘要文案、i18n、预设、mock、spec；最后跑 golden | 子代理 B |
| T13 审查 | 两路审查（后端+迁移、前端），按编码规范：抽象、复用、解耦、低嵌套、低侵入 | 子代理 C、D |
| T14 | 修复、全量测试、合入 develop、构建 0.1.307、只读 check-prices、等用户原话发布 | 主线程 |
