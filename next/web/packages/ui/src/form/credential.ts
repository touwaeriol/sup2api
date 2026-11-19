// Native account forms (CONTRACTS §23.5): useForm wired to the props the
// account editor passes.
import { computed, type ComputedRef, type PropType } from 'vue'
import type { AccountTypeView, ProxyTicketSource } from '@sub2api/host'
import { useForm, type FormApi } from './useForm'
import type { Rules } from './rules'

type Model = Record<string, unknown>

/** The props the account editor passes to a native account form (CONTRACTS §23.5, unchanged). */
export interface NativeAccountFormProps {
  modelValue: Record<string, unknown>
  mode: 'create' | 'edit'
  account?: { id: number; type: string; credentials: Record<string, unknown>; [key: string]: unknown } | null
  /** Includes guarded_settings, custom_upstream, sensitive_fields. */
  accountType?: AccountTypeView | null
  errors?: Record<string, string>
  proxyTicket?: ProxyTicketSource
}

/**
 * The §23.5 props as a runtime declaration: `defineProps(nativeAccountFormProps)`.
 * A plugin's SFC compiler cannot resolve `defineProps<NativeAccountFormProps>()`
 * (a type imported from another package), this object it takes as it is.
 */
export const nativeAccountFormProps = {
  modelValue: { type: Object as PropType<Record<string, unknown>>, default: () => ({}) },
  mode: { type: String as PropType<'create' | 'edit'>, default: 'create' },
  account: { type: Object as PropType<NativeAccountFormProps['account']>, default: null },
  accountType: { type: Object as PropType<AccountTypeView | null>, default: null },
  errors: { type: Object as PropType<Record<string, string>>, default: undefined },
  proxyTicket: { type: Function as PropType<ProxyTicketSource>, default: undefined }
} as const

/**
 * useForm over the credentials v-model: `props.errors` are the server's
 * field errors, `defaults` apply while creating only. The component writes
 * defineExpose({ validate: form.validate }). `proxyTicket` stays on the props
 * for forms running an authorization flow.
 */
export function useCredentialForm<M extends Model = Model>(
  props: NativeAccountFormProps,
  emit: (e: 'update:modelValue', v: M) => void,
  opts: { rules: Rules<M> | (() => Rules<M>); defaults?: Partial<M> }
): FormApi<M> & { creating: ComputedRef<boolean> } {
  const form = useForm<M>({
    model: () => props.modelValue as M,
    update: (v) => emit('update:modelValue', v),
    rules: opts.rules,
    serverErrors: () => props.errors,
    defaults: props.mode === 'create' ? opts.defaults : undefined
  })
  return Object.assign(form, { creating: computed(() => props.mode === 'create') })
}
