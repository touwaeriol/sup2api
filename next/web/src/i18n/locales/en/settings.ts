export default {
  description: 'System-wide billing, gateway and scheduling settings.',
  missingTab: 'That settings page is not available to you (or no longer exists); showing the first one instead.',
  groups: {
    gateway: 'Gateway',
    billing: 'Billing & notifications',
    pluginSettings: 'Plugin settings',
    runtime: 'Runtime'
  },
  tabs: {
    notifications: 'Notifications',
    environment: 'Environment variables',
    billing: 'Billing',
    gateway: 'Gateway settings',
    sticky: 'Sticky sessions',
    offload: 'CPU protection'
  },
  scheduling: {
    tabs: { failure: 'Failure policy', balance: 'Load balancing' }
  },
  balance: {
    subtitle: 'How new sessions pick an account within the same priority. Only new sessions and failover are affected; bound sessions stay where they are.',
    mode: 'Strategy',
    modes: { default: 'Default', traffic: 'Traffic' },
    modeHints: {
      default: 'Accounts of the same priority are picked at random by weight.',
      traffic: "Accounts using less than their weight's share are preferred, so traffic follows the weight ratio."
    },
    limitLoad: 'Limit-aware load',
    limitLoadHint: 'The closer an account is to its limit, the lower its weight. E.g. RPM limit 100 with 90 used: weight ×10%. Accounts without a limit (nor, when balancing by model, a per-model limit for the requested model) are not affected.',
    metric: 'Metric',
    metricHint: 'Both the traffic balance and the limit load use this metric. For accounts without a TPM limit, and always when balancing by model, tokens are counted only when a request finishes, so TPM lags slightly. Only used with the Traffic strategy or Limit-aware load.',
    metrics: { rpm: 'RPM', concurrency: 'Concurrency', tpm: 'TPM' },
    modelLimits: 'Balance by model',
    modelLimitsHint: "Balance by the requested model: the traffic balance compares that model's own usage on each account, and when the model is in one of the account's per-model limit lists, the limit-aware load also uses that list's usage and limit, and the higher load wins. Only used with the Traffic strategy or Limit-aware load."
  },
  offload: {
    nodesTitle: 'Node load',
    title: 'CPU protection',
    subtitle: 'A node whose CPU is too busy hands new requests to idle nodes. Available on gateway-managed clusters only.',
    enabled: 'Enabled',
    enabledHint: 'When off, every node serves the requests of its own entrance.',
    threshold: 'CPU threshold',
    thresholdHint:
      'A node starts handing off when its CPU averaged over 10 seconds reaches this value, and stops below the threshold minus 10. Requests only go to healthy nodes of the same version below the threshold minus 10; without one they stay local. Requests in progress are not moved.',
    range: 'Range {min}–{max}.',
    unmanaged: 'This node is not managed by the gateway and has no CPU protection. Use the gateway for multi-node deployments.',
    node: 'Node',
    cpu: 'CPU',
    state: 'State',
    states: {
      serving: 'Serving locally',
      offloading: 'Handing off',
      forwarding: 'Forwarding to primary',
      unavailable: 'Not ready',
      disabled: 'Disabled'
    }
  },
  notifications: {
    channelsTitle: 'Notification channels',
    channelsHint: 'Declared and delivered by plugins (the mailer plugin provides "Email", for example). Other plugins and the core send by channel and never need to know the provider.',
    noChannels: 'No plugin provides a notification channel yet. Enable a notification plugin such as the mailer to see its channels here.',
    channel: 'Channel',
    provider: 'Provider',
    recipientFormat: 'Recipient format',
    recipient: { email: 'Email address', text: 'Text (interpreted by the plugin)' },
    testTitle: 'Send a test notification',
    testHint: 'The core sends a test notification to check a channel. See the delivery record below for the outcome.',
    recipients: 'Recipients',
    recipientsHint: 'Separate recipients with commas or spaces, at most 20.',
    sendTest: 'Send test',
    testQueued: 'Test notification queued',
    recordsTitle: 'Delivery record',
    recordsHint: 'Failed deliveries are retried with backoff, up to 8 times; records are kept for 30 days.',
    allStatuses: 'All statuses',
    source: 'Source',
    subject: 'Subject',
    attempts: 'try {n}',
    status: { pending: 'Pending', sending: 'Sending', sent: 'Sent', failed: 'Failed' },
    severity: { info: 'Info', warning: 'Warning', critical: 'Critical' },
    errors: {
      unknown_channel: 'No such notification channel; the plugin providing it may be disabled',
      recipients_count: 'Give 1-20 recipients',
      recipient_too_long: 'A recipient is longer than 320 bytes',
      invalid_recipient: 'A recipient does not match the format of this channel'
    }
  },
  environment: {
    title: 'Environment variables',
    subtitle: 'General runtime parameters of forwarding, declared by the core; plugins read the resolved values. Accounts and groups override these; empty follows the next layer (account → group → global → process environment variable). Changes apply to new requests on every node.',
    groupHint: 'Overrides the global environment variables; empty follows the global value.',
    accountHint: 'Overrides the group and global environment variables; empty follows the group.',
    range: 'Range {min}–{max}.',
    processSet: 'Process environment variable {name} = {value}.',
    processUnset: 'The process environment variable {name} is the fallback.',
    follow: 'Follow',
    followValue: 'Follow ({value})',
    units: { seconds: 's', minutes: 'min', mb: 'MB', count: 'times' }
  },
  gateway: {
    title: 'Gateway',
    subtitle: 'Failover, timeouts and protocol conversion of gateway requests. Changes apply to new requests on every node.',
    fields: {
      max_attempts: 'Max attempts',
      platform_call_timeout_ms: 'Platform call timeout',
      default_hook_timeout_ms: 'Default hook timeout',
      platform_hotpath_timeout_ms: 'Platform hot-path timeout',
      queue_sticky_max_waiting: 'Max queued for a bound account',
      queue_sticky_wait_seconds: 'Longest wait for a bound account',
      queue_fallback_max_waiting: 'Max queued per account when all are full',
      queue_fallback_wait_seconds: 'Longest wait when all accounts are full',
      media_download_max_mb: 'Media download cap',
      claude_default_max_tokens: 'Claude default max_tokens'
    },
    hints: {
      max_attempts: 'Accounts tried per request, including the first attempt (failover).',
      queue_sticky_max_waiting: 'How many requests per account may wait for a session\'s bound account when it is full, staying on it to keep the prompt cache. 0 turns this queue off.',
      queue_sticky_wait_seconds: 'Longest wait for the bound account; after it the sticky rule switches account or answers 429. 0 turns this queue off.',
      queue_fallback_max_waiting: 'How many requests per account may queue when every candidate is full. 0 answers 429 at once.',
      queue_fallback_wait_seconds: 'Longest wait when every candidate is full; after it the request gets 429. 0 turns this queue off.',
      platform_call_timeout_ms: 'Timeout of plugin platform calls on the request path (building the upstream request, parsing usage).',
      default_hook_timeout_ms: 'Used by hooks whose manifest sets no timeout.',
      platform_hotpath_timeout_ms:
        'Timeout of the per-request platform calls that are not hooks: resolving the model before scheduling, and reading usage after the response was sent.',
      media_download_max_mb: 'Size cap of one http(s) image or other media file downloaded for a request converted to Gemini (MB).',
      claude_default_max_tokens: 'max_tokens of a request converted to Anthropic when the client sets no output limit: by exact client model name, else the default row. The default row cannot be removed or renamed; at most 200 rows, values 0–1048576.'
    },
    maxTokens: {
      model: 'Model',
      tokens: 'max_tokens',
      modelPlaceholder: 'Client model name',
      add: 'Add model',
      remove: 'Remove {model}',
      defaultLocked: 'The default row cannot be removed or renamed',
      errors: {
        value: 'Every row needs a whole number from 0 to {max}',
        model: 'Model names must be 1–200 characters',
        duplicate: 'Duplicate model name',
        too_many: 'At most {n} rows'
      }
    },
    range: 'Range {min}–{max}.',
    notInteger: 'Enter a whole number'
  },
  billing: {
    preConsumeTokens: 'Request reservation tokens',
    preConsumeHint: 'Minimum input tokens reserved for text requests, default 500. Larger local estimates take precedence. Zero disables the minimum. Final billing uses actual usage; video uses plugin estimates.',
    preConsumeInvalid: 'Enter an integer from 0 to 100000000',
    title: 'Billing',
    missingPolicy: 'When a model has no price',
    policy: {
      reject: 'Reject the request',
      free: 'Allow for free'
    },
    policyHint: {
      reject: 'Return 403 model_price_not_configured. Recommended: nothing is served without a price.',
      free: 'Serve the request and record it with billing status "free".'
    },
    minBalance: 'Minimum balance',
    minBalanceHint: 'Requests are rejected with 402 when the balance is at or below this value (default 0).',
    bigCost: 'High-cost warning threshold',
    bigCostHint: 'Saving a price that costs more than this for 1M input tokens requires confirmation.',
    decimalInvalid: 'Enter a number with at most 8 decimals'
  }
}
