import type { CatalogRow, OrderDraft, OrderLine } from './workflows/models'

// Empty on a new line means it follows the default person. Saved lines retain their own ID.
export const lineWorkerId = (line: Pick<OrderLine, 'worker_id'>, defaultId: string) => line.worker_id || defaultId
export function assignLineWorker(line: OrderLine, worker: CatalogRow | null, defaultId = '') {
  const id = worker?.id || ''
  const samePerson = lineWorkerId(line, defaultId) === (id || defaultId)
  // A deliberate row choice is independent, even if it currently matches the default.
  // Only the empty choice returns to inheritance; reselecting a person keeps their snapshot.
  const name = samePerson && line.worker_name ? line.worker_name : worker?.name || ''
  line.worker_override = !!id
  line.worker_id = id
  line.worker_name = name
}
export function orderWorkerIds(order: Pick<OrderDraft, 'worker_id' | 'lines' | 'category_workers'>): string[] {
  return [...new Set([order.worker_id, ...Object.values(order.category_workers || {}), ...order.lines.map(line => lineWorkerId(line, order.category_workers?.[line.category] || ''))].filter(Boolean))]
}
export function needsMaterialWorker(order: Pick<OrderDraft, 'materials' | 'sales' | 'delivery_wage_cents'>): boolean {
  return order.materials.some(item => Number(item.qty_quantum || item.qty_micro) > 0) ||
    (order.sales || []).some(item => Number(item.qty_quantum || item.qty_micro) > 0) || Number(order.delivery_wage_cents || 0) > 0
}
