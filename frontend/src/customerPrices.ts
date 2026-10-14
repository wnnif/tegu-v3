import type { Numeric } from './workflows/models'

export const CUSTOMER_PRICE_HIDDEN = '无查看权限'

// Missing is not zero. The API deliberately omits these fields without permission.
export function hasCustomerPrice(value: unknown): value is Numeric {
  return (typeof value === 'number' || (typeof value === 'string' && value.trim() !== '')) && Number.isFinite(Number(value))
}

export function customerPrice(value: unknown, allowed = true): string {
  return allowed && hasCustomerPrice(value) ? (Number(value) / 100).toFixed(2) : CUSTOMER_PRICE_HIDDEN
}

export function customerMoney(value: unknown, allowed = true): string {
  return allowed && hasCustomerPrice(value) ? '¥ ' + (Number(value) / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : CUSTOMER_PRICE_HIDDEN
}

const customerFields = new Set(['price_cents', 'total_cents', 'amount_cents', 'received_cents', 'outstanding_cents', 'construction_cents', 'receivable_cents', 'sales_cents', 'sale_cents', 'undelivered_cents', 'delivered_outstanding_cents'])

// Only used for order drafts/payloads. Purchase cost and wage permissions are separate.
// Return a copy so removing protected amounts never destroys ordinary editor input.
export function withoutCustomerPrices<T>(value: T): T {
  if (Array.isArray(value)) return value.map(item => withoutCustomerPrices(item)) as T
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).filter(([key]) => !customerFields.has(key)).map(([key, item]) => [key, withoutCustomerPrices(item)])) as T
  }
  return value
}
