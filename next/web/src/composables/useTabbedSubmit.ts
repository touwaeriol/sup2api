import type { TabbedForm } from '@sub2api/ui'
import { notifyError } from '@/utils/errors'
import { confirmDiscard } from './confirmDiscard'

export interface TabbedSubmitOptions {
  /** The one request, with the body every part of the form makes. */
  send: (body: Record<string, unknown>) => Promise<unknown>
  /** After a save. */
  done: () => void
  /** Leaving: closes the editor once nothing unsaved is lost. */
  close?: () => void
}

/**
 * Saving an editor's tabbed form (useTabbedForm) in one request. A save the
 * form refuses (a check, or field errors of the answer) marks its tabs and
 * keeps the editor open; any other failure is reported. leave() asks first
 * when something is not saved.
 */
export function useTabbedSubmit(tabbed: TabbedForm, options: TabbedSubmitOptions) {
  async function submit(): Promise<boolean> {
    try {
      if ((await tabbed.submit(options.send)) === false) return false
    } catch (e) {
      notifyError(e)
      return false
    }
    options.done()
    return true
  }
  async function leave() {
    if (await confirmDiscard(tabbed.anyDirty.value)) options.close?.()
  }
  return { submit, leave }
}
