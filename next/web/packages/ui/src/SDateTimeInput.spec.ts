import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { h, ref } from 'vue'
import SDateTimeInput from './SDateTimeInput.vue'
import SField from './SField.vue'
import { toLocalInput } from './timeRange'

describe('SDateTimeInput', () => {
  function box(initial?: string) {
    const value = ref<string | undefined>(initial)
    const emitted: unknown[] = []
    const w = mount(() =>
      h(SField, { label: 'Expires' }, () =>
        h(SDateTimeInput, { modelValue: value.value, placeholder: 'when', 'onUpdate:modelValue': (v: string | undefined) => (emitted.push(v), (value.value = v)) })
      )
    )
    return { w, value, emitted, input: w.get('input') }
  }

  it('shows an RFC 3339 value in local time, labelled by its field', async () => {
    const { w, input } = box('2026-11-19T10:00:00Z')
    await flushPromises()
    expect(input.attributes('type')).toBe('datetime-local')
    expect((input.element as HTMLInputElement).value).toBe(toLocalInput('2026-11-19T10:00:00Z'))
    expect(input.attributes('placeholder')).toBe('when')
    expect(w.get('label').attributes('for')).toBe(input.attributes('id'))
  })

  it('emits a picked time as RFC 3339, a cleared box as undefined (the key is not sent)', async () => {
    const { value, emitted, input } = box()
    await input.setValue('2026-11-19T10:00')
    expect(value.value).toBe(new Date('2026-11-19T10:00').toISOString())
    await input.setValue('')
    expect(value.value).toBeUndefined()
    expect(emitted.at(-1)).toBeUndefined()
  })
})
