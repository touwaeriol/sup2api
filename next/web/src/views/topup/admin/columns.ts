import type { TableColumn } from '@sub2api/ui'
import { i18n } from '@/i18n'

// Table columns of the admin pages, labelled by their messages.

/** A one-line column labelled by the message `topup.<prefix>.<key>`. */
export function columnOf(prefix: string) {
  return (key: string, extra: Partial<TableColumn> = {}): TableColumn => ({
    key,
    label: i18n.global.t(`topup.${prefix}.${key}`),
    nowrap: true,
    ...extra
  })
}

/** The row actions column, last in every table. */
export const actionsColumn = (): TableColumn => ({ key: 'actions', label: i18n.global.t('common.actions'), align: 'right', nowrap: true })
