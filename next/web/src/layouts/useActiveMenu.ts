import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

/** The current page and the pages above it (meta.parent, as the breadcrumbs follow), nearest first. */
export function useRouteLineage() {
  const route = useRoute()
  const router = useRouter()
  return computed(() => {
    const out: string[] = []
    let p: string | undefined = route.path
    while (p && !out.includes(p)) {
      out.push(p)
      p = out.length === 1 ? route.meta.parent : router.resolve(p).meta.parent
    }
    return out
  })
}

/**
 * The menu entry of the current page: the page itself, else the nearest page
 * above it, else a path prefix. So /plugins/x, a child of the plugin list,
 * lights up Installed plugins.
 */
export function useActiveMenuPath() {
  const app = useAppStore()
  const route = useRoute()
  const lineage = useRouteLineage()
  return computed(() => {
    const paths = app.menus.flatMap(s => s.items.map(i => i.path))
    const own = lineage.value.find(p => paths.includes(p))
    return own ?? paths.filter(x => x !== '/' && route.path.startsWith(x + '/')).sort((a, b) => b.length - a.length)[0]
  })
}
