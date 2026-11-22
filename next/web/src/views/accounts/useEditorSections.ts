// The sections of the account editor as tabs of its side navigation, each
// with a one-line summary of what it holds: connection, protocols and models,
// scheduling and limits, advanced (request forwarding), then the sections
// plugins declare for the type (account.editor.section, docs/COMMON-CONTROLS.md
// §4). A section made only of common controls is left out when every one of
// them is hidden.
import { computed, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FormTab } from '@sub2api/ui'
import type { AccountType } from '@/api/types'
import { forAccountType, slotKey } from '@/components/plugin/slots'
import { lt } from '@/i18n'
import { usePluginStore } from '@/stores/plugins'
import { policyLabel } from '@/utils/conversion'
import type { CommonControlName, ControlViews } from './commonControls'
import { pluginSection, SECTION_CONTROLS, type EditorSection } from './editorErrors'
import { entryCount } from './headerOverride/model'
import type { ModelLimitCard } from './modelLimits'
import { operationCount } from './paramOverride/model'
import type { ProtocolEditor } from './protocols/useProtocolEdit'
import type { AccountBasics, ModelEditor } from './useAccountSave'

type T = ReturnType<typeof useI18n>['t']
export type EditorTab = FormTab & { key: EditorSection }

export interface EditorSectionsDeps {
  basic: AccountBasics
  controls: ComputedRef<ControlViews>
  accountType: () => AccountType | null
  editing: Ref<boolean>
  models: ModelEditor
  protocols: ProtocolEditor
  modelLimits: Ref<ModelLimitCard[]>
  /** The usage limit's summary ('' when there is none). */
  usageLimitSummary: Ref<string>
}

/** Summary parts of the shown controls (falsy parts dropped). */
const shownParts = (controls: ControlViews, parts: Array<[CommonControlName, string | number | false]>) => parts.filter(([n, v]) => v && controls[n].visible).map(([, v]) => v)

/** "Protocols 4/4 · 2 models limited · 1 converted · 2 models · 1 mapping". */
function modelsSummary(t: T, d: EditorSectionsDeps): string {
  const { open, total, limited, converting } = d.protocols.summary.value
  const mappings = Object.keys(d.models.mapping.value).length
  const count = d.models.models.value.length
  const protocols = [
    t('accounts.protocols.summary.open', { open, total }),
    ...(limited ? [t('accounts.protocols.summary.limited', { n: limited })] : []),
    converting ? t('accounts.protocols.summary.converting', { n: converting }) : t('accounts.protocols.summary.noConversion')
  ]
  return [
    ...(d.accountType() ? protocols : []),
    count ? t('accounts.editorUi.modelsSummary', { n: count }) : t('accounts.editorUi.allModels'),
    ...shownParts(d.controls.value, [['model_mapping', mappings && t('accounts.editorUi.mappingSummary', { n: mappings })]])
  ].join(' · ')
}

function schedulingSummary(t: T, d: EditorSectionsDeps): string {
  const { basic } = d
  return shownParts(d.controls.value, [
    ['priority', t('accounts.editorUi.summaryPriority', { n: basic.priority })],
    ['weight', t('accounts.editorUi.summaryWeight', { n: basic.weight })],
    ['max_concurrency', t('accounts.editorUi.summaryConcurrency', { n: basic.max_concurrency || '∞' })],
    ['model_limits', d.modelLimits.value.length && t('accounts.modelLimits.summary', { n: d.modelLimits.value.length })],
    ['usage_limit', d.usageLimitSummary.value]
  ]).join(' · ')
}

/** Header passthrough, header override, parameter override, the account's own conversion loss policy and environment values. */
function forwardingSummary(t: T, { basic, controls }: EditorSectionsDeps): string {
  const rules = basic.header_passthrough
  const headers = entryCount(basic.header_override)
  const overrides = operationCount(basic.param_override)
  const custom = Object.keys(basic.environment).length
  const policy = basic.conversion_loss_policy ? [t('accounts.forwarding.conversion', { policy: policyLabel(t, basic.conversion_loss_policy) })] : []
  if (!shownParts(controls.value, [['header_passthrough', rules.length], ['header_override', headers], ['param_override', overrides], ['environment', custom]]).length && !policy.length) return t('accounts.forwarding.allDefault')
  return [
    ...shownParts(controls.value, [
      ['header_passthrough', rules.includes('*') ? t('accounts.forwarding.passAll') : rules.length ? t('accounts.forwarding.passRules', { n: rules.length }) : t('accounts.forwarding.passNone')],
      ['header_override', headers && t('accounts.forwarding.headerOverride', { n: headers })],
      ['param_override', overrides && t('accounts.forwarding.override', { n: overrides })]
    ]),
    ...policy,
    ...shownParts(controls.value, [['environment', custom ? t('accounts.forwarding.envCustom', { n: custom }) : t('accounts.forwarding.envFollow')]])
  ].join(' · ')
}

export function useEditorSections(d: EditorSectionsDeps) {
  const { t } = useI18n()
  const plugins = usePluginStore()
  /** Plugin sections declared for this account type. */
  const pluginSections = computed(() => plugins.slotEntries('account.editor.section').filter((e) => forAccountType(e, d.accountType())))
  const pluginTabs = (): EditorTab[] =>
    pluginSections.value.map((e) => ({
      key: pluginSection(slotKey(e)),
      icon: e.icon || 'puzzle',
      label: lt(e.label) || e.name,
      summary: d.editing.value ? lt(plugins.plugin(e.pluginKey)?.name) || e.pluginKey : t('accounts.editorUi.pluginSectionAfterSave')
    }))
  const shown = (key: EditorSection) => !SECTION_CONTROLS[key]?.length || SECTION_CONTROLS[key]!.some((n) => d.controls.value[n].visible)
  const tabs = computed<EditorTab[]>(() =>
    [
      { key: 'connection' as const, icon: 'key', label: t('accounts.editorUi.connection'), summary: d.basic.name.trim() || t('accounts.editorUi.unnamed') },
      { key: 'models' as const, icon: 'cpu', label: t('accounts.editorUi.models'), summary: modelsSummary(t, d) },
      { key: 'scheduling' as const, icon: 'bolt', label: t('accounts.editorUi.scheduling'), summary: schedulingSummary(t, d) },
      { key: 'advanced' as const, icon: 'code', label: t('accounts.editorUi.advanced'), summary: forwardingSummary(t, d) },
      ...pluginTabs()
    ].filter((s) => shown(s.key))
  )
  return { tabs, pluginSections }
}
