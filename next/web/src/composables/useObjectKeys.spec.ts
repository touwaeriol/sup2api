import { describe, expect, it } from 'vitest'
import { reactive, toRaw } from 'vue'
import { useObjectKeys } from './useObjectKeys'

describe('useObjectKeys', () => {
  it('keys each object once, and keeps the keys when the list changes', () => {
    const keyOf = useObjectKeys()
    const list = reactive([{ n: 1 }, { n: 2 }, { n: 3 }])
    const before = list.map(keyOf)
    expect(new Set(before).size).toBe(3)
    list.splice(0, 1)
    list.reverse()
    expect(list.map(keyOf)).toEqual([before[2], before[1]])
  })

  it('gives a reactive proxy its object’s key, and a copy a key of its own', () => {
    const keyOf = useObjectKeys()
    const item = reactive({ n: 1 })
    expect(keyOf(toRaw(item))).toBe(keyOf(item))
    expect(keyOf({ ...item })).not.toBe(keyOf(item))
  })
})
