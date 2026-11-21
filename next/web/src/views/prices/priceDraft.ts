// A price as its editor holds it (PriceDefEditor): the mode, the visual
// config being edited and the expression being written. Each mode keeps its
// own; what is sent is worked out of the mode in use (pricePayload).
import { compactVisualConfig, visualExpr, type PriceMode, type VisualConfig } from './priceExpr'
import type { PricePayload } from './usePriceValidation'
import { defaultVisualConfig, editableVisualConfig } from './visualPrice'

export interface PriceDraft {
  mode: PriceMode
  /** The visual config as edited (visualPrice): values as typed, a ticked price held while blank. */
  config: VisualConfig
  /** The expression as written in the expression mode. */
  expression: string
}

/** A new price: visual, from the default template. */
export const newPriceDraft = (): PriceDraft => ({ mode: 'visual', config: defaultVisualConfig(), expression: '' })

/** A saved price as a draft: a visual one's config editable (an expression one starts the visual mode from the default template), its expression as saved. */
export function priceDraftOf(p: { mode: PriceMode; config: unknown; expression?: string }): PriceDraft {
  return { mode: p.mode, config: p.mode === 'visual' ? editableVisualConfig(p.config) : defaultVisualConfig(), expression: p.expression || '' }
}

/** What a draft is saved as: a visual config compacted, with the expression it generates; an expression as written, without a config. */
export function pricePayload(d: PriceDraft): PricePayload & { mode: PriceMode } {
  if (d.mode === 'expression') return { mode: 'expression', config: {}, expression: d.expression }
  const config = compactVisualConfig(d.config)
  return { mode: 'visual', config, expression: visualExpr(config) }
}
