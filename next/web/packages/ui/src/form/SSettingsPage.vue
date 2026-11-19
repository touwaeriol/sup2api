<script setup lang="ts">
// A plugin's settings page over usePluginSettings: optional title, the
// errors no row claims, the default slot (SSettingsGroup blocks of
// SSettingRow with `prop`), and for callers who may edit a bar pinned to
// the bottom: the unsaved-changes note, Reset and Save (as the console's
// settings tab). Leaving with unsaved changes asks first (route leave and
// closing the tab). Without edit rights every control is disabled.
import { onBeforeUnmount, onMounted } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import SAlert from '../SAlert.vue'
import SButton from '../SButton.vue'
import SHint from '../SHint.vue'
import SPageHeader from '../SPageHeader.vue'
import SSpinner from '../SSpinner.vue'
import { confirm } from '../feedback'
import { useUiT } from '../i18n'
import SForm from './SForm.vue'
import type { PluginSettingsForm } from './pluginSettings'

const props = defineProps<{ settings: PluginSettingsForm<any>; title?: string; description?: string }>()
const t = useUiT()

onBeforeRouteLeave(
  () =>
    !props.settings.dirty.value ||
    confirm({ title: t('ui.form.leave.title'), message: t('ui.form.leave.message'), cancelText: t('ui.form.leave.stay'), confirmText: t('ui.form.leave.discard'), danger: true })
)
function beforeUnload(e: BeforeUnloadEvent) {
  if (props.settings.dirty.value) e.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>

<template>
  <div class="s-settings-page" data-testid="plugin-settings">
    <SPageHeader v-if="title" :title="title" :description="description" />
    <SSpinner v-if="settings.loading.value && !settings.loadError.value" block />
    <SAlert v-else-if="settings.loadError.value" tone="danger" :title="t('ui.loadFailed')">
      {{ (settings.loadError.value as Error)?.message }}
      <template #actions><SButton size="sm" @click="settings.reload()">{{ t('ui.retry') }}</SButton></template>
    </SAlert>
    <SForm v-else :form="settings" :disabled="!settings.canEdit.value || settings.saving.value" @submit="settings.dirty.value && settings.save({ validated: true })">
      <div class="s-settings-page-body">
        <SAlert v-if="settings.unclaimed.value.length" tone="danger" dense data-testid="plugin-settings-errors">
          <p v-for="(msg, i) in settings.unclaimed.value" :key="i">{{ msg }}</p>
        </SAlert>
        <slot />
      </div>
    </SForm>
    <div v-if="settings.canEdit.value && !settings.loading.value && !settings.loadError.value" class="s-settings-page-bar" data-testid="settings-save-bar">
      <SHint v-if="settings.dirty.value" size="xs" tone="warning" data-testid="plugin-settings-dirty">{{ t('ui.form.unsaved') }}</SHint>
      <div class="s-settings-page-actions">
        <SButton v-if="settings.dirty.value" size="sm" :disabled="settings.saving.value" data-testid="settings-reset" @click="settings.reset()">{{ t('ui.form.reset') }}</SButton>
        <SButton size="sm" variant="primary" :loading="settings.saving.value" :disabled="!settings.dirty.value" data-testid="settings-save" @click="settings.save()">
          {{ t('ui.save') }}
        </SButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.s-settings-page {
  display: flex;
  max-width: 56rem;
  flex-direction: column;
  gap: 1rem;
}
.s-settings-page-body {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.s-settings-page-bar {
  position: sticky;
  bottom: 0;
  z-index: 10;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.75rem;
  border: 1px solid rgb(var(--line));
  border-radius: 0.75rem;
  background: rgb(var(--surface) / 0.95);
  color: rgb(var(--fg));
}
.s-settings-page-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-left: auto;
}
</style>
