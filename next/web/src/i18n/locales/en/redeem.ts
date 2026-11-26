// Redeem codes (core, Top-up & payment > Redeem codes; Mine > Wallet redeem card).
export default {
  title: 'Redeem codes',
  description: 'Generate codes in batches for users to redeem in their wallet; each adds its value to the balance. Plain codes are shown once, when generated.',
  empty: 'No redeem codes',
  generate: 'Generate',
  anyStatus: 'All statuses',
  never: 'Never',
  void: 'Void',
  voidTitle: 'Void this redeem code?',
  voidConfirm: '{code} (worth {value}) can no longer be redeemed. This cannot be undone.',
  voided: 'Redeem code voided',
  status: { unused: 'Unused', used: 'Used', voided: 'Voided', expired: 'Expired' },
  cols: { code: 'Code', value: 'Value', status: 'Status', used_by: 'Used by', used_at: 'Used at', expires_at: 'Expires', notes: 'Notes', created_at: 'Created' },
  form: {
    title: 'Generate redeem codes',
    submit: 'Generate',
    count: 'Count',
    countHint: '1 to 1000 at a time',
    value: 'Value',
    valueHint: 'Balance credited on redemption, at most the per-credit limit',
    expires: 'Expires',
    expiresHint: 'Leave empty for never',
    future: 'Pick a time in the future',
    positive: 'Must be more than 0',
    notes: 'Notes',
    notesPlaceholder: 'e.g. Singles Day campaign'
  },
  result: {
    title: '{n} redeem codes generated',
    warn: 'The plain codes are not shown again after closing: copy or download them first.',
    copyAll: 'Copy all',
    download: 'Download txt',
    done: 'I saved them'
  },
  card: {
    title: 'Redeem code',
    subtitle: 'Add funds with a code',
    placeholder: 'Enter a redeem code',
    submit: 'Redeem',
    required: 'Enter a redeem code',
    success: '{amount} credited',
    failed: 'Could not redeem the code'
  }
}
