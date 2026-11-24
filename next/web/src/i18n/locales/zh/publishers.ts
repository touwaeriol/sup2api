export default {
  title: '发布者',
  description: '商店发布者由官方商店维护；本地发布者只用于手动上传的签名包。',
  tabs: { store: '商店发布者', local: '本地发布者' },
  store: {
    hint: '只读，来自官方商店索引（默认渠道）。增删或吊销发布者请向商店仓库提 PR：',
    plugins: '商店插件',
    installed: '已安装',
    empty: '商店索引里没有发布者'
  },
  local: {
    hint: '本地发布者及其 ed25519 签名公钥，只用于校验手动上传的插件包。',
    idRule: '本地发布者 ID 不能是 sub2api，也不能和商店发布者重名，保证“发布者 + key”全站唯一。'
  },
  create: '新建发布者',
  trustLevel: '信任级别',
  trust_: {
    official: '官方',
    verified: '已认证',
    community: '社区'
  },
  trustHint_: {
    official: '官方发布者（由内置根公钥认证）：可申请全部权限，允许原生界面。',
    verified: '管理员确认过的发布者：可申请全部权限（极高风险仍需逐项确认），允许原生界面。',
    community: '已登记但未验证：不能申请极高风险权限，不允许原生界面。'
  },
  keys: '有效公钥',
  signingKeys: '签名公钥',
  keysUnavailable: '发布者列表中未包含公钥信息。',
  noKeys: '暂无签名公钥。',
  addKey: '添加公钥',
  addKeyTitle: '为 {name} 添加签名公钥',
  keyId: '公钥 ID',
  keyIdHint: '插件签名中引用的标识，必须唯一。',
  publicKey: '公钥',
  publicKeyHint: 'Base64 编码的 ed25519 公钥（32 字节）。',
  publicKeyInvalid: '不是有效的 Base64 ed25519 公钥（32 字节）',
  notBefore: '生效时间',
  notAfter: '失效时间',
  rangeInvalid: '必须晚于生效时间',
  keyAdded: '公钥已添加',
  notYetValid: '未生效',
  revoke: '吊销',
  copyPublicKey: '复制公钥 {key}',
  revokeKey: '吊销公钥',
  revoked: '已吊销',
  confirmRevoke: '确定吊销发布者"{name}"？其所有公钥将失效，由其签名的插件会被标记为"签名已吊销"（默认自动禁用）。',
  confirmRevokeKey: '确定吊销 {name} 的公钥"{key}"？用它签名的插件会被标记为"签名已吊销"（默认自动禁用）。'
}
