// The gateway's payment page in a new window. The window is opened blank
// inside the click (popup blockers allow only that) and pointed at the page
// once the order exists; without the order it is closed again. Only an https
// address is ever loaded: the blank window shares this console's origin, so
// a javascript: (or any other) address would run as the console.

/** How loading the payment page went: refused when the address is not https. */
export type PayOpen = 'opened' | 'blocked' | 'refused'

export interface PayWindow {
  /** Loads the payment page. */
  go(url: string): PayOpen
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

export function reservePayWindow(): PayWindow {
  const win = window.open('', '_blank')
  const close = () => win?.close()
  return {
    go(url) {
      if (!isPayUrl(url)) {
        close()
        return 'refused'
      }
      if (!win || win.closed) return 'blocked'
      win.opener = null
      win.location.href = url
      return 'opened'
    },
    close
  }
}

/** Opens the payment page right away (inside a click). */
export function openPayPage(url: string): PayOpen {
  return isPayUrl(url) ? reservePayWindow().go(url) : 'refused'
}
