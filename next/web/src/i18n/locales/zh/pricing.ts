export default {
  segment: {
    personal: '个人',
    personalOf: '个人 · {name}'
  },
  exclusive: '专属',
  rate: {
    label: '倍率',
    follow: '跟随 {rate}',
    invalid: '填 0 到 999999.9999 之间的数字，最多 4 位小数'
  },
  rule: {
    mode: '定价方式',
    kind: {
      none: '用原价',
      multiplier: '原价 × N',
      custom: '自定义价格'
    },
    multiplierText: '原价 × {rate}',
    customText: '自定义',
    timesPrefix: '原价 ×',
    multiplierHint: '原价变了跟着变',
    customHint: '和“模型价格”页同一个编辑器：可以分档、按次或写表达式。原价变了不跟着变。',
    preview: '模型价格 {price}，乘倍率 {rate} 后实付 {paid}',
    multiplierInvalid: '请输入不小于 0、小于 1000000 的数字，最多 4 位小数',
    noBase: '这个模型还没有原价，先到“模型价格”页设置。'
  },
  price: {
    perRequest: '{price} / 次',
    expression: '表达式计费',
    expressionTimes: '表达式计费 {rate}',
    none: '未定价',
    tiered: '{n} 档，显示兜底档位'
  },
  source: {
    group: '分组价',
    segment: '用户组价（已停用的功能）'
  },
  table: {
    title: '模型价格',
    count: '{n} 个模型',
    ruled: '{n} 个改过价',
    base: '原价',
    group: '分组价',
    paid: '实付（{rate}）',
    useBase: '用原价',
    notOffered: '账号暂未提供',
    notOfferedHint: '分组里的账号还没有这个模型，价格先存着',
    editing: '{model} 在本分组的价格',
    set: '改价',
    done: '完成',
    clear: '清除',
    clearHint: '“清除”= 恢复用原价',
    add: '添加模型',
    addNone: '没有可添加的模型',
    addFooter: '只能添加在“模型价格”页定了原价的模型',
    unsaved: '未保存',
    unsavedCount: '{n} 项改动未保存',
    search: '搜索模型',
    onlyRuled: '只看改过价的',
    unit: '单位：$ / 百万 token（输入 / 输出）；按次计费的模型是每次价格。',
    emptyNew: '在“账号”里加入账号后，账号提供的模型会自动列在这里；也可以先“添加模型”定价。',
    noAccounts: '在“账号”里加入账号后，账号提供的模型会自动列在这里。',
    emptyModels: '分组还没有提供模型：模型来自分组里账号的模型，经“模型名单”过滤。可以先“添加模型”定价。',
    emptyFiltered: '没有符合条件的模型',
    invalid: '{model} 的价格没有填完整，请先改正。'
  }
}
