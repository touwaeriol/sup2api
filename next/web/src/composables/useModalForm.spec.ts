import { describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { ApiError } from '@sub2api/host'
import { useModalForm } from './useModalForm'

const { notifyError } = vi.hoisted(() => ({ notifyError: vi.fn() }))
vi.mock('@/utils/errors', async orig => ({ ...(await orig<object>()), notifyError }))

const invalid = (field: string) =>
  new ApiError(400, { code: 'invalid_argument', message: 'bad', details: { fields: [{ field, message: 'taken' }] } })

function setup(strip?: string) {
  const scope = effectScope()
  return scope.run(() => useModalForm(() => ({ name: '' }), { name: { required: true } }, { strip }))!
}

describe('modal form', () => {
  it('runs only a valid value, unless the caller already validated it', async () => {
    const f = setup()
    const run = vi.fn(async () => 'ok')
    expect(await f.submit(run)).toBeUndefined()
    expect(run).not.toHaveBeenCalled()
    expect(await f.submit(run, { validated: true })).toBe('ok')

    f.reset({ name: 'a' })
    expect(await f.submit(run)).toBe('ok')
    expect(run).toHaveBeenLastCalledWith({ name: 'a' })
  })

  it('shows field errors at their fields, without the prefix the server puts before them', async () => {
    const f = setup('values.')
    f.reset({ name: 'a' })
    expect(await f.submit(() => Promise.reject(invalid('values.name')))).toBeUndefined()
    expect(f.form.error('name')).toBe('taken')
    expect(notifyError).not.toHaveBeenCalled()
  })

  it('shows any other refusal as a toast and starts over clean on reset', async () => {
    const f = setup()
    f.reset({ name: 'a' })
    const boom = new Error('boom')
    await f.submit(() => Promise.reject(boom))
    expect(notifyError).toHaveBeenCalledWith(boom)

    await f.submit(() => Promise.reject(invalid('name')))
    f.reset()
    expect(f.form.error('name')).toBeUndefined()
    expect(f.model.value).toEqual({ name: '' })
  })
})
