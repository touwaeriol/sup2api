export default {
  segment: {
    personal: 'Personal',
    personalOf: 'Personal · {name}'
  },
  exclusive: 'Exclusive',
  rate: {
    label: 'Own rate',
    follow: 'Follows {rate}',
    groupDefault: 'Group default {group}',
    discounted: 'Group default {group} × discount {discount}',
    override: 'Own rate, no longer follows {rate}',
    invalid: 'Enter a number from 0 to below 1000000, up to 4 decimals'
  },
  rule: {
    mode: 'Pricing',
    multiplier: 'Multiplier',
    custom: 'Custom price',
    multiplierText: 'Global {rate}',
    customText: 'Custom {price}',
    applied: 'Comes to {price}; follows the global price',
    appliedOpaque: 'Every price is multiplied by it; follows the global price',
    multiplierInvalid: 'Enter a number from 0 to below 1000000, up to 4 decimals',
    tokenUnit: 'A fixed price that does not follow the global price. In $ per 1M tokens',
    requestUnit: 'A fixed price that does not follow the global price. In $ per request',
    tokenInvalid: 'Input and output prices are required; no price may be negative',
    requestInvalid: 'Enter a price of 0 or more',
    multiplierOnly: 'The global price of this model is not a visual price of one tier without surcharge rules (it is an expression, or has several tiers or surcharge rules): only a multiplier applies.',
    noBase: 'This model has no global price yet. Set one under Model prices first.',
    requestPrice: 'Price'
  },
  price: {
    perRequest: '{price} per request',
    expression: 'Billed by expression',
    expressionTimes: 'Billed by expression {rate}',
    none: 'No price',
    tiered: '{n} tiers, first shown'
  },
  source: {
    segment: 'Own price',
    group: 'Group pricing',
    global: 'Global'
  },
  table: {
    base: 'Global price',
    groupRule: 'Group pricing',
    segmentRule: 'Own price',
    listPrice: 'List price',
    followGlobal: 'Follows global',
    followGroup: 'Follows group pricing',
    segmentRules: '{n} user group has a price of its own | {n} user groups have a price of their own',
    set: 'Set',
    done: 'Done',
    clear: 'Clear',
    unsaved: 'Unsaved',
    unsavedCount: '{n} unsaved change | {n} unsaved changes',
    search: 'Search models',
    onlyRuled: 'Only models priced here',
    unit: 'In $ per 1M tokens (input / output); per request for models billed that way.',
    emptyNew: 'The models this group provides are listed here once the group is saved and has accounts.',
    emptyModels: 'This group provides no models yet. Its models are those its accounts declare, filtered by the group model list.',
    emptyFiltered: 'No models match',
    invalid: 'The price of {model} is incomplete. Correct it first.'
  }
}
