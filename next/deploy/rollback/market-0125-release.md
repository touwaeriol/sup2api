# 插件商店：迁移 0125 的发布与回退

带迁移 `0125_market_sources_drop_public_key.sql`（CONTRACTS §136）的核心版本用这份手册。版本号发布时才定，定下后把本文件改名为 `<版本>-market-0125-release.md`，并在 `CHANGELOG.md` 的发布小节里引用。所有环境都升级完、不再可能回退后删除本文件。

## 这次升级改变了什么

- 0125 是单向迁移：删除 `market_sources.public_key`，给 `plugin_versions` 加 `trust_level`、`source` 两列。旧核心读 `market_sources` 时要选 `public_key`，所以**迁移之后旧核心不能再跑在这个库上**。
- 市场源只接受 https；`SUB2API_MARKET_ALLOW_HTTP=true` 才接受 http（只给 e2e、`deploy/single` 这类本地市场用，生产不设）。`SUB2API_MARKET_SOURCES` 里有不合规的地址时**新核心启动失败**。
- 旧核心要求每个市场源带合法的 `public_key`，缺了同样启动失败；新核心不读 `public_key`。所以过渡期的 `MARKET_SOURCES` 写 https 地址并保留旧的 `public_key`，新旧核心都能启动。

## 一、发布前

1. **网关**：市场源是 https 时不依赖新网关。用 http 市场的栈（开了 `SUB2API_MARKET_ALLOW_HTTP`）要先换上把这个变量列进透传白名单的网关，再发核心，否则核心收不到它、读 http 源启动失败。
2. **市场源**：每个节点的 `.env` 把 `MARKET_SOURCES` 改成官方索引，**名字保持原来的**（核心按名字更新已有的源，源的 id 不变，从它装的插件以后仍从它升级；换名字会留下一条读不了的 http 源），先保留旧的 `public_key`：

   ```sh
   MARKET_SOURCES='[{"name":"<原名字>","url":"https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json","public_key":"<原公钥>"}]'
   ```

   改完按网关手册逐个节点让出、重建网关容器，让核心拿到新环境。旧核心这时启动正常，但市场页读不了新索引（它只认签名的 v1），这段时间不要从市场安装。ovh 还要把 compose 里的 `SUB2API_MARKET_ALLOW_HTTP` 去掉（本仓库的 `gateway/ovh/compose.yml` 已去掉）。
3. **备份**：`pg_dump -Fc` 整库（或确认托管数据库有升级前的快照），记下时间。回退只靠它。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- `SELECT count(*) FROM information_schema.columns WHERE table_name = 'market_sources' AND column_name = 'public_key'` 为 0；`plugin_versions` 有 `trust_level`、`source`。
- 控制台“插件市场”能列出官方索引的插件，发布者、信任级别正确。
- 下次重建网关容器时可以把 `MARKET_SOURCES` 里的 `public_key` 去掉（新核心不读）。

## 三、回退

迁移之后**只能恢复备份**：停掉所有节点，把数据库恢复到第一步第 3 项的备份，换回旧核心版本和旧的 `.env`，再启动。备份之后写入的数据（包括升级后从市场装的插件版本）会丢失。

删掉的 `public_key` 不用恢复：新核心不再用它，恢复备份时它随旧库一起回来。
