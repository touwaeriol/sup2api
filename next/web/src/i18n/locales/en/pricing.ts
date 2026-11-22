export default {
  segment: {
    personal: 'Personal',
    personalOf: 'Personal · {name}'
  },
  exclusive: 'Exclusive',
  rate: {
    label: 'Rate',
    follow: 'Follows {rate}',
    invalid: 'Enter a number from 0 to 999999.9999, up to 4 decimals'
  },
  rule: {
    mode: 'Pricing',
    kind: {
      none: 'Original price',
      multiplier: 'Original × N',
      custom: 'Custom price'
    },
    multiplierText: 'Original × {rate}',
    customText: 'Custom',
    timesPrefix: 'Original ×',
    multiplierHint: 'Follows the original price',
    customHint: 'The same editor as on Model prices: tiers, per request or an expression. Does not follow the original price.',
    preview: 'Model price {price}; at rate {rate} you pay {paid}',
    multiplierInvalid: 'Enter a number from 0 to below 1000000, up to 4 decimals',
    noBase: 'This model has no original price yet. Set one on Model prices first.'
  },
  price: {
    perRequest: '{price} per request',
    expression: 'Billed by expression',
    expressionTimes: 'Billed by expression {rate}',
    none: 'No price',
    tiered: '{n} tiers, fallback shown'
  },
  source: {
    group: 'Group price',
    segment: 'User group price (retired)'
  },
  table: {
    title: 'Model prices',
    count: '{n} model | {n} models',
    ruled: '{n} repriced',
    base: 'Original',
    group: 'Group price',
    paid: 'Paid ({rate})',
    useBase: 'Original price',
    notOffered: 'Not provided yet',
    notOfferedHint: 'No account in the group has this model yet; the price is kept until one does',
    editing: 'Price of {model} in this group',
    set: 'Change',
    done: 'Done',
    clear: 'Clear',
    clearHint: '"Clear" goes back to the original price',
    add: 'Add model',
    addNone: 'No model to add',
    addFooter: 'Only models with an original price on Model prices can be added',
    unsaved: 'Unsaved',
    unsavedCount: '{n} unsaved change | {n} unsaved changes',
    search: 'Search models',
    onlyRuled: 'Repriced only',
    unit: 'In $ per 1M tokens (input / output); per request for models billed that way.',
    emptyNew: 'Once accounts are added on Accounts, the models they provide are listed here. You can also add a model and price it now.',
    noAccounts: 'Once accounts are added on Accounts, the models they provide are listed here.',
    emptyModels: 'The group provides no models yet: its models are those of its accounts, through the model list. You can add a model and price it now.',
    emptyFiltered: 'No models match',
    invalid: 'The price of {model} is incomplete. Correct it first.'
  }
}
