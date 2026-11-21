import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { unsent, withDraft } from './drafts'
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
  it('leave, once some are saved, those made or changed after they were sent', () => {
    const sent = new Map([['a', { v: 1 }], ['b', { v: 2 }]])
    const now = new Map([['a', sent.get('a')!], ['b', { v: 3 }], ['c', { v: 4 }]])
    expect([...unsent(now, sent).keys()]).toEqual(['b', 'c'])
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

  it('sends nothing without drafts, and nothing while they cannot be saved (check says so first)', async () => {
    let problem: string | null = 'not yet'
    const { state } = setup(() => problem)
    const put = vi.fn(async () => ['saved'])
    expect([state.check(), await state.save(put)]).toEqual([true, true])
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    expect(state.check()).toBe(false)
    expect(await state.save(put)).toBe(false)
    expect(toast).toHaveBeenCalledWith('not yet', 'error')
    problem = null
    expect(await state.save(put)).toBe(true)
    expect(put).toHaveBeenCalledTimes(1)
    expect([state.saved.value, state.dirty.value]).toEqual([['saved'], false])
  })

  it('keeps the drafts when the save fails', async () => {
    const { state } = setup()
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    await expect(state.save(async () => Promise.reject(new Error('conflict')))).rejects.toThrow('conflict')
    expect(state.drafts.value.get('a')).toBe(1)
  })

  it('keeps what is edited while the save is under way, and its answer over a fetch still out', async () => {
    const { answers, state } = setup()
    state.drafts.value = withDraft(withDraft(state.drafts.value, 'a', 1), 'b', 2)
    const answer = deferred<string[]>()
    const put = vi.fn((_sent: ReadonlyMap<string, number>) => answer.promise)
    const saving = state.save(put)
    state.refresh()
    state.drafts.value = withDraft(withDraft(state.drafts.value, 'b', 3), 'c', 4)

    answer.resolve(['saved'])
    expect(await saving).toBe(true)
    answers[0].resolve(['stale'])
    await flushPromises()
    expect([...put.mock.calls[0][0]]).toEqual([['a', 1], ['b', 2]])
    expect([...state.drafts.value]).toEqual([['b', 3], ['c', 4]])
    expect(state.saved.value).toEqual(['saved'])
    expect(state.loading.value).toBe(false)
  })

  it('leaves out the answer of a save that comes after another object is loaded', async () => {
    const { answers, state } = setup()
    state.drafts.value = withDraft(state.drafts.value, 'a', 1)
    const answer = deferred<string[]>()
    const saving = state.save(() => answer.promise)
    state.load()
    state.drafts.value = withDraft(state.drafts.value, 'b', 2)

    answer.resolve(['of the one before'])
    expect(await saving).toBe(true)
    expect([...state.drafts.value]).toEqual([['b', 2]])
    expect([state.saved.value, state.loading.value]).toEqual([[], true])
    answers[0].resolve(['loaded'])
    await flushPromises()
    expect([state.saved.value, state.loading.value]).toEqual([['loaded'], false])
  })
})
