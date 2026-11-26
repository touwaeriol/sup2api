import { afterEach, describe, expect, it, vi } from 'vitest'
import { isPayUrl, openPayPage, reservePayWindow } from './payWindow'

/** A window.open answer: a blank window that remembers where it was sent. */
function fakeWindow() {
  return { closed: false, opener: {} as unknown, location: { href: 'about:blank' }, close: vi.fn() }
}

describe('payment window', () => {
  afterEach(() => vi.restoreAllMocks())

  it('takes absolute https addresses only', () => {
    expect(isPayUrl('https://pay.example.com/submit.php?a=1')).toBe(true)
    for (const bad of ['javascript:alert(document.domain)', 'JavaScript:alert(1)', 'data:text/html,<script>1</script>', 'http://pay.example.com', '//pay.example.com', '/me/wallet', '', undefined]) {
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
    expect(reservePayWindow().go('javascript:alert(document.domain)')).toBe('refused')
    expect(win.location.href).toBe('about:blank')
    expect(win.close).toHaveBeenCalled()
  })

  it('reports a blocked window', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(reservePayWindow().go('https://pay.example.com')).toBe('blocked')
  })

  it('opens no window at all for an address it refuses', () => {
    const open = vi.spyOn(window, 'open')
    expect(openPayPage('javascript:alert(1)')).toBe('refused')
    expect(open).not.toHaveBeenCalled()
  })
})
