import { ref, computed } from 'vue';
import { api } from '../api';
import { withoutCustomerPrices } from '../customerPrices';
import { orderedCompanyCategories } from '../companyCraftOrder';
import { clone, type CatalogRow, type CatalogDraft, type Craft, type Entity, type UserRow, type UserDraft, type PageResult, type References, type WorkflowContext } from './models';
export function useCatalog(ctx: WorkflowContext, filter: {
    search: import('vue').Ref<string>;
    catalogStatus: import('vue').Ref<string>;
    pageNo: import('vue').Ref<number>;
    reload: () => void;
}) {
    const { page, categories, category, references, dialog, perform, submit, load, notice } = ctx;
    const { search, catalogStatus, pageNo, reload } = filter;
    const draft = ref<CatalogDraft>({ entity: 'companies', id: '', name: '', version: 0, crafts: [], fixed_crafts: [], subitems: [], ticket_mode: 'single', craft_category_order: [], active: true, worker_id: '', company_id: '', category: '', mode: '平方', price_cents: 0, wage_cents: 0, unit: '桶', aux_unit: '组', aux_ratio: 1 });
    const userDraft = ref<UserDraft>({ id: '', version: 0, name: '', username: '', password: '', role: 'employee', permissions: [], crafts: [], active: true });
    const deselectedCompanyCrafts = new Map<string, { craft: Craft; index: number }>();
    const craftCategory = ref('全部'), craftQuery = ref(''), companyCraftPage = ref(1), companyCraftTotal = ref(0);
    async function loadCompanyCraftOptions(nextPage = 1) {
        const query = new URLSearchParams({ entity: 'crafts', active: '1', q: craftQuery.value, page: String(nextPage) });
        if (craftCategory.value !== '全部')
            query.set('category', craftCategory.value);
        const result = await api<PageResult<Craft>>('/reference-options?' + query);
        references.value.crafts = [...references.value.crafts.filter((c: Craft) => !!c.company_id), ...result.rows];
        companyCraftPage.value = nextPage;
        companyCraftTotal.value = result.total;
    }
    async function searchCompanyCrafts(query: string) {
        craftQuery.value = query.trim();
        await loadCompanyCraftOptions();
    }
    async function changeCompanyCraftCategory(value: string) {
        craftCategory.value = value;
        await loadCompanyCraftOptions();
    }
    function editEntity(row?: CatalogRow, entity: Entity = page.value as Entity) {
        void perform(async () => {
            deselectedCompanyCrafts.clear();
            if (entity === 'companies') {
                references.value.crafts = [];
                craftCategory.value = '全部';
                craftQuery.value = '';
                companyCraftPage.value = 1;
                if (row?.id) {
                    const params = new URLSearchParams({ tables: 'crafts', craft_scope: 'order', company_id: row.id });
                    const next = await api<References>('/reference?' + params);
                    references.value.crafts = next.crafts;
                }
            }
            draft.value = { entity, fixed_crafts: [], crafts: [], subitems: [], ticket_mode: 'single', name: '', id: '', version: 0, contact: '', phone: '', customer_type: '装修公司', active: true, worker_id: '', brand: '', spec: '', note: '', warehouse_type: '普通仓库', color: '', weight_kg: '', unit: '桶', aux_unit: '组', aux_ratio: 1, company_id: '', category: entity === 'warehouses' ? '默认分类' : category.value === '全部' ? categories.value[0] : category.value, mode: '平方', price_cents: 0, wage_cents: 0, ...clone(row || {}) };
            draft.value.active = !!Number(draft.value.active);
            draft.value.worker_id = draft.value.worker_id || '';
            if (entity === 'crafts') {
                draft.value.price_cents = 0;
                draft.value.subitems = clone(row?.subitems || []);
            }
            if (entity === 'companies') {
                draft.value.craft_category_order = clone(row?.craft_category_order || []);
                draft.value.fixed_crafts = clone(references.value.crafts.filter((c: CatalogRow) => c.company_id === draft.value.id && !!draft.value.id && Number(c.active))).map(c => ({...c, ticket_slot: c.ticket_slot || 1, included_by_craft_id: c.included_by_craft_id || ''}));
                craftCategory.value = '全部';
            }
            if (entity === 'companies')
                await loadCompanyCraftOptions();
            dialog.value = 'entity';
        });
    }
    function filterCatalog(query: string, active = catalogStatus.value) {
        search.value = query.trim();
        catalogStatus.value = active;
        pageNo.value = 1;
        reload();
    }
    function catalogPage(next: number) {
        pageNo.value = next;
        reload();
    }
    async function toggleCatalog(row: CatalogRow) {
        await perform(async () => {
            await submit('/entity', { ...row, entity: page.value, active: !Number(row.active), aux_ratio: String(row.aux_ratio || '1') });
            await load();
            notice.value = '状态已更新';
        });
    }
    async function saveEntity() {
        await perform(async () => {
            if (draft.value.entity === 'companies' && !ctx.can('customer_prices') && draft.value.fixed_crafts.some(c => !c.id))
                throw new Error('新增公司收费工艺需要“查看客户价格”权限');
            const payload = draft.value.entity === 'companies' && !ctx.can('customer_prices') ? withoutCustomerPrices(draft.value) : draft.value;
            await submit('/entity', payload);
            dialog.value = '';
            notice.value = '资料已保存';
            await load();
        });
    }
    const companyCraftOptions = computed(() => references.value.crafts.filter((c: CatalogRow) => !c.company_id && Number(c.active) && (craftCategory.value === '全部' || c.category === craftCategory.value)));
    const companyFixedVisible = computed(() => {
        const visible = (draft.value.fixed_crafts || []).filter(c => craftCategory.value === '全部' || c.category === craftCategory.value);
        return orderedCompanyCategories(visible.map(c => c.category), draft.value.craft_category_order)
            .flatMap(category => visible.filter(c => c.category === category));
    });
    function hasCompanyCraft(c: Craft) {
        return draft.value.fixed_crafts.some((x: Craft) => x.source_id === c.id);
    }
    function toggleCompanyCraft(c: Craft, checked: boolean) {
        if (!ctx.can('customer_prices')) {
            if (checked && !hasCompanyCraft(c)) { ctx.error.value = '新增公司收费工艺需要“查看客户价格”权限'; return; }
            if (!checked && draft.value.fixed_crafts.some(item => item.source_id === c.id && draft.value.fixed_crafts.some(child => child.included_by_craft_id === (item.id || 'source:' + item.source_id)))) { ctx.error.value = '调整含价组合需要“查看客户价格”权限'; return; }
        }
        if (checked) {
            if (!hasCompanyCraft(c)) {
                const deselected = deselectedCompanyCrafts.get(c.id);
                const previous = deselected?.craft || references.value.crafts.find(item => item.company_id === draft.value.id && !!draft.value.id && item.source_id === c.id);
                const item = previous ? clone(previous) : { ...clone(c), id: '', source_id: c.id, version: 0, price_cents: 0, ticket_slot: 1, included_by_craft_id: '', source_missing: false };
                const selected = { ...item, company_id: draft.value.id, active: true, name: c.name, category: c.category, mode: c.mode, spec: c.spec, wage_cents: c.wage_cents, subitems: clone(c.subitems || []), source_missing: false, source_active: !!Number(c.active) };
                const restoreIndex = deselected ? deselected.index - [...deselectedCompanyCrafts.values()].filter(item => item.index < deselected.index).length : draft.value.fixed_crafts.length;
                draft.value.fixed_crafts.splice(Math.min(restoreIndex, draft.value.fixed_crafts.length), 0, selected);
                deselectedCompanyCrafts.delete(c.id);
            }
        }
        else {
            for (const item of draft.value.fixed_crafts.filter(x => x.source_id === c.id)) removeCompanyCraft(item);
        }
    }
    function removeCompanyCraft(craft: Craft) {
        const index = draft.value.fixed_crafts.indexOf(craft);
        if (index < 0) return;
        const key = craft.id || 'source:' + craft.source_id;
        if (!ctx.can('customer_prices') && draft.value.fixed_crafts.some(item => item.included_by_craft_id === key)) {
            ctx.error.value = '调整含价组合需要“查看客户价格”权限';
            return;
        }
        // Preserve positions across several removals, including re-selection in
        // a different order. Missing items still occupy their original slot.
        let originalIndex = index;
        for (const removed of [...deselectedCompanyCrafts.values()].sort((a, b) => a.index - b.index)) if (removed.index <= originalIndex) originalIndex++;
        if (craft.source_id) deselectedCompanyCrafts.set(craft.source_id, { craft: clone(craft), index: originalIndex });
        draft.value.fixed_crafts = draft.value.fixed_crafts.filter(item => item !== craft);
        for (const item of draft.value.fixed_crafts) if (item.included_by_craft_id === key) item.included_by_craft_id = '';
    }
    function selectCompanyCategory() {
        if (!ctx.can('customer_prices')) { ctx.error.value = '新增公司收费工艺需要“查看客户价格”权限'; return; }
        for (const c of companyCraftOptions.value)
            toggleCompanyCraft(c, true);
    }
    function editUser(row?: UserRow) {
        userDraft.value = { id: '', version: 0, name: '', username: '', password: '', role: 'employee', permissions: [], crafts: [], active: true, ...clone(row || {}) };
        userDraft.value.active = !!Number(userDraft.value.active);
        // Categories are worker skills, never public craft account grants.
        userDraft.value.crafts = userDraft.value.crafts.filter(c => !categories.value.includes(c));
        dialog.value = 'user';
    }
    async function saveUser() {
        await perform(async () => {
            await submit('/user', userDraft.value);
            dialog.value = '';
            await load();
            notice.value = '账号已保存，权限将在下一次操作或页面重新核验时更新';
        });
    }
    async function deleteUser(row: UserRow) {
        if (!confirm('删除账号 ' + row.name + '？历史业务和日志会保留。'))
            return;
        await perform(async () => {
            await submit('/user', { ...row, delete: true });
            await load();
        });
    }
    function reset() {
        deselectedCompanyCrafts.clear();
        draft.value.fixed_crafts = [];
        draft.value.craft_category_order = [];
        userDraft.value.password = '';
    }
    return { catalogDraft: draft, userDraft, craftCategory, companyCraftPage, companyCraftTotal, loadCompanyCraftOptions, searchCompanyCrafts, changeCompanyCraftCategory, editEntity, filterCatalog, catalogPage, toggleCatalog, saveEntity, companyCraftOptions, companyFixedVisible, hasCompanyCraft, toggleCompanyCraft, removeCompanyCraft, selectCompanyCategory, editUser, saveUser, deleteUser, resetCatalog: reset };
}
