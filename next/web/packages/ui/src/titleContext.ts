import { computed, inject, provide, type InjectionKey, type ComputedRef, type Ref } from 'vue'

/** What a page shows in the title bar of the shell it is in: its own title and description. */
export interface PageHead {
  /** The page header that put it there; a header only takes back its own. */
  owner: number
  title: string
  description: string
}

/**
 * The title bar of an app shell, as a page header sees it. `head` is where
 * the page puts its title and description. `extra` is the selector of an
 * element beside that title, which takes what a page shows with its title
 * (a version, a status badge). A page's actions stay in the page.
 */
export interface PageHeadSink {
  head: Ref<PageHead | null>
  extra?: string | null
}

const TITLE_CONTEXT: InjectionKey<ComputedRef<string>> = Symbol('s-title-context')
const PAGE_HEAD_SINK: InjectionKey<PageHeadSink | null> = Symbol('s-page-head-sink')
const normalize = (title: string | undefined) => (title || '').trim().replace(/\s+/g, ' ')

/**
 * Scope belongs to a visible navigation title, never to arbitrary DOM text.
 * An app shell passes `sink`, its title bar: a page header directly in the
 * shell shows its title and description there, whatever the title says. A
 * nested scope (a tab, a dialog) has no sink; a header in it stays in place
 * and only leaves out a title that repeats the scope's.
 */
export function provideTitleContext(title: () => string | undefined, sink: PageHeadSink | null = null) {
  provide(TITLE_CONTEXT, computed(() => normalize(title())))
  provide(PAGE_HEAD_SINK, sink)
}

/** Dialogs start an independent surface even though Teleport retains injection. */
export function resetTitleContext() {
  provideTitleContext(() => '')
}

export function useTitleVisibility(title: () => string | undefined, always: () => boolean = () => false) {
  const parent = inject(TITLE_CONTEXT, null)
  return computed(() => always() || !parent?.value || normalize(title()) !== parent.value)
}

/** The title bar of the enclosing shell, when the caller is directly in one. */
export function usePageHeadSink() {
  return inject(PAGE_HEAD_SINK, null)
}
