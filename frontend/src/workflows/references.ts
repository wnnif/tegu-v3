import { api } from '../api';
import type { References, Entity, CatalogRow, Balance, PageResult, Craft } from './models';
import type { Ref } from 'vue';
export async function balances(warehouse: string) {
    if (!warehouse)
        return [];
    const result: Balance[] = [];
    for (let p = 1;; p++) {
        const r = await api<PageResult<Balance>>('/inventory?' + new URLSearchParams({ warehouse_id: warehouse, show_zero: '0', page: String(p) }));
        result.push(...r.rows);
        if (result.length >= r.total || !r.rows.length)
            break;
    }
    return result;
}
export async function referenceById(entity: Entity, id: string): Promise<CatalogRow | null> {
    if (!id)
        return null;
    const result = await api<PageResult<CatalogRow>>('/reference-options?' + new URLSearchParams({ entity, id }));
    return result.rows[0] || null;
}
export function useReferences(references: Ref<References>) {
    function keepReference(entity: Exclude<Entity, 'crafts'>, item: CatalogRow | null) {
        if (!item)
            return;
        references.value[entity] = [...references.value[entity].filter(row => row.id !== item.id), item];
    }
    async function loadCompanyCrafts(companyId: string, projectId = '', current: () => boolean = () => true) {
        if (current()) references.value.crafts = [];
        if (!companyId)
            return;
        const result = await api<{
            crafts: Craft[];
        }>('/reference?' + new URLSearchParams({ tables: 'crafts', craft_scope: 'order', company_id: companyId, project_id: projectId }));
        if (current()) references.value.crafts = result.crafts;
    }
    return { keepReference, loadCompanyCrafts };
}
