export default {
  segment: '用户分组',
  access: '可用',
  rate: '倍率',
  paid: '实付倍率',
  public: '公开',
  private: '私有',
  granted: '已开通',
  notGranted: '未开通',
  grant: {
    segment: '给「{name}」开通这个账号分组',
    group: '开通「{name}」'
  },
  rateOf: '「{name}」的倍率',
  follow: '跟随',
  source: {
    segment_model: '用户分组的模型倍率',
    segment: '用户分组设置',
    group_model: '账号分组的模型倍率',
    group: '跟随账号分组'
  },
  unavailable: '用不了',
  unsaved: '未保存',
  refused: '未通过',
  remove: '移除',
  groupRate: '基本倍率',
  baseRate: '基本倍率',
  members: '{n} 人',
  overrideNote: '倍率留空 = 跟随账号分组的基本倍率；只有一个倍率生效，不会相乘。',
  inGroup: {
    restricted: '只有开通的用户分组能用这个账号分组。',
    public: '所有人都能用这个账号分组。',
    keptGrants: '公开账号分组所有人可用；改回私有时这些开通会恢复。',
    nobody: '这个私有账号分组还没有开通给任何用户分组，现在没有人能用。',
    nobodyHint: '用“添加用户分组”或“添加用户”来开通。',
    addSegment: '添加用户分组',
    addUser: '添加用户',
    addNone: '用户分组都已在列表里',
    empty: {
      public: '还没有单独设置：所有人按基本倍率计费。',
      restricted: '还没有开通给任何用户分组或用户，现在没有人能用。'
    }
  },
  invalidRate: '「{name}」的倍率填得不对：填 0 到 999999.9999 之间的数字，最多 4 位小数。'
}
