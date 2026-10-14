import { isCountCraftMode, measureUnit } from './orderSegments'

export interface PrintSegment {
  surface?: string
  mode?: string
  length?: string | number
  width?: string | number
  height?: string | number
  count?: string | number
  amount_cents?: string | number
}

export interface PrintLine {
  id?: string
  craft_id?: string
  temporary?: boolean
  category: string
  name: string
  spec?: string
  region?: string
  subitem_name?: string
  actual_location?: string
  description?: string
  charge_note?: string
  charge_mode?: string
  use_included_price?: boolean
  package_included?: boolean
  work_date?: string
  worker_name?: string
  worker_id?: string
  mode: string
  qty_micro?: string | number
  price_cents: string | number
  total_cents: string | number
  wage_cents?: string | number
  labor_cents?: string | number
  segments?: PrintSegment[]
}

export interface PrintMaterial {
  product_name: string
  warehouse_name: string
  qty_micro: string | number
  unit: string
}

export interface PrintOrder {
  id?: string
  company_id: string
  company_name?: string
  worker_id: string
  worker_name?: string
  worker_names?: string
  number: string
  project_number?: string
  project_name?: string
  address: string
  work_date: string
  contact?: string
  phone?: string
  status: string
  total_cents?: string | number
  package_charge?: {name: string; room_count: number; amount_cents: string | number}
  wage_cents?: string | number
  delivery_wage_cents?: string | number
  wage_records?: WageRecord[]
  wage_paid?: boolean | number | string
  materials?: PrintMaterial[]
  sales?: {id?:string;product_name?:string;qty_micro:string|number;sale_qty_micro?:string|number;unit?:string;price_unit?:string;price_cents:string|number;total_cents?:string|number}[]
  note?: string
  print_settings?: {title?: string; company?: string}
  lines: PrintLine[]
}

export interface WageRecord {
  id?: string; order_id?: string; worker_id?: string; worker_name: string
  number: string; work_date: string; wage_cents: string | number; delivery_wage_cents: string | number
  wage_paid?: boolean | number | string; version?: number
}
export function wageRecordKey(record: WageRecord): string {
  return record.id || `${record.order_id || record.number}:${record.worker_id || record.worker_name}`
}
export function printWorkerNames(order: PrintOrder): string {
  return order.worker_names || [...new Set([
    ...order.lines.map(line => line.worker_name || ((!line.worker_id || line.worker_id === order.worker_id) ? order.worker_name : '')),
    ...(order.wage_records || []).map(record => record.worker_name),
    ...(Number(order.delivery_wage_cents) > 0 || order.materials?.length || order.sales?.length ? [order.worker_name] : []),
  ].filter(Boolean))].join('、') || order.worker_name || ''
}
export function savedLineWorkerName(line:{worker_id?:string;worker_name?:string},order:{worker_id?:string;worker_name?:string}):string {
  return line.worker_name || ((!line.worker_id || line.worker_id === order.worker_id) ? order.worker_name || '' : '')
}

export interface PrintRow {line: PrintLine; segment?: PrintSegment; first: boolean}

export function printableLines(order: PrintOrder): PrintLine[] {
  return order.lines.filter(line => Number(line.qty_micro) > 0 || Number(line.total_cents) > 0)
}

export function printableRows(lines: PrintLine[]): PrintRow[] {
  return lines.flatMap<PrintRow>(line => {
    const segments = line.mode === '金额' ? [] : (line.segments || []).filter(segment =>
      (segment.mode || line.mode) === '米' ? Number(segment.length) > 0 : isCountCraftMode(segment.mode || line.mode) ? Number(segment.count) > 0 : segment.mode === '金额' ? Number(segment.amount_cents) > 0 :
        Number(segment.length) > 0 || Number(segment.width) > 0 || Number(segment.height) > 0)
    return segments.length
      ? segments.map((segment, index) => ({line, segment, first: index === 0}))
      : [{line, first: true}]
  })
}

export interface AcceptanceRow { region: string; worker_name:string; worker_id?:string; work_date?:string; wall: string; floor: string; dimensions:string[]; quantity: string; description: string; total_cents: string | number }
export interface AcceptanceGroup { label: string; mode: string; price_cents: string | number; total_cents: bigint; quantity_micro: bigint; rows: AcceptanceRow[]; charge_note?: string }

export function formatCents(value: string | number | bigint | undefined): string {
  const cents = BigInt(value || 0)
  return `${cents < 0n ? '-' : ''}${(cents < 0n ? -cents : cents) / 100n}.${String((cents < 0n ? -cents : cents) % 100n).padStart(2, '0')}`
}

export function formatQuantity(value: string | number | bigint): string {
  const micro = BigInt(value || 0)
  const absolute = micro < 0n ? -micro : micro
  const whole = absolute / 1000000n
  const fraction = String(absolute % 1000000n).padStart(6, '0').replace(/0+$/, '')
  return `${micro < 0n ? '-' : ''}${whole}${fraction ? '.' + fraction : ''}`
}

export function printLineLocation(line: {subitem_name?:string;actual_location?:string;region?:string}): string {
  const parts = [line.subitem_name]
  if (line.actual_location && line.actual_location !== line.subitem_name) parts.push(line.subitem_name ? `实际部位：${line.actual_location}` : line.actual_location)
  if (line.region && !parts.includes(line.region) && line.region !== line.actual_location) parts.push(line.subitem_name || line.actual_location ? `原部位：${line.region}` : line.region)
  return parts.filter(Boolean).join('；') || '—'
}

export function printChargeDescription(line: {description?:string;charge_note?:string;charge_mode?:string;use_included_price?:boolean;package_included?:boolean}): string {
  return [...new Set([
    line.description,
    line.charge_mode === 'free' ? '免费施工' : line.charge_mode === 'redo' ? '补做不收费' : '',
    line.package_included ? '套餐内项目，不另收施工费' : line.charge_note || (line.use_included_price ? '组合含价，不另收费' : ''),
  ].filter(Boolean))].join('；')
}

function dimension(value: string | number | undefined): string {
  return String(value ?? '').replace(/\.0+$/, '')
}

function segmentText(segment: PrintSegment, mode: string): string {
  if (mode === '米') return `${dimension(segment.length)} 米`
  if (isCountCraftMode(mode)) return `${segment.count || 0} ${mode}`
  if (mode === '金额') return `金额 ${formatCents(segment.amount_cents)} 元`
  return [segment.length, segment.width, ...(mode === '立方' ? [segment.height] : [])].map(dimension).join(' × ')
}
export function savedLineDimensions(line:{mode:string;segments?:PrintSegment[];description?:string}):string[] {
  if(line.mode==='金额')return ['已施工，按整项计量']
  return (line.segments||[]).map(segment=>`${segment.surface ? segment.surface+'：' : ''}${segmentText(segment,segment.mode||line.mode)}`)
}

function quantity(line: PrintLine): string {
  if (line.mode === '金额') return '1 项'
  const modes = new Set((line.segments || []).map(segment => segment.mode || line.mode))
  const unit = modes.size > 1 ? '（混合计量，详见各段）' : measureUnit(line.mode)
  // The saved quantity is authoritative; printing must not measure segments again.
  return `${formatQuantity(line.qty_micro || 0)} ${unit}`
}

export function acceptanceGroups(order: PrintOrder): AcceptanceGroup[] {
  const groups: AcceptanceGroup[] = []
  const byKey = new Map<string, AcceptanceGroup>()
  for (const line of printableLines(order)) {
    const identity = line.temporary ? ['temporary', line.id || groups.length] : ['craft', line.craft_id || 'legacy', line.category, line.name]
    const key = JSON.stringify([...identity, line.mode, line.price_cents, line.charge_mode || 'normal', line.charge_note || '', !!line.use_included_price])
    let group = byKey.get(key)
    if (!group) {
      group = { label: line.name === line.category ? line.name : `${line.category} / ${line.name}`, mode: line.mode,
        price_cents: line.price_cents, total_cents: 0n, quantity_micro: 0n, rows: [], charge_note: line.charge_note || (line.use_included_price ? '组合含价，不另收费' : '') }
      byKey.set(key, group)
      groups.push(group)
    }
    group.total_cents += BigInt(line.total_cents || 0)
    group.quantity_micro += BigInt(line.qty_micro || 0)
    const wall: string[] = [], floor: string[] = []
    for (const segment of line.segments || []) {
      const mode = segment.mode || line.mode
      const text = segmentText(segment, mode)
      if (/墙/.test(segment.surface || '')) wall.push(text)
      else floor.push(text)
    }
    group.rows.push({ region: printLineLocation(line), worker_name:savedLineWorkerName(line,order),worker_id:line.worker_id||order.worker_id,work_date:line.work_date||order.work_date,wall: wall.join(' + ') || '／', dimensions:savedLineDimensions(line),
      floor: floor.join(' + ') || (line.mode === '金额' ? line.description || '按金额计价' : '／'),
      quantity: quantity(line), description: printChargeDescription(line), total_cents: line.total_cents })
  }
  return groups
}
