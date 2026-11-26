// Redeem codes (core, Finance > Redeem codes; Mine > Wallet redeem card).
export default {
  title: '兑换码',
  description: '批量生成兑换码发给用户，用户在钱包里兑换后直接加余额。明文只在生成时显示一次。',
  empty: '暂无兑换码',
  generate: '批量生成',
  anyStatus: '全部状态',
  never: '永不过期',
  void: '作废',
  voidTitle: '作废兑换码？',
  voidConfirm: '{code}（面额 {value}）作废后不能再兑换，此操作不能撤销。',
  voided: '兑换码已作废',
  status: { unused: '未使用', used: '已使用', voided: '已作废', expired: '已过期' },
  cols: { code: '兑换码', value: '面额', status: '状态', used_by: '使用者', used_at: '使用时间', expires_at: '过期时间', notes: '备注', created_at: '创建时间' },
  form: {
    title: '批量生成兑换码',
    submit: '生成',
    count: '数量',
    countHint: '一次 1–1000 个',
    value: '面额',
    valueHint: '兑换后给用户入账的余额，不超过单次入账上限',
    expires: '过期时间',
    expiresHint: '留空表示永不过期',
    future: '过期时间要晚于现在',
    positive: '必须大于 0',
    notes: '备注',
    notesPlaceholder: '如：双十一活动'
  },
  result: {
    title: '已生成 {n} 个兑换码',
    warn: '关闭后不再显示明文，请先复制或下载保存。',
    copyAll: '复制全部',
    download: '下载 txt',
    done: '我已保存'
  },
  card: {
    title: '兑换码',
    subtitle: '使用兑换码直接为余额充值',
    placeholder: '输入兑换码',
    submit: '兑换',
    required: '请输入兑换码',
    success: '已入账 {amount}',
    failed: '兑换失败'
  }
}
