<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { isApiError } from '@sub2api/host'
import { SButton, SField, SInput, SModal, SSegmented, fromLocalInput, toLocalInput } from '@sub2api/ui'
import { applyErrorsOrToast, clearErrors, idNumber, type FieldErrors } from './forms'
import { enumOptions } from './format'
import { canManageSettings, createWatch, fetchPluginSettings, updateWatch, useModHost } from './host'
import { settingValue } from './settingsForm'
import type { Watch, WatchChange, WatchLevel } from './types'
import { WATCH_LEVELS } from './watchLevels'

// Add a user to the watch list or edit an entry (new-api watch-add-dialog /
// watch-change-dialogs): level, absolute expiry or none, note and a reason
// for the history. The user of a new entry is picked with the host's
// EntityPicker. Only changed fields are sent, and a note alone is logged as a
// note change. An untouched expiry is left to the backend, which on a new
// entry or a level change sets watch_days at observe and none at enforce; the
// form shows that default for the chosen level until the expiry is edited.
// A locked entry is not edited here (the watch list offers no edit for it);
// the user picked for a new entry may be on a user list: the backend then
// answers 409 locked, shown at the user.
const props = defineProps<{ open: boolean; entry: Watch | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved'): void }>()
const host = useModHost()
const t = host.t
const { EntityPicker } = host.components

type Expiry = 'never' | 'at'
const form = reactive({ user: '', level: 'observe' as WatchLevel, expiry: 'at' as Expiry, expiresAt: '', note: '', reason: '' })
/** The form as opened, to tell what changed. */
const initial = { level: 'observe' as WatchLevel, expiry: 'at' as Expiry, expiresAt: '', note: '' }
const errs = reactive<FieldErrors>({})
const saving = ref(false)

/** watch_days of the settings (null when not readable: moderation:read users); the default expiry of a new entry. */
const watchDays = ref<number | null>(null)
async function loadWatchDays() {
  if (!canManageSettings()) return
  const s = await fetchPluginSettings().catch(() => null)
  const days = Number(settingValue(s, 'watch_days'))
  watchDays.value = days > 0 ? days : null
}

const inDays = (days: number) => new Date(Date.now() + days * 86400_000).toISOString()

/** The expiry the backend keeps or sets for `level` when none is sent. */
function defaultExpiry(level: WatchLevel): { expiry: Expiry; expiresAt: string } {
  const w = props.entry
  if (w && level === w.level) return { expiry: w.expires_at ? 'at' : 'never', expiresAt: toLocalInput(w.expires_at) }
  if (level === 'enforce') return { expiry: 'never', expiresAt: '' }
  return { expiry: 'at', expiresAt: watchDays.value ? toLocalInput(inDays(watchDays.value)) : '' }
}
/** Shows the default expiry of the level unless the user edited it (it then stays unsent). */
function followLevel() {
  if (expiryChanged.value) return
  const d = defaultExpiry(form.level)
  Object.assign(initial, d)
  Object.assign(form, d)
}

function fill() {
  const w = props.entry
  const level = w?.level ?? 'observe'
  Object.assign(initial, { level, note: w?.note ?? '' }, defaultExpiry(level))
  Object.assign(form, initial, { user: w ? String(w.user_id) : '', reason: '' })
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    clearErrors(errs)
    fill()
    if (watchDays.value !== null) return
    await loadWatchDays()
    if (props.open) followLevel()
  }
)
watch(() => form.level, followLevel)

const levelOptions = computed(() => enumOptions('level', WATCH_LEVELS))
const expiryOptions = computed(() => [
  { value: 'at', label: t('watchEdit.expiresAt') },
  { value: 'never', label: t('watch.noExpiry') }
])
const expiryChanged = computed(() => form.expiry !== initial.expiry || (form.expiry === 'at' && form.expiresAt !== initial.expiresAt))
/** The hint on an untouched expiry that the backend sets (new entry or new level). */
const expiryHint = computed(() => (expiryChanged.value || (props.entry && form.level === props.entry.level) ? '' : t(`watchEdit.defaultExpiry.${form.level}`)))

function body(): WatchChange {
  const b: WatchChange = {}
  if (!props.entry || form.level !== initial.level) b.level = form.level
  if (form.note.trim() !== initial.note.trim()) b.note = form.note.trim()
  if (expiryChanged.value) b.expires_at = form.expiry === 'never' ? '' : fromLocalInput(form.expiresAt)
  if (form.reason.trim()) b.reason = form.reason.trim()
  return b
}

/** The user picked for a new entry, else a field error. */
function userId(): number | null {
  const id = idNumber(form.user)
  if (id > 0) return id
  errs.user_id = t('blocks.invalidUser')
  return null
}

async function save() {
  clearErrors(errs)
  const b = body()
  if (expiryChanged.value && form.expiry === 'at' && !b.expires_at) {
    errs.expires_at = t('watchEdit.expiryRequired')
    return
  }
  if (props.entry && b.level === undefined && b.note === undefined && b.expires_at === undefined) {
    emit('update:open', false)
    return
  }
  saving.value = true
  try {
    if (props.entry) await updateWatch(props.entry.user_id, b)
    else {
      const uid = userId()
      if (uid === null) return
      await createWatch({ user_id: uid, ...b })
    }
    host.toast(t('watch.saved'), 'success')
    emit('update:open', false)
    emit('saved')
  } catch (e) {
    if (isApiError(e) && e.code === 'locked') errs.user_id = t('lock.refused')
    else applyErrorsOrToast(errs, e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SModal :open="open" :title="entry ? t('watch.editTitle', { id: entry.user_id }) : t('watch.add')" width="md" @update:open="emit('update:open', $event)">
    <!-- A new entry: focus starts on the form, not in the user picker, which would drop its list over the other fields. -->
    <form class="mod-form" v-bind="entry ? {} : { tabindex: -1, 'data-autofocus': '' }" @submit.prevent="save">
      <SField v-if="!entry" :label="t('events.user')" required :error="errs.user_id">
        <EntityPicker v-model:id="form.user" kind="user" />
      </SField>
      <SField :label="t('watch.col.level')" :hint="t('watch.levelHint')" :error="errs.level">
        <SSegmented v-model="form.level" :options="levelOptions" />
      </SField>
      <SField :label="t('watch.col.expires')" :hint="expiryHint" :error="errs.expires_at || errs.days">
        <div class="mod-inline mod-gap">
          <SSegmented v-model="form.expiry" :options="expiryOptions" size="sm" />
          <SInput v-if="form.expiry === 'at'" v-model="form.expiresAt" type="datetime-local" size="sm" />
        </div>
      </SField>
      <SField :label="t('watch.col.note')" :error="errs.note">
        <SInput v-model="form.note" :maxlength="500" />
      </SField>
      <SField :label="t('watchEdit.reason')" :hint="t('watchEdit.reasonHint')" :error="errs.reason">
        <SInput v-model="form.reason" :maxlength="500" />
      </SField>
    </form>
    <template #footer>
      <SButton @click="emit('update:open', false)">{{ host.i18n.t('ui.cancel') }}</SButton>
      <SButton variant="primary" :loading="saving" @click="save">{{ t('save') }}</SButton>
    </template>
  </SModal>
</template>
