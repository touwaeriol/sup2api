<script setup lang="ts">
// Button with the console's .btn classes. `to` renders a <RouterLink>, `href`
// an <a> (both with the same classes); otherwise a native <button> honouring
// `type`, `loading` (spinner + disabled + aria-busy) and `disabled`. A
// disabled / loading link renders an <a aria-disabled="true"> without href.
// `icon` / `iconRight` are SIcon names; `iconOnly` drops the text padding
// (square button) and needs `ariaLabel`; `square` makes any button square.
// `size` xs is the compact variant for table cells.
// `tone="danger"` / `"info"` colours a ghost / secondary button's text red /
// blue: a text action that stops or starts something (disable / enable).
// `fab` (with an `icon`): on a compact screen a 56px round button floating
// bottom-right above the home indicator, its text visually hidden (kept for
// screen readers), marked `data-fab` so the page layout can keep room for it
// under the last row, and not shown while the bulk selection dock (SBulkBar)
// holds the bottom of the screen; an ordinary button on wide screens.
// In a toolbar (toolbar.ts) the toolbar sets the size, and in a page's own
// toolbar a primary button with an icon is a fab unless `:fab="false"`.
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import SSpinner from './SSpinner.vue'
import SIcon from './SIcon.vue'
import { isDev } from './dev'
import { useViewport } from './viewport'
import { useToolbar } from './toolbar'

const props = withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'success' | 'warning'
    size?: 'xs' | 'sm' | 'md' | 'lg'
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit' | 'reset'
    block?: boolean
    to?: string | RouteLocationRaw
    href?: string
    icon?: string
    iconRight?: string
    iconOnly?: boolean
    square?: boolean
    ariaLabel?: string
    tone?: 'default' | 'danger' | 'info'
    fab?: boolean
  }>(),
  { variant: 'secondary', size: 'md', type: 'button', tone: 'default', fab: undefined }
)

if (isDev && props.iconOnly && !props.ariaLabel) {
  console.warn('[@sub2api/ui] <SButton icon-only> needs an aria-label')
}
if (isDev && props.fab && !props.icon) {
  console.warn('[@sub2api/ui] <SButton fab> needs an icon')
}

// Literal class names so Tailwind keeps them (it drops unused .btn-* rules).
const VARIANT = {
  primary: 'btn-primary',
  secondary: 'btn-secondary',
  ghost: 'btn-ghost',
  danger: 'btn-danger',
  success: 'btn-success',
  warning: 'btn-warning'
}
const TONE = {
  default: '',
  danger: '!text-red-600 hover:!bg-red-50 dark:!text-red-400 dark:hover:!bg-red-900/20',
  info: '!text-blue-600 hover:!bg-blue-50 dark:!text-blue-400 dark:hover:!bg-blue-900/20'
}
const SIZE = { xs: 'btn-xs', sm: 'btn-sm', md: 'btn-md', lg: 'btn-lg' }
// Square = as wide as tall, so icon buttons stay square when touch screens raise the control height.
const SQUARE = 'aspect-square px-0'
const ICON: Record<string, string> = { xs: 'h-3.5 w-3.5', sm: 'h-3.5 w-3.5', md: 'h-4 w-4', lg: 'h-5 w-5' }
// Hidden while rows are selected: the bulk selection dock is shorter than the fab and would cover only part of it.
// The console layout pads a page holding a fab by 5.5rem (+ the safe area) at the bottom:
// the 3.5rem button plus its 1rem offset and a 1rem gap. Change them together.
const FAB = {
  button:
    'fixed bottom-[calc(1rem+env(safe-area-inset-bottom))] right-4 z-20 h-14 w-14 rounded-full px-0 shadow-lg [:root:has([data-bulk-dock])_&]:hidden',
  icon: 'h-6 w-6'
}
const { compact } = useViewport()
const toolbar = useToolbar()
const size = computed(() => (toolbar ? 'md' : props.size))
const floats = computed(() => props.fab ?? (!!toolbar?.float && props.variant === 'primary' && !props.block))
const fab = computed(() => floats.value && !!props.icon && compact.value)
const inactive = computed(() => props.disabled || props.loading)
const isLink = computed(() => !!(props.to || props.href))
const cls = computed(() => [
  'btn',
  VARIANT[props.variant],
  SIZE[size.value],
  props.block ? 'w-full' : '',
  props.iconOnly || props.square ? SQUARE : '',
  fab.value ? FAB.button : '',
  TONE[props.tone],
  isLink.value && inactive.value ? 'pointer-events-none cursor-not-allowed opacity-50' : ''
])
// Element and its own attributes: a disabled / loading link is an <a> without href.
const target = computed(() => {
  if (isLink.value && inactive.value) return { is: 'a', attrs: { role: 'link', 'aria-disabled': 'true' } }
  if (props.to) return { is: RouterLink, attrs: { to: props.to } }
  if (props.href) return { is: 'a', attrs: { href: props.href } }
  return { is: 'button', attrs: { type: props.type, disabled: inactive.value } }
})
</script>

<template>
  <component
    :is="target.is"
    v-bind="target.attrs"
    :class="cls"
    :aria-label="ariaLabel"
    :aria-busy="loading || undefined"
    :data-fab="fab || undefined"
  >
    <SSpinner v-if="loading" size="sm" decorative />
    <SIcon v-else-if="icon" :name="icon" :class="fab ? FAB.icon : ICON[size]" />
    <span v-if="fab && !iconOnly && !square" class="sr-only"><slot /></span>
    <slot v-else-if="!iconOnly || !icon" />
    <SIcon v-if="iconRight && !iconOnly && !fab" :name="iconRight" :class="ICON[size]" />
  </component>
</template>
