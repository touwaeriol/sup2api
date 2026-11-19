import { afterEach, describe, expect, it } from 'vitest'
import type { PluginHost } from '@sub2api/host'
import { mount } from '@vue/test-utils'
import { SInput, SSelect } from '@sub2api/ui'
import DurationInput from './DurationInput.vue'
import { joinDuration, splitDuration } from './duration'
import { setHost } from './host'
import { pageKeys, settingDefault, specOf } from './settingsForm'

describe('durations in seconds edited as a number and a unit', () => {
  it('shows the largest whole unit', () => {
    expect(splitDuration(10)).toEqual({ n: 10, unit: 's' })
    expect(splitDuration(600)).toEqual({ n: 10, unit: 'm' })
    expect(splitDuration(90)).toEqual({ n: 90, unit: 's' })
    expect(splitDuration(3600)).toEqual({ n: 1, unit: 'h' })
    expect(splitDuration(54000)).toEqual({ n: 15, unit: 'h' })
    expect(splitDuration(5400)).toEqual({ n: 90, unit: 'm' })
    expect(splitDuration(null)).toEqual({ n: null, unit: 'h' })
    // only the units offered
    expect(splitDuration(600, ['s', 'h'])).toEqual({ n: 600, unit: 's' })
  })

  it('converts the number with its unit', () => {
    expect(joinDuration(10, 's')).toBe(10)
    expect(joinDuration(10, 'm')).toBe(600)
    expect(joinDuration(15, 'h')).toBe(54000)
    expect(joinDuration(null, 'm')).toBeNull()
  })

  afterEach(() => setHost(null))

  it('applies the unit picked to the number typed', () => {
    setHost({ t: (k: string) => k } as unknown as PluginHost)
    const w = mount(DurationInput, { props: { modelValue: 600 } })
    expect(w.getComponent(SInput).props('modelValue')).toBe(10)
    expect(w.getComponent(SSelect).props('modelValue')).toBe('m')
    w.getComponent(SSelect).vm.$emit('update:modelValue', 'h')
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([36000])
    w.getComponent(SInput).vm.$emit('update:modelValue', 15)
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([54000])
    w.getComponent(SInput).vm.$emit('update:modelValue', null)
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual([null])
  })
})

describe('the escalation settings (D10)', () => {
  it('sit on the list rules page next to the ban threshold, off by default', () => {
    const keys = pageKeys('lists')
    expect(keys.slice(keys.indexOf('watch_days') + 1, keys.indexOf('ban_threshold') + 1)).toEqual(['escalate_violations', 'escalate_window_seconds', 'ban_threshold'])
    expect(settingDefault('escalate_violations')).toBe(0)
    expect(specOf('escalate_window_seconds').kind).toBe('duration')
  })
})
