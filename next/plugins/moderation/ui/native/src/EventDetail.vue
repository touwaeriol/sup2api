<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { SAlert, SBadge, SButton, SCode, SCopyButton, SDescriptions, SHint, SModal, SSectionTitle, SSpinner, type DescItem } from '@sub2api/ui'
import ReplayBadge from './ReplayBadge.vue'
import ResultBadge from './ResultBadge.vue'
import CategoryChips from './CategoryChips.vue'
import TraceView from './TraceView.vue'
import VerdictBadge from './VerdictBadge.vue'
import FlowRef from './FlowRef.vue'
import FlowSnapshotCard from './FlowSnapshotCard.vue'
import InputMessage from './InputMessage.vue'
import UserRef from './UserRef.vue'
import UserStateSection from './UserStateSection.vue'
import { useCatalog } from './catalog'
import { dt, enumLabel, idText, isSuspicious, ms, num } from './format'
import { canManage, deleteEvent, fetchEvent, reviewDoneText, reviewEvents, toastError, useModHost } from './host'
import { modeTone, severityTone } from './tones'
import type { EventFilter, ModEvent, ModEventDetail, ReviewStatus, Watch } from './types'
import { useAsync } from './useAsync'

// Record detail (GET /events/:id, new-api record-detail-drawer): result, the
// user's state with inline watch actions, per-stage trace, the
// flow version it ran with, other records of the same request, text and the
// structured input message; manage users review a suspicious record (hit,
// block) or delete any. userLink=false (the user drawer, already showing
// the user) leaves the user as plain text. filterable (the records tab)
// offers to filter the list by the record's endpoint or session, and by the
// request a replayed hit took its verdict from (filter).
const props = withDefaults(defineProps<{ open: boolean; eventId: number | null; userLink?: boolean; filterable?: boolean }>(), { userLink: true })
const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'deleted', id: number): void
  (e: 'reviewed', ids: number[], status: ReviewStatus): void
  (e: 'show-user', userId: number): void
  (e: 'open-user', userId: number): void
  (e: 'filter', q: EventFilter): void
}>()
const host = useModHost()
const t = host.t
const catalog = useCatalog()

const busy = ref(false)
const manage = computed(() => canManage())

// The shown record: the prop, or a related record opened from it.
const currentId = ref<number | null>(props.eventId)
watch(
  () => [props.eventId, props.open] as const,
  ([id]) => (currentId.value = id)
)

const { data: ev, loading, error, reload } = useAsync<ModEventDetail>(() => fetchEvent(currentId.value!), () => t('detail.loadFailed'))
watch(
  () => [props.open, currentId.value] as const,
  ([open, id]) => {
    if (!open || id === null || ev.value?.id === id) return
    ev.value = null
    reload()
  },
  { immediate: true }
)

function close() {
  emit('update:open', false)
}

async function review(status: ReviewStatus) {
  const e = ev.value
  if (!e) return
  busy.value = true
  try {
    const r = await reviewEvents([e.id], status)
    e.review_status = status
    emit('reviewed', [e.id], status)
    host.toast(reviewDoneText(r, 1), 'success')
  } catch (err) {
    toastError(err)
  } finally {
    busy.value = false
  }
}

async function remove() {
  const e = ev.value
  if (!e) return
  const ok = await host.confirm({ message: t('detail.deleteConfirm'), danger: true, confirmText: t('detail.delete') })
  if (!ok) return
  busy.value = true
  try {
    await deleteEvent(e.id)
    host.toast(t('detail.deleted'), 'success')
    emit('deleted', e.id)
    ev.value = null
    close()
  } catch (err) {
    toastError(err)
  } finally {
    busy.value = false
  }
}

function onWatchChanged(w: Watch | null) {
  if (ev.value?.user_state) ev.value.user_state.watch = w
}
function showUser(id: number) {
  close()
  emit('show-user', id)
}
function openUser(id: number) {
  close()
  emit('open-user', id)
}
function filterBy(q: EventFilter) {
  close()
  emit('filter', q)
}

/** Why the bound flow no longer applies; an unknown reason shows as sent. */
const fallbackReason = (e: ModEvent) => (e.flow_fallback ? enumLabel('detail.match.reason', e.flow_fallback.reason) : '')

/** How the flow was found; records written before it was kept show nothing. */
function matchText(e: ModEvent): string {
  if (!e.flow_source) return ''
  return e.flow_source === 'fallback' ? t('detail.match.fallback', { reason: fallbackReason(e) }) : enumLabel('detail.match', e.flow_source)
}

/** Checked first or in the background, and what decided it (the record's mode alone said neither). */
function handlingText(e: ModEvent): string {
  if (!e.level_source || !e.level) return ''
  return enumLabel('detail.level', e.level_source === 'watch' || e.level_source === 'default' ? `${e.level_source}_${e.level}` : e.level_source)
}

const fields = computed<DescItem[]>(() => {
  const e = ev.value
  if (!e) return []
  const items: DescItem[] = [
    { key: 'id', label: t('detail.f.id'), value: String(e.id) },
    { key: 'request', label: t('detail.f.request'), value: e.request_id || '', mono: true, copyable: true },
    { key: 'categories', label: t('detail.f.categories'), full: true },
    { key: 'user', label: t('detail.f.user') },
    { key: 'key', label: t('detail.f.key'), value: idText(e.api_key_id) },
    { key: 'group', label: t('detail.f.group'), value: catalog.groupName(e.group_id) },
    { key: 'flow', label: t('detail.f.flow') },
    { key: 'model', label: t('detail.f.model'), value: e.model || '' },
    { key: 'protocol', label: t('detail.f.protocol'), value: e.protocol || '' },
    { key: 'endpoint', label: t('detail.f.endpoint'), value: e.endpoint || '', mono: true },
    { key: 'latency', label: t('detail.f.latency'), value: ms(e.latency_ms) + (e.queue_ms ? ` (${t('detail.queued', { n: num(e.queue_ms) })})` : '') },
    { key: 'counted', label: t('detail.f.counted'), value: e.counted ? t('yes') : t('no') },
    { key: 'keep', label: t('detail.f.keepUntil'), value: e.keep_until ? dt(e.keep_until) : '' }
  ]
  // Written from the prototype record fields on; older records keep the mode badge instead.
  for (const [key, value] of [['match', matchText(e)], ['handling', handlingText(e)], ['scope', e.scope ? enumLabel('flows.scope', e.scope) : '']] as const) {
    if (value) items.push({ key, label: t(`detail.f.${key}`), value })
  }
  if (e.reviewed_at) items.push({ key: 'reviewed', label: t('detail.f.reviewed'), value: `${dt(e.reviewed_at)} ${idText(e.reviewed_by)}` })
  if (e.session_fp) items.push({ key: 'session', label: t('detail.f.session'), value: e.session_fp, mono: true, copyable: true, full: true })
  if (e.llm_model) items.push({ key: 'llm', label: t('detail.f.llmModel'), value: e.llm_model })
  return items
})
</script>

<template>
  <SModal :open="open" :title="t('detail.title')" width="xl" @update:open="emit('update:open', $event)">
    <SSpinner v-if="loading" block />
    <SAlert v-else-if="error" tone="danger">{{ error }}</SAlert>
    <div v-else-if="ev" class="mod-detail">
      <div class="mod-detail-head">
        <ResultBadge :result="ev.result" />
        <ReplayBadge v-if="ev.replayed" />
        <VerdictBadge :event="ev" />
        <SBadge v-if="ev.timing">{{ enumLabel('timing', ev.timing) }}</SBadge>
        <SBadge v-if="ev.mode && !ev.level_source" :tone="modeTone(ev.mode)">{{ enumLabel('mode', ev.mode) }}</SBadge>
        <SBadge v-if="ev.severity" :tone="severityTone(ev.severity)">{{ t('detail.f.severity') }}: {{ enumLabel('severity', ev.severity) }}</SBadge>
        <SHint inline size="xs" class="mod-detail-time">{{ dt(ev.created_at) }}</SHint>
      </div>

      <SAlert v-if="ev.flow_fallback" tone="warning" data-testid="mod-match-fallback">
        {{ t('detail.matchFallbackHint', { id: ev.flow_fallback.flow_id, reason: fallbackReason(ev) }) }}
      </SAlert>
      <SAlert v-if="ev.reason" tone="neutral" :icon="false" :title="t('detail.f.reason')">{{ ev.reason }}</SAlert>
      <SAlert v-if="ev.error" tone="danger" :title="t('detail.f.error')"><span class="mod-prewrap">{{ ev.error }}</span></SAlert>

      <UserStateSection v-if="ev.user_id && ev.user_state" :user-id="ev.user_id" :state="ev.user_state" :openable="userLink" @watch-changed="onWatchChanged" @show-user="showUser" @open-user="openUser" />

      <div v-if="ev.trace">
        <SSectionTitle tag="p">{{ t('detail.trace') }}</SSectionTitle>
        <TraceView :trace="ev.trace" :request-link="filterable" @show-request="filterBy({ request_id: $event })" />
      </div>

      <FlowSnapshotCard v-if="ev.flow_snapshot" :snapshot="ev.flow_snapshot" :name="ev.flow_name" :current="ev.flow_current_version" />

      <div v-if="ev.related?.length">
        <SSectionTitle tag="p">{{ t('detail.related') }}</SSectionTitle>
        <ul class="mod-related">
          <li v-for="r in ev.related" :key="r.id">
            <button type="button" class="mod-related-item" @click="currentId = r.id">
              <ResultBadge :result="r.result" />
              <SBadge v-if="r.timing" size="xs">{{ enumLabel('timing', r.timing) }}</SBadge>
              <FlowRef :flow-id="r.flow_id" :version="r.flow_version" compact />
              <SHint inline size="xs" class="mod-ml-auto">{{ dt(r.created_at) }} · #{{ r.id }}</SHint>
            </button>
          </li>
        </ul>
      </div>

      <div>
        <SSectionTitle tag="p">
          {{ t('detail.text') }}
          <SHint v-if="ev.text_chars" inline size="xs" class="mod-chars">{{ t('detail.chars', { n: num(ev.text_chars) }) }}</SHint>
          <SHint v-if="ev.image_count" inline size="xs" class="mod-chars">{{ t('events.images', { n: ev.image_count }) }}</SHint>
          <template #actions>
            <SCopyButton v-if="ev.text" :text="ev.text" />
          </template>
        </SSectionTitle>
        <SCode v-if="ev.text" :text="ev.text" class="mod-text-block" />
        <SHint v-else size="xs">{{ t('detail.textNotStored') }}</SHint>
      </div>

      <InputMessage v-if="ev.input_message_size" :event-id="ev.id" :size="ev.input_message_size" :input-text="ev.input_text" />

      <SDescriptions :items="fields" :columns="2" dense>
        <template #item-categories><CategoryChips :categories="ev.hit_categories" wrap /></template>
        <template #item-user><UserRef :user-id="ev.user_id" :link="userLink" :title="userLink ? t('events.openUser') : undefined" @click="openUser" /></template>
        <template #item-flow><FlowRef :flow-id="ev.flow_id" :version="ev.flow_version" :name="ev.flow_name" :current="ev.flow_current_version" /></template>
        <template #item-endpoint>
          {{ ev.endpoint || '—' }}
          <SButton v-if="filterable && ev.endpoint" size="xs" variant="ghost" icon="filter" @click="filterBy({ endpoint: ev.endpoint })">{{ t('detail.endpointRecords') }}</SButton>
        </template>
        <template #item-session>
          {{ ev.session_fp }}
          <SButton v-if="filterable" size="xs" variant="ghost" icon="filter" @click="filterBy({ session_fp: ev.session_fp })">{{ t('detail.sessionRecords') }}</SButton>
        </template>
      </SDescriptions>
    </div>
    <template #footer>
      <template v-if="manage && ev">
        <SButton variant="danger" icon="trash" class="mod-mr-auto" :loading="busy" @click="remove">{{ t('detail.delete') }}</SButton>
        <template v-if="isSuspicious(ev)">
          <SButton v-if="ev.review_status !== 'false_positive'" :loading="busy" @click="review('false_positive')">{{ t('review.falsePositive') }}</SButton>
          <SButton v-if="ev.review_status !== 'confirmed'" variant="danger" :loading="busy" @click="review('confirmed')">{{ t('review.confirmed') }}</SButton>
          <SButton v-if="ev.review_status !== 'pending'" variant="ghost" :loading="busy" @click="review('pending')">{{ t('review.reset') }}</SButton>
        </template>
      </template>
      <SButton @click="close">{{ host.i18n.t('ui.close') }}</SButton>
    </template>
  </SModal>
</template>
