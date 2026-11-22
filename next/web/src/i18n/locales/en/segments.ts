export default {
  title: 'User groups',
  description: 'Every user is in one user group. It decides which private account groups they may use and how many times the price they pay.',
  create: 'New user group',
  created: 'User group created',
  unsavedNew: 'Not saved yet: fill in any tab, everything is sent together on Create. Members are added on the users page.',
  editTitle: 'Edit user group {name}',
  viewTitle: 'User group {name}',
  searchPlaceholder: 'Search name, note or user email',
  default: 'Default for new users',
  defaultHint: 'New users join this user group; it cannot be deleted.',
  defaultName: 'The default user group cannot be renamed.',
  note: 'Note',
  publicOnly: 'Public account groups only',
  simulate: 'Price simulator',
  deleteTitle: 'Delete the user group "{name}"',
  deleteConfirm:
    'Its {n} member goes back to the default user group, and its settings and model rates in the account groups are deleted with it. This cannot be undone. | Its {n} members go back to the default user group, and its settings and model rates in the account groups are deleted with it. This cannot be undone.',
  kinds: {
    label: 'Filter by kind',
    all: 'All {n}',
    named: 'User groups {n}',
    personal: 'Exclusive {n}'
  },
  columns: {
    name: 'Name',
    members: 'Members',
    granted: 'Private account groups it may use',
    overrides: 'Own rates'
  },
  tabs: {
    basic: 'Basics',
    groups: 'Account group rates'
  },
  modelRates: {
    toggle: 'Model rates ({n})',
    intro: 'A model with a rate of its own is billed at its original price × that model rate; the others follow this row, {rate}.',
    add: 'Add model',
    addNone: 'No model to add',
    addFooter: 'Only the models of this account group with an original price',
    empty: 'No model has a rate of its own yet',
    rateOf: 'Model rate of {model}',
    undo: 'Undo',
    invalid: 'The model rate of {model} in "{group}" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.'
  },
  personal: {
    origin: 'Was in {name}',
    remove: 'Move back to a user group',
    notice: 'These settings apply to this user only.',
    noticeOrigin: 'These settings apply to this user only. They were in "{origin}"; later changes to "{origin}" no longer affect them.'
  },
  select: {
    placeholder: 'Choose a user group',
    default: 'default for new users'
  },
  convert: {
    pick: 'Set a rate for one user',
    pickShort: 'Set rates for a user',
    pickHint: 'The user leaves their user group and becomes Exclusive; one who is Exclusive already opens their settings.',
    pickPlaceholder: 'Search a user by email or name',
    title: 'Set rates for {name}',
    intro: 'The user becomes Exclusive and leaves "{from}"; later changes to "{from}" no longer affect them.',
    currentSegment: 'the current user group',
    startFrom: 'Start from',
    copy: 'Copy the current settings of "{from}"',
    copyHint: 'The private account groups granted, and the rate and model rates in each account group are copied; change them afterwards.',
    blank: 'Start from blank',
    blankHint: 'Public account groups only, at each account group rate.',
    submit: 'Set own rates',
    done: '{name} is now Exclusive'
  },
  remove: {
    title: 'Move {name} back to a user group',
    target: 'Move to user group',
    warning: 'Their own settings will be deleted; from then on they pay as the chosen user group.',
    submit: 'Move and delete own settings',
    done: 'Moved {name} to "{segment}"'
  }
}
