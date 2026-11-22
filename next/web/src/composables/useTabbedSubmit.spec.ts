import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import type { TabbedForm } from '@sub2api/ui'
import { useTabbedSubmit } from './useTabbedSubmit'

const h = vi.hoisted(() => ({ notifyError: vi.fn(), discard: true, asked: [] as boolean[] }))
vi.mock('@/utils/errors', () => ({ notifyError: h.notifyError }))
vi.mock('./confirmDiscard', () => ({ confirmDiscard: async (dirty: boolean) => (h.asked.push(dirty), !dirty || h.discard) }))

type Submit = (send: (body: Record<string, unknown>) => Promise<unknown>) => Promise<unknown>

function setup(submit: Submit, dirty = false) {
  const tabbed = { submit, anyDirty: ref(dirty) } as unknown as TabbedForm
  const options = { send: vi.fn(), done: vi.fn(), close: vi.fn() }
  return { options, ...useTabbedSubmit(tabbed, options) }
}

beforeEach(() => {
  h.discard = true
  h.asked = []
})

describe('useTabbedSubmit', () => {
  it('sends through the form and finishes after a save', async () => {
    const s = setup(async (send) => void (await send({ a: 1 })))
    expect(await s.submit()).toBe(true)
    expect(s.options.send).toHaveBeenCalledWith({ a: 1 })
    expect(s.options.done).toHaveBeenCalledOnce()
  })
  it('keeps the editor open when the form refuses the save', async () => {
    const s = setup(async () => false)
    expect(await s.submit()).toBe(false)
    expect(s.options.done).not.toHaveBeenCalled()
    expect(h.notifyError).not.toHaveBeenCalled()
  })
  it('reports any other failure', async () => {
    const err = new Error('down')
    const s = setup(async () => Promise.reject(err))
    expect(await s.submit()).toBe(false)
    expect(h.notifyError).toHaveBeenCalledWith(err)
    expect(s.options.done).not.toHaveBeenCalled()
  })
  it('asks before leaving unsaved changes', async () => {
    const clean = setup(async () => undefined)
    await clean.leave()
    expect(clean.options.close).toHaveBeenCalledOnce()
    h.discard = false
    const dirty = setup(async () => undefined, true)
    await dirty.leave()
    expect(h.asked).toEqual([false, true])
    expect(dirty.options.close).not.toHaveBeenCalled()
  })
})
