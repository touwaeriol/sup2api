// Merging the settings tab into the stored settings (CONTRACTS §72.15). The
// core settings API has no version, and PUT replaces every value, so a save
// re-reads the stored values and writes back only the keys this page changed
// since it loaded: a key left alone keeps whatever is stored now, also when
// another admin changed it meanwhile. loaded and latest are the stored values
// brought to the current keys (settingsForm.ts currentSettings), so only
// accepted keys go back, retired values folded into their new keys.

/** A key's value as this page would send it; undefined = removed (its default applies). */
export type PageValues = Record<string, unknown>

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

export interface SettingsMerge {
  /** the values to PUT: latest with the changed keys of this page */
  values: Record<string, unknown>
  /** keys this page changed */
  changed: string[]
  /** changed keys someone else also changed since the page loaded (this page's value wins) */
  conflicts: string[]
}

/**
 * keys: this page's keys. loaded: the stored values when the page loaded;
 * latest: the stored values now; initial / current: the page's values as
 * sent, when it loaded and now.
 */
export function mergeSettings(
  keys: readonly string[],
  loaded: Record<string, unknown>,
  latest: Record<string, unknown>,
  initial: PageValues,
  current: PageValues
): SettingsMerge {
  const values: Record<string, unknown> = { ...latest }
  const changed: string[] = []
  const conflicts: string[] = []
  for (const k of keys) {
    if (same(initial[k], current[k])) continue
    changed.push(k)
    if (!same(loaded[k], latest[k]) && !same(latest[k], current[k])) conflicts.push(k)
    if (current[k] === undefined) delete values[k]
    else values[k] = current[k]
  }
  return { values, changed, conflicts }
}
