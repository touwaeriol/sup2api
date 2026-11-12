import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { defineComponent, h } from 'vue'
import type { UIPageNav } from '@/api/types'
import { activeSection, usePluginPageView } from './pluginNav'

const nav: UIPageNav = {
  groups: [
    { key: 'run', label: { zh: '运行' }, items: [{ key: 'overview', label: { zh: '概览' }, icon: 'dashboard' }, { key: 'events', label: '记录' }] },
    { key: 'sys', label: '系统', items: [{ key: 'settings', label: '设置' }] }
  ]
}
const pages: Record<string, { page: { type: string; nav?: UIPageNav } }> = {
  'mod/dash': { page: { type: 'native', nav } },
  'mod/plain': { page: { type: 'native' } }
}
vi.mock('@/stores/plugins', () => ({ usePluginStore: () => ({ page: (k: string, p: string) => pages[`${k}/${p}`] ?? null }) }))

const Blank = defineComponent({ render: () => null })

/** The view and its menu on the page at path. */
async function viewAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:plugin/:page', component: Blank },
      { path: '/:any(.*)*', component: Blank }
    ]
  })
  await router.push(path)
  let out: { key?: string; groups?: unknown; entry?: unknown } = {}
  mount(
    defineComponent({
      setup() {
        const view = usePluginPageView()
        const groups = view.value?.useGroups()
        out = {
          key: view.value?.key,
          groups: groups?.value,
          entry: view.value?.entry([{ key: 'x', items: [{ id: 'm', path: '/p/mod/dash' }] }])
        }
        return () => h('div')
      }
    }),
    { global: { plugins: [router] } }
  )
  return out
}

describe('plugin page sections in the sidebar', () => {
  it('opens the asked section, else the first one', () => {
    expect(activeSection(nav, 'settings')).toBe('settings')
    expect(activeSection(nav, 'gone')).toBe('overview')
    expect(activeSection(nav, undefined)).toBe('overview')
  })

  it('gives a page that declares sections a view of its own, entered from its menu entry', async () => {
    const v = await viewAt('/p/mod/dash?tab=events')
    expect(v.key).toBe('plugin:mod/dash')
    expect(v.entry).toEqual({ id: 'm', path: '/p/mod/dash' })
    expect(v.groups).toEqual([
      {
        key: 'run',
        label: '运行',
        links: [
          { key: 'overview', item: { id: 'mod:overview', label: { zh: '概览' }, icon: 'dashboard', path: '/p/mod/dash?tab=overview', pluginKey: 'mod' }, active: false },
          { key: 'events', item: { id: 'mod:events', label: '记录', icon: undefined, path: '/p/mod/dash?tab=events', pluginKey: 'mod' }, active: true }
        ]
      },
      { key: 'sys', label: '系统', links: [{ key: 'settings', item: { id: 'mod:settings', label: '设置', icon: undefined, path: '/p/mod/dash?tab=settings', pluginKey: 'mod' }, active: false }] }
    ])
  })

  it('leaves pages without sections, and other pages, in the console menu', async () => {
    expect((await viewAt('/p/mod/plain')).key).toBeUndefined()
    expect((await viewAt('/p/other/dash')).key).toBeUndefined()
    expect((await viewAt('/dashboard')).key).toBeUndefined()
  })
})
