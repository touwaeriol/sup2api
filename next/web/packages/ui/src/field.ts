// Form-item context shared by SField / SSettingRow and the controls inside
// them. The frame provides ids; the first labelable control without its own
// `id` claims the frame's id (so `<label for>` reaches it), group controls
// (radio groups, segmented, key-value) point aria-labelledby at the label.
// Hint / error ids go to aria-describedby and `error` sets aria-invalid.
// Every control joins the frame, so the frame knows when its only control is
// an inline toggle (switch / checkbox) that fits beside the label.
import { computed, inject, onBeforeUnmount, provide, ref, useAttrs, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import type { Trigger } from './form/rules'

export type ControlSize = 'sm' | 'md'

export interface FieldContext {
  /** Id a labelable control may take (the label's `for`). */
  controlId: ComputedRef<string>
  /** Id of the label element (aria-labelledby of group controls); '' without a label. */
  labelId: ComputedRef<string>
  /** Space-separated ids describing the control (hint, error), or undefined. */
  describedBy: ComputedRef<string | undefined>
  invalid: ComputedRef<boolean>
  size: ComputedRef<ControlSize | undefined>
  /** True once a control took controlId. */
  claimed: Ref<boolean>
  claim(): boolean
  release(): void
  /** Counts a control in the frame until the returned function runs; `inline` = a switch / checkbox. Optional for contexts provided outside the SDK. */
  join?(inline: boolean): () => void
  /** The control lost focus ('blur') or changed ('change'). Optional for contexts provided outside the SDK. */
  touch?(kind: Trigger): void
}

/** null inside a control: a nested frame's controls are not linked to the outer one. */
export const FIELD_KEY: InjectionKey<FieldContext | null> = Symbol('s-field')

/**
 * Provides the form-item context of a frame (SField, SSettingRow). `uid` is
 * the frame's generated id (the control id is `${uid}-control` unless the
 * frame's `for` names one). Returns the label's `for`: set once a control
 * took the id (or `for` is given), so a label never points at nothing.
 */
export function provideField(
  uid: string,
  opts: {
    for: () => string | undefined
    labelId: ComputedRef<string>
    describedBy: ComputedRef<string | undefined>
    invalid: ComputedRef<boolean>
    size: ComputedRef<ControlSize | undefined>
    touch?: (kind: Trigger) => void
  }
) {
  const claimed = ref(false)
  let taken = false
  const members = ref({ all: 0, inline: 0 })
  // Applied a microtask later, like claim(): controls join during the frame's render.
  const count = (inline: boolean, step: number) =>
    Promise.resolve().then(() => (members.value = { all: members.value.all + step, inline: members.value.inline + (inline ? step : 0) }))
  const controlId = computed(() => opts.for() || `${uid}-control`)
  const ctx: FieldContext = {
    controlId,
    labelId: opts.labelId,
    describedBy: opts.describedBy,
    invalid: opts.invalid,
    size: opts.size,
    claimed,
    // The flag is plain (a control claims during its setup, inside the
    // frame's render); the ref follows a microtask later so the frame
    // re-renders its label with for=.
    claim() {
      if (taken) return false
      taken = true
      Promise.resolve().then(() => (claimed.value = taken))
      return true
    },
    release() {
      taken = false
      Promise.resolve().then(() => (claimed.value = taken))
    },
    join(inline: boolean) {
      count(inline, 1)
      return () => count(inline, -1)
    },
    touch: opts.touch
  }
  provide(FIELD_KEY, ctx)
  return {
    labelFor: computed(() => (opts.for() || claimed.value ? controlId.value : undefined)),
    /** The frame holds a single control and it is an inline toggle. */
    inlineOnly: computed(() => members.value.all === 1 && members.value.inline === 1)
  }
}

/**
 * Accessibility attributes of a control inside SField / SSettingRow.
 * `labelable` controls (input, select, textarea, switch) claim the frame id
 * when they have no `id` attr; group controls get aria-labelledby instead.
 * `inline` marks a switch / checkbox (it may sit beside the frame's label).
 * `blur` goes on the control's blur event (the frame's form validates).
 */
export function useFieldControl(props: { error?: boolean; size?: string; ariaLabel?: string }, labelable = true, inline = false) {
  const field = inject(FIELD_KEY, null)
  const attrs = useAttrs()
  // Reset the shared context so controls nested inside this one are unaffected.
  provide(FIELD_KEY, null)
  const owns = labelable && !!field && attrs.id === undefined && field.claim()
  const leave = field?.join?.(inline)
  onBeforeUnmount(() => {
    if (owns) field?.release()
    leave?.()
  })
  const id = computed(() => (attrs.id as string | undefined) ?? (owns ? field!.controlId.value : undefined))
  const invalid = computed(() => !!props.error || !!field?.invalid.value)
  const size = computed<ControlSize>(() => (props.size === 'sm' || props.size === 'md' ? props.size : (field?.size.value ?? 'md')))
  const a11y = computed(() => {
    const out: Record<string, string | undefined> = {}
    const described = [attrs['aria-describedby'] as string | undefined, field?.describedBy.value].filter(Boolean).join(' ')
    if (described) out['aria-describedby'] = described
    if (invalid.value) out['aria-invalid'] = 'true'
    if (!labelable && field?.labelId.value && !props.ariaLabel && attrs['aria-labelledby'] === undefined && attrs['aria-label'] === undefined) {
      out['aria-labelledby'] = field.labelId.value
    }
    if (props.ariaLabel) out['aria-label'] = props.ariaLabel
    return out
  })
  const blur = () => field?.touch?.('blur')
  return { id, invalid, size, a11y, field, blur }
}

/** Splits fallthrough attrs: class/style (and optionally data-* / listeners) for the wrapper, the rest for the control. */
export function splitAttrs(attrs: Record<string, unknown>, wrapperKeys: (k: string) => boolean = () => false) {
  const wrap: Record<string, unknown> = {}
  const control: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(attrs)) (k === 'class' || k === 'style' || wrapperKeys(k) ? wrap : control)[k] = v
  return { wrap, control }
}
