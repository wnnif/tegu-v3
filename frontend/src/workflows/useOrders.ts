import { ref, computed } from 'vue';
import { api, apiSession } from '../api';
import { hasCustomerPrice, withoutCustomerPrices } from '../customerPrices';
import { effectiveCatalogWage } from '../craftWages';
import { filled, segmentFilled } from '../orderSegments';
import { lineAccountCanEdit, lineCanEdit } from '../orderAccess';
import { canAddOrderLocation, canRemoveOrderLocation, createOrderLocation, orderLineKey } from '../orderLocations';
import { combinationLineRef, serializeCombinations } from '../orderCombinations';
import { toQuantum } from '../stockQuantity';
import { assignLineWorker, lineWorkerId, needsMaterialWorker, orderWorkerIds } from '../orderWorkers';
import type { Sale } from '../projectWork';
import type { CompanyPackageOption, ProjectList, ProjectRecord } from '../projects';
import { referenceById, balances, useReferences } from './references';
import type { SavedOrderRecord } from './readModels';
import { clone, type OrderDraft, type OrderLine, type OrderSegment, type Material, type Craft, type CatalogRow, type PageResult, type WorkflowContext } from './models';
export function useOrders(ctx: WorkflowContext, refreshProjects: (fresh?: boolean) => Promise<void | undefined>) {
    const { perform, references, inventory, categories, company, dialog, submit, load, notice, error, can } = ctx;
    const { keepReference, loadCompanyCrafts } = useReferences(references);
    const draft = ref<OrderDraft>({ id: '', version: 0, number: '', company_id: '', worker_id: '', project_id: '', address: '', work_date: '', status: '进行中', lines: [], category_workers: {}, materials: [] });
    const projectOptions = ref<ProjectRecord[]>([]), projectPage = ref(1), projectTotal = ref(0), projectQuery = ref('');
    const companyPackages = ref<CompanyPackageOption[]>([]);
    let selectionGeneration = 0, projectRequest = 0, workerRequest = 0;
    function selectionCurrent() {
        const selected = draft.value, companyId = selected.company_id, projectId = selected.project_id, generation = selectionGeneration;
        return () => draft.value === selected && selected.company_id === companyId && selected.project_id === projectId && generation === selectionGeneration;
    }
    function resetTicketSelection(o: OrderDraft) {
        o.ticket_action = 'normal';
        o.ticket_id = '';
        o.ticket_version = 0;
        o.sales_ticket_slot = 1;
        o.combinations = [];
        o.combination_version = 0;
        o.combination_dirty = false;
        o.combination_enabled = false;
        o.combination_error = '';
    }
    const selectedOrderCategory = ref('');
    const companyCraftCategories = computed(() => [...new Set(references.value.crafts.filter(c => c.company_id === draft.value.company_id).map(c => c.category))]);
    const workerOptions = computed<CatalogRow[]>(() => references.value.workers.filter(w => w.active === undefined || !!Number(w.active)));
    const allowedCrafts = computed<string[]>(() => {
        if (!draft.value.company_id)
            return [];
        return categories.value;
    });
    const nextLineSortOrder = () => draft.value.lines.reduce((sort, line) => Math.max(sort, line.sort_order ?? -1), draft.value.lines.length - 1) + 1;
    function lineFrom(c: Partial<Craft>, temporary = false, subitem?: {id:string;name:string;wage_cents?:number|string|null}): OrderLine {
        return createOrderLocation({ craft_id: temporary ? '' : c.id || '', category: c.category || '', name: temporary ? '' : c.name || '', spec: c.spec || '', subitem_id: subitem?.id || '', subitem_name: subitem?.name || '', mode: c.mode || '平方', ...(hasCustomerPrice(c.price_cents) ? { price_cents: Number(c.price_cents) } : temporary && can('customer_prices') ? { price_cents: 0 } : {}), wage_cents: effectiveCatalogWage(c, subitem), included_by_craft_id: '', use_included_price: false, temporary, editable: temporary ? can('orders_temporary') : c.editable === true }, nextLineSortOrder());
    }
    function addFixed() {
        const o = draft.value;
        o.lines = o.lines.filter((l: OrderLine) => l.temporary || !!l.id || references.value.crafts.some((c: Craft) => c.id === l.craft_id && c.company_id === o.company_id));
        for (const c of references.value.crafts.filter((c: Craft) => c.company_id === o.company_id)) {
            for (const line of o.lines.filter(l => !l.id && !l.temporary && l.craft_id === c.id)) {
                line.included_by_craft_id = '';
                line.use_included_price = false;
            }
            const subitems = c.subitems?.length ? c.subitems : [{id:'',name:''}];
            for (const subitem of subitems) if (!o.lines.some(l => l.craft_id === c.id && (l.subitem_id || '') === subitem.id)) o.lines.push(lineFrom(c, false, subitem));
        }
    }
    async function companyChanged() {
        const o = draft.value, current = selectionCurrent();
        companyPackages.value = [];
        o.lines = [];
        o.materials = [];
        o.sales = [];
        o.delivery_wage_cents = 0;
        o.worker_id = '';
        o.worker_name = '';
        o.category_workers = {};
        references.value.crafts = [];
        references.value.workers = [];
        references.value.warehouses = [];
        references.value.products = [];
        inventory.value = [];
        selectedOrderCategory.value = '';
        const companyOption = await referenceById('companies', o.company_id);
        if (!current()) return;
        keepReference('companies', companyOption);
        await Promise.all([loadCompanyCrafts(o.company_id, o.project_id, current), loadCompanyPackages(o.company_id, current)]);
        if (!current()) return;
        selectedOrderCategory.value = companyCraftCategories.value[0] || '';
        addFixed();
    }
    async function loadCompanyPackages(companyId: string, current: () => boolean) {
        companyPackages.value = [];
        if (!companyId || !can('customer_prices')) return;
        const result = await api<{ rows: CompanyPackageOption[] }>('/company-packages?' + new URLSearchParams({ company_id: companyId }));
        if (current()) companyPackages.value = result.rows || [];
    }
    async function loadProjectOptions(query = '', nextPage = 1) {
        const request = ++projectRequest, o = draft.value, companyId = o.company_id;
        if (!companyId || o.id) {
            projectOptions.value = [];
            projectTotal.value = 0;
            projectPage.value = 1;
            projectQuery.value = '';
            return;
        }
        const r: ProjectList = await api('/projects?' + new URLSearchParams({ company_id: companyId, q: query, status: '进行中', page: String(nextPage) }));
        if (request !== projectRequest || draft.value !== o || o.company_id !== companyId) return;
        projectOptions.value = r.rows;
        projectTotal.value = r.total;
        projectQuery.value = query;
        projectPage.value = nextPage;
    }
    async function onOrderCompanyChanged() {
        if (draft.value.id) return;
        selectionGeneration++;
        draft.value.project_id = '';
        draft.value.package_id = '';
        draft.value.project_name = '';
        draft.value.project_number = '';
        draft.value.address = '';
        companyPackages.value = [];
        resetTicketSelection(draft.value);
        projectOptions.value = [];
        projectTotal.value = 0;
        projectPage.value = 1;
        projectQuery.value = '';
        await Promise.all([loadProjectOptions(), companyChanged()]);
    }
    async function projectChanged() {
        await perform(async () => {
            if (draft.value.id) return;
            selectionGeneration++;
            const o = draft.value, current = selectionCurrent();
            const p = projectOptions.value.find(x => x.id === draft.value.project_id);
            o.package_id = '';
            o.project_name = p?.name || '';
            o.project_number = p?.number || '';
            o.address = p?.address || '';
            resetTicketSelection(o);
            o.lines = [];
            o.category_workers = {};
            selectedOrderCategory.value = '';
            await loadCompanyCrafts(o.company_id, o.project_id, current);
            if (!current()) return;
            selectedOrderCategory.value = companyCraftCategories.value[0] || '';
            await workerChanged();
        });
    }
    async function continueProject(p: ProjectRecord) {
        const revision = apiSession.capture();
        if (p.status !== '进行中') {
            error.value = '项目已停用或结项，请先重新启用后继续开单';
            return;
        }
        await openOrder();
        if (!apiSession.current(revision) || dialog.value !== 'order')
            return;
        await perform(async () => {
            selectionGeneration++;
            draft.value.company_id = p.company_id;
            draft.value.project_id = p.id;
            draft.value.package_id = '';
            draft.value.project_name = p.name;
            draft.value.project_number = p.number;
            draft.value.address = p.address;
            resetTicketSelection(draft.value);
            await Promise.all([loadProjectOptions(), companyChanged()]);
        });
    }
    async function orderCategoryChanged(category: string) {
        selectedOrderCategory.value = category;
    }
    async function openOrder(row?: {
        id: string;
    }) {
        await perform(async () => {
            const opening = ++selectionGeneration;
            projectOptions.value = [];
            projectTotal.value = 0;
            projectPage.value = 1;
            projectQuery.value = '';
            inventory.value = [];
            references.value.crafts = [];
            references.value.workers = [];
            references.value.warehouses = [];
            references.value.products = [];
            if (row) {
                const saved = await api<OrderDraft & SavedOrderRecord>('/order?id=' + row.id);
                if (opening !== selectionGeneration) return;
                const { combination_details: _readOnlyCombinations, ...editableOrder } = saved;
                draft.value = {...editableOrder, saved_snapshot: clone(saved)};
                draft.value.category_workers = { ...(saved.category_workers || {}) };
                for (const line of draft.value.lines) {
                    const workerId = line.worker_id || saved.worker_id || '';
                    if (workerId && !draft.value.category_workers[line.category]) draft.value.category_workers[line.category] = workerId;
                }
                draft.value.lines = draft.value.lines.map((l: OrderLine) => {
                    const workerId = l.worker_id || saved.worker_id || '';
                    // The API stores actual people, not inheritance intent. Pin saved people
                    // until the user explicitly chooses inheritance; equal IDs prove nothing.
                    const restored: OrderLine = { ...l, client_key: '', subitem_name: l.subitem_name ?? '', actual_location: l.actual_location ?? '', region: l.region ?? '', worker_id: workerId, worker_name: l.worker_name || (!l.worker_id ? saved.worker_name || '' : ''), worker_override: !!workerId, temporary: !!Number(l.temporary), entered: true, segments: (l.segments || []).map((s: OrderSegment) => ({ ...s, mode: s.mode || l.mode || '平方', ...(hasCustomerPrice(s.amount_cents) ? { amount_cents: Number(s.amount_cents) } : {}) })) };
                    orderLineKey(restored);
                    return restored;
                });
                draft.value.materials = (draft.value.materials || []).map((m: Material) => ({ ...m, qty_micro: Number(m.qty_micro) }));
                draft.value.sales = (draft.value.sales || []).map(s => ({...s, qty_micro: Number(s.qty_micro)}));
            }
            else
                draft.value = { id: '', version: 0, number: '', company_id: company.value, project_id: '', package_id: '', work_date: new Date().toLocaleDateString('sv-SE'), address: '', contact: '', worker_id: '', note: '', status: '进行中', wage_paid: 0, lines: [], category_workers: {}, materials: [], sales: [], delivery_wage_cents: 0, ticket_action: 'normal', ticket_id: '', ticket_version: 0, sales_ticket_slot: 1, combinations: [], combination_version: 0, combination_dirty: false, combination_enabled: false, combination_error: '' };
            const current = selectionCurrent();
            const currentCompany = draft.value.company_id, currentWorker = draft.value.worker_id;
            if (can('orders') && currentCompany)
                await loadCompanyCrafts(currentCompany, draft.value.project_id, current);
            if (can('orders') && currentCompany)
                await loadCompanyPackages(currentCompany, current);
            if (!current()) return;
            const companyOption = await referenceById('companies', currentCompany);
            if (!current()) return;
            keepReference('companies', companyOption);
            const workers = await Promise.all(orderWorkerIds(draft.value).map(id => referenceById('workers', id)));
            if (!current()) return;
            for (const worker of workers) keepReference('workers', worker);
            if (can('orders') && currentWorker) {
                const result = await api<PageResult<CatalogRow>>('/reference-options?' + new URLSearchParams({ entity: 'warehouses', worker_id: currentWorker, active: '1' }));
                if (!current()) return;
                references.value.warehouses = result.rows;
            }
            if (can('orders'))
                for (const productId of [...new Set<string>([...(draft.value.materials || []),...(draft.value.sales || [])].map((m: Material) => m.product_id).filter(Boolean))]) {
                    const productOption = await referenceById('products', productId);
                    if (!current()) return;
                    keepReference('products', productOption);
                }
            await loadProjectOptions();
            if (!current()) return;
            selectedOrderCategory.value = companyCraftCategories.value.find((c: string) => c === (draft.value.lines || [])[0]?.category) || companyCraftCategories.value[0] || '';
            addFixed();
            await refreshOrderInventory();
            if (!current()) return;
            dialog.value = 'order';
        });
    }
    const orderLocked = computed(() => !can('orders') || draft.value.status !== '进行中' || draft.value.editable === false || !!Number(draft.value.wage_paid));
    const orderWarehouse = computed(() => references.value.warehouses.find((w: CatalogRow) => w.worker_id === draft.value.worker_id && Number(w.active))?.id || '');
    async function workerChanged(item: CatalogRow | null = null) {
        const o = draft.value, workerId = o.worker_id, current = selectionCurrent(), request = ++workerRequest;
        const currentWorker = () => current() && o.worker_id === workerId && request === workerRequest;
        keepReference('workers', item);
        o.worker_name = item?.id === workerId ? item.name : references.value.workers.find(w => w.id === workerId)?.name || '';
        references.value.warehouses = [];
        if (workerId) {
            const result = await api<PageResult<CatalogRow>>('/reference-options?' + new URLSearchParams({ entity: 'warehouses', worker_id: workerId, active: '1' }));
            if (!currentWorker()) return;
            references.value.warehouses = result.rows;
        }
        addFixed();
        for (const m of draft.value.materials)
            if (!can('warehouse') || !m.warehouse_id) m.warehouse_id = orderWarehouse.value;
        for (const sale of draft.value.sales || [])
            if (!can('warehouse') || !sale.warehouse_id) sale.warehouse_id = orderWarehouse.value;
        await refreshOrderInventory();
    }
    function lineWorkerChanged(line: OrderLine, item: CatalogRow | null) {
        // Account/settlement rights protect personnel, but a skill mismatch must be repairable.
        if (orderLocked.value || !draft.value.lines.includes(line) || !lineAccountCanEdit(line, draft.value.company_id, references.value.crafts, can('orders_temporary'))) return;
        assignLineWorker(line, item, draft.value.category_workers?.[line.category] || '');
        keepReference('workers', item);
    }
    function locationCanChange(line: OrderLine, adding = false) {
        if (orderLocked.value || !draft.value.lines.includes(line) || (adding && line.temporary && !can('customer_prices'))) return false;
        const candidate = adding ? { ...line, id: '' } : line;
        const workerId = lineWorkerId(line, draft.value.category_workers?.[line.category] || '');
        return lineAccountCanEdit(candidate, draft.value.company_id, references.value.crafts, can('orders_temporary')) &&
            (!workerId || lineCanEdit(candidate, draft.value.company_id, references.value.crafts, references.value.workers.find(worker => worker.id === workerId), can('orders_temporary')));
    }
    function addLocation(line: OrderLine) {
        if (!canAddOrderLocation(line) || !locationCanChange(line, true)) return;
        draft.value.lines.push(createOrderLocation(line, nextLineSortOrder()));
        return draft.value.lines.at(-1);
    }
    function removeLocation(line: OrderLine) {
        const index = draft.value.lines.indexOf(line);
        if (index < 0 || !locationCanChange(line) || !canRemoveOrderLocation(draft.value.lines, line)) return false;
        if (draft.value.materials.some(material => material.line_id === combinationLineRef(line))) {
            error.value = '请先移除本项耗材后再移除此明细';
            return false;
        }
        draft.value.lines.splice(index, 1);
        return true;
    }
    function categoryWorkerChanged(_category: string, item: CatalogRow | null) {
        keepReference('workers', item);
    }
    function materialProductSelected(item: CatalogRow | null) {
        keepReference('products', item);
    }
    function addMaterial() {
        if (!orderWarehouse.value && !can('warehouse')) {
            error.value = '请先为材料 / 送货责任人关联材料结余位置';
            return;
        }
        ;
        draft.value.materials.push({ product_id: '', warehouse_id: orderWarehouse.value, line_id: '', qty_micro: 0, qty_quantum: '', input_qty: '', input_unit: 'base' });
    }
    function addTemporary() {
        if (!can('customer_prices')) { error.value = '新增临时收费项需要查看客户价格权限；已有固定工艺仍可填写'; return; }
        if (!can('orders_temporary') || !allowedCrafts.value.length)
            return;
        draft.value.lines.push(lineFrom({ category: allowedCrafts.value[0], mode: '平方' }, true));
    }
    function addSale() {
        if (!can('customer_prices')) { error.value = '新增销售材料需要查看客户价格权限'; return; }
        if (!orderWarehouse.value && !can('warehouse')) { error.value = '请先选择已关联材料结余位置的施工/送货人'; return; }
        (draft.value.sales ||= []).push({id:'',product_id:'',warehouse_id:orderWarehouse.value,qty_micro:0,qty_quantum:'',input_qty:'',input_unit:'base',price_cents:0});
    }
    async function saveOrder() {
        await perform(async () => {
            const revision = apiSession.capture();
            const o = clone(draft.value);
            delete o.saved_snapshot; // The read-only API snapshot never belongs in a write request.
            delete o.wage_adjustments;
            if (!o.company_id) throw new Error('请选择公司');
            if (o.package_id && (!can('customer_prices') || o.project_id || !companyPackages.value.some(item => item.id === o.package_id))) throw new Error('所选套餐已失效或当前账号无客户价格权限，请重新选择');
            if (!o.id) o.address = o.address.trim();
            if (!o.address.trim()) throw new Error('请填写施工地址；后续施工请查找并选择原地址');
            if (Array.from(o.address).length > 300) throw new Error('施工地址最多填写300字');
            const categoryWorker = (l: OrderLine) => o.category_workers?.[l.category] || '';
            const editable = (l: OrderLine) => lineCanEdit(l, o.company_id, references.value.crafts, references.value.workers.find(w => w.id === lineWorkerId(l, categoryWorker(l))), can('orders_temporary'));
            if (o.lines.some(l => filled(l) && l.editable !== false && !lineWorkerId(l, categoryWorker(l)))) throw new Error('请为已填写的小项选择施工人，或选择默认施工人');
            if (o.lines.some(l => filled(l) && !l.id && !editable(l))) throw new Error('存在无权填写或施工人未获技能授权的新明细，请先核对施工人');
            if (o.lines.some(l => !!l.id && l.editable !== false && filled(l) && !editable(l))) throw new Error('已保存工艺与施工人当前技能不匹配，请核对施工人技能后再保存，原明细不会自动删除');
            if (o.lines.some(l => editable(l) && filled(l) && Array.from(l.actual_location || '').length > 200)) throw new Error('实际施工部位最多填写200字');
            if (!can('customer_prices') && (o.lines.some(l => l.temporary && !l.id && filled(l)) || (o.sales || []).some(s => !s.id && Number(s.qty_quantum || s.qty_micro) > 0))) throw new Error('新增临时收费项或销售材料需要查看客户价格权限');
            if (o.combination_dirty) {
                if (!can('combo_pricing')) throw new Error('组合免收权限已撤销，不能保存收费变更；请重新打开核对');
                if (o.combination_error) throw new Error(o.combination_error);
                o.combinations = serializeCombinations(o.combinations || []);
            } else delete o.combinations; // Ordinary saves preserve server relations, including after permission revocation.
            const originalLines = new Map((draft.value.saved_snapshot?.lines || []).map(line => [line.id, line]));
            const savedLines = o.lines.filter(l => editable(l) && filled(l));
            if (can('wages')) for (const line of savedLines) {
                const original = originalLines.get(line.id);
                const before = original?.wage_override_cents;
                const after = line.wage_override_cents;
                const changed = after !== undefined && (before == null ? '' : String(before)) !== (after === null ? '' : String(after));
                if (changed && !line.wage_adjustment_reason?.trim()) throw new Error('调整本项实际工资时，请填写工资调整原因');
                if (after != null && (!Number.isSafeInteger(Number(after)) || Number(after) < 0 || Number(after) > 100000000)) throw new Error('本项实际工资须为0至1000000元，最多两位小数');
            }
            o.lines = savedLines.map((l: OrderLine, index) => {
                const line: OrderLine = { ...l, client_ref: combinationLineRef(l), client_key: undefined, subitem_name: l.subitem_name ?? '', actual_location: l.actual_location ?? '', worker_override: undefined, worker_id: lineWorkerId(l, categoryWorker(l)), sort_order: l.sort_order ?? index, ...(hasCustomerPrice(l.price_cents) ? { price_cents: Number(l.price_cents) } : {}), wage_cents: Number(l.wage_cents || 0), segments: l.mode === '金额' ? [] : l.segments.filter(segmentFilled).map((s: OrderSegment) => ({ ...s, mode: s.mode || l.mode, count: Number(s.count || 0), length: Number(s.length || 0), width: Number(s.width || 0), height: Number(s.height || 0), ...(hasCustomerPrice(s.amount_cents) ? { amount_cents: Number(s.amount_cents) } : {}) })) };
                delete line.wage_adjustments;
                if (!can('customer_prices')) delete line.charge_mode;
                if (!can('wages')) {
                    delete line.wage_override_cents;
                    delete line.wage_adjustment_reason;
                    delete line.wage_cents;
                    delete line.labor_cents;
                }
                return line;
            });
            const preciseQuantity = (m: Material) => {
                if (!m.input_qty) return Number(m.qty_micro) > 0;
                const product = references.value.products.find(p => p.id === m.product_id);
                // Existing sales can outlive a product reference that has since been
                // retired or removed from the selectable catalogue. Preserve their
                // already stored exact quantity while editing ordinary order fields;
                // new rows (and rows with no saved id) must still choose a product so
                // the server can validate the unit conversion.
                if (!product && (m as Sale).id && (Number(m.qty_quantum || m.qty_micro) > 0)) return true;
                if (!product) throw new Error('请重新选择材料商品并核对单位');
                const quantum = toQuantum(m.input_qty, m.input_unit || 'base', { unit: product.unit || '', aux_unit: product.aux_unit, aux_ratio: product.aux_ratio });
                if (!quantum || BigInt(quantum) <= 0n) throw new Error('材料数量须为大于零、最多六位小数且能精确换算的数值');
                m.qty_quantum = quantum;
                return true;
            };
            o.materials = o.materials.filter(preciseQuantity);
            const actualRefs = new Set(o.lines.map(line => line.client_ref));
            const protectedRefs = new Set(draft.value.lines.filter(line => !!line.id && line.editable === false).map(line => line.id));
            if (o.materials.some(material => !!material.line_id && !actualRefs.has(material.line_id) && !protectedRefs.has(material.line_id))) throw new Error('本项耗材必须关联已填写的施工明细；请填写施工数量或移除本项耗材');
            o.sales = (o.sales || []).filter(preciseQuantity).map(s => ({...s, ...(hasCustomerPrice(s.price_cents) ? { price_cents:Number(s.price_cents) } : {})}));
            if (!o.sales.length && can('wages')) o.delivery_wage_cents = 0;
            if (needsMaterialWorker(o) && !o.worker_id) throw new Error('有耗材、售料或带货工资时，请选择材料 / 送货人');
            if (!o.id && !o.lines.length && !o.sales.length) throw new Error('请至少填写一项施工内容或销售材料');
            if (o.ticket_action === 'correct' && !o.ticket_id) throw new Error('请选择需要更正的原票');
            if (o.ticket_action === 'correct' && (!o.ticket_version || o.ticket_version < 1)) throw new Error('请重新选择原票，核对当前版本');
            if (!can('wages')) delete o.delivery_wage_cents;
            delete o.category_workers;
            delete o.combination_dirty;
            delete o.combination_enabled;
            delete o.combination_error;
            await submit('/order', can('customer_prices') ? o : withoutCustomerPrices(o));
            dialog.value = '';
            await load();
            await refreshProjects(!o.id);
            apiSession.assert(revision);
            notice.value = '本次施工/送货已保存，并计入工地票据和库存';
        });
    }
    async function cancelOrder() {
        if (!confirm('取消此施工单并返还材料库存？'))
            return;
        await perform(async () => {
            const { id, version, company_id, project_id, address, work_date, worker_id } = draft.value;
            await submit('/order', { id, version, company_id, project_id, address, work_date, worker_id, delete: true });
            dialog.value = '';
            await load();
            await refreshProjects();
        });
    }
    async function refreshOrderInventory() {
        const current = selectionCurrent();
        const sourceIDs = [...new Set([orderWarehouse.value, ...draft.value.materials.map(m => m.warehouse_id), ...(draft.value.sales || []).map(s => s.warehouse_id)].filter(Boolean))].sort();
        const result = can('orders') || can('warehouse') ? (await Promise.all(sourceIDs.map(balances))).flat() : [];
        const latestIDs = [...new Set([orderWarehouse.value, ...draft.value.materials.map(m => m.warehouse_id), ...(draft.value.sales || []).map(s => s.warehouse_id)].filter(Boolean))].sort();
        if (current() && sourceIDs.join('|') === latestIDs.join('|')) inventory.value = result;
    }
    return { orderDraft: draft, selectedOrderCategory, companyCraftCategories, workerOptions, allowedCrafts, projectOptions, projectPage, projectTotal, projectQuery, companyPackages, loadProjectOptions, onOrderCompanyChanged, projectChanged, continueProject, orderCategoryChanged, openOrder, orderLocked, orderWarehouse, workerChanged, lineWorkerChanged, categoryWorkerChanged, addLocation, removeLocation, materialProductSelected, addMaterial, addSale, addTemporary, saveOrder, cancelOrder, refreshOrderInventory, resetOrders: () => {
            selectionGeneration++;
            projectRequest++;
            workerRequest++;
            draft.value.lines = [];
            draft.value.materials = [];
            draft.value.sales = [];
            projectOptions.value = [];
            companyPackages.value = [];
            projectTotal.value = 0;
            projectPage.value = 1;
            projectQuery.value = '';
            selectedOrderCategory.value = '';
        } };
}
