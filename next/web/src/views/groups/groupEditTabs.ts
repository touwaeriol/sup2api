// The tabs of the group editor and the form fields each of them holds: an
// error on a field opens its tab, a changed field marks it. The prices tab
// also holds the price table, a sub-editor that keeps its own drafts.

export const GROUP_EDIT_TABS = ['basic', 'models', 'pricing', 'advanced'] as const
export type GroupEditTab = (typeof GROUP_EDIT_TABS)[number]

const TAB_FIELDS: Record<GroupEditTab, readonly string[]> = {
  basic: ['name', 'description', 'status', 'visibility'],
  models: ['model_allowlist', 'model_filter_mode'],
  pricing: ['rate_multiplier'],
  advanced: ['environment', 'conversion_loss_policy']
}

/** The first tab with a field in `errors`; a key may name a part of a field ("environment.HTTP_PROXY"). */
export function tabOfErrors(errors: Record<string, string>): GroupEditTab | undefined {
  const keys = Object.keys(errors)
  return GROUP_EDIT_TABS.find((tab) => TAB_FIELDS[tab].some((field) => keys.some((key) => key === field || key.startsWith(`${field}.`))))
}

/** The tabs whose fields differ between two states of the form. */
export function changedTabs(form: Record<string, unknown>, saved: Record<string, unknown>): GroupEditTab[] {
  return GROUP_EDIT_TABS.filter((tab) => TAB_FIELDS[tab].some((field) => JSON.stringify(form[field]) !== JSON.stringify(saved[field])))
}
