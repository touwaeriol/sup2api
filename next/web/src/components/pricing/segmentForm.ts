// The form of a named user group ("segment"): its name and description. A
// personal one (named after its user by the system) has no fields of its
// own; its account group rates are a part of their own (SegmentGroupsEditor)
// that the same request sends.
import type { Segment } from '@/api/pricing'

export interface SegmentForm {
  name: string
  description: string
}
export const SEGMENT_FIELDS = ['name', 'description'] as const

/** The form of `segment`; of a new one without it. */
export function formOf(segment: Segment | null): SegmentForm {
  return { name: segment?.name ?? '', description: segment?.description ?? '' }
}

const trimmed = (form: SegmentForm): SegmentForm => ({ name: form.name.trim(), description: form.description.trim() })

/** The fields of `form` that differ from the form of `segment` (of a new one: the empty form). */
function changes(form: SegmentForm, segment: Segment | null): Partial<SegmentForm> {
  const now = trimmed(form)
  const was = trimmed(formOf(segment))
  return Object.fromEntries(SEGMENT_FIELDS.filter((k) => now[k] !== was[k]).map((k) => [k, now[k]]))
}

/** What POST /segments sends of the form (all of it), or PATCH /segments/:id (what changed). */
export const segmentBody = (form: SegmentForm, segment: Segment | null): Partial<SegmentForm> => (segment ? changes(form, segment) : trimmed(form))

/** Whether the form differs from the segment it was opened on (of a new one: whether anything is typed). */
export const formChanged = (form: SegmentForm, segment: Segment | null): boolean => Object.keys(changes(form, segment)).length > 0
