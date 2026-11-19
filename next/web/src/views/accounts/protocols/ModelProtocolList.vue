<script setup lang="ts">
// List view of 模型的协议限定 (DESIGN-2026-11-17-PROTOCOL-BINDING §5.1), laid
// out like ModelLimitsEditor: a card per set of models served on the same
// protocols, plus an optional one for the other models. A card keeps its
// identity while it is edited: two cards given the same protocols stay two
// cards, and only what is saved joins them into one entry. A model is in one
// card only: adding it to a card takes it out of the one it was in.
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SField, SHint, toast } from '@sub2api/ui'
import ModelTagSelect from '@/components/models/ModelTagSelect.vue'
import { cardRules, modelProtocolRules, REST, ruleCards, sameModelProtocols, toggleColumn, withCardModels, withNewCard, type ProtocolColumn, type RuleCard } from './protocolBinding'
import ProtocolChips from './ProtocolChips.vue'
import type { ProtocolEditor } from './useProtocolEdit'

const props = defineProps<{
  editor: ProtocolEditor
  /** Models offered by the model inputs. */
  options: string[]
  /** Messages of the server's errors by saved entry. */
  errors?: Record<number, string[]>
  disabled?: boolean
}>()
const { t } = useI18n()
const { columns, rules, defaults } = props.editor

const cards = ref<RuleCard[]>(ruleCards(rules.value, columns.value))
/** The cards become the rules; a card without a model goes. */
function commit(next: RuleCard[]) {
  cards.value = next.filter(c => c.models.length)
  rules.value = cardRules(cards.value)
}
// Rules set elsewhere (another account loaded) start the cards over, numbered past the ones they replace.
watch(rules, r => {
  if (!sameModelProtocols(cards.value, modelProtocolRules(r, columns.value))) cards.value = ruleCards(r, columns.value, Math.max(0, ...cards.value.map(c => c.id)) + 1)
})

const isRest = (c: RuleCard) => c.models[0] === REST
const invalidModel = (model: string) => toast(t('accounts.modelInvalid', { model }), 'warning')
/** The server's errors of the saved entry holding the card's models. */
const errorsOf = (c: RuleCard) => props.errors?.[modelProtocolRules(rules.value, columns.value).findIndex(e => e.models.includes(c.models[0]))] ?? []

const setModels = (c: RuleCard, models: string[]) => commit(withCardModels(cards.value, c.id, models))
const setProtocols = (c: RuleCard, protocols: string[]) => commit(cards.value.map(x => (x.id === c.id ? { ...x, protocols } : x)))
const toggle = (c: RuleCard, column: ProtocolColumn) => setProtocols(c, toggleColumn(c.protocols, column))
const remove = (c: RuleCard) => commit(cards.value.filter(x => x.id !== c.id))
/** A new card starts from the default protocols. */
const add = (models: string[]) => commit(withNewCard(cards.value, models, defaults.value))
</script>

<template>
  <div class="space-y-3" data-testid="protocol-list">
    <SHint v-if="!cards.length" size="xs" data-testid="protocol-list-empty">{{ t('accounts.protocols.rules.empty') }}</SHint>
    <div v-for="c in cards" :key="c.id" class="space-y-2 rounded-lg border border-line p-3" :data-rest="isRest(c) || undefined" data-testid="protocol-entry">
      <SField :label="t('accounts.protocols.rules.models')">
        <p v-if="isRest(c)" class="text-sm font-medium text-fg">{{ t('accounts.protocols.rules.rest') }}<SHint inline size="xs" class="ml-2">{{ t('accounts.protocols.rules.restHint') }}</SHint></p>
        <ModelTagSelect v-else :model-value="c.models" :options="options" :placeholder="t('accounts.modelLimits.placeholder')" :readonly="disabled" @update:model-value="setModels(c, $event)" @invalid="invalidModel" />
      </SField>
      <SField :label="t('accounts.protocols.rules.protocols')">
        <ProtocolChips :columns="columns" :ids="c.protocols" :disabled="disabled" @toggle="toggle(c, $event)" @remove="setProtocols(c, c.protocols.filter(id => id !== $event))" />
      </SField>
      <SHint v-for="msg in errorsOf(c)" :key="msg" tone="danger" size="xs">{{ msg }}</SHint>
      <SButton size="sm" variant="ghost" tone="danger" :disabled="disabled" data-testid="protocol-entry-remove" @click="remove(c)">{{ t('accounts.protocols.rules.removeEntry') }}</SButton>
    </div>
    <div class="space-y-2 rounded-lg border border-dashed border-line p-3" data-testid="protocol-entry-add">
      <SField :label="t('accounts.protocols.rules.add')">
        <ModelTagSelect :model-value="[]" :options="options" :placeholder="t('accounts.protocols.rules.addPlaceholder')" :readonly="disabled" @update:model-value="add" @invalid="invalidModel" />
      </SField>
      <SButton v-if="!(REST in rules)" size="sm" :disabled="disabled" data-testid="protocol-entry-add-rest" @click="add([REST])">{{ t('accounts.protocols.rules.addRest') }}</SButton>
    </div>
  </div>
</template>
