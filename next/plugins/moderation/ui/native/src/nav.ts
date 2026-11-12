import { satisfiesRange, type PluginHost } from '@sub2api/host'

// The sections of the moderation page and their groups. A console from UI
// 3.14 lists them in its sidebar (manifest ui.pages.dashboard.nav, which
// nav.spec.ts keeps equal to this list) and opens ?tab=<key>; an older one
// ignores the declaration and the page shows the same list as tabs.

/** Who may open a section: readers, moderation:manage, or those who may edit the settings. */
export type Access = 'read' | 'manage' | 'settings'

export const NAV_GROUPS = [
  {
    key: 'monitor',
    items: [
      { key: 'overview', icon: 'dashboard', access: 'read' },
      { key: 'events', icon: 'list', access: 'read' },
      { key: 'test', icon: 'play', access: 'manage' }
    ]
  },
  {
    key: 'policy',
    items: [
      { key: 'flows', icon: 'node', access: 'read' },
      { key: 'bindings', icon: 'link', access: 'read' },
      { key: 'policy', icon: 'shield', access: 'settings' },
      { key: 'cache', icon: 'clock', access: 'settings' }
    ]
  },
  {
    key: 'users',
    items: [
      { key: 'watch', icon: 'eye', access: 'read' },
      { key: 'blocks', icon: 'lock', access: 'read' },
      { key: 'lists', icon: 'user', access: 'settings' },
      { key: 'analysis', icon: 'bolt', access: 'settings' }
    ]
  },
  {
    key: 'resources',
    items: [
      { key: 'auditors', icon: 'filter', access: 'read' },
      { key: 'channels', icon: 'globe', access: 'read' },
      { key: 'jev', icon: 'edit', access: 'settings' }
    ]
  },
  {
    key: 'system',
    items: [
      { key: 'response', icon: 'warning', access: 'settings' },
      { key: 'records', icon: 'ledger', access: 'settings' },
      { key: 'advanced', icon: 'settings', access: 'settings' }
    ]
  }
] as const satisfies ReadonlyArray<{ key: string; items: ReadonlyArray<{ key: string; icon: string; access: Access }> }>

export type SectionKey = (typeof NAV_GROUPS)[number]['items'][number]['key']
export type NavGroupKey = (typeof NAV_GROUPS)[number]['key']

interface NavItem {
  key: SectionKey
  icon: string
  access: Access
}
/** The list with plain item types, for the helpers below. */
const GROUPS: ReadonlyArray<{ key: NavGroupKey; items: readonly NavItem[] }> = NAV_GROUPS
const ITEMS = GROUPS.flatMap((g) => g.items)

/** The settings pages: each shows the ui:section groups of forms/settings.ui.json whose page is its key. */
export const isSettingsPage = (key: SectionKey) => ITEMS.find((i) => i.key === key)?.access === 'settings'

/** Old links: ?tab=settings opened the one settings tab. */
const RENAMED: Record<string, SectionKey> = { settings: 'policy' }

/** The section a ?tab= value names among the visible ones, else the first visible one. */
export function sectionOf(asked: unknown, visible: readonly SectionKey[]): SectionKey {
  const key = typeof asked === 'string' ? (RENAMED[asked] ?? asked) : ''
  return (visible as readonly string[]).includes(key) ? (key as SectionKey) : visible[0]
}

/** The groups with the sections the caller may open. */
export function visibleGroups(may: (a: Access) => boolean) {
  return GROUPS.map((g) => ({ key: g.key, items: g.items.filter((i) => may(i.access)) })).filter((g) => g.items.length)
}

/** The console lists the sections in its sidebar (UI 3.14). */
export const hostShowsNav = (host: Pick<PluginHost, 'version'>) => satisfiesRange(host.version, '>=3.14.0')
