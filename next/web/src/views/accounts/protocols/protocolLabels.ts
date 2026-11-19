// Display names of protocols and models in the protocol cards and marks:
// a protocol the console has a name for shows it ("Chat Completions"), any
// other the part of its id after the platform.
import { useI18n } from 'vue-i18n'
import { useOptionalT } from '@/composables/optionalText'
import type { LText } from '@/api/types'
import { usePlatforms } from '@/composables/platforms'
import { REST, type ProtocolColumn } from './protocolBinding'

export function useProtocolLabels() {
  const { t } = useI18n()
  const tr = useOptionalT()
  const platforms = usePlatforms()
  platforms.load()

  /** Name of a protocol id. */
  const protocolName = (id: string) => tr(`accounts.protocols.names.${id}`)
  /** Name of a column; a conversion is named with its platform ("Anthropic Messages"). */
  const columnName = (c: ProtocolColumn) => (c.via === 'converted' ? `${platforms.label(c.platform)} ${protocolName(c.key)}` : protocolName(c.key))
  /** The protocol a request on the column is sent upstream as. */
  const targetName = (c: ProtocolColumn) => (c.hops?.length ? protocolName(c.hops[c.hops.length - 1]) : '')
  /** A model id, or "other models" for the catch-all entry. */
  const modelName = (m: string) => (m === REST ? t('accounts.protocols.rules.rest') : m)
  /** Platforms named together ("Anthropic / OpenAI"); `label` names one the catalog lacks. */
  const platformsName = (list: ReadonlyArray<{ id: string; label?: LText | null }>) => list.map(p => platforms.label(p.id, p.label)).join(' / ')

  return { protocolName, columnName, targetName, modelName, platformLabel: platforms.label, platformsName }
}
