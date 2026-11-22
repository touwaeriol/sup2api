export default {
  title: 'Usage logs',
  description: 'Every gateway request with its billing trace.',
  myTitle: 'Usage records',
  myDescription: 'Each of your requests and your balance changes.',
  overviewTitle: 'Usage overview',
  overviewDescription: 'Requests, tokens and cost across the gateway.',
  myOverviewTitle: 'Usage overview',
  myOverviewDescription: 'Your requests, tokens and cost.',
  accountTitle: 'Account usage logs',
  accountDescription: 'The requests the accounts in your reach served, the account’s side only: no users, keys, IPs or billing; costs at the list price.',
  accountOverviewTitle: 'Account usage overview',
  accountOverviewDescription: 'Requests, tokens and list-price cost of the accounts in your reach.',
  live: {
    cost: 'Usage',
    standard: 'Base quota',
    listPrice: 'List price',
    cacheHit: 'Cache hit',
    successRate: 'Success rate',
    rangeHint: 'Follows the time range and filters above',
    rpmHint: 'Requests that succeeded in the last minute. Follows the filters, not the time range.',
    tpmHint: 'Tokens of those requests (non-cached input + output). Follows the filters, not the time range.',
    successHint: 'Last minute: {ok} succeeded / {fail} failed (400 errors, client disconnects and gateway-side rejections excluded)',
    noRequests: 'No requests in the last minute'
  },
  statsTitle: 'Usage statistics',
  statsDescription: 'Your requests, tokens and cost over a period.',
  stats: {
    input: 'Input tokens',
    output: 'Output tokens',
    byModel: 'By model'
  },
  tabs: {
    requests: 'Requests',
    ledger: 'Balance changes'
  },
  nonStream: 'Non-streaming',
  search: {
    logType: 'Log type',
    retried: 'Retries',
    // The model, user, API key, account group and account are entity pickers (EntityPicker), named by
    // the picker itself; these are the other fields' placeholders and the filter chips' names
    // (by query parameter).
    client_ip: 'Client IP (exact)',
    group_id: 'Account group',
    group_name: 'Account group name contains',
    request_id: 'Request ID',
    client_request_id: 'Client request ID',
    platform: 'Platform',
    protocol: 'Request type',
    endpoint: 'Endpoint',
    status_code: 'HTTP status',
    error_type: 'Error type',
    account_id: 'Account',
    account_name: 'Account name contains',
    plugin_key: 'Plugin key',
    account_type: 'Account type',
    node_id: 'Node ID',
    upstream_model: 'Upstream model',
    upstream_protocol: 'Upstream protocol',
    conversion: 'Protocol conversion',
    stream: 'Response mode'
  },
  success: {
    ok: 'Succeeded',
    failed: 'Failed'
  },
  summary: {
    requests: 'Requests',
    successRate: 'Success rate',
    tokens: 'Tokens',
    tokensSub: 'in {input} · out {output}',
    cost: 'Cost',
    daily: 'Daily',
    trend: 'Trend',
    cacheHit: 'Cache hit'
  },
  showDetails: 'View details',
  hideDetails: 'Hide details',
  // A request's or an attempt's error_type; a type the console does not know shows as recorded.
  errors: {
    model_not_allowed: 'Model unavailable',
    no_available_account: 'No available account',
    rate_limit_exceeded: 'Rate limit exceeded',
    upstream_error: 'Upstream error',
    client_canceled: 'Client canceled',
    internal: 'Internal error',
    rate_limited: 'Rate limited',
    invalid_request: 'Invalid request',
    model_availability: 'Model temporarily unavailable',
    usage_unreported: 'Usage not reported (not billed)'
  },
  unreportedHint: 'The plugin returned no usage for this request (or chose the host\'s reading and it was empty), so it was not billed.',
  cols: {
    timing: 'Timing',
    detail: 'Details',
    input: 'Input',
    output: 'Output',
    cost: 'Cost',
    accountType: 'Account type',
    upstreamProtocol: 'Upstream protocol'
  },
  filterByClientRequestId: '{id} (click to filter by this ID)',
  filterByAccount: '{account} (click to filter by this account)',
  noAccount: 'No upstream account (the request ended before one was picked)',
  cacheTitle: 'cache read {r} · cache write {w}',
  cached: 'cached',
  cacheEvidence: {
    title: 'Observed upstream cache writes',
    total: 'Reported total writes',
    five: 'Reported 5-minute writes',
    hour: 'Reported 1-hour writes',
    unclassified: 'TTL not classified',
    pricing: 'Billing buckets are separate from observed TTL facts. Unclassified writes use the platform default cache-write rate; this does not establish a 5-minute TTL. Recorded charges are unchanged.',
    missing: 'This record has no usable TTL provenance. The values above are the original billing buckets, not proof of actual 5-minute usage. Recorded charges are unchanged.',
    counter: { absent: 'Not reported', null: 'Reported null', invalid: 'Invalid reported value' },
    component: { additional: 'Separate cache observations for additional usage #{index}', replacement: 'Separate cache observations for replacement usage #{index}' },
    status: {
      complete: 'The reported TTL breakdown is complete.',
      partial: 'The TTL breakdown is incomplete; reported buckets may precede the latest total.',
      unknown: 'TTL allocation is unknown. Missing buckets were not inferred.',
      inconsistent: 'Reported cache counters are inconsistent; TTL allocation cannot be determined.'
    }
  },
  conversion: {
    title: 'Protocol conversion',
    grade: 'Grade',
    policy: 'Effective policy',
    hops: 'Steps',
    omitted: 'Paths not recorded',
    requestLost: 'Lost from the request',
    responseLost: 'Lost from the response',
    rejected: 'Account types refused',
    allRejected: 'All refused',
    secondHop: 'Paths prefixed 1: belong to the second hop ({hops})',
    lossy: 'Conversion lost content: {categories}',
    filters: { converted: 'Converted', lossy: 'With losses' }
  },
  converted: 'converted',
  convertedFrom: 'Converted from endpoint protocol {protocol}',
  free: 'Free',
  stream: 'Streaming',
  ips: {
    title: 'Client IPs',
    count: '{n} IPs, busiest first',
    top: 'The {n} busiest IPs',
    empty: 'No client IP recorded in this period',
    unknown: 'Not recorded',
    lastAt: 'Last call'
  },
  blocked: 'blocked',
  blockedBy: 'blocked by {plugin}',
  billing: {
    hash: 'hash {hash}…',
    len: 'len = {n}',
    title: 'Billing',
    price: 'Price rule',
    tier: 'Tier',
    listPrice: 'Model price',
    listCost: 'Cost at model price',
    rules: 'Markup rules',
    matched: 'matched',
    notMatched: 'not matched',
    inputs: 'Billing condition values',
    statusLabel: 'Billing status',
    ledger: 'ledger #{id}',
    components: {
      additional: 'Additional usage (each at its own model\'s price, included in the total)',
      replacement: 'Replacement usage (each attempt round priced on its own, billed instead of the primary usage, included in the total)'
    },
    componentKinds: { advisor: 'Advisor model' },
    componentRound: 'Round {round}',
    primaryUsage: 'Primary usage',
    componentsTotal: 'Total',
    freeNote: 'Requests rejected by hooks or failing upstream without usage are not billed.',
    status: {
      billed: 'Billed',
      pending: 'Pending',
      failed: 'Billing failed',
      free: 'Free'
    }
  },
  request: {
    title: 'Request',
    id: 'Request ID',
    clientId: 'Client request ID',
    log: 'Request log',
    clientIdNone: 'The client sent no X-Request-Id',
    endpoint: 'Endpoint',
    upstreamModel: 'Upstream model',
    apiKey: 'API key',
    clientIP: 'Client IP',
    userAgent: 'User-Agent',
    node: 'Node',
    attempts: 'Upstream attempts',
    latency: 'Latency',
    firstToken: 'first token',
    upload: 'upload',
    error: 'Error'
  },
  tokens: {
    title: 'Tokens',
    cacheDefaultBucket: 'Cache writes (default billing bucket)',
    cache1hBucket: 'Cache writes (1h billing bucket)',
    knownRound: 'Known usage for round {round}',
    incompleteRounds: 'These are recorded per-round counts only. Overall usage is unconfirmed and these counts are not the request total. Missing usage is not estimated; billing status is unchanged.'
  },
  hooks: {
    title: 'Hook decisions'
  },
  attempts: {
    title: 'Call chain',
    retried: 'Retried once | Retried {n} times',
    retriedShort: '{n} retry | {n} retries',
    step: 'Attempt {n}',
    accountRef: 'Account {n}',
    ok: 'Succeeded',
    failed: 'Failed',
    next: { same: 'Retried on the same account', switch: 'Retried on another account' },
    omitted: '{n} more attempts not listed',
    legacy: 'Recorded before call chains existed: only the attempt count is known',
    hint: 'Failed attempts are not billed; only the last attempt\'s usage is in this record.',
    mineHint: 'The request failed upstream and was retried automatically; failed attempts are not billed.',
    filterYes: 'Retried',
    filterNo: 'Not retried'
  },
  scheduling: {
    title: 'Account scheduling',
    seeBelow: 'See "Account scheduling" below for why each account was not used.',
    outcomes: {
      none: 'No account could serve',
      busy: 'All accounts busy or rate limited',
      picked: 'Picked after skipping accounts'
    },
    candidates: 'Schedulable accounts',
    eligible: 'Can serve this request',
    counts: 'By reason',
    skipped: 'Accounts not used',
    omitted: '{n} more accounts not listed',
    until: 'until {time}',
    hiddenAccount: 'No permission to view accounts',
    reasonWithDetail: '{reason}: {detail}',
    reasons: {
      not_schedulable: 'Account is not being scheduled',
      no_route: 'Account type does not serve this endpoint',
      model_not_served: 'Model not in the account\'s model list',
      referenced_model_not_served: 'Referenced model not in the account\'s model list',
      protocol_closed: 'Account does not open this protocol',
      conversion_not_enabled: 'Conversion is not turned on for the model',
      model_protocol_closed: 'Model is not served on this protocol',
      usage_unsupported: 'Account type cannot meter a usage the request asks for',
      bound_elsewhere: 'Request is bound to another account',
      availability_blocked: 'Blocked by the model availability precheck',
      failure_policy: 'Failure policy narrowed the retry',
      rank_excluded: 'A scheduling plugin set the weight to 0',
      conversion_refused: 'Protocol conversion unavailable or refused by the loss policy',
      rate_limited: 'Rate limited',
      concurrency_full: 'Concurrency full',
      queued: 'Requests are queueing for it',
      attempt_failed: 'Attempt failed, moved on'
    },
    details: {
      disabled: 'disabled',
      unschedulable: 'scheduling off',
      auto_disabled: 'automatically disabled',
      cooldown: 'cooling down',
      left: 'left scheduling during the request',
      resource: 'file resource',
      helper_history: 'helper history',
      fallback_credit: 'fallback credit',
      additional: 'additional usage',
      attempts: 'fallback attempt usage',
      same_account: 'retrying the same account only',
      proxy_changed: 'account proxy changed',
      egress: 'egress restriction'
    }
  },
  availability: {
    title: 'Model availability check',
    seeBelow: 'See "Model availability check" below for the reason.',
    outcomes: {
      blocked_all: 'All blocked',
      partial: 'Partly blocked',
      fail_open: 'Check failed, let through',
      pass: 'Precheck passed'
    },
    candidates: 'Candidates',
    admitted: 'Let through',
    retryAt: 'Earliest retry',
    sampleRetry: 'Retry at',
    afterRequest: '{time} after the request',
    counts: 'By reason',
    samples: 'Sample accounts',
    omitted: '{n} more accounts not listed',
    guard: 'Pre-send recheck',
    attempt: 'Attempt {n}',
    hiddenAccount: 'No permission to view accounts',
    batch: 'Whole batch of plugin {plugin}',
    reasonWithDetail: '{reason}: {detail}',
    decisions: {
      block: 'Blocked',
      fail_open: 'Let through'
    },
    reasons: {
      snapshot_unavailable: 'Quota snapshot unavailable',
      plugin_error: 'Plugin call failed',
      plugin_timeout: 'Plugin call timed out',
      plugin_bad_response: 'Plugin returned an invalid result',
      plugin_unknown: 'Plugin could not tell',
      unavailable: 'Unavailable',
      guard_identity_changed: 'Account was re-authorized',
      guard_models_changed: 'Account model list changed',
      upstream_model_mismatch: 'Upstream request model differs from the checked model'
    },
    pluginReasons: {
      quota_rejected: 'Quota window {window} exhausted',
      invalid_candidate: 'Invalid candidate',
      duplicate_candidate: 'Duplicate candidate',
      unsupported_account_type: 'Unsupported account type'
    }
  },
  sticky: {
    title: 'Sticky session',
    rule: 'Rule',
    hit: 'Binding',
    hitYes: 'hit',
    hitNo: 'miss'
  }
}
