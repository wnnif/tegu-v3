import { customerMoney, hasCustomerPrice } from './customerPrices'
import { isCountCraftMode, measureUnit } from './orderSegments'
import { groupOrderRowsBySubitem } from './orderRowGroups'
import { orderedCompanyCategories } from './companyCraftOrder'
import { formatQuantum } from './stockQuantity'
import type { Numeric } from './workflows/models'
import type {
  SavedCombinationDetail, SavedCombinationLine, SavedCombinationLineView,
  SavedOrderCombination, SavedOrderLine, SavedOrderMaterial, SavedOrderRecord,
  SavedOrderSegment, SavedOrderWorkView, SavedWorkCategory, SavedWorkCombination,
  SavedWorkDimension, SavedWorkMaterial, SavedWorkMoney, SavedWorkRow,
} from './workflows/readModels'

const UNRECORDED = '未记录'
const measurementModes = new Set(['个', '米', '平方', '立方', '金额', '根', '处', '孔位', '副', '张', '套'])

function integerMicro(value: Numeric | undefined): bigint | undefined {
  if (typeof value === 'number') return Number.isSafeInteger(value) ? BigInt(value) : undefined
  if (typeof value === 'string' && /^-?\d+$/.test(value.trim())) return BigInt(value.trim())
  return undefined
}

// Format the saved integer, never measure segments again. Missing is not zero.
export function savedQuantityText(value: Numeric | undefined, unit: string): string {
  const micro = integerMicro(value)
  if (micro === undefined) return UNRECORDED
  const absolute = micro < 0n ? -micro : micro
  const fraction = String(absolute % 1000000n).padStart(6, '0').replace(/0+$/, '')
  return (micro < 0n ? '-' : '') + String(absolute / 1000000n) + (fraction ? '.' + fraction : '') + ' ' + unit
}

function unitFor(mode: string): string {
  return measurementModes.has(mode) ? measureUnit(mode) : mode || '未记录单位'
}

function savedMoney(price: Numeric | undefined, total: Numeric | undefined, allowed: boolean): SavedWorkMoney {
  return {
    ...(allowed && hasCustomerPrice(price) ? { price_cents: price } : {}),
    ...(allowed && hasCustomerPrice(total) ? { total_cents: total } : {}),
    price_text: customerMoney(price, allowed),
    amount_text: customerMoney(total, allowed),
  }
}

function dimensionValue(value: Numeric | undefined): string {
  return value === undefined || value === '' ? UNRECORDED : String(value)
}

function segmentText(segment: SavedOrderSegment, mode: string, allowed: boolean): string {
  let text: string
  switch (mode) {
    case '米': text = '长度：' + dimensionValue(segment.length) + ' 米'; break
    case '平方': text = '长 ' + dimensionValue(segment.length) + ' 米 × 宽 ' + dimensionValue(segment.width) + ' 米'; break
    case '立方': text = '长 ' + dimensionValue(segment.length) + ' 米 × 宽 ' + dimensionValue(segment.width) + ' 米 × 高 ' + dimensionValue(segment.height) + ' 米'; break
    case '金额': text = '整项金额：' + customerMoney(segment.amount_cents, allowed); break
    default: text = '计量方式：' + (mode || UNRECORDED)
  }
  if (isCountCraftMode(mode)) text = '数量：' + dimensionValue(segment.count) + ' ' + mode
  return segment.surface ? segment.surface + '：' + text : text
}

function savedDimensions(line: SavedOrderLine, key: string, allowed: boolean): SavedWorkDimension[] {
  const segments = (line.segments || []).map((segment, index) => {
    const mode = segment.mode || line.mode
    return { key: key + ':segment:' + index, mode, text: segmentText(segment, mode, allowed) }
  })
  if (segments.length) return segments
  return [{ key: key + ':no-segments', mode: line.mode, text: line.mode === '金额' ? '整项施工（无尺寸）' : '未记录方量明细' }]
}

export function savedRowAnchor(orderID: string, lineID: string): string {
  return 'saved-order-' + encodeURIComponent(orderID) + '-line-' + encodeURIComponent(lineID)
}

function savedRow(order: SavedOrderRecord, line: SavedOrderLine, index: number, allowed: boolean, canWages: boolean): SavedWorkRow {
  const identity = line.id || 'unrecorded-' + index
  const key = JSON.stringify([order.id, identity])
  const unit = unitFor(line.mode)
  return {
    key, anchor: savedRowAnchor(order.id, identity), id: line.id, craft_id: line.craft_id,
    category: line.category, name: line.name, mode: line.mode, unit, spec: line.spec || '',
    subitem_id: line.subitem_id || '', subitem_name: line.subitem_name || '',
    actual_location: line.actual_location || '', region: line.region || '', worker_id: line.worker_id || '',
    worker_name: line.worker_name || '', description: line.description || '',
    charge_note: line.charge_note || '', temporary: !!line.temporary,
    ...(allowed && line.charge_mode ? { charge_mode: line.charge_mode } : {}),
    dimensions: savedDimensions(line, key, allowed),
    ...(line.qty_micro === undefined ? {} : { qty_micro: line.qty_micro }),
    ...(line.waived_qty_micro === undefined ? {} : { waived_qty_micro: line.waived_qty_micro }),
    ...(line.chargeable_qty_micro === undefined ? {} : { chargeable_qty_micro: line.chargeable_qty_micro }),
    quantity_text: savedQuantityText(line.qty_micro, unit),
    waived_quantity_text: savedQuantityText(line.waived_qty_micro, unit),
    chargeable_quantity_text: savedQuantityText(line.chargeable_qty_micro, unit),
    has_waiver: (integerMicro(line.waived_qty_micro) || 0n) > 0n,
    ...savedMoney(line.price_cents, line.total_cents, allowed),
    ...(canWages && line.labor_cents !== undefined ? { labor_cents: line.labor_cents } : {}),
    ...(canWages && line.wage_override_cents !== undefined ? { wage_override_cents: line.wage_override_cents } : {}),
    ...(canWages && line.wage_adjustments?.length ? { wage_adjustments: line.wage_adjustments } : {}),
  }
}

interface CraftBuilder { key: string; craft_id: string; name: string; rows: SavedWorkRow[] }
interface CategoryBuilder { key: string; category: string; crafts: CraftBuilder[]; byCraft: Map<string, CraftBuilder> }

export interface SavedWorkOrderConfiguration {
  readonly categoryOrder?: readonly string[];
  readonly crafts?: readonly { readonly id: string; readonly subitems?: readonly { readonly id: string }[] }[];
  readonly canWages?: boolean;
}

function groupedRows(rows: readonly SavedWorkRow[], configuration: SavedWorkOrderConfiguration): SavedWorkCategory[] {
  const groups: CategoryBuilder[] = [], byCategory = new Map<string, CategoryBuilder>()
  // The API snapshots remain immutable. Current company/subitem configuration
  // changes display order only, never names, positions, personnel or amounts.
  for (const row of rows) {
    let category = byCategory.get(row.category)
    if (!category) {
      category = { key: JSON.stringify(['category', row.category]), category: row.category, crafts: [], byCraft: new Map() }
      groups.push(category)
      byCategory.set(row.category, category)
    }
    const identity = row.temporary || !row.craft_id ? ['actual-line', row.id || row.key] : ['fixed-craft', row.craft_id]
    const key = JSON.stringify([row.category, ...identity])
    let craft = category.byCraft.get(key)
    if (!craft) {
      craft = { key, craft_id: row.craft_id, name: row.name, rows: [] }
      category.crafts.push(craft)
      category.byCraft.set(key, craft)
    }
    craft.rows.push(row)
  }
  const categoryOrder = orderedCompanyCategories(groups.map(group => group.category), configuration.categoryOrder)
  const byCraftID = new Map((configuration.crafts || []).map(craft => [craft.id, craft]))
  const craftRank = new Map((configuration.crafts || []).map((craft, index) => [craft.id, index]))
  return categoryOrder.map(name => byCategory.get(name)!).map(({ key, category, crafts }) => ({
    key, category,
    crafts: [...crafts].sort((left, right) =>
      (craftRank.get(left.craft_id) ?? Number.MAX_SAFE_INTEGER) - (craftRank.get(right.craft_id) ?? Number.MAX_SAFE_INTEGER)
    ).map(craft => ({ ...craft, rows: groupOrderRowsBySubitem(craft.rows, byCraftID.get(craft.craft_id)?.subitems) })),
  }))
}

function materialView(material: SavedOrderMaterial, key: string, ratioSnapshotOnly = false): SavedWorkMaterial {
  // Sales retain the ratio but omit the auxiliary label. The canonical marker
  // activates that saved ratio; it is never shown or read from today's catalog.
  const auxiliaryUnit = ratioSnapshotOnly && material.conversion_ratio ? 'aux' : material.aux_unit
  const exactQuantity = material.qty_quantum && material.unit
    ? formatQuantum(material.qty_quantum, 'base', { unit: material.unit, aux_unit: auxiliaryUnit, aux_ratio: material.conversion_ratio || '1' }) + ' ' + material.unit
    : savedQuantityText(material.qty_micro, material.unit || '未记录单位')
  return {
    key, id: material.id || '', line_id: material.line_id || '', product_id: material.product_id, product_name: material.product_name || material.name || '',
    warehouse_name: material.warehouse_name || '',
    quantity_text: exactQuantity,
  }
}

function combinationMetadata(detail: SavedCombinationDetail | undefined, kind: 'waterproof' | 'polypropylene', lineID: string, orderID: string | undefined): SavedCombinationLine | undefined {
  const line = detail?.[kind]
  return line?.id === lineID && (!orderID || line.order_id === orderID) ? line : undefined
}

function combinationLine(order: SavedOrderRecord, relation: SavedOrderCombination, detail: SavedCombinationDetail | undefined, kind: 'waterproof' | 'polypropylene', anchors: ReadonlyMap<string, string>, allowed: boolean): SavedCombinationLineView {
  const lineID = kind === 'waterproof' ? relation.waterproof_line_id : relation.polypropylene_line_id
  const explicitOrderID = kind === 'waterproof' ? relation.waterproof_order_id : relation.polypropylene_order_id
  const line = combinationMetadata(detail, kind, lineID, explicitOrderID)
  // Without metadata, keep only explicit relationship IDs. Even the local order's
  // defaults/date must not be guessed as the remote side's historical snapshot.
  const orderID = line?.order_id || explicitOrderID || ''
  const anchor = orderID === order.id ? anchors.get(lineID) : undefined
  const unit = line ? unitFor(line.mode) : '㎡'
  return {
    kind, id: lineID, order_id: orderID, number: line?.number || '', work_date: line?.work_date || '',
    name: line?.name || '', category: line?.category || '', subitem_name: line?.subitem_name || '',
    actual_location: line?.actual_location || '', worker_name: line?.worker_name || '',
    quantity_text: savedQuantityText(line?.qty_micro, unit),
    waived_quantity_text: savedQuantityText(line?.waived_qty_micro, unit),
    chargeable_quantity_text: savedQuantityText(line?.chargeable_qty_micro, unit),
    has_details: !!line, ...(anchor ? { anchor } : {}),
    ...savedMoney(line?.price_cents, line?.total_cents, allowed),
  }
}

function combinationViews(order: SavedOrderRecord, rows: readonly SavedWorkRow[], allowed: boolean): SavedWorkCombination[] {
  const details = new Map((order.combination_details || []).map(detail => [detail.id, detail]))
  const anchors = new Map(rows.filter(row => row.id).map(row => [row.id, row.anchor]))
  return (order.combinations || []).map(relation => {
    const detail = relation.id ? details.get(relation.id) : undefined
    const waterproof = combinationLine(order, relation, detail, 'waterproof', anchors, allowed)
    const polypropylene = combinationLine(order, relation, detail, 'polypropylene', anchors, allowed)
    return {
      key: 'combination:' + (relation.id || JSON.stringify([relation.waterproof_line_id, relation.polypropylene_line_id])),
      id: relation.id || '', direction: relation.waive_direction,
      direction_text: relation.waive_direction === 'waterproof' ? '免收防水' : relation.waive_direction === 'polypropylene' ? '免收丙纶' : '未记录免收方向',
      waived_quantity_text: savedQuantityText(relation.waived_qty_micro, '㎡'),
      cross_date: !!waterproof.work_date && !!polypropylene.work_date && waterproof.work_date !== polypropylene.work_date,
      waterproof, polypropylene,
    }
  })
}

// A presentation projection only: no API, mutation, wage aggregation or fee calculation.
export function savedOrderWorkView(order: SavedOrderRecord, canCustomerPrices: boolean, configuration: SavedWorkOrderConfiguration = {}): SavedOrderWorkView {
  const rows = order.lines.map((line, index) => savedRow(order, line, index, canCustomerPrices, !!configuration.canWages))
  const combinations = combinationViews(order, rows, canCustomerPrices)
  return {
    id: order.id, categories: groupedRows(rows, configuration), combinations,
    combination_enabled: combinations.length > 0,
    sales: (order.sales || []).map((sale, index) => ({
      ...materialView(sale, 'sale:' + (sale.id || JSON.stringify([sale.warehouse_id, sale.product_id, index])), true),
      price_unit: sale.price_unit || '',
      sale_quantity_text: savedQuantityText(sale.sale_qty_micro, sale.price_unit || '未记录计价单位'),
      ...savedMoney(sale.price_cents, sale.total_cents, canCustomerPrices),
    })),
    materials: (order.materials || []).map((material, index) => materialView(material, 'material:' + JSON.stringify([material.warehouse_id, material.product_id, index]))),
    material_worker_name: order.worker_name || '', note: order.note || '',
    ...(canCustomerPrices && hasCustomerPrice(order.total_cents) ? { total_cents: order.total_cents } : {}),
    ...(configuration.canWages && order.wage_records ? { wage_records: order.wage_records } : {}),
    ...(configuration.canWages && order.wage_adjustments ? { wage_adjustments: order.wage_adjustments } : {}),
  }
}
