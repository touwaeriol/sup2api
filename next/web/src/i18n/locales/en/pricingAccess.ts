export default {
  segment: 'User group',
  access: 'Available',
  rate: 'Rate',
  paid: 'Paid rate',
  public: 'Public',
  private: 'Private',
  granted: 'Granted',
  notGranted: 'Not granted',
  grant: {
    segment: 'Grant this account group to "{name}"',
    group: 'Grant "{name}"'
  },
  rateOf: 'Rate of "{name}"',
  follow: 'Follows',
  source: {
    segment_model: 'Model rate of the user group',
    segment: 'Set by the user group',
    group_model: 'Model rate of the account group',
    group: 'Follows the account group'
  },
  unavailable: 'Not available',
  remove: 'Remove',
  undo: 'Undo',
  groupRate: 'Base rate',
  baseRate: 'Base rate',
  members: '{n} member | {n} members',
  overrideNote: 'An empty rate follows the account group: a model with a model rate pays that, the others its base rate. Only one rate applies; rates are never multiplied.',
  inGroup: {
    restricted: 'Only the user groups it is granted to may use this account group.',
    public: 'Everyone may use this account group.',
    keptGrants: 'Everyone may use a public account group; these grants apply again if it goes back to private.',
    nobody: 'This private account group is granted to no user group: nobody can use it now.',
    nobodyHint: 'Add a user group or a user to grant it.',
    addSegment: 'Add user group',
    addUser: 'Add user',
    addNone: 'Every user group is listed',
    modelRates: 'Remove deletes the model rates too',
    empty: {
      public: 'Nothing set here yet: everyone pays the base rate.',
      restricted: 'Not granted to any user group or user yet: nobody can use it.'
    }
  },
  invalidRate: 'The rate of "{name}" is not valid: enter a number from 0 to 999999.9999, up to 4 decimals.'
}
