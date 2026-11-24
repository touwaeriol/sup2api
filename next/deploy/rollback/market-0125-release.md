# 插件商店：迁移 0125 的发布与回退

带迁移 `0125_market_sources_drop_public_key.sql`（CONTRACTS §136、§137.6）与 `0126_plugin_id_guard.sql`（§137.5）的核心版本用这份手册。版本号发布时才定，定下后把本文件改名为 `<版本>-market-0125-release.md`，并在 `CHANGELOG.md` 的发布小节里引用。所有环境都升级完、不再可能回退后删除本文件。

## 这次升级改变了什么

- 0125 是单向迁移：删除 `market_sources.public_key`，给 `plugin_versions` 加 `trust_level`；新建 `market_stores`（种下官方商店），`market_sources` 变成商店的频道（加 `store_id`、`branch`、`is_default`），旧的 `official` 行变成官方商店的默认频道“正式”/`main`，加“测试”/`develop`，删掉其他源行；`publishers` 加 `store_id`。旧核心读 `market_sources` 时要选 `public_key`，所以**迁移之后旧核心不能再跑在这个库上**。
- 0126 只检查：装着发布者不是 `sub2api` 的插件时升级失败并列出它们。生产只有第一方插件，不受影响；有第三方插件的库要先在旧版本卸载它们（并释放标识），升级后重装。
- 新核心读官方商店 `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin` 下各频道的 `index-<分支>/index.json`，每次启动（主节点）按 base 重算频道地址。它不读 `SUB2API_MARKET_SOURCES`；`SUB2API_MARKET_BASE_URL`、`SUB2API_MARKET_ALLOW_HTTP` 只给 e2e、`deploy/single` 这类本地市场用，生产不设。
- 旧核心照旧读 `SUB2API_MARKET_SOURCES`（缺了也能启动，只是不更新源）。所以**生产发布前不用改 `.env`**。

## 一、发布前

1. **网关**：生产用默认的官方地址，不依赖新网关。用本地 http 市场的栈（设了 `SUB2API_MARKET_BASE_URL`、`SUB2API_MARKET_ALLOW_HTTP`）要先换上把这两个变量列进透传白名单的网关，再发核心，否则核心收不到它们，会去读官方商店。
2. **备份**：`pg_dump -Fc` 整库（或确认托管数据库有升级前的快照），记下时间。回退只靠它。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- `market_sources` 没有 `public_key` 列，是官方商店的两个频道“正式”（`main`，默认，保留原 `official` 行的 id）与“测试”（`develop`），地址在 `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index-<分支>/index.json`；`market_stores` 只有官方商店；`plugin_versions` 有 `trust_level`。
- 控制台“插件市场”页选官方商店的“正式”频道能列出插件，发布者、信任级别正确；系统设置的“商店配置”页列出官方商店与两个频道。
- 之后重建网关容器时，从各节点的 `.env` 删掉 `MARKET_SOURCES`（本仓库的 ovh、clicodeplus compose 已不再引用它）。

## 三、回退

迁移之后**只能恢复备份**：停掉所有节点，把数据库恢复到第一节第 2 项的备份，换回旧核心版本，再启动。备份之后写入的数据（包括升级后从商店装的插件版本）会丢失。

删掉的 `public_key`、其他市场源与新加的商店表不用另外处理：新核心不再用它们，恢复备份时它们随旧库一起回来；旧核心不设 `SUB2API_MARKET_SOURCES` 也能启动，用库里已有的源。
