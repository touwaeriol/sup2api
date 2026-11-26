import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { latestOnly, useLatestRequest } from './useLatestRequest'

/** A `load` whose answers the test hands out one by one. */
function deferredLoad() {
  const pending: Array<{ input: string; resolve: (v: string) => void; reject: (e: unknown) => void }> = []
  const load = vi.fn((input: string) => new Promise<string>((resolve, reject) => pending.push({ input, resolve, reject })))
  return { load, pending }
}

describe('latest request', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  function setup(initial: string | null) {
    const input = ref(initial)
    const { load, pending } = deferredLoad()
    const scope = effectScope()
    const state = scope.run(() => useLatestRequest(() => input.value, load, 300))!
    return { input, load, pending, scope, ...state }
  }

  it('asks once the input has settled, for the latest input only', async () => {
    const { input, load, pending, result, loading } = setup('a')
    expect(loading.value).toBe(true)
    await vi.advanceTimersByTimeAsync(200)
    input.value = 'ab'
    await vi.advanceTimersByTimeAsync(200)
    expect(load).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(100)
    expect(load.mock.calls).toEqual([['ab']])

    pending[0].resolve('answer to ab')
    await vi.advanceTimersByTimeAsync(0)
    expect(result.value).toBe('answer to ab')
    expect(loading.value).toBe(false)
  })

  it('drops the answer of an input that is no longer the latest and keeps the last one meanwhile', async () => {
    const { input, pending, result, loading } = setup('a')
    await vi.advanceTimersByTimeAsync(300)
    pending[0].resolve('answer to a')
    await vi.advanceTimersByTimeAsync(0)

    input.value = 'b'
    await vi.advanceTimersByTimeAsync(300)
    input.value = 'c'
    await vi.advanceTimersByTimeAsync(300)
    pending[1].resolve('answer to b')
    await vi.advanceTimersByTimeAsync(0)
    expect(result.value).toBe('answer to a')
    expect(loading.value).toBe(true)

    pending[2].resolve('answer to c')
    await vi.advanceTimersByTimeAsync(0)
    expect(result.value).toBe('answer to c')
    expect(loading.value).toBe(false)
  })

  it('asks nothing for a null input and forgets the last answer', async () => {
    const { input, load, pending, result, loading } = setup('a')
    await vi.advanceTimersByTimeAsync(300)
    pending[0].resolve('answer to a')
    await vi.advanceTimersByTimeAsync(0)

    input.value = null
    await nextTick()
    expect(result.value).toBeNull()
    expect(loading.value).toBe(false)
    await vi.advanceTimersByTimeAsync(300)
    expect(load).toHaveBeenCalledTimes(1)
  })

  it('reports a failure and clears it on the next input', async () => {
    const { input, pending, result, error } = setup('a')
    await vi.advanceTimersByTimeAsync(300)
    pending[0].reject(new Error('boom'))
    await vi.advanceTimersByTimeAsync(0)
    expect(error.value).toBe('boom')
    expect(result.value).toBeNull()

    input.value = 'b'
    await nextTick()
    expect(error.value).toBe('')
  })

  it('asks again for the latest input on reload', async () => {
    const { load, pending, result, error, reload } = setup('a')
    await vi.advanceTimersByTimeAsync(300)
    pending[0].reject(new Error('boom'))
    await vi.advanceTimersByTimeAsync(0)
    expect(error.value).toBe('boom')

    reload()
    expect(error.value).toBe('')
    await vi.advanceTimersByTimeAsync(0)
    expect(load).toHaveBeenLastCalledWith('a')
    pending[1].resolve('A')
    await vi.advanceTimersByTimeAsync(0)
    expect(result.value).toBe('A')
  })

  it('stops asking when its scope ends', async () => {
    const { load, scope } = setup('a')
    scope.stop()
    await vi.advanceTimersByTimeAsync(300)
    expect(load).not.toHaveBeenCalled()
  })
})

describe('latest only', () => {
  it('holds for a request until the next one starts, each row of requests on its own', () => {
    const begin = latestOnly()
    const first = begin()
    expect(first()).toBe(true)
    const second = begin()
    expect([first(), second()]).toEqual([false, true])

    const other = latestOnly()()
    expect([second(), other()]).toEqual([true, true])
  })
})
