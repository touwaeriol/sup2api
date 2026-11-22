export default {
  rulesTitle: '会话绑定规则',
  title: '粘性会话',
  description: '让同一会话的连续请求尽量落到同一个账号，提高上游缓存命中率。',
  newRule: '新增规则',
  any: '任意',
  seconds: '秒',
  ttlValue: '{n} 秒',
  ttlDefault: '默认',
  copyAsAdmin: '复制为管理员规则',
  customized: '已修改',
  customizedHint: '已修改内置规则的定义，升级不会覆盖；可恢复为默认。',
  resetDefault: '恢复默认',
  resetConfirm: '把内置规则"{name}"的定义恢复为默认？启用状态和优先级保持不变。',
  resetDone: '已恢复默认',
  flush: '清空绑定',
  flushConfirm: '清空规则"{name}"的所有绑定？之后的请求会重新调度。',
  flushed: '绑定已清空',
  flushedN: '已清空 {n} 条绑定',
  statsLine: '命中 {hits} · 未命中 {misses} · 改绑 {rebinds}',
  source: {
    admin: '管理员',
    builtin: '内置'
  },
  cols: {
    ua: 'UA',
    priority: '优先级',
    match: '匹配',
    binding: '绑定',
    stats: '命中率',
    keySources: '会话 key',
    ttl: 'TTL',
    keyIncludes: 'key 包含',
    onFailure: '失败策略'
  },
  keyTypes: {
    body: '请求体字段',
    header: '请求头',
    api_key: 'API Key',
    user: '用户'
  },
  keyTypeHint: {
    api_key: '发起请求的 API Key',
    user: 'API Key 所属用户'
  },
  includes: {
    group: '账号分组',
    model: '模型',
    rule: '规则'
  },
  onFailure: {
    failover: '切换',
    stick: '保持'
  },
  onFailureHint: {
    failover: '绑定的账号失败时照常切换账号，成功后改绑。',
    stick: '不切换，直接返回错误（保护上游缓存）。'
  },
  settings: {
    title: '全局设置',
    enabled: '粘性会话',
    enabledHint: '所有规则的总开关',
    defaultTtl: '默认 TTL',
    defaultTtlHint: '规则未设置 TTL 时使用',
    ttlInvalid: '请输入正整数秒数',
    keepOnDisabled: '账号禁用时保留绑定',
    keepOnDisabledHint: '默认在账号被禁用时删除其绑定'
  },
  modal: {
    createTitle: '新增粘性规则',
    editTitle: '编辑规则 · {name}',
    copyTitle: '复制为管理员规则',
    limitedHint: '内置规则：名称不能修改，其余内容可以修改，修改后升级不会覆盖，可随时恢复默认。',
    priorityHint: '数值越大越先匹配',
    match: '匹配条件',
    protocols: '协议',
    protocolPick: '+ 选择协议',
    protocolUnknown: '未知',
    protocolPattern: '通配',
    protocolUnknownHint: '标记为"未知"的协议不在当前已声明的端点里（声明它的插件可能已卸载或停用），保存时按原样保留。',
    protocolsPartial: '平台列表加载失败，只能列出内置平台：插件声明的协议可能缺失，可在下方手动填写。',
    protocolManual: '手动填写协议，如 anthropic.messages',
    models: '模型',
    modelsHint: '支持通配符；为空表示任意',
    ua: 'User-Agent 包含',
    emptyAny: '为空表示任意',
    keySources: '会话 key 来源',
    keySourcesHint: '按顺序取第一个非空值。',
    valueRegex: '值正则',
    valueRegexHint: '可选；取第一个捕获组（或整个匹配）作为值',
    passHeaders: '透传请求头',
    passHeadersHint: '命中本规则的请求把这些客户端请求头带给上游。* 表示全部；regex:正则 按名称匹配；其他按名称精确匹配。凭证、客户端地址和网关控制头不会透传',
    ttlHint: '0 表示使用默认 TTL',
    keyIncludesHint: '会话 key 还包含哪些维度',
    needSource: '至少添加一个 key 来源',
    needPath: '请填写请求体字段路径',
    needHeader: '请填写请求头名称',
    badRegex: '正则表达式无效',
    badTtl: '请输入不小于 0 的整数秒数'
  }
}
