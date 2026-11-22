import { describe, expect, it } from 'vitest'
import { modelRatePatches, modelRateRows, newModelRate, setModelRateDraft, type ModelRateDrafts, type SegmentModelRate } from './modelRateDrafts'

const saved: SegmentModelRate[] = [{ group_id: 2, model: 'claude-haiku-4-5', rate: '0.5' }]
const savedRow = () => modelRateRows(saved, new Map())[0]

describe('the model rates of a segment under their drafts', () => {
  it('read as saved without a draft', () => {
    expect(modelRateRows(saved, new Map())).toEqual([{ ...saved[0], key: '2:claude-haiku-4-5', saved: saved[0], removed: false, dirty: false, invalid: false }])
  })

  it('keep only a rate that differs from the saved one', () => {
    let drafts: ModelRateDrafts = setModelRateDraft(new Map(), savedRow(), '0.8')
    expect(modelRateRows(saved, drafts)[0]).toMatchObject({ rate: '0.8', dirty: true })
    drafts = setModelRateDraft(drafts, savedRow(), '0.50')
    expect(drafts.size).toBe(0)
  })

  it('remove a saved rate once saved, and mark a rate that is not one', () => {
    expect(modelRateRows(saved, setModelRateDraft(new Map(), savedRow(), null))[0]).toMatchObject({ removed: true, invalid: false, rate: '0.5' })
    expect(modelRateRows(saved, setModelRateDraft(new Map(), savedRow(), 'x'))[0]).toMatchObject({ invalid: true })
  })

  it('list a rate added after the saved ones, which goes again without a trace', () => {
    const added = newModelRate(1, 'gpt-4o')
    let drafts = setModelRateDraft(new Map(), added, '0.9')
    expect(modelRateRows(saved, drafts).map((r) => [r.key, r.rate, r.saved])).toEqual([
      ['2:claude-haiku-4-5', '0.5', saved[0]],
      ['1:gpt-4o', '0.9', null]
    ])
    // Being typed it stays; removed it goes.
    drafts = setModelRateDraft(drafts, added, '')
    expect(modelRateRows(saved, drafts)).toHaveLength(2)
    expect(setModelRateDraft(drafts, added, null).size).toBe(0)
  })

  it('become one patch per row, null removing the rate', () => {
    const drafts = setModelRateDraft(setModelRateDraft(new Map(), savedRow(), null), newModelRate(1, 'gpt-4o'), '0')
    expect(modelRatePatches(drafts)).toEqual([
      { group_id: 2, model: 'claude-haiku-4-5', rate: null },
      { group_id: 1, model: 'gpt-4o', rate: '0' }
    ])
  })
})
