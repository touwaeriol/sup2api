// @sub2api/ui — component library shared by the console and native plugins.
// Styling relies on the console's global component classes (.btn, .input,
// .card, .table, .badge, ...), so plugin UIs look native without shipping CSS.

export { default as SAlert } from './SAlert.vue'
export { default as SAutoRefresh } from './SAutoRefresh.vue'
export { useAutoRefresh } from './autoRefresh'
export type { AutoRefresh, AutoRefreshOptions } from './autoRefresh'
export { useNow } from './now'
export { default as SBadge } from './SBadge.vue'
export { default as SBulkBar } from './SBulkBar.vue'
export { default as SButton } from './SButton.vue'
export { default as SCard } from './SCard.vue'
export { default as SChart } from './SChart.vue'
export { default as SChip } from './SChip.vue'
export { default as SChipList } from './SChipList.vue'
export { default as SChoiceCards } from './SChoiceCards.vue'
export { default as SConfirmHost } from './SConfirmHost.vue'
export { default as SCopyButton } from './SCopyButton.vue'
export { default as SDateTime } from './SDateTime.vue'
export { default as SDateTimeInput } from './SDateTimeInput.vue'
export { default as SDescriptions } from './SDescriptions.vue'
export { default as SDescItem } from './SDescItem.vue'
export { default as SDisclosure } from './SDisclosure.vue'
export { default as SDrawer } from './SDrawer.vue'
export { default as SDropdown } from './SDropdown.vue'
export { default as SEmpty } from './SEmpty.vue'
export { default as SField } from './SField.vue'
export { default as SFilterBar } from './SFilterBar.vue'
export { default as SFilterChips } from './SFilterChips.vue'
export { default as SIcon } from './SIcon.vue'
// Icon vocabulary: the authoritative list of names <SIcon> (and therefore
// manifest ui.menus[].icon) accepts.
export { ICON_NAMES, ICON_PATHS, hasIcon, iconPaths } from './icons'
export { default as SInput } from './SInput.vue'
export { default as SIPAddress } from './SIPAddress.vue'
export { default as SKeyValue } from './SKeyValue.vue'
export { lazyPage } from './lazyPage'
export { useViewport, provideViewport } from './viewport'
export type { Viewport } from './viewport'
export { default as SModal } from './SModal.vue'
export { dismissTopLayer } from './overlay'
export { default as SFormModal } from './SFormModal.vue'
export { default as SPageHeader } from './SPageHeader.vue'
export { provideTitleContext, type PageHead, type PageHeadSink } from './titleContext'
export { default as SPagination } from './SPagination.vue'
export { default as SProgress } from './SProgress.vue'
export { default as SSegmented } from './SSegmented.vue'
export { default as SSelect } from './SSelect.vue'
export { default as SOptionMatrix } from './SOptionMatrix.vue'
export { default as SSettingRow } from './SSettingRow.vue'
export { default as SSettingsGroup } from './SSettingsGroup.vue'
export { default as SSkeleton } from './SSkeleton.vue'
export { default as SSpinner } from './SSpinner.vue'
export { default as SStatCard } from './SStatCard.vue'
export { default as SStatusDot } from './SStatusDot.vue'
export { default as SStatusBadge } from './SStatusBadge.vue'
export { statusTone } from './status'
export { default as SSteps } from './SSteps.vue'
export { default as SRowAction } from './SRowAction.vue'
export { default as SSwitch } from './SSwitch.vue'
export { default as STable } from './STable.vue'
export { default as STableColumnSettings } from './STableColumnSettings.vue'
export { useTableColumns, TABLE_COLUMN_SCOPE_KEY } from './tableColumns'
export type { TableColumnState, TableColumnStorage, TableColumnPreferences } from './tableColumns'
export { default as STabs } from './STabs.vue'
export { default as STagInput } from './STagInput.vue'
// A panel whose title is shown by its own navigation (a tab, a side menu):
// headings in it that repeat the title are hidden, as inside STabs.
export { default as STitleScope } from './STitleScope.vue'
export { default as STextarea } from './STextarea.vue'
export { default as STimeRange } from './STimeRange.vue'
export { default as SDateRange } from './SDateRange.vue'
export { default as STooltip } from './STooltip.vue'
export { default as SGrid } from './SGrid.vue'
export { default as SStack } from './SStack.vue'
export { default as SCheckbox } from './SCheckbox.vue'
export { default as SRadio } from './SRadio.vue'
export { default as SRadioGroup } from './SRadioGroup.vue'
export { default as SLink } from './SLink.vue'
export { default as SCode } from './SCode.vue'
export { default as SSectionTitle } from './SSectionTitle.vue'
export { default as SHint } from './SHint.vue'
export { default as SToastHost } from './SToastHost.vue'
export { toast, confirm, toastState, confirmState, dismissToast, settleConfirm, pauseToast, resumeToast } from './feedback'
export { copyToClipboard } from './clipboard'
export { createItemId } from './itemId'
export { default as uiMessages } from './messages'
export { rangeBounds, closedRangeBounds, logRangeBounds, rangeSpanMs, toLocalInput, toLocalSecondsInput, fromLocalInput, dayKey, RANGE_LABELS } from './timeRange'
export type { RangeKey } from './timeRange'
export type {
  TableColumn,
  TableReorderEvent,
  TabItem,
  SelectOption,
  MenuAction,
  Tone,
  StatusTone,
  ModalMobile,
  ChoiceOption,
  ChipListItem,
  FilterChipItem,
  DescItem,
  StepItem,
  StepState,
  OptionMatrixRow,
  OptionMatrixGroup
} from './types'
// Form-item context (custom controls that want SField / SSettingRow ids).
export { FIELD_KEY, useFieldControl } from './field'
export type { FieldContext, ControlSize } from './field'
// Form validation (rules in the async-validator style, useForm over a
// v-model, SForm; SField / SSettingRow take `prop`).
export { default as SForm } from './form/SForm.vue'
export { useForm } from './form/useForm'
export type { FormApi, UseFormOptions } from './form/useForm'
export { SECRET_MASK } from './form/rules'
export type { Rule, Rules, RuleContext, RuleMessage, RuleType, Trigger } from './form/rules'
export { getPath, setPath } from './form/path'
// Form building blocks over useForm (native account forms, plugin settings, list pages).
export { default as SSecretInput } from './SSecretInput.vue'
export { default as SUrlPresetInput } from './SUrlPresetInput.vue'
export { default as SCredentialForm } from './form/SCredentialForm.vue'
export { default as SFormSection } from './form/SFormSection.vue'
export { useCredentialForm, nativeAccountFormProps } from './form/credential'
export type { NativeAccountFormProps } from './form/credential'
export { createApiKeyForm } from './form/apiKeyForm'
export type { ApiKeyFormSpec, ExtraField, FieldSection } from './form/apiKeyForm'
export { usePluginSettings } from './form/pluginSettings'
export type { PluginSettingsForm } from './form/pluginSettings'
export { default as SSettingsPage } from './form/SSettingsPage.vue'
export { default as SResourcePage } from './SResourcePage.vue'
// LocalizedText ({en, zh} or a bare string) of the core's payloads.
export { localizedText } from './i18n'
export { useScrollClip } from './scrollClip'
