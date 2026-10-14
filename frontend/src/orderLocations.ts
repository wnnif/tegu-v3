import type { OrderLine } from './workflows/models'

type LocationIdentity = Pick<OrderLine, 'craft_id' | 'category' | 'name' | 'spec' | 'subitem_id' | 'subitem_name' | 'mode' | 'price_cents' | 'wage_cents' | 'temporary' | 'editable' | 'included_by_craft_id' | 'use_included_price'>
let nextClientKey = 0

// A configured subitem can have several rows; its ID is not a unique Vue key.
export function orderLineKey(line: OrderLine): string {
  return line.client_key ||= 'order-line-' + ++nextClientKey
}

export function createOrderLocation(identity: LocationIdentity, sortOrder: number): OrderLine {
  const line: OrderLine = {
    id: '', craft_id: identity.craft_id, category: identity.category, name: identity.name, spec: identity.spec,
    subitem_id: identity.subitem_id || '', subitem_name: identity.subitem_name || '',
    region: '', actual_location: '', sort_order: sortOrder, description: '', mode: identity.mode,
    ...(identity.price_cents === undefined ? {} : { price_cents: Number(identity.price_cents) }), wage_cents: Number(identity.wage_cents || 0),
    included_by_craft_id: identity.included_by_craft_id || '', use_included_price: !!identity.use_included_price,
    temporary: identity.temporary, editable: identity.editable, entered: false,
    // A new location follows the category default, not another location's explicit person.
    worker_id: '', worker_name: '', worker_override: false,
    segments: [{ mode: identity.mode, surface: '地面', length: '', width: '', height: '', count: '', amount_cents: 0 }]
  }
  orderLineKey(line)
  return line
}

// A configured location can have repeated independent actual construction rows.
export function canAddOrderLocation(_line: Pick<OrderLine, 'subitem_id'>): boolean {
  return true
}

export function canRemoveOrderLocation(lines: readonly OrderLine[], line: OrderLine): boolean {
  // Keep one empty/fillable row for every configured item; temporary items may be removed entirely.
  return lines.includes(line) && (line.temporary || lines.some(other => other !== line && !other.temporary &&
    other.craft_id === line.craft_id && (other.subitem_id || '') === (line.subitem_id || '')))
}
