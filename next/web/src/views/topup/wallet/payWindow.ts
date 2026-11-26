// The gateway's payment page in a new window. The window is opened blank
// inside the click (popup blockers allow only that) and pointed at the page
// once the order exists; without the order it is closed again. Only an https
// address is ever loaded: the blank window shares this console's origin, so
// a javascript: (or any other) address would run as the console. The core
// already refuses such addresses; this is the last line.

/** How loading the payment page went. */
export type PayOpen = 'opened' | 'blocked'

interface PayWindow {
  /** Loads the payment page; null (window closed) when the address is not https. */
  go(url: string | undefined): PayOpen | null
  close(): void
}

/** Whether a gateway's address may be opened: an absolute https URL. */
export function isPayUrl(url: string | undefined): url is string {
  if (!url) return false
  try {
    return new URL(url).protocol === 'https:'
  } catch {
    return false
  }
}

/** Whether a QR code may be shown: the PNG data URL the core draws, nothing else. */
export function isQrImage(src: string | undefined): src is string {
  return !!src?.startsWith('data:image/png;base64,')
}

export function reservePayWindow(): PayWindow {
  const win = window.open('', '_blank')
  const close = () => win?.close()
  return {
    go(url) {
      if (!isPayUrl(url)) {
        close()
        return null
      }
      if (!win || win.closed) return 'blocked'
      win.opener = null
      win.location.href = url
      return 'opened'
    },
    close
  }
}

/** Opens the payment page right away (inside a click); null without an https address. */
export function openPayPage(url: string | undefined): PayOpen | null {
  return isPayUrl(url) ? reservePayWindow().go(url) : null
}

// Methods whose last order came with no page to open (a QR code only): the
// next order through one reserves no blank window, which would only flash
// open and shut. Should such a method give a page after all, it is opened
// after the order (a blocker may stop that; the waiting dialog then offers
// to reopen it).
const pageless = new Set<string>()

/** The payment window for an order through `method`, reserved inside the click unless the method gave no page last time. */
export function payWindowFor(method: string): PayWindow {
  const reserved = pageless.has(method) ? null : reservePayWindow()
  return {
    go(url) {
      if (isPayUrl(url)) pageless.delete(method)
      else pageless.add(method)
      return reserved ? reserved.go(url) : openPayPage(url)
    },
    close: () => reserved?.close()
  }
}
