// A display-only projection per craft. Stable IDs group configured subitems;
// legacy rows never merge or lose their individual dimensions, workers or keys.
// When configuration is available, only group order follows its explicit IDs.
export function groupOrderRowsBySubitem<T extends { readonly subitem_id?: string }>(
  rows: readonly T[], configuredSubitems: readonly { readonly id: string }[] = [],
): T[] {
  const groups: { id: string; rows: T[]; index: number }[] = []
  const bySubitem = new Map<string, typeof groups[number]>()
  for (const row of rows) {
    const id = row.subitem_id || ''
    let group = id ? bySubitem.get(id) : undefined
    if (!group) {
      group = { id, rows: [], index: groups.length }
      groups.push(group)
      if (id) bySubitem.set(id, group)
    }
    group.rows.push(row)
  }
  const ranks = new Map<string, number>()
  configuredSubitems.forEach(item => { if (item.id && !ranks.has(item.id)) ranks.set(item.id, ranks.size) })
  if (ranks.size) groups.sort((a, b) =>
    (ranks.get(a.id) ?? ranks.size) - (ranks.get(b.id) ?? ranks.size) || a.index - b.index)
  return groups.flatMap(group => group.rows)
}
