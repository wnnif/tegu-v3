import { filled, lineQuantityMicro, saleAmountCents, scaledQuantity } from './orderSegments'
import { orderLineKey } from './orderLocations'
import type { OrderDraft, OrderLine, Numeric } from './workflows/models'

export type CombinationKind = 'waterproof' | 'polypropylene'
export interface ProjectCombination {
  id?: string
  version?: number
  waterproof_line_id: string
  polypropylene_line_id: string
  waive_direction: CombinationKind | ''
  waived_qty_micro: number
  waterproof_order_id?: string
  polypropylene_order_id?: string
  waterproof_order_version: number
  polypropylene_order_version: number
  input_area?: Numeric // Editor-only; the API uses integer micro-square-metres.
  ui_key?: string
}
export interface CombinationCandidate {
  id: string
  order_id: string
  order_version: number
  number: string
  work_date: string
  craft_id: string
  kind: CombinationKind
  category: string
  name: string
  subitem_name: string
  actual_location: string
  region: string
  worker_id: string
  worker_name: string
  qty_micro: number
  waived_qty_micro: number
  available_qty_micro: number
  price_cents?: Numeric
  total_cents?: Numeric
  editable: boolean
  blocked_reason: string
}
export interface CombinationPage {
  rows: CombinationCandidate[]
  total: number
  page: number
  page_size: number
  project_id: string
  combination_version: number
}

export function combinationKind(line: OrderLine): CombinationKind | '' {
  if (line.temporary || line.use_included_price || line.mode !== '平方' || !line.craft_id || !filled(line)) return ''
  return line.category === '丙纶' ? 'polypropylene' : ['防水涂料', '室外防水'].includes(line.category) ? 'waterproof' : ''
}
export function combinationLineRef(line: OrderLine): string {
  return line.id || orderLineKey(line)
}
export function combinationTarget(combo: ProjectCombination): string {
  return combo.waive_direction === 'waterproof' ? combo.waterproof_line_id : combo.waive_direction === 'polypropylene' ? combo.polypropylene_line_id : ''
}
export function quantityDecimal(micro: number): string {
  const value = BigInt(Math.max(0, Math.trunc(micro)))
  const fraction = String(value % 1000000n).padStart(6, '0').replace(/0+$/, '')
  return `${value / 1000000n}${fraction ? '.' + fraction : ''}`
}
export function combinationAreaMicro(combo: ProjectCombination): number | null {
  if (combo.input_area !== undefined) {
    const text = String(combo.input_area).trim()
    if (!/^\d+(?:\.\d{1,6})?$/.test(text)) return null
    const micro = scaledQuantity(text)
    return micro > 0n && micro <= 1000000000000n ? Number(micro) : null
  }
  const micro = Number(combo.waived_qty_micro)
  return Number.isSafeInteger(micro) && micro > 0 && micro <= 1000000000000 ? micro : null
}
function selectedWaivers(combos: readonly ProjectCombination[]): Map<string, number> {
  const totals = new Map<string, number>()
  for (const combo of combos) {
    const target = combinationTarget(combo), amount = combinationAreaMicro(combo)
    if (target && amount !== null) totals.set(target, (totals.get(target) || 0) + amount)
  }
  return totals
}
// Keep reservations from other orders. Replacing this order's full list removes
// only its previous contributions, not someone else's cross-date allocation.
export function combinationWaivers(original: readonly ProjectCombination[], desired: readonly ProjectCombination[], candidates: readonly CombinationCandidate[]): Map<string, number> {
  const previous = selectedWaivers(original), next = selectedWaivers(desired)
  return new Map(candidates.map(candidate => [candidate.id,
    Math.max(0, Number(candidate.waived_qty_micro) - (previous.get(candidate.id) || 0)) + (next.get(candidate.id) || 0)]))
}
export function localCombinationCandidates(draft: OrderDraft, savedWaivers: ReadonlyMap<string, number>, workerName: (id: string) => string): CombinationCandidate[] {
  return draft.lines.flatMap(line => {
    const kind = combinationKind(line), qty = Number(lineQuantityMicro(line))
    if (!kind || qty <= 0) return []
    const ref = combinationLineRef(line), worker = line.worker_id || draft.category_workers?.[line.category] || draft.worker_id || ''
    const waived = savedWaivers.get(ref) || 0
    return [{ id: ref, order_id: draft.id, order_version: draft.version, number: draft.number || '本次未保存施工', work_date: draft.work_date,
      craft_id: line.craft_id, kind, category: line.category, name: line.name, subitem_name: line.subitem_name || '',
      actual_location: line.actual_location || '', region: line.region || '', worker_id: worker, worker_name: line.worker_name || workerName(worker),
      qty_micro: qty, waived_qty_micro: waived, available_qty_micro: qty - waived,
      ...(line.price_cents === undefined ? {} : { price_cents: line.price_cents }), editable: line.editable !== false,
      blocked_reason: line.editable === false ? '没有此固定工艺的填写权限' : '' }]
  })
}
export function combinationValidation(combos: readonly ProjectCombination[], original: readonly ProjectCombination[], candidates: readonly CombinationCandidate[], currentRefs: ReadonlySet<string>, enabled: boolean): string {
  if (enabled && !combos.length) return '请添加并明确选择一组防水、丙纶实际施工明细；不需要免收时关闭组合'
  if (combos.length > 200) return '一次最多设置200组组合'
  const byId = new Map(candidates.map(candidate => [candidate.id, candidate])), pairs = new Set<string>()
  for (const combo of combos) {
    const w = byId.get(combo.waterproof_line_id), p = byId.get(combo.polypropylene_line_id)
    if (!w || !p || w.kind !== 'waterproof' || p.kind !== 'polypropylene') return '请选择明确的平方米防水、丙纶实际施工明细，不能使用未填写或旧含价行'
    if (!currentRefs.has(w.id) && !currentRefs.has(p.id)) return '每组至少要有一条本次施工明细，不能改变无关记录'
    if (!w.editable || !p.editable) return w.blocked_reason || p.blocked_reason || '关联施工明细不能直接修改组合'
    const pair = w.id + ':' + p.id
    if (pairs.has(pair)) return '同一对实际施工明细不能重复设置免收'
    pairs.add(pair)
    if (!['waterproof', 'polypropylene'].includes(combo.waive_direction)) return '请明确选择免收防水或丙纶'
    if (combinationAreaMicro(combo) === null) return '免收面积须手动填写，必须大于0，最多6位小数，且不超过1000000㎡'
  }
  const totals = combinationWaivers(original, combos, candidates)
  for (const candidate of candidates) {
    if ((totals.get(candidate.id) || 0) > candidate.qty_micro) return '免收面积超过所选施工明细的剩余可用面积，不能重复免收同一面积'
  }
  return ''
}
export function serializeCombinations(combos: readonly ProjectCombination[]): ProjectCombination[] {
  return combos.map(combo => {
    const qty = combinationAreaMicro(combo)
    if (!combo.waterproof_line_id || !combo.polypropylene_line_id || !combo.waive_direction || qty === null) throw new Error('请核对组合明细、免收方向和手动填写的面积')
    return { ...(combo.id ? { id: combo.id } : {}), ...(combo.version ? { version: combo.version } : {}),
      waterproof_line_id: combo.waterproof_line_id, polypropylene_line_id: combo.polypropylene_line_id,
      waive_direction: combo.waive_direction, waived_qty_micro: qty,
      waterproof_order_version: combo.waterproof_order_version, polypropylene_order_version: combo.polypropylene_order_version }
  })
}
export function combinationCharge(candidate: CombinationCandidate, waived: number): number | undefined {
  return candidate.price_cents === undefined ? undefined : saleAmountCents(quantityDecimal(Math.max(0, candidate.qty_micro - waived)), candidate.price_cents)
}
