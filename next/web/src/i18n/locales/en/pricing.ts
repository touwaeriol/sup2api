export default {
  segment: {
    personal: 'Exclusive',
    personalOf: 'Exclusive · {name}'
  },
  exclusive: 'Exclusive',
  draft: {
    unsaved: 'Unsaved',
    refused: 'Refused',
    removed: 'To be removed'
  },
  rate: {
    label: 'Rate',
    follow: 'Follows {rate}',
    invalid: 'Enter a number from 0 to 999999.9999, up to 4 decimals'
  },
  editor: {
    mode: 'Pricing',
    kind: {
      none: 'Follows the original price',
      custom: 'Custom price'
    },
    customText: 'Custom',
    customHint: 'The same editor as on Model prices: tiers, per request or an expression. Does not follow the original price; to scale it, use the model rate.',
    preview: 'Model price {price}; at rate {rate} you pay {paid}',
    noBase: 'This model has no original price yet. Set one on Model prices first.'
  },
  price: {
    perRequest: '{price} per request',
    expression: 'Billed by expression',
    expressionTimes: 'Billed by expression {rate}',
    none: 'No price',
    tiered: '{n} tiers, fallback shown'
  },
  table: {
    title: 'Model prices',
    count: '{n} model | {n} models',
    base: 'Original price',
    price: 'Model price',
    rate: 'Model rate',
    paid: 'Paid',
    followBase: 'Follows the original price',
    followRate: 'Follows the base rate',
    rateOf: 'Model rate of {model}',
    rateSource: {
      group_model: 'Model rate',
      group: 'Follows the base rate'
    },
    notOffered: 'Not provided yet',
    notOfferedHint: 'No account in the account group has this model yet; the settings are kept until one does',
    editing: 'Model price of {model} in this account group',
    set: '{n} set | {n} set',
    change: 'Change price',
    done: 'Done',
    clear: 'Clear',
    clearHint: '"Clear" makes the model price follow the original price and the model rate follow the base rate',
    add: 'Add model',
    addNone: 'No model to add',
    addFooter: 'Only models with an original price on Model prices can be added',
    unsavedCount: '{n} unsaved change | {n} unsaved changes',
    search: 'Search models',
    onlySet: 'Set only',
    unit: 'In $ per 1M tokens (input / output); per request for models billed that way.',
    emptyNew: 'A new account group has no accounts yet. Once accounts are put in this account group (when editing an account), the models they provide are listed here. You can also add a model and price it now.',
    noAccounts: 'Once accounts are put in this account group (when editing an account), the models they provide are listed here.',
    emptyModels: 'The account group provides no models yet: its models are those of its accounts, through the model list. You can add a model and price it now.',
    emptyFiltered: 'No models match',
    invalid: 'The model price or model rate of {model} is not valid. Correct it first.'
  }
}
