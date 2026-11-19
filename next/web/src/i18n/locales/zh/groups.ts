export default {
  fillWhitelist: '填入分组全部模型（{n}）',
  fillBlacklist: '将分组全部模型加入黑名单（{n}）',
  groupModelSource: '从分组账号配置提取模型 ID，合并去重；填充会保留现有规则。',
  unrestrictedModelsHint: '{n} 个账号未限制模型，无法枚举其全部模型；下方仅列出已明确配置的模型 ID。',
  modelsLoadFailed: '分组模型加载失败，请重新打开编辑界面重试。',

  modelRules: '模型名单',
  whitelist: '白名单',
  blacklist: '黑名单',
  whitelistHint: '仅允许名单内的模型',
  blacklistHint: '禁止名单内的模型，允许其余模型',
  emptyPolicy: '名单为空：不限制模型',

  title: '分组',
  description: '分组是一组一起提供服务的账号（可混放不同类型），每个 API Key 只绑定一个分组。分组决定费率倍率和可用模型。',
  create: '新建分组',
  editTitle: '编辑分组 {name}',
  visibility: '可见性',
  public: '公开',
  restricted: '私有',
  publicHint: '所有用户都能用。',
  restrictedHint: '只有被授权的用户组和个人专属能用，在“授权与倍率”里授权。',
  rateMultiplier: '默认倍率',
  multiplierHint: '实付 = 标价 × 倍率，例如 0.8 表示八折。没有专属倍率的用户组按它乘全场折扣计费。',
  conversionDefault: '默认（允许）',
  conversionHint: '本组账号的请求经协议转换会丢失内容时如何处理；账号自己的设置优先。允许：照常服务并记录丢失；安全：工具会丢失时跳过账号；严格：有任何丢失都跳过账号。',
  multiplierInvalid: '请输入非负数，小于 1000000，最多 4 位小数',
  modelAllowlist: '模型白名单',
  allowlistHint: '留空表示不限。支持通配符，例如 claude-*。',
  allowlistPlaceholder: '输入模型匹配规则后按回车',
  accountCount: '账号数',
  keyCount: 'Key 数',
  deleteWithAccounts: '该分组下的 {a} 个账号只会移出分组，账号本身不会删除。',
  deleteKeys: {
    title: '删除分组 {name}',
    intro: '该分组下还有 {k} 个 API Key。API Key 不会随分组删除，请选择如何处理它们。',
    move: '迁移到其他分组',
    unbind: '解除绑定',
    target: '目标分组',
    moveHint: 'Key 迁移后立即按新分组的倍率和模型规则调用；目标分组不对 Key 的所有者开放时，调用会被拒绝。',
    unbindHint: 'Key 保留但暂时不能调用，请求会返回"API Key 所属分组已被删除"，所有者在 API Key 页面重新选择分组后即可恢复。'
  },
  accounts: {
    title: '账号',
    hint: '同一分组可以混放任意类型的账号，它们一起参与各自类型可服务端点的调度。',
    onlySelected: '只看已选',
    selectVisible: '全选当前列表',
    clearVisible: '取消当前列表',
    none: '没有账号',
    selected: '已选 {n} 个',
    saveFailed: '分组已保存，但以下账号更新失败：{names}',
    notEditable: '没有修改该账号的权限（只能修改属于自己的账号）'
  },
  whoCanUse: '谁能用',
  modelPricing: '模型定价',
  modelCount: '提供 {n} 个模型',
  pickUser: '指定用户单独定价',
  pricingTitle: '{name} 的授权与定价',
  viewPricing: '查看授权与定价',
  deleteWithPricing: '该分组上的授权、专属倍率和模型定价会一并删除。',
  tabs: {
    basic: '基本信息',
    segments: '授权与倍率',
    pricing: '模型定价',
    models: '模型名单',
    accounts: '账号',
    advanced: '高级'
  },
  access: {
    everyone: '所有人',
    nobody: '还没授权给任何人',
    personal: '{n} 位个人专属'
  },
  pricingSummary: {
    global: '跟随全局',
    models: '{n} 个模型单独定价',
    segments: '{n} 条用户组专属价'
  },
  segments: {
    hint: '每个用户组在这个分组能不能用、按几倍计费。专属倍率留空就跟随分组默认倍率 × 该用户组的全场折扣；填了就按填的算，不再乘折扣。要给某个用户单独设置，用“指定用户单独定价”搜索他：他会作为个人专属加进表格，还在用户组里的会先让你确认。',
    nobody: '这个私有分组还没有授权给任何人，现在没有人能用。',
    nobodyHint: '打开下面“能否使用”的开关来授权。',
    members: '{n} 人'
  },
  pricing: {
    target: '给谁定价',
    groupTarget: '分组内定价（所有人）',
    namedTargets: '用户组',
    notGrantedOption: '{name}（未授权）',
    groupScope: '分组内定价对所有使用这个分组的人生效，没单独定价的模型跟随全局价。实付 = 标价 × 各自的倍率（分组默认 {rate}）。',
    segmentScope: '正在给「{name}」定专属价：只对它生效，没设的模型跟随分组内定价。',
    segmentScopeReadonly: '「{name}」的专属价：只对它生效，没设的模型跟随分组内定价。',
    segmentRate: '它在本分组的实际倍率是 {rate}，实付 = 标价 {rate}。',
    segmentNotGranted: '它还没被授权使用这个私有分组，专属价要等授权后才用得上。',
    emptyNew: '先保存分组并加入账号后再定价。',
    unsavedSave: '保存并切换'
  }
}
