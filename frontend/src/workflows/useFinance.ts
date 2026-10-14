import { computed, ref, type Ref } from 'vue'
import { financeOrder } from '../settlements'
import type { OrderRow } from './models'

export type FinanceAction = 'edit' | 'submit' | 'deliver' | 'return' | 'receive_partial' | 'receive_full' | 'pay' | 'undo-pay'

export function useFinance(selected: Ref<string[]>, orders: Ref<OrderRow[]>, error: Ref<string>) {
  const batchAction = ref<FinanceAction | ''>('')
  const settlementOrders = computed(() => orders.value.filter(order => selected.value.includes(order.id)).map(financeOrder))

  function settle(action: FinanceAction) {
    if (action === 'receive_partial' && selected.value.length !== 1) {
      error.value = '部分收款一次请选择一张施工单'
      return
    }
    if (selected.value.length) batchAction.value = action
  }

  return { batchAction, settlementOrders, settle }
}
