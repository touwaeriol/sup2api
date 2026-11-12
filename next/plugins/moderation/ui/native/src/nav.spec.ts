import { describe, expect, it } from 'vitest'
import { nextTick, ref } from 'vue'
import manifest from '../../../manifest.json'
import messages from './messages'
import { NAV_GROUPS, isSettingsPage, sectionOf, useSection, visibleGroups, type SectionKey } from './nav'

// The console lists the sections from the manifest (ui.pages.dashboard.nav);
// an older console gets them as tabs from nav.ts. The two must not drift.
describe('page sections', () => {
  it('declares in the manifest what nav.ts lists, with the same labels and permissions', () => {
    const label = (part: 'groups' | 'items', key: string) => ({ en: messages.en.nav[part][key as never], zh: messages.zh.nav[part][key as never] })
    const permissions = { read: {}, manage: { permission: 'moderation:manage' }, settings: { permission: 'moderation:manage', corePermission: 'plugin:manage' } }
    const expected = NAV_GROUPS.map((g) => ({
      key: g.key,
      label: label('groups', g.key),
      items: g.items.map((i) => ({ key: i.key, label: label('items', i.key), icon: i.icon, ...permissions[i.access] }))
    }))
    expect(manifest.ui.pages.dashboard.nav.groups).toEqual(expected)
  })

  it('opens the asked section when visible, maps the old settings tab and falls back to the first', () => {
    const visible: SectionKey[] = ['overview', 'events', 'policy']
    expect(sectionOf('events', visible)).toBe('events')
    expect(sectionOf('settings', visible)).toBe('policy')
    expect(sectionOf('settings', ['overview'])).toBe('overview')
    expect(sectionOf('nope', visible)).toBe('overview')
    expect(sectionOf(undefined, visible)).toBe('overview')
  })

  it('hides what the caller may not open and the groups left empty', () => {
    const groups = visibleGroups((a) => a === 'read')
    const keys = groups.flatMap((g) => g.items.map((i) => i.key))
    expect(keys).not.toContain('test')
    expect(keys.some((k) => isSettingsPage(k))).toBe(false)
    expect(groups.map((g) => g.key)).not.toContain('system')
  })
})

describe('useSection', () => {
  type Query = Record<string, string | undefined>
  type Route = { path: string; query: Query }
  /** The console router as far as useSection uses it: the current route, push, replace and back. */
  function fakeRouter(path: string) {
    const parse = (to: string): Route => {
      const [p, q = ''] = to.split('?')
      return { path: p, query: Object.fromEntries(new URLSearchParams(q)) }
    }
    const stack = [parse(path)]
    const currentRoute = ref<Route>(stack[0])
    const clean = (q: Query) => Object.fromEntries(Object.entries(q).filter(([, v]) => v !== undefined)) as Query
    const go = (how: 'push' | 'replace') => (to: string | { query: Query }) => {
      const next = typeof to === 'string' ? parse(to) : { path: currentRoute.value.path, query: clean(to.query) }
      if (how === 'push') stack.push(next)
      else stack[stack.length - 1] = next
      currentRoute.value = next
      return Promise.resolve()
    }
    const back = () => {
      if (stack.length > 1) stack.pop()
      currentRoute.value = stack[stack.length - 1]
    }
    return { currentRoute, push: go('push'), replace: go('replace'), back }
  }
  const flush = () => nextTick()
  async function at(path: string, inSidebar = true) {
    const router = fakeRouter(path)
    const section = useSection(router as never, ref<SectionKey[]>(['overview', 'events', 'policy']), inSidebar)
    await flush()
    return { router, section, q: () => router.currentRoute.value.query }
  }

  it('writes the section it resolved back into ?tab=, so closing a linked record keeps the records', async () => {
    const { router, section, q } = await at('/p/moderation/dashboard?record=12')
    expect(section.value).toBe('events')
    expect(q()).toEqual({ record: '12', tab: 'events' })
    await router.replace({ query: { ...q(), record: undefined } })
    await flush()
    expect(section.value).toBe('events')
  })

  it('turns an old ?tab=settings link into the page it opens', async () => {
    const { section, q } = await at('/p/moderation/dashboard?tab=settings')
    expect(section.value).toBe('policy')
    expect(q().tab).toBe('policy')
  })

  it('adds a history entry in the sidebar, replaces it with tabs, and ignores sections the caller cannot open', async () => {
    for (const inSidebar of [true, false]) {
      const { router, section, q } = await at('/p/moderation/dashboard?tab=overview', inSidebar)
      section.value = 'events'
      await flush()
      expect(q().tab).toBe('events')
      router.back()
      await flush()
      expect(q().tab).toBe(inSidebar ? 'overview' : 'events')
      section.value = 'test' as SectionKey
      await flush()
      expect(section.value).not.toBe('test')
    }
  })

  it('keeps its section while the console leaves the page', async () => {
    const { router, section } = await at('/p/moderation/dashboard?tab=events')
    await router.push('/settings?tab=sticky')
    await flush()
    expect(section.value).toBe('events')
    expect(router.currentRoute.value.query.tab).toBe('sticky')
  })
})
