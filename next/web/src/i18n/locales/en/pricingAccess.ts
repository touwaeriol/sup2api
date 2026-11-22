export default {
  segment: 'User group',
  access: 'Available',
  rate: 'Rate',
  paid: 'Paid rate',
  public: 'Public',
  private: 'Private',
  granted: 'Enabled',
  notGranted: 'Not enabled',
  grant: {
    segment: 'Enable this group for "{name}"',
    group: 'Enable "{name}"'
  },
  rateOf: 'Rate of "{name}"',
  follow: 'Follows',
  source: {
    segment: 'Own rate',
    discount: 'Group rate × discount',
    group: 'Follows the group rate'
  },
  discounted: 'Group rate {group} × discount {discount}',
  unavailable: 'Not available',
  unsaved: 'Unsaved',
  remove: 'Remove',
  groupRate: 'Group rate',
  members: '{n} member | {n} members',
  discountOf: 'Discount {rate}',
  overrideNote: 'A rate you enter is used as is, without the discount; clear it to follow again.',
  inGroup: {
    restricted: 'Only the user groups it is enabled for may use this group.',
    public: 'Everyone may use this group.',
    keptGrants: 'Everyone may use a public group; these settings apply again if it goes back to private.',
    nobody: 'This private group is enabled for no user group: nobody can use it now.',
    nobodyHint: 'Add a user group or a user to enable it.',
    addSegment: 'Add user group',
    addUser: 'Add user',
    addNone: 'Every user group is listed',
    empty: {
      public: 'Nothing set here yet: everyone pays the group rate times their discount.',
      restricted: 'Not enabled for any user group or user yet: nobody can use it.'
    }
  },
  invalidRate: 'The rate of "{name}" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.'
}
