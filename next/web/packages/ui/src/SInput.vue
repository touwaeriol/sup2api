<script setup lang="ts">
// Styled native <input class="input">. type="number" emits a number (null
// when empty/invalid); type="file" emits the FileList (null when cleared)
// and ignores `modelValue`; every other type emits the raw string. `lazy`
// emits on `change` instead of `input` (default true for datetime-local /
// date / time so half-typed dates do not emit). Remaining attrs (id, name,
// list, data-testid, ...) fall through to the <input>; with adornments
// (`icon`, `suffix`, `clearable`, `revealable`, #prefix / #suffix) the input
// sits in a framed div that takes class / style.
// Inside SField / SSettingRow it takes the frame's id, aria-describedby,
// aria-invalid and size unless set here.
// Phone keyboards: non-prose fields (password / number / url / email / tel,
// or `mono` such as keys and model names) turn off autocapitalize,
// autocorrect and spellcheck, and type="number" with `min` >= 0 asks for the
// decimal pad (it has no minus key); attrs passed in win.
import { computed, ref, useAttrs, useSlots } from 'vue'
import SIcon from './SIcon.vue'
import { useUiT } from './i18n'
import { splitAttrs, useFieldControl, type ControlSize } from './field'
import { isBlank } from './value'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    modelValue: string | number | null | undefined
    type?: 'text' | 'password' | 'number' | 'url' | 'email' | 'search' | 'datetime-local' | 'date' | 'time' | 'file' | 'color' | 'tel'
    placeholder?: string
    disabled?: boolean
    readonly?: boolean
    size?: ControlSize
    mono?: boolean
    error?: boolean
    maxlength?: number
    inputmode?: 'none' | 'text' | 'tel' | 'url' | 'email' | 'numeric' | 'decimal' | 'search'
    autocomplete?: string
    lazy?: boolean
    min?: number | string
    max?: number | string
    step?: number | string
    /** SIcon name inside the start of the field. */
    icon?: string
    /** Unit / text after the value (e.g. "ms", "%"). */
    suffix?: string
    /** Show a clear button while there is a value. */
    clearable?: boolean
    /** type="password": show / hide toggle. */
    revealable?: boolean
    ariaLabel?: string
  }>(),
  { type: 'text', lazy: undefined, size: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string | number | FileList | null): void; (e: 'clear'): void }>()
const attrs = useAttrs()
const slots = useSlots()
const t = useUiT()
const { id, a11y, invalid, size, blur } = useFieldControl(props)
const input = ref<HTMLInputElement>()
const reveal = ref(false)

const DATE_TYPES = new Set(['datetime-local', 'date', 'time'])
const PLAIN_TYPES = new Set(['password', 'number', 'url', 'email', 'tel'])
const plain = computed(() => props.mono || PLAIN_TYPES.has(props.type))
const lazy = computed(() => props.lazy ?? DATE_TYPES.has(props.type))
const grouped = computed(() => !!(props.icon || props.suffix || props.clearable || props.revealable || slots.prefix || slots.suffix))
const parts = computed(() => splitAttrs(attrs))
const hasValue = computed(() => !isBlank(props.modelValue))
// Attributes of the <input> in both renderings (bare, or inside the frame).
const inputProps = computed(() => ({
  type: props.type === 'password' && reveal.value ? 'text' : props.type,
  value: props.type === 'file' ? undefined : (props.modelValue ?? ''),
  placeholder: props.placeholder,
  disabled: props.disabled,
  readonly: props.readonly,
  maxlength: props.maxlength,
  inputmode: props.inputmode ?? (props.type === 'number' && Number(props.min) >= 0 ? 'decimal' : undefined),
  autocapitalize: plain.value ? 'off' : undefined,
  autocorrect: plain.value ? 'off' : undefined,
  spellcheck: plain.value ? false : undefined,
  autocomplete: props.autocomplete,
  min: props.min,
  max: props.max,
  step: props.step,
  id: id.value
}))

function onInput(e: Event) {
  const el = e.target as HTMLInputElement
  if (props.type === 'file') return emit('update:modelValue', el.files && el.files.length ? el.files : null)
  const v = el.value
  if (props.type !== 'number') return emit('update:modelValue', v)
  if (v === '') return emit('update:modelValue', null)
  const n = Number(v)
  emit('update:modelValue', Number.isNaN(n) ? null : n)
}
function clear() {
  emit('update:modelValue', props.type === 'number' ? null : '')
  emit('clear')
  input.value?.focus()
}
defineExpose({ focus: () => input.value?.focus(), el: input })
</script>

<template>
  <input
    v-if="!grouped"
    ref="input"
    class="input"
    :class="[size === 'sm' ? 'input-sm' : '', mono ? 'font-mono' : '', invalid ? 'input-error' : '']"
    v-bind="{ ...inputProps, ...attrs, ...a11y }"
    @input="lazy ? undefined : onInput($event)"
    @change="lazy || type === 'file' ? onInput($event) : undefined"
    @blur="blur"
  />
  <div v-else class="input input-group" :class="[size === 'sm' ? 'input-sm' : '', invalid ? 'input-error' : '', disabled ? 'input-group-disabled' : '']" v-bind="parts.wrap">
    <slot name="prefix"><SIcon v-if="icon" :name="icon" class="h-4 w-4 shrink-0 text-fg-subtle" /></slot>
    <input
      ref="input"
      :class="mono ? 'font-mono' : ''"
      v-bind="{ ...inputProps, ...parts.control, ...a11y }"
      @input="lazy ? undefined : onInput($event)"
      @change="lazy || type === 'file' ? onInput($event) : undefined"
      @blur="blur"
    />
    <button
      v-if="clearable && hasValue && !disabled && !readonly"
      type="button"
      class="shrink-0 rounded text-fg-subtle hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
      :aria-label="t('ui.clear')"
      @click="clear"
    >
      <SIcon name="x" class="h-4 w-4" />
    </button>
    <span v-if="suffix" class="shrink-0 text-xs text-fg-muted">{{ suffix }}</span>
    <slot name="suffix" />
    <button
      v-if="revealable && type === 'password'"
      type="button"
      class="shrink-0 rounded text-fg-subtle hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
      :aria-label="reveal ? t('ui.hidePassword') : t('ui.showPassword')"
      :aria-pressed="reveal"
      :disabled="disabled"
      @click="reveal = !reveal"
    >
      <SIcon :name="reveal ? 'eye-off' : 'eye'" class="h-4 w-4" />
    </button>
  </div>
</template>
