import { ref, watch, type Ref } from 'vue'
import type { NavItem, NavSection } from '@/stores/app'
import { SIDEBAR_VIEWS } from './sidebarViews'

/**
 * The console menu shows three groups (CONTRACTS §90): the console every role
 * has (section "overview"), personal ("me") and admin, which holds every other
 * section in the server's order: gateway, security and finance listed flat,
 * the sections of FOLDED_SECTIONS and a plugin's own folded into a submenu,
 * and last the entries that open a sidebar view (system settings).
 */
export type NavGroupKey = 'overview' | 'me' | 'admin'

export interface NavLink {
  key: string
  item: NavItem
}

type NavRow = (NavLink & { section?: never }) | { key: string; section: NavSection; item?: never }

/** A row of a group: a menu entry, or a section folded into a submenu. `gap` sets it a little apart from the row above. */
export type NavEntry = NavRow & { gap?: boolean }

export interface NavGroup {
  key: NavGroupKey
  entries: NavEntry[]
}

/** Sections that make a caller an operator; only then is the admin group titled. */
const OPERATOR_SECTIONS = ['gateway', 'security', 'finance', 'users', 'system']

/** Core sections folded into a submenu, as a plugin's own section is. */
const FOLDED_SECTIONS = ['users', 'plugins', 'system']

/** A flat section that continues the block of another one; the rest start their own. */
const BLOCK_OF: Record<string, string> = { finance: 'security' }

/** A plugin's own section is keyed "<plugin>:<id>"; core sections are bare ids. */
const isPluginSection = (s: NavSection) => s.key.includes(':')

const folds = (s: NavSection) => isPluginSection(s) || FOLDED_SECTIONS.includes(s.key)

/** The admin group reads as blocks: the gateway, security with finance, the submenus, the view entries. */
const blockOf = (s: NavSection) => (folds(s) ? 'submenus' : (BLOCK_OF[s.key] ?? s.key))

const linkOf = (item: NavItem): NavLink => ({ key: `item:${item.id}`, item })

/** A section that folds does so with two pages or more; one page stands for itself. */
const sectionRows = (s: NavSection): NavRow[] =>
  folds(s) && s.items.length > 1 ? [{ key: `section:${s.key}`, section: s }] : s.items.map(linkOf)

function adminEntries(menus: NavSection[]): NavEntry[] {
  const views = SIDEBAR_VIEWS.flatMap(v => v.entry(menus) ?? [])
  // A view's entry takes the place of the menu entry of the same page (/settings).
  const sections = menus
    .filter(s => s.key !== 'overview' && s.key !== 'me')
    .map(s => ({ ...s, items: s.items.filter(i => !views.some(v => v.path === i.path)) }))
  const rows = [
    ...sections.flatMap(s => sectionRows(s).map(row => ({ row, block: blockOf(s) }))),
    ...views.map(v => ({ row: linkOf(v), block: 'views' }))
  ]
  return rows.map(({ row, block }, i) => ({ ...row, gap: i > 0 && rows[i - 1].block !== block }))
}

export function groupMenus(menus: NavSection[]): NavGroup[] {
  const linksIn = (key: string) => menus.filter(s => s.key === key).flatMap(s => s.items.map(linkOf))
  const groups: NavGroup[] = [
    { key: 'overview', entries: linksIn('overview') },
    { key: 'me', entries: linksIn('me') },
    { key: 'admin', entries: adminEntries(menus) }
  ]
  return groups.filter(g => g.entries.length > 0)
}

/**
 * The admin title is for operators: a user whose only other section is a
 * public plugin page sees that page without it.
 */
export function showGroupTitle(g: NavGroup, menus: NavSection[]): boolean {
  return g.key !== 'admin' || menus.some(s => OPERATOR_SECTIONS.includes(s.key))
}

/** The links a collapsed sidebar shows as icons: submenus unfolded. */
export function groupLinks(g: NavGroup): NavLink[] {
  return g.entries.flatMap(e => (e.section ? e.section.items.map(linkOf) : [e]))
}

const OPEN_KEY = 's2a.navOpen'

function readOpen(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(OPEN_KEY) || '[]')
    return Array.isArray(v) ? v.filter((k): k is string => typeof k === 'string') : []
  } catch {
    return []
  }
}

/**
 * Which submenus are expanded, remembered across visits. The submenu of the
 * current page opens by itself; closing it again is the user's call.
 */
export function useOpenSections(activeSection: Ref<string | undefined>) {
  const open = ref(new Set(readOpen()))
  watch(activeSection, k => k && open.value.add(k), { immediate: true })
  watch(
    open,
    v => {
      try {
        localStorage.setItem(OPEN_KEY, JSON.stringify([...v]))
      } catch {
        // Storage full or blocked: the submenus stay as they are, just not remembered.
      }
    },
    { deep: true }
  )
  const isOpen = (key: string) => open.value.has(key)
  const toggle = (key: string) => (isOpen(key) ? open.value.delete(key) : open.value.add(key))
  return { isOpen, toggle }
}
