export default {
  kind: {
    user: '用户',
    group: '账号分组',
    api_key: 'API Key',
    account: '账号',
    model: '模型',
    proxy: '代理',
    pluginScope: '插件数据'
  },
  contains: '名称包含 “{text}”',
  containsHint: '模糊匹配',
  byId: '使用 ID #{id}',
  byIdHint: '列表里没有这个 ID，可能已删除；仍可直接使用',
  asTyped: '使用 “{text}”',
  asTypedHint: '列表里没有这个名称；仍可直接使用',
  inactive: '已停用',
  more: '只显示前 {n} 个，输入以缩小范围',
  moreUnlisted: '还有更多未列出，输入以缩小范围',
  none: '没有匹配的{name}',
  failed: '列表加载失败',
  failedTypeValue: '候选暂不可用，可直接输入值',
  typeId: '{name} ID',
  typeIdOrName: '{name} ID 或名称',
  typeFor: '{label}（{what}）',
  add: '添加{name}',
  remove: '移除 {name}',
  scope: {
    userKeys: '只列 {name} 的密钥',
    groupAccountsFirst: '{name} 里的账号排在前面'
  }
}
