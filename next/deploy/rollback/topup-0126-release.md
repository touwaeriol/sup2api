# 核心充值：迁移 0126 的发布与回退

带迁移 `0126_topup.sql`（CONTRACTS §140）的核心版本用这份手册。在线充值、优惠码、兑换码从 payment 插件搬进核心，插件 `sub2api/payment` 随之下架。确认不再需要回退后删除本文件。

## 这次升级改变了什么

- 0126 只**新建**表：`payment_methods`、`payment_orders`、`payment_events`、`promo_codes`、`promo_code_usages`、`redeem_codes`，不改已有表。旧核心不读这些表，所以迁移之后旧核心仍能跑在这个库上（回退见第三节）。
- 库里有 payment 插件的表时，0126 一次性复制它的优惠码（`plg_payment__promo_codes`，使用记录指向插件的订单，不复制，只保留已用次数）和未使用的兑换码（`plg_payment__redeem_codes`，只复制哈希，插件和核心用同一种哈希）。核心的数据库身份读不了插件表时，迁移只打一条 WARNING 跳过复制，所以要做第一节第 3 项。
- 新核心的内置角色默认多了权限，启动时的权限同步自动授予：普通用户、运维、供应商代理（`supplier_agent`）角色得到 `topup:use`（在线充值）和 `redeem:use`（兑换码）（供应商角色是叠加在普通用户角色上的，靠普通用户角色得到）。管理类权限（`topup:manage`、`topup:order:read`、`redeem:manage`）不进任何内置角色的默认权限。
- 充值默认关闭：管理员在“系统设置 → 充值/支付 → 充值配置”里配好支付方式与易支付 / Stripe 网关再打开。站点地址 `SUB2API_PUBLIC_URL` 必须是 https 才能开通充值（回调与跳回地址由它拼出）。

## 一、发布前

1. **关 newapi 接 sup2api 的渠道**（项目规则：带迁移的核心发布先关，确认无流量再发，验证后恢复）。
2. **先停用 payment 插件**（插件页“停用”，不要卸载）。停用前插件和核心会同时认同一批兑换码：迁移复制之后、插件下架之前，同一个码在插件和核心各能兑一次。停用后插件不再处理兑换和充值。
   - 插件配了真实支付网关、有用户在付款时，先在插件设置里关掉充值，等插件订单全部过期（插件的订单超时时间）再停用，否则停用后付款的订单收不到回调、不会入账。
3. **payment 插件的数据**（只读，在主节点的库上跑；插件没装时表不存在，跳过）：

   ```sql
   SELECT to_regclass('plg_payment__promo_codes') AS promo, to_regclass('plg_payment__redeem_codes') AS redeem;
   SELECT count(*) FROM plg_payment__promo_codes WHERE length(code) <= 64;
   SELECT count(*) FROM plg_payment__promo_codes WHERE length(code) > 64;
   SELECT count(*) FROM plg_payment__redeem_codes WHERE status = 'unused' AND type = 'balance';
   ```

   记下数字，发布后与核心表对照（第二节）。第三条不为 0 时，这些码超过核心优惠码的 64 个字符，0126 **不搬**它们；需要的话发布后由管理员在“优惠码”页用不超过 64 个字符的新码重建。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- 六张新表都在；第一节第 3 项的数字（优惠码取不超过 64 个字符的那条）与 `SELECT count(*) FROM promo_codes;`、`SELECT count(*) FROM redeem_codes WHERE status = 'unused';` 一致。迁移日志里有 `payment plugin tables not readable` 的 WARNING 时，按第四节手动复制。
- 控制台侧栏有“充值/支付”分组（充值订单、兑换码、优惠码），系统设置里有“充值配置”；普通用户的钱包页有充值卡（未开通时显示“暂未开放在线充值”）和兑换码卡。
- 恢复 newapi 接 sup2api 的渠道。
- 确认不再回退后，在插件页卸载 `payment` 插件（卸载会删它的表，回退就只能靠数据库备份找回插件数据），并在 touwaeriol/sup2api-plugin 下架 `sub2api/payment`（CONTRACTS §136 的弃用）。

## 三、回退

0126 不改已有表，换回旧核心版本即可，新表留在库里，旧核心不读。要处理的是新核心期间发生、旧核心不知道的事：

1. **回退前先关充值并查在途订单**：在“充值配置”里关掉充值，再查新核心还可能入账的订单：

   ```sql
   SELECT id, out_trade_no, user_id, amount, gateway, status, updated_at
     FROM payment_orders
    WHERE status IN ('pending', 'paid')
       OR (status IN ('expired', 'cancelled') AND updated_at > now() - interval '24 hours');
   ```

   有结果时最好等它们付完入账或过期满 24 小时再回退。回退期间这些订单的支付回调打到旧核心会 404，网关会重试一段时间；回到新核心时还在 24 小时迟到窗口内的会自动入账，超出的不会，需要对照网关后台手动给用户加余额。
2. **兑换码对账**（插件表还在时）：新核心期间核心兑掉或作废的码，旧版插件仍认为未使用。回退后、重新启用插件前，在主节点的库上执行：

   ```sql
   UPDATE plg_payment__redeem_codes p
      SET status = CASE WHEN r.status = 'used' THEN 'used' ELSE 'expired' END,
          used_by = r.used_by, used_at = r.used_at, updated_at = now()
     FROM redeem_codes r
    WHERE r.code_hash = p.code_hash AND r.status IN ('used', 'voided') AND p.status = 'unused';
   ```

   新核心里新生成的兑换码插件不认，回退期间无法使用。
3. 已经卸载了 payment 插件又要回退时，旧核心不会自动重装它；需要旧版充值的话重新安装插件（它的数据随卸载删除，只能从备份恢复）。

**再次升级**时 0126 已经执行过，不会再跑：

- 先按第一节第 2 项停用插件。
- 把回退期间插件兑掉的码在核心作废：

  ```sql
  UPDATE redeem_codes r SET status = 'voided', updated_at = now()
    FROM plg_payment__redeem_codes p
   WHERE p.code_hash = r.code_hash AND p.status <> 'unused' AND r.status = 'unused';
  ```

- 回退期间插件里新建的优惠码与兑换码，按第四节执行一次复制（`ON CONFLICT DO NOTHING`，已有的跳过）。

## 四、手动复制（迁移跳过复制时，或再次升级时）

以能读插件表的身份（例如数据库超级用户）在主节点的库上执行 0126 文件末尾 `DO $$ … $$` 块里的两条 `INSERT … SELECT`，再做第二节的对照。
