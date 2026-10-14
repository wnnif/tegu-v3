// These are exact category identities, not craft names or substring matches.
export const DEFAULT_COMPANY_CATEGORY_ORDER: readonly string[] = [
  '开工', '包管', '安全防护架', '回填', '防水涂料', '丙纶',
]

// A display projection only: never reorder configuration, craft IDs or snapshots.
// Manual order wins; newly available identities append in their original order.
export function orderedCompanyCategories(
  available: readonly string[],
  configured: readonly string[] = [],
): string[] {
  const present = new Set(available)
  const seen = new Set<string>()
  const ordered: string[] = []
  const preferred = configured.length ? configured : DEFAULT_COMPANY_CATEGORY_ORDER
  for (const category of [...preferred, ...available]) {
    if (present.has(category) && !seen.has(category)) {
      seen.add(category)
      ordered.push(category)
    }
  }
  return ordered
}
