import { describe, expect, it } from 'vitest'
import { i18n } from '@/i18n'
import type { CondLeaf, CondVar } from './priceExpr'
import { roomFor, setLeafVar, tzOptions } from './tierCond'

const changed = (from: CondLeaf, to: CondVar) => {
  const leaf = { ...from }
  setLeafVar(leaf, to)
  return leaf
}

describe('changing what a comparison reads', () => {
  it('keeps the value between token amounts, and blanks it between a token amount and a time', () => {
    expect(changed({ var: 'len', op: '>', value: 200000 }, 'p')).toEqual({ var: 'p', op: '>', value: 200000 })
    expect(changed({ var: 'len', op: '>', value: 22 }, 'hour')).toEqual({ var: 'hour', op: '>', value: null, tz: 'Asia/Shanghai' })
    expect(changed({ var: 'hour', op: '>=', value: 22, tz: 'UTC' }, 'c')).toEqual({ var: 'c', op: '>=', value: null })
  })

  it('keeps the value and the zone between times while the new time takes the value, and blanks a value it does not take', () => {
    expect(changed({ var: 'hour', op: '>=', value: 22, tz: 'UTC' }, 'minute')).toEqual({ var: 'minute', op: '>=', value: 22, tz: 'UTC' })
    expect(changed({ var: 'minute', op: '>=', value: '45', tz: 'UTC' }, 'hour')).toEqual({ var: 'hour', op: '>=', value: null, tz: 'UTC' })
    expect(changed({ var: 'hour', op: '==', value: 0, tz: 'UTC' }, 'day')).toEqual({ var: 'day', op: '==', value: null, tz: 'UTC' })
    expect(changed({ var: 'day', op: '==', value: 6, tz: 'UTC' }, 'weekday')).toEqual({ var: 'weekday', op: '==', value: 6, tz: 'UTC' })
  })
})

describe('the room a condition has', () => {
  it('counts the levels from where it is, the root being level 1', () => {
    expect(roomFor(1, 5)).toBe(true)
    expect(roomFor(1, 6)).toBe(false)
    expect(roomFor(4, 2)).toBe(true)
    expect(roomFor(4, 3)).toBe(false)
  })
})

describe('the zones to pick from', () => {
  it('builds its groups once a language, and heads them with a zone neither lists', () => {
    i18n.global.locale.value = 'zh'
    const t = i18n.global.t
    const groups = tzOptions(t, 'Asia/Shanghai')
    expect(groups.map(g => g.label)).toEqual(['常用', '全部时区'])
    expect(tzOptions(t, 'UTC')).toBe(groups)
    expect(tzOptions(t, undefined)).toBe(groups)
    const [odd, ...rest] = tzOptions(t, 'Mars/Olympus')
    expect(odd).toEqual({ value: 'Mars/Olympus', label: 'Mars/Olympus' })
    expect(rest).toEqual(groups)
  })
})
