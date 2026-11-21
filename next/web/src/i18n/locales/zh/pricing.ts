export default {
  segment: {
    personal: '个人专属',
    personalOf: '个人专属 · {name}'
  },
  exclusive: '专属',
  rate: {
    label: '专属倍率',
    follow: '跟随 {rate}',
    groupDefault: '分组默认 {group}',
    discounted: '分组默认 {group} × 全场折扣 {discount}',
    override: '专属倍率，不再跟随 {rate}',
    invalid: '请输入不小于 0、小于 1000000 的数字，最多 4 位小数'
  },
  rule: {
    mode: '定价方式',
    multiplier: '按倍数',
    custom: '自定义价格',
    multiplierText: '全局 {rate}',
    customText: '自定义 {price}',
    applied: '套用后 {price}，全局价变化时跟着变',
    appliedOpaque: '各项价格同乘该倍数，全局价变化时跟着变',
    multiplierInvalid: '请输入不小于 0、小于 1000000 的数字，最多 4 位小数',
    tokenUnit: '固定价格，不随全局价变化。单位：$ / 1M tokens',
    requestUnit: '固定价格，不随全局价变化。单位：$ / 次',
    tokenInvalid: '输入、输出价格必填，各项都不能为负数',
    requestInvalid: '请填写不小于 0 的单价',
    multiplierOnly: '该模型的全局价不是只有一档、没有加价规则的可视化价格（按表达式计费，或分了多档、带加价规则），只能按倍数调整。',
    noBase: '该模型还没有全局价，请先到“模型价格”里设置。',
    requestPrice: '单价'
  },
  price: {
    perRequest: '{price} / 次',
    expression: '表达式计费',
    expressionTimes: '表达式计费 {rate}',
    none: '未定价',
    tiered: '{n} 档，显示兜底档位'
  },
  source: {
    segment: '专属价',
    group: '分组内定价',
    global: '全局'
  },
  table: {
    base: '全局价',
    groupRule: '分组内定价',
    segmentRule: '专属价',
    listPrice: '标价',
    followGlobal: '跟随全局',
    followGroup: '跟随分组内定价',
    segmentRules: '另有 {n} 个用户组有专属价',
    set: '设置',
    done: '完成',
    clear: '清除',
    unsaved: '未保存',
    unsavedCount: '{n} 项改动未保存',
    search: '搜索模型',
    onlyRuled: '只看已单独定价',
    unit: '单位：$ / 1M tokens（输入 / 输出）；按次计费的模型为每次价格。',
    emptyNew: '保存分组并加入账号后，这里会列出分组提供的模型。',
    emptyModels: '该分组还没有可提供的模型。模型来自分组内账号声明的模型，并经分组模型名单过滤。',
    emptyFiltered: '没有符合条件的模型',
    invalid: '{model} 的定价没有填完整，请先改正。'
  }
}
