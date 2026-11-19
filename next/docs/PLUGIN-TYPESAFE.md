# typesafe 插件：TypeSafe（Jev）API Key

> 状态：2026-11-18 按分支 `feat/up-p7` 的代码写成，**未发布**（插件 0.1.0，需核心 ≥0.1.304）。只对官方文档（https://docs.typesafe.ai 、OpenAPI 0.2.0，2026-11-18 查阅）和单元测试、合规套件验证过，**没有用真实 TypeSafe 账号验证**。
> 设计见 [DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md](DESIGN-2026-11-18-UPSTREAM-PLUGINS-AND-BALANCE.md) 2.4、3.5.3、3.6 与 D21、D22；实现是 SDK 预设 `sdk/pluginsdk/providers/typesafe`。

## 1. 它做什么

- 自己声明平台 `typesafe`，网关多一个端点 `POST /v1/systemone`（协议 `typesafe.systemone`，只有非流式），和 TypeSafe 官方路径一致。
- 一个账号类型 `apikey`（控制台显示 "TypeSafe"）：把客户端的 systemone 请求**原样**转给 `{Base URL}/v1/systemone`，Key 放在 `Authorization: Bearer`。
- 插件只构造请求，由核心发送；插件不联网（不申请 `net`）。
- 内置、仅安装、默认不启用（D11）：管理员在插件页启用后才有这个平台和端点。停用后 `/v1/systemone` 返回 404。
- 平台定义的唯一来源是 SDK 的 `providers/typesafe.Platform()`，插件清单里的平台与它逐字段相同（清单测试钉住）。别的插件（New API）的账号类型也可以服务这个平台。

## 2. 建账号

| 字段 | 必填 | 说明 |
|---|---|---|
| API Key（`api_key`） | 是 | TypeSafe 的 Key，任意非空字符串（保存时去掉首尾空白）；加密保存 |
| Base URL（`base_url`） | 否 | 留空用官方地址 `https://api.typesafe.ai`；按填写的保存，地址已以 `/v1` 结尾时不再重复添加（请求时去掉这一段再拼 `/v1/systemone`） |

- 没有 `account:settings:custom` 权限的人只能用官方地址（`guardedSettings`，官方地址后带 `/v1` 也算，清单的 `versions`）；改成别的地址的账号照 §21.3 需要 `account:relay`。
- 默认模型：`jev-latest`、`jev-1.14.0`、`jev-1.13.0`、`jev-preview`。"拉取模型"读 `GET /v1/models` 的 `models[].name`。
- 账号测试：用 `jev-latest` 发一条 `noul` 问题（`state: "ping"`）。

## 3. 请求

- 接口：只有 `POST /v1/systemone`（官方公开接口除它之外只有模型列表）。
- 请求体顶层 `model`、`state`、`questions`、`assets`。四种问题类型 `noul`、`choice`、`score`、`box` 都只是请求体内容，网关不解析、不检查，原样转发；题目数量、上下文长度、图片格式与尺寸、32 MiB 的请求体上限都是厂商侧的限制，被拒时错误原样返回。网关自己只有一个请求体上限：网关设置的 `max_request_body_mb`（默认 128 MB）。
- 图片：只能用 JSON 里的 `assets[].data_base64`。官方另有 `multipart/form-data` 上传形式，网关只收 JSON 请求体，这种请求在网关就以 400 拒绝、不调度。
- 两个请求头由客户端决定带不带，网关放行（平台的 `passHeaders`）、不改值：
  - `TypeSafe-Danger-Zone-Images: true`：请求里有图片或 `box` 问题时必须带，否则 TypeSafe 回 400。
  - `TypeSafe-Danger-Zone-Long-Context: true`：把上下文上限从每请求 64k / 每问题 32k 提到 262k。
- 钩子（内容审核）读 `state` 作为要判断的文本，与其它端点一样。

## 4. 用量与计费

- 核心按应答的 `usage.input_tokens`、`usage.output_tokens` 记用量（不是插件提供的用量）。应答里的 `model` 是实际作答的版本（别名会被解析，如 `jev-latest` → `jev-1.14.0`），记作上游模型；计价按客户端请求的模型名。
- TypeSafe 只收输入：**$0.042 / 百万输入 token，输出免费**，图片折成输入 token，没有单独的图片费（来源：TypeSafe 官方文档，2026-11-18 查阅）。
- 价格不随插件带来。管理员要给每个会被请求的模型名（别名和具体版本都要，或只开放其中一种）录 `per_token` 价：输入 `0.042`、输出 `0`（每百万 token，美元）。
- 没有余额查询：TypeSafe 没有余额或用量接口，账号类型不声明 `balance`，控制台不查也不显示余额；花费看网关自己的使用记录。

## 5. 错误

- 上游错误原样透传给客户端（状态码与 `detail` 体都不改）。网关**自己**产生的错误（鉴权失败、没有可用账号、余额不足）是 OpenAI 形状（`errorFormat: "openai"`，D22）。
- 对账号的处理（`providers/typesafe.Policy`）：

| 上游状态 | 账号 | 请求 |
|---|---|---|
| 401、403 | 停用（Key 被拒） | 换号 |
| 429 | 冷却到 `Retry-After` / `retry-after-ms`，都没有时用 SDK 的默认冷却 | 换号 |
| 529（过载）、其它 5xx | 短暂冷却 | 换号 |
| 400（含超上下文 `max_tokens_exceeded`）、413、415、422 | 不动 | 原样返回客户端 |

- 账号停用原因里带 `detail` 的文字（字符串、对象的 `message` 或数组第一项的 `msg`）。需要别的处理时用故障策略（§58）按关键词改写。
