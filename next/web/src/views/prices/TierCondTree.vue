<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SBadge, SDropdown, SInput, SSelect, type MenuAction } from '@sub2api/ui'
import { useObjectKeys } from '@/composables/useObjectKeys'
import { CMP_OPS, COND_VARS, MAX_COND_LEAVES, condChildren, condDepth, condKind, condLeaves, isSet, isTimeVar, num, type CmpOp, type CondLeaf, type CondVar, type TierCond } from './priceExpr'
import { newLeaf, roomFor, setLeafVar, tzOptions, weekdayNames } from './tierCond'

// A tier's condition as a tree (design 2026-11-22 §5): a comparison is one
// row (what it reads, a time's zone, the operator, the value, a weekday by
// name); an "all / any" group lists its conditions and takes more conditions
// and groups; a negation frames what it negates. Every node can be negated
// and, but for the root and a group's only child, removed. Additions stop at
// MAX_COND_DEPTH levels and MAX_COND_LEAVES comparisons. Edits change the
// bound node in place, or replace it (wrapping, negating, unwrapping).
// Disabled, it reads the same and changes nothing. On a phone a row's
// controls share the width and wrap, and a level indents less.
const node = defineModel<TierCond>({ required: true })
const props = withDefaults(
  defineProps<{
    disabled?: boolean
    /** The level of this node, the root's 1. */
    level?: number
    /** Where the node is ("1.2"), for its labels. */
    path?: string
    /** The comparisons of the whole tree; the root counts them. */
    leaves?: number
    removable?: boolean
  }>(),
  { level: 1, path: '1', leaves: undefined }
)
const emit = defineEmits<{ (e: 'remove'): void }>()
const { t } = useI18n()

const kind = computed(() => condKind(node.value))
const leaf = computed(() => node.value as CondLeaf)
const kids = computed(() => condChildren(node.value))
const total = computed(() => props.leaves ?? condLeaves(node.value))
const isFull = computed(() => total.value >= MAX_COND_LEAVES)

const varOptions = computed(() => COND_VARS.map(value => ({ value, label: t(`prices.cond.var.${value}`) })))
const opOptions = CMP_OPS.map(value => ({ value, label: value }))
const kindOptions = computed(() => [
  { value: 'and', label: t('prices.cond.and') },
  { value: 'or', label: t('prices.cond.or') }
])
const weekdayOptions = computed(() => weekdayNames().map((label, value) => ({ value, label })))
const zoneOptions = computed(() => (kind.value === 'leaf' && isTimeVar(leaf.value.var) ? tzOptions(t, leaf.value.tz) : []))

/** A group's kids keep their rows (and the rows' own state) when one before them goes. */
const keyOf = useObjectKeys()

function setKind(k: 'and' | 'or') {
  if (k !== kind.value) node.value = k === 'and' ? { and: kids.value } : { or: kids.value }
}

const isGroup = computed(() => kind.value === 'and' || kind.value === 'or')
/**
 * The additions: a comparison, or a group of the other kind holding one. A
 * comparison grows into an "all" group with its new sibling, 2 levels from
 * here (3 with a group); a group takes a comparison at the level its kids are
 * at already, and a group 3 levels from here.
 */
const additions = computed<MenuAction[]>(() => [
  { key: 'addCond', label: t('prices.cond.addCond'), disabled: isFull.value || (!isGroup.value && !roomFor(props.level, 2)) },
  { key: 'addGroup', label: t('prices.cond.addGroup'), disabled: isFull.value || !roomFor(props.level, 3) }
])
const actions = computed<MenuAction[]>(() => {
  const k = kind.value
  const negation =
    k === 'not'
      ? { key: 'unnegate', label: t('prices.cond.unnegate') }
      : { key: 'negate', label: t('prices.cond.negate'), disabled: !roomFor(props.level, condDepth(node.value) + 1), divider: k === 'leaf' }
  return [...(k === 'leaf' ? additions.value : []), negation, { key: 'remove', label: t('prices.cond.remove'), danger: true, divider: true, hidden: !props.removable }]
})

function add(group: boolean) {
  const added: TierCond = !group ? newLeaf() : kind.value === 'or' ? { and: [newLeaf()] } : { or: [newLeaf()] }
  if (isGroup.value) kids.value.push(added)
  else node.value = { and: [node.value, added] }
}

function onAction(key: string) {
  if (key === 'remove') emit('remove')
  else if (key === 'negate') node.value = { not: node.value }
  else if (key === 'unnegate') node.value = kids.value[0]
  else if (key === 'addCond' || key === 'addGroup') add(key === 'addGroup')
}

const removeKid = (i: number) => kids.value.splice(i, 1)
const setKid = (i: number, v: TierCond) => kids.value.splice(i, 1, v)
const setNegated = (v: TierCond) => (node.value = { not: v })
const ACCENT = { and: 'border-l-primary-500', or: 'border-l-amber-500', not: 'border-l-red-500' } as const
</script>

<template>
  <!-- a comparison: one row -->
  <div v-if="kind === 'leaf'" role="group" :aria-label="t('prices.cond.leafLabel', { path })" class="flex min-w-0 flex-wrap items-center gap-2" data-testid="cond-leaf">
    <SSelect
      :model-value="leaf.var"
      size="sm"
      class="min-w-0 flex-1 basis-28 sm:w-36 sm:flex-none"
      :options="varOptions"
      :disabled="disabled"
      :aria-label="t('prices.cond.varLabel')"
      data-testid="cond-var"
      @update:model-value="setLeafVar(leaf, $event as CondVar)"
    />
    <SSelect
      v-if="isTimeVar(leaf.var)"
      v-model="leaf.tz"
      searchable
      size="sm"
      class="min-w-0 flex-1 basis-40 sm:w-52 sm:flex-none"
      placeholder="UTC"
      :options="zoneOptions"
      :disabled="disabled"
      :aria-label="t('prices.cond.tz')"
      data-testid="cond-tz"
    />
    <SSelect :model-value="leaf.op" size="sm" class="w-20 shrink-0" :options="opOptions" :disabled="disabled" :aria-label="t('prices.cond.op')" data-testid="cond-op" @update:model-value="leaf.op = $event as CmpOp" />
    <SSelect
      v-if="leaf.var === 'weekday'"
      :model-value="isSet(leaf.value) ? num(leaf.value) : null"
      size="sm"
      class="min-w-0 flex-1 basis-20 sm:w-32 sm:flex-none"
      :options="weekdayOptions"
      :placeholder="t('prices.cond.value')"
      :disabled="disabled"
      :aria-label="t('prices.cond.value')"
      data-testid="cond-value"
      @update:model-value="leaf.value = $event as number"
    />
    <SInput
      v-else
      v-model="leaf.value"
      size="sm"
      type="number"
      step="any"
      class="min-w-0 flex-1 basis-20 sm:w-36 sm:flex-none"
      :placeholder="t('prices.cond.value')"
      :disabled="disabled"
      :aria-label="t('prices.cond.value')"
      data-testid="cond-value"
    />
    <SDropdown v-if="!disabled" :actions="actions" width="11rem" :aria-label="t('prices.cond.actions', { path })" class="ml-auto" @select="onAction" />
  </div>

  <!-- a group or a negation: a framed block -->
  <section v-else-if="kind" role="group" :aria-label="t('prices.cond.groupLabel', { path })" class="min-w-0 rounded-lg border border-l-[3px] border-line bg-surface-2/40" :class="ACCENT[kind]" :data-testid="`cond-${kind}`">
    <div class="flex flex-wrap items-center gap-2 px-3 py-2">
      <SBadge v-if="kind === 'not'" tone="danger" size="xs">{{ t('prices.cond.negated') }}</SBadge>
      <SSelect v-else :model-value="kind" size="sm" class="w-24 shrink-0" :options="kindOptions" :disabled="disabled" :aria-label="t('prices.cond.kind')" data-testid="cond-kind" @update:model-value="setKind($event as 'and' | 'or')" />
      <span class="text-xs text-fg-subtle">{{ t('prices.cond.group', { path }) }}</span>
      <div v-if="!disabled" class="ml-auto flex items-center gap-1">
        <SDropdown v-if="isGroup" :actions="additions" :label="t('prices.cond.add')" width="11rem" data-testid="cond-add" @select="onAction" />
        <SDropdown :actions="actions" width="11rem" :aria-label="t('prices.cond.actions', { path })" @select="onAction" />
      </div>
    </div>
    <ol class="space-y-2 pb-2.5 pl-3 pr-2 sm:pl-5 sm:pr-3">
      <li v-if="kind === 'not'">
        <TierCondTree :model-value="kids[0]" :level="level + 1" :path="`${path}.1`" :leaves="total" :disabled="disabled" @update:model-value="setNegated" />
      </li>
      <template v-else>
        <li v-for="(kid, i) in kids" :key="keyOf(kid)">
          <TierCondTree
            :model-value="kid"
            :level="level + 1"
            :path="`${path}.${i + 1}`"
            :leaves="total"
            :disabled="disabled"
            :removable="kids.length > 1"
            @update:model-value="setKid(i, $event)"
            @remove="removeKid(i)"
          />
        </li>
      </template>
    </ol>
  </section>

  <SAlert v-else tone="danger" dense data-testid="cond-unknown">
    {{ t('prices.cond.unknown') }} <span class="break-all font-mono">{{ JSON.stringify(node) }}</span>
  </SAlert>
</template>
