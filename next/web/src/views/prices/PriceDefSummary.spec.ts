import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { i18n } from '@/i18n'
import type { PriceSyncDef } from '@/api/types'
import PriceDefSummary from './PriceDefSummary.vue'
import { PRESETS } from './presets'

enableAutoUnmount(afterEach)

const def = (config: object): PriceSyncDef => ({ mode: 'visual', config, expression: '' })
const highlighted = (w: ReturnType<typeof mount>) => w.findAll('.font-semibold').map(s => s.text())

function render(current: PriceSyncDef, compare: PriceSyncDef) {
  i18n.global.locale.value = 'en'
  return mount(PriceDefSummary, { props: { def: current, compare }, global: { plugins: [i18n] } })
}

describe('the summary of a price definition', () => {
  it('highlights a tier whose condition changed, its prices the same', () => {
    const tiers = PRESETS.claudeLong()
    const moved = PRESETS.claudeLong()
    moved[0].when = { var: 'len', op: '<=', value: 272000 }
    const w = render(def({ tiers: moved, rules: [] }), def({ tiers, rules: [] }))
    expect(highlighted(w)).toHaveLength(1)
    expect(highlighted(w)[0]).toContain('standard')
    expect(highlighted(w)[0]).toContain('272,000')
  })

  it('highlights nothing of the same definition', () => {
    const w = render(def({ tiers: PRESETS.claudeLong(), rules: [] }), def({ tiers: PRESETS.claudeLong(), rules: [] }))
    expect(highlighted(w)).toEqual([])
  })
})
