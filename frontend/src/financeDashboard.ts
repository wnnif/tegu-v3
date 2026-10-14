export interface FinanceDashboardMetric {
  key: string
  label: string
  amount_cents: string
  basis: string
  source: string
}

export interface FinanceDashboardGroup {
  key: string
  label: string
  metrics: FinanceDashboardMetric[]
}

export interface FinanceDashboardResponse {
  range: { from: string; to: string; all: boolean }
  queried_at: string
  balance_note: string
  groups: FinanceDashboardGroup[]
}

export interface FinanceDashboardFilter { from: string; to: string; all: boolean }
export interface FinanceDashboardSourceNavigation {
  view: 'orders' | 'expenses' | 'inventory'
  scope: 'period' | 'snapshot'
  filters?: { from: string; to: string; kind?: string; inventoryTab?: 'history' }
}

export interface FinanceDashboardRange { from: string; to: string; all: boolean }

const groupPermission: Record<string, string> = { customer: 'customer_prices', inventory: 'warehouse', payroll: 'wages', expenses: 'expenses' }

export function financeDashboardGroups(groups: FinanceDashboardGroup[], permissions: string[]): FinanceDashboardGroup[] {
  if (!permissions.includes('finance')) return []
  return groups.filter(group => !!groupPermission[group.key] && permissions.includes(groupPermission[group.key]!))
}

export function financeDashboardMoney(value: unknown): string {
  if (typeof value !== 'string' || !/^-?\d+$/.test(value)) return '—'
  const cents = BigInt(value), amount = cents < 0n ? -cents : cents
  return `${cents < 0n ? '-' : ''}¥ ${(amount / 100n).toLocaleString('zh-CN')}.${String(amount % 100n).padStart(2, '0')}`
}

export function financeDashboardMonth(now = new Date()): FinanceDashboardFilter {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(now)
  const part = (name: string) => parts.find(item => item.type === name)!.value
  return { from: `${part('year')}-${part('month')}-01`, to: `${part('year')}-${part('month')}-${part('day')}`, all: false }
}

export function financeDashboardParams(filter: FinanceDashboardFilter): URLSearchParams {
  if (filter.all) return new URLSearchParams({ all: '1' })
  const valid = (value: string) => /^\d{4}-\d{2}-\d{2}$/.test(value) && value >= '1000-01-01' && new Date(value+'T00:00:00Z').toISOString().slice(0, 10) === value
  try {
    if (!valid(filter.from) || !valid(filter.to)) throw new Error()
  } catch { throw new Error('请选择有效的开始日期和结束日期') }
  if (filter.from > filter.to) throw new Error('开始日期不能晚于结束日期')
  return new URLSearchParams({ from: filter.from, to: filter.to })
}

export function financeDashboardExport(filter: FinanceDashboardFilter): string {
  const params = financeDashboardParams(filter)
  params.set('export', '1')
  return '/api/finance-summary?'+params
}

export function financeDashboardSourceAllowed(source: string, permissions: string[]): boolean {
  if (source === 'ticket-issue') return permissions.includes('finance') && permissions.includes('customer_prices')
  return permissions.includes(source === 'inventory' ? 'warehouse' : source)
}

export function financeDashboardSourceNavigation(metric: Pick<FinanceDashboardMetric, 'key'|'source'>, range: FinanceDashboardRange): FinanceDashboardSourceNavigation | null {
  if (metric.source === 'orders' && ['construction', 'sales'].includes(metric.key)) {
    return { view: 'orders', scope: 'period', filters: { from: range.from, to: range.to } }
  }
  if (metric.source === 'expenses' && metric.key === 'expense_registered') {
    return { view: 'expenses', scope: 'period', filters: { from: range.from, to: range.to } }
  }
  if (metric.source === 'inventory' && metric.key === 'purchase') {
    return { view: 'inventory', scope: 'period', filters: { from: range.from, to: range.to, kind: '进货', inventoryTab: 'history' } }
  }
  if (metric.source === 'inventory' && metric.key === 'inventory_value') {
    return { view: 'inventory', scope: 'snapshot' }
  }
  return null
}
