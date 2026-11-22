export default {
  title: '用户组',
  description: '每个用户只在一个用户组里。用户组决定能用哪些私有分组、按几倍付费。',
  create: '新建用户组',
  created: '已新建用户组',
  unsavedNew: '还没保存：点“新建”时用户组、分组倍率和成员一起保存',
  editTitle: '编辑用户组 {name}',
  viewTitle: '用户组 {name}',
  searchPlaceholder: '搜索名称、说明或用户邮箱',
  default: '新用户默认',
  defaultHint: '新用户自动加入这个用户组，它不能删除。',
  defaultName: '默认用户组不能改名。',
  note: '说明',
  publicOnly: '只有公开分组',
  simulate: '价格试算',
  deleteTitle: '删除用户组「{name}」',
  deleteConfirm: '{n} 位成员将回到默认用户组，这个用户组的开通和倍率一并删除。此操作无法撤销。',
  kinds: {
    label: '按类型筛选',
    all: '全部 {n}',
    named: '用户组 {n}',
    personal: '个人 {n}'
  },
  columns: {
    name: '名称',
    members: '成员',
    discount: '折扣',
    granted: '可用的私有分组',
    overrides: '单独倍率'
  },
  tabs: {
    basic: '基本信息',
    groups: '分组倍率',
    members: '成员',
    membersPicked: '成员 · 已选 {n} 人'
  },
  discount: {
    label: '折扣',
    hint: '乘在每个分组的分组倍率上；单独设了倍率的分组不打折。填 1 就是不打折。',
    example: '例如：分组倍率 {group} → 实付倍率 {rate}',
    invalid: '填大于 0、小于 1000000 的数字，最多 4 位小数；不打折填 1。'
  },
  groups: {
    discount: '折扣',
    discountHint: '（在“基本信息”里改）：没单独设倍率的分组按 分组倍率 × {discount} 算',
    noDiscount: '没有折扣（在“基本信息”里改）：没单独设倍率的分组按分组倍率算'
  },
  members: {
    notice: '添加和移出成员立即生效，不用点“保存”。',
    noticeNew: '选好的成员在点“新建”时一起加入。',
    add: '添加成员',
    addHint: '每个用户只在一个用户组里：加入「{segment}」就是把他从原来的用户组移过来。',
    addPlaceholder: '按邮箱或昵称搜索要加入的用户',
    search: '搜索成员',
    inUsers: '在用户列表中查看',
    empty: '这个用户组还没有成员',
    emptyNew: '还没选成员',
    thisSegment: '这个用户组',
    already: '{name} 已经在这个用户组里',
    dropPersonal: '{name} 现在是个人。加入「{segment}」后，他单独设的开通和倍率会被删除，之后按「{segment}」付费。',
    dropPersonalConfirm: '加入并删除单独设置',
    movedFrom: '已把 {name} 从「{from}」移到「{to}」',
    remove: '移出',
    removeConfirm: '把 {name} 移出「{segment}」？他会回到「{to}」，之后按「{to}」付费。',
    movedTo: '已把 {name} 移到「{to}」',
    defaultHint: '默认用户组的成员不能直接移出：到目标用户组里添加他即可。'
  },
  personal: {
    origin: '原来在 {name}',
    remove: '移回用户组',
    notice: '下面的设置只对他一人生效。',
    noticeOrigin: '下面的设置只对他一人生效。他原来在「{origin}」，之后「{origin}」的改动不再影响他。'
  },
  select: {
    placeholder: '选择用户组',
    default: '新用户默认',
    discount: '折扣 {rate}'
  },
  convert: {
    pick: '给某个用户单独设倍率',
    pickShort: '给用户单独设倍率',
    pickHint: '他会离开原来的用户组，成为“个人”；已经是个人的直接打开他的设置。',
    pickPlaceholder: '按邮箱或昵称搜索用户',
    title: '给 {name} 单独设倍率',
    intro: '他会成为“个人”，不再属于「{from}」，之后「{from}」的改动不再影响他。',
    currentSegment: '原用户组',
    startFrom: '从哪里开始',
    copy: '复制「{from}」现在的设置',
    copyHint: '折扣、开通的私有分组和各分组的倍率照抄一份，之后单独改。',
    blank: '从空白开始',
    blankHint: '只能用公开分组，按分组倍率付费。',
    submit: '单独设倍率',
    done: '{name} 现在是个人'
  },
  remove: {
    title: '把 {name} 移回用户组',
    target: '移到哪个用户组',
    warning: '他单独设的开通和倍率会被删除，之后按所选用户组付费。',
    submit: '移回并删除单独设置',
    done: '已把 {name} 移到「{segment}」'
  }
}
