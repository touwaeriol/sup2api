export default {
  title: '用户分组',
  description: '每个用户只在一个用户分组里。用户分组决定能用哪些私有账号分组、按几倍付费。',
  create: '新建用户分组',
  created: '已新建用户分组',
  unsavedNew: '还没保存：各标签都可以先填，点“新建”时一次提交。成员在用户页里加入。',
  editTitle: '编辑用户分组 {name}',
  viewTitle: '用户分组 {name}',
  searchPlaceholder: '搜索名称、说明或用户邮箱',
  default: '新用户默认',
  defaultHint: '新用户自动加入这个用户分组，它不能删除。',
  defaultName: '默认用户分组不能改名。',
  note: '说明',
  publicOnly: '只有公开账号分组',
  simulate: '价格试算',
  deleteTitle: '删除用户分组「{name}」',
  deleteConfirm: '{n} 位成员将回到默认用户分组，这个用户分组的开通、倍率和模型倍率一并删除。此操作无法撤销。',
  kinds: {
    label: '按类型筛选',
    all: '全部 {n}',
    named: '用户分组 {n}',
    personal: '专享 {n}'
  },
  columns: {
    name: '名称',
    members: '成员',
    granted: '可用的私有账号分组',
    overrides: '单独倍率'
  },
  tabs: {
    basic: '基本信息',
    groups: '账号分组倍率'
  },
  modelRates: {
    toggle: '模型倍率（{n}）',
    intro: '单独设了倍率的模型按 原价 × 模型倍率 计费；其他模型按这一行的倍率 {rate}。',
    add: '添加模型',
    addNone: '没有可添加的模型',
    addFooter: '只列这个账号分组里有原价的模型',
    empty: '还没有单独设倍率的模型',
    rateOf: '{model} 的模型倍率',
    undo: '撤销',
    invalid: '「{group}」里 {model} 的模型倍率填得不对：填 0 到 999999.9999 之间的数字，最多 4 位小数。'
  },
  personal: {
    origin: '原来在 {name}',
    remove: '移回用户分组',
    notice: '下面的设置只对他一人生效。',
    noticeOrigin: '下面的设置只对他一人生效。他原来在「{origin}」，之后「{origin}」的改动不再影响他。'
  },
  select: {
    placeholder: '选择用户分组',
    default: '新用户默认'
  },
  convert: {
    pick: '给某个用户单独设倍率',
    pickShort: '给用户单独设倍率',
    pickHint: '他会离开原来的用户分组，成为“专享”；已经是专享的直接打开他的设置。',
    pickPlaceholder: '按邮箱或昵称搜索用户',
    title: '给 {name} 单独设倍率',
    intro: '他会成为“专享”，不再属于「{from}」，之后「{from}」的改动不再影响他。',
    currentSegment: '原用户分组',
    startFrom: '从哪里开始',
    copy: '复制「{from}」现在的设置',
    copyHint: '开通的私有账号分组、各账号分组的倍率和模型倍率照抄一份，之后单独改。',
    blank: '从空白开始',
    blankHint: '只能用公开账号分组，按账号分组倍率付费。',
    submit: '单独设倍率',
    done: '{name} 现在是专享'
  },
  remove: {
    title: '把 {name} 移回用户分组',
    target: '移到哪个用户分组',
    warning: '他单独设的开通和倍率会被删除，之后按所选用户分组付费。',
    submit: '移回并删除单独设置',
    done: '已把 {name} 移到「{segment}」'
  }
}
