// Codex UI messages, registered under plugin.codex.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  // tabs.CodexResetTab is the account detail tab's label (plugin.codex.tabs.<component>).
  tabs: { CodexResetTab: '官方重置' },
  intro: {
    device: '在任意设备的浏览器里打开 OpenAI 验证页并输入验证码，授权后自动完成。与 Codex CLI 设备码登录相同。',
    oauth: '与 Codex CLI 的浏览器登录相同。在浏览器里登录 ChatGPT 并同意后，把跳转后的网址粘回这里。'
  },
  current: {
    title: '当前授权',
    email: '邮箱',
    expires: '令牌过期',
    expired: '已过期（会自动刷新）',
    accountId: 'ChatGPT 账号 ID',
    unknown: '—',
    reauth: '重新授权',
    cancelReauth: '取消重新授权',
    reauthHint: '重新授权成功后保存账号才会替换令牌；未完成时保存，保留原有凭证。',
    kept: '未重新授权，保存时保留原有凭证。'
  },
  agent: {
    title: '客户端标识',
    userAgent: 'User-Agent',
    userAgentHint: '这个账号发往 OpenAI 的全部请求（授权、上游、测试、令牌刷新、用量重置）使用的 User-Agent，授权时用这里当前填的值。留空沿用插件设置，插件设置也留空时为 sup2api-codex/插件版本。',
    originator: 'originator',
    originatorHint: '同上，originator 请求头。留空沿用插件设置，插件设置也留空时为 sup2api。',
    invalid: '不能包含制表符以外的控制字符'
  },
  settings: {
    userAgent: 'User-Agent',
    userAgentHint: '未自定义 User-Agent 的账号发往 OpenAI 的全部请求（授权、上游、账号测试、令牌刷新、用量重置）使用的 User-Agent 请求头；授权时账号表单里填了值就用表单的值。留空使用内置的 sup2api-codex/<插件版本>。',
    originator: 'originator',
    originatorHint: '未自定义 originator 的账号在上述请求中使用的 originator 请求头。留空使用内置的 sup2api。'
  },
  done: {
    title: '已授权',
    titleReauth: '已重新授权，保存账号后生效',
    detail: '{email} · {plan}',
    noEmail: '已获取令牌',
    again: '重新授权'
  },
  device: {
    start: '开始',
    step1: '打开验证页面',
    step2: '输入验证码',
    codeLabel: '验证码',
    copyCode: '复制验证码',
    waiting: '等待授权…',
    expiresIn: '{time} 后过期',
    expired: '验证码已过期，请重新开始。',
    failed: '授权失败',
    retry: '重新开始',
    cancel: '取消',
    copyLink: '复制链接',
    guideTitle: '先在 ChatGPT 开启设备代码登录',
    guidePath: '打开 ChatGPT 设置 → 账户安全与登录 → 应用安全，打开“为 Codex、Excel、PowerPoint 和 Word 启用设备代码登录”。企业工作区需管理员允许。',
    guideOpen: '打开 ChatGPT 安全设置'
  },
  oauth: {
    generate: '生成授权链接',
    regenerate: '重新生成链接',
    linkLabel: '授权链接',
    copyLink: '复制链接',
    openLink: '在新窗口打开',
    step1: '在浏览器中打开授权链接，登录 ChatGPT 并同意授权',
    step2: '粘贴跳转后的完整网址',
    redirectNote: '同意后浏览器会跳到一个 localhost:1455 开头、打不开的页面，这是正常的。把地址栏里的完整网址复制下来，粘到下面。',
    callbackLabel: '回调网址',
    callbackPlaceholder: 'http://localhost:1455/auth/callback?code=…',
    complete: '完成授权',
    shape: {
      invalid: '无法解析这段内容（含有非法的 % 转义或分号），请粘贴地址栏里的完整网址。',
      denied: '这个网址带有 error 参数：授权被拒绝或失败，请重新生成链接再试。',
      no_code: '网址里没有 code 参数，请确认复制的是同意授权后跳转到的地址。'
    },
    failed: '授权失败'
  },
  plan: {
    free: 'Free',
    go: 'Go',
    plus: 'Plus',
    pro: 'Pro',
    team: 'Team',
    business: 'Business',
    enterprise: 'Enterprise',
    edu: 'Edu'
  },
  validate: {
    required: '请先完成授权，拿到令牌后再保存。',
    inProgress: '授权还没完成：完成后再保存。'
  },
  reset: {
    available: '次官方重置可用',
    useNext: '使用一次重置',
    use: '使用',
    hint: 'OpenAI 发给你账号的 Codex 用量重置次数：用一次立即清零当前的 5 小时与周用量，账号马上恢复可用；过期作废。只能手动使用。',
    fullHint: '当前用量已用满，账号被暂停到窗口重置；使用一次重置可立即恢复。',
    window5h: '5 小时',
    window7d: '周',
    resetsAt: '重置于',
    noUsage: '还没有用量数据',
    usageAwaiting: '用量会在账号下一次请求后更新',
    expires: '过期于',
    noExpiry: '不过期',
    granted: '发放于',
    defaultTitle: '完整重置',
    defaultDescription: '清零当前的用量限制',
    status: { available: '可用', redeeming: '使用中', redeemed: '已使用' },
    emptyTitle: '还没有重置卡',
    emptyText: 'OpenAI 会在推荐好友成功等活动中发放重置次数，发放后会显示在这里。',
    loadFailed: '读取重置次数失败',
    needsUpdate: '查看和使用重置次数需要修改该账号的权限（account:update）。',
    confirm: {
      title: '使用一次官方重置？',
      body: '将消耗账号 {name} 的 1 次重置次数（{title}），立即清零当前用量窗口。',
      usage: '当前用量：{usage}',
      notFull: '当前用量还没用满：OpenAI 可能回复“无需重置”且不扣次数。',
      final: '次数用掉后不能撤回。'
    },
    done: '已重置 {n} 个用量窗口，账号已恢复可用。',
    doneNotRestored: '重置已生效，但账号的自动暂停没能解除，请在账号列表里点“重置状态”。',
    failed: '使用重置失败',
    badge: '重置 ×{n}',
    badgeTitle: '官方重置次数 {n}，点开查看明细',
    modalTitle: '官方重置 · {name}'
  },
  actionFailed: '操作失败'
}

type Messages = typeof zh

const en: Messages = {
  tabs: { CodexResetTab: 'Official resets' },
  intro: {
    device: 'Open the OpenAI verification page in a browser on any device and enter the code; this form finishes by itself once you approve. Same as the Codex CLI device code login.',
    oauth: "Same as the Codex CLI's browser sign-in. Sign in to ChatGPT and consent in your browser, then paste the address you land on back here."
  },
  current: {
    title: 'Current authorization',
    email: 'Email',
    expires: 'Token expires',
    expired: 'Expired (refreshed automatically)',
    accountId: 'Account ID',
    unknown: '—',
    reauth: 'Re-authorize',
    cancelReauth: 'Cancel re-authorization',
    reauthHint: 'The tokens are replaced when you save the account after re-authorizing; saving before that keeps the current credentials.',
    kept: 'Not re-authorized: saving keeps the current credentials.'
  },
  agent: {
    title: 'Client identification',
    userAgent: 'User-Agent',
    userAgentHint: "The User-Agent of every request of this account to OpenAI (authorization, upstream, test, token refresh, usage resets); an authorization uses the value entered here at that moment. Leave empty for the plugin settings' value, or sup2api-codex/plugin version when that is empty too.",
    originator: 'originator',
    originatorHint: "The same for the originator header. Leave empty for the plugin settings' value, or sup2api when that is empty too.",
    invalid: 'Must not contain control characters other than tab'
  },
  settings: {
    userAgent: 'User-Agent',
    userAgentHint: "The User-Agent header of every request to OpenAI (authorization, upstream, account test, token refresh, usage resets) of the accounts that set none of their own; an authorization uses the account form's value when it has one. Leave empty for the built-in sup2api-codex/<plugin version>.",
    originator: 'originator',
    originatorHint: 'The originator header of the same requests, for the accounts that set none of their own. Leave empty for the built-in sup2api.'
  },
  done: {
    title: 'Authorized',
    titleReauth: 'Re-authorized: save the account to apply',
    detail: '{email} · {plan}',
    noEmail: 'Tokens received',
    again: 'Authorize again'
  },
  device: {
    start: 'Start',
    step1: 'Open the verification page',
    step2: 'Enter the code',
    codeLabel: 'Code',
    copyCode: 'Copy code',
    waiting: 'Waiting for approval…',
    expiresIn: 'Expires in {time}',
    expired: 'The code has expired. Start again.',
    failed: 'Authorization failed',
    retry: 'Start again',
    cancel: 'Cancel',
    copyLink: 'Copy link',
    guideTitle: 'Turn on device code sign-in in ChatGPT first',
    guidePath: 'Open ChatGPT Settings → Security and login → App security and turn on "Enable device code sign-in for Codex, Excel, PowerPoint, and Word". A workspace admin has to allow it for business workspaces.',
    guideOpen: 'Open ChatGPT security settings'
  },
  oauth: {
    generate: 'Generate sign-in link',
    regenerate: 'Generate a new link',
    linkLabel: 'Sign-in link',
    copyLink: 'Copy link',
    openLink: 'Open in a new window',
    step1: 'Open the link in your browser, sign in to ChatGPT and consent',
    step2: 'Paste the full address you were sent to',
    redirectNote: 'After you consent, the browser goes to a page starting with localhost:1455 that does not load. That is expected: copy the full address from the address bar and paste it below.',
    callbackLabel: 'Callback address',
    callbackPlaceholder: 'http://localhost:1455/auth/callback?code=…',
    complete: 'Complete sign-in',
    shape: {
      invalid: 'This cannot be read (a malformed % escape or a semicolon). Paste the full address from the address bar.',
      denied: 'This address carries an error parameter: consent was refused or failed. Generate a new link and try again.',
      no_code: 'The address has no code parameter. Make sure you copied the address you landed on after consenting.'
    },
    failed: 'Sign-in failed'
  },
  plan: {
    free: 'Free',
    go: 'Go',
    plus: 'Plus',
    pro: 'Pro',
    team: 'Team',
    business: 'Business',
    enterprise: 'Enterprise',
    edu: 'Edu'
  },
  validate: {
    required: 'Finish the authorization first: save once the tokens are in.',
    inProgress: 'The authorization is not finished yet: save once it is.'
  },
  reset: {
    available: 'official resets available',
    useNext: 'Use a reset',
    use: 'Use',
    hint: "OpenAI's Codex usage resets granted to your account: one clears the current 5-hour and weekly usage at once and the account is usable again right away. They expire. Only used when you press the button.",
    fullHint: 'The usage is used up and the account is paused until the window resets; a reset brings it back now.',
    window5h: '5-hour',
    window7d: 'Weekly',
    resetsAt: 'resets',
    noUsage: 'No usage seen yet',
    usageAwaiting: "Usage updates after the account's next request",
    expires: 'Expires',
    noExpiry: 'Does not expire',
    granted: 'granted',
    defaultTitle: 'Full reset',
    defaultDescription: 'Reset your current usage limits',
    status: { available: 'Available', redeeming: 'In use', redeemed: 'Used' },
    emptyTitle: 'No resets yet',
    emptyText: 'OpenAI grants resets in promotions such as a successful referral; they show up here once granted.',
    loadFailed: 'Could not read the resets',
    needsUpdate: 'Viewing and using the resets needs the permission to update this account (account:update).',
    confirm: {
      title: 'Use an official reset?',
      body: 'This spends 1 reset of account {name} ({title}) and clears the current usage windows at once.',
      usage: 'Current usage: {usage}',
      notFull: 'The usage is not used up: OpenAI may answer that there is nothing to reset, and then no reset is spent.',
      final: 'A spent reset cannot be taken back.'
    },
    done: '{n} usage window(s) reset; the account is usable again.',
    doneNotRestored: 'The reset took effect, but the account could not be resumed: use "Reset status" in the account list.',
    failed: 'Could not use the reset',
    badge: 'Resets ×{n}',
    badgeTitle: '{n} official reset(s): open for the details',
    modalTitle: 'Official resets · {name}'
  },
  actionFailed: 'Action failed'
}

export default { zh, en }
