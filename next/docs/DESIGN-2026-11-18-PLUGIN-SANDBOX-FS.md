# 插件沙箱：默认限制文件与 unix socket（2026-11-18）

契约见 CONTRACTS §43.8。本文记录现状、内核能提供什么、我们怎么用、白名单每一条的依据，以及没解决的问题。

## 1. 要求与现状

仓库所有者的两条要求：

1. “默认支持严格网络、关闭开发模式，除非主动配置。”——改之前已经满足：`SUB2API_PLUGIN_STRICT_NETWORK`、`SUB2API_PLUGIN_SECCOMP` 在 Linux 上默认 true，`SUB2API_PLUGIN_DEV_MODE` 默认 false（`server/internal/config`；`sub2api dev` 这个本机开发命令自己打开开发模式，不在此列）。本次只在测试里钉住这几个默认值。
2. “插件并不需要去操作文件，如果需要操作文件是需要明确授权的，能不能默认给它限制住？”——指不让插件连本机上不属于它的 unix socket、读写不属于它的文件。

插件进程与核心、网关外壳是同一个系统用户（容器里 uid 1000）。改之前：

- seccomp 在严格网络下只允许 `socket(AF_UNIX)`，但看不到 `connect` 的路径，任何 unix socket 都能连；
- Landlock 默认关（只有网关托管的核心由网关打开，线上两节点 2026-10-29 起开着），而且只处理第 1 版的一部分文件权限（socket、fifo、链接、设备不受限），白名单包括插件的工作目录、整个私有运行目录（含 `TMPDIR`）和一批 `/etc` 文件；
- 网关的管理令牌是一个 0600 文件，`management_auth.go` 明说文件权限分不开同一个 UID 的进程：不开 Landlock 时插件能读到它。

## 2. 内核各版本提供什么

来源：`include/uapi/linux/landlock.h`、`Documentation/userspace-api/landlock.rst`（torvalds/linux），版本号取自各 tag 的 `security/landlock/syscalls.c` 里的 `landlock_abi_version`（v6.12 为 6，v6.15–v6.19 为 7，v7.0 为 8，v7.1 为 9，v7.2 为 10，v7.3 为 11）。

| ABI | 内核 | 新增 |
|---|---|---|
| 1 | 5.13 | 13 项文件权限：执行、读写文件、读目录、删文件与目录、建普通文件/目录/socket/fifo/符号链接/字符与块设备 |
| 2 | 5.19 | `REFER`（跨目录移动、链接；第 1 版一律拒绝） |
| 3 | 6.2 | `TRUNCATE` |
| 4 | 6.7 | 网络：TCP `bind`、`connect` |
| 5 | 6.10 | `IOCTL_DEV`（设备文件的 ioctl） |
| 6 | 6.12 | 作用域：`ABSTRACT_UNIX_SOCKET`（连沙箱外创建的抽象 socket，EPERM）、`SIGNAL`（给沙箱外进程发信号，EPERM） |
| 7 | 6.15 | 审计日志的标志 |
| 8 | 7.0 | `RESTRICT_SELF_TSYNC`（多线程一起限制） |
| 9 | **7.1** | `RESOLVE_UNIX`：连接（以及带目标地址的 `sendmsg`）沙箱外创建的路径型 unix socket，EACCES；同一沙箱里新建的服务端不受影响 |
| 10 | 7.2 | 网络：UDP `bind`、`connect`/`sendto`；审计静默字段 |
| 11 | 7.3 | `RESTRICT_SELF_NO_NEW_PRIVS` 标志 |
| 12 | 7.4-rc | 命名空间、capability 的使用权限 |

第 9 版对应 Linux 7.1：v7.0 的 `syscalls.c` 是 8、头文件没有 `RESOLVE_UNIX`，v7.1 是 9、头文件有。官方的做法是“尽力而为”：先问内核支持的版本，把规则集里不支持的位去掉。

## 3. 我们怎么用

- `plugin-exec`（核心二进制的隐藏子命令）在 `execve` 插件之前：`no_new_privs`、rlimit → Landlock → seccomp → `execve`。Landlock 限制调用线程，`execve` 之后进程只剩这个线程，插件整体在沙箱里。
- 规则集 `landlockHandled(abi, strictNetwork)`：按表把内核支持的全部位加进 handled。网络位只在严格网络下处理（没开严格网络的插件可能自己拨号）；不加任何端口规则，等于全部拒绝，是 seccomp 之下的第二道。作用域两项都加、没有放行项。第 12 版的命名空间与 capability 不处理：seccomp 已拒绝 `unshare`、`setns`，插件没有 capability。
- 白名单规则的权限再与 handled 取交集，所以老内核上不会出现“规则含内核不认识的位”的错误；文件上只给文件类权限。
- 核心启动时自己问一次内核版本（`sandbox.ProbeStatus`），与 `plugin-exec` 同一内核、同一张表，日志与节点页都出自它；不从每个插件进程回传，免得每启动一个插件就打一遍。

## 4. 白名单逐条依据

| 条目 | 结论 | 依据 |
|---|---|---|
| 插件二进制：读、执行 | 留 | `plugin-exec` 限制自己之后 `execve` 它。插件由 `sub2api-plugin build` 以 `CGO_ENABLED=0` 构建，静态链接；测试里动态链接的测试二进制被拒（`exec: permission denied`），所以测试另编一个静态的 |
| go-plugin 的 socket 目录：读写文件、建文件、删文件、建 socket、`RESOLVE_UNIX` | 留，从整个运行目录收窄到这个子目录 | 读 go-plugin v1.8.0：`serverListener_unix` 先 `os.CreateTemp(socketDir, "plugin")`（O_RDWR 建普通文件）再删掉、在同名处 `Listen`；broker 由核心在同一目录监听，插件 `broker.Dial` 去连（HostService、出口隧道都走它）。目录由 go-plugin 在核心一侧建（`plugin-dir*`），以 `PLUGIN_UNIX_SOCKET_DIR` 传给插件，`plugin-exec` 从环境变量读。不给建目录、fifo、链接 |
| 私有运行目录本身与 `tmp/`（`TMPDIR`） | 去 | 现有插件和 SDK 都不写临时文件（查过 `plugins/*`、`sdk/pluginsdk` 的非测试代码：没有 `os.CreateTemp`、`MkdirTemp`、`TempDir`、`ParseMultipartForm`、`exec.Command`）。`TMPDIR` 仍指向这里（不开 Landlock 时行为不变），开着时写入得到 EACCES |
| 工作目录 / 数据目录 | 改为只给获授 `fs.data` 的插件 | 现有插件都不读写它（没有插件读 `SUB2API_PLUGIN_DATA_DIR`，SDK 也不读） |
| `/dev/null`：读写 | 留 | Go 运行时启动时发现标准描述符被关掉会打开 `/dev/null`（Go 1.22 起）；go-plugin 总是给插件接上管道，平时用不到，留着无害 |
| `/dev/urandom`、`/dev/random` | 去 | `crypto/rand` 在 Linux 上用 `getrandom(2)`（Landlock 要求的 5.13 一定有），不打开设备 |
| `/usr/share/zoneinfo`、`/etc/localtime` | 留，全部插件 | `time.Local` 读 `/etc/localtime`；growth 的 `time.LoadLocation` 读时区库（guard 与 gemini 预设内嵌了 `time/tzdata`，growth 没有）。只读、不敏感 |
| CA 证书 | 改为只给获授 `net` 的插件 | 有 `net` 的插件经出口隧道自己做 TLS（`http.DefaultTransport` 的拨号换成隧道，TLS 在插件里），`crypto/x509` 读系统根证书。没有 `net` 的插件不对外连：数据库经隧道时 DSN 是 `sslmode=disable` 的虚拟地址，TLS 在核心（CONTRACTS §115）。路径照 Go 在 Linux 上的 `certFiles`、`certDirectories`，加上 Debian（`/usr/share/ca-certificates`、`/usr/local/share/ca-certificates`）和 RHEL（`/etc/pki/ca-trust/extracted`）的符号链接目标，以及 `SSL_CERT_FILE`、`SSL_CERT_DIR` 的每一项（e2e 栈靠 `SSL_CERT_DIR=/etc/ssl/certs:/e2e-tls` 信任 mock 的 CA）。`/etc/ca-certificates` 是 update-ca-certificates 的配置，去掉 |
| `/etc/resolv.conf`、`/etc/hosts`、`/etc/nsswitch.conf` | 去 | 严格网络下 SDK 把 `net.DefaultResolver` 换成 `PreferGo` + 经隧道拨 `dns.sub2api:53`，地址参数被忽略；HTTP 拨号把主机名交给核心解析。读不到这几个文件时 Go 的解析器用默认配置，不报错 |
| `/etc/services` | 去 | 只有按端口名拨号时读；net/http 用数字端口，Go 另有内置的常用端口表 |

e2e 全套在收窄后的白名单下通过（见 §6），是“没漏”的主要证据。

## 5. 文件授权：`fs.data`

- 宿主权限 `fs.data`，风险中：低风险会在同意时自动授予，不符合“明确授权”；它只开放插件自己的目录，不到高。审查页文案见 CONTRACTS §43.8。
- 只有获授它的插件：核心建 `<SUB2API_PLUGIN_DIR>/<key>/work`、以 `SUB2API_PLUGIN_DATA_DIR` 交给插件、作为工作目录，`plugin-exec --data-dir` 放行它（读写文件与目录、截断、目录内移动；不给 socket、fifo、链接、设备）。没有它的插件不收到这个变量（SDK 不读它，不会崩），工作目录是私有运行目录。
- 授予在进程启动时固定，所以和资源上限一样记在 `specLimits` 里：撤销或新授予 `fs.data`、`net` 后，下一次协调发现 `LimitsStale` 就重启插件进程。
- 现有插件都不需要文件，没有插件改清单、升版本；`hostCompat` 不动（没有插件用新权限）。将来第一个申请它的插件要把 `hostCompat` 下限提到带 `fs.data` 的核心版本，旧核心会把未知权限当校验错误拒绝。内置插件的宿主权限由核心在安装时全部授予（CONTRACTS §26 的内置插件条），申请了 `fs.data` 的内置插件同样自动获得。

## 6. 验证

- **单元测试**（`sandbox` 包）：`landlock_linux_test.go` 起受限的子进程逐项尝试，`goplugin_linux_test.go` 用真实的 go-plugin（AutoMTLS、私有 socket 目录、`RunnerFunc`）起一个插件、核心调插件、插件经 broker 回调核心。内核低于某层的 ABI 时，测试断言该项“仍放行”并记日志，不把它当作已限制。常量对照 uapi 的值（`TestLandlockConstantsMatchUAPI`）。这些测试在 Docker 默认 seccomp 配置下就能跑（Landlock 系统调用被放行），CI 的 `-race` 测试二进制是动态链接的，测试会用 `go test -c` 另编一个静态的作为插件。
- **新内核**：本机内核 6.1（ABI 2）。`next/deploy/ci/sandbox-kernel.sh` 在容器里用 QEMU 软件模拟（不需要 KVM）启动发行版内核包里的 `vmlinuz`，initramfs 里只有静态的测试二进制和一个小 init（`sandbox/testdata/vminit`：挂 `/proc`、`/sys`、securityfs、devtmpfs、`/tmp`，拉起 `lo`，以 uid 1000 跑测试，打印内核版本与 ABI，关机），启动参数 `lsm=landlock`。用法：

  ```sh
  sh next/deploy/ci/sandbox-kernel.sh \
    https://deb.debian.org/debian/pool/main/l/linux-signed-amd64/linux-binary-7.3.5+deb14-amd64_7.3.5-1_amd64.deb \
    https://deb.debian.org/debian/pool/main/l/linux-signed-amd64/linux-image-6.12.117+deb13-amd64_6.12.117-1_amd64.deb
  ```

  参数是内核包的 URL（Debian 7.x 的内核在 `linux-binary-*`，6.x 在 `linux-image-*`），`--` 之后的参数交给测试二进制。每个内核约一分钟。没接进 GitHub CI；GitHub 的 `ubuntu-latest`（24.04）镜像说明写的内核是 7.0（ABI 8），CI 里作用域测试会真跑，路径型 socket 那条仍会走“仍放行”的分支。
- **e2e**：栈里核心默认开着 Landlock，启动日志记生效的层；全套用例通过；另用一个静态探针经同样的 `plugin-exec` 参数核对令牌文件、其他插件目录、`/proc`、`/etc` 被拒。

## 7. 没解决的

- **老内核上路径型 unix socket 仍然能连**（Linux 7.1 之前）：插件能连到同一用户能连的任何路径型 socket，例如网关的管理 socket。网关的管理接口另有令牌（插件读不到令牌文件），但别的同用户 socket 没有这层保护。核心对这种情况逐层记 WARN、在节点页显示。
- 不依赖内核版本的备选方案，这一版不做：
  - **用户命名空间 + 挂载命名空间**（插件只看到绑定挂载进来的几个路径）：需要非特权用户命名空间，很多发行版和容器默认关掉（Docker 默认 seccomp 拒绝 `unshare(CLONE_NEWUSER)`，我们自己的 seccomp 也拒绝 `unshare`），要改容器的安全配置，而且引入一层新的复杂度。
  - **插件用单独的系统用户**：要 root 或 `CAP_SETUID` 启动插件，容器现在 `cap_drop: [ALL]`、`no-new-privileges`；目录和 socket 的属主、组权限都要重新设计（审计 PL-P0-4 已把“独立 UID”另行排期）。
  - **seccomp 用户态通知审查 `connect`**（`SECCOMP_RET_USER_NOTIF`，核心读出路径再决定）：有 TOCTOU 问题（核心读路径与内核实际使用之间插件能改内存），要可靠就得核心代为完成 `connect` 再把描述符注入（`SECCOMP_IOCTL_NOTIF_ADDFD`），实现和维护成本高，而且每次 `connect` 都要绕核心一圈。
- 设备 ioctl（6.10 前）不受限：插件能打开的设备只有 `/dev/null`，影响很小，没单列成一层。
- `TMPDIR` 在 Landlock 下不可写；第一个需要临时文件的插件用 `fs.data` 下的目录。
