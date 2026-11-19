export default {
  title: 'Plugin network access',
  description: 'Plugins cannot reach the network themselves: the core sends all their outbound traffic. This sets which domains plugins may reach, for every plugin - both their own connections (the egress tunnel) and the requests platform plugins send to their upstreams (including the upstream addresses configured on accounts). A plugin\'s own connections still need the net permission, and a plugin set to "Allowlist only" still reaches only its approved domains.',
  defaultLabel: 'Default for all domains',
  defaultHint: 'Choose whether plugins may reach every domain by default',
  modes: {
    allow_all: 'Allow all domains',
    block_all: 'Block all domains'
  },
  info: {
    allow_all: 'Plugins may reach every domain except the ones you block below.',
    block_all: 'Plugins may reach only the domains you allow below.'
  },
  list: {
    allow_all: 'Blocked domains',
    block_all: 'Allowed domains'
  },
  listHint: {
    allow_all: 'Plugins cannot reach these domains or their subdomains.',
    block_all: 'Plugins may reach these domains and their subdomains.'
  },
  blockAllWarning: {
    title: 'Platform plugins reach only the allowed domains too',
    message: 'Every request of a platform whose upstream domain is not on the allowed list (for example api.anthropic.com, chatgpt.com, or the domain of a custom upstream address on an account) is refused. Add the upstream domains of every platform and the custom upstream domains of your accounts to the allowed list. Refused requests are listed under External access on the plugin\'s page.'
  },
  blockAllConfirm: {
    title: 'Block all domains?',
    message: 'Once all domains are blocked, plugins reach only the domains on the allowed list, and so do the requests platform plugins send to their upstreams: every request of a platform whose upstream domain is not on the list (for example api.anthropic.com, chatgpt.com, or the domain of a custom upstream address on an account) is refused at once. Switch anyway?'
  },
  domain: 'Domain',
  empty: 'No domains added yet.',
  add: 'Add domain',
  egressTabHint: 'The domains every plugin may reach are set in Settings → Plugin network access; they apply together with the egress policy here. That setting also rules the upstream requests the core sends for the plugin, and the requests it refuses are listed below as well.',
  egressTabLink: 'Open plugin network access',
  dialog: {
    title: {
      allow_all: 'Add domains to block',
      block_all: 'Add domains to allow'
    },
    addRow: 'Add another domain',
    bulk: 'Bulk add',
    single: 'One by one',
    bulkHint: 'One domain per line; blank lines are ignored.',
    continue: 'Continue',
    invalid: 'Not a valid domain or IP address (no https://, port or path)',
    lineInvalid: 'Line {line}: "{value}" is not a valid domain or IP address',
    empty: 'Enter at least one domain.'
  }
}
