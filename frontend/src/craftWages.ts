import type { Craft, CraftSubitem, Numeric } from './workflows/models'

export function effectiveCatalogWage(craft: Pick<Craft, 'wage_cents' | 'wage_override_cents' | 'wage_configured'>, subitem?: Pick<CraftSubitem, 'wage_cents'>): number | undefined {
  const value: Numeric | null | undefined = craft.wage_override_cents ?? subitem?.wage_cents ?? craft.wage_cents
  if (value == null) return craft.wage_configured === false ? undefined : 0
  if (subitem?.wage_cents == null && craft.wage_override_cents == null && craft.wage_configured === false) return undefined
  return Number(value)
}
