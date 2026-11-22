export default {
  fillWhitelist: '填入账号分组全部模型（{n}）',
  fillBlacklist: '将账号分组全部模型加入黑名单（{n}）',
  groupModelSource: '从账号分组里账号的配置提取模型 ID，合并去重；填充会保留现有规则。',
  unrestrictedModelsHint: '{n} 个账号未限制模型，无法枚举其全部模型；下方仅列出已明确配置的模型 ID。',
  modelsLoadFailed: '账号分组模型加载失败，请重新打开编辑界面重试。',

  modelRules: '模型名单',
  whitelist: '白名单',
  blacklist: '黑名单',
  whitelistHint: '仅允许名单内的模型',
  blacklistHint: '禁止名单内的模型，允许其余模型',
  emptyPolicy: '名单为空：不限制模型',

  title: '账号分组',
  description: '账号分组是一组一起提供服务的账号（可混放不同类型），每个 API Key 只绑定一个账号分组。账号分组决定可用模型、模型价格和倍率。',
  create: '新建账号分组',
  editTitle: '编辑账号分组 {name}',
  visibility: '可见性',
  public: '公开',
  restricted: '私有',
  publicHint: '所有用户都能用。',
  restrictedHint: '只有开通的用户分组和专享用户能用，在“用户分组倍率”标签里开通。',
  rateMultiplier: '基本倍率',
  multiplierHint: '0.8 就是八折。模型和用户分组都没单独设倍率时按它算；只有一个倍率生效，不会相乘。',
  saveHint: '各标签的改动点“保存”后一次提交、一起生效',
  createHint: '还没保存：各标签都可以先填，点“新建”时一次提交',
  conversionDefault: '默认（允许）',
  conversionHint: '本组账号的请求经协议转换会丢失内容时如何处理；账号自己的设置优先。允许：照常服务并记录丢失；安全：工具会丢失时跳过账号；严格：有任何丢失都跳过账号。',
  modelAllowlist: '模型白名单',
  allowlistHint: '留空表示不限。支持通配符，例如 claude-*。',
  allowlistPlaceholder: '输入模型匹配规则后按回车',
  accountCount: '账号数',
  keyCount: 'Key 数',
  deleteWithAccounts: '该账号分组下的 {a} 个账号只会移出账号分组，账号本身不会删除。',
  deleteKeys: {
    title: '删除账号分组 {name}',
    intro: '该账号分组下还有 {k} 个 API Key。API Key 不会随账号分组删除，请选择如何处理它们。',
    move: '迁移到其他账号分组',
    unbind: '解除绑定',
    target: '目标账号分组',
    moveHint: 'Key 迁移后立即按新账号分组的倍率和模型规则调用；目标账号分组不对 Key 的所有者开放时，调用会被拒绝。',
    unbindHint: 'Key 保留但暂时不能调用，请求会返回"API Key 所属账号分组已被删除"，所有者在 API Key 页面重新选择账号分组后即可恢复。'
  },
  whoCanUse: '谁能用',
  modelPricing: '模型价格',
  modelCount: '提供 {n} 个模型',
  pricingTitle: '{name} 的价格',
  viewPricing: '查看价格',
  deleteWithPricing: '该账号分组的模型价格、模型倍率，以及各用户分组在它上面的开通、倍率和模型倍率，会一并删除。',
  tabs: {
    basic: '基本信息',
    models: '模型名单',
    pricing: '价格',
    segments: '用户分组倍率',
    advanced: '高级'
  },
  access: {
    everyone: '所有人',
    nobody: '还没开通给任何人',
    personal: '{n} 位专享用户'
  },
  pricingSummary: {
    base: '全部用原价',
    models: '{n} 个模型单独设置'
  }
}
