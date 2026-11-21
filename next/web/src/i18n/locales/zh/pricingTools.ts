export default {
  segment: {
    placeholder: '选择用户组',
    default: '新用户默认',
    discount: '全场 {rate}'
  },
  convert: {
    title: '把 {name} 设为个人专属',
    intro: '设为个人专属后，他不再属于「{from}」，之后「{from}」的改动不再影响他。',
    currentSegment: '原用户组',
    startFrom: '个人专属的设置从哪里开始',
    copy: '复制「{from}」现在的设置',
    copyHint: '授权、专属倍率、专属价、全场折扣都照抄一份，再单独改。',
    blank: '从空白开始',
    blankHint: '只能用公开分组，按分组默认倍率和默认价格。',
    submit: '设为个人专属',
    done: '已把 {name} 设为个人专属'
  },
  remove: {
    title: '把 {name} 移回用户组',
    target: '移到哪个用户组',
    warning: '他的个人专属设置（授权、专属倍率、专属价）会被删除，之后按所选用户组计费。',
    submit: '移回并删除个人专属设置',
    done: '已把 {name} 移到「{segment}」'
  },
  sim: {
    title: '价格试算',
    description: '选一个用户或用户组、一个分组和一个模型，看一次调用按哪一层的标价和倍率计费。',
    who: '对象',
    subject: {
      user: '用户',
      segment: '用户组'
    },
    modelPlaceholder: '选择模型',
    modelHint: '只列这个分组提供的模型。',
    inputTokens: '输入 tokens',
    outputTokens: '输出 tokens',
    tokensInvalid: '填 0 到 {max} 之间的整数',
    usageInvalid: '用量填写有误，改正后自动计算。',
    incomplete: '选好对象、分组和模型后自动计算。',
    unprovided: '还没有分组提供 {model}，无法试算。'
  },
  quote: {
    access: {
      title: '能否使用',
      public: '公开分组，可以使用',
      grant: '私有分组，已授权',
      none: '私有分组，未授权'
    },
    denied: '用不了这个私有分组，下面是授权后的价格。',
    price: {
      title: '标价',
      hint: '从上往下，第一层设置了的生效。价格为每百万 token 的输入 / 输出价。',
      segment: '专属价（{segment}）',
      group: '分组内定价（{group}）',
      global: '全局价',
      tiered: '分段计价：单价显示的是兜底档位（最后一档），费用按这次用量所在的档计算。'
    },
    rate: {
      title: '倍率',
      hint: '从上往下，第一层设置了的生效。专属倍率直接替换，不再乘全场折扣。',
      segment: '专属倍率（{segment}）',
      discount: '分组默认 {base} × 全场折扣 {discount}',
      group: '分组默认倍率（{group}）',
      noDiscount: '没有全场折扣'
    },
    unset: '未设置',
    applies: '生效',
    paid: '实付',
    paidIfGranted: '授权后的价格',
    unpriced: '这个模型还没有全局价，无法计费。请先到“模型价格”里设置。',
    listCost: '标价费用',
    rateName: '倍率'
  }
}
