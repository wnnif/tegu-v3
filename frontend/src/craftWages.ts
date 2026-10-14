import type { Craft, CraftSubitem, Numeric } from './workflows/models'

export function effectiveCatalogWage(craft: Pick<Craft, 'wage_cents' | 'wage_override_cents'>, subitem?: Pick<CraftSubitem, 'wage_cents'>): number {
  const value: Numeric | null | undefined = craft.wage_override_cents ?? subitem?.wage_cents ?? craft.wage_cents
  return value == null ? 0 : Number(value)
}
