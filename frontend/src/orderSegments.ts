export function scaledQuantity(value: string | number): bigint {
  const text=String(value||0)
  const parsed=text.match(/^(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d{1,3}))?$/)
  if(!parsed||text.length>100)return 0n
  const exponent=Number(parsed[3]||0),fraction=parsed[2]||''
  if(exponent>12||exponent<-20)return 0n
  const valueInteger=BigInt(parsed[1]!+fraction),power=6-fraction.length+exponent
  if(power>=0)return valueInteger*10n**BigInt(power)
  const divisor=10n**BigInt(-power)
  return (valueInteger+divisor/2n)/divisor
}
export function saleAmountCents(quantity:string|number,price:string|number):number {
  const priceText=String(price||0)
  if(!/^\d+$/.test(priceText))return 0
  return Number((scaledQuantity(quantity)*BigInt(priceText)+500000n)/1000000n)
}
export interface OrderSegmentShape {
  mode?: string
  length?: string | number
  width?: string | number
  height?: string | number
  count?: string | number
  amount_cents?: string | number
}
export interface OrderLineShape {
  mode?: string
  entered?: boolean
  segments?: OrderSegmentShape[]
  price_cents?: string | number
  use_included_price?: boolean
  charge_mode?: 'normal' | 'free' | 'redo'
  waived_qty_micro?: string | number
}
const countCraftModes = new Set(['个', '根', '处', '孔位', '副', '张', '套'])
export const isCountCraftMode = (mode?: string) => countCraftModes.has(mode || '')
export const measureUnit = (mode?: string) => mode === '米' ? '米' : mode === '立方' ? 'm³' : isCountCraftMode(mode) ? mode! : mode === '金额' ? '项' : '㎡'
export function segmentFilled(s: OrderSegmentShape): boolean {
  if (s.mode === '米') return s.length !== '' && s.length != null && Number(s.length) !== 0
  if (isCountCraftMode(s.mode)) return s.count !== '' && s.count != null && Number.isInteger(Number(s.count)) && Number(s.count) > 0
  if (s.mode === '金额') return Number(s.amount_cents || 0) > 0
  return [s.length, s.width, s.height].some(value => value !== '' && value != null)
}
export function filled(l: OrderLineShape): boolean {
  return l.mode === '金额' ? !!l.entered : !!l.segments?.some(s => segmentFilled({...s, mode: s.mode || l.mode}))
}
export function lineQuantity(l: OrderLineShape): number {
  return Number(lineQuantityMicro(l)) / 1e6
}
interface DecimalQuantity { integer: bigint; places: number }
function dimension(value: string | number | undefined): DecimalQuantity {
  // SaveOrder converts dimensions to numbers; use that same shortest decimal,
  // then multiply/add as integers rather than multiplying binary floats.
  const number = Number(value || 0)
  if (!Number.isFinite(number) || number <= 0) return {integer: 0n, places: 0}
  const parts = String(number).match(/^(\d+)(?:\.(\d+))?(?:e([+-]?\d+))?$/i)!
  const fraction = parts[2] || '', places = fraction.length - Number(parts[3] || 0)
  const integer = BigInt(parts[1]! + fraction)
  return places < 0 ? {integer: integer * 10n ** BigInt(-places), places: 0} : {integer, places}
}
function multiply(a: DecimalQuantity, b: DecimalQuantity): DecimalQuantity {
  return {integer: a.integer * b.integer, places: a.places + b.places}
}
function add(a: DecimalQuantity, b: DecimalQuantity): DecimalQuantity {
  const places = Math.max(a.places, b.places)
  return {integer: a.integer * 10n ** BigInt(places - a.places) + b.integer * 10n ** BigInt(places - b.places), places}
}
export function lineQuantityMicro(l: OrderLineShape): bigint {
  if (!filled(l)) return 0n
  if (l.mode === '金额') return 1000000n
  const quantity = (l.segments || []).reduce((sum, s) => {
    const mode = s.mode || l.mode
    const measured = mode === '米' ? dimension(s.length) : isCountCraftMode(mode) ? dimension(s.count) : mode === '金额' ? {integer: 0n, places: 0} : multiply(dimension(s.length), dimension(s.width))
    return add(sum, mode === '立方' ? multiply(measured, dimension(s.height)) : measured)
  }, {integer: 0n, places: 0})
  if (quantity.places <= 6) return quantity.integer * 10n ** BigInt(6 - quantity.places)
  const divisor = 10n ** BigInt(quantity.places - 6)
  return (quantity.integer + divisor / 2n) / divisor
}
export function lineAmount(l: OrderLineShape): number {
  if (l.use_included_price || l.charge_mode === 'free' || l.charge_mode === 'redo' || !filled(l)) return 0
  const price = String(l.price_cents || 0)
  if (!/^\d+$/.test(price)) return 0
  const actual=lineQuantityMicro(l),waived=BigInt(String(l.waived_qty_micro||0))
  const billable=actual>waived?actual-waived:0n
  return Number((billable * BigInt(price) + 500000n) / 1000000n)
}
export function quantityLabel(l: OrderLineShape): string {
  const micro = lineQuantityMicro(l), fraction = String(micro % 1000000n).padStart(6, '0').replace(/0+$/, '')
  return `${micro / 1000000n}${fraction ? '.' + fraction : ''} ${measureUnit(l.mode)}`
}
