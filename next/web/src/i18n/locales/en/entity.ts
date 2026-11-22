export default {
  kind: {
    user: 'User',
    group: 'Account group',
    api_key: 'API key',
    account: 'Account',
    model: 'Model',
    proxy: 'Proxy',
    pluginScope: 'Plugin data'
  },
  contains: 'Name contains “{text}”',
  containsHint: 'Partial match',
  byId: 'Use ID #{id}',
  byIdHint: 'This ID is not in the list (it may have been deleted); you can still use it',
  asTyped: 'Use “{text}”',
  asTypedHint: 'This name is not in the list; you can still use it',
  inactive: 'Disabled',
  more: 'Showing the first {n}. Type to narrow the list.',
  moreUnlisted: 'More match than the list shows. Type to narrow the list.',
  none: 'No matching {name}',
  failed: 'Could not load the list',
  failedTypeValue: 'Candidates are unavailable now; type a value instead',
  typeId: '{name} ID',
  typeIdOrName: '{name} ID or name',
  typeFor: '{label} ({what})',
  add: 'Add {name}',
  remove: 'Remove {name}',
  scope: {
    userKeys: 'Only the keys of {name}',
    groupAccountsFirst: 'Accounts of {name} come first'
  }
}
