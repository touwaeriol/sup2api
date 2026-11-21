import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import { SDropdown, SSelect } from '@sub2api/ui'
import { i18n } from '@/i18n'
import TierCondTree from './TierCondTree.vue'
import { MAX_COND_LEAVES, whenExpr, type CondLeaf, type TierCond } from './priceExpr'
import { condText } from './tierCond'

enableAutoUnmount(afterEach)

function render(start: TierCond, disabled = false) {
  i18n.global.locale.value = 'zh'
  const model = ref(start)
  const w = mount(TierCondTree, {
    props: { modelValue: model.value, disabled, 'onUpdate:modelValue': (v: TierCond) => (model.value = v) },
    global: { plugins: [i18n] }
  })
  // A replaced root goes back in as the parent's v-model would put it.
  const sync = async () => {
    await w.setProps({ modelValue: model.value })
    await nextTick()
  }
  return { w, model, sync }
}

const len = (op: CondLeaf['op'], value: number): CondLeaf => ({ var: 'len', op, value })
/** The menu actions of a row or a group (the last dropdown of the node), by key. */
type Menu = VueWrapper<InstanceType<typeof SDropdown>>
const menuOf = (node: Pick<DOMWrapper<Element>, 'findAllComponents'>) => node.findAllComponents(SDropdown).at(-1)! as Menu
const keysOf = (dd: Menu) => (dd.props('actions') as Array<{ key: string; disabled?: boolean; hidden?: boolean }>).filter(a => !a.hidden).map(a => (a.disabled ? `-${a.key}` : a.key))
const pick = async (dd: Menu, key: string) => {
  dd.vm.$emit('select', key)
  await nextTick()
}
const LABELS = { var: '比较的量', tz: '时区', op: '比较符', value: '值', kind: '组合方式' } as const
const select = (w: VueWrapper, which: keyof typeof LABELS) => w.findAllComponents(SSelect).find(s => s.props('ariaLabel') === LABELS[which])!

describe('a tier condition tree', () => {
  it('edits a comparison in one row: what it reads, a time zone for a time, the operator and the value', async () => {
    const { w, model } = render(len('>', 200000))
    expect(w.findAll('[data-testid="cond-leaf"]')).toHaveLength(1)
    expect(select(w, 'tz')).toBeUndefined()
    expect((select(w, 'var').props('options') as Array<{ value: string }>).map(o => o.value)).toEqual(['len', 'p', 'c', 'hour', 'minute', 'weekday', 'month', 'day'])
    select(w, 'var').vm.$emit('update:modelValue', 'hour')
    await nextTick()
    expect(model.value).toEqual({ var: 'hour', op: '>', value: 200000, tz: 'Asia/Shanghai' })
    expect(select(w, 'tz').props('modelValue')).toBe('Asia/Shanghai')
    select(w, 'op').vm.$emit('update:modelValue', '>=')
    await w.get('input[aria-label="值"]').setValue('22')
    expect(whenExpr(model.value)).toBe('hour("Asia/Shanghai") >= 22')
    select(w, 'var').vm.$emit('update:modelValue', 'p')
    await nextTick()
    expect('tz' in model.value).toBe(false)
  })

  it('picks a weekday by its name, from Sunday', async () => {
    const { w, model } = render({ var: 'weekday', tz: 'UTC', op: '==', value: 0 })
    const day = select(w, 'value')
    expect((day.props('options') as Array<{ label: string }>).map(o => o.label)).toEqual(['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六'])
    day.vm.$emit('update:modelValue', 6)
    await nextTick()
    expect(model.value).toMatchObject({ value: 6 })
    expect(condText(i18n.global.t, model.value)).toBe('星期(UTC) = 星期六')
  })

  it('grows a comparison into an "all" group, adds conditions and groups, and switches and / or', async () => {
    const { w, model, sync } = render(len('>', 200000))
    // The root comparison is not removable.
    expect(keysOf(menuOf(w))).toEqual(['addCond', 'addGroup', 'negate'])
    await pick(menuOf(w), 'addCond')
    await sync()
    expect(model.value).toEqual({ and: [len('>', 200000), { var: 'len', op: '>', value: null }] })
    expect(w.get('[data-testid="cond-and"]').text()).toContain('组 1')
    await pick(w.findAllComponents(SDropdown)[0], 'addGroup')
    expect(model.value).toEqual({ and: [len('>', 200000), { var: 'len', op: '>', value: null }, { or: [{ var: 'len', op: '>', value: null }] }] })
    select(w, 'kind').vm.$emit('update:modelValue', 'or')
    await sync()
    expect(Object.keys(model.value)).toEqual(['or'])
    expect((model.value as { or: TierCond[] }).or).toHaveLength(3)
    // Its kids are removable now there are several; a group's only child is not.
    const rows = w.findAll('[data-testid="cond-leaf"]')
    expect(keysOf(menuOf(rows[0]))).toContain('remove')
    expect(keysOf(menuOf(rows[2]))).not.toContain('remove')
    await pick(menuOf(rows[0]), 'remove')
    expect((model.value as { or: TierCond[] }).or).toHaveLength(2)
  })

  it('negates a node and takes the negation off again', async () => {
    const { w, model, sync } = render(len('<=', 1000))
    await pick(menuOf(w), 'negate')
    await sync()
    expect(model.value).toEqual({ not: len('<=', 1000) })
    expect(w.get('[data-testid="cond-not"]').text()).toContain('取反')
    expect(keysOf(w.findAllComponents(SDropdown)[0])).toEqual(['unnegate'])
    await pick(w.findAllComponents(SDropdown)[0], 'unnegate')
    await sync()
    expect(model.value).toEqual(len('<=', 1000))
  })

  it('stops adding at 5 levels and at 20 comparisons', async () => {
    const deep: TierCond = { not: { not: { not: { and: [len('>', 1), len('<', 9)] } } } }
    const { w } = render(deep)
    const leaf = w.findAll('[data-testid="cond-leaf"]')[0]
    // A comparison at level 5 neither grows nor negates; its "all" group at level 4 takes a comparison but no group.
    expect(keysOf(menuOf(leaf))).toEqual(['-addCond', '-addGroup', '-negate', 'remove'])
    const group = w.get('[data-testid="cond-and"]')
    expect(keysOf(group.findAllComponents(SDropdown)[0])).toEqual(['addCond', '-addGroup'])

    const full = render({ and: Array.from({ length: MAX_COND_LEAVES }, (_, i) => len('>', i)) })
    expect(keysOf(full.w.findAllComponents(SDropdown)[0])).toEqual(['-addCond', '-addGroup'])
    expect(keysOf(menuOf(full.w.findAll('[data-testid="cond-leaf"]')[0]))).toEqual(['-addCond', '-addGroup', 'negate', 'remove'])
  })

  it('read-only, shows the tree and takes no edits', () => {
    const { w } = render({ and: [len('>', 200000), { or: [{ var: 'hour', tz: 'UTC', op: '>=', value: 22 }, { not: len('<', 5) }] }] }, true)
    expect(w.findAll('[data-testid="cond-leaf"]')).toHaveLength(3)
    expect(w.findAllComponents(SDropdown)).toHaveLength(0)
    expect(w.findAllComponents(SSelect).every(s => s.props('disabled'))).toBe(true)
    expect(w.findAll('input').every(i => i.attributes('disabled') !== undefined)).toBe(true)
  })
})
