export default {
  rulesTitle: 'Session binding rules',
  title: 'Sticky sessions',
  description: 'Route consecutive requests of a conversation to the same account to keep upstream caches warm.',
  newRule: 'New rule',
  any: 'any',
  seconds: 'seconds',
  ttlValue: '{n}s',
  ttlDefault: 'default',
  copyAsAdmin: 'Copy as admin rule',
  customized: 'Edited',
  customizedHint: 'The built-in definition was edited; upgrades keep it. It can be restored to the default.',
  resetDefault: 'Restore default',
  resetConfirm: 'Restore the definition of built-in rule "{name}" to the default? Enabled and priority stay as they are.',
  resetDone: 'Default restored',
  flush: 'Flush bindings',
  flushConfirm: 'Delete all current bindings of rule "{name}"? The next requests will be scheduled normally.',
  flushed: 'Bindings flushed',
  flushedN: '{n} binding(s) flushed',
  statsLine: '{hits} hits · {misses} misses · {rebinds} rebinds',
  source: {
    admin: 'Admin',
    builtin: 'Built-in'
  },
  cols: {
    ua: 'UA',
    priority: 'Priority',
    match: 'Match',
    binding: 'Binding',
    stats: 'Hit rate',
    keySources: 'Key',
    ttl: 'TTL',
    keyIncludes: 'Key includes',
    onFailure: 'On failure'
  },
  keyTypes: {
    body: 'Body field',
    header: 'Header',
    api_key: 'API key',
    user: 'User'
  },
  keyTypeHint: {
    api_key: 'the API key making the request',
    user: 'the user owning the API key'
  },
  includes: {
    group: 'account group',
    model: 'model',
    rule: 'rule'
  },
  onFailure: {
    failover: 'Failover',
    stick: 'Stick'
  },
  onFailureHint: {
    failover: 'When the bound account fails, switch accounts as usual and rebind on success.',
    stick: 'Never switch; return the error instead (protects the upstream cache).'
  },
  settings: {
    title: 'Global settings',
    enabled: 'Sticky sessions',
    enabledHint: 'Master switch for all rules',
    defaultTtl: 'Default TTL',
    defaultTtlHint: 'Used by rules without their own TTL',
    ttlInvalid: 'Enter a positive number of seconds',
    keepOnDisabled: 'Keep bindings of disabled accounts',
    keepOnDisabledHint: 'By default bindings are deleted when their account is disabled'
  },
  modal: {
    createTitle: 'New sticky rule',
    editTitle: 'Edit rule · {name}',
    copyTitle: 'Copy as admin rule',
    limitedHint: 'Built-in rule: the name is fixed; everything else can be edited. Upgrades keep your edits, and the default can be restored at any time.',
    priorityHint: 'Higher is checked first',
    match: 'Match',
    protocols: 'Protocols',
    protocolPick: '+ Add protocol',
    protocolUnknown: 'unknown',
    protocolPattern: 'pattern',
    protocolUnknownHint: 'Protocols marked "unknown" are not among the declared endpoints (the plugin declaring them may be uninstalled or disabled); they are kept as they are on save.',
    protocolsPartial: 'The platform list could not be loaded, so only built-in platforms are listed: protocols declared by plugins may be missing — add them by hand below.',
    protocolManual: 'protocol, e.g. anthropic.messages',
    models: 'Models',
    modelsHint: 'Wildcards allowed; empty = any',
    ua: 'User-Agent contains',
    emptyAny: 'Empty = any',
    keySources: 'Session key sources',
    keySourcesHint: 'Tried in order; the first non-empty value is used.',
    valueRegex: 'Value regex',
    valueRegexHint: 'Optional; the first capture group (or whole match) of the value is used',
    passHeaders: 'Pass headers',
    passHeadersHint: 'Client headers a request matching this rule forwards upstream: * for all, regex:<pattern> by name, or exact names. Credentials, client addresses and gateway control headers never pass',
    ttlHint: '0 = use the default TTL',
    keyIncludesHint: 'Dimensions added to the session key',
    needSource: 'Add at least one key source',
    needPath: 'Enter the body field path',
    needHeader: 'Enter the header name',
    badRegex: 'Invalid regular expression',
    badTtl: 'Enter a whole number of seconds (0 or more)'
  }
}
