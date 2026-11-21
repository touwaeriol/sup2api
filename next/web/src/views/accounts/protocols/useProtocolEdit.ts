// State of the protocol cards of the account editor and of the bulk edit
// (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1): the own protocols the account
// opens (all / chosen), the protocols of chosen models, and the JSON view of
// both. The rules and checks themselves are in protocolBinding.
import { computed, ref, watch } from 'vue'
import { useViewport } from '@sub2api/ui'
import type { Account, AccountType } from '@/api/types'
import {
  bindingIssues, bindingJSON, bindingSummary, conversionInUse, defaultProtocols, modelProtocolRules, parseBinding, protocolColumns,
  rulesByModel, toggleColumn, type BindingProblem, type ModelRules, type ProtocolBinding, type ProtocolColumn
} from './protocolBinding'

export type ProtocolMode = 'all' | 'custom'
export type RulesView = 'visual' | 'json'

/**
 * @param accountType the type whose endpoints give the protocol columns
 * @param models the account's model list: the rows of the matrix
 */
export function useProtocolEdit(accountType: () => AccountType | null | undefined, models: () => readonly string[] = () => []) {
  const viewport = useViewport()
  const columns = computed(() => protocolColumns(accountType()))

  const mode = ref<ProtocolMode>('all')
  /** The own protocols ticked under "chosen"; kept while the mode is "all", null until it was ever chosen. */
  const chosen = ref<string[] | null>(null)
  const rules = ref<ModelRules>({})

  const protocols = computed(() => (mode.value === 'all' ? null : (chosen.value ?? [])))
  const defaults = computed(() => defaultProtocols(columns.value, protocols.value))
  /** Nothing is checked (or saved) for an account whose type is gone: its protocols are unknown. */
  const issues = computed(() => (accountType() ? bindingIssues(protocols.value, rules.value, columns.value) : []))
  const summary = computed(() => bindingSummary(protocols.value, rules.value, columns.value))
  const conversionUsed = computed(() => conversionInUse(protocols.value, rules.value, columns.value))

  /** The two account fields as they are saved. */
  const binding = (): ProtocolBinding => ({ protocols: protocols.value && [...protocols.value], model_protocols: modelProtocolRules(rules.value, columns.value) })

  // ---------------------------------------------------------------- views of the model rules

  /** The matrix needs rows and room: an account serving every model, or a phone, starts on the list. */
  const defaultView = (): RulesView => 'visual'
  const view = ref<RulesView>(defaultView())
  const text = ref('')
  const textProblem = ref<{ problem: BindingProblem; model?: string } | null>(null)

  function apply(b: ProtocolBinding) {
    mode.value = b.protocols === null ? 'all' : 'custom'
    if (b.protocols !== null) chosen.value = [...b.protocols]
    rules.value = rulesByModel(b.model_protocols)
  }

  /** Parses the JSON view back into the state; false (and a problem) when it cannot. */
  function syncText(): boolean {
    const parsed = parseBinding(text.value)
    textProblem.value = 'problem' in parsed ? parsed : null
    if (!('problem' in parsed)) apply(parsed)
    return !textProblem.value
  }

  /** Switches the view; the JSON view is left only once its text parses. */
  function setView(next: RulesView) {
    if (next === view.value || (view.value === 'json' && !syncText())) return
    if (next === 'json') text.value = bindingJSON(binding())
    view.value = next
  }

  // The JSON view holds both fields: what the own-protocols card changes while it is open is written into its text
  // (a text that parsed is rewritten in the saved form). A text that did not parse is kept for the user to correct,
  // and replaces the state once it does.
  watch([protocols, rules], () => view.value === 'json' && !textProblem.value && (text.value = bindingJSON(binding())), { flush: 'sync' })

  /** Commits the JSON view before a save; false when its text is invalid. */
  const commitPending = () => view.value !== 'json' || syncText()

  // ---------------------------------------------------------------- load and edit

  /** Loads an account's fields (or the defaults of a new one). */
  function setSaved(a?: Pick<Account, 'protocols' | 'model_protocols'> | null) {
    chosen.value = null
    apply({ protocols: a?.protocols ?? null, model_protocols: a?.model_protocols ?? [] })
    textProblem.value = null
    view.value = defaultView()
  }

  /** "All" keeps what was ticked; "chosen" starts from every own protocol. */
  function setMode(next: ProtocolMode) {
    if (next === 'custom') chosen.value ??= columns.value.own.flatMap(c => c.ids)
    mode.value = next
  }

  function toggleOwn(c: ProtocolColumn) {
    if (mode.value === 'custom') chosen.value = toggleColumn(chosen.value ?? [], c)
  }

  /** Drops a chosen id that is no own protocol of the type (it has no row to untick). */
  function removeOwn(protocol: string) {
    if (mode.value === 'custom') chosen.value = (chosen.value ?? []).filter(id => id !== protocol)
  }

  return {
    columns, mode, protocols, rules, defaults, issues, summary, conversionUsed, view, text, textProblem,
    binding, setSaved, setMode, toggleOwn, removeOwn, setView, syncText, commitPending
  }
}

export type ProtocolEditor = ReturnType<typeof useProtocolEdit>
