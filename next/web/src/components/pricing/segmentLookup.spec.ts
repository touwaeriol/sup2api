import { beforeEach, describe, expect, it, vi } from 'vitest'

const firstPage = vi.hoisted(() => vi.fn())
vi.mock('@/composables/lookups', () => ({ fetchFirstPage: firstPage }))

const named = [{ id: 1, kind: 'named', name: '默认' }]

describe('the lookup of the named user groups', () => {
  beforeEach(() => {
    vi.resetModules()
    firstPage.mockReset()
  })

  it('reads once for the selects, and afresh when forced', async () => {
    const renamed = [{ id: 1, kind: 'named', name: '默认组' }]
    firstPage.mockResolvedValueOnce(named).mockResolvedValue(renamed)
    const { useSegmentsLookup } = await import('./segmentLookup')
    const { segments, ready } = useSegmentsLookup()
    await ready
    expect(segments.value).toEqual(named)
    await useSegmentsLookup().ready
    expect(firstPage).toHaveBeenCalledTimes(1)
    await useSegmentsLookup(true).ready
    expect(segments.value).toEqual(renamed)
    expect(firstPage).toHaveBeenCalledTimes(2)
    expect(firstPage).toHaveBeenCalledWith('/segments')
  })

  it('asks again after a failed request', async () => {
    firstPage.mockRejectedValueOnce(new Error('down')).mockResolvedValue(named)
    const { useSegmentsLookup } = await import('./segmentLookup')
    await expect(useSegmentsLookup().ready).resolves.toBeUndefined()
    const { segments, ready } = useSegmentsLookup()
    await expect(ready).resolves.toBeUndefined()
    expect(segments.value).toEqual(named)
    expect(firstPage).toHaveBeenCalledTimes(2)
  })
})
