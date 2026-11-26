import { ref } from 'vue'
import { paymentApi, type CheckoutInfo } from '../topup'
import type { MethodLook } from './methodIcon'

// The enabled payment methods (GET /checkout) for pages that show orders:
// an order names its method by type key and keeps the name it had then.

type CatalogMethod = CheckoutInfo['methods'][number]

let pending: Promise<CatalogMethod[]> | null = null

/** The enabled methods; calls made while one is in flight share it, later calls fetch again. */
export function loadMethods(): Promise<CatalogMethod[]> {
  pending ??= paymentApi
    .checkout()
    .then((c) => c.methods)
    .finally(() => (pending = null))
  return pending
}

/** How an order's method shows: the method's look under the order's name, or the name alone (a method since removed or disabled). */
export function methodLook(methods: readonly CatalogMethod[], type: string, name: string): MethodLook {
  const m = methods.find((x) => x.type === type)
  return m ? { icon: m.icon, color: m.color, logo: m.logo, name } : { name }
}

/** The catalog as a ref (empty until loaded, or when the call fails) and the lookup over it. */
export function useMethodCatalog() {
  const methods = ref<CatalogMethod[]>([])
  loadMethods().then(
    (list) => (methods.value = list),
    () => undefined
  )
  return { methods, look: (type: string, name: string) => methodLook(methods.value, type, name) }
}
