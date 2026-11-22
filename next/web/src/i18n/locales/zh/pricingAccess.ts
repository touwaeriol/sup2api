export default {
  segment: '用户组',
  access: '可用',
  rate: '倍率',
  paid: '实付倍率',
  public: '公开',
  private: '私有',
  granted: '已开通',
  notGranted: '未开通',
  grant: {
    segment: '给「{name}」开通这个分组',
    group: '开通「{name}」'
  },
  rateOf: '「{name}」的倍率',
  follow: '跟随',
  source: {
    segment: '单独设的倍率',
    discount: '分组倍率 × 折扣',
    group: '跟随分组倍率'
  },
  discounted: '分组倍率 {group} × 折扣 {discount}',
  unavailable: '用不了',
  unsaved: '未保存',
  remove: '移除',
  groupRate: '分组倍率',
  members: '{n} 人',
  discountOf: '折扣 {rate}',
  overrideNote: '倍率填了就按填的算，不再乘折扣；清空就恢复跟随',
  inGroup: {
    restricted: '只有开通的用户组能用这个分组。',
    public: '所有人都能用这个分组。',
    keptGrants: '公开分组所有人可用；改回私有时这些开通会恢复。',
    nobody: '这个私有分组还没有开通给任何用户组，现在没有人能用。',
    nobodyHint: '用“添加用户组”或“添加用户”来开通。',
    addSegment: '添加用户组',
    addUser: '添加用户',
    addNone: '用户组都已在列表里',
    empty: {
      public: '还没有单独设置：所有人按分组倍率 × 各自的折扣计费。',
      restricted: '还没有开通给任何用户组或用户，现在没有人能用。'
    }
  },
  invalidRate: '「{name}」的倍率填得不对：填 0 到 999999.9999 之间的数字，最多 4 位小数。'
}
