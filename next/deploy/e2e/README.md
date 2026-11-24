# 隔离双节点 E2E 环境

这套 Compose 专供 `next/e2e`。它创建自己的 PostgreSQL、Redis、应用数据卷、签名插件市场和网络，默认项目名 `sub2api-next-e2e`，唯一宿主端口是 `127.0.0.1:3120`。Caddy 同时连接项目专用的 `edge` 网桥与 `internal: true` 的 `e2e` 内网；后端容器仅连接内网，一次性测试 runner 只连接 `edge`。不得使用业务项目名 `sup2api`、业务 `.env` 或单测项目 `sub2api-next-testdb` 的数据库和网络。

需要 Linux Docker Engine 与 Docker Compose v2。全部服务由 Compose 启动；推荐在 Docker 主机上通过 `test.sh` 的 Go 1.27 容器运行测试，也可以使用自备 Go 工具链通过 SSH 隧道访问远端环境。测试会创建账号、扣费、停止容器、杀插件进程和触发 OOM，因此仅运行在本目录提供的专用环境。

## 创建独立配置

从仓库根目录执行。下面生成器不会覆盖已经存在的 `.env`：

```sh
env_file=next/deploy/e2e/.env
(
  umask 077
  set -C
  {
    printf 'E2E_PROJECT=sub2api-next-e2e\n'
    printf 'E2E_APP_IMAGE=sup2api-e2e:local\n'
    printf 'E2E_MOCK_IMAGE=sup2api-e2e-mock:local\n'
    printf 'E2E_ADMIN_EMAIL=admin@sub2api.test\n'
    printf 'E2E_DB_PASSWORD=%s\n' "$(openssl rand -hex 24)"
    printf 'E2E_MASTER_KEY=%s\n' "$(openssl rand -base64 32)"
    printf 'E2E_ADMIN_PASSWORD=%s\n' "$(openssl rand -hex 24)"
  } > "$env_file"
)
```

也可以复制 `.env.example` 后填写三项必需密钥。数据库密码放入连接 URL，因此使用十六进制等 URL 安全字符。`E2E_APP_IMAGE`、`E2E_MOCK_IMAGE` 可指向已经构建的镜像；两个应用节点和 `market-init` 必须使用同一个应用镜像。改用其他端口时同时设置 `E2E_PORT` 和 `E2E_PUBLIC_URL`，测试端再设置对应的 `E2E_BASE_URL`。

## 构建、校验和启动

从仓库根目录执行：

```sh
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env config --quiet
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env build node-1 mock-upstream
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env up -d --no-build --wait --wait-timeout 240
```

使用预先构建的镜像时跳过 `build`。`market-init` 会检查市场索引（`index.json` v2，不签名，由 `build-demo.sh` 经 `sub2api-plugin registry import` 与 `index --dev` 生成，CONTRACTS §135）和公钥存在，随后复制镜像内市场到本项目的数据卷；配置错误或市场缺失会阻止节点启动。官方插件商店的 base 经 `SUB2API_MARKET_BASE_URL`（与 `SUB2API_MARKET_ALLOW_HTTP=true`）指向这个市场，市场在 `index-main/`、`index-develop/` 下给每个频道同一份索引（CONTRACTS §137.6），应用入口脚本从同一镜像的公钥配置插件信任（市场里的包仍带开发密钥的签名，上传与内置包照常验签）；不会关闭签名验证。

Caddy 路由：

- `/`：按最少在途请求选择节点，按 `/healthz` 的 200 状态做主动健康检查，网络失败后暂时被动剔除节点。只有 GET/HEAD 分支启用 5 秒的选择重试窗口；POST、PUT、DELETE 等写请求不启用重试，业务 503 不触发被动摘除。
- `/__node1/*`、`/__node2/*`：去掉前缀后固定访问指定节点，保留原响应与健康状态。
- `/__mock/*`：去掉前缀后访问 mock 的控制和观测接口。
- `/market/*`：只读提供索引、公钥和插件包。

容器内 mock 地址为 `http://mock-upstream:8080`；Caddy 内部地址为 `http://caddy:3120`，用于测试的自调用保护检查。PG 用户和数据库均为 `sub2api`，SQL/Redis 检查通过专用容器的 `docker exec` 完成，无需发布数据库端口。

读取和写入使用独立的代理 handler，因为 Caddy 的 `lb_retry_match` 只限制连接建立后的失败，拨号失败仍可对任意方法重试；仅靠该选项无法保证 POST 不重试。直连节点辅助路由不做跨节点回退，以保留节点故障的真实结果。参见 [Caddy 重试与健康检查说明](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#load-balancing)。

## 运行测试

在 Docker 主机上的完整仓库目录中执行，宿主无需安装 Go：

```sh
# 整套约 27 分钟，脚本默认 -timeout=150m。
sh next/deploy/e2e/test.sh
# 只跑选定用例；参数直接传给 go test。
sh next/deploy/e2e/test.sh -run '^TestAC(14|15|17)'
```

用例按 `acNN_*_test.go` 编号。转发 V2 的用例是 AC30–AC36，AC37、AC38 是同批的插件联网与授权代理票据，AC39–AC41 是上游插件一批的账号余额、OpenAI 生图与 Responses 生图工具，AC42 是 Codex 账号经核心转换服务 OpenAI 生图，AC43 是插件账号上下文：

| 用例 | 内容 |
|---|---|
| AC30 | 十一个平台插件各经 `Execute` 转发一个非流式、一个流式请求（typesafe 的 `/v1/systemone` 只有非流式），另有 New API 账号转发 Jev；mock 看到的是插件构造的请求（主机、路径、凭证、请求体；sub2api 的 Gemini 只带 `x-goog-api-key`，systemone 的请求体逐字节不变、危险区头透传），请求只计费一次。newapi、sub2api、typesafe 是只安装的内置插件，用例先启用；它们不申请文件和网络权限，在默认沙箱下运行 |
| AC31 | 运营方的请求头覆盖压过插件的默认头、透传只补没有的头、签名（权威）头改不了、账号代理 |
| AC32 | 上游出错和插件在发送前失败时换号（只计费一次，账号不受罚） |
| AC33 | 旧契约的清单在上传时以固定信息被拒、不留版本行；旧协议的二进制在首次启用时加载被拒，发布在准备阶段失败后插件列表的 `refusal` 仍给出代码、版本、核心版本和信息 |
| AC34、AC35 | Responses WebSocket 逐轮（openai、codex）；请求体上限设置（413、WebSocket 1009、运行时调高） |
| AC36 | 官方 OpenAI（openai-go）和 Anthropic（anthropic-sdk-go）Go SDK 驱动网关：同协议、流式与非流式、工具调用往返、Responses WebSocket，账号开启转换后的跨协议（开启前 404） |
| AC37 | 插件联网（CONTRACTS §113）：guard 的 webhook 告警在默认设置下送达 mock；mock 的主机进屏蔽列表后连接被拒、出口日志记 `denied` 和屏蔽原因；切到“屏蔽所有域名”而允许列表没有它时被拒，加进允许列表后送达。设置只管新建连接，用例先等 guard 到 mock 的空闲连接关闭；另一个用例把 anthropic 账号的上游主机（mock）加进屏蔽列表或不列进允许列表，经网关的请求不再到达 mock、客户端得到 5xx、anthropic 的出口日志记 `denied` 和原因，移出或列入后恢复；保存与触发都发到节点 1（另一节点要等广播）；结束时恢复默认 |
| AC38 | codex 授权走账号表单选的代理（CONTRACTS §117）：已有代理、粘贴的代理串、不用代理三种选择下，设备码登录的全部请求（申请、一次待批准的轮询、批准的轮询、换令牌）到达 `auth.openai.com` 时全部经过代理或全部直连，粘贴的代理串不留代理记录；令牌经 `POST /accounts` 建成账号后经网关发一条请求到 Codex 后端并计费；Sign in with Codex 的换令牌经已有代理 |
| AC39 | 账号余额（CONTRACTS §52，显示上游的数字和单位本身、不换算）：New API 账号（额度点数 1250000、没有单位，用户额度更低时取它，不限额）与 Sub2API 账号（旧版额度 7.5 USD、没有 `unit` 时没有单位、任意文字的 `unit` 原样、订阅 `-1` 为不限、钱包、`isValid:false` 为认证被拒）手动刷新的结果，余额请求带 Bearer、不请求 `/api/status`、返回没有 `source`；新账号不刷新，由集群任务 `core.account.balance_sweep` 在几分钟内填上；站点回 401 时记失败、账号仍启用可调度；经账号代理（mock 记 `proxied`）；mock 主机进插件联网屏蔽列表后刷新失败、mock 收不到请求、newapi 的出口日志记 `denied`（设置的保存与刷新都发到节点 1，结束时恢复默认） |
| AC40 | OpenAI 生图（CONTRACTS §118）：OpenAI API Key、New API、Sub2API 账号各转发 generations（JSON、流式）与 edits（JSON），使用记录有图片输入 / 输出 token，表达式价格 `p*5 + cr*1.25 + img*8 + img_o*32 + c*10` 按分组倍率 1.5 计费（算式在用例注释里）；multipart 的 edits 在网关回 400、留一条免费的 `invalid_request` 记录；Azure 部署（`azure_deployment`）账号不服务生图端点（404；codex 账号经转换服务，见 AC42）；没有价格的模型 403 `model_price_not_configured`（缺价一律报错，CONTRACTS §17.3），都不到 mock |
| AC41 | Responses 的 `image_generation` 工具（CONTRACTS §17.2、§118.10）：OpenAI API Key 账号（JSON、流式、WebSocket 一轮）与 codex 账号（客户端 JSON，到达 Codex 后端时是 `stream: true`、`store: false` 的请求，网关聚合流式应答，图片在应答里）的请求只有一条使用记录、一笔扣费，按客户请求的模型计价：token 是对话与 `tool_usage.image_gen` 之和（输入 159、输出 557、图片输出 515），金额按请求模型的表达式价格乘分组倍率（算式在用例注释里）；`billing_detail` 没有附加或替代分项、没有 `primary_*`；应答 `tools` 回显的生图模型和请求里工具点名的模型都不需要价格；不带工具的请求只有对话的用量 |
| AC42 | Codex 账号经核心转换服务 OpenAI 生图（CONTRACTS §121）：经账号代理，generations（JSON、流式）与 edits（JSON）到达 Codex 后端时是流式的 Responses 请求（顶层 `gpt-6-luna`，强制的 `image_generation` 工具带客户端的模型，edits 带 `action: edit` 和输入图片）；客户端拿到 Images API 形状（`data[].b64_json`、生图部分的 `usage`；流式最后是 `image_generation.completed`）；一条记录：`openai.images` / `openai.images_edit`、上游协议 `openai.responses`、客户端模型、上游模型为上游回显的 `gpt-image-2-codex`，token 是对话与生图两部分之和，按客户端模型的表达式价格乘分组倍率（算式在用例注释里），一笔扣费；上游完成但没出图（mock 的 `MOCK-NO-IMAGE`，不带 `tool_usage`）时 200、`data` 为空、没有 `usage`（流式没有 `completed` 也没有 `error` 事件），记录成功、`error_type` 为空、照扣，账号不记失败（仍启用、可调度、没有冷却）；`n: 2` 回 400、缺价 403，都不到 mock |
| AC43 | 插件账号上下文（CONTRACTS §124）：插件不需授权就收到自己账号类型的账号。codex 账号挂 mock 的代理：重置次数列表路由经 `HostService.GetAccount` 读账号，审计写一条 `plugin.account.read`（写明插件、账号类型和 `host.GetAccount`，不含令牌和代理）；插件描述的 Codex 后端请求（`GET /backend-api/wham/rate-limit-reset-credits`，mock 回答没有重置次数）由核心经账号代理发出（mock 记 `proxied`、主机 `chatgpt.com`）；仍申请 `accounts.credentials` 或 `accounts.read` 的包在上传时以旧契约的固定信息被拒（400） |

插件发布期间节点保持就绪，测试不等 `/healthz`（AC05 的启用用例在发布内置插件期间探测两个节点和负载均衡）。上传插件遇到 409 `cluster_change_busy`（另一个集群变更正在提交）时重试最多 20 秒：上一个用例的卸载要等各节点停掉插件，期间占着集群变更锁。

脚本只接受 `sub2api-next-e2e` 或 `sub2api-next-e2e-<后缀>` 项目名；启动 runner 前核对 6 个运行中容器的 Compose 项目、服务标签和 `edge` 网络归属。它不会启动或重建后端服务。默认读取同目录 `.env`，可用 `E2E_ENV_FILE` 指定其他独立配置；自定义项目时必须显式传入同一个名称，例如 `E2E_PROJECT=sub2api-next-e2e-ci sh next/deploy/e2e/test.sh`，脚本中的项目名优先于 `.env`。

如果部署文件与源码快照分别存放，给脚本设置 `E2E_SOURCE_ROOT=/绝对路径/仓库根目录`，该目录必须包含 `next/e2e`、`next/sdk` 和 `next/plugins/guard`；默认从脚本所在目录推导仓库根目录。源码绑定仍固定落在 runner 的 `/src`，不改变测试内的定位规则。

`runner` 位于 `test` profile，普通 `up` 不会运行它。它把完整仓库只读挂到 `/src`，在 `/src/next/e2e` 当场编译并执行测试，使用项目专属的 Go 模块和构建缓存；测试目标固定为 `http://caddy:3120`，Docker 操作固定走本机 socket。默认开启 `E2E_LONG=1`；`E2E_GO_IMAGE` 和 `E2E_GOPROXY` 可在 `.env` 中调整。Docker CLI 默认 `/usr/bin/docker`，可通过脚本环境变量 `E2E_DOCKER_BIN` 指定，必须是与 Linux runner 架构匹配的可执行文件。首次运行需要拉取 Go 镜像及下载模块；runner 可通过 `edge` 出网，数据库和业务节点仍仅连接内网。

AC14/AC15 会重编译派生 guard 插件，因此**只复制预编译测试二进制并不够**：执行环境必须有 Go 1.27 和完整源码，且源码位于 `runtime.Caller` 记录的编译路径。不要用 `-trimpath` 编译 E2E 测试，也不要编译后搬走源码。`test.sh` 同路径编译和执行，满足这个要求。

runner 为执行故障注入而挂载 `/var/run/docker.sock`，拥有宿主 Docker 管理权限；项目校验用于防误操作，不是 Docker 权限隔离。它只应执行受信任的仓库测试。其余部署服务均不挂载 socket。脚本要求在测试 Docker 主机本地执行，不接受远程 Docker context；远端测试先 SSH 到专用源码目录再执行同一条命令。

也可以在有 Go 1.27 的主机上，从编译时相同的完整源码目录直接运行：

```sh
set -a
. ./next/deploy/e2e/.env
set +a
export E2E_BASE_URL="${E2E_PUBLIC_URL:-http://127.0.0.1:3120}"
export E2E_DOCKER_HOST=local
export E2E_LONG=1
(cd next/e2e && go test -count=1 -timeout=150m -v ./...)
```

如果 Docker 位于远端，在本机建立 `ssh -N -L 3120:127.0.0.1:3120 <测试主机>`，将 `E2E_DOCKER_HOST` 改为该 SSH 主机名，并给测试进程提供**这套 E2E 环境**的管理员凭据及 `E2E_PROJECT`。`E2E_NODE_URLS`、`E2E_MOCK_URL` 默认从 `E2E_BASE_URL` 推导，不需要逐项指定。不要把单测的 `TEST_DATABASE_URL` / `TEST_REDIS_URL` 指向这里；E2E 自己创建完整业务状态。

节点采用 `restart: "no"`，保证 SIGKILL 后不会自动复活干扰接管断言；测试负责重启，测试异常退出后可重新 `up -d`。节点内存固定为 1 GiB，匹配 AC14 的 1500 MiB OOM 场景。插件内存默认不限（不设 `SUB2API_PLUGIN_MAX_MEMORY_MB`），需要上限的用例（AC14）自己经 `PUT /plugins/:key/resources` 设插件上限。节点停止宽限为 180 秒，允许 HTTP、后台工作和账务完成排空。

插件的 strict network、seccomp、Landlock（核心默认开启，CONTRACTS §43.8；生效到哪一层看节点日志里的 `plugin sandbox:` 几行，Docker 默认的 seccomp 配置放行它的系统调用）和签名校验全部开启，开发模式及未签名包关闭。插件数据库隔离（每个插件一个数据库身份，CONTRACTS §130）始终开启：栈里的 `POSTGRES_USER` 是超级用户，也是库的属主。网络策略例外只有两项：`SUB2API_GATEWAY_ALLOW_PRIVATE_UPSTREAM=true` 与 `SUB2API_PLUGIN_EGRESS_ALLOW_PRIVATE=true`（插件自己的出口，如 guard 的 webhook、moderation 的审核模型调用），因为 mock 位于专用容器私网；这两项只适合隔离测试，不应复制到生产。

其他与生产不同、只为这套环境存在的安排：

- 镜像版本默认 `0.1.999`（`E2E_VERSION`），必须满足全部内置插件的 `hostCompat`（目前 `>=0.1.304 <0.2.0`），否则节点拒绝安装内置插件。
- 两个节点直接运行核心（没有网关外壳），核心的插件包目录 `plugins/blobs` 只服务一个节点；这里两个节点挂同一个卷 `pkgblobs`，上传的插件包两边都能取到。
- 没有可配置地址的平台插件（bedrock、vertex、codex）和用默认官方地址的 typesafe 账号访问厂商主机。mock 在 `e2e` 网络上带这些主机名的别名（`compose.yml` 的 `aliases`），在 443 端口提供 TLS，证书按客户端请求的主机名（SNI）用自建 CA 现签（`MOCK_TLS_ADDR`、`MOCK_TLS_DIR`，mock-upstream 的 `tls.go`），所以主机名只列在 `aliases` 一处，CA 写在卷 `mocktls`，节点以 `SSL_CERT_DIR=/etc/ssl/certs:/e2e-tls` 信任它。所以这些插件以正式构建原样运行，请求只到 mock；内网是 `internal: true`，到不了真实厂商。
- mock 同时是 HTTP 正向代理（账号的 `proxy_url` 指向它时记 `proxied`）：http 地址是绝对 URI 的请求；https 地址经 `CONNECT`，mock 自己用 TLS 监听的证书和处理器接管隧道，隧道里的请求记 `tls` 和 `proxied`（mock-upstream 的 `connect.go`）。mock 还扮演 OpenAI 授权服务器 `auth.openai.com`（codex 两种登录的设备码与换令牌，`openaiauth.go`；同一设备码第一次轮询答“未批准”，之后批准，轮询间隔 1 秒），并能按提示里的 `MOCK-TOOL-CALL` 返回工具调用（官方 SDK 用例）；它也扮演 New API、Sub2API 站点的余额接口和 OpenAI 生图接口（`sites.go`，余额应答可用 `/__control` 规则的 `balance` 换掉），在 `api.typesafe.ai` 主机名下按 TypeSafe 的形状列模型；Responses 请求带 `image_generation` 工具时回 `tools` 回显与 `tool_usage.image_gen`（提示里有 `MOCK-IMAGE-ECHO-NO-MODEL` 时回显不带模型）。两个网络都由本次 Compose 项目创建，没有外部网络引用或宿主网络。`edge` 为 Caddy 提供宿主端口发布通路，也供显式启动的 runner 访问入口；PG、Redis、节点和 mock 不因此获得外部网桥连接。

## 保留现场与清理

```sh
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env ps -a
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env logs --no-color > e2e-stack.log
# 停止环境，保留数据库和其他测试数据。
docker compose -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env down
```

需要全新数据库重跑时，核对 `.env` 中项目名为本次专用 E2E 项目，再对上述同一条 `down` 命令添加 `--volumes`。若还要删除仅被 `test` profile 使用的 Go 缓存卷，清理时需显式启用该 profile：

```sh
docker compose --profile test -f next/deploy/e2e/compose.yml --env-file next/deploy/e2e/.env down --volumes
```

这会删除该项目的全部测试数据及运行器缓存；普通 `down --volumes` 可能保留未启用 profile 的缓存卷。不要用 `docker system prune` 或操作其他项目。
