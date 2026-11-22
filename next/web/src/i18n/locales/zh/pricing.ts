export default {
  segment: {
    personal: '专享',
    personalOf: '专享 · {name}'
  },
  exclusive: '专属',
  draft: {
    unsaved: '未保存',
    refused: '未通过',
    removed: '将移除'
  },
  rate: {
    label: '倍率',
    follow: '跟随 {rate}',
    invalid: '填 0 到 999999.9999 之间的数字，最多 4 位小数'
  },
  editor: {
    mode: '定价方式',
    kind: {
      none: '跟随原价',
      custom: '自定义价格'
    },
    customText: '自定义',
    customHint: '和“模型价格”页同一个编辑器：可以分档、按次或写表达式。原价变了不跟着变；要按比例算，请用“模型倍率”。',
    preview: '模型价格 {price}，乘倍率 {rate} 后实付 {paid}',
    noBase: '这个模型还没有原价，先到“模型价格”页设置。'
  },
  price: {
    perRequest: '{price} / 次',
    expression: '表达式计费',
    expressionTimes: '表达式计费 {rate}',
    none: '未定价',
    tiered: '{n} 档，显示兜底档位'
  },
  table: {
    title: '模型价格',
    count: '{n} 个模型',
    base: '原价',
    price: '模型价格',
    rate: '模型倍率',
    paid: '实付',
    followBase: '跟随原价',
    followRate: '跟随基本倍率',
    rateOf: '{model} 的模型倍率',
    rateSource: {
      group_model: '模型倍率',
      group: '跟随基本倍率'
    },
    notOffered: '账号暂未提供',
    notOfferedHint: '账号分组里的账号还没有这个模型，设置先存着',
    editing: '{model} 在本账号分组的模型价格',
    set: '{n} 个单独设置',
    change: '改价',
    done: '完成',
    clear: '清除',
    clearHint: '“清除”= 模型价格跟随原价、模型倍率跟随基本倍率',
    add: '添加模型',
    addNone: '没有可添加的模型',
    addFooter: '只能添加在“模型价格”页定了原价的模型',
    unsavedCount: '{n} 项改动未保存',
    search: '搜索模型',
    onlySet: '只看单独设置的',
    unit: '单位：$ / 百万 token（输入 / 输出）；按次计费的模型是每次价格。',
    emptyNew: '新账号分组还没有账号：在账号编辑里把账号加入这个账号分组后，账号提供的模型会自动列在这里；也可以先“添加模型”定价。',
    noAccounts: '在账号编辑里把账号加入这个账号分组后，账号提供的模型会自动列在这里。',
    emptyModels: '账号分组还没有提供模型：模型来自账号分组里账号的模型，经“模型名单”过滤。可以先“添加模型”定价。',
    emptyFiltered: '没有符合条件的模型',
    invalid: '{model} 的模型价格或模型倍率填得不对，请先改正。'
  }
}
