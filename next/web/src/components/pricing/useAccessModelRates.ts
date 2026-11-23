import { useTabbedFormPart } from '@sub2api/ui'
import type { SettingSide } from './accessRateDrafts'
import type { useAccessRateDrafts } from './useAccessRateDrafts'
import { useModelRateDrafts } from './useModelRateDrafts'

/**
 * The model rates under the rows of an editor of grants and rates (the rows
 * differ in `side`, the far end of the model rates), and the part of tab
 * `tab` of the enclosing tabbed form both lists make while `active`: one
 * request sends them, and each table marks what is wrong in it.
 */
export function useAccessModelRates(access: ReturnType<typeof useAccessRateDrafts>, side: SettingSide, tab: string, active: () => boolean) {
  const ends = () => access.saved.value.map((s) => s[side])
  const modelRates = useModelRateDrafts({
    far: side === 'group' ? 'group_id' : 'segment_id',
    saved: () => access.saved.value.flatMap((s) => s.model_rates.map((r) => ({ far: s[side].id, ...r }))),
    farName: (id) => ends().find((e) => e.id === id)?.name ?? `#${id}`
  })
  useTabbedFormPart(
    {
      tab,
      // Both run, so each table marks what is wrong.
      check: () => [access.check(), modelRates.check()].every(Boolean),
      body: () => ({ ...access.body(), ...modelRates.body() }),
      dirty: () => access.dirty.value || modelRates.dirty.value,
      fields: [...access.fields, modelRates.field],
      applyErrors: (errors) => [access.applyErrors(errors), modelRates.applyErrors(errors)]
    },
    active
  )
  return modelRates
}
