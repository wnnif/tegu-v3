export interface SessionUser {
  id: string
  role: string
  permissions: readonly string[]
  crafts?: readonly string[]
}

// The ID must be the public source. Actual order controls use the server's
// editable projection; a company fixed ID is never a substitute for a grant.
export function canFillCraft(user: SessionUser | null, publicCraftId: string): boolean {
  const legacyCategory = ['开工', '包管', '安全防护架', '回填', '防水涂料', '室外防水', '丙纶', '维修'].includes(publicCraftId)
  return !!publicCraftId && !legacyCategory && canAccess(user, 'orders') && (user?.role === 'admin' || user?.role === 'operator' || !!user?.crafts?.includes(publicCraftId))
}

export function canAccess(user: SessionUser | null, permission: string): boolean {
  if (!user) return false
  if (user.role === 'admin') return true
  if (user.role === 'operator') return permission !== 'backups' && (permission !== 'combo_pricing' || user.permissions.includes(permission))
  return user.role === 'employee' && !['users', 'backups'].includes(permission) && user.permissions.includes(permission)
}

const pagePermissions: Record<string, readonly string[]> = {
  dashboard: [], account: [], products: ['warehouse'], inventory: ['warehouse'], warehouses: ['warehouse'],
  companies: ['customers'], projects: ['orders', 'customers', 'finance'], orders: ['orders'],
  wages: ['wages'], finance: ['finance'], 'finance-summary': ['finance'], expenses: ['expenses'], archives: ['orders', 'finance', 'wages', 'expenses', 'warehouse'],
  workers: ['workers'], crafts: ['rates'], users: ['users'], audit: ['users'], backups: ['backups'], 'ticket-issue': ['finance'],
}

export function pageAllowed(user: SessionUser | null, page: string): boolean {
  if (!Object.hasOwn(pagePermissions, page)) return false
  if (page === 'ticket-issue') return !!user && canAccess(user, 'finance') && canAccess(user, 'customer_prices')
  const permissions = pagePermissions[page]
  return !!user && !!permissions && (!permissions.length || permissions.some(permission => canAccess(user, permission)))
}

export class SessionChangedError extends Error {
  constructor() { super('登录账号已改变，原请求结果已忽略'); this.name = 'SessionChangedError' }
}

// A response started under a previous login must never enter the new login's UI.
export function createSessionGate() {
  let revision = 0
  return {
    capture: () => revision,
    invalidate: () => ++revision,
    current: (value: number) => value === revision,
    assert(value: number) { if (value !== revision) throw new SessionChangedError() },
  }
}
