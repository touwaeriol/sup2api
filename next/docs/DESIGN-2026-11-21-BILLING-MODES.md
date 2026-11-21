# 设计：计费方式只留按次、按 token、表达式（2026-11-21）

所有者 2026-11-21 定：“统一改成按次、按 token 和表达式，其他的方式全部删掉”；“按 token 计费其实就是把 new-api 的表达式计费参考抄过来；表达式计费相当于去掉了 UI，直接写我们支持的任意表达式”。设计稿（真实页面原型截图）在项目文件夹 `price-modes/`，原型分支 `proto/price-modes`（不合入）。new-api 是 AGPL，只参照交互，不拷代码。

## 1. 三种方式

| mode | 是什么 | config | expression |
|---|---|---|---|
| `per_request` | 每次固定价 | `{price}`（不变） | 生成 |
| `per_token` | 带界面的分档 + 加价规则（取代原“表达式 → 可视化”和原按 token 的五个单价） | 见第 2 节 | 生成 |
| `expression` | 只写源码 | 恒为 `{}` | 手写 |

删掉：视频计费模板（`expr.VideoConfig`、`generateVideo`、`VideoPriceEditor`、`videoPrice.ts`、试算里的视频示例按钮）、表达式模式的可视化页与视频页（`config.tiers/rules/video` 在 `expression` 下不再接受）。`VideoOnly` 仍由表达式里的 `u("video_*")` 判定（`pricer.go`），只删按 config 判定的那半。

## 2. `per_token` 的 config

```json
{
  "tiers": [
    {"name": "standard", "max_len": 200000, "unit": "token", "p": 3, "c": 15, "cr": 0.3, "cc": 3.75, "cc1h": 6},
    {"name": "long_context", "max_len": null, "unit": "token", "p": 6, "c": 22.5, "img": 10, "img_o": 40},
    {"name": "bulk", "max_len": null, "unit": "request", "price": 0.05}
  ],
  "rules": [
    {"name": "priority", "multiplier": 2, "conds": [
      {"source": "param",  "key": "service_tier",   "op": "eq",       "value": "priority"},
      {"source": "usage",  "key": "service_tier",   "op": "eq",       "value": "priority"},
      {"source": "header", "key": "anthropic-beta", "op": "contains", "value": "fast-mode"},
      {"source": "time",   "tz": "Asia/Shanghai", "from_hour": 0, "to_hour": 8}
    ]}
  ]
}
```

- `tiers` 至少一档；按 `max_len` 从小到大匹配 `len`，恰有一档 `max_len: null`（其余）；名字唯一、符合 `tierNameRe`；`max_len` 不重复。一档时那一档就是 `max_len: null`。
- `unit`：`token`（缺省）或 `request`。`token`：`p`、`c` 必有（缺省 0），`cr`、`cc`、`cc1h`、`img`、`img_o` 可缺，缺的不出现在表达式里（缓存 token 不收费；图片 token 留在 `p`、`c` 里按文字价算，§118.3）。`request`：只有 `price`，生成 `flat(price)`。界面的“缓存写一种价 / 分 5 分钟与 1 小时”由 `cc1h` 是否存在决定，“图片单独计价”由 `img`/`img_o` 是否存在决定，不另存字段。
- `rules` 可空。一条规则：`name`（可空，只给人看）、`multiplier`（非负）、`conds` 至少一个，条件之间是“且”，生成 `c1 && c2 ? m : 1`。规则用乘法写在表达式里：`(基础) * (规则1) * (规则2)`；`|||` 分隔符删掉（2026-11-21 追加决定，CONTRACTS §131.3）。读了请求信息、命中分支是数字字面量、否则为 1 的三元式都按规则追踪命中。
- 条件 `source`：`param`（`param(key)`）、`header`（`header(key)`，名字转小写并校验）、`usage`（`u(key)`，插件报的用量字段）、`time`（`tz`、`from_hour`、`to_hour`，跨午夜照旧）。`op`：`eq`、`ne`（值为字符串 / 数字 / 布尔字面量）、`contains`（非空字符串，`has(...)`）、`exists`（无值；`header` 为 `!= ""`，`param` 为 `!= nil`，`usage` 为 `!= 0`：没报的用量字段读出来是 0）、`gt`、`gte`、`lt`、`lte`（值为数字）。`time` 不带 `op`。
- **没有规则时生成结果与旧生成器逐字节相同**（旧 per_token 五个单价、旧可视化 tiers 迁移过来后表达式和 `expr_hash` 不变）。带规则的旧表达式由迁移改写成乘积写法、换 `expr_hash`（第 4 节）。

## 3. 分组 / 用户组的自定义价（§97）

`price_rules` 里 `kind = custom, mode = per_token` 的 `config` 同样是第 2 节的形状。自定义价只能用于全局价是按次、或**只有一档、没有规则、按 token 计**的按 token 价；分档或带规则的按 token 价与表达式价一样只能“按倍数”。控制台的自定义价编辑器仍是一行单价，存成单档。

## 4. 迁移（核心迁移 0121，单向；1–3 表达式与 hash 不变）

`model_prices`：
1. `per_token`（旧五个单价）→ `{"tiers":[{"name":"base","max_len":null,"unit":"token", …原键}],"rules":[]}`。
2. `expression` 且 `config.tiers` 非空、每档无 `flat`（或为 0）、规则都能映射（header 的 contains/eq、param 的 contains/eq、time）→ `mode = per_token`，档位展平 `prices` 嵌套、补 `unit`，规则变成单条件的 `conds`。不能映射的（带 flat、param `range`）→ 留在 `expression`，`config = {}`。
3. 其余 `expression`（含视频模板）→ `config = {}`。
`price_rules`：`custom` + `per_token` 的旧五个单价同 1。
1–3 迁移前后每行的 `expression`、`expr_hash` 不变；测试对照迁移前后的生成结果。
4. 含 `|||` 的表达式（`model_prices`、`price_rules`、价格历史、未结算使用记录里携带的表达式）改写为 `(A) * (B) * (C)`、换哈希；历史表为新哈希加一行，旧哈希的行保留哈希、换成新文本（CONTRACTS §131.6）。

## 5. 价格同步

LiteLLM / models.dev 同步产出的价格一律是 `per_token`：一档为单档，带 200K 档的为两档（原来是 `expression` + tiers）。同步预览的“有无变化”按新形状比较。

## 6. 控制台

- 价格编辑页：三张卡片选方式；按 token 为新编辑器（档位卡片、每档按 token / 按次、缓存写一种价或分 5 分钟 / 1 小时、图片单独计价开关、从模板开始、多条件加价规则，条件来源含“用量字段”）；表达式为源码框 + 变量函数说明 + 示例；按次一个单价。“查看生成的表达式”收起，可“改用表达式编辑”（把生成的源码带进表达式模式）。校验、试算移到右栏（宽屏固定）。
- 价格列表、价格摘要、分组价格表等读 config 的地方按新形状显示（按 token 显示第一档单价，多档注明档数）。
- mock 同步改。

## 7. 不在本次

OpenAI / Codex 应答里的 `service_tier` 报成用量字段（待所有者点头）；按次带加价规则（所有者未要求）。
