# 插件商店：迁移 0125 的发布与回退

带迁移 `0125_market_sources_drop_public_key.sql`（CONTRACTS §136）的核心版本用这份手册。版本号发布时才定，定下后把本文件改名为 `<版本>-market-0125-release.md`，并在 `CHANGELOG.md` 的发布小节里引用。所有环境都升级完、不再可能回退后删除本文件。

## 这次升级改变了什么

- 0125 是单向迁移：删除 `market_sources.public_key`，给 `plugin_versions` 加 `trust_level`。旧核心读 `market_sources` 时要选 `public_key`，所以**迁移之后旧核心不能再跑在这个库上**。
- 新核心只读官方商店 `https://raw.githubusercontent.com/touwaeriol/sup2api-plugin/index/index.json`：每次启动（主节点）把源 `official` 设为这个地址，删掉其他源行。它不读 `SUB2API_MARKET_SOURCES`；`SUB2API_MARKET_URL`、`SUB2API_MARKET_ALLOW_HTTP` 只给 e2e、`deploy/single` 这类本地市场用，生产不设。
- 旧核心照旧读 `SUB2API_MARKET_SOURCES`（缺了也能启动，只是不更新源）。所以**生产发布前不用改 `.env`**。

## 一、发布前

1. **网关**：生产用默认的官方地址，不依赖新网关。用本地 http 市场的栈（设了 `SUB2API_MARKET_URL`、`SUB2API_MARKET_ALLOW_HTTP`）要先换上把这两个变量列进透传白名单的网关，再发核心，否则核心收不到它们，会去读官方商店。
2. **备份**：`pg_dump -Fc` 整库（或确认托管数据库有升级前的快照），记下时间。回退只靠它。

## 二、发布

照常导入核心发布，计划是 `primary-first-v1`（有迁移）。完成后核验：

- `market_sources` 没有 `public_key` 列，只剩一行 `official`，地址是官方商店；`plugin_versions` 有 `trust_level`。
- 控制台“插件市场”页的“商店地址”是官方地址，能列出官方索引的插件，发布者、信任级别正确。
- 之后重建网关容器时，从各节点的 `.env` 删掉 `MARKET_SOURCES`（本仓库的 ovh、clicodeplus compose 已不再引用它）。

## 三、回退

迁移之后**只能恢复备份**：停掉所有节点，把数据库恢复到第一节第 2 项的备份，换回旧核心版本，再启动。备份之后写入的数据（包括升级后从商店装的插件版本）会丢失。

删掉的 `public_key` 和其他市场源不用另外恢复：新核心不再用它们，恢复备份时它们随旧库一起回来；旧核心不设 `SUB2API_MARKET_SOURCES` 也能启动，用库里已有的源。
