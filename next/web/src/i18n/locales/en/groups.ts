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
  description: 'A group is a set of accounts (any types mixed) serving together; each API key is bound to one group. The group decides the rate multiplier and the allowed models.',
  create: 'New group',
  editTitle: 'Edit group {name}',
  visibility: 'Visibility',
  public: 'Public',
  restricted: 'Private',
  publicHint: 'Every user can use it.',
  restrictedHint: 'Only the user groups and personal users granted on the "Access & rates" tab can use it.',
  rateMultiplier: 'Default rate',
  multiplierHint: 'Paid = list price × rate, e.g. 0.8 for 20% off. A user group without an own rate pays it times its discount.',
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
  modelPricing: 'Model pricing',
  modelCount: 'Provides {n} model | Provides {n} models',
  pickUser: 'Price one user individually',
  pricingTitle: 'Access & pricing of {name}',
  viewPricing: 'View access & pricing',
  deleteWithPricing: 'Its grants, own rates and model prices are deleted with it.',
  tabs: {
    basic: 'Basics',
    segments: 'Access & rates',
    pricing: 'Model pricing',
    models: 'Model policy',
    accounts: 'Accounts',
    advanced: 'Advanced'
  },
  access: {
    everyone: 'Everyone',
    nobody: 'Granted to nobody yet',
    personal: '{n} personal user | {n} personal users'
  },
  pricingSummary: {
    global: 'Follows global',
    models: '{n} model priced here | {n} models priced here',
    segments: '{n} own price of user groups | {n} own prices of user groups'
  },
  segments: {
    hint: 'Whether each user group can use this group, and at which rate. An empty own rate follows the group\'s default rate × the user group\'s discount; a value replaces both. To set one user apart, search for them under "Price one user individually": they join the table as Personal, after you confirm when they are still in a user group.',
    nobody: 'This private group is granted to nobody yet, so nobody can use it.',
    nobodyHint: 'Turn on a switch under "Access" below to grant it.',
    members: '{n} user | {n} users'
  },
  pricing: {
    target: 'Pricing for',
    groupTarget: 'Group pricing (everyone)',
    namedTargets: 'User groups',
    notGrantedOption: '{name} (not granted)',
    groupScope: 'Group pricing applies to everyone using this group; a model without it follows the global price. Paid = list price × each user\'s rate (group default {rate}).',
    segmentScope: 'Setting the own prices of "{name}": they apply to it only; a model without one follows group pricing.',
    segmentScopeReadonly: 'The own prices of "{name}": they apply to it only; a model without one follows group pricing.',
    segmentRate: 'Its effective rate in this group is {rate}: paid = list price {rate}.',
    segmentNotGranted: 'It is not granted this private group yet, so these prices only matter once it is.',
    emptyNew: 'Save the group and add accounts before pricing its models.',
    unsavedSave: 'Save and switch'
  }
}
