import type { PaymentMethod } from '../host'

// How a payment method looks, shared by settings, wallet and finance pages:
// a built-in icon (brand glyph on its color) or a custom logo image.

/** What MethodIcon needs of a method; an order without its method has only the name. */
export type MethodLook = Pick<PaymentMethod, 'name'> & Partial<Pick<PaymentMethod, 'icon' | 'color' | 'logo'>>

/** A built-in icon: its brand color and the glyph drawn on it, or a UI icon in place of the glyph. */
export interface BuiltinIcon {
  color: string
  glyph?: string
  uiIcon?: string
}

export const BUILTIN_ICONS: Record<string, BuiltinIcon> = {
  alipay: { color: '#1677ff', glyph: '支' },
  wxpay: { color: '#07c160', glyph: '微' },
  qqpay: { color: '#12b7f5', glyph: 'Q' },
  stripe: { color: '#635bff', glyph: 'S' },
  card: { color: '#475569', uiIcon: 'balance' }
}

/** The built-in icon of a key, if it is one. */
export const builtinIcon = (key: string | undefined): BuiltinIcon | undefined => (key && Object.hasOwn(BUILTIN_ICONS, key) ? BUILTIN_ICONS[key] : undefined)

/** Brand color of a built-in icon key ('' when unknown). */
export const brandColor = (key: string) => builtinIcon(key)?.color ?? ''

/** Image types a logo may have (data URLs carry one of these), with their display names. */
export const LOGO_TYPES = { 'image/png': 'PNG', 'image/jpeg': 'JPG', 'image/webp': 'WebP', 'image/svg+xml': 'SVG' } as const
export type LogoType = keyof typeof LOGO_TYPES
export const isLogoType = (v: string): v is LogoType => Object.hasOwn(LOGO_TYPES, v)

/** A parsed logo: an inline image (type and decoded size) or an https address. */
export type LogoSource = { kind: 'data'; type: LogoType; bytes: number } | { kind: 'url'; host: string }

const DATA_URL = /^data:(image\/[a-z+]+);base64,([A-Za-z0-9+/]*={0,2})$/

function parseDataUrl(v: string): LogoSource | null {
  const m = DATA_URL.exec(v)
  if (!m || !isLogoType(m[1])) return null
  const b64 = m[2]
  const bytes = Math.floor((b64.length * 3) / 4) - (b64.endsWith('==') ? 2 : b64.endsWith('=') ? 1 : 0)
  return { kind: 'data', type: m[1], bytes }
}

function parseHttps(v: string): LogoSource | null {
  try {
    const u = new URL(v)
    return u.protocol === 'https:' && u.hostname ? { kind: 'url', host: u.hostname } : null
  } catch {
    return null
  }
}

/** What a logo value is, or null when it is neither a base64 image data URL nor an https address. */
export function parseLogo(v: string | undefined): LogoSource | null {
  if (!v) return null
  return v.startsWith('data:') ? parseDataUrl(v) : parseHttps(v)
}
