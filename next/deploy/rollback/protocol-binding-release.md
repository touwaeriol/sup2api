# 账号协议绑定：升级前导出存量转换，升级后在账号上开启

带迁移 0106（`accounts.protocols`、`accounts.model_protocols`，CONTRACTS §110）的核心版本用这份手册。版本号发布时才定，定下后把本文件和 `protocol-binding-conversions.sql` 改名为 `<版本>-protocol-binding-release.md`、`<版本>-protocol-binding-conversions.sql`（同时改两个文件里互相引用的名字），并在 `CHANGELOG.md` 的发布小节里引用。所有环境都升级并配置完后删除这两个文件。

## 这次升级改变了什么

- 升级前：任何账号都接受跨平台协议转换。OpenAI 账号会回答 `/v1/messages`，Claude 账号会回答 `/v1/chat/completions`、`/v1/responses` 和 Gemini 端点。
- 升级后：**跨平台协议转换默认关闭**。账号只按它的类型声明的平台服务请求；要转换，必须在账号上为具体的模型勾上转换协议（账号编辑页“协议与模型 › 模型的协议限定”）。
- 迁移不回填（用户 2026-11-17 决定）。字段升级后才存在，没法提前配置，所以**从升级完成到配置好之间，依赖转换的客户端会收到 404** `no account in this group serves this model on this endpoint`。404 不会被客户端当成过载重试。
- 不受影响的：
  - 同一平台内的适配。codex 账号回答 `/v1/chat/completions`（转到 Responses）、只有 chat 的 OpenAI 类型回答 `/v1/responses` 照常工作。
  - 账号类型声明了多个平台的（如 volcengine 声明 openai、anthropic、volcengine），这些平台的协议都是本平台协议，照常工作。
  - 原生请求。
- Bedrock 账号只声明 anthropic 平台：OpenAI 协议的客户端访问 Bedrock 账号属于跨平台转换，升级后同样要开启。

## 一、升级前：导出近 30 天实际发生过的转换

在线上库执行（只读，可以在只读副本上跑）：

```sh
psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 --csv \
  -f next/deploy/rollback/protocol-binding-conversions.sql > conversions-before-upgrade.csv
```

- 查询按 `usage_logs.created_at` 取最近 30 天（索引 `usage_logs_created_idx`），只扫描这段时间内的行；使用记录很多时要跑一会儿，放在低峰期或只读副本上。要缩短窗口加 `-v days=7`。
- 取的是：使用记录里 `conversion` 非空、有服务账号、入站协议（`protocol`）与上游协议（`upstream_protocol`）的平台前缀不同的请求，另加核心本地估算的 count_tokens 请求（`upstream_protocol = 'local'`）。
- 脚本不读 0106 的列，升级前后都能跑。升级后才想起来导出也可以，只是窗口里会少掉升级之后的时间。

每行是一个（账号，入站协议，客户端模型）：

| 列 | 含义 |
|---|---|
| `account_id`、`account_name`、`account_type` | 服务这些请求的账号，类型写成 `插件/类型` |
| `account_state` | `deleted`（账号已删除，不用配置）或账号现在的状态 |
| `route` | `converted`：跨平台转换，升级后关闭，要配置。`local_count`：核心本地估算的 count_tokens，见下 |
| `inbound_protocol` | 客户端用的协议 |
| `enable_protocol` | 要在账号上为这个模型勾选的协议。count_tokens 不单独勾，这一列给出它跟随的生成协议 |
| `model` | 客户端请求的模型名（模型映射之前的名字），配置时填的就是它 |
| `upstream_protocol`、`grade` | 当时转到的上游协议和转换等级 |
| `requests`、`succeeded`、`last_at` | 请求数、其中成功的、最近一次的时间 |

`local_count` 行只在账号类型**没有声明**该协议所属平台时受影响（例如 OpenAI 账号回答 `/v1/messages/count_tokens`）。这种账号通常同时有同一模型的 `converted` 行，给模型勾上 Messages 后计数自动跟着开放。账号类型声明了该平台、只是没有计数端点的（例如 Bedrock 账号回答 `/v1/messages/count_tokens`），升级后照常本地估算，不用配置。

导出结果为空：没有客户端依赖转换，升级后不用配置。

拿到结果后先筛一遍，不必全部恢复：

- `succeeded` 为 0 或很少的行，多半是客户端拿错了模型名打到了别家账号，上游本来就报模型不存在。这种不要开。
- 只给有模型映射、上游确实能服务的模型开转换（CONTRACTS §110.10 第 3 条）。

## 二、升级

迁移 0106 只给 `accounts` 加两列（可空的 `text[]` 和带默认值的 `jsonb`），不回填、不重写表。单独发布时按 [gateway/README.md](../gateway/README.md) “升级和恢复”的常规流程做，改库的版本走主节点优先（`primary-first-v1`）。

**与转发 V2 同版发布时**（协议绑定已合并进 `feat/forwarding-v2`）：转发 V2 改了插件协议和 Host API（CONTRACTS §112.10、§112.11），核心和全部插件要一起换，托管升级走不通，只能停机重新部署（新核心加整套新插件，再把非内置插件逐个装上新版本），见 `CHANGELOG.md` “未发布”里转发 V2 一节的升级步骤。第一节的导出和第三节的配置照做，第二节的升级方式以那一节为准。

升级前把导出的 CSV 留好，并通知依赖转换的客户：升级后到配置完成之间会有一段 404。想缩短这一段，就在升级前把第三节要点的账号和模型列成清单，升级完成后马上配置。

## 三、升级后：照着导出结果在控制台配置

对 CSV 里每个 `account_state` 不是 `deleted` 的账号：

1. 账号列表 → 编辑该账号 → 左侧“协议与模型”。
2. 卡片“模型的协议限定”：
   - **矩阵视图**（账号的模型列表非空时默认）：行是账号的模型，末尾一行“其余模型”；“协议转换（默认不提供）”一组里的列就是可以开启的转换协议。在 CSV 的 `model` 那一行，勾上 `enable_protocol` 对应的列。勾选后这一行从“默认”变成“已指定”，本平台协议的勾保持不变。
   - **列表视图**（账号的模型列表为空时默认）：在“新增一组”里输入 CSV 的 `model`、回车，新的一组已带上账号开放的本平台协议，再点上 `enable_protocol` 对应的转换协议。协议相同的模型会合并成一组。
   - CSV 里的模型名不在账号的模型列表里时（账号靠模型映射接这个名字），矩阵里没有这一行：切到列表视图添加，或先把它加进模型列表。
   - Gemini 的 `gemini.generate` 和 `gemini.stream_generate` 在界面上是一列，一起勾选。
3. 保存。保存后立即生效（各节点的调度快照随账号变更失效），不用重启。

几个账号要配成一样的：账号列表勾选这些账号 → 批量编辑 → 勾“模型的协议限定”，配好后应用。批量编辑是**替换**每个账号现有的设置，并且只有所选账号属于同一个账号类型时可用（不同类型能服务的协议不同）。

用接口配置（`PATCH /api/v1/accounts/:id`，body `{"model_protocols":[{"models":[…],"protocols":[…]}]}`）时注意：**被列出的模型只在这一条写的协议下提供**。模型原来也走本平台协议的，要把本平台协议一起写进去，否则这个模型的原生请求会被关掉。例：OpenAI 账号上 `gpt-5.6` 既要原生又要接 Messages：

```json
{"model_protocols": [
  {"models": ["gpt-5.6"], "protocols": ["openai.chat", "openai.responses", "anthropic.messages"]}
]}
```

控制台的矩阵和列表视图会自动带上本平台协议，不会出这个问题。

## 四、核对

- 账号列表：开了转换的账号，名称下方对应平台的图标带“转”角标，悬停能看到协议和模型。
- 分组详情的“端点”一栏：按成员账号实际开放的协议列出，每个端点后面是提供它的账号数。客户要用的端点在这里应当能看到。
- 用原来依赖转换的客户端（或 curl）各发一个请求，确认不再 404。
- 仍然 404 时看使用记录详情的调度记录，每个没被选的账号都有原因：
  - “模型未开启协议转换”（`conversion_not_enabled`）：这个账号没有为该模型勾这个协议，或模型名与客户端发来的不一致（要填映射之前的名字）。
  - “模型在该协议下不提供”（`model_protocol_closed`）：模型被列进了某一条，但那一条没有这个协议。
  - “账号未开放该协议”（`protocol_closed`）：账号的“本平台协议”设成了“指定”，没有勾这个协议。
- 升级后再跑一次第一节的查询（`-v days=1`），`converted` 行重新出现说明转换流量恢复了。

## 五、出问题时

- 迁移 0106 改了 schema，网关不允许跨 schema 回退核心（升级计划和 `sub2api-gateway rollback` 都会拒绝），核心不回退，问题修复前进。
- 行为上的应急办法：在受影响的账号上给“其余模型”那一行勾上需要的转换协议，这个账号就像升级前一样对所有模型接受该协议的转换。这只是应急：客户端发来的、上游不认识的模型名会原样（或经映射）发给上游，上游报模型不存在，按故障策略可能冷却账号。查清需要哪些模型后改回按模型开启。
- 账号的“本平台协议”保持“全部”（默认）时，本平台的请求与升级前完全一样；这次升级不需要动它。
