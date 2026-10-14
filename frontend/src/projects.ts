import { customerMoney } from './customerPrices'
export type ProjectStatus = '进行中' | '已停用' | '已结项'
export type ProjectAction = 'disable' | 'enable' | 'close'

export interface CompanyOption { id: string; name: string }
export type PackageCoverageMapping = 'mapped' | 'included_unmapped' | 'pending_mapping' | 'pending'
export interface PackageCoverage {
  item_name: string
  craft_id?: string
  source_craft_id?: string
  subitem_id?: string
  mapping: PackageCoverageMapping
}
export interface ProjectPackage {
  id: string
  name: string
  package_type?: 'room_count' | 'fixed_scope'
  room_count: number
  price_cents?: number | string
  coverage: PackageCoverage[]
}
export interface CompanyPackageOption extends ProjectPackage {
  price_cents: number | string
}
export interface PackageCoverageGroups {
  mapped: string[]
  includedUnmapped: string[]
  pending: string[]
}
export function packageCoverageGroups(coverage: PackageCoverage[] = []): PackageCoverageGroups {
  return coverage.reduce<PackageCoverageGroups>((groups, item) => {
    if (item.mapping === 'mapped') groups.mapped.push(item.item_name)
    else if (item.mapping === 'included_unmapped') groups.includedUnmapped.push(item.item_name)
    else groups.pending.push(item.item_name)
    return groups
  }, {mapped: [], includedUnmapped: [], pending: []})
}
export function packageDisplayLabel(pkg: Pick<ProjectPackage, 'name' | 'package_type' | 'room_count'>): string {
  return pkg.package_type === 'fixed_scope' ? pkg.name : `${pkg.name} · ${pkg.room_count} 房`
}
export interface ProjectRecord {
  id: string
  number: string
  company_id: string
  company_name: string
  name: string
  address: string
  status: ProjectStatus
  version: number
  order_count: number
  total_cents?: number
  received_cents?: number
  undelivered_cents?: number
  delivered_outstanding_cents?: number
  outstanding_cents?: number
  discount_cents?: number|string
  allocated_cents?: number|string
  delivered_cents?: number|string
  unsettled_cents?: number|string
  package?: ProjectPackage
}
export interface ProjectOrder {
  id: string
  number: string
  work_date: string
  address: string
  status: string
  total_cents?: number
  received_cents?: number
  outstanding_cents?: number
  worker_name?: string
  worker_names?: string
}
export interface UnassignedOrder extends Pick<ProjectOrder, 'id' | 'number' | 'work_date' | 'address' | 'status' | 'total_cents'> { version: number }
export interface ProjectList { rows: ProjectRecord[]; total: number; page: number }
export interface ProjectDetail { project: ProjectRecord; orders: ProjectOrder[]; page: number }
export interface UnassignedOrderList { rows: UnassignedOrder[]; total: number; page: number }
export const projectMoney = customerMoney
export const projectError = (error: unknown) => error instanceof Error ? error.message : '操作失败，请稍后重试'
export const isProjectConflict = (message: string) => /版本|已被|冲突|刷新|重新加载|已关联/.test(message)
