export default {
  title: 'User groups',
  description: 'Every user is in one user group. It decides which private groups they may use and how many times the price they pay.',
  create: 'New user group',
  created: 'User group created',
  unsavedNew: 'Not saved yet: Create saves the user group, its group rates and its members together.',
  editTitle: 'Edit user group {name}',
  viewTitle: 'User group {name}',
  searchPlaceholder: 'Search name, note or user email',
  default: 'Default for new users',
  defaultHint: 'New users join this user group; it cannot be deleted.',
  defaultName: 'The default user group cannot be renamed.',
  note: 'Note',
  publicOnly: 'Public groups only',
  simulate: 'Price simulator',
  deleteTitle: 'Delete the user group "{name}"',
  deleteConfirm:
    'Its {n} member goes back to the default user group, and its settings in the groups are deleted with it. This cannot be undone. | Its {n} members go back to the default user group, and its settings in the groups are deleted with it. This cannot be undone.',
  kinds: {
    label: 'Filter by kind',
    all: 'All {n}',
    named: 'User groups {n}',
    personal: 'Exclusive {n}'
  },
  columns: {
    name: 'Name',
    members: 'Members',
    discount: 'Discount',
    granted: 'Private groups it may use',
    overrides: 'Own rates'
  },
  tabs: {
    basic: 'Basics',
    groups: 'Group rates',
    members: 'Members',
    membersPicked: 'Members · {n} picked'
  },
  discount: {
    label: 'Discount',
    hint: 'Multiplies the group rate of every group; a group with an own rate ignores it. 1 means no discount.',
    example: 'E.g. group rate {group} → paid rate {rate}',
    invalid: 'Enter a number above 0, up to 999999.9999 and 4 decimals; 1 for no discount.'
  },
  groups: {
    discount: 'Discount',
    discountHint: '(change it under Basics): a group without an own rate charges the group rate × {discount}',
    noDiscount: 'No discount (change it under Basics): a group without an own rate charges its group rate'
  },
  members: {
    notice: 'Adding and removing members takes effect at once, without Save.',
    noticeNew: 'The members picked join when you click Create.',
    add: 'Add a member',
    addHint: 'A user is in one user group: joining "{segment}" moves them out of the one they are in.',
    addPlaceholder: 'Search the user to add by email or name',
    search: 'Search members',
    inUsers: 'View in the users list',
    empty: 'This user group has no members yet',
    emptyNew: 'No member picked yet',
    thisSegment: 'this user group',
    already: '{name} is in this user group already',
    dropPersonal: '{name} is Exclusive now. Joining "{segment}" deletes their own settings; from then on they pay as "{segment}".',
    dropPersonalConfirm: 'Join and delete own settings',
    movedFrom: 'Moved {name} from "{from}" to "{to}"',
    remove: 'Remove',
    removeConfirm: 'Remove {name} from "{segment}"? They go back to "{to}" and pay as "{to}" from then on.',
    movedTo: 'Moved {name} to "{to}"',
    defaultHint: 'Members of the default user group cannot be removed here: add them to the user group they should be in.'
  },
  personal: {
    origin: 'Was in {name}',
    remove: 'Move back to a user group',
    notice: 'These settings apply to this user only.',
    noticeOrigin: 'These settings apply to this user only. They were in "{origin}"; later changes to "{origin}" no longer affect them.'
  },
  select: {
    placeholder: 'Choose a user group',
    default: 'default for new users',
    discount: 'discount {rate}'
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
    copyHint: 'The discount, the private groups enabled and the rate in each group are copied; change them afterwards.',
    blank: 'Start from blank',
    blankHint: 'Public groups only, at each group rate.',
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
