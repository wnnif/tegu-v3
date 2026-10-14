type Worker = {id: string; crafts?: string[]; active?: number | boolean | string}
type EditableLine = { id?: string; craft_id: string; category: string; temporary: boolean; editable?: boolean }
type EditableCraft = { id: string; company_id: string; editable?: boolean }

export function lineAccountCanEdit(line: EditableLine, company: string, crafts: EditableCraft[], canTemporary: boolean): boolean {
  if (line.editable === false || !company) return false
  if (line.temporary) return canTemporary
  const craft = crafts.find(item => item.id === line.craft_id && item.company_id === company)
  if (line.id && line.editable === true) return !crafts.some(item => item.id === line.craft_id && item.company_id !== company)
  return !!craft && craft.editable === true
}

export function lineCanEdit(line: EditableLine, company: string, crafts: EditableCraft[], worker: Worker | undefined, canTemporary: boolean): boolean {
  return lineAccountCanEdit(line, company, crafts, canTemporary) && !!worker &&
    (worker.active === undefined || !!Number(worker.active)) && (worker.crafts || []).includes(line.category)
}

export function companyCategories(company: string, loginCrafts: string[]): string[] {
  return company ? [...new Set(loginCrafts)] : []
}

export function matchingWorkers<T extends Worker>(company: string, workers: T[], category: string, loginCrafts: string[]): T[] {
  if (!company || !category || !loginCrafts.includes(category)) return []
  return workers.filter(w => (w.active === undefined || !!Number(w.active)) && (w.crafts || []).includes(category))
}

export function craftScope(loginCrafts: string[], worker?: Worker): string[] {
  if (!worker) return loginCrafts
  if (worker.active !== undefined && !Number(worker.active)) return []
  return loginCrafts.filter(c => (worker.crafts || []).includes(c))
}
