<script setup lang="ts">
// The dialog surface behind SModal, SDrawer and the phone sheets of
// SDropdown / SSelect: teleported to <body>, a layer of the modal stack
// (useDialog), with a header (`title` / `description` or #header, and the
// close button), a scrolling body and #footer. `form`:
// - center: the floating dialog (the console's .modal-* classes); on phones
//   it keeps 16px from the screen edges;
// - right / left: a full-height side panel at most `width` wide (sm 24rem,
//   md 32rem, lg 42rem, xl 56rem, 2xl 72rem or any CSS width; also the
//   center dialog's width);
// - sheet: a bottom sheet with a grab handle, swipe down closes it (unless
//   persistent);
// - fullscreen: a full-screen dialog sliding up, with a ×;
// - page: a full-screen page sliding in from the right, with a back arrow
//   before the title.
// Sheets and pages fit above the soft keyboard (--s-vvh / --s-vvt) and keep
// their footer clear of the home indicator. On phones the footer's children
// share its width (two buttons side by side). `closeButton` false hides the
// × / arrow (the layer still closes on the backdrop, Escape, Back or a
// swipe); `closable` false hides it and implies persistent. `flush` drops
// the body padding (full-width lists). `fill` keeps one size whatever the
// content: the panel is as tall as its form's max-height allows (pickers
// whose list changes length, multi-step editors) and the body keeps its
// scrollbar gutter, so neither the dialog nor the content width moves.
import { computed, ref, useId } from 'vue'
import { useUiT } from '../i18n'
import SIcon from '../SIcon.vue'
import { useDialog, useSheetSwipe, type OverlayForm } from '../overlay'
import { resetTitleContext } from '../titleContext'
import { provideToolbar } from '../toolbar'
resetTitleContext()
provideToolbar(null)

const props = withDefaults(
  defineProps<{
    open: boolean
    form: OverlayForm
    title?: string
    description?: string
    describedby?: string
    width?: string
    closable?: boolean
    persistent?: boolean
    top?: boolean
    closeButton?: boolean
    fill?: boolean
    flush?: boolean
  }>(),
  { title: undefined, description: undefined, describedby: undefined, width: 'md', closable: true, closeButton: true }
)
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'close'): void }>()
const t = useUiT()
const titleId = useId()
const panel = ref<HTMLElement>()
const body = ref<HTMLElement>()
const { dismissible, close, zIndex } = useDialog(props, emit, panel)
const swipe = useSheetSwipe(panel, body, () => props.form === 'sheet' && dismissible.value, close)

const WIDTHS: Record<string, string> = { sm: '24rem', md: '32rem', lg: '42rem', xl: '56rem', '2xl': '72rem' }
// The two families: the console's centered modal and the panel anchored to a screen edge.
const CENTER = {
  transition: 's-modal',
  panel: 'modal-content flex flex-col max-md:w-[calc(100vw-32px)]',
  header: 'modal-header',
  title: 'text-base font-semibold text-gray-900 dark:text-white',
  body: 'modal-body flex-1 overflow-y-auto',
  pad: '',
  footer: 'modal-footer max-md:flex-wrap max-md:*:flex-1'
}
const PANEL = {
  transition: 's-drawer',
  panel: 's-drawer-panel flex w-full flex-col border-line bg-surface shadow-modal outline-none',
  header: 'flex shrink-0 touch-none items-start justify-between gap-3 border-b border-line px-5 py-3.5',
  title: 'truncate text-base font-semibold text-fg',
  body: 'min-h-0 flex-1 overflow-y-auto',
  pad: 'px-5 py-4',
  footer: 'flex shrink-0 items-center justify-end gap-3 border-t border-line px-5 py-3.5 max-md:flex-wrap max-md:*:flex-1'
}
// Per form: the backdrop, the panel's own classes, and whether `width` applies.
const FORMS: Record<OverlayForm, { overlay: string; panel: string; sized: boolean }> = {
  center: { overlay: 'modal-overlay', panel: '', sized: true },
  right: {
    overlay: 'fixed inset-0 flex justify-end bg-black/40 backdrop-blur-[2px]',
    panel: 's-drawer-right h-full border-l',
    sized: true
  },
  left: {
    overlay: 'fixed inset-0 flex justify-start bg-black/40 backdrop-blur-[2px]',
    panel: 's-drawer-left h-full border-r',
    sized: true
  },
  sheet: {
    overlay: 'fixed inset-x-0 top-[var(--s-vvt,0px)] flex h-[var(--s-vvh,100dvh)] items-end bg-black/40 backdrop-blur-[2px]',
    panel: 's-drawer-bottom max-h-[calc(var(--s-vvh,100dvh)*0.92)] rounded-t-panel border-t pb-[env(safe-area-inset-bottom)]',
    sized: false
  },
  fullscreen: {
    overlay: 'fixed inset-x-0 top-[var(--s-vvt,0px)] flex h-[var(--s-vvh,100dvh)] bg-black/40',
    panel: 's-drawer-bottom h-full pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)]',
    sized: false
  },
  page: {
    overlay: 'fixed inset-x-0 top-[var(--s-vvt,0px)] flex h-[var(--s-vvh,100dvh)] bg-black/40',
    panel: 's-drawer-right h-full pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)]',
    sized: false
  }
}
const parts = computed(() => (props.form === 'center' ? CENTER : PANEL))
const panelClass = computed(() => [parts.value.panel, FORMS[props.form].panel, props.fill ? 'h-full' : ''])
const bodyClass = computed(() => [parts.value.body, props.flush ? '' : parts.value.pad, props.fill ? '[scrollbar-gutter:stable]' : ''])
const panelStyle = computed(() => (FORMS[props.form].sized ? { maxWidth: WIDTHS[props.width] ?? props.width } : undefined))
const showClose = computed(() => props.closable && props.closeButton)
const closer = computed(() =>
  props.form === 'page' ? { icon: 'arrow-left', label: t('ui.back'), class: 'order-first' } : { icon: 'x', label: t('ui.close'), class: '' }
)
</script>

<template>
  <Teleport to="body">
    <Transition :name="parts.transition">
      <div v-if="open" :class="FORMS[form].overlay" :style="{ zIndex }" @mousedown.self="dismissible && close()">
        <div
          ref="panel"
          :class="panelClass"
          :style="panelStyle"
          role="dialog"
          aria-modal="true"
          tabindex="-1"
          :aria-labelledby="title || $slots.header ? titleId : undefined"
          :aria-describedby="describedby"
        >
          <div v-if="form === 'sheet'" class="flex shrink-0 touch-none justify-center pb-1 pt-2" @pointerdown="swipe">
            <div class="h-1 w-10 rounded-full bg-line-strong" aria-hidden="true" />
          </div>
          <div v-if="title || $slots.header || showClose" :class="parts.header" @pointerdown="swipe">
            <div :id="titleId" class="min-w-0 flex-1">
              <slot name="header">
                <h3 :class="parts.title">{{ title }}</h3>
                <p v-if="description" class="mt-0.5 text-xs leading-5 text-fg-muted">{{ description }}</p>
              </slot>
            </div>
            <button
              v-if="showClose"
              type="button"
              class="btn btn-ghost btn-sm w-8 shrink-0 px-0"
              :class="closer.class"
              data-overlay-close
              :aria-label="closer.label"
              @click="close"
            >
              <SIcon :name="closer.icon" class="h-5 w-5" />
            </button>
          </div>
          <div ref="body" :class="bodyClass">
            <slot />
          </div>
          <div v-if="$slots.footer" :class="parts.footer">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.s-modal-enter-active,
.s-modal-leave-active {
  transition: opacity 0.15s ease;
}
.s-modal-enter-from,
.s-modal-leave-to {
  opacity: 0;
}
.s-drawer-enter-active,
.s-drawer-leave-active {
  transition: opacity 0.2s ease;
}
.s-drawer-enter-active .s-drawer-panel,
.s-drawer-leave-active .s-drawer-panel {
  transition: transform 0.2s ease;
}
.s-drawer-enter-from,
.s-drawer-leave-to {
  opacity: 0;
}
.s-drawer-enter-from .s-drawer-right,
.s-drawer-leave-to .s-drawer-right {
  transform: translateX(100%);
}
.s-drawer-enter-from .s-drawer-left,
.s-drawer-leave-to .s-drawer-left {
  transform: translateX(-100%);
}
.s-drawer-enter-from .s-drawer-bottom,
.s-drawer-leave-to .s-drawer-bottom {
  transform: translateY(100%);
}
</style>
