# 核心充值：迁移 0126 的发布与回退

带迁移 `0126_topup.sql`（CONTRACTS §140）的核心版本用这份手册。在线充值、优惠码、兑换码从 payment 插件搬进核心，插件 `sub2api/payment` 随之下架。确认不再需要回退后删除本文件。

## 这次升级改变了什么

- 0126 只**新建**表：`payment_methods`、`payment_orders`、`payment_events`、`promo_codes`、`promo_code_usages`、`redeem_codes`，不改已有表。旧核心不读这些表，所以迁移之后旧核心仍能跑在这个库上（回退见第三节）。
- 库里有 payment 插件的表时，0126 一次性复制它的优惠码（`plg_payment__promo_codes`，使用记录指向插件的订单，不复制，只保留已用次数）和未使用的兑换码（`plg_payment__redeem_codes`，只复制哈希，插件和核心用同一种哈希）。核心的数据库身份读不了插件表时，迁移只打一条 WARNING 跳过复制，所以要做第一节第 2 项。
- 新核心的普通用户角色默认多了 `topup:use`（在线充值）和 `redeem:use`（兑换码），启动时的权限同步自动授予。
- 充值默认关闭：管理员在“系统设置 → 充值/支付 → 充值配置”里配好支付方式与易支付 / Stripe 网关再打开。站点地址 `SUB2API_PUBLIC_URL` 必须是 https 才能开通充值（回调与跳回地址由它拼出）。

## 一、发布前

1. **关 newapi 接 sup2api 的渠道**（项目规则：带迁移的核心发布先关，确认无流量再发，验证后恢复）。
2. **payment 插件的数据**（只读，在主节点的库上跑；插件没装时表不存在，跳过）：

   ```sql
   SELECT to_regclass('plg_payment__promo_codes') AS promo, to_regclass('plg_payment__redeem_codes') AS redeem;
   SELECT count(*) FROM plg_payment__promo_codes;
   SELECT count(*) FROM plg_payment__redeem_codes WHERE status = 'unused' AND type = 'balance';
   ```

   记下数字，发布后与核心表对照（第二节）。
3. **备份**：`pg_dump -Fc` 整库（或确认托管数据库有升级前的快照），记下时间。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- 六张新表都在；第一节第 2 项的数字与 `SELECT count(*) FROM promo_codes;`、`SELECT count(*) FROM redeem_codes WHERE status = 'unused';` 一致。迁移日志里有 `payment plugin tables not readable` 的 WARNING 时，按第四节手动复制。
- 控制台侧栏有“充值/支付”分组（充值订单、兑换码、优惠码），系统设置里有“充值配置”；普通用户的钱包页有充值卡（未开通时显示“暂未开放在线充值”）和兑换码卡。
- 恢复 newapi 接 sup2api 的渠道。
- 在插件页卸载 `payment` 插件（卸载会删它的表），并在 touwaeriol/sup2api-plugin 下架 `sub2api/payment`（CONTRACTS §136 的弃用）。

## 三、回退

0126 不改已有表，回退不需要恢复备份：换回旧核心版本即可。新表留在库里，旧核心不读；再次升级时 0126 跳过已有的表（`IF NOT EXISTS`），复制用 `ON CONFLICT DO NOTHING`，不会重复。回退期间产生的充值订单只在新表里，旧核心看不到，升级回来后照常。

已经卸载了 payment 插件又要回退时，旧核心不会自动重装它；需要旧版充值的话重新安装插件（它的数据随卸载删除，只能从备份恢复）。

## 四、手动复制（只在迁移跳过复制时）

以能读插件表的身份（例如数据库超级用户）在主节点的库上执行 0126 文件末尾 `DO $$ … $$` 块里的两条 `INSERT … SELECT`，再做第二节的对照。
