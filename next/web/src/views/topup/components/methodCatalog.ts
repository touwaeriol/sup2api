import { ref } from 'vue'
import { topupApi } from '../api'
import type { CheckoutMethod } from '../topup'
import type { MethodLook } from './methodIcon'

// The enabled payment methods (GET /me/topup) for pages that show orders:
// an order names its method by type key and keeps the name it had then.

let pending: Promise<CheckoutMethod[]> | null = null

/** The enabled methods; calls made while one is in flight share it, later calls fetch again. */
function loadMethods(): Promise<CheckoutMethod[]> {
  pending ??= topupApi
    .checkout()
    .then(c => c.methods)
    .finally(() => (pending = null))
  return pending
}

/** How an order's method shows: the method's look under the order's name, or the name alone (a method since removed or disabled). */
function methodLook(methods: readonly CheckoutMethod[], type: string, name: string): MethodLook {
  const m = methods.find(x => x.type === type)
  return m ? { icon: m.icon, color: m.color, logo: m.logo, name } : { name }
}

/** The catalog as a ref (empty until loaded, or when the call fails) and the lookup over it. */
export function useMethodCatalog() {
  const methods = ref<CheckoutMethod[]>([])
  loadMethods().then(
    list => (methods.value = list),
    () => undefined
  )
  return { methods, look: (type: string, name: string) => methodLook(methods.value, type, name) }
}
