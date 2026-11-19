<script setup lang="ts">
// Native account form of the key file types (manifest accountTypes[].form
// {mode: "native", component: "VertexKeyFileForm"}, CONTRACTS §23.5):
// vertex_sa takes the service account key (service_account_json),
// vertex_authorized_user the user credentials file (authorized_user_json),
// then the project and the location (global for a new account).
//
// access_token, expires_at and token_for are the core's: it writes them when
// it renews the token. The form never shows or writes them, so they go back
// as the editor loaded them (the token masked) and a save does not drop the
// token; ValidateCredentials drops it when the key file changed.
import { computed } from 'vue'
import { nativeAccountFormProps, SCredentialForm, SField, SInput, SSecretInput, useCredentialForm } from '@sub2api/ui'
import { useVertexHost } from './host'
import { KEY_FIELD, KEY_FILE_DEFAULTS, keyFileRules, keyFileType } from './fields'

const props = defineProps(nativeAccountFormProps)
const emit = defineEmits<{ (e: 'update:modelValue', value: Record<string, unknown>): void }>()
const t = useVertexHost().t

const type = computed(() => keyFileType(props.accountType, props.account))
const field = computed(() => KEY_FIELD[type.value])
const form = useCredentialForm(props, emit, { rules: () => keyFileRules(type.value), defaults: KEY_FILE_DEFAULTS })
defineExpose({ validate: form.validate })
</script>

<template>
  <SCredentialForm :form="form">
    <SField :prop="field" :label="t(`keyFile.${type}.label`)" :hint="t(`keyFile.${type}.help`)">
      <SSecretInput v-bind="form.bind(field)" :placeholder="t(`keyFile.${type}.placeholder`)" />
    </SField>
    <SField prop="project_id" :label="t('project.label')" :hint="t(`project.help.${type}`)">
      <SInput v-bind="form.bind('project_id')" :placeholder="type === 'vertex_sa' ? t('project.fromKeyFile') : 'my-project-123456'" mono />
    </SField>
    <SField prop="location" :label="t('location.label')" :hint="t('location.help')">
      <SInput v-bind="form.bind('location')" placeholder="global" mono />
    </SField>
  </SCredentialForm>
</template>
