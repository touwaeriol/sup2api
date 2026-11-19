<script setup lang="ts">
import { Teleport, computed, mergeProps, nextTick, ref, useAttrs, useId, watch } from 'vue'
import type { SelectOption } from './types'
import { useUiT } from './i18n'
import { splitAttrs, useFieldControl, type ControlSize } from './field'
import { useFloating } from './floating'
import { useViewport } from './viewport'
import { isBlank } from './value'
import SIcon from './SIcon.vue'
import SSpinner from './SSpinner.vue'
import OverlaySurface from './internal/OverlaySurface.vue'

// Styled native <select>. Values keep their type (number/string/boolean/null).
// An option with `options` renders an <optgroup>; the <option> value is the
// index into the flattened option list so lookup works across groups.
// `searchable` renders a combobox (text filter + listbox, group titles); its
// list is teleported to <body> so scrolling containers and modals do not clip
// it. Attrs: on the <select>; when searchable class / style / data-* go to
// the wrapper and id / name / aria-* to the text input. Inside SField /
// SSettingRow it takes the frame's id, aria-describedby, aria-invalid, size.
// On phones a searchable select opens a bottom sheet instead: the search
// field on top, the options below in 44px rows, the selected one checked.
// More of the searchable form (packages/ui/README.md, UI 3.11): options carry
// `hint` / `description` / `tag` / `muted` / `accent`; `icon`, `clearable` or
// `tinted` frame the field (icon, text, the selected hint, ×, chevron);
// `remote` leaves the matching to the caller (`search` event, `loading`,
// `selected` for a value the results do not list); `note` / `footer` head and
// end the list.
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    modelValue: SelectOption['value'] | undefined
    options: SelectOption[]
    placeholder?: string
    disabled?: boolean
    searchable?: boolean
    noResults?: string
    ariaLabel?: string
    size?: ControlSize
    error?: boolean
    /** Searchable: SIcon name at the start of the field. */
    icon?: string
    /** Searchable: a × button while a value is set; it emits null and stands in for the placeholder row. */
    clearable?: boolean
    /** Searchable: tints the field while a value is set (a filter in effect). */
    tinted?: boolean
    /** Searchable: `options` are search results, shown as given; opening ('') and typing emit `search`. */
    remote?: boolean
    /** Searchable: a search is running; the list ends with "Searching…". */
    loading?: boolean
    /** Searchable: how `modelValue` shows when `options` does not list it (remote results). */
    selected?: SelectOption
    /** Searchable: a line heading the list. */
    note?: string
    /** Searchable: a line kept at the bottom of the list. */
    footer?: string
  }>(),
  { size: undefined, icon: undefined, selected: undefined, note: undefined, footer: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: SelectOption['value']): void; (e: 'search', query: string): void }>()
const attrs = useAttrs()
const t = useUiT()
const { id, a11y, invalid, size, blur } = useFieldControl(props)

interface Choice extends SelectOption {
  group?: string
}
// Every selectable option in order (group children carry the group label);
// the native <option> value is the index into this list.
const flat = computed<Choice[]>(() => props.options.flatMap((o) => (o.options ? o.options.map((c) => ({ ...c, group: o.label })) : [o])))
// Native <select> rows: groups keep their children.
const rows = computed(() => {
  let idx = 0
  return props.options.map((o) => ({ group: o.options ? o.label : undefined, items: (o.options ?? [o]).map((c) => ({ o: c, idx: idx++ })) }))
})
const open = ref(false)
const query = ref('')
const active = ref(-1)
const listId = useId()
const root = ref<HTMLElement>()
const field = ref<HTMLInputElement>()
const menu = ref<HTMLElement>()
const teleported = ref(false)
const floating = useFloating(root, menu, { matchWidth: true, onLost: () => (open.value = false) })
const { compact } = useViewport()
const sheet = computed(() => props.searchable && compact.value)
const floats = computed(() => teleported.value && !sheet.value)
// The list's container: the sheet on phones, <body> in a live page, inline while detached.
const listHost = computed(() => {
  if (sheet.value) return { is: OverlaySurface, props: { open: open.value, form: 'sheet' as const, fill: true, flush: true, onClose: hide } }
  return teleported.value ? { is: Teleport, props: { to: 'body' } } : { is: 'div', props: {} }
})
// The placeholder is the "no value" row, unless the × button clears.
const choices = computed<Choice[]>(() =>
  props.placeholder === undefined || props.clearable ? flat.value : [{ value: null, label: props.placeholder }, ...flat.value]
)
const filtered = computed(() => {
  if (props.remote) return choices.value
  const q = query.value.trim().toLocaleLowerCase()
  return choices.value.filter((o) => [o.label, o.hint, o.description].some((text) => text?.toLocaleLowerCase().includes(q)))
})
const shown = computed(() => {
  const given = props.selected
  return given && given.value === props.modelValue ? given : choices.value.find((o) => o.value === props.modelValue)
})
const selectedLabel = computed(() => shown.value?.label ?? props.modelValue?.toString() ?? '')
const parts = computed(() => splitAttrs(attrs, (k) => k.startsWith('data-')))
const framed = computed(() => !!props.icon || props.clearable || props.tinted)
const hasValue = computed(() => !isBlank(props.modelValue))
/** The field holds the search text (on a phone the sheet's own field does). */
const typing = computed(() => open.value && !sheet.value)

// A row with any of the extra texts, and every row of a remote list, is laid
// out in two lines: label and tag with the hint at the end, the description
// under them.
const rich = (o: Choice) => props.remote || !!(o.hint || o.description || o.tag || o.muted || o.accent)
const PRIMARY_TEXT = 'text-primary-700 dark:text-primary-300'
const MARK = 'rounded-sm bg-yellow-200 px-px text-inherit dark:bg-yellow-700/60'
function labelClass(o: Choice) {
  if (o.accent) return PRIMARY_TEXT
  if (o.muted) return 'text-fg-muted'
  return o.value === props.modelValue ? `font-semibold ${PRIMARY_TEXT}` : ''
}
/** A row's text as [before, typed, after] around the typed text of a remote search (any case); the whole text when it is not there. */
function cut(text: string, o: Choice): string[] {
  const q = props.remote && !o.accent ? query.value.trim() : ''
  const at = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1
  return at < 0 ? [text] : [text.slice(0, at), text.slice(at, at + q.length), text.slice(at + q.length)]
}
const clearLabel = computed(() => {
  const name = props.ariaLabel || props.placeholder
  return name ? t('ui.clearItem', { name }) : t('ui.clear')
})

function show() {
  if (props.disabled || open.value) return
  query.value = ''
  open.value = true
  if (props.remote) emit('search', '')
  active.value = filtered.value.findIndex((o) => o.value === props.modelValue && !o.disabled)
  if (active.value < 0) active.value = filtered.value.findIndex((o) => !o.disabled)
  teleported.value = !!root.value?.isConnected
  nextTick(() => {
    if (open.value && floats.value) floating.start()
  })
  scrollActive()
}
function hide() {
  open.value = false
}
watch(open, (v) => {
  if (!v) floating.stop()
})
function scrollActive() {
  nextTick(() => menu.value?.querySelector(`[data-index="${active.value}"]`)?.scrollIntoView?.({ block: 'nearest' }))
}
function search(e: Event) {
  const v = (e.target as HTMLInputElement).value
  if (!open.value) show()
  query.value = v
  if (props.remote) emit('search', v)
  relist()
}
/** After the list changed: its first enabled option is active and the panel is placed again. */
function relist() {
  active.value = filtered.value.findIndex((o) => !o.disabled)
  if (floats.value) nextTick(() => floating.update())
}
// Remote results arrive after the typing that asked for them. The same values
// given again (the caller rebuilt its list) leave the highlight where it is.
watch(filtered, (now, before) => {
  if (!open.value || !props.remote) return
  if (now.length !== before.length || now.some((o, i) => o.value !== before[i].value)) relist()
})
function pick(o: SelectOption) {
  if (props.disabled || o.disabled) return
  emit('update:modelValue', o.value)
  open.value = false
}
/** The × button: it goes away with the value, so focus that was on it (keyboard) moves to the field. */
function clear(e: MouseEvent) {
  emit('update:modelValue', null)
  open.value = false
  if (e.currentTarget === document.activeElement) field.value?.focus()
}
/** A press on the frame beside the text (icon, hint, chevron) goes to the field. */
function toField(e: MouseEvent) {
  if (e.target === field.value) return
  e.preventDefault()
  field.value?.focus()
  show()
}
function move(from: number, step: number) {
  for (let i = from; i >= 0 && i < filtered.value.length; i += step) {
    if (!filtered.value[i].disabled) {
      active.value = i
      scrollActive()
      return
    }
  }
}
function onKey(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'Escape') {
    if (open.value) {
      e.preventDefault()
      e.stopPropagation()
      open.value = false
    }
  } else if (e.key === 'Enter' && open.value) {
    e.preventDefault()
    const o = filtered.value[active.value]
    if (o) pick(o)
  } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (!open.value) return show()
    const step = e.key === 'ArrowDown' ? 1 : -1
    move(active.value + step, step)
  } else if ((e.key === 'Home' || e.key === 'End') && open.value) {
    e.preventDefault()
    if (e.key === 'Home') move(0, 1)
    else move(filtered.value.length - 1, -1)
  } else if (e.key === 'Tab') {
    open.value = false
  }
}
watch(
  () => props.disabled,
  (value) => {
    if (value) open.value = false
  }
)

function onChange(e: Event) {
  emit('update:modelValue', flat.value[Number((e.target as HTMLSelectElement).value)]?.value ?? null)
}
const selectedIndex = computed(() => flat.value.findIndex((o) => o.value === props.modelValue))

// The text field, bare or inside the frame. Attrs come last: native listeners
// passed in (input, focus) run after the select's own.
const control = computed(() =>
  mergeProps(
    {
      id: id.value,
      role: 'combobox',
      'aria-label': props.ariaLabel,
      'aria-autocomplete': 'list',
      'aria-expanded': open.value,
      'aria-controls': listId,
      'aria-activedescendant': typing.value && active.value >= 0 ? `${listId}-${active.value}` : undefined,
      value: typing.value ? query.value : selectedLabel.value,
      placeholder: props.placeholder,
      disabled: props.disabled,
      readonly: sheet.value,
      autocomplete: 'off',
      onFocus: () => sheet.value || show(),
      onClick: show,
      onInput: search,
      onKeydown: onKey,
      onBlur: () => sheet.value || (hide(), blur())
    },
    parts.value.control,
    a11y.value
  )
)
</script>

<template>
  <div v-if="searchable" ref="root" class="relative" v-bind="parts.wrap">
    <div
      v-if="framed"
      class="input input-group"
      :class="[
        size === 'sm' ? 'input-sm' : '',
        invalid ? 'input-error' : '',
        disabled ? 'input-group-disabled' : 'cursor-text',
        tinted && hasValue ? 'border-primary-300 bg-primary-50 dark:border-primary-700 dark:bg-primary-900/20' : ''
      ]"
      :title="typing ? undefined : [selectedLabel, shown?.hint].filter(Boolean).join(' ') || undefined"
      @mousedown="toField"
    >
      <SIcon v-if="icon" :name="icon" class="h-4 w-4 shrink-0 text-fg-subtle" />
      <input ref="field" v-bind="control" />
      <span v-if="shown?.hint && !typing" class="shrink-0 font-mono text-xs text-fg-subtle">{{ shown.hint }}</span>
      <button
        v-if="clearable && hasValue && !disabled"
        type="button"
        class="shrink-0 rounded text-fg-subtle hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
        :aria-label="clearLabel"
        @mousedown.prevent.stop
        @click.stop="clear"
      >
        <SIcon name="x" class="h-4 w-4" />
      </button>
      <SIcon name="chevron-down" class="h-4 w-4 shrink-0 text-fg-subtle" />
    </div>
    <template v-else>
      <input v-bind="control" class="input pr-8" :class="[size === 'sm' ? 'input-sm' : '', invalid ? 'input-error' : '']" />
      <SIcon name="chevron-down" class="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-subtle" />
    </template>
    <!-- Teleported in a live page; inline while detached (tests, previews; a
         disabled <Teleport> would still be stubbed away by shallowMount). -->
    <component :is="listHost.is" v-bind="listHost.props">
      <template v-if="sheet" #header>
        <input
          class="input"
          role="combobox"
          :aria-label="ariaLabel || t('ui.search')"
          aria-autocomplete="list"
          aria-expanded="true"
          :aria-controls="listId"
          :aria-activedescendant="active >= 0 ? `${listId}-${active}` : undefined"
          :value="query"
          :placeholder="t('ui.search')"
          autocomplete="off"
          data-autofocus
          @input="search"
          @keydown="onKey"
        />
      </template>
      <ul
        v-if="open || sheet"
        :id="listId"
        ref="menu"
        role="listbox"
        :aria-label="ariaLabel"
        :class="[
          sheet
            ? 'py-1'
            : [
                'overflow-y-auto rounded-control border border-gray-200 bg-white py-1 shadow-popover dark:border-dark-600 dark:bg-dark-800',
                teleported ? 'fixed z-popover' : 'absolute z-50 mt-1 w-full',
                // Results coming in must not resize the panel under the pointer.
                remote ? 'max-h-72 w-80 max-w-[calc(100vw-1rem)]' : 'max-h-60'
              ],
          // The footer sticks to the list's content edge: no padding under it for rows to show through.
          footer ? 'pb-0' : ''
        ]"
        :style="floats ? floating.style.value : undefined"
        @mousedown.prevent
      >
        <li
          v-if="note"
          role="presentation"
          class="-mt-1 mb-1 border-b border-line bg-primary-50 py-1.5 text-xs dark:bg-primary-900/20"
          :class="[PRIMARY_TEXT, sheet ? 'px-5' : 'px-3']"
        >
          {{ note }}
        </li>
        <template v-for="(o, i) in filtered" :key="i">
          <li
            v-if="o.group && (i === 0 || filtered[i - 1].group !== o.group)"
            role="presentation"
            class="pb-1 pt-2 text-xs font-semibold uppercase tracking-wide text-fg-subtle"
            :class="sheet ? 'px-5' : 'px-3'"
          >
            {{ o.group }}
          </li>
          <li
            :id="`${listId}-${i}`"
            role="option"
            :data-index="i"
            :aria-selected="o.value === modelValue"
            :aria-disabled="o.disabled || undefined"
            class="cursor-pointer py-2"
            :class="[
              sheet ? 'flex min-h-11 items-center gap-2 px-5 text-base' : ['text-sm', o.group ? 'pl-5 pr-3' : 'px-3'],
              i === active ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-700 dark:text-gray-200',
              o.disabled ? 'cursor-not-allowed opacity-40' : '',
              o.accent ? 'border-dashed border-line [&:not(:last-child)]:mb-1 [&:not(:last-child)]:border-b' : '',
              footer ? 'scroll-mb-8' : ''
            ]"
            @mouseenter="active = i"
            @click="pick(o)"
          >
            <span v-if="!rich(o)" class="min-w-0 flex-1">{{ o.label }}</span>
            <span v-else class="block min-w-0 flex-1">
              <span class="flex items-baseline gap-3">
                <span class="min-w-0 flex-1 truncate" :class="labelClass(o)">
                  <template v-for="(part, k) in cut(o.label, o)" :key="k">
                    <mark v-if="k === 1" :class="MARK">{{ part }}</mark>
                    <template v-else>{{ part }}</template>
                  </template>
                  <span v-if="o.tag" class="ml-1.5 rounded bg-surface-2 px-1 py-px align-middle text-[10px] font-normal leading-4 text-fg-muted">{{ o.tag }}</span>
                </span>
                <span v-if="o.hint" class="shrink-0 font-mono text-xs text-fg-subtle">{{ o.hint }}</span>
              </span>
              <!-- An accent row's second line explains the row, so it wraps; an option's is data and stays one line. -->
              <span v-if="o.description" class="block text-xs font-normal text-fg-muted" :class="o.accent ? '' : 'truncate'">
                <template v-for="(part, k) in cut(o.description, o)" :key="k">
                  <mark v-if="k === 1" :class="MARK">{{ part }}</mark>
                  <template v-else>{{ part }}</template>
                </template>
              </span>
            </span>
            <SIcon v-if="sheet && o.value === modelValue" name="check" class="h-4 w-4 shrink-0 text-primary-600" />
          </li>
        </template>
        <li v-if="loading" role="presentation" class="flex items-center gap-2 py-2 text-sm text-fg-muted" :class="sheet ? 'px-5' : 'px-3'">
          <SSpinner size="sm" decorative />
          {{ t('ui.searching') }}
        </li>
        <li v-else-if="!filtered.length" role="presentation" class="px-3 py-2 text-sm text-fg-muted">{{ noResults ?? t('ui.noResults') }}</li>
        <li
          v-if="footer"
          role="presentation"
          class="sticky bottom-0 mt-1 border-t border-line py-1.5 text-xs text-fg-muted"
          :class="sheet ? 'bg-surface px-5' : 'bg-white px-3 dark:bg-dark-800'"
        >
          {{ footer }}
        </li>
      </ul>
    </component>
  </div>
  <select
    v-else
    :id="id"
    class="input"
    :class="[size === 'sm' ? 'input-sm' : '', invalid ? 'input-error' : '']"
    :value="selectedIndex"
    :disabled="disabled"
    :aria-label="ariaLabel"
    v-bind="{ ...attrs, ...a11y }"
    @change="onChange"
    @blur="blur"
  >
    <option v-if="placeholder !== undefined" :value="-1">{{ placeholder }}</option>
    <template v-for="(r, i) in rows" :key="i">
      <optgroup v-if="r.group !== undefined" :label="r.group">
        <option v-for="c in r.items" :key="c.idx" :value="c.idx" :disabled="c.o.disabled">{{ c.o.label }}</option>
      </optgroup>
      <template v-else>
        <option v-for="c in r.items" :key="c.idx" :value="c.idx" :disabled="c.o.disabled">{{ c.o.label }}</option>
      </template>
    </template>
  </select>
</template>
