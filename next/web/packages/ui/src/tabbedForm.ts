// Saving a form whose tabs each hold a part of one request (UI 4.1). Every
// tab keeps its edits as drafts; Save checks every part, merges what they
// send into one body and makes one request, so nothing has to be saved
// before another tab can be filled in. Field errors of the answer go back to
// the parts that sent those fields, and their tabs are marked.
import { computed, inject, onScopeDispose, provide, ref, shallowRef, toValue, type InjectionKey, type MaybeRefOrGetter, type Ref } from 'vue'
import { isApiError } from '@sub2api/host'

/** One part of the request, held by one tab (a tab may hold several). */
export interface TabbedFormPart {
  /** The tab that shows the part: a failed check or a field error opens it. */
  tab: string
  /** Whether the part can be sent; false keeps the request back, and the part shows why. Default true. */
  check?: () => boolean
  /** What the part adds to the request body; it is merged with the other parts' (their fields must not overlap). */
  body: () => Record<string, unknown>
  /** Whether the part holds unsaved changes. */
  dirty?: MaybeRefOrGetter<boolean>
  /** The body fields the part sends, as path prefixes: "name" claims the field errors "name" and "name.x", "model_prices" also "model_prices.0.rule". */
  fields?: readonly string[]
  /** Takes the field errors the part claims, keyed by their full paths. */
  applyErrors?: (errors: Record<string, string>) => void
}

export interface TabbedForm {
  /** The tab in front. */
  current: Ref<string>
  /** A request is under way. */
  saving: Readonly<Ref<boolean>>
  /** The tabs with unsaved changes, in tab order. */
  dirtyTabs: Readonly<Ref<string[]>>
  /** The tabs a failed check or a field error marked, in tab order; cleared when Save is pressed again. */
  errorTabs: Readonly<Ref<string[]>>
  anyDirty: Readonly<Ref<boolean>>
  /** Adds a part; the returned function takes it away again. */
  register: (part: TabbedFormPart) => () => void
  /**
   * Checks every part, sends the merged body through `send` once and returns
   * its answer; false when a check failed or the answer had field errors the
   * parts claim (their tabs are marked, the first is opened). Any other
   * failure rejects.
   */
  submit: <T>(send: (body: Record<string, unknown>) => Promise<T>) => Promise<T | false>
  /** Unmarks the tabs: when the form is opened on another object. */
  clearErrors: () => void
}

const TABBED_FORM_KEY: InjectionKey<TabbedForm> = Symbol('tabbed-form')

/** Whether a field error belongs to a field prefix ("a" claims "a" and "a.b", not "ab"). */
const claims = (prefix: string, field: string) => field === prefix || field.startsWith(`${prefix}.`)

/** The field errors by the part that claims them; those no part claims are left out. */
export function errorsByPart(parts: readonly TabbedFormPart[], fields: Record<string, string>): Map<TabbedFormPart, Record<string, string>> {
  const out = new Map<TabbedFormPart, Record<string, string>>()
  for (const [field, message] of Object.entries(fields)) {
    const part = parts.find((p) => p.fields?.some((prefix) => claims(prefix, field)))
    if (part) out.set(part, { ...out.get(part), [field]: message })
  }
  return out
}

/** The parts of a form, and the tabs they make dirty, in tab order. */
function useParts(tabs: MaybeRefOrGetter<readonly string[]>) {
  // The list is kept apart from the ref, which is only written: a part comes
  // and goes while a parent renders (mounting or unmounting the part), and
  // reading the ref there would make that render depend on the parts.
  let list: TabbedFormPart[] = []
  const parts = shallowRef<TabbedFormPart[]>(list)
  const set = (next: TabbedFormPart[]) => (parts.value = list = next)
  const inOrder = (keys: Iterable<string>) => {
    const wanted = new Set(keys)
    return toValue(tabs).filter((key) => wanted.has(key))
  }
  const dirtyTabs = computed(() => inOrder(parts.value.filter((p) => toValue(p.dirty)).map((p) => p.tab)))
  function register(part: TabbedFormPart) {
    set([...list, part])
    return () => set(list.filter((p) => p !== part))
  }
  return { parts, inOrder, dirtyTabs, register }
}

/** The tabs marked for an error; marking opens the first of them. */
function useMarks(current: Ref<string>, inOrder: (keys: Iterable<string>) => string[]) {
  const marked = ref<string[]>([])
  function mark(tabKeys: string[]) {
    marked.value = tabKeys
    const first = inOrder(tabKeys)[0]
    if (first !== undefined) current.value = first
  }
  return { errorTabs: computed(() => inOrder(marked.value)), mark, clear: () => (marked.value = []) }
}

/** The tabs of the parts whose check fails; every check runs, so each tab shows what is wrong. */
const failedChecks = (parts: readonly TabbedFormPart[]) => parts.filter((p) => !(p.check?.() ?? true)).map((p) => p.tab)

/** Hands the claimed field errors of a failed request to their parts and gives their tabs; none when no part claims one. */
function claimErrors(parts: readonly TabbedFormPart[], e: unknown): string[] {
  if (!isApiError(e)) return []
  const byPart = errorsByPart(parts, e.fields)
  for (const [part, errors] of byPart) part.applyErrors?.(errors)
  return [...byPart.keys()].map((p) => p.tab)
}

/**
 * A form of tabs saved by one request. `current` is the tab in front (the
 * parent's v-model of STabbedForm); `tabs` gives the tab keys in order, for
 * which marked tab to open first. The form is provided to the components
 * below the caller (useTabbedFormPart).
 */
export function useTabbedForm(current: Ref<string>, tabs: MaybeRefOrGetter<readonly string[]>): TabbedForm {
  const { parts, inOrder, dirtyTabs, register } = useParts(tabs)
  const { errorTabs, mark, clear } = useMarks(current, inOrder)
  const saving = ref(false)

  async function submit<T>(send: (body: Record<string, unknown>) => Promise<T>): Promise<T | false> {
    clear()
    const failed = failedChecks(parts.value)
    if (failed.length) {
      mark(failed)
      return false
    }
    saving.value = true
    try {
      return await send(Object.assign({}, ...parts.value.map((p) => p.body())))
    } catch (e) {
      const claimed = claimErrors(parts.value, e)
      if (!claimed.length) throw e
      mark(claimed)
      return false
    } finally {
      saving.value = false
    }
  }

  const form: TabbedForm = { current, saving, dirtyTabs, errorTabs, anyDirty: computed(() => dirtyTabs.value.length > 0), register, submit, clearErrors: clear }
  provide(TABBED_FORM_KEY, form)
  return form
}

/**
 * Adds a part to the tabbed form of an enclosing component (useTabbedForm
 * provides it) for as long as the calling component lives. Outside such a
 * form it does nothing.
 */
export function useTabbedFormPart(part: TabbedFormPart): void {
  const form = inject(TABBED_FORM_KEY, null)
  if (!form) return
  onScopeDispose(form.register(part))
}
