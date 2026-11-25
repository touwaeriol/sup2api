// The gateway's payment page in a new window. The window is opened blank
// inside the click (popup blockers allow only that) and pointed at the page
// once the order exists; without the order it is closed again.

export interface PayWindow {
  /** Loads the payment page; false when the browser blocked the window. */
  go(url: string): boolean
  close(): void
}

export function reservePayWindow(): PayWindow {
  const win = window.open('', '_blank')
  return {
    go(url) {
      if (!win || win.closed) return false
      win.opener = null
      win.location.href = url
      return true
    },
    close: () => win?.close()
  }
}

/** Opens the payment page right away (inside a click); false when blocked. */
export function openPayPage(url: string): boolean {
  return reservePayWindow().go(url)
}
