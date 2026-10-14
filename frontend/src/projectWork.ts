import { hasCustomerPrice } from './customerPrices'
import type { Material, Numeric, OrderDraft } from './workflows/models'
import type { ProjectPackage } from './projects'
import { printWorkerNames, savedLineWorkerName, wageRecordKey, type PrintOrder, type WageRecord } from './printOrder'
export interface Sale extends Material {
  id?: string
  editable?: boolean
  company_price_fixed?: boolean
  price_cents?: Numeric
  total_cents?: Numeric
  sale_qty_micro?: Numeric
  price_unit?: string
  conversion_ratio?: string
}
export interface ProjectTicket {
  id: string
  company_id?: string
  company_name?: string
  project_name?: string
  address?: string
  number: string
  ticket_mode_snapshot?: string
  kind: 'normal' | 'append' | 'summary'
  slot?: number | null
  status: string
  version: number
  total_cents?: Numeric
  received_cents?: Numeric
  outstanding_cents?: Numeric
  package_charge_cents?: Numeric
  package_charge?: {id: string; name: string; package_type?: 'room_count' | 'fixed_scope'; room_count: number; amount_cents: Numeric}
  delivered_at?: string
  recipient?: string
}
export interface ProjectWork {
  project: {id: string; number: string; name: string; address: string; company_id: string; ticket_mode: string; package?: ProjectPackage}
  orders: OrderDraft[]
  tickets: ProjectTicket[]
  total: number
  page: number
  page_size: number
}
export interface TicketDetail {
  ticket: ProjectTicket
  orders: PrintOrder[]
  versions: {version: number; created_at: string; event: string}[]
  payment_receipts?: {id: string; amount_cents?: Numeric; received_at: string; method?: string; voucher_no?: string; note?: string; voided?: boolean; voided_at?: string}[]
}
export const ticketLabel = (ticket: ProjectTicket) => ticket.kind === 'summary' ? '全工地累计' : ticket.kind === 'append' ? '追加票' : ticket.ticket_mode_snapshot === 'single' ? '统一票' : ticket.ticket_mode_snapshot === 'split' ? (ticket.slot === 2 ? '第二票' : '第一票') : '正常票'
export type ProjectTicketPrintOrder = PrintOrder & {
  package_charge?: NonNullable<PrintOrder['package_charge']> & {package_type?: 'room_count' | 'fixed_scope'}
}
export function ticketPrintOrder(detail: TicketDetail): ProjectTicketPrintOrder {
  if (!hasCustomerPrice(detail.ticket.total_cents)) throw new Error('打印客户金额需要查看客户价格权限')
  const orders = detail.orders
  const first = orders[0]
  const dates = [...new Set(orders.map(order => order.work_date))].sort()
  const workerNames = [...new Set(orders.flatMap(order => printWorkerNames(order).split('、')).filter(Boolean))].join('、')
  const hasWages = orders.length > 0 && orders.every(order => order.wage_cents != null)
  const wageRecords = new Map<string, WageRecord>()
  if (hasWages) for (const order of orders) {
    const records = order.wage_records?.length ? order.wage_records : [{
      order_id: order.id, worker_id: order.worker_id, number: order.number, work_date: order.work_date,
      worker_name: order.worker_name || '', wage_cents: order.wage_cents || 0,
      delivery_wage_cents: order.delivery_wage_cents || 0, wage_paid: order.wage_paid,
    }]
    for (const record of records) if (!wageRecords.has(wageRecordKey(record))) wageRecords.set(wageRecordKey(record), record)
  }
  return {
    company_id: detail.ticket.company_id || first?.company_id || '', company_name: detail.ticket.company_name || first?.company_name || '', worker_id: '',
    worker_name: workerNames, worker_names: workerNames,
    number: detail.ticket.number, address: detail.ticket.address || first?.address || '', work_date: dates.join('、'),
    status: detail.ticket.status, total_cents: detail.ticket.total_cents,
    ...(detail.ticket.package_charge ? { package_charge: {
      name: detail.ticket.package_charge.name,
      ...(detail.ticket.package_charge.package_type ? { package_type: detail.ticket.package_charge.package_type } : {}),
      room_count: detail.ticket.package_charge.room_count,
      amount_cents: detail.ticket.package_charge.amount_cents,
    } } : {}),
    project_name: detail.ticket.project_name || first?.project_name, project_number: first?.project_number,
    contact: first?.contact, print_settings: first?.print_settings,
    lines: orders.flatMap(order => order.lines.map(line => ({...line, work_date: order.work_date, worker_id: line.worker_id || order.worker_id, worker_name:savedLineWorkerName(line,order)}))),
    materials: orders.flatMap(order => order.materials || []), sales: orders.flatMap(order => order.sales || []),
    ...(hasWages ? {
      wage_cents: [...wageRecords.values()].reduce((sum, record) => sum + Number(record.wage_cents || 0), 0),
      delivery_wage_cents: [...wageRecords.values()].reduce((sum, record) => sum + Number(record.delivery_wage_cents || 0), 0),
      wage_records: [...wageRecords.values()],
    } : {}),
  }
}
