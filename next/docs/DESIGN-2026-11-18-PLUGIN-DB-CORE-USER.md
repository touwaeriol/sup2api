# 插件数据库只用核心的数据库用户

2026-11-18，所有者决定："它访问数据库都是通过核心的数据库配置来访问，而不需要任何新数据库用户"，随后补充："不能每个插件一个用户…数据库直接委托给核心来代理就行了"。第一部分（§2–§5）去掉插件的数据库用户；第二部分（§6）由核心代为登录，数据库凭据不进插件进程。契约写进 CONTRACTS §115。

## 1. 为什么改

改前，核心给每个持有 `db.schema` 的插件建一个登录角色 `plg_<key>`（`CREATE ROLE … LOGIN CONNECTION LIMIT 20`，密码由主密钥 HMAC 派生，角色注释作归属标记，`GRANT plg_<key> TO CURRENT_USER`，schema 交给该角色，角色的 `search_path` 固定为自己的 schema），插件迁移以该角色登录执行，迁移记录经两个 SECURITY DEFINER 函数（迁移 0003、0023）写入，卸载清除数据时 `DROP OWNED BY` 再 `DROP ROLE`。核心的数据库用户因此要有 `CREATEROLE`，没有时授予 `db.schema` 失败（`plugin_db_isolation_unavailable`），运维可用 `SUB2API_PLUGIN_DB_ROLE_ISOLATION=false` 改为共用核心的用户。

用生产库副本演练恢复时，不带属主（`--no-owner`）恢复后插件读不了自己的表（permission denied），要手工把 26 个对象改回属主。每个插件一个用户让备份恢复依赖属主和集群级的角色，而角色不在单库的备份里。

## 2. 改后的模型

- 只有一个数据库用户：核心的（`DATABASE_URL` 里的）。核心不再建、改任何角色，也不再需要 `CREATEROLE`。
- 持有 `db.schema` 的插件仍有自己的 schema `plg_<key>`，属主是核心的用户。核心在插件第一次需要时（迁移、`GetDSN`）`CREATE SCHEMA IF NOT EXISTS`。
- 插件迁移由核心以自己的用户执行，每个文件一个事务，执行前 `SET LOCAL search_path TO plg_<key>`；迁移记录由核心直接写 `public.plugin_migrations`。迁移 0110 删除两个 SECURITY DEFINER 函数。
- `HostService.GetDSN` 返回的 DSN 见 §6：严格网络下（Linux 默认）是不带凭据的虚拟地址，由核心代为登录；`role_isolated` 不再填写，随后从协议删除（`GetDSNResponse` 字段 3 `reserved`，SDK 的 `DSNInfo.RoleIsolated` 一并删除）。
- 卸载并清除数据：`DROP SCHEMA plg_<key> CASCADE`，删除迁移记录。
- 删除的东西：角色的创建与修改、密码派生、归属标记的写入、成员授予、schema 移交、角色的 `search_path`、`rewriteDSN` 换凭据的分支、`Tracker.AsPluginRole`、`Status.Role`/`RoleIsolated`、错误 `plugin_db_isolation_unavailable` 与 `plugin_db_role_conflict`、配置 `SUB2API_PLUGIN_DB_ROLE_ISOLATION`，以及它们的测试。

## 3. 隔离的含义（直说）

插件拿不到数据库密码（§6），但它能执行核心的数据库用户能执行的任何 SQL：持有 `db.schema` 的插件能访问核心的用户能访问的一切：核心自己的表（`public`）、升级计划（`updater`）、其他插件的 schema，能读也能写。插件之间、插件与核心之间的分隔只靠约定（`search_path` 只是默认位置，不是边界）和安装时对高风险权限 `db.schema` 的同意，PostgreSQL 不再强制。

实际能读到什么（已在代码里核对）：

- 读不到明文的：上游账号凭据（`accounts.credentials_enc`）、代理密码（`proxies.password_enc`）、API Key 的可还原副本（`api_keys.key_cipher`）、TOTP 密钥、插件的加密设置与 `secrets`、价格同步源的密钥，都用主密钥加密（`secret.Cipher`，AES-GCM），主密钥只在核心进程里（`SUB2API_MASTER_KEY`，读完即从环境删除），不在数据库里。API Key 与访问密钥按 SHA-256 存（`key_hash`、`token_hash`），用户密码存哈希。会话令牌在 Valkey，不在 PostgreSQL。
- 读得到的：用户邮箱和资料、余额与账本、用量日志、分组与价格、角色与授权（`casbin_rule`）、审计日志、其他插件表里的数据。
- 写得了的：上面所有表。能改余额、给自己或任何用户授予角色、改账号配置、覆盖加密列（换成自己的值不需要主密钥就能让数据失效）。也就是说，持有 `db.schema` 的插件能拿到整个系统的管理权。

因此：

- 官方发布者的插件照常授予 `db.schema`。
- 非官方插件授予 `db.schema` 一律要求操作者持有超管专属的 `plugin:grant:db_schema_unconfined`（改前只在关闭角色隔离时要求）；评审接口的 `needs_db_schema_unconfined` 同步，权限的说明改为"插件使用核心的数据库用户，能访问核心能访问的所有表"。

## 4. 已有数据库的升级

生产库里有这些角色，插件的表、视图、序列、schema 都属于它们。

- **启动时一次**：核心列出名为 `plg_%` 的角色，逐个在插件自己的 schema 锁（`plugin-schema:<key>`，与 Ensure、Drop 同一把事务级 advisory 锁）里转换：确认角色是本库为该插件建的（注释等于 `sub2api plugin role; database=<本库>; plugin=<key>`，或角色拥有本库的 `plg_<key>` schema——标记出现之前建的角色），然后在一个事务里 `REASSIGN OWNED BY plg_<key> TO CURRENT_USER`、`DROP OWNED BY plg_<key>`（撤销授给它的权限）、`DROP ROLE plg_<key>`。已卸载插件留下的角色同样转换。不是本库的同名角色（另一部署、运维自建）不动。
- **Ensure 时**：每次确保某插件的 schema（迁移、`GetDSN`、清除数据）先做同样的转换。
- **权限**：`REASSIGN OWNED`、`DROP OWNED` 要求核心有该角色的权限，`DROP ROLE` 要求 `CREATEROLE`（PostgreSQL 16 起还要对该角色有 ADMIN）。这些都是旧代码建角色时就具备的：角色由核心的用户创建（16 起自动得到 ADMIN），并且旧代码执行过 `GRANT plg_<key> TO CURRENT_USER`（继承权限）。所以不需要超级用户；Supabase 的 `postgres` 用户就是这种情况。逐个对象 `ALTER … OWNER TO` 要求同样的权限，不是退路，所以不做。
- **还有会话的角色**：PostgreSQL 允许删除仍有会话的角色（已在 PostgreSQL 17 上验证），删掉后旧进程会失去自己表的权限。所以转换前查 `pg_stat_activity`，有该角色的会话就整个跳过、只记一次警告，下次 Ensure（以及下次启动）再试；此期间核心的用户经成员关系照样能读写这些表，新插件进程不受影响。
- **多节点同时启动**：同一插件的转换在 schema 锁里串行，后到的节点再查时角色已不在，什么也不做；不同插件的转换另取一把全局锁 `plugin-role-conversion`（总在插件锁之后取），避免并发改同一对象的 ACL 报 `tuple concurrently updated`。
- **滚动重启怎么收尾**：本版本有核心迁移（0110），按主节点优先升级：从节点先停，主节点停机、迁移、启动；旧核心停下时它的插件进程随之退出，会话结束，所以主节点第一次启动就能转换全部角色。若某个旧插件进程的连接还没断（例如节点没按计划停），该角色留到下次 Ensure：那个节点换成新核心、插件重启取 DSN 时，或任何节点下次启动时完成。
- **恢复的三种情况**：
  - 不带属主恢复（全部对象已属于核心的用户、没有角色）：什么也不做，正常启动。
  - 带属主恢复（角色存在、对象属于角色）：与生产相同，启动时转换。
  - 带属主恢复但目标集群没有这些角色：恢复本身会在 `ALTER … OWNER TO plg_<key>` 上报错，对象留给执行恢复的用户；用核心的用户恢复即可，之后同第一种。

## 5. 不能回退

迁移 0110 删除了旧核心给插件角色用的两个函数。回退到旧核心时，旧核心会重新建角色并在授予这两个函数时失败，持有 `db.schema` 的插件起不来。所以这是单向升级：回退只能连同数据库一起回到升级前的备份。

## 6. 核心代理插件的数据库连接

第一部分之后，`GetDSN` 交出的是核心自己的 DSN，核心的数据库用户和密码会进入每个持有 `db.schema` 的插件进程。第二部分让核心代为登录：

- **DSN**：严格网络下（`SUB2API_PLUGIN_STRICT_NETWORK`，Linux 默认开），`GetDSN` 返回 `postgres://plugin@db.sub2api:5432/plg_<key>?sslmode=disable`：固定的虚拟地址，没有密码，用户名和库名只为满足客户端的 DSN 解析，核心不看。
- **隧道**：插件经出口隧道拨 `db.sub2api:5432`（SDK 的 `Host.DB` 本来就这样拨号，`DialByName`）。隧道只对策略里有 Database（获授 `db.schema` 且 manifest 声明了 `database`）的插件接受这个地址，否则 `egress: the plugin has no database`；接受后像 `dns.sub2api` 一样在进程内接一条管道交给数据库代理（`egress/database.go`，`egress.go` 的分派处多一个分支）。隧道本身已经认出是哪个插件实例，所以插件侧不问密码。
- **代理**（`runtime-contract/gormpg/pgproxy`，只有 gormpg 下可以 import pgx；`dbschema.Manager.ServeTunnel` 调它）：
  - 插件侧的启动：SSL 和 GSS 加密请求回 `N`（管道在本进程内）；读启动消息，忽略其中的 `user`、`database`、`options`、`replication` 和 `_pq_.*`，其余运行参数（`datestyle`、`intervalstyle`、`search_path` 等）带到服务器。
  - 服务器侧：用核心解析过的同一份配置（`gormpg.ParseConfig(DATABASE_URL)`：主机、端口、库名、用户、密码、TLS、`sslnegotiation`）经 pgconn 登录，SCRAM/MD5 与 TLS 都由核心完成；运行参数另外固定 `search_path=plg_<key>`、`application_name=sub2api plugin <key>`（运维在 `pg_stat_activity` 里能看出是哪个插件的连接）；协议版本与插件请求的一致（pgx 默认 3.0）。
  - 登录后给插件发 `AuthenticationOk`、服务器报告的全部 `ParameterStatus`、服务器的 `BackendKeyData`（真实的进程号与密钥）和 `ReadyForQuery`，然后接管（`Hijack`）服务器连接，两个方向原样转发字节，任一方向结束就关闭两边。
  - 连不上服务器时给插件发 `FATAL`：服务器自己的拒绝（如连接数满）原样带上 SQLSTATE 和消息，其他原因只说“the host could not open the database connection”，详细原因记在核心日志里（可能含主机和用户名，不发给插件）。
  - **取消**：pgx 5.11 在语句的 ctx 结束时先让连接读写超时，再在后台 `asyncClose`：经同一个 `DialFunc`（隧道）向连接的远端地址发 CancelRequest，再发 Terminate、读完残余数据后关闭。隧道连接的远端地址就是 `db.sub2api:5432`，所以取消请求回到代理；代理按进程号找到这个插件自己的会话、比对密钥，再按会话的加密方式（TLS 时先 SSLRequest，`sslnegotiation=direct` 时直接 TLS）把取消请求发给服务器。别的插件的会话或错的密钥一律忽略（与服务器的做法相同）。插件进程直接死掉时没有取消请求：代理关闭服务器连接，正在执行的语句要到它下次向客户端写数据时才结束，这是 PostgreSQL 的行为，与改前直连相同。
- **连接数**：每条插件连接是核心向 PostgreSQL 开的一条连接，不在核心的连接池里，不另设上限（所有者规则）。数量 = 插件自己的池：SDK 默认每节点每插件 4 个（`pgdb.DefaultMaxConns`，插件可用 `pluginsdk.WithDBPoolSize` 改），连接空闲 30 分钟、存活 1 小时后由插件侧关闭；每次取消另有一条瞬时连接。规划 `max_connections`：节点数 ×（核心池 `SUB2API_PG_MAX_CONNS`，默认 20 + 每个用数据库的插件 4 个）。现在 6 个官方插件用数据库（anthropic、dynweight、guard、moderation、payment、volcengine；growth 已在 0.1.304 删除，CONTRACTS §125），全部启用时每节点最多 20 + 24 个。
- **没有隧道时**（`SUB2API_PLUGIN_STRICT_NETWORK=false`、非 Linux、`sub2api dev`，插件不在沙箱里，SDK 的 `installed` 为 false 时直连）：`GetDSN` 仍返回核心的 DSN 加 `search_path`（第一部分的做法），插件拿得到密码。这种模式下插件进程本来就不受网络与文件限制，只用于开发和测试。插件单元测试（`pluginsdktest`）不经核心，不受影响。
- **隔离的说法换成**：插件拿不到数据库密码；它仍能执行核心的数据库用户能执行的任何 SQL，schema 之间的隔离仍只靠约定（§3）。不多说。
- **遗留给别的包**：隧道对真实数据库地址的例外（`egress.Options.DatabaseAddrs`）在严格网络下用不到（插件只拿到虚拟地址，也没有凭据），但仍有一条路径用它：插件用 `pluginsdk.WithStrictNetwork(StrictAlways)` 在不要求严格网络的核心上（`SUB2API_PLUGIN_STRICT_NETWORK=false`、非 Linux、`sub2api dev`）自己装上隧道，`GetDSN` 给它核心的 DSN，它经隧道拨真实地址。核心只要插件有 `net` 或 `db.schema` 就提供出口服务，与是否严格网络无关，所以这条路径能走通；第一方插件都不用 `StrictAlways`。因此保留（转发 V2 收尾核实，`egress.go` 的注释写明）；proto 的 `GetDSNResponse.role_isolated` 和 SDK 的 `DSNInfo.RoleIsolated` 已在转发 V2 收尾的协议清理中删除（字段 3 `reserved`），`host.proto` 对 GetDSN 的注释一并改写；`sub2api-plugin db diff|check` 的比对工具（`pgdbtool.Drift`）也不再用临时角色，以连接的用户、只设 `search_path` 执行迁移，和宿主一样（CONTRACTS §115）。

