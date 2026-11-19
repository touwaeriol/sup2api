// Value helpers shared by the display components (STable, SDescItem,
// SCopyButton, SInput) and the forms.

/** True for undefined, null, '' and an empty array. */
export function isBlank(v: unknown): boolean {
  return v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0)
}

/** Text of a read-only value: '—' for undefined / null / '', JSON for objects. */
export function displayValue(v: unknown): string {
  if (v === undefined || v === null || v === '') return '—'
  return typeof v === 'object' ? JSON.stringify(v) : String(v)
}

/** Value at a dotted path ('a.b.0'), undefined when a step is missing. */
export function getPath(obj: unknown, path: string): any {
  return path.split('.').reduce<any>((o, k) => (o == null ? undefined : o[k]), obj)
}
