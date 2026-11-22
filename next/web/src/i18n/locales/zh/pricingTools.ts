export default {
  sim: {
    title: '价格试算',
    description: '选一个用户或用户组、一个分组和一个模型，看一次调用按哪个模型价格和倍率计费。',
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
      title: '可用',
      public: '公开分组，可以用',
      grant: '私有分组，已开通',
      none: '私有分组，未开通',
      disabled: '分组已停用'
    },
    denied: {
      none: '用不了这个私有分组，下面是开通后的价格。',
      disabled: '分组停用了，谁都用不了，下面是启用后的价格。'
    },
    price: {
      title: '模型价格',
      hint: '从上往下，第一个设了的生效。价格为每百万 token 的输入 / 输出价。',
      group: '分组价（{group}）',
      global: '原价',
      tiered: '分段计价：单价显示的是兜底档位（最后一档），费用按这次用量所在的档计算。'
    },
    rate: {
      title: '倍率',
      hint: '从上往下，第一个设了的生效。单独设的倍率直接用，不再乘折扣。',
      segment: '单独设的倍率（{segment}）',
      discount: '分组倍率 {base} × 折扣 {discount}',
      group: '分组倍率（{group}）',
      noDiscount: '没有折扣'
    },
    unset: '未设置',
    applies: '生效',
    paid: '实付',
    paidIf: {
      none: '开通后的价格',
      disabled: '启用后的价格'
    },
    unpriced: '这个模型还没有原价，无法计费。先到“模型价格”页设置。',
    listCost: '按模型价格的费用',
    rateName: '倍率'
  }
}
