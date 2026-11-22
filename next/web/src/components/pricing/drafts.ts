// Unsaved edits by row key, as plain data: what the editors that keep drafts
// (useDrafts.ts) and the modules that say what a draft is (pricingRows.ts,
// accessRateDrafts.ts, modelRateDrafts.ts) share.

/** The drafts with `draft` on `key`; undefined takes the draft of the row away: it reads as saved again. */
export function withDraft<K, D>(drafts: ReadonlyMap<K, D>, key: K, draft: D | undefined): ReadonlyMap<K, D> {
  const next = new Map(drafts)
  if (draft === undefined) next.delete(key)
  else next.set(key, draft)
  return next
}

/**
 * The server's field errors on the items of the list `name` a request sent,
 * by the key of the item at that position (`model_prices.2.rule` names
 * sent[2]); the first error of an item wins.
 */
export function itemErrors<T, K>(errors: Record<string, string>, name: string, sent: readonly T[], keyOf: (item: T) => K): Map<K, string> {
  const out = new Map<K, string>()
  for (const [field, message] of Object.entries(errors)) {
    const item = sent[Number(new RegExp(`^${name}\\.(\\d+)`).exec(field)?.[1] ?? -1)]
    if (item !== undefined && !out.has(keyOf(item))) out.set(keyOf(item), message)
  }
  return out
}

/**
 * A list of a request made of drafts: body() is what the request sends of it
 * (nothing while `patches` gives no item) and keeps what it sent, so that
 * errors() can take the field errors of the answer back to the rows, by
 * `keyOf` of the item they name.
 */
export function listRequest<T, K>(name: string, patches: () => T[], keyOf: (item: T) => K) {
  let sent: T[] = []
  return {
    body(): Record<string, T[]> {
      sent = patches()
      return sent.length ? { [name]: sent } : {}
    },
    errors: (errors: Record<string, string>) => itemErrors(errors, name, sent, keyOf)
  }
}
