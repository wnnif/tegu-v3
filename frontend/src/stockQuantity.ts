export interface UnitDefinition {
  unit: string
  aux_unit?: string
  aux_ratio?: string | number
  base_factor?: string | number
  aux_factor?: string | number
}

const SCALE = 1000000n

function gcd(left: bigint, right: bigint): bigint {
  while (right !== 0n) [left, right] = [right, left % right]
  return left
}

export function decimalScaled(value: string, places = 6): bigint | null {
  const match = new RegExp(`^(?:0|[1-9]\\d*)(?:\\.(\\d{1,${places}}))?$`).exec(value.trim())
  if (!match) return null
  const factor = 10n ** BigInt(places)
  return BigInt(value.trim().split('.')[0]) * factor + BigInt((match[1] || '').padEnd(places, '0'))
}

export function scaledDecimal(value: bigint, places = 6): string {
  const factor = 10n ** BigInt(places)
  const sign = value < 0n ? '-' : ''
  const absolute = value < 0n ? -value : value
  const fraction = String(absolute % factor).padStart(places, '0').replace(/0+$/, '')
  return sign + String(absolute / factor) + (fraction ? '.' + fraction : '')
}

export function unitFactors(product: UnitDefinition): {base: bigint; aux: bigint} | null {
  if (product.base_factor != null && product.aux_factor != null) {
    const base = BigInt(String(product.base_factor)), aux = BigInt(String(product.aux_factor))
    return base > 0n && aux > 0n ? {base, aux} : null
  }
  if (!product.aux_unit) return {base: 1n, aux: 1n}
  const ratio = decimalScaled(String(product.aux_ratio ?? '1'))
  if (ratio == null || ratio <= 0n) return null
  const divisor = gcd(ratio, SCALE)
  return {base: ratio / divisor, aux: SCALE / divisor}
}

export function toQuantum(input: string, inputUnit: string, product: UnitDefinition): string | null {
  const amount = decimalScaled(input)
  const factors = unitFactors(product)
  if (amount == null || !factors || amount <= 0n) return null
  const factor = inputUnit === 'base' || inputUnit === product.unit ? factors.base : (inputUnit === 'aux' || inputUnit === product.aux_unit) && product.aux_unit ? factors.aux : null
  return factor == null ? null : String(amount * factor)
}

export function fromQuantum(quantum: string, unit: string, product: UnitDefinition): string | null {
  if (!/^\d+$/.test(quantum)) return null
  const factors = unitFactors(product)
  if (!factors) return null
  const factor = unit === 'base' || unit === product.unit ? factors.base : (unit === 'aux' || unit === product.aux_unit) && product.aux_unit ? factors.aux : null
  if (factor == null || BigInt(quantum) % factor !== 0n) return null
  return scaledDecimal(BigInt(quantum) / factor)
}

export function restoreStockInput(quantum: string, savedUnit: string, savedInput: string, product: UnitDefinition): {unit: 'base' | 'aux'; quantity: string} | null {
  const unit = savedUnit === 'aux' || !!product.aux_unit && savedUnit === product.aux_unit ? 'aux' : 'base'
  if (savedInput && toQuantum(savedInput, unit, product) === quantum) return {unit, quantity: savedInput}
  const exact = fromQuantum(quantum, unit, product)
  if (exact !== null) return {unit, quantity: exact}
  const other = unit === 'base' ? 'aux' : 'base'
  const fallback = fromQuantum(quantum, other, product)
  return fallback === null ? null : {unit: other, quantity: fallback}
}

export function formatQuantum(quantum: string, unit: string, product: UnitDefinition): string {
  const exact = fromQuantum(quantum, unit, product)
  if (exact != null) return exact
  const factors = unitFactors(product)
  if (!factors || !/^\d+$/.test(quantum)) return '—'
  const factor = unit === 'base' || unit === product.unit ? factors.base : factors.aux
  const numerator = BigInt(quantum), denominator = SCALE * factor
  const divisor = gcd(numerator, denominator)
  return `${numerator / divisor}/${denominator / divisor}`
}

export function formatSignedQuantum(quantum: string, unit: string, product: UnitDefinition): string {
  if (!/^-?\d+$/.test(quantum)) return '—'
  const signed = BigInt(quantum)
  const formatted = formatQuantum(String(signed < 0n ? -signed : signed), unit, product)
  return formatted === '—' ? formatted : (signed > 0n ? '+' : signed < 0n ? '-' : '') + formatted
}

export function moneyCents(input: string): string | null {
  const cents = decimalScaled(input, 2)
  return cents == null ? null : String(cents)
}

export function moneyFromCents(cents: string | number | undefined): string {
  if (!/^-?\d+$/.test(String(cents ?? '0'))) return '0.00'
  const amount = BigInt(String(cents ?? '0'))
  const sign = amount < 0n ? '-' : ''
  const absolute = amount < 0n ? -amount : amount
  return `${sign}${absolute / 100n}.${String(absolute % 100n).padStart(2, '0')}`
}
