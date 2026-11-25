// Messages shared by every area: order statuses, gateways and buttons.
export default {
  en: {
    status: { pending: 'Pending', paid: 'Paid', recharging: 'Crediting', completed: 'Completed', failed: 'Failed', expired: 'Expired', cancelled: 'Cancelled', refunding: 'Refunding', refunded: 'Refunded', partially_refunded: 'Partially refunded' },
    gateway: { epay: 'Epay', stripe: 'Stripe' },
    action: { save: 'Save', cancel: 'Cancel', confirm: 'Confirm', edit: 'Edit', delete: 'Delete', add: 'Add', copy: 'Copy', copied: 'Copied', refresh: 'Refresh' },
    loadFailed: 'Failed to load',
    saveFailed: 'Failed to save'
  },
  zh: {
    status: { pending: '待支付', paid: '已支付', recharging: '入账中', completed: '已完成', failed: '失败', expired: '已过期', cancelled: '已取消', refunding: '退款中', refunded: '已退款', partially_refunded: '部分退款' },
    gateway: { epay: '易支付', stripe: 'Stripe' },
    action: { save: '保存', cancel: '取消', confirm: '确定', edit: '编辑', delete: '删除', add: '添加', copy: '复制', copied: '已复制', refresh: '刷新' },
    loadFailed: '加载失败',
    saveFailed: '保存失败'
  }
}
