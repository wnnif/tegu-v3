export type PayrollTab = 'wages' | 'adjustments' | 'deductions' | 'advances' | 'settlements' | 'history'
export type CentsValue = number | string

export interface PayrollWorkerSummary {
  worker_id: string
  worker_name: string
  order_count: number
  wage_pending_count?: number
  gross_cents: CentsValue
  adjustment_cents: CentsValue
  deduction_cents: CentsValue
  advance_issued_cents: CentsValue
  advance_applied_cents: CentsValue
  advance_balance_cents: CentsValue
  paid_cents: CentsValue
  unpaid_cents: CentsValue
  bonus_accrued_cents?: CentsValue
  bonus_adjustment_cents?: CentsValue
  bonus_paid_cents?: CentsValue
  bonus_balance_cents?: CentsValue
}

export function bonusEntryKindLabel(kind: string): string {
  if (kind === 'automatic') return '施工计提'
  if (kind === 'reversal') return '更正冲销'
  return '手动调整'
}

export function bonusEntryCanReverse(kind: string, reversalId?: string): boolean {
  return kind === 'manual' && !reversalId
}

export interface PayrollWorkersPage {
  rows: PayrollWorkerSummary[]
  total: number
  page: number
  page_size: number
}

export function payrollExportUrl(endpoint: '/payroll-workers' | '/payroll', filters: URLSearchParams): string {
  const params = new URLSearchParams(filters)
  params.delete('page')
  params.set('export', '1')
  return '/api' + endpoint + '?' + params
}

export interface PayrollProjectOption {
  id: string
  name: string
  address: string
  status: string
}

export interface PayrollProjectPage {
  rows: PayrollProjectOption[]
  total: number
  page: number
}

interface PayrollRowBase {
  id: string
  worker_id?: string
  worker_name?: string
  project_id?: string
  project_name?: string
  address?: string
  reason?: string
  actor?: string
  version?: number
  settled?: boolean | number
  reversal_id?: string
  reversal_of?: string
  reversed_by?: string
}

export interface PayrollWageRow extends PayrollRowBase {
  order_id: string
  number: string
  work_date: string
  wage_cents?: CentsValue
  delivery_wage_cents: CentsValue
  wage_configured?: boolean
  settled: boolean | number
}

export interface PayrollAdjustmentRow extends PayrollRowBase {
  amount_cents: CentsValue
  effective_date: string
}

export interface PayrollDeductionRow extends PayrollRowBase {
  amount_cents: CentsValue
  deduction_date: string
  legal_basis?: string
  status: 'pending' | 'confirmed' | 'voided' | string
}

export interface PayrollAdvanceRow extends PayrollRowBase {
  amount_cents: CentsValue
  advance_date: string
  note: string
  allocated_cents: CentsValue
  remaining_cents: CentsValue
  status: 'active' | 'voided' | string
}

export interface PayrollPaymentRow {
  id: string
  amount_cents: CentsValue
  paid_date?: string
  payment_date?: string
  method?: string
  note?: string
  version?: number
  reversed?: boolean | number
  reversal_reason?: string
}

export interface PayrollSettlementRow extends PayrollRowBase {
  settlement_date: string
  gross_cents: CentsValue
  adjustment_cents: CentsValue
  deduction_cents: CentsValue
  advance_cents: CentsValue
  payable_cents: CentsValue
  paid_cents: CentsValue
  remaining_cents: CentsValue
  status: string
  legacy_paid?: boolean | number
  payments: PayrollPaymentRow[]
}

export type PayrollRow = PayrollWageRow | PayrollAdjustmentRow | PayrollDeductionRow | PayrollAdvanceRow | PayrollSettlementRow

export interface PayrollPage<T extends PayrollRow = PayrollRow> {
  rows: T[]
  total: number
  page: number
  page_size: number
  summary?: Record<string, CentsValue | undefined>
}

export interface PayrollHistoryRow {
  id: string
  entity_type: string
  entity_id: string
  event: string
  reason: string
  before_snapshot: Record<string, unknown> | null
  after_snapshot: Record<string, unknown> | null
  actor_name: string
  operation_id: string
  created_at: string
}

export interface PayrollHistoryPage {
  rows: PayrollHistoryRow[]
  total: number
  page: number
  page_size: number
}

export function payrollSnapshotText(snapshot: Record<string, unknown> | null | undefined): string {
  if (snapshot == null) return '—'
  try {
    return JSON.stringify(snapshot, null, 2) ?? '—'
  } catch {
    return '无法显示快照'
  }
}

export interface PayrollSettlementSelection {
  wages: PayrollWageRow[]
  adjustments: PayrollAdjustmentRow[]
  deductions: PayrollDeductionRow[]
  advances: PayrollAdvanceRow[]
}

export function payrollSettlementCanConfirm(selection: PayrollSettlementSelection): boolean {
  const sourceCount = selection.wages.length + selection.adjustments.length + selection.deductions.length
  return sourceCount > 0 && !selection.wages.some(row => row.wage_configured === false) && payrollSettlementBase(selection) >= 0n && payrollSettlementScopeCompatible(selection)
}

export function payrollHasUnconfiguredWage(selection: PayrollSettlementSelection): boolean {
  return selection.wages.some(row => row.wage_configured === false)
}

export function payrollSettlementScopeCompatible(selection: PayrollSettlementSelection): boolean {
  const scopes = new Set([
    ...selection.wages,
    ...selection.adjustments,
    ...selection.deductions,
    ...selection.advances,
  ].map(row => row.project_id || ''))
  return scopes.size <= 1
}

export function payrollSettlementCanReverse(row: PayrollSettlementRow): boolean {
  return row.status === 'confirmed' && !Boolean(Number(row.legacy_paid)) && payrollCents(row.paid_cents) === 0n && !row.payments?.length
}

export function payrollSourceSelectable(tab: PayrollTab, row: PayrollRow): boolean {
  if (tab === 'wages') return 'order_id' in row && row.wage_configured !== false && !Boolean(Number(row.settled))
  if (tab === 'adjustments') return 'effective_date' in row && !Boolean(Number(row.settled)) && !row.reversal_id && !row.reversal_of && !row.reversed_by
  if (tab === 'deductions') return 'deduction_date' in row && row.status === 'confirmed' && !Boolean(Number(row.settled)) && !row.reversed_by
  if (tab === 'advances') return 'advance_date' in row && row.status !== 'voided' && payrollCents(row.remaining_cents) > 0n
  return false
}

export function payrollAdjustmentCanReverse(row: PayrollAdjustmentRow): boolean {
  return !Boolean(Number(row.settled)) && !row.reversal_id && !row.reversal_of && !row.reversed_by
}

export type PayrollActionDraft =
  | { action: 'create-adjustment'; worker_id: string; amount: string; effective_date: string; project_id: string; reason: string }
  | { action: 'reverse-adjustment'; id: string; version: number; reason: string }
  | { action: 'create-deduction'; worker_id: string; amount: string; deduction_date: string; project_id: string; reason: string }
  | { action: 'edit-deduction'; id: string; version: number; confirmed: boolean; amount: string; deduction_date: string; project_id: string; reason: string; legal_basis: string }
  | { action: 'confirm-deduction'; id: string; version: number; legal_basis: string }
  | { action: 'reverse-deduction'; id: string; version: number; reason: string }
  | { action: 'create-advance'; worker_id: string; amount: string; advance_date: string; project_id: string; note: string }
  | { action: 'edit-advance'; id: string; version: number; amount: string; advance_date: string; project_id: string; note: string }
  | { action: 'reverse-advance'; id: string; version: number; reason: string }
  | { action: 'create-settlement'; worker_id: string; settlement_date: string; note: string; selection: PayrollSettlementSelection; advance_allocation: string }
  | { action: 'reverse-settlement'; id: string; version: number; reason: string }
  | { action: 'create-payment'; settlement_id: string; settlement_version: number; amount: string; paid_date: string; method: string; note: string }
  | { action: 'reverse-payment'; settlement_id: string; id: string; version: number; reason: string }

export type PayrollActionPayload =
  | { action: 'create-adjustment'; worker_id: string; amount_cents: number; effective_date: string; project_id?: string; reason: string }
  | { action: 'reverse-adjustment'; id: string; version: number; reason: string }
  | { action: 'create-deduction'; worker_id: string; amount_cents: number; deduction_date: string; project_id?: string; reason: string }
  | { action: 'edit-deduction'; id: string; version: number; amount_cents: number; deduction_date: string; project_id?: string; reason: string; legal_basis: string }
  | { action: 'confirm-deduction'; id: string; version: number; legal_basis: string }
  | { action: 'reverse-deduction'; id: string; version: number; reason: string }
  | { action: 'create-advance'; worker_id: string; amount_cents: number; advance_date: string; project_id?: string; note: string }
  | { action: 'edit-advance'; id: string; version: number; amount_cents: number; advance_date: string; project_id?: string; note: string }
  | { action: 'reverse-advance'; id: string; version: number; reason: string }
  | { action: 'create-settlement'; worker_id: string; settlement_date: string; note: string; wage_ids: string[]; wage_versions: {id:string;version:number}[]; adjustment_ids: string[]; deduction_ids: string[]; deduction_versions: {id:string;version:number}[]; advance_allocations: { id: string; version: number; amount_cents: number }[] }
  | { action: 'reverse-settlement'; id: string; version: number; reason: string }
  | { action: 'create-payment'; settlement_id: string; settlement_version: number; amount_cents: number; paid_date: string; method: string; note: string }
  | { action: 'reverse-payment'; settlement_id: string; id: string; version: number; reason: string }

const MAX_CENTS = 100_000_000_000_000n

export function payrollCents(value: unknown): bigint {
  if (typeof value === 'bigint') return value
  const text = String(value ?? '0')
  return /^-?\d+$/.test(text) ? BigInt(text) : 0n
}

export function payrollMoney(value: unknown): string {
  const amount = payrollCents(value)
  const absolute = amount < 0n ? -amount : amount
  return (amount < 0n ? '-¥ ' : '¥ ') + (absolute / 100n).toLocaleString('zh-CN') + '.' + String(absolute % 100n).padStart(2, '0')
}

export function parsePayrollAmount(input: string, options: { signed?: boolean; allowZero?: boolean } = {}): number {
  const text = input.trim()
  const pattern = options.signed ? /^-?\d+(\.\d{1,2})?$/ : /^\d+(\.\d{1,2})?$/
  if (!pattern.test(text)) throw new Error(options.signed ? '请输入金额，最多保留两位小数' : '请输入正数金额，最多保留两位小数')
  const negative = text.startsWith('-')
  const [whole, fraction = ''] = (negative ? text.slice(1) : text).split('.')
  const cents = BigInt(whole!) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (cents > MAX_CENTS) throw new Error('金额不能超过 1 万亿元')
  if (!options.allowZero && cents === 0n) throw new Error('金额必须大于 0')
  return Number(negative ? -cents : cents)
}

export function payrollSettlementBase(selection: PayrollSettlementSelection): bigint {
  const wages = selection.wages.reduce((sum, row) => sum + payrollCents(row.wage_cents) + payrollCents(row.delivery_wage_cents), 0n)
  const adjustments = selection.adjustments.reduce((sum, row) => sum + payrollCents(row.amount_cents), 0n)
  const deductions = selection.deductions.reduce((sum, row) => sum + payrollCents(row.amount_cents), 0n)
  return wages + adjustments - deductions
}

export function maxAdvanceAllocation(selection: PayrollSettlementSelection): bigint {
  const available = selection.advances.reduce((sum, row) => sum + payrollCents(row.remaining_cents), 0n)
  const base = payrollSettlementBase(selection)
  return base > 0n && available > 0n ? (available < base ? available : base) : 0n
}

function required(value: string, label: string): string {
  const result = value.trim()
  if (!result) throw new Error(`请填写${label}`)
  return result
}

function requiredDate(value: string, label: string): string {
  const date = required(value, label)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new Error(`请填写有效的${label}`)
  const parsed = new Date(date + 'T00:00:00Z')
  if (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== date) throw new Error(`请填写有效的${label}`)
  return date
}

function optionalProject(value: string): string | undefined {
  return value.trim() || undefined
}

export function buildPayrollAction(draft: PayrollActionDraft): PayrollActionPayload {
  switch (draft.action) {
    case 'create-adjustment': return { action: draft.action, worker_id: required(draft.worker_id, '施工人'), amount_cents: parsePayrollAmount(draft.amount), effective_date: requiredDate(draft.effective_date, '生效日期'), project_id: optionalProject(draft.project_id), reason: required(draft.reason, '工资补发原因') }
    case 'reverse-adjustment': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, reason: required(draft.reason, '更正原因') }
    case 'create-deduction': return { action: draft.action, worker_id: required(draft.worker_id, '施工人'), amount_cents: parsePayrollAmount(draft.amount), deduction_date: requiredDate(draft.deduction_date, '扣款日期'), project_id: optionalProject(draft.project_id), reason: required(draft.reason, '扣款原因') }
    case 'edit-deduction': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, amount_cents: parsePayrollAmount(draft.amount), deduction_date: requiredDate(draft.deduction_date, '扣款日期'), project_id: optionalProject(draft.project_id), reason: required(draft.reason, '扣款原因'), legal_basis: draft.confirmed ? required(draft.legal_basis, '合法或约定依据') : draft.legal_basis.trim() }
    case 'confirm-deduction': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, legal_basis: required(draft.legal_basis, '合法或约定依据') }
    case 'reverse-deduction': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, reason: required(draft.reason, '撤销原因') }
    case 'create-advance': return { action: draft.action, worker_id: required(draft.worker_id, '施工人'), amount_cents: parsePayrollAmount(draft.amount), advance_date: requiredDate(draft.advance_date, '预支日期'), project_id: optionalProject(draft.project_id), note: draft.note.trim() }
    case 'edit-advance': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, amount_cents: parsePayrollAmount(draft.amount), advance_date: requiredDate(draft.advance_date, '预支日期'), project_id: optionalProject(draft.project_id), note: draft.note.trim() }
    case 'reverse-advance': return { action: draft.action, id: required(draft.id, '原记录'), version: draft.version, reason: required(draft.reason, '更正原因') }
    case 'create-settlement': {
      if (payrollHasUnconfiguredWage(draft.selection)) throw new Error('存在工价待配置的施工工资，不能纳入结算')
      const base = payrollSettlementBase(draft.selection)
      if (base < 0n) throw new Error('所选工资、调整和扣款不能形成负工资')
      if (base === 0n && draft.selection.wages.length + draft.selection.adjustments.length + draft.selection.deductions.length === 0) throw new Error('没有工资来源时不能单独结算工资预支')
      const requested = parsePayrollAmount(draft.advance_allocation, { allowZero: true })
      const maximum = maxAdvanceAllocation(draft.selection)
      if (BigInt(requested) > maximum) throw new Error(`预支抵扣不能超过 ${payrollMoney(maximum)}`)
      let left = BigInt(requested)
      const advance_allocations = draft.selection.advances.flatMap(row => {
        if (left <= 0n) return []
        const balance = payrollCents(row.remaining_cents)
        const amount = left < balance ? left : balance
        left -= amount
        return amount > 0n ? [{ id: row.id, version: Number(row.version || 0), amount_cents: Number(amount) }] : []
      })
      return { action: draft.action, worker_id: required(draft.worker_id, '施工人'), settlement_date: requiredDate(draft.settlement_date, '结算日期'), note: draft.note.trim(), wage_ids: draft.selection.wages.map(row => row.id), wage_versions: draft.selection.wages.map(row => ({id:row.id,version:Number(row.version||0)})), adjustment_ids: draft.selection.adjustments.map(row => row.id), deduction_ids: draft.selection.deductions.map(row => row.id), deduction_versions: draft.selection.deductions.map(row => ({id:row.id,version:Number(row.version||0)})), advance_allocations }
    }
    case 'reverse-settlement': return { action: draft.action, id: required(draft.id, '结算记录'), version: draft.version, reason: required(draft.reason, '撤销原因') }
    case 'create-payment': return { action: draft.action, settlement_id: required(draft.settlement_id, '结算记录'), settlement_version: draft.settlement_version, amount_cents: parsePayrollAmount(draft.amount), paid_date: requiredDate(draft.paid_date, '实际发放日期'), method: draft.method.trim(), note: draft.note.trim() }
    case 'reverse-payment': return { action: draft.action, settlement_id: required(draft.settlement_id, '结算记录'), id: required(draft.id, '付款记录'), version: draft.version, reason: required(draft.reason, '付款更正原因') }
  }
}

export type BonusPayrollAction = 'create-adjustment' | 'reverse-adjustment' | 'create-settlement' | 'reverse-settlement' | 'create-payment' | 'reverse-payment'

export function bonusPayrollActionName(action: BonusPayrollAction): string {
  const actions: Record<BonusPayrollAction, string> = {
    'create-adjustment': 'bonus-create-adjustment',
    'reverse-adjustment': 'bonus-reverse-adjustment',
    'create-settlement': 'bonus-create-settlement',
    'reverse-settlement': 'bonus-reverse-settlement',
    'create-payment': 'bonus-create-payment',
    'reverse-payment': 'bonus-reverse-payment',
  }
  return actions[action]
}
