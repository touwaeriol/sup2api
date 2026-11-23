import { inject, provide, type InjectionKey } from 'vue'

/**
 * A row of actions (a page header's toolbar, the end of a tab bar). Buttons
 * in it take the toolbar's size rather than their own, so every toolbar is one
 * control high. In a page's own toolbar (`float`) a primary button with an
 * icon floats bottom-right on a phone, as `SButton fab` does. A dialog,
 * drawer or sheet opened from a toolbar is not part of it: OverlaySurface
 * resets it to null.
 */
export interface Toolbar {
  float: boolean
}

const TOOLBAR: InjectionKey<Toolbar | null> = Symbol('s-toolbar')

export function provideToolbar(toolbar: Toolbar | null) {
  provide(TOOLBAR, toolbar)
}

/** The toolbar the caller is in, or null. */
export function useToolbar() {
  return inject(TOOLBAR, null)
}
