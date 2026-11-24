# 插件商店：迁移 0125 的发布与回退

带迁移 `0125_market_sources_drop_public_key.sql`（CONTRACTS §136、§137.5、§137.6）的核心版本用这份手册。版本号发布时才定，定下后把本文件改名为 `<版本>-market-0125-release.md`，并在 `CHANGELOG.md` 的发布小节里引用。所有环境都升级完、不再可能回退后删除本文件。

## 这次升级改变了什么

- 0125 先检查、再改库，都在一个事务里：
  - **检查**（不改数据）：装着清单发布者不是 `sub2api` 的插件，或留着这样的发布者退役的标识（`plugin_key_retirements` 里发布者不是 `sub2api` 的行）时，迁移在改任何东西之前失败并列出它们。生产只有第一方插件，不受影响；有第三方插件的库要先在旧版本卸载它们并释放标识，升级后重装（见第一节第 1 项）。没有发布者的退役行（未签名的插件）照旧保留。
  - **改库**（单向）：删除 `market_sources.public_key` 与 `url`，给 `plugin_versions` 加 `trust_level`；新建 `market_stores`（只有官方商店），`market_sources` 变成官方商店的频道（加 `store_id`、`branch`、`is_default`）。第一次运行时加“测试”/`develop`，把名为 `official` 的源行改成默认频道“正式”/`main`（保留 id），**删掉其他源行**；没有默认频道时加“正式”/`main`。重跑不改数据，管理员删掉的频道不会回来。旧核心读 `market_sources` 时要选 `public_key`，所以**迁移之后旧核心不能再跑在这个库上**。
- 新核心读官方商店 `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin` 下各频道的 `index-<分支>/index.json`，频道地址由商店 base 与分支在读的时候推出，不存库。包只从 GitHub Release 的附件下载（`https://github.com/<owner>/<repo>/releases/download/<tag>/<文件>.s2plugin`），本地市场除外。它不读 `SUB2API_MARKET_SOURCES`；`SUB2API_MARKET_BASE_URL`、`SUB2API_MARKET_ALLOW_HTTP` 只给 e2e、`deploy/single` 这类本地市场用，生产不设。
- 旧核心照旧读 `SUB2API_MARKET_SOURCES`（缺了也能启动，只是不更新源）。所以**生产发布前不用改 `.env`**。

## 一、发布前

1. **预检**（只读，在主节点的库上跑；两条都必须返回 0 行，否则 0125 会让升级失败）：

   ```sql
   -- 发布者不是 sub2api 的插件版本
   SELECT DISTINCT plugin_key, coalesce(manifest->>'publisher', 'none') AS publisher
     FROM plugin_versions WHERE manifest->>'publisher' IS DISTINCT FROM 'sub2api';
   -- 发布者不是 sub2api 的退役标识
   SELECT r.plugin_key, p.name AS publisher
     FROM plugin_key_retirements r JOIN publishers p ON p.id = r.publisher_id
    WHERE p.name <> 'sub2api';
   ```

   有行时在**旧版本**上卸载这些插件，并在“已安装插件”页底部的退役标识里释放它们（释放会清掉它们留下的表与数据），再重跑预检。升级后从商店或手动上传重装。
2. **市场源**（只读）：`SELECT id, name, url, enabled FROM market_sources ORDER BY id;`。0125 只保留名为 `official` 的行（改成“正式”/`main`，id 不变），**其他行一律删除**。已发布的核心按 `SUB2API_MARKET_SOURCES` 种源，名字取自那里的配置，不一定叫 `official`：如果指向官方索引的那一行叫别的名字，又想保留它的 id（审计 `plugin.upload` 的 `source` 记的 `market:<id>` 是它），先在旧版本上 `UPDATE market_sources SET name = 'official' WHERE id = <那一行>`（名字唯一，先确认没有别的 `official` 行）；不在乎 id 的话不用动，0125 会新建“正式”/`main`。那一行指向的不是官方索引（比如以前单机栈的本地市场 `local-dev`）时也不要改名：改名会把来自本地市场的旧上传记录挂到正式频道下，接受删除即可（ovh 生产 11-25 演练时就是这种情况）。
3. **网关**：生产用默认的官方地址，不依赖新网关。用本地 http 市场的栈（设了 `SUB2API_MARKET_BASE_URL`、`SUB2API_MARKET_ALLOW_HTTP`）要先换上把这两个变量列进透传白名单的网关，再发核心，否则核心收不到它们，会去读官方商店。
4. **备份**：`pg_dump -Fc` 整库（或确认托管数据库有升级前的快照），记下时间。回退只靠它。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- `market_sources` 没有 `public_key`、`url` 列，是官方商店的两个频道“正式”（`main`，默认；原来有 `official` 行时保留它的 id）与“测试”（`develop`）；`GET /market/sources` 返回的地址是 `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-<分支>/index.json`；`market_stores` 只有官方商店；`plugin_versions` 有 `trust_level`。
- 控制台“插件市场”页选官方商店的“正式”频道能列出插件，发布者、信任级别正确；系统设置的“商店配置”页只有官方商店一张卡片和它的两个频道。
- 之后重建网关容器时，先把节点上的 compose 换成本仓库的版本（ovh、clicodeplus 的 compose 已不再引用 `MARKET_SOURCES`；节点上旧 compose 是必填写法 `${MARKET_SOURCES:?}`，先删变量会让 compose 起不来），再从 `.env` 删掉 `MARKET_SOURCES`。

## 三、回退

迁移之后**只能恢复备份**：停掉所有节点，把数据库恢复到第一节第 4 项的备份，换回旧核心版本，再启动。备份之后写入的数据（包括升级后从商店装的插件版本）会丢失。

删掉的 `public_key`、`url`、其他市场源与新加的商店表不用另外处理：新核心不再用它们，恢复备份时它们随旧库一起回来；旧核心不设 `SUB2API_MARKET_SOURCES` 也能启动，用库里已有的源。
