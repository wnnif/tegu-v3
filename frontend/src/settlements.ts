export type CentsValue = number | string
export interface FinanceOrder {
  id: string; number: string; version: number; company_id: string
  address: string; contact: string; work_date: string; note: string; status: string
  total_cents: CentsValue; received_cents: CentsValue; outstanding_cents: CentsValue
  wage_cents: CentsValue; wage_paid: boolean
  order_id: string; worker_id: string; worker_name: string
}
export interface PaymentReceipt {
  id: string; amount_cents: CentsValue; received_at: string; method: string
  voucher_no: string; attachment: string; note: string; voided: boolean | number | string
}
export interface OrderSettlement {
  total_cents?: CentsValue; received_cents?: CentsValue; outstanding_cents?: CentsValue
  ticket_no?: string; delivered_at?: string; recipient?: string; ticket_attachment?: string; return_note?: string
  payment_receipts?: PaymentReceipt[]
}
export interface FinanceOrderSource extends OrderSettlement {
  id: string; number: string; version: number; company_id: string
  address: string; work_date: string; status: string
  contact?: string; note?: string; wage_cents?: CentsValue; wage_paid?: boolean | number | string
  order_id?: string; worker_id?: string; worker_name?: string
}
export function cents(value: unknown): bigint {
  if (typeof value === 'bigint') return value
  const text = String(value ?? '0')
  return /^-?\d+$/.test(text) ? BigInt(text) : 0n
}
export function settlementMoney(value: unknown): string {
  const amount = cents(value), absolute = amount < 0n ? -amount : amount
  return '¥ ' + (amount < 0n ? '-' : '') + (absolute / 100n).toLocaleString('zh-CN') + '.' + (absolute % 100n).toString().padStart(2, '0')
}
export function parseReceiptAmount(text: string): number {
  const input = text.trim()
  if (!/^\d+(\.\d{1,2})?$/.test(input)) throw new Error('本次收款请输入金额，最多保留两位小数')
  const [whole, fraction = ''] = input.split('.')
  const amount = BigInt(whole!) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (amount <= 0n || amount > 100000000000000n) throw new Error('本次收款须大于 0，且不超过 1 万亿元')
  return Number(amount)
}
export function financeOrder(row: FinanceOrderSource): FinanceOrder {
  const total = cents(row.total_cents), received = cents(row.received_cents ?? (row.status === '已收款' ? row.total_cents : 0))
  return {
    id: String(row.id || ''), number: String(row.number || ''), version: Number(row.version), company_id: String(row.company_id || ''),
    address: String(row.address || ''), contact: String(row.contact || ''), work_date: String(row.work_date || ''), note: String(row.note || ''), status: String(row.status || ''),
    total_cents: total.toString(), received_cents: received.toString(), outstanding_cents: cents(row.outstanding_cents ?? (row.status === '已取消' ? 0 : total - received)).toString(),
    wage_cents: cents(row.wage_cents).toString(), wage_paid: !!Number(row.wage_paid),
    order_id: row.order_id || row.id, worker_id: row.worker_id || '', worker_name: row.worker_name || '',
  }
}
export const sourceOrderId = (row: Pick<FinanceOrderSource, 'id' | 'order_id'>): string => row.order_id || row.id
export function wageSettlementPayload(action: string, rows: Pick<FinanceOrder, 'id' | 'version'>[], note = '') {
  if (!['pay', 'undo-pay'].includes(action)) throw new Error('请选择工资发放或撤销操作')
  return { action, orders: rows.map(row => ({ id: row.id, version: row.version })), note }
}
