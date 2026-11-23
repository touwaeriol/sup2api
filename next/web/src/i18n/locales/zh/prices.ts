export default {
  title: '模型价格',
  description: '按模型配置计费表达式。价格由管理员维护：手动填写，或从同步源导入。',
  scopeNote: '这里设的是原价。账号分组可以给模型另定模型价格，账号分组和用户分组可以设倍率；实付 = 模型价格 × 生效的那一个倍率。',
  scopeLinks: { groups: '账号分组', segments: '用户分组' },
  simulate: '价格试算',
  simulateModel: '试算这个模型',
  new: '新增价格',
  newTitle: '新增价格',
  editTitleShort: '编辑价格',
  source: {
    manual: '手动',
    sync: '同步'
  },
  sourceSync: '同步 · {name}',
  fromSource: '同步源：{name}',
  clearSourceFilter: '清除同步源筛选',
  syncPrices: '同步价格',
  viewSources: '同步源',
  syncedNotice: '从 {source} 同步于 {time}；修改价格后会变成手动价格，之后同步不会再自动覆盖（除非在同步预览中勾选）。',
  syncedNoticeDeleted: '已删除的同步源',
  model: '模型',
  modelHint: 'API 请求中使用的完整模型 ID，不支持通配符。',
  modelInvalid: "请填写完整的模型 ID：只能包含字母、数字和 . _ : / {'@'} + -，不能有通配符或空格",
  modeCol: '方式',
  summaryCol: '摘要',
  searchPlaceholder: '模型、备注…',
  exprHash: '表达式 hash',
  viewHistory: '查看表达式',
  billingMode: '计费方式',
  expression: '表达式',
  mode: {
    visual: '可视化',
    expression: '表达式'
  },
  perRequestPrice: '每次价格',
  per: {
    request: '/次',
    token: '/1M token'
  },
  vars: {
    p: '输入',
    c: '输出',
    cr: '缓存读',
    cc: '缓存写 5 分钟',
    cc1h: '缓存写 1 小时',
    img: '图片输入',
    img_o: '图片输出'
  },
  unsetPlaceholder: '不收费',
  toExpression: '改用表达式编辑',
  serverGenerated: '服务端生成的表达式',
  edit: {
    modeHint: {
      visual: '按档位计价：每档按 token 或按次收费，按顺序取第一个满足条件（长度、token 数、时间）的档位，最后一档兜底；再按请求参数、请求头、用量字段或时间加价。保存时转成表达式计费。',
      expression: '界面写不了的复杂计费用表达式。从可视化切过来会填入它生成的表达式；只有可视化生成的表达式才能切回可视化。'
    },
    notVisual: '这条表达式不是可视化生成的，无法转成可视化，已丢弃',
    preview: '预览',
    previewMode: '定价',
    previewExpr: '计费表达式（USD）',
    previewSame: '即上方编辑框里的表达式。',
    updatedAt: '更新于 {time}',
    save: '保存模型价格',
    guide: '表达式使用说明'
  },
  presets: {
    title: '预设模板',
    group: {
      fixed: '固定价格',
      tiered: '阶梯',
      advanced: '进阶'
    },
    item: {
      single: '单一价格',
      claude: 'Claude（含缓存）',
      image: '图片模型',
      claudeLong: 'Claude 长上下文',
      gptLong: 'GPT 长上下文',
      three: '三档',
      request: '按次',
      priority: 'priority 整单 ×2',
      night: '夜间五折',
      minimum: '最低收 $0.001'
    }
  },
  tier: {
    listHint: '按顺序匹配档位，第一个满足条件的档位决定价格；最后一档兜底。',
    add: '添加档位',
    matchOnly: '始终匹配（只有这一个档位）',
    matchRest: '兜底：前面的档位都没命中时',
    moveUp: '上移',
    moveDown: '下移',
    unnamed: '未命名',
    restBadge: '兜底档位',
    remove: '删除这个档位',
    condition: '阶梯条件',
    onlyHint: '只有一个档位，所有请求都按它算。加档位后，这一档变成兜底。',
    restHint: '兜底：前面的档位都没命中时按这一档算，不用设条件。',
    badge: '档位',
    name: '档位名称',
    unit: '这一档的计价方式',
    unitOf: {
      token: '按 token',
      request: '按次'
    },
    requestHint: '命中这一档的请求按次收费，不看 token 数。',
    tokenPrices: 'Token 价格',
    media: '媒体定价',
    fallback: {
      cc1h: '不勾选按缓存写 5 分钟价',
      img: '不勾选按输入价',
      img_o: '不勾选按输出价'
    }
  },
  cond: {
    var: {
      len: '输入长度 len',
      p: '输入 token p',
      c: '输出 token c',
      hour: '小时',
      minute: '分钟',
      weekday: '星期',
      month: '月',
      day: '日'
    },
    and: '与',
    or: '或',
    notText: '非（{cond}）',
    negated: '取反',
    kind: '组合方式',
    group: '组 {path}',
    groupLabel: '条件组 {path}',
    leafLabel: '条件 {path}',
    actions: '条件 {path} 的操作',
    varLabel: '比较的量',
    tz: '时区',
    op: '比较符',
    value: '值',
    add: '添加',
    addCond: '添加条件',
    addGroup: '添加条件组',
    negate: '条件取反',
    unnegate: '取消取反',
    remove: '删除条件',
    commonTz: '常用',
    allTz: '全部时区',
    unknown: '无法识别的条件：',
    hint: '同一组里的条件按“与 / 或”组合，可以嵌套子组、取反；最多 {depth} 层、{leaves} 个条件。len = 输入 + 缓存读写 + 图片输入；时间按所选时区，星期日是 0。'
  },
  rules: {
    title: '请求规则计费',
    hint: '条件满足时，最终价格乘以倍率；多组命中时倍率相乘；小于 1 的倍率为折扣。',
    group: '规则组 #{n}',
    defaultName: '规则组 {n}',
    name: '规则组名称',
    namePlaceholder: '名称（可选）',
    remove: '删除规则组',
    condSource: '条件来源',
    source: {
      param: '请求体参数',
      header: '请求头',
      usage: '用量字段',
      time: '时间'
    },
    tz: '时区',
    fromHour: '开始',
    toHour: '结束',
    to: '到',
    field: '字段',
    op: '比较',
    ops: {
      eq: '等于',
      ne: '不等于',
      contains: '包含',
      exists: '存在',
      gt: '>',
      gte: '≥',
      lt: '<',
      lte: '≤'
    },
    value: '值',
    removeCond: '删除条件',
    addField: '新增参数 / 请求头',
    addTime: '新增时间条件',
    multiplier: '倍率',
    multiplierHint: '条件全部满足时，最终费用 = 基础费用 × 倍率',
    add: '新增规则组',
    common: '常用规则',
    preset: {
      priority: 'OpenAI priority ×2（请求体 service_tier）',
      flex: 'OpenAI flex ×0.5',
      fast: 'Claude fast mode ×2（请求头）',
      night: '夜间 0–8 点 ×0.8（上海时间）'
    }
  },
  estimator: {
    title: 'Token 估算器',
    titleRequest: '费用估算',
    hint: '输入 token 数量预览费用（不含倍率）。',
    hintRequest: '一次请求的费用（不含倍率）；在请求模拟里填参数、请求头或时间，看规则是否命中。',
    more: '更多用量（1 小时缓存、图片）',
    len: '指定输入上下文总长度',
    lenPlaceholder: '使用现有 token 总长度（{n}）',
    fixFirst: '先修好上面的问题再估算。',
    cost: '预计成本：',
    tier: '命中档位：',
    rule: '规则 ×{m}',
    detail: '明细',
    hideDetail: '收起明细',
    simulate: '请求模拟',
    simulateHint: '请求体参数、请求头、用量字段、时间',
    paramsHint: '值按 JSON 读，读不了按文本',
    at: '请求时间',
    atHint: '按你的浏览器时区；空着按现在'
  },
  check: {
    ok: '检查通过',
    checking: '检查中…',
    problems: '有 {n} 个问题，不能保存',
    position: '第 {line} 行第 {column} 列',
    locate: '定位',
    costPerMillion: '100 万输入 token 的费用：{cost}'
  },
  validate: {
    warnTitle: '确认高额费用',
    warnConfirm: '服务端提示：{list}。仍然保存？'
  },
  exprEditor: {
    vars: '变量：',
    fns: '函数：',
    fnList: 'tier(name, 费用), flat(美元), param(path), header(name), u(name), has(文本, 片段), hour(tz), weekday(tz), max, min, ceil, floor, abs',
    note: '单价按美元 / 百万 token 写，`flat(0.01)` 是每次 0.01 美元；取不到的 token 数和用量字段按 0 算。',
    guide: '完整说明',
    label: '计费表达式',
    format: '格式化'
  },
  // Texts of the expression guide (ExprGuide) may mark `code` and **bold** (GuideText).
  guide: {
    engine: '后端用 Go 的表达式库 **expr-lang/expr**（v1.17.8），语法和运算符以它为准，内置函数只开放 `max()` `min()` `abs()` `ceil()` `floor()`。本平台在它之上加了 `tier()`、`flat()`、`param()`、`header()`、`u()`、`has()`、时间函数 `hour()` `minute()` `weekday()` `month()` `day()` 和变量 `p`、`c`、`cr`、`cc`、`cc1h`、`img`、`img_o`、`ai`、`ao`、`len`，并禁用了 `__rule`、`$env`。',
    engineLink: 'expr-lang 文档',
    nav: {
      basics: '基本写法',
      vars: '变量',
      ops: '运算符',
      fns: '函数',
      tiers: '档位',
      rules: '加价规则',
      examples: '例子',
      check: '检查'
    },
    insert: '插入',
    fill: '填入',
    basics: {
      cost: '表达式算出这一次请求的费用。token 变量是这次用掉的 token 数，单价按**美元 / 百万 token**写：`p*3` 就是输入每百万 3 美元。',
      flat: '`flat(0.05)` 是直接加 0.05 美元，不按百万换算。',
      round: '结果不能是负数。最终金额 = 结果 × 倍率，**进一到 8 位小数**（只会多扣，不会少扣）。',
      layout: '可以随意换行和加空格；“格式化”会把每个乘数排成一行。',
      generated: '可视化的价格保存时也转成表达式，切到“表达式”就能接着改。'
    },
    var: {
      p: '输入 token，不含缓存读写，也不含单独计价的图片输入',
      c: '输出 token，不含单独计价的图片输出',
      cr: '缓存读 token',
      cc: '缓存写 token（5 分钟）。表达式没写 cc1h 时，1 小时的缓存写也算在这里',
      cc1h: '缓存写 token（1 小时）',
      img: '图片输入 token',
      img_o: '图片输出 token',
      len: '完整上下文长度 = 输入 + 缓存读写 + 图片输入，分档时用'
    },
    varsNote: '表达式里没写到的缓存类别不收费；没写 img、img_o 时图片 token 留在 p、c 里按文字价算。',
    ternary: '条件 ? 甲 : 乙',
    op: {
      arith: '加减乘除、取余',
      compare: '比较',
      logic: '与、或、非（也可写 and、or、not）',
      ternary: '条件成立取甲，否则取乙，可以连着写',
      coalesce: 'a 是空（nil）时取 b，常用于 param() 读不到的情况',
      paren: '括号改变先后顺序'
    },
    opsNote: '字符串用双引号；比较字符串区分大小写。',
    fnGroup: {
      tier: '档位与固定价',
      read: '读请求和用量',
      time: '时间（按请求开始的时刻）',
      math: '数学'
    },
    fn: {
      tier: { sig: 'tier("名称", 费用)', desc: '返回费用本身，并把这次请求记为命中这个档位（使用记录里会显示）' },
      flat: { sig: 'flat(美元)', desc: '固定金额，直接按美元计，不用除以百万' },
      param: { sig: 'param("路径")', desc: '读请求体里的字段，路径用点分隔，如 "reasoning.effort"；"messages.#" 是消息条数。读不到时是 nil' },
      header: { sig: 'header("名称")', desc: '读请求头，名称不分大小写；没有这个头时是空字符串 ""' },
      u: { sig: 'u("字段")', desc: '读插件报的用量字段（应答里来的，如服务等级、生成秒数）；没报时是 0。字段名必须是插件声明过的' },
      has: { sig: 'has(文本, "片段")', desc: '文本里含有这个片段时为真，如 has(header("anthropic-beta"), "context-1m")' },
      hour: { sig: 'hour("时区")', desc: '0–23 点' },
      minute: { sig: 'minute("时区")', desc: '0–59 分' },
      weekday: { sig: 'weekday("时区")', desc: '星期，0 是星期日，1–6 是星期一到六' },
      day: { sig: 'day("时区")', desc: '几号，1–31' },
      month: { sig: 'month("时区")', desc: '几月，1–12' },
      maxMin: { sig: 'max(a, b)  min(a, b)', desc: '取大、取小，可用来设最低收费或封顶' },
      rounding: { sig: 'ceil(x)  floor(x)  abs(x)', desc: '向上取整、向下取整、绝对值' }
    },
    fnsNote: 'param、header、u、tier 和时间函数的参数必须直接写成字符串（如 "Asia/Shanghai"），不能是算出来的值。',
    tiers: {
      wrap: '用 `tier("名称", 费用)` 包住每一档的费用，名称只能用字母、数字和 `_ . -`，同一个表达式里不能重名。',
      len: '按上下文分档就用 `len <= 上限 ? … : …` 连着写，从小到大，最后一档不写条件。',
      any: '不只是 len，任何条件都能分档：请求参数、请求头、时间都可以。',
      record: '命中的档位会写进使用记录，估算里也会显示。'
    },
    rules: {
      intro: '在基础费用后面乘上 `(条件 ? 倍数 : 1)`，几条规则就乘几个：',
      trace: '写成“条件 ? 数字 : 1”、条件里读了请求、用量或时间的，都算一条规则，使用记录里会写明哪条命中、乘了多少。',
      negative: '倍数不能是负数；小于 1 就是打折。',
      nil: '条件读到 nil（如请求里没有这个参数）按不命中算。'
    },
    example: {
      single: { title: '单一价格', desc: '输入每百万 3 美元，输出 15 美元，缓存读 0.3 美元。' },
      longContext: { title: '长上下文两档', desc: '上下文不超过 20 万按标准价，超过后整单按长上下文价。' },
      priority: { title: 'priority 整单两倍', desc: '请求里写了 service_tier=priority 时整单 ×2。' },
      serviceTier: { title: '按应答的服务等级', desc: '用插件报的实际等级计费，比只看请求更准。' },
      night: { title: '夜间五折', desc: '上海时间 0 点到 8 点半价。' },
      weekend: { title: '周末八折', desc: '星期六、星期日打八折。' },
      minimum: { title: '最低收费', desc: '每次至少收 0.001 美元。' },
      flatFee: { title: '按 token 加每次固定费', desc: '每次另收 0.002 美元。' },
      effort: { title: '按推理强度分价', desc: 'reasoning.effort 为 high 时输出价更高。' }
    },
    check: {
      intro: '编辑时会自动检查，结果显示在编辑框下方，有错误时不能保存：',
      syntax: '**语法**：能不能编译，写错的地方会标出行号和列号。',
      fields: '**字段**：函数参数是不是字符串、时区对不对、u() 的字段有没有插件声明、档位有没有重名。',
      smoke: '**试跑**：拿几十组用量（全 0、每个变量单独 1 / 1,000 / 1,000,000、档位边界上下）各算一遍，算不出数或算出负数都算失败。',
      cost: '**金额提醒**：100 万输入 token 一次超过设置的阈值时提醒你确认，不阻止保存。'
    }
  },
  items: {
    p: '输入',
    c: '输出',
    cr: '缓存读',
    cc: '缓存写 5 分钟',
    cc1h: '缓存写 1 小时',
    img: '图片输入',
    img_o: '图片输出',
    flat: '固定费',
    other: '其他',
    subtotal: '小计',
    rules: '条件总倍率',
    base: '基础费用',
    listScale: '模型价格倍数',
    groupRate: '倍率'
  },
  summary: {
    perRequest: '{price} / 次',
    perToken: '输入 {p} · 输出 {c} · 缓存读 {cr} /百万',
    tiers: '共 {n} 档',
    rules: '{n} 条加价规则',
    analysis: '{tiers} 档 · {rules} 条加价规则',
    custom: '自定义表达式'
  },
  local: {
    noTier: '至少需要一个档位',
    tierName: '第 {n} 个档位的名称只能用字母、数字和 _ . -（最多 64 个）',
    tierDup: '档位名称重复："{name}"',
    tooManyTiers: '最多 {n} 个档位',
    tierWhen: '档位"{name}"需要条件（只有最后一档兜底，不设条件）',
    tierRest: '最后一个档位兜底，不能设条件',
    condShape: '档位"{name}"有一个条件不完整或格式不对',
    condValue: '档位"{name}"的条件要填一个数字',
    condTz: '档位"{name}"的条件时区无效',
    condTime: '档位"{name}"的时间条件要填 {lo} 到 {hi} 的整数',
    condEmpty: '档位"{name}"有一个空的条件组',
    condDepth: '档位"{name}"的条件最多嵌套 {n} 层',
    condLeaves: '档位"{name}"最多 {n} 个条件',
    tierPrice: '档位"{name}"需要填写按次价格',
    negative: '档位"{name}"的价格不能为负数',
    ruleField: '第 {n} 条加价规则不完整或有误：倍数不能为负，比较的值不能为空，大小比较要填数字',
    ruleHeader: '第 {n} 条加价规则的请求头名称无效',
    ruleTz: '第 {n} 条加价规则的时区无效',
    emptyExpr: '表达式为空'
  },
  history: {
    title: '表达式历史',
    notFound: '该 hash 下没有保存的表达式。'
  },
  def: {
    perRequestLabel: '每次',
    perMillion: '/百万',
    otherwise: '其余'
  },
  sources: {
    title: '价格同步源',
    description: '从公开价格库或上游 sup2api 实例导入价格。同步由管理员手动触发：先预览差异，勾选要导入的模型，再应用。',
    create: '新建同步源',
    editTitle: '编辑同步源 · {name}',
    kindCol: '类型',
    url: '地址',
    lastSynced: '上次同步',
    lastError: '错误',
    priceCount: '价格数',
    never: '从未',
    preview: '预览同步',
    empty: '还没有同步源。',
    kind: {
      litellm: 'LiteLLM',
      models_dev: 'models.dev',
      sup2api: '上游 sup2api'
    },
    kindHint: {
      litellm: 'LiteLLM 维护的公开价格表（model_prices_and_context_window.json），覆盖主流厂商的模型。',
      models_dev: 'models.dev 公开的模型与价格库，按厂商整理。',
      sup2api: '上游 sup2api 实例：用上游发给我们的 API Key 读取上游价格。'
    },
    urlHint: {
      litellm: '选择类型时已填入默认地址，一般无需修改。',
      models_dev: '选择类型时已填入默认地址，一般无需修改。',
      sup2api: '上游地址，如 https://up.example.com'
    },
    urlInvalid: '请填写以 http:// 或 https:// 开头的地址',
    apiKey: 'API Key',
    apiKeyHint: '上游发给我们的 Key（sk-s2a-...）',
    apiKeyRequired: '上游 sup2api 类型必须填写 API Key',
    apiKeyStored: '已保存（不显示）',
    apiKeyKeep: '已保存 Key，留空表示不修改。',
    apiKeyClear: '清除已保存的 Key',
    hasKey: '已配置 Key',
    providers: '厂商',
    providersHint: {
      litellm: 'LiteLLM 的 litellm_provider 值，如 anthropic、openai、gemini；只导入这些厂商的模型。按回车添加。',
      models_dev: 'models.dev 的厂商 id，如 anthropic、openai、google；只导入这些厂商的模型。按回车添加。'
    },
    providersPlaceholder: '输入厂商后回车',
    applyMultiplier: '按上游倍率导入',
    applyMultiplierHint: '导入时把价格乘以上游 Key 的倍率，也就是按我们实际付给上游的价格导入。',
    multiplierOn: '按上游倍率',
    multiplierOff: '按基础价格',
    deleteConfirm: '删除同步源"{name}"？已从它导入的 {n} 条价格会保留，但不再关联这个同步源。',
    viewPrices: '查看这些价格'
  },
  sync: {
    title: '同步预览 · {name}',
    fetching: '正在从 {name} 拉取价格并对比…',
    fetchFailed: '拉取失败',
    retry: '重试',
    refetch: '重新拉取',
    fetchedAt: '拉取于 {time}，共 {total} 个模型',
    stats: {
      create: '新增',
      update: '更新',
      manual: '手动价格不同',
      unchanged: '未变化',
      skipped: '跳过'
    },
    skippedHint: '同步源里无法转换成价格的条目（例如没有 token 价格）',
    tabs: {
      pending: '有差异',
      create: '新增',
      update: '更新',
      manual: '手动价格不同',
      unchanged: '未变化'
    },
    action: {
      create: '新增',
      update: '更新',
      manual: '手动价格不同',
      unchanged: '未变化'
    },
    searchPlaceholder: '搜索模型…',
    selectAll: '全选（{n}）',
    selectNone: '全不选',
    selectScope: '只作用于当前筛选结果',
    selected: '已选 {n} 个模型',
    selectedManual: '其中 {n} 个会覆盖手动价格',
    incoming: '新价格',
    current: '当前价格',
    noCurrent: '本地没有',
    manualWarn: '会覆盖手动价格',
    unchangedHint: '价格相同，无需导入',
    currentSync: '同步',
    currentManual: '手动',
    currentDisabled: '已停用',
    empty: '没有符合条件的模型。',
    apply: '应用所选（{n}）',
    applyConfirmTitle: '确认导入',
    applyConfirm: '导入 {n} 个模型的价格？',
    applyConfirmManual: '导入 {n} 个模型的价格？其中 {m} 个手动价格会被覆盖成同步价格。',
    resultTitle: '同步结果',
    result: {
      created: '新增',
      updated: '更新',
      unchanged: '未变化',
      skipped: '跳过'
    },
    skippedList: '跳过的模型',
    reason: '原因',
    viewPrices: '查看价格'
  }
}
