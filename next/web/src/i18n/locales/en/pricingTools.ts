export default {
  segment: {
    placeholder: 'Choose a user group',
    default: 'default for new users',
    discount: '{rate} on every group'
  },
  convert: {
    title: 'Set {name} to Personal',
    intro: 'Once Personal, this user no longer belongs to "{from}", and later changes to "{from}" no longer affect them.',
    currentSegment: 'the current user group',
    startFrom: 'Where the personal settings start from',
    copy: 'Copy the current settings of "{from}"',
    copyHint: 'Grants, own rates, own prices and the discount are all copied; change them separately afterwards.',
    blank: 'Start from blank',
    blankHint: "Public groups only, at each group's default rate and default prices.",
    submit: 'Set to Personal',
    done: '{name} is now Personal'
  },
  remove: {
    title: 'Move {name} back to a user group',
    target: 'Move to user group',
    warning: 'Their personal settings (grants, own rates, own prices) will be deleted; from then on they are billed as the chosen user group.',
    submit: 'Move and delete personal settings',
    done: 'Moved {name} to "{segment}"'
  },
  sim: {
    title: 'Price simulator',
    description: 'Pick a user or a user group, a group and a model to see which level of list price and rate a call is billed at.',
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
      title: 'Access',
      public: 'Public group, usable',
      grant: 'Private group, granted',
      none: 'Private group, not granted'
    },
    denied: 'Cannot use this private group; below is the price once it is granted.',
    price: {
      title: 'List price',
      hint: 'Top down, the first level that is set applies. Token prices are input / output per 1M tokens.',
      segment: 'Own price ({segment})',
      group: 'Group pricing ({group})',
      global: 'Global price',
      tiered: 'Tiered price: the fallback tier (the last) is shown; the cost is that of the tier this usage falls in.'
    },
    rate: {
      title: 'Rate',
      hint: 'Top down, the first level that is set applies. An own rate replaces the rate; the discount is not applied on top.',
      segment: 'Own rate ({segment})',
      discount: 'Group default {base} × discount {discount}',
      group: 'Group default rate ({group})',
      noDiscount: 'No discount'
    },
    unset: 'Not set',
    applies: 'Applies',
    paid: 'Paid',
    paidIfGranted: 'Price once granted',
    unpriced: 'This model has no global price yet, so it cannot be billed. Set one under Model prices first.',
    listCost: 'List cost',
    rateName: 'Rate'
  }
}
