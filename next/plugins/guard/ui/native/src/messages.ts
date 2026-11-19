// Guard UI messages, registered under plugin.guard.* via host.addMessages.
export default {
  en: {
    title: 'Request guard',
    subtitle: 'Blocks gateway requests whose prompt matches keyword or regex rules.',
    manageRules: 'Manage rules',
    blocked: 'Blocked',
    requests: 'Requests',
    blockRate: 'Block rate',
    trend: 'Blocking trend',
    topRules: 'Top rules',
    hits: '{n} hits',
    recent: 'Recently blocked',
    noBlocks: 'Nothing blocked in this period',
    blockedToday: 'Blocked today',
    vsYesterday: 'vs. same time yesterday',
    kind: { keyword: 'Keyword', regex: 'Regex' },
    col: { time: 'Time', rule: 'Rule', model: 'Model', user: 'User', request: 'Request', snippet: 'Snippet' },
    rules: {
      title: 'Blocking rules',
      name: 'Name',
      kind: 'Type',
      pattern: 'Pattern',
      enabled: 'Enabled',
      add: 'Add rule',
      save: 'Save rules',
      saved: 'Rules saved; they take effect immediately',
      empty: 'No rules yet',
      invalidRegex: 'Invalid regular expression',
      required: 'Required',
      readOnly: 'You can view rules but not change them.',
      regexHint: 'Regex uses Go RE2 syntax (no look-around).',
      remove: 'Remove rule',
      keywordPlaceholder: 'keyword',
      loadFailed: 'Failed to load rules',
      saveFailed: 'Failed to save rules'
    },
    loadFailed: 'Failed to load guard statistics',
    refresh: 'Refresh',
    openDashboard: 'Open the request guard dashboard',
    settings: {
      webhookUrl: {
        label: 'Webhook URL',
        help: 'A JSON POST is sent here whenever a rule blocks a request. Leave empty to disable. With an allowlist egress policy the domain must be approved under the "net" permission.'
      },
      cooldown: {
        label: 'Alert cooldown',
        unit: 's',
        help: 'Send at most one alert per rule within this many seconds, across all nodes (0–300). Requests are still blocked and logged during the cooldown, just not alerted. 0 sends an alert for every blocked request. Cluster-wide throttling needs the optional "lock" permission; without it each node throttles on its own, up to one alert per node.'
      },
      snippets: {
        label: 'Record matched snippets',
        help: 'Store about 120 characters around the match in the block log and alerts. May contain user data.'
      }
    }
  },
  zh: {
    title: '请求守卫',
    subtitle: '拦截提示词命中关键词或正则规则的网关请求。',
    manageRules: '管理规则',
    blocked: '拦截',
    requests: '请求',
    blockRate: '拦截率',
    trend: '拦截趋势',
    topRules: '命中排行',
    hits: '{n} 次',
    recent: '最近拦截',
    noBlocks: '该时间段内没有拦截',
    blockedToday: '今日拦截',
    vsYesterday: '较昨日同时段',
    kind: { keyword: '关键词', regex: '正则' },
    col: { time: '时间', rule: '规则', model: '模型', user: '用户', request: '请求', snippet: '片段' },
    rules: {
      title: '拦截规则',
      name: '名称',
      kind: '类型',
      pattern: '匹配内容',
      enabled: '启用',
      add: '添加规则',
      save: '保存规则',
      saved: '规则已保存，立即生效',
      empty: '还没有规则',
      invalidRegex: '正则表达式无效',
      required: '必填',
      readOnly: '你可以查看规则，但不能修改。',
      regexHint: '正则使用 Go RE2 语法（不支持环视）。',
      remove: '删除规则',
      keywordPlaceholder: '关键词',
      loadFailed: '加载规则失败',
      saveFailed: '保存规则失败'
    },
    loadFailed: '加载拦截统计失败',
    refresh: '刷新',
    openDashboard: '打开请求守卫面板',
    settings: {
      webhookUrl: {
        label: 'Webhook 地址',
        help: '规则拦截请求时向该地址发送 JSON POST，留空则不发送。出口策略为白名单时，域名需在 net 权限中获批。'
      },
      cooldown: {
        label: '告警冷却时间',
        unit: '秒',
        help: '同一条规则在这段时间内全集群最多发送一次告警（0–300 秒）。冷却期内请求照常拦截和记录，只是不再告警。0 表示每次拦截都发送。全集群节流需要可选的 lock 权限；未授权时各节点各自节流，每个节点最多各发一次。'
      },
      snippets: {
        label: '记录命中的文本片段',
        help: '在拦截记录和告警中保存命中位置附近约 120 个字符，可能包含用户数据。'
      }
    }
  }
}
