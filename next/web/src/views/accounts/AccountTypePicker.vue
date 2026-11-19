<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SEmpty, SGrid, SHint, SInput, SLink, SIcon, SSpinner } from '@sub2api/ui'
import type { AccountType } from '@/api/types'
import { lt } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useAccountCaps } from '@/composables/useAccountCaps'
import PluginAvatar from '@/views/plugins/parts/PluginAvatar.vue'
import TrustBadge from '@/views/plugins/parts/TrustBadge.vue'
import AccountTypeEndpoints from './AccountTypeEndpoints.vue'
import AccountTypePlatforms from './AccountTypePlatforms.vue'
import { typeKey, useAccountTypes } from './accountTypes'
import { creationChoices, sameCreationGroup } from './accountTypeChoices'

// Step 1 of "new account" (wireframe A.3 / CONTRACTS §18.4): one flat grid of
// compact cards, one per account type. Endpoints are collapsed by default.
const emit = defineEmits<{ (e: 'pick', t: AccountType): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const caps = useAccountCaps()
/**
 * A card stands for its creation group: it opens on the first type of the
 * group the caller can create; with none it is greyed with the permission it needs.
 */
const creatableOf = (at: AccountType) => items.value.find(x => (x === at || sameCreationGroup(at, x)) && caps.creatable(x))
function pick(at: AccountType) {
  const target = creatableOf(at)
  if (target) emit('pick', target)
}
const { grouped, loaded, load } = useAccountTypes()
load(true)

/** Flattened list of account types, keeping the per-plugin server order. */
const items = computed(() => grouped.value.flatMap((g) => g.types))

const search = ref('')
const filteredItems = computed(() => {
  const q = search.value.trim().toLocaleLowerCase()
  return creationChoices(items.value.filter(at => !q || [lt(at.label), lt(at.auth_method_label), lt(at.plugin_name), at.plugin_key, at.type, lt(at.description)].some(v => v?.toLocaleLowerCase().includes(q))))
})

const expanded = ref<Record<string, boolean>>({})
function toggle(at: AccountType) {
  const k = typeKey(at.plugin_key, at.type)
  expanded.value = { ...expanded.value, [k]: !expanded.value[k] }
}
const isOpen = (at: AccountType) => !!expanded.value[typeKey(at.plugin_key, at.type)]
</script>

<template>
  <div>
    <div v-if="!loaded" class="flex justify-center py-10"><SSpinner /></div>
    <SEmpty v-else-if="!items.length" :text="t('accounts.noTypes')" />
    <template v-else>
    <SInput v-model="search" class="mb-4" icon="search" clearable :placeholder="t('accounts.editorUi.searchTypes')" :aria-label="t('accounts.editorUi.searchTypes')" />
    <SEmpty v-if="!filteredItems.length" :text="t('common.noData')" />
    <SGrid v-else :gap="3" :lg-cols="3">
      <div
        v-for="at in filteredItems"
        :key="typeKey(at.plugin_key, at.type)"
        role="button"
        tabindex="0"
        class="card flex flex-col gap-2 p-3 text-left transition"
        :class="creatableOf(at) ? 'cursor-pointer hover:border-primary-300 hover:shadow-card-hover dark:hover:border-primary-700' : 'cursor-not-allowed opacity-60'"
        :aria-disabled="!creatableOf(at)"
        :data-type="typeKey(at.plugin_key, at.type)"
        @click="pick(at)"
        @keydown.enter.self.prevent="pick(at)"
        @keydown.space.self.prevent="pick(at)"
      >
        <div class="flex items-start gap-2">
          <PluginAvatar :name="lt(at.plugin_name) || at.plugin_key" :plugin-key="at.plugin_key" :icon="at.icon" :asset-base="at.asset_base" size="md" />
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium text-fg">{{ lt(at.label) || at.type }}</div>
            <SHint size="xs" class="truncate">
              {{ lt(at.plugin_name) || at.plugin_key }}
              <span class="font-mono">{{ at.plugin_version ? `v${at.plugin_version}` : '' }}</span>
            </SHint>
          </div>
          <TrustBadge v-if="at.trust" :trust="at.trust" />
        </div>

        <AccountTypePlatforms :items="at.platforms" />

        <SHint v-if="at.description" size="xs" class="line-clamp-2">{{ lt(at.description) }}</SHint>
        <SHint v-if="!creatableOf(at)" size="xs" tone="warning" data-testid="type-needs-relay">{{ t('accounts.typeNeedsRelay') }}</SHint>

        <div class="mt-auto border-t border-line pt-2">
          <SLink
            as="button"
            class="text-xs"
            data-testid="type-endpoints-toggle"
            :aria-expanded="isOpen(at)"
            @click.stop="toggle(at)"
          >
            {{ t('accounts.endpointCount', { n: at.endpoints.length }) }}
            <SIcon name="chevron-down" class="inline h-3.5 w-3.5 transition-transform" :class="isOpen(at) ? 'rotate-180' : ''" />
          </SLink>
          <div v-if="isOpen(at)" class="mt-1.5" @click.stop>
            <AccountTypeEndpoints :endpoints="at.endpoints" :platforms="at.platforms.map((p) => p.id)" compact />
          </div>
        </div>
      </div>
    </SGrid>
    </template>
    <SHint class="mt-4">
      {{ t('accounts.noTypeHint') }}
      <SLink v-if="auth.has('plugin:market:read')" to="/market">{{ t('accounts.goMarket') }}</SLink>
    </SHint>
  </div>
</template>
