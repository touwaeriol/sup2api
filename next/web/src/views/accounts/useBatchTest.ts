// Batch model test of the accounts list (selected rows, the plugin default
// model each): the run pool, the rows under test and the outcome toast. A
// test has no time limit: stopping the batch (or leaving the page) aborts the
// tests in flight and starts no more; those rows keep their last test. The
// selection itself is the list's (useSelection).
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { toast } from '@sub2api/ui'
import type { AccountLastTest, AccountTestResult } from '@/api/types'
import { useAbortScope } from '@/composables/useAbortScope'
import { errorMessage } from '@/utils/errors'
import { TEST_CONCURRENCY, hasWarnings, lastTestOf } from './accountTest'
import { runPool } from './pool'

export interface BatchTestDeps {
  /** Records a test outcome on the row. */
  tested: (id: number, last: AccountLastTest) => void
}

export function useBatchTest(deps: BatchTestDeps) {
  const { t } = useI18n()
  const requests = useAbortScope()
  const batchTesting = ref(new Set<number>())
  const batchRunning = ref(false)
  const batchDone = ref(0)
  const batchTotal = ref(0)
  async function batchTest(ids: number[]) {
    if (batchRunning.value || !ids.length) return
    batchRunning.value = true
    batchDone.value = 0
    batchTotal.value = ids.length
    let ok = 0
    let failed = 0
    let warned = 0
    const stopped = await requests.run(async ({ signal }) => {
      try {
        await runPool(ids, TEST_CONCURRENCY, async (id) => {
          batchTesting.value = new Set(batchTesting.value).add(id)
          try {
            const r = await api.post<AccountTestResult>(`/accounts/${id}/test`, {}, { signal })
            if (r.ok) ok++
            else failed++
            if (hasWarnings(r)) warned++
            deps.tested(id, lastTestOf(r, ''))
          } catch (e) {
            if (signal.aborted) return
            failed++
            deps.tested(id, { at: new Date().toISOString(), ok: false, latency_ms: 0, model: '', message: errorMessage(e) })
          } finally {
            const next = new Set(batchTesting.value)
            next.delete(id)
            batchTesting.value = next
            batchDone.value++
          }
        }, signal)
      } finally {
        batchRunning.value = false
      }
      return signal.aborted
    })
    const msg = t(stopped ? 'accounts.batchTest.stopped' : 'accounts.batchTest.done', { ok, fail: failed }) + (warned ? t('accounts.testWarnedSuffix', { n: warned }) : '')
    toast(msg, failed || warned ? 'warning' : stopped ? 'info' : 'success')
  }

  return {
    batchTesting, batchRunning, batchDone, batchTotal,
    batchTest, stopBatch: requests.stopAll
  }
}
