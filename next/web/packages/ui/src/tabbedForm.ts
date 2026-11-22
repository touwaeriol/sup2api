// Saving a form whose tabs each hold a part of one request (UI 4.1). Every
// tab keeps its edits as drafts; Save checks every part, merges what they
// send into one body and makes one request, so nothing has to be saved
// before another tab can be filled in. Field errors of the answer go back to
// the parts that sent those fields, and their tabs are marked.
import { computed, inject, provide, ref, shallowRef, toValue, watch, type InjectionKey, type MaybeRefOrGetter, type Ref } from 'vue'
import { isApiError } from '@sub2api/host'
import { toast } from './feedback'

/** One part of the request, held by one tab (a tab may hold several). */
export interface TabbedFormPart {
  /** The tab that shows the part: a failed check or a field error opens it. */
  tab: string
  /** Whether the part can be sent; false keeps the request back, and the part shows why. It may wait (a plugin form validating). Default true. */
  check?: () => boolean | Promise<boolean>
  /** What the part adds to the request body; it is merged with the other parts' (their fields must not overlap). */
  body: () => Record<string, unknown>
  /** Whether the part holds unsaved changes. */
  dirty?: MaybeRefOrGetter<boolean>
  /** The body fields the part sends, as path prefixes: "name" claims the field errors "name", "name.x" and "name[0]", "model_prices" also "model_prices.0.rule". */
  fields?: readonly string[]
  /** Takes the field errors the part claims, keyed by their full paths. */
  applyErrors?: (errors: Record<string, string>) => void
  /**
   * Whether the part shows an error now (one typed in, or one the server
   * gave and the part still holds). When given, it alone marks the tab, as
   * the part's errors come and go; else the last Save's result does.
   */
  invalid?: MaybeRefOrGetter<boolean>
}

export interface TabbedForm {
  /** The tab in front. */
  current: Ref<string>
  /** Save is under way: its checks or its request. */
  saving: Readonly<Ref<boolean>>
  /** The tabs with unsaved changes, in tab order. */
  dirtyTabs: Readonly<Ref<string[]>>
  /** The tabs a failed check or a field error marked (cleared when Save is pressed again), or whose parts are `invalid`, in tab order. */
  errorTabs: Readonly<Ref<string[]>>
  anyDirty: Readonly<Ref<boolean>>
  /** Adds a part; the returned function takes it away again. */
  register: (part: TabbedFormPart) => () => void
  /**
   * Checks every part, sends the merged body through `send` once and returns
   * its answer; false when a check failed or the answer had field errors the
   * parts claim (their tabs are marked, the first is opened; a toast tells
   * the field errors no part claims). Any other failure rejects.
   */
  submit: <T>(send: (body: Record<string, unknown>) => Promise<T>) => Promise<T | false>
  /** Unmarks the tabs: when the form is opened on another object. */
  clearErrors: () => void
}

const TABBED_FORM_KEY: InjectionKey<TabbedForm> = Symbol('tabbed-form')

/** Whether a field error belongs to a field prefix ("a" claims "a", "a.b" and "a[0]", not "ab"). */
const claims = (prefix: string, field: string) => field === prefix || field.startsWith(`${prefix}.`) || field.startsWith(`${prefix}[`)

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

/**
 * The tabs with an error: a part that says whether it is `invalid` marks its
 * tab by that alone, the others by the last marking. Marking the failed
 * parts opens the first of their tabs.
 */
function useMarks(current: Ref<string>, parts: Ref<TabbedFormPart[]>, inOrder: (keys: Iterable<string>) => string[]) {
  const marked = ref<string[]>([])
  const shows = (p: TabbedFormPart) => (p.invalid === undefined ? marked.value.includes(p.tab) : toValue(p.invalid))
  function mark(failed: TabbedFormPart[]) {
    marked.value = failed.filter((p) => p.invalid === undefined).map((p) => p.tab)
    const first = inOrder(failed.map((p) => p.tab))[0]
    if (first !== undefined) current.value = first
  }
  const errorTabs = computed(() => inOrder(parts.value.filter(shows).map((p) => p.tab)))
  return { errorTabs, mark, clear: () => (marked.value = []) }
}

/** The parts whose check fails; every check runs, so each tab shows what is wrong. */
async function failedChecks(parts: readonly TabbedFormPart[]): Promise<TabbedFormPart[]> {
  const passed = await Promise.all(parts.map((p) => p.check?.() ?? true))
  return parts.filter((_, i) => !passed[i])
}

/**
 * Hands the claimed field errors of a failed request to their parts and
 * gives those parts; none when no part claims one. The field errors no part
 * claims are told in one toast, as no tab can show them.
 */
function claimErrors(parts: readonly TabbedFormPart[], e: unknown): TabbedFormPart[] {
  if (!isApiError(e)) return []
  const byPart = errorsByPart(parts, e.fields)
  for (const [part, errors] of byPart) part.applyErrors?.(errors)
  const claimed = new Set([...byPart.values()].flatMap(Object.keys))
  const left = Object.entries(e.fields).filter(([field]) => !claimed.has(field))
  if (byPart.size && left.length) toast(left.map(([field, message]) => `${field}: ${message}`).join('; '), 'error')
  return [...byPart.keys()]
}

/**
 * A form of tabs saved by one request. `current` is the tab in front (the
 * parent's v-model of STabbedForm); `tabs` gives the tab keys in order, for
 * which marked tab to open first. The form is provided to the components
 * below the caller (useTabbedFormPart).
 */
export function useTabbedForm(current: Ref<string>, tabs: MaybeRefOrGetter<readonly string[]>): TabbedForm {
  const { parts, inOrder, dirtyTabs, register } = useParts(tabs)
  const { errorTabs, mark, clear } = useMarks(current, parts, inOrder)
  const saving = ref(false)

  async function submit<T>(send: (body: Record<string, unknown>) => Promise<T>): Promise<T | false> {
    clear()
    saving.value = true
    try {
      const failed = await failedChecks(parts.value)
      if (failed.length) {
        mark(failed)
        return false
      }
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
 * form it does nothing. `active` (default true) keeps the part in the form
 * only while it holds: a component that turns editable while it stays
 * mounted adds its part then, and takes it away when it is read-only again.
 */
export function useTabbedFormPart(part: TabbedFormPart, active: MaybeRefOrGetter<boolean> = true): void {
  const form = inject(TABBED_FORM_KEY, null)
  if (!form) return
  watch(
    () => toValue(active),
    (on, _, onCleanup) => {
      if (on) onCleanup(form.register(part))
    },
    { immediate: true }
  )
}
