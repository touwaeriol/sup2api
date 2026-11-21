export default {
  sim: {
    title: 'Price simulator',
    description: 'Pick a user or a user group, a group and a model to see which model price and rate a call is billed at.',
    who: 'For',
    subject: {
      user: 'User',
      segment: 'User group'
    },
    modelPlaceholder: 'Choose a model',
    modelHint: 'Only the models this group provides.',
    inputTokens: 'Input tokens',
    outputTokens: 'Output tokens',
    tokensInvalid: 'Enter a whole number from 0 to {max}',
    usageInvalid: 'Correct the usage above: the price is calculated once it is valid.',
    incomplete: 'Pick who, a group and a model: the price is calculated at once.',
    unprovided: 'No group provides {model} yet, so there is nothing to simulate.'
  },
  quote: {
    access: {
      title: 'Open to them',
      public: 'Public group, usable',
      grant: 'Private group, opened',
      none: 'Private group, not opened',
      disabled: 'Group disabled'
    },
    denied: {
      none: 'Cannot use this private group; below is the price once it is opened.',
      disabled: 'The group is disabled, so nobody can use it; below is the price once it is enabled.'
    },
    price: {
      title: 'Model price',
      hint: 'Top down, the first level that is set applies. Token prices are input / output per 1M tokens.',
      group: 'Group price ({group})',
      global: 'Original price',
      tiered: 'Tiered price: the fallback tier (the last) is shown; the cost is that of the tier this usage falls in.'
    },
    rate: {
      title: 'Rate',
      hint: 'Top down, the first level that is set applies. A rate set on its own is used as it is; the discount is not applied on top.',
      segment: 'Rate set on its own ({segment})',
      discount: 'Group rate {base} × discount {discount}',
      group: 'Group rate ({group})',
      noDiscount: 'No discount'
    },
    unset: 'Not set',
    applies: 'Applies',
    paid: 'Paid',
    paidIf: {
      none: 'Price once opened',
      disabled: 'Price once enabled'
    },
    unpriced: 'This model has no original price yet, so it cannot be billed. Set one on Model prices first.',
    listCost: 'Cost at model price',
    rateName: 'Rate'
  }
}
