export default {
  fillWhitelist: 'Fill account group models ({n})',
  fillBlacklist: 'Block all account group models ({n})',
  groupModelSource: 'Model IDs from account group accounts, deduplicated. Filling preserves existing rules.',
  unrestrictedModelsHint: '{n} accounts have unrestricted models and cannot be fully enumerated. Only explicitly configured IDs are listed.',
  modelsLoadFailed: 'Failed to load account group models. Reopen the editor to retry.',

  modelRules: 'Model policy',
  whitelist: 'Whitelist',
  blacklist: 'Blacklist',
  whitelistHint: 'Allow only matching models',
  blacklistHint: 'Block matching models; allow the rest',
  emptyPolicy: 'Empty list: all models allowed',

  title: 'Account groups',
  description: 'An account group is a set of accounts (any types mixed) serving together; each API key is bound to one account group. The account group decides the allowed models, their prices and rates.',
  create: 'New account group',
  editTitle: 'Edit account group {name}',
  visibility: 'Visibility',
  public: 'Public',
  restricted: 'Private',
  publicHint: 'Every user can use it.',
  restrictedHint: 'Only the user groups and exclusive users it is granted to can use it; grant it under the "User group rates" tab.',
  rateMultiplier: 'Base rate',
  multiplierHint: '0.8 is 20% off. Applies where neither the model nor the user group has a rate of its own; only one rate applies, rates are never multiplied.',
  saveHint: 'The changes of every tab are sent together on Save',
  createHint: 'Not saved yet: fill in any tab, everything is sent together on Create',
  conversionDefault: 'Default (allow)',
  conversionHint: 'What happens when a converted request of this account group\'s accounts would lose content; an account\'s own setting wins. Allow: serve it and record the losses; Safe: skip the account when tools would be lost; Strict: skip it on any loss.',
  modelAllowlist: 'Model allowlist',
  allowlistHint: 'Empty = unlimited. Wildcards such as claude-* are supported.',
  allowlistPlaceholder: 'Type a model pattern and press Enter',
  accountCount: 'Accounts',
  keyCount: 'Keys',
  deleteWithAccounts: 'Its {a} accounts only leave the account group; they are not deleted.',
  deleteKeys: {
    title: 'Delete account group {name}',
    intro: 'This account group still has {k} API keys. They are not deleted with the account group; choose what happens to them.',
    move: 'Move them to another account group',
    unbind: 'Unbind them',
    target: 'Target account group',
    moveHint: 'Moved keys use the new account group\'s rates and model rules right away; if the account group is not open to a key\'s owner, its calls are refused.',
    unbindHint: 'The keys are kept but cannot call until their owners pick another account group on the API keys page; meanwhile calls answer "the account group of this API key has been deleted".'
  },
  whoCanUse: 'Who can use it',
  modelPricing: 'Model prices',
  modelCount: 'Provides {n} model | Provides {n} models',
  pricingTitle: 'Prices of {name}',
  viewPricing: 'View prices',
  deleteWithPricing: 'Its model prices and model rates, and who it is granted to at which rates, are deleted with it.',
  tabs: {
    basic: 'Basics',
    models: 'Model policy',
    pricing: 'Prices',
    segments: 'User group rates',
    advanced: 'Advanced'
  },
  access: {
    everyone: 'Everyone',
    nobody: 'Open to nobody yet',
    personal: '{n} exclusive user | {n} exclusive users'
  },
  pricingSummary: {
    base: 'All at original prices',
    models: '{n} model set | {n} models set'
  }
}
