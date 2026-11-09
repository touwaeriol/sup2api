import { useI18n } from 'vue-i18n'
import { lt } from '@/i18n'
import type { NavItem } from '@/stores/app'

/** A menu entry's label: a core entry's i18n key, else the label the server sent. */
export function useNavItemLabel() {
  const { t } = useI18n()
  return (item: NavItem) => (item.labelKey ? t(item.labelKey) : lt(item.label))
}

/** The look of a navigation entry, the current one highlighted. */
export const navStateClass = (active: boolean) =>
  active
    ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
    : 'text-fg-muted hover:bg-surface-2 hover:text-fg'

/** Attribution for SIcon's unknown-name warning: which menu entry declared the icon. */
export const navItemContext = (item: NavItem) => (item.pluginKey ? `plugin "${item.pluginKey}" menu "${item.id}"` : `menu "${item.id}"`)
