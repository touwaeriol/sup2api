import { describe, expect, it } from 'vitest'
import manifest from '../../../manifest.json'
import messages from './messages'
import { NAV_GROUPS, isSettingsPage, sectionOf, visibleGroups, type SectionKey } from './nav'

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
