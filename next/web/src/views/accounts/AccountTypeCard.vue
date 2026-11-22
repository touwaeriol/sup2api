<script setup lang="ts">
// The account type being created or edited, above the account editor's side
// navigation (desktop only): plugin, version and platforms.
import type { AccountType } from '@/api/types'
import { lt } from '@/i18n'
import PluginAvatar from '@/views/plugins/parts/PluginAvatar.vue'
import AccountTypePlatforms from './AccountTypePlatforms.vue'

defineProps<{ accountType: AccountType }>()
</script>

<template>
  <div class="hidden rounded-2xl border border-line bg-surface-2 p-3 md:block" data-testid="editor-type-card">
    <div class="flex items-center gap-2.5">
      <PluginAvatar :name="lt(accountType.plugin_name) || accountType.plugin_key" :plugin-key="accountType.plugin_key" :icon="accountType.icon" :asset-base="accountType.asset_base" size="md" />
      <div class="min-w-0">
        <div class="truncate text-sm font-semibold text-fg">{{ lt(accountType.label) || accountType.type }}</div>
        <div class="truncate text-xs text-fg-muted">
          {{ lt(accountType.plugin_name) || accountType.plugin_key }}<span v-if="accountType.plugin_version" class="font-mono"> · v{{ accountType.plugin_version }}</span>
        </div>
      </div>
    </div>
    <AccountTypePlatforms v-if="accountType.platforms.length" :items="accountType.platforms" class="mt-2.5" />
  </div>
</template>
