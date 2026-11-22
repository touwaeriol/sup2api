import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { itemErrors, listRequest, withDraft } from './drafts'
import { useDrafts } from './useDrafts'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@sub2api/ui', async (original) => ({ ...(await original<object>()), toast }))

/** A promise the test settles. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => ((resolve = res), (reject = rej)))
  return { promise, resolve, reject }
}

describe('drafts by row key', () => {
  it('take a draft on and off a row', () => {
    const one = withDraft(new Map<string, number>(), 'a', 1)
    expect([...withDraft(one, 'b', 2)]).toEqual([['a', 1], ['b', 2]])
    expect(withDraft(one, 'a', undefined).size).toBe(0)
    expect(one.get('a')).toBe(1)
  })
  it('take the field errors of a list back to the rows that were sent at those places', () => {
    const sent = [{ model: 'a' }, { model: 'b' }]
    const errors = { 'model_prices.1.rule': 'bad', 'model_prices.7.rule': 'gone', 'model_rates.0.rate': 'other list', name: 'not a list' }
    expect([...itemErrors(errors, 'model_prices', sent, (i) => i.model)]).toEqual([['b', 'bad']])
  })
  it('send a list only with items, and take the errors back to what was sent then', () => {
    let items = [{ model: 'a' }, { model: 'b' }]
    const list = listRequest('model_rates', () => items, (i) => i.model)
    expect(list.body()).toEqual({ model_rates: items })
    items = []
    expect([...list.errors({ 'model_rates.1.rate': 'bad' })]).toEqual([['b', 'bad']])
    expect(list.body()).toEqual({})
  })
})

describe('an editor that keeps drafts', () => {
  beforeEach(() => toast.mockReset())

  function setup(problem: () => string | null = () => null) {
    const answers: Array<ReturnType<typeof deferred<string[]>>> = []
    const fetch = vi.fn(() => {
      answers.push(deferred<string[]>())
      return answers[answers.length - 1].promise
    })
    return { answers, fetch, state: useDrafts<string[], string, number>({ empty: () => [], fetch, problem }) }
  }

  it('takes the answer of the latest fetch only', async () => {
    const { answers, state } = setup()
    state.refresh()
    state.refresh()
    answers[1].resolve(['new'])
    answers[0].resolve(['old'])
    await flushPromises()
    expect(state.saved.value).toEqual(['new'])
    expect(state.loading.value).toBe(false)
  })

  it('keeps the saved state when a fetch fails, and says so', async () => {
    const { answers, state } = setup()
    state.refresh()
    answers[0].resolve(['a'])
    await flushPromises()
    state.refresh()
    answers[1].reject(new Error('down'))
    await flushPromises()
    expect(state.saved.value).toEqual(['a'])
    expect(state.error.value).toEqual(new Error('down'))
  })

  it('starts over on load: no drafts, nothing saved until the server answers', async () => {
    const { answers, state } = setup()
    state.refresh()
    answers[0].resolve(['a'])
    await flushPromises()
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    state.load()
    expect([state.saved.value, state.dirty.value]).toEqual([[], false])
  })

  it('can always be sent without drafts; with some, only once nothing is wrong (a toast says what)', () => {
    let problem: string | null = 'not yet'
    const { state } = setup(() => problem)
    expect(state.check()).toBe(true)
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    expect([state.dirty.value, state.check()]).toEqual([true, false])
    expect(toast).toHaveBeenCalledWith('not yet', 'error')
    problem = null
    expect(state.check()).toBe(true)
  })

  it('keeps the drafts on a refresh', async () => {
    const { answers, state } = setup()
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    state.refresh()
    answers[0].resolve(['fresh'])
    await flushPromises()
    expect([state.saved.value, [...state.drafts.value]]).toEqual([['fresh'], [['a', 1]]])
  })
})
