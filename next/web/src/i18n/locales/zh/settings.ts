export default {
  description: '全局的计费、网关与调度设置。',
  missingTab: '链接指向的设置页不可用（没有权限，或已不存在），已显示第一个设置页。',
  groups: {
    gateway: '网关',
    pricing: '分组与定价',
    billing: '计费与通知',
    runtime: '运行环境'
  },
  tabs: {
    notifications: '通知',
    environment: '环境变量',
    billing: '计费',
    gateway: '网关设置',
    sticky: '粘性会话',
    offload: 'CPU 保护'
  },
  scheduling: {
    tabs: { failure: '故障策略', balance: '均衡负载' }
  },
  balance: {
    subtitle: '在同一优先级内给新会话选号。只影响新会话和换号，已绑定的会话不会被移走。',
    mode: '均衡策略',
    modes: { default: '默认', traffic: '流量' },
    modeHints: {
      default: '同优先级内按权重随机选号。',
      traffic: '同优先级内优先选用量低于权重份额的账号，让流量按权重比分配。'
    },
    limitLoad: '基于上限负载调度',
    limitLoadHint: '用量越接近上限，权重越低。例：RPM 上限 100、已用 90，权重 ×10%。没设上限（按模型调度时也没给该模型设指定模型限速）的账号不受影响。',
    metric: '均衡指标',
    metricHint: '流量均衡和上限负载都按这个指标计算。没设 TPM 上限或按模型调度时，TPM 在请求结束后才计入，会稍有滞后。仅在流量策略或基于上限负载调度开启时生效。',
    metrics: { rpm: 'RPM', concurrency: '并发', tpm: 'TPM' },
    modelLimits: '根据模型限速',
    modelLimitsHint: '按请求的模型调度：流量均衡比较各账号上这个模型自己的用量；上限负载在该模型设了指定模型限速时，也按该列表的用量和上限算，取较高的一个。仅在流量策略或基于上限负载调度开启时生效。'
  },
  offload: {
    nodesTitle: '节点负载',
    title: 'CPU 保护',
    subtitle: '节点 CPU 过高时，把新请求转给其他空闲节点。只在网关托管的多节点集群中可用。',
    enabled: '启用',
    enabledHint: '关闭时每个节点只处理自己入口的请求。',
    threshold: 'CPU 阈值',
    thresholdHint: '节点最近 10 秒的平均 CPU 达到该值后开始转移，降到阈值减 10 以下时停止。只转给低于阈值减 10、版本相同的正常节点；没有这样的节点就留在本节点处理。已在处理的请求不受影响。',
    range: '范围 {min}–{max}。',
    unmanaged: '当前节点不是由网关托管的，没有 CPU 保护。多节点部署请使用网关托管。',
    node: '节点',
    cpu: 'CPU',
    state: '状态',
    states: {
      serving: '本地处理',
      offloading: '正在转移',
      forwarding: '转发到主节点',
      unavailable: '未就绪',
      disabled: '已禁用'
    }
  },
  notifications: {
    channelsTitle: '通知渠道',
    channelsHint: '由插件声明并投递（例如邮件插件提供「邮件」）。其他插件和核心按渠道发送通知，不需要了解提供渠道的插件。',
    noChannels: '还没有提供通知渠道的插件。启用邮件插件等通知插件后，这里会出现对应渠道。',
    channel: '渠道',
    provider: '提供插件',
    recipientFormat: '收件人格式',
    recipient: { email: '邮箱地址', text: '文本（由插件解释）' },
    testTitle: '发送测试通知',
    testHint: '由核心发出一条测试通知，用于检查渠道配置。发送结果见下方投递记录。',
    recipients: '收件人',
    recipientsHint: '多个收件人用逗号或空格分隔，最多 20 个。',
    sendTest: '发送测试',
    testQueued: '测试通知已加入发送队列',
    recordsTitle: '投递记录',
    recordsHint: '失败的投递会按退避重试，最多 8 次；记录保留 30 天。',
    allStatuses: '全部状态',
    source: '来源',
    subject: '主题',
    attempts: '第 {n} 次',
    status: { pending: '等待发送', sending: '发送中', sent: '已发送', failed: '失败' },
    severity: { info: '信息', warning: '警告', critical: '严重' },
    errors: {
      unknown_channel: '没有这个通知渠道，可能提供它的插件已停用',
      recipients_count: '收件人需为 1-20 个',
      recipient_too_long: '单个收件人不能超过 320 字节',
      invalid_recipient: '收件人格式不符合该渠道的要求'
    }
  },
  environment: {
    title: '环境变量',
    subtitle: '转发的通用运行参数，由核心声明，插件读取生效后的值。账号、分组可以覆盖这里的值；留空表示跟随下一层（账号 → 分组 → 全局 → 进程环境变量）。修改后各节点对新请求生效。',
    groupHint: '覆盖全局环境变量；留空跟随全局。',
    accountHint: '覆盖分组与全局的环境变量；留空跟随分组。',
    range: '范围 {min}–{max}。',
    processSet: '进程环境变量 {name} = {value}。',
    processUnset: '可用进程环境变量 {name} 兜底。',
    follow: '跟随',
    followValue: '跟随（{value}）',
    units: { seconds: '秒', minutes: '分钟', mb: 'MB', count: '次' }
  },
  gateway: {
    title: '网关',
    subtitle: '网关请求的失败切换、超时与协议转换。修改后各节点对新请求生效。',
    fields: {
      max_attempts: '最大尝试次数',
      platform_call_timeout_ms: '平台调用超时',
      default_hook_timeout_ms: '钩子默认超时',
      platform_hotpath_timeout_ms: '平台热路径超时',
      queue_sticky_max_waiting: '粘性账号最多排队数',
      queue_sticky_wait_seconds: '粘性账号最长等待',
      queue_fallback_max_waiting: '全部账号满时每账号最多排队数',
      queue_fallback_wait_seconds: '全部账号满时最长等待',
      media_download_max_mb: '媒体下载上限',
      claude_default_max_tokens: 'Claude 默认 max_tokens'
    },
    hints: {
      max_attempts: '每个请求最多尝试的账号数（含首次，用于失败切换）。',
      queue_sticky_max_waiting: '会话绑定的账号并发满时，每个账号最多几个请求排队等它，不换号以保住提示缓存。0 表示不排队。',
      queue_sticky_wait_seconds: '等绑定账号的最长时间，超时后按粘性规则换号或返回 429。0 表示不排队。',
      queue_fallback_max_waiting: '所有候选账号都满时，每个账号最多几个请求排队。0 表示不排队，直接返回 429。',
      queue_fallback_wait_seconds: '所有候选账号都满时的最长等待，超时返回 429。0 表示不排队。',
      platform_call_timeout_ms: '请求路径上调用插件平台接口（构造上游请求、解析用量）的超时。',
      default_hook_timeout_ms: 'manifest 未设置超时的钩子使用此值。',
      platform_hotpath_timeout_ms: '每个请求中不属于钩子的平台调用超时：调度前解析模型、响应发完后提取用量。',
      media_download_max_mb: '请求转换到 Gemini 时下载 http(s) 图片等媒体，单个文件的大小上限（MB）。',
      claude_default_max_tokens: '请求转换到 Anthropic 时客户端没给输出上限，按客户端模型名精确匹配取 max_tokens，没有匹配用 default 行。default 行不能删除或改名，最多 200 行，取值 0–1048576。'
    },
    maxTokens: {
      model: '模型',
      tokens: 'max_tokens',
      modelPlaceholder: '客户端模型名',
      add: '添加模型',
      remove: '删除 {model}',
      defaultLocked: 'default 行不能删除或改名',
      errors: {
        value: '每行都要填 0 到 {max} 之间的整数',
        model: '模型名不能为空，最长 200 个字符',
        duplicate: '模型名重复',
        too_many: '最多 {n} 行'
      }
    },
    range: '范围 {min}–{max}。',
    notInteger: '请输入整数'
  },
  billing: {
    preConsumeTokens: '请求预扣 Token 数',
    preConsumeHint: '文本请求预占的输入 Token 下限，默认 500；本地估算超过此值时使用估算值。0 表示不设下限，最终仍按实际用量结算。视频使用插件自己的用量估算。',
    preConsumeInvalid: '请输入 0 到 100000000 之间的整数',
    title: '计费',
    missingPolicy: '模型没有配置价格时',
    policy: {
      reject: '拒绝请求',
      free: '免费放行'
    },
    policyHint: {
      reject: '返回 403 model_price_not_configured。推荐：没有价格的模型不对外提供。',
      free: '照常转发，使用记录的结算状态为"免费"。'
    },
    minBalance: '最低余额',
    minBalanceHint: '余额不高于该值时，请求返回 402 余额不足（默认 0）。',
    bigCost: '高额费用警告阈值',
    bigCostHint: '保存价格时，若 100 万输入 token 的费用超过该值，需要二次确认。',
    decimalInvalid: '请输入数字，最多 8 位小数'
  }
}
