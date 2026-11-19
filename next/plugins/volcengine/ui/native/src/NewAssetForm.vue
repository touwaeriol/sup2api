<script setup lang="ts">
// The asset create dialog (POST /assets). The account is the group's.
import { SField, SForm, SInput } from '@sub2api/ui'
import { useCreateForm } from './createForm'
import { useVolcHost } from './host'

const t = useVolcHost().t
const { form, submit } = useCreateForm('/assets', {
  group_id: { required: true, type: 'integer', min: 1 },
  name: { max: 128 },
  asset_type: { max: 64 },
  url: { required: true, min: 1, max: 2048, pattern: /^(https?:\/\/|tos:\/\/)[^\s]+$/ }
})
defineExpose({ submit })
</script>

<template>
  <SForm :form="form">
    <div class="volcengine-fields">
      <SField prop="group_id" :label="t('newAsset.group')" :hint="t('newAsset.groupHelp')">
        <SInput v-bind="form.bind('group_id')" type="number" :min="1" :step="1" />
      </SField>
      <SField prop="name" :label="t('newAsset.name')">
        <SInput v-bind="form.bind('name')" />
      </SField>
      <SField prop="asset_type" :label="t('newAsset.assetType')" :hint="t('newAsset.assetTypeHelp')">
        <SInput v-bind="form.bind('asset_type')" />
      </SField>
      <SField prop="url" :label="t('newAsset.url')" :hint="t('newAsset.urlHelp')">
        <SInput v-bind="form.bind('url')" />
      </SField>
    </div>
  </SForm>
</template>
