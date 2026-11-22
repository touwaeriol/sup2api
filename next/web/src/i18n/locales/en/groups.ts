export default {
  fillWhitelist: 'Fill group models ({n})',
  fillBlacklist: 'Block all group models ({n})',
  groupModelSource: 'Model IDs from group accounts, deduplicated. Filling preserves existing rules.',
  unrestrictedModelsHint: '{n} accounts have unrestricted models and cannot be fully enumerated. Only explicitly configured IDs are listed.',
  modelsLoadFailed: 'Failed to load group models. Reopen the editor to retry.',

  modelRules: 'Model policy',
  whitelist: 'Whitelist',
  blacklist: 'Blacklist',
  whitelistHint: 'Allow only matching models',
  blacklistHint: 'Block matching models; allow the rest',
  emptyPolicy: 'Empty list: all models allowed',

  title: 'Groups',
  description: 'A group is a set of accounts (any types mixed) serving together; each API key is bound to one group. The group decides the allowed models, their prices and the group rate.',
  create: 'New group',
  editTitle: 'Edit group {name}',
  visibility: 'Visibility',
  public: 'Public',
  restricted: 'Private',
  publicHint: 'Every user can use it.',
  restrictedHint: 'Only the user groups and exclusive users it is opened to on the "User groups" tab can use it.',
  rateMultiplier: 'Group rate',
  multiplierHint: '0.8 is 20% off. A user group without a rate of its own pays this (times its discount).',
  saveHint: 'Changes take effect together on Save',
  createHint: 'Not saved yet: everything is saved on Create',
  conversionDefault: 'Default (allow)',
  conversionHint: 'What happens when a converted request of this group\'s accounts would lose content; an account\'s own setting wins. Allow: serve it and record the losses; Safe: skip the account when tools would be lost; Strict: skip it on any loss.',
  multiplierInvalid: 'Enter a non-negative number below 1000000, with up to 4 decimals',
  modelAllowlist: 'Model allowlist',
  allowlistHint: 'Empty = unlimited. Wildcards such as claude-* are supported.',
  allowlistPlaceholder: 'Type a model pattern and press Enter',
  accountCount: 'Accounts',
  keyCount: 'Keys',
  deleteWithAccounts: 'Its {a} accounts only leave the group; they are not deleted.',
  deleteKeys: {
    title: 'Delete group {name}',
    intro: 'This group still has {k} API keys. They are not deleted with the group; choose what happens to them.',
    move: 'Move them to another group',
    unbind: 'Unbind them',
    target: 'Target group',
    moveHint: 'Moved keys use the new group\'s multiplier and model rules right away; if the group is not open to a key\'s owner, its calls are refused.',
    unbindHint: 'The keys are kept but cannot call until their owners pick another group on the API keys page; meanwhile calls answer "the group of this API key has been deleted".'
  },
  accounts: {
    title: 'Accounts',
    hint: 'Accounts of any type can be mixed in one group; they are scheduled together for every endpoint their types can serve.',
    onlySelected: 'Selected only',
    selectVisible: 'Select shown',
    clearVisible: 'Clear shown',
    none: 'No accounts',
    selected: '{n} selected',
    saveFailed: 'Group saved, but these accounts could not be updated: {names}',
    notEditable: 'You cannot edit this account (only accounts you own)'
  },
  whoCanUse: 'Who can use it',
  modelPricing: 'Model prices',
  modelCount: 'Provides {n} model | Provides {n} models',
  pricingTitle: 'Prices & user groups of {name}',
  viewPricing: 'View prices & user groups',
  deleteWithPricing: 'Its model prices, and who it is open to at which rate, are deleted with it.',
  tabs: {
    basic: 'Basics',
    accounts: 'Accounts',
    models: 'Model policy',
    pricing: 'Prices',
    segments: 'User groups',
    advanced: 'Advanced'
  },
  access: {
    everyone: 'Everyone',
    nobody: 'Open to nobody yet',
    personal: '{n} exclusive user | {n} exclusive users'
  },
  pricingSummary: {
    base: 'All at original prices',
    models: '{n} model repriced | {n} models repriced'
  },
  pricing: {
    payer: 'Paid at',
    payerAll: 'Everyone (group rate) {rate}',
    payerSegments: 'User groups',
    payerPersonal: 'Exclusive',
    notAccessible: 'Not open'
  }
}
