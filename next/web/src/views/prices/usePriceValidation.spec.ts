import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'
import { usePriceValidation, type PricePayload } from './usePriceValidation'

const post = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/host', async importOriginal => {
  const real = await importOriginal<typeof import('@sub2api/host')>()
  return { ...real, api: { ...real.api, post } }
})
enableAutoUnmount(afterEach)

/** Answers of /prices/validate, settled by the test in any order. */
function answers() {
  const pending: Array<(v: unknown) => void> = []
  post.mockImplementation(() => new Promise(resolve => pending.push(resolve)))
  return (i: number, expression: string) => pending[i]({ ok: true, expression, errors: [], warnings: [] })
}

function setup() {
  const payload = ref<PricePayload>({ mode: 'expression', config: {}, expression: 'p*1' })
  const issues = ref<string[]>([])
  let v!: ReturnType<typeof usePriceValidation>
  mount(defineComponent({ setup: () => ((v = usePriceValidation(payload, issues)), () => null) }))
  return { payload, issues, v }
}

describe('the server check of the price being edited', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('drops the answer to a price that has changed since', async () => {
    const answer = answers()
    const { payload, v } = setup()
    await vi.advanceTimersByTimeAsync(500)
    expect(v.validating.value).toBe(true)
    payload.value = { ...payload.value, expression: 'p*2' }
    await nextTick()
    expect(v.validating.value).toBe(false)
    answer(0, 'p*1')
    await flushPromises()
    expect(v.validation.value).toBeNull()
    await vi.advanceTimersByTimeAsync(500)
    answer(1, 'p*2')
    await flushPromises()
    expect(v.validation.value?.expression).toBe('p*2')
    expect(v.validating.value).toBe(false)
  })

  it('drops it when the next run is skipped, and on a reset', async () => {
    const answer = answers()
    const { payload, issues, v } = setup()
    await vi.advanceTimersByTimeAsync(500)
    issues.value = ['blank price']
    payload.value = { ...payload.value, expression: 'p*2' }
    await vi.advanceTimersByTimeAsync(500)
    answer(0, 'p*1')
    await flushPromises()
    expect([v.validation.value, v.validating.value]).toEqual([null, false])

    issues.value = []
    payload.value = { ...payload.value, expression: 'p*3' }
    await vi.advanceTimersByTimeAsync(500)
    v.reset()
    answer(1, 'p*3')
    await flushPromises()
    expect([v.validation.value, v.validating.value]).toEqual([null, false])
  })
})
