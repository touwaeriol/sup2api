import { describe, expect, it } from 'vitest'
import { builtinIcon, parseLogo } from './methodIcon'
import { LOGO_MAX_BYTES, logoFileError, logoValidator } from '../settings/settingsForm'

const t = (key: string) => key
const dataUrl = (type: string, bytes: number) => `data:${type};base64,${btoa('x'.repeat(bytes))}`

describe('parseLogo', () => {
  it('reads a base64 image data URL with its decoded size', () => {
    expect(parseLogo(dataUrl('image/svg+xml', 10))).toEqual({ kind: 'data', type: 'image/svg+xml', bytes: 10 })
    expect(parseLogo(dataUrl('image/png', 11))).toMatchObject({ bytes: 11 })
  })

  it('reads an https address', () => {
    expect(parseLogo('https://cdn.example.com/a.png')).toEqual({ kind: 'url', host: 'cdn.example.com' })
  })

  it('refuses other schemes, types and encodings', () => {
    for (const v of ['http://example.com/a.png', 'javascript:alert(1)', dataUrl('text/html', 4), 'data:image/svg+xml,<svg/>', 'logo.png', '']) {
      expect(parseLogo(v)).toBeNull()
    }
  })
})

describe('logo rules', () => {
  const check = logoValidator(t)

  it('accepts empty, small images and https addresses', () => {
    expect(check('')).toBe(true)
    expect(check(dataUrl('image/webp', 100))).toBe(true)
    expect(check('https://example.com/logo.svg')).toBe(true)
  })

  it('names what is wrong', () => {
    expect(check(dataUrl('image/png', LOGO_MAX_BYTES + 1))).toBe('topup.settings.methods.logo.rule.size')
    expect(check('ftp://example.com/a.png')).toBe('topup.settings.methods.logo.rule.format')
    expect(check(`https://example.com/${'a'.repeat(2048)}`)).toBe('topup.settings.methods.logo.rule.urlLength')
  })

  it('refuses a file of another type or over the size before reading it', () => {
    expect(logoFileError(new File(['x'], 'a.gif', { type: 'image/gif' }), t)).toBe('topup.settings.methods.logo.rule.type')
    expect(logoFileError(new File([new Uint8Array(LOGO_MAX_BYTES + 1)], 'a.png', { type: 'image/png' }), t)).toBe('topup.settings.methods.logo.rule.size')
    expect(logoFileError(new File(['<svg/>'], 'a.svg', { type: 'image/svg+xml' }), t)).toBe('')
  })
})

describe('builtinIcon', () => {
  it('knows only its own keys', () => {
    expect(builtinIcon('alipay')?.color).toBe('#1677ff')
    expect(builtinIcon('constructor')).toBeUndefined()
    expect(builtinIcon(undefined)).toBeUndefined()
  })
})
