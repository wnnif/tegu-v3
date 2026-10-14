export type ExpenseTab = 'claims' | 'advances' | 'history'
export type ExpenseCategory = '材料采购' | '油费' | '停车费' | '餐费' | '其他'

export interface ExpenseClaim {
  id: string
  person_name: string
  expense_date: string
  category: ExpenseCategory
  amount_cents: number
  note: string
  status: string
  advance_applied_cents: number
  paid_cents: number
  due_cents: number
  purchase_document_id?: string
  purchase_number?: string
  purchase_date?: string
  purchase_total_cents?: number
  version: number
  actor_name?: string
  created_at?: string
}

export interface ExpenseAdvance {
  id: string
  person_name: string
  advance_date: string
  amount_cents: number
  purpose: string
  note: string
  applied_cents: number
  returned_cents: number
  remaining_cents: number
  status: string
  version: number
  actor_name?: string
  created_at?: string
}

export interface ExpenseHistory {
  id: string
  entity_type: string
  entity_id: string
  event: string
  reason: string
  before_snapshot: unknown
  after_snapshot: unknown
  actor_name?: string
  operation_id: string
  created_at: string
  current_status?: string
  current_version?: number
  claim_id?: string
  advance_id?: string
  amount_cents?: number
}

export interface ExpensePurchase {
  id: string
  work_date: string
  total_cents: number
  available_cents?: number
  note: string
  line_items: unknown[]
  version: number
}

export interface ExpensePage<T> {
  rows: T[]
  total: number
  page: number
  page_size: number
}

export type ExpenseAction =
  | { action: 'create-claim'; person_name: string; expense_date: string; category: ExpenseCategory; amount_cents: number; note: string; purchase_document_id?: string }
  | { action: 'update-claim'; id: string; version: number; person_name: string; expense_date: string; category: ExpenseCategory; amount_cents: number; note: string; purchase_document_id?: string; reason: string }
  | { action: 'confirm-claim'; id: string; version: number }
  | { action: 'void-claim'; id: string; version: number; reason: string }
  | { action: 'pay-claim'; id: string; version: number; amount_cents: number; paid_date: string; note: string }
  | { action: 'reverse-payment'; payment_id: string; version: number; reason: string }
  | { action: 'create-advance'; person_name: string; advance_date: string; amount_cents: number; purpose: string; note: string }
  | { action: 'correct-advance'; id: string; version: number; advance_date: string; purpose: string; note: string; reason: string }
  | { action: 'reverse-advance'; id: string; version: number; reason: string }
  | { action: 'allocate-advance'; claim_id: string; claim_version: number; advance_id: string; advance_version: number; amount_cents: number; applied_date: string; note: string }
  | { action: 'reverse-allocation'; allocation_id: string; version: number; reason: string }
  | { action: 'return-advance'; advance_id: string; advance_version: number; amount_cents: number; returned_date: string; note: string }
  | { action: 'reverse-return'; return_id: string; version: number; reason: string }

export function expenseCents(value: unknown): bigint {
  if (typeof value === 'bigint') return value
  if (typeof value === 'number' && Number.isSafeInteger(value)) return BigInt(value)
  if (typeof value === 'string' && /^-?\d+$/.test(value)) return BigInt(value)
  return 0n
}

export function expenseMoney(value: unknown): string {
  const cents = expenseCents(value)
  const negative = cents < 0n
  const amount = negative ? -cents : cents
  const whole = (amount / 100n).toLocaleString('zh-CN')
  return `${negative ? '-' : ''}¥ ${whole}.${String(amount % 100n).padStart(2, '0')}`
}

export function parseExpenseAmount(input: string): number {
  const value = input.trim()
  if (!/^\d+(?:\.\d{1,2})?$/.test(value)) throw new Error('请输入有效的正数金额，最多保留两位小数')
  const [whole, fraction = ''] = value.split('.')
  const cents = BigInt(whole) * 100n + BigInt((fraction + '00').slice(0, 2))
  if (cents <= 0n) throw new Error('金额必须大于 0')
  if (cents > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('金额超出可录入范围')
  return Number(cents)
}

export function expenseClaimStatus(row: Pick<ExpenseClaim, 'status' | 'advance_applied_cents' | 'paid_cents' | 'due_cents'>): string {
  if (row.status === 'voided' || row.status === '已撤销') return '已撤销'
  if (row.status === 'pending' || row.status === '待审核') return '待审核'
  if (!['confirmed','已确认','部分报销','预支已核销','已付清'].includes(row.status)) return row.status || '状态未识别'
  if (['部分报销','预支已核销','已付清'].includes(row.status)) return row.status
  const due = expenseCents(row.due_cents)
  const paid = expenseCents(row.paid_cents)
  const advanced = expenseCents(row.advance_applied_cents)
  if (due <= 0n && advanced > 0n && paid === 0n) return '预支已核销'
  if (due <= 0n && paid > 0n) return '已付清'
  if (paid > 0n || advanced > 0n) return '部分报销'
  return '已确认'
}

export function expenseClaimPending(row: Pick<ExpenseClaim, 'status'>): boolean {
  return row.status === 'pending' || row.status === '待审核'
}

export function expenseClaimConfirmed(row: Pick<ExpenseClaim, 'status'>): boolean {
  return ['confirmed','已确认','部分报销','预支已核销','已付清'].includes(row.status)
}

export function expenseClaimVoided(row: Pick<ExpenseClaim, 'status'>): boolean {
  return row.status === 'voided' || row.status === '已撤销'
}

export function expenseHistorySnapshot(value: unknown): string {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'string') {
    try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
  }
  try { return JSON.stringify(value, null, 2) || '—' } catch { return '—' }
}

export function expenseHistoryCanReverse(row: ExpenseHistory): 'payment' | 'allocation' | 'return' | null {
  if (row.current_status !== 'active' || !Number.isSafeInteger(Number(row.current_version))) return null
  if (row.entity_type === 'payment' || row.entity_type === 'expense_payment') return 'payment'
  if (row.entity_type === 'allocation' || row.entity_type === 'expense_allocation') return 'allocation'
  if (row.entity_type === 'return' || row.entity_type === 'expense_return') return 'return'
  return null
}

export function expenseHistoryReverseAction(row: ExpenseHistory, reason: string): ExpenseAction {
  const kind = expenseHistoryCanReverse(row)
  if (!kind) throw new Error('该历史记录没有可撤销的有效付款或核销事件')
  const why = reason.trim()
  if (!why) throw new Error('请填写更正或撤销原因')
  if (kind === 'payment') return { action: 'reverse-payment', payment_id: row.entity_id, version: Number(row.current_version), reason: why }
  if (kind === 'allocation') return { action: 'reverse-allocation', allocation_id: row.entity_id, version: Number(row.current_version), reason: why }
  return { action: 'reverse-return', return_id: row.entity_id, version: Number(row.current_version), reason: why }
}
