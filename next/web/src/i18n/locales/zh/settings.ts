export default {
  description: '全局的计费、网关与调度设置。',
  missingTab: '链接指向的设置页不可用（没有权限，或已不存在），已显示第一个设置页。',
  groups: {
    gateway: '网关',
    pricing: '定价',
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
    subtitle: '在同一优先级内给新会话选号：优先选用量低于权重份额的账号，让流量按权重比分配。只影响新会话和换号，已绑定的会话不会被移走。',
    limitLoad: '基于上限负载调度',
    limitLoadHint: '用量越接近上限，权重越低。例：RPM 上限 100、已用 90，权重 ×10%。没设上限（按模型调度时也没给该模型设指定模型限速）的账号不受影响。',
    metric: '均衡指标',
    metricHint: '流量均衡和上限负载都按这个指标计算。',
    metricDetails: 'TPM：没设 TPM 上限或按模型调度时，在请求结束后才计入，会稍有滞后。会话：各账号在自己的会话周期（默认 60 秒）内活跃的会话数，没有命中粘性规则的请求每个都算一个会话，对应的上限是会话数限制。同优先级的账号会话周期不同时，会话数不能直接比较，按会话均衡流量时请设成相同的周期。',
    metrics: { rpm: 'RPM', concurrency: '并发', tpm: 'TPM', sessions: '会话' },
    modelLimits: '根据模型限速',
    modelLimitsHint: '按请求的模型调度：流量均衡比较各账号上这个模型自己的用量；上限负载在该模型设了指定模型限速时，也按该列表的用量和上限算，取较高的一个。'
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
    testSent: '测试通知已发送',
    testFailed: '测试通知发送失败：{error}',
    testRetrying: '测试通知发送失败，将按退避重试：{error}',
    testWaiting: '测试通知仍在发送，结果稍后见投递记录',
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
    range: '范围 {min}–{max}。',
    processSet: '进程环境变量 {name} = {value}。',
    processUnset: '可用进程环境变量 {name} 兜底。',
    follow: '跟随',
    followValue: '跟随（{value}）',
    units: { seconds: '秒', minutes: '分钟', count: '次' }
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
      max_request_body_mb: '请求体上限',
      media_download_max_mb: '媒体下载上限',
      claude_max_tokens: 'Claude 默认 max_tokens（内置参数覆盖）'
    },
    hints: {
      max_attempts: '每个请求最多尝试的账号数（含首次，用于失败切换）。',
      queue_sticky_max_waiting: '会话绑定的账号并发满时，每个账号最多几个请求排队等它，不换号以保住提示缓存。0 表示不排队。',
      queue_sticky_wait_seconds: '等绑定账号的最长时间，超时后按粘性规则换号或返回 429。0 表示不排队。',
      queue_fallback_max_waiting: '所有候选账号都满时，每个账号最多几个请求排队。0 表示不排队，直接返回 429。',
      queue_fallback_wait_seconds: '所有候选账号都满时的最长等待，超时返回 429。0 表示不排队。',
      platform_call_timeout_ms:
        '两种单次调用的超时：插件对上游失败做错误分类；网关为请求读取“客户端中途断开时”的计费策略，超时读不到按预扣计费。插件转发请求时自身的处理不限时间。',
      default_hook_timeout_ms: 'manifest 未设置超时的钩子使用此值。',
      platform_hotpath_timeout_ms: '每个请求在调度前不属于钩子的平台调用超时：解析模型、为预扣估算用量。',
      max_request_body_mb:
        '每个网关请求的请求体大小上限（MB），超出返回 413；WebSocket 的单条消息也按它限制。这是唯一的请求大小上限，端点、分组和插件都不能另设。',
      media_download_max_mb: '请求转换到 Gemini 时下载 http(s) 图片等媒体，单个文件的大小上限（MB）。',
      claude_max_tokens:
        '开启后，发往 Anthropic 账号的请求没带 max_tokens 时按下表补上，带了不改；账号自己的参数覆盖在其后执行，可再修改。关闭时只有转换到 Anthropic 的请求按协议要求补 {fallback}。'
    },
    maxTokens: {
      title: '各模型的 max_tokens',
      hint: '按客户端模型名精确匹配，没有匹配用 default 行；没有 default 行时其他模型不补（转换到 Anthropic 的请求仍补 {fallback}）。最多 {models} 行，取值 {min}–{max}。',
      model: '模型',
      tokens: 'max_tokens',
      modelPlaceholder: '客户端模型名或 default',
      add: '添加模型',
      remove: '删除 {model}',
      errors: {
        value: '每行都要填 {min} 到 {max} 之间的整数',
        model: '模型名不能为空，最长 {nameLength} 个字符',
        duplicate: '模型名重复',
        too_many: '最多 {models} 行'
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
    minBalance: '最低余额',
    minBalanceHint: '余额不高于该值时，请求返回 402 余额不足（默认 0）。',
    bigCost: '高额费用警告阈值',
    bigCostHint: '保存价格时，若 100 万输入 token 的费用超过该值，需要二次确认。',
    decimalInvalid: '请输入数字，最多 8 位小数',
    disconnectPolicy: '客户端中途断开时',
    disconnect: {
      precharge: '按预扣计费',
      continue: '读完再计费',
      free: '不计费'
    },
    disconnectHint: {
      precharge: '立即中止上游请求，按请求前的预扣估算计费。',
      continue:
        '不再向客户端发送，但继续读完上游响应，按上游报告的实际用量计费。读取期间仍占用账号并发，并和普通请求一样受上游整体超时、流式空闲超时限制；读不完时按预扣估算计费。默认。',
      free: '立即中止上游请求，这次请求不计费。客户端收完正文再断开即可不付输出费用，只建议在内部或可信环境使用。'
    }
  }
}
