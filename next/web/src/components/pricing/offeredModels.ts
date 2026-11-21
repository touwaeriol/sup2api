// The models a group would provide with the accounts and the model policy
// being edited, before it is saved: what the server works out of the saved
// ones (group.Models.Offers, core.GroupInfo.AllowsModel). Kept alike with
// them, so a new group's price table lists what its saved one will.

/** The accounts of a group as its editor has them, and its model policy. */
export interface ModelOffer {
  /** The models the accounts declare. */
  models: string[]
  /** Accounts that declare none: they serve every model with an original price. */
  unrestricted_accounts: number
  model_allowlist: string[]
  model_filter_mode: 'whitelist' | 'blacklist'
}

/** `*` any run of characters, `?` one, as core.globMatch. */
export function globMatch(pattern: string, s: string): boolean {
  let [p, i, star, mark] = [0, 0, -1, 0]
  while (i < s.length) {
    if (p < pattern.length && (pattern[p] === '?' || pattern[p] === s[i])) [p, i] = [p + 1, i + 1]
    else if (p < pattern.length && pattern[p] === '*') [star, mark, p] = [p, i, p + 1]
    else if (star >= 0) [p, mark, i] = [star + 1, mark + 1, mark + 1]
    else return false
  }
  while (pattern[p] === '*') p++
  return p === pattern.length
}

/** Whether the model policy lets a model through: an empty list lets every model through. */
export function allowsModel(policy: Pick<ModelOffer, 'model_allowlist' | 'model_filter_mode'>, model: string): boolean {
  if (!policy.model_allowlist.length) return true
  const listed = policy.model_allowlist.some((pattern) => globMatch(pattern, model))
  return policy.model_filter_mode === 'blacklist' ? !listed : listed
}

/** The models provided, sorted: those the accounts declare, and every model of `priced` once an account declares none, through the policy. */
export function offeredModels(offer: ModelOffer, priced: readonly string[]): string[] {
  const all = new Set([...offer.models, ...(offer.unrestricted_accounts > 0 ? priced : [])])
  return [...all].filter((model) => allowsModel(offer, model)).sort()
}
