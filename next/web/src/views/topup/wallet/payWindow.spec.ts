import { afterEach, describe, expect, it, vi } from 'vitest'
import { isPayUrl, isQrImage, openPayPage, payWindowFor, reservePayWindow } from './payWindow'

/** A window.open answer: a blank window that remembers where it was sent. */
function fakeWindow() {
  return { closed: false, opener: {} as unknown, location: { href: 'about:blank' }, close: vi.fn() }
}

describe('payment window', () => {
  afterEach(() => vi.restoreAllMocks())

  it('takes absolute https addresses only', () => {
    expect(isPayUrl('https://pay.example.com/submit.php?a=1')).toBe(true)
    for (const bad of [
      'javascript:alert(document.domain)',
      'JavaScript:alert(1)',
      'data:text/html,<script>1</script>',
      'http://pay.example.com',
      '//pay.example.com',
      '/me/wallet',
      '',
      undefined
    ]) {
      expect(isPayUrl(bad), String(bad)).toBe(false)
    }
  })

  it('loads an https page into the reserved window, cut off from this one', () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    expect(reservePayWindow().go('https://checkout.stripe.com/c/pay/cs_test')).toBe('opened')
    expect(win.location.href).toBe('https://checkout.stripe.com/c/pay/cs_test')
    expect(win.opener).toBeNull()
  })

  it('refuses any other address and closes the reserved window without loading it', () => {
    const win = fakeWindow()
    vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    expect(reservePayWindow().go('javascript:alert(document.domain)')).toBeNull()
    expect(win.location.href).toBe('about:blank')
    expect(win.close).toHaveBeenCalled()
  })

  it('reports a blocked window', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(reservePayWindow().go('https://pay.example.com')).toBe('blocked')
  })

  it('opens no window at all for an address it refuses', () => {
    const open = vi.spyOn(window, 'open')
    expect(openPayPage('javascript:alert(1)')).toBeNull()
    expect(open).not.toHaveBeenCalled()
  })

  it('shows only the PNG QR codes the core draws', () => {
    expect(isQrImage('data:image/png;base64,iVBORw0KGgo=')).toBe(true)
    for (const bad of ['data:image/svg+xml;base64,PHN2Zz4=', 'https://evil.example/qr.png', 'javascript:alert(1)', '', undefined]) {
      expect(isQrImage(bad), String(bad)).toBe(false)
    }
  })

  it('reserves no window for a method that gave only a QR code last time, and opens its page should it give one', () => {
    const win = fakeWindow()
    const open = vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
    expect(payWindowFor('qr_only').go(undefined)).toBeNull()
    expect(win.close).toHaveBeenCalled()
    expect(open).toHaveBeenCalledTimes(1)

    const next = payWindowFor('qr_only')
    expect(open).toHaveBeenCalledTimes(1)
    expect(next.go('https://pay.example.com/p')).toBe('opened')
    expect(open).toHaveBeenCalledTimes(2)
    payWindowFor('qr_only')
    expect(open).toHaveBeenCalledTimes(3)
  })
})
