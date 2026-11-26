export default {
  title: '余额流水',
  description: '所有用户的每一笔余额变动。',
  balance: '余额',
  negativeHint: '余额为负，新的请求会被拒绝，请先充值。',
  operator: '管理员 #{id}',
  cols: {
    kind: '类型',
    delta: '变动',
    balance: '余额',
    note: '说明'
  },
  kinds: {
    usage: '使用扣费',
    admin_adjust: '管理员调整',
    plugin_credit: '插件入账',
    plugin_debit: '插件扣减',
    refund: '退款',
    redeem: '兑换码'
  },
  adjust: {
    currentBalance: '当前余额',
    button: '调整余额',
    title: '调整余额',
    userRequired: '请选择用户',
    direction: '方向',
    credit: '入账（增加）',
    debit: '扣减（减少）',
    amount: '金额',
    amountInvalid: '请输入正数，最多 8 位小数',
    notePlaceholder: '如：充值',
    done: '余额已调整',
    doneBalance: '余额已调整，当前余额 {balance}',
    duplicate: '该调整已经执行过'
  }
}
