import type { FilterChipItem, SelectOption } from '@sub2api/ui'

// Filters of the records tab (EventsTab): the select that stands for two
// query parameters (flow); and the chips of the applied filters, shared
// with the watch list tab.

/**
 * Options of a select that stands for two query parameters: each value is
 * "<first>:<option value>" (a flow id and a version).
 */
export const pairOptions = (first: string | number, options: SelectOption[]): SelectOption[] => options.map((o) => ({ ...o, value: `${first}:${o.value}` }))

function splitPair(v: string): [string, string] {
  const i = v.indexOf(':')
  return i < 0 ? ['', ''] : [v.slice(0, i), v.slice(i + 1)]
}

/** The flow select's value as its query: "<flow id>:<version>", an empty version for every version. */
export function flowQuery(v: string): { flow_id: number | ''; flow_version: number | '' } {
  const [id, version] = splitPair(v)
  return { flow_id: id ? Number(id) : '', flow_version: id && version ? Number(version) : '' }
}

/** The flow select's value of a query ('' without flow_id). */
export const flowValue = (q: { flow_id?: unknown; flow_version?: unknown }): string => (q.flow_id ? `${q.flow_id}:${q.flow_version || ''}` : '')

/** A filter field: a select (its options), else a text input or an entity picker (`text` names the picked id). */
export interface FilterField<K extends string = string> {
  key: K
  label: string
  options?: SelectOption[]
  text?: (value: string) => string
}

export const isSet = (v: unknown): boolean => v !== undefined && v !== null && String(v).trim() !== ''

/** The label of a select's value, "group · option" inside an optgroup; the value itself when no option has it. */
export function optionText(options: SelectOption[], value: unknown): string {
  for (const o of options) {
    const hit = o.options?.find((c) => String(c.value) === String(value))
    if (hit) return `${o.label} · ${hit.label}`
    if (!o.options && String(o.value) === String(value)) return o.label
  }
  return String(value)
}

/** What a chip says of a field's value: the option's label, the field's own text, else the value trimmed. */
function chipText(f: FilterField, value: unknown): string {
  if (f.options) return optionText(f.options, value)
  const text = String(value).trim()
  return f.text ? f.text(text) : text
}

/** Chips of the fields set in `values` (the applied filters, keyed like the fields), in field order. */
export function filterChips(fields: FilterField[], values: Record<string, unknown>): FilterChipItem[] {
  return fields.filter((f) => isSet(values[f.key])).map((f) => ({ key: f.key, label: f.label, value: chipText(f, values[f.key]) }))
}
